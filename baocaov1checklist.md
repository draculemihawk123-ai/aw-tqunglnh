# V1 — Báo cáo triển khai liên tục

> File làm việc cá nhân, không commit vào git (giống `baocaov0checklist.md`). Cập nhật sau mỗi task.
> Quy ước: khi vướng quyết định không tường minh trong task spec, tôi chọn theo khuyến nghị tốt nhất
> của tôi, ghi rõ lựa chọn + lý do ở đây, rồi tiếp tục — không dừng lại hỏi trừ khi thực sự chặn cứng
> (thiếu thông tin không thể suy luận được, hoặc rủi ro phá hoại/không thể đảo ngược).

## Trạng thái tổng quan

| Task | Trạng thái | PR | CI | Ghi chú |
|---|---|---|---|---|
| V1-00A | DONE | — (commit trực tiếp) | ✅ | Merge trước phiên báo cáo này |
| V1-00B | DONE | — (commit trực tiếp) | ✅ | |
| V1-00C | DONE | [#3](https://github.com/taQuangLing/agent-workflow/pull/3) | ✅ debt=0 | Phát hiện + sửa 1 bug parser thật, đóng 107 gap coverage thật |
| V1-01 | DONE | [#4](https://github.com/taQuangLing/agent-workflow/pull/4) | ✅ | CLI skeleton, xanh ngay lần đầu |
| V1-02 | DONE | [#5](https://github.com/taQuangLing/agent-workflow/pull/5) | ✅ | Bug cú pháp thật (composite literal trong if-header) + 1 flake SPK-10 không liên quan — cả hai đã xử lý, xem chi tiết bên dưới |
| V1-02A | DONE | [#6](https://github.com/taQuangLing/agent-workflow/pull/6) | ✅ | Bug đếm depth thật (Interface-kind qua `any`-typed map/slice) — đã sửa + trace lại, CI xanh lần 2 |
| V1-03 | DONE | [#7](https://github.com/taQuangLing/agent-workflow/pull/7) | ✅ | CI xanh ngay lần đầu — 2 bug thật bắt được trước khi push (struct so sánh chứa map, tự chế lại errors.As) |
| V1-04 | DONE | [#8](https://github.com/taQuangLing/agent-workflow/pull/8) | ✅ | Bug thật: thiếu `.gitattributes` → Windows CI checkout ra CRLF cho .sql, checksum khác Linux. Đã thêm `.gitattributes` (`*.sql text eol=lf`), verify local `git add --renormalize` không đổi byte nào |
| V1-04A | DONE | [#9](https://github.com/taQuangLing/agent-workflow/pull/9) | ✅ | verifyDurabilityPragmas + RunSerializedWrite/RunReadOnly + MapSQLiteError — verify local đầy đủ trước push |
| V1-05 | DONE | [#10](https://github.com/taQuangLing/agent-workflow/pull/10) | ✅ | CI xanh cả 6 job ngay lần đầu. **Quyết định phạm vi cần review** — xem chi tiết bên dưới bảng |
| V1-06 | DONE | [#11](https://github.com/taQuangLing/agent-workflow/pull/11) | ✅ | CI xanh cả 6 job ngay lần đầu |
| V1-07 | DONE | [#12](https://github.com/taQuangLing/agent-workflow/pull/12) | ✅ (lần 2) | journal_position + causation_id + outbox (migration 0003), dispatcher claim/mark-dispatched/recover-lease — **bắt được 1 bug thật phá cả bộ regression V0** trước khi push + 1 flake timing SPK-09 trên CI (không liên quan diff) — xem chi tiết bên dưới |
| V1-07A | DONE | [#13](https://github.com/taQuangLing/agent-workflow/pull/13) | ✅ | CI xanh cả 6 job ngay lần đầu |
| V1-08 | DONE | [#14](https://github.com/taQuangLing/agent-workflow/pull/14) | ✅ | CI xanh cả 6 job ngay lần đầu |
| V1-09 | DONE | [#15](https://github.com/taQuangLing/agent-workflow/pull/15) | ✅ | CI xanh cả 6 job ngay lần đầu |
| V1-10 | DONE | [#16](https://github.com/taQuangLing/agent-workflow/pull/16) | ✅ (lần 2) | `workerpool.Pool` — bắt được 1 bug logic thật (context hierarchy) trước khi push, và 1 bug ở chính test (self-race) + 1 flake SPK-10 đã biết trên CI lần đầu, cả hai đã xử lý — xem chi tiết bên dưới |
| V1-11 | DONE | [#17](https://github.com/taQuangLing/agent-workflow/pull/17) | ✅ (lần 2) | `doctor` package — bắt được 1 vi phạm kiến trúc thật local trước khi push, và 1 bug CRLF/LF thật trên CI Windows lần đầu (cùng root cause với V1-04's `.sql` bug), cả hai đã xử lý — xem chi tiết bên dưới |
| V1-12 | DONE | [#18](https://github.com/taQuangLing/agent-workflow/pull/18) | ✅ | CI xanh cả 6 job ngay lần đầu, kể cả `cross-platform semantic diff (SPK-13)` — **V1 Alpha Foundation đóng tại đây** |

## V1 Alpha Foundation — verdict: GO

Toàn bộ 20 task (V1-00A…V1-00C, V1-01…V1-12) đã DONE, mỗi task có PR riêng (trừ V1-00A/B commit trực
tiếp), mỗi PR có CI thật xanh (6 job: contract × 2 OS, spike acceptance × 2 OS, Linux race and stability
V0-12, cross-platform semantic diff SPK-13) trước khi merge — không có task nào tự nhận hoàn thành mà
thiếu bằng chứng CI thật.

**Tổng số bug/vi phạm thật bắt được trong phiên này** (không tính flake không liên quan): 1 bug cú pháp
Go (V1-02), 1 bug đếm depth reflect (V1-02A), 2 bug tự bắt trước push (V1-03), 1 bug `.gitattributes`/CRLF
cho `.sql` (V1-04), 1 near-miss ghi đè file test (V1-04A, tự phục hồi), 1 bug thật phá cả bộ regression V0
do quên 4 nơi ghi `domain_events` cũ (V1-07), 1 bug logic context hierarchy khiến shutdown grace vô tác
dụng (V1-10), 1 bug ở chính test tự đua với pool (V1-10), 1 vi phạm kiến trúc thật (doctor import thẳng
adapter, V1-11), 1 bug `.gitattributes`/CRLF cho `.json` golden fixture (V1-11) — tổng cộng khoảng 11 vấn
đề thật, tất cả đều được phát hiện bằng bằng chứng thật (test tự viết fail thật, hoặc CI fail thật với log
đọc được) chứ không phải suy đoán, và đều đã xử lý trước khi task tương ứng được coi là DONE.

**2 flake đã biết, không phải do V1 gây ra, không chặn merge nào:** SPK-10 cross-platform non-determinism
(lộ lại ở V1-02 và V1-07; task điều tra `task_d9ce5cac` vẫn pending) và SPK-09 Windows stress-test timing
(lộ ở V1-10; task điều tra `task_16a782e5` vẫn pending). Cả hai đều được minh chứng KHÔNG liên quan tới
diff đang xét (rerun cùng commit ra xanh, hoặc lỗi xảy ra ở đoạn code PR không đụng tới) trước khi merge.

**Quyết định phạm vi cần bạn xem lại (chưa tự ý làm, đã ghi rõ lý do ở mục tương ứng phía trên):**
- V1-05: 7 UnitOfWork repository interface (Catalog/Work/Definitions/Runtime/Jobs) vẫn để trống — chỉ
  Events/Receipts được điền (V1-06/07).
- V1-07A: `EnforcingEventsRepository` là decorator rời, chưa tự động wire vào mọi `Tx.Events()`.
- V1-10: `workerpool.Pool` chưa wire vào CLI stub `serve`/`worker` (V1-01).

**Chưa làm, ngoài phạm vi V1 theo chính roadmap:** wiring CLI `serve`/`worker` thật, loopback HTTP API
(V1-11 nhắc tới nhưng config chưa có field), full outbox sequence/upcaster production wiring (V1-07A mới
là contract, chưa gắn vào caller thật), AdapterBuildVersion registry (V2-07A).

## Ràng buộc môi trường quan trọng

- **[CẬP NHẬT ngay trước V1-04]** Tìm ra Go toolchain thật đã có sẵn trên máy, chỉ nằm ở vị trí chưa tìm
  tới lúc đầu: `.tools/go1.27.0/go/bin/go.exe` (go1.27.0 windows/amd64). Đã verify: `go build ./...`,
  `go vet ./...`, `go test -count=1 ./...` chạy sạch 100% trên toàn bộ code hiện có (kể cả V1-02/V1-02A/
  V1-03 vừa merge) — 0 lỗi. Từ V1-04 trở đi: build/vet/test cục bộ TRƯỚC khi push, chỉ push khi đã xanh
  cục bộ; CI vẫn là gate cuối cùng xác nhận chéo Windows/Linux + SPK-13 trước khi merge, nhưng vòng lặp
  sửa lỗi cú pháp/logic cơ bản không còn phải chờ CI nữa.
- Trước phát hiện này (V1-02, V1-02A, V1-03): quy trình là viết → tự trace tay → push → chờ CI thật →
  sửa theo log thật → lặp lại — đã bắt được nhiều bug thật theo cách đó (composite literal trong
  if-header, double-count depth qua reflect Interface, struct chứa map không so sánh được, SPK-10 flake
  không liên quan), giữ nguyên trong lịch sử bên dưới vì đều là bằng chứng thật đã xử lý.

## Quyết định/khuyến nghị đã áp dụng khi vướng

- **V1-02, lần push đầu tiên FAIL thật trên CI:** `internal/app/clock/clock_test.go:16` viết
  `if System{}.Now().IsZero() { ... }` — cú pháp Go không hợp lệ: composite literal bắt đầu bằng `{`
  ngay trong header `if` bị hiểu nhầm là dấu `{` mở block, trình biên dịch từ chối. Đây là lỗi thật do
  không có Go toolchain để compile tại chỗ trước khi push. Đã sửa bằng cách gán ra biến trước
  (`now := System{}.Now(); if now.IsZero() {...}`), push lại, đang chờ CI xác nhận lần 2.

- **V1-02, lần push thứ hai — phát hiện ngoài phạm vi task:** sau khi sửa lỗi cú pháp, `contract` xanh
  trên cả 2 OS (code V1-02 đúng), nhưng job `cross-platform semantic diff (SPK-13)` FAIL thật — SPK-10
  ("Optimistic concurrency của runtime state") cho kết quả khác nhau giữa Windows và Linux trong CÙNG
  một lần chạy: `assertions[1].detail: "version=2 state=RUNNING" != "version=2 state=CANCELLED"`, và
  `runtime/transitions.jsonl` khác cả hash lẫn size (81 vs 83 byte). Cả hai job `spike acceptance`
  (Windows, Linux) tự thân đều PASS độc lập — nghĩa là race CAS "đúng một transaction thắng" vẫn đúng
  trên từng platform riêng lẻ, chỉ có KẾT QUẢ AI THẮNG không được pin giống nhau giữa hai lần chạy độc
  lập trên hai platform khác nhau. PR V1-02 không đụng gì tới `internal/spikeacceptance`,
  `internal/adapters/sqlite` hay bất kỳ code nào liên quan SPK-10 — chỉ thêm 3 package mới độc lập
  (`clock`/`idsource`/`apperror`) và promote `google/uuid` lên direct dependency trong go.mod.
  **Khuyến nghị đã áp dụng:** đây rất có khả năng là non-determinism có sẵn từ trước trong chính kịch
  bản SPK-10 (không phải do V1-02 gây ra), lộ ra lần đầu ở lần chạy này. Đã trigger rerun toàn bộ
  workflow (không phải rerun để "lấy màu xanh" — mục đích là lấy thêm 1 data point độc lập để phân biệt
  flake ngẫu nhiên với regression thật). Nếu rerun cũng fail y hệt kiểu này → phải coi là regression
  thật, dừng merge, báo cáo riêng cho V0/SPK-10 (ngoài phạm vi V1-02). Nếu rerun xanh → coi V1-02 sạch,
  merge, nhưng vẫn ghi lại đây như một rủi ro cần theo dõi vì gate roadmap §6 yêu cầu full SPK-01…14 pass
  liên tục từ V1 trở đi.
  **Kết quả:** rerun toàn bộ workflow (cùng commit `f4cecbe`) → xanh cả 6 job, kể cả
  `cross-platform semantic diff (SPK-13)`. Kết luận: flake một lần, không phải regression từ V1-02.
  Đã spawn task riêng đề nghị điều tra non-determinism của SPK-10 (ngoài phạm vi V1-02, không tự sửa ở
  đây). V1-02 merge bình thường.

- **V1-02A, lần push đầu tiên FAIL thật:** `TestValue_WithinMaxDepthSucceeds` fail thật với lỗi
  "exceeds max depth" dù chỉ dựng 30 tầng lồng nhau (MaxDepth=32). Nguyên nhân: `map[string]any` khi
  `MapRange().Value()` trả về `reflect.Value` có `Kind()==Interface` (bọc giá trị thật bên trong); code
  cũ tăng `depth` cả lúc đệ quy vào giá trị Interface đó VÀ lúc unwrap nó — đếm depth gấp đôi thực tế
  mỗi tầng lồng thật. Đã sửa: tách case `Interface` (unwrap không tăng depth) khỏi case `Ptr` (dereference
  con trỏ thật vẫn tăng depth), tự trace lại chính xác cả hai test theo logic mới trước khi push.

- **V1-04, lỗi thật trên Windows CI (không phải bug tại chỗ, mà là lỗ hổng cấu hình repo có sẵn từ
  trước):** `contract (ubuntu-latest)` xanh nhưng `contract (windows-latest)` fail — checksum của
  `0001_initial_schema.sql` khác nhau giữa 2 OS (`36f787fb...` trên Windows runner vs `701c2afd...` như
  local/Linux). Nguyên nhân: repo chưa từng có `.gitattributes`, nên line-ending normalization khi
  checkout phụ thuộc `core.autocrlf` của từng máy/runner — windows-latest checkout ra CRLF cho file text
  mới, đổi hẳn byte content (và checksum) của riêng platform đó, trong khi local Windows của tôi (file
  được ghi trực tiếp bằng tool, chưa từng qua checkout thật) và ubuntu-latest vẫn giữ LF đúng như lúc
  extract. Đây là rủi ro có sẵn từ trước cho MỌI file cần checksum ổn định, V1-04 chỉ là task đầu tiên
  chạm phải nó. Đã sửa bằng `.gitattributes` (`*.sql text eol=lf`), verify bằng `git add --renormalize`
  không đổi byte nào của file đã commit — xác nhận đây thuần là bug ở thời điểm checkout, không phải ở
  nội dung đã lưu.

- **V1-04A, tự bắt được lỗi trước khi commit (không phải CI):** khi viết `internal/adapters/sqlite/db_test.go`
  cho test pragma-verification, tôi dùng tool Write mà KHÔNG kiểm tra file đã tồn tại — hoá ra file này
  đã có sẵn từ commit gốc `ced83ca` (2 test: `TestOpenMigratesAndEnablesForeignKeys`,
  `TestMigrationIsIdempotent`), và Write đã GHI ĐÈ mất cả hai. Phát hiện khi thấy `git status` báo "M"
  (modified) thay vì "??" (untracked) sau khi merge V1-04 sang master — lẽ ra phải là file mới hoàn toàn
  nếu tôi tạo đúng. Đã khôi phục bằng `git show HEAD:...` lấy lại nội dung gốc, ghép với 3 test mới của
  tôi, verify `git diff --stat` chỉ còn thêm dòng (0 xoá), chạy lại toàn bộ test package — 38/38 pass kể
  cả 2 test được khôi phục. Bài học: phải kiểm tra file tồn tại (Read/Glob) trước khi Write, kể cả khi
  "chắc chắn" đây là file mới do task tạo ra.

- **V1-05, quyết định phạm vi quan trọng cần bạn xem lại:** task gốc nói "Tx exposes
  catalog/work/definitions/runtime/jobs/events/receipts". `WorkflowPersistence` hiện tại (8 method,
  internal/app/ports/persistence.go) là chính "Store tổng hợp" mà task này nhắm thay thế theo Mục tiêu
  ("thay WorkflowPersistence spike bằng UoW/repository nhỏ"). Nhưng TẤT CẢ các method hiện có
  (WorkflowPersistence lẫn ~25 method khác trên `sqlite.Store`) đều tự quản lý transaction riêng
  (BeginTx...Commit nội bộ mỗi lần gọi) — gọi chúng từ TRONG một closure UnitOfWork chính là
  "nested transaction" mà task tự cấm ("không nested transaction... trong UoW"). Nghĩa là muốn có method
  thật cho một concern, phải viết lại logic mới hoạt động trên `*sql.Tx` dùng chung — không thể chỉ bọc
  method cũ.
  **Quyết định đã áp dụng:** xây đầy đủ khung UnitOfWork/Tx với cả 7 accessor (đúng tên, đúng shape),
  adapter sqlite THẬT bọc RunSerializedWrite/RunReadOnly của V1-04A (test bằng SQLite thật, xác nhận
  commit/rollback đúng), fake in-memory cho app-layer test — nhưng CẢ 7 repository interface hiện chỉ là
  interface rỗng/tối giản, CHƯA port method thật nào. Lý do: viết lại 868 dòng logic của
  `workflow_store.go` (và các file khác) thành bản `*sql.Tx`-composable ngay bây giờ, khi chưa có caller
  thật nào cần, là rủi ro thật (đã bắt được 2 bug thật trong code ĐƠN GIẢN hơn nhiều ở các task trước) và
  đi ngược nguyên tắc `00-roadmap.md` §3 ("Không chia chỉ để tạo file/field nếu phần đó chưa có contract
  test hoặc behavior quan sát được"). `sqlite.Store`/`WorkflowPersistence` cũ giữ nguyên, tiếp tục phục
  vụ bộ regression V0 không đổi. Mỗi interface rỗng có comment ghi rõ task nào sẽ điền method thật
  (Catalog→V3-01, Work→V3-04/06, Definitions→V2, Runtime→V4, Receipts→V1-06 — task NGAY SAU đây).
  **Nếu bạn muốn tôi port đầy đủ ngay (rủi ro cao hơn, cần refactor code V0 đã qua CI), hãy yêu cầu sửa
  lại task này.**

- **V1-06, thiết kế `CommandScope` để hai điều kiện reject của Verify tự động đúng bằng type system,
  không cần validate runtime riêng:** yêu cầu "command project-scoped thiếu ProjectID và command
  installation-scoped mang ProjectID đều bị reject" được thoả bằng constructor: `ProjectScope("")`
  panic ngay tại chỗ gọi (bug tạo lệnh sai, không phải điều kiện runtime cần xử lý), còn
  `InstallationScope()` không nhận tham số nào nên KHÔNG THỂ mang ProjectID — trạng thái bất hợp lệ
  không biểu diễn được (illegal state unrepresentable), nên không cần một hàm validate riêng và cũng
  không có nhánh test nào "installation scope mang ProjectID" có thể viết được (code không compile).
  Test `TestProjectScope_EmptyProjectID_Panics` xác nhận vế còn lại.
- **V1-06, `command_receipts` đổi cột `project_id NOT NULL` → `scope_key NOT NULL` (migration 0002,
  DROP+CREATE):** bảng gốc (0001) chỉ có khái niệm project-scoped, chưa có installation-scoped. Thêm
  installation-scoped mà vẫn giữ `project_id` sẽ phải làm cột nullable — đúng thứ GC-INV-35 cấm (SQLite
  coi mọi NULL trong unique index là khác nhau, hai lệnh installation-scoped trùng idempotency key sẽ
  lọt qua ràng buộc unique). Bảng đang trống (chưa có caller thật nào ghi vào), nên DROP+CREATE an toàn,
  không mất dữ liệu thật.
- **V1-06, `receiptsRepository.Record` dùng `ON CONFLICT DO NOTHING` + kiểm `RowsAffected` thay vì parse
  message lỗi SQLite để phát hiện duplicate key:** giữ đúng nguyên tắc V1-02 ("bỏ parse message SQL khỏi
  application decisions"). Khi `RowsAffected == 0` (đã có row), `Load` lại để so `RequestHash`: giống hệt
  → coi là replay hợp lệ của đúng request đó (trả nil, không lỗi) — đây CHÍNH LÀ nhánh xử lý race hai
  connection cùng ghi một receipt giống hệt nhau (test
  `TestReceiptsRepository_ConcurrentDuplicateInstallationCommand_OnlyOneReceiptStored`, 2 connection thật
  chạy song song); khác `RequestHash` → `ports.ErrReceiptConflict`.
- **V1-06, package `fake` đổi `Tx.events`/`Tx.receipts` từ `struct{}` placeholder sang con trỏ
  `*EventsRepository`/`*ReceiptsRepository` có state thật:** vì method thật cần side-effect (append vào
  slice, ghi vào map) phải thấy được ở caller sau khi `fn` return, mà `Tx` là value type truyền vào `fn`
  bằng value — nếu field vẫn là value (không phải con trỏ) thì mọi mutation bên trong `fn` sẽ mất khi
  `fn` return, không bao giờ commit được vào `Snapshot`. Đổi sang con trỏ kèm `clone()` sâu (copy slice/
  map, không chia sẻ backing array) trước mỗi lần `WithSerializedWrite`/`WithReadOnly` để giữ đúng ngữ
  nghĩa "attempt thất bại không được làm bẩn Snapshot đã commit" — verify bằng 2 test
  `TestFake*Repository_FailedAttempt_DoesNotPersist`.
- **V1-06, handler mẫu (`handleCreateProject`, trong `command_handler_example_test.go`) chỉ để minh hoạ
  V1-06's "Hoàn thành khi", không phải API thật:** dùng bảng `projects` có sẵn từ migration 0001 làm
  "state" thay vì tạo bảng demo riêng, vì `Catalog`/`Definitions`/... vẫn là interface rỗng (quyết định
  V1-05) nên chưa có repository thật nào để gọi — handler truy cập `*sql.Tx` thô qua type-assert
  `tx.(*txAdapter)`, đúng pattern các test UnitOfWork hiện có đã dùng. Không định port thành API thật ở
  đây; task xây API/handler thật cho Catalog thuộc V3-01.
- **V1-06, sửa 2 test hiện có (`db_test.go`, `unitofwork_test.go`) từ "migration count = 1" thành "= 2":**
  hệ quả tất yếu của việc thêm migration 0002 — không phải bug, chỉ là assertion cũ giờ sai vì có thêm 1
  migration thật.

- **V1-07, lỗi thật tự bắt được trước khi push — phá cả bộ regression V0 (nghiêm trọng nhất phiên này):**
  sau khi thêm cột `journal_position NOT NULL` vào `domain_events` (migration 0003), chạy `go test ./...`
  local FAIL 8 test — không phải test tôi viết, mà là các test regression V0 thật:
  `TestSPK04FaultAfter*`, `TestSPK03HardCrashJoinsCheckpointContextRecovery`,
  `TestCrashRestartReclaimsLeasedJobAndPreservesPinnedWorkflow`,
  `TestSPK09QuarantineRecreateFencesStaleGeneration`, v.v. — tất cả lỗi
  `NOT NULL constraint failed: domain_events.journal_position`. Nguyên nhân: có 4 nơi khác trong code V0
  spike (`attempt_store.go`, `node_dispatch.go` — 2 chỗ, `workspace_lifecycle.go`, `workflow_store.go`)
  tự viết `INSERT INTO domain_events` bằng raw SQL riêng, KHÔNG đi qua `eventsRepository.Append` mới của
  V1-06/V1-07 — tôi đã quên hẳn những nơi này khi thiết kế migration 0003 (chỉ nhớ tới attempt vì tài
  liệu comment cũ trong `unitofwork.go` từng nhắc tên 4 file này, nhưng tôi không grep lại trước khi
  DROP+CREATE `domain_events` với cột NOT NULL mới). Đây CHÍNH LÀ lý do `00-roadmap.md` §5B bắt buộc giữ
  `cmd/agentkit-spike` build/test không đổi làm "live regression gate" xuyên suốt V1 — nếu tôi chỉ chạy
  test package `sqlite` hẹp (như thói quen ở V1-06) thay vì `go test ./...` toàn repo trước khi push, lỗi
  này sẽ lọt thẳng lên CI thật.
  **Đã sửa:** viết 1 helper dùng chung `allocateJournalPosition(ctx, tx)` (trong `domain_events.go`), gọi
  nó từ CẢ 5 nơi ghi domain_events (4 nơi V0 cũ + `eventsRepository.Append` mới) để `journal_position` là
  một sequence toàn cục duy nhất bất kể aggregate/call site. Chỉ thêm `journal_position` cho 4 writer cũ,
  KHÔNG thêm outbox row cho chúng — quyết định phạm vi có chủ đích, xem mục riêng bên dưới. Chạy lại
  `go test ./...` toàn repo → xanh 100% (kể cả 8 test vừa fail).
- **V1-07, quyết định phạm vi: 4 domain_events writer cũ của V0 KHÔNG được thêm outbox row, chỉ mới
  (`eventsRepository.Append`) mới tự động thêm:** GC-INV-16 yêu cầu "transition tạo side effect phải
  enqueue durable job/outbox" — chữ "hoặc" cho phép một trong hai cơ chế. 4 writer V0 cũ đã enqueue qua
  `durable_jobs` từ trước (cơ chế đã có, đã qua CI, đây là lý do GC-INV-16 vốn đã được thoả cho code V0),
  nên không bắt buộc phải thêm outbox nữa — retrofit outbox vào 4 file V0 đã test kỹ là rủi ro không cần
  thiết cho phạm vi V1-07 (nguyên tắc "không sửa code đã chạy tốt nếu không có lý do bắt buộc"). outbox
  là cơ chế durable-delivery MỚI cho nhánh `ports.EventsRepository` (V1-06 trở đi); mọi domain event ghi
  qua nhánh đó LUÔN có đúng 1 outbox row tương ứng — cứng bằng code, không phải quy ước caller phải nhớ
  gọi thêm.
- **V1-07, `outbox` dispatcher (`ClaimNextOutboxMessage`/`MarkOutboxMessageDispatched`/
  `RecoverExpiredOutboxLeases`) cố tình mô phỏng CHÍNH XÁC pattern CAS đã có của `durable_jobs`
  (`ClaimJob`/`CompleteJob`/`RecoverExpiredJobs` trong `scheduling.go`)** — cùng kiểu
  `WITH candidate AS (...) UPDATE ... WHERE id = (SELECT ...) AND status = 'AVAILABLE' RETURNING ...`,
  cùng `lease_token` là integer tăng dần làm fencing token — thay vì tự nghĩ ra cơ chế mới, để giữ nhất
  quán với idiom đã được review/qua CI trong chính codebase này.
  **"Idempotent dispatcher cursor"** của V1-07 áp dụng đúng cơ chế status-based (AVAILABLE/LEASED/
  DISPATCHED) này: không có bảng cursor toàn cục riêng — vị trí "đã dispatch tới đâu" chính là tập hợp
  row còn AVAILABLE/LEASED so với đã DISPATCHED. Một dispatcher restart sau crash chỉ cần
  `RecoverExpiredOutboxLeases` rồi `ClaimNextOutboxMessage` lại — không cần đọc/ghi một cursor riêng.
  Test `TestDispatchNext_CrashAfterCommitBeforeDispatch_StillDelivers` và
  `TestDispatchNext_DuplicateDeliveryDoesNotCreateTwoLogicalEffects` (trong
  `internal/app/outbox/dispatcher_test.go`) verify đúng 2 kịch bản Verify của task: message không bị mất
  khi dispatcher chưa kịp chạy, và redelivery sau lease-expiry không tạo 2 hiệu ứng logic (Sink tự
  idempotent theo EventID — Dispatcher chỉ đảm bảo at-least-once, không exactly-once, đúng như comment
  trong `ports.OutboxSink`).
- **V1-07, `Topic`/`CausationID` trên `ports.DomainEvent` là optional, có default, không phải field bắt
  buộc:** để không phải sửa lại các struct literal `DomainEvent{...}` đã có từ V1-06 (test cũ, handler
  mẫu) — thêm field optional vào một struct dùng named field literal là thay đổi hoàn toàn tương thích
  ngược trong Go, không cần touch code cũ. `Topic` rỗng mặc định = `EventType`; `CausationID` rỗng lưu
  NULL. Nếu sau này có caller thật cần bắt buộc set Topic/CausationID, có thể siết lại validate ở
  `Append` — chưa làm vì chưa có nhu cầu quan sát được (đúng nguyên tắc `00-roadmap.md` §3).
- **V1-07, payload size limit 256 KiB (`maxDomainEventPayloadBytes`) là số tự chọn, không có trong tài
  liệu nguồn:** ADR/GC-INV không ghi con số cụ thể, chỉ nói "payload size... limits". Chọn 256 KiB vì đủ
  lớn cho một JSON quyết định/diff nhỏ nhưng đủ nhỏ để chặn ai đó nhét cả file/artifact vào event — đúng
  tinh thần "domain_events/outbox không phải chỗ chứa artifact body" đã ghi trong go-core-spec (artifact
  lớn có locator riêng qua V1-08). Có thể yêu cầu đổi số nếu con số này không phù hợp.

- **V1-07, flake thật trên CI (không phải do diff của task này) — `TestSPK09QuarantineRecreateFencesStaleGeneration`:**
  job `contract (windows-latest)` FAIL ở bước "Stress write-lease race path (Windows, V0-11A diagnostic)"
  (chạy test này 30 lần liên tục, TTL 300ms) — lỗi ngay ở lệnh `AcquireWriteLeases` ĐẦU TIÊN của W1, trước
  khi bất kỳ code nào tôi sửa trong PR này (domain_events/outbox) được thực thi trong luồng test đó.
  `contract (ubuntu-latest)` xanh; các job sau bị skip do phụ thuộc. PR V1-07 không đụng tới
  `ClaimJob`/`AcquireWriteLeases` (chỉ sửa domain_events writer + thêm outbox). **Khuyến nghị đã áp dụng:**
  trigger rerun đúng commit đó (cùng lý do như flake SPK-10 ở V1-02 — lấy thêm 1 data point độc lập,
  không phải "rerun để lấy xanh"). Rerun → xanh cả 6 job. Kết luận: flake timing thật trên Windows runner
  (comment sẵn có trong chính test này đã ghi "300ms... to keep this deterministic on a busy Windows
  runner" — nghĩa là class flake này từng bị đụng tới trước đây). Đã spawn task riêng điều tra (task_id
  `task_16a782e5`), không tự sửa test ở đây vì ngoài phạm vi V1-07 và cần đánh giá kỹ hơn liệu 300ms có
  đủ margin hay cần cơ chế robust hơn (đo elapsed time, retry thay vì hard-fail) — không muốn làm yếu test
  chỉ để "cho qua". V1-07 merge bình thường sau rerun xanh.

- **V1-07A, quyết định phạm vi: `EnforcingEventsRepository` là decorator rời, KHÔNG tự động wire vào
  `sqlite.NewUnitOfWork`/`fake.New`:** task nói "từ chối emit event chưa đăng ký ngay tại boundary
  append" — cân nhắc giữa (a) sửa signature `NewUnitOfWork(store, registry)`/`fake.New(registry)` để bắt
  buộc MỌI Tx.Events() đều qua registry, hay (b) một decorator `ports.EventsRepository` độc lập mà caller
  tự bọc quanh Inner khi cần. Chọn (b) vì: chưa có caller thật nào (chỉ test/handler minh hoạ) cần enforce
  ngay bây giờ; sửa signature ảnh hưởng ≥4 file gọi `NewUnitOfWork`/`fake.New` chỉ để ép mọi test (kể cả
  test không liên quan schema) phải tạo registry; và đúng tinh thần `00-roadmap.md` §3 (không chia chỉ để
  tạo field/thay signature khi chưa có contract test/behavior quan sát được đòi hỏi). "Boundary" ở đây là
  điểm caller chọn bọc `Inner` bằng `EnforcingEventsRepository{Inner, Registry}` — decorator tự nó CHƯA
  wire vào production path nào; khi V2+ có handler thật cần enforce, chỉ cần bọc tại nơi đó, không phải
  sửa lại V1-05..V1-07's plumbing. **Nếu bạn muốn ép cứng ngay từ bây giờ (mọi Tx.Events() luôn qua
  registry, kể cả khi chưa có caller thật), hãy yêu cầu sửa lại task này** — cùng dạng quyết định như
  V1-05's UnitOfWork repos.
- **V1-07A, tự verify Verify requirement "test fail khi decoder đang được fixture tham chiếu bị xóa"
  bằng cách thực sự tạo ra tình huống đó:** không chỉ tin vào code/comment — đã tạm xoá dòng
  `r.Register("ProjectCreated", 1, decodeProjectCreatedV1)` khỏi `newExampleRegistry()`, chạy lại
  `TestGoldenFixtures_DecodeEveryRegisteredVersion`, xác nhận FAIL đúng với thông báo
  "event type/schema version is not registered: ProjectCreated v1", rồi khôi phục lại dòng đó và xác nhận
  xanh trở lại. Đây là cách duy nhất chắc chắn task's Verify bullet thật sự được thoả, không phải suy diễn
  từ code.
- **V1-07A, `EnforcingEventsRepository.Append` dùng `apperror.Wrap(CodeInvalidArgument, ..., ErrNotRegistered)`
  thay vì trả thẳng `ErrNotRegistered`:** giữ nhất quán với cách `domain_events.go`'s payload-size-limit
  check đã làm ở V1-07 — mọi lỗi từ application layer trả về `*apperror.Error` có Code rõ ràng; `errors.Is`
  với `ErrNotRegistered` vẫn hoạt động qua `Unwrap()` (đã verify bằng test `TestRegistry_Decode_NotRegistered_ReturnsErrNotRegistered`
  và `apperror.CodeOf` trong `enforce_test.go`).

- **V1-08, `ArtifactRef.SHA256` trùng giá trị với `ArtifactRef.Locator` trong implementation này — chủ ý,
  không phải lỗi:** interface `ArtifactStore` chỉ cho `Put` trả về đúng 1 kiểu (`ArtifactRef`), nên mọi
  thông tin caller cần (locator để gọi lại Open/Verify, cộng size/hash/content-type để dùng ngay không
  cần round-trip) đều phải nằm chung 1 struct. `Locator` được tài liệu hoá là "opaque — không được parse",
  còn `SHA256` là field tiện ích tường minh — implementation filesystem này tình cờ dùng cùng format
  `"sha256:<hex>"` cho cả hai, nhưng một implementation khác (S3/Postgres-backed cho beta) có thể dùng
  locator scheme khác mà vẫn giữ đúng field SHA256/Size/ContentType/Sensitivity/Redacted.
  `ArtifactMetadata.Redacted` cũng được echo lại vào `ArtifactRef.Redacted` cho đủ — bản thân ArtifactStore
  không tự redact nội dung (đúng HE-11-S06: DB giữ metadata, ArtifactStore chỉ giữ content); caller vẫn là
  nơi chịu trách nhiệm gọi `redact` package trước khi Put nếu cần.
- **V1-08, "duplicate content" xử lý bằng cách bỏ temp file, KHÔNG rewrite/verify lại file đã có sẵn tại
  content address:** vì store là single-writer-per-object-theo-hash (chỉ `Put` ghi, không method nào sửa
  object đã finalize), một file đã tồn tại đúng tại path theo hash của nó chắc chắn đã đúng nội dung đó —
  không cần hash lại. Race 2 `Put` cùng nội dung chạy đồng thời vẫn an toàn: `os.Rename` cả 2 platform
  (POSIX rename, Windows `MoveFileEx`+`MOVEFILE_REPLACE_EXISTING` mà Go dùng) đều cho phép ghi đè đích đã
  tồn tại — và vì nội dung giống hệt nhau (cùng hash), ai "thắng" cũng ra kết quả đúng, không cần lock
  riêng.
- **V1-08, traversal defense bằng regex format-validation (`^sha256:[0-9a-f]{64}$`) trên MỌI Locator nhận
  vào Open/Verify, kể cả locator do chính Put trả về:** vì `ArtifactRef` là struct Go thường (không có cơ
  chế "opaque" thật ở compile-time), một caller vẫn có thể tự tạo `ArtifactRef{Locator: "../../etc/passwd"}`
  bằng tay và gọi Open — test `TestOpen_MalformedLocator_RejectedBeforeTouchingFilesystem` verify 8 dạng
  locator hỏng (traversal, non-hex, sai độ dài, sai case prefix, rỗng, có `/` cuối) đều bị reject bằng
  `apperror.CodeInvalidArgument` TRƯỚC khi bất kỳ path filesystem nào được dựng — không phải "dựng path
  rồi kiểm tra nằm trong root" (`ensureWithin` kiểu package `evidence`), vì với locator content-addressed,
  validate format là đủ mạnh và đơn giản hơn.
- **V1-08, "verify-open" implement bằng cách Verify() hash lại TOÀN BỘ file trước khi Open() mở file lần
  2 để trả reader — chấp nhận đọc đĩa 2 lần thay vì 1 lần streaming-verify:** đơn giản hơn nhiều để làm
  đúng (không có edge case về "đọc dở thì sao", "Close() có cần verify nốt phần chưa đọc không") so với
  một reader tự tính hash khi caller Read() và verify ở EOF. V1 alpha chưa có yêu cầu hiệu năng cụ thể cho
  artifact lớn — "Hoàn thành khi" chỉ nói "không cần inline SQLite", không nói gì về throughput. Nếu sau
  này cần streaming-verify cho artifact rất lớn (double I/O quá đắt), có thể đổi implementation mà không
  đổi interface `ArtifactStore`.
- **V1-08, KHÔNG xây cơ chế dọn temp file mồ côi (orphaned tmp sau crash thật, khác với lỗi trả về bình
  thường mà defer đã dọn được):** Verify requirement của task chỉ nói "interrupted write" theo nghĩa Put
  trả lỗi thì không để lại artifact — đã test đúng bằng `failingReader`. Một crash THẬT của tiến trình
  (kill -9 giữa chừng) sẽ không chạy được defer, để lại 1 file trong `tmp/` — nhưng file đó KHÔNG BAO GIỜ
  hiện diện tại content-addressed path (chỉ rename mới làm nó "thật"), nên không ảnh hưởng tính đúng đắn,
  chỉ tốn dung lượng đĩa. Dọn dẹp định kỳ (giống `evidence.PruneExpired`) có thể thêm sau nếu cần, ngoài
  phạm vi V1-08.

- **V1-09, tự bắt được 1 test viết sai do hiểu nhầm contract của `redact.Matcher` (không phải bug ở
  logger hay ở redactor):** viết test mong đợi secret nhúng GIỮA một message tự do (`"leaked " + secret +
  " in message"`) phải bị redact — chạy local FAIL thật, message trong output vẫn chứa nguyên secret. Đọc
  lại doc comment gốc của `redact.Matcher` (V1-02A): "Matching is always exact equality, never a
  pattern/regex" — đây là quyết định thiết kế đã được xác nhận (confirmed) từ trước, không phải thiếu sót.
  `Matcher.Value`/`Matcher.String` chỉ redact khi TOÀN BỘ string bằng đúng secret, không quét substring
  trong văn bản tự do — nhúng secret vào giữa câu prose nằm ngoài khả năng (và ngoài chủ đích) của
  redactor. Đã sửa: xoá test sai, thay bằng test đúng contract (message CHÍNH XÁC BẰNG secret thì bị
  redact), thêm doc comment ngay trong `logger.go` giải thích rõ giới hạn này cho người dùng package sau
  này — không đụng gì tới `redact` package (đã đúng, đã được xác nhận từ V1-02A).
- **V1-09, `Logger.Error` cố tình KHÔNG nhận tham số `error`, chỉ nhận `apperror.Code` + `message string`
  caller tự viết:** đây là cách "ép cứng bằng type system" giống `CommandScope`/`ProjectScope` ở V1-06 —
  thay vì tin caller "nhớ" chỉ log phần safe của lỗi (dễ quên, dễ lỡ tay gọi `err.Error()` của một lỗi
  chưa qua apperror mà lộ chi tiết nội bộ), signature của `Error()` đơn giản KHÔNG CÓ chỗ để nhét cause
  thô vào — muốn log cause thô, không method nào trong package này cho phép. Thoả đúng "Hoàn thành khi:
  error safe/public tách raw private cause" bằng thiết kế, không bằng kỷ luật/quy ước.
- **V1-09, "bounded values" áp dụng cho field value VÀ message, sau khi redact chứ không phải trước:**
  redact trước rồi mới cắt bớt (bound) — nếu cắt trước, một secret dài có thể bị cắt còn 1 phần và không
  còn match exact-equality nữa, lọt qua Matcher mà vẫn hiện 1 phần secret trong output. Thứ tự
  redact→bound đảm bảo Matcher luôn thấy đúng giá trị gốc trước khi bất kỳ phần nào của nó bị cắt.

- **V1-10, bug logic thật tự bắt được qua test tự viết (không phải CI, không phải review sau) —
  "shutdown grace" không thật sự có hiệu lực:** thiết kế ban đầu derive `jobCtx` (context truyền cho
  Handler và vòng lặp heartbeat) làm CON của `poolCtx`, mà `poolCtx` lại là con của `ctx` (context caller
  truyền vào `Run`). Hệ quả: `ctx.Done()` fire → TỰ ĐỘNG cancel `poolCtx` → TỰ ĐỘNG cancel `jobCtx` ngay
  lập tức, bất kể lúc nào mình gọi `cancelPool()` thủ công — nghĩa là handler đang chạy bị cancel NGAY khi
  shutdown bắt đầu, "grace period" hoàn toàn không có tác dụng (dù code trông như có `select` chờ
  `ShutdownGrace` trước khi escalate). Test `TestPool_ShutdownGraceExceeded_ReturnsErrAndEscalatesCancellation`
  FAIL thật khi chạy local: `Run` trả về `nil` gần như ngay lập tức thay vì đợi 150ms rồi trả
  `ErrShutdownGraceExceeded`. **Nguyên nhân gốc:** context trong Go, con LUÔN bị cancel khi cha bị cancel,
  không có cách "trì hoãn" việc đó — muốn một context sống lâu hơn tín hiệu shutdown, nó phải KHÔNG phải
  con của context mang tín hiệu đó. **Đã sửa:** tách thành 2 cây context độc lập — `ctx` (caller) chỉ điều
  khiển "còn được claim job mới không" (vòng lặp worker check trực tiếp `ctx`), còn `jobsCtx` là context
  GỐC RIÊNG (`context.WithCancel(context.Background())`) mà `Run` tự sở hữu, chỉ bị cancel bởi chính
  `Run` khi hết grace period — không bao giờ bị cancel tự động chỉ vì `ctx` bị cancel. `CompleteJob`/
  `HeartbeatJob` vẫn dùng `context.WithoutCancel(...)` như thiết kế ban đầu (đúng, không đổi) để các lệnh
  DB này không bị cắt ngang bởi tín hiệu shutdown. Đây là bài học quan trọng về context composition trong
  Go: "context cha-con" không phải công cụ đúng cho "hai deadline độc lập, một cái trễ hơn cái kia" — cần
  hai root context tách biệt.
- **V1-10, không có `FailJob`/method nào đánh dấu job FAILED tường minh — dùng lại đúng cơ chế
  lease-expiry đã có sẵn từ V0:** kiểm tra `ports.JobQueue` thấy chỉ có `CompleteJob` (SUCCEEDED), không
  có method fail tường minh nào — `JobState` có FAILED/DEAD nhưng chỉ đạt tới qua `RecoverExpiredJobs`
  (lease hết hạn, retry tới khi hết `MaxClaims` thì DEAD). Quyết định: khi Handler trả lỗi HOẶC panic HOẶC
  không có handler đăng ký cho Kind đó, Pool đơn giản KHÔNG gọi CompleteJob — để lease tự hết hạn, đúng
  con đường sẵn có, không tự chế thêm state machine mới song song với cái đã có và đã qua CI.
- **V1-10, KHÔNG wire `serve`/`worker` CLI stub (từ V1-01) vào `workerpool.Pool` thật ở task này:** Mục
  tiêu của task chỉ nói "serve và worker DÙNG CÙNG protocol" (mô tả đích thiết kế), còn "Thực hiện" liệt
  kê thuần cơ chế pool (claim/heartbeat/shutdown/registry) — không nhắc CLI wiring, HTTP loopback API
  (đó là V1-11's "loopback API config"). Xây `workerpool` package độc lập, test đầy đủ qua `ports.JobQueue`
  thật (SQLite), để task nào wire CLI thật sau này (chưa thấy trong V1-00A..V1-12) có sẵn khối lắp ráp
  đúng, đã test kỹ — giống cách V1-05 để 7 repository interface trống chờ task sau điền.

- **V1-10, CI lần push đầu tiên FAIL thật — 2 job fail, cả hai đều đã xác nhận KHÔNG phải do code sản
  phẩm (`pool.go`) sai:**
  - `Linux race and stability (V0-12)` (chạy `go test` lặp 10 lần trên Linux): `TestPool_HeartbeatKeepsLongRunningJobAlive`
    FAIL ở lần chạy 6 và 9 trên 10. Đọc kỹ lại chính test mình viết: vòng lặp chờ có gọi
    `store.ClaimJob(ctx, "prober", 10ms)` để "thăm dò" xem job còn tồn tại không — nhưng lệnh này TỰ NÓ
    tranh giành claim với chính worker thật của pool đang chạy, và nếu "prober" thắng trước worker thật
    (dễ xảy ra hơn khi máy CI bận, lịch goroutine chậm), job bị cướp mất, `invocations` không bao giờ lên
    1 trong deadline. Đây là bug trong TEST, không phải trong `pool.go`. Đã sửa: bỏ hẳn "prober", thay
    bằng chờ handler tự báo hoàn thành qua `atomic.Bool` — loại bỏ hoàn toàn race tự gây ra. Verify lại
    bằng cách chạy `go test ./internal/app/workerpool/...` 5 lần liên tục local, không lần nào fail.
  - `cross-platform semantic diff (SPK-13)`: log thật cho thấy `SPK-10 diffs=3` — CHÍNH XÁC cùng dạng lỗi
    đã ghi nhận ở V1-02 (`assertions[1].detail: "version=2 state=CANCELLED" != "version=2 state=RUNNING"`,
    `runtime/transitions.jsonl` khác hash/size 83 vs 81 byte — same shape, chỉ đổi chiều 81/83). PR V1-10
    không đụng bất kỳ file nào liên quan `workflow_runs`/SPK-10/cancellation racing (chỉ thêm package
    `internal/app/workerpool` hoàn toàn mới). Đây là flake THẬT đã biết, đã spawn task điều tra riêng từ
    V1-02 (`task_d9ce5cac`, vẫn pending) — không phải do V1-10 gây ra, không tự sửa ở đây (ngoài phạm vi).
  **Hành động:** sửa bug test #1, push commit thứ 2 lên CÙNG branch/PR #16 (không tạo PR mới), chờ CI
  chạy lại — nếu SPK-13 vẫn fail do đúng flake SPK-10 (không phải regression mới), sẽ trigger rerun như
  đã làm ở V1-02 để lấy thêm data point trước khi merge.

- **V1-11, tự bắt được vi phạm kiến trúc thật qua `go test ./...` local (không phải CI) — `doctor`
  import thẳng `internal/adapters/sqlite`:** thiết kế ban đầu của `CheckDatabase` gọi thẳng
  `sqlite.Open(ctx, databasePath)` để vừa mở vừa migrate DB — biên dịch sạch, nhưng
  `internal/archtest.TestDomainAppNeverImportAdapters` (chính test đã đóng V0-13, giữ nguyên tắc
  "domain/app không bao giờ phụ thuộc adapter") FAIL thật: `internal/app/doctor depends on adapter
  package .../adapters/sqlite`. Đây CHÍNH LÀ điều V1-12's "Hoàn thành khi: no adapter import trong
  domain/app" sẽ khoá cứng — nếu bug này lọt qua V1-11 mà không bị bắt, V1-12 chắc chắn sẽ fail vì lý do
  này. **Đã sửa:** đổi `CheckDatabase` nhận `ports.QueryStore` (interface đã có sẵn từ V1-05, chỉ có
  `Ping(ctx) error`) thay vì tự mở DB — đúng nguyên tắc "mở+migrate DB là việc của code khởi động
  (adapter-aware), Doctor chỉ hỏi 'connection hiện có còn sống không'" — tách biệt readiness-check khỏi
  cold-start. Test của `doctor` package (file `_test.go`, external test package `doctor_test`) VẪN được
  phép import `internal/adapters/sqlite` trực tiếp — `archtest`'s `go list` (không có cờ `-test`) chỉ xét
  import của package thật, không xét file test — đã verify điều này đúng qua tiền lệ `internal/app/outbox/
  dispatcher_test.go` ở V1-07 (cũng import `sqlite` trong test, chưa từng bị archtest chặn).
- **V1-11, `CheckGit`/`CheckRoot` gọi thẳng `os/exec`/`os` (stdlib), KHÔNG qua `ports.ProcessSupervisor`:**
  cân nhắc kỹ vì go-core-spec có port `ProcessSupervisor` riêng cho việc spawn process — nhưng
  `archtest.TestDomainAppNeverImportAdapters` chỉ chặn import `internal/adapters/...` của chính repo này,
  không chặn stdlib `os`/`os/exec`. `ProcessSupervisor` được thiết kế cho DISPATCH công việc thật (argv,
  env allow-list, cancellation policy đầy đủ) — một probe đọc-only, không side effect, không cần cách ly
  workspace (`git --version`, `os.Stat`) là use case khác hẳn, và bản thân Doctor tồn tại ĐỂ quan sát môi
  trường thật bên ngoài ranh giới ứng dụng (đó là lý do nó tồn tại). Giữ nguyên stdlib trực tiếp, không tự
  chế thêm abstraction không cần thiết.
- **V1-11, "loopback API config" trong Thực hiện KHÔNG có check tương ứng:** `internal/app/config.Config`
  (V1-03) chưa có field nào cho loopback/HTTP API (chỉ có DatabasePath/ArtifactRoot/WorkerID/
  WorkerConcurrency/LeaseTTL/LeaseHeartbeat/ProcessOutputLimit/ProviderExecutables) — phần này thuộc phạm
  vi version sau (HTTP/loopback API chưa được task nào trong V1 xây). Không tự bịa field/check giả cho
  cấu hình chưa tồn tại; khi task nào thêm loopback API config thật, `doctor` có thể thêm 1 check mới mà
  không đổi gì kiến trúc hiện tại.
- **V1-11, golden JSON test dùng kỹ thuật "normalize trước khi marshal" thay vì so khớp byte thô:** output
  thật của `Run` luôn chứa path tempdir (đổi mỗi lần chạy test) và chuỗi `git --version` (đổi theo máy) —
  không thể golden-match byte-for-byte trực tiếp. Thử lần đầu thay chuỗi trên JSON đã mã hoá (encoded text)
  bị lỗi thật: path Windows có `\`, JSON encode thành `\\`, chuỗi thay thế (chứa `\` gốc) không bao giờ
  khớp được `\\` đã encode — sửa bằng cách thay thế trên GIÁ TRỊ FIELD THÔ (`Detail`/`Remediation`) TRƯỚC
  khi gọi `json.MarshalIndent`, không phải trên text JSON sau khi encode.

- **V1-11, CI lần push đầu tiên FAIL thật trên Windows — cùng root cause với bug `.sql` ở V1-04:**
  `contract (windows-latest)` FAIL cả 3 golden test (`TestRun_Golden_Healthy/Degraded/Blocked`),
  `contract (ubuntu-latest)` xanh. Đọc log thật: nội dung "got" và "want" nhìn GIỐNG HỆT NHAU khi in ra
  (từng dòng khớp), nhưng test vẫn báo mismatch — dấu hiệu kinh điển của lệch line-ending: `got` do
  `json.MarshalIndent` sinh ra luôn LF thuần, còn `want` đọc thẳng từ file golden trên đĩa — và
  `.gitattributes` lúc đó CHỈ có rule cho `*.sql`, không có cho `*.json`, nên Windows Actions runner
  checkout normalize file JSON đã commit (LF) thành CRLF, trong khi Linux giữ nguyên LF. Xác nhận bằng
  `git add --renormalize .`: KHÔNG có byte nào của 3 file JSON đã commit cần đổi (đúng như dự đoán — bug
  hoàn toàn nằm ở thời điểm checkout, không phải nội dung đã lưu). **Đã sửa:** thêm rule `*.json text
  eol=lf` vào `.gitattributes` (đặt cạnh rule `*.sql` đã có, cùng comment giải thích chung gốc). Push
  commit thứ 2 lên cùng branch/PR #17, chờ CI xác nhận.

- **V1-12, `internal/integration` package đặt NGOÀI `internal/domain/...`/`internal/app/...` có chủ đích:**
  test end-to-end thật sự cần import cả `internal/adapters/sqlite`, `internal/adapters/artifactstore` LẪN
  `internal/app/...` cùng lúc — đúng bản chất của một integration test (nối adapter thật với app-layer
  thật, giống cách một binary `serve`/`worker` thật sẽ làm). Đặt trong `internal/domain`/`internal/app` sẽ
  VI PHẠM chính `archtest.TestDomainAppNeverImportAdapters` mà V1-11 vừa tự bắt được — do đó chọn vị trí
  ngang hàng với `internal/spikeacceptance` (cũng là package cross-cutting đã có sẵn), không phải dưới
  `internal/app`.
- **V1-12, "kill" trong test integration được mô phỏng bằng cách gọi `store.Close()` thật SỰ trong khi
  job vẫn đang LEASED (chưa complete), không phải chỉ hủy context:** hủy context (`cancel()`) chỉ dừng
  goroutine một cách "sạch," không mô phỏng đúng kịch bản "tiến trình chết đột ngột, kết nối DB đóng bất
  ngờ" — đây là lý do `worker crash không crash control loop và job không mất` (V1-10's Hoàn thành khi)
  thực sự cần verify. `store.Close()` khi job còn LEASED buộc lease đó phải hết hạn tự nhiên rồi mới được
  `RecoverExpiredJobs` của Pool #2 (mở kết nối MỚI, hoàn toàn độc lập) nhặt lại — đúng con đường crash
  recovery thật, không phải đường tắt do test tự dàn xếp.
- **V1-12, verify domain event "sống sót" sau restart bằng cách CỐ APPEND LẠI event trùng
  (aggregate_type, aggregate_id, sequence) và kỳ vọng bị reject, thay vì query trực tiếp:** không có
  method đọc domain event theo ID nào trong `ports`/`sqlite` hiện tại (đọc event là việc của V6
  projection, chưa xây) — không tự thêm 1 method sản xuất chỉ để phục vụ 1 test. Lợi dụng ràng buộc
  UNIQUE(aggregate_type, aggregate_id, sequence) đã có sẵn: append trùng CHỈ bị reject nếu row gốc còn
  tồn tại — đây là bằng chứng gián tiếp nhưng chắc chắn, không cần API mới.
- **V1-12, `agentkit-spike acceptance --full --require-all-pass` chạy local (một mình, một platform) LUÔN
  báo SPK-13 "fail" — đây là hành vi ĐÚNG THIẾT KẾ, không phải bug, không phải regression:** đọc thẳng
  report.json của bundle SPK-13: `"detail": "PENDING_PEER_PLATFORM: a single-platform run cannot conclude
  Windows/Linux parity by itself; the authoritative SPK-13 result is produced by the cross-platform
  semantic-diff job..."` — chính harness tự giải thích rõ. SPK-13 chỉ có kết quả thật khi so sánh evidence
  từ CẢ Windows lẫn Linux (đúng như job `cross-platform semantic diff (SPK-13)` trên CI đã làm, và đã xanh
  liên tục xuyên suốt V1-06 → V1-11). Không cố "fix" cho local pass được — làm vậy sẽ phá đúng invariant
  mà SPK-13 tồn tại để bảo vệ. 13/14 PASS thật + 1 PENDING đúng nghĩa + `--offline` PASS là bằng chứng đầy
  đủ ở mức local; bằng chứng THẬT cho SPK-13 vẫn là CI 6 job xanh (đã có sẵn từ mọi PR V1-06..V1-11).

## Ghi chú quy trình

Do không có Go toolchain cục bộ, quy trình cho mỗi task code là: viết → tự trace tay đối chiếu code
nguồn thật → push branch riêng → mở PR → CI thật xác nhận (không tự nhận thành công nếu chưa thấy log
CI) → nếu fail, đọc log thật, sửa, lặp lại → merge khi xanh cả 2 OS. V1-02 là ví dụ thực tế cho thấy
"trace tay" không thay thế được compiler thật 100% — vẫn cần vòng lặp CI để bắt lỗi cú pháp tinh vi.
