# Đối chiếu 14 lecture với Agent Kit sau Alpha (đầu vào cho V9)

> Trạng thái: **ĐÃ DUYỆT** (PR #143, 2026-10-01). Tài liệu này không đổi nhãn phase hay owner của bất kỳ
> `HE-NN-Mxx` nào; nó ghi lại những chỗ mà tiêu chí đã có owner nhưng **chạy thật chưa đạt**, làm đầu vào cho
> [V9](../design/12-v9-harness-alignment.md).
>
> Nguồn lecture: [walkinglabs/learn-harness-engineering tại `38ddcd2`](https://github.com/walkinglabs/learn-harness-engineering/tree/38ddcd2bf8d65271f668b94e7c875ca1d629d622),
> đọc lại đủ 14 bài ngày 2026-10-01. So với commit `77e7a3e` mà [bộ tiêu chí hiện có](00-tong-quan.md) dựa vào,
> 13 commit mới chỉ gắn nguồn hoặc đánh dấu "minh họa" cho các con số; **không có tiêu chí mới**. Vì vậy bộ
> `HE-NN-{M,S}NN` hiện có vẫn là danh mục chuẩn, và tài liệu này dùng lại đúng các mã đó.
>
> Code đối chiếu: `master` tại `f6fd6f6` (verdict `ALPHA_READY`).

## 1. Cách làm và nguyên tắc chọn lọc

- Bằng chứng lấy từ **code, test và lần chạy thật**, không lấy từ tài liệu thiết kế. Lần chạy thật dùng binary `aw`
  build từ `7d0fb4c` với Maven/npm thật và agent giả lập nói đúng giao thức stream-json của Claude CLI; các hành vi
  được trích dẫn đã được đối chiếu lại với code ở `f6fd6f6`.
- Theo §15 của [tổng quan](00-tong-quan.md) và yêu cầu của product owner: **chỉ giữ tiêu chí có failure mode thật,
  có acceptance test rõ và đáng chi phí**. Tiêu chí nhỏ, chỉ là ngưỡng minh họa của lecture, hoặc là nội dung của
  repository người dùng (không phải năng lực của platform) bị bỏ qua.
- Mức đánh giá: **Đạt** (code + chạy thật khớp tiêu chí), **Một phần** (có cơ chế nhưng thiếu ở điểm quyết định),
  **Chưa** (chạy thật vi phạm tiêu chí).

## 2. Tiêu chí tóm tắt của 14 lecture

Mỗi lecture rút về vài tiêu chí cốt lõi. Mã `HE-*` trỏ tới tiêu chí tương ứng trong bộ hiện có.

| Lec | Ý chính | Tiêu chí cốt lõi | Mã |
|---|---|---|---|
| 01 | Thất bại thường do harness, không do model | Mỗi task có Definition of Done chạy được; "xong" dựa trên evidence, không dựa trên lời tự khai; mỗi lỗi lưu đủ dữ liệu để chẩn đoán | HE-01-M02, M04, M05 |
| 02 | Harness = instruction, tool, environment, state, feedback | Môi trường chạy tái lập được và có khai báo; quyền theo least privilege; feedback có thẩm quyền | HE-02-M03, M04, M06 |
| 03 | Repository là nguồn sự thật | Tri thức gần code hoặc được định tuyến chính xác tới đúng khu vực; tri thức có nguồn gốc và mốc kiểm tra | HE-03-M04, M06 |
| 04 | Không nhồi mọi thứ vào một file instruction | Chỉ nạp thứ liên quan; phân biệt ràng buộc cứng với gợi ý **và agent nhìn thấy sự phân biệt đó**; điều quan trọng không nằm giữa prompt; phát hiện mâu thuẫn; ghi manifest context thực tế | HE-04-M01…M06 |
| 05 | Liên tục qua nhiều session | Trạng thái và quyết định do platform giữ; bàn giao có cấu trúc; context không phình vô hạn | HE-05-M01…M04, M07 |
| 06 | Khởi tạo là phase riêng | Có lệnh setup/verify chuẩn; chạy baseline trước khi agent được ghi; tách lỗi môi trường khỏi lỗi code | HE-06-M01…M03, M07 |
| 07 | Ranh giới task, WIP=1 | Scope rõ và được kiểm khi kết thúc; một writer mỗi worktree; chia nhỏ có kiểm soát | HE-07-M01, M03, M04, M07 |
| 08 | Danh sách việc là primitive | Mỗi việc có hành vi + cách kiểm + trạng thái; chỉ engine chuyển trạng thái; một nguồn sự thật cho mỗi việc | HE-08-M01…M05 |
| 09 | Không cho agent tự tuyên bố xong | Kết thúc do bên ngoài quyết định; **maker và checker tách biệt**; fail quay lại maker có giới hạn; lỗi trả kèm cách sửa | HE-09-M01…M08 |
| 10 | Chỉ full pipeline mới là kiểm chứng thật | Mức kiểm theo rủi ro; luật kiến trúc thành check chạy được; thông báo lỗi viết cho agent | HE-10-M01, M04, M06 |
| 11 | Observability nằm trong harness | Event chuẩn, trace xuyên WorkItem → Run → Node → Attempt; verdict theo từng tiêu chí | HE-11-M01…M06 |
| 12 | Mỗi session để lại trạng thái sạch | Cổng release kép (task + clean state); cleanup idempotent; so với baseline | HE-12-M01, M03, M05, M07 |
| 13 | Vòng lặp tự động | Goal + verifier độc lập + điều kiện dừng; giới hạn vòng; kiểm soát context qua các vòng | HE-13-M01, M02, M04, M08 |
| 14 | Đồ thị điều phối | Graph bất biến, đã validate; **định tuyến theo kết quả kiểm chứng** (pass → đi tiếp, fail → quay lại); verifier có context riêng; neo vào sự thật bên ngoài | HE-14-M01, M03, M06…M08, M10 |

## 3. Đối chiếu với Agent Kit

### 3.1 Những gì đã đạt (không cần V9)

| Nhóm | Bằng chứng |
|---|---|
| DoD có cấu trúc trước khi chạy (HE-01-M02, HE-08-M01) | `readiness` báo `problems` khi contract thiếu behavior/AC/verificationSpec/riskLevel (chạy thật) |
| Engine quyết định "xong" (HE-01-M04, HE-08-M03, HE-09-M01/M02) | CompletionPolicy V1/V2 (`requiredAssurance` STATIC…HUMAN) quyết định WorkItem `DONE`; agent nói "done" không đủ (chạy thật) |
| Version bất biến, pin theo run (HE-02-M07, HE-14-M01) | Publish lại không đổi run đang chạy; contract pin `workflowVersionId` (chạy thật) |
| Nguồn gốc tri thức bắt buộc (HE-03-M06) | Thiếu `owner`/`source`/`lastVerified|revision` thì publish bị từ chối (`internal/domain/layer/validate.go:149-168`, tương tự skill) |
| Ưu tiên, ngân sách, chống mâu thuẫn cùng khóa (HE-04-M03, M05) | `contextassembler.Resolve`: HARD_CONSTRAINT không bao giờ bị cắt, vượt ngân sách thì fail; trùng key + có HARD_CONSTRAINT thì không lên lịch (test `TestResolve_*` pass) |
| Manifest context và audit lựa chọn (HE-04-M06, HE-05-M04) | ContextSnapshot pin hash từng resource; DecisionArtifact `CONTEXT_RESOLUTION_V1` lưu cả resource bị loại và lý do (`internal/app/runtime/schedule.go:381`, `:852`) |
| Một writer mỗi worktree, scope theo path (HE-07-M03, M04) | Thay đổi ngoài `pathScopes` làm attempt fail (chạy thật); FORK hai nhánh cùng ghi một repository bị `CONFLICT` (chạy thật) |
| Vòng sửa có giới hạn ở cổng người (HE-09-M07 cho APPROVAL, HE-13-M04) | `cyclePolicy {maxIterations, escalationOutcome}` bắt buộc khi publish; vượt vòng thì đi nhánh thoát (chạy thật) |
| Trace và evidence (HE-11-M01…M04) | Timeline NODE_RUN/EXECUTION_ATTEMPT, evidence gắn revision; V8-01 trace completeness |
| Cổng release (HE-12-M01, M07) | ReleaseSet seal + local commit chỉ sau khi WorkItem đạt completion (chạy thật) |
| Graph validate khi publish (HE-14-M06) | Outcome thiếu edge, vòng không có `cyclePolicy`, ROUTER nhiều outcome đều bị từ chối (chạy thật) |

### 3.2 Những chỗ chạy thật chưa đạt

| # | Tiêu chí | Mức | Hiện trạng và bằng chứng | V9 |
|---|---|---|---|---|
| G1 | **Maker/checker tách biệt** (HE-09-M04, HE-05-M06, HE-14-M08, HE-13-M02) | **Chưa** | CHECKER và MACHINE_GATE bắt buộc diff "rỗng tuyệt đối". Diff được đo so với revision **đã commit** của mount (`agent_node_executor_resources.go:253`, `validateStrictlyReadOnlyDiffs` tại `:349`), mà thay đổi của maker chưa commit cho tới bước ReleaseSet. Vì vậy đặt checker/gate sau maker trong cùng run **luôn** bị tính là vi phạm scope (chạy thật). Thiết kế V5-12 muốn checker nhận "requirement/diff/evidence" của maker, nhưng không có đường nào để điều đó xảy ra | V9-01 |
| G2 | **Fail quay lại maker có giới hạn** (HE-09-M07, HE-14-M03) và **lỗi có thể hành động** (HE-09-M03, HE-09-M08, HE-10-M06, HE-01-M05) | **Chưa** | COMMAND thoát mã khác 0 → attempt `FAILED` → run `FAILED`, **không lưu evidence/output** (`command_node_executor.go:259-263`). GATE không PASS có lưu evidence nhưng cũng làm attempt `FAILED` (`gate_node_executor.go:318-353`). Không có outcome nào cho "kiểm tra fail", nên không vẽ được cạnh "test đỏ → quay lại BUILD"; vòng sửa duy nhất là qua người duyệt hoặc tạo WorkItem mới | V9-02 |
| G3 | **Agent thấy được mức ưu tiên và outcome hợp lệ** (HE-04-M03 phía agent, HE-14-M07, HE-14-M02) và **điều quan trọng không nằm giữa prompt** (lec04) | **Một phần** | Prompt chỉ có `{taskContract, messages, resources}`; mỗi resource chỉ có `ownerVersionId/resourceKey/contentHash/content`, **không có priority** (`assemble_execution_request.go:58-81`). HARD_CONSTRAINT đứng sau toàn bộ messages; cuối prompt là REFERENCE. Node nhiều outcome yêu cầu marker nhưng prompt không liệt kê outcome nào hợp lệ — người vận hành phải tự viết vào Skill | V9-03 |
| G4 | **Định tuyến tri thức theo component/path** (HE-04-M02, HE-03-M04) | **Chưa** | Lúc chạy chỉ điền `TaskKind` và `RiskClass` (`schedule.go:357-360`). Resource khai `componentTags`/`pathTags`/`blockKinds` hợp lệ khi publish nhưng **luôn bị loại** vì ngữ cảnh rỗng (selector AND giữa các chiều, `contextassembler.go:60-92`). Muốn mỗi khu vực code nhận tri thức riêng thì phải tách agent profile | V9-04 |
| G5 | **Môi trường agent có khai báo, tái lập được** (HE-02-M04, HE-02-M03, HE-06-M04) | **Chưa** | Tiến trình agent được spawn với **environment rỗng**; `aw worker --env-allowlist` chỉ đi vào config snapshot và COMMAND/GATE (`command_node_executor.go:198`, `gate_node_executor.go:265`), agent executor không dùng `InheritedEnvironment`. Claude CLI cần `HOME`/`PATH`, nên người vận hành phải viết wrapper hard-code đường dẫn — một phần môi trường nằm ngoài mọi version/hash của aw | V9-05 |
| G6 | **Một việc, một lịch sử** (HE-08-M02, HE-08-M05, HE-05-M03) | **Một phần** | Run `FAILED` để WorkItem `ACTIVE` và không cho `run start` lần nữa; phải hủy rồi tạo WorkItem mới với contract gần như y hệt (chạy thật). Lịch sử, message và evidence của cùng một việc bị chia sang nhiều WorkItem; Kanban hiện nhiều thẻ cho một việc | V9-06 |
| G7 | **Context không phình vô hạn qua các vòng** (HE-13-M08, HE-05-M07) | **Một phần** | Mọi node không phải CHECKER nhận **toàn bộ** message của WorkItem (`schedule.go:326-341`); ngân sách CONTEXT chỉ áp cho resource. Mỗi vòng sửa thêm phản hồi/log lỗi, prompt lớn dần không có trần | V9-07 |
| G8 | **Khởi tạo và baseline trước khi agent ghi** (HE-06-M01…M03, HE-12-M03) | **Một phần** | Readiness profile (lệnh setup/verify) và baseline attempt có ở tầng application (`internal/app/readinesscheck/`) nhưng **không có CLI/HTTP/UI** để người vận hành khai báo; khi chạy thật không có gì chặn agent ghi trước baseline | V9-08 |
| G9 | **Instruction file của repository nằm ngoài manifest** (HE-04-M01, HE-04-M06) | **Một phần** | Provider CLI tự nạp file instruction của repo (ví dụ `CLAUDE.md`). File này không đi qua ngân sách, không có trong ContextSnapshot, không bị kiểm kích thước; một `CLAUDE.md` dài đưa lại đúng vấn đề của lec04. Chưa kiểm với Claude CLI thật | V9-10 |
| G10 | **Tri thức hết hạn** (HE-04-M04, lec03 "knowledge decay") | **Một phần** | `lastVerified` bắt buộc khai nhưng chỉ được chép vào bản ghi audit (`schedule.go:959-975`); không có cảnh báo resource đã cũ | V9-10 |

### 3.3 Lỗi vận hành tái hiện được (không phải tiêu chí, nhưng chặn vòng làm việc)

| Lỗi | Ảnh hưởng | V9 |
|---|---|---|
| Local commit trên worktree không có thay đổi: job retry tới `DEAD`, local commit kẹt `REQUESTED`; ReleaseSet đã seal không abandon được | Người vận hành kẹt ở bước bàn giao (HE-12-M05) | V9-09 |
| `definition create` với id đã tồn tại trả `sqlite: unexpected error` (cùng họ với LIM-06) | Không phân biệt được trùng id với lỗi DB | V9-09 |
| Agent sửa ngoài `pathScopes` bị báo `PROVIDER_UNAVAILABLE` thay vì lỗi scope | Chẩn đoán sai tầng (HE-09-M08) | V9-09 |
| `aw version show/diff` bị lệnh tiến trình `aw version` che (`cmd/aw/cli.go:171-175`) | Phải gọi HTTP để so sánh version | V9-09 |

## 4. Không đưa vào V9

| Ý từ lecture | Lý do không làm lúc này |
|---|---|
| Phân loại lỗi theo năm tầng (lec01) | Đã có error code và termination reason có kiểu; gắn nhãn tầng là việc của người chẩn đoán, chi phí làm tự động cao hơn giá trị ở Alpha một người dùng |
| Lịch chạy và trigger theo sự kiện (lec13 Automations) | `aw` CLI đã có idempotency key cho mọi lệnh ghi; cron của hệ điều hành + CLI đủ cho Alpha local. Scheduler riêng là hạ tầng Beta |
| ROUTER nhiều outcome theo shared state (lec14) | ADR-026 giữ ROUTER một outcome. Trường hợp quan trọng nhất của lec14 — "test pass → đi tiếp, fail → quay lại" — được V9-02 giải quyết bằng outcome của COMMAND/GATE |
| Node tự sinh WorkItem (lec08, lec13 self-feeding loop) | Roadmap §9 loại dynamic child-workflow khi chưa có ADR; chia task bằng thao tác của người vận hành vẫn đủ an toàn |
| Công cụ A/B so sánh hai bộ harness (lec01, lec02, lec12) | V8-01 golden workload đã là benchmark cố định; so sánh hai version definition làm được bằng cách chạy lại workload |
| Ngưỡng cứng (≤15 hard constraint, entry 50–200 dòng) | Lecture ghi rõ là mặc định để dạy, không phải ngưỡng đã kiểm chứng; V9-10 chỉ thêm cảnh báo |
| Quality document theo module, OpenTelemetry export | Nội dung của repository người dùng / SHOULD; journal event hiện có đủ cho Alpha |
| Nhiều writer song song trên cùng repository | Roadmap §9 ngoài phạm vi |
