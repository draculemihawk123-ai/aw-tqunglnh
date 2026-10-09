# BPM-GL: layer cho aw (Spring Boot + Oracle + engine BPM, Angular 12 + Nx)

Nguồn: báo cáo khảo sát ngày 2026-10-09 trên ba repository `ms001-bpmacct-contract`, `ms004-bpmacct-commons`,
`mfe-bpmacct-contract`. Thư mục này chứa **ba layer riêng của project**, chưa gắn vào `aw-project.json` (xem "Việc còn lại").

| Layer | File | Áp cho | Nội dung |
|---|---|---|---|
| `layer-bpmgl-spring-oracle` | `definitions/layers/layer-bpmgl-spring-oracle.json` | repo backend (ms001, ms004) | cấu trúc package, API, dữ liệu Oracle/JPA, bí mật, test |
| `layer-bpmgl-bpm-engine` | `definitions/layers/layer-bpmgl-bpm-engine.json` | repo backend dùng engine BPM | 8 mục (v2, sau khảo sát BPM): mô hình hai tầng, tên không được đổi, thêm task, handler, thêm field pipeline, deploy XML (ngoài repo), tích hợp, điều chưa xác minh |
| `layer-bpmgl-angular12` | `definitions/layers/layer-bpmgl-angular12.json` | repo frontend (mfe) | Angular 12 + Nx, gọi API, UI, test, công cụ và lệnh, màn hình cho user task BPM |

## Không dùng layer chung của kit
- `layer-stack-spring-sqlite`: cố định SQLite và Flyway, ngược với project này (Oracle, không có migration).
- `layer-stack-react-vite`, `layer-react-quality`, `layer-frontend-testing`: dành cho React/Vite/Vitest.
- `layer-api-design`: dạy URL danh từ số nhiều và không có động từ trong đường dẫn, trái với kiểu hành động đang dùng
  (`POST /contract/delivery?taskId=`). Chỉ thêm lại sau khi chủ dự án quyết định có muốn chuyển hướng không.
- Có thể thêm: `layer-backend-security` và `layer-sql-quality` (chung, không phụ thuộc DBMS); đọc lại nội dung trước khi gắn.

## Việc còn lại trước khi chạy workflow thật
1. **Gắn layer** vào `aw-project.json` bằng `packs` và `addResources` như `docs/guides/issue-tracker/aw-project.json`. Chưa viết vì cần chốt cấu trúc
   repository (ba repository độc lập, không có thư mục `backend/` và `frontend/`).
2. **Command kiểm tra.** `kit/commands/maven-test.sh` và `npm-test.sh` giả định `backend/pom.xml` và `frontend/package.json`; BPM-GL đặt `pom.xml` ở gốc repo
   backend và là workspace Nx ở gốc repo mfe. Cần hai script riêng của project (`mvn -B -o test -Dtest=...`; `npx nx test|build|lint <project>`).
3. **Test nền ở ms001: đã biết nguyên nhân.** (a) 70/98 test lỗi `Mockito cannot mock this class: FacadeTaskService` vì bytecode của `bpm-engine` bị obfuscate, thiếu
   StackMapTable (JVM ném `VerifyError`); production né bằng `-noverify`. Chạy test đúng bằng
   `mvn -B -o test -DargLine="-XX:+UnlockDiagnosticVMOptions -XX:-BytecodeVerificationRemote"` (đã xác nhận 98/98 pass trên bản sao); sửa lâu dài là đặt `<argLine>` cho
   `maven-surefire-plugin` trong `pom.xml` (chưa làm). (b) `ContractAppendixServiceGenerateTest` (test chưa commit của người khác) thiếu import
   `vn.com.mbv.bpm.acct.contract.service.contract.ContractPaymentTermService` nên `testCompile` hỏng; chừng nào chưa sửa thì `mvn test` và `mvn -DskipTests package` fail trên cây thực.
   Cổng test của aw phải dùng lệnh có `argLine` ở (a) và xử lý (b) (sửa import hoặc chạy trên bản sao).
4. **Rủi ro bảo mật:** `application-local.properties` được commit chứa thông tin xác thực. Cần xoay vòng khóa; layer đã cấm agent đọc và chép file này.
5. **Câu hỏi cho chủ dự án** (ảnh hưởng nội dung layer): service dùng class cụ thể hay interface+Impl (layer đang theo code: class cụ thể); Node chuẩn 14 hay 16
   (layer đang theo 16.20.2 đã chạy thật); `BpmCsVendorBankAccController` thiếu `@PreAuthorize` có chủ ý không; quy ước tên field pha tiếng Việt; chuẩn commit message.
6. **Phát hiện từ khảo sát BPM cần chủ dự án quyết** (layer đã tránh đi qua những chỗ này, nhưng nên sửa):
   - `create_new_provider.bpmn` trong repo dùng `acctData.rmOpn`/`rmControl`, còn `AcctVendorPipeline` là `acctVendorData.rmOpn`/`rmControlOpn`: lệch nhau; bản trong DB chưa biết.
   - UUID `TaskType` viết HOA, `docs/bpmn/contract.bpmn` viết thường; so sánh phân biệt hoa thường và `getStatus()` trên kết quả `null` ném NPE.
   - `ContractProcessController.startProcess` có `@PreAuthorize` bị comment (dòng 41); scope trong code là `gl.contract.start.process`, tài liệu nói `acct.contract.start.process`.
   - `NccControlProcessTask` gọi `enqueueCreateVendor(..., "123123")` với stepId cứng; thân `NccBeforeEnd` bị comment.
   - Không có job nào gọi `OutboxService.findReadyForRetry`: bản ghi outbox lỗi không tự chạy lại.
   - `.bpmn` hợp đồng và `script.sql` nằm ngoài git; chưa có quy trình deploy trong repo; không có script nạp `T_RE_TASK_TYPE` (UUID, `TASK_URL`).
   - Hai việc của team engine: có chung transaction với `completeTask` không, retry, định nghĩa mới có áp cho instance đang chạy không (xem mục "chưa xác minh" trong `layer-bpmgl-bpm-engine`).
