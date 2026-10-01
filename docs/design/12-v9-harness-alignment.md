# V9 — Harness alignment sau Alpha

> Trạng thái: **ĐỀ XUẤT — chờ product owner duyệt.** Chưa task nào được bắt đầu. Tài liệu chỉ được merge vào
> `master` sau khi được duyệt; khi đó V9-00 là task đầu tiên.
>
> Entry: verdict Alpha `ALPHA_READY` (`f6fd6f6`).
>
> Exit: G1–G10 trong [đối chiếu 14 lecture](../harness-engineering/15-doi-chieu-v9.md) đóng bằng test và chạy
> thật; gate `v8-alpha-gate` vẫn enforcing và xanh; không mở phạm vi Beta.

> Phạm vi task mặc định: kế thừa mục 3 của `00-roadmap.md`.

## Bối cảnh

Alpha đạt 208/208 tiêu chí `ALPHA_MUST` theo gate. Khi dùng `aw` cho một project thật (todolist Spring Boot +
SQLite + React, chạy với Maven/npm thật), một số tiêu chí **đã có owner nhưng chạy thật vẫn vi phạm**. Chỗ dễ thấy
nhất: không đặt được checker sau maker trong cùng run, và kiểm tra máy fail thì cả run fail mà không có đường quay
lại maker. Đọc lại đủ 14 lecture (nguồn tại `38ddcd2`) không thấy tiêu chí mới so với bộ `HE-*` hiện có. Vì vậy V9
không thêm tiêu chí; nó **đóng khoảng cách giữa tiêu chí và hành vi thật**. Chi tiết, bằng chứng và danh sách
những gì cố ý không làm nằm trong [15-doi-chieu-v9.md](../harness-engineering/15-doi-chieu-v9.md).

## Nguyên tắc của V9

- **ADR trước code.** Thay đổi chạm quyết định đã khóa (read-only của checker, ADR-026 về rẽ nhánh, schema
  instruction, vòng đời WorkItem) phải có ADR được chấp nhận ở V9-00 trước khi task tương ứng bắt đầu.
- **Tương thích ngược theo mặc định.** Definition đã publish và run đang chạy giữ nguyên hành vi. Hành vi mới chỉ
  bật khi definition khai báo trường mới hoặc dùng schema version mới.
- **Mỗi khoảng cách có test hồi quy riêng.** Test tái hiện đúng failure mode G1–G10 đã gặp, không chỉ test đơn vị
  của thành phần mới.
- **Gate V8 không lùi.** `v8-alpha-gate` giữ `--enforce=true`; mọi task V9 phải giữ nó xanh.

## Thứ tự và ưu tiên

| Ưu tiên | Task | Đóng khoảng cách |
|---|---|---|
| Nền | V9-00 | ADR cho mọi thay đổi semantics |
| P1 | V9-01, V9-02, V9-03, V9-04, V9-05 | G1, G2, G3, G4, G5 — chặn trực tiếp maker/checker, vòng sửa và chất lượng context |
| P2 | V9-06, V9-07, V9-08, V9-09 | G6, G7, G8 và lỗi vận hành |
| P3 | V9-10 | G9, G10 — vệ sinh tri thức ở mức cảnh báo và audit |
| Kết | V9-11, V9-12 | Kiểm với Claude CLI thật; verdict |

Mặc định làm tuần tự theo Task ID, vì V9-01…V9-08 cùng sửa package `internal/app/runtime`.

> **Có thể song song:** V9-09 chỉ sửa ReleaseSet, definition catalog và định tuyến CLI, không chung contract hay
> schema với V9-01…V9-08, nên được chạy song song với chúng sau V9-00.

## V9-00 — ADR cho các thay đổi semantics của V9

- **Mục tiêu:** chốt bằng ADR mọi thay đổi chạm quyết định đã khóa, trước khi viết code.
- **Phụ thuộc:** product owner duyệt tài liệu này.
- **Thực hiện:** ghi bốn ADR mới trong `docs/architecture/02-architecture-decisions.md`, mỗi ADR nêu bối cảnh, quyết
  định, tương thích ngược và migration nếu có:
  - ADR-030 — read-only của CHECKER/MACHINE_GATE được đo so với trạng thái worktree **lúc attempt bắt đầu**, không so
    với revision đã commit (cho V9-01);
  - ADR-031 — kết quả fail chức năng của COMMAND/MACHINE_GATE có thể map thành outcome khai báo; lỗi kỹ thuật vẫn
    đi retry/FAILED (cho V9-02; bổ sung, không thay ADR-026);
  - ADR-032 — instruction artifact schema v2: thứ tự, priority, outcome hợp lệ (cho V9-03);
  - ADR-033 — chạy lại một WorkItem sau run terminal không thành công (cho V9-06).
  Cập nhật danh sách ADR baseline trong `docs/00-start-here.md`.
- **Verify:** `go test ./internal/docscoverage/ ./internal/alphagate/` pass; bốn heading ADR mới được parser ADR
  nhận diện.
- **Hoàn thành khi:** product owner chấp nhận bốn ADR; mỗi task V9 sau trỏ được tới ADR của mình.
- **Nguồn:** ADR-026, ROADMAP-§1, ROADMAP-§9.

## V9-01 — Checker và gate đánh giá được thay đổi của maker trong cùng run

- **Mục tiêu:** workflow `maker → checker` và `maker → machine gate` chạy được trong một run. "Read-only" nghĩa là
  không thay đổi gì so với lúc chính attempt đó bắt đầu.
- **Phụ thuộc:** V9-00 (ADR-030).
- **Thực hiện:**
  - Trước mỗi attempt read-only, chụp trạng thái worktree (tree của index + working tree, gồm file untracked không
    bị ignore) thành `InputTree` và ghi vào RevisionSet đầu vào của attempt.
  - `validateStrictlyReadOnlyDiffs` đo diff so với `InputTree` thay vì `mount.VCSObjectID`.
  - Diff manifest của maker (artifact đã có) được đưa vào evidence ref của checker, để checker nhận đúng
    "requirement, diff và evidence" như V5-12 dự định.
  - Khi worktree không có thay đổi chưa commit, hành vi giữ y như cũ.
- **Verify:**
  - Integration test: MAKER sửa file → CHECKER không sửa → PASS.
  - CHECKER sửa một file → `SCOPE_VIOLATION`.
  - MAKER → MACHINE_GATE có criteria trên file vừa sửa → verdict đúng với nội dung mới.
  - Crash giữa maker và checker: attempt mới vẫn dùng đúng `InputTree`.
  - Chạy trên Windows và Linux.
- **Hoàn thành khi:** G1 không còn tái hiện với workflow `implement → review (CHECKER) → end`; evidence của checker
  trỏ tới diff manifest của maker.
- **Nguồn:** HE-09-M04, HE-05-M06, HE-14-M08, HE-13-M02.

## V9-02 — Outcome cho kết quả kiểm tra máy, evidence khi fail

- **Mục tiêu:** vẽ được cạnh "kiểm tra fail → quay lại maker" có giới hạn; mọi lần fail đều để lại evidence đọc được.
- **Phụ thuộc:** V9-01 (cùng sửa gate executor), V9-00 (ADR-031).
- **Thực hiện:**
  - COMMAND **luôn** persist evidence `COMMAND_EXECUTION`, kể cả khi exit ≠ 0: argv, cwd/target, exit code, thời
    gian, revision, stdout/stderr đã redact và cắt theo `maxOutputBytes` (có cờ "đã cắt").
  - Node COMMAND/MACHINE_GATE được khai `failureOutcome` (ví dụ `failed`). Exit ≠ 0 hoặc verdict FAIL → NodeRun
    `SUCCEEDED` với outcome đó.
  - Timeout, lỗi spawn, output bị cắt, vi phạm scope vẫn là lỗi kỹ thuật: đi retry theo ATTEMPT policy rồi `FAILED`
    như hiện nay.
  - Validation khi publish: edge từ `failureOutcome` quay về node trước phải nằm trong vòng có `cyclePolicy`.
  - Ở lần chạy lại, maker nhận evidence ref của lần fail, kèm phần tóm tắt WHAT/WHY/FIX: cuối stderr, hoặc criteria
    fail của gate.
  - Node không khai `failureOutcome` giữ hành vi cũ.
- **Verify:**
  - Workflow `build → gate1 (failed) → build → gate1 (passed) → end` với `maxIterations: 2`; vượt vòng thì đi
    `escalationOutcome`.
  - Evidence của lần fail có exit code và output.
  - Golden workload V8-01 không đổi.
- **Hoàn thành khi:** G2 đóng; người vận hành không còn phải tự chạy lại test trong worktree mới biết lỗi gì.
- **Nguồn:** HE-09-M07, HE-09-M03, HE-09-M08, HE-10-M06, HE-14-M03, HE-01-M05.

## V9-03 — Instruction artifact v2: ưu tiên, outcome hợp lệ, thứ tự

- **Mục tiêu:** agent nhìn thấy đâu là luật bắt buộc và được chọn outcome nào; thứ quan trọng nằm ở đầu và cuối
  prompt.
- **Phụ thuộc:** V9-00 (ADR-032).
- **Thực hiện:**
  - `schemaVersion: 2` với thứ tự cố định:
    1. `hardConstraints`: các resource HARD_CONSTRAINT;
    2. `taskContract`: thêm `riskLevel`, `allowedOutcomes` kèm mô tả, và giao thức marker khi node có hơn một
       outcome;
    3. `resources`: các resource còn lại, mỗi resource có `priority`;
    4. `messages`: theo giới hạn của V9-07;
    5. `closingChecklist`: lặp lại key của các HARD_CONSTRAINT và danh sách outcome hợp lệ.
  - Hash của artifact vẫn xác định; provider adapter không đổi.
  - Snapshot v1 cũ vẫn đọc và hiển thị được.
- **Verify:**
  - Golden test cho instruction artifact v2; hai lần lắp cùng snapshot cho cùng hash.
  - Agent giả lập chỉ chọn outcome trong `allowedOutcomes`; outcome ngoài danh sách vẫn bị `OUTCOME_REJECTED`.
- **Hoàn thành khi:** Skill không còn phải tự liệt kê outcome; HARD_CONSTRAINT xuất hiện ở đầu prompt và trong
  checklist cuối.
- **Nguồn:** HE-04-M03, HE-04-M06, HE-14-M02, HE-14-M07.

## V9-04 — Selector theo component, path và block có hiệu lực lúc chạy

- **Mục tiêu:** tri thức của một khu vực code chỉ đến với task chạm khu vực đó, không phải tách agent profile.
- **Phụ thuộc:** V9-00.
- **Thực hiện:**
  - `ResolutionContext.PathTags` lấy từ `pathScopes` của effective scope, đã chuẩn hóa.
  - `ComponentTags` là các Component có path giao với scope; scope không có `pathScopes` nghĩa là mọi component của
    repository.
  - `BlockKind` lấy từ loại/role của node.
  - Ghi các giá trị này vào DecisionArtifact `CONTEXT_RESOLUTION_V1`.
- **Verify:**
  - Resource `pathTags: ["backend"]` được nạp cho task scope `backend` và bị loại với lý do `NOT_APPLICABLE` cho
    task scope `frontend`.
  - Task không có `pathScopes` nhận theo quy tắc toàn repository.
  - Mở rộng test `TestMatchesContext_SelectorMatrix` sang ngữ cảnh thật lấy từ scope.
- **Hoàn thành khi:** G4 đóng; một agent profile dùng chung cho backend và frontend vẫn nhận đúng Layer của từng
  khu vực.
- **Nguồn:** HE-04-M02, HE-03-M04.

## V9-05 — Môi trường của agent được khai báo và ghi vào manifest

- **Mục tiêu:** bỏ được wrapper hard-code `HOME`/`PATH`; môi trường của agent tái lập được và có audit.
- **Phụ thuộc:** V9-00.
- **Thực hiện:**
  - AGENT_PROFILE thêm `envAllowlist` (tên biến, không có giá trị).
  - Tiến trình agent nhận giao của `envAllowlist` trong profile với `--env-allowlist` của worker.
  - Execution manifest ghi tên biến được truyền, không ghi giá trị.
  - `aw doctor` báo lỗi có kiểu khi provider executable không chạy được với môi trường đó.
- **Verify:**
  - Test adapter: thiếu `PATH` thì probe lỗi có kiểu; có `PATH`/`HOME` thì provider giả lập chạy.
  - Quét log/evidence/DB không thấy giá trị biến.
- **Hoàn thành khi:** G5 đóng; tài liệu vận hành bỏ được yêu cầu wrapper bắt buộc.
- **Nguồn:** HE-02-M04, HE-02-M03, HE-06-M04.

## V9-06 — Chạy lại WorkItem sau run không thành công

- **Mục tiêu:** một việc giữ một WorkItem và một lịch sử.
- **Phụ thuộc:** V9-02 (evidence khi fail), V9-00 (ADR-033).
- **Thực hiện:**
  - Cho phép `run start` mới khi run gần nhất kết thúc `FAILED` hoặc `CANCELLED`; bắt buộc `--reason` và expected
    version.
  - Message và evidence cũ được giữ, gắn run id; completion chỉ xét evidence của run hiện tại.
  - Kanban và task detail hiện số lần chạy.
  - Đổi contract vẫn phải tạo WorkItem mới. Có cho chọn workflow version mới hơn hay không do ADR-033 quyết định.
- **Verify:**
  - fail → rerun → `DONE` trên cùng WorkItem; timeline thấy cả hai run.
  - Crash giữa hai run không tạo run trùng.
  - Projection Kanban đúng sau rerun.
- **Hoàn thành khi:** G6 đóng; hướng dẫn "hủy rồi tạo WorkItem mới" không còn cần.
- **Nguồn:** HE-08-M02, HE-08-M05, HE-05-M03, HE-12-M02.

## V9-07 — Ngân sách cho message trong context

- **Mục tiêu:** prompt không lớn dần vô hạn qua các vòng sửa.
- **Phụ thuộc:** V9-03.
- **Thực hiện:**
  - CONTEXT policy thêm `messages: {maxBytes, keepLatest}`.
  - Luôn giữ các message mới nhất và message được ghim; phần còn lại thay bằng tham chiếu (id, tác giả, thời điểm).
  - ContextSnapshot ghi message bị loại và lý do.
  - Policy không khai `messages` giữ hành vi cũ.
- **Verify:** 10 vòng rework, mỗi vòng thêm log lỗi lớn: kích thước prompt có trần, message mới nhất luôn có mặt, audit
  thấy message bị loại.
- **Hoàn thành khi:** G7 đóng.
- **Nguồn:** HE-13-M08, HE-05-M07, HE-04-M06.

## V9-08 — Readiness profile và baseline cho người vận hành

- **Mục tiêu:** khởi tạo là một phase riêng có bằng chứng, người vận hành dùng được.
- **Phụ thuộc:** V9-02 (định dạng evidence khi fail).
- **Thực hiện:**
  - Thêm CLI, HTTP và UI cho readiness profile của repository (lệnh setup/verify) trên năng lực đã có ở
    `internal/app/readinesscheck/`.
  - Baseline chạy khi tạo WorkspaceSet và khi profile đổi.
  - WorkItem con có quyền WRITE chỉ `READY` khi baseline PASS, hoặc người vận hành chấp nhận ngoại lệ có audit.
  - Baseline fail được phân loại là lỗi môi trường, tách khỏi lỗi code.
- **Verify:**
  - Repository có sẵn test đỏ → readiness báo baseline FAIL và không admit writer.
  - Baseline PASS → chạy bình thường.
  - Test đỏ do task gây ra được phân biệt với test đỏ có từ trước.
- **Hoàn thành khi:** G8 đóng; thao tác mới có parity CLI/UI theo ADR-028.
- **Nguồn:** HE-06-M01, HE-06-M02, HE-06-M03, HE-12-M03, ADR-028.

## V9-09 — Sửa các lỗi vận hành đã tái hiện

- **Mục tiêu:** bỏ các bẫy trong vòng làm việc hằng ngày.
- **Phụ thuộc:** V9-00.
- **Thực hiện:**
  - Local commit khi worktree không có thay đổi → lỗi có kiểu `NO_CHANGES` ngay, không để job retry tới `DEAD`.
    ReleaseSet đã seal mà local commit fail phải có đường abandon hoặc thử lại.
  - Tạo definition trùng id → lỗi `CONFLICT` có kiểu; sửa cùng lúc LIM-06 (trùng repository id).
  - Agent sửa ngoài `pathScopes` → `SCOPE_VIOLATION` kèm danh sách path, thay cho `PROVIDER_UNAVAILABLE`.
  - `aw version show|diff` không bị lệnh tiến trình `aw version` che.
- **Verify:** mỗi lỗi có test tái hiện đúng bước đã gặp; parity ledger không đổi.
- **Hoàn thành khi:** bốn lỗi không còn tái hiện; LIM-06 đóng trong báo cáo release.
- **Nguồn:** HE-09-M08, HE-12-M05, HE-10-M06, ADR-028.

## V9-10 — Vệ sinh tri thức: instruction file của repository và resource cũ

- **Mục tiêu:** không còn đường vòng đưa instruction dài vào agent ngoài ngân sách và ngoài audit.
- **Phụ thuộc:** V9-03.
- **Thực hiện:**
  - Mỗi provider adapter khai danh sách file instruction mà CLI tự nạp (ví dụ `CLAUDE.md`, `AGENTS.md`).
  - Lúc lắp context, ghi path, hash và kích thước của các file đó (nếu có trong worktree) vào ContextSnapshot; cảnh
    báo khi vượt ngưỡng cấu hình.
  - Khi publish, cảnh báo (không chặn) khi resource có `lastVerified` quá hạn cấu hình, hoặc khi số HARD_CONSTRAINT
    của một agent vượt ngưỡng cấu hình.
- **Verify:**
  - Snapshot có entry cho `CLAUDE.md` khi file tồn tại; hash snapshot không đổi khi không có file.
  - Publish phát cảnh báo đúng hai trường hợp trên.
- **Hoàn thành khi:** G9 và G10 đóng ở mức cảnh báo và audit.
- **Nguồn:** HE-04-M01, HE-04-M04, HE-04-M06, HE-03-M03.

## V9-11 — Kiểm chứng với Claude CLI thật

- **Mục tiêu:** chứng minh các thay đổi V9 chạy với provider thật; báo cáo release hiện ghi "Live provider
  compatibility: UNVERIFIED".
- **Phụ thuộc:** V9-01…V9-07.
- **Thực hiện:** chạy một workload mẫu với Claude CLI thật, ví dụ golden workload V8-01 rút gọn hoặc quy trình hai
  tầng tính năng → task của todolist. Workload phải đi qua:
  - chọn outcome theo `allowedOutcomes`;
  - checker sau maker;
  - kiểm tra fail → quay lại maker;
  - môi trường agent không cần wrapper.
  Lưu transcript, evidence và run id.
- **Verify:** báo cáo có run id, evidence, và so sánh kết quả giữa agent giả lập với CLI thật.
- **Hoàn thành khi:** mục "Live provider compatibility" của báo cáo release được cập nhật bằng kết quả thật (pass,
  hoặc danh sách lỗi có owner).
- **Nguồn:** HE-01-M06, HE-02-M02.

## V9-12 — Verdict V9 và handoff

- **Mục tiêu:** tổng hợp evidence của G1–G10 và ghi verdict.
- **Phụ thuộc:** V9-01…V9-11.
- **Thực hiện:**
  - Cập nhật `15-doi-chieu-v9.md`: mỗi G có test hoặc evidence chạy thật.
  - Cập nhật `docs/00-start-here.md` và báo cáo release.
  - Ghi verdict `V9_DONE|REWORK|CHƯA ĐỦ EVIDENCE`.
- **Verify:** `go test ./...`, `go vet ./...`, job `v8-alpha-gate` enforcing xanh trên commit được đánh giá.
- **Hoàn thành khi:** verdict được ghi từ chính lần chạy gate của commit đã merge, không suy ra từ unit test.
- **Nguồn:** ROADMAP-§6.

## Gate của V9

Kế thừa gate chung ở mục 6 của `00-roadmap.md`, thêm:

1. Workflow và definition đã publish trước V9 chạy ra cùng kết quả (golden workload V8-01 không đổi).
2. Mỗi G1–G10 có ít nhất một test tái hiện failure mode gốc và pass sau khi sửa.
3. `v8-alpha-gate` enforcing xanh.
4. Mọi ADR-030…033 đã được chấp nhận trước khi task tương ứng merge.
