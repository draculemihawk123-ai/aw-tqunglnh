# V6 checklist — HTTP API, CLI và projections

> Nhật ký kiểm chứng V6 được theo dõi trong repository, tiếp nối đúng kỷ luật đã dùng cho V0-V5
> (xem `baocaov5checklist.md` cho toàn bộ lịch sử trước đó). Ghi narrative đầy đủ cho mỗi task:
> quyết định, lý do, câu hỏi tự phát hiện và verify output thật.

## V6-00A — Domain-event catalog closure (5 PR: #23, #24, #25, #26, #27)

### Bối cảnh

V5 đóng hoàn toàn (V5-15E, PR #21) đúng lúc user ngắt giữa chừng, gửi bản draft rewrite một phần
`docs/design/08-v6-api-projections.md` (V6-00A..V6-04, tiếng Anh nguyên văn) và cho phép triển khai ngay
task đầu tiên trong draft mà không cần chờ phần còn lại: "bạn có thể triển khai trước các task trong bản
draft hiện tại." V6-00A là lựa chọn tự nhiên để bắt đầu — task DUY NHẤT trong draft không phụ thuộc HTTP,
mục tiêu rõ ràng: "mọi event đã persist từ V1…V5 đều decode/classify được trước khi projection đọc journal"
— khớp đúng mẫu "typed constants/decoder/golden" đã lặp lại xuyên suốt V1-V5 cho mỗi event mới, chỉ khác ở
chỗ lần này áp dụng NGƯỢC cho toàn bộ lịch sử thay vì một event đơn lẻ mới sinh ra.

### Nghiên cứu

Agent Explore quét toàn bộ `internal/app/**` cho mọi `ports.DomainEvent{}`/`Events().Append(` call site, đối
chiếu với `eventschema.Registry` hiện có: tìm thấy 34 cặp (EventType, SchemaVersion) thật sự được emit,
chỉ 16 đã đăng ký — gap 18 event trên 8 package (catalog 3, definitions 2, message 1, work+scope 6,
workspacerelease 1, workspacereconcile 1, runtime 3, artifactsweep 1).

Tự rà soát thêm HAI lần thủ công (không tin audit đầu là đủ, vì audit đầu chỉ quét `internal/app/**` — bỏ
sót khả năng một adapter tự ghi domain event mà không qua `ports.EventsRepository.Append`) phát hiện thêm 6
gap event nữa, thật sự nằm ngoài phạm vi audit đầu vì chúng append qua raw SQL `INSERT INTO domain_events`
ngay trong `internal/adapters/sqlite`, bỏ qua hoàn toàn `ports.EventsRepository.Append` (và do đó bỏ qua cả
`eventschema.EnforcingEventsRepository`, dù đến giờ interface đó vẫn CHƯA được wire vào composition root
thật nào — xác nhận qua grep không có gì dưới `cmd/`):
- `workspace_lifecycle.go`: `REPOSITORY_WORKSPACE_QUARANTINED`/`RELEASED`/`RECREATED` (3 event) — qua một
  struct wrapper `repositoryWorkspaceEvent{eventType: ...}` dùng chung một hàm `appendRepositoryWorkspaceEvent`.
- `node_dispatch.go`/`workflow_store.go`: `NODE_RUN_COMPLETED`/`NODE_RUN_DISPATCHED`/`WORKFLOW_RUN_FINALIZED`
  (3 event) — literal SQL trực tiếp trong text câu lệnh (`'NODE_RUN_COMPLETED', 1,` ...), một quyết định có
  chủ đích ghi rõ trong `event_schema.go`: không parameterize vì đó là thay đổi ngoài phạm vi task này.

Tổng cộng xác nhận: 40 event thật sự tồn tại, 24 event cần đóng gap (18 + 6), 16 đã có sẵn từ trước.

### Quyết định

Chia theo đúng nhóm package của chính audit — 4 PR mechanical (part1-4) cho việc đóng gap, cộng 1 PR thứ 5
(automation) cho chính "CI inventory guard" mà Verify line của design doc yêu cầu — giữ đúng doctrine "một
task/part = một branch = một PR = CI 6/6 xanh = merge" đã dùng xuyên suốt 5 phần A-E của V5-15. Mỗi event
mới đăng ký đều có golden fixture + 2 test (`Test<Name>V1_GoldenFixtureDecodes`,
`Test<Name>V1_RealEventPayloadDecodes`), mirror đúng mẫu `internal/app/runtime/event_schema_test.go` đã có
từ trước — không phát minh mẫu mới.

Phạm vi CHỦ ĐỘNG không làm (ghi rõ, không âm thầm bỏ qua): KHÔNG wire `EnforcingEventsRepository` vào bất kỳ
composition root thật nào (chưa tồn tại — V6-01 mới là nơi composition root đầu tiên xuất hiện); KHÔNG đổi
business transition nào; KHÔNG sửa raw history.

### Thực hiện

**PR #23 (`feat/v6-00a-event-catalog-part1`, merged `221076b`)** — catalog + definitions + message, 6 event:
`RepositoryRegistered`, `RepositoryProbeRetried`, `ComponentPackAssigned` (catalog); `DefinitionCreated`,
`DefinitionVersionPublished` (definitions); `MessageAppended` (message). Mỗi package một file
`event_schema.go`/`event_schema_test.go`/`testdata/golden/*.json` mới, cộng sửa `commands.go` để dùng lại
đúng constant đã đăng ký (không đổi giá trị EventType/SchemaVersion nào đang persist thật).

**PR #24 (`feat/v6-00a-event-catalog-part2`, merged `e6c127e`)** — work (WorkItem + scope), 6 event:
`RootWorkItemCreated`, `ChildWorkItemCreated`, `ScopeExpansionRequested`/`Approved`/`Rejected`/`Withdrawn`.
Sửa thêm `scope_expansion.go` cùng `commands.go`.

**PR #25 (`feat/v6-00a-event-catalog-part3`, merged `1146a61`)** — workspacerelease + workspacereconcile +
artifactsweep (3 event từ audit đầu: `WorkspaceSetReleaseRequested`, `WorkspaceReconciliationRequested`,
`ARTIFACT_SWEEP_COMPLETED` — riêng event cuối chỉ cần decoder+registration vì struct payload
`SweepManifest`/`LocatorGroupResult` đã có sẵn tên/export từ trước) CỘNG 3 event từ sweep sâu thứ hai
(`REPOSITORY_WORKSPACE_QUARANTINED`/`RELEASED`/`RECREATED`) — file mới
`internal/adapters/sqlite/event_schema.go`, widen `repositoryWorkspaceEvent.payload` từ `map[string]string`
sang `any` (không đổi JSON shape đã persist).

**PR #26 (`feat/v6-00a-event-catalog-part4`, stack trên #25, merge-conflict-resolve `a714fde`, merged
`d3fcd88`)** — runtime's stragglers: `WorkflowRunStarted` (chưa từng có type), `COMPLETION_DECIDED`/
`EXECUTION_ATTEMPT_TERMINATED` (đã có constant/payload thật, chỉ thiếu registration) CỘNG 3 event nữa từ
cùng sweep sâu (`NODE_RUN_COMPLETED`, `NODE_RUN_DISPATCHED`, `WORKFLOW_RUN_FINALIZED`) mở rộng CÙNG file
`event_schema.go` PR #25 vừa tạo. GitHub báo conflict sau khi #25 squash-merge làm đứt shared history của
nhánh stack — xử lý bằng `git merge origin/master` (không rebase/force-push) trên branch #26, giải 2
conflict add/add (`event_schema.go`, `event_schema_test.go`) bằng cách giữ bản HEAD (superset), build+test
sạch trước khi commit.

**Khoảng hở giữa các phiên (phát hiện đầu phiên này)**: PR #22 (`docs: apply user's partial rewrite of
V6-00A..V6-04`, branch `docs/v5-15e-pr-merge`) — bản rewrite chính design doc đang dùng làm nguồn cho toàn
bộ V6-00A — đã bị ghi nhận NHẦM là đã merge (`bee95f2`) trong bàn giao phiên trước, nhưng
`git merge-base --is-ancestor bee95f2 origin/master` xác nhận NGƯỢC LẠI: PR vẫn OPEN, chưa từng merge, dù CI
đã 6/6 xanh và `mergeStateStatus=CLEAN` từ trước. Merge ngay (squash) trước khi tiếp tục — tránh tình trạng
code event-schema đã merge (PR #23-26) nhưng chính design doc mô tả task đó lại chưa nằm trong master.

**PR #27 (`feat/v6-00a-event-catalog-part5`, CI inventory guard, merged `a87fee4`)** — trước khi viết guard,
xác nhận LẠI một lần nữa (không tin số "40 event, đã đăng ký hết" từ phiên trước là đủ) rằng
`attempt_store.go`'s own raw `INSERT INTO domain_events` (chứa literal `'EXECUTION_ATTEMPT_TERMINATED', 1,`)
KHÔNG PHẢI một event thứ 25 chưa đếm — đọc thấy comment trong chính `agent_node_executor_cancellation.go`:
payload `executionAttemptTerminatedEventPayload{AttemptID, NextState, Reason}` "mirrors
internal/adapters/sqlite/attempt_store.go's own TerminateInterruptedAttempt" — tức 2 code path (một qua
`tx.Events().Append` từ V5-15D, một qua raw SQL từ SPK-04 crash-recovery) CHỦ Ý emit CÙNG một
(EventType, SchemaVersion) với CÙNG JSON shape, không phải 2 event khác nhau trùng tên. Xác nhận: đếm 40
event đứng vững, không cần decoder thứ 25.

Viết `TestEmittedDomainEventInventoryMatchesRegisteredInventory` (`internal/archtest/event_catalog_test.go`)
— side "registered" gọi THẬT cả 9 `RegisterEventSchemas` (sqlite, catalog, definitions, message, work,
workspacerelease, workspacereconcile, runtime, artifactsweep) vào một `eventschema.Registry` chung (thêm
`Registry.Keys() []EventKey` — method introspection mới, thuần đọc, trong
`internal/app/eventschema/registry.go`) — chọn gọi hàm thật thay vì parse text, vì một lệnh gọi thật không
bao giờ lệch khỏi hành vi đăng ký thật dù package implement kiểu gì (vd `agentevents` đăng ký qua loop trên
một slice, không phải literal). Side "emitted" parse AST thật trên source production cho đúng 3 shape đã
xác nhận tồn tại trong codebase này: `ports.DomainEvent{}` composite literal (khắp `internal/app`),
`repositoryWorkspaceEvent{}` composite literal (`workspace_lifecycle.go`, SchemaVersion cố định 1 vì không
phải field của struct), và literal SQL `'EVENT_TYPE', N` nhúng thẳng trong text `INSERT INTO domain_events`
(quét theo nội dung, không hardcode tên file, để tự bắt được insert mới trong tương lai). Loại trừ có chủ
đích `internal/app/agentevents` khỏi cả 2 phía: journal riêng (`agent_events`, không phải `domain_events`),
kind set đã tự đóng và tự nhất quán từ trước, chưa bao giờ nằm trong phạm vi audit gap của task này.

Chiều ngược (registered nhưng scan không thấy emit) CHỦ ĐỘNG không coi là lỗi — chỉ log — vì ADR-008/ADR-015
(raw event đã lưu là bất biến vĩnh viễn) có nghĩa một decoder cho một điểm emit đã bị retire vẫn PHẢI ở lại
đăng ký để dòng lịch sử cũ còn decode được; coi đó là lỗi sẽ tạo động lực sai (xoá decoder của dữ liệu thật).

### Test

- `go build ./...`, `go vet ./...` sạch sau mọi PR.
- `go test ./...` (toàn bộ module) pass 100% sau MỖI PR, kể cả sau merge-conflict-resolve của PR #26.
- Guard mới (PR #27) chạy thật cho kết quả: **40 registered, 40 emitted, 0 missing, 0 unemitted** — khớp
  CHÍNH XÁC số đã audit thủ công trước đó.
- Tự kiểm chứng guard THẬT SỰ fail-closed (không chỉ pass vì vô tình rỗng): tạm comment out
  `catalog.RegisterEventSchemas(registry)`, chạy lại — guard fail đúng, nêu tên chính xác cả 3 event
  (`ComponentPackAssigned`, `RepositoryProbeRetried`, `RepositoryRegistered`, kèm file:line thật) — rồi phục
  hồi lại nguyên trạng trước khi commit.
- 4 flake CI riêng biệt xuyên suốt cả 5 PR (`TestSPK09QuarantineRecreateFencesStaleGeneration` trên #23;
  `TestSupervisorNormalExit_TreeQuiescedFalseWhileDescendantStillRuns` và `TestProjectWorkspaceGate` — 2 lần
  liên tiếp — trên #26; lại đúng `TestSupervisorNormalExit_...` một lần nữa trên #27), TẤT CẢ xác nhận
  unrelated qua `git diff --stat` (0 dòng chạm package đó) cộng local repro (10-30 lần liên tục, pass 100%)
  trước khi rerun — không dùng `--no-verify` hay bỏ qua bất kỳ bước nào. Nhận ra thêm một điều về cơ chế
  auto-fix monitor trong lúc này: nó chỉ chủ động báo khi CI FAIL (hoặc conflict/comment), KHÔNG báo khi CI
  chuyển xanh — nên sau lần rerun cuối trên #27, phải đợi user tự hỏi mới phát hiện CI đã xanh và merge được;
  từ nay tự `ScheduleWakeup` canh đúng thời lượng job chậm nhất ("Linux race and stability", ~13-17 phút) để
  chủ động kiểm tra lại thay vì chỉ dựa vào auto-fix.

### Verify

- `docs/design/08-v6-api-projections.md` V6-00A's own Verify line: "go test ./internal/app/eventschema/...;
  replay toàn bộ golden; emitted-key inventory bằng registered-key inventory; unknown version fail-closed;
  old DB fixture vẫn decode sau restart" — cả 4 vế đều có bằng chứng thật: package `eventschema` pass 100%
  (`TestGoldenFixtures_DecodeEveryRegisteredVersion` + `EveryTestdataFileIsCovered` tự động chứng minh mọi
  golden fixture có mặt VÀ decode được); inventory guard mới (PR #27) tự động hoá chính vế thứ 3; vế "unknown
  version fail-closed" đã có sẵn từ `Registry.Decode`→`ErrNotRegistered`+`EnforcingEventsRepository`
  (V1-07A), guard mới không làm yếu đi; vế restart-decode được cover gián tiếp qua các integration test
  restart có sẵn (`v5accept`) dùng chung `eventschema.Registry`.
- "Hoàn thành khi": "không còn historical event hợp lệ nhưng thiếu decoder/golden/scope classification" — nay
  có bằng chứng TỰ ĐỘNG (guard CI), không chỉ audit thủ công một lần.

### Kết quả

V6-00A hoàn thành đầy đủ cả 5 phần (PR #23/#24/#25/#26/#27) — 24 gap event thật đã đóng, 40/40 event hiện có
decoder đã đăng ký, chứng minh bằng một test tự động thay vì chỉ một audit thủ công một lần. Tiện thể đóng
luôn một khoảng hở quy trình thật (PR #22 chưa merge dù đã ghi nhận nhầm là xong) trước khi tiếp tục — tránh
để design doc lệch khỏi code đã merge mô tả nó. Đây là task V6 đầu tiên hoàn thành theo đúng uỷ quyền của
user ("triển khai trước các task trong bản draft hiện tại"); các task tiếp theo (V6-01, V6-01A, ...) cần đọc
lại chính design doc một lần nữa trước khi bắt đầu, vì user đã báo sẽ bổ sung tiếp phần rewrite còn thiếu.
