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

## V6-00 — UX artifact framework-neutral (PR #29)

### Bối cảnh

V6-00 chạy song song với V6-00A trong cùng nhóm P0 của dependency graph (`08-v6-api-projections.md`
§2: `{V6-00, V6-00A, V6-01, V6-10C}` không chờ nhau, chỉ cùng phụ thuộc V5 đã đóng), trong một
worktree/session riêng — đúng kỷ luật "một session một Task ID". Spec task trích nguyên văn từ chính
design doc: đặc tả hành vi cho 13 screen trước khi freeze API, không chọn UI framework, không viết
production UI/endpoint/domain command; mỗi action/query phải có proposed operationId/`aw` leaf/public
operation hoặc một gap có owner Task ID cụ thể, cấm tự tạo generic status setter.

### Nghiên cứu

Đọc toàn bộ `docs/design/08-v6-api-projections.md` (822 dòng, V6-00A…V6-15P) để lấy đúng danh sách
Task ID và tên command đã bị khóa cứng trong chính các dòng "Thực hiện"/"Phạm vi" của từng task (ví
dụ `MarkWorkItemReady`, `GetSafeSettings`, `GetSource`/`GetDiff`/`GetRepositoryLog`,
`AppendConversationAttachment`, `GetArtifactContent`, `RequestReleaseSetLocalCommit`) — ưu tiên tuyệt
đối các tên này thay vì tự đặt. Đọc tiếp toàn bộ `docs/design/09-v7-alpha-ui.md` (V7 UI phase) để tìm
"13 screen" thật: loại V7-01…V7-04 (framework/workspace/token/shell — hạ tầng, không phải screen) và
V7-17 (full-journey gate, không phải screen), phần còn lại V7-05…V7-16 cộng V7-13A đúng 13 task —
khớp chính xác con số V6-00 tự đặt ra, không cần suy diễn hay bịa danh sách. Xác nhận thêm bằng grep
`projection|rebuild` trong `09-v7-alpha-ui.md`: không có screen "Projection/rebuild status" nào được
đặt tên — chỉ xuất hiện dưới dạng "stale/degraded projection indicator" (V7-04) và "projection resync"
(V7-17, test), nên KHÔNG đưa nó vào 13 screen như gợi ý ví dụ ban đầu của brief; ghi lại lý do tường
minh trong tài liệu thay vì tự thêm một screen không có trong nguồn.

Đọc `docs/architecture/02-architecture-decisions.md` §11 (ADR-010), §20 (ADR-018), §30 (ADR-028) toàn
văn để trích đúng contract bốn cột `UI action/query <-> HTTP operationId <-> aw command <-> public
application command/query` (ADR-028) và nguyên tắc "route derives scope, không tin payload" — làm
khung cột cho mọi bảng action/query.

Trước khi tự bịa tên public command, grep thật `internal/app/**` để tìm type Go đã tồn tại: xác nhận
đã có sẵn `CreateProjectRequest` (ports/catalog.go), `RegisterRepositoryRequest`/
`RetryRepositoryProbeRequest`/`AssignComponentPackRequest` (catalog), `CreateRootWorkItemRequest`/
`CreateChildWorkItemRequest`/`RequestScopeExpansionRequest`/`Approve|Reject|WithdrawScopeExpansion
Request` (work), `StartWorkflowRunRequest`/`CancelRunRequest`/`CancelWorkItemRequest`/
`RetryBlockedActivationRequest`/`ResolveWorkItemBlockerRequest`/`ResolveApprovalRequest` (runtime),
`AppendMessageRequest` (message), `CreateDefinitionRequest`/`PublishDefinitionVersionRequest`
(definitions), `RequestWorkspaceSetReleaseRequest`/`RequestWorkspaceReconciliationRequest`
(workspacerelease/workspacereconcile), `CreateReleaseSetRequest`/`SealReleaseSetRequest`/
`AbandonReleaseSetRequest` (work/release_set.go), và tiền thân nội bộ `ProbeRequest`/`RegisterRequest`
(adapterbuild) chưa có CommandEnvelope. Việc này quan trọng vì phát hiện `ResolveApproval` (không phải
"ApproveApproval"/"RejectApproval" hai command riêng) đã là command thật (V4-09) nhận `Outcome` là
vocabulary do node khai báo — tránh việc tài liệu tự đề xuất hai authority giả cho đúng một mutation.
Cũng xác nhận `ports.CreateComponentRequest` tồn tại NHƯNG bị cấm expose public theo đúng ADR-028 (
"không expose helper CreateComponent"), nên Screen 2 chỉ liệt kê `ListComponents` (query), không có
command tạo component.

### Quyết định

Ánh xạ 13 screen 1:1 vào V7-05…V7-16 + V7-13A (bảng ở §0 của tài liệu), mỗi screen có đúng bốn phần cố
định: Purpose / States (loading-empty-stale-error-blocked, "N/A" là giá trị hợp lệ) / Keyboard &
accessibility / bảng Actions-Queries. Mỗi hàng action/query có cột Owner Task ID bắt buộc — không bao
giờ để trống — trỏ vào một Task ID đã tồn tại thật trong `08-v6-api-projections.md`; nhờ vậy toàn bộ
tài liệu không có "gap chưa gán" nào theo đúng nghĩa spec yêu cầu (owner luôn có, kể cả khi command
Go chưa tồn tại — đánh dấu [ĐÃ CÓ]/[CHƯA CÓ] rõ ràng thay vì giả vờ đã xong).

Chủ động xử lý ba điểm mơ hồ thay vì im lặng bỏ qua (ghi rõ trong §16.2 của tài liệu):
1. Nhiều screen dùng chung một authority thật (`StartWorkflowRun` ở cả Kanban lẫn Task detail;
   `CancelRun`/`CancelWorkItem`/`ResolveWorkItemBlocker` ở cả Task detail lẫn Graph/Timeline;
   `RequestWorkspaceSetRelease` ở cả Workspace lẫn ReleaseSet screen; `GetRunDiagnostics` ở cả
   Graph/Timeline lẫn Settings) — đánh dấu tường minh "authority dùng chung với Screen #N" ở MỌI hàng
   liên quan để checker "không trùng authority" không hiểu nhầm thành hai owner tranh nhau.
2. Screen 13 (Settings) có tiêu đề "run diagnostics" nhưng không có task nào định nghĩa một query
   aggregate "mọi Run cần chú ý" cấp installation trong toàn bộ `08-v6-api-projections.md` — quyết
   định KHÔNG bịa Task ID mới cho nhu cầu giả định này (đúng "Không làm: không viết domain command"
   của chính V6-00); Kanban badge + Task detail deep-link + Graph/Timeline's `GetRunDiagnostics` đã đủ
   đường vận hành thật, Settings chỉ tái dùng đúng authority đó khi drill-down từ một Run cụ thể.
3. "Projection/rebuild status" không phải screen thứ 14 — ghi rõ lý do (không được `09-v7-alpha-ui.md`
   đặt tên) trong mục Cross-cutting concerns thay vì âm thầm bỏ nó khỏi tài liệu mà không giải thích.

Draft definition (V7-08) là "format-preserving, local-only" theo đúng chữ V7-08 — không có authority
server cho việc lưu draft tạm; ghi rõ đây là quyết định thiết kế đã có sẵn, không phải một ô bị bỏ
trống trong bảng.

### Thực hiện

Tạo `docs/design/11-v6-00-ux-artifact.md` (số 11 tiếp nối đúng convention `00-roadmap.md`…
`10-v8-alpha-hardening.md` đã chiếm hết 00-10) — 17 mục: §0 nguồn gốc 13 screen, §1 cách đọc bảng, §2-
§14 mỗi mục một trong 13 screen (Screen 8 Graph/Timeline có thêm đoạn fork/join/rework/checkpoint
visual-state riêng theo đúng yêu cầu Verify của V6-06B: fork = nhánh rẽ có nhãn route, join = visual
state "đang chờ join" tới khi đủ nhánh bắt buộc, rework = cạnh nét đứt + bộ đếm iteration + timeline
entry riêng từng lần lặp, checkpoint = marker icon + trạng thái resumable), §15 cross-cutting concerns
(health, SSE `WatchProjectEvents`, bootstrap token, projection rebuild), §16 gap register + tự-kiểm
từng dòng Verify bar của chính V6-00, §17 kết luận sẵn sàng cho endpoint task.

Thêm một dòng trỏ tới tài liệu mới vào `docs/00-start-here.md` §5 "Đọc theo thứ tự này" (mục 10 mới)
để task V6-0x/V7-0x tự tìm ra artifact mà không cần được nhắc lại.

Nhánh `feat/v6-00-ux-artifact` từ `origin/master` (`df0a5ab`). Commit đầu (`f6c4060`) chỉ có hai file
docs (đúng "Không làm" của task: zero Go code, zero file ngoài `docs/design/` và `docs/00-start-here.md`).
Mở PR #29, CI 6/6 xanh (bao gồm "Linux race and stability" ~12m53s dù thay đổi thuần docs — chấp nhận
chạy đủ vì repo doctrine không có ngoại lệ "docs-only skip CI"). Trong lúc CI chạy, phát hiện PR #28
(nhánh `docs/v6-00a-pr-merge`, ghi narrative V6-00A vào `baocaov6checklist.md` — file MỚI, do một
session song song khác tạo) đã merge (`fd0be07`) — đúng lúc, nên `git merge origin/master` vào nhánh
này (không rebase, giữ lịch sử) để lấy `baocaov6checklist.md` về trước khi viết chính đoạn narrative
này, gộp cả vào CÙNG PR #29 thay vì mở PR thứ hai riêng cho checklist — theo đúng correction mới nhất
của user (bỏ pattern "mỗi phần một PR" của V5-15 khi không cần thiết, giữ 1 PR/task trừ trường hợp bất
khả kháng branch đã bị xoá sau merge).

### Test

- Docs-only: không có `go build`/`go test` áp dụng cho chính nội dung thay đổi (đúng "Không làm" của
  V6-00), nhưng CI 6 job của repo vẫn chạy đầy đủ trên PR #29 vì đó là gate chung cho MỌI PR, không có
  ngoại lệ theo loại file:
  - `contract` (ubuntu + windows): pass.
  - `spike acceptance` (ubuntu + windows): pass.
  - `cross-platform semantic diff (SPK-13)`: pass, 20s.
  - `Linux race and stability (V0-12)`: pass, 12m53s (job chậm nhất, đã chờ đủ chứ không dừng sớm).
- Không gặp CI flake nào trên lần chạy này (khác V6-00A's 4 flake) — 6/6 xanh ngay từ lần push đầu.

### Verify

- Tự đối chiếu §16.3 của chính tài liệu mới với Verify bar nguyên văn của V6-00:
  - "đủ 13 screen": §0 liệt kê đúng 13, khớp 1:1 V7-05…V7-16 + V7-13A.
  - "mọi ô có mapping hoặc owner": mọi hàng ở 13 bảng Actions/Queries đều có đủ operationId/aw
    leaf/public operation/Owner Task ID, không ô nào trống.
  - "không có gap chưa gán": mọi ô [CHƯA CÓ] có đúng một Owner Task ID trỏ Task ID đã tồn tại thật.
  - "không trùng authority": mọi authority dùng ≥2 screen được đánh dấu tường minh "dùng chung với
    Screen #N", không có hai Task ID cùng tuyên bố sở hữu một mutation.
  - Không có hàng nào là generic status setter (`set-status`/`TransitionWorkItemStatus`/
    `UpdateRunState`) — toàn bộ transition dùng command hẹp đã có tên trong chính `08-v6-api-
    projections.md`/ADR-028.
- "Hoàn thành khi" của V6-00: "inventory đủ để endpoint task biết dữ liệu/interaction cần làm mà không
  hỏi lại product decision" — đáp ứng bằng cách ưu tiên tên command đã khóa cứng trong design doc/ADR,
  dùng type Go thật đã tồn tại khi có, và ghi rõ lý do cho mọi điểm mơ hồ thay vì để trống.

### Kết quả

Tính tới lúc viết dòng này, PR #29 (`feat/v6-00-ux-artifact`) đã CI 6/6 xanh và sẵn sàng merge squash
+ xóa nhánh ngay sau khi commit chứa chính narrative này được push — thực hiện trong đúng PR #29 đó,
không mở PR thứ hai, theo đúng correction mới nhất của user. V6-00 đóng hoàn toàn trong một PR duy
nhất (ba commit trên cùng nhánh: doc chính `f6c4060` + merge từ master để nhận `baocaov6checklist.md`
+ narrative này). Deliverable: `docs/design/11-v6-00-ux-artifact.md` (13 screen, không thiếu ô nào,
zero gap chưa gán) và một dòng index mới trong `docs/00-start-here.md`. Các task V6-0x (đặc biệt
V6-03…V6-11) và V6-15C…V6-15N có thể bắt đầu mà không cần hỏi lại screen/action/query nào tồn tại hay
gọi authority nào — đúng mục tiêu "Hoàn thành khi" của chính V6-00.

## V6-01 — HTTP lifecycle, health và route registration

### Bối cảnh

User đẩy thẳng lên master bản rewrite hoàn chỉnh `docs/design/08-v6-api-projections.md` (commit `df0a5ab`,
"lock contracts and parallel execution plan") — bổ sung mục 1 "Contract chung đã khóa" và mục 2 "Dependency
và nhóm triển khai song song", cho phép nhiều task V6 chạy song song ngay khi dependency của chính task đó
đã pass, thay vì tuần tự từng task một như V4/V5. Theo đúng dependency graph mục 2, nhóm P0 ("ngay sau V5")
gồm {V6-00, V6-00A, V6-01, V6-10C} — V6-00A đã xong, 3 task còn lại chỉ phụ thuộc V5 (đã xong) và không đụng
file của nhau, nên chạy song song được thật. User yêu cầu tách subtask song song cho các task đủ điều kiện.
V6-00 (đặc tả UX, thuần doc) và V6-10C (query đọc-only, package mới độc lập) được giao cho 2 subagent chạy
song song (PR #29 và một PR riêng); V6-01 được tự làm trực tiếp (không qua subagent) vì đây là composition
root cho toàn bộ HTTP layer — mọi task endpoint sau này (V6-01A trở đi) đều build trên route registration
primitive của chính task này, sai ở đây sẽ dội ngược lên hàng chục task sau.

**Điều chỉnh quy trình giữa chừng (áp dụng ngay từ task này trở đi)**: user phản hồi trực tiếp rằng việc tách
nhiều PR nhỏ như V5-15/V6-00A không phải quy tắc chuẩn — đó là quyết định tình huống lúc đang làm, không phải
thống nhất từ đầu — và mỗi lần CI chạy tốn nhiều thời gian (job "Linux race and stability" một mình đã
~13-17 phút). Từ V6-01 trở đi: một task một PR duy nhất (implementation + narrative checklist gộp chung),
chỉ tách nhiều PR khi diff thực sự lớn đến mức khó review/CI trong một lần.

### Nghiên cứu

Trước khi viết code, xác nhận qua Explore agent các sự thật sau (không đoán):
- `internal/delivery/httpapi` và mọi `internal/delivery/*` **chưa tồn tại** — package hoàn toàn mới.
- `internal/app/readinesscheck` **không liên quan** đến health-check HTTP — đây là handler V3-07 chạy
  baseline-evidence job cho repo readiness, không phải thứ V6-01 cần dùng lại. Tự kiểm tra kỹ để không nhầm
  package chỉ vì tên gần giống.
- `cmd/agentkit serve` hiện là `stub("serve")` — trả `errNotYetImplemented`, chưa có implementation thật.
- Composition pattern có sẵn (mirror từ `definition.go`): `sqlite.Open(ctx, path)` tự chạy migration bên
  trong, `sqlite.NewUnitOfWork(store)` dựng UnitOfWork — không có DI framework, wiring trực tiếp tại
  composition root.
- ADR-016 (local HTTP trust boundary): external bind phải bị từ chối thật (không chỉ off-by-default) — đây
  là lý do `NewServer` phải tự validate host resolve về loopback, không chỉ tin flag người dùng truyền vào.
  ADR-025 xác nhận "health/doctor" là 2 query duy nhất thuộc installation scope (không phải project scope).
  ADR-028 xác nhận V6-01 vẫn ở `cmd/agentkit` — việc đổi tên thành `aw` là V6-15A, chưa phải task này.
- Không có framework/router bên thứ 3 nào trong `go.mod` — toàn bộ `net/http` chuẩn, chưa file nào import nó
  trước đây. Go 1.27 hỗ trợ `ServeMux` pattern `"METHOD /path"` sẵn, không cần viết router riêng.
- Mẫu registry "Register panic khi trùng key" đã có 2 tiền lệ y hệt cần mirror: `eventschema.Registry` và
  `workerpool.Registry` (cùng dùng `sync.RWMutex` + map, panic thay vì trả lỗi).
- `idsource.Random{}.NewID()` (UUID v4) là nguồn sinh ID chuẩn của codebase — dùng lại cho correlation ID
  thay vì tự viết generator mới.
- Không có type `Installation` nào tồn tại — "installation-scoped health query" phải tự dựng từ
  Config/UnitOfWork/artifact-root/route-composition trực tiếp, không có aggregate có sẵn để query qua.

### Quyết định

Thiết kế `internal/delivery/httpapi` với 4 khối tách rời, mỗi khối một file, theo đúng "mỗi endpoint/task sở
hữu file/package riêng" (Contract chung mục 1.8 của chính doc): `route.go` (RouteDescriptor + RouteRegistry,
{Method, Path, OperationID, ScopeKind, RequestSchema, ResponseSchema, Handler} đúng field list trong doc),
`health.go` (ReadinessChecker chạy check thật mỗi lần gọi, không cache boolean), `middleware.go` (correlation
ID, panic recovery dùng lại `logging.Logger` thật, body-size limit), `json.go` (DecodeJSON strict — reject
unknown field + reject multiple JSON value trong 1 body), `server.go` (Server thật bọc `net/http.Server`,
tự validate loopback host bằng `net.LookupIP` thật chứ không chỉ so sánh chuỗi "127.0.0.1").

`RequestSchema`/`ResponseSchema` cố tình để kiểu `any` (chỉ cần non-nil) — vì "Không làm: không implement
business endpoint" của chính task này nghĩa là chưa cần mô hình JSON-schema thật; V6-02A mới là task định
nghĩa shared schema-fragment contract. Đây không phải bỏ sót mà là biên giới phạm vi được ghi rõ.

`ReadinessChecker` chạy **check thật mỗi request** (real DB ping qua `uow.WithReadOnly`, real `os.Stat` trên
artifact root) thay vì cache một boolean set lúc startup — đúng tinh thần "ready gọi installation-scoped
health query" (query, không phải cờ tĩnh) và tự nhiên cho phép ready quay lại NOT_READY nếu dependency chết
sau khi đã từng ready, không chỉ đúng lúc startup.

`Register` (cả RouteRegistry lẫn ReadinessChecker) panic khi trùng key hoặc thiếu field bắt buộc — mirror
đúng 2 tiền lệ đã tìm thấy, đúng yêu cầu "descriptor trùng fail" của Verify line.

### Thực hiện

- `internal/delivery/httpapi/route.go`: `ScopeKind` (INSTALLATION/PROJECT theo đúng ADR-025), `RouteDescriptor`,
  `RouteRegistry.Register` validate đủ 7 field trước khi panic-on-duplicate, `Descriptors()` trả theo đúng
  thứ tự đăng ký.
- `internal/delivery/httpapi/health.go`: `ReadinessCheck func(ctx) error`, `ReadinessChecker.Register`/`Evaluate`
  (chạy hết mọi check, gom lỗi thành `[]readinessFailure` sort theo tên), `LiveHandler` (luôn 200, không đụng
  checker), `ReadyHandler` (503 kèm JSON `{status, checks:[{check, reason}]}` khi có check fail, 200 khi hết).
- `internal/delivery/httpapi/middleware.go`: `CorrelationID` (đọc header `X-Correlation-Id`, sinh mới qua
  `idsource.Source` nếu thiếu, echo lại response header), `Recover` (bắt panic, log qua `logging.Logger` thật
  với `apperror.CodeInternal`, trả 500 JSON, không crash server — logger `nil` vẫn phải recover được), `MaxBytes`
  (wrap `http.MaxBytesReader`), `Chain` (compose middleware theo thứ tự).
- `internal/delivery/httpapi/json.go`: `DecodeJSON` — `DisallowUnknownFields`, decode lần 2 để bắt trailing
  JSON value, map lỗi vượt limit (`*http.MaxBytesError`) thành `ErrBodyTooLarge` riêng biệt với `ErrMalformedJSON`.
- `internal/delivery/httpapi/server.go`: `NewServer` validate Host thật (loopback IP literal hoặc resolve
  hostname qua `net.LookupIP`, mọi địa chỉ trả về phải loopback — reject `0.0.0.0`, `example.com`, ...), compose
  `http.ServeMux` từ registry + middleware chain, `net.JoinHostPort` (không tự ghép chuỗi `host:port` — bug
  thật gặp phải: `::1:0` là địa chỉ không hợp lệ nếu ghép tay, `JoinHostPort` tự bọc ngoặc IPv6 đúng).
- `cmd/agentkit/serve.go`: tách `runServe` (wire `signal.NotifyContext` cho SIGINT/SIGTERM) khỏi `serve(ctx,
  args, stdout)` (logic composition thật) — để test điều khiển shutdown bằng cách cancel ctx trực tiếp, không
  cần gửi tín hiệu OS thật vào cả tiến trình test. `serve` mở DB thật (tái dùng `openDefinitionDB` có sẵn,
  không viết trùng), validate artifact-root là thư mục tồn tại thật, đăng ký đúng 3 readiness check (database,
  artifact_root, routes — 2 field "config"/"migration" trong spec không tách check riêng vì chúng đã đảm bảo
  đúng ngay khi tới được điểm này, không thể fail lại sau đó — chỉ 3 check còn lại thật sự có thể thay đổi
  trạng thái sau khi server đã chạy), in `{"address":"..."}` ra stdout ngay khi bind xong.
- `cmd/agentkit/cli.go`: nối `"serve": runServe` thay cho `stub("serve")`.
- `cmd/agentkit/cli_test.go`: sửa `TestRun_StubCommandsReportNotYetImplemented` (bỏ "serve" khỏi danh sách
  stub, chỉ còn "worker"/"doctor") và đổi tên `TestRun_StubCommandRejectsUnknownFlag` thành
  `TestRun_ServeRejectsUnknownFlag` (test vẫn đúng hành vi, chỉ tên cũ không còn phản ánh đúng "serve" đã là
  implementation thật, không phải stub nữa).

### Test

- `go build ./...`, `go vet ./...` sạch sau toàn bộ thay đổi.
- `internal/delivery/httpapi`: bộ test thật đầy đủ theo đúng Verify line — malformed JSON, unknown field,
  trailing JSON value, oversized body (`DecodeJSON`); panic bị Recover bắt và server sống tiếp cho request kế
  tiếp thật (`TestServer_PanicInHandlerReturns500NotCrash` gọi tiếp một request thứ 2 để chứng minh); request
  bị client cancel giữa chừng, handler thật nhận được `ctx.Done()` (`TestServer_RequestCancellation_...`);
  graceful shutdown chờ đúng request đang chạy dở xong mới return (`TestServer_GracefulShutdown_WaitsForInFlightRequest`,
  dùng channel đồng bộ thật, không dùng sleep đoán thời gian); live luôn pass dù mọi readiness check fail
  (`TestLiveHandler_PassesEvenWhenDependenciesWouldFailReadiness`); ready fail typed trước, pass sau khi cờ
  startup bật (`TestReadyHandler_FailsTypedBeforeStartupThenPassesAfter`); descriptor trùng panic
  (`TestRouteRegistry_Register_DuplicateMethodAndPathPanics`, `TestServer_DuplicateRouteRegistration_FailsAtComposition`).
  Race thật phát hiện lúc viết test: ghép `host:port` bằng `fmt.Sprintf` vỡ với `::1` — sửa bằng `net.JoinHostPort`.
- `cmd/agentkit`: `serve()` (đã tách khỏi `runServe`) được test bằng ctx tự cancel — không cần tín hiệu OS
  thật; test thật chạy DB SQLite thật + thư mục artifact-root thật + HTTP round-trip thật qua `net/http.Client`
  tới địa chỉ server tự bind (port 0, OS cấp ephemeral port). `TestServe_ReadyFailsIfArtifactRootRemoved` tự
  xoá thư mục artifact-root SAU KHI server đã ready, xác nhận ready quay lại 503 đúng tên check `artifact_root`
  — chứng minh check chạy thật mỗi lần, không phải cờ cache một lần lúc startup.
  Một flake timing thật tự phát hiện khi chạy lặp: `TestServe_StartsServesHealthAndShutsDownGracefully` thỉnh
  thoảng mất ~6s (thay vì ~0.1s) do `http.Server.Shutdown` phải chờ idle keep-alive connection của
  `http.Client` tự đóng — sửa bằng gọi `client.CloseIdleConnections()` thật trước khi cancel ctx, không phải
  che bằng tăng timeout.
- `go test ./...` toàn bộ module pass 100% sau khi sửa `TestRun_StubCommandsReportNotYetImplemented` (test cũ
  giả định "serve" còn là stub — lỗi thật bị phát hiện ngay lần chạy full suite đầu tiên, không phải bỏ qua).
- **CI race detector ("Linux race and stability") bắt được một data race thật** (không phải flake đã biết từ
  trước — kiểm tra kỹ trước khi kết luận): `waitForServeAddress` (helper poll `stdout` để lấy địa chỉ server
  vừa bind) đọc `bytes.Buffer.String()` từ goroutine chính của test, trong khi `serve()` chạy ở goroutine
  riêng ghi `fmt.Fprintf(stdout, ...)` vào ĐÚNG buffer đó — `bytes.Buffer` không an toàn cho truy cập đồng
  thời, và race detector chỉ ra chính xác 2 goroutine, 2 dòng code xung đột. Không có CGO/race detector local
  trên máy Windows này (`CGO_ENABLED=0`, không có gcc) nên không tái hiện được tại chỗ — phải đọc kỹ log CI
  thật để xác định chính xác nguyên nhân trước khi sửa. Sửa bằng `syncBuffer` (wrapper `bytes.Buffer` +
  `sync.Mutex` cho cả `Write` và `String`), chỉ dùng ở 2 test có goroutine thật; 3 test còn lại gọi `serve()`
  đồng bộ (không goroutine) nên giữ nguyên `bytes.Buffer` trần, không cần đổi.

### Verify

- Verify line của chính task: "malformed/oversized/cancel/panic/shutdown" — cả 5 case đều có test thật riêng
  biệt, không gộp chung một test mơ hồ. "live vẫn pass khi dependency degraded" — có test riêng chứng minh
  bằng một checker mà MỌI check đều fail. "ready fail typed trước readiness và pass sau startup" — có test
  riêng chứng minh đúng trình tự trước/sau, không chỉ test trạng thái cuối. "descriptor trùng fail" — có test
  ở cả 2 tầng (RouteRegistry trực tiếp và qua Server thật).
- ADR-016 được verify thật (không chỉ đọc rồi tin): `TestNewServer_RejectsNonLoopbackHost` (`0.0.0.0`) và
  `TestNewServer_RejectsExternalHostname` (`example.com`) đều gọi `net.LookupIP` thật, không mock.

### Kết quả

V6-01 hoàn thành trong 1 PR duy nhất (đúng điều chỉnh quy trình đầu task này) — package `internal/delivery/httpapi`
mới (5 file production + 4 file test) và `cmd/agentkit serve` chuyển từ stub sang implementation thật, chạy
song song với 2 subagent làm V6-00/V6-10C theo đúng dependency graph P0 của bản rewrite mới nhất. Route
registration primitive đã sẵn sàng để mọi task endpoint sau này (V6-01A trở đi) tự thêm fragment riêng mà
không cần sửa file chung này.

## V6-01A — Local-browser HTTP security và principal snapshot

### Bối cảnh

V6-01 (`d8c1323`) vừa merge, mở khóa V6-01A — dependency duy nhất của task này. Task đang chạy song song với
V6-02A, V6-15A, V6-03 (theo đúng nhóm P1 "sau V6-01" của `docs/design/08-v6-api-projections.md` mục 2), tất cả
cùng append vào `baocaov6checklist.md`. Giữ đúng điều chỉnh quy trình đã chốt từ V6-01: một task một PR duy
nhất, implementation + narrative gộp chung một commit/PR, chỉ tách khi diff thật sự quá lớn.

**Merge conflict thật, lớn hơn dự kiến ban đầu**: lúc bắt đầu implement, `origin/master` chỉ có tới V6-01
(`d8c1323`); lúc chuẩn bị mở PR, V6-15A đã merge trước (`fdd95a8`, PR #32) — đổi tên toàn bộ composition root
từ `cmd/agentkit` sang `cmd/aw`. Đây không chỉ là xung đột 1 file `baocaov6checklist.md` như dự kiến, mà
`cmd/agentkit/serve.go` (file chính task này sửa) không còn tồn tại ở path cũ nữa. Xử lý bằng cách tạo lại
branch `feat/v6-01a-browser-security` từ đúng `origin/master` mới nhất, rồi áp lại diff riêng của task này
(không phải nội dung file) vào `cmd/aw/serve.go` — xác nhận trước bằng `git diff d8c1323:cmd/agentkit/serve.go
origin/master:cmd/aw/serve.go` rằng nội dung file không đổi qua rename (chỉ đổi path), nên patch áp sạch,
không có xung đột logic thật nào giữa V6-01A và V6-15A.

### Nghiên cứu

Đọc toàn bộ `internal/delivery/httpapi/server.go` và `middleware.go` trước khi viết bất kỳ dòng nào (đúng yêu
cầu của task), xác nhận các sự thật sau:
- `validateLoopbackHost` (V6-01) đã reject external bind thật bằng `net.LookupIP`, không chỉ so chuỗi —
  Verify bullet "external bind" của chính task này không cần code mới, chỉ cần xác nhận vẫn áp dụng (2 test
  cũ `TestNewServer_RejectsNonLoopbackHost`/`TestNewServer_RejectsExternalHostname` vẫn còn nguyên, không sửa).
- `Chain(mux, CorrelationID, Recover, MaxBytes)` là toàn bộ middleware hiện có — chưa có bất kỳ khái niệm nào
  về Host/Origin/CORS/token/principal. `idsource.Source`/`idsource.Random{}.NewID()` là generator ID chuẩn
  duy nhất trong codebase (dùng lại cho token và cho CSP nonce, không tự viết `crypto/rand` riêng).
- Đọc lại `docs/architecture/02-architecture-decisions.md` ADR-016 (mục 18) và ADR-025 (mục 27) đầy đủ —
  ADR-016 chốt rõ: external bind reject, Host/Origin validate riêng biệt (CORS deny-by-default *không thay
  thế* 2 kiểm tra này), token per-start không ghi URL/log/artifact/persisted config, chỉ inject vào bootstrap
  HTML `Cache-Control: no-store` + CSP chặt cho Host loopback hợp lệ.
- `LocalPrincipalSnapshot {Actor, Roles[]}` **không nằm trong ADR-028's phần đầu** (canonical CLI `aw`) mà
  nằm ở đoạn văn cuối cùng của chính ADR-028 (dòng 767-778 file ADR) — dễ đọc sót nếu chỉ đọc tiêu đề mục.
  Đối chiếu thêm 3 chỗ khác nói cùng nội dung để chắc chắn không suy diễn sai: `docs/design/01-system-design.md`
  dòng 540-550, `docs/architecture/04-go-core-spec.md` dòng 500-505, `docs/architecture/03-system-architecture.md`
  dòng 463-473 — cả 4 chỗ khớp nhau: config key canonical `localPrincipal.actor`/`localPrincipal.roles`,
  thiếu toàn bộ thì default `local-operator`/`[operator]`, actor/role non-empty, roles unique case-sensitive,
  "không có per-command `--actor`/`--role`" nhưng "global composition option chọn một trusted config file vẫn
  được phép".
- `grep -r "LocalPrincipal"` xác nhận **chưa có type nào tồn tại** trong `internal/app/config` hay bất kỳ đâu
  trong `internal/` — phải tự dựng từ đầu, không có aggregate/type có sẵn để tái dùng.
- `internal/app/config.Config`/`Overrides`/`Load` là pipeline `defaults < file < env < flags` cho
  DatabasePath/WorkerID/... nhưng **`cmd/agentkit/serve.go` chưa từng gọi `config.Load` cả** — `serve` tự
  parse flag riêng (`--db`, `--artifact-root`, `--host`, `--port`, `--max-body-bytes`), không đụng gì tới
  package `config` từ trước tới giờ. Wiring toàn bộ `config.Config` vào `serve` (WorkerID, LeaseTTL, ...) sẽ
  là một refactor ngoài phạm vi task này.
- `internal/app/redact.NewMatcher(secrets ...string)` là exact-match matcher (không phải regex/pattern) —
  đăng ký token làm secret đã biết là cách dùng đúng ý (`WithSecrets` doc comment đã mô tả đúng pattern này
  cho `Attempt`'s `SecretRefs`, dùng lại y hệt cho session token).

### Quyết định

**`LocalPrincipal` tách khỏi pipeline `Config`/`Overrides`/`Load` chung**, thành file riêng
`internal/app/config/local_principal.go` với loader/validator riêng (`LoadLocalPrincipalFile`,
`ValidateLocalPrincipal`), thay vì thêm field vào `Config`/`Overrides`. Hai lý do: (1) pipeline chung hỗ trợ
`file < env < flags`, nhưng ADR-028 chỉ cho phép đúng một cơ chế — "global composition option chọn một
trusted config file" — nên thêm field vào pipeline chung sẽ vô tình mở đúng cái cửa hậu ADR-028 cấm (env/flag
override actor/role); (2) `serve.go` chưa wire `config.Load` bao giờ, kéo cả pipeline vào chỉ để dùng 1 field
là over-engineering ngoài phạm vi. Key JSON cố tình lồng `{"localPrincipal": {"actor", "roles"}}` (không phải
snake_case phẳng như các key khác của `Config`) — khớp đúng cách ADR-028 tự viết `localPrincipal.actor` (có
dấu chấm), và là tín hiệu thị giác rằng đây là loader khác, hẹp hơn.

**File thiếu `localPrincipal` hoàn toàn → default; file có `localPrincipal` nhưng thiếu `roles` → KHÔNG tự
default nốt phần thiếu, để `Validate` fail thật.** ADR-028 chỉ nói "thiếu toàn bộ" mới default — một khai báo
nửa vời gần như chắc chắn là lỗi cấu hình của operator, default ngầm phần còn thiếu sẽ che mất lỗi đó.

**Token sinh ở composition root (`serve.go`), không sinh bên trong `NewServer`.** Lý do kỹ thuật, không phải
sở thích: route bootstrap cần token để dựng `BootstrapHandler` closure, nhưng `RouteRegistry` phải đăng ký
xong *trước khi* gọi `NewServer` (constructor đọc `cfg.Routes.Descriptors()` ngay lúc dựng mux) — nếu để
`NewServer` tự sinh token thì xảy ra gà-trứng: token chưa tồn tại lúc route bootstrap cần nó. `Config.Token`
trở thành field bắt buộc (giống `Routes`/`IDs`/`MaxBodyBytes` đã có), `NewServer` chỉ validate không rỗng.

**`HostOriginGuard` tính `expectedHost` từ `cfg.Host` (chuỗi operator cấu hình gốc) ghép với PORT THẬT đã
bind** (`net.SplitHostPort(listener.Addr().String())`), không dùng thẳng `listener.Addr().String()`. Nếu
dùng thẳng địa chỉ đã resolve, cấu hình `--host localhost` sẽ khiến `expectedHost` thành `"127.0.0.1:PORT"`
(hoặc `"[::1]:PORT"`, tùy resolver) trong khi trình duyệt thật gửi `Host: localhost:PORT` — validate luôn
fail sai ngay cả với request hợp lệ. Ghép `cfg.Host` gốc với port thật (cần thật vì `Port:0` là ephemeral)
mới đúng những gì client thật sự gửi.

**CSP dùng `script-src 'self' 'nonce-<random>'` thay vì `'unsafe-inline'`.** Bootstrap HTML dùng `<script>`
inline để gán `window.__AW_BOOTSTRAP__` — nếu không có nonce, CSP `script-src 'self'` chặt sẽ tự chặn luôn
chính script đó, token không bao giờ tới được tay UI thật. Nonce sinh mới mỗi response qua `idsource.Source`
(không phải secret — chỉ cần không đoán trước được, tái dùng nguồn ID chuẩn thay vì tự viết `crypto/rand`).
Token/actor/roles nhúng qua `encoding/json.Marshal` (tự HTML-escape `<`/`>`/`&`) — không cần tự escape tay,
không có nguy cơ `</script>` breakout.

**CORS deny-by-default bằng cách không bao giờ set bất kỳ header `Access-Control-Allow-*` nào**, thay vì cấy
logic preflight riêng rồi tự giới hạn dần. Đơn giản hơn và đúng ADR-016 ("CORS deny-by-default không thay thế
2 kiểm tra Host/Origin") theo nghĩa đen: không có gì để "thay thế" cả vì CORS không emit gì hết; một preflight
cross-origin thật sự đã bị `HostOriginGuard` chặn ở tầng Origin trước khi chạm route.

**`RequireSessionToken` chỉ áp cho method không an toàn (không phải GET/HEAD/OPTIONS)** — đúng nghĩa đen
"Mutation cần token" của ADR-016 (không phải "mọi request"), và giải quyết gọn bài toán gà-trứng bootstrap:
trang bootstrap tự nó là GET nên không cần token để tải về token.

### Thực hiện

- `internal/app/config/local_principal.go` (mới): `LocalPrincipal{Actor, Roles[]}`, `DefaultLocalPrincipal()`
  (`local-operator`/`[operator]`), `LoadLocalPrincipalFile(path)` (path rỗng hoặc file không tồn tại hoặc
  thiếu key `localPrincipal` → default; JSON lỗi → error thật), `ValidateLocalPrincipal` (actor non-empty,
  roles non-empty, mỗi role non-empty, unique case-sensitive — dùng `apperror.New(CodeInvalidArgument, ...)`
  + `WithDetails` đúng pattern `Validate` cũ trong `validate.go`).
- `internal/delivery/httpapi/principal.go` (mới): `LocalPrincipalSnapshot{Actor, Roles[]}` (type riêng của
  package delivery, không import `internal/app/config` — composition root tự convert), `PrincipalFromContext`/
  `BindPrincipal` (context injection, không đọc `r` — không body, không header, không URL — đúng nghĩa đen
  "Actor/ActorRoles là authentication context, không phải input tự khai" của ADR-028).
- `internal/delivery/httpapi/security.go` (mới): `LocalOrigin{Host, Origin}`, `HostOriginGuard` (exact-match
  Host bắt buộc; Origin chỉ reject khi có mặt và sai, absent thì cho qua), `SessionTokenHeader = "X-Aw-Session-
  Token"`, `RequireSessionToken` (`crypto/subtle.ConstantTimeCompare`, skip GET/HEAD/OPTIONS), `isSafeMethod`.
- `internal/delivery/httpapi/bootstrap.go` (mới): `BootstrapHandler(token, principal, ids)` — set
  `Cache-Control: no-store` + CSP nonce-based trước `WriteHeader`, nhúng `{token, actor, roles}` qua
  `json.Marshal` vào `<script nonce=...>`.
- `internal/delivery/httpapi/server.go`: thêm `Config.Token`/`Config.Principal` (bắt buộc, validate ở đầu
  `NewServer` — `Principal.validate()` reject actor rỗng/roles rỗng/role rỗng/role trùng case-sensitive), tính
  `LocalOrigin` từ `cfg.Host` + port thật sau khi listener đã bind, mở rộng `Chain(...)` thành
  `HostOriginGuard → CorrelationID → Recover → RequireSessionToken → MaxBytes → BindPrincipal → mux` (giữ
  nguyên thứ tự tương đối 3 middleware cũ của V6-01 để không đổi hành vi test cũ đã có).
- `cmd/aw/serve.go` (path đổi từ `cmd/agentkit/serve.go` do V6-15A merge trước — nội dung áp lại y nguyên
  bằng patch, không đổi logic gì so với thiết kế ban đầu): thêm flag `--principal-config` (JSON file, optional
  — đây là "global composition option" ADR-028 cho phép, không phải per-command `--actor`/`--role`), resolve +
  validate `LocalPrincipal` trước khi mở DB, sinh `sessionToken := idsource.Random{}.NewID()` một lần duy nhất
  mỗi lần `serve` khởi động, đăng ký thêm route `GET /` → `httpapi.BootstrapHandler`, đăng ký `sessionToken`
  làm known secret với `redact.NewMatcher(sessionToken)` (defense-in-depth cho logger — dù không có code path
  nào chủ động log token, một lỗi tương lai vô tình thêm log vẫn bị chặn).
- `internal/delivery/httpapi/server_test.go`: `newTestServer` và `TestNewServer_AcceptsLoopbackIPAndLocalhost`
  thêm `Token`/`Principal` (2 field mới bắt buộc); thêm `TestNewServer_RequiresTokenAndPrincipal` (5 case: hợp
  lệ, thiếu Token, thiếu Actor, thiếu Roles, role rỗng, role trùng).

### Test

Toàn bộ test là HTTP round-trip thật qua `httptest`/`net/http.Client` tới listener thật đã bind — không
fabricate DB row, không mock Host/Origin.

- `internal/delivery/httpapi/security_test.go` (mới, 18 test function): `TestHostOriginGuard_RejectsForeignHost`
  (giả lập DNS rebinding — set `req.Host` khác trong khi TCP peer thật vẫn là 127.0.0.1, đúng cơ chế tấn công
  thật), `TestHostOriginGuard_RejectsHostnameMismatchEvenWhenBothLoopback` (`localhost` vs `127.0.0.1` — cả
  hai đều loopback nhưng không "exact match", phải reject), `TestHostOriginGuard_AcceptsExactBoundHost`,
  `TestHostOriginGuard_RejectsForeignOrigin`, `TestHostOriginGuard_RejectsRightHostWrongPortOrigin` (đúng host
  sai port — dễ bỏ sót nếu chỉ so sánh hostname), `TestHostOriginGuard_MissingOriginAllowed`,
  `TestHostOriginGuard_AcceptsExactMatchingOrigin`, `TestCORS_NeverEmitsAccessControlAllowOriginHeader` (cả
  response thành công lẫn preflight cross-origin bị reject đều không có header `Access-Control-Allow-Origin`),
  `TestRequireSessionToken_MissingTokenRejectsMutation`/`WrongTokenRejectsMutation`/
  `CorrectTokenAllowsMutation`/`SafeMethodNeedsNoToken`, `TestActorRoleSpoof_BodyAndHeaderIgnored` (POST body
  `{"actor":"attacker","roles":["admin","root"]}` + header `X-Actor`/`X-Roles` giả mạo, handler chỉ đọc
  `PrincipalFromContext` — response echo đúng `local-operator`/`[operator]` của server, không phải giá trị kẻ
  tấn công khai), `TestPrincipal_ChangeOnlyTakesEffectOnFreshServer` (2 `Server` độc lập, principal khác nhau
  — server đầu chạy tiếp không đổi khi server thứ 2 dựng xong, chứng minh không có state global rò rỉ),
  `TestSecretScan_TokenNeverAppearsInLogOutput` (đẩy traffic đa dạng gồm cả request mang đúng token qua
  header, scan toàn bộ buffer log sau đó — assertion thật, không suy diễn từ "không có code log token"),
  `TestSecretScan_BootstrapHTMLNeverPutsTokenInAURLOrQueryString` (assert token CÓ mặt trong HTML — sanity
  chống test giả — rồi assert không xuất hiện trong bất kỳ context giống URL nào: `href="`, `src="`,
  `?token=`, `&token=`, `?session=`), `TestBootstrapHandler_SetsNoStoreAndCSP`,
  `TestBootstrapHandler_RejectedByHostGuardLikeAnyOtherRoute` (bootstrap không tự kiểm Host — chứng minh nó
  dựa hoàn toàn vào middleware chung, không có kiểm tra riêng dễ lệch pha).
- `internal/app/config/local_principal_test.go` (mới, 12 test function): default khi path rỗng/file không tồn
  tại/file thiếu key `localPrincipal`, đọc đúng actor/roles tùy chỉnh, JSON lỗi fail, validate reject
  actor rỗng/actor toàn khoảng trắng/roles rỗng/role rỗng/role trùng case-sensitive, chấp nhận biến thể hoa-
  thường là 2 role khác nhau (đúng nghĩa "case-sensitive"), và riêng `TestLoadLocalPrincipalFile_
  PartialDeclarationIsNotSilentlyDefaulted` chứng minh khai báo nửa vời (actor không có roles) KHÔNG bị default
  ngầm — `Load` trả nguyên trạng, `Validate` mới fail.
- `cmd/aw/serve_principal_test.go` (mới, path đổi từ `cmd/agentkit/` do V6-15A, 4 test function):
  `TestServe_BootstrapUsesDefaultPrincipalWhenNoConfigFlag`
  (không truyền `--principal-config` → bootstrap thật trả `local-operator`/`[operator]`),
  `TestServe_BootstrapUsesCustomPrincipalFromConfigFile` (file JSON thật với actor/roles tùy chỉnh → bootstrap
  phản ánh đúng), `TestServe_RejectsInvalidPrincipalConfigAtStartup` (`roles: []` → `serve` fail ngay lúc khởi
  động, không chạy với principal rỗng), `TestServe_SessionTokenNeverPersistedInSQLite` (secret-scan thật:
  chạy `serve` thật với SQLite thật, lấy token qua ĐÚNG kênh hợp lệ duy nhất — parse response bootstrap thật,
  không đọc biến nội bộ — rồi tắt server hẳn, mở lại file `.db` bằng connection độc lập thứ 2, liệt kê mọi
  table từ `sqlite_master`, scan từng cột từng dòng tìm substring token — assertion thật trên dữ liệu thật).
- **Flake thật tự phát hiện khi chạy full suite `cmd/aw`** (không phải flake đã biết từ trước — đối
  chiếu kỹ, đây là do chính task này gây ra): lúc đầu `TestServe_SessionTokenNeverPersistedInSQLite` mở
  connection SQLite thứ 2 để scan trong khi `serve()` vẫn đang chạy, connection production vẫn giữ pool mở —
  tranh chấp lock WAL-mode giữa 2 connection khiến CẢ CÁC TEST KHÁC chạy sau nó trong cùng suite chậm hẳn
  (một test cũ vô can, `TestServe_ReadyFailsIfArtifactRootRemoved`, từ ~0.8s vọt lên 11.48s và timeout hẳn khỏi
  deadline 5s của `waitForServeAddress`). Sửa bằng cách đổi `startServeForTest` trả thêm `stop func()`
  (idempotent qua `sync.Once`), gọi `stop()` tắt hẳn server (đóng pool connection production) TRƯỚC KHI mở
  connection thứ 2 để scan — test giờ đúng nghĩa "process đã dừng để lại gì trên đĩa", không phải "đọc đồng
  thời với writer đang chạy". Sau khi sửa: suite `cmd/aw` chạy 3 lần liên tiếp (`-count=3`) đều xanh,
  thời gian giảm từ 36s (có fail) xuống ~17s/lần (toàn xanh).
- `go build ./...`, `go vet ./...` sạch. `go test ./internal/delivery/httpapi/... -count=10` và
  `go test ./cmd/aw/... -count=3` đều xanh 100% (không có `-race` local được — máy Windows này
  `CGO_ENABLED=0`, không có gcc, y hệt giới hạn đã ghi nhận ở V6-01; race thật sẽ do CI Linux bắt nếu có).
  `go test ./...` toàn bộ module (68+ package) xanh 100%.

### Verify

Đối chiếu từng bullet của Verify line gốc ("external bind, DNS rebinding, foreign/missing Origin, missing/
wrong token, actor/role spoof, role downgrade sau restart và secret scan trên log/DB/bootstrap cache"):

- **external bind**: tái dùng nguyên vẹn `TestNewServer_RejectsNonLoopbackHost`/`TestNewServer_
  RejectsExternalHostname` của V6-01 — không sửa, chỉ xác nhận vẫn áp dụng đúng vì `HostOriginGuard` nằm
  SAU `validateLoopbackHost` trong luồng, không thay thế nó.
- **DNS rebinding**: `TestHostOriginGuard_RejectsForeignHost` — mô phỏng đúng cơ chế thật (peer TCP là
  127.0.0.1, `Host` header là domain kẻ tấn công kiểm soát).
- **foreign/missing Origin**: `TestHostOriginGuard_RejectsForeignOrigin` + `RejectsRightHostWrongPortOrigin`
  (foreign) và `TestHostOriginGuard_MissingOriginAllowed` (missing — đúng hành vi ADR-016 cho phép, không
  phải lỗi thiếu sót).
- **missing/wrong token**: `TestRequireSessionToken_MissingTokenRejectsMutation`/`WrongTokenRejectsMutation`.
- **actor/role spoof**: `TestActorRoleSpoof_BodyAndHeaderIgnored` — cả body JSON lẫn header giả mạo đều bị
  bỏ qua, response echo đúng principal server tự giữ.
- **role downgrade sau restart**: `TestPrincipal_ChangeOnlyTakesEffectOnFreshServer` (tầng httpapi, 2
  `Server` độc lập) + `TestServe_BootstrapUsesCustomPrincipalFromConfigFile` (tầng composition root, chứng
  minh đường thật từ file config tới bootstrap) — không có API nào trong `Server` cho phép đổi `Principal`
  khi đang chạy, chỉ đổi được bằng cách dựng `Server`/khởi động lại `serve` mới.
- **secret scan trên log**: `TestSecretScan_TokenNeverAppearsInLogOutput` (tầng httpapi, buffer log có thể
  kiểm soát) — assertion thật trên log output thật, không dựa vào redactor (matcher không đăng ký token làm
  secret trong test này, để không che giấu một leak thật nếu có).
- **secret scan trên DB**: `TestServe_SessionTokenNeverPersistedInSQLite` (tầng cmd/aw, SQLite thật) —
  quét toàn bộ table/column sau khi server đã tắt hẳn.
- **secret scan trên bootstrap cache**: `TestSecretScan_BootstrapHTMLNeverPutsTokenInAURLOrQueryString` — xác
  nhận token không nằm trong bất kỳ context giống URL nào trong chính response bootstrap (đây là nơi DUY NHẤT
  token được phép xuất hiện, nên "cache" ở đây nghĩa là response đó không được tự nó rò rỉ token ra URL).

### Kết quả

V6-01A hoàn thành trong 1 PR duy nhất. 4 file production mới (`internal/app/config/local_principal.go`,
`internal/delivery/httpapi/{principal,security,bootstrap}.go`), 2 file production sửa
(`internal/delivery/httpapi/server.go` thêm Token/Principal + middleware chain mới, `cmd/aw/serve.go`
thêm flag `--principal-config` + wiring token/principal/bootstrap route), 3 file test mới (34 test function
thật: 18 trong `security_test.go`, 12 trong `local_principal_test.go`, 4 trong `serve_principal_test.go`) cộng
2 chỗ sửa test cũ (`newTestServer`, `TestNewServer_AcceptsLoopbackIPAndLocalhost` — thêm Token/Principal) và
1 test mới trong file cũ (`TestNewServer_RequiresTokenAndPrincipal`). Một flake thật tự phát hiện và sửa tận
gốc (WAL-mode lock contention giữa 2 SQLite connection đồng thời trong chính test mới thêm), cộng một merge
conflict lớn hơn dự kiến do V6-15A đổi tên `cmd/agentkit` → `cmd/aw` merge trước — xử lý bằng dựng lại branch
từ `origin/master` mới nhất và áp lại patch riêng của task này (xác nhận trước nội dung file base không đổi
qua rename, nên không có xung đột logic). `go build ./...`, `go vet ./...`, `go test ./...` xanh 100% toàn bộ
module. Caller không còn cách nào tự khai actor/role qua HTTP hay CLI flag, và mọi mutation thiếu proof trình
duyệt (token đúng qua bootstrap loopback hợp lệ) bị chặn tại `HostOriginGuard`/`RequireSessionToken` trước khi
chạm tới `mux` — đúng "Hoàn thành khi" của chính task này. V6-02 (phụ thuộc trực tiếp V6-01A) có thể bắt đầu
ngay.

## V6-10C — Bounded workspace inspection application queries (PR #31)

### Bối cảnh

V6-10C là 1 trong 4 task P0 được phép chạy ngay sau V5 (`{V6-00, V6-00A, V6-01, V6-10C}`, chỉ phụ thuộc V5),
chạy song song với các session khác vì package hoàn toàn mới không ai đang chạm:
`internal/app/workspaceinspection` (mới), cộng mở rộng `internal/app/ports` và `internal/adapters/gitworktree`
theo đúng convention "narrow port riêng" đã có từ V5-10A (`LocalCommitCreator`). Mục tiêu design doc: public
`GetSource`, `GetDiff`, `GetRepositoryLog` — 3 named application query cho phép đọc nội dung repository/
workspace bounded, an toàn, TRƯỚC KHI V6-10D (task khác, không thuộc phạm vi phiên này) map chúng sang HTTP
GET route. Phạm vi khoá cứng: typed read-only port, project/revision/path authorization, bounded result DTO;
không làm HTTP, không OS path, không arbitrary ref expression/argv, không mutable/unpinned working tree, không
write.

### Nghiên cứu

Đọc `internal/app/ports/workspace.go` (`WorkspaceHandle` opaque — "application code must not derive an OS
path from a handle") và `internal/app/ports/localcommit.go` (`LocalCommitCreator` — mẫu "port hẹp declare
riêng thay vì mở rộng `WorkspaceProvider`", đúng lý do áp dụng lại cho `WorkspaceInspectionReader`).
Đọc `internal/adapters/gitworktree/provider.go` để tìm mẫu tự vệ traversal/symlink/option-injection đã có sẵn
từ V3: `ensureLexicallyWithin`/`canonicalExistingDirectory` cho path OS thật, và quan trọng nhất —
`resolveCommit` dùng `git rev-parse --verify --end-of-options <rev>^{commit}` để chặn một revision string bị
hiểu nhầm thành flag. Đọc `internal/adapters/gitworktree/localcommit.go` (mẫu narrow-port + real git
invocation `-c user.name=`/`-c user.email=` one-off, không ghi vào config vĩnh viễn).

Đọc `internal/app/ports/work.go`/`unitofwork.go` để tìm cách "reload workspace/repository/project" đúng
convention repo: không có `ProjectStore`/`RepositoryStore` riêng — mọi thứ đi qua `ports.Tx`
(`tx.Work().GetRepositoryWorkspaceByID` trả `RepositoryWorkspaceRecord{Workspace, FamilyID}`,
`tx.Catalog().GetRepository`/`GetProject` cho ownership chain). `internal/app/readinesscheck/handler.go` là
mẫu tham chiếu tốt nhất cho "load qua `uow.WithReadOnly` trước khi làm I/O thật ngoài transaction".

Kiểm tra `ports.QueryStore` (`internal/app/ports/unitofwork.go`) — đây là placeholder "real query methods
added by whichever task first builds a read handler" nhưng KHÔNG dùng được cho task này: `QueryStore` dành
cho projection/read-model tương lai (V6-08+), còn V6-10C đọc trực tiếp nội dung Git thật qua adapter, không
qua database — quyết định thêm port hoàn toàn riêng (`ports.WorkspaceInspectionReader`) thay vì đụng vào
`QueryStore`.

Phát hiện một bug thật khi viết test thật cho cursor của `ReadRepositoryLog`: `git rev-parse --end-of-options
<sha>^` MỘT MÌNH (không có `--verify`) không được git 2.55.0 hiểu là parseopt flag — git echo lại chính
`--end-of-options` như một revision argument và fail với "fatal: ambiguous argument". Xác nhận bằng một
script debug độc lập (`os/exec` trực tiếp, ngoài test) so sánh có/không `--verify`: chỉ khi có `--verify` thì
`--end-of-options` mới được nhận diện đúng — khớp chính xác với cách `resolveCommit` đã dùng cả hai flag cùng
nhau từ trước trong `provider.go`, một chi tiết bị bỏ sót khi viết `resolveLogCursor` lần đầu. Sửa lại dùng
`rev-parse --verify --end-of-options <cursor>^`.

### Quyết định

1. **Port hẹp mới, không mở rộng `WorkspaceProvider`.** `ports.WorkspaceInspectionReader` (3 method) khai báo
   trong file mới `internal/app/ports/workspaceinspection.go` — `gitworktree.Provider` tự động thoả interface
   qua structural typing, không cần sửa `var _ ports.WorkspaceProvider` đã có.
2. **"Authorized revision" = đúng Base hoặc Current, không phải arbitrary ref.** Từ chối tuyệt đối mọi ref
   string không phải chính xác object id hex 40/64 ký tự VÀ bằng đúng `BaseRevision`/`CurrentRevision` của
   `Inspect()` sống — kể cả khi ref đó là một commit THẬT, reachable trong cùng object database dùng chung
   giữa worktree và source repo (worktree share ODB với repo gốc). Test
   `TestReadSourceRejectsRealButUnauthorizedRevision`/`TestReadDiffRejectsRealButUnauthorizedRevision`/
   `TestReadRepositoryLogRejectsRealButUnauthorizedAnchor` dựng đúng kịch bản này: commit tạo trực tiếp trong
   source repo (không qua workspace), reachable, nhưng vẫn bị từ chối.
3. **`GetSource` không bao giờ chạm filesystem thật — chỉ `git ls-tree`/`cat-file <rev>:<path>`.** Nội dung
   file luôn resolve qua git object model, path chỉ cần validate cú pháp
   (`normalizeTreePath`: NUL byte, backslash, leading `/` hoặc `-`, `..`, không tự ý normalize hộ path lệch
   chuẩn). Symlink (mode 120000) và submodule gitlink (mode 160000) bị từ chối tường minh qua `git ls-tree`
   TRƯỚC KHI bao giờ chạy `cat-file -p` — tránh trả nội dung target-path của symlink như thể file thật.
4. **Cursor phải chứng minh là tổ tiên thật của anchor.** `git merge-base --is-ancestor <cursor> <anchor>`
   bắt buộc trước khi cho phép resume trang tiếp theo — một cursor giả mạo trỏ tới lịch sử khác (dù commit đó
   có thật trong ODB) bị từ chối (`TestReadRepositoryLogRejectsCursorOutsideAnchorAncestry`).
5. **Bounded streaming thật, không phải "đọc hết rồi cắt".** `runGitCapped` đọc qua
   `io.LimitReader(stdout, maxBytes+1)` rồi `Kill()` tiến trình con NGAY khi vượt giới hạn, thay vì buffer
   toàn bộ blob/patch/log output rồi mới cắt — dùng chung cho cả 3 query (blob content, diff patch, log
   page), tránh một blob/lịch sử khổng lồ chiếm bộ nhớ chỉ để lấy N byte đầu.
6. **App layer tự kiểm State, không tin riêng adapter.** `internal/app/workspaceinspection` tự thêm
   `ErrWorkspaceNotReady` khi persisted `RepositoryWorkspace.State != READY` — độc lập với check
   `WorkspaceReleased` mà adapter tự làm lại lần nữa qua `Inspect()` sống ngay bên dưới (defense-in-depth 2
   lớp, không lớp nào tin lớp kia).
7. **Lỗi scope mismatch không echo lại giá trị thật.** `ErrScopeMismatch` không bao giờ đưa Locator/path/owner
   thật vào message lỗi — đúng "no path leak" (Verify line của design doc), một caller đoán sai
   RepositoryWorkspaceID không học thêm được gì về workspace thật.

### Thực hiện

- `internal/app/ports/workspaceinspection.go` (181 dòng): interface `WorkspaceInspectionReader` + DTO
  `ReadSourceRequest`/`SourceContent`, `ReadDiffRequest`/`DiffContent`/`DiffFileChange`,
  `ReadRepositoryLogRequest`/`RepositoryLogPage`/`RepositoryLogEntry`.
- `internal/adapters/gitworktree/inspection.go` (593 dòng): `Provider.ReadSource`/`ReadDiff`/
  `ReadRepositoryLog` thật, cộng helper `authorizeRevision`, `lsTreeEntry`, `runGitCapped` (bounded streaming
  + kill-on-overflow), `normalizeTreePath`, `isBinaryContent` (heuristic NUL-byte trong 8000 byte đầu, giống
  chính git), `clampLines`, `parseNumstat` (`git diff --numstat -z --no-renames`), `parseLogRecords` (format
  `%H\x1f%P\x1f%an\x1f%ae\x1f%aI\x1f%s\x1e`, record/field separator là byte điều khiển không thể xuất hiện
  trong object id/timestamp — chỉ subject commit ác ý mới có thể chứa, và một record vỡ khi đó dừng parse +
  báo `Truncated` thay vì fabricate entry sai), `resolveLogCursor` (`merge-base --is-ancestor` +
  `rev-parse --verify --end-of-options`). Thêm `ErrUnsupportedEntry`/`ErrPathNotFound` vào
  `internal/adapters/gitworktree/errors.go`.
- `internal/app/workspaceinspection/queries.go` (251 dòng): `Queries.GetSource`/`GetDiff`/`GetRepositoryLog`,
  `resolveScope` (reload Project → Repository → WorkspaceSet → RepositoryWorkspace qua `uow.WithReadOnly` +
  `tx.Catalog()`/`tx.Work()`, chạy lại từ đầu mỗi lần gọi, không cache), clamp limit caller-supplied về
  `[default, max]` cho byte/line/file/commit thay vì lỗi khi caller không truyền hoặc truyền quá lớn.

### Test

- `internal/adapters/gitworktree/inspection_test.go`: 26 test function thật, không mock git command nào,
  chạy trên real temp git repo qua đúng helper đã có sẵn từ `provider_test.go`
  (`newTestProvider`/`createGitRepository`/`runTestGit`). Cover: exact blob tại Base/Current, byte-limit +
  line-limit typed truncation (assert đúng `len(Content)==ByteLimit`), binary detection, path traversal (8
  biến thể: `../`, `..\`, `/etc/passwd`, `a/../../b`, `a/./b`, rỗng, `-rf`, `a/`), symlink entry (tạo bằng
  `git update-index --add --cacheinfo 120000,<blob>,link.txt` — plumbing thuần, không cần OS symlink thật nên
  chạy được cả trên Windows), directory entry, path không tồn tại, arbitrary ref (`HEAD`, `main`, `HEAD~1`,
  rỗng, `--upload-pack=x`, short-sha), commit thật nhưng unauthorized, sai RepositoryID/Generation, released
  workspace, cancellation (context đã cancel trước khi gọi cho cả 3 method public, cộng test riêng cho
  `runGitCapped` xác nhận `errors.Is(err, context.Canceled)`), diff file-limit/byte-limit/binary-file, log
  pagination ổn định qua 3 trang (đối chiếu tổng số với `git rev-list --count` thật), log byte-limit
  truncation, log cursor ngoài ancestry.
- `internal/app/workspaceinspection/queries_test.go`: 15 test function, dùng `fake.UnitOfWork` (persistence
  in-memory thật của repo — KHÔNG mutate database trực tiếp, mọi seed đi qua `tx.Catalog().CreateProject`/
  `RegisterRepository`, `work.NewRootWorkItem`/`NewTaskFamily`/`workspace.NewWorkspaceSet`/
  `NewRepositoryWorkspace` — đúng domain constructor thật, chỉ gán tay `rw.State`/`rw.CurrentRevision` sau
  constructor giống hệt mẫu `workspaceprovision.Handler.finishReady` đã làm) cộng real `gitworktree.Provider`
  thật (không mock). Cover: happy path, default limit khi không truyền, mismatch từng phần riêng biệt
  (project/repository/workspace-set), unknown RepositoryWorkspaceID (`ErrPersistenceNotFound`), non-READY
  workspace (`ErrWorkspaceNotReady`), propagate lỗi thật từ adapter (arbitrary ref, path traversal,
  unauthorized revision) xuyên qua `errors.Is`, cancellation, GetDiff/GetRepositoryLog scope mismatch +
  pagination.
- `go build ./...`, `go vet ./...` sạch. `go test ./...` toàn bộ module (68 package, KHÔNG chỉ package mới)
  pass 100%. `internal/archtest` pass, đặc biệt `TestDomainAppNeverImportAdapters` xác nhận
  `internal/app/workspaceinspection` và `internal/app/ports` không import `internal/adapters/...` — dù test
  file của package đó import `gitworktree` trực tiếp để assert `errors.Is` lên sentinel thật, vì `go list`
  không dùng `-test` nên không quét test import, đúng quy ước sẵn có của mọi `*_sqlite_test.go` khác trong
  repo.

### Verify

- Design doc's own Verify line: "scope, arbitrary ref, traversal/reparse, binary/large, stable cursor,
  mismatch, cancellation; no path leak" — cả 8 vế đều có test thật tương ứng (liệt kê ở trên); "no path leak"
  cụ thể là `ErrScopeMismatch`/`ErrWorkspaceNotReady` không bao giờ format Locator/path/owner thật vào
  message lỗi.
- Option injection: revision luôn phải là hex object id (không thể bắt đầu bằng `-`); path cho `ls-tree` luôn
  truyền sau `--` như argv riêng, không bao giờ ghép chung token có thể bị hiểu thành flag.
- Không mutation: 3 method public chỉ chạy `{ls-tree, cat-file, diff, log, merge-base, rev-parse}` — không
  method nào của `ReadSource`/`ReadDiff`/`ReadRepositoryLog` gọi `worktree add/remove`, `commit`, hay bất kỳ
  lệnh nào đổi HEAD/working tree.
- "Hoàn thành khi": "named application queries bound output before delivery serialization" — `GetSource`/
  `GetDiff`/`GetRepositoryLog` trả thẳng `ports.SourceContent`/`DiffContent`/`RepositoryLogPage` đã bounded
  đầy đủ (byte/line/file/commit limit, typed Truncated/Binary), sẵn sàng serialize mà không cần V6-10D tự
  tính lại bound nào.

### Kết quả

PR #31 (branch `feat/v6-10c-workspace-inspection-queries`) — 2 file mới trong `internal/app/ports` và
`internal/adapters/gitworktree` cộng package hoàn toàn mới `internal/app/workspaceinspection/`. 41 test
function thật (26 adapter + 15 application), `go test ./...` xanh 100% trên toàn bộ 68 package. V6-10D (map
3 query này sang HTTP GET route) giờ có thể bắt đầu ngay khi dependency riêng của nó
(V6-00, V6-01A, V6-02A, V6-10B) sẵn sàng — không còn chờ gì thêm từ V6-10C.

## V6-15A — Composition root canonical `aw`

### Bối cảnh

V6-15A là 1 trong 3 task nhóm P1 được phép chạy ngay sau V6-01 (`sau V6-01: {V6-01A, V6-02A, V6-15A}`,
dependency graph mục 2 của `docs/design/08-v6-api-projections.md`) — V6-01 vừa merge (`d8c1323`, #30) nên
task này unblock. ADR-028 đã khoá quyết định từ trước: executable sản phẩm canonical là `aw`, không phải
`agentkit` — tên ban đầu vừa dài vừa dễ nhầm với binary bằng chứng V0 `agentkit-spike`. V6-15A là task duy
nhất thực thi đổi tên đó tại tầng composition root, trước khi bất kỳ HTTP black-box acceptance test nào
(V6-15P) khoá production composition cuối cùng.

Phạm vi khoá cứng theo đúng "Không làm" của chính task: không tạo alias `agentkit` (không giữ lại một
`cmd/agentkit` stub gọi vào `cmd/aw`), giữ nguyên `cmd/agentkit-spike` không đụng tới, không redesign leaf
CLI (grammar `aw <command> [flags]` giữ nguyên y hệt, chỉ đổi tên binary xuất hiện trong usage/error text).

### Nghiên cứu

Trước khi sửa, grep toàn bộ repo (không chỉ `cmd/`) cho `cmd/agentkit`, `"agentkit"` và biến thể để tự dựng
worklist thay vì đoán:

- `cmd/agentkit/` có đúng 9 file (`adapter.go`, `adapter_test.go`, `cli.go`, `cli_test.go`, `definition.go`,
  `definition_test.go`, `main.go`, `serve.go`, `serve_test.go`) — tất cả `package main`, không có sub-package
  nào khác cần đổi.
- Không có Makefile/justfile/Taskfile trong repo; `.github/workflows/` chỉ có một file (`spike-gate.yml`) và
  nó chỉ build/chạy `cmd/agentkit-spike`, `cmd/fake-claude`, `cmd/fake-codex`, `cmd/spike-worker`,
  `cmd/spike-helper` — không có step nào build riêng `cmd/agentkit` (mọi job chỉ gọi `go build/vet/test ./...`,
  glob này tự động nhặt `cmd/aw` sau khi đổi tên, không cần sửa YAML).
- Bên trong `cmd/agentkit/main.go`/`cli.go` có text thật hiển thị cho operator: package doc comment ("Command
  agentkit is..."), usage banner (`Usage: agentkit <command> [flags]`, `Run 'agentkit <command> -h'`), 2 dòng
  lỗi runtime (`"agentkit: unknown command %q"`, `"agentkit:", err`) — đây là phần "serve/worker/version/help
  wiring" chính task phải đổi, không phải chỉ đường dẫn thư mục. `cli_test.go` có 2 assertion khớp cứng chuỗi
  `"Usage: agentkit"` nên bắt buộc sửa theo cùng lúc, nếu không test tự vỡ sau khi đổi `cli.go`.
- 7 file NGOÀI `cmd/` có comment tham chiếu trực tiếp đường dẫn `cmd/agentkit` (mô tả file/composition root cụ
  thể, sẽ sai sau khi move): `internal/adapters/providers/internal/versionprobe/versionprobe.go`,
  `internal/app/adapterbuild/drift.go`, `internal/adapters/sqlite/migration_0006_test.go`,
  `internal/app/runtime/agent_node_executor.go`, `internal/integration/foundation_test.go`,
  `internal/integration/definitionplane_test.go` (ví dụ CLI `agentkit adapter probe|register`), và
  `internal/adapters/sqlite/migrations/0006_repository_status_components.sql`.
- Toàn bộ `docs/architecture/04-go-core-spec.md`, `docs/design/01-system-design.md` đã sẵn ghi `cmd/aw/` là
  composition root đích và `agentkit-spike` là binary tách riêng — đây là spec baseline "north star", không
  cần sửa. `docs/architecture/02-architecture-decisions.md` ADR-028 và `docs/design/08-v6-api-projections.md`
  (chính task này) mô tả quyết định/kế hoạch đổi tên bằng thì hiện tại — giữ nguyên, không viết lại sau khi
  làm xong, đúng convention đã thấy ở các task V2 khác (`docs/design/04-v2-definition-plane.md` V2-07B/V2-11
  vẫn giữ nguyên text `agentkit adapter ...`/`agentkit definition ...` dù đã đổi tên, vì dòng 10 của chính file
  đó tự ghi "Ghi chú lịch sử tên lệnh" — spec/task doc là lịch sử bất biến, không phải tracker sống).
- Phân biệt rõ các chuỗi `agentkit-*` KHÔNG liên quan đến binary và cố tình KHÔNG đổi: tên file SQLite tạm
  trong test (`agentkit-*.db`, hàng trăm chỗ khắp `internal/adapters/sqlite`, `internal/app/*`,
  `internal/integration`), biến môi trường helper subprocess (`AGENTKIT_HELPER_MODE`,
  `AGENTKIT_PROVIDER_HELPER`, ...), custom media type (`application/vnd.agentkit.*+json`), marker giao thức
  agent output (`<agentkit-outcome>`), prefix branch/workspace Git (`agentkit/w-...`, `agentkit/family-...`) —
  tất cả là namespace nội bộ dùng chung chữ "agentkit" nhưng không phải tham chiếu tới binary/thư mục
  `cmd/agentkit`; đổi các chỗ này là hành vi thật (protocol/naming), vượt phạm vi "no leaf CLI redesign" của
  chính task.

### Quyết định

1. Dùng `git mv cmd/agentkit cmd/aw` (không xoá-tạo-lại) để diff hiện đúng là rename, giữ file history.
2. Trong 9 file vừa move: chỉ sửa các chuỗi hiển thị/tự-tham-chiếu thật ("Command agentkit", usage banner,
   error prefix, comment tự trỏ tới đường dẫn `cmd/agentkit`) — không đổi bất kỳ logic/behavior/flag nào khác,
   giữ đúng "no leaf CLI redesign". Không đổi tên file tạm `agentkit-*.db` trong 3 test file
   (`adapter_test.go`, `definition_test.go`, `serve_test.go`) — nhất quán với quyết định không đụng namespace
   `agentkit-*` nội bộ ở trên.
3. 7 file ngoài `cmd/` chỉ sửa đúng chuỗi con `cmd/agentkit` → `cmd/aw` bên trong comment, giữ nguyên phần còn
   lại của câu (kể cả khi câu đó có chỗ đã cũ như "serve/worker/doctor/definition are all still stubs" —
   không thuộc phạm vi mechanical rename, không tự ý viết lại).
4. Thêm một test kiến trúc thật `internal/archtest/composition_root_test.go`
   (`TestCompositionRootIsCmdAwNotCmdAgentkit`), mirror đúng `findModuleRoot` helper có sẵn từ
   `boundary_test.go` trong cùng package: assert `cmd/aw` tồn tại (có `main.go`), `cmd/agentkit` KHÔNG tồn tại,
   `cmd/agentkit-spike` vẫn tồn tại nguyên vẹn — để một PR tương lai lỡ tạo lại `cmd/agentkit` hoặc xoá nhầm
   spike sẽ fail CI ngay, không phải một audit thủ công.

### Thực hiện

- `git mv cmd/agentkit cmd/aw` — diff hiện `R`/`RM` (rename, một vài file có nội dung đổi kèm theo).
- `cmd/aw/main.go`: "Command agentkit is..." → "Command aw is...".
- `cmd/aw/cli.go`: usage banner "Usage: agentkit ..." → "Usage: aw ...", "Run 'agentkit <command> -h'" →
  "Run 'aw <command> -h'", `"agentkit: unknown command %q\n\n%s"` → `"aw: unknown command %q\n\n%s"`,
  `"agentkit:", err` → `"aw:", err`. Dòng comment nhắc tới `agentkit-spike evidence verify` giữ nguyên.
- `cmd/aw/cli_test.go`: 2 assertion `strings.Contains(..., "Usage: agentkit")` → `"Usage: aw"`.
- `cmd/aw/serve_test.go`: comment "as `agentkit serve` would" → "as `aw serve` would".
- `cmd/aw/adapter.go`: comment tự trỏ "this file is the first place in cmd/agentkit that" → "cmd/aw".
- 7 file ngoài `cmd/`: `internal/adapters/providers/internal/versionprobe/versionprobe.go`,
  `internal/app/adapterbuild/drift.go`, `internal/adapters/sqlite/migration_0006_test.go`,
  `internal/app/runtime/agent_node_executor.go`, `internal/integration/foundation_test.go`,
  `internal/integration/definitionplane_test.go`, `internal/adapters/sqlite/migrations/
  0006_repository_status_components.sql` — mỗi file đổi đúng chuỗi con `cmd/agentkit`/`agentkit adapter` sang
  `cmd/aw`/`aw adapter` trong comment liên quan.
- `internal/archtest/composition_root_test.go` (file mới): `TestCompositionRootIsCmdAwNotCmdAgentkit` —
  `os.Stat` trực tiếp trên `cmd/aw` (phải tồn tại + có `main.go`), `cmd/agentkit` (phải `os.IsNotExist`),
  `cmd/agentkit-spike` (phải tồn tại + là directory).
- Không sửa `.github/workflows/spike-gate.yml` — xác nhận không có step nào build riêng `cmd/agentkit`, mọi
  job dùng `go build/vet/test ./...` tự nhặt `cmd/aw` sau khi đổi tên.
- Không sửa `docs/architecture/02-architecture-decisions.md` (ADR-028), `docs/design/08-v6-api-projections.md`
  (chính task này), `docs/design/03-v1-alpha-foundation.md`, `docs/design/04-v2-definition-plane.md` — các
  file này mô tả quyết định/lịch sử tại thời điểm viết, tự ghi rõ là ghi chú lịch sử hoặc là spec/task doc bất
  biến, không phải trạng thái sống cần đồng bộ theo code hiện tại.

### Test

- `go build ./...` sạch toàn bộ module sau khi move + sửa reference.
- `go build -o <tmp> ./cmd/aw` — thành công (composition root mới build được).
- `go build ./cmd/agentkit` — fail đúng như kỳ vọng: `stat .../cmd/agentkit: directory not found` (đường dẫn
  cũ đã biến mất thật, không phải alias rỗng).
- `go build -o <tmp> ./cmd/agentkit-spike` — thành công không đổi (binary V0 không bị đụng).
- `go vet ./...` sạch.
- `go test ./cmd/aw/... ./internal/archtest/... -count=1 -v` — toàn bộ pass, gồm `TestServe_
  StartsServesHealthAndShutsDownGracefully` (smoke test start/stop server thật của chính V6-01, giờ chạy tại
  vị trí mới `cmd/aw`, không đổi nội dung), `TestRun_ServeRejectsUnknownFlag`, và test kiến trúc mới
  `TestCompositionRootIsCmdAwNotCmdAgentkit`.
- `go test ./... -count=1` — toàn bộ module (70 package tính cả `cmd/aw` mới và `internal/archtest`) pass
  100%, 0 dòng `FAIL`. Không có regression ở bất kỳ package nào khác — xác nhận rename không đụng semantics.

### Verify

- Verify line của chính task: "`go build ./cmd/aw`" — pass. "old production path absent" — `go build
  ./cmd/agentkit` fail với lỗi thư mục không tồn tại, cộng `TestCompositionRootIsCmdAwNotCmdAgentkit` khoá lại
  bằng CI thật thay vì chỉ một lần chạy tay. "spike builds" — `go build ./cmd/agentkit-spike` pass không đổi.
  "serve/worker startup/shutdown smoke" — `TestServe_StartsServesHealthAndShutsDownGracefully` (di chuyển
  nguyên vẹn từ V6-01, chạy tại `cmd/aw`) chứng minh server thật bind/serve health/shutdown graceful đúng như
  trước khi đổi tên.
- "Không làm": xác nhận không có alias `agentkit` nào được tạo (không còn thư mục `cmd/agentkit` dưới bất kỳ
  hình thức nào, kể cả stub gọi sang `cmd/aw`); `cmd/agentkit-spike` không bị sửa nội dung, chỉ xuất hiện
  trong comment không đổi; không có thay đổi grammar/flag nào trong `cmd/aw/cli.go` ngoài chuỗi tên binary.
- "Hoàn thành khi": không còn "second root" nào — `cmd/aw` là composition root sản xuất duy nhất trong repo,
  sẵn sàng cho HTTP black-box acceptance test cuối V6 (V6-15P) khoá lại production composition.

### Kết quả

`cmd/agentkit` → `cmd/aw` bằng `git mv` thật (giữ file history), 9 file bên trong sửa đúng phần text hiển thị
cho operator (package doc, usage banner, 2 dòng lỗi runtime, 2 assertion test tương ứng), 7 file ngoài `cmd/`
sửa comment tự tham chiếu đường dẫn cũ, và 1 test kiến trúc mới (`internal/archtest/composition_root_test.go`)
khoá cứng bất biến "chỉ một composition root, tên `aw`, `agentkit-spike` không đổi" bằng CI thật. `go build/
vet/test ./...` xanh 100% trên toàn bộ module, 3 assertion Verify (`go build ./cmd/aw` pass, `go build
./cmd/agentkit` fail, `go build ./cmd/agentkit-spike` pass) đều xác nhận bằng lệnh thật, không suy đoán. V6-15B
(nền tảng CLI dùng chung) giờ có đủ dependency `V6-15A` để bắt đầu ngay khi `V6-02`/`V6-02A` cũng sẵn sàng.
