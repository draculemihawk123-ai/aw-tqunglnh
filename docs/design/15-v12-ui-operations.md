# V12 — Vận hành aw trên giao diện: sửa bất cập từ dùng thật

> Trạng thái: **ĐANG TRIỂN KHAI** (bắt đầu 2026-11-03, nhánh `claude/admiring-brown-d1uf4r`). Phần B (run graph) đã được product
> owner chốt 7 quyết định ngày 2026-11-03. Tiến độ: xem khối "Kết quả" dưới từng task.
> Làm tuần tự theo Task ID; mỗi task một commit, merge vào `master` khi product owner duyệt.
>
> Exit: gate V12 ở cuối file.

> Phạm vi task mặc định: kế thừa mục 3 của `00-roadmap.md`. Như V11, **V12 được phép sửa core engine** (`internal/`, `cmd/`,
> `web/`) và `kit/`, vì các bất cập nằm ở cả ba tầng.

## Bối cảnh

Từ 2026-11-01 tới 2026-11-03, aw được dùng thật để phát triển dự án `chochuyennha-platform` (Spring Boot + Next.js, Windows 10,
Claude CLI thật): 12 task (2 tài liệu, 8 sửa lỗi QA, 2 chuẩn bị production) qua `wf-docs-change`, `wf-bugfix`,
`wf-task-delivery`, `wf-task-delivery-plus`. Mục tiêu của product owner: **vận hành aw hoàn toàn trên giao diện web**. Mỗi lần
một thao tác bắt phải rời giao diện (chạy lệnh, sửa file tay, dùng git), hoặc aw hỏng hay bất tiện, người vận hành ghi một mục
`UX-nn` vào danh sách giữ ngoài repo (`D:\aw\aw-update-tasks.md` trên máy product owner). V12 chuyển 45 mục đó thành task.
Mỗi task dưới đây ghi mã UX nguồn để truy ngược.

Các khoản đã mất vì bất cập, trong tổng khoảng 21 USD tiền agent của 3 ngày:
- P2-01: 0,65 USD, run hỏng do vượt scope.
- P2-02: khoảng 1,8 USD cho 3 lần simplify không giảm được gì.
- P1-01: run đầu hỏng do hết hạn mức, phải chạy lại từ đầu.

Ngoài ra, sau UX-22, mọi commit phải làm bằng git rồi chạy `adopt-head.sh`.

## Nguyên tắc của V12

- **Thứ tự: lỗi trước, giao diện sau.** Phần A sửa lỗi làm hỏng run hoặc mất tiền, vì chúng gặp ở gần như mọi task và vì banner
  lý do dừng của phần B chỉ đúng khi lỗi gốc đã được phân loại đúng. Phần B là run graph đã chốt. Phần C bổ sung thao tác còn
  thiếu trên giao diện. Phần D gồm các việc đụng nguyên tắc Alpha hoặc bảo mật, nên mỗi task bắt đầu bằng ADR và quyết định
  của product owner.
- **Tái hiện trước, sửa sau** (như V11) cho mọi task loại lỗi. Task nào chưa xác minh nguyên nhân trong mã thì ghi
  "cần xác minh", và task bắt đầu bằng khảo sát.
- **Fail-closed vẫn là mặc định.** Không sửa bằng cách bỏ một kiểm tra. Quyết định cho qua một kiểm tra là của người, ở một
  cổng, và có evidence.
- **Không tự repin** run đang chạy sang version mới (yêu cầu cốt lõi 16). Mọi thao tác "dùng bản mới" là lựa chọn rõ ràng
  của người vận hành.
- Đổi hành vi công khai (mã lỗi, trạng thái, lệnh CLI, API) phải có ADR mới trong `docs/architecture/02-architecture-decisions.md`
  và cập nhật `docs/operator/`.
- Mỗi task có kiểm chứng chạy thật trên Windows: stack thật, agent giả lập khi được, Claude thật ở các task về provider.
- Tài liệu tiếng Việt, thuật ngữ kỹ thuật giữ tiếng Anh. Giao diện tiếng Anh (product owner chốt 2026-11-03).

## Danh sách task

| Task | Nội dung | UX nguồn | Mức | Tầng |
|---|---|---|---|---|
| **Phần A — Lỗi làm hỏng run hoặc mất tiền** | | | | |
| V12-01 | Job commit cục bộ chết im lặng, bản ghi commit kẹt REQUESTED | UX-22 | Cao | engine |
| V12-02 | Lỗi ở bước sau cổng duyệt làm mất kết quả đã duyệt | UX-23 | Cao | engine, kit |
| V12-03 | Chạy tiếp từ đúng node sau khi hết hạn mức; xử lý thay đổi dở của attempt hỏng | UX-15, UX-17 | Cao | engine, web |
| V12-04 | Sức khỏe worker và provider: probe lỗi không phải drift, worker mồ côi, phiên đăng nhập hết hạn | UX-16, UX-18, UX-07 | Cao | engine, web |
| V12-05 | Claude CLI tự cập nhật: một thao tác "dùng bản CLI mới"; nút Redo bị thiếu sau khi hủy | UX-05, UX-06 | Cao | engine, web |
| V12-06 | Vòng `size → simplify`: tối đa 3 lần rồi sang cổng người; ngưỡng diff theo Component | UX-26 | Cao | kit, engine, web |
| V12-07 | Lỗi của kit: `expect-fail`, `review-task.sh --commit`, `context-check` trên Windows, UTF-8 | UX-17 (phần kit), UX-19, UX-21, UX-11, UX-10 | TB | kit |
| V12-08 | Một lệnh khởi động `serve` cùng `worker`; khóa migration | UX-08 | TB | engine |
| **Phần B — Run graph (đã chốt 2026-11-03)** | | | | |
| V12-09 | Hàm bố cục thuần `layoutRunGraph` và phân loại hình dạng | UX-27 | Cao | web |
| V12-10 | Vẽ sơ đồ mới: hình dạng, trạng thái, huy hiệu, Main/All paths, bàn phím | UX-27 | Cao | web |
| V12-11 | API: tóm tắt kết quả mỗi activation, lý do dừng run có kiểu | UX-27 | Cao | engine, OpenAPI |
| V12-12 | Panel lịch sử bên phải, gộp mọi run của WorkItem | UX-27 | Cao | web |
| V12-13 | Banner, bỏ cột Timeline và mã cũ, Playwright | UX-27 | Cao | web, e2e |
| **Phần C — Thao tác còn thiếu trên giao diện** | | | | |
| V12-14 | Form tạo task con có contract: mẫu theo loại việc, chọn workflow có gợi ý, kiểm tra scope | UX-02, UX-03, UX-25, UX-38, UX-42 | Cao | web, API |
| V12-15 | Sơ đồ workflow trên trang Definitions và trong form tạo task | UX-44 | TB | web |
| V12-16 | Hộp "Needs you" và thông báo | UX-28, UX-29 | Cao | engine, web |
| V12-17 | Trình xem diff theo file, bình luận theo dòng, gửi "revise" | UX-31 | Cao | web, API |
| V12-18 | Message của người vận hành nhắm vào một node hoặc attempt kế tiếp | UX-20 | TB | engine, web |
| V12-19 | Hàng đợi task và phụ thuộc giữa task trong một family | UX-30 | TB | engine, web |
| V12-20 | Đồng bộ nhánh family với nhánh gốc, trạng thái đã merge, dọn nhánh | UX-24, UX-14 | TB | engine, web |
| V12-21 | Publish định nghĩa project trên giao diện | UX-04 | TB | engine, web |
| V12-22 | Tạo các task từ `tasks.json` sau cổng DESIGN | UX-09 | TB | web, API |
| V12-23 | Chi phí và hạn mức: tổng theo task/đợt/project, trần ngân sách, hạn mức provider | UX-35, UX-36 | TB | engine, web |
| V12-24 | Trang trạng thái project | UX-37 | TB | web |
| V12-25 | Commit message theo quy ước của repo | UX-13 | TB | engine, web |
| V12-26 | Xem prompt mà node đã nhận | UX-12 | Thấp | web |
| V12-27 | CLI: cờ đặt sau tham số vị trí bị bỏ qua mà không báo | UX-41 | Thấp | cmd |
| **Phần D — Cần ADR hoặc quyết định của product owner trước** | | | | |
| V12-28 | Merge vào nhánh gốc, push, tạo PR, trạng thái CI từ giao diện | UX-01, UX-39 | Cao | engine, web, ADR |
| V12-29 | Kho secret theo repository | UX-34 | TB | engine, web, ADR |
| V12-30 | Chạy ứng dụng (dev server) của worktree để QA tay | UX-32 | TB | engine, web |
| V12-31 | QA trong aw: WorkItem loại QA; biến thể bugfix cho UI chưa có test | UX-33, UX-43 | TB | kit, engine, web |
| V12-32 | Truy cập từ xa và màn duyệt cho điện thoại | UX-40 | Thấp | ADR bảo mật |
| V12-33 | Nơi lưu tài liệu trung gian của kit (`frame.md`, `plan.md`) | UX-45 | Thấp | kit, quyết định |

Mặc định làm tuần tự theo Task ID. Phần B (V12-09 … V12-13) không phụ thuộc phần A về mã. Nếu product owner muốn thấy giao
diện mới sớm, phần B có thể làm trước phần A; khi đó V12-11 phải thêm mã `stopReason` cho các lỗi phần A sửa sau.

---

## Phần A — Lỗi làm hỏng run hoặc mất tiền

### V12-01 — Job commit cục bộ chết im lặng (UX-22)

- **Bằng chứng (chochuyennha, 2026-11-02):** job `RELEASE_SET_LOCAL_COMMIT` bị worker nhận 5 lần, lần nào cũng `LEASE_EXPIRED`
  rồi `DEAD`. Bản ghi local commit giữ `REQUESTED` mãi, giao diện không hiện lỗi, `commit-task.sh` chờ 5 phút rồi báo timeout.
  Git trong worktree vẫn chạy dưới 0,1 giây. Lặp lại với worker mới và với commit gắn cổng duyệt (`approval resolve --commit-spec`)
  trên cùng workspace.
- **Nguyên nhân: cần xác minh.** Nghi ngờ: (1) các local commit cũ kẹt `REQUESTED` chặn mọi lần commit sau của workspace đó;
  (2) handler trả lỗi nhưng `workerpool/pool.go` (`runJob`) không ghi log lý do.
- **Mục tiêu:** job commit hoặc thành công, hoặc kết thúc `FAILED` kèm lý do có kiểu, hiện trên giao diện và CLI. Có thao tác
  "thử commit lại" có kiểm soát (idempotency key, expected version).
- **Test:** tái hiện treo bằng handler giả lập; job `DEAD` phải đóng bản ghi commit ở `FAILED`; worker ghi log lý do.
- **Kết quả (đã xong, ADR-045):** khảo sát mã xác nhận nghi ngờ (2): `workerpool.runJob` bỏ lỗi handler, job chỉ đi tiếp qua lease
  hết hạn, và không có gì đóng thao tác khi job `DEAD`. Nghi ngờ (1) không đúng: không có kiểm tra nào chặn commit mới vì thao tác cũ
  còn `REQUESTED` (write lease của local commit chỉ giữ tối đa `--local-commit-write-lease-ttl`). Nguyên nhân gốc của lỗi handler ở
  máy Windows chưa tái hiện được ở đây; nay nó hiện ra trong `failureDetail` và log. Đã làm: lý do `EXECUTION_ERROR` + cột
  `failure_detail` (migration 0054); lượt claim cuối đóng `FAILED` cùng transaction hoàn tất job; worker quét job `DEAD` mỗi phút;
  `OnHandlerError` ghi log; `RELEASE_FAILED` mang chi tiết; thử lại bằng yêu cầu mới (marker mới), `commit-task.sh` in lý do và nhận
  `COMMIT_ATTEMPT`. Test: `releasesetcommit/execution_error_test.go` (Git + SQLite thật), `workerpool/pool_handler_error_test.go`.
  Chưa chạy: lần chạy thật trên Windows.

### V12-02 — Lỗi ở bước sau cổng duyệt làm mất kết quả đã duyệt (UX-23)

- **Bằng chứng:** trong `wf-task-delivery-plus`, `sync` chạy sau `gate2`. Sync ghi một file ngoài scope (`SCOPE_VIOLATION`)
  thì cả run `FAILED`, WorkItem `BLOCKED`, dù code đã được duyệt. Chạy lại thì bắt đầu từ `START`. `gate2` của workflow này
  không có `release`, nên không duyệt kèm commit được.
- **Mục tiêu:** lỗi ở node sau một cổng duyệt đã `approved` không xóa kết quả đã duyệt. Có thể chạy lại riêng node hỏng, hoặc
  commit phần đã duyệt rồi đóng run có ghi chú.
- **Thực hiện (đề xuất):** bật `release: {commit: OFFERED}` cho `gate2` của `wf-task-delivery-plus` và `wf-task-delivery`.
  Scope mặc định của `sync` cho phép `docs/`. Chạy lại từ node hỏng dùng chung cơ chế với V12-03.
- **Kết quả (đã xong, kit 1.8.0):** đề xuất "bật `release` cho `gate2`" bị compiler từ chối đúng luật của ADR-040: `gate2 approved`
  dẫn tới `sync`, một node còn ghi worktree, nên commit lúc quyết định sẽ tranh với nó. Thay vào đó đổi thứ tự trong
  `wf-task-delivery` và `wf-task-delivery-plus`: `review`/`quality` → `sync` → `gate2` (có `release: {commit: OFFERED}`) → `END`.
  Lỗi của `sync` nay xảy ra trước khi người duyệt, nên không còn kết quả đã duyệt nào bị mất; người duyệt xem cả tài liệu.
  `run-task.sh` thêm `docs` vào `pathScopes` của scope WRITE cho `wf-task-delivery*` (`NO_SYNC_DOCS=1` để tắt), như
  `split-tasks.sh` đã làm. Skill `flow.sync` v6 bỏ câu "đã được duyệt ở GATE 2". 11 scenario walk của bản plus cập nhật thứ tự;
  `check-kit.sh` 79/79. Phần "chạy lại riêng node hỏng" làm ở V12-03.

### V12-03 — Chạy tiếp từ đúng node sau khi hết hạn mức (UX-15, UX-17)

- **Bằng chứng:** Claude báo "You've hit your 5-hour limit · resets …" giữa node `fix` của task P1-01. Attempt và run đều
  `FAILED`. Chạy lại thì workflow bắt đầu lại từ `repro` trên worktree còn code sửa dở; agent `repro` không được sửa code sản
  phẩm nên lặp mãi. V11-02 đã nhận diện được lỗi hạn mức (`PROVIDER_QUOTA_EXCEEDED`), nhưng run vẫn hỏng.
- **Mục tiêu:**
  - Lỗi hạn mức đưa node sang trạng thái chờ có hạn giờ (giờ reset, nếu provider cho biết). Hết giờ, node tự chạy tiếp từ đúng
    node đó, run không hỏng.
  - Khi chạy lại một run hỏng, người vận hành chọn ngay trên giao diện cách xử lý thay đổi chưa commit của attempt hỏng:
    giữ, cất (park vào ref ẩn) hoặc bỏ. Hiện chỉ có biến `OUT_OF_SCOPE=KEEP|PARK` của `retry-task.sh`.
- **Ràng buộc:** cần xác minh xem rerun luôn chạy từ `START` là do thiết kế (ADR) hay do giới hạn hiện tại (guide V9 đã ghi
  khiếm khuyết này).
- **Kết quả (đã xong, ADR-046):** khảo sát xác nhận rerun luôn từ `START` là thiết kế (ADR-033/041), nên không đổi; thay vào đó lỗi hạn
  mức không còn làm hỏng run. Attempt AGENT `BLOCKED` lý do `PROVIDER_QUOTA_EXCEEDED`, NodeRun `BLOCKED`, blocker cùng tên (migration
  0055), run vẫn `RUNNING`. `ParseQuotaReset` đọc giờ reset từ lời của CLI; job `PROVIDER_QUOTA_RESUME` chạy tiếp **đúng node** qua
  `RetryBlockedActivation` (cùng pin, giữ `Iteration`), tối đa 6 lần mỗi node, mặc định sau 1 giờ khi không đọc được giờ. Nút Retry của
  node bị chặn (validActions) và `aw node-run retry-blocked` dùng cùng đường. Thay đổi dở: hộp thoại Run Again / Redo có thêm **Park all**
  (`PARK_ALL`, cất mọi thay đổi chưa commit vào ref ẩn; "bỏ" phá hủy không làm, cất là cách bỏ khôi phục được), CLI và
  `retry-task.sh OUT_OF_SCOPE=PARK_ALL`. Test: `quota_resume_test.go` (chặn → job → chạy tiếp cùng node, idempotent; parser),
  `agent_node_executor_test.go`, `redo_service_sqlite_test.go` (PARK_ALL), `TaskDetail.redo.test.tsx`. Chưa chạy: Claude thật hết hạn mức.

### V12-04 — Sức khỏe worker và provider (UX-16, UX-18, UX-07)

- **Bằng chứng:**
  - Probe `claude.exe --version` nhận mã Windows `3221225794` (0xC0000142, tiến trình không khởi động được), nhưng aw báo
    `ADAPTER_BUILD_DRIFT` "executable no longer matches the registered build" dù CLI không đổi. Giao diện không có nút retry cho
    node bị chặn kiểu này; `aw node-run retry-blocked` trên CLI thì chạy được ngay.
  - Worker mất console cha (shell nền bị tắt) vẫn nhận việc nhưng không khởi chạy được tiến trình con. P2-02: `gate1` lần 4
    `EXECUTION_FAILED` và không có evidence nào.
  - Phiên OAuth của Claude hết hạn nhưng `aw doctor` vẫn báo `HEALTHY`. Lỗi chỉ lộ ra khi attempt hỏng.
- **Mục tiêu:**
  - Tách "không chạy được probe" khỏi "CLI đã đổi"; lỗi probe tạm thời thì tự thử lại vài lần trước khi chặn node.
  - Worker tự kiểm tra khả năng khởi chạy tiến trình con trước khi nhận việc, và tự rút khỏi hàng đợi nếu không chạy được.
  - Lỗi khởi chạy tiến trình được ghi thành evidence có lý do.
  - `doctor` kiểm tra trạng thái đăng nhập của provider. Khi chưa đăng nhập, attempt chờ ở trạng thái chặn có lý do rõ, không
    làm hỏng run.
  - Giao diện có danh sách worker đang chạy và nút Retry khi server cho phép.
- **Kết quả (phần lớn đã xong, ADR-047):** `VerifyNoDrift` tách `ErrAdapterBuildProbeFailed` và thử probe 3 lần; admission có lý do/blocker
  `ADAPTER_PROBE_FAILED` (retry được, không cần build mới). `ClaimGate` của worker pool: `aw worker` không nhận việc khi không chạy được
  `git --version` (cache 30 giây), log `WORKER_CANNOT_SPAWN`. Lỗi khởi chạy tiến trình (spawn hoặc `0xC0000142`) ghi diagnostic
  `PROCESS_START_FAILED` và hiện ở `failureDetail`. Lời "Invalid API key · Please run /login"/"OAuth token has expired" cho
  `PROVIDER_LOGIN_REQUIRED`: node `BLOCKED`, run sống, `aw doctor` có check `provider_login`. Nút **Retry** trên thẻ blocker ở đầu trang
  task. Migration 0056. **Chưa làm:** danh sách worker đang chạy trên giao diện (cần bảng heartbeat của worker; ghi lại cho task sau).
  Test: `drift_test.go`, `admission_test.go` (probe hỏng → retry), `quota_resume_test.go` (đăng nhập), `command_node_executor_test.go`,
  `pool_handler_error_test.go` (ClaimGate), `provider_login_test.go` (doctor), contract test provider, `TaskDetail.test.tsx`.

### V12-05 — Claude CLI tự cập nhật và nút Redo bị thiếu (UX-05, UX-06)

- **Bằng chứng:** CLI tự lên 2.1.316 thì mọi node AGENT bị chặn `ADAPTER_BUILD_DRIFT`. Muốn dùng bản mới phải publish lại
  định nghĩa, hủy run trên giao diện, rồi `recreate-task.sh` trong terminal. Sau khi hủy, trang task không hiện "Redo with
  Latest Version", dù `aw work-item redo-preview` báo `canRecreate: true`.
- **Nguyên nhân phần nút (đã đọc mã):** `TaskDetail.tsx` chỉ hiện nút khi `runValidActions` có `recreateWorkItem`. Nguồn này
  khác nguồn mà `redo-preview` dùng. Cần xác minh vì sao hai nguồn lệch nhau sau khi hủy một run bị drift.
- **Mục tiêu:** trên node bị chặn vì drift có một thao tác "Use the new CLI and redo": đăng ký build mới, publish workflow
  version mới, recreate, có xác nhận. Nút Redo lấy từ cùng nguồn với `redo-preview`.
- **Ràng buộc:** không tự repin run đang chạy; thao tác là lựa chọn rõ ràng của người vận hành.
- **Kết quả (đã xong, ADR-048):** nguyên nhân hai nguồn lệch: nút chỉ đọc `validActions` của chẩn đoán run đang hiện trên thẻ; sau khi
  hủy thẻ không còn run nên không có chẩn đoán. Nay khi thẻ không có run, nút Run Again / Redo lấy từ `getWorkItemRedoPreview`. Lệnh mới
  `UseNewCli` (`POST /work-items/{id}/use-new-cli`, `aw work-item use-new-cli`, `internal/app/cliupgrade`): đo lại CLI của build đang ghim
  (`adapterbuild.MeasureCurrent`), đăng ký build, publish version mới của workflow ghim build mới, tùy chọn redo (run còn sống thì trả
  `nextStep`). Blocker drift có nút **Use the new CLI** kèm hộp thoại xác nhận. Test: `v5accept/cli_upgrade_test.go` (fake-claude thật,
  SQLite thật: build mới, version 2 ghim build mới, run cũ không repin, replay), `TaskDetail.test.tsx`, `TaskDetail.redo.test.tsx`; route
  inventory, security matrix, contract golden, generated.ts, UX doc.

### V12-06 — Vòng `size → simplify` không hội tụ (UX-26)

- **Bằng chứng:** `size` (`kit/commands/diff-size.sh`) dùng một ngưỡng cố định cho cả repo: 400 dòng, 8 file, 200 dòng mỗi
  file, bỏ qua `docs/` và `.md`. `simplify` có `cyclePolicy {maxIterations: 2, escalationOutcome: escalated}` nên chạy tối đa 3
  lần, lần thứ 4 thì `escalated → reject`. Task P2-02 vốn phải chạm 10 file mã và cấu hình; simplify chạy 3 lần không giảm được
  file nào, tốn khoảng 1,8 USD. Nếu run chạy tiếp thì task sẽ bị đóng dù `gate1` đã đạt cả 3 lần.
- **Mục tiêu (đề xuất của product owner, 2026-11-03):**
  1. Giữ `simplify` tối đa 3 lần. Lần 3 xong mà `size` vẫn `large` thì đi tới một **cổng người** (APPROVAL, ví dụ
     `size-review`) thay vì `reject`. Các lựa chọn ở cổng: chấp nhận diff lớn và đi tiếp tới `quality`, cho simplify thêm,
     tách task hoặc từ chối. Cổng hiện số liệu diff theo từng component.
  2. **Ngưỡng `diff-size` theo Component** (backend, frontend, infra, …), đặt và đổi được trên trang Components. Component chưa
     cấu hình thì dùng ngưỡng mặc định.
  3. `simplify` không đổi được gì so với lần trước thì sang cổng người ngay.
- **Ràng buộc:** `diff-size` vẫn không bỏ qua được (V10-15). Component hiện chưa có trường cấu hình, nên cần migration, API,
  giao diện và ADR. Sửa workflow mẫu và `diff-size.sh` để đọc ngưỡng theo component.
- **Kết quả (phần lớn đã xong, ADR-049, kit 1.9.0):** (1) cổng người `size-review` (accept/rework/rejected) sau khi `simplify` hết 3 lần;
  cần ngoại lệ của ADR-036 vì cổng nằm trong vòng (rework → build): cạnh escalation được dẫn tới node APPROVAL trong vòng (compiler và
  `advanceRunTx`). (3) `simplify` báo `no_change` thì tới cổng ngay. (2) ngưỡng theo component làm ở mức kit: file `.aw-diff-size` trong
  repository, output có số liệu từng component; **chưa làm** phần đặt ngưỡng trên trang Components (cần migration/API/UI). Test:
  `cycle_escalation_test.go`, 3 kịch bản walk mới (`plus-large-accept`, `plus-large-exhaust` sửa, `plus-simplify-no-change`; chạy với
  engine thật), `check-kit.sh` có ca ngưỡng component (81/81). Đã chạy `WALK_ALL` cho mọi kịch bản (kể cả các kịch bản V12-02 sửa).

### V12-07 — Lỗi của kit (UX-17 phần kit, UX-19, UX-21, UX-11, UX-10)

- `expect-fail` chỉ in `[ERROR] COMPILATION ERROR :` mà không in dòng lỗi thật (`file.java:[l,c] …`), nên agent không biết sửa
  gì. Lệnh phải in các dòng lỗi biên dịch.
- `expect-fail` cho qua test đỏ vì lỗi dựng dữ liệu (`DataIntegrityViolationException`, constraint SQL, NPE trong `@BeforeEach`).
  Chỉ được cho qua khi test thất bại ở assertion.
- `review-task.sh --commit` gọi `aw repository-workspace diff … --working-tree` thiếu `<repositoryWorkspaceId>`, nên chỉ in
  usage. Sửa cho khớp CLI hiện tại, và thêm một lần chạy thật vào check của kit.
- `context-check` dùng `os.killpg` nên không chạy trên Windows, và để lại một worker tạm mồ côi.
- Script của kit in tiếng Việt lỗi font trong console CP437. Script phải tự đặt UTF-8 hoặc in ASCII.
- **Test:** test của kit (`kit/tests`) cho từng lỗi, chạy trên Windows.
- **Kết quả (đã xong ở mức kit; chưa chạy trên Windows):** `expect-fail` in dòng lỗi biên dịch có vị trí (`File.java:[l,c]`, `file.ts(l,c)`)
  và chỉ cho qua khi test đỏ ở assertion (Maven: `Failures` > 0; `Errors` không kèm `Failures`, lỗi constraint/`@BeforeEach`/context
  thì không đạt; frontend: phải có dòng assertion). `review-task.sh --commit` truyền `<repositoryWorkspaceId>` sau các cờ. `context-check.py`
  dừng worker tạm bằng `taskkill /T` trên Windows (không dùng `os.killpg`), chờ nó thoát. Mọi script Python của kit in UTF-8 và đặt code
  page console 65001 trên Windows; script bash gọi `chcp.com 65001`. Test: `check-kit.sh` 83/83 (2 ca `expect-fail` mới). Chưa có lần
  chạy thật `review-task.sh --commit` trong check của kit (cần stack aw đầy đủ); lỗi cú pháp lệnh được sửa theo usage hiện tại.

### V12-08 — Một lệnh khởi động và khóa migration (UX-08)

- **Bằng chứng:** bật aw cần hai cửa sổ terminal (`worker`, `serve`). Bật cùng lúc trên database mới thì một bên lỗi migration
  `duplicate column name`.
- **Mục tiêu:** sửa tranh chấp migration trước, bằng khóa migration để hai tiến trình không chạy migration cùng lúc. Sau đó có
  một lệnh (ví dụ `aw up`) chạy cả serve và worker, dừng cả hai cùng nhau, ghi log về một chỗ.
- **Kết quả (đã xong, ADR-050):** nguyên nhân: kiểm "đã áp dụng" nằm ngoài transaction. Nay kiểm lại dưới khóa ghi (`appliedInsideTx`),
  chờ khóa tới 2 phút. Test `migration_lock_test.go` (4 tiến trình mở cùng một DB mới; trước khi sửa: `table projects already exists`).
  `aw up` chạy serve + worker trong một tiến trình (`up.go`, `up_test.go`; đã chạy thật: cả hai báo sẵn sàng, Ctrl+C dừng cả hai).

---

## Phần B — Run graph (UX-27; đã chốt 2026-11-03)

### Bằng chứng

Tab **Graph & Timeline** (`web/src/screens/TaskDetail.tsx`, `GraphTimelineTab`, `computeLayout`) không trả lời được ba câu
người vận hành cần: run đang ở bước nào, vì sao hỏng, giờ làm gì. Run `71307cba…` của task P2-02 (`wf-task-delivery-plus`,
18 node, 32 cạnh) cho thấy:

- `computeLayout` đặt mọi node cùng hạng trên một hàng: SVG rộng 2120px, cao 270px, phải cuộn ngang. Cạnh vẽ thẳng cắt ngang
  node, nhãn outcome đè nhau, END nằm giữa hàng.
- Mỗi node chỉ hiện lần chạy mới nhất. Vòng `gate1 → size → simplify → gate1` chạy 3 lần trông như 1 lần.
- Cột Timeline là 35 dòng `NODE_RUN` / `EXECUTION_ATTEMPT` mã thô, không có giờ, thời lượng, chi phí hay kết quả bằng lời.
- Banner chỉ ghi `RUN_FAILED … (RUN_FAILED)`. Nguyên nhân thật (gate1 lần 4 không khởi chạy được, không có evidence) phải đọc
  bằng CLI.

### Quyết định đã chốt (product owner, 2026-11-03)

1. **Giữ sơ đồ workflow làm giao diện chính.** Bấm một node thì hiện lịch sử thao tác và kết quả của node đó. Giao diện tiếng Anh.
2. **Bộ quy tắc hình dạng** ở mục "Quy tắc hiển thị" (gate là hình thoi).
3. **Mặc định chỉ hiện đường chính**. Node thoát (ví dụ `needs-info`, `reject`) ẩn; nút **All paths** hiện đầy đủ.
4. **Bỏ hẳn cột Timeline** cũ.
5. **Panel lịch sử đặt bên phải** sơ đồ.
6. **Task có nhiều run thì gộp lịch sử** các run vào cùng sơ đồ.
7. **Huy hiệu số lần chạy đếm theo số lần chạy thật** (mọi activation, kể cả lần bị chặn rồi retry), không đếm theo vòng.

### Quy tắc hiển thị

**Hình dạng** (suy ra từ dữ liệu sẵn có, không thêm trường vào định nghĩa workflow):

| Node | Hình |
|---|---|
| `AGENT` | Chữ nhật bo góc |
| `COMMAND` có từ 2 outcome trở lên (ví dụ `gate1`, `size`, `hygiene`, `quality`, `check-plan`, `expect-fail`) | Hình thoi (gate) |
| `APPROVAL` | Hình thoi viền kép (cổng người) |
| `COMMAND` chỉ có 1 outcome (ví dụ `reject`) | Chữ nhật góc vuông (hành động) |
| `START`, `END` | Viên thuốc |
| `FORK`, `JOIN` | Thanh ngang mảnh (chưa workflow mẫu nào dùng, nhưng vẫn phải vẽ được) |

**Bố cục:**

- **Đường chính** xếp dọc ở giữa: chuỗi node dài nhất đi từ `START` tới `END` theo outcome không phải thoát. Cạnh trên đường
  chính vẽ thẳng, nhãn outcome đặt bên phải.
- **Cạnh quay lại** (tới node có hạng nhỏ hơn) đi theo làn riêng bên trái, mỗi cạnh một làn. Các cạnh vào node đích ở độ cao
  khác nhau để không cắt nhau. Cạnh quay lại ngắn (ví dụ `check-plan → plan`, `expect-fail → repro`) và cạnh tắt (ví dụ
  `frame → build` qua outcome `fast`) đi bên phải.
- **Node phụ của một vòng** (ví dụ `debug`, `simplify`) đặt cạnh đường chính, ngang hàng với node mà vòng quay về.
- **Node thoát** là node chỉ tới được qua outcome thoát (`needs_info`, `escalated`, `rejected`, `abandon`) và dẫn về `END`.
  Chế độ mặc định ẩn chúng; node có cạnh tới node thoát mang huy hiệu `+n` (n là số cạnh bị ẩn). Chế độ **All paths** vẽ node
  thoát ở cột phải, cạnh nét đứt, các cạnh vào `reject` gom chung một làn.
- **Fallback:** workflow không có đường chính rõ (ví dụ đồ thị người dùng tự viết, rất lớn) thì dùng bố cục xếp hạng. Không bao
  giờ ném lỗi khi vẽ.

**Trạng thái:**

- Màu node theo trạng thái lần chạy cuối: thành công, thất bại, đang chạy, đang chờ; chưa chạy tới thì viền nét đứt. Dòng phụ
  trong node là outcome của lần chạy cuối.
- Huy hiệu `×n` khi n > 1, với n là số activation của node, gộp mọi run (quyết định 6 và 7).
- Cạnh đã đi qua được tô màu; nhãn kèm `×n` là số lần đi qua (lấy từ `takenEdges`), gộp mọi run.
- Banner trên sơ đồ: trạng thái run mới nhất, lý do dừng bằng lời (V12-11), tổng thời gian và chi phí.

**Panel lịch sử (bên phải).** Bấm một node (bằng chuột hoặc bàn phím) mở panel gồm:

- Tên node, loại, mô tả ngắn, giới hạn vòng (`cyclePolicy.maxIterations`) nếu có.
- **Outcomes:** mỗi outcome dẫn tới node nào và đã đi bao nhiêu lần.
- **History:** mỗi activation một dòng, gộp mọi run, mới nhất ở cuối. Mỗi dòng có số run, số lần chạy, giờ, thời lượng, chi
  phí, outcome và tóm tắt kết quả một dòng (V12-11). Mở một dòng ra thì thấy link tới Agent output và Files changed (AGENT),
  Command log (COMMAND), quyết định và diff đã duyệt (APPROVAL), và Evidence; dùng lại nội dung của `NodeDetailPane`.
- **Hành động** chỉ hiện khi server trả trong `validActions` (ví dụ Retry cho node bị chặn, các outcome của cổng duyệt đang
  chờ). Không suy hành động từ chữ trạng thái.

### Dữ liệu: đã có và còn thiếu

| Cần | Hiện trạng (`web/src/api/rundetail.ts`) |
|---|---|
| Node, cạnh, outcome, `cyclePolicy` | Có: `GraphNodeView`, `GraphEdgeView` |
| Số activation mỗi node, trạng thái, outcome | Có: `NodeActivationView` (`iteration`, `state`, `selectedOutcome`) |
| Số lần đi mỗi cạnh | Có: `TakenEdgeView` |
| Giờ, thời lượng, chi phí, mã lỗi từng attempt | Có: `TimelineEntryView` (`startedAt`, `finishedAt`, `usage`, `failureCode`, `terminationReason`) |
| Evidence, sự kiện attempt, lời agent khi hỏng | Có: `getNodeRunDetail`, `listAttemptEvents` (V11-11) |
| Danh sách run của một WorkItem để gộp lịch sử | Cần xác minh ở đầu V12-12 |
| **Tóm tắt kết quả một dòng mỗi activation** | **Thiếu** (V12-11) |
| **Lý do dừng run có kiểu, viết bằng lời** | **Thiếu** (V12-11) |

### V12-09 — Hàm bố cục và phân loại hình dạng

- **Mục tiêu:** một hàm thuần, không phụ thuộc React. Đầu vào là `nodes` và `possibleEdges`. Đầu ra là vị trí node, đường đi
  của từng cạnh (danh sách điểm), loại cạnh (đường chính, quay lại, tắt, thoát), hình dạng node và tập node thoát.
- **Thực hiện:**
  1. Phát hiện cạnh quay lại bằng DFS từ `START` theo thứ tự outcome đã khai báo; cạnh `COMPLETION_REWORK` luôn là cạnh quay lại.
  2. Xác định node thoát theo quy tắc ở mục "Bố cục". Danh sách outcome thoát là hằng số có tài liệu, không suy từ tên node.
  3. Đường chính là đường dài nhất từ `START` tới `END` trên đồ thị đã bỏ cạnh quay lại và node thoát.
  4. Gán làn cho cạnh quay lại sao cho đoạn ngang của làn ngoài không cắt đoạn dọc của làn trong (làn ngoài vào node đích ở
     vị trí cao hơn).
  5. FORK/JOIN: các nhánh song song đặt cạnh nhau giữa FORK và JOIN.
  6. Không tìm được đường chính thì fallback về bố cục xếp hạng.
- **Test (unit):** fixture là JSON thật của mọi workflow trong `kit/workflows/` và `wf-docs-change` của chochuyennha (chép vào
  fixture), cộng một workflow có FORK/JOIN. Với mỗi fixture, kiểm tra:
  - mọi node có vị trí, không có hai node chồng lên nhau;
  - mọi cạnh có đường đi;
  - tập node thoát đúng, hình dạng đúng theo bảng;
  - các làn không cắt nhau theo quy tắc ở bước 4.
- **Kết quả (đã xong):** `web/src/graph/layoutRunGraph.ts` (thuần, không React). Tinh chỉnh so với bước 3: "dài nhất" xếp sau "ít đường
  vòng nhất" — đường vòng là cạnh không phải outcome khai báo đầu tiên của nút nguồn **và** nằm trong cùng một vòng (SCC) với nguồn; nếu
  không, `size → simplify → size-review → quality` (V12-06) sẽ "dài hơn" luồng chính, còn `frame → build` (`fast` là outcome đầu) vẫn là
  cạnh tắt. Hai cạnh vào cùng đích có làn lồng nhau thì làn ngoài vào cao hơn; hai khoảng xen kẽ (không lồng) không thể tránh cắt trên một
  phía, test chỉ khẳng định cho cặp lồng nhau. Test: `layoutRunGraph.test.ts` (67 ca: mọi workflow của `kit/workflows` ở hai chế độ, một
  workflow FORK/JOIN, fallback, quy tắc hình dạng, node thoát, làn). Chưa có fixture `wf-docs-change` của chochuyennha (không có trong repo).

### V12-10 — Vẽ sơ đồ mới

- **Mục tiêu:** thay phần canvas của `GraphTimelineTab` bằng sơ đồ theo mục "Quy tắc hiển thị", dùng kết quả của V12-09.
- **Thực hiện:**
  1. Vẽ SVG theo hình dạng, kèm huy hiệu `×n`, `+n` và nút Main path / All paths. Lựa chọn của người xem lưu trong
     `localStorage`, bọc try/catch.
  2. **Bàn phím:** SVG đổi từ `role="img"` sang `role="group"`. Mỗi node là phần tử có `role="button"`, `tabindex="0"`, và
     `aria-label` gồm tên, loại, trạng thái, số lần chạy; Enter hoặc Space mở panel. Giữ chế độ **Accessible List** (hợp đồng
     accessibility của V7) cho tới khi V12-13 chứng minh sơ đồ mới đạt cùng mức.
  3. Theo dõi run đang chạy như hiện nay (`refetchInterval` 4 giây khi run còn sống).
- **Test:** component test với dữ liệu của 4 run thật đã ghi lại thành fixture JSON của API:
  - P2-02: hỏng giữa vòng lặp;
  - P1-01: nhiều vòng, có node bị chặn rồi retry;
  - P2-01: hai run;
  - P0-03: chỉ sửa tài liệu.
- **Kết quả (đã xong một phần):** `web/src/graph/RunGraphCanvas.tsx` thay canvas cũ trong tab Graph: hình dạng theo bảng, màu theo lần chạy
  cuối (viền nét đứt khi chưa tới), dòng phụ là outcome cuối, `×n` theo số activation, `+n` cho cạnh thoát bị ẩn, cạnh đã đi tô màu kèm
  `outcome ×n`, nút Main path / All paths (nhớ trong `localStorage`, bọc try/catch), SVG `role="group"`, mỗi node `role="button"` có
  `aria-label` (tên, loại, trạng thái, số lần chạy), Enter/Space chọn node. Accessible List giữ nguyên. Test: `RunGraphCanvas.test.tsx`
  với fixture dạng P2-02 (vòng lặp 3 lần, gate1 hỏng lần 4), axe. **Chưa có** fixture API ghi lại của 4 run thật (không có trong repo);
  P1-01, P2-01 (hai run, cần V12-12) và P0-03 sẽ thêm khi có dữ liệu.

### V12-11 — API: tóm tắt kết quả và lý do dừng

- **Khảo sát đầu task:** nội dung thật của evidence `AGENT_OUTPUT` và `COMMAND_EXECUTION`; các mã `failureCode`,
  `terminationReason` và blocker đang có; trường hợp attempt `EXECUTION_FAILED` mà không có evidence (P2-02, gate1 lần 4).
- **Thực hiện (đề xuất, chốt sau khảo sát):**
  1. Thêm `resultSummary` (chuỗi ngắn, đã che bí mật) vào mỗi activation trong projection đọc của run:
     - AGENT: lấy từ báo cáo cuối có cấu trúc của agent;
     - COMMAND: lấy dòng tóm tắt lệnh in ra theo quy ước (ví dụ `diff-size: large diff: …`), không có thì lấy dòng cuối không rỗng;
     - APPROVAL: quyết định, người duyệt và ghi chú.
  2. Thêm `stopReason` có kiểu vào chẩn đoán run, gồm `code` (ví dụ `COMMAND_NOT_STARTED`, `SCOPE_VIOLATION`,
     `PROVIDER_QUOTA_EXCEEDED`, `CYCLE_ESCALATED`, `ADAPTER_BUILD_DRIFT`, `APPROVAL_REJECTED`), `nodeKey`, `nodeRunId` và
     `message` tiếng Anh.
  3. Cập nhật OpenAPI và sinh lại `web/src/api/generated`. Viết ADR mới vì đổi API công khai. Cập nhật `docs/operator/`.
- **Test:** Go test cho từng mã `stopReason`, gồm ca command không khởi chạy được.
- **Kết quả (đã xong, ADR-051):** khảo sát: `AGENT_OUTPUT` là lời cuối của agent; `COMMAND_EXECUTION` là JSON argv/stdout/stderr/exit;
  attempt `EXECUTION_FAILED` không evidence của P2-02 là lỗi spawn, từ V12-04 có diagnostic `PROCESS_START_FAILED`. Đã làm:
  `runtime.ResultSummary` + `SummarizeRunActivations` (route graph điền `resultSummary` cho trang activation), `DeriveStopReason` (thuần)
  vào `RunDiagnostics.stopReason` (HTTP và CLI), `ApprovalRequestView.resolvedBy`. OpenAPI/contract golden và `generated.ts` sinh lại;
  kiểu tay ở `web/src/api/diagnostics.ts`, `rundetail.ts`. Test: `run_summary_test.go` (mỗi mã một ca, gồm hai ca command không khởi chạy
  được; ResultSummary theo loại node).

### V12-12 — Panel lịch sử bên phải

- **Khảo sát đầu task:** cách lấy mọi run của WorkItem. Khi các run dùng workflow version khác nhau (ví dụ sau Redo with Latest
  Version), sơ đồ vẽ theo version của run mới nhất; lịch sử của node không còn trong version đó vẫn hiện trong panel, gắn nhãn
  version cũ.
- **Thực hiện:** gộp activation của mọi run theo `nodeKey`. Mỗi dòng lịch sử ghi số run. Mở rộng dòng thì dùng lại nội dung
  `NodeDetailPane`. Nút hành động lấy từ `validActions`.
- **Test:** component test với fixture P2-01: lịch sử của run 1 (hỏng ở `build`) và run 2 (thành công) cùng hiện ở node `build`.
- **Kết quả (đã xong):** khảo sát: `getWorkItemProjectedDetail.runs` đã liệt kê mọi run (số thứ tự, version) — không cần API mới. Tab Graph
  tải graph và timeline (trang đầu) của các run trước, `web/src/graph/runHistory.ts` (`mergeRunHistory`, thuần) gộp theo `nodeKey`: `×n` và
  cạnh đã đi đếm mọi run, dòng lịch sử ghi số run, giờ, thời lượng, chi phí, outcome, `resultSummary`; run khác version gắn nhãn "older
  version". `RunHistoryPanel` bên phải: tên, loại, giới hạn vòng, outcome và số lần đi, lịch sử (dòng mới nhất mở sẵn, mở dòng thì là
  `NodeDetailPane` của activation đó), hành động chỉ từ `validActions` (Retry) và approval đang chờ. Không còn run hoạt động thì sơ đồ vẽ
  run mới nhất. Test: `RunHistoryPanel.test.tsx` (fixture dạng P2-01, version cũ, axe).

### V12-13 — Banner, dọn mã cũ, Playwright

- **Thực hiện:** banner dùng `stopReason`. Bỏ cột Timeline, `computeLayout` và mã chỉ phục vụ chúng; cập nhật test cũ của tab.
- **Test:** Playwright trên stack thật (`aw serve` và worker với agent giả lập) cho các hành trình: run có vòng lặp, run hỏng
  giữa chừng, task hai run, node bị chặn rồi retry, cổng duyệt đang chờ. Kiểm tra bàn phím mở được panel của mọi node. Đọc phần
  bài học Playwright của V7-17C trước khi viết.
- **Kết quả (đã xong một phần):** banner trên sơ đồ: run thứ mấy, trạng thái, `stopReason` (bấm thì mở panel của node đó), thời gian, chi
  phí. Bỏ cột Timeline, `computeLayout`, `NODE_STYLE`, `STRUCTURAL_KINDS`; panel lịch sử chiếm cột phải, có gợi ý khi chưa chọn node. Test cũ
  của tab cập nhật (chi phí ở banner và ở dòng lịch sử). Playwright `web/e2e/v12-run-graph.spec.ts` trên stack thật (`aw serve` + worker,
  fake provider): task hai run (gộp `×2`, Run 1 và Run 2 trong panel), Tab + Enter mở panel của **mọi** node, banner, không còn Timeline;
  cổng duyệt đang chờ: banner nói, bấm mở panel của cổng, quyết định ở đó. **Chưa có** hành trình Playwright cho run có vòng lặp và node bị
  chặn rồi retry (cần workflow có COMMAND trên stack e2e; phần này có component test ở V12-10/V12-12). Accessible List giữ lại: sơ đồ mới
  đạt bàn phím và axe trong test, nhưng chưa thay hợp đồng accessibility của V7 bằng một quyết định của product owner.

---

## Phần C — Thao tác còn thiếu trên giao diện

### V12-14 — Form tạo task con có contract (UX-02, UX-03, UX-25, UX-38, UX-42)

- **Bằng chứng:** hộp thoại New WorkItem chỉ tạo WorkItem gốc. Task con phải viết bằng file JSON rồi chạy `run-task.sh`. Trường
  `parentJoinPolicy` bắt buộc nhưng không có mặc định và không có hướng dẫn. Người dùng không biết workflow nào dùng cho việc gì.
  Ở task P2-01, contract nhắc một file nằm ngoài `pathScopes` mà aw vẫn tạo task; agent sửa file đó và run hỏng (mất 0,65 USD).
- **Mục tiêu:** form "New task" trong một family:
  - các trường: tiêu đề, behavior, AC, scope (chọn thư mục), risk, workflow; hoặc dán JSON;
  - mẫu theo loại việc: bugfix, feature, docs, infra;
  - mỗi workflow có mô tả "dùng khi nào". Gợi ý theo scope và rủi ro (chỉ `docs/` thì `wf-docs-change`; có test tái hiện được
    thì `wf-bugfix`; rủi ro MEDIUM trở lên thì bản plus). Cảnh báo khi chọn lệch;
  - `parentJoinPolicy` có mặc định;
  - kiểm tra trước khi tạo: đường dẫn được nhắc trong contract mà nằm ngoài `pathScopes` thì cảnh báo; AC thiếu thì báo.
- **Ghi chú:** API `create-child` đã có. "Soạn contract bằng AI từ một câu mô tả" là phần mở rộng, chỉ làm nếu product owner duyệt.
- **Kết quả (đã xong, không cần API mới):** nút **New task** trên trang task mở `NewTaskDialog`: mẫu theo loại việc (bug fix, feature,
  documentation, infrastructure), tiêu đề, behavior, AC, cách kiểm, rủi ro, scope theo repository với thư mục (gợi ý từ Components và
  `docs`), workflow chọn từ các định nghĩa project đã publish (version mới nhất) kèm "use when" và nhãn "suggested" (chỉ `docs/` → docs-change,
  bug fix → bugfix, rủi ro MEDIUM+ → plus), cảnh báo khi chọn lệch, `parentJoinPolicy` mặc định `ALL_CHILDREN_DONE`, hoặc dán JSON của
  `run-task.sh`. Kiểm tra trước khi tạo (`web/src/api/newTask.ts`, thuần): đường dẫn trong contract mà không scope nào phủ (trường hợp P2-01)
  là cảnh báo; thiếu tiêu đề/behavior/AC/scope/workflow, AC còn placeholder là lỗi chặn. Test: `newTask.test.ts`, `NewTaskDialog.test.tsx`.
  Không làm phần "soạn contract bằng AI" (chờ product owner duyệt).

### V12-15 — Sơ đồ workflow trên trang Definitions (UX-44)

- Dùng lại V12-09 và V12-10, không có dữ liệu run, để vẽ sơ đồ workflow trên trang Definitions và trong form tạo task của V12-14.
- **Kết quả (đã xong):** `web/src/graph/WorkflowDiagram.tsx` dùng `RunGraphCanvas` không có activation (mọi node "not reached"), Main/All
  paths, bàn phím; chọn node thì liệt kê outcome dẫn tới đâu. Trang Definitions vẽ version WORKFLOW đang chọn (từ `compiledSnapshot`, không
  được thì `canonicalSource`); form New task vẽ workflow đang chọn. Test: `WorkflowDiagram.test.tsx` (`wf-bugfix` của kit).

### V12-16 — Hộp "Needs you" và thông báo (UX-28, UX-29)

- **Mục tiêu:** một trang gom mọi việc chờ người của mọi task: cổng duyệt đang chờ, run hỏng, node bị chặn, task cần thông tin.
  Mỗi việc có hành động một chạm (lấy từ `validActions`), và menu có bộ đếm.
- Thông báo của trình duyệt khi có việc mới. Kênh ngoài tùy chọn (email, webhook) để người vận hành làm việc từ xa vẫn biết khi
  cần duyệt hoặc run hỏng. Kênh ngoài gửi dữ liệu ra khỏi máy nên cần ADR và chỉ gửi tiêu đề kèm link, không gửi nội dung code.
- **Kết quả (đã xong trừ kênh ngoài, ADR-052):** `runtime.ListNeedsYou` (`GET /projects/{id}/needs-you`, `aw run needs-you`): approval
  `PENDING` (kèm outcome và version) và blocker `OPEN` của các task còn mở. Trang **Needs you** trong menu project, bộ đếm đỏ (hỏi lại 15 giây),
  quyết định approval một chạm, "Open task" cho blocker (Run Again/Retry/Use the new CLI/Resolve ở trang task), thông báo trình duyệt khi
  có mục mới. Kênh e-mail/webhook **chưa làm** (cần ADR riêng). Test: `quota_resume_test.go` (`ListNeedsYou`), `NeedsYou.test.tsx`; route
  inventory, security matrix, contract golden, UX doc.

### V12-17 — Trình xem diff có bình luận (UX-31)

- **Mục tiêu:** ở cổng duyệt, xem diff từng file có tô màu và bình luận theo dòng. Nút "Request changes" gom các bình luận
  thành message gửi kèm outcome `revise`. Bình luận được lưu cùng quyết định để làm evidence.
- **Kết quả (đã xong, chỉ web):** nút **Review diff** trên dải approval của tab Graph mở `web/src/review/DiffReviewDialog.tsx`:
  diff chưa commit của mọi repository `READY` của family, từng file, dòng thêm/xóa tô màu, bấm một dòng để bình luận.
  **Request changes** (outcome `revise`, hoặc `rework` ở `size-review`) gửi bình luận thành message USER của task (`appendMessage`,
  maker đọc ở attempt sau) và quyết định outcome đó với cùng văn bản làm `reason` (lưu cùng quyết định). Khi còn bình luận thì nút
  Approve tắt; approve kèm commit vẫn đi qua hộp commit của dải approval. Không đổi API. Logic thuần ở `diffReview.ts`; test
  `diffReview.test.ts`, `DiffReviewDialog.test.tsx`.

### V12-18 — Message nhắm node (UX-20)

- **Bằng chứng:** message của người vận hành gắn vào cả WorkItem, nên mọi node AGENT về sau đều nhận. Ghi chú "không sửa code
  sản phẩm" viết cho `repro` làm `fix` cũng không sửa code, mất 2 vòng.
- **Mục tiêu:** chọn phạm vi cho message: một node, hoặc chỉ attempt kế tiếp. Giao diện hiện rõ message nào đang có hiệu lực với
  node nào.
- **Kết quả (đã xong, ADR-053):** message có `targetNodeKey` và `nextAttemptOnly` (migration 0057, HTTP `appendMessage`,
  `aw message append --node --next-attempt-only`). Scheduler bỏ message nhắm node khác khỏi snapshot; message chỉ-attempt-kế-tiếp
  vào attempt đầu tiên sau nó rồi thôi (đã giao đọc từ snapshot, `listMessages` trả `delivered`). Chạy lại kèm note giữ đích, bỏ message
  đã giao. Tab Chat: ô **For** (các node AGENT của run mới nhất) và **only the next attempt**; mỗi message hiện nhãn "for repro",
  "next attempt only — delivered/waiting". Test: `message_target_sqlite_test.go`, `TaskDetail.test.tsx`.

### V12-19 — Hàng đợi task (UX-30)

- **Mục tiêu:** xếp nhiều task vào hàng đợi của family. Task kế tự chạy khi task trước đi qua cổng duyệt cuối. Khai báo được
  "chờ task X". Family dùng chung một worktree nên hàng đợi luôn chạy tuần tự.
- **Kết quả (đã xong, ADR-054):** bảng `work_item_queue_entries` (migration 0058), `SetWorkItemQueue`/`GetFamilyQueue`
  (`POST`/`GET /work-items/{id}/queue`, `aw work-item queue`, `queue-show`). Worker quét mỗi 10 giây (`AdvanceFamilyQueues`), chạy
  mục đầu khi task trước `DONE`, family không có run đang chạy và task được chờ `DONE`. Task trước hỏng thì hàng đợi dừng
  ("paused: …"); không chạy được thì ghi `problem`. Điều kiện là `DONE` thay cho "qua cổng duyệt cuối" để không tranh worktree với
  commit. Tab Overview có panel Queue (Add to queue, Wait for, Remove from queue). Test: `queue_sqlite_test.go`,
  `FamilyQueue.test.tsx`; route inventory, security matrix, contract golden, UX doc.

### V12-20 — Đồng bộ với nhánh gốc và dọn nhánh (UX-24, UX-14)

- **Bằng chứng:** sau khi PR được merge trên GitHub, nhánh family vẫn đứng ở commit cũ. Muốn task mới chạy trên `master` phải
  chạy `git merge --ff-only` trong worktree rồi `adopt-head.sh`. Merge xong thì nhánh `agentkit/w-…` và worktree vẫn còn.
- **Mục tiêu:**
  - Nút "Sync with base branch": fetch, fast-forward hoặc báo cần merge, rồi adopt, tất cả trong một bước có audit.
  - Family hiện trạng thái "đã merge vào <nhánh>".
  - Nút "Close work batch": release workspace set, xóa các nhánh đã merge.
- **Ràng buộc:** chỉ fetch và cập nhật cục bộ; không push (push thuộc V12-28).
- **Kết quả (đã xong, ADR-055):** `SyncWorkspaceWithBase` (`POST .../sync-base`, `aw repository-workspace sync-base`): fetch default
  ref từ `origin`, fast-forward worktree, ghi HEAD qua `AdoptWorkspaceHead` (audit `WorkspaceHeadAdopted`); rẽ nhánh báo `NEEDS_MERGE`,
  worktree bẩn báo `DIRTY`; HEAD chưa ghi thì từ chối (adopt trước). `GetWorkspaceBaseStatus` (`base-status`): ahead/behind/merged.
  Tab Workspace có dải "Base branch … merged into main" và nút **Sync with base branch**. Đóng đợt việc là release workspace set:
  release nay xóa nhánh `agentkit/w-…` khi ref khác đã chứa tip (nhánh còn commit riêng thì giữ). Test: `basesync_test.go`
  (git thật, có origin), `sync_base_sqlite_test.go`, `BaseBranchStrip.test.tsx`; route inventory, security matrix, contract golden.

### V12-21 — Publish định nghĩa trên giao diện (UX-04)

- **Mục tiêu:** màn "Project definitions": chọn manifest (`aw-project.json`), chạy `--check`, xem diff version, bấm Publish.
- **Ràng buộc:** `kit/scripts/aw-publish.py` hiện là lớp ghép ngoài engine. Phải quyết định đưa logic publish vào engine hay để
  giao diện gọi qua API, và cần ADR.
- **Kết quả (đã xong, ADR-056):** logic publish ở lại trong kit; `aw serve --project-manifest <projectId>=<path>` cho server chạy
  `aw-publish.py` của kit trên manifest đó (đường dẫn chỉ từ cấu hình lúc khởi động). Route `getDefinitionManifest`,
  `runDefinitionManifest {mode: CHECK|PUBLISH}`, CLI `aw definition manifest-status`/`manifest-run`. Trang Definitions (Project) có
  panel Manifest: Check, Publish… (xác nhận), output và mã thoát; diff version dùng API có sẵn. Test: `kitpublish/runner_test.go`
  (Python thật, script giả), `ManifestPanel.test.tsx`; route inventory, security matrix, parity, contract golden.

### V12-22 — Tạo task từ `tasks.json` (UX-09)

- Sau cổng DESIGN của `wf-feature-definition`, có nút "Create tasks from tasks.json": xem trước danh sách rồi tạo các WorkItem
  con. Thay cho `split-tasks.sh`.
- **Kết quả (đã xong, chỉ web):** nút **Tasks from tasks.json** trên trang task mở `CreateTasksDialog`: đọc
  `docs/features/<slug>/tasks.json` ở revision đã ghi của các repository trong family (hoặc dán vào), báo mục thiếu trường, xem trước
  (repository, thư mục, số AC, lane, workflow theo rủi ro), tạo task con theo thứ tự với đúng tiêu đề/scope/contract của
  `split-tasks.sh` (`web/src/api/tasksJson.ts`), mark ready và (tùy chọn) xếp vào hàng đợi của V12-19. Idempotency key theo task nên
  tạo lại không nhân đôi. Không đổi API. Test: `tasksJson.test.ts`, `CreateTasksDialog.test.tsx`.

### V12-23 — Chi phí và hạn mức (UX-35, UX-36)

- Màn chi phí: tổng theo task, đợt (WorkItem gốc) và project. Trần ngân sách theo WorkItem gốc, có cảnh báo khi vượt.
- Hiện hạn mức còn lại và giờ reset của provider nếu provider cung cấp; tạm dừng hàng đợi (V12-19) khi gần hết. Cần xác minh
  Claude CLI có trả số liệu này không.
- **Kết quả (đã xong, ADR-057):** `GetProjectCosts` (`GET /projects/{id}/costs`, `aw run costs`) và trang **Costs**: token và USD
  theo task, đợt (WorkItem gốc) và project, từ `USAGE_REPORTED`. Trần ngân sách theo WorkItem gốc (`work_item_budgets`,
  migration 0059; `POST /work-items/{id}/budget`, `aw work-item budget`), cảnh báo gần/vượt; vượt thì hàng đợi V12-19 không chạy
  task mới. Hạn mức còn lại **chưa làm**: Claude CLI có in `rate_limit_event` nhưng adapter bỏ nội dung và cấu trúc trường chưa
  được xác minh với CLI thật (cần bản ghi stream thật); aw chỉ biết khi chạm (V12-03). Khi có `PROVIDER_QUOTA_EXCEEDED` đang mở,
  mọi hàng đợi dừng và trang Costs hiện giờ chạy tiếp. Test: `costs_sqlite_test.go`, `Costs.test.tsx`; route inventory, security
  matrix, parity, contract golden.

### V12-24 — Trang trạng thái project (UX-37)

- Trang Overview của project: tiến độ theo đợt và WorkItem gốc, việc đang chờ, lịch sử gần đây, có xuất báo cáo. Thay cho file
  `STATUS.md` mà người vận hành đang tự giữ.
- **Kết quả (đã xong, chỉ web):** panel **Status** trên Overview của project (`web/src/screens/ProjectStatus.tsx`): từng đợt (WorkItem
  gốc và mọi task dưới nó, qua mọi tầng cha) với thanh tiến độ, số task xong/bị chặn/đang chạy, chi phí của đợt (V12-23); danh sách
  đang chạy; số việc chờ người (V12-16). **Export report** tải `STATUS-<project>-<ngày>.md` cùng nội dung (`api/projectStatus.ts`).
  Chưa làm: lịch sử có mốc thời gian (thẻ kanban không mang thời điểm; cần một truy vấn sự kiện gần đây). Test:
  `projectStatus.test.ts`, `ProjectStatus.test.tsx`.

### V12-25 — Commit message theo quy ước repo (UX-13)

- **Bằng chứng:** commit do aw tạo có tiêu đề là tiêu đề WorkItem kèm hậu tố "(lần 3)" và chuỗi
  `[Release-Set-Local-Commit-Marker: sha256:…]`, không có thân commit.
- **Mục tiêu:** ô commit message có mẫu mặc định cấu hình theo project (ví dụ Conventional Commits). Marker chuyển xuống trailer
  cuối thân commit. Bỏ hậu tố "(lần N)".
- **Ràng buộc:** marker cần để truy ngược ReleaseSet, chỉ đổi vị trí, không bỏ.
- **Kết quả (đã xong, ADR-058):** adapter Git nhận message nhiều dòng (`git commit --cleanup=verbatim -F -`); marker là trailer cuối
  thân (`commitMessageWithMarker`, nối vào khối trailer có sẵn); mẫu commit message theo project (`project_settings`, migration 0060;
  `GET`/`POST /projects/{id}/commit-template`, `aw project commit-template`/`set-commit-template`) với `{title}` (bỏ " (lần N)"),
  `{taskId}`, `{workItemId}`; hộp commit ở cổng duyệt điền sẵn từ mẫu; panel sửa mẫu trên Overview của project. Test:
  `localcommit_test.go` (message nhiều dòng, git đọc được trailer), `marker_trailer_test.go`, `commit_template_test.go`,
  `commitTemplate.test.ts`; route inventory, security matrix, parity, contract golden.

### V12-26 — Xem prompt của node (UX-12)

- Tab "Prompt" trong panel của node, đọc từ ContextSnapshot (giới hạn đã ghi ở V11-11).
- **Kết quả (đã xong, ADR-059):** `ReadAttemptPrompt` dựng lại prompt từ ContextSnapshot bằng chính hàm render của bước lắp request
  (`renderAttemptInstruction`, tách khỏi `AssembleAgentExecutionRequest`), che secret, tối đa 512 KiB;
  `GET /projects/{id}/execution-attempts/{attemptId}/prompt`, `aw node-run prompt [--raw]`. Ngăn chi tiết node AGENT có mục Prompt
  (Show prompt, chọn attempt, Copy). Test: `attempt_prompt_test.go` (bằng đúng prompt đã lắp, che secret, chặn project khác),
  `AttemptPrompt.test.tsx`; route inventory, security matrix, parity, contract golden.

### V12-27 — Cờ CLI bị bỏ qua (UX-41)

- **Bằng chứng:** usage của `aw evidence list` in `<workItemId> --project-id …`, nhưng cờ đặt sau tham số vị trí bị bộ parse cờ
  của Go bỏ qua, và lệnh chỉ in lại usage.
- **Mục tiêu:** chấp nhận cờ ở mọi vị trí cho mọi lệnh, hoặc usage ghi đúng thứ tự và báo rõ "cờ đặt sau tham số bị bỏ qua".
  Rà mọi lệnh dùng cùng kiểu parse.
- **Kết quả (đã xong):** `cli.Interspersed` đưa mọi cờ của FlagSet lên trước tham số vị trí (đặt sau `--`); mọi leaf trong
  `internal/delivery/cli` (27 chỗ `fs.Parse(args)` và doctor/health/settings) parse qua nó, nên cờ đặt ở đâu cũng được đọc. Cờ lạ vẫn
  báo lỗi; cờ cần giá trị mà thiếu vẫn báo lỗi (không lấy `--` làm giá trị); `--` vẫn kết thúc cờ. Tùy chọn toàn cục của `aw` (đứng
  trước tên resource) giữ nguyên. Test: `interspersed_test.go`, toàn bộ test CLI. Docs: operator 03.

---

## Phần D — Cần ADR hoặc quyết định của product owner trước

Mỗi task ở phần này bắt đầu bằng một ADR. Không viết mã trước khi product owner duyệt ADR.

### V12-28 — Merge, push, PR và CI từ giao diện (UX-01, UX-39)

- **Bằng chứng:** duyệt kèm commit làm được trên giao diện, nhưng đưa commit vào nhánh làm việc, push và tạo PR đều phải làm
  trong terminal.
- **Ràng buộc:** đụng nguyên tắc Alpha "không có executor cho push/PR/merge" (`00-start-here` mục 2.11, ADR về ReleaseSet).
  ADR phải quyết: những thao tác nào được phép, ai được bấm, có xác nhận gì, credentials lấy từ đâu (dùng `gh` hay git
  credential của máy), và audit.

### V12-29 — Kho secret theo repository (UX-34)

- **Bằng chứng:** gate và ứng dụng cần DB, R2…, nên giá trị phải để trong file local (`backend/local.properties`). Mật khẩu DB
  dev của chochuyennha từng nằm trong git.
- **Mục tiêu:** kho secret theo repository, tiêm vào gate và vào lúc chạy ứng dụng (V12-30), che giá trị khi hiển thị, không bao
  giờ đưa vào prompt của agent.
- **Ràng buộc:** mã hóa khi lưu, phạm vi truy cập, tương tác với `env-allowlist`.

### V12-30 — Chạy ứng dụng của worktree để QA tay (UX-32)

- Hồ sơ "Run app" theo repository (lệnh, cổng, biến môi trường lấy từ V12-29). Có Start/Stop, log, link mở preview; tự tắt khi
  đóng. Phải dọn tiến trình con để không để lại tiến trình mồ côi giữ cổng (đã gặp với backend và next dev).

### V12-31 — QA trong aw (UX-33, UX-43)

- WorkItem loại "QA": danh sách mục đạt hoặc không đạt, đính kèm ảnh, nút "Create fix task" từ mục không đạt (điền sẵn contract
  qua form của V12-14).
- Biến thể bugfix cho UI chưa có bộ test: tái hiện bằng script trình duyệt (Playwright), hoặc dùng checklist QA có ảnh trước và
  sau làm evidence ở cổng người.

### V12-32 — Truy cập từ xa và màn duyệt cho điện thoại (UX-40)

- Giao diện hiện chỉ mở được trên máy chạy aw. Cần thiết kế bảo mật riêng (tunnel có xác thực, phạm vi thao tác cho phép từ xa)
  trước khi làm màn duyệt gọn cho điện thoại.

### V12-33 — Nơi lưu tài liệu trung gian của kit (UX-45)

- Các node của kit ghi `frame.md`, `plan.md` vào `docs/features/<feature>/tasks/<task>/` của repo sản phẩm, mỗi task thêm một
  thư mục. Product owner quyết định có cho chọn lưu chỉ trong artifact của aw (xem được trên giao diện) hay không; đây là thiết
  kế của V10.

---

## Ngoài phạm vi V12

- Chọn effort hoặc ngân sách riêng cho từng node (product owner đã từ chối, 2026-10-04).
- Gọi model phân loại bên ngoài ở các gate (đã đánh giá TypeSafe Jev ngày 2026-11-03; product owner không chọn vì giá trị chưa
  nổi bật trong khi có rủi ro đưa dữ liệu ra ngoài).

## Gate V12

- `go test ./...`, test của `web/` và `kit/tests`, Playwright xanh trên CI.
- Phần A: mỗi lỗi có test hồi quy và một lần chạy thật trên Windows tái hiện đúng tình huống đã gặp ở chochuyennha.
- Phần B: unit test của V12-09 xanh với mọi workflow trong `kit/workflows/` và fixture FORK/JOIN. Product owner mở 4 run thật của
  chochuyennha (P2-02, P1-01, P2-01, P0-03) trên giao diện mới và xác nhận: thấy đúng vòng lặp và số lần chạy, đọc được lý do
  dừng của P2-02, mở được lịch sử mọi node bằng chuột và bàn phím.
- Phần C: chạy trọn một task chochuyennha từ tạo task tới commit và đồng bộ nhánh mà không mở terminal.
- Phần D: chỉ các task có ADR đã được duyệt mới tính vào gate.
