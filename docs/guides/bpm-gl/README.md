# BPM-GL: layer cho aw (Spring Boot + Oracle + engine BPM, Angular 12 + Nx)

Nguồn: báo cáo khảo sát ngày 2026-10-09 trên ba repository `ms001-bpmacct-contract`, `ms004-bpmacct-commons`,
`mfe-bpmacct-contract`. Thư mục này chứa **ba layer riêng của project**, chưa gắn vào `aw-project.json` (xem "Việc còn lại").

| Layer | File | Áp cho | Nội dung |
|---|---|---|---|
| `layer-bpmgl-spring-oracle` | `definitions/layers/layer-bpmgl-spring-oracle.json` | repo backend (ms001, ms004) | cấu trúc package, API, dữ liệu Oracle/JPA, bí mật, test |
| `layer-bpmgl-bpm-engine` | `definitions/layers/layer-bpmgl-bpm-engine.json` | repo backend dùng engine BPM | **bản sơ bộ**: các ràng buộc đã kiểm chứng; sẽ mở rộng sau khi khảo sát riêng về BPM |
| `layer-bpmgl-angular12` | `definitions/layers/layer-bpmgl-angular12.json` | repo frontend (mfe) | Angular 12 + Nx, gọi API, UI, test, công cụ và lệnh |

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
3. **Test nền đang đỏ ở ms001** (2026-10-09): test chưa commit không biên dịch được (`ContractAppendixServiceGenerateTest`) và 70/98 test lỗi Mockito
   (`FacadeTaskService`). Một cổng "mọi test pass" sẽ đỏ ngay từ đầu. Phải sửa nền hoặc dùng cổng chỉ chạy lớp test do chính task thêm.
4. **Rủi ro bảo mật:** `application-local.properties` được commit chứa thông tin xác thực. Cần xoay vòng khóa; layer đã cấm agent đọc và chép file này.
5. **Câu hỏi cho chủ dự án** (ảnh hưởng nội dung layer): service dùng class cụ thể hay interface+Impl (layer đang theo code: class cụ thể); Node chuẩn 14 hay 16
   (layer đang theo 16.20.2 đã chạy thật); `BpmCsVendorBankAccController` thiếu `@PreAuthorize` có chủ ý không; quy ước tên field pha tiếng Việt; chuẩn commit message.
6. **Khảo sát BPM** để mở rộng `layer-bpmgl-bpm-engine` (lớp pipeline, user task, service task, cách thêm một quy trình mới, tài liệu `docs/BPM-*.md`, thư mục `bpmn/`).
