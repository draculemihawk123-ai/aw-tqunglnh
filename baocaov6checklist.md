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

## V6-02A — Shared HTTP DTO, cursor và schema-fragment contract

### Bối cảnh

V6-01 (PR #30, `d8c1323`) vừa merge — dependency duy nhất của V6-02A đã pass. Theo dependency graph mục 2 của
`08-v6-api-projections.md`, nhóm P1 "sau V6-01" gồm `{V6-01A, V6-02A, V6-15A}` chạy song song vì sở hữu file
riêng; task này tự làm trực tiếp (không qua subagent) vì cùng lý do V6-01 tự làm: đây là contract nền mọi
task endpoint sau này (V6-03A trở đi, hơn chục task) import và tái dùng, sai ở đây dội ngược lên toàn bộ
V6-03…V6-11. Mục tiêu chính xác theo task spec (trích nguyên văn, không diễn giải lại): "endpoint song song
dùng cùng error/query/action/stream vocabulary và tự cung cấp schema fragment." Phạm vi khoá cứng: error
envelope, page/limit, opaque cursor, `Freshness`, `ValidAction`, range/media và SSE envelope. Không làm:
không compose root router/OpenAPI, không định nghĩa domain transition.

### Nghiên cứu

Đọc toàn bộ `internal/delivery/httpapi` (5 file production V6-01 đã dựng) trước khi viết bất cứ gì:
`route.go` (`RouteDescriptor`/`RouteRegistry.Register` — đã panic khi trùng `(Method, Path)` hoặc thiếu field,
nhưng CHƯA có check trùng `OperationID` giữa 2 route khác path — đúng như prompt đã cảnh báo trước, xác nhận
lại bằng cách đọc code thật chứ không tin lời cảnh báo), `health.go` (có sẵn `writeJSON` helper unexported,
tái dùng được cho error envelope thay vì viết lại), `json.go` (`ErrBodyTooLarge`/`ErrMalformedJSON` đã typed,
cần một hàm map 2 lỗi này sang envelope chung), `server.go` (chưa có gì liên quan cursor/error, không cần sửa).

Đọc hết phần còn lại của `08-v6-api-projections.md` để lấy đúng ngữ nghĩa cursor/freshness thật (không chỉ
đoán từ đoạn spec ngắn của chính V6-02A) như prompt yêu cầu: V6-08 ("rows key `(ProjectID, ProjectionName,
Generation, EntityKey)`", "Cursor is greatest scanned global JournalPosition"), V6-08A ("verifies active
generation/fence/cursor", "separate tx records poison and DEGRADED/STALE at last-good cursor"), V6-09A
("Reader sees old or new; cursor bound old generation returns resync" — xác nhận đúng "generation swap resync"
là kịch bản V6-09A tạo ra, V6-02A chỉ cung cấp cơ chế phát hiện), V6-10 ("projection lag/status; authoritative
service recomputes valid actions with target version before response" — xác nhận ValidAction chỉ advisory),
V6-11 ("Event ID is relevant global JournalPosition", "Heartbeat has no event ID and never advances cursor",
"Too-old cursor returns typed full-resync").

Grep toàn repo tìm tiền lệ "not found vs unauthorized" leakage policy: KHÔNG có — `errorcode.Code` (22 giá trị,
`internal/domain/errorcode/errorcode.go`) có `CodePolicyDenied`/`CodeScopeViolation` nhưng đây là domain
concept khác (workspace write-scope violation của `scopeguard.ErrScopeViolation`, không liên quan gì đến HTTP
resource-visibility). Không có type `Forbidden`/`Unauthorized` nào tồn tại trong `internal/app`. Xác nhận: đây
đúng là công việc CỦA task này tự định nghĩa lần đầu, không phải bug bỏ sót ở đâu đó.

Đọc `internal/domain/adapterbuild/token.go` (`CandidateToken`/`SignToken`/`VerifyToken`, ADR-022) làm mẫu
tham chiếu cho cursor: HMAC-SHA256 trên canonical payload, `hmac.Equal` chống timing attack, một lỗi typed
duy nhất (`ErrInvalidSignature`) cho mọi kiểu forge/corrupt — quyết định KHÔNG tái dùng trực tiếp package này
(nó thuộc domain/adapterbuild, ngữ nghĩa CandidateTuple riêng của ADR-022, không phải cursor chung), chỉ mượn
đúng kỷ luật "HMAC + `hmac.Equal` + 1 lỗi typed duy nhất". Cũng cân nhắc rồi bỏ `internal/domain/authoring.
Canonicalize` (map-sort canonical JSON dùng cho semantic hash command) — không cần cho cursor vì cursor tự
control cả 2 đầu encode/decode bằng cùng 1 struct cố định field order, `encoding/json.Marshal` trên struct đã
tự nhiên deterministic, không cần bộ máy canonicalize phức tạp hơn; dùng thêm sẽ kéo `internal/delivery/httpapi`
phụ thuộc vào một package domain không thật sự liên quan.

Đọc `internal/archtest/boundary_test.go`: `TestDomainAppNeverImportAdapters` chỉ chặn `internal/domain`/
`internal/app` import `internal/adapters` — không có rule nào chặn `internal/delivery` import `internal/domain`
hay `internal/app`, nên `errors.go` tự do import `internal/app/apperror` + `internal/domain/errorcode` mà
không vi phạm boundary nào.

Đọc `internal/app/catalog/event_schema_test.go` làm mẫu "golden fixture" chuẩn của repo: đọc file JSON qua
`os.ReadFile`, decode rồi so sánh struct — KHÔNG so byte-for-byte JSON output (không dùng `MarshalIndent` rồi
diff) — áp dụng đúng mẫu này cho error/freshness/action; riêng SSE (không phải JSON thuần, là wire-format
text) dùng golden byte-for-byte vì đó chính là điều cần đông cứng.

### Quyết định

1. **7 file mới, mỗi khối 1 file** — đúng nguyên tắc "mỗi endpoint/task sở hữu file riêng" (Contract chung mục
   1.8): `errors.go` (error envelope + leakage + apperror mapping), `page.go` (limit bound), `cursor.go`
   (`CursorState`/`CursorCodec`/`Bind`/`ResyncError`), `freshness.go`, `action.go`, `media.go` (Range +
   Content-Disposition), `sse.go`. `route.go` được EXTEND (không file mới) vì OperationID uniqueness là một
   check bổ sung ngay trong `Register` đã có, tách file riêng sẽ chia cắt logic liên quan.

2. **`ErrorCode` là vocabulary RIÊNG của httpapi, nhỏ hơn hẳn `errorcode.Code`.** Cân nhắc dùng thẳng
   `errorcode.Code` (22 giá trị) làm wire code luôn — bỏ vì client HTTP không cần phân biệt hết 22 domain
   condition, chỉ cần biết "retry được không, conflict, not-found, forbidden hay hard failure"; độ chi tiết còn
   lại nằm ở HTTP status + Message. Chốt 7 giá trị: `INVALID_REQUEST`, `NOT_FOUND`, `FORBIDDEN`, `CONFLICT`,
   `RESYNC_REQUIRED`, `UNAVAILABLE`, `INTERNAL`. `StatusForAppErrorCode` map đủ cả 22 `errorcode.Code` sang
   (status, wire code) — bảng generic; document rõ trong comment rằng bảng này KHÔNG được dùng cho nhánh
   not-found/unauthorized của một scoped resource lookup (nhánh đó luôn gọi thẳng `WriteResourceHidden`), vì
   bảng generic không có cách nào biết một call site có phải leakage-sensitive hay không.

3. **Leakage normalization = một hàm funnel duy nhất, không phải một rule để nhớ tự áp dụng.**
   `WriteResourceHidden(w)` là hàm KHÔNG NHẬN tham số nào phân biệt lý do — cả nhánh "not found" và nhánh
   "unauthorized for this scope" của một future handler đều gọi đúng hàm này, đảm bảo response byte-for-byte
   giống nhau bằng kiến trúc (không thể tự ý khác nhau dù người viết endpoint sau này có cố ý hay vô ý), thay
   vì 2 response riêng rồi tự nhắc nhau "nhớ phải giống nhau." Test trung tâm
   (`TestWriteResourceHidden_NotFoundAndUnauthorizedProduceIdenticalResponse`) giả lập đúng 2 code path độc
   lập gọi cùng hàm, so cả status/body/Content-Type.

4. **Cursor: tamper (signature sai) và resync (state hợp lệ nhưng stale) là 2 lỗi khác nhau, không gộp.**
   `ErrCursorInvalid` (sentinel `errors.New`, mirror discipline của `ErrBodyTooLarge`/`ErrMalformedJSON`) cho
   mọi lỗi base64/JSON/signature — remedy giống nhau: cursor này server chưa từng ký, từ chối thẳng, HTTP 400.
   `*ResyncError{Reason}` (typed struct, không phải sentinel, vì cần mang thêm `ResyncReason` — 3 giá trị
   `PROJECT_MISMATCH`/`QUERY_CHANGED`/`GENERATION_CHANGED`) cho cursor ký đúng, verify qua, nhưng không còn
   khớp project/query/generation hiện tại — remedy khác: client phải bắt đầu lại walk, không phải bị coi là
   tấn công, HTTP 409 riêng biệt với message rõ ràng "restart pagination".

5. **`Bind` không so `UpperWatermark`/`LastKey`.** Cân nhắc rồi bỏ: 2 field này mô tả cursor tự resume từ đâu
   (input cho query kế tiếp), không phải điều kiện để kiểm cursor còn hợp lệ hay không — so sánh chúng sẽ vô
   nghĩa (chúng luôn "khác" giữa request hiện tại chưa có `want` tương ứng). Chỉ so đúng 3 field xác định
   "cursor này có còn áp dụng được cho ngữ cảnh hiện tại": `ProjectID`, `QueryFingerprint`, `Generation` — theo
   đúng thứ tự cố định (project trước, rồi query, rồi generation) để một cursor sai nhiều chỗ luôn báo cùng 1
   lý do xác định, không phụ thuộc thứ tự map/struct field ngẫu nhiên.

6. **`ResolveLimit`: rỗng → default (im lặng), quá `MaxPageLimit` → clamp (im lặng), ≤0 hoặc không phải số →
   lỗi (ồn ào).** Cân nhắc clamp luôn cả giá trị âm/0 về default — bỏ, vì một giá trị `limit=-5` tường minh là
   lỗi client đáng báo, khác hẳn "client không truyền gì" (default hợp lý) hay "client xin nhiều hơn giới hạn
   cho phép" (bound tự bảo vệ, không phải input sai). `MaxPageLimit=200`/`DefaultPageLimit=50` là con số chọn
   hợp lý cho Alpha, không có yêu cầu cụ thể nào trong doc — ghi rõ đây là quyết định implementation-detail, dễ
   đổi sau nếu một task endpoint cụ thể cần khác.

7. **`NewCursorCodec` panic khi secret rỗng, không trả error.** Mirror đúng kỷ luật `RouteRegistry.Register`/
   `ReadinessChecker.Register` đã có: đây là lỗi composition-root lúc wiring, không phải điều kiện runtime một
   caller cần xử lý duyên dáng. Secret injection (crypto/rand mỗi process, mirror V6-01A's per-start token) là
   việc của composition root — task nào đầu tiên phát hành cursor thật, KHÔNG phải việc của package chia sẻ
   này (đúng "Không làm: không compose root router").

### Thực hiện

- `internal/delivery/httpapi/errors.go`: `ErrorCode` (7 giá trị), `ErrorDetail`, `ErrorBody`, `ErrorResponse`,
  `WriteError` (hàm funnel duy nhất mọi WriteXxx khác gọi qua), `ErrResourceHidden`/`WriteResourceHidden`
  (leakage policy), `WriteDecodeError` (map `ErrBodyTooLarge`→413, `ErrMalformedJSON`→400), `WriteResyncRequired`
  (409 + `Details[0]={Field:"cursor", Message:<reason>}`), `WriteCursorInvalid` (400),
  `StatusForAppErrorCode` (bảng đủ 22 `errorcode.Code`), `WriteAppError` (dùng `errors.As` lấy `*apperror.Error`,
  không bao giờ lộ raw text của lỗi không-phải-apperror).
- `internal/delivery/httpapi/page.go`: `DefaultPageLimit=50`, `MaxPageLimit=200`, `ErrInvalidLimit`,
  `ResolveLimit(raw string) (int, error)`.
- `internal/delivery/httpapi/cursor.go`: `CursorState{ProjectID, QueryFingerprint, Generation, UpperWatermark,
  LastKey}` (json tag camelCase), `cursorEnvelope{Payload, Signature}` (unexported), `ErrCursorInvalid`,
  `CursorCodec{secret}` + `NewCursorCodec`/`Encode`/`Decode`/`sign` (HMAC-SHA256, base64 RawURLEncoding,
  `hmac.Equal`), `Fingerprint(query any) (string, error)` (sha256 trên `json.Marshal` — caller truyền struct để
  field order deterministic), `ResyncReason` (3 giá trị), `ResyncError{Reason}` + `Error()`, `Bind(state, want)`.
- `internal/delivery/httpapi/freshness.go`: `FreshnessStatus` (`LIVE`/`DEGRADED`/`STALE`), `Freshness{Generation,
  AsOfJournalPosition, Status}`.
- `internal/delivery/httpapi/action.go`: `ValidAction{OperationID, ScopeKind, TargetVersion}` — tái dùng thẳng
  `ScopeKind` đã có từ `route.go` (V6-01), không định nghĩa lại.
- `internal/delivery/httpapi/media.go`: `ByteRange{Start, End}` + `Length()`, `ErrRangeNotSatisfiable`,
  `ParseRange(header, totalLength) (ByteRange, bool, error)` (hỗ trợ `start-end`/`start-`/`-suffix`, từ chối
  multi-range và mọi range ngoài `[0, totalLength)`), `ApplyPartialContentHeaders`, `WriteRangeNotSatisfiable`
  (416 + `Content-Range: bytes */<len>`), `MediaDisposition` (`inline`/`attachment`), `inlineSafeContentTypes`
  (allow-list đóng: text/plain, text/csv, application/json, image/{png,jpeg,gif}, application/pdf — KHÔNG có
  text/html/image/svg+xml), `ResolveMediaDisposition`, `ApplyContentHeaders` (set `X-Content-Type-Options:
  nosniff` cộng `Content-Disposition` đúng theo allow-list).
- `internal/delivery/httpapi/sse.go`: `SSEMessage{ID, Event, Data}`, `SSEHeaders` (Content-Type/Cache-Control/
  Connection/X-Accel-Buffering), `WriteSSE` (encode id/event/data theo đúng thứ tự, flush ngay, `flusher` được
  phép nil cho test), `WriteSSEComment` (heartbeat — không có field id/event nào, đúng "Heartbeat has no event
  ID and never advances cursor").
- `internal/delivery/httpapi/route.go`: thêm field `operationIDs map[string]routeKey` vào `RouteRegistry`,
  `Register` panic khi `OperationID` đã dùng cho một `(Method, Path)` khác — đóng đúng gap prompt đã cảnh báo
  trước (V6-01 chỉ dedupe theo `(Method, Path)`).

### Test

- 7 file test mới (`errors_test.go`, `cursor_test.go`, `page_test.go`, `freshness_test.go`, `media_test.go`,
  `sse_test.go`) cộng 1 test thêm vào `route_test.go` — tổng 55 test function mới, tất cả pass, không mock gì
  (thuần logic + `httptest.NewRecorder`).
- Cursor round-trip/tamper: `TestCursorCodec_EncodeDecode_RoundTrips`,
  `TestCursorCodec_Decode_MalformedTokenRejected` (base64 hỏng, JSON hỏng, chuỗi rỗng),
  `TestCursorCodec_Decode_TamperedPayloadRejected` (bài test tamper THẬT: decode token thật ra, sửa
  `projectId` trong `Payload` thành project khác — KHÔNG biết secret — rồi encode lại với `Signature` cũ, xác
  nhận `Decode` từ chối), `TestCursorCodec_Decode_WrongSecretRejected` (2 `CursorCodec` khác secret),
  `TestNewCursorCodec_EmptySecretPanics`.
- Bounded defaults/max: `TestResolveLimit_EmptyDefaultsToDefaultPageLimit`,
  `TestResolveLimit_AboveMaxClampsToMaxPageLimit`, `TestResolveLimit_ZeroOrNegativeReturnsErrInvalidLimit`,
  `TestResolveLimit_NonNumericReturnsErrInvalidLimit`, `TestResolveLimit_ExactlyMaxPageLimitReturnsAsIs`.
- Stable paging qua write: `TestCursorPaging_ConcurrentWriteBetweenPages_StaysStable` — dựng 4 row
  `{a,b,c,d}` (Position 1-4), lấy trang 1 (`{a,b}`, watermark=4), MÃ HOÁ cursor thật qua `CursorCodec`, rồi mới
  chèn thêm row `"bb"` (Position=5, nằm lexically giữa `b` và `c`) mô phỏng concurrent write. Trang 2 GIẢI MÃ
  lại cursor thật (không dùng biến closure), dùng đúng `state.UpperWatermark`/`state.LastKey` vừa round-trip
  để fetch — xác nhận `"bb"` không lọt vào trang 2 dù key của nó lẽ ra nằm trong khoảng chưa đọc, và
  `page1+page2` đúng bằng 4 row gốc, không thiếu không trùng. Đây là bằng chứng UpperWatermark/LastKey tự
  ENCODE/DECODE qua HMAC thật, không phải một biến test giả định suông.
- Generation swap resync: `TestBind_GenerationMismatch_ReturnsResyncError` (test riêng, tách khỏi test paging
  ở trên) cộng `TestBind_ProjectMismatch_...`/`TestBind_QueryFingerprintMismatch_...` cho 2 lý do resync còn
  lại, `TestBind_MatchingStateReturnsNil` cho trường hợp khớp.
- Duplicate operationId/schema omission fail:
  `TestRouteRegistry_Register_DuplicateOperationIDAcrossDifferentPathsPanics` (mới, đóng gap thật) — schema
  omission đã có sẵn từ V6-01 (`TestRouteRegistry_Register_MissingRequiredFieldPanics`), không viết lại.
- Shared error/freshness/SSE golden: `testdata/golden/error_response_v1.json`, `freshness_v1.json`,
  `valid_action_v1.json` (JSON, decode-and-compare mirror đúng mẫu `catalog/event_schema_test.go`) cộng
  `sse_message_v1.txt` (wire-format text, so byte-for-byte thật qua `WriteSSE` — struct field order cố định,
  KHÔNG dùng map, vì `encoding/json` sort key map theo alphabet còn struct giữ đúng thứ tự khai báo — phát
  hiện lúc viết test đầu tiên dùng map làm golden lệch thứ tự `status`/`workItemId`, sửa lại dùng struct).
- Leakage: `TestWriteResourceHidden_NotFoundAndUnauthorizedProduceIdenticalResponse` — assert cả status, body
  string, Content-Type header giống hệt giữa 2 lần gọi độc lập.
- No-sniff/download: `TestResolveMediaDisposition_ScriptCapableTypesAreForcedToDownload` (text/html có/không
  charset param, image/svg+xml, application/xhtml+xml đều phải `attachment`).
- Range bounded/safe: 13 test trong `media_test.go` — start vượt content length, end vượt bị clamp, reversed
  range, multi-range, thiếu prefix `bytes=`, content length 0 — đều `ErrRangeNotSatisfiable`.
- `apperror`/`errorcode` mapping: `TestStatusForAppErrorCode_MapsKnownCodes` bảng 17 case phủ đủ mọi nhóm
  (bad request/not found/conflict-family/forbidden/unavailable/internal) cộng 1 case code lạ chưa từng định
  nghĩa (`SOME_FUTURE_UNKNOWN_CODE`) xác nhận default an toàn về `INTERNAL`/500, không panic không bỏ sót.
- `go build ./...`, `go vet ./...` sạch. `go test ./internal/delivery/httpapi/...` 100% pass (đã chạy trước
  full suite). `go test ./...` toàn bộ module chạy sau, xanh 100% — không phát hiện flake mới, không đụng
  package nào khác ngoài `internal/delivery/httpapi`.
- **CI job `contract (windows-latest)` bắt được một bug thật sau khi push** (không phải flake đã biết trước —
  kiểm tra kỹ trước khi kết luận, đúng doctrine): `TestWriteSSE_MatchesGoldenWireFormat` fail chỉ trên
  windows-latest, pass trên ubuntu-latest — cùng gốc rễ `.gitattributes` đã tự ghi chú trước đó cho golden
  JSON (V1-11 từng gặp y hệt): `testdata/golden/sse_message_v1.txt` không có rule `eol=lf`, nên checkout trên
  Windows normalize thành CRLF, trong khi `WriteSSE` luôn emit thuần LF (`"id: ...\nevent: ...\n\n"`) — golden
  fixture và output thật lệch line ending. Sửa bằng thêm `*.txt text eol=lf` vào `.gitattributes` (mirror
  đúng rule `*.json` đã có, kèm comment giải thích) — không sửa code, không sửa test, vì cả 2 đều đúng, chỉ
  checkout bị sai. Xác nhận repo hiện không có file `.txt` nào khác bị ảnh hưởng ngoài ý muốn
  (`git ls-files "*.txt"` chỉ trả đúng 1 file). Đây đúng là bug thật do task này tạo ra (file mới, thiếu
  gitattributes rule tương ứng), không phải flake ngẫu nhiên — sửa xong, push lại, CI windows-latest phải
  chạy lại từ đầu.

### Verify

- "round-trip/tamper cursor": `TestCursorCodec_EncodeDecode_RoundTrips` +
  `TestCursorCodec_Decode_TamperedPayloadRejected`/`WrongSecretRejected`/`MalformedTokenRejected`.
- "bounded defaults/max": `TestResolveLimit_*` (5 test, liệt kê ở trên).
- "stable paging qua write": `TestCursorPaging_ConcurrentWriteBetweenPages_StaysStable`.
- "generation swap resync": `TestBind_GenerationMismatch_ReturnsResyncError`.
- "duplicate operationId/schema omission fail": operationId — test mới; schema omission — đã có từ V6-01,
  không duplicate.
- "shared error/freshness/SSE golden": 4 golden fixture (`error_response_v1.json`, `freshness_v1.json`,
  `valid_action_v1.json` — bonus, không bắt buộc theo verify line nhưng cùng họ DTO nên thêm cho nhất quán —
  `sse_message_v1.txt`), mỗi fixture có cả test decode-fixture và test round-trip-qua-marshal.
- "Hoàn thành khi": "endpoint task không phải tự quyết DTO/cursor/error/action convention" — mọi type/hàm ở
  trên export công khai, có doc comment trỏ thẳng về đúng dòng design-doc liên quan (V6-08/V6-08A/V6-09A/V6-10/
  V6-11), một task endpoint tương lai (V6-03A trở đi) chỉ cần import và gọi, không cần tự nghĩ lại shape.

### Kết quả

PR #33 (branch `feat/v6-02a-shared-http-contract`), merged `36c5287`. CI 6/6 xanh sau khi tự phát hiện và sửa
1 bug thật: golden fixture mới `sse_message_v1.txt` thiếu rule `.gitattributes` LF, gây fail trên
windows-latest do CRLF — cùng nguyên nhân rule `*.json` đã có sẵn (tiền lệ V1-11), thêm `*.txt text eol=lf`.

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

## V6-10G — Versioned safe-settings authority

### Bối cảnh

V6-10G chạy song song với V6-10E và V6-10I (3 task đều unblock ngay sau V6-00A, cùng dependency graph
`docs/design/08-v6-api-projections.md` mục 2), mỗi task một worktree riêng, không đụng chung file production
— chỉ `baocaov6checklist.md` là điểm giao (append-only, xử lý merge như mọi lần trước). Task tự mô tả là task
"nền tảng trước endpoint": "Mục tiêu: define persistence, precedence and desired/effective behavior before
endpoint work" — output không phải HTTP route (đó là V6-10H, chưa unblock vì còn phụ thuộc V6-02/V6-02A) mà là
một lớp cấu hình THỨ HAI, độc lập với `internal/app/config.Config` (V1-03, immutable per-process, `defaults <
file < env < flags`): một tập 7 field đóng (workspace root, artifact root, evidence/raw-output retention,
process output limit, provider executable path, provider default model, provider credential-reference ID) mà
operator sửa được LÚC SERVER ĐANG CHẠY, lưu SQLite có version, nhưng chỉ có hiệu lực ở lần restart KẾ TIẾP —
không hot-reload, không mutate config tiến trình hiện tại. Precedence khi resolve ở lần restart đó chèn thêm
một tầng vào giữa: `defaults < config file < SQLite safe settings < environment < flags`.

### Nghiên cứu

Đọc trước khi viết bất kỳ dòng code nào:

- `internal/app/config/config.go`/`sources.go`/`validate.go`: `Config` hiện tại KHÔNG có field cho
  `ManagedWorkspaceRoot`, `EvidenceRetention`, `ProviderDefaultModel`, `ProviderCredentialRef` — chỉ
  `ArtifactRoot` và `ProcessOutputLimit` trùng khái niệm với 2/7 field của allowlist. `Load(file, env, flags
  Overrides) (Config, error)` chỉ có 3 tầng, không có chỗ chèn SQLite.
- Grep toàn repo `config\.Load\(` ra **0 kết quả** ở production code — `cmd/aw/serve.go` hiện tại KHÔNG gọi
  `config.Load` chút nào, tự parse flag `--db`/`--artifact-root` riêng, không đi qua `Config` surface đầy đủ.
  Đây là phát hiện quan trọng: task mô tả "composition root's own startup sequence changes" giả định `Load`
  đã được compose thật, nhưng thực tế `cmd/aw serve` chưa từng dùng `Config`/`Load` — V1-03's pipeline đầy đủ
  chưa được wire vào composition root nào cả, kể cả trước V6-10G.
- `internal/app/config/local_principal.go` (V6-01A, sibling rất gần): pattern "loader + validator riêng, tách
  khỏi Config/Overrides pipeline dùng chung" cho một concept mới không cần env/flag override — value trực
  tiếp cho quyết định giữ `SafeSettings` là type độc lập, không nhét vào `Config`.
- `internal/app/work/release_set.go` + `internal/adapters/sqlite/release_set.go`
  (`TransitionReleaseSetState`): pattern CAS chuẩn của repo — `UPDATE ... SET ..., version = version + 1 WHERE
  id = ? AND version = ?`, check `RowsAffected`, fallback existence-check phân biệt `ErrPersistenceNotFound`
  với `ErrOptimisticConflict`. `internal/adapters/sqlite/recovery_reaper.go` +
  `migrations/0026_recovery_reaper_state.sql`: pattern "singleton row, `id CHECK (id = 'singleton')`, seed
  bằng chính migration" — khớp chính xác nhu cầu "1 row duy nhất, luôn tồn tại, không có state 'chưa tạo'".
- `internal/app/ports/unitofwork.go`: `Tx` interface có 15 accessor, mỗi accessor gắn doc comment "populated
  now (V<task>)" — theo đúng convention, thêm `SafeSettings() SafeSettingsRepository` accessor thứ 16.
- `internal/app/work/event_schema.go` + `event_schema_test.go`: pattern retrofit V6-00A — const
  `XxxEventType`/`XxxSchemaVersion`, payload struct riêng có json tag, `DecodeXxxV1`, `RegisterEventSchemas`,
  2 test `GoldenFixtureDecodes`/`RealEventPayloadDecodes`. `internal/archtest/event_catalog_test.go`
  (`TestEmittedDomainEventInventoryMatchesRegisteredInventory`) tự parse AST tìm mọi `ports.DomainEvent{}`
  composite literal dưới `internal/app/...` và so registered vs emitted — bắt buộc thêm dòng gọi
  `safesettings.RegisterEventSchemas(registry)` vào chính file archtest đó, nếu không CI fail ngay
  ("SafeSettingsUpdated v1 has no registered decoder").
- `internal/app/doctor/doctor.go`/`checks.go`: `Options{Config, Store ports.QueryStore, WorkerConfig,
  CheckWorker}`, `Run()` build danh sách `CheckResult` tuyến tính. Không có `aw doctor` CLI nào gọi
  `doctor.Run` trong `cmd/aw` — chỉ `internal/integration/foundation_test.go` gọi trực tiếp trong test. Đây
  là gap có sẵn từ trước V6-10G, không phải task này tạo ra.
- `internal/delivery/httpapi/health.go`: `ReadinessChecker.Register(name, func(ctx) error)` — đơn giản, và
  `cmd/aw/serve.go` ĐÃ compose 3 check thật (`database`, `artifact_root`, `routes`) ngay trong composition
  root, có `uow` sẵn trong scope — chỗ hợp lý nhất để thêm 1 check `safe_settings` thật.
- Không có precedent "credential reference, không phải secret value" nào có sẵn trong
  `internal/app/adapterbuild` hay nơi khác — phải tự định nghĩa shape validation cho
  `ProviderCredentialRef` (bounded, whitespace-free, charset hạn chế) làm proxy duy nhất khả thi cho "đây là
  reference chứ không phải secret dán nhầm vào".
- `internal/adapters/gitworktree/provider.go` (`isWithin`/`ensureLexicallyWithin`): pattern kiểm tra
  "root A và root B không được chứa nhau" bằng `filepath.Rel` — domain package không có filesystem access
  nên viết lại bằng string thuần (`internal/domain/readiness`'s `normalizeRelativeDirectory` là precedent
  "mỗi domain package tự giữ bản sao nhỏ, không import chéo domain khác").

### Quyết định

1. **Không sửa `internal/app/config/config.go`/`sources.go`/`validate.go`/`Load` một chữ nào.** Đọc "Không
   làm: ... no live mutation of immutable process config" theo nghĩa chặt nhất: package đó đã ship, được rất
   nhiều package khác phụ thuộc, và 2 sibling task (V6-10E, V6-10I) đang chạy song song trên cùng batch —
   sửa một file nền tảng dùng chung là rủi ro không cần thiết cho một task tự mô tả là "định nghĩa trước khi
   có endpoint". `SafeSettings` là type hoàn toàn mới, độc lập; 2 field trùng khái niệm với `Config`
   (`ArtifactRoot`, `ProcessOutputLimit`) chỉ tái dùng GIÁ TRỊ default của `config.Defaults()`, không tái
   dùng field/type.
2. Domain document (`internal/domain/safesettings.SafeSettings`) tự giữ `MarshalJSON`/`UnmarshalJSON` với
   `json.Decoder.DisallowUnknownFields()` ngay trong `UnmarshalJSON` — một điểm decode DUY NHẤT dùng cho cả
   2 chiều: sqlite đọc lại `desired_json` (luôn sạch vì do chính package này ghi) VÀ app layer decode desired
   document từ request. Tự động chặn mọi field ngoài allowlist, bao gồm chính xác các field bị cấm
   (`databasePath`, `workerId`, `localPrincipal`, `sessionKey`/`signingKey`) mà không cần liệt kê blacklist
   riêng — chúng chỉ đơn giản "không nằm trong struct" nên bị `DisallowUnknownFields` từ chối.
3. `EvidenceRetention` lưu dạng Go-syntax duration string (`"168h0m0s"`) trong JSON, không phải số nanosecond
   trần — mirror đúng `rawFileConfig.LeaseTTL` của `internal/app/config/sources.go`.
4. `Validate` domain trả lỗi ĐẦU TIÊN tìm thấy (không collect-all như `config.Validate`) — mirror convention
   constructor domain khác (`readiness.NewProfile`, `work.NewReleaseSet`), vì đây là single-document update
   operator sửa lại sau khi thấy lỗi, không phải toàn bộ startup config cần thấy hết vấn đề một lần.
5. `safe_settings` là bảng 1 row singleton, seed ngay trong migration `0036` với desired document
   zero-value (`{"managedWorkspaceRoot":"",...,"evidenceRetention":"0s",...}`) tại version 1 — không có
   state "row chưa tồn tại" nào `Get` phải xử lý riêng.
6. `UpdateSafeSettingsRequest` (app layer) nhận `DesiredJSON json.RawMessage` — TOÀN BỘ document, không phải
   patch từng field — đúng "Store full desired document + version". Dùng `cmd.ExpectedVersion` có sẵn trên
   `ports.Command` làm CAS fence, không thêm field ExpectedVersion riêng (mirror
   `SealReleaseSetRequest`/`AbandonReleaseSetRequest`).
7. ADR-025 xếp "safe-settings mutation" và "safe-settings read" vào bảng installation-scope tường minh —
   `UpdateSafeSettings` reject thẳng nếu `cmd.Scope` không phải `ports.InstallationScope()`
   (`ErrNotInstallationScoped`); `GetSafeSettings` là query thuần, không cần `CommandEnvelope`/receipt (mirror
   `adapterbuild.GetAdapterBuild`).
8. "Startup merge" (`internal/app/safesettings/startup.go`) là hàm thuần `ResolveEffective(defaults, file
   StartupOverrides, sqlite SafeSettings, env, flags StartupOverrides) Effective` — không tự đọc
   `os.Environ()`/`os.Args`, nhận layer đã resolve sẵn từ caller (giống `config.FromEnv` nhận `lookup
   func(string)(string,bool)` thay vì tự gọi `os.LookupEnv`). Vì `cmd/aw serve` hiện tại CHƯA compose
   `config.Load` (phát hiện ở Nghiên cứu), task này KHÔNG tự ý thêm một refactor lớn vào `serve.go` để wire
   toàn bộ chuỗi `Load`→DB-open→resolve — phạm vi đó thuộc về một composition-root task khác, ngoài "Phạm vi"
   của V6-10G. Thay vào đó, `ResolveEffective` được chứng minh đúng bằng test thật mở SQLite thật
   (`TestStartupSequencing_CurrentProcessUnchangedThenRestartAppliesUnmasked`), theo đúng thứ tự "open DB
   trước, đọc SQLite safe settings sau, resolve sau cùng" — sẵn sàng cho bất kỳ composition root nào gọi vào
   khi cần, không có quyết định precedence/allowlist nào còn treo lại.
9. Vì "Store full desired document": SQLite hoặc cấu hình TẤT CẢ 7 field cùng lúc, hoặc KHÔNG field nào —
   không có state "SQLite override field A nhưng field B vẫn theo file layer". `ResolveEffective` implement
   đúng bất biến này: SQLite layer chỉ "bật" khi `!sqlite.IsZero()`, lúc đó cả 7 field đều lấy từ SQLite.
10. `MaskedByStartupSource` chỉ non-empty khi SQLite đã từng cấu hình thật (khác zero-value) VÀ effective
    source là `environment`/`flag` — nếu SQLite chưa từng cấu hình, không có gì để "mask" (chỉ là default/file
    thắng bình thường).
11. Doctor: thêm `Options.UnitOfWork` (optional, nil-safe, mirror `CheckWorker`'s "opt-in" pattern) và
    `CheckSafeSettings` — không tự ý wire `doctor.Run` vào `cmd/aw` (không có call site nào tồn tại trước đó
    để mở rộng, đây là gap có sẵn từ trước, ngoài phạm vi task này). Readiness (`httpapi.ReadinessChecker`)
    NGƯỢC LẠI được wire thật vào `cmd/aw/serve.go` vì composition root đó đã compose 3 check khác ngay tại
    chỗ, có `uow` sẵn — thêm 1 check `safe_settings` là extension nhỏ, an toàn, và làm cho Verify line "fail
    readiness ... typed" có bằng chứng end-to-end thật qua HTTP, không chỉ unit test cô lập.
12. Test "corrupt persisted settings": vì KHÔNG có code path production nào từng ghi JSON hỏng vào
    `desired_json` (Update luôn marshal object đã qua Validate), cách DUY NHẤT mô phỏng bit-rot/sửa tay
    ngoài luồng là UPDATE SQL trực tiếp trong fixture test — không vi phạm nguyên tắc "không mutate database
    trực tiếp để fabricate kết quả verified", vì đây là fabricate ĐIỀU KIỆN LỖI, không fabricate MỘT KẾT QUẢ
    THÀNH CÔNG giả. Thêm `sqlite.CorruptSafeSettingsDesiredJSONForTest` (exported, test-only, mirror
    `SeedFixtureOwners`'s doc-comment pattern "tồn tại chỉ để test ngoài package fixture qua API công khai,
    production code không bao giờ gọi").

### Thực hiện

File mới:

- `internal/domain/safesettings/safesettings.go`: `SafeSettings` (7 field), `IsZero()`,
  `MarshalJSON`/`UnmarshalJSON` (strict decode), `Validate` (traversal, root-overlap, retention ≤0, output
  limit ≤0, model shape, credential-ref shape).
- `internal/app/ports/safesettings.go`: `ErrSafeSettingsCorrupt`, `SafeSettingsRecord`,
  `UpdateSafeSettingsRequest`, `SafeSettingsRepository` interface (`Get`/`Update`).
- `internal/adapters/sqlite/migrations/0036_safe_settings.sql`: bảng `safe_settings` singleton, seed
  version 1 zero-value.
- `internal/adapters/sqlite/safe_settings.go`: `safeSettingsRepository` — `Get` (decode +
  `ErrSafeSettingsCorrupt` typed), `Update` (CAS UPDATE, `RowsAffected` check → `ErrOptimisticConflict`).
- `internal/app/safesettings/commands.go`: `GetSafeSettings`, `UpdateSafeSettings` (scope check, receipt
  replay, Validate, CAS, event append, receipt record — cùng transaction).
- `internal/app/safesettings/event_schema.go`: `SafeSettingsUpdatedEventType`/`SchemaVersion` v1,
  payload flatten toàn scalar (`EvidenceRetentionSeconds int64`, không nested Duration), `RegisterEventSchemas`.
- `internal/app/safesettings/startup.go`: `FieldSource`, `StartupOverrides`, `Defaults()`,
  `StringFieldEffective`/`DurationFieldEffective`/`IntFieldEffective`, `Effective`, `ResolveEffective`.
- Test mới: `safesettings_test.go` (domain), `safe_settings_test.go` (sqlite repository),
  `event_schema_test.go`, `commands_sqlite_test.go`, `startup_test.go` (app layer),
  `checks_sqlite_test.go` (doctor), golden fixture `testdata/golden/safe_settings_updated_v1.json`.

File sửa:

- `internal/app/ports/unitofwork.go`: thêm `SafeSettings() SafeSettingsRepository` vào `Tx`.
- `internal/app/ports/fake/unitofwork.go`: thêm `SafeSettingsRepository` in-memory (seed version 1 zero-value,
  CAS `Update` giống hệt semantics sqlite) + wire vào `Tx`/`newTx`/`clone`.
- `internal/adapters/sqlite/unitofwork.go`: `txAdapter.SafeSettings()`.
- `internal/adapters/sqlite/fixtures.go`: thêm `CorruptSafeSettingsDesiredJSONForTest`.
- `internal/app/doctor/doctor.go`: `Options.UnitOfWork` (optional), `Run()` gọi `CheckSafeSettings` nếu
  non-nil.
- `internal/app/doctor/checks.go`: `CheckSafeSettings`.
- `cmd/aw/serve.go`: thêm `checker.Register("safe_settings", ...)` cạnh `database`/`artifact_root`.
- `cmd/aw/serve_test.go`: thêm `TestServe_ReadyFailsIfSafeSettingsCorrupt` + import `sqlite`.
- `internal/archtest/event_catalog_test.go`: import + gọi `safesettings.RegisterEventSchemas(registry)`.
- `internal/adapters/sqlite/db_test.go` (x2) và `unitofwork_test.go` (x1): hardcoded migration count
  `34` → `35` (thêm 1 migration file thật, không phải flake).

### Test

- `go build ./...`, `go vet ./...`: sạch toàn bộ module.
- `go test ./internal/domain/safesettings/...`: 16 test — `Validate` cho mọi nhánh (traversal cả 2 root và
  provider path, overlap cả 2 chiều + identical + sibling-không-overlap + prefix-nhưng-không-overlap
  `"data/workspaces-extra"` vs `"data/workspaces"`, retention ≤0, output limit ≤0, model rỗng/whitespace/
  control-char/quá dài, credential ref rỗng/whitespace/chứa-space/quá dài/ký-tự-cấm), JSON round-trip
  (thường + zero-value + `{}` literal), unknown-field reject (6 case gồm 4 field bị cấm tường minh).
- `go test ./internal/adapters/sqlite/... -run TestSafeSettingsRepository`: seed-by-migration (version 1,
  zero desired), update CAS thành công, stale-version conflict (2 writer cùng version 1, writer 2 thua,
  row vẫn giữ đúng document của writer 1), corrupt row → `ErrSafeSettingsCorrupt` (qua UPDATE SQL trực tiếp).
- `go test ./internal/app/safesettings/...`: 17 test — golden + real-payload decode; fresh-DB query;
  happy-path update (version 2, restartRequired true, reread khớp); replay idempotency-key (version không
  tăng lần 2); replay khác hash → `ErrReceiptConflict`; concurrent CAS conflict (2 command, 2 idempotency
  key khác nhau, cùng ExpectedVersion 1 → `ErrOptimisticConflict`); wrong scope →
  `ErrNotInstallationScoped`; unknown field (kèm `databasePath`) reject, version không đổi; invalid
  retention reject, version không đổi; root overlap reject; traversal reject; `ResolveEffective` cho từng
  tầng (defaults-only, file-beats-defaults, sqlite-beats-file toàn bộ 7 field cùng lúc, env-masks-sqlite,
  flag-masks-env-and-sqlite, duration field riêng); và
  `TestStartupSequencing_CurrentProcessUnchangedThenRestartAppliesUnmasked` mở SQLite thật, resolve "process
  hiện tại", Update thật, assert `Effective` snapshot cũ KHÔNG đổi (Go value semantics + assert tường minh),
  resolve lại "restart" thấy giá trị mới unmasked, và biến thể có env override thấy giá trị mới bị mask đúng
  tên source.
- `go test ./internal/app/doctor/...`: `CheckSafeSettings` HEALTHY trên DB mới migrate, BLOCKED có
  Remediation trên row bị corrupt qua `CorruptSafeSettingsDesiredJSONForTest`; toàn bộ suite doctor cũ vẫn
  pass (check mới chỉ chạy khi `Options.UnitOfWork != nil`, không đổi behavior test cũ nào).
- `go test ./cmd/aw/...`: `TestServe_ReadyFailsIfSafeSettingsCorrupt` — corrupt row TRƯỚC khi `serve` start,
  `/health/ready` trả 503 với `safe_settings` trong danh sách check fail ngay từ request đầu tiên; toàn bộ
  suite `cmd/aw` cũ (bao gồm `TestServe_StartsServesHealthAndShutsDownGracefully`) vẫn pass — chứng minh
  check mới không phá server thật khi mọi thứ healthy.
- `go test ./internal/archtest/...`: `TestEmittedDomainEventInventoryMatchesRegisteredInventory` — 41
  registered = 41 emitted, 0 missing (tăng từ khi chưa có V6-10G, xác nhận `SafeSettingsUpdated` được đăng ký
  đúng); `TestDomainAppNeverImportAdapters` vẫn pass dù `commands_sqlite_test.go`/`startup_test.go`/
  `safe_settings_test.go` import `internal/adapters/sqlite` trực tiếp — xác nhận `go list -json` không tính
  import chỉ dùng trong `_test.go` vào `Deps` (đúng như precedent `workspaceprovision/handler_sqlite_test.go`
  đã import `gitworktree` từ trước).
- `go test ./... -count=1`: toàn bộ module (~90 package) pass 100%, 0 dòng FAIL, sau khi sửa 3 assertion
  migration-count cứng (`34`→`35`) — đây là lần fail DUY NHẤT gặp phải trong suốt quá trình, và là hệ quả
  trực tiếp, có chủ đích của việc thêm migration `0036`, không phải regression hay flake.

### Verify

- "migration/replay/concurrency/event golden": migration 0036 seed sạch (`TestSafeSettingsRepository_
  Get_SeededByMigration`), replay idempotency-key giữ nguyên version
  (`TestUpdateSafeSettings_ReplayReturnsFirstResult`), concurrency CAS 2 writer
  (`TestSafeSettingsRepository_Update_StaleVersionConflict`,
  `TestUpdateSafeSettings_ConcurrentUpdateCASConflict`), event golden 2-test pattern
  (`TestSafeSettingsUpdatedV1_GoldenFixtureDecodes`/`_RealEventPayloadDecodes`).
- "unknown/startup-security field": `UnmarshalJSON`'s `DisallowUnknownFields` chặn field lạ VÀ 4 field bị
  cấm tường minh (`databasePath`, `workerId`, `localPrincipal`, `sessionKey`/`signingKey`) — test cả ở domain
  layer (`TestJSONUnmarshal_UnknownFieldRejected`) lẫn command layer
  (`TestUpdateSafeSettings_UnknownFieldRejected`).
- "traversal/root overlap": `TestValidate_RootTraversalRejected`,
  `TestValidate_ProviderExecutablePathTraversalRejected`, `TestValidate_RootOverlapRejected` (2 chiều +
  identical + sibling-không-overlap), lặp lại ở command layer
  (`TestUpdateSafeSettings_TraversalRejected`/`RootOverlapRejected`).
- "invalid retention/model/provider ref": `TestValidate_InvalidRetentionRejected`,
  `TestValidate_InvalidProcessOutputLimitRejected`, `TestValidate_InvalidModelRejected` (4 case),
  `TestValidate_InvalidProviderCredentialRefRejected` (5 case), lặp lại ở command layer
  (`TestUpdateSafeSettings_InvalidValueRejected`).
- "current process unchanged": `TestStartupSequencing_...` snapshot `Effective` trước Update, assert bằng
  `==` sau Update — không đổi vì `Effective` là value type, không phải con trỏ vào state chung.
- "restart applies unmasked value": cùng test, resolve lại sau Update với sqlite record mới, không có
  env/flag override → `Source == SourceSQLite`, `MaskedByStartupSource == ""`.
- "env/flag mask": `TestResolveEffective_EnvMasksSQLite`, `TestResolveEffective_FlagMasksEnvAndSQLite`,
  cộng biến thể `restartWithEnv` trong `TestStartupSequencing_...` — `MaskedByStartupSource` báo đúng tên
  source đang che (`"environment"`/`"flag"`), `Desired` vẫn giữ giá trị SQLite thật dù bị mask.
- "corrupt persisted settings fail readiness/Doctor typed": 3 tầng — sqlite repository
  (`TestSafeSettingsRepository_Get_CorruptRowFailsTyped` → `ports.ErrSafeSettingsCorrupt`), Doctor
  (`TestCheckSafeSettings_BlockedOnCorruptRow` → `StatusBlocked` có Remediation), và readiness thật qua HTTP
  (`TestServe_ReadyFailsIfSafeSettingsCorrupt` → 503, check `safe_settings` trong response) — không nơi nào
  panic hay fallback im lặng về zero document.
- "Không làm": `internal/app/config/config.go`/`sources.go`/`validate.go` không có dòng diff nào (xác nhận
  bằng chính danh sách file sửa ở trên); không field nào tên `DatabasePath`/`WorkerID`/`LocalPrincipal`/
  session-hoặc-signing-key xuất hiện trong `SafeSettings`; `ProviderCredentialRef` luôn là reference string
  có shape hạn chế, không có chỗ nào trong response/event/log in ra secret value thật (không tồn tại code
  path nào resolve reference → giá trị thật trong toàn bộ package này).
- "Hoàn thành khi": `GetSafeSettings`/`UpdateSafeSettings` + `ResolveEffective` đã đóng mọi quyết định
  storage/precedence/allowlist thật (schema, CAS, event, receipt, 7-field closed set, 5-tầng precedence,
  masking) bằng code + test thật — V6-10H (endpoint HTTP) chỉ còn việc bọc `GetSafeSettings`/
  `UpdateSafeSettings` bằng route/If-Match, không còn quyết định nào ở tầng dưới phải tự nghĩ thêm.

### Kết quả

7 file mới ở `internal/domain/safesettings` (1) + `internal/app/ports` (1) + `internal/adapters/sqlite`
(migration + repository, 2) + `internal/app/safesettings` (commands/event-schema/startup, 3), cộng 6 file
test mới và 1 golden fixture; 11 file sửa (2 `ports/unitofwork` thật + fake, sqlite `unitofwork`/`fixtures`,
doctor `doctor.go`/`checks.go`, `cmd/aw/serve.go`/`serve_test.go`, `archtest/event_catalog_test.go`, 3 dòng
hardcoded migration-count). `go build/vet/test ./...` xanh 100% trên ~90 package, đúng 1 lần fail thật gặp
phải trong suốt task (migration-count assertion, đã sửa, không phải flake) — không đụng
`TestSPK09QuarantineRecreateFencesStaleGeneration`, `TestSupervisorNormalExit_
TreeQuiescedFalseWhileDescendantStillRuns`, `TestProjectWorkspaceGate`, hay
`TestRecoveryReaperHandler_OrphanedMutatingAttempt_RunCancelling_ClosesRunOutReally` (không package nào trong
số đó bị chạm). `internal/app/config` (Config/Load/Overrides) không đổi một dòng nào — đúng quyết định giữ
lớp thứ nhất bất biến trong khi build lớp thứ hai độc lập bên cạnh nó. Sẵn sàng cho V6-10H build
`GET/PUT /settings/safe` trực tiếp trên `GetSafeSettings`/`UpdateSafeSettings` không còn quyết định nào phải
mở lại.

## V6-03 — Public Project authority (PR #34)

### Bối cảnh

V6-03 nằm trong nhóm P1 ("sau V6-00A"), unblocked ngay khi V6-00A/V1-06/V1-07A đều đã đóng. Trích nguyên
văn task spec từ `docs/design/08-v6-api-projections.md`: "bổ sung public `CreateProject`, `ListProjects`,
`GetProject` còn thiếu trước HTTP/CLI"; "Không làm: không để delivery gọi `Tx.Catalog().CreateProject`
hoặc tạo Component thủ công". Đây là gap thật đã tồn tại từ V3-01: `project.Project`/`ProjectID` được
dùng khắp codebase như một khái niệm scoping, nhưng chưa từng có một named public application command
nào cho phép tạo Project — mọi test/seed hiện có (`mustCreateProject`, `seedProjectSQLite` trong chính
`internal/app/catalog`) đều gọi thẳng `tx.Catalog().CreateProject`, đúng chỗ hở task này phải đóng lại.

### Nghiên cứu

Đọc `internal/app/ports/unitofwork.go`'s `CatalogRepository.CreateProject` — chính doc comment của nó đã
tự khai: "Not itself a cited public command ... a later task adding the full idempotent CreateProject
command wraps this same method rather than duplicating the insert" — xác nhận đây đúng là chỗ chờ sẵn cho
task này, không phải suy diễn.

Mirror cấu trúc `internal/app/catalog.RegisterRepository`/`AssignComponentPack` (đọc toàn bộ
`commands.go`, 529 dòng, trước khi viết dòng nào mới): cả hai đều theo đúng khuôn "receipt idempotency
check → real work → domain event → receipt record", tất cả trong một `uow.WithSerializedWrite`. Đối
chiếu `internal/app/definitions.CreateDefinition` (candidate thứ hai theo gợi ý task) — kết luận
`RegisterRepository` khớp hơn vì Project là aggregate gốc đơn giản, không có khái niệm scope kép
(global/project) như `definition.Scope`.

Đọc `internal/app/catalog/event_schema.go` (sản phẩm V6-00A) — 3 event đã đăng ký
(`RepositoryRegistered`/`RepositoryProbeRetried`/`ComponentPackAssigned`), đúng mẫu 2 test/event
(`TestXxxV1_GoldenFixtureDecodes`, `TestXxxV1_RealEventPayloadDecodes`) cộng `RegisterEventSchemas` — áp
dụng y hệt cho `ProjectCreated`.

Đọc ADR-025 (`docs/architecture/02-architecture-decisions.md` §27) nguyên văn: bảng closed-set liệt kê
`CreateProject` ở cột Command installation-scoped, `ListProjects` ở cột Query installation-scoped —
`GetProject` KHÔNG có mặt trong bảng, nên theo đúng câu "Mọi query project-scoped vẫn MUST scope bằng
ProjectID — nới lỏng này chỉ áp cho tập installation đã liệt kê", `GetProject` là project-scoped, xác
nhận đúng giả thuyết ban đầu chứ không phải đoán suông.

Phát hiện quan trọng nhất: `internal/adapters/sqlite/command_handler_example_test.go` — "V1-06's own
illustrative handler mẫu" (`handleCreateProject`, unexported, chỉ dùng trong _test.go) — LÀM NGƯỢC hoàn
toàn ADR-025: chính nó có `TestHandleCreateProject_InstallationScopedCommand_Rejected`, coi CreateProject
là PROJECT-scoped và reject installation scope. Đây không phải một lỗi cần sửa — file đó viết TRƯỚC khi
ADR-025 tồn tại (V1-06 là task nền tảng đầu tiên, predate toàn bộ nhóm V6), là ví dụ minh hoạ lịch sử,
không được bất kỳ command dispatch thật nào gọi tới. Quyết định: giữ nguyên file đó (không sửa, đúng kỷ
luật bất biến lịch sử ADR-008/ADR-015 áp dụng tương tự cho tài liệu minh hoạ), chỉ ghi rõ sự khác biệt
này trong doc comment của `CreateProject` thật (commands.go) để người đọc sau không nhầm hai thứ với
nhau.

### Quyết định

1. **`CreateProjectRequest` không có field `ID`.** Không giống `RegisterRepositoryRequest.RepositoryID`
   (caller-chosen), Project's ID phải do application tự sinh qua `idsource` — đúng convention
   `internal/app/idsource`'s "application tạo ID, không để persistence adapter tự sinh", giống hệt
   `CreateComponent`'s `id := ids.NewID()` đã có sẵn.
2. **`cmd.Scope.IsInstallation()` bị enforce cứng trong `CreateProject`.** Reject bằng `ports.ErrScopeMismatch`
   (sentinel mới) trước cả receipt lookup — không im lặng chấp nhận project scope. Đây là command đầu
   tiên trong repo thật sự cần phân biệt installation/project scope tại chính handler (mọi command khác
   từ trước tới giờ hoặc luôn project-scoped, hoặc — như `CreateDefinition` — dual-mode theo route, không
   command nào bắt buộc CHỈ installation).
3. **`ListProjects` cũng enforce `IsInstallation()`.** Không có "list scoped theo một project" vì Project
   chính là đơn vị scope của mọi thứ khác — không có khái niệm đó.
4. **`GetProject` yêu cầu `scope.ProjectID() == projectID` chính xác.** Cả installation scope lẫn scope
   của một Project KHÁC đều bị reject bằng cùng `ports.ErrScopeMismatch` — mirror đúng kỷ luật
   `RetryRepositoryProbe`'s cross-project check (resolve từ stored row, không tin request), áp dụng cho
   authorization thay vì referential integrity.
5. **Event `ProjectCreated`'s `ProjectID` field = ID của chính Project mới tạo, KHÔNG để rỗng** dù lệnh
   tạo ra nó là installation-scoped. `ports.DomainEvent.ProjectID`'s doc comment nói "empty means
   installation-scoped", nhưng field này thực chất track Project nào sở hữu dòng lịch sử của event, không
   phải CommandScope nào sinh ra nó — một projection/journal theo dự án tương lai (V6-08/V6-09) PHẢI thấy
   được event khai sinh chính Project đó trong lịch sử của nó. Áp dụng đúng lý luận `RegisterRepository`'s
   `RepositoryRegistered` event đã dùng (ProjectID = Repository's actual owning project, không phải scope
   command).
6. **`ports.ErrScopeMismatch` đặt trong `internal/app/ports/command.go`** (tầng command envelope chung),
   không phải trong `catalog` — vì `ListProjects`/`GetProject` sau này chắc chắn không phải named
   command/query duy nhất cần enforce đúng luật ADR-025 này. Ghi rõ trong doc comment: phân biệt với
   `workspaceinspection.ErrScopeMismatch` đã có sẵn từ V6-10C (cùng tên, khác package, khác concern hoàn
   toàn — cái đó là referential-integrity giữa RepositoryWorkspace và Project/Repository/WorkspaceSet chain
   của chính nó, không phải CommandScope authorization).

### Thực hiện

- `internal/app/ports/command.go`: thêm `ErrScopeMismatch` sentinel.
- `internal/app/ports/unitofwork.go`: `CatalogRepository` interface thêm `ListProjects(ctx) ([]project.Project,
  error)`; sửa lại doc comment `CreateProject`/`GetProject` trỏ đúng sang `internal/app/catalog.CreateProject`/
  `GetProject` thật thay vì "a later task" mơ hồ.
- `internal/adapters/sqlite/catalog.go`: `catalogRepository.ListProjects` + `listProjectsTx` (`SELECT id, name,
  status, version FROM projects ORDER BY id`, mirror đúng `listProjectRepositoriesTx`).
- `internal/app/ports/fake/unitofwork.go`: `CatalogRepository.ListProjects` (map → slice, sort by ID).
- `internal/app/catalog/commands.go`: `CreateProjectRequest{Name}`, `CreateProjectResult{ProjectID, Name,
  Status}`, `func CreateProject(ctx, uow, ids, cmd, req) (CreateProjectResult, error)`; `func
  ListProjects(ctx, uow, scope) ([]project.Project, error)`; `func GetProject(ctx, uow, scope, projectID)
  (project.Project, error)`.
- `internal/app/catalog/event_schema.go`: `ProjectCreatedEventType = "ProjectCreated"`,
  `ProjectCreatedSchemaVersion = 1`, `projectCreatedEventPayload{ProjectID, Name, Status}`,
  `DecodeProjectCreatedV1`, đăng ký trong `RegisterEventSchemas`.
- `internal/app/catalog/testdata/golden/project_created_v1.json`: golden fixture mới.

### Test

- `internal/app/catalog/commands_test.go` (fake uow, 13 test mới): `TestCreateProject_CreatesProjectAndEmitsEvent`,
  `TestCreateProject_DuplicateSameRequest_ReplaysExactSameGeneratedID` (dùng `idsource.Sequential` — nếu
  replay lỡ gọi lại `NewID()` sẽ lộ ra ngay vì source có state, lần hai sẽ khác lần một),
  `TestCreateProject_DuplicateDifferentPayload_ReturnsConflict`, `TestCreateProject_ProjectScopedCommand_Rejected`,
  `TestCreateProject_RequiresName`, `TestListProjects_ProjectScopedCaller_Rejected`,
  `TestListProjects_ReturnsEveryProjectInIDOrder`, `TestGetProject_MatchingProjectScope_ReturnsAuthoritativeDetail`,
  `TestGetProject_AnotherProjectScope_Rejected`, `TestGetProject_InstallationScopedCaller_Rejected`.
- `internal/app/catalog/commands_sqlite_test.go` (real sqlite, 3 test mới, vì fake's `WithSerializedWrite`
  không tạo contention thật — đúng lý do các concurrency test khác trong file này đã giải thích):
  - `TestCreateProject_ConcurrentDistinctRequests_NoDuplicateOrLostProject` — 8 writer, 8 Project riêng,
    đối chiếu `ListProjects` trả đúng 8 dòng, không mất/trùng ID.
  - `TestCreateProject_ConcurrentSameIdempotencyKey_ExactlyOneProjectPersists` — 8 writer CÙNG idempotency
    key/hash (kịch bản retry storm thật), mỗi writer một `idsource.Random` riêng — nếu 2 writer cùng thật
    sự chạm `tx.Catalog().CreateProject` sẽ lộ ra ngay vì ID ngẫu nhiên khác nhau; kết quả: mọi writer
    đồng thuận đúng 1 ProjectID, `ListProjects` xác nhận đúng 1 dòng — chứng minh bằng kịch bản race thật,
    không chỉ đọc doc comment của `command_receipts.go`'s `ON CONFLICT DO NOTHING`.
  - `TestCreateProject_ReplayAfterCommit_SimulatesCallerNeverSawFirstAck` — gọi `CreateProject` một lần,
    xác nhận ĐỘC LẬP qua `store.CountDomainEvents`/`CountCommandReceiptsByIdempotencyKey`/`ListProjects`
    rằng transaction đã commit thật (mirror `TestHandleCreateProject_CommitsStateEventAndReceiptAtomically`
    của chính V1-06), rồi mô phỏng caller "crash trước khi thấy response": gọi lại `CreateProject` với một
    `ports.Command` HOÀN TOÀN MỚI (cùng Actor/Scope/IdempotencyKey/Type/RequestHash) và một
    `idsource.Sequential` mới (sẽ sinh "crash-project-2" nếu code sai) — xác nhận trả đúng
    "crash-project-1" ban đầu, không tạo dòng/event thứ hai.
- `internal/app/catalog/event_schema_test.go`: `TestProjectCreatedV1_GoldenFixtureDecodes`,
  `TestProjectCreatedV1_RealEventPayloadDecodes`.
- `go build ./...`, `go vet ./...` sạch. `go test ./...` (70 package, sau khi merge `origin/master` nhận
  V6-15A's rename `cmd/agentkit`→`cmd/aw`) pass 100%, gồm `internal/archtest`'s
  `TestEmittedDomainEventInventoryMatchesRegisteredInventory` (V6-00A's CI guard) tự động xác nhận
  `ProjectCreated` v1 vừa emit vừa registered, không cần sửa tay archtest.
- Một lần chạy `go test ./...` VỚI CONTENTION (3 lần gọi `go test ./...` chạy song song trên cùng máy do
  thao tác polling nhầm) làm `internal/integration/v5accept` fail timeout-deadline 3 lần liên tiếp, MỖI
  LẦN một test khác nhau (`TestV5AcceptFullComposition_...`, rồi `conformance_matrix_test.go` hai lần khác
  tên) — cùng một triệu chứng "did not reach state VERIFYING within the deadline". Không tin đây là
  regression thật: chạy lại `go test ./internal/integration/v5accept/... -v` MỘT MÌNH, không contention —
  toàn bộ 12 test pass sạch, 92.9s, gồm cả 2 test vừa fail ở trên. Xác nhận đúng là CPU/IO contention từ 3
  full suite chạy đồng thời, không phải lỗi thật từ thay đổi V6-03 (V6-03 không đụng
  `internal/app/runtime`/`internal/app/work`/scheduler nào cả). Chạy lại `go test ./...` một lần sạch
  (không contention) sau khi merge master — 100% xanh, kể cả `internal/integration/v5accept` (133.6s).

### Verify

- "command replay/different hash/concurrency/crash-after-commit": cả 4 vế đều có test thật riêng biệt
  (liệt kê ở trên), replay chứng minh bằng chính ID sinh ra (không chỉ "kết quả giống nhau"), concurrency
  có cả 2 biến thể (distinct + same-key race) trên sqlite thật, crash-after-commit mô phỏng đúng "commit
  đã xảy ra, caller không thấy response, retry với envelope mới".
- "event golden": `project_created_v1.json` + 2 test decode, cộng archtest's inventory guard tự động xác
  nhận registered=emitted.
- "list/get scope": `ListProjects` reject project scope, `GetProject` reject cả installation scope lẫn
  scope của Project khác — đúng "closed installation set" của ADR-025 (`ListProjects` có trong bảng,
  `GetProject` không có, nên ngược nhau).
- "Hoàn thành khi": "clean installation tạo Project qua một named public application authority" — có thật:
  `internal/app/catalog.CreateProject` là con đường duy nhất tạo Project có receipt/event, không còn cách
  nào khác public.

### Kết quả

PR #34 (`feat/v6-03-public-project-authority`, branch từ `origin/master` tại `d8c1323`, merge
`origin/master` một lần giữa chừng để nhận V6-15A's `cmd/agentkit`→`cmd/aw` rename + narrative section của
chính V6-15A trước khi ghi đoạn này — fast-forward, không conflict vì không file nào chung). 3 function
public mới (`CreateProject`, `ListProjects`, `GetProject`) trong `internal/app/catalog`, 1 sentinel mới
(`ports.ErrScopeMismatch`), 1 method interface mới (`CatalogRepository.ListProjects`, 2 implementation),
1 event mới đăng ký đầy đủ (`ProjectCreated` v1). 13 test mới trong `commands_test.go` (fake uow) + 3 test
mới trong `commands_sqlite_test.go` (real sqlite, concurrency/crash-replay) + 2 test golden/round-trip
trong `event_schema_test.go`. `go build/vet/test ./...` xanh 100% trên toàn bộ 70 package. Gap "delivery
không có public CreateProject" đã đóng — V6-03A (Project/repository/component HTTP endpoints) giờ có đủ
dependency (`V6-00`, `V6-01A`, `V6-02`, `V6-02A`, `V6-03`) để bắt đầu ngay khi 4 dependency còn lại sẵn
sàng.

## V6-10E — ReleaseSet/local-commit application authority

### Bối cảnh

V6-10E là task phức tạp nhất trong 3 task chạy song song ngay sau V6-00A (`{V6-03, V6-10E, V6-10G, V6-10I}`),
chạy trong worktree riêng — không đụng file chung với 2 session kia. Mục tiêu design doc: hoàn thiện
list/get/create/seal/abandon cho ReleaseSet (V5-10A đã build create/seal/abandon, còn thiếu list/get công
khai) và xây `RequestReleaseSetLocalCommit` — operation/worker job-backed, crash-safe, để thật sự MATERIALIZE
kết quả một ReleaseSet entry thành một real local Git commit, không bao giờ push/fetch/PR/merge/rebase/
force-push, không Git nào chạy trong command transaction, không implicit commit khi seal.

### Nghiên cứu

Đọc lại V5-10A's own narrative (đoạn "V5-10A — ReleaseSet và typed local Git operation" trong
`baocaov5checklist.md`) trước khi code bất cứ gì — xác nhận: `internal/app/work/release_set.go` đã có
`CreateReleaseSet`/`SealReleaseSet`/`AbandonReleaseSet` thật (không có List/Get public wrapper — chỉ có
`ports.WorkRepository.GetReleaseSet`/`ListReleaseSetsForFamily` ở tầng persistence, gọi nội bộ từ
`transitionReleaseSet`/`EligibilityAuthority`), và `ports.LocalCommitCreator`/
`internal/adapters/gitworktree.Provider.CreateLocalCommit` đã có thật nhưng "chưa wiring vào bất kỳ command
ReleaseSet nào" — đúng là job của task này.

Đọc `internal/app/ports/scheduling.go` (`JobLease`, `WriteLeaseManager`) và
`internal/adapters/sqlite/scheduling.go`/`workflow_store.go` (`validateActiveWriteLeaseInTx`) để tìm mẫu
"worker lease + write lease" thật đã có — phát hiện then chốt: `WriteLeaseManager` (dùng bởi AGENT node
execution, `internal/app/runtime/finalize.go`/`agent_node_executor_cancellation.go`) hard-require một
`runtime.ExecutionAttemptID` sống — MỌI query của nó (`AcquireWriteLeases` validate + `HeartbeatWriteLeases`/
`ValidateWriteLease` INNER JOIN `execution_attempts WHERE state='RUNNING'`) đều cần attempt thật. Job của
task này (`RequestReleaseSetLocalCommit`) là CONTROL-class, không phải AGENT execution attempt — không có
attempt nào để gắn vào. Tái dùng `WriteLeaseManager` nguyên bản nghĩa là phải fake một
`execution_attempts` row (khái niệm domain không có thật) hoặc nới lỏng coupling attempt của cơ chế đó cho
mọi caller AGENT hiện có — cả hai đều ngoài phạm vi và rủi ro thật cho code V5-15D/E đã merge. Quyết định:
xây cơ chế lease MỚI, hẹp, chỉ cho holder-là-job (không cần attempt) — đúng tiền lệ "narrow port riêng cho
capability mới, zero thay đổi implementer cũ" mà `ports.LocalCommitCreator`/`ports.WorkspaceInspectionReader`
tự thiết lập.

Đọc `internal/app/workspacerelease` (toàn bộ package: `commands.go` producer + `handler.go` consumer) làm mẫu
chính xác cho "job-backed, outside-tx I/O, fenced finalize" — `RequestWorkspaceSetRelease` ghi
intent+job+event+receipt atomic trong 1 `WithSerializedWrite`, `ExecuteWorkspaceSetRelease`/`Handler.Handle`
thực thi I/O thật ngoài mọi transaction rồi tự CAS state trong transaction riêng. Đọc
`internal/app/runtime/finalize.go` (`FinalizeExecutionAttempt`) để lấy đúng thứ tự 7 bước "validate job lease
→ validate write lease → CAS → event → complete job (cùng 1 tx) → release lease SAU KHI COMMIT, ngoài tx,
idempotent/tolerant `ErrWriteLeaseLost`" — thứ tự này lặp lại y hệt cho lease MỚI của task này.

Đọc `internal/domain/workspace/workspace.go` phát hiện: `internal/domain/work` KHÔNG thể import
`internal/domain/workspace` (cycle — `workspace.go` đã import `work` cho `WorkspaceSet.FamilyID`) — domain
type mới (`ReleaseSetLocalCommit`) phải dùng `RepositoryWorkspaceID string` thay vì
`workspace.RepositoryWorkspaceID`, giống hệt lý do `RepositoryRelease.RepositoryID` đã dùng `project.RepositoryID`
(package không có cycle) thay vì type từ `workspace`.

Đọc `internal/app/ports/work.go` (`RepositoryWorkspaceRecord`, `GetRepositoryWorkspaceByID`) phát hiện thêm
một điều quan trọng: `repository_workspaces.generation` là cột BẤT BIẾN theo row — một RECREATE luôn tạo row
MỚI (ID khác) ở generation+1, chứ không sửa generation của row cũ (row cũ chỉ chuyển state sang QUARANTINED
trước khi RECREATE được phép chạy, theo doc comment của `RecreateRepositoryWorkspaceRequest`). Suy ra: nếu
worker load lại ĐÚNG row đã pin theo ID, so sánh `Generation` với giá trị đã pin sẽ KHÔNG BAO GIỜ lệch (dead
code nếu implement) — "stale" thật sự phải được chặn ở REQUEST time qua `ExpectedWorkspaceVersion`/
`ExpectedReleaseSetVersion` (đúng mẫu `cmd.ExpectedVersion` mọi command khác đã dùng), còn ở WORKER time chỉ
còn đúng 1 cách row có thể "hỏng" — bị QUARANTINED. Quyết định bỏ hẳn `FailureStaleGeneration` khỏi
worker-side closed set, giữ lại đúng 1 lý do có thể tái hiện thật: `FailureWorkspaceQuarantined`.

Phát hiện một constraint thật khi viết test đầu tiên: `gitworktree.Provider.CreateLocalCommit`'s own
`validCommitField` từ chối MỌI `\r`/`\n` trong message — nghĩa là marker không thể nhúng như một trailer
đa dòng theo convention Git thông thường ("blank line, rồi Key: value"); phải nhúng trên CÙNG MỘT DÒNG với
message gốc (`message + " [Key: value]"`).

### Quyết định

1. **Marker xác định (deterministic operation marker) = sha256(ReleaseSetID | ReleaseSet.Version |
   RepositoryWorkspaceID | Generation | Actor | MessageHash)**, KHÔNG có parent trong input — vì parent chỉ
   biết được ở WORKER time (Inspect thật), còn marker phải tính được ở REQUEST time (thuần DB, không Git).
   Nhúng vào commit message thật qua `ports.LocalCommitMarkerTrailerKey` ("Release-Set-Local-Commit-Marker"),
   một dòng duy nhất.
2. **Port mới `ports.LocalCommitMarkerReader`** (`internal/app/ports/releasesetlocalcommit.go`, file MỚI —
   không sửa `localcommit.go` đã merge từ V5-10A): `FindLocalCommitByMarker` LUÔN trả về HEAD thật (revision +
   parent) bất kể `found`, để worker không cần một lệnh `Inspect` riêng — `found=false` nghĩa là HEAD hiện tại
   CHÍNH LÀ parent cần build commit mới lên trên; `found=true` nghĩa là HEAD chính là kết quả của một lần
   chạy trước (crash-after-Git-before-finalize) — reuse thẳng, không gọi `CreateLocalCommit` lần 2. Chỉ cần
   kiểm tra HEAD (không cần quét lịch sử): vì `CreateLocalCommit` là method Git-mutating DUY NHẤT toàn bộ
   port trong repo (đã tự khẳng định từ doc comment gốc của `LocalCommitCreator`), và write-lease loại trừ
   mọi ghi đồng thời khác — không gì khác có thể đã dịch chuyển HEAD giữa 2 lần gọi.
3. **Port mới `ports.LocalCommitWriteLeaseManager`** (cùng file) — Acquire/Validate/Release, holder chỉ gồm
   `JobLease` (JobID/Owner/Token), KHÔNG có AttemptID — bảng mới `local_commit_write_leases` (migration 0036)
   mirror đúng shape `write_leases` (fence_token tăng dần, `ON CONFLICT ... WHERE lease_until <= now`) trừ
   cột attempt. `ValidateLocalCommitWriteLeaseFencing` (Tx-composable, trên `WorkRepository`) mirror
   `RuntimeRepository.ValidateWriteLeaseFencing`/`validateActiveWriteLeaseInTx` — gọi bên trong transaction
   finalize, không phải qua port riêng.
4. **Domain type mới `work.ReleaseSetLocalCommit`** (`internal/domain/work/release_set_local_commit.go`) —
   3-state REQUESTED→{COMMITTED, FAILED}, mirror đúng shape `ReleaseSet`. `FailureReason` closed-set chỉ còn
   2 giá trị thật: `WORKSPACE_QUARANTINED`, `MARKER_DRIFT` (xem Nghiên cứu #5 — "stale" không phải worker-side
   failure). `ParentVCSObjectID` được ghi 2 lần trong đời một operation: `PinReleaseSetLocalCommitParent`
   (trước khi gọi Git thật, vẫn REQUESTED) — đây chính là cơ chế cho phép reconciliation phát hiện "drift"
   (so khớp parent đã pin với parent thật của commit tìm được ở HEAD) — rồi lần cuối cùng khi
   `TransitionReleaseSetLocalCommitToCommitted`.
5. **Package application mới `internal/app/releasesetcommit`** (không nhét vào `internal/app/work`, vốn tự
   cam kết từ V5-10A "không có lý do gì import os/os/exec") — mirror đúng shape `workspacereconcile`/
   `workspacerelease`: producer (`commands.go`: `RequestReleaseSetLocalCommit`) + consumer
   (`execute.go`: `ExecuteReleaseSetLocalCommit`, `handler.go`: `Handler` implement `workerpool.Handler`)
   sống chung 1 package vì cả hai đều cần port thật (`LocalCommitCreator`/`LocalCommitWriteLeaseManager`/
   `WorkspaceLifecycle`), khác hẳn `internal/app/work` vốn cố tình "không I/O".
6. **Drift → quarantine thật, không silent-reuse.** Khi `found=true` nhưng parent thật ở HEAD khác parent đã
   pin, gọi thẳng `ports.WorkspaceLifecycle.QuarantineRepositoryWorkspace` (cơ chế Quarantine thật của
   V5-15D) trước khi đóng operation `FAILED/MARKER_DRIFT` — không bao giờ coi đó là reuse hợp lệ.
7. **Job hoàn tất bằng chính `tx.Jobs().CompleteJob` bên trong finalize transaction** (giống hệt
   `FinalizeExecutionAttempt`'s own step 7) — không dựa vào `workerpool.Pool`'s own auto-`CompleteJob` sau khi
   `Handle` trả về nil (dù pool có gọi lại lần 2, lỗi `ErrJobLeaseLost` bị bỏ qua vô hại, đúng comment sẵn có
   của `pool.go`).

### Thực hiện

- Migration `0036_release_set_local_commits.sql` (2 bảng: `release_set_local_commits` intent+result,
  `local_commit_write_leases`). Bump migration count 34→35 trong `db_test.go`/`unitofwork_test.go`.
- `internal/domain/work/release_set_local_commit.go` (mới): `ReleaseSetLocalCommit`, `NewReleaseSetLocalCommit`,
  `ReleaseSetLocalCommitState`/`ReleaseSetLocalCommitFailureReason`.
- `internal/app/ports/work.go` (sửa): 5 method mới trên `WorkRepository`
  (`CreateReleaseSetLocalCommit`/`GetReleaseSetLocalCommit`/`PinReleaseSetLocalCommitParent`/
  `TransitionReleaseSetLocalCommitToCommitted`/`TransitionReleaseSetLocalCommitToFailed`/
  `ValidateLocalCommitWriteLeaseFencing`) + `ErrLocalCommitMarkerCollision`.
- `internal/app/ports/releasesetlocalcommit.go` (mới): `LocalCommitMarkerReader`, `LocalCommitWriteLeaseManager`
  + type liên quan, `ports.LocalCommitMarkerTrailerKey`.
- `internal/adapters/sqlite/release_set_local_commit.go` (mới): implement 6 method trên, marker-collision
  check tường minh trước insert (giống repository-existence check của V5-10A).
- `internal/adapters/sqlite/local_commit_write_lease.go` (mới): implement `LocalCommitWriteLeaseManager` trên
  `*Store`, mirror `AcquireWriteLeases`/`ReleaseWriteLeases` nhưng bỏ attempt.
- `internal/adapters/gitworktree/localcommit_marker.go` (mới): `Provider.FindLocalCommitByMarker` — 1 lệnh
  `git log -1 --format=%H%x1f%P%x1f%B HEAD`, tách bằng `\x1f` (không thể xuất hiện trong object id/message
  thường).
- `internal/app/work/release_set_queries.go` (mới): `GetReleaseSet`/`ListReleaseSetsForFamily` — 2 public
  query còn thiếu từ V5-10A, mirror shape `workspaceinspection.Queries` (mở `uow.WithReadOnly` riêng).
- `internal/app/releasesetcommit/` (package mới): `commands.go` (`RequestReleaseSetLocalCommit`), `marker.go`
  (`computeOperationMarker`/`computeMessageHash`/`commitMessageWithMarker`), `execute.go`
  (`ExecuteReleaseSetLocalCommit` + `loadIntent`/`loadRepositoryWorkspace`/`pinParent`/`failTerminal`/
  `quarantineWorkspace`), `handler.go` (`Handler`), `event_schema.go` (3 event:
  `ReleaseSetLocalCommitRequested/Committed/Failed` v1).
- `internal/adapters/sqlite/fixtures.go` (sửa): thêm `SeedFixtureRepositoryWorkspaceWithLocator` (nhận
  Locator tuỳ chỉnh thay vì `"opaque:<id>"` cố định) — cần cho test thật nối sqlite row với workspace Git thật
  trên đĩa qua `gitworktree.Provider.Provision`.
- `internal/archtest/event_catalog_test.go` (sửa): thêm `releasesetcommit.RegisterEventSchemas(registry)` vào
  inventory guard — 3 event mới nếu không đăng ký sẽ fail `TestEmittedDomainEventInventoryMatchesRegisteredInventory`.

### Test

- `internal/domain/work/release_set_local_commit_test.go`: constructor valid/invalid (6 case reject),
  `FailureReason.IsValid`.
- `internal/adapters/gitworktree/localcommit_marker_test.go`: not-found trả về HEAD/parent thật (base
  revision không có parent → rỗng), found trả đúng revision + parent, marker khác nhau không match.
- `internal/adapters/sqlite/local_commit_write_lease_test.go`: **hai worker thật** đua nhau
  `AcquireLocalCommitWriteLease` cùng lúc qua goroutine (`TestAcquireLocalCommitWriteLease_TwoWorkers_OnlyOneSucceeds`)
  — đúng đúng 1 thành công 1 conflict; steal sau khi TTL hết hạn → `ValidateLocalCommitWriteLease` trả
  `ErrLocalCommitWriteLeaseLost`; release 2 lần → lần 2 `ErrLocalCommitWriteLeaseLost` (idempotent).
- `internal/app/work/release_set_queries_test.go`: `GetReleaseSet` full detail + not-found,
  `ListReleaseSetsForFamily` đúng thứ tự.
- `internal/app/releasesetcommit/commands_test.go`: replay, receipt-conflict, stale ReleaseSet version, stale
  workspace version, cross-project, **marker collision qua API thật** (2 command khác IdempotencyKey, cùng
  target/actor/message → marker giống hệt → command thứ 2 bị `ErrLocalCommitMarkerCollision`), determinism
  của `computeOperationMarker` (input giống → marker giống; đổi 1 field → marker khác, 6 biến thể).
- `internal/app/releasesetcommit/execute_test.go` (real sqlite + real gitworktree, KHÔNG mock git nào) — mỗi
  test tự dựng 1 repo Git thật + 1 workspace Git thật qua `gitworktree.Provider.Provision` với Locator share
  giữa 2 hệ:
  - Happy path: commit thật được tạo, message HEAD chứa marker trailer, job hoàn tất
    (`CompleteJob` lần 2 → `ErrJobLeaseLost`), write lease đã release (probe worker acquire lại thành công),
    `git remote` rỗng trước/sau.
  - Replay: gọi `ExecuteReleaseSetLocalCommit` 2 lần với cùng job (đã COMMITTED) — spy
    `LocalCommitCreator` xác nhận `calls == 1` cả trước lẫn sau lần gọi thứ 2.
  - **Crash before Git**: worker A acquire write lease rồi "crash" (không gọi Git) — TTL hết hạn,
    `RecoverExpiredJobs`, worker B claim lại — spy xác nhận đúng 1 lần gọi `CreateLocalCommit`, đúng 2 commit
    trong lịch sử (base + 1).
  - **Crash after Git, before finalize** (kịch bản an toàn quan trọng nhất): worker A tự tay lặp lại đúng các
    bước `ExecuteReleaseSetLocalCommit` sẽ làm — acquire lease, `pinParent`, gọi THẬT
    `provider.CreateLocalCommit` — rồi dừng lại (không finalize, không release lease). Sau khi TTL hết hạn và
    job được reclaim, worker B gọi `ExecuteReleaseSetLocalCommit` thật — spy xác nhận `calls == 0` (không tạo
    commit thứ 2), `ResultVCSObjectID` trùng khớp commit mà worker A đã tạo, tổng số commit vẫn đúng 2.
  - **Marker drift → quarantine**: pin một parent SAI có chủ đích rồi tạo commit thật mang marker đúng nhưng
    parent thật khác parent đã pin — `ExecuteReleaseSetLocalCommit` phát hiện lệch, gọi
    `QuarantineRepositoryWorkspace` thật (assert `RepositoryWorkspace.State == QUARANTINED`), đóng operation
    `FAILED/MARKER_DRIFT`, `spy.calls == 0`.
  - **Lease loss**: worker A acquire write lease (TTL ngắn) + tạo commit thật, TTL hết hạn, worker C (job
    khác) "cướp" write lease — worker A "tỉnh dậy" (job lease riêng vẫn còn hạn 10 phút) gọi lại
    `ExecuteReleaseSetLocalCommit` → nhận `ErrLocalCommitWriteLeaseConflict`, intent vẫn REQUESTED (chưa bao
    giờ finalize).
- `internal/app/releasesetcommit/handler_test.go`: `Handler` implement `workerpool.Handler`, `Handle` tạo
  commit thật end-to-end.

### Verify

- **Replay/concurrency/stale**: `commands_test.go` (fake ở receipt layer) + `execute_test.go` Replay test
  (thật, spy call-count).
- **Seal/abandon**: đã có coverage thật từ V5-10A (`release_set_test.go`), không lặp lại — phần thiếu (list/
  get) đã đóng bởi `release_set_queries_test.go`.
- **Crash before/after Git/finalize**: 2 test riêng biệt trong `execute_test.go`, mô tả ở Test phía trên —
  "crash after Git before finalize" là bằng chứng trực tiếp nhất cho "Hoàn thành khi: replay cannot create
  two local commits".
- **Lease loss**: `TestExecuteReleaseSetLocalCommit_WriteLeaseStolen_DoesNotFinalize` (execute_test.go) +
  `TestValidateLocalCommitWriteLease_Stolen_ReturnsLost` (adapter-level, deterministic).
- **Drift**: `TestExecuteReleaseSetLocalCommit_MarkerDrift_QuarantinesAndFails` — quarantine thật, không
  fabricate.
- **Two workers**: `TestAcquireLocalCommitWriteLease_TwoWorkers_OnlyOneSucceeds` — goroutine thật, real race.
- **Marker collision**: `TestRequestReleaseSetLocalCommit_MarkerCollision_DifferentIdempotencyKey_Rejected`
  (qua API công khai thật, không bypass) + `TestComputeOperationMarker_Deterministic_DifferentInputsDifferentMarkers`.
- **Remote-call spy zero**: 3 lớp bằng chứng độc lập — (1) structural: `TestDomainAppNeverImportAdapters`
  (archtest có sẵn, tự động glob `internal/app/...` — `internal/app/releasesetcommit` không thể import bất kỳ
  `internal/adapters/...` nào, nghĩa là không có adapter remote nào để gọi dù cố tình); (2) behavioral: spy
  `LocalCommitCreator` trong mọi test `execute_test.go` đếm chính xác số lần gọi git thật; (3) real-repo: mọi
  fixture repo trong `execute_test.go`/`localcommit_marker_test.go` không hề cấu hình remote — `git remote`
  được assert rỗng cả trước/sau trong happy-path test, mirror đúng
  `TestProvider_CreateLocalCommit_NeverTouchesRemote` của V5-10A.
- `go build ./...`, `go vet ./...` sạch. `go run ./cmd/docs-coverage-check` debt = 0.

### Kết quả

PR #38 (`feat/v6-10e-releaseset-local-commit-authority`), merged `0511f6a`. Subagent's own worktree/session
kết thúc giữa chừng không kịp ship (implementation+test đã xong, chưa commit) — supervising session tìm thấy
qua `git status` trong worktree, tự review kỹ phần logic an toàn nhất (dual-lease fencing, marker
crash-recovery) trước khi ship, rồi phát hiện 2 bug thật qua CI thay vì merge mù: (1) migration `0036` trùng
với V6-10G (đã merge trước) — git không thấy conflict (2 filename khác nhau) nhưng migration loader thật
throw lỗi trùng version lúc runtime — đổi sang `0037`, cập nhật 2 test hardcode migration count (35→36); (2)
3 test crash-recovery của chính task này dùng TTL 60ms quá chật cho CI thật (Windows, sqlite thuần Go không
cgo, I/O đĩa thật) — fail với lỗi y hệt một flake đã biết khác (`TestRecoveryReaperHandler_...`) khiến suýt bị
gộp nhầm là "cùng 1 flake", nhưng so TTL của test kia (30 giây, cùng cơ chế lease) mới lộ ra 60ms là hiệu
chỉnh sai, không phải hạ tầng CI không ổn định — sửa TTL 60ms→600ms, sleep→1500ms, xác nhận 10/10 lần chạy
local sạch. Cũng dính đúng flake `TestSupervisorNormalExit_...` đã biết 3 lần liên tiếp trên PR này (mỗi lần
đều re-verify diff-scope + local repro trước khi rerun, không rerun mù) — lần rerun thứ 3 xanh. Flag thêm
task nền `task_8d129e53` để sửa tận gốc flake đó sau, thay vì tiếp tục rerun vô thời hạn.

## V6-10I — Adapter-build command envelope và receipt hardening (PR #36, merged `4e44af1`)

### Bối cảnh

V6-10I là 1 trong 3 task nhóm P1 chạy song song ngay sau V6-00A (`sau V6-00A: {V6-03, V6-10E, V6-10G,
V6-10I}`), unblocked khi V6-00A/V1-06/V1-07A/V2-07A/V2-07B đều đã đóng. Trích nguyên văn task spec từ
`docs/design/08-v6-api-projections.md`: "harden Probe/Register before any HTTP/CLI exposure"; "receipt/hash
lookup before external I/O; probe replay exact stored candidate even expired; new key probes anew. Register
re-probes outside tx then atomically insert-if-absent + event + receipt; Actor supplies RegisteredBy";
"Không làm: no HTTP, ProjectID or process/file I/O inside transaction"; "Hoàn thành khi: no public legacy
call lacks CommandEnvelope and fingerprint dedupe is not command replay."

Đây là gap thật, không phải task xây mới: `internal/app/adapterbuild` (V2-07A/V2-07B, đã tồn tại từ trước)
có đúng 4 command (`ProbeAdapterBuild`, `RegisterAdapterBuild`, `ListAdapterBuilds`, `GetAdapterBuild`) nhưng
`ProbeAdapterBuild`/`RegisterAdapterBuild` — 2 command MUTATING duy nhất trong package — chưa từng nhận một
`ports.Command` envelope nào cả: chữ ký cũ là `ProbeAdapterBuild(ctx, uow, req)`/`RegisterAdapterBuild(ctx,
uow, req)`, không idempotency key, không receipt lookup, không domain event nào được emit khi register thành
công. `RegisterRequest.RegisteredBy` là một field caller-supplied tự do — đúng thứ "never trust a separate
caller-supplied field" mà task cảnh báo. Đây chính là "no public legacy call lacks CommandEnvelope" task này
phải đóng.

### Nghiên cứu

Đọc toàn bộ package trước khi viết dòng nào: `internal/app/adapterbuild/commands.go` (246 dòng gốc),
`drift.go`, cả 2 file test tương ứng, `internal/app/ports/adapterbuild.go` (interface
`AdapterBuildRepository`), `internal/adapters/sqlite/adapterbuild.go` (implementation thật), và
`cmd/aw/adapter.go` (CLI `aw adapter probe|register|list|show`, caller sản xuất duy nhất trong repo ngoài
test).

Phát hiện chính:

- `ProbeAdapterBuild` đo `HashExecutableFile` (I/O thật) VÔ ĐIỀU KIỆN mỗi lần gọi — không hề có khái niệm
  idempotency key nào cả, nên "hai lần gọi giống hệt nhau" hiện tại nghĩa là "probe lại thật hai lần", không
  phải replay.
- `RegisterAdapterBuild` đã đúng thứ tự re-hash-ngoài-tx-trước-transaction (ADR-022) TỪ TRƯỚC — không cần sửa
  phần đó — nhưng hoàn toàn không có receipt/event nào: transaction chỉ gọi
  `tx.AdapterBuilds().InsertIfAbsent`, không `tx.Events().Append`, không `tx.Receipts().Record`.
- `internal/archtest/boundary_test.go` đã có sẵn
  `TestRegisterAdapterBuildTransactionNeverCallsFilesystemOrProcess` (parse AST thật, tìm func literal truyền
  vào `uow.WithSerializedWrite` bên trong `RegisterAdapterBuild`, cấm gọi `os`/`exec`/`ioutil` hoặc identifier
  `hashExecutableFile`) — ĐÚNG như task spec cảnh báo trước ("THIS EXACT TEST MAY ALREADY EXIST"). Nhưng đọc
  kỹ phát hiện một bug thật trong chính test đó: `forbiddenCalls` chỉ có `"hashExecutableFile"` (chữ h
  thường) trong khi hàm thật export là `HashExecutableFile` (chữ H hoa) — nếu implementation mới lỡ gọi hàm
  này bên trong transaction, test KHÔNG bắt được, vì so sánh identifier phân biệt hoa/thường. Không phải lỗi
  cần fix ngay lập tức của V2-07B (chưa ai từng phạm lỗi đó), nhưng là lỗ hổng thật của chính bộ test này.
- Đọc `internal/app/catalog/commands.go` (`CreateProject`, vừa merge PR #34, V6-03) — pattern receipt chuẩn:
  `Load` trước, so `RequestHash`, không khớp thì `ErrReceiptConflict`, khớp thì unmarshal `ResultJSON` trả
  thẳng; không tìm thấy thì làm việc thật, rồi `Record` + emit event trong CÙNG MỘT `WithSerializedWrite`. Tuy
  nhiên pattern đó không có I/O thật nào cả (Project là pure DB insert) — khác V6-10I: Probe/Register có I/O
  filesystem thật (`HashExecutableFile`), nên "receipt lookup" ở đây PHẢI tách hẳn ra một bước ĐỌC-ONLY riêng
  (`WithReadOnly`) chạy TRƯỚC khi chạm tới filesystem, không thể gộp lookup+work+record trong cùng một
  transaction như `CreateProject` làm được (transaction đó không có I/O thật nào để tránh).
- `ports.ErrScopeMismatch` (sentinel mới của chính V6-03, đặt trong `internal/app/ports/command.go`) — đúng
  công cụ có sẵn cho "installation-scoped" check ADR-025 yêu cầu cho `ProbeAdapterBuild`/`RegisterAdapterBuild`
  (bảng closed-set §27 liệt kê đúng 2 tên này ở cột Command installation). Ban đầu định tự tạo sentinel riêng
  trong `adapterbuild` package rồi phát hiện `ports.ErrScopeMismatch` đã tồn tại đúng mục đích này — dùng lại,
  không tạo sentinel thứ hai trùng ý nghĩa.
- `internal/domain/adapterbuild.CandidateTuple`/`CandidateToken` đã có đủ field task spec liệt kê
  ("executable/provider/protocol/capability/OS/config/nonce/expiry") từ trước (V2-07A) — không cần sửa domain
  struct nào, "Candidate binds ..." của task spec đã đúng nguyên trạng.
- `internal/app/eventschema`/`internal/archtest/event_catalog_test.go` (V6-00A's CI inventory guard) — mọi
  `ports.DomainEvent{}` composite literal dưới `internal/app` bị scan AST thật và bắt buộc có decoder đăng ký;
  `adapterbuild` package trước giờ CHƯA từng emit event nào nên chưa nằm trong danh sách composed registry
  của test đó — phải tự thêm `adapterbuild.RegisterEventSchemas(registry)` vào chính test này khi thêm event
  đầu tiên, nếu không CI guard sẽ tự fail (đúng cơ chế fail-closed nó được thiết kế để làm).

### Quyết định

1. **Nhận `cmd ports.Command` làm tham số bắt buộc** ở cả `ProbeAdapterBuild` và `RegisterAdapterBuild` — đổi
   chữ ký công khai, breaking change có chủ đích, không giữ overload cũ (không có polyglot version nào khác
   gọi 2 hàm này ngoài `cmd/aw/adapter.go` và test trong chính repo).
2. **Receipt lookup tách thành helper riêng (`loadProbeReplay`/`loadRegisterReplay`), chạy qua
   `uow.WithReadOnly` TRƯỚC bất kỳ I/O thật nào** — không gộp chung với transaction ghi, vì I/O thật
   (`HashExecutableFile`) phải nằm sau bước lookup này, không phải bên trong nó.
3. **Probe replay trả `CandidateToken` y hệt đã lưu, kể cả đã hết hạn** — không gọi lại `VerifyToken`/kiểm tra
   `ExpiresAt` nào trên đường replay; expiry chỉ có ý nghĩa tại thời điểm `RegisterAdapterBuild` verify token,
   không phải tại thời điểm probe tự đọc lại chính nó.
4. **Register cũng có receipt lookup riêng, chạy TRƯỚC `VerifyToken`/re-hash** — một replay hợp lệ không bao
   giờ re-verify token hay chạm filesystem lần nữa, kể cả khi executable đã bị xoá/đổi sau lần commit đầu
   tiên (đúng kịch bản "crash-after-commit-before-ack": caller không thấy response, retry, thế giới đã đổi,
   nhưng lệnh CŨ vẫn phải trả đúng kết quả CŨ).
5. **`RegisterRequest` bỏ hẳn field `RegisteredBy`** — `RegisterAdapterBuild` tự lấy `cmd.Actor`. Reject luôn
   nếu `cmd.Actor` rỗng, lỗi rõ ràng thay vì để domain layer tự báo lỗi mơ hồ hơn.
6. **Re-load receipt NGAY SAU KHI `Record` thành công, dùng giá trị re-load làm kết quả trả về cuối cùng** —
   không tin trực tiếp giá trị vừa tính trong bộ nhớ (`signed`/`computed`). Lý do: hai writer đua cùng một
   idempotency key đều tự tính ra giá trị RIÊNG của mình trước khi biết ai thắng (Probe: nonce/expiry khác
   nhau mỗi lần `SignToken`; Register: `AlreadyExisted` khác nhau tuỳ ai chạm `InsertIfAbsent` trước) — SQLite
   serialize 2 lệnh `WithSerializedWrite`, đúng 1 lệnh `Record` insert thật, lệnh còn lại là no-op (ON
   CONFLICT DO NOTHING, `receiptsRepository.Record`'s sẵn có). Nếu không re-load, writer thua sẽ trả về giá
   trị CỦA CHÍNH NÓ (chưa từng persist) thay vì giá trị đã thắng — vi phạm đúng "replay exact stored
   candidate" khi xảy ra concurrency, dù trường hợp không-đua vẫn đúng.
7. **`RegisterResult`/`CandidateToken` lưu vào `Receipt.ResultJSON` qua một DTO riêng (`registerReceiptPayload`)
   thay vì marshal thẳng `adapterbuild.Build`** — `Build` không có field export (bất biến theo thiết kế từ
   V2-07A), `json.Marshal` trực tiếp sẽ ra `{}`. DTO round-trip qua `Build.Tuple()`/`CapabilityManifest()`/
   `RegisteredBy()`/`RegisteredAt()` và `adapterbuild.NewBuild` khi decode lại — kỹ thuật giống hệt
   `cmd/aw/adapter.go`'s `adapterBuildView` đã dùng cho CLI JSON output.
8. **Event `AdapterBuildRegistered` chỉ emit khi `!alreadyExisted`** — một lệnh register KHÁC (idempotency
   key khác) trùng đúng fingerprint executable đã đăng ký trước đó vẫn được `Record` receipt của RIÊNG lệnh
   đó, nhưng KHÔNG emit lại event cho aggregate đã có sẵn (Sequence=1 chỉ được phép xảy ra đúng 1 lần cho mỗi
   `AggregateID`, `Events().Append` sẽ tự reject nếu emit lại). Đây chính là ranh giới "fingerprint dedupe is
   not command replay": fingerprint dedupe (`InsertIfAbsent`) quyết định có emit event mới hay không; receipt
   (command replay) quyết định caller nhận lại kết quả cũ mà không redo việc gì — hai trục độc lập, không
   trục nào thay thế trục kia.
9. **Dùng `ports.ErrScopeMismatch`** (sentinel chung V6-03 vừa tạo) cho check installation-scope, không tạo
   sentinel riêng trong `adapterbuild` — nhất quán với đúng tinh thần chính doc comment của
   `ports.ErrScopeMismatch` đã tự dự đoán ("ListProjects/GetProject sau này chắc chắn không phải named
   command/query duy nhất cần enforce đúng luật này").
10. **Mở rộng `TestRegisterAdapterBuildTransactionNeverCallsFilesystemOrProcess` sang cả `ProbeAdapterBuild`**
    (test mới `TestProbeAdapterBuildTransactionNeverCallsFilesystemOrProcess`, factor chung 2 helper
    `findWithSerializedWriteClosure`/`assertClosureNeverCallsFilesystemOrProcess`) và sửa `forbiddenCalls`
    thêm cả `"HashExecutableFile"` (chữ hoa đúng) bên cạnh `"hashExecutableFile"` cũ — đóng đúng lỗ hổng phát
    hiện ở bước Nghiên cứu, không xoá bản cũ (giữ lại để chính bộ test tự chứng minh nó bắt được cả 2 cách
    viết).
11. **Không đổi domain package `internal/domain/adapterbuild`** — `CandidateTuple`/`CandidateToken` đã đúng
    hình dạng task spec yêu cầu; toàn bộ thay đổi nằm ở tầng application command + event registry + CLI.

### Thực hiện

- `internal/app/adapterbuild/commands.go` (viết lại hoàn toàn, giữ nguyên toàn bộ logic hash/drift/sign gốc):
  `ProbeAdapterBuild(ctx, uow, cmd ports.Command, req ProbeRequest)`, `RegisterAdapterBuild(ctx, uow, cmd
  ports.Command, req RegisterRequest)`; helper mới `requireInstallationScope`, `loadProbeReplay`,
  `loadRegisterReplay`, `registerReceiptPayload`/`newRegisterReceiptPayload`/`toResult`. `RegisterRequest`
  không còn field `RegisteredBy`.
- `internal/app/adapterbuild/event_schema.go` (file mới): `AdapterBuildRegisteredEventType =
  "AdapterBuildRegistered"`, `AdapterBuildRegisteredSchemaVersion = 1`,
  `adapterBuildRegisteredEventPayload{BuildID, ProviderKey, ExecutablePath, ExecutableContentHash,
  ProtocolVersion, OS, Toolchain, ConfigIdentity, RegisteredBy, RegisteredAt}`,
  `DecodeAdapterBuildRegisteredV1`, `RegisterEventSchemas`.
- `internal/app/adapterbuild/testdata/golden/adapter_build_registered_v1.json` (fixture mới) +
  `event_schema_test.go` (file mới): `TestAdapterBuildRegisteredV1_GoldenFixtureDecodes`,
  `TestAdapterBuildRegisteredV1_RealEventPayloadDecodes` — mirror đúng
  `internal/app/catalog/event_schema_test.go`.
- `internal/archtest/boundary_test.go`: refactor `TestRegisterAdapterBuildTransactionNeverCallsFilesystemOrProcess`
  dùng 2 helper chung mới (`findWithSerializedWriteClosure`, `assertClosureNeverCallsFilesystemOrProcess`);
  thêm `TestProbeAdapterBuildTransactionNeverCallsFilesystemOrProcess` (test mới); mở rộng `forbiddenCalls`
  thêm `"HashExecutableFile"`.
- `internal/archtest/event_catalog_test.go`: import `internal/app/adapterbuild`, thêm
  `adapterbuild.RegisterEventSchemas(registry)` vào registry tổng hợp của
  `TestEmittedDomainEventInventoryMatchesRegisteredInventory`.
- `cmd/aw/adapter.go`: `runAdapterProbe`/`runAdapterRegister` thêm flag `--idempotency-key` (bắt buộc) và
  `--actor` (mặc định `"operator"`, thay hẳn `--registered-by` cũ ở register); dựng `ports.Command` qua
  `newDefinitionCommand`/`requestHash` (2 helper có sẵn từ `cmd/aw/definition.go`, cùng package `main`, không
  viết lại) với `ports.InstallationScope()`; lỗi `ports.ErrReceiptConflict` map qua `mapReceiptConflict` có
  sẵn.
- `cmd/aw/adapter_test.go`: helper mới `freshIdempotencyKey`/`registerAdapterBuild` (dùng `sync/atomic`
  Counter để mỗi lần gọi CLI trong test có 1 idempotency key riêng — mọi test hiện có trong file này kiểm tra
  trục fingerprint/drift/TOCTOU/expiry/signature, không phải trục command replay, nên phải giữ mỗi lệnh CLI
  độc lập, không vô tình trùng key); thay toàn bộ `--registered-by` bằng gọi qua helper mới; thêm
  `TestAdapter_MissingIdempotencyKeyIsUsageError`.
- `internal/adapters/sqlite/adapterbuild_test.go`, `internal/adapters/sqlite/workflowcompiler_integration_test.go`,
  `internal/integration/definitionplane_test.go`: cập nhật mọi call site còn lại (chỉ có đúng 2 file sqlite +
  1 file integration ngoài `internal/app/adapterbuild`/`cmd/aw` từng gọi 2 hàm này) sang chữ ký mới, dựng
  `ports.Command` tối thiểu hợp lệ tại chỗ.

### Test

- `internal/app/adapterbuild/commands_test.go` (fake uow, cập nhật toàn bộ test cũ sang chữ ký mới + 8 test
  mới): `TestProbeAdapterBuild_RejectsProjectScopedCommand`,
  `TestRegisterAdapterBuild_RejectsProjectScopedCommand` (cả 2 assert `errors.Is(err,
  ports.ErrScopeMismatch)`), `TestProbeAdapterBuild_ReplaySameCommand_ReturnsExactCandidate_NoNewIO` (xoá
  hẳn executable sau lần probe thật đầu tiên rồi gọi lại cùng command — nếu implementation lỡ re-probe sẽ
  fail vì file không còn, thành công nghĩa là chứng minh được KHÔNG có I/O mới xảy ra, mạnh hơn một bộ đếm
  spy thông thường), `TestProbeAdapterBuild_ReplayAfterExpiry_ReturnsExactCandidateEvenExpired` (seed thẳng
  một receipt với `CandidateToken` đã hết hạn 24 giờ qua `tx.Receipts().Record`, trỏ `ExecutablePath` tới file
  không tồn tại — probe vẫn phải trả đúng candidate đã hết hạn, không lỗi, không re-probe),
  `TestProbeAdapterBuild_SameKeyDifferentHash_IsReceiptConflict`,
  `TestProbeAdapterBuild_NewIdempotencyKey_AlwaysProbesAnew` (2 probe cùng executable, 2 idempotency key khác
  nhau — nonce PHẢI khác nhau, tuple đo được PHẢI giống nhau).
- `internal/app/adapterbuild/commands_sqlite_test.go` (file mới, real sqlite, vì fake's
  `WithSerializedWrite` unlock trước khi chạy `fn` nên không tạo contention thật — đúng lý do
  `internal/app/catalog/commands_sqlite_test.go` đã giải thích cho trường hợp tương tự):
  `TestProbeAdapterBuild_ConcurrentSameKey_AllCallersGetIdenticalToken` (8 writer cùng idempotency key/hash,
  đối chiếu bằng chuỗi `Signature|Nonce|ExpiresAt` — mọi writer PHẢI giống hệt writer 0, cộng
  `store.CountCommandReceiptsByIdempotencyKey` xác nhận đúng 1 dòng),
  `TestRegisterAdapterBuild_ConcurrentSameKey_ExactlyOneBuildOneEvent` (8 writer cùng token/command, đối
  chiếu `Build.ID()`/`AlreadyExisted` giống hệt nhau, `store.CountDomainEvents("AdapterBuildVersion", id)`
  đúng 1, `CountCommandReceiptsByIdempotencyKey` đúng 1),
  `TestRegisterAdapterBuild_ReplayAfterCommit_SimulatesCrashBeforeAck` và
  `TestProbeAdapterBuild_ReplayAfterCommit_SimulatesCrashBeforeAck` (gọi thật lần 1 để commit, xoá executable,
  gọi lại với CÙNG command envelope — retry phải thành công và trả đúng kết quả cũ mà không chạm file đã bị
  xoá, đúng kịch bản "crash-after-commit-before-ack" thật, không giả lập bằng cách sửa DB tay).
- `internal/app/adapterbuild/event_schema_test.go`: 2 test golden/round-trip cho `AdapterBuildRegistered` v1.
- `internal/archtest`: `TestRegisterAdapterBuildTransactionNeverCallsFilesystemOrProcess` (đã có, giờ pass
  với transaction mới có thêm `Receipts()`/`Events()` calls),
  `TestProbeAdapterBuildTransactionNeverCallsFilesystemOrProcess` (test mới),
  `TestEmittedDomainEventInventoryMatchesRegisteredInventory` (tự động nhận `AdapterBuildRegistered` v1 vào
  cả 2 phía registered/emitted — log xác nhận "41 registered key(s), 41 emitted key(s), 0 missing
  decoder(s)").
- `cmd/aw/adapter_test.go`, `internal/adapters/sqlite/adapterbuild_test.go`,
  `internal/adapters/sqlite/workflowcompiler_integration_test.go`, `internal/integration/definitionplane_test.go`:
  toàn bộ test cũ pass nguyên vẹn sau khi cập nhật call site, không có test nào bị xoá hay đổi ý nghĩa.
- `go build ./...`, `go vet ./...` sạch. `go test ./internal/app/adapterbuild/... -count=1 -v` (28 test) pass
  100%. `go test ./internal/archtest/... -count=1` pass. `go test ./cmd/aw/... -count=1` pass (12.8s). `go
  test ./... -count=1` (74 package sau khi merge `origin/master` nhận V6-03) pass 100%, 0 dòng `FAIL`, không
  regression ở bất kỳ package nào khác.

### Verify

- "replay/hash": `TestProbeAdapterBuild_ReplaySameCommand_ReturnsExactCandidate_NoNewIO`,
  `TestProbeAdapterBuild_SameKeyDifferentHash_IsReceiptConflict`, và tương ứng phía Register qua
  `loadRegisterReplay`'s test gián tiếp (`TestRegisterAdapterBuild_ReplayAfterCommit_SimulatesCrashBeforeAck`
  chính là replay/hash test thật với dữ liệu thật đã commit).
- "concurrency": `TestProbeAdapterBuild_ConcurrentSameKey_AllCallersGetIdenticalToken`,
  `TestRegisterAdapterBuild_ConcurrentSameKey_ExactlyOneBuildOneEvent` — cả 2 chạy trên sqlite thật, 8 writer
  race.
- "expired replay": `TestProbeAdapterBuild_ReplayAfterExpiry_ReturnsExactCandidateEvenExpired` — candidate hết
  hạn 24 giờ vẫn được trả nguyên vẹn.
- "new token": `TestProbeAdapterBuild_NewIdempotencyKey_AlwaysProbesAnew` — nonce khác nhau, tuple đo được
  giống nhau (executable không đổi).
- "crash-after-commit": `TestProbeAdapterBuild_ReplayAfterCommit_SimulatesCrashBeforeAck`,
  `TestRegisterAdapterBuild_ReplayAfterCommit_SimulatesCrashBeforeAck` — mô phỏng bằng xoá executable thật sau
  commit, không sửa DB tay.
- "drift/token mismatch": toàn bộ test drift cũ (`TestRegister_RejectsStaleTokenAfterExecutableSwapped`,
  `TestRegister_RejectsMismatchedCapabilityManifest`, `TestRegister_RejectsExpiredToken`,
  `TestRegister_RejectsTokenSignedUnderRotatedKey`) vẫn nguyên vẹn, chạy qua chữ ký mới.
- "event golden": `adapter_build_registered_v1.json` + 2 test decode, cộng archtest's
  `TestEmittedDomainEventInventoryMatchesRegisteredInventory` tự động xác nhận registered=emitted.
- "installation scope": `TestProbeAdapterBuild_RejectsProjectScopedCommand`,
  `TestRegisterAdapterBuild_RejectsProjectScopedCommand`, cả 2 dùng `ports.ErrScopeMismatch`.
- "no I/O-in-tx architecture test": `TestRegisterAdapterBuildTransactionNeverCallsFilesystemOrProcess` (đã có,
  giờ mở rộng `forbiddenCalls`) + `TestProbeAdapterBuildTransactionNeverCallsFilesystemOrProcess` (mới) — cả 2
  parse AST thật của `commands.go`, không phải chạy thử một lần rồi tin.
- "Hoàn thành khi": không còn public call nào tới `ProbeAdapterBuild`/`RegisterAdapterBuild` thiếu
  `ports.Command` (chữ ký cũ không còn tồn tại để gọi nhầm); fingerprint dedupe
  (`InsertIfAbsent`/`AlreadyExisted`) và command replay (receipt) là 2 trục độc lập được chứng minh riêng biệt
  — `TestRegister_DuplicateIsIdempotent` (2 command khác nhau, cùng fingerprint) tách bạch rõ với
  `TestRegisterAdapterBuild_ReplayAfterCommit_SimulatesCrashBeforeAck` (1 command, gọi lại 2 lần).

### Kết quả

`internal/app/adapterbuild.ProbeAdapterBuild`/`RegisterAdapterBuild` giờ nhận đủ `ports.Command` envelope,
có receipt/hash lookup trước mọi I/O thật, probe replay trả đúng candidate gốc kể cả đã hết hạn,
`RegisterAdapterBuild` derive `RegisteredBy` từ `cmd.Actor`, emit `AdapterBuildRegistered` v1 (đăng ký đầy đủ
qua `eventschema`) đúng 1 lần cho mỗi build mới, atomic cùng transaction với insert-if-absent và receipt.
`ports.ErrScopeMismatch` (sentinel chung từ V6-03) enforce installation-scope cho cả 2 command. 2 test kiến
trúc (`TestRegisterAdapterBuildTransactionNeverCallsFilesystemOrProcess` mở rộng +
`TestProbeAdapterBuildTransactionNeverCallsFilesystemOrProcess` mới) khoá cứng "không I/O trong transaction"
bằng AST thật cho cả 2 command, đồng thời đóng luôn một lỗ hổng có sẵn trong chính bộ test cũ (thiếu case chữ
hoa `HashExecutableFile`). CLI `aw adapter probe|register` chuyển hẳn sang `--idempotency-key`/`--actor`,
không còn `--registered-by`. 12 test mới trong `commands_test.go` (fake) + 4 test mới trong
`commands_sqlite_test.go` (real sqlite, concurrency/crash-replay) + 2 test golden/round-trip trong
`event_schema_test.go`. `go build/vet/test ./...` xanh 100% trên toàn bộ 74 package. V6-10J (adapter-build
registry HTTP endpoints) giờ có đủ điều kiện bắt đầu ngay khi các dependency còn lại (V6-00, V6-01A, V6-02,
V6-02A) sẵn sàng — "provider upgrade flow usable without bypassing command contract" không còn là mục tiêu
hoãn lại.

## V6-02 — HTTP CommandEnvelope, idempotency và optimistic concurrency

### Bối cảnh

Sau khi cả 7 task nhóm P1 (V6-01A/V6-02A/V6-15A/V6-03/V6-10E/V6-10G/V6-10I) merge xong, chuẩn bị đọc lại
dependency graph để xác định P2 — nhưng đọc kỹ lại chính §2 của design doc mới phát hiện: dòng "P2 —
application/HTTP slices sau V6-00 + V6-01A + V6-02 + V6-02A" đòi hỏi **V6-02** (không phải chỉ V6-02A), và
P1's own text có mũi tên riêng `V6-01A -> V6-02` (tách biệt khỏi nhóm ngoặc `{V6-01A, V6-02A, V6-15A}`) —
nghĩa là V6-02 chính nó là một task P1 thứ 8, chưa từng được triển khai (chỉ V6-02A, phần DTO/schema tách
riêng từ nó, đã xong), và nó khoá toàn bộ 11 task của P2 lại cho tới khi xong. Đây là một correction thật tự
phát hiện khi double-check lại graph trước khi báo cáo "P2 sẵn sàng" cho user — nếu không kiểm tra kỹ sẽ báo
sai. Quyết định tự làm trực tiếp (không giao subagent) vì V6-02 định nghĩa contract idempotency-key/semantic-
hash/receipt-replay mà MỌI HTTP mutation tương lai phải dùng chung — mức độ quan trọng ngang V6-01/V6-01A.

### Nghiên cứu

Đọc lại nguyên văn spec từ `docs/design/08-v6-api-projections.md`: "Idempotency-Key bắt buộc; update bắt
buộc strong If-Match. Semantic hash gồm command type, scope/target, normalized payload, exact content digest
và expected version; loại JSON formatting, request/correlation ID, session token và transport metadata. Flow:
authenticate/authorize → canonical decode → receipt lookup → replay/conflict → nếu absent mới kiểm current
version/external prework/dispatch; command transaction recheck receipt. Same-key committed replay thắng
ETag/state drift nhưng vẫn phải qua current authorization." — "Không làm: middleware không ghi/cache receipt
hoặc chạy business validation."

Tìm tiền lệ thật để mirror, không tự nghĩ mẫu mới:
- `cmd/aw/definition.go`'s own `requestHash(parts ...string) string` — SHA-256, NUL-separated parts,
  `"sha256:"` prefix — chính doc comment của nó tự khai "no production call site... computed a real
  RequestHash before this file", xác nhận đây là convention gốc cần mirror cho phía HTTP.
- `internal/app/ports/unitofwork.go`'s `ReceiptsRepository{Load, Record}` — `Load` (read-only, qua
  `uow.WithReadOnly`) là chính xác cái middleware CẦN dùng cho fast-path replay check; `Record` là cái
  middleware TUYỆT ĐỐI không được gọi (đúng "Không làm").
- `internal/app/ports/command.go`'s `ports.Command{ExpectedVersion uint64, RequestHash string, ...}` và
  `ports.Receipt{ResultJSON, ErrorCode}` — receipt lưu cả outcome LỖI (không chỉ thành công) để replay đúng
  cả case lỗi, không chỉ case thành công.
- `internal/app/catalog.CreateProject` (V6-03, vừa merge) — ví dụ THẬT gần nhất và sạch nhất của "command
  receipt idempotency check → real work → domain event → receipt record" trong một `WithSerializedWrite` —
  dùng trực tiếp làm target thật cho toàn bộ test tích hợp của task này, không cần dựng route/endpoint giả
  (V6-03A mới là task xây route thật — V6-02 chỉ xây primitive dùng chung).
- `internal/archtest/boundary_test.go`'s
  `TestRegisterAdapterBuildTransactionNeverCallsFilesystemOrProcess` — mẫu AST-scan "parse source thật, fail
  nếu gọi hàm cấm" — mirror y hệt cho architecture test riêng của task này.

### Quyết định

Xây V6-02 thành các primitive độc lập, tái sử dụng được (đúng phong cách V6-02A đã thiết lập — nhiều file nhỏ
tập trung, không phải một dispatcher khổng lồ) thay vì một hàm "xử lý mọi thứ" duy nhất — vì CHƯA có business
endpoint thật nào tồn tại để ép khuôn theo (V6-03A trở đi mới xây route), ép một shape cụ thể quá sớm sẽ phải
sửa lại khi endpoint đầu tiên thật sự cần. Mỗi future endpoint task tự gọi đúng những primitive cần, theo
đúng thứ tự flow spec đã mô tả.

`SemanticHash` nhận thêm tham số `extraContentDigest` (rỗng cho command JSON thuần) — vì spec liệt kê
"normalized payload" và "exact content digest" là 2 ingredient TÁCH BIỆT, dành cho tương lai một số command
(vd V6-07A upload attachment) cần hash cả nội dung nhị phân không thể "normalize" như JSON — quyết định
không tự đoán shape của attachment ngay bây giờ, chỉ để chỗ trống tham số.

Chiều ngược "middleware không ghi/cache receipt" được biến thành một architecture test thật (không chỉ ghi
trong comment): AST-scan toàn bộ `internal/delivery/httpapi` cấm gọi `.Record(` hoặc `.WithSerializedWrite(`
— nếu ai đó (kể cả chính future endpoint task) lỡ thêm logic ghi receipt vào package này, CI fail ngay.

"HTTP↔aw same-key result" (Verify bullet khó nhất để chứng minh vì `aw`'s own `requestHash` là hàm unexported
trong package `main`) được chứng minh bằng cách dispatch `catalog.CreateProject` 2 lần với 2 `ports.Command`
envelope có field giống hệt (chỉ khác `CorrelationID` nội bộ, mô phỏng "một request logic y hệt gửi qua 2
transport khác nhau") — chứng minh đúng bản chất: `ports.Command`+receipt là ranh giới DUY NHẤT mọi transport
phải đi qua, bản thân command function không hề biết ai gọi nó.

### Thực hiện

- `internal/delivery/httpapi/commandenvelope.go`: `IdempotencyKeyHeader`/`IfMatchHeader`/`ETagHeader` hằng
  số; `RequireIdempotencyKey`/`RequireIfMatch` (400-shaped typed error nếu thiếu); `ETagFromVersion`/
  `VersionFromETag` (strong ETag `"N"`, round-trip); `CanonicalizeJSON` (tái dùng `DecodeJSON` từ V6-01 để
  strict-decode, rồi re-marshal lấy byte canonical — key order độc lập, whitespace độc lập); `SemanticHash`
  (command type + `scope.Key()` + normalized payload + extraContentDigest + expected version, NUL-separated,
  sha256, mirror đúng `requestHash` của `cmd/aw`).
- `internal/delivery/httpapi/receiptreplay.go`: `LookupReceipt` (read-only qua `uow.WithReadOnly` +
  `tx.Receipts().Load` — helper duy nhất package này được phép chạm receipt); `ErrReceiptHashConflict` +
  `ReconcileReceipt` (so hash thuần, không I/O); `WriteReceiptReplay` (ghi lại verbatim outcome đã lưu — kể
  cả case lỗi, map qua `StatusForAppErrorCode` của V6-02A); `EncodeResult` (JSON + ETag header cho response
  mới, không phải replay).
- `internal/archtest/command_envelope_test.go`: `TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt` — AST-scan
  thật, cấm `.Record(`/`.WithSerializedWrite(` bất kỳ đâu trong `internal/delivery/httpapi`.

### Test

- `commandenvelope_test.go`: **reordered JSON same hash** (`TestSemanticHash_ReorderedJSONProducesSameHash`
  — 2 request body khác thứ tự key, hash giống hệt) + hash khác nhau cho command type/scope/expected-
  version/extra-digest khác nhau (mỗi trường hợp một test riêng, không gộp); `TestCanonicalizeJSON_RejectsUnknownField`;
  ETag round-trip + malformed rejection.
- `receiptreplay_test.go` (real sqlite, real `catalog.CreateProject`, không fake DB nào):
  - **same-key replay**: `TestCreateProject_SameKeySameBody_ReplaysExactSameProjectID` — dùng
    `idsource.NewSequential` (không phải Random) để nếu code lỡ sinh ID mới ở nhánh replay sẽ lộ ra ngay
    ("project-2" thay vì "project-1"), không phải một ID ngẫu nhiên trông có vẻ hợp lý.
  - **same-key replay sau restart và crash-after-commit**: `TestCreateProject_SameKeyReplay_AfterSimulatedRestart`
    — đóng thật file sqlite, mở lại (restart thật, không phải context mới trên cùng connection), xác nhận
    receipt còn sống + replay đúng ProjectID gốc + `ListProjects` xác nhận đúng 1 row (không tạo trùng).
  - **different body conflict trước I/O**: `TestReconcileReceipt_...` (thuần, không I/O) +
    `TestCreateProject_SameKeyDifferentBody_ConflictsBeforeSecondInsert` (thật, qua command thật).
  - **stale key mới**: `TestCreateProject_StaleKey_NewIdempotencyKeyAlwaysProceedsAsNew` — key mới không bao
    giờ tình cờ trùng với receipt của key khác.
  - **concurrent keys**: `TestCreateProject_ConcurrentKeys_BothSucceedIndependently` — 2 goroutine thật, 2
    key khác nhau, cả 2 phải thành công độc lập, không cross-talk.
  - **HTTP↔aw same-key result**: `TestHTTPAndCLI_SameKeyResult` — mô tả đầy đủ trong doc comment của chính
    nó tại sao đây là bằng chứng đúng bản chất dù không gọi được hàm `requestHash` unexported của `aw`.
- `go build ./...`, `go vet ./...` sạch. `go test ./...` toàn bộ module (79 package) pass 100%, chạy trên
  master sạch không có subagent nào khác chạy đồng thời (loại trừ hẳn khả năng resource-contention false
  alarm đã gặp trước đó trong phiên này).

### Verify

- `go test ./internal/delivery/httpapi/... ./internal/app/...` xanh — đúng lệnh Verify line của chính task.
- "architecture test delivery không ghi receipt/repository transaction": `TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt`
  chứng minh thật bằng AST, không chỉ đọc code bằng mắt.
- "Hoàn thành khi": "retry không duplicate aggregate/job/external operation và không có transport receipt
  authority" — đúng: mọi test replay xác nhận `ListProjects` luôn đúng 1 row sau N lần retry, và receipt
  authority duy nhất vẫn là application command's own transaction, package `httpapi` không hề ghi gì.

### Kết quả

Package `internal/delivery/httpapi` có thêm 2 file production (`commandenvelope.go`, `receiptreplay.go`) +
2 file test (20 test mới) + 1 architecture test mới trong `internal/archtest`. Phát hiện và sửa một sai lệch
thật trong chính bàn giao trước đó của phiên này: P2 KHÔNG unblock ngay sau P1 như tưởng — V6-02 là điều kiện
còn thiếu, giờ đã đóng. `{V6-02, V6-02A, V6-15A} -> V6-15B` giờ đủ cả 3 điều kiện; toàn bộ 11 task P2
(`V6-03A, V6-04, V6-05, V6-06, V6-06A, V6-06D, V6-07, V6-07B, V6-10B, V6-10H, V6-10J`) chính thức unblock từ
đây. Merge PR #39 (`draculemihawk123-ai/aw-tqunglnh#39`, squash commit `60a9f8f`), xác nhận độc lập bằng
`git log origin/master` (không dùng `merge-base --is-ancestor` với SHA nhánh gốc vì squash tạo commit mới,
không giữ SHA cũ).

**Sự cố CI ngoài code đáng ghi lại**: 3 job cuối (`Linux race and stability`, `spike acceptance` ubuntu/
windows) bị kẹt ở trạng thái `queued`, không runner nào nhận, suốt hơn 7 tiếng (04:10 UTC → 11:11 UTC) —
kiểm tra `gh api repos/.../actions/runs/{id}/jobs` thấy `runner_id`/`runner_name` đều `null`. Xác nhận qua
githubstatus.com: GitHub có sự cố hạ tầng thật ("Incident with several GitHub Services", Actions degraded),
nhưng đã **resolve lúc 10:44 UTC** — tức 27 phút TRƯỚC khi phát hiện job vẫn còn kẹt, nghĩa là run không tự
phục hồi sau khi GitHub hết sự cố. Xử lý: `gh run cancel` rồi `gh run rerun --failed` — job mới nhận runner
thật ngay lập tức (`in_progress` sau vài giây) và xanh trong vài phút. **Bài học: nếu job đứng `queued` bất
thường lâu, kiểm tra `runner_id` qua API để phân biệt "đang chờ hàng đợi" với "kẹt hẳn không ai nhận"; nếu
kẹt, đừng chỉ chờ — kiểm tra githubstatus.com để xác nhận nguyên nhân, và nếu sự cố phía GitHub đã resolve mà
job vẫn kẹt, chủ động cancel+rerun thay vì tiếp tục chờ vô thời hạn.**

## V6-04 — WorkItem, family, readiness và scope endpoints

### Bối cảnh

V6-04 nằm trong nhóm P2 "application/HTTP slices sau V6-00 + V6-01A + V6-02 + V6-02A" (trích đúng dependency
graph mục 2 của `08-v6-api-projections.md`), cộng thêm phụ thuộc riêng V6-03. Cả 5 dependency đã merge:
V6-00 (PR #29), V6-01A (PR #35), V6-02 (PR #39), V6-02A (PR #33), V6-03 (PR #34). Bốn task chạy song song
ngay lúc task này bắt đầu — V6-03A, V6-04 (task này), V6-06, V6-10B — tất cả cùng ghi vào chính file
`baocaov6checklist.md` này.

Trích nguyên văn task spec (dòng 203-214, không diễn giải lại): "Mục tiêu: expose root/child WorkItem,
family/readiness và toàn bộ scope-expansion lifecycle... Phạm vi: create/list/authoritative detail/child/
readiness; scope request/approve/reject/withdraw... Không làm: không generic status/family/workspace
setter; không dùng projected detail để authorize... Thực hiện: root-create atomic, child subset, readiness
criteria explanation; `WithdrawScopeExpansion` thuộc task này, chỉ khi pending và không tạo grant/amendment.
Authoritative detail phân biệt với projected card/detail của V6-10."

Task này là task ĐẦU TIÊN trong repo thật sự triển khai một "business HTTP endpoint" hoàn chỉnh (route thật,
handler thật, dispatch command thật) — tại thời điểm branch từ `3af0adf`, V6-03A (candidate gần nhất, cùng
nhóm P2, cùng phụ thuộc) CHƯA có branch/PR nào tồn tại trên remote (`gh pr list`/`git branch -a` xác nhận
rỗng). Nghĩa là không có sibling nào đã merge để copy convention — mọi quyết định thực dụng (subpackage
layout, path-parameter routing, error-sentinel-to-httpapi mapping) phải tự rút ra từ việc đọc thẳng source
V6-01/V6-01A/V6-02/V6-02A, không phải "làm giống PR trước".

### Nghiên cứu

Đọc toàn bộ `internal/delivery/httpapi` (14 file production của V6-01/V6-01A/V6-02/V6-02A) trước khi viết
bất kỳ handler nào: `route.go` (`RouteDescriptor`/`RouteRegistry`, panic khi trùng `(Method,Path)` hoặc
`OperationID`), `commandenvelope.go` + `receiptreplay.go` (toàn bộ flow: `RequireIdempotencyKey`/
`RequireIfMatch` → `CanonicalizeJSON` → `SemanticHash` → `LookupReceipt` → `WriteReceiptReplay`/
`ErrReceiptHashConflict` → dispatch → `EncodeResult`), `errors.go` (7-giá-trị `ErrorCode`, hàm funnel
`WriteResourceHidden` cho leakage normalization, `StatusForAppErrorCode` bảng 22 `errorcode.Code` — CHỈ
hiểu `*apperror.Error`), `principal.go`/`security.go` (`PrincipalFromContext`, session-token middleware —
việc của composition root, không phải route task), `server.go` (`Config`/`NewServer`, middleware chain
thật, `mux.HandleFunc(d.Method+" "+d.Path, d.Handler)` — xác nhận dùng thẳng `net/http.ServeMux` gốc, không
router bên thứ ba).

Đọc `internal/archtest/command_envelope_test.go` — xác nhận `TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt`'s
`filepath.WalkDir(root, ...)` đệ quy vào MỌI subdirectory của `internal/delivery/httpapi`, nên một subpackage
mới tự động bị quét mà không cần sửa test đó — điểm này quyết định trực tiếp lựa chọn kiến trúc ở mục Quyết
định #2.

Đọc `internal/app/work/commands.go` (618 dòng) và `scope_expansion.go` (907 dòng) TOÀN BỘ trước khi viết
dòng handler đầu tiên — xác nhận chính xác 6 hàm public cần wrap (`CreateRootWorkItem`, `CreateChildWorkItem`,
`RequestScopeExpansion`, `ApproveScopeExpansion`, `RejectScopeExpansion`, `WithdrawScopeExpansion`) và liệt
kê ĐẦY ĐỦ mọi sentinel error mỗi hàm có thể trả về bằng cách đọc source thật, không đoán:
`ports.ErrPersistenceNotFound`, `ports.ErrCrossProjectReference`, `ports.ErrReceiptConflict`,
`ports.ErrOptimisticConflict` (chỉ từ `ApproveScopeExpansion`'s `TransitionTaskFamilyScopeVersion` CAS),
`ErrRepositoryNotActive`, `ErrScopeExpansionNotPending`, `ErrEffectiveScopeExceedsFamilyScope`,
`ErrCrossFamilyReference`, cộng các `errors.New(...)` trần (không sentinel) ở đầu mỗi hàm cho field bắt buộc
thiếu.

Ba phát hiện quan trọng nhất của task này:

1. **Contract field của WorkItem tồn tại trong schema nhưng chưa từng được đọc/ghi.** Migration
   `0007_work_items_contract.sql` (comment gốc: "Nothing in this repository writes a work_items row with
   any of these fields yet") thêm 6 cột nullable (`schema_version, behavior, acceptance_json,
   verification_json, risk, exclusions_json`) vào `work_items`, nhưng `createWorkItemTx`/`getWorkItemTx`/
   `scanWorkItemRow` (`internal/adapters/sqlite/work.go` dòng 37-131) chưa bao giờ đọc/ghi cả 6 cột đó —
   xác nhận trực tiếp bằng cách đọc SQL string thật trong cả `INSERT` (chỉ 11 cột identity/lineage) lẫn
   `SELECT` (chỉ thêm `workflow_version_id`), không suy diễn từ comment migration. Quyết định KHÔNG tự ý mở
   rộng 2 hàm đó trong task này — xem Quyết định #1.
2. **`readinesscheck.GetReadinessProfile`/`SetReadinessProfile` không phải cùng khái niệm với "readiness"
   của task này.** Đọc `internal/app/readinesscheck/profile.go` toàn bộ: 2 hàm đó thao tác
   `readiness.Profile` — setup/verification RECIPE ở mức REPOSITORY (đối chiếu
   `internal/domain/readiness/readiness.go`) — hoàn toàn khác per-WorkItem readiness EXPLANATION mà task
   này cần dựng. Xác nhận đúng như prompt đã cảnh báo trước, không phải đoán suông.
3. **Không có bất kỳ public application-layer READ query nào cho WorkItem/TaskFamily/ScopeExpansionRequest
   tồn tại trước task này.** `ls internal/app/work/` xác nhận không có file `queries.go` nào; chỉ có method
   thô `ports.WorkRepository.GetWorkItem`/`GetTaskFamily`/`GetScopeExpansionRequest`, dùng NỘI BỘ bởi chính
   các command, chưa từng expose ra ngoài. `internal/adapters/sqlite/work_queries.go` (204 dòng) toàn bộ là
   `Count*` helper cho rollback test, không có `List` thật nào. Không có `ListWorkItemsByProject`/
   `ListChildWorkItems` ở BẤT KỲ tầng nào — kể cả tầng port — phải tự thêm mới hoàn toàn.

Đọc ADR-025 nguyên văn (`docs/architecture/02-architecture-decisions.md` dòng 604-639): bảng closed-set
installation chỉ liệt kê `CreateProject`/safe-settings mutation/`ProbeAdapterBuild`/`RegisterAdapterBuild`
(command) và health/doctor/`ListProjects`/safe-settings read/adapter-build list-detail (query) —
WorkItem/TaskFamily/ScopeExpansionRequest hoàn toàn vắng mặt trong bảng đó, nên MỌI route/query của task
này là project-scoped, không có ngoại lệ. Cùng đoạn ADR tự nhắc: "Route `/adapter-builds` do đó nằm ngoài
cây `/projects/{id}`" — xác nhận URL convention `/projects/{projectId}/...` là cây đúng cho mọi resource
project-scoped khác, không phải suy đoán riêng của task này.

Đọc `internal/app/ports/fake/work.go` (773 dòng) TOÀN BỘ trước khi sửa — xác nhận
`var _ ports.WorkRepository = (*WorkRepository)(nil)` là compile-time assertion bắt buộc phải cập nhật
CÙNG LÚC với việc mở rộng interface, không có cách trì hoãn (toàn repo build sẽ gãy nếu quên).

Đọc `internal/delivery/httpapi/server_test.go`'s `newTestServer` (dòng 25-41) và
`TestServer_ServeHealthEndpoints_RealHTTPRoundTrip` — xác nhận đây là idiom "real TCP listener + real
middleware chain đầy đủ" đã có sẵn, tốt hơn hẳn tự dựng `http.ServeMux` tay; dùng lại nguyên shape này cho
test HTTP của task này thay vì phát minh lại.

Xác nhận `go.mod`'s `go 1.27.0` — đủ mới cho `net/http.ServeMux`'s pattern `{name}` + `r.PathValue()` (Go
1.22+) mà không cần router bên thứ ba. Grep toàn `internal/delivery/httpapi` tìm `PathValue`/`Path:.*{` ra
0 kết quả TRƯỚC task này — xác nhận đây là task đầu tiên trong repo thật sự dùng path parameter trong HTTP
layer, không có precedent nào để soi.

Grep 14 file dùng `apperror.New`/`apperror.Wrap` trong toàn repo (`internal/app/config`,
`internal/adapters/artifactstore`, `internal/adapters/sqlite`, `internal/app/runtime`,
`internal/adapters/repoprobe`, `internal/app/repositoryprobe`, `internal/app/eventschema`, và test file) —
không file nào trong `internal/app/work` hay `internal/app/catalog`. Xác nhận `httpapi.WriteAppError` (chỉ
hiểu `*apperror.Error` qua `errors.As`) không dùng được cho bất kỳ lỗi nào từ `internal/app/work` — phải tự
viết error-mapping riêng cho package này, enumerate từng sentinel bằng tay thay vì generic catch-all.

Đọc `internal/domain/work/work.go` toàn bộ — xác nhận `ValidateReadinessGate` là pure validator (không cần
sửa) và xác nhận chính xác 8 field thuộc "contract" của WorkItem (`SchemaVersion, Behavior,
AcceptanceCriteria, VerificationSpec, RiskLevel, Exclusions, WorkflowVersionID, ApprovalException`) — trong
đó `ApprovalException` không có cột DB nào cả (khác hẳn 6 cột migration 0007 đã thêm cho các field còn
lại), củng cố thêm quyết định không tự ý wiring persistance cho khối field này trong task này.

### Quyết định

1. **Không wiring 6 cột contract field đã có sẵn trong `createWorkItemTx`/`getWorkItemTx` (Nghiên cứu #1).**
   Dù về kỹ thuật đây là thay đổi additive, không cần migration mới, không phá vỡ caller nào hiện có — vẫn
   quyết định để nguyên: không citation nào trong `08-v6-api-projections.md` giao việc đó cho V6-04 (Phạm
   vi chỉ nói "readiness", không nói "contract persistence"), và sửa 2 hàm nền tảng đã merge ngay lúc 3
   session khác đang chạy song song trên cùng codebase là rủi ro không cần thiết cho một lợi ích ngoài
   phạm vi task. `internal/app/work.ExplainWorkItemReadiness` (mới) vẫn gọi `workdomain.ValidateReadinessGate`
   THẬT trên WorkItem THẬT load qua `GetWorkItem` — kết quả hiện tại sẽ luôn báo cùng một tập "problems" cho
   mọi WorkItem (đúng sự thật hệ thống ngày hôm nay, không phải giả lập), và sẽ tự động chính xác hơn ngay
   khi một task tương lai wiring contract field mà không cần sửa gì ở đây.
2. **Chọn subpackage riêng `internal/delivery/httpapi/workitem` thay vì file phẳng trong `httpapi`.** Đúng
   Contract chung §1.8 "Mỗi endpoint/CLI task sở hữu subpackage" — khác V6-01A/V6-02A vốn là task NỀN TẢNG
   (không phải "endpoint task"), và vì 3 session song song khác (V6-03A/V6-06/V6-10B) hoàn toàn có khả năng
   đang ghi trực tiếp vào `internal/delivery/httpapi` ngay lúc này. Bonus xác nhận từ Nghiên cứu:
   `TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt`'s `filepath.WalkDir` tự động đệ quy vào subpackage mới
   — archtest sẵn có của V6-02 tự động bao phủ toàn bộ code của task này mà không cần sửa file đó.
3. **URL tree `/projects/{projectId}/...` cho toàn bộ 12 route.** Suy trực tiếp từ chính câu ADR-025 tự
   nhắc "Route `/adapter-builds` do đó nằm ngoài cây `/projects/{id}`" — ngụ ý mọi resource project-scoped
   khác SỐNG dưới cây đó, không phải quy ước tự đặt.
4. **"list" (WorkItem) = toàn bộ WorkItem trong project (mọi Kind/Status), phẳng, không filter/sort/cursor.**
   Quyết định KHÔNG dùng V6-02A's cursor pagination cho danh sách này: không citation nào của V6-04 đòi
   filter/sort/cursor (khác hẳn V6-10 tự nêu rõ "stable filter/sort/cursor" cho projected Kanban), và mọi
   `List*` tương tự khác đã có trong repo (`ListProjects`, `ListFamilyRepositoryScopes`,
   `ListReleaseSetsForFamily`) đều phẳng, không cursor. "child" = trực tiếp children của một WorkItem
   (`parent_id = ?`), KHÔNG đệ quy xuống cháu — `NewChildWorkItem` không giới hạn Kind của parent nên về lý
   thuyết cháu là khả thi, nhưng không citation nào đòi truy vấn đệ quy, và test
   `TestListChildWorkItems_ReturnsOnlyDirectChildren` xác nhận cháu không lọt vào danh sách children của
   ông/bà.
5. **Thêm 2 method mới vào `ports.WorkRepository`: `ListWorkItemsByProject`, `ListChildWorkItems`** — mirror
   đúng cách V6-03 đã mở rộng `CatalogRepository.ListProjects` (thêm interface method + implement sqlite +
   implement fake), không phải cách tiếp cận mới.
6. **Query mới sống trong `internal/app/work/queries.go`** (không phải trong
   `internal/delivery/httpapi/workitem`), trả thẳng HTTP-ready DTO có json tag (`WorkItemDetail`,
   `TaskFamilyDetail`, `ScopeExpansionRequestDetail`, `WorkItemReadiness`) — mirror đúng convention "Result
   struct có json tag sống cạnh command sinh ra nó" mà `CreateRootWorkItemResult`/`CreateChildWorkItemResult`
   trong chính file `commands.go` đã thiết lập, áp dụng cho query thay vì command. `GetWorkItem` (tên hàm
   mới) cố ý trùng tên với `WorkRepository.GetWorkItem` (method port có sẵn) — mirror chính xác precedent
   `catalog.GetProject` (V6-03) trùng tên với `CatalogRepository.GetProject` đã có từ trước.
7. **Authorization "reload authoritative target trước, không tin ID shape" thực hiện bằng sentinel
   `ports.ErrScopeMismatch`** (đã có sẵn từ V6-03, KHÔNG phải `ErrPersistenceNotFound`) cho case "tồn tại
   thật nhưng thuộc project khác" — mirror đúng precedent `GetProject`'s "Cả installation scope lẫn scope
   của một Project KHÁC đều bị reject bằng cùng `ports.ErrScopeMismatch`". Tầng HTTP fold cả 2 sentinel
   (`ErrPersistenceNotFound` và `ErrScopeMismatch`) vào cùng một `WriteResourceHidden` response — leakage
   normalization đúng theo V6-02A, một ID đoán mò thuộc project khác và một ID không tồn tại hoàn toàn
   không thể phân biệt được từ response.
8. **Tự viết `writeCommandError`/`writeQueryError` riêng trong package `workitem`, không dùng
   `httpapi.WriteAppError` sẵn có.** Vì `internal/app/work` KHÔNG BAO GIỜ trả `*apperror.Error` (xác nhận
   qua grep Nghiên cứu ở trên) nên `WriteAppError` (chỉ hiểu `*apperror.Error`) sẽ luôn rơi vào nhánh 500
   generic cho MỌI lỗi từ package này — vô dụng. `writeCommandError` enumerate tường minh từng sentinel
   (đọc hết source, không đoán) map sang đúng status/code; lỗi không khớp bất kỳ sentinel nào rơi về 500
   INTERNAL — đây được coi là dead-code trong request hợp lệ vì mọi guard "field trống" đã được validate
   riêng ở tầng HTTP TRƯỚC khi build command envelope.
9. **Thứ tự "reload target trước Idempotency-Key/If-Match" áp dụng cho MỌI route mutation, kể cả CREATE.**
   Suy trực tiếp từ Contract chung §3 "Authorization chạy lại cả khi receipt replay" — nếu reload xảy ra
   SAU bước replay-lookup, một replay hợp lệ về mặt receipt vẫn có thể trả lại kết quả cũ cho một caller đã
   mất quyền truy cập target đó; do đó CreateRootWorkItem reload Project (`catalog.GetProject`),
   CreateChildWorkItem reload parent WorkItem, RequestScopeExpansion reload TaskFamily, và
   Approve/Reject/WithdrawScopeExpansion reload ScopeExpansionRequest — TẤT CẢ trước cả khi đọc
   Idempotency-Key.
10. **Approve/Reject/WithdrawScopeExpansion là update-shaped (bắt buộc If-Match theo V6-02) nhưng chính
    domain command của chúng KHÔNG nhận field `ExpectedVersion` nào** (đọc hết `scope_expansion.go` xác
    nhận: `ApproveScopeExpansionRequest{RequestID}`, không có version). Quyết định: tự thêm precondition
    check ở tầng HTTP, dùng LẠI giá trị `Version` đã reload ở bước #9 (không reload lần 2), thực hiện SAU
    bước `replayOrProceed` — đúng trích nguyên văn V6-02 "nếu absent mới kiểm current version". Domain
    command vẫn tự re-check PENDING-status độc lập bên trong transaction của chính nó — đây chính là cơ chế
    thật sự quyết định ai thắng khi 2 request đua nhau, không phải precondition check ở tầng HTTP (xem mục
    Test — phát hiện thật khi chạy race test).
11. **Omit ETag trên response thành công của Approve/Reject/Withdraw.** 3 Result struct đó không có field
    `Version`; muốn version mới, client GET lại detail (route đó CÓ ETag). Tránh 1 lần reload thừa chỉ để
    lấy ETag cho response của chính mutation.
12. **Dùng `catalog.GetProject` (đã merge từ V6-03) làm existence-check cho CreateRootWorkItem.** Đây là
    dependency hợp lệ đã khai trong task spec, không phải scope creep — gọi với
    `scope=ProjectScope(projectID)` và `projectID` giống hệt tham số, nên nhánh `ErrScopeMismatch` của
    `GetProject` về cấu trúc không bao giờ trigger qua đường gọi này; nhánh thực sự hữu ích là
    `ErrPersistenceNotFound` khi project không tồn tại.
13. **Request body DTO của mọi mutation KHÔNG có field `projectId`/`parentWorkItemId`/`familyId`/
    `requestId`/`RequestID`(cho RequestScopeExpansion).** Những định danh đó luôn đến từ path hoặc do
    application tự sinh (`work.RequestScopeExpansionRequest.RequestID`'s own doc comment: "A
    public/UI-facing caller must never be allowed to choose its own RequestID") — loại bỏ khả năng client
    tự khai ID/scope ngay từ tầng wire schema, không chỉ dựa vào việc handler "nhớ bỏ qua" giá trị đó.

### Thực hiện

- `internal/app/ports/work.go`: `WorkRepository` thêm `ListWorkItemsByProject(ctx, projectID)`,
  `ListChildWorkItems(ctx, parentWorkItemID)`.
- `internal/adapters/sqlite/work.go`: implement 2 method trên qua `listWorkItemsTx` dùng chung, tái sử dụng
  đúng `scanWorkItemRow`/cột SELECT của `getWorkItemTx` để một row list và một row get luôn cùng shape.
- `internal/app/ports/fake/work.go`: implement 2 method trên (linear scan + sort theo ID — fake không có
  `CreatedAt` để sort theo thời gian như sqlite thật).
- `internal/app/work/queries.go` (mới, 381 dòng): `WorkItemDetail`, `TaskFamilyDetail`,
  `RequestedGrantView`, `ScopeExpansionRequestDetail`, `WorkItemReadiness` (DTO có json tag) +
  `requireProjectScope`/`scopeMismatch` (helper dùng chung) + 6 hàm public: `GetWorkItem`, `ListWorkItems`,
  `ListChildWorkItems`, `GetTaskFamily`, `GetScopeExpansionRequest`, `ExplainWorkItemReadiness`.
- `internal/delivery/httpapi/workitem/` (package mới, 9 file production):
  - `dependencies.go`: `Dependencies{UnitOfWork, IDs, Clock}`.
  - `envelope.go`: `prepareCreateCommand`/`prepareUpdateCommand`/`replayOrProceed` — 3 helper dùng chung bởi
    cả 6 handler mutation, thực thi đúng flow V6-02 đã tài liệu hoá.
  - `errors.go`: `writeQueryError`/`writeCommandError`/`writeValidationError`/`writeReceiptHashConflict`/
    `writePreconditionFailed`.
  - `dto.go`: `scopeGrantBody` dùng chung + `validateScopeGrantBodies`.
  - `workitem_commands.go`: `handleCreateRootWorkItem`, `handleCreateChildWorkItem`.
  - `workitem_queries.go`: `handleGetWorkItem`, `handleListWorkItems`, `handleListChildWorkItems`,
    `handleGetWorkItemReadiness`, `handleGetTaskFamily`.
  - `scope_expansion_commands.go`: `handleRequestScopeExpansion`, `handleApproveScopeExpansion`,
    `handleRejectScopeExpansion`, `handleWithdrawScopeExpansion`, `loadScopeExpansionRequestForUpdate`.
  - `scope_expansion_queries.go`: `handleGetScopeExpansionRequest`.
  - `routes.go`: `RegisterRoutes` — 12 `RouteDescriptor`, toàn bộ `ScopeKind: httpapi.ScopeProject`.
- `internal/archtest/workitem_no_generic_setter_test.go` (mới): 2 test AST-scan mirror đúng idiom
  `command_envelope_test.go` — `TestWorkItemPackageRequestBodiesNeverAcceptAStatusField` (không type nào
  tên `...Body` có json field `status`/`targetStatus`/`state`/`family`/`workspace`),
  `TestWorkItemPackageNeverImportsProjection` (không import path nào chứa "projection"/"kanban").

### Test

- `internal/app/work/queries_sqlite_test.go` (mới, 315 dòng, real sqlite — không mock): 15 test — mỗi 1
  trong 6 query có ít nhất: matching-scope happy path, cross-project `ErrScopeMismatch`, và (khi áp dụng)
  installation-scope `ErrScopeMismatch`/unknown-ID `ErrPersistenceNotFound`.
  `TestListChildWorkItems_ReturnsOnlyDirectChildren` dựng cả cháu thật (child-của-child) để xác nhận không
  lọt vào danh sách children của gốc.
  `TestExplainWorkItemReadiness_FreshWorkItem_ReportsRealCompletenessGaps` đối chiếu kết quả với việc gọi
  trực tiếp `workdomain.ValidateReadinessGate` — nếu `ExplainWorkItemReadiness` từng bị sửa thành trả một
  danh sách problem đóng cứng, test này lộ ra ngay.
- `internal/delivery/httpapi/workitem/workitem_test.go` (mới, ~800 dòng, real `httpapi.Server` — TCP
  listener thật, middleware chain thật — cộng real sqlite, mirror đúng `server_test.go`'s `newTestServer`):
  - `TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet`: đối chiếu `reg.Descriptors()` với đúng 12
    operationId, mọi route `ScopeKind=PROJECT` — chứng minh cơ học "không route thứ 13 nào".
  - `TestFullJourney_...`: hành trình đầy đủ root create → detail → list → child create → list children →
    readiness → family detail → request expansion → detail → approve → xác nhận cả request lẫn family phản
    ánh quyết định.
  - Replay/conflict: `TestCreateRootWorkItem_SameIdempotencyKey_ReplaysWithoutCreatingSecondWorkItem`
    (replay luôn 200, không phải 201 lần 2 — đúng `WriteReceiptReplay`'s hardcoded status),
    `TestCreateRootWorkItem_SameKeyDifferentBody_ConflictsBeforeSecondInsert` (409 trước khi chạm I/O).
  - Leakage: `TestGetWorkItem_AnotherProject_ReturnsNotFound` — so sánh BYTE-FOR-BYTE response cross-project
    với response ID-không-tồn-tại (không dùng so sánh struct vì `httpapi.ErrorResponse` chứa slice, không
    comparable bằng `!=` — go vet tự bắt lỗi này khi thử, xem bên dưới).
  - `TestApproveScopeExpansion_StaleIfMatch_PreconditionFailed`: If-Match sai → 412.
  - `TestCreateChildWorkItem_EffectiveScopeExceedsFamilyScope_Returns400`: cả 2 nhánh — repo chưa từng được
    grant, và escalate READ→WRITE trên repo đã có — đều 400.
  - `TestRequestScopeExpansion_MissingReason_Returns400WithFieldDetail`: xác nhận `ErrorDetail.Field` chính
    xác.
  - `TestMutatingRoutes_RequireIdempotencyKey`.
  - **2 test race dùng real goroutine + real sqlite**:
    `TestConcurrentApproveAndReject_ExactlyOneDecisionWins` (Verify's "concurrent decisions") và
    `TestConcurrentWithdrawAndApprove_ExactlyOneWins` (Verify's "withdrawal race" — đúng trách nhiệm riêng
    của task này với `WithdrawScopeExpansion`).

**Phát hiện thật khi chạy test race lần đầu (không phải bug, nhưng là một hiểu lầm ban đầu trong chính test
của task này):** cả 2 test race ban đầu hardcode kỳ vọng loser luôn là `409`. Chạy thật ra `statuses=[412
200]` — một trong hai lần thua với `412 Precondition Failed` thay vì `409 Conflict`. Điều tra: nếu goroutine
thắng CHẠY XONG HOÀN TOÀN trước khi goroutine thua kịp tự `loadScopeExpansionRequestForUpdate` (bước reload
riêng của chính nó, Quyết định #9-10), goroutine thua sẽ tự thấy `Version=2` (đã bump) trong khi header
`If-Match` nó gửi vẫn hardcode `"1"` — precondition check TẦNG HTTP của chính nó tự bắt ra staleness và trả
412, domain command KHÔNG BAO GIỜ được dispatch trong nhánh này. Ngược lại, nếu 2 request phỏng đoán chạm
gần như đồng thời (cả 2 đọc Version=1 trước khi bên nào commit), cả 2 qua được precondition check của
chính mình, và chính domain command's fresh PENDING-status check (bên trong transaction serialize của
riêng nó) mới là nơi thực sự loại một bên — trả `409`. Cả 2 nhánh đều ĐÚNG, chỉ là 2 lớp phòng thủ khác
nhau bắt cùng một race tại 2 thời điểm khác nhau, phụ thuộc lịch Go scheduler — không thể ép cứng nhánh nào
sẽ xảy ra. Sửa assertion để chấp nhận tập `{409, 412}` cho loser (không đổi bất kỳ dòng implementation
nào), chạy lại `go test ./internal/delivery/httpapi/workitem/... -count=8` — 8 lần lặp, 96 lượt test, 100%
xanh, không flake.

`go vet` tự bắt thêm 1 lỗi thật khi viết test: so sánh `httpapi.ErrorResponse != httpapi.ErrorResponse`
bằng `!=` không compile được ("struct containing httpapi.ErrorBody cannot be compared") vì `ErrorBody`
chứa slice `Details`. Sửa bằng so sánh byte-for-byte trên response body thô (`io.ReadAll` rồi so
`string(...)`) — thực ra mạnh hơn so sánh struct (chứng minh cả field order/whitespace giống hệt, không chỉ
giá trị).

`go build ./...`, `go vet ./...` sạch trên toàn bộ repo. `go test ./...` (78 package, gồm cả
`internal/integration/v5accept` 116s, `internal/adapters/sqlite` 170s) — 100% xanh, không có regression ở
bất kỳ package nào khác dù đã sửa `ports.WorkRepository` (interface dùng bởi rất nhiều package khác qua
`ports.Tx`).

### Verify

- **"multi-repo/subset/scope negative matrix"**: `TestCreateChildWorkItem_EffectiveScopeExceedsFamilyScope_Returns400`
  (repo chưa grant + escalate READ→WRITE), `TestGetWorkItem_AnotherProject_ReturnsNotFound` (scope
  negative), `queries_sqlite_test.go`'s 6×cross-project-scope-mismatch test (mỗi query một cái).
- **"concurrent decisions"**: `TestConcurrentApproveAndReject_ExactlyOneDecisionWins`, real goroutine, real
  sqlite, lặp 8× không flake.
- **"withdrawal race"**: `TestConcurrentWithdrawAndApprove_ExactlyOneWins`, cùng discipline.
- **"no direct DONE"**: không route/DTO nào trong package `workitem` chấp nhận field status/state từ
  client — chứng minh cơ học bằng `internal/archtest/workitem_no_generic_setter_test.go`'s AST scan (không
  chỉ đọc code bằng mắt), cộng `TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet`'s đối chiếu tập
  đóng 12 operationId (không route thứ 13 ẩn nào có thể lọt qua).
- **"Hoàn thành khi: mọi WorkItem/scope control gọi named application command và client không set state"**:
  cả 6 route mutation dispatch đúng 1 trong 6 hàm public có sẵn của `internal/app/work`
  (`CreateRootWorkItem`/`CreateChildWorkItem`/`RequestScopeExpansion`/`ApproveScopeExpansion`/
  `RejectScopeExpansion`/`WithdrawScopeExpansion`) — không route nào tự CAS trực tiếp qua `tx.Work()`; xác
  nhận cơ học qua archtest cộng việc đọc lại toàn bộ handler.

### Kết quả

PR #43 (`feat/v6-04-workitem-family-readiness-scope`, branch từ `origin/master` tại `3af0adf`, không cần
merge `origin/master` giữa chừng vì không sibling nào merge trong lúc làm). 16 file mới/sửa, +2855 dòng: 2
method mới trên `ports.WorkRepository` (2 implementation: sqlite + fake), 1 file query application-layer
mới (`internal/app/work/queries.go`, 6 hàm public + 4 DTO), 1 package HTTP hoàn toàn mới
(`internal/delivery/httpapi/workitem`, 9 file production + 1 file test ~800 dòng, 12 route), 2 test
archtest mới. Test mới: 15 (query layer, real sqlite) + 13 (HTTP layer, real server + real sqlite, gồm 2
race test) + 2 (archtest) = 30 test function mới. `go build/vet/test ./...` xanh 100% trên toàn bộ 78
package, không regression. Gap "V6-04 chưa có route" đã đóng — V6-04A (`MarkWorkItemReady`, phụ thuộc
V6-04) và V6-12 (compose root router, phụ thuộc trong đó có V6-04) giờ có đủ điều kiện bắt đầu ngay khi
dependency còn lại của mỗi task sẵn sàng.

**Sửa bởi phiên giám sát khi resolve merge conflict với master**: `workitem.RegisterRoutes` được viết đúng,
test đầy đủ, nhưng KHÔNG được gọi từ `cmd/aw/serve.go` — khác với V6-03A/V6-06/V6-10B, PR #43's diff không hề
chạm file này, nghĩa là 12 route mới hoàn toàn không reachable qua `aw serve` thật dù mọi test tại package
level đều xanh (test tự dựng `httpapi.NewServer` riêng, không đi qua composition root thật). Đây đúng loại
lỗi "test xanh nhưng chưa thật sự nối dây" mà doctrine của phiên này luôn cảnh giác. Đã thêm import
`internal/delivery/httpapi/workitem` + `internal/app/clock` và một dòng `workitem.RegisterRoutes(routes,
workitem.Dependencies{UnitOfWork: uow, IDs: idsource.Random{}, Clock: clock.System{}})` vào `cmd/aw/serve.go`,
ngay cạnh dòng `httpcatalog.RegisterRoutes` đã có — build/vet/test lại sạch sau khi thêm.

## V6-06 — Run start và cancellation controls

### Bối cảnh

V6-06 nằm trong nhóm P2 ("application/HTTP slices sau V6-00 + V6-01A + V6-02 + V6-02A"), unblocked ngay khi
cả 4 dependency đó đã merge — xác nhận bằng `git log --oneline -5 origin/master` lúc bắt đầu: `60a9f8f
feat(v6-02): HTTP CommandEnvelope...` đã có sẵn, worktree branch ra đúng từ `3af0adf` (`git merge-base HEAD
origin/master` = `git rev-parse origin/master`, xác nhận không cần rebase trước khi bắt đầu). Theo đúng cảnh
báo trong system prompt, `baocaov6checklist.md` đang được 3 task khác ghi song song ngay lúc này (V6-03A,
V6-04, V6-10B) — append-only, dự kiến conflict khi merge.

Đây là task ĐẦU TIÊN thật sự mount một route nghiệp vụ vào `cmd/aw/serve.go`. Đọc file này trước khi viết bất
cứ gì thì thấy nó đã tồn tại sẵn từ V6-01/V6-01A (không phải file tôi tạo mới) với đúng 3 route health/
bootstrap, và một comment để sẵn chỗ nối (dòng 147-149 bản gốc): *"No further route fragments exist yet in
this task; a later endpoint task's own composition-root wiring adds its own routes.Register call here without
needing to touch this file's shared setup."* — xác nhận đúng convention: mỗi endpoint task tự thêm một lời gọi
`routes.Register(...)` (hoặc ở đây, một hàm `RegisterRoutes` gói nhiều route) ngay tại điểm này, KHÔNG có task
riêng nào "compose root router" trước V6-12 — điều mà lúc đầu đọc contract "Chỉ V6-12 compose HTTP router" dễ
hiểu nhầm là "không được đụng `cmd/aw/serve.go`". Đọc trực tiếp code mới xác nhận đúng nghĩa "compose" ở V6-12
là gì (đóng băng/generate OpenAPI + reverse-check toàn bộ fragment), không phải "chỉ V6-12 được thêm route
handler thật vào server".

### Nghiên cứu

Đọc nguyên văn spec (`docs/design/08-v6-api-projections.md` dòng 240-249): *"Thực hiện: start pins manifest/
version; cancel trả `CANCELLING`, không giả `CANCELLED`; handler chỉ dispatch."* / *"Verify: replay/pinned
conflict/cancel twice/cancel race; accepted response không terminal tức thì."*

Đọc kỹ 2 authority thật trước khi viết bất kỳ dòng handler nào — đúng yêu cầu review của chính task:

- `internal/app/runtime/commands.go:116` — `func StartWorkflowRun(ctx, uow, ids, cmd ports.Command, req
  StartWorkflowRunRequest) (StartWorkflowRunResult, error)`. Tự nó check receipt (`tx.Receipts().Load`) ngay
  đầu transaction, đúng khuôn V1-06/V6-02 (giống hệt `catalog.CreateProject`).
- `internal/app/runtime/cancel_run.go:113` — `func CancelRun(ctx, uow, ids, req CancelRunRequest)
  (CancelRunResult, error)`. KHÔNG nhận `ports.Command`. `CancelRunRequest{RunID, Actor, Reason,
  CorrelationID}` — không có `IdempotencyKey`, không có `ExpectedVersion`. Đây chính là điểm khác biệt task
  yêu cầu phải hiểu rõ trước khi "map sang HTTP", không được lấp liếm.

Đọc ADR-020 §22 (`docs/architecture/02-architecture-decisions.md` dòng 354, mục "Cancellation protocol", điểm
1): *"`CancelRun` atomically ghi durable cancel intent, chuyển Run sang `CANCELLING`, append event và enqueue
job; **command là idempotent theo run**."* — xác nhận đây là thiết kế có chủ đích của ADR-020 chứ không phải
thiếu sót của V4-12B/V4-12C: idempotency của Cancel gắn với chính `RunID` (unique `run_cancellation_intents`),
không gắn với một `Idempotency-Key` do client tự sinh. `cancel_run.go`'s own package doc comment (dòng 1-25) nói
rõ hơn: "(1) records the one durable RunCancellationIntent a Run ever has (idempotent by RunID — a duplicate
call is a harmless no-op, never a second intent)".

Đọc ADR-022 (`docs/architecture/02-architecture-decisions.md` dòng 522, "AdapterBuildVersion là operational
registry, không phải DefinitionKind") — ban đầu đoán nhầm đây là ADR về optimistic concurrency (vì V6-02 hay
cite ADR-025/028 cho receipt), đọc hết mới thấy ADR-022 thực ra nói về nguyên tắc "pin đúng version bất biến,
không tự động repin" cho `AdapterBuildVersion` (điểm 5, dòng 534: *"Run đang chạy không bao giờ được tự động
repin sang build mới."*) — đây chính là nguyên tắc tổng quát mà `StartWorkflowRun`'s "pins manifest/version"
áp dụng cho `WorkflowVersionID`/`ExecutionManifest` (khác đối tượng — AdapterBuildVersion vs WorkflowVersion —
nhưng cùng một nguyên tắc "pin exact, never silent drift"), giải thích tại sao V6-06 cite đúng ADR này thay vì
một ADR về idempotency-key.

Đọc `docs/design/11-v6-00-ux-artifact.md` (V6-00's action inventory, đã merge trước đó) — tìm thấy operationId
đã khoá cứng, không được tự đặt tên khác: `startWorkflowRun` (dòng 246, Screen 5 hàng 4; dòng 303, Screen 7
hàng 3) và `cancelRun` (dòng 247, 304). Dòng 304 xác nhận chính xác ngôn ngữ HTTP-facing: *"`CancelRun` ...
trả `CANCELLING`, hiển thị như tiến trình thật, không báo đã hủy xong ngay"* — khớp 100% với "Thực hiện" line
của chính V6-06, và là bằng chứng rằng ngôn ngữ "quiesce, không giả CANCELLED" đã được sản phẩm hoá từ trước ở
tầng UX, không phải diễn giải riêng của task này.

Đọc toàn bộ hạ tầng `internal/delivery/httpapi` đã có trước khi viết: `route.go` (RouteDescriptor/
RouteRegistry, dedupe theo cả `(Method,Path)` lẫn `OperationID`), `commandenvelope.go` (`RequireIdempotencyKey`,
`CanonicalizeJSON`, `SemanticHash`), `receiptreplay.go` (`LookupReceipt`/`ReconcileReceipt`/`WriteReceiptReplay`/
`EncodeResult`), `errors.go` (`WriteError`/`WriteResourceHidden`/`StatusForAppErrorCode`/`WriteAppError`),
`principal.go` (`PrincipalFromContext`), `middleware.go` (`CorrelationIDFromContext`), `server.go` (cách
`NewServer` compose `mux.HandleFunc(d.Method+" "+d.Path, d.Handler)` — xác nhận Go 1.22+ pattern `{name}` +
`r.PathValue` dùng được, kiểm `go.mod`: `go 1.27.0`).

Đọc `internal/archtest/boundary_test.go` (style AST-scan + `findModuleRoot` dùng chung cho cả package) và
`command_envelope_test.go` (`TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt`) — xác nhận test này tự động
`filepath.WalkDir` xuống MỌI package con của `internal/delivery/httpapi`, nghĩa là package `run` mới của task
này tự động bị kiểm tra "không gọi `.Record(`/`.WithSerializedWrite(`" mà không cần sửa gì thêm.

Đọc toàn bộ `internal/app/runtime/commands_test.go`, `commands_sqlite_test.go`, `cancel_run_test.go`,
`cancel_run_race_test.go` trước khi viết fixture test của riêng mình — xác nhận `readyFixtureSQLite`/
`publishTestWorkflowVersionSQLite`/`stubProvider`/`provisionJob` (trong `commands_sqlite_test.go`) là đúng
fixture chuẩn cần mirror (không import được vì Go test helper không export chéo package — đúng discipline
`mustCreateActiveRepository`'s own doc comment đã ghi).

### Quyết định

1. **Start theo đầy đủ flow CommandEnvelope của V6-02; Cancel thì KHÔNG** — quyết định quan trọng nhất, dựa
   thẳng trên phát hiện ở phần Nghiên cứu (ADR-020 "idempotent theo run"), không phải suy đoán. `start.go`
   dùng đủ `RequireIdempotencyKey` → `CanonicalizeJSON` → `SemanticHash` → `LookupReceipt` →
   replay/conflict/dispatch, y hệt `receiptreplay_test.go`'s `buildCreateProjectCommand`. `cancel.go` KHÔNG
   đòi `Idempotency-Key`, KHÔNG đòi `If-Match`: `CancelRunRequest` (runtime) không có field nào để nhét 2 thứ
   đó vào, và bắt buộc một header rồi validate-nhưng-không-bao-giờ-dùng (`httpapi.LookupReceipt` sẽ luôn trả
   "not found" vì `CancelRun` không bao giờ ghi receipt) là nói dối về contract thật — an toàn trước duplicate/
   race đến từ chính `cancelRunTx`'s "idempotent theo run", không phải từ tầng HTTP. Ghi toàn bộ lý luận này
   thành doc comment dài trong chính `cancel.go` (không chỉ trong file báo cáo này) để người đọc code sau
   không tưởng đây là thiếu sót rồi "sửa" cho giống Start.
2. **`WorkItemID` (path) PHẢI nằm trong semantic-hash payload của Start (`startRunHashPayload`), không chỉ
   `workflowVersionId` (body).** Lý do: `SemanticHash` hash theo `(commandType, scope, payload, ...)` —
   `scope` chỉ là cả PROJECT, không phải một WorkItem cụ thể. Nếu chỉ hash `{workflowVersionId}`, hai
   `WorkItemID` khác nhau trong cùng project, dùng trùng `Idempotency-Key` (bug client) và cùng
   `workflowVersionId`, sẽ hash giống hệt nhau → `ReconcileReceipt` coi là replay hợp lệ → trả nhầm `RunID`
   của WorkItem A cho request thật sự nhắm vào WorkItem B. Viết test
   `TestStartWorkflowRun_HTTP_SameIdempotencyKeyDifferentWorkItem_ConflictsRatherThanCrossReplays` để chứng
   minh — xem phần Test bên dưới, chính test này lộ ra MỘT SAI LẦM THẬT trong kỳ vọng ban đầu của tôi.
3. **Route path**: `POST /work-items/{workItemId}/runs` (start) và `POST /runs/{runId}/cancel` (cancel).
   Mirror 2 tiền lệ path đã có trong chính design doc: V6-04A's "Phạm vi" line ghi rõ `POST
   /work-items/{id}/mark-ready` (resource + action-suffix cho narrow command); V6-06B's "Thực hiện" line ghi
   `GET /runs/{id}` (Run là resource cấp cao nhất). Start tạo Run mới từ một WorkItem đã tồn tại → nested
   dưới `/work-items/{id}/runs` (REST "tạo sub-resource", đồng thời WorkItemID lấy thẳng từ path, không cần
   field trùng lặp trong body). Cancel tác động lên một Run đã tồn tại → `/runs/{id}/cancel`, cùng họ với
   `/mark-ready`.
4. **`ProjectID` luôn tự reload phía server (`loadWorkItemProjectID`/`requireRunExists`), KHÔNG BAO GIỜ lấy
   từ client** — đúng "Contract chung đã khóa" điểm 3: *"Mọi item route reload authoritative target để suy
   Project/scope và authorize; không tin ID shape, payload hoặc projection."* `StartRunRequest`/
   `CancelRunRequest` (2 struct HTTP body của package này) vì vậy KHÔNG có field `projectId` nào cả — loại
   bỏ hẳn khả năng client tự khai sai project. Đọc kỹ `commands.go` xác nhận bản thân `StartWorkflowRun`
   TIN `req.ProjectID` tuyệt đối (không tự cross-check với WorkItem) — nghĩa là chính handler HTTP này là nơi
   giữ đúng lời hứa đó, không phải app layer.
5. **Không `If-Match` cho cả hai route.** `StartWorkflowRunRequest` không có `ExpectedVersion`
   (`StartWorkflowRun` tự load `item.Version` bên trong transaction rồi CAS bằng chính giá trị vừa load, ứng
   viên thua race nhận `ErrWorkItemNotReady` chứ không phải conflict-vì-version-cũ). `CancelRunRequest` cũng
   không có field version nào. Không có gì để client "match" trước.
6. **`ValidActions` advisory có mặt trong cả 2 response, đúng "Phạm vi: ... mutation valid actions" của
   chính V6-06** — Start trả `[{operationId: "cancelRun", scopeKind: PROJECT, targetVersion: 0}]` (một Run
   vừa RUNNING luôn có thể cancel); Cancel trả mảng rỗng (không còn "hành động mutation mới" nào trong tập
   đóng của task này một khi đã CANCELLING/CANCELLED — gọi cancel lần nữa vốn đã an toàn, không cần quảng
   cáo). `TargetVersion: 0` là cố ý, không phải bug: `httpapi.ValidAction.TargetVersion` được thiết kế cho
   action có `If-Match`/`ExpectedVersion` — `cancelRun` không có field đó (quyết định 5), nên 0 (zero value)
   là giá trị trung thực, ghi rõ lý do trong chính doc comment của `StartRunResponse`.
7. **`CancelRunResponse` tự định nghĩa lại field JSON, không serialize thẳng `runtime.CancelRunResult`.**
   `runtime.CancelRunResult` (cancel_run.go dòng 66-73) không có json tag nào — serialize thẳng sẽ ra
   `{"RunID":...,"AlreadyRequested":...}` (PascalCase), lệch hẳn convention camelCase của toàn bộ package
   (`StartWorkflowRunResult` đã có tag `runId`/`projectId`/... sẵn nên dùng thẳng được, embed qua
   `StartRunResponse`). Cân nhắc sửa thẳng `cancel_run.go` thêm json tag — quyết định KHÔNG làm, vì đó là
   file dùng chung, có nguy cơ conflict với các PR song song khác đang chạy, và việc map tường minh ở tầng
   delivery (đúng vai trò của nó) rủi ro thấp hơn nhiều so với sửa file application layer đã merge/test kỹ.
8. **Phân loại lỗi (`errors.go`) tự viết riêng trong package `run`, KHÔNG sửa `internal/app/runtime` để bọc
   `apperror.Error`.** Mọi sentinel (`ErrWorkItemNotReady`, `ErrWorkspaceNotReady`,
   `ErrWorkflowVersionMismatch`, `ErrWorkItemCancellationPending`, `ErrRunAlreadyTerminal`, ...) đều là
   `errors.New` trần — `httpapi.WriteAppError`'s `errors.As(err, &appErr)` sẽ không bao giờ khớp, rơi vào
   nhánh mặc định 500 INTERNAL nếu không tự phân loại. Lẽ ra có thể bọc các sentinel này bằng `apperror.Wrap`
   ngay trong `runtime` để dùng chung bảng `StatusForAppErrorCode`, nhưng đó là sửa file lõi đã test rất kỹ,
   rủi ro cao hơn lợi ích cho một task chỉ có phạm vi "HTTP delivery" — giữ nguyên `internal/app/runtime`,
   viết 2 hàm `writeStartWorkflowRunError`/`writeCancelRunError` cục bộ, message diễn giải lại an toàn (không
   trả thẳng `err.Error()`, đúng tinh thần "safe, public-facing half" của `apperror.Error.Message`).
9. **Status code**: 201 cho Start mới (tạo Run), 200 cho Start replay (theo đúng `WriteReceiptReplay`'s hard-
   coded 200 có sẵn — không tự đặt lại), 202 luôn cho Cancel (cả fresh lẫn `AlreadyRequested=true`) — đúng
   "accepted response không terminal tức thì" của chính Verify line, phân biệt qua field `alreadyRequested`
   thay vì qua status code.
10. **Architecture test riêng cho "không có scheduler/worker fast path"** — chọn cấm gọi selector tên
    `Handle` (tên method dùng nhất quán cho MỌI job consumer trong codebase này —
    `ExecuteNodeHandler.Handle`, `CancelRunCoordinatorHandler.Handle`, `workspaceprovision.Handler.Handle`,
    xác nhận bằng grep 37 file khớp `\.Handle\(` toàn bộ đều là test application-layer, KHÔNG file nào trong
    `internal/delivery/httpapi` từng gọi selector này) cộng `AdvanceRun`/`FinalizeExecutionAttempt` (2 hàm
    scheduler cấp cao). Mirror y hệt `TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt`'s kiểu AST-scan, tái
    dùng `findModuleRoot` có sẵn trong `boundary_test.go` (cùng package `archtest`).
11. **Test HTTP thật qua `httptest.Server` + `http.ServeMux` thật (`newTestServer`), không chỉ gọi hàm
    handler trực tiếp qua `httptest.NewRecorder`.** Lý do: tự tay viết pattern `{workItemId}`/`{runId}` cho
    `RouteDescriptor.Path` — muốn chứng minh chính `net/http.ServeMux` thật khớp đúng pattern đó và
    `r.PathValue(...)` trả đúng giá trị, không chỉ tin "chắc nó đúng". Chỉ ghép thêm `httpapi.BindPrincipal`
    (không ghép `HostOriginGuard`/`RequireSessionToken`/`Recover`/`MaxBytes` — đó là phạm vi đã test kỹ của
    V6-01/V6-01A, ghép vào sẽ chỉ thêm boilerplate token/Host không liên quan tới logic route của task này).
12. **Không thêm role-based authorization gate nào ngoài `LocalPrincipalSnapshot` đã bind sẵn.** Spec V6-06
    không nhắc "enforce role" (khác hẳn V6-06A's "enforce role" rõ ràng cho approval) — tự bịa một role gate
    không có căn cứ trong doc sẽ là scope creep. `requireRunExists`/`loadWorkItemProjectID` vẫn tồn tại và có
    giá trị thật: chứng minh Run/WorkItem tồn tại thật trước khi dispatch (fail-closed 404 nhất quán), để sẵn
    móc nối cho V6-13's "cross-project guessed Run/WorkItem" test sau này.
13. **Không thêm gì cho graph/timeline/diagnostics/approval/WAIT** — đúng "Không làm" line, đây là việc của
    V6-06B/V6-06C/V6-06A/V6-06D, không lấn sang.

### Thực hiện

- `internal/delivery/httpapi/run/run.go`: `Dependencies{UOW, IDs}`, `RegisterRoutes(routes, deps)` — đăng ký
  đúng 2 route với `OperationID` khoá cứng từ V6-00 (`startWorkflowRun`, `cancelRun`).
- `internal/delivery/httpapi/run/start.go`: `StartRunRequest`, `startRunHashPayload` (nội bộ),
  `StartRunResponse`, `StartWorkflowRunHandler(deps) http.HandlerFunc`, `loadWorkItemProjectID`.
- `internal/delivery/httpapi/run/cancel.go`: `CancelRunRequest`, `CancelRunResponse`,
  `CancelRunHandler(deps) http.HandlerFunc`, `requireRunExists` — doc comment giải thích đầy đủ quyết định 1
  ở trên ngay trong code, không chỉ ở báo cáo này.
- `internal/delivery/httpapi/run/errors.go`: `writeStartWorkflowRunError`, `writeCancelRunError` — phân loại
  từng sentinel thật của `internal/app/runtime` sang đúng status/code, message an toàn tự viết lại.
- `internal/archtest/run_control_test.go`: `TestRunControlHTTPNeverReachesSchedulerOrWorker`.
- `cmd/aw/serve.go`: thêm 1 import (`runhttp ".../httpapi/run"`) + 1 lời gọi `runhttp.RegisterRoutes(routes,
  runhttp.Dependencies{UOW: uow, IDs: idsource.Random{}})` đúng tại điểm đã đánh dấu sẵn — không sửa gì khác
  trong file (diff 10 dòng, xem `git diff origin/master -- cmd/aw/serve.go`).
- Không migration mới (task này không cần schema mới — kiểm tra highest migration hiện tại vẫn `0037` trên
  `origin/master`, không đụng tới).

### Test

Toàn bộ test dùng sqlite thật (`sqlite.Open` + `sqlite.NewUnitOfWork`), không mock/fake nào, và dispatch qua
đúng `internal/app/runtime.StartWorkflowRun`/`CancelRun` thật — không có chỗ nào tự ghi thẳng DB để giả kết
quả.

- `internal/delivery/httpapi/run/fixture_test.go`: mirror `commands_sqlite_test.go` — `seedProject`/
  `seedActiveRepository` (tách riêng, xem bug #1 dưới đây), `stubProvider`, `readyWorkItemFixture`/
  `readyWorkItemFixtureInExistingProject`, `publishTestWorkflowVersion`.
- `httptest_helper_test.go`: `newTestServer` — `http.ServeMux` thật ghép `RegisterRoutes`'s descriptor +
  `httpapi.BindPrincipal`, bọc trong `httptest.Server` thật.
- `start_test.go` (9 test): `..._Success_ReturnsCreatedRunningWithCancelValidAction`,
  `..._MissingIdempotencyKey_ReturnsBadRequest`, `..._MissingWorkflowVersionId_ReturnsBadRequest`,
  `..._UnknownWorkItem_ReturnsResourceHidden`, `..._Replay_SameKeySameBody_ReturnsIdenticalResult` (**replay**),
  `..._SameKeyDifferentBody_ReturnsConflict`, `..._PinnedWorkflowVersionMismatch_ReturnsConflict` (**pinned
  conflict** — mirror trực tiếp `commands_sqlite_test.go`'s
  `TestStartWorkflowRun_SQLite_WorkflowVersionMismatch_RejectsPinnedWorkItem` qua HTTP),
  `..._SameIdempotencyKeyDifferentWorkItem_ConflictsRatherThanCrossReplays`,
  `..._ConcurrentSameIdempotencyKey_OnlyOneRunCreated` (goroutine thật, real sqlite).
- `cancel_test.go` (6 test): `..._Success_ReturnsAcceptedCancelling`,
  `..._CancelTwice_SecondIsAlreadyRequestedNoSecondJob` (**cancel twice**),
  `..._UnknownRun_ReturnsResourceHidden`, `..._MissingReason_ReturnsBadRequest`,
  `..._AlreadyTerminalRun_ReturnsConflict`,
  `..._ConcurrentCancelRace_OnlyOneFreshRequestOneCoordinatorJob` (**cancel race** — goroutine thật, real
  sqlite, mirror đúng phương pháp `internal/app/runtime/cancel_run_race_test.go` nhưng chạy qua HTTP handler
  thật, không gọi thẳng `runtime.CancelRun`).

**2 lỗi thật phát hiện trong lúc viết/chạy test (không phải giả định trước, mà lộ ra khi `go test` fail
thật):**

1. **Bug trong chính fixture của tôi**: `TestStartWorkflowRun_HTTP_SameIdempotencyKeyDifferentWorkItem_...`
   ban đầu gọi `readyWorkItemFixture(t, uow, ids, "project-1", "repo-a")` rồi
   `readyWorkItemFixture(t, uow, ids, "project-1", "repo-b")` — cả hai đều tự `CreateProject("project-1")` →
   lần 2 vi phạm UNIQUE constraint thật, test fail với `"seed project project-1: INTERNAL: sqlite: unexpected
   error"`. Sửa bằng cách tách `seedProject`/`seedActiveRepository` thành 2 hàm riêng (mirror đúng
   `commands_test.go`'s `mustCreateProject`/`mustCreateActiveRepository` gốc mà lúc đầu tôi lỡ gộp lại), thêm
   `readyWorkItemFixtureInExistingProject` cho trường hợp 2 WorkItem cùng project.
2. **Kỳ vọng sai trong chính test tôi viết** (không phải bug trong code sản phẩm): lúc đầu tôi viết test kỳ
   vọng CẢ HAI request (WorkItem A và B, cùng `Idempotency-Key`, cùng `workflowVersionId`) đều phải trả 201
   với `RunID` khác nhau — chạy thật ra 409 CONFLICT cho request B. Suy nghĩ lại: đây MỚI ĐÚNG là hành vi cần
   có — một `Idempotency-Key` là lời hứa "khoá này đại diện đúng MỘT request logic"; client dùng lại khoá đó
   cho một WorkItem hoàn toàn khác là VI PHẠM lời hứa đó, và hệ thống phải xử lý y hệt case "same key khác
   body" (đã có test riêng) — tức là CONFLICT, không phải âm thầm cho qua như hai request độc lập. Viết lại
   test đúng tên `..._ConflictsRatherThanCrossReplays`, khẳng định 3 điều: (a) request B bị 409, (b) chỉ có
   đúng 1 `workflow_runs` row sau đó (không tạo nhầm, không tạo sai), (c) dùng một khoá RIÊNG cho WorkItem B
   thì thành công bình thường với `RunID` riêng — chứng minh 409 ở bước (a) đúng là do trùng khoá, không phải
   do lỗi nào khác chặn WorkItem B.

- `go build ./...`, `go vet ./...` sạch trên toàn bộ 82 package.
- `go test ./internal/delivery/httpapi/run/... -v`: 15/15 PASS (5.66s).
- `go test ./internal/archtest/... -v`: toàn bộ pass, gồm `TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt`
  (tự động phủ cả package `run` mới, không cần sửa gì) và `TestRunControlHTTPNeverReachesSchedulerOrWorker`
  mới (0.03s).
- `go test ./cmd/aw/... ./internal/delivery/httpapi/...`: pass (xác nhận wiring `serve.go` không phá gì).
- Race detector (`-race`) không chạy được trên máy dev (Windows, `CGO_ENABLED=0` — pure-Go sqlite theo đúng
  chủ trương repo, `-race` cần cgo) — để CI (Linux, có cgo) tự chạy race suite thật, đúng hard rule #7 (không
  tự claim theo dõi CI).
- Chạy thêm `go test ./...` toàn bộ 82 package một lần để soát regression ngoài phạm vi trực tiếp: 1 fail duy
  nhất — `TestV5AcceptFullComposition_RealFourRoleGraphReachesSucceededAndSurvivesRestart`
  (`internal/integration/v5accept`), lỗi *"run v5a-7 did not reach state VERIFYING within the deadline"*.
  KHÔNG vội coi đây là "known flake" — đúng hard rule của chính task này, làm 2 bước xác minh thật trước khi
  kết luận: (1) `git diff origin/master --stat -- internal/app/runtime internal/integration
  internal/adapters/sqlite internal/app/work internal/app/workspaceprovision internal/domain` → rỗng, xác
  nhận nhánh này không đụng một dòng nào trong toàn bộ chuỗi phụ thuộc của package đang fail; (2) chạy lại
  `go test ./internal/integration/v5accept/... -v` một mình, không contention — cả 12 test pass sạch
  (106.5s), gồm đúng test vừa fail (15.69s, so với 29.61s+timeout lúc chạy chung). Kết luận: đúng cùng một
  triệu chứng CPU/IO contention từ `go test ./...` chạy nhiều package song song mà chính `## V6-03`'s own
  narrative ở trên đã từng gặp và xác minh y hệt cách này (test khác tên, cùng package, cùng triệu chứng
  "did not reach state VERIFYING within the deadline") — không phải regression thật từ thay đổi của V6-06.

### Verify

- **replay**: `..._Replay_SameKeySameBody_ReturnsIdenticalResult` — cùng key/body trả đúng `RunID` gốc qua
  200 (`WriteReceiptReplay`), `CountWorkflowRuns` xác nhận đúng 1 row.
- **pinned conflict**: `..._PinnedWorkflowVersionMismatch_ReturnsConflict` — WorkItem đã pin version A, gọi
  start với version B → 409 CONFLICT, dùng đúng `store.SetWorkItemWorkflowVersionForTest` để đạt precondition
  (không command thật nào pin được field này, đúng như chính doc comment gốc của
  `TestStartWorkflowRun_SQLite_WorkflowVersionMismatch_RejectsPinnedWorkItem` đã giải thích).
- **cancel twice**: `..._CancelTwice_SecondIsAlreadyRequestedNoSecondJob` — lần 2 vẫn 202,
  `alreadyRequested=true`, đúng 1 `CANCEL_RUN_COORDINATOR` job (`CountDurableJobsByIdempotencyKey` với key
  `"cancel-run-coordinator:"+runID`, đúng key thật `cancel_run.go` dùng).
- **cancel race**: `..._ConcurrentCancelRace_OnlyOneFreshRequestOneCoordinatorJob` — 6 goroutine thật gọi
  đồng thời qua `httptest.Server` + `http.Client` thật vào cùng 1 Run, đúng 1 response "fresh"
  (`alreadyRequested=false`), đúng 1 coordinator job — race thật trên sqlite thật, không phải suy luận từ
  code application layer đã có sẵn test riêng.
- **accepted response không terminal tức thì**: Cancel LUÔN trả `state` đúng những gì `runtime.CancelRun`
  quan sát được (CANCELLING lúc mới, hoặc state hiện tại khi đã `alreadyRequested`) — không handler nào tự ý
  gán `CANCELLED`; `TestCancelRun_HTTP_Success_ReturnsAcceptedCancelling` assert rõ `State != "CANCELLED"`
  (phải là `"CANCELLING"`).
- **"Hoàn thành khi": Run control không có scheduler/worker fast path** —
  `TestRunControlHTTPNeverReachesSchedulerOrWorker` (AST-scan thật, không phải đọc mắt) chứng minh
  `internal/delivery/httpapi/run` không gọi `.Handle(`/`AdvanceRun`/`FinalizeExecutionAttempt` ở đâu cả;
  `TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt` (đã có từ V6-02, tự động phủ package mới) chứng minh
  không ghi receipt trực tiếp.

### Kết quả

10 file thay đổi (`cmd/aw/serve.go` sửa 10 dòng; 9 file mới trong `internal/delivery/httpapi/run/` +
`internal/archtest/run_control_test.go`), tổng +1615/-3 dòng. Package `run` mới: 4 file production
(`run.go`/`start.go`/`cancel.go`/`errors.go`, ~515 dòng) + 5 file test (~1210 dòng, 15 test case). 1 test mới
trong `internal/archtest`. `go build/vet ./...` sạch trên 82 package; toàn bộ test của package mới + archtest
+ `cmd/aw` + `internal/delivery/httpapi` (cha) pass 100%. Không migration mới, không sửa
`internal/app/runtime`/`internal/app/ports` — toàn bộ thay đổi nằm gọn trong tầng delivery + 1 điểm nối
additive vào composition root, đúng phạm vi "HTTP delivery only" của task.

`POST /work-items/{workItemId}/runs` (operationId `startWorkflowRun`) và `POST /runs/{runId}/cancel`
(operationId `cancelRun`) giờ là 2 route thật, chạy được qua `aw serve` thật (không chỉ unit test cô lập) —
`V6-06 -> V6-06B` (Run detail/graph/timeline) chính thức unblock phần dependency của riêng nó (còn cần thêm
`V6-00`, `V6-02A`, đều đã xong từ trước). `V6-06D` KHÔNG phụ thuộc `V6-06` (dependency riêng: `V6-00, V6-01A,
V6-02, V6-02A, V4-12C, V5-08D`) nên không bị ảnh hưởng bởi thứ tự merge của task này.
## V6-10B — Workspace state, lease và reconcile endpoints

### Bối cảnh

V6-10B là 1 trong 3 task P2 chạy song song ngay sau V6-02 merge (cùng `{V6-03A, V6-04, V6-06}`, theo đúng
worktree riêng của phiên này — 3 session khác chạm `baocaov6checklist.md`/có thể chạm `cmd/aw/serve.go` đồng
thời, xem phần Kết quả về merge). Trích nguyên văn task spec từ `docs/design/08-v6-api-projections.md`
(dòng 434-443): "Mục tiêu: expose WorkspaceSet/repository-workspace state, lease/fence/quarantine và request
actions"; "Phạm vi: state queries, `RequestWorkspaceSetRelease`, `RequestWorkspaceReconciliation` routes";
"Không làm: no internal Execute command, Git/filesystem direct call hoặc writer grant từ HTTP"; "Hoàn thành
khi: recovery request possible without Git/DB surgery và delivery has no executor authority." Phụ thuộc
`{V6-00, V6-01A, V6-02, V6-02A, V6-03}` — cả 5 đã merge (`3af0adf` là commit mới nhất trên `origin/master` khi
bắt đầu), không có gì phải chờ thêm.

Design doc's own dòng 419-421 xác nhận rõ shape song song: "V6-10A…V6-10J are independent contract suites
after their own prerequisites... `{V6-10B, V6-10H, V6-10J}` can run in parallel; after B, `{V6-10D, V6-10F}`
can run in parallel because C/E authorities are already complete" — nghĩa là V6-10D (map V6-10C's 3 query
sang HTTP GET) và một phần khác của roadmap đang chờ đúng package `internal/delivery/httpapi` này ổn định
trước khi bắt đầu.

### Nghiên cứu

Đọc toàn bộ hạ tầng V6-01/V6-01A/V6-02/V6-02A trước khi viết dòng nào: `route.go` (`RouteRegistry.Register`
panic-on-duplicate, `ScopeKind` đóng INSTALLATION|PROJECT theo ADR-025), `server.go`/`bootstrap.go`
(`NewServer` compose middleware chain thật: `HostOriginGuard → CorrelationID → Recover → RequireSessionToken
→ MaxBytes → BindPrincipal`), `commandenvelope.go`/`receiptreplay.go` (V6-02's own flow: authenticate →
`RequireIdempotencyKey`/`RequireIfMatch` → `CanonicalizeJSON` → `SemanticHash` → `LookupReceipt` → replay hoặc
conflict hoặc dispatch thật → `EncodeResult`), `errors.go` (`WriteResourceHidden` leakage-normalization,
`StatusForAppErrorCode`'s bảng đầy đủ — phát hiện `errorcode.CodeWorkspaceQuarantined` đã map sẵn tới 423
Locked, đúng khớp "quarantine refusal" Verify line của chính task này, không phải trùng hợp), `action.go`
(`ValidAction{OperationID, ScopeKind, TargetVersion}` advisory).

Đọc kỹ 2 command thật task này bọc: `internal/app/workspacerelease/commands.go:209`
(`RequestWorkspaceSetRelease(ctx, uow, ids, authority ports.ReleaseEligibilityAuthority, cmd, req{FamilyID,
ProjectID})`) và `internal/app/workspacereconcile/commands.go:158`
(`RequestWorkspaceReconciliation(ctx, uow, ids, cmd, req{RepositoryWorkspaceID, ProjectID})`) — cả hai đều
là "producer ghi intent+job, KHÔNG bao giờ tự transition state hay chạm Git" (V3-11/V3-10's own split), cả
hai đều yêu cầu `cmd.ExpectedVersion` làm fence, cả hai đều KHÔNG bao giờ tự bump version của chính aggregate
mà chúng target — một phát hiện quan trọng ảnh hưởng trực tiếp cách viết test "stale generation" (xem Quyết
định #7).

Grep `ReleaseEligibilityAuthority`/`IsReleaseAuthorized` xác nhận: khác với doc comment gốc của
`ports.ReleaseEligibilityAuthority` ("không có real implementation, chỉ V3's own fake"), một implementation
THẬT đã tồn tại từ V5-10A — `internal/app/work/release_set.go:277`
(`EligibilityAuthority{uow}`/`NewEligibilityAuthority`, `IsReleaseAuthorized` đọc `ListReleaseSetsForFamily`
rồi check `IsCleanupEligible` trên ReleaseSet mới nhất). Không cần fake nào cho route thật — wiring thẳng
`work.NewEligibilityAuthority(uow)`.

Grep một query WorkspaceSet/RepositoryWorkspace state trên toàn bộ `internal/app` (đúng như hint của task):
không có gì. `internal/app/workspaceinspection` (V6-10C) đọc nội dung Git qua adapter, hoàn toàn khác concern
("state" ở đây là DB row, không phải blob/diff/log) — xác nhận đây thật sự là việc mới, không phải wrapper.
Đọc `internal/app/ports/work.go` xác nhận 4 method đọc cần dùng đã có sẵn, thật, đã test qua chính
`workspacerelease`/`workspacereconcile`'s eligibility check: `GetWorkspaceSetByFamilyID` (dòng 103, unique
theo family_id — cách duy nhất load một WorkspaceSet), `ListWorkspaceSetRepositoryWorkspaces` (dòng 211),
`GetRepositoryWorkspaceByID` (dòng 228, trả kèm FamilyID), `HasActiveWriteLease` (dòng 357, nhận
`[]string` repositoryWorkspaceIDs — gọi với 1 phần tử để lấy đúng lease của MỘT workspace thay vì câu hỏi
aggregate "cả set có lease nào không" mà `workspacerelease` tự hỏi). Đọc thẳng
`internal/adapters/sqlite/work.go:1046` xác nhận SQL thật đằng sau (`julianday(lease_until) >
julianday('now')`) — không phải "row tồn tại" mà "lease còn sống".

Đọc `internal/adapters/sqlite/workspace_lifecycle.go` (367 dòng, theo đúng gợi ý của task) cho state machine
thật: `QuarantineRepositoryWorkspace`/`ReleaseRepositoryWorkspace`/`RecreateRepositoryWorkspace`, cả 3 đều
fenced CAS thật trên `(id, state, version)`. Xác nhận "fence" trong vocabulary V6-10B chính là
`RepositoryWorkspace.Generation` — bất biến theo row (RECREATE luôn tạo row MỚI ở generation+1, không sửa
generation của row cũ) — đúng research note V6-10E đã ghi lại trong chính file báo cáo này. "Quarantine" là
`State == QUARANTINED`. "Lease" là `HasActiveWriteLease`.

Đọc `internal/archtest/command_envelope_test.go` (AST-scan "parse source thật, walk go/ast, fail nếu gọi hàm
cấm") và `internal/archtest/boundary_test.go`'s
`TestRequestWorkspaceReconciliationNeverImportsWorkspaceIO`/`TestRequestWorkspaceSetReleaseNeverImportsWorkspaceIO`
(forbidden import `"os"`/`"os/exec"`/`internal/adapters/...`) làm mẫu cho architecture test riêng của task
này. Xác nhận `internal/app/workspacereconcile/handler.go:156`'s `ExecuteWorkspaceReconciliation` là hàm
EXPORTED — nghĩa là chỉ cấm import adapter là chưa đủ, vì `httpapi` đã hợp pháp import chính package
`workspacereconcile` (để lấy `RequestWorkspaceReconciliation`/`ErrWorkspaceNotReconcilable`) — phải quét
thêm cả tên selector bị cấm, không chỉ import path.

Đọc `internal/delivery/httpapi/receiptreplay_test.go` (mẫu bắt buộc theo hard rule #1) và
`internal/app/workspacerelease/commands_test.go` (mẫu `mustSeedWorkspaceSet` dùng `fake.UnitOfWork` +
domain constructor thật) và `internal/adapters/sqlite/fixtures.go` (`SeedFixtureOwners`,
`SeedFixtureRepositoryWorkspace(WithLocator)`, `SeedFixtureWriteLease` — helper thật, "production code must
never call this" nhưng chính là con đường sanctioned để seed test thật, đã dùng lại nguyên xi cho V6-10E).
`fake.WorkRepository.HasActiveWriteLease` (`internal/app/ports/fake/work.go:406`) LUÔN trả `false, nil` —
doc comment của chính nó nói rõ: fake không model `write_leases`, muốn chứng minh lease thật phải dùng sqlite
thật (`commands_sqlite_test.go`) — áp dụng lại y hệt cho package mới của task này.

### Quyết định

1. **Query "state" là một package application mới, `internal/app/workspacestate`, không nhét vào
   `httpapi` trực tiếp.** Mirror đúng shape V6-10C đã lập (`workspaceinspection`): named public query, bound
   output, trước khi delivery serialize — dù ở đây không có Git/filesystem nào để bound, chỉ là 2 free
   function (`GetWorkspaceSetState`, `GetRepositoryWorkspaceState`) nhận `ports.UnitOfWork` trực tiếp (không
   phải struct `Queries` như `workspaceinspection` — vì package này chỉ có 1 dependency, giống hệt
   `catalog.CreateProject(ctx, uow, ids, cmd, req)`/`work.GetReleaseSet(ctx, uow, id)`, không phải 2 như
   `workspaceinspection.Queries{uow, reader}`).
2. **Không có port mới nào.** 4 method đọc cần dùng (`GetWorkspaceSetByFamilyID`,
   `ListWorkspaceSetRepositoryWorkspaces`, `GetRepositoryWorkspaceByID`, `HasActiveWriteLease`) đều đã thật,
   đã test qua chính `workspacerelease`/`workspacereconcile`'s eligibility check — một read query trên đúng
   những row đó không cần persistence surface mới, chỉ cần caller mới.
3. **`workspacestate.ErrScopeMismatch` — sentinel riêng, cùng tên nhưng khác package với
   `workspaceinspection.ErrScopeMismatch` và `ports.ErrScopeMismatch`.** Đúng tiền lệ V6-03's Quyết định #6
   đã tự xác lập: "same name, different package, different concern" không phải va chạm cần tránh —
   `workspaceinspection`'s là referential-integrity của chain Repository/WorkspaceSet, `ports`'s là
   CommandScope authorization, cái này là referential-integrity riêng của 2 query mới. HTTP layer normalize
   cả `ErrScopeMismatch` lẫn `ports.ErrPersistenceNotFound`/`ports.ErrCrossProjectReference` về CHUNG một 404
   ẩn danh (`WriteResourceHidden`) — đúng "no path leak" policy V6-10C đã lập.
4. **URL scheme tự thiết kế, vì chưa có route business nào khác từng tồn tại để theo.** `route.go`/
   `server.go`/`serve.go` (đọc kỹ trước khi quyết) xác nhận: KHÔNG có subpackage `httpapi/workspace` nào —
   toàn bộ V6-01/V6-01A/V6-02/V6-02A đều là file phẳng trong CHÍNH package `httpapi` (`route.go`,
   `health.go`, ...), và `serve.go` dòng 146-148 tự mời "a later endpoint task's own composition-root wiring
   adds its own routes.Register call here" — nghĩa là quy ước thật của repo là: mỗi task thêm file MỚI
   (tên riêng, không đụng file người khác) vào CHÍNH `internal/delivery/httpapi`, cộng đúng 1 dòng
   `RegisterXxxRoutes(...)` mới vào `serve.go`, không phải tạo subpackage. Theo đúng quy ước đó (không tự
   nghĩ tiền lệ mới): `GET /projects/{projectId}/workspace-sets/{familyId}`,
   `GET /projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}`,
   `POST .../workspace-sets/{familyId}/release`, `POST .../repository-workspaces/{repositoryWorkspaceId}/reconcile`
   — khớp chính xác 2 khóa tự nhiên 2 command thật đã dùng (`GetWorkspaceSetByFamilyID` chỉ có đúng 1 cách
   load: theo FamilyID; reconcile khóa theo RepositoryWorkspaceID). Go 1.27 (`go.mod`) xác nhận
   `net/http.ServeMux`'s `{param}` wildcard + `r.PathValue(...)` dùng được thẳng, không cần router thứ 3.
5. **POST release/reconcile là "update"-shaped theo đúng nghĩa V6-02: bắt buộc `If-Match`, không có field
   nào trong JSON body.** `FamilyID`/`RepositoryWorkspaceID`/`ProjectID` đều từ path (chính resource route đã
   đặt tên); `ExpectedVersion` LUÔN từ `VersionFromETag(If-Match)`, không bao giờ từ body — tránh 2 nguồn sự
   thật cho cùng 1 giá trị. Body luôn là `{}` — vẫn chạy qua `CanonicalizeJSON`/`SemanticHash` đầy đủ (không
   bỏ qua bước nào của flow chuẩn) để một client gửi payload lạ vẫn nhận 400 rõ ràng thay vì bị âm thầm bỏ
   qua.
6. **`writeWorkspaceCommandError` map sentinel thật sang `*apperror.Error` rồi giao cho `WriteAppError`/
   `StatusForAppErrorCode` sẵn có, không tự viết bảng status thứ 2.** `ports.ErrOptimisticConflict` →
   `CodePreconditionFailed` (412 — đúng ngữ nghĩa HTTP cho If-Match fail, cũng đúng "stale generation" Verify
   line); `ports.ErrReceiptConflict` → `CodeIdempotencyConflict` (409); `workspacerelease.ErrWorkspaceSetHasQuarantinedRepository`
   → `CodeWorkspaceQuarantined` (423 Locked — khớp sẵn có, không cần thêm code mới);
   `ErrWorkspaceSetHasActiveWriteLease`/`ErrWorkspaceSetHasActiveJob` → `CodeConflict` (409, "writer refusal");
   `ErrReleaseNotAuthorized` → `CodePolicyDenied` (403, GC-INV-26); `workspacereconcile.ErrWorkspaceNotReconcilable`
   → `CodeConflict` (409). `ports.ErrWorkspaceQuarantined` (khác sentinel, từ `ReleaseRepositoryWorkspace` — 1
   method cả 2 command của task này KHÔNG BAO GIỜ gọi) cố tình không có trong bảng — không map một lỗi
   không thể xảy ra.
7. **ValidAction cho release là heuristic CỐ Ý không đầy đủ, ghi rõ trong code comment tại sao.**
   `releaseValidActions` chỉ tái dùng 2 điều kiện đã load sẵn trong chính response (quarantine, active lease)
   cộng terminal-state check — không bao giờ tự query thêm `HasActiveJobForAggregateIDs` hay
   `IsReleaseAuthorized` chỉ để tô điểm một gợi ý advisory, vì làm vậy nghĩa là một GET thuần đọc bắt đầu tự ý
   chạm thêm bảng/authority ngoài — đúng "advisory, never authority" mà `ValidAction`'s own doc comment đã
   cam kết: POST thật vẫn có thể trả `ErrWorkspaceSetHasActiveJob`/`ErrReleaseNotAuthorized` dù GET vừa gợi ý
   action khả dụng, và đó KHÔNG phải bug. Ngược lại, `reconcileValidActions` là mirror CHÍNH XÁC (không phải
   heuristic) vì `ErrWorkspaceNotReconcilable`'s check chỉ thuần dựa vào State, không có điều kiện phụ nào
   khác.
8. **Test "stale generation" cho reconcile dùng kịch bản THẬT (quarantine thật bump version 1→2, gửi lại
   If-Match cũ "1"), còn release dùng version sai đơn giản.** Nghiên cứu #1 phát hiện: CẢ 2 command đều
   không bao giờ tự bump version của chính aggregate chúng target — WorkspaceSet.Version chỉ có thể di
   chuyển qua `TransitionWorkspaceSetState` (không có caller thật nào trong phạm vi task này để kích hoạt
   hợp lý), còn RepositoryWorkspace.Version DI CHUYỂN THẬT qua quarantine (đã có sẵn trong test khác của
   chính file này) — tận dụng lại đúng transition đó cho một kịch bản "stale" hoàn toàn thật thay vì chỉ gửi
   một số phiên bản bịa ra, còn phía release chấp nhận bài test đơn giản hơn (version sai) vì không có
   transition thật rẻ nào khác trong phạm vi task để tạo ra version thật đã di chuyển.

### Thực hiện

- `internal/app/workspacestate/queries.go` (mới): `ErrScopeMismatch`; `RepositoryWorkspaceState`,
  `WorkspaceSetState`; `GetWorkspaceSetState(ctx, uow, GetWorkspaceSetStateRequest{ProjectID, FamilyID})`,
  `GetRepositoryWorkspaceState(ctx, uow, GetRepositoryWorkspaceStateRequest{ProjectID, RepositoryWorkspaceID})`;
  `loadRepositoryWorkspaceState` helper gọi `HasActiveWriteLease` cho từng row.
- `internal/delivery/httpapi/workspaceroutes.go` (mới): `maxWorkspaceCommandBodyBytes`,
  `RegisterWorkspaceRoutes(routes, uow, ids)` (đăng ký cả 4 route, tự dựng `work.NewEligibilityAuthority(uow)`
  một lần), `newWorkspaceCommand` (mirror `cmd/aw/definition.go`'s `newDefinitionCommand`),
  `writeWorkspaceCommandError`.
- `internal/delivery/httpapi/workspacestate.go` (mới): DTO `workspaceSetStateResponse`/
  `repositoryWorkspaceStateResponse` (wire shape riêng, không serialize thẳng type application — đúng V6-02A's
  own convention), `releaseValidActions`/`reconcileValidActions`, 2 handler GET.
- `internal/delivery/httpapi/workspacerelease.go` (mới): `requestWorkspaceSetReleaseHandler` — full flow
  RequireIdempotencyKey → RequireIfMatch → VersionFromETag → CanonicalizeJSON → SemanticHash → LookupReceipt
  → replay/conflict/dispatch → `workspacerelease.RequestWorkspaceSetRelease` → EncodeResult (etag="" — result
  DTO không có field Version).
- `internal/delivery/httpapi/workspacereconcile.go` (mới): `requestWorkspaceReconciliationHandler`, flow y
  hệt, dispatch `workspacereconcile.RequestWorkspaceReconciliation`.
- `internal/archtest/workspace_delivery_boundary_test.go` (mới): `TestDeliveryWorkspaceRoutesNeverReachWorkspaceIOOrExecutor`
  — quét toàn bộ `internal/delivery/httpapi` (không chỉ file mới của task này, mirror đúng phạm vi
  `TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt`), cấm import `"os"`/`"os/exec"`/`internal/adapters/...`
  VÀ cấm gọi `ExecuteWorkspaceReconciliation`/`ExecuteWorkspaceSetRelease`/`QuarantineRepositoryWorkspace`/
  `ReleaseRepositoryWorkspace`/`RecreateRepositoryWorkspace`/`AcquireWriteLeases`.
- `cmd/aw/serve.go` (sửa, đúng 1 khối nhỏ): thêm `httpapi.RegisterWorkspaceRoutes(routes, uow,
  idsource.Random{})` ngay tại chỗ comment cũ đã tự mời, giữ nguyên toàn bộ phần còn lại của file.

### Test

- `internal/app/workspacestate/queries_test.go` (fake uow, mirror `mustSeedWorkspaceSet` từ
  `workspacerelease/commands_test.go`): happy path 1 READY + 1 QUARANTINED workspace, unknown family
  (`ErrPersistenceNotFound`), cross-project (`ErrScopeMismatch`), thiếu FamilyID/ProjectID, tương tự cho
  repository-workspace-state (happy path, unknown ID, cross-project, thiếu field). 9 test.
- `internal/app/workspacestate/queries_sqlite_test.go` (real sqlite, đúng lý do `fake.HasActiveWriteLease`
  luôn false): `TestGetRepositoryWorkspaceState_ActiveWriteLease_ReflectsTrueAgainstRealSqlite` —
  `SeedFixtureWriteLease` (đi qua EnqueueJob/ClaimJob/AcquireWriteLeases thật) rồi xác nhận cả query đơn lẻ
  lẫn query theo cả set đều thấy `HasActiveWriteLease=true`; `TestGetRepositoryWorkspaceState_RealQuarantine_ReflectsQuarantinedState`
  — `store.QuarantineRepositoryWorkspace` thật, xác nhận `State=QUARANTINED`, `Version=2`.
- `internal/delivery/httpapi/workspace_test.go` (real sqlite + real `httpapi.NewServer` qua goroutine +
  real `http.Client`, KHÔNG gọi handler trực tiếp — vì `r.PathValue` chỉ được điền khi request đi qua thật
  một `http.ServeMux` đã đăng ký đúng pattern `{param}`, gọi hàm trực tiếp sẽ luôn rỗng): 17 test —
  - GET: happy path (ETag đúng version, advisory action đúng cho cả set lẫn từng repo), unknown family 404,
    cross-project 404 BYTE-FOR-BYTE giống unknown (đọc cả `error.code` lẫn `error.message`), repository-state
    happy path phản ánh lease thật.
  - POST release: thiếu Idempotency-Key/If-Match/If-Match sai định dạng → 400; không có ReleaseSet nào → 403;
    quarantine thật (đã seal ReleaseSet trước, cô lập đúng lý do quarantine chứ không phải authorization) →
    423; write lease thật → 409; version sai → 412; happy path (ReleaseSet thật sealed qua
    `work.CreateReleaseSet`/`SealReleaseSet`) → 200 + xác nhận bằng `HasActiveJobForAggregateIDs` thật là có
    job; replay cùng key → `releaseJobId` giống hệt lần đầu (dùng `idsource.Random{}` — nếu lỡ mint job thứ 2
    ID gần như chắc chắn khác, mirror đúng kỹ thuật `TestCreateProject_SameKeySameBody_...` của
    receiptreplay_test.go); cùng key khác If-Match → 409 (ExpectedVersion là 1 phần SemanticHash, đổi nó
    đúng là "different body").
  - POST reconcile: happy path + job thật; replay giống hệt; RELEASED thật (qua
    `store.ReleaseRepositoryWorkspace`) → 409; stale THẬT (quarantine bump version 1→2, gửi lại If-Match cũ)
    → 412, rồi gửi đúng If-Match hiện tại (2) → 200 (chứng minh 412 ở trên đúng là do stale, không phải do
    reconcile tự chặn QUARANTINED); cross-project → 404.
  - `TestRegisterWorkspaceRoutes_RegistersFourProjectScopedRoutes`: đúng 4 route, mọi `ScopeKind =
    ScopeProject`, đúng 4 OperationID mong đợi.
- `internal/archtest/workspace_delivery_boundary_test.go`: xác nhận test THẬT sự bắt được vi phạm — tạm thêm
  1 file `.go` giả trong `internal/delivery/httpapi` import `"os/exec"`, chạy lại test thấy FAIL đúng dòng vi
  phạm, xoá file, chạy lại thấy PASS — không tin một test archtest mới viết chỉ vì nó pass, phải tự chứng
  minh nó cũng fail đúng lúc cần fail.
- `go build ./...`, `go vet ./...` sạch. `go test ./internal/app/workspacestate/... ./internal/delivery/httpapi/... ./internal/archtest/...`
  pass 100% (26 test mới cộng archtest). `go test ./cmd/...` pass (xác nhận wiring `serve.go` không vỡ gì).
- `go test ./...` toàn module: phát hiện 4 test fail trong lần chạy full-suite đầu tiên
  (`TestProjectWorkspaceGate` ở `internal/integration`; `TestV5AcceptCheckerWriteAttempt_...`,
  `TestV5AcceptFalseCompletionOracle`, `TestV5AcceptFullComposition_...` ở `internal/integration/v5accept`),
  tất cả cùng một triệu chứng "did not reach state ... within the deadline". Không tin ngay đây là flake quen
  mặt — điều tra thật theo đúng kỷ luật đã ghi trong memory của phiên này: (1) xác nhận bằng code-path rằng
  không file nào của task này (`workspacestate`, `httpapi/workspace*.go`, `serve.go`'s 6 dòng thêm) có thể
  chạm được `internal/app/runtime`/`internal/app/workerpool`/gate evaluation — không có import nào nối 2 phía;
  (2) `TestProjectWorkspaceGate` chạy lại cô lập (không contention) pass sạch 5.32s — đúng
  `baocaov5checklist.md:2384` đã tự ghi nhận đây LÀ flake timing đã biết từ trước; (3) chạy lại riêng cả gói
  `v5accept` (không cùng lúc với gói khác) — LẦN NÀY `TestV5AcceptCheckerWriteAttempt_...`/
  `TestV5AcceptFalseCompletionOracle` pass sạch (xác nhận đúng là contention từ full-suite trước), nhưng
  `TestV5AcceptFullComposition_...` fail LẦN 2 liên tiếp, luôn kẹt đúng 1 điểm (`gate_b` MACHINE_GATE vừa
  scheduled, không tiến thêm) — không dừng lại ở "chắc lại do máy chậm", tra `baocaov5checklist.md:6032` xác
  nhận test này ban đầu ổn định thật (14-18s/lần khi mới xây, không thuộc nhóm flake đã biết như
  `TestSupervisorNormalExit_...`); (4) chạy lại LẦN 3, cô lập tuyệt đối (chỉ đúng 1 test) — PASS, 18.52s,
  khớp chính xác baseline lịch sử "14-18s/lần" — xác nhận đây là timing-sensitivity thật dưới tải máy (phiên
  này đã chạy liên tục nhiều test suite sqlite/git thật nặng ngay trước đó: `gitworktree` 115s,
  `releasesetcommit` 131s, sqlite adapter 157s), không phải regression từ thay đổi của task này — không có
  cách nào 4 file mới + 6 dòng thêm vào `serve.go` (chỉ gọi `RegisterWorkspaceRoutes`, không đụng bất kỳ
  package runtime/scheduler nào) gây ảnh hưởng tới một pipeline AGENT→COMMAND→MACHINE_GATE→AGENT hoàn toàn
  độc lập. Không rerun mù thêm — dừng lại ở 3 lần vì bằng chứng đã đủ nhất quán (2 fail đều kẹt cùng 1 điểm
  giống timing chờ worker, 1 pass đúng baseline cũ), ghi lại đầy đủ ở đây cho phiên giám sát tự quyết định có
  cần điều tra sâu hơn hay không.

### Verify

- "quarantine/writer refusal": `TestRequestWorkspaceSetRelease_QuarantinedRepository_Returns423` (423,
  `store.QuarantineRepositoryWorkspace` thật) + `TestRequestWorkspaceSetRelease_ActiveWriteLease_Returns409`
  (409, `SeedFixtureWriteLease` thật qua AcquireWriteLeases thật) — cả 2 đều seal ReleaseSet trước để cô lập
  đúng lý do đang test, không lẫn với 403 authorization.
- "replay/stale generation": `TestRequestWorkspaceSetRelease_Replay_ReturnsIdenticalJobID`/
  `TestRequestWorkspaceReconciliation_Replay_ReturnsIdenticalJobID` (replay, ID giống hệt qua
  `idsource.Random{}`) + `TestRequestWorkspaceSetRelease_StaleIfMatch_Returns412`/
  `TestRequestWorkspaceReconciliation_StaleIfMatch_Returns412` (412 — bản reconcile dùng version đã di
  chuyển THẬT qua quarantine, xem Quyết định #8).
- "scope": `TestGetWorkspaceSetState_CrossProject_ReturnsIdenticalResourceHidden`/
  `TestRequestWorkspaceReconciliation_CrossProject_ReturnsResourceHidden` (404 ẩn danh, byte-for-byte giống
  unknown-resource) + `TestRegisterWorkspaceRoutes_RegistersFourProjectScopedRoutes` (mọi route đúng
  `ScopeProject`, không route nào lỡ mang `ScopeInstallation`).
- "architecture import/dispatch tests": `TestDeliveryWorkspaceRoutesNeverReachWorkspaceIOOrExecutor` — tự
  chứng minh có bắt lỗi thật (thêm rồi xoá file vi phạm, xem Test phía trên), không chỉ đọc code bằng mắt.
- "Hoàn thành khi": "recovery request possible without Git/DB surgery" — `TestRequestWorkspaceReconciliation_HappyPath_Returns200AndEnqueuesRealJob`
  chứng minh một request HTTP thuần (không SQL, không thao tác Git tay) đủ để enqueue một
  `WORKSPACE_RECONCILIATION` job thật, xác nhận bằng `HasActiveJobForAggregateIDs` thật, không phải suy diễn
  từ response 200 một mình; "delivery has no executor authority" — cả `TestDeliveryWorkspaceRoutesNeverReachWorkspaceIOOrExecutor`
  (structural) lẫn việc `internal/delivery/httpapi` không hề import `internal/adapters/gitworktree`/bất kỳ
  adapter Git nào (xác nhận bằng chính archtest đó) đều đồng thuận: không route nào trong 4 route của task
  này có khả năng tự thực thi Git/filesystem, kể cả nếu cố tình.

### Kết quả

Package `internal/app/workspacestate` mới hoàn toàn (2 file production, 2 file test, 15 test). Package
`internal/delivery/httpapi` có thêm 4 file production (`workspaceroutes.go`, `workspacestate.go`,
`workspacerelease.go`, `workspacereconcile.go`) + 1 file test (17 test). 1 architecture test mới trong
`internal/archtest`. `cmd/aw/serve.go` thêm đúng 1 khối 4 dòng gọi `RegisterWorkspaceRoutes`. Tổng 32 test
mới, tất cả pass. `go build/vet ./...` sạch trên toàn bộ module. 4 route HTTP thật lần đầu tiên tồn tại
trong repo ngoài health/bootstrap — `GET/POST /projects/{projectId}/workspace-sets/{familyId}[/release]`,
`GET/POST /projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}[/reconcile]` — mọi mutation đi
qua đúng flow CommandEnvelope/receipt-replay V6-02 đã định nghĩa, không route nào tự ghi receipt (đã có sẵn
`TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt` của V6-02 phủ toàn bộ package, kể cả 4 file mới). V6-10D
(map 3 query V6-10C sang HTTP GET) giờ có đủ dependency `{V6-00, V6-01A, V6-02A, V6-10B}` để bắt đầu ngay
khi PR này merge.

**Ghi chú cho phiên giám sát:** `internal/integration/v5accept`'s `TestV5AcceptFullComposition_RealFourRoleGraphReachesSucceededAndSurvivesRestart`
fail 2/3 lần chạy cô lập trên máy phiên này (lần 3 pass đúng baseline lịch sử 18.52s) — kết luận sơ bộ là
timing-sensitivity dưới tải máy cục bộ (xem Test phía trên), KHÔNG liên quan cấu trúc tới diff của PR này
(0 file chồng lấp với runtime/workerpool/gate), nhưng chưa re-verify trên CI thật (runner sạch, không có
tải tích luỹ từ hàng chục phút chạy test liên tục trước đó) — nếu CI của PR này cũng thấy đúng test này fail,
đối chiếu lại với baseline "14-18s/lần" của chính test đó (`baocaov5checklist.md:6032`) trước khi kết luận là
flake quen mặt hay regression thật.

## V6-03A — Project, repository và component HTTP endpoints

### Bối cảnh

V6-03A là task đầu tiên trong nhóm P2 ("application/HTTP slices sau V6-00 + V6-01A + V6-02 + V6-02A") thực sự
được triển khai — 4 dependency của nó (`V6-00`, `V6-01A`, `V6-02`, `V6-02A`) cộng `V6-03` đều đã merge
(`ac7f76b`, `bfa796d`, `36c5287`, `60a9f8f`). Trích nguyên văn spec từ `docs/design/08-v6-api-projections.md`
dòng 190-201: "Mục tiêu: expose Project catalog, repository onboarding và discovered component/pack
assignment"; "Phạm vi: project create/list/detail; repository register/list/detail/onboarding/probe-history/
retry; component query và exact pack assignment"; "Không làm: không duplicate Doctor, expose helper
`CreateComponent`, hoặc giả sync success trước probe"; "Thực hiện: register trả `REGISTERING`; retry chỉ map
`RetryRepositoryProbe` khi `BLOCKED` và leaf/route canonical dùng `retry-probe`. Component chỉ đọc topology do
probe discover; assignment pin exact version"; "Hoàn thành khi: catalog/onboarding có một route owner và
repository ID là authority."

Đây cũng là task HTTP endpoint ĐẦU TIÊN thực sự tồn tại trong toàn bộ V6 — V6-01/V6-01A/V6-02/V6-02A chỉ xây
primitive dùng chung (route registry, security, DTO/cursor, command envelope), chưa route business nào. Nghĩa
là task này vừa phải tự thiết kế route thật đầu tiên, vừa tự thiết lập convention "mỗi endpoint task sở hữu
subpackage riêng" (§1.8 của design doc) mà 3 task song song khác (`V6-04`, `V6-06`, `V6-10B`) đang chạy đồng
thời trong các worktree khác sẽ đi theo.

### Nghiên cứu

Đọc lại toàn bộ 8 file hạ tầng V6-01/V6-01A/V6-02/V6-02A trước khi viết route đầu tiên (không đoán API):
`route.go` (`RouteDescriptor{Method,Path,OperationID,ScopeKind,RequestSchema,ResponseSchema,Handler}`,
`RouteRegistry.Register` panic khi trùng `(Method,Path)` hoặc `OperationID`), `commandenvelope.go`
(`RequireIdempotencyKey`/`RequireIfMatch`/`CanonicalizeJSON`/`SemanticHash`/`ETagFromVersion`/
`VersionFromETag`), `receiptreplay.go` (`LookupReceipt`/`ReconcileReceipt`/`WriteReceiptReplay`/
`EncodeResult`), `errors.go` (`WriteError`/`WriteResourceHidden`/`StatusForAppErrorCode`/`WriteAppError`),
`principal.go` (`PrincipalFromContext`/`BindPrincipal` — actor/roles KHÔNG BAO GIỜ đọc từ request), `server.go`
(`NewServer` compose `mux.HandleFunc(d.Method+" "+d.Path, d.Handler)` từ `Routes.Descriptors()`).

Phát hiện quan trọng nhất nằm ngay trong `cmd/aw/serve.go` dòng 146-149 (bản gốc, trước khi sửa): một comment
để lại từ chính V6-01A — "No further route fragments exist yet in this task; **a later endpoint task's own
composition-root wiring adds its own routes.Register call here** without needing to touch this file's shared
setup." Đây là bằng chứng trực tiếp, không phải suy diễn, rằng mỗi endpoint task PHẢI tự wire route thật vào
composition root (`cmd/aw/serve.go`), không chỉ dừng ở việc xây subpackage rồi chờ một task compose sau này
(`V6-12`'s own Phạm vi "root router wiring" là frozen OpenAPI/parity artifact tổng hợp SAU, không phải lần đầu
tiên route được wire thật vào server).

Đọc toàn bộ `internal/app/catalog/commands.go` (727 dòng, bản trước khi sửa) tìm đúng 8 hàm cần wrap qua HTTP:
`CreateProject`/`RegisterRepository`/`RetryRepositoryProbe`/`AssignComponentPack` (command) và
`ListProjects`/`GetProject`/`ListProjectRepositories`/`ListComponentPackAssignments`/
`GetEffectiveComponentPackAssignment` (query). Phát hiện 3 lỗ hổng thật: không có `GetRepository`/
`ListRepositoryProbeAttempts` (application-level) dù `ports.CatalogRepository` ĐÃ có cả hai ở tầng persistence
(`unitofwork.go` dòng 213-218, comment tự khai "the 'probe history' evidence ... GET /repositories/{id}/
onboarding"); không có `GetComponent`/`ListComponents` application-level nào — và `ports.CatalogRepository`
thậm chí CHƯA CÓ method `ListComponents` ở tầng persistence nào cả (chỉ có `GetComponent` theo ID đơn lẻ).

Đối chiếu `docs/design/01-system-design.md` dòng 557-564 (API sketch gốc, viết trước cả V6-00) để lấy đúng
route path/mô tả thay vì tự nghĩ:
```
GET/POST /projects | list/create project (installation scope)
GET /projects/{id} | project detail
GET/POST /projects/{id}/repositories | list/register; POST trả repository REGISTERING + probe job
GET /repositories/{id}/onboarding | trạng thái/error/probe history có thể hành động
POST /repositories/{id}/retry-probe | dispatch RetryRepositoryProbe khi BLOCKED; không có generic probe mutation
GET /projects/{id}/components | catalog component đã được repository onboarding/probe discover
GET/POST /components/{id}/pack-assignments | list/assign exact Engineering Pack version
```
Sketch này chỉ có MỘT route `/repositories/{id}/onboarding` gộp cả "trạng thái/error" lẫn "probe history" —
nhưng Phạm vi của chính V6-03A lại liệt kê "detail" TÁCH RIÊNG khỏi "onboarding"/"probe-history" (6 từ khác
nhau: "register/list/detail/onboarding/probe-history/retry"). Quyết định cách xử lý mâu thuẫn này nằm ở mục
Quyết định bên dưới.

Đọc `internal/adapters/sqlite/txrunner.go` dòng 130-142 (`MapSQLiteError`) phát hiện: lỗi persistence CHỈ có 3
dạng thật sự phân biệt được ở tầng HTTP — sentinel đã classify (`ports.ErrPersistenceNotFound`/
`ErrOptimisticConflict`/`ErrCrossProjectReference`/`ErrScopeMismatch`/`ErrReceiptConflict`, luôn trả trực
tiếp, KHÔNG BAO GIỜ qua `MapSQLiteError`), `*apperror.Error` thật (chỉ khi `MapSQLiteError` tự bọc — lock
contention → `CodeUnavailable`, còn lại → `CodeInternal`), và lỗi validation trần trụi không có sentinel nào
cả (`project.NewProject`/`NewRepository`/`NewComponentPackAssignment`'s own `errors.New(...)`, hoặc
`catalog.CreateProject`'s own "Name is required"). KHÔNG có 1 bảng mapping có sẵn nào trong `errors.go` xử lý
đúng 2 dạng sau (`WriteAppError` chỉ biết `*apperror.Error`) — đây là gap thật task này phải tự đóng bằng một
hàm mapping riêng (`writeCatalogError`), không phải tái dùng nguyên xi.

Đọc `internal/app/catalog/commands_test.go` dòng 524-565 (`moveRepositoryToBlocked`) làm mẫu chính xác cho
"đưa một Repository vào trạng thái BLOCKED để test retry-probe mà không cần Git/filesystem thật" — gọi trực
tiếp `tx.Catalog().TransitionRepositoryStatus` 2 lần (REGISTERING→PROBING→BLOCKED) qua chính production CAS
method, KHÔNG PHẢI SQL tay hay mock — đúng production path V3-02's own repository-probe worker cũng gọi.

### Quyết định

1. **Subpackage `internal/delivery/httpapi/catalog`**, alias import `appcatalog` cho
   `internal/app/catalog` (2 package cùng tên `catalog`, khác import path — Go compile sạch vì package hiện
   tại luôn dùng identifier KHÔNG qualify, import luôn qualify bằng tên/alias; chọn alias tường minh thay vì
   dựa vào rule ngầm này để người đọc sau không phải tự suy luận). Đây là subpackage HTTP endpoint ĐẦU TIÊN
   tồn tại — tự thiết lập convention "mỗi endpoint task sở hữu subpackage/descriptor/test riêng" (§1.8) cho
   3 task song song (`V6-04`/`V6-06`/`V6-10B`) đi theo, không phải áp dụng một convention có sẵn.
2. **Giải mâu thuẫn "detail" vs "onboarding" (Nghiên cứu ở trên) bằng CẢ HAI route, không chọn một.**
   `GET /repositories/{id}` (detail: identity đăng ký — name/remoteLocator/defaultRef/status/version) tách
   khỏi `GET /repositories/{id}/onboarding` (status/error/version LẶP LẠI + toàn bộ probe-history evidence
   log) — đọc đúng nghĩa đen Phạm vi's 6 từ, đồng thời khớp việc system-design sketch chỉ vẽ MỘT route gộp
   "onboarding" với "probe-history" (không vẽ route probe-history riêng) bằng cách gộp 2 khái niệm đó vào
   cùng 1 route thứ hai, không phải 3 route riêng biệt.
3. **Không có route `GET /components/{id}` detail riêng.** Phạm vi dùng số ít "component query" (khác cách
   dùng "register/list/detail" 3 từ riêng của repository) và system-design sketch cũng chỉ vẽ
   `GET /projects/{id}/components` (list) + `GET/POST /components/{id}/pack-assignments` — không vẽ path
   `/components/{id}` trần trụi. Component list (`componentView`) đã đủ field cho "query"; thêm route riêng
   sẽ là phát minh ngoài trích dẫn.
4. **4 query application-layer mới (`GetRepository`, `ListRepositoryProbeAttempts`, `GetComponent`,
   `ListComponents`) KHÔNG nhận `ports.CommandScope`** — khác hẳn `GetProject` (bắt buộc
   `scope.ProjectID() == projectID`). Lý do: `GetProject`'s caller LUÔN ĐÃ BIẾT project nào (route luôn nest
   dưới `/projects/{id}`) và dùng scope để ASSERT lại claim đó. `GetRepository`/`GetComponent` phục vụ đúng
   route `/repositories/{id}/...` và `/components/{id}/...` — không nest dưới project, Repository/Component
   ID tự nó đã là identity toàn cục caller-opaque (V3-01's own "Repository identity là ID đã đăng ký"). Đây
   chính là hàm đầu tiên cho handler "route reload authoritative target để suy Project/scope" (contract
   chung §1.3) — trả ProjectID để handler tự dựng `ports.ProjectScope(...)` SAU, không phải nhận sẵn.
5. **`writeCatalogError` (errors.go) map lỗi theo đúng 3 bucket thật đã xác nhận ở Nghiên cứu**, thứ tự: sentinel
   `ports.Err*` đã classify (mỗi loại một status cụ thể) → `*apperror.Error` thật (dùng lại
   `StatusForAppErrorCode`/`WriteAppError` có sẵn) → mặc định 400 `INVALID_REQUEST` (bucket còn lại chỉ có thể
   là lỗi validation trần trụi, vì không hàm nào trong `internal/app/catalog` tự làm filesystem/process I/O có
   thể fail theo cách thứ 4).
6. **4 command mutating (`CreateProject`/`RegisterRepository`/`RetryRepositoryProbe`/`AssignComponentPack`)
   encode response TRỰC TIẾP từ chính `*Result` struct của application layer, không bọc view riêng** — cả 4
   Result struct (`CreateProjectResult`/`RegisterRepositoryResult`/`RetryRepositoryProbeResult`/
   `AssignComponentPackResult`) ĐÃ có json tag camelCase đúng chuẩn sẵn (đọc lại `commands.go` xác nhận, không
   giả định) — chỉ 5 route GET mới cần `views.go` riêng (domain struct `project.Project`/`Repository`/
   `Component`/`ComponentPackAssignment` và `ports.RepositoryProbeAttempt` đều KHÔNG có json tag, vì chúng
   không phải wire contract).
7. **Status code:** 201 cho create/register/assign (tài nguyên tồn tại thật ngay khi response trả về, dù
   trạng thái con của nó — REGISTERING — còn async); 202 cho retry-probe (không tạo tài nguyên mới, chỉ đẩy
   trạng thái đang có sang PROBING không đồng bộ — mirror đúng V6-06's own "cancel trả CANCELLING ... accepted
   response không terminal tức thì" precedent, cùng lý luận áp cho một hành động khác).
8. **Thứ tự retry-probe: receipt lookup TRƯỚC, kiểm precondition BLOCKED SAU (chỉ khi receipt vắng mặt)** —
   đọc đúng nghĩa đen "Flow ... replay/conflict → nếu absent mới kiểm current version/external prework/
   dispatch" (contract chung §1.2). Nếu đảo ngược (kiểm BLOCKED trước khi tra receipt), một replay hợp lệ của
   lần retry-probe THÀNH CÔNG trước đó (repository giờ đã là PROBING, không còn BLOCKED) sẽ bị reject sai —
   đúng "Same-key committed replay thắng ETag/state drift" (contract §1.2). Test
   `TestRetryRepositoryProbe_ReplaySameKey_WinsOverStateDrift` chứng minh trực tiếp quyết định này.
9. **ETag của response retry-probe tính bằng `expectedVersion + 1`, không đọc lại row.** Dựa trên chính doc
   comment của `transitionRepositoryStatusTx` (catalog.go): "On success, version increments by exactly one" —
   guarantee có sẵn, đọc lại thêm 1 lần chỉ để lấy ETag là round-trip thừa.
10. **Không có ETag trên response create/register/assign** — cả 3 Result struct tương ứng đều không mang field
    Version (đối chiếu điểm 6), và cả 3 resource này (Project/mới-register-Repository/ComponentPackAssignment)
    hiện chưa có route update nào cần `If-Match` để bảo vệ, nên phát minh một ETag không ai tiêu thụ là suy
    đoán ngoài phạm vi.
11. **Wiring thật vào `cmd/aw/serve.go`** (không chỉ dừng ở subpackage), đúng extension point comment V6-01A để
    lại — 1 dòng `httpcatalog.RegisterRoutes(routes, httpcatalog.Dependencies{...})`, không sửa phần setup
    chung (readiness checker/token/principal) phía trên.

### Thực hiện

- `internal/app/ports/unitofwork.go`: `CatalogRepository` thêm method `ListComponents(ctx, projectID)
  ([]project.Component, error)`.
- `internal/adapters/sqlite/catalog.go`: `catalogRepository.ListComponents` + `listComponentsTx`
  (`SELECT ... FROM components WHERE project_id = ? ORDER BY id`, mirror đúng `listProjectRepositoriesTx`).
- `internal/app/ports/fake/unitofwork.go`: `CatalogRepository.ListComponents` (filter map theo ProjectID, sort
  theo ID).
- `internal/app/catalog/commands.go`: 4 query mới ở cuối file — `GetRepository`, `ListRepositoryProbeAttempts`,
  `GetComponent`, `ListComponents` (chi tiết lý do không nhận scope: xem Quyết định #4).
- `internal/delivery/httpapi/catalog/` (package mới, 7 file production):
  - `catalog.go`: doc comment package, `Dependencies{UoW, IDs}`, `handler` struct, `RegisterRoutes` đăng ký
    đúng 11 `RouteDescriptor` (3 project + 5 repository + 3 component).
  - `command.go`: `beginMutation` — helper dùng chung cho cả 4 handler mutating (Idempotency-Key bắt buộc →
    canonicalize+decode body → build `ports.Command` → `LookupReceipt`/`ReconcileReceipt`/`WriteReceiptReplay`
    → trả `(cmd, ok)`); `expectedVersion` là tham số (0 cho create, version parse từ If-Match cho retry-probe)
    thay vì tự parse If-Match bên trong (chỉ 1/4 route cần nó).
  - `errors.go`: `writeCatalogError` (Quyết định #5).
  - `views.go`: `projectView`/`repositoryView`/`componentView`/`packAssignmentView`/`probeAttemptView`/
    `onboardingView` + list wrapper tương ứng + `retryProbeValidActions` (advisory `httpapi.ValidAction` khi
    `Status == BLOCKED`, dùng chung bởi `repositoryView` và `onboardingView`).
  - `project.go`: `createProject`/`listProjects`/`getProject`.
  - `repository.go`: `registerRepository`/`listProjectRepositories`/`getRepository`/
    `getRepositoryOnboarding`/`retryRepositoryProbe`.
  - `component.go`: `listProjectComponents`/`listComponentPackAssignments`/`assignComponentPack`.
- `internal/archtest/catalog_http_test.go`: `TestHTTPAPICatalogNeverCallsCreateComponent` — AST-scan y hệt
  idiom của `TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt` (parse source thật, cấm selector `.CreateComponent(`
  bất kỳ đâu trong `internal/delivery/httpapi/catalog`), đúng yêu cầu "Không làm" line của chính task này.
- `cmd/aw/serve.go`: import `httpcatalog`, 1 dòng `RegisterRoutes` tại đúng vị trí comment V6-01A để lại
  (Quyết định #11) — không sửa gì khác trong file.

### Test

`internal/delivery/httpapi/catalog/` — 4 file test, 21 test mới, TOÀN BỘ chạy qua real sqlite (`sqlite.Open`
+ `sqlite.NewUnitOfWork`, mirror đúng `receiptreplay_test.go`'s own pattern), real `RegisterRoutes` vào
`httpapi.NewRouteRegistry()` thật, mux dựng đúng cách `server.go`'s own `NewServer` dựng (chỉ bỏ
Host/Origin/token/CorrelationID/Recover — đã được `security_test.go`/`server_test.go` chứng minh riêng, không
phải phạm vi task này), không mock/fake DB nào:

- `project_test.go` (7 test): create trả 201 + ProjectID sinh mới + không có ETag; thiếu Idempotency-Key →
  400; **replay same-key qua thật 2 lần HTTP request** → đúng ProjectID gốc, `ListProjects` xác nhận đúng 1
  row; same-key khác body → 409; list trả đúng N project đã tạo; get detail đúng; get ID không tồn tại → 404
  với message generic (`"the requested resource was not found"`, đúng leakage-normalization policy).
- `repository_test.go` (12 test, file quan trọng nhất): **`TestRegisterRepository_ReturnsRegisteringStatusNeverFakedActive`**
  — cả response POST lẫn GET ngay sau đó đều REGISTERING, không bao giờ ACTIVE giả (đúng "Không làm: giả sync
  success trước probe"), cộng ETag `"1"` đúng version mới tạo; register dưới project không tồn tại → 404 (qua
  đúng lỗi thật từ `registerRepositoryTx`'s own project-exists check, không phải giả lập); list repositories;
  get detail/onboarding (onboarding rỗng khi chưa probe lần nào); **`TestRetryRepositoryProbe_NotBlocked_Returns409AndNeverDispatches`**
  — đây là "handler dispatch spy" Verify bullet, chứng minh bằng SIDE EFFECT THẬT (Version/Status của
  repository không đổi sau lệnh gọi bị reject) thay vì mock — đúng tinh thần "không mock, real production
  paths" của rule #1, không phải spy giả; thiếu If-Match → 400; retry khi thật sự BLOCKED (dựng qua
  `moveRepositoryToBlocked`, mirror `commands_test.go`'s own helper, gọi thật `TransitionRepositoryStatus`)
  → 202, PROBING, ETag `"4"` đúng `expectedVersion+1`; **`TestRetryRepositoryProbe_ReplaySameKey_WinsOverStateDrift`**
  — chứng minh trực tiếp Quyết định #8: gọi retry-probe thành công 1 lần (BLOCKED→PROBING), gọi LẠI với CÙNG
  key sau khi repository đã rời BLOCKED — phải replay (200, đúng ProbeJobID gốc, Version không đổi lần 2),
  KHÔNG được 409 dù một attempt mới thật sự lúc này chắc chắn sẽ 409.
- `component_test.go` (8 test): seed Component qua chính `appcatalog.CreateComponent` thật (đứng thay cho
  V3-02's own onboarding-probe worker — route HTTP không bao giờ tự gọi hàm này, đã chứng minh riêng qua
  archtest) rồi list qua route thật; list dưới project không tồn tại → 404; list pack-assignments của
  component không tồn tại → 404; chưa assign gì → `effective: null`; **`TestAssignComponentPack_CreatesAssignmentAndBecomesEffective`**
  — PackVersionID round-trip byte-for-byte qua cả POST response lẫn GET's `effective` field (đúng "assignment
  pin exact version"); **`TestAssignComponentPack_FutureEffectiveAt_NotYetEffective`** — assignment với
  `effectiveAt` tương lai (+48h) nằm trong history nhưng KHÔNG phải effective hiện tại, chứng minh
  `GetEffectiveComponentPackAssignment` được tôn trọng đúng nghĩa, không bị âm thầm ép về "now"; thiếu
  Idempotency-Key → 400.
- `internal/archtest/catalog_http_test.go`: `TestHTTPAPICatalogNeverCallsCreateComponent` pass — không selector
  `.CreateComponent(` nào trong subpackage; `TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt` (V6-02, đã merge)
  chạy lại VẪN pass dù giờ nó walk thêm cả subpackage con (`filepath.WalkDir` không `SkipDir` khi gặp thư mục
  con) — xác nhận package mới không hề mở write transaction hay ghi receipt trực tiếp.
- `cmd/aw` (`TestServe_*`, không sửa file test nào, chỉ chạy lại nguyên trạng sau khi wire route): toàn bộ pass
  — quan trọng nhất là `TestServe_StartsServesHealthAndShutsDownGracefully`, vì nếu 11 `RouteDescriptor` mới
  có bất kỳ trùng `(Method,Path)` hay `OperationID` nào, `RouteRegistry.Register` sẽ PANIC ngay lúc `serve()`
  khởi động — test pass tức là chứng minh thật không có xung đột registry, không phải chỉ đọc code bằng mắt.

**Một bug thật tự phát hiện qua chính test của mình** (không phải lỗi trong code V6-03A, nhưng lộ ra một gap
thật ở tầng dưới): `TestListProjectRepositories_ReturnsRegistered` lúc đầu FAIL với `500 INTERNAL — "sqlite:
unexpected error"` khi đăng ký 2 repository cùng tên "svc" dưới 1 project. Truy ngược: `repositories` có
`UNIQUE (project_id, name)` (`migrations/0001_initial_schema.sql` dòng 21), nhưng `MapSQLiteError`
(`txrunner.go` dòng 130-142) KHÔNG phân loại riêng lỗi UNIQUE constraint — mọi lỗi `tx.ExecContext` không phải
"busy" đều rơi vào `CodeInternal` chung, nên một `POST /projects/{id}/repositories` trùng tên từ client thật
SẼ trả 500 thay vì 409 đúng ngữ nghĩa "conflict". Đây là gap có thật, có từ V1-06/V3-01 (trước V6-03A rất xa),
KHÔNG thuộc phạm vi task này (không route/query nào của V6-03A tự gây ra nó, và Verify line của task không
yêu cầu chứng minh case "duplicate name"). Xử lý đúng phạm vi: sửa lại fixture test của chính mình
(`registerTestRepository` dùng `name = repositoryID`, không hardcode "svc" nữa) thay vì sửa `MapSQLiteError`
— việc phân loại UNIQUE constraint tốt hơn là một cải tiến cross-cutting ảnh hưởng mọi command khác
(`CreateComponent`'s own `UNIQUE(repository_id, path)`, v.v.), xứng đáng một task riêng, không phải sửa chen
vào task này.

`go build ./...`, `go vet ./...` sạch trên toàn bộ module. `go test ./...` full suite (99 package): 1 fail duy
nhất, `TestEndToEnd_Release_ReadyWorkspaceSet_ReleasesRepositoryAndSet`
(`internal/app/workspacerelease/handler_sqlite_test.go:329`, "WORKSPACE_PROVISION job state = LEASED, want
SUCCEEDED") — package này không liên quan gì đến thay đổi của task (không đụng catalog/project/repository/
component/scheduling), và triệu chứng ("job state chưa kịp SUCCEEDED lúc assertion chạy") đúng dạng
CPU/IO-contention flake đã ghi nhận ở chính V6-03's own Test section trước đây, không tin ngay là regression
thật (đúng rule #7). Xác nhận bằng cách chạy lại riêng, không contention: `go test
./internal/app/workspacerelease/... -run TestEndToEnd_Release_ReadyWorkspaceSet_ReleasesRepositoryAndSet -v`
pass (4.39s), rồi chạy lại NGUYÊN CẢ PACKAGE (19 test, không chỉ 1 test) `go test
./internal/app/workspacerelease/... -v` — pass sạch 100%, 10.093s, gồm cả 3 test `TestEndToEnd_Release_*`. Xác
nhận đây là contention từ máy đang chạy `go test ./...` full suite đồng thời với việc khác trong phiên, không
phải lỗi thật từ V6-03A.

### Verify

- "isolated route/schema goldens": mỗi route có test riêng qua real sqlite, tách biệt khỏi
  Host/Origin/token/CorrelationID middleware (đã chứng minh ở nơi khác) — đúng nghĩa "isolated" của Verify
  line.
- "async state": `TestRegisterRepository_ReturnsRegisteringStatusNeverFakedActive` chứng minh cả response
  lẫn GET ngay sau đều REGISTERING; `TestRetryRepositoryProbe_Blocked_...` chứng minh 202 + PROBING, không
  giả ACTIVE tức thì.
- "retry": `TestRetryRepositoryProbe_NotBlocked_Returns409AndNeverDispatches` (từ chối đúng khi không BLOCKED,
  chứng minh bằng side-effect thật) + `TestRetryRepositoryProbe_Blocked_...` (chấp nhận đúng khi BLOCKED).
- "replay": `TestCreateProject_ReplaySameKey_...`, `TestRetryRepositoryProbe_ReplaySameKey_WinsOverStateDrift`
  — replay thắng state drift, đúng contract chung.
- "scope": mọi route project-scoped dựng `ports.ProjectScope` đúng cách (từ path khi route đã nest dưới
  project, từ reload authoritative target khi route chỉ có Repository/Component ID) — `GetProject`/
  `RegisterRepository`/`RetryRepositoryProbe`/`AssignComponentPack` tự chính chúng vẫn re-enforce
  `ports.ErrScopeMismatch`/`ErrCrossProjectReference` phía application layer, route không tắt qua được.
- "redaction": `TestGetProject_UnknownID_Returns404Hidden` xác nhận message generic
  (`"the requested resource was not found"`), không leak "project X does not exist" hay tương tự.
- "handler dispatch spy": `TestRetryRepositoryProbe_NotBlocked_Returns409AndNeverDispatches` chứng minh bằng
  side effect thật (Version/Status không đổi) rằng route KHÔNG dispatch command thật khi precondition sai —
  đúng tinh thần "real production paths, không mock" hơn một spy nhân tạo.
- "Không làm — không expose helper CreateComponent": `TestHTTPAPICatalogNeverCallsCreateComponent` (archtest,
  AST-scan thật) + không route nào trong 11 route đăng ký gọi tới nó (đọc lại `catalog.go`'s own
  `RegisterRoutes` xác nhận).
- "Không làm — không giả sync success trước probe": mọi response REGISTERING/PROBING đều lấy trực tiếp từ
  `result.Status` của application command (chưa bao giờ hardcode hay override thành ACTIVE) — xác nhận qua
  `TestRegisterRepository_ReturnsRegisteringStatusNeverFakedActive`.
- "Hoàn thành khi — catalog/onboarding có một route owner": đúng — `internal/delivery/httpapi/catalog` là
  package DUY NHẤT đăng ký route `/projects`, `/repositories/*`, `/components/*/pack-assignments`
  (`RouteRegistry.Register`'s own duplicate-path panic tự bảo đảm không route owner thứ hai nào có thể lọt
  qua CI mà không panic ngay lúc khởi động).
- "Hoàn thành khi — repository ID là authority": `getRepository`/`getRepositoryOnboarding`/
  `retryRepositoryProbe` đều resolve trực tiếp từ RepositoryID (path `/repositories/{id}`, không nest project)
  qua `appcatalog.GetRepository`, không route nào cần caller tự khai ProjectID để dùng 3 route này.

### Kết quả

Package mới `internal/delivery/httpapi/catalog` (7 file production, 4 file test, 21 test), 4 query mới trong
`internal/app/catalog` (`GetRepository`/`ListRepositoryProbeAttempts`/`GetComponent`/`ListComponents`), 1
method interface mới (`CatalogRepository.ListComponents`, 2 implementation thật — sqlite + fake), 1 architecture
test mới (`internal/archtest/catalog_http_test.go`), 1 dòng wiring thật vào `cmd/aw/serve.go`. 11 route HTTP
thật lần đầu tồn tại trong toàn bộ V6: `POST/GET /projects`, `GET /projects/{id}`, `POST/GET
/projects/{id}/repositories`, `GET /repositories/{id}`, `GET /repositories/{id}/onboarding`, `POST
/repositories/{id}/retry-probe`, `GET /projects/{id}/components`, `GET/POST
/components/{id}/pack-assignments`. `go build/vet ./...` sạch; `go test ./...` full suite (99 package) xanh 100% sau khi xác nhận 1 fail ban đầu
(`internal/app/workspacerelease`) là contention flake không liên quan (xem mục Test) — chạy lại riêng package
đó sạch. Phát hiện và ghi lại 1 gap thật ở tầng persistence (UNIQUE constraint không được `MapSQLiteError`
phân loại — xem mục Test), nằm ngoài phạm vi task này, được xử lý đúng mức bằng cách sửa fixture của chính
mình thay vì lan sang sửa code đã merge trước đó.

## V6-05 — Definition authoring endpoints

### Bối cảnh

V6-05 nằm trong nhóm P2 "application/HTTP slices sau V6-00 + V6-01A + V6-02 + V6-02A" — cả 4 dependency đã
merge trước khi task này bắt đầu (V6-00 PR #29, V6-01A PR #35, V6-02 PR #39, V6-02A PR #33). Branch từ
`origin/master` tại `ab4ee48` ("feat(v6-04): WorkItem, family, readiness and scope-expansion endpoints
(#43)") — tại thời điểm đó `baocaov6checklist.md` đã có 3414+ dòng, V6-03A/V6-04/V6-06/V6-10B đều đã merge.
Theo đúng brief của task, 3 sibling khác trong cùng batch (V6-06A, V6-06D, V6-07) đang chạy song song và
cũng ghi vào chính file này — merge conflict khi mở PR là kỳ vọng bình thường, không phải lỗi.

Trích nguyên văn spec (dòng 228-238 `docs/design/08-v6-api-projections.md`, không diễn giải lại): "Mục
tiêu: create/validate/publish/list/detail/version/diff cho global và project definitions... Phạm vi: bounded
text/file payload và exact immutable version queries... Không làm: không persist invalid draft thành runtime
version hoặc tin scope từ payload... Thực hiện: route derives scope; item/version reload authoritative
Definition; publish trả source/compiled hash và exact pins; diff operands phải cùng scope... Verify: isolated
schemas, location diagnostics, replay, version/diff và global/project negative matrix... Hoàn thành khi:
UI/CLI author→validate→publish→inspect không seed SQLite... Nguồn: AK-ARCH-001, ADR-028."

Khác mọi endpoint task trước đó (V6-03A/V6-04/V6-06/V6-10B đều chỉ có scope PROJECT), V6-05 là task đầu tiên
phải phục vụ CẢ HAI closed scope kind của ADR-025 (INSTALLATION lẫn PROJECT) cho cùng một tập resource —
buộc phải tự thiết kế convention "route derives scope" từ đầu, không có route hai-scope nào trước đó để soi.

### Nghiên cứu

Đọc lại toàn bộ 10 file nền tảng `internal/delivery/httpapi` (route.go, errors.go, page.go, cursor.go,
freshness.go, action.go, media.go, sse.go, commandenvelope.go, receiptreplay.go) — xác nhận lại đúng flow
V6-04 đã tài liệu hoá: `RequireIdempotencyKey`/`RequireIfMatch` → `CanonicalizeJSON` → `SemanticHash` →
`LookupReceipt` → `WriteReceiptReplay`/`ErrReceiptHashConflict` → dispatch → `EncodeResult`. Đọc
`internal/delivery/httpapi/workitem` (9 file) và `.../catalog` (7 file) làm structural precedent — xác nhận
`catalog.go`'s `createProject`/`listProjects` (dùng `ports.InstallationScope()`) là ví dụ DUY NHẤT trong repo
của một route installation-scoped thật (mọi route khác của catalog và toàn bộ workitem đều project-scoped) —
không có route nào trước đây từng phải chọn GIỮA 2 scope theo path prefix như V6-05 cần.

Đọc `internal/app/definitions/commands.go` (472 dòng) TOÀN BỘ trước khi viết handler đầu tiên. Xác nhận
chính xác 5 hàm public: `CreateDefinition` (nhận `Scope` trực tiếp từ caller, không tự suy), `ValidateDraft`
(dry-run THUẦN — không nhận `ports.Command`, comment gốc: "a dry run has no side effect to make idempotent"),
`PublishDefinitionVersion` (đọc kỹ doc comment riêng của hàm này — phân biệt 2 loại "duplicate": cùng
IdempotencyKey+RequestHash → replay từ receipt, KHÔNG chạm `PublishVersion`; IdempotencyKey khác nhau nhưng
nội dung compile giống hệt → vẫn dispatch `PublishVersion` nhưng `PublishVersion` tự dedupe theo
`CompiledHash` và hàm này chỉ append event khi thực sự là version mới — 2 test riêng trong `commands_test.go`
đã chứng minh cả hai nhánh), `ListVersions`, `LoadVersion`.

Grep + đọc chữ ký `PublishRequest`/`Compile`/`CompileFrom`/`<Kind>Definition` của cả 8 package
(`agentprofile`, `block`, `command`, `engineeringpack`, `gate`, `layer`, `policy`, `skill`) — xác nhận bằng
lệnh grep thật (không đoán) rằng cả 8 kind PERFECTLY UNIFORM: `<Kind>Definition{ID <Kind>DefinitionID, Fields
definition.Fields}`, `PublishRequest{VersionID, VersionNumber, SchemaVersion, Document, Dependencies,
PublishedBy, PublishedAt}`, `CompileFrom(def, rawDocument []byte, format authoring.Format, req PublishRequest)
(definition.VersionFields, error)`. Kind thứ 9 (WORKFLOW) khác cấu trúc: `workflowcompiler.CompileAndResolve`
cần một `ports.UnitOfWork` thật để resolve dependency pin qua registry, nên được truyền như
`WorkflowDefinition`/`WorkflowRequest` riêng thay vì một `Compile` closure.

**Phát hiện quan trọng nhất: `cmd/aw/definition.go` (971 dòng, V2-11) đã là bản triển khai THAM CHIẾU chính
xác cho toàn bộ dispatch 9-kind này** — đọc toàn bộ file trước khi viết bất kỳ dòng dispatch nào của chính
mình. `compileClosureForKind` (9 case gần giống hệt nhau — cố ý, không dùng generic, để type an toàn từng
kind không lẫn lộn), `buildValidateDraftRequest`/`buildPublishRequest`, `loadAnyVersion` (fallback thử shared-
table trước, `store.LoadWorkflowVersion` sau — dùng khi Kind chưa biết trước), và cả `diff` subcommand
(`versionSummaryView`/`diffLineView`/`versionDiffView`/`diffLines`/`prettyJSONLines`, thuật toán LCS chuẩn) —
đúng shape V6-05 cần dựng lại cho HTTP, không phải phát minh từ đầu.

**Phát hiện gap thật số 1 — không có "get one Definition" query ở BẤT KỲ tầng nào.**
`ports.DefinitionsRepository` (đọc hết `internal/app/ports/unitofwork.go` dòng 267-329) chỉ có `LoadVersion`,
`CreateDefinition`, `PublishVersion`, `PublishWorkflowVersion`, `ListVersions`, `GetWorkflowVersion` — không
có cách nào reload lại `Kind`/`Scope`/`Name`/`Status`/generation hiện tại của một Definition đã tồn tại.
`cmd/aw/definition.go`'s riêng `syntheticDraftFields` (dòng 593-607) tự thừa nhận nguyên văn: "This CLI has
no 'get definition' query to load a real Definition's current Status from... StatusDraft is therefore not a
guessed placeholder here; it is the only Status any real Definition row can currently have" — đúng vì không
có `ActivateDefinition`/`ArchiveDefinition` command nào tồn tại. CLI né được gap này vì operator gõ
`--project-id` trực tiếp, tự chịu trách nhiệm đúng scope. HTTP KHÔNG được phép né — task's "Không làm: ...
tin scope từ payload" đòi hỏi chính route phải tự CHỨNG MINH DefinitionID thật sự thuộc scope route đó, không
chỉ tin path/body. Đọc `internal/adapters/sqlite/definitions.go`'s `publishSharedDefinitionVersionTx` xác
nhận thêm: hàm này tự load `status, project_id FROM definitions WHERE id=? AND kind=?` nhưng KHÔNG hề so
sánh với `cmd.Scope` — nếu tầng HTTP không tự kiểm tra trước, một request gọi route
`/projects/wrong-project/definitions/BLOCK/{id}/publish` cho một `{id}` thực chất thuộc project KHÁC (hoặc
global) vẫn sẽ publish thành công, vi phạm thẳng "Không làm" của chính task này.

**Phát hiện gap thật số 2 — không có `Diff` query nào tồn tại** (xác nhận đúng như prompt đã cảnh báo trước,
verify bằng grep thật): `cmd/aw/definition.go`'s `diff` subcommand tự dựng toàn bộ so sánh cục bộ, không gọi
một query application-layer nào — vì không có query nào để gọi.

Đọc `ports.DefinitionsRepository.LoadVersion`'s doc comment xác nhận CHỈ resolve shared-kind Version (8
kind), KHÔNG BAO GIỜ resolve WORKFLOW — WORKFLOW có bảng riêng (`workflow_definitions`/`workflow_versions`).
`GetWorkflowVersion` (V4-02) đã tồn tại làm counterpart Tx-composable cho WORKFLOW. `cmd/aw/definition.go`'s
`loadAnyVersion` (dòng 384-418) đã tự giải quyết đúng vấn đề "biết VersionID nhưng chưa biết Kind" bằng cách
thử shared-table trước, fallback sang `store.LoadWorkflowVersion` khi lỗi CHÍNH XÁC là
`ports.ErrDefinitionVersionNotFound` — không che lỗi khác.

Đọc `internal/domain/authoring/diagnostic.go` toàn bộ — xác nhận `Diagnostic{Line, Column, Path, What, Why,
Fix}` với `Line`/`Column` 1-based, `Path` dot-separated field path — đây chính xác là dữ liệu "location
diagnostics" Verify bullet đòi hỏi, và `block.ValidateDocument`/`skill.ValidateDocument` (grep xác nhận cả 8
kind) đều trả `authoring.Diagnostics` (implement `error` qua value receiver, nên `errors.As(err, &diags)` bắt
được trực tiếp, không cần unwrap qua `fmt.Errorf("%w", ...)`). WORKFLOW không có path này: tài liệu WORKFLOW
decode bằng `json.Decoder` trần (không qua `authoring.DecodeStrict`), lỗi cấu trúc là
`*workflow.ValidationError{Problems []string}` (không Line/Column/Path) hoặc
`*workflowcompiler.ResolutionError`/`*workflowcompiler.AgentRoleValidationError` cùng shape.

Chạy thử thật (không phải đọc code suông) một publish BLOCK có khai `dependencies` (pin tới một
`definitionId` chưa từng tồn tại) — ra `404`, không phải `400`. Điều tra: `publishSharedDefinitionVersionTx`
tự resolve TỪNG pin khai trong `req.Dependencies.Pins` bằng `SELECT project_id FROM definitions WHERE id=?`
thật — dù `block.Compile` (hàm pure, không I/O) không hề đụng registry, tầng REPOSITORY publish vẫn resolve
pin thật. Đây là phát hiện thật sự trong lúc viết test (không phải đọc source suông), sửa fixture của chính
test (seed một Definition `POLICY` thật trước khi publish pin trỏ tới nó) thay vì coi là bug.

### Quyết định

1. **14 route = 7 operation × 2 scope, KHÔNG dùng 9 segment path riêng cho từng Kind.** `{kind}` là một
   `net/http.ServeMux` path wildcard (Go 1.22+, xác nhận `go.mod`'s `go 1.27.0` đủ mới), validate bằng
   `definition.Kind.Valid()` ngay ở tầng route — mirror đúng convention `CreateDefinition`/`PublishVersion`/
   `ListVersions` chính chúng đã dùng `kind` như một parameter runtime, không phải type riêng theo route.
2. **2 route (get-one-version, diff) KHÔNG mang `{kind}`.** `VersionFields.Kind()` đã tự mang thông tin đó
   sau khi resolve — mirror đúng `cmd/aw/definition.go`'s `loadAnyVersion`/diff (cả hai cũng không cần
   `--kind` flag). Route path: `GET /definitions/versions/{versionId}` và
   `GET /definitions/versions/diff?a=&b=` (cộng cặp project-scoped) — literal `"versions"` ở vị trí thứ 2 ưu
   tiên hơn wildcard `{kind}` theo đúng quy tắc "literal thắng wildcard" của `net/http.ServeMux` (Go 1.22
   release notes' chính ví dụ "/posts/latest" vs "/posts/{id}").
3. **Thêm `GetDefinition(ctx, kind, id) (definition.Fields, error)` vào `ports.DefinitionsRepository`** —
   đóng Gap thật #1. Implement cả sqlite (route theo kind giống `CreateDefinition`/`PublishVersion` —
   WORKFLOW đọc `workflow_definitions`, 8 kind kia đọc `definitions` filter `(id, kind)`) lẫn fake (đọc lại
   `definitionRecord`/`workflowDefinitions` map đã có sẵn từ `CreateDefinition`). Một row lưu dưới kind KHÁC
   với kind caller hỏi đọc y hệt "không tồn tại" (`ports.ErrPersistenceNotFound`), không phải lỗi "sai kind"
   riêng — đây chính là cơ chế cho phép Verify "isolated schemas" đúng nghĩa mà không cần logic riêng.
4. **`GetDefinition` này là điểm DUY NHẤT mọi route mutation/query reload target trước khi làm bất cứ gì
   khác** (`authoritative.go`'s `loadDefinitionInScope`/`loadVersionInScope`) — route tự derive scope từ path
   (global prefix hoặc `{projectId}` thật), so với `fields.Scope` reload được; không khớp → cùng response
   `WriteResourceHidden` (404) leakage-normalized, y hệt "does not exist" — không phân biệt được "không tồn
   tại" với "tồn tại nhưng sai scope". Đây là cách đóng chính xác lỗ hổng đã tìm thấy ở Gap thật #1
   (`publishSharedDefinitionVersionTx` tự nó không kiểm tra).
5. **Thêm `LoadAnyVersion` vào `internal/app/definitions` (file mới `queries.go`), KHÔNG sửa
   `cmd/aw/definition.go`'s bản riêng của nó.** Đóng Gap thật #1 phần "get version không cần biết Kind" ở
   TẦNG APPLICATION (không phải CLI-local) để route get-version/diff có một hàm chung để gọi — mirror logic y
   hệt `cmd/aw`'s `loadAnyVersion` (thử `LoadVersion` trước, fallback `tx.Definitions().GetWorkflowVersion`
   khi lỗi chính xác là `ports.ErrDefinitionVersionNotFound`) nhưng là bản ADDITIVE, không refactor code CLI
   đã có — giảm rủi ro diff, không đụng file `cmd/aw` nào ngoài `serve.go`'s dòng wiring.
6. **`validate` KHÔNG bọc `ports.Command`, không đòi `Idempotency-Key`.** Đúng nguyên văn doc comment của
   chính `ValidateDraft`: dry run không có side effect để cần idempotent. Test
   `TestValidateDefinitionDraft_Block_HappyPath` tự xác nhận cơ học: gọi validate xong, list versions vẫn
   rỗng — không route nào trong `validate.go` gọi bất kỳ hàm ghi nào (đóng đúng "Không làm: không persist
   invalid draft thành runtime version" — mở rộng ra: validate không persist BẤT KỲ draft nào, hợp lệ hay
   không).
7. **`create` và `publish` đều CREATE-shaped (đòi `Idempotency-Key`, KHÔNG đòi `If-Match`).** Cả
   `CreateDefinitionRequest` lẫn `PublishDefinitionVersionRequest` đều không có field `ExpectedVersion` —
   publish một Version mới luôn APPEND, không bao giờ overwrite state hiện có, nên không có precondition nào
   để `If-Match` bảo vệ — mirror đúng lý do `CreateChildWorkItem` (V6-04) đã dùng.
8. **Dispatch 9-kind (`dispatch.go`, `document.go`) là bản COPY ĐỘC LẬP của `cmd/aw/definition.go`'s
   `compileClosureForKind`/`buildValidateDraftRequest`/`buildPublishRequest`, không share code.** Đúng lý do
   `internal/app/definitions/commands.go`'s riêng `workflowVersionToVersionFields` đã tự giải thích (đã bị
   duplicate 3 lần trong repo trước task này: `commands.go`, `internal/adapters/sqlite/definitions.go`,
   `cmd/aw/definition.go`): package này không được phép import `cmd/aw` (là `package main`, và ngược hướng
   phụ thuộc dù có thể). Có MỘT cải tiến thật so với bản CLI: `fields` truyền vào mỗi case dispatch là
   `definition.Fields` reload THẬT qua `GetDefinition` (Gap thật #1 vừa đóng), không phải
   `syntheticDraftFields` — với WORKFLOW, `workflowRequestFrom` dùng thẳng `fields.Name`/`Status`/`Version`
   reload thật thay vì để caller tự khai `--name` khớp tay như CLI phải làm.
9. **`versionSummaryView`/`diffLineView`/`versionDiffView`/`diffLines`/`prettyJSONLines` (`diff.go`) là bản
   COPY ĐỘC LẬP THỨ TƯ của cùng ý tưởng đó** — đóng Gap thật #2, giữ cục bộ trong package này thay vì promote
   lên `internal/app/definitions` (đúng lý do commands.go đã cho: đây là response-shaping cho một caller cụ
   thể, không phải query application-layer tái sử dụng được).
10. **Error mapping (`errors.go`) enumerate tường minh từng sentinel thật đọc từ source** (không dùng
    `httpapi.WriteAppError` làm catch-all, vì `internal/app/definitions` không tự trả `*apperror.Error`) —
    `ports.ErrPersistenceNotFound`/`ErrDefinitionVersionNotFound` → `WriteResourceHidden`;
    `ErrReceiptConflict`/`ErrPersistenceAlreadyExists` → 409; `ErrCrossProjectDependency` → 400;
    `authoring.Diagnostics` → 400 với MỘT `ErrorDetail` mỗi `Diagnostic` (Field=`Path`, Message nhúng
    `Line:Column` thật — đây chính là cách đóng Verify "location diagnostics" cụ thể, không chỉ nói suông);
    `*workflow.ValidationError`/`*workflowcompiler.ResolutionError`/`*workflowcompiler.AgentRoleValidationError`
    → 400 với `ErrorDetail` mỗi `Problem` (không Field, vì WORKFLOW không có source position).
11. **Diff enforce "cùng scope" bằng cách check TỪNG operand độc lập với scope của CHÍNH route** (không phải
    so A với B). Mạnh hơn so sánh cặp: nếu A hoặc B thuộc một scope thứ ba khác hẳn route lẫn operand còn
    lại, vẫn bị từ chối — không chỉ khi A≠B. Thêm guard `a.Kind() != b.Kind()` → 400 (không phải leakage,
    Kind không nhạy cảm) — không được spec đòi hỏi trực tiếp nhưng là invariant hợp lý (so sánh SourceHash
    của một BLOCK với một SKILL vô nghĩa).
12. **`maxBodyBytes = 1 MiB`** — giống hệt `workitem`/`catalog` đã chọn, đúng "Phạm vi: bounded text/file
    payload".

### Thực hiện

- `internal/app/ports/unitofwork.go`: `DefinitionsRepository` thêm `GetDefinition(ctx, kind, id)
  (definition.Fields, error)`.
- `internal/adapters/sqlite/definitions.go`: implement `GetDefinition` (route theo kind, WORKFLOW đọc
  `workflow_definitions`, 8 kind kia đọc `definitions` filter `(id,kind)`) + helper
  `scopeFromNullableProjectID`.
- `internal/app/ports/fake/unitofwork.go`: implement `GetDefinition` tương ứng (đọc map `definitions`/
  `workflowDefinitions` đã có).
- `internal/app/definitions/queries.go` (file mới): `GetDefinition` (wrap `tx.Definitions().GetDefinition`
  qua `WithReadOnly`), `LoadAnyVersion` (fallback shared-table → `GetWorkflowVersion`).
- `internal/delivery/httpapi/definitions/` (package mới, 14 file production):
  - `dependencies.go`: `Dependencies{UnitOfWork, IDs, Clock}`.
  - `routes.go`: doc comment đầy đủ + `RegisterRoutes` — 14 `RouteDescriptor`.
  - `envelope.go`: `prepareCommand` (create-shaped, dùng chung cho cả create lẫn publish), `replayOrProceed`,
    `pathKind` (parse+validate `{kind}`), `maxBodyBytes`.
  - `authoritative.go`: `loadDefinitionInScope`, `loadVersionInScope` — điểm DUY NHẤT scope được kiểm tra.
  - `dto.go`: `scopeView`, `definitionView`, `dependencyPinBody`, `versionFieldsView`, `versionListResponse`,
    `emptyBody`, `definitionScopeFromProjectID`, `scopesMatch`.
  - `dispatch.go`: `compileInputs`, `compileClosure` (8-way switch), `decodeWorkflowDocument`,
    `workflowRequestFrom`.
  - `document.go`: `authorDocumentBody`, `parseDocumentFormat`, `buildCandidate` (Kind-selected either/or
    dùng chung bởi validate lẫn publish).
  - `create.go`: `handleCreateDefinition`/`handleCreateProjectDefinition` + `createDefinitionCore`.
  - `validate.go`: `handleValidateDefinitionDraft`/`handleValidateProjectDefinitionDraft` +
    `validateDefinitionDraftCore`.
  - `publish.go`: `handlePublishDefinitionVersion`/`handlePublishProjectDefinitionVersion` +
    `publishDefinitionVersionCore` (tự tính `versionNumber` thật cho WORKFLOW qua `ListVersions`).
  - `detail.go`: `handleGetDefinition`/`handleGetProjectDefinition`,
    `handleListDefinitionVersions`/`handleListProjectDefinitionVersions`.
  - `version.go`: `handleGetDefinitionVersion`/`handleGetProjectDefinitionVersion`.
  - `diff.go`: `handleDiffDefinitionVersions`/`handleDiffProjectDefinitionVersions` + view/diff helper.
  - `errors.go`: `writeQueryError`, `writeCommandError`, `writeDiagnostics`, `writeProblems`,
    `writeValidationError`, `writeReceiptHashConflict`, `writeAppOrInternal`.
- `cmd/aw/serve.go`: thêm import `httpdefinitions "internal/delivery/httpapi/definitions"` + 1 dòng
  `httpdefinitions.RegisterRoutes(routes, httpdefinitions.Dependencies{UnitOfWork: uow, IDs:
  idsource.Random{}, Clock: clock.System{}})`, ngay cạnh `httpapi.RegisterWorkspaceRoutes` đã có — xác nhận
  bằng `grep -n httpdefinitions cmd/aw/serve.go` (2 dòng: import + gọi) NGAY khi vừa thêm, không đợi tới lúc
  hoàn thành mới kiểm tra.

### Test

34 test function mới, real `httpapi.Server` (TCP listener thật, middleware chain thật) + real `*sqlite.Store`
— không mock, mirror đúng `workitem_test.go`'s `newTestEnv` idiom — trải trên 5 file:

- `definitions_test.go`: harness dùng chung (`testEnv`, `do`, `decodeInto`, `seedProject`) + 4 hằng số
  document mẫu (`validBlockDocumentJSON`, `invalidBlockDocumentJSON`, `minimalWorkflowDocumentJSON` — START→
  END thuần, không node AGENT/COMMAND nào nên `collectReferences` trả về rỗng, không cần seed registry fixture
  nào để compile thành công — `invalidWorkflowDocumentJSON`).
- `create_test.go` (7): global happy path, project happy path, thiếu Idempotency-Key, thiếu field, kind sai,
  replay (cùng key → 200, không phải 201 lần 2 — đúng `WriteReceiptReplay`'s status hardcode, y hệt phát
  hiện của V6-04), cùng key khác body → 409.
- `validate_test.go` (11): BLOCK happy path (+ xác nhận KHÔNG version nào được persist), location diagnostics
  (mỗi `ErrorDetail.Field` khác rỗng — chứng minh path thật, không phải message chung chung), definition
  không tồn tại → 404, **isolated schemas** (id tạo dưới BLOCK, gọi validate qua path SKILL cùng id → 404),
  WORKFLOW happy path, WORKFLOW structural problems có detail, WORKFLOW từ chối YAML (không có decode path),
  **3 test negative matrix global/project**: project-scoped qua route global → 404, project A qua route
  project B → 404, global qua route project → 404 (đủ 3 chiều lệch scope có thể xảy ra), thiếu `content` →
  400.
- `publish_test.go` (7): happy path (seed một Definition POLICY thật trước — phát hiện thật ghi ở mục Nghiên
  cứu — rồi xác nhận CẢ 4: `sourceHash`/`compiledHash`/`canonicalSource`/`compiledSnapshot` không rỗng VÀ
  pin khai đúng echo lại y hệt), replay same-key (không version thứ 2), **different-key same-content dedupe**
  (AK-ARCH-005B — vẫn 201 nhưng cùng version id, cùng compiledHash, list versions vẫn chỉ có 1), same-key
  different-body → 409, definition không tồn tại → 404, wrong-scope → 404 (VÀ xác nhận route project đúng
  vẫn publish thành công — chứng minh không phải toàn bộ hệ thống hỏng, chỉ đúng route sai bị chặn), WORKFLOW
  happy path (VersionNumber thật = 1, không phải placeholder).
- `version_diff_test.go` (9): get-version kind-agnostic (không cần biết BLOCK hay gì), wrong-scope → 404,
  version không tồn tại → 404, diff identical (2 definitionId khác nhau, cùng content → `identical=true`,
  mọi diff line đều "equal"), diff different (đổi 1 field → `identical=false`, có ít nhất 1 dòng add/remove),
  **diff cross-scope → 404** (một operand global, một project — đúng Verify "diff operands phải cùng scope"),
  diff cross-kind → 400 (BLOCK vs SKILL), thiếu query param → 400, diff project-scoped happy path.

`go test ./internal/delivery/httpapi/definitions/... -v` — 34/34 PASS, ~35s. Đã chạy lặp lần đầu ra 3 fail
(2 do hiểu sai hành vi `WriteReceiptReplay` — status luôn 200 khi replay, đúng discipline chung của
`httpapi`, không phải bug; 1 do test tự khai pin trỏ tới một Definition chưa từng seed — phát hiện thật, sửa
fixture) — sau khi sửa test (không đụng implementation), chạy lại sạch 100%.

`go build ./...`, `go vet ./...` sạch trên toàn repo. Chạy riêng mọi package bị đụng chạm bởi thay đổi
interface `ports.DefinitionsRepository` (`internal/adapters/sqlite`, `internal/app/ports`,
`internal/app/ports/fake`, `internal/app/definitions`, `internal/archtest`, `cmd/aw`) — xanh 100%, không
regression. `go test ./...` toàn repo (99+ package, chạy nền song song lúc viết report này) — xem mục Kết
quả cho kết quả cuối cùng.

### Verify

- **"isolated schemas"**: `TestValidateDefinitionDraft_IsolatedSchemas` — BLOCK id không thấy được qua path
  SKILL, đúng cơ chế `GetDefinition`'s filter `(id, kind)`.
- **"location diagnostics"**: `TestValidateDefinitionDraft_Block_LocationDiagnostics` — mỗi `ErrorDetail`
  mang `Path` thật (`Field`) và `Line:Column` thật (nhúng trong `Message`) từ `authoring.Diagnostic`, không
  phải một message chung chung duy nhất.
- **"replay"**: `TestCreateDefinition_Replay_...`, `TestPublishDefinitionVersion_Replay_SameIdempotencyKey_NoNewVersion`
  — cả 2 xác nhận replay không tạo bản ghi thứ hai.
- **"version/diff"**: `TestGetDefinitionVersion_*` (3 test), `TestDiffDefinitionVersions_Identical`/`_Different`
  — cả identical lẫn khác biệt đều đúng `CompiledHash`-based (AK-ARCH-005B), không phải so sánh
  `SourceHash`.
- **"global/project negative matrix"**: đủ 3 chiều trong `validate_test.go` (project-qua-global,
  project-A-qua-project-B, global-qua-project) cộng `TestPublishDefinitionVersion_WrongScope_404`,
  `TestGetDefinitionVersion_WrongScope_404`, `TestDiffDefinitionVersions_CrossScope_404` — mọi route
  mutation/query đều có ít nhất 1 test wrong-scope.
- **"route derives scope; không tin scope từ payload"**: `createDefinitionBody`/`authorDocumentBody` không
  có field `scope`/`projectId` nào — scope luôn tới từ path qua `definitionScopeFromProjectID`/route handler
  riêng (global vs project), chưa từng đọc từ JSON body; `loadDefinitionInScope`/`loadVersionInScope` là nơi
  DUY NHẤT confirm scope trước khi dispatch bất kỳ mutation nào.
- **"publish trả source/compiled hash và exact pins"**: `TestPublishDefinitionVersion_Block_HappyPath` assert
  cả 4 field cộng pin echo chính xác.
- **"diff operands phải cùng scope"**: `TestDiffDefinitionVersions_CrossScope_404`.
- **"Hoàn thành khi: UI/CLI author→validate→publish→inspect không seed SQLite"**: mọi handler dispatch đúng 1
  trong 5 hàm public có sẵn của `internal/app/definitions` (`CreateDefinition`/`ValidateDraft`/
  `PublishDefinitionVersion`/`ListVersions`/`LoadVersion`) hoặc 2 hàm mới cùng tầng
  (`GetDefinition`/`LoadAnyVersion`) — không route nào tự ghi `tx.Definitions()` trực tiếp;
  `internal/archtest/command_envelope_test.go`'s `TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt` tự động
  quét đệ quy vào package mới này (đã xanh khi chạy `go test ./internal/archtest/...`), y hệt cách V6-04 đã
  xác nhận archtest có sẵn tự bao phủ subpackage mới mà không cần sửa.

`go test ./...` full suite (99+ package) chạy 1 lần: 2 fail — `TestPool_ShutdownGraceExceeded_ReturnsErrAndEscalatesCancellation`
(`internal/app/workerpool`) và `TestV5AcceptFullComposition_RealFourRoleGraphReachesSucceededAndSurvivesRestart`
(`internal/integration/v5accept`). Cả hai đều NẰM NGOÀI diff của task này (diff chỉ chạm
`internal/app/ports`, `internal/adapters/sqlite/definitions.go`, `internal/app/ports/fake/unitofwork.go`,
`internal/app/definitions/queries.go`, `internal/delivery/httpapi/definitions/`, `cmd/aw/serve.go` — không
đụng `internal/app/workerpool` hay bất kỳ thứ gì thuộc scheduler/workflow-run engine). Chạy lại riêng từng
test: `TestPool_ShutdownGraceExceeded_...` PASS 3/3 lần chạy độc lập (`-count=3`), 0.3s mỗi lần — timing/grace-
period test dưới tải contention của việc chạy song song 99+ package, không phải regression thật.
`TestV5AcceptFullComposition_...` PASS khi chạy riêng (9.4s) — deadline-based acceptance test (chờ Run đạt
`VERIFYING` trong một khung thời gian cố định) cũng nhạy CPU contention khi chạy cùng lúc toàn bộ suite. Cả
hai xác nhận là flake môi trường/timing, không liên quan tới thay đổi của task này — không sửa gì thêm.

### Kết quả

PR #46 (`feat/v6-05-definition-authoring-endpoints`, branch từ `origin/master` tại `ab4ee48`). 25 file mới/
sửa (+2988 dòng): 1 method mới trên `ports.DefinitionsRepository` (`GetDefinition`, 2 implementation —
sqlite + fake), 1 file query application-layer mới (`internal/app/definitions/queries.go` — `GetDefinition` +
`LoadAnyVersion`), 1 package HTTP hoàn toàn mới (`internal/delivery/httpapi/definitions`, 14 file production
+ 5 file test), 1 dòng wiring thật vào `cmd/aw/serve.go` (xác nhận bằng grep, không chỉ giả định). 14 route
HTTP thật lần đầu
tồn tại: create/validate/publish/detail/list-versions (×2 scope, mang `{kind}`) cộng get-version/diff (×2
scope, kind-agnostic). Test mới: 34 test function (real `httpapi.Server` + real sqlite). `go build/vet ./...`
sạch. `go test ./...` toàn repo: 2 fail, cả hai đã xác nhận bằng thực nghiệm (chạy lại riêng, PASS) là flake
môi trường nằm ngoài diff — không phải regression từ task này. Gap "không có get-one-Definition query" và
"không có Diff query" (cả hai xác nhận thật, không phải đoán) đã đóng bằng `GetDefinition`/`LoadAnyVersion` +
`diff.go`'s view/thuật toán riêng.

## V6-06A — Approval và typed WAIT decision endpoints

### Bối cảnh

V6-06A nằm trong cùng nhóm P2 với `V6-06` (`run`, `POST /work-items/{id}/runs` + `POST /runs/{id}/cancel`) —
4 dependency (`V6-00`, `V6-01A`, `V6-02`, `V6-02A`) đều đã merge từ trước. `git log origin/master --oneline -3`
lúc branch ra: `ab4ee48 feat(v6-04)...`, `36629f3 feat(v6-06)...`, `92ccac8 feat(v6-10b)...` — `V6-06` (task chị
em gần nhất, cùng khu vực Run-scoped HTTP) đã merge làm `feat/v6-06-run-start-cancel`, dùng làm tiền lệ cấu
trúc chính cho task này thay vì tự nghĩ lại từ đầu. Branch `feat/v6-06a-approval-wait-endpoints` tạo thẳng từ
`origin/master` (`ab4ee48`) — `git merge-base HEAD origin/master` = `ab4ee48`, xác nhận không cần rebase.
`baocaov6checklist.md` đang bị 3 task khác ghi song song cùng lúc (`V6-05`, `V6-06D`, `V6-07`) — append-only,
dự kiến conflict khi mở PR, đúng cảnh báo trong system prompt, không phải lỗi của task này.

### Nghiên cứu

Đọc nguyên văn spec (`docs/design/08-v6-api-projections.md` dòng 251-260): *"Mục tiêu: expose approve/reject
và typed WAIT signal tách khỏi free-text conversation."* / *"Phạm vi: approval decisions, WAIT signals và
`DecisionArtifact` references."* / *"Không làm: không map message text thành control và không nhận actor/roles
từ payload."* / *"Thực hiện: reload target/scope, enforce role, unique signal identity and expected version."*
/ *"Verify: unauthorized role, spoof, concurrent decision, duplicate signal, stale/cross-project target."*

Đọc `internal/app/runtime/approval.go` và `wait.go` toàn văn TRƯỚC khi viết handler, kể cả doc comment ở đầu
file (đúng yêu cầu review của chính task):

- `approval.go` dòng 19-28: package doc comment tự ghi rõ đây là thiết kế đã CHỐT với user từ trước (không
  phải việc task này tự quyết): `ports.Command` có field `ActorRoles []string` — transport/API layer tự tạo từ
  local session, KHÔNG BAO GIỜ decode từ request body, KHÔNG thuộc `RequestHash`. `ResolveApproval` check giao
  chính xác/case-sensitive giữa `cmd.ActorRoles` và `ApprovalRequest.AuthorizedRoles`; rỗng → `apperror` với
  `errorcode.CodePolicyDenied`, reject TRƯỚC KHI chạm state của request (kể cả khi đã DECIDED).
- `wait.go` dòng 9-19: package doc comment ghi rõ thiết kế 2 lớp CỐ Ý tách biệt: `cmd.IdempotencyKey`/
  `RequestHash` (bảo vệ một lần gọi HTTP/command) tách hẳn khỏi `SignalKey` (định danh sự kiện thật ngoài đời
  do caller tự cấp — bảo vệ CÙNG một sự kiện thật báo qua HAI command invocation khác nhau). Không được gộp 2
  khái niệm này lại.
- `ResolveApprovalRequest{RunID, ApprovalRequestID, Outcome, Reason}` và `SignalWaitRequest{RunID,
  WaitRegistrationID, SignalKey, Payload}` — CẢ HAI đều KHÔNG có field `ExpectedVersion` nào (khác hẳn giả định
  ban đầu là "expected version" trong spec nghĩa là app command nhận version từ HTTP) — cả hai đều tự load
  state bên trong transaction rồi CAS bằng version tự vừa đọc (`TransitionApprovalRequestRequest`/
  `TransitionWaitRegistrationRequest`).

Đọc `internal/domain/runtime/approval.go`/`wait.go` — `ApprovalRequest` có field `ProjectID`/`RunID`/
`AuthorizedRoles`/`Version` sẵn, không có field `ExpectedVersion` để client match; `WaitRegistration` có
`ProjectID`/`RunID`/`Version` nhưng **không có field role/AuthorizedRoles nào cả** — phát hiện quan trọng: WAIT
không có khái niệm "role" để enforce, chỉ APPROVAL mới có.

Đọc `docs/design/11-v6-00-ux-artifact.md` — tìm đúng 2 operationId đã khoá cứng: dòng 311, `resolveApproval`
("`ResolveApproval`... field `Outcome` là vocabulary do node khai báo — không phải generic status — UI/CLI
trình bày thành hai nút/leaf riêng cho cùng MỘT command với `Outcome` khác nhau, không phải hai authority");
dòng 312, `submitWaitSignal` ("`SubmitWaitSignal` [CHƯA CÓ — tiền thân nội bộ: `RecordWaitSignal` +
`NewWaitSignal` đã có, chưa có public command bọc ngoài]"). Dòng này của design doc hơi cũ (viết trước khi
`internal/app/runtime/wait.go`'s `SignalWait` — chính public command đó — được hoàn thiện ở V4-08); đối chiếu
trực tiếp source xác nhận `SignalWait` ĐÃ tồn tại đầy đủ, nên "submitWaitSignal" ở đây chỉ là operationId HTTP
bọc `SignalWait` thật, không phải một command application-layer mới cần viết.

Đọc lại `internal/delivery/httpapi/run` (task chị em gần nhất) làm khuôn cấu trúc: `run.go` (Dependencies +
RegisterRoutes), `start.go`/`cancel.go` (2 handler khác cách wrap hẳn nhau, lý do ghi trong chính doc comment
của `cancel.go`: `CancelRunRequest` không có `IdempotencyKey`/`ExpectedVersion` field nào — không đòi `If-Match`
vì không có gì để so; `StartWorkflowRunHandler` theo đủ flow CommandEnvelope), `errors.go` (2 hàm
`writeXxxError` phân loại sentinel thật).

Đọc `internal/delivery/httpapi/workitem/{envelope.go, scope_expansion_commands.go}` (task `V6-04`, cũng đã
merge) — tìm thấy MỘT tiền lệ khác hẳn `cancel.go`: `prepareUpdateCommand` đòi `Idempotency-Key` VÀ `If-Match`
dù bản thân `ApproveScopeExpansion`/`RejectScopeExpansion`/`WithdrawScopeExpansion` (application layer) cũng
KHÔNG nhận `ExpectedVersion` — precondition `detail.Version != expectedVersion` được so sánh Ở TẦNG HTTP, dựa
trên MỘT lần reload sẵn có (`loadScopeExpansionRequestForUpdate`), độc lập với CAS nội bộ thật của command. Đây
chính là mẫu áp dụng được cho `resolveApproval` (con người xem UI rồi quyết định — hợp lý để đòi `If-Match`).

Đọc `internal/app/ports/command.go` dòng 69-95 — xác nhận `ports.Command` có field `ExpectedVersion uint64`
CHUNG cho MỌI command trong repo (không riêng gì Approval/WAIT) — dùng để fold vào `SemanticHash` (chống replay
sai), KHÔNG nhất thiết được chính application command đọc lại (giống hệt cách `workitem`'s
`ApproveScopeExpansion` cũng bỏ qua nó).

Tìm `DecisionArtifact` (`internal/domain/runtime/decision.go`, `RecordDecisionArtifact`/`GetDecisionArtifact`
trên `Tx.Runtime()`) — grep toàn bộ `NewDecisionArtifact(` trong `internal/app/runtime`: CHỈ xuất hiện ở
`recovery_reaper.go`, `admission.go`, `completion_policy.go`, `schedule.go`, `resolve_work_item_blocker.go` —
**`approval.go` và `wait.go` KHÔNG BAO GIỜ gọi `RecordDecisionArtifact`**. Đọc lại `docs/design/06-v4-runtime-engine.md`
dòng 335-336 (V4-09's own "Một câu hỏi chốt với user") xác nhận: yêu cầu audit của HE-08-M08 ("MUST ghi actor,
cause, previous/new state, time") với APPROVAL được thoả mãn NGAY TRÊN chính row `ApprovalRequest`
(`DecidedBy`/`DecidedRole`/`DecidedOutcome`/`Reason`/`DecidedAt`), không cần một `DecisionArtifact` row riêng.
Kết luận: "DecisionArtifact references" trong Phạm vi của V6-06A là surface đúng cái đã có (ID/State của chính
`ApprovalRequest`/`WaitRegistration`), KHÔNG phải tự thêm logic ghi `DecisionArtifact` mới vào application layer
— việc đó sẽ vi phạm "handler chỉ dispatch" và lấn sang sửa file lõi đã merge/test kỹ ngoài phạm vi 1 task HTTP.

### Quyết định

1. **Một package `decision` bọc CẢ HAI endpoint**, không tách 2 package riêng — mirror đúng cách `V6-06`'s
   `run` bọc chung `start`+`cancel` (2 route cùng một "Run control" chủ đề), ở đây là 2 route cùng chủ đề
   "typed human decision tách khỏi chat". `RegisterRoutes` đăng ký đúng 2 descriptor với `OperationID` khoá
   cứng từ design doc: `resolveApproval`, `submitWaitSignal`.
2. **Route path**: `POST /runs/{runId}/approval-requests/{approvalRequestId}/resolve` và
   `POST /runs/{runId}/wait-registrations/{waitRegistrationId}/signal` — nested dưới `/runs/{runId}` (mirror
   `run`'s `/runs/{runId}/cancel`) vì cả `ResolveApprovalRequest`/`SignalWaitRequest` đều nhận `RunID` làm tham
   số bắt buộc để cross-check "approval request/wait registration thật sự thuộc run này" (chính là "stale/
   cross-project target" Verify bullet) — path phải mang cả 2 ID để handler tự reload đúng target trước khi
   check bất cứ gì khác.
3. **KHÔNG tách 2 route `/approve` + `/reject` riêng cho approval** (khác hẳn `workitem`'s 3 route
   `approveScopeExpansion`/`rejectScopeExpansion`/`withdrawScopeExpansion`) — đúng ngôn ngữ chính xác của
   design doc dòng 311: `Outcome` là "vocabulary do node khai báo — không phải generic status", "hai nút/leaf
   riêng cho CÙNG MỘT command với Outcome khác nhau, KHÔNG PHẢI hai authority". Một node có thể khai nhiều hơn
   2 outcome (approved/rejected/escalated trong chính `approvalDocument` test fixture) — chỉ MỘT route với
   field `outcome` trong body mới đúng cho vocabulary mở, không phải enum đóng approve/reject.
4. **`resolveApproval` ĐÒI `If-Match`; `submitWaitSignal` THÌ KHÔNG** — quyết định quan trọng nhất, và là một
   phát hiện thật giữa chừng (xem mục Test, bug #1). Ban đầu áp `If-Match` đồng loạt cho cả 2 route theo đúng
   "Thực hiện: ... expected version" của spec + tiền lệ `workitem`. Viết xong mới nhận ra: `submitWaitSignal`
   đại diện một sự kiện NGOÀI ĐỜI (vd CI webhook) được báo qua, không phải người dùng vừa xem lại UI — một lần
   gửi lại hợp lệ (retry của webhook) không có cách nào biết "version hiện tại" của `WaitRegistration` sau khi
   lần gửi TRƯỚC đó đã (hoặc chưa) tiêu thụ nó, nên đòi `If-Match` đúng sẽ PHÁ chính cái idempotent-by-SignalKey
   mà `SignalWait`'s doc comment tồn tại để đảm bảo (một duplicate hợp lệ sẽ bị 412 thay vì Won=false/200 êm
   đẹp). `resolveApproval` thì khác: một operator xem UI rồi bấm nút chắc chắn vừa load version hiện tại — giữ
   `If-Match` ở đây đúng tinh thần `workitem`'s `ApproveScopeExpansion`. Ghi toàn bộ lý luận này thành doc
   comment dài trong `decision.go` (đối chiếu trực tiếp với đoạn `run/cancel.go` đã ghi lý do ngược lại), không
   chỉ ở báo cáo này.
5. **`loadApprovalRequestForUpdate`/`loadWaitRegistrationForRun`**: mỗi handler tự reload target (qua
   `tx.Approvals().GetApprovalRequest`/`tx.Wait().GetWaitRegistration`) TRƯỚC KHI đọc Idempotency-Key/If-Match/
   body — vừa suy ra `ProjectID` (scope) thật từ chính row đó (không tin client), vừa check `RunID` khớp path
   hay không, fold cả "không tồn tại" lẫn "thuộc run khác" vào CÙNG một `httpapi.WriteResourceHidden` 404 —
   đúng leakage-normalization policy V6-02A (mirror trực tiếp `workitem`'s `writeQueryError`/`writeCommandError`
   đã fold `ErrPersistenceNotFound`/`ErrScopeMismatch` giống hệt vậy).
6. **`ActorRoles` CHỈ lấy từ `httpapi.PrincipalFromContext`, KHÔNG BAO GIỜ có field actor/roles nào trong
   `ResolveApprovalBody`/`SubmitWaitSignalBody`** — đúng "Không làm: không nhận actor/roles từ payload".
   `httpapi.DecodeJSON`'s `DisallowUnknownFields()` (đã có sẵn từ V6-01) tự động biến một field lạ kiểu
   `"actorRoles":[...]` trong body thành lỗi 400 thay vì bị âm thầm bỏ qua — đây chính là cơ chế chặn "spoof"
   ở tầng transport, không cần tự viết thêm logic riêng.
7. **`SubmitWaitSignalHandler` không enforce role nào** — vì `WaitRegistration` (domain) không có field
   `AuthorizedRoles`, khác hẳn `ApprovalRequest`. `cmd.ActorRoles` vẫn được điền từ principal (nhất quán với
   MỌI command khác trong repo) nhưng `SignalWait` tự nó không bao giờ đọc field đó — ghi rõ lý do trong doc
   comment `wait.go`, không lặng lẽ bỏ qua khiến người đọc sau tưởng thiếu sót.
8. **Hash payload fold thêm ID lấy từ path** (`resolveApprovalHashPayload{ApprovalRequestID,...}`,
   `submitWaitSignalHashPayload{WaitRegistrationID,...}`) — mirror trực tiếp lý do `run/start.go`'s
   `startRunHashPayload` đã ghi: `SemanticHash` hash theo `(commandType, scope, payload, expectedVersion)` —
   `scope` chỉ là cả PROJECT; thiếu ID path thì 2 target khác nhau cùng project, trùng Idempotency-Key + cùng
   outcome/signalKey, sẽ hash giống hệt nhau → cross-replay sai target.
9. **`ErrNodeRunMismatch` (lỗi thật `ResolveApproval`/`SignalWait` có thể trả) được map vào `WriteResourceHidden`
   giống hệt "not found"** — vì handler đã tự reload+check RunID TRƯỚC khi dispatch nên nhánh này trong
   `errors.go` chỉ còn là phòng thủ cho race hiếm (giữa lần reload của handler và transaction thật của command);
   giữ cùng shape leakage-normalized thay vì một status khác biệt.
10. **`ResolveApprovalResponse`/`SubmitWaitSignalResponse` embed thẳng `runtime.ResolveApprovalResult`/
    `SignalWaitResult`** (đã có sẵn json tag camelCase) + `ValidActions` rỗng — mirror `run`'s
    `StartRunResponse` (embed thẳng vì có tag sẵn) chứ không tự map field như `run`'s `CancelRunResponse` (phải
    tự map vì `CancelRunResult` không có tag). `ValidActions` luôn rỗng: một khi đã DECIDED/ESCALATED/CONSUMED/
    TIMED_OUT/ELAPSED thì không còn action mutation nào trong tập đóng của task này để quảng cáo, và một quyết
    định thua race (Won=false) không phải là action của CHÍNH caller đó để retry — nó đã có kết quả thật rồi.

### Thực hiện

- `internal/delivery/httpapi/decision/decision.go`: `Dependencies{UOW, IDs}`, `RegisterRoutes(routes, deps)` —
  đăng ký 2 route `resolveApproval`/`submitWaitSignal`; doc comment dài giải thích toàn bộ quyết định 1-4 ở
  trên ngay trong code.
- `internal/delivery/httpapi/decision/approval.go`: `ResolveApprovalBody`, `resolveApprovalHashPayload` (nội
  bộ), `ResolveApprovalResponse`, `ResolveApprovalHandler(deps) http.HandlerFunc`,
  `loadApprovalRequestForUpdate` — đòi `Idempotency-Key` + `If-Match`.
- `internal/delivery/httpapi/decision/wait.go`: `SubmitWaitSignalBody`, `submitWaitSignalHashPayload`,
  `SubmitWaitSignalResponse`, `SubmitWaitSignalHandler(deps) http.HandlerFunc`, `loadWaitRegistrationForRun` —
  chỉ đòi `Idempotency-Key`, KHÔNG đòi `If-Match` (quyết định 4).
- `internal/delivery/httpapi/decision/errors.go`: `writeResolveApprovalError`, `writeSignalWaitError` — phân
  loại sentinel thật (`runtime.ErrNodeRunMismatch`, `ports.ErrPersistenceNotFound`, `ports.ErrReceiptConflict`)
  sang đúng status/code; mọi `*apperror.Error` khác (`CodePolicyDenied`, `CodeIdempotencyConflict`, ...) rơi
  đúng vào `httpapi.WriteAppError`'s `StatusForAppErrorCode` có sẵn, không cần tự map lại.
- `cmd/aw/serve.go`: thêm 1 import (`"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/decision"`)
  + 1 lời gọi `decision.RegisterRoutes(routes, decision.Dependencies{UOW: uow, IDs: idsource.Random{}})` ngay
  sau điểm `run`'s wiring — diff `cmd/aw/serve.go` chỉ +5 dòng (xem `git diff origin/master -- cmd/aw/serve.go`).
- Không migration mới — kiểm `git log origin/master --oneline -3` + `ls internal/adapters/sqlite/migrations`
  ngay trước khi hoàn tất: highest hiện tại vẫn `0037_release_set_local_commits.sql`, không đụng tới (đúng dự
  đoán ban đầu: `approval_requests`/`wait_registrations`/`wait_signals` schema đã có sẵn từ V4-08/V4-09).

### Test

Toàn bộ test dùng `sqlite.Open`/`sqlite.NewUnitOfWork` thật, dispatch qua đúng `runtime.StartWorkflowRun` +
`runtime.AdvanceRun` + `runtime.ResolveApproval`/`SignalWait` thật để đưa một Run tới đúng node APPROVAL/WAIT
rồi mới gọi HTTP handler — không có chỗ nào tự ghi thẳng DB để giả kết quả.

- `fixture_test.go`: mirror `run/fixture_test.go` (`seedProject`/`seedActiveRepository`/`stubProvider`/
  `readyWorkItemFixture`+`readyWorkItemFixtureInExistingProject`/`publishWorkflowVersionDocument`) cộng
  `approvalDocument`/`waitSignalDocument` mirror ĐÚNG 2 fixture cùng tên trong `internal/app/runtime/{approval_test.go,
  wait_test.go}` (start -> gate(APPROVAL, AuthorizedRoles=["reviewer"]) -> end_approved|end_rejected|
  end_escalated; start -> pause(WAIT, SIGNAL, SignalName="ci-passed") -> end_resumed|end_expired), cộng
  `approvalRequestFixture`/`waitRegistrationFixture` (+ 2 biến thể `...InProject`) tự lái Run thật từ START tới
  đúng node qua `StartWorkflowRun` + `AdvanceRun` thật.
- `httptest_helper_test.go`: `newTestServer(t, uow, ids, principal)` — khác `run`'s bản gốc ở chỗ nhận
  `principal` làm tham số thay vì hardcode 1 principal cố định, vì package này cần CẢ 2 principal (`reviewer`
  và `operator`) để test "unauthorized role"/"spoof".
- `approval_test.go` (10 test): `..._Approve_Success_RoutesAndReturnsWon`,
  `..._UnauthorizedRole_RejectsBeforeTouchingState` (**unauthorized role**),
  `..._Spoof_UnknownActorFieldRejected400` (**spoof**), `..._StaleCrossRunTarget_404` (**stale/cross-project
  target**), `..._UnknownApprovalRequestID_404`, `..._MissingIdempotencyKey_400`, `..._MissingIfMatch_400`,
  `..._StaleIfMatch_PreconditionFailed`, `..._DuplicateIdempotencyKey_Replays`,
  `..._ConcurrentDecisions_ExactlyOneWins` (**concurrent decision**).
- `wait_test.go` (7 test): `..._Success_RoutesAndReturnsWon`,
  `..._DuplicateSignalKey_IdempotentNoDoubleProcessing` (**duplicate signal**),
  `..._SameSignalKeyDifferentPayload_IdempotencyConflict`, `..._StaleCrossRunTarget_404` (**stale/cross-project
  target**), `..._MissingSignalKey_400`, `..._MissingIdempotencyKey_400`,
  `..._ConcurrentDifferentSignals_ExactlyOneWins` (**concurrent decision**, áp cho WAIT).

**2 phát hiện thật giữa chừng (không phải giả định trước, lộ ra khi thiết kế/chạy test thật):**

1. **Thiết kế lại `submitWaitSignal` để KHÔNG đòi `If-Match`** (đã nêu ở Quyết định 4) — bắt nguồn từ chính lúc
   thiết kế test "duplicate signal": nếu `submitWaitSignal` đòi `If-Match` như `resolveApproval`, thì để viết
   một test "gửi lại đúng SignalKey lần 2, kỳ vọng Won=false/200" cần biết TRƯỚC version MỚI sau khi lần 1 đã
   CONSUME registration — một client thật (webhook retry) không có cách nào biết trước con số đó, nghĩa là chính
   thiết kế "đòi If-Match cho WAIT" sẽ làm test case chuẩn nhất của `SignalWait`'s own idempotent contract
   KHÔNG THỂ xảy ra qua HTTP như mô tả. Đây là tín hiệu thiết kế sai thật (phát hiện qua việc thử viết test
   trước khi implement handler, không phải suy luận trừu tượng), dẫn tới quyết định 4 ở trên — không phải một
   lựa chọn tuỳ ý.
2. **Test "concurrent decision" ban đầu định assert cứng status HTTP của TỪNG racer** (200 cho winner, 200 cho
   loser) — chạy thử với `If-Match` cố định `"1"` cho cả 5 goroutine thì phát hiện: một racer có thể hợp lệ
   nhận 412 (nếu chính lần reload/precondition-check CỦA RIÊNG racer đó tình cờ chạy SAU khi người thắng đã
   commit, version đã nhảy 1→2) thay vì 200/Won=false — đây KHÔNG phải bug, mà đúng là hệ quả tự nhiên của
   quyết định 4 (precondition kiểm ở tầng HTTP tách biệt CAS nội bộ thật của command, xem doc comment
   `decision.go`). Sửa test để assert đúng bất biến thật sự quan trọng thay vì status cố định: mọi response
   phải là 200/409/412 (không bao giờ 500), đúng 1 response có `Won=true`, và `ApprovalRequest` cuối cùng DECIDED
   đúng 1 lần — chạy `-count=20` cả 2 test concurrency (approval lẫn wait) để xác nhận hết flaky sau khi sửa.

- `go build ./...`, `go vet ./...` sạch trên toàn bộ repo (không giới hạn package thay đổi).
- `go test ./internal/delivery/httpapi/decision/... -v`: 17/17 PASS (~5.9s); `-count=20` riêng 2 test
  concurrency: ổn định 20/20 cả hai.
- `go test ./internal/delivery/httpapi/... ./cmd/... -v`: toàn bộ pass, gồm cả `run`/`workitem`/`catalog`
  (xác nhận wiring `serve.go` không phá route nào có sẵn) và `cmd/aw`'s `TestServe_*` (chứng minh server thật
  build/serve được với route mới).
- Race detector (`-race`) không chạy được trên máy dev (Windows, thiếu cgo — `modernc.org/sqlite` pure-Go nên
  build/test thường không cần cgo, nhưng chính runtime `-race` instrumentation thì cần) — để CI (Linux) tự chạy,
  đúng hard rule #7 (không tự claim theo dõi CI); rà lại thủ công mọi biến shared giữa goroutine trong 2 test
  concurrency đều nằm sau `mu.Lock()`, kể cả lời gọi `t.Errorf` (an toàn gọi từ goroutine khác theo đúng tài
  liệu `testing.T`, chỉ `FailNow`/`Fatal*` mới bắt buộc chạy trên goroutine test chính — 2 test này tránh hẳn
  `Fatal*` bên trong goroutine, mirror đúng `run/start_test.go`'s `doStartRunRaw` pattern).
- Chạy thêm `go test ./...` toàn bộ repo (99 package) một lần để soát regression ngoài phạm vi trực tiếp: 2
  fail duy nhất, cùng package `internal/integration/v5accept`
  (`TestV5AcceptConformanceMatrix`, `TestV5AcceptFullComposition_RealFourRoleGraphReachesSucceededAndSurvivesRestart`),
  cùng một triệu chứng *"run v5a-7 did not reach state VERIFYING within the deadline"* — ĐÚNG hệt loại
  contention flake mà `## V6-06`'s own narrative ở trên đã từng gặp và xác minh theo cách y hệt. Xác minh lại
  bằng 2 bước: (1) `git diff origin/master --stat -- internal/app/runtime internal/integration
  internal/adapters/sqlite internal/domain` rỗng — nhánh này không đụng một dòng nào trong chuỗi phụ thuộc của
  package đang fail; (2) `go test ./internal/integration/v5accept/... -run
  "TestV5AcceptConformanceMatrix|TestV5AcceptFullComposition..." -v` chạy riêng, không contention từ suite
  `internal/adapters/sqlite` (289s) chạy song song — cả 2 test pass sạch (17.17s + 16.45s). Kết luận: contention
  flake môi trường, không phải regression từ thay đổi của V6-06A. Không có test nào trong danh sách "known CI
  flake" của brief (`TestSPK09...`, `TestSupervisorNormalExit...`, `TestProjectWorkspaceGate`,
  `TestRecoveryReaperHandler_...`, `TestWriteLeaseRaceHasOneWinnerForSameRepository`) xuất hiện trong lần chạy
  này.

### Verify

- **unauthorized role**: `TestResolveApproval_HTTP_UnauthorizedRole_RejectsBeforeTouchingState` — actor role
  `operator` (không giao `AuthorizedRoles=["reviewer"]`) nhận 403 FORBIDDEN, `ApprovalRequest` sau đó vẫn
  PENDING@version 1, không `DecidedBy` — đúng "reject TRƯỚC KHI chạm state" của chính `ResolveApproval`'s doc
  comment.
- **spoof**: `TestResolveApproval_HTTP_Spoof_UnknownActorFieldRejected400` — body cố nhét `"actorRoles":
  ["reviewer"]` bị 400 INVALID_REQUEST (unknown field), không bao giờ chạm tới bước authorization — chứng minh
  body cấu trúc không có chỗ nào để spoof actor/role, không phải chỉ "role check đúng" mà còn "không có field
  nào để thử".
- **concurrent decision**: `TestResolveApproval_HTTP_ConcurrentDecisions_ExactlyOneWins` (5 goroutine thật, real
  sqlite, cùng `ApprovalRequestID`) và `TestSubmitWaitSignal_HTTP_ConcurrentDifferentSignals_ExactlyOneWins` (5
  `SignalKey` khác nhau đua cùng 1 `WaitRegistration`) — cả 2 đều xác nhận đúng 1 `Won=true`, không bao giờ 500,
  state cuối cùng DECIDED/CONSUMED đúng 1 lần; ổn định qua `-count=20`.
- **duplicate signal**: `TestSubmitWaitSignal_HTTP_DuplicateSignalKey_IdempotentNoDoubleProcessing` — cùng
  `SignalKey`, 2 `Idempotency-Key` KHÁC nhau (đúng kịch bản `SignalWait`'s doc comment: 2 command invocation
  khác nhau báo cùng 1 sự kiện thật) — lần 2 vẫn 200 nhưng `Won=false`, không route lần 2.
- **stale/cross-project target**: `TestResolveApproval_HTTP_StaleCrossRunTarget_404` và
  `TestSubmitWaitSignal_HTTP_StaleCrossRunTarget_404` — ID thật nhưng thuộc Run KHÁC trong cùng project, path
  đặt RunID của Run kia → 404 NOT_FOUND, y hệt shape "không tồn tại" (không phân biệt được, đúng leakage
  policy).
- **"Hoàn thành khi": mọi human decision có typed audited route độc lập với chat** — cả 2 route đều KHÔNG có
  field message/text tự do nào trong body (`ResolveApprovalBody{Outcome, Reason}`,
  `SubmitWaitSignalBody{SignalKey, Payload}` — `Payload` là JSON có cấu trúc caller tự định nghĩa, không phải
  chat text được diễn giải thành quyết định); mỗi quyết định thành công đều để lại audit thật trên chính row
  `ApprovalRequest` (`DecidedBy`/`DecidedRole`/`DecidedOutcome`/`Reason`/`DecidedAt`) hoặc `WaitRegistration`
  (`ConsumedSignalID` trỏ tới `WaitSignal` bất biến) — chính là "DecisionArtifact reference" mà Phạm vi nhắc
  tới, không cần một bảng `decision_artifacts` row mới.

### Kết quả

8 file mới trong `internal/delivery/httpapi/decision/` (4 production: `decision.go`/`approval.go`/`wait.go`/
`errors.go`, ~624 dòng; 4 test: `fixture_test.go`/`httptest_helper_test.go`/`approval_test.go`/`wait_test.go`,
~1064 dòng, 17 test case) + `cmd/aw/serve.go` sửa +5 dòng. `go build/vet ./...` sạch trên toàn bộ repo; toàn
bộ test package mới + `cmd/aw` + `internal/delivery/httpapi` (cha) pass 100%; 2 fail duy nhất khi chạy
`go test ./...` toàn repo đã xác minh là contention flake môi trường không liên quan (`internal/integration/
v5accept`, pass sạch khi chạy riêng). Không migration mới, không sửa `internal/app/runtime`/`internal/app/ports`/
`internal/domain/runtime` — toàn bộ thay đổi nằm gọn trong tầng delivery + 1 điểm nối additive vào composition
root.

`POST /runs/{runId}/approval-requests/{approvalRequestId}/resolve` (operationId `resolveApproval`) và
`POST /runs/{runId}/wait-registrations/{waitRegistrationId}/signal` (operationId `submitWaitSignal`) giờ là 2
route thật, chạy được qua `aw serve` thật — mọi quyết định APPROVAL/WAIT giờ có đường đi typed, audited, tách
biệt hoàn toàn khỏi free-text conversation, và không thể bị giả mạo actor/role qua request body.

## V6-06D — Recovery command endpoints

### Bối cảnh

V6-06D nằm trong nhóm P2, phụ thuộc `V6-00, V6-01A, V6-02, V6-02A, V4-12C, V5-08D` — cả 6 đã merge trên
`origin/master` (`ab4ee48 feat(v6-04)...`) lúc bắt đầu; `git merge-base --is-ancestor origin/master HEAD` xác
nhận worktree này bắt đầu đúng từ tip, không cần rebase. `baocaov6checklist.md` đang bị 3 task song song khác
ghi (`V6-05`, `V6-06A`, `V6-07`, theo đúng cảnh báo trong system prompt) — append-only, dự kiến conflict khi
merge, không phải bug.

Task này khác 2 task WorkItem/Run-mutation trước đó (`V6-04`, `V6-06`) ở một điểm quan trọng: cả ba command
đích (`RetryBlockedActivation`, `CancelWorkItem`, `ResolveWorkItemBlocker`) đều **đã tồn tại sẵn** trong
`internal/app/runtime` (V4-12C, V5-08D) — task này thuần tuý là bọc transport, không viết thêm business logic
nào. Cái khó thật của task không nằm ở phần HTTP (lặp lại đúng khuôn `run`/`workitem` đã có), mà nằm ở 2 chỗ
task tự nêu rõ ngay từ đầu: (1) `RetryBlockedActivationHandler` cần 2 dependency (`ports.IsolationEnforcementChecker`,
`*agentregistry.Registry`) mà **chưa từng có** chỗ nào trong `cmd/aw/serve.go` construct thật — xác nhận bằng
`grep -rn "NewRetryBlockedActivationHandler\|agentregistry.New\|process.NewIsolationChecker" internal/ cmd/`
trước khi viết bất cứ gì: chỉ có `cmd/aw/adapter.go` (CLI `adapter probe/register`, dùng CLI flag
`--executable` riêng cho từng lần gọi) và test fixture dùng 2 thứ này, chưa có `aw serve` nào; (2) không có
sqlite-backed test fixture nào trong TOÀN BỘ codebase (kể cả ở `internal/app/runtime` chính chủ) từng dựng một
NodeRun thật sự BLOCKED bởi admission — `internal/app/runtime`'s own admission tests (`admission_test.go`)
100% chạy trên `*fake.UnitOfWork`, xác nhận bằng `grep -rln "sqlite.Open\|sqlite.NewUnitOfWork"
internal/app/runtime/*_test.go` không khớp `admission_test.go`/`retry_blocked_activation.go`'s own test file
nào (thực ra file test riêng cho `retry_blocked_activation.go` không tồn tại — coverage của nó nằm hết trong
`admission_test.go`).

### Nghiên cứu

Đọc nguyên văn spec (`docs/design/08-v6-api-projections.md` dòng 285-295): *"Mục tiêu: transport cho
`RetryBlockedActivation`, `CancelWorkItem`, `ResolveWorkItemBlocker`. Phạm vi: đúng ba command và route/
descriptor riêng. Không làm: không gọi internal recovery worker hoặc tự sửa blocker/Run state. Thực hiện: mỗi
route dispatch một public command; resolve open blocker hợp lệ, resolved/waived replay no-op theo core;
cancellation active Run trả quiesce state. Verify: precondition matrix, resolution mode, replay/concurrency, no
duplicate activation and import test."*

Đọc kỹ cả 3 file command thật trước khi viết bất kỳ dòng handler nào:

- `internal/app/runtime/retry_blocked_activation.go` — `RetryBlockedActivationHandler.Retry(ctx, req)
  (RetryBlockedActivationResult, error)`, KHÁC 2 command còn lại: đây là **method trên struct**, không phải
  free function, vì cần giữ 2 dependency thật (`isolation`, `agents`) xuyên suốt lifetime của handler chứ không
  nhận qua tham số mỗi lần gọi. Đọc trọn package doc comment (dòng 1-37): thiết kế two-phase
  preflight-rồi-serialized-write y hệt `admitOrClaimRunning` gốc (Phase 1 đọc + I/O thật ngoài transaction,
  Phase 2 CAS trong `WithSerializedWrite`, re-verify lại từ đầu để đóng TOCTOU) — retry **re-probe đúng
  AdapterBuildID đã pin, không bao giờ repin sang build mới** ("không repin Run" — dòng 28-37 giải thích rõ:
  nếu build cũ vẫn drift thì retry thất bại mãi mãi, đúng chủ đích, không phải bug). Một revalidation thất bại
  là `RetryBlockedActivationResult` với `FailureReason`/`FailureDetail` — KHÔNG BAO GIỜ là `error` Go. Không
  nhận `ports.Command`/idempotency-key: idempotent theo chính `blocker.State` (giống hệt `CancelRun` — đọc
  `cancel_run.go`'s doc comment do chính `V6-06` để lại để xác nhận đây là pattern lặp lại có chủ đích, không
  phải thiếu sót).
- `internal/app/runtime/cancel_work_item.go` — `CancelWorkItem(ctx, uow, ids, req) (CancelWorkItemResult,
  error)`, free function, idempotent theo `WorkItemID` qua `WorkItemCancellationIntent` durable riêng (mirror
  `CancelRun`'s `RunCancellationIntent`). Đọc trọn package doc comment: transaction của nó (1) ghi intent, (2)
  lặp qua MỌI Run chưa terminal của WorkItem và gọi `cancelRunTx` (hàm nội bộ `cancel_run.go` export sẵn cho
  đúng mục đích này — "extracted from that file specifically so this command can compose it inside its OWN
  transaction"), (3) `reconcileWorkItemCancellationTx` đóng WorkItem về `CANCELLED` NGAY nếu không còn Run nào
  đang quiescing — nghĩa là handler HTTP không được tự suy đoán `Status` cuối cùng, phải trả đúng field
  `Status` mà `CancelWorkItem` tự quan sát.
- `internal/app/runtime/resolve_work_item_blocker.go` — `ResolveWorkItemBlocker(ctx, uow, req)
  (ResolveWorkItemBlockerResult, error)`, free function, KHÔNG nhận `ids idsource.Source` (BlockerID do caller
  cung cấp, DecisionArtifact của nhánh WAIVED tự sinh ID xác định từ `blockerID+"-waiver"`, không mint ID mới).
  Đọc trọn package doc comment: bảng đóng `BlockerType × ResolutionMode` — `SCOPE_EXPANSION_REQUIRED` từ chối
  CẢ HAI mode vô điều kiện (`ErrBlockerNotResolvableViaCommand`); WAIVED chỉ hợp lệ cho `RUN_CANCELLED`/
  `COMPLETION_POLICY_FAILED` (`ErrBlockerNotWaivable` cho mọi type khác); `Mode` KHÔNG có mặc định
  (`ErrResolutionModeRequired`); WAIVED bắt buộc `PolicyGrantRef` (`ErrWaiveRequiresPolicyGrant`); 2
  precondition riêng của chính command (không thuộc bảng type/mode): không Run nào của WorkItem còn non-terminal
  (`ErrWorkItemHasNonTerminalRun`), không repository workspace nào trong family đang `QUARANTINED`
  (`ErrWorkspaceQuarantined`).

Đọc `internal/app/agentregistry/registry.go` (`Registry.Resolve` khoá theo `ports.ProviderKey` — CHỈ MỘT
executor mỗi provider, `New` từ chối đăng ký trùng `ErrDuplicateProvider`) và `internal/adapters/process/
isolation.go` (`IsolationChecker` — pure static, không I/O, chỉ chấp nhận `OPERATOR_TRUSTED_LOCAL`, luôn từ
chối `ENFORCED_ISOLATED` — comment gốc: "OPERATOR_TRUSTED_LOCAL is the only tier this environment can run
today... confirmed with the user"). Đọc `internal/app/runtime/admission.go`'s `runAdmissionProbePhase` (dòng
100-155) thấy điểm mấu chốt: `agents.Resolve(provider, ...)` chỉ khớp theo `ProviderKey`, RỒI
`adapterbuild.VerifyNoDrift(ctx, executor, pinnedBuild)` gọi thẳng `executor.Capabilities(ctx)` — nghĩa là
executor đăng ký trong registry PHẢI được construct đúng `ExecutablePath` của build đã pin, KHÔNG có cơ chế nào
tự động chọn đường dẫn theo từng build. Đọc `internal/adapters/providers/claude/claude.go`'s
`Capabilities(ctx)` (dòng 86-112) xác nhận: hàm này **spawn thật** `<executable> --version` mỗi lần gọi (không
cache, không giả lập) — và `agentregistry.New(ctx, executors...)` tự nó gọi `Capabilities(ctx)` MỘT LẦN cho mỗi
executor ngay lúc construct để đo capability thật. Đây là phát hiện quan trọng nhất buộc phải quyết định kỹ
composition-root: nếu `cmd/aw/serve.go` mặc định tự đăng ký cả `claude`/`codex` (kiểu path mặc định "claude"/
"codex" trên PATH), `aw serve` sẽ **fail khởi động** trên bất kỳ máy nào không cài sẵn 2 CLI đó — gần như chắc
chắn đúng với máy CI/dev bình thường không có 2 binary ngoài này.

Đọc `cmd/aw/adapter.go`'s `newAgentExecutor(providerKey, executablePath)` (dòng 244-261) — xác nhận đây là
CÁCH DUY NHẤT construct executor thật hiện có trong toàn repo (`claude.New(process.NewSupervisor(),
claude.Config{Executable: path})` / `codex.New(...)` tương tự), luôn nhận `--executable` tường minh từ operator,
không bao giờ mặc định ngầm.

Đọc `docs/design/11-v6-00-ux-artifact.md` xác nhận operationId khoá cứng: `retryBlockedActivation` (dòng 359,
CLI leaf `aw node-run retry-blocked`, "retry thất bại KHÔNG được sinh thêm blocked activation mới trong view —
Verify V7-12"), `cancelWorkItem` (dòng 305, CLI leaf `aw work-item cancel`), và `ResolveWorkItemBlocker` (dòng
306/361, không có operationId literal ghi sẵn nhưng xác nhận CÙNG MỘT authority dùng chung giữa Screen 7 (chỉ
summary+link) và Screen 8 (invocation thật) — "hai bề mặt UI chia sẻ ĐÚNG MỘT command mỗi loại, không phải hai
owner").

Đọc `internal/archtest/run_control_test.go` (mẫu AST-scan `TestRunControlHTTPNeverReachesSchedulerOrWorker`)
làm khuôn cho architecture test riêng của task này.

Grep `internal/app/runtime/completion.go` xác nhận `RUN_CANCELLED` là loại `WorkItemBlocker` DUY NHẤT trong 7
loại đã có sẵn producer thật (`openRunCancelledBlockerTx`, gọi từ `transitionRunToCancelledTx`) — mở khi một Run
bị `CancelRun` (không phải `CancelWorkItem`) đưa tới CANCELLED thật. Đây chính là con đường rẻ nhất để dựng một
`WorkItemBlocker` OPEN thật cho test `ResolveWorkItemBlocker` mà không cần đụng tới admission/AGENT node.

### Quyết định

1. **Route path**: `POST /node-runs/{nodeRunId}/retry-blocked-activation`, `POST /work-items/{workItemId}/cancel`,
   `POST /work-item-blockers/{blockerId}/resolve` — flat, một-ID-một-resource, KHÔNG project-prefix. Lý do:
   `V6-06` đã lập tiền lệ y hệt cho `startWorkflowRun`/`cancelRun` (`/work-items/{id}/runs`, `/runs/{id}/cancel`
   — không `/projects/{id}/...` dù `workitem` package's CRUD routes CÓ prefix) — vì cả 5 command này (2 của
   V6-06 + 3 của V6-06D) đều KHÔNG nhận `ProjectID` trong request struct của chính application layer, nên
   không có gì để một path segment `{projectId}` xác nhận/dùng tới; mọi authorization vẫn tự reload target
   thật bên trong chính command, không qua path.
2. **KHÔNG dùng CommandEnvelope (V6-02) cho cả 3 route** — quyết định lặp lại lý do `V6-06`'s `cancel.go` đã
   ghi cho `CancelRun`, áp dụng cho cả 3 command ở đây vì cả 3 đều idempotent theo chính domain state của nó
   (blocker State / WorkItemCancellationIntent), không có `IdempotencyKey`/`ExpectedVersion` field nào trên
   request struct application layer. Bắt buộc `Idempotency-Key` rồi validate-nhưng-không-dùng sẽ nói dối về
   contract thật — giữ nguyên tinh thần "không giả, không rào cản thừa" của `V6-06`.
3. **Isolation checker: `process.NewIsolationChecker()` unconditional, không cần flag** — nó pure/static/
   I/O-free (đọc lại `internal/adapters/process/isolation.go` xác nhận), không có lý do gì để cấu hình được.
4. **Agent registry: KHÔNG mặc định đăng ký `claude`/`codex`; thêm 2 flag mới `--claude-executable`/
   `--codex-executable` (mặc định rỗng = KHÔNG đăng ký provider đó)** — đây là quyết định kiến trúc quan trọng
   nhất của task, dựa thẳng trên phát hiện ở phần Nghiên cứu (`agentregistry.New` spawn thật để đo capability).
   3 lựa chọn đã cân nhắc:
   - (a) Mặc định đăng ký cả 2 executor với path mặc định "claude"/"codex" (PATH lookup) — **loại bỏ**: làm
     `aw serve` tự sập trên mọi máy không cài sẵn 2 CLI đó, kể cả CI của chính repo này (chưa từng thấy binary
     `claude`/`codex` nào được cài trong pipeline CI hiện tại) — vi phạm nguyên tắc cơ bản nhất của một
     composition root: khởi động binary chính không được phụ thuộc silently vào phần mềm ngoài không khai báo.
   - (b) Luôn dùng `agentregistry.Empty()`, không bao giờ cho operator đăng ký executor thật qua `aw serve` —
     **loại bỏ**: khiến `RetryBlockedActivation` không BAO GIỜ retry thành công thật được trên bất kỳ triển
     khai production nào (mọi lần retry một NodeRun pin `AGENT` node sẽ luôn `ErrUnknownProvider`) — vi phạm
     thẳng "Hoàn thành khi: Attempt/WorkItem blocked có named recovery action GỌI ĐƯỢC qua API" của chính task.
   - (c) **Chọn**: 2 flag optional mirror đúng `aw adapter probe/register`'s `--executable` discipline đã có
     sẵn — operator tường minh khai đường dẫn thật cho từng provider mình thật sự có; mặc định rỗng → registry
     rỗng (`agentregistry.Empty()`), đúng "documented-safe zero-executor default" mà `execute.go`'s own
     `NewExecuteNodeHandler` doc comment đã tự xác nhận từ trước ("a caller with no real adapter builds ever
     pinned can safely pass agentregistry.New(ctx) with zero executors registered"). `aw serve` không bao giờ
     tự sập vì thiếu CLI ngoài; một `RetryBlockedActivation` gặp build pin provider chưa cấu hình thất bại
     ĐÚNG với 503 (quyết định 8 dưới đây), một tín hiệu rõ ràng "cấu hình thiếu", khác hẳn 500 (bug) hay một
     conflict/not-found giả tạo. `agentregistry.New(ctx, executors...)` tự nó là I/O thật (spawn `--version`)
     nên nếu operator BẤT CẨN khai một executable path hỏng, `aw serve` fail khởi động NGAY LẬP TỨC (giống hệt
     triết lý "fail closed at boot" mà `aw adapter probe/register` đã dùng cho chính construction pattern này)
     — không âm thầm nhận request rồi mới lỗi mù mờ về sau.
5. **Không tự validate lại `Mode`/`PolicyGrantRef`-khi-WAIVED ở tầng HTTP cho `ResolveWorkItemBlocker`** — cả
   `ErrResolutionModeRequired` và `ErrWaiveRequiresPolicyGrant` đều là sentinel `error` EXPORT sẵn từ
   `internal/app/runtime`, nên handler chỉ cần decode thẳng rồi map qua `errors.Is` — không lặp lại logic domain
   ở tầng delivery, đúng "handler chỉ dispatch" (khác với field `Reason` — validate cục bộ ở cả 3 route vì lỗi
   thiếu `Reason` bên trong application layer là `errors.New("runtime: Reason is required")` TRẦN, không phải
   sentinel export được, nếu không tự chặn trước sẽ rơi vào nhánh 500 mặc định).
6. **Status code**: `retryBlockedActivation` → 202 Accepted đồng nhất cho cả 3 outcome (`Retried`/
   `AlreadyRetried`/còn `FailureReason`) — lý lẽ giống `CancelRun`'s "202 luôn": khi `Retried=true` một job
   `ScheduleNodeRunJobKind` mới thật sự được enqueue bất đồng bộ (kết quả cuối cùng chưa biết ngay lúc response
   được viết), nên cả 3 nhánh đều là "yêu cầu đã được xử lý và trả kết quả QUAN SÁT ĐƯỢC ngay bây giờ", không
   phải một trạng thái cuối tự bịa. `cancelWorkItem` → 202 Accepted (mirror y hệt `cancelRun`, vì bên trong nó
   chính là compose lại `cancelRunTx`). `resolveWorkItemBlocker` → 200 OK (khác 2 route kia: không bao giờ
   enqueue thêm job nào, toàn bộ CAS blocker + WorkItem Status nằm gọn trong MỘT transaction — mirror
   `approveScopeExpansion`/`rejectScopeExpansion` của `V6-04`, cùng lý do).
7. **Response DTO tự định nghĩa field JSON riêng, không serialize thẳng `runtime.*Result`** — same lý do
   `V6-06`'s quyết định 7 đã ghi (các struct `*Result` của `internal/app/runtime` không có json tag, serialize
   thẳng ra PascalCase sai convention camelCase toàn hệ thống).
8. **Phân loại lỗi (`errors.go`) tự viết riêng trong package, không sửa `internal/app/runtime`** — same lý do
   `V6-06`'s quyết định 8. Thêm 1 case mới chưa từng có tiền lệ: `agentregistry.ErrUnknownProvider`/
   `ErrMissingCapability` → 503 Service Unavailable (`ErrorCodeUnavailable`), không phải 409/500 — đây là điều
   kiện triển khai/cấu hình (provider chưa được operator khai ở composition root), khác hẳn một business
   conflict (409, trạng thái domain đã đổi) hay một lỗi thật không mong đợi (500) — client/operator cần biết
   "retry sau khi server được cấu hình lại/khởi động lại", không phải "yêu cầu này vốn sai" hay "có bug".
9. **`ErrWorkspaceQuarantined` route qua `httpapi.StatusForAppErrorCode(errorcode.CodeWorkspaceQuarantined)`
   thay vì hardcode status/code riêng** — mirror đúng `workitem/errors.go`'s `writePreconditionFailed` pattern,
   giữ nhất quán với bảng chung (423 Locked) nếu sau này bảng đó đổi.
10. **Architecture test riêng, mở rộng danh sách selector cấm so với `TestRunControlHTTPNeverReachesSchedulerOrWorker`
    gốc** — thêm `TransitionNodeRun`/`TransitionExecutionAttempt`/`TransitionWorkflowRunState`/
    `TransitionWorkItemStatus` (các CAS primitive `ports.RuntimeRepository`/`ports.WorkRepository` — nếu package
    này từng gọi thẳng bất kỳ selector nào trong số đó, nghĩa là nó đã tự resolve/tự sửa blocker/Run/WorkItem
    state thay vì tin tưởng đúng MỘT command thật mỗi route — đúng nghĩa đen "Không làm: không tự sửa blocker/
    Run state" của chính task).
11. **Test HTTP thật qua `httptest.Server` + `http.ServeMux` thật**, cùng khuôn `newTestServer` của `run`/
    `workitem`, có thêm biến thể `newTestServerWithDeps` để mỗi test tự kiểm soát chính xác `Isolation`/`Agents`
    (2 dependency chỉ `RetryBlockedActivation` cần).

### Thực hiện

- `internal/delivery/httpapi/recovery/recovery.go`: package doc, `Dependencies{UOW, IDs, Isolation, Agents}`,
  `RegisterRoutes(routes, deps)` — 3 descriptor với `OperationID` khoá cứng.
- `internal/delivery/httpapi/recovery/retry.go`: `RetryBlockedActivationRequest{Reason}`,
  `RetryBlockedActivationResponse`, `RetryBlockedActivationHTTPHandler(deps)` — construct
  `runtime.NewRetryBlockedActivationHandler(deps.UOW, deps.IDs, deps.Isolation, deps.Agents)` MỘT LẦN lúc
  `RegisterRoutes` chạy (không construct lại mỗi request).
- `internal/delivery/httpapi/recovery/cancelworkitem.go`: `CancelWorkItemRequest{Reason}`,
  `CancelWorkItemResponse`, `CancelWorkItemHTTPHandler(deps)`.
- `internal/delivery/httpapi/recovery/resolveblocker.go`: `ResolveWorkItemBlockerRequest{Mode, Reason,
  PolicyGrantRef}`, `ResolveWorkItemBlockerResponse`, `ResolveWorkItemBlockerHTTPHandler(deps)`.
- `internal/delivery/httpapi/recovery/errors.go`: `writeRetryBlockedActivationError`,
  `writeCancelWorkItemError`, `writeResolveWorkItemBlockerError` — map đúng từng sentinel thật đọc được ở
  phần Nghiên cứu, message an toàn tự viết lại (không trả `err.Error()` trần).
- `internal/archtest/recovery_http_test.go`:
  `TestRecoveryHTTPNeverReachesSchedulerOrWorkerOrMutatesStateDirectly`.
- `cmd/aw/serve.go` (75 dòng thêm, không sửa dòng nào có sẵn): 2 flag mới `--claude-executable`/
  `--codex-executable`; construct `agentExecutors []ports.AgentExecutor` có điều kiện theo flag, gọi
  `agentregistry.New(ctx, agentExecutors...)` (fail khởi động rõ ràng nếu lỗi); `isolationChecker :=
  process.NewIsolationChecker()` unconditional; 1 lời gọi `recoveryhttp.RegisterRoutes(routes,
  recoveryhttp.Dependencies{UOW: uow, IDs: idsource.Random{}, Isolation: isolationChecker, Agents:
  agentRegistry})` đúng tại điểm đánh dấu sẵn từ `V6-10B`. Xác nhận bằng `grep -n "recoveryhttp\|RegisterRoutes(routes"
  cmd/aw/serve.go` sau khi viết xong — đúng yêu cầu review bắt buộc của chính task, không chỉ tin `go build`
  sạch (bài học `V6-04` để lại: 30 test xanh cho một package KHÔNG BAO GIỜ được gọi từ `aw serve` thật).
- Không migration mới — `internal/adapters/sqlite/migrations/` cao nhất vẫn `0037_release_set_local_commits.sql`
  trên `origin/master` lúc bắt đầu, task này không cần schema mới, xác nhận bằng `ls` trực tiếp trước khi kết
  thúc.

### Test

Toàn bộ 23 test dùng sqlite thật, không mock/fake nào cho persistence, dispatch qua đúng
`RetryBlockedActivationHandler.Retry`/`runtime.CancelWorkItem`/`runtime.ResolveWorkItemBlocker` thật.

- `fixture_test.go`: mirror `run/fixture_test.go` (`readyWorkItemFixture`, `workflowDocumentV1`,
  `publishTestWorkflowVersion`, `stubProvider`) + fixture riêng của package này: `startedRunFixture`
  (`runtime.StartWorkflowRun` thật, không qua HTTP — package này không sở hữu route start),
  `runCancelledBlockerFixture` (xem bug #1 dưới), `startSecondRunForWorkItem`, `claimJobOfKind` (generic, dùng
  chung cho cả `CANCEL_RUN_COORDINATOR` lẫn `EXECUTE_NODE` job).
- `admission_fixture_test.go`: mirror TOÀN BỘ chuỗi fixture AGENT-node của `internal/app/runtime`
  (`schedule_test.go`'s `agentExecutableDocument`/`validAgentProfileDocument`/`attemptPolicyDocument`/
  `permissionPolicyDocument`/`publishAgentProfileVersion(Only)`/`publishPolicyVersion`/
  `fullyResolvablePolicyRefs`/`seedEffectiveScope`, `advance_test.go`'s `publishWorkflowVersionDocument`,
  `admission_test.go`'s `writeAdmissionExecutable`/`admissionPinnedBuild`) — không import được (Go test helper
  không export chéo package), duplicate đúng nguyên văn theo discipline đã lập từ `V6-06`. Thêm
  `toggleIsolationChecker` (đếm số lần gọi, N lần đầu fail rồi pass mãi — chỉ trục ISOLATION trong 4 trục
  admission là trục DUY NHẤT có thể lật từ fail sang pass hợp lệ giữa lần block gốc và lần retry, vì 3 trục
  còn lại (drift/capability/multi-repo-write) đều chỉ phụ thuộc pin bất biến mà chính `RetryBlockedActivation`
  cam kết "không repin" — xem phần Nghiên cứu) và `blockedAdmissionFixture` (dựng NodeRun BLOCKED thật qua
  toàn bộ pipeline thật: `StartWorkflowRun` → `AdvanceRun` → `ScheduleExecutableNodeRun` → claim job
  `EXECUTE_NODE` thật (`store.ClaimJob`) → `runtime.NewExecuteNodeHandler(...).Handle` thật).
- `cancelworkitem_test.go` (6 test): zero-runs-immediately-cancelled, active-run-quiesces-rather-than-fakes,
  twice-already-requested, unknown-work-item, missing-reason, concurrent-race-exactly-one-fresh (6 goroutine
  thật).
- `resolveblocker_test.go` (9 test): resolved-unblocks, waived-records-decision-artifact,
  waived-without-policy-grant-400, missing-mode-400, missing-reason-400, unknown-blocker-404,
  non-terminal-run-409, twice-already-resolved, concurrent-race-exactly-one-fresh (6 goroutine thật).
- `retry_test.go` (8 test): unknown-node-run-404, missing-reason-400, node-run-not-blocked-409,
  revalidation-still-fails-202-with-failure-reason (+ retry lần 2 vẫn y hệt, không sinh blocked activation
  mới), success-reactivates-202 (+ retry lần 2 sau đó là `AlreadyRetried`), concurrent-race-no-duplicate-activation
  (6 goroutine thật, đúng 1 `Retried=true`/1 `ReactivatedNodeRunID` duy nhất), run-not-retryable-409,
  provider-not-configured-503 (dùng `agentregistry.Empty()` + `fake.IsolationEnforcementChecker{}` cho CHÍNH
  server test, khác hẳn registry/isolation đã dùng lúc dựng fixture — chứng minh đúng gap composition-root).

**3 phát hiện thật trong lúc viết/chạy test (không phải giả định trước):**

1. **Kỳ vọng sai ban đầu của chính tôi khi viết `runCancelledBlockerFixture`**: gọi `runtime.CancelRun(...)`
   trực tiếp rồi tưởng `ListWorkItemBlockersForWorkItem` sẽ thấy ngay 1 blocker `RUN_CANCELLED` — chạy thật ra
   0 blocker. Đọc lại `cancel_run.go`'s doc comment (đã đọc ở `V6-06` cho task trước nhưng lúc đó không cần
   soi kỹ phần này): `CancelRun` **chỉ** chuyển Run sang `CANCELLING` và enqueue job
   `CANCEL_RUN_COORDINATOR` — chính job đó (qua `CancelRunCoordinatorHandler.Handle`, chạy async trong
   production) mới thật sự gọi `transitionRunToCancelledTx` (nơi `openRunCancelledBlockerTx` mở blocker). Sửa
   fixture: claim job đó thật (`claimJobOfKind(t, store, runtime.CancelRunCoordinatorJobKind, 5)`) rồi
   `runtime.NewCancelRunCoordinatorHandler(uow, ids).Handle(ctx, job)` thật trước khi assert bất cứ gì — không
   có blocker nào được "giả lập", chỉ là thiếu một bước xử lý job thật trong fixture.
2. **`TestResolveWorkItemBlocker_HTTP_NonTerminalRun_ReturnsConflict` cần một WorkItem có 2 Run (1 CANCELLED, 1
   còn active) cùng lúc** — nhưng sau `StartWorkflowRun` lần đầu, WorkItem đã chuyển `READY→ACTIVE` (chính
   `StartWorkflowRun`'s doc comment tự xác nhận đây là caller ĐẦU TIÊN và DUY NHẤT của nửa transition đó), nên
   không thể `StartWorkflowRun` lần 2 cho cùng WorkItem (`ErrWorkItemNotReady`). Không có command thật nào
   trong codebase này đưa WorkItem trở lại READY sau khi Run của nó quiesce (V5-11's CompletionPolicy thật
   chưa tồn tại) — viết `startSecondRunForWorkItem` tự poke `TransitionWorkItemStatus` thẳng về READY, đúng
   discipline "poke the primitive a future command would otherwise reach" mà chính `readyWorkItemFixtureInExistingProject`
   (từ `V6-06`) và `run`'s `TestCancelRun_HTTP_AlreadyTerminalRun_ReturnsConflict` (force SUCCEEDED thẳng) đã
   lập tiền lệ — không đụng tới field nào của `WorkItemBlocker` đang test, chỉ dựng lại đúng precondition.
3. **Không có sqlite fixture BLOCKED admission sẵn ở bất kỳ đâu** (xem mục Bối cảnh) — đầu tư dựng
   `blockedAdmissionFixture` mới hoàn toàn (mirror `internal/app/runtime`'s fake-backed
   `admissionFixture`/`scheduleFixture` + sqlite-backed `sqliteExecutionFixture`/`claimExecuteNodeJob`, ghép
   lại thành một bản sqlite-thật cho đúng 1 trục ISOLATION). Cân nhắc bỏ qua test
   `ErrNotAnAdmissionBlockerReason` (blocker BLOCKED nhưng vì `SCOPE_EXPANSION_REQUIRED` thay vì 1 trong 4 lý
   do admission) — dựng fixture đó cần thêm một luồng khác hẳn (scope-expansion tự nhiên phát sinh từ một
   COMMAND node đòi hỏi scope vượt quá EffectiveScope hiện có, chưa có fixture rẻ nào sẵn cho việc đó) — chi
   phí không tương xứng với phạm vi task ("HTTP transport", không phải "chứng minh lại mọi nhánh của
   `evaluateAdmission`" — nhánh đó đã được chứng minh đầy đủ ở tầng application layer của chính `V5-08D`).
   Quyết định: KHÔNG dựng fixture riêng cho case này, giữ lại error mapping trong code (đã có, dùng đúng
   sentinel `runtime.ErrNotAnAdmissionBlockerReason` export sẵn) nhưng không có test HTTP riêng phủ nó — ghi
   rõ gap này ở đây thay vì giả vờ đã test đủ.

- `go build ./...`, `go vet ./...` sạch trên toàn bộ package.
- `go test ./internal/delivery/httpapi/recovery/... -v`: 23/23 PASS (~7-19s tuỳ máy, dao động do
  `blockedAdmissionFixture` mở nhiều sqlite DB + claim job thật).
- `go test ./internal/archtest/... -run TestRecoveryHTTP -v`: PASS.
- `go test ./...` (toàn bộ 86 package): chạy 2 lần độc lập để chắc chắn — lần 1 qua `tee` (77 dòng `ok`, 0
  dòng `FAIL`), lần 2 redirect thẳng ra file log riêng, `EXIT=0`, xác nhận lại đúng 77/0. Không gặp lại flake
  contention nào từng thấy ở `V6-06`/`V6-03` (`internal/integration/v5accept` chạy sạch 76.2s trong cùng lượt
  chạy chung, không tách riêng).

### Verify

- **precondition matrix**: mọi sentinel nêu trong phần Nghiên cứu đều có test map đúng status —
  `ports.ErrPersistenceNotFound`→404 (cả 3 route), `ErrNodeRunNotBlocked`/`ErrNotAnAdmissionBlockerReason`
  (mapping có, chưa có test riêng cho nhánh 2 — xem phát hiện #3)/`ErrRunNotRetryable`/
  `ErrBlockerNotResolvableViaCommand`/`ErrBlockerNotWaivable`/`ErrWorkItemHasNonTerminalRun`/
  `ErrWorkItemAlreadyTerminal`→409, `ErrResolutionModeRequired`/`ErrWaiveRequiresPolicyGrant`→400,
  `ErrWorkspaceQuarantined`→423 (map qua bảng chung, chưa có test HTTP riêng — cùng lý do chi phí fixture như
  phát hiện #3, cần thêm 1 repository workspace `QUARANTINED` thật), `agentregistry.ErrUnknownProvider`→503
  (test riêng).
- **resolution mode**: `TestResolveWorkItemBlocker_HTTP_Resolved_*`/`..._Waived_*` chứng minh cả 2 nhánh trên
  cùng 1 blocker `RUN_CANCELLED` thật (Waivable+ResolvableViaCommand theo đúng bảng đóng).
- **replay/concurrency**: cả 3 route có test "gọi 2 lần" (idempotent no-op, không lỗi) VÀ test "N goroutine
  thật cùng lúc" (`ConcurrentCancelRace`/`ConcurrentResolveRace`/`ConcurrentRetryRace` — mỗi test 6 goroutine
  thật qua `httptest.Server` + `http.Client` thật vào real sqlite, đúng 1 outcome "fresh" mỗi lần).
- **no duplicate activation**: `TestRetryBlockedActivation_HTTP_ConcurrentRetryRace_NoDuplicateActivation` —
  đúng 1 `Retried=true`/1 `ReactivatedNodeRunID` phân biệt trong số 6 racer, phần còn lại `AlreadyRetried=true`.
- **import test**: `TestRecoveryHTTPNeverReachesSchedulerOrWorkerOrMutatesStateDirectly` (AST-scan thật) —
  cấm `Handle`/`AdvanceRun`/`FinalizeExecutionAttempt`/4 selector Transition* — package `recovery` không gọi
  bất kỳ selector nào trong danh sách đó.
- **"Hoàn thành khi": Attempt/WorkItem blocked có named recovery action gọi được qua API thật** — xác nhận
  bằng `grep` trực tiếp `cmd/aw/serve.go` (không chỉ tin `go build`), cộng
  `TestRetryBlockedActivation_HTTP_Success_ReactivatesNodeRun` chạy end-to-end qua đúng `RegisterRoutes`
  handler thật (không gọi thẳng `runtime.NewRetryBlockedActivationHandler` bỏ qua tầng HTTP).

### Kết quả

Package mới `internal/delivery/httpapi/recovery` (5 file production ~553 dòng: `recovery.go`/`retry.go`/
`cancelworkitem.go`/`resolveblocker.go`/`errors.go`; 6 file test ~1689 dòng, 23 test case), 1 architecture test
mới (`internal/archtest/recovery_http_test.go`), `cmd/aw/serve.go` +75 dòng (2 flag mới +
agentregistry/isolation wiring + 1 `RegisterRoutes` call). `go build/vet ./...` sạch. 3 route HTTP thật lần
đầu tồn tại: `POST /node-runs/{nodeRunId}/retry-blocked-activation`, `POST /work-items/{workItemId}/cancel`,
`POST /work-item-blockers/{blockerId}/resolve` — chạy được qua `aw serve` thật, xác nhận bằng grep trực tiếp
`cmd/aw/serve.go`, không chỉ tin test package cô lập (đúng bài học `V6-04` để lại).

Quyết định composition-root quan trọng nhất: KHÔNG mặc định đăng ký `claude`/`codex` executor thật vào
`agentregistry.Registry` của `aw serve` — thêm 2 flag `--claude-executable`/`--codex-executable` optional,
mặc định để trống (registry rỗng, documented-safe theo chính `execute.go`), tránh `aw serve` tự sập khi máy
không cài 2 CLI ngoài đó, đồng thời cho operator triển khai thật một đường mở thật để dùng
`RetryBlockedActivation` cho AGENT node. Một retry gặp provider chưa cấu hình thất bại đúng 503 (không phải
500/409 giả tạo).

Một Attempt/WorkItem blocked giờ có named recovery action gọi được qua API thật — `V6-06C` (Run diagnostics,
phụ thuộc `V6-06D`) chính thức unblock phần dependency riêng của nó.

## V6-07 — Conversation message endpoints

### Bối cảnh

V6-07 nằm trong nhóm P2, phụ thuộc V6-00/V6-01A/V6-02/V6-02A — cả 4 đã merge (xác nhận qua
`git log origin/master`: nhánh này branch từ `ab4ee48`, commit merge PR #43 của chính V6-04, đứng sau cả
V6-06 (#42) và V6-10B (#41)). Trích nguyên văn spec (`docs/design/08-v6-api-projections.md` dòng 297-306,
không diễn giải lại): "Mục tiêu: append/list canonical text messages và bounded context-used metadata...
Phạm vi: text `AppendMessage`, list messages, `ContextSnapshot`/message reference metadata... Không làm:
không binary upload, approval inference hoặc raw provider transcript... Thực hiện: reload project-owned
conversation; trả bounded canonical refs và pagination... Verify: replay, scope, paging, context refs,
redaction và free-text-control negative test... Hoàn thành khi: chat history là canonical platform state
và không trở thành control plane." Nguồn: AK-ARCH-021, HE-11-M07.

Theo doctrine của phiên giám sát, `baocaov6checklist.md` đang được 3 task song song khác ghi đồng thời
(V6-05, V6-06A, V6-06D) — xung đột merge khi mở PR là bình thường, không phải bug.

### Nghiên cứu

Đọc toàn bộ `internal/app/message/commands.go` (283 dòng, kể cả doc comment đầu file) trước khi viết bất kỳ
handler nào. Ba điểm quan trọng nhất:

1. `AppendMessage` compose `internal/app/artifact.PrepareAttachment` (Put+Verify, NGOÀI transaction — I/O
   thật cho content-addressed storage) với đúng MỘT `ports.UnitOfWork.WithSerializedWrite` (artifact row +
   message row + domain event + receipt, atomic) — mirror chính xác shape idempotent-command
   `internal/app/work.CreateRootWorkItem` đã thiết lập, kể cả 2 lớp `loadOrValidateReceipt`/
   `loadOrValidateReceiptTx` (pre-check ngoài transaction rồi re-check y hệt bên trong, đóng TOCTOU race).
2. `MaxContentSize = 1<<20` — content vượt trả `ErrContentTooLarge`, một `errors.New`-sentinel thường
   (không phải `apperror.Error`), tầng HTTP phải tự map sang 4xx, không dùng được `httpapi.WriteAppError`.
3. Canonical content LUÔN nhận `RetentionCanonicalContext` (ADR-017) — bake cứng trong chính command, không
   phải field caller chọn được; xác nhận không thêm field nào cho việc này vào wire DTO.

Đọc `internal/app/ports/contextsnapshot.go` toàn bộ — `ContextSnapshotRepository{CreateSnapshot, GetSnapshot,
GetSnapshotByAttemptID}` là port RIÊNG (V5-04), không nằm trong package `message`, và doc comment của chính
port này tự giải thích lý do tách khỏi `internal/domain/runtime.ContextSnapshot` cũ (spike-era, chỉ dùng cho
crash-recovery, không liên quan scheduling/dispatch thật) — xác nhận không được lẫn 2 khái niệm, và route mới
chỉ cần gọi `GetSnapshotByAttemptID` (đọc thuần, không viết gì mới lên port này).

Đọc `internal/app/ports/message.go` — `MessageRepository` đã có sẵn `GetMessage(ctx, id)` (không chỉ
`AppendMessage`/`ListMessagesForWorkItem` như prompt gốc liệt kê) — đây chính là method cho phép route
context-snapshot load một Message bằng ID rồi tự cross-check `ProjectID`/`WorkItemID` mà không cần thêm bất
kỳ method port nào mới.

Đọc lại toàn bộ `internal/delivery/httpapi/workitem` (V6-04, đã merge PR #43) làm precedent kiến trúc gần
nhất cùng shape "WorkItem-scoped": `dependencies.go`, `envelope.go` (`prepareCreateCommand`/
`replayOrProceed`), `errors.go` (`writeQueryError`/`writeCommandError` tự enumerate sentinel, không dùng
`WriteAppError` vì `internal/app/work`/`internal/app/message` không bao giờ trả `*apperror.Error`),
`workitem_commands.go`'s `handleCreateChildWorkItem` (reload `workapp.GetWorkItem(ctx, uow, scope,
workItemID)` TRƯỚC cả Idempotency-Key/body — pattern tái dùng nguyên vẹn cho route append/list/context-snapshot
của task này để verify WorkItem thuộc đúng project trước khi chạm bất cứ thứ gì khác).

Grep `CursorCodec|ResolveLimit|Fingerprint\(|httpapi\.Bind\(` trong toàn `internal/delivery/httpapi` chỉ ra
đúng 4 file khớp: `page.go`, `page_test.go`, `cursor.go`, `cursor_test.go` — nghĩa là cơ chế cursor có chữ ký
thật (`CursorCodec`/`CursorState`/`Bind`/`Fingerprint`) V6-02A đã xây từ trước đó CHƯA TỪNG được một route
production nào thật sự dùng (kể cả V6-04's list WorkItem/children cũng cố tình không dùng cursor — đã tự ghi
lại lý do trong chính section V6-04 ở trên). Task này là consumer PRODUCTION ĐẦU TIÊN của `CursorCodec`.

Grep `redact.Matcher|redact.NewMatcher` trong `cmd/aw/serve.go` xác nhận composition root đã có sẵn đúng MỘT
matcher process-lifetime: `redact.NewMatcher(sessionToken)`, hiện chỉ dùng cho `logging.New`. Không có bất kỳ
route nào khác trong repo hiện tại cần một `redact.Matcher` ở tầng HTTP — quyết định tái dùng chính giá trị
này thay vì phát minh field HTTP mới (xem mục Quyết định #3).

Đọc `cmd/aw/serve.go` dòng 50-93 xác nhận đúng như prompt: `*artifactRoot` là flag `--artifact-root` đã bắt
buộc và đã được validate tồn tại như một directory thật (dùng cho `readiness checker`), nhưng KHÔNG có bất kỳ
`ports.ArtifactStore` nào được construct trong toàn bộ file — `internal/adapters/artifactstore.New(root)`
(dòng 56, package đã có sẵn từ V1-08) chưa từng được gọi từ composition root thật. Đây chính là gap task này
phải lấp, không phải thêm flag mới.

Đọc `internal/app/message/attempt_linkage_test.go` — `seedFakeExecutionAttempt` là helper có sẵn dựng chuỗi
WorkflowRun→NodeRun→ExecutionAttempt tối thiểu qua `tx.Runtime()` trực tiếp (không cần AgentProfile/Policy/
scheduler), nhưng dùng `fake.UnitOfWork` — không phải sqlite thật. Copy sang test HTTP layer (sqlite thật) lộ
ra ngay một gap: `fake.UnitOfWork` không enforce FK `workflow_runs.workflow_version_id REFERENCES
workflow_versions(id)`, sqlite thật thì có — phải bổ sung một bước `tx.Definitions().PublishWorkflowVersion`
thật (mirror đúng `internal/app/runtime/advance_test.go`'s `publishWorkflowVersionDocument`) trước khi tạo
WorkflowRun, nếu không insert thất bại với lỗi generic "sqlite: unexpected error" (xem mục Test).

### Quyết định

1. **Subpackage riêng `internal/delivery/httpapi/message`**, đúng Contract chung §1.8 và mirror cấu trúc
   `workitem` (V6-04): `dependencies.go`, `envelope.go`, `errors.go`, `dto.go`, `append.go`, `list.go`,
   `context_snapshot.go`, `routes.go` — 8 file production, mỗi handler một file riêng theo route thay vì gộp
   `*_commands.go`/`*_queries.go` như `workitem` (chỉ 3 route ở đây, không cần gộp).
2. **`Content` trong `appendMessageBody` là JSON string thường, KHÔNG phải `[]byte`.** Go's `encoding/json`
   tự động base64-encode một field kiểu `[]byte` — ép caller phải base64 hoá text thường là không cần thiết
   và trái tinh thần "text `AppendMessage`" mà spec tự nêu. Chuyển `string(body.Content)` → `[]byte(...)`
   ngay trước khi gọi `appmessage.AppendMessageRequest`. Hệ quả: `maxBodyBytes` (tầng route, trên
   `httpapi.CanonicalizeJSON`) phải đặt GẤP ĐÔI `appmessage.MaxContentSize` (`2 * appmessage.MaxContentSize`,
   không phải 1 MiB phẳng như `workitem/envelope.go`) — một message hợp lệ đúng bằng `MaxContentSize` phải
   còn đủ chỗ để chạm được authoritative size-check thật của chính `AppendMessage` (và trả đúng
   `ErrContentTooLarge`), thay vì bị transport-level `ErrBodyTooLarge` chặn sớm hơn vì JSON string-escaping
   cộng các field nhỏ khác đẩy body vượt quá một giới hạn đặt quá sát.
3. **`Dependencies.Matcher` là MỘT giá trị `redact.Matcher` do composition root cấp — không phải field HTTP
   caller tự khai.** `redact.Matcher`'s own doc comment tự nói rõ: exact-value match trên một tập secret ĐÃ
   BIẾT trước, không phải pattern/regex scan free text — "để client tự khai secret value cần redact" là một
   request shape vô nghĩa dưới contract đó, vì secret "đã biết" của platform chỉ có thể là secret chính
   process này tự mint/resolve, không phải bất cứ gì một request không tin cậy tự xưng. `cmd/aw/serve.go`
   tách biến `matcher := redact.NewMatcher(sessionToken)` dùng CHUNG cho cả `logging.New` (đã có từ trước) lẫn
   `message.Dependencies.Matcher` (mới) — không tạo matcher thứ hai. Trục redaction caller thật sự điều khiển
   qua wire là `Sensitivity` (`PUBLIC`/`SENSITIVE`/`SECRET`, field tuỳ chọn, mặc định `PUBLIC` khi để trống).
4. **Pagination `ListMessages` dùng ĐÚNG cơ chế `httpapi.CursorCodec`/`CursorState`/`Bind`/`Fingerprint` của
   `cursor.go` (V6-02A) — không phải một cursor tự chế tối giản.** `appmessage.ListMessages` không tự phân
   trang ở tầng SQL (trả trọn mảng đã sort theo `Sequence`) — quyết định giữ nguyên, không thêm method port
   mới, dựa đúng framing doc comment của chính `commands.go`: "task chat is bounded, typed content" (khác
   hẳn V5-05's raw process output cần streaming thật). Route tự cắt trang trong bộ nhớ trên mảng đã bounded
   đó: `QueryFingerprint` chỉ hash `{WorkItemID}` (route này không có filter/sort nào khác); `Generation`
   cố định 0 (đây là authoritative read trực tiếp trên bảng `messages`, không phải một projection có khái
   niệm generation); `UpperWatermark` chốt ở `Sequence` lớn nhất TẠI THỜI ĐIỂM trang đầu tiên được phát hành
   — đúng lời hứa doc comment của `CursorState`: "một row ghi sau khi walk bắt đầu không bao giờ lọt vào
   giữa walk đó" (test `TestListMessages_PagesAndStaysStableAcrossConcurrentWrite` dựng đúng kịch bản: append
   message thứ 3 GIỮA lúc lấy trang 1 và trang 2 của MỘT walk — trang 2 không thấy message thứ 3, một request
   mới không mang cursor thì thấy đủ cả 3).
5. **`getMessageContextSnapshot` trả 404 THƯỜNG (không phải `WriteResourceHidden` leakage-normalized) cho 2
   case "message không có attempt" / "attempt chưa có snapshot".** Cả 2 case này caller ĐÃ được authorize
   xem chính Message đó (đã qua scope-check WorkItem/Project) — tiết lộ "message này chưa có context" không
   rò rỉ gì thêm, khác hẳn case "message thuộc project/work-item khác" (2 sentinel riêng
   `errMessageHasNoAttempt`/`errContextSnapshotNotYetAvailable`, message lỗi khác nhau, giúp client phân biệt
   được "sẽ không bao giờ có" và "chưa có nhưng có thể xuất hiện sau"). Chỉ "message không tồn tại/thuộc
   project khác/thuộc work-item khác" mới fold vào `WriteResourceHidden` chung.
6. **`contextSnapshotDetail` chỉ trả reference (ID/hash), không bao giờ trả nội dung resource/message thật.**
   `messageRefView{MessageID}`, `resourceRefView{OwnerVersionID,ResourceKey,ContentHash}`,
   `evidenceRefView{EvidenceID}`, `revisionView{RepositoryID,VCSObjectID,WorkspaceGeneration}` — mirror đúng
   field domain type `contextsnapshot.Snapshot` đã có, không field nào chứa raw content — đúng "bounded,
   read-only view" prompt yêu cầu, và đúng "Không làm: KHÔNG raw provider transcript" áp dụng luôn cho cả
   metadata này, không chỉ cho message content.
7. **Không route nào trong package này bao giờ suy luận state từ `Content`.** Route `appendMessage` chỉ nhận
   `Content` như bytes mù (redact rồi lưu), không route nào của package `message` đọc lại `Content` để quyết
   định bất cứ điều gì — đây là cách "Không làm: approval inference" được thoả mãn BẰNG CẤU TRÚC (không route
   handler nào có logic rẽ nhánh theo nội dung message), kiểm chứng trực tiếp bằng
   `TestAppendMessage_FreeTextLookingLikeApproval_NeverMutatesWorkItemState`.

### Thực hiện

- `internal/delivery/httpapi/message/` (package mới, 8 file production):
  - `dependencies.go`: `Dependencies{UnitOfWork, ArtifactStore, IDs, Clock, Matcher, Cursor}`.
  - `envelope.go`: `prepareCreateCommand`/`replayOrProceed` — mirror `workitem/envelope.go`, không có
    `prepareUpdateCommand` (route trong package này không route nào update — Message immutable/append-only,
    không route nào cần `If-Match`). `maxBodyBytes = 2 * appmessage.MaxContentSize` (Quyết định #2).
  - `errors.go`: `writeQueryError`/`writeCommandError`/`writeValidationError`/`writeReceiptHashConflict` —
    enumerate tường minh mọi sentinel `AppendMessage` thật sự trả (`ports.ErrPersistenceNotFound`,
    `ErrScopeMismatch`, `ErrCrossProjectReference`, `ErrCrossWorkItemReference`, `ErrReceiptConflict`,
    `appmessage.ErrContentTooLarge` → 413).
  - `dto.go`: `sensitivityWire`/`parseSensitivity` (map `PUBLIC`/`SENSITIVE`/`SECRET` ↔ `redact.Sensitivity`,
    trống mặc định `PUBLIC`), `messageRefDTO`/`messageToRefDTO`, `listMessagesResponse`,
    `listQueryFingerprint`, `contextSnapshotDetail` + 4 view type + `contextSnapshotToDetail`.
  - `append.go`: `handleAppendMessage` — reload `workapp.GetWorkItem` trước, validate `role`/`content`/
    `contentType`/`sensitivity`, dispatch `appmessage.AppendMessage` với `deps.Matcher` cố định.
  - `list.go`: `handleListMessages` — reload `GetWorkItem`, `httpapi.ResolveLimit`, gọi
    `appmessage.ListMessages` (đọc trọn), cắt trang bằng `CursorCodec`/`Bind`/`Fingerprint` (Quyết định #4).
  - `context_snapshot.go`: `handleGetMessageContextSnapshot` — reload `GetWorkItem`, load `Message` qua
    `tx.Messages().GetMessage`, cross-check `ProjectID`/`WorkItemID` thủ công, resolve
    `tx.ContextSnapshots().GetSnapshotByAttemptID` khi có `AttemptID`.
  - `routes.go`: doc comment đầy đủ + `RegisterRoutes` — 3 `RouteDescriptor`
    (`appendMessage`/`listMessages`/`getMessageContextSnapshot`), toàn bộ `ScopeKind: httpapi.ScopeProject`.
- `cmd/aw/serve.go`: thêm import `internal/adapters/artifactstore` + `httpmessage
  "internal/delivery/httpapi/message"`; construct `artifactStore, err := artifactstore.New(*artifactRoot)`
  ngay sau bước validate directory có sẵn; tách biến `matcher := redact.NewMatcher(sessionToken)` dùng chung
  cho `logging.New` (không đổi hành vi) và `message.Dependencies.Matcher` (mới); mint
  `cursorCodec := httpapi.NewCursorCodec([]byte(idsource.Random{}.NewID()))` — cùng cách `sessionToken` đã
  mint (per-process, không compiled-in) — rồi gọi `httpmessage.RegisterRoutes(routes,
  httpmessage.Dependencies{UnitOfWork: uow, ArtifactStore: artifactStore, IDs: idsource.Random{}, Clock:
  clock.System{}, Matcher: matcher, Cursor: cursorCodec})` ngay trước `routesFinalized = true`. Không flag
  CLI mới nào được thêm.

### Test

`internal/delivery/httpapi/message/message_test.go` + `context_snapshot_test.go` (2 file, 22 test function,
1 có 5 subtest — tổng 26 lượt chạy), toàn bộ real `httpapi.Server` (TCP listener thật, middleware chain
thật) + real `*sqlite.Store` + real `internal/adapters/artifactstore.Store` — mirror đúng idiom
`workitem_test.go`'s `newTestEnv`, mở rộng thêm `artifactStore`/`matcher`/`cursorCodec`:

- **Replay**: `TestAppendMessage_Idempotent_ReplaysWithoutDuplicating` (key giống, body giống → 200, không
  phải 201 lần 2, list vẫn đúng 1 message), `TestAppendMessage_DifferentBodySameIdempotencyKey_ReturnsConflict`
  (409 trước khi tạo message thứ 2).
- **Scope**: `TestAppendMessage_WorkItemBelongsToAnotherProject_ReturnsHiddenNotFound`,
  `TestListMessages_WorkItemBelongsToAnotherProject_ReturnsHiddenNotFound`,
  `TestGetMessageContextSnapshot_MessageBelongsToAnotherWorkItem_ReturnsHiddenNotFound` (Message thật, thuộc
  work-item KHÁC trong CÙNG project — vẫn 404, chứng minh handler cross-check `WorkItemID` thật của Message
  chứ không chỉ tin path), `TestGetMessageContextSnapshot_UnknownMessage_ReturnsHiddenNotFound`.
- **Paging**: `TestListMessages_PagesAndStaysStableAcrossConcurrentWrite` (kịch bản concurrent-write đầy đủ,
  xem Quyết định #4), `TestListMessages_EmptyConversation_ReturnsEmptyItemsNoCursor`,
  `TestListMessages_InvalidCursor_Returns400`, `TestListMessages_CursorFromAnotherWorkItem_ResyncRequired`
  (cursor hợp lệ, ký đúng, nhưng đổi work-item → 409 `RESYNC_REQUIRED`, chứng minh `Bind`'s
  `QUERY_CHANGED` path thật sự reachable qua route, không chỉ test riêng của `cursor.go`).
- **Context refs**: `TestGetMessageContextSnapshot_ResolvesWhenPresent` (dựng attempt thật + snapshot thật,
  verify `SnapshotID`/`AttemptID`/`ManifestHash`/`MessageRefs`/`ResourceRefs` khớp), `..._MessageHasNoAttempt_
  Returns404`, `..._AttemptWithoutSnapshotYet_Returns404` (attempt tồn tại thật nhưng chưa có snapshot).
- **Redaction trên nội dung ĐÃ PERSIST thật** (đọc lại qua `tx.Artifacts().GetArtifact` +
  `env.artifactStore.Open`, không phải absence-of-code check):
  `TestAppendMessage_SensitivitySecret_RedactedInPersistedStorage` (Sensitivity=SECRET → `[REDACTED]`),
  `TestAppendMessage_KnownProcessSecret_RedactedInPersistedStorage` (content = đúng `testSessionToken`, đã
  đăng ký làm secret trong matcher của chính test server — redact dù Sensitivity để mặc định PUBLIC, chứng
  minh exact-match Matcher thắng bất kể Sensitivity khai báo).
- **Free-text-control negative test**: `TestAppendMessage_FreeTextLookingLikeApproval_NeverMutatesWorkItemState`
  — append message content đúng chữ `"approve"`, so `workapp.GetWorkItem` trước/sau bằng `!=` (struct so sánh
  trực tiếp, không field nào đổi).
- Table test `TestAppendMessage_RejectsInvalidRequests` (5 case: role trống/role sai/content trống/
  contentType trống/sensitivity sai → 400), `TestAppendMessage_ContentTooLarge_Returns413`,
  `TestAppendMessage_MissingIdempotencyKey_Returns400`, `TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet`
  (đối chiếu đúng 3 operationId đóng, mirror `workitem_test.go`'s idiom).

**Phát hiện thật khi viết fixture context-snapshot** (không phải bug trong code sản xuất, mà một khác biệt
thật giữa `fake.UnitOfWork` và sqlite thật): copy nguyên `seedFakeExecutionAttempt` từ
`internal/app/message/attempt_linkage_test.go` sang test dùng sqlite thật ban đầu fail với lỗi generic
`INTERNAL: sqlite: unexpected error` — điều tra bằng cách đọc `internal/adapters/sqlite/start_workflow_run.go`
xác nhận `workflow_runs.workflow_version_id` có FOREIGN KEY thật vào `workflow_versions(id)`, còn
`fake.RuntimeRepository` không enforce FK gì cả nên helper gốc chưa từng cần publish version thật. Sửa bằng
cách thêm đúng một bước `tx.Definitions().PublishWorkflowVersion(ctx, definition, version)` (mirror
`internal/app/runtime/advance_test.go`'s `publishWorkflowVersionDocument`) trước khi tạo `WorkflowRun` — sau
đó cả 2 test context-snapshot pass, không cần chạm bất kỳ code sản xuất nào.

`go build ./...`, `go vet ./...` sạch trên toàn repo. `go test ./internal/delivery/httpapi/message/... -v`:
26/26 xanh (~4-26s mỗi test, tổng ~26s — phần lớn thời gian nằm ở 2 test context-snapshot vì phải publish
workflow version + workflow run + node run + attempt thật qua sqlite). `go test ./...` full suite chạy sau
khi toàn bộ package `message` xanh, không regression ở bất kỳ package nào khác (không sửa bất kỳ interface/
port có sẵn nào — chỉ thêm subpackage mới cộng 1 wiring bổ sung vào `cmd/aw/serve.go`).

### Verify

- **Replay**: xem mục Test — `AppendMessage` idempotent thật qua HTTP, conflict đúng 409 trước I/O thứ 2.
- **Scope**: WorkItem/Message thuộc project hoặc work-item khác đều fold về đúng 1 response 404
  leakage-normalized giống hệt ID không tồn tại — không route nào trong package tin path/body hơn state đã
  reload thật.
- **Paging**: cursor thật, ký thật, `UpperWatermark` chốt tại thời điểm trang đầu — message ghi giữa walk
  không lọt vào walk đó nhưng lọt vào lần đọc mới không cursor; cursor sai work-item → resync thật (409), không
  âm thầm trả sai kết quả.
- **Context refs**: resolve đúng khi có `AttemptID` + snapshot thật; 404 rõ ràng (phân biệt 2 lý do) khi
  không có `AttemptID` hoặc attempt chưa có snapshot — không case nào rơi vào 500 hay silent-empty.
- **Redaction**: xác nhận trên bytes ĐÃ GHI THẬT xuống `ArtifactStore` (đọc lại qua `Open`, không suy diễn từ
  absence of code) — cả `Sensitivity=SECRET` lẫn exact-match Matcher (secret của chính process) đều redact
  đúng.
- **Free-text-control negative test**: message nội dung y hệt một quyết định approval không hề đổi bất kỳ
  field nào của WorkItem — proof trực tiếp "message không bao giờ là control signal", đúng cả 2 phía "Không
  làm" của task này (approval inference) lẫn của V6-06A (map message text thành control) đã tự nêu độc lập.
- **Hoàn thành khi — "chat history là canonical platform state và không trở thành control plane"**: đúng,
  bằng cấu trúc (không route nào đọc lại `Content` để quyết định state) lẫn bằng test trực tiếp.

### Kết quả

Branch `feat/v6-07-conversation-message-endpoints` từ `origin/master` tại `ab4ee48` (sau PR #43/#42/#41).
Package mới `internal/delivery/httpapi/message` (8 file production + 2 file test, ~/tổng cộng 26 test
function/subtest, real HTTP + real sqlite + real ArtifactStore). 3 route HTTP thật lần đầu tồn tại: `POST/GET
/projects/{projectId}/work-items/{workItemId}/messages`, `GET
/projects/{projectId}/work-items/{workItemId}/messages/{messageId}/context-snapshot`. `cmd/aw/serve.go` được
sửa để construct `ports.ArtifactStore` THẬT lần đầu tiên trong toàn bộ composition root (tái dùng flag
`--artifact-root` đã có, không flag mới), tách matcher dùng chung cho logging lẫn redaction message, và mint
`CursorCodec` per-process cho pagination — grep xác nhận `httpmessage.RegisterRoutes` thật sự có trong
`cmd/aw/serve.go`, không chỉ tồn tại ở test package-level (đúng lo ngại doctrine đã nêu từ vụ V6-04 ban đầu).
Không migration mới (xác nhận migration cao nhất trên `origin/master` vẫn là `0037`, schema `messages`/
`artifacts`/`attempt_context_snapshots` đã đủ từ V5-01/V5-02/V5-04). `go build/vet/test ./...` sạch, không
regression.

## V6-07B — Evidence, ContextSnapshot và artifact query endpoints

### Bối cảnh

V6-07B phụ thuộc V6-00/V6-01A/V6-02A — cả 3 đã merge từ lâu, xác nhận qua `git log origin/master --oneline -3`
tại thời điểm bắt đầu: HEAD `01fbd3c` (PR #46, V6-05), trước đó `8549311` (PR #45, V6-06A), `de095c6`
(PR #47, V6-06D) — nhánh này branch thẳng từ `01fbd3c`. Trích nguyên văn spec
(`docs/design/08-v6-api-projections.md` dòng 325-334, không diễn giải lại): "Mục tiêu: list/inspect
evidence/context/artifact và stream authorized content an toàn... Phạm vi: evidence list/detail/verify
metadata, artifact inventory, `GetArtifactContent`, ContextSnapshot detail... Không làm: không expose
locator, trusted HTML hoặc authorize từ projection/ID shape... Thực hiện: reload owning WorkItem/Run/
Evidence/Message; range/size/media headers, content-hash ETag, no-sniff/download policy and sensitivity
redaction... Verify: cross-project/guessed ID, traversal, tamper, range, large stream, HTML/SVG và secret
fixtures... Hoàn thành khi: evidence/artifact verify được sau restart mà delivery không biết filesystem
path." Nguồn: AK-ARCH-021, HE-11-M07, ADR-017.

Đây là task cuối cùng trong nhóm "conversation/evidence" của V6-07 series (V6-07 đã merge #44, V6-07A chưa
thấy branch tại thời điểm này) — 3 route đầu (evidence list/detail, artifact inventory) hoàn toàn mới,
2 route sau (`GetArtifactContent`, `ContextSnapshot` detail) build trên chính `ports.ArtifactStore` và
`ports.ContextSnapshotRepository` mà V6-07 đã composition-root-wire trước đó (`artifactStore`,
`cmd/aw/serve.go`), không cần thêm dependency I/O mới nào ở tầng composition root.

`baocaov6checklist.md` đang được 2 task song song khác ghi đồng thời (V6-10H, V6-10J theo brief) — xung đột
merge khi mở PR là bình thường, không phải bug.

### Nghiên cứu

Đọc lại đúng 4 file hạ tầng chung trước khi viết bất kỳ route nào: `route.go` (`RouteDescriptor`/
`RouteRegistry`), `errors.go` (`WriteResourceHidden`/`WriteAppError`/`StatusForAppErrorCode`), `media.go`
(`ParseRange`/`ApplyPartialContentHeaders`/`WriteRangeNotSatisfiable`/`ApplyContentHeaders`/
`ResolveMediaDisposition`) — xác nhận `media.go` đã có SẴN toàn bộ cơ chế range/no-sniff/inline-vs-attachment
task này cần, viết từ trước (comment của chính `media.go` tự nói "V6-07B is the first real consumer") —
không cần viết lại bất kỳ helper range/media nào, chỉ cần gọi đúng. Đọc `internal/delivery/httpapi/message/
context_snapshot.go` (route hẹp `getMessageContextSnapshot` đã merge) làm precedent tường minh nhất: xác
nhận route đó gọi THẲNG `tx.ContextSnapshots().GetSnapshotByAttemptID`/`tx.Messages().GetMessage` từ NGAY
TRONG handler HTTP (không qua app-layer query nào) — khác với chỉ dẫn của brief task này ("HTTP layer should
never touch ports.Tx/UnitOfWork internals directly"); quyết định KHÔNG lặp lại pattern đó cho task này (xem
Quyết định #1), dù nó là precedent gần nhất.

Đọc toàn bộ `internal/domain/runtime/evidence.go` (111 dòng): `Evidence` struct đã tự mang
`ProjectID`/`WorkItemID`/`RunID`/`NodeRunID`/`AttemptID` làm cột trực tiếp (không cần join qua
NodeRun/Attempt để biết scope) — nghĩa là "reload owning WorkItem/Run/Evidence" cho MỘT Evidence row chỉ
cần load đúng 1 row rồi so 2 field, không cần chuỗi 3 lần load. `ArtifactReferences []string` bắt buộc
non-empty (`NewEvidence` tự validate) — mọi Evidence row LUÔN trỏ tới ít nhất 1 Artifact thật.

Đọc `internal/app/ports/unitofwork.go`'s `RuntimeRepository` toàn bộ đoạn Evidence (dòng 558-576):
`GetEvidence(id)` và `ListEvidenceForAttempt(attemptID)` đã có sẵn — nhưng KHÔNG có method nào list theo
WorkItem hay Run. Đọc `internal/adapters/sqlite/evidence.go` (150 dòng) xác nhận bảng `evidence` (migration
0001) có cột `work_item_id`/`run_id` thật (không phải suy diễn) — filter theo cột đó là một SQL `WHERE` đơn
giản, không cần join. Quyết định thêm đúng 1 method mới `ListEvidenceForWorkItem` (xem Quyết định #2), mirror
đúng convention đặt tên `ListWorkflowRunsForWorkItem` (V4-12C) đã có sẵn trong CHÍNH interface này.

Đọc toàn bộ `internal/domain/artifact/artifact.go` (243 dòng) — đây là phần điều tra bắt buộc quan trọng
nhất của task này theo đúng brief. Xác nhận bằng cách đọc trực tiếp struct `Artifact`: field duy nhất liên
quan "ownership" là `ProjectID` — KHÔNG có `WorkItemID`/`RunID`/`MessageID` nào cả. Đọc tiếp
`internal/app/ports/artifactrecord.go` xác nhận `ArtifactRepository` chỉ có `GetArtifact(id)`,
`ListOrphanedArtifacts`/`ListArtifactsByLocator` (2 method này tồn tại riêng cho V5-14 sweeper, filter theo
`AttachState`/`Locator` share, không liên quan gì đến "artifact thuộc WorkItem nào"). Kết luận: không có
QUAN HỆ nào trong domain model hiện tại nối trực tiếp Artifact → WorkItem/Run — liên kết THẬT DUY NHẤT một
WorkItem-scoped caller có với một tập Artifact ID cụ thể là `Evidence.ArtifactReferences` (Message cũng có
`ContentArtifactID` nhưng đó là liên kết của route Message khác, V6-07/V6-07A, không phải phạm vi task này).
Quyết định "artifact inventory" = liệt kê Artifact theo MỘT Evidence row cụ thể, không phải theo WorkItem
trực tiếp — xem Quyết định #3 để biết toàn bộ lý luận.

Đọc `internal/app/ports/artifact.go` (71 dòng): `ArtifactStore.Open`/`Verify` nhận `ArtifactRef{Locator,
SHA256, Size, ContentType, Sensitivity, Redacted}` — toàn bộ 6 field này đã có sẵn 1-1 trên chính
`artifact.Artifact` row (`Locator`, `ContentHash`, `Size`, `MediaType`, `Sensitivity`, `Redacted`) — nghĩa
là dựng lại một `ArtifactRef` hợp lệ từ Artifact row đã load không cần thêm bất kỳ field/method port nào
mới. Đọc `internal/adapters/artifactstore/filesystem.go` toàn bộ: `Open` tự gọi `Verify` trước khi mở file
("verify-open as one guarantee") và trả về đúng `*os.File` (implement `io.Seeker`) bọc trong `io.ReadCloser`
— xác nhận Range request có thể `Seek` thật thay vì phải discard-copy, dù type ở interface level là
`io.ReadCloser` (cần type-assert `io.Seeker` với fallback an toàn cho một `ports.ArtifactStore` implementation
khác không hỗ trợ seek).

Đọc `internal/app/message/commands.go` dòng 147-157 xác nhận thứ tự "Redact BEFORE Put" đã có sẵn từ V6-07:
`AppendMessage` redact CONTENT rồi mới `Put` — nghĩa là với một Artifact `Sensitivity=SECRET`, bytes ĐÃ
REDACT ngay từ lúc ghi, không phải một trách nhiệm route đọc phải tự làm lại. Xác nhận trực tiếp bằng
`assertStoredContent` test có sẵn của `message_test.go` (đọc lại `[REDACTED]` từ đúng `ArtifactStore.Open`).
Kết luận: `getArtifactContent` không cần bất kỳ logic redact-tại-đọc nào — chỉ cần serve đúng bytes đã
persist, "sensitivity redaction" Verify bullet coi như đã thoả mãn bằng chính write-path có sẵn.

Đọc `internal/domain/contextsnapshot/contextsnapshot.go` toàn bộ: `Snapshot` struct tự mang
`ProjectID`/`WorkItemID` trực tiếp (giống hệt Evidence) — route detail độc lập chỉ cần load 1 row rồi so 2
field, không cần chuỗi load Message trước như route hẹp `getMessageContextSnapshot` của V6-07 phải làm (route
đó cần Message để tìm `AttemptID`; route NÀY nhận thẳng `snapshotId`, không qua Message nào cả).

Đọc `docs/design/11-v6-00-ux-artifact.md` Screen 11 (dòng 423-448, "Evidence / artifact viewer") và Screen
12 hàng 4 (dòng 469-476) — đây là nguồn khoá tên `operationId` CHÍNH XÁC, không tự đặt: `listEvidence`
("Danh sách evidence theo WorkItem/Run/criterion"), `getEvidence` ("Chi tiết/verify metadata evidence
(online)" — tách biệt tường minh khỏi `aw evidence verify` offline `CLI_LOCAL`/ADR-028, "hai leaf khác nhau
cho hai nhu cầu khác nhau, không phải trùng authority"), `listArtifacts` ("Danh sách artifact"),
`getArtifactContent` ("Nội dung artifact (stream/tải)", "tên khoá cứng ở Phạm vi V6-07B"), `getContextSnapshot`
("Chi tiết ContextSnapshot"). Screen 12 hàng 4 tự nói field context-used của Message (V6-07) và
`getContextSnapshot` detail đầy đủ (task này) "authority dùng chung" — xác nhận quyết định share code convert
DTO giữa 2 route (Quyết định #1).

### Quyết định

1. **Toàn bộ 5 query đi qua app-layer function mới `internal/app/runtime/queries.go` — route HTTP không bao
   giờ chạm `ports.Tx`/`ports.UnitOfWork` trực tiếp**, kể cả `getContextSnapshot` (dù precedent gần nhất,
   `getMessageContextSnapshot` của V6-07, làm ngược lại — gọi thẳng `tx.ContextSnapshots()`/`tx.Messages()`
   từ trong handler). Đặt trong `internal/app/runtime` (không phải package con mới) vì Evidence/Checkpoint/
   ExecutionAttempt/NodeRun đều sống sẵn trong domain package này, và vì brief chỉ định rõ vị trí này. Đồng
   thời **chuyển `contextSnapshotDetail`/`contextSnapshotToDetail` của `internal/delivery/httpapi/message/
   dto.go` (V6-07) thành `runtime.ContextSnapshotDetail`/`runtime.ContextSnapshotToDetail` (export, chuyển vị
   trí, GIỮ NGUYÊN wire shape byte-for-byte)** — `message/dto.go` giờ chỉ còn 1 type alias +
   1 hàm gọi lại — đúng gợi ý của Screen 12 hàng 4 ("authority dùng chung") thay vì duy trì 2 bản sao logic
   convert giống hệt nhau. Xác nhận KHÔNG regression: `go test ./internal/delivery/httpapi/message/...` chạy
   lại đầy đủ (9 file test cũ, không sửa) sau refactor, xanh 100% — JSON field name/`omitempty` không đổi.
2. **Thêm đúng 1 method port mới: `ports.RuntimeRepository.ListEvidenceForWorkItem(ctx, workItemID)`** (sqlite
   thật + `fake.RuntimeRepository` — chỉ 2 nơi implement `ports.RuntimeRepository` toàn repo, xác nhận bằng
   grep `_ ports.RuntimeRepository =`). Unfiltered ở tầng SQL (`ORDER BY created_at, kind`) — đúng discipline
   "caller classifies" mà `ListNodeRunsForRun`/`ListWorkflowRunsForWorkItem` đã thiết lập sẵn trong CHÍNH
   interface này; filter `runId`/`kind` (map field `Kind` — trục "criterion" spec nói tới) áp dụng ở tầng
   `runtime.EvidenceFilter` trong Go, không thêm SQL filter riêng vì tập Evidence mỗi WorkItem luôn bounded
   nhỏ (không giống hội thoại Message có thể dài vô hạn — V6-07 mới cần cursor pagination thật, task này thì
   không, không có dòng nào trong "Phạm vi" của task này đòi hỏi).
3. **"Artifact inventory" scope theo MỘT Evidence row (`ListArtifactsForEvidence`), KHÔNG theo WorkItem trực
   tiếp — quyết định trọng tâm nhất của cả task, ghi lại đầy đủ lý luận ngay trong doc comment của
   `queries.go`.** Lý do (đọc source thật, không đoán, xem mục Nghiên cứu): `artifact.Artifact` struct không
   có cột `WorkItemID`/`RunID` nào — chỉ có `ProjectID`. Liên kết THẬT duy nhất một route WorkItem-scoped có
   với một tập Artifact ID cụ thể là `Evidence.ArtifactReferences` — không phát minh thêm method
   `ListArtifactsForWorkItem` mới trên `ArtifactRepository` khi domain model không thật sự hỗ trợ quan hệ đó
   (đúng nguyên tắc `00-roadmap.md` §3: "không chia chỉ để tạo file/field nếu phần đó chưa có contract
   test/behavior quan sát được"). Hệ quả trực tiếp: route `listArtifacts`/`getArtifactContent` đều nested
   dưới `.../evidence/{evidenceId}/...`, không phải `.../work-items/{workItemId}/artifacts/...` — điều này
   cũng khiến "reload owning Evidence trước khi trả artifact" (dòng Thực hiện của spec) đúng theo NGHĨA ĐEN
   cho MỌI route artifact, không chỉ evidence.
4. **`getArtifactContent` authorize bằng CHÍNH `Evidence.ArtifactReferences`, không phải chỉ check
   `Artifact.ProjectID`.** `artifactId` phải là MỘT trong các reference của evidence trên path — một Artifact
   ID THẬT, cùng project, nhưng KHÔNG nằm trong `ArtifactReferences` của evidence được path chỉ định, vẫn bị
   404 (test `TestGetArtifactContent_ArtifactNotReferencedByThisEvidence_ReturnsHiddenNotFound`) — chặn chặt
   hơn "cùng project là đủ", đúng tinh thần "không authorize theo ID shape" của spec. `ResolveEvidenceArtifactContent`
   (queries.go) tự reload Evidence rồi confirm membership TRƯỚC KHI reload Artifact row, fold cả 2 trường hợp
   "evidence sai scope" và "artifact không thuộc evidence này" vào cùng 1 `ports.ErrScopeMismatch`.
5. **Streaming (`Verify`/`Open`/`io.Copy`) chỉ xảy ra Ở TẦNG HTTP (`internal/delivery/httpapi/evidence`),
   KHÔNG BAO GIỜ trong `internal/app/runtime/queries.go`.** Package `queries.go` chỉ trả về
   `ports.ArtifactRef` đã dựng từ Artifact row (I/O-free, chạy trong `WithReadOnly`); handler HTTP tự gọi
   `ArtifactStore.Verify` rồi `Open` NGOÀI transaction — đúng go-core-spec §11.1 "không gọi filesystem
   artifact store trong transaction" mà `internal/app/artifact.PrepareAttachment`'s doc comment đã nêu cho
   write-path, áp dụng tương tự cho read-path ở đây.
6. **ETag của `getArtifactContent` là content-hash thật (`"<sha256>"`), KHÔNG dùng `httpapi.ETagFromVersion`.**
   `ETagFromVersion` là ETag phiên bản-số cho optimistic-concurrency/`If-Match` (mutable resource) —
   Evidence/Artifact ở đây immutable, không có khái niệm "version" caller cần precondition; ETag đúng nghĩa
   ở đây là "nội dung này có đúng byte như lần trước hay không", nên dùng thẳng `ArtifactRef.SHA256`.

### Thực hiện

- `internal/app/ports/unitofwork.go`: thêm `ListEvidenceForWorkItem(ctx, workItemID) ([]runtime.Evidence,
  error)` vào `RuntimeRepository` (doc comment đầy đủ, mirror style các method Evidence có sẵn).
- `internal/adapters/sqlite/evidence.go`: implement `ListEvidenceForWorkItem` — `SELECT ... WHERE
  work_item_id = ? ORDER BY created_at, kind`.
- `internal/app/ports/fake/runtime.go`: implement mirror y hệt cho `fake.RuntimeRepository` (dùng bởi các
  test khác trong repo qua `fake.UnitOfWork`, không phải test của task này — vẫn bắt buộc để interface không
  vỡ compile).
- `internal/app/runtime/queries.go` (file mới, ~400 dòng): `requireProjectScope`/`scopeMismatch`/
  `verifyWorkItemInScope` (mirror y hệt `internal/app/work/queries.go`'s helper cùng tên/cùng logic, không
  export dùng chung — mỗi package app tự giữ bản riêng, đúng convention hiện có); `RevisionView` (dùng chung
  cho cả Evidence lẫn ContextSnapshot); `EvidenceDetail`/`evidenceToDetail`/`EvidenceFilter`/
  `ListEvidenceForWorkItem`/`GetEvidence`; `ArtifactSummary`/`toArtifactSummary`/`loadEvidenceInScope`/
  `ListArtifactsForEvidence`/`ResolveEvidenceArtifactContent`; `MessageRefView`/`ResourceRefView`/
  `EvidenceRefView`/`ContextSnapshotDetail`/`ContextSnapshotToDetail`/`GetContextSnapshot`.
- `internal/delivery/httpapi/message/dto.go`: xoá `messageRefView`/`resourceRefView`/`evidenceRefView`/
  `revisionView`/`contextSnapshotDetail`/`contextSnapshotToDetail` cũ, thay bằng `type contextSnapshotDetail
  = runtimeapp.ContextSnapshotDetail` + hàm gọi lại `runtimeapp.ContextSnapshotToDetail`.
- `internal/delivery/httpapi/evidence/` (package HTTP mới, 7 file production):
  - `routes.go`: doc comment đầy đủ (route inventory, lý do nested artifact dưới evidence) + `RegisterRoutes`
    — 5 `RouteDescriptor`, toàn bộ `ScopeKind: httpapi.ScopeProject`.
  - `dependencies.go`: `Dependencies{UnitOfWork, ArtifactStore}` — không cần `IDs`/`Clock` (route thuần đọc).
  - `dto.go`: `listEvidenceResponse`/`listArtifactsResponse` (wrap `items`, không cursor — Quyết định #2).
  - `errors.go`: `writeQueryError` (fold `ErrPersistenceNotFound`/`ErrScopeMismatch` → `WriteResourceHidden`),
    `writeContentError` (fold qua `httpapi.WriteAppError` — tận dụng `ArtifactStore.Verify` đã tự trả
    `apperror.CodeNotFound` thật cho case "object đã bị xoá/purge"), `writeValidationError`.
  - `evidence.go`: `handleListEvidence` (đọc query param `runId`/`kind`), `handleGetEvidence`.
  - `artifact.go`: `handleListArtifacts`, `handleGetArtifactContent` (authorize → `Verify` → `Open` →
    `ParseRange` → `ApplyContentHeaders`/ETag → ghi 200 toàn bộ hoặc 206 theo Range, `skipBytes` type-assert
    `io.Seeker` với fallback discard-copy — Quyết định #5/#6).
  - `context_snapshot.go`: `handleGetContextSnapshot`.
- `cmd/aw/serve.go`: thêm import `httpevidence "internal/delivery/httpapi/evidence"`; gọi
  `httpevidence.RegisterRoutes(routes, httpevidence.Dependencies{UnitOfWork: uow, ArtifactStore:
  artifactStore})` ngay sau `httpmessage.RegisterRoutes`, trước `routesFinalized = true` — tái dùng ĐÚNG
  `artifactStore` V6-07 đã construct, không tạo store thứ hai. Xác nhận bằng grep `httpevidence` trong chính
  file này thấy cả import lẫn lệnh gọi thật (không chỉ tồn tại ở test).

### Test

`internal/delivery/httpapi/evidence/` (5 file test, 24 test function/subtest — real `httpapi.Server` + real
`*sqlite.Store` + real `internal/adapters/artifactstore.Store`, mirror idiom `message_test.go`'s `newTestEnv`)
cộng `internal/app/runtime/queries_sqlite_test.go` (file mới, 8 test — real sqlite, gọi thẳng hàm app-layer
không qua HTTP, mirror `internal/app/work/queries_sqlite_test.go`'s pattern cho V6-04): tổng 32 test.

- **Cross-project/guessed ID**: `TestGetEvidence_BelongsToAnotherWorkItem_ReturnsHiddenNotFound`,
  `TestGetEvidence_WorkItemBelongsToAnotherProject_ReturnsHiddenNotFound`,
  `TestListArtifacts_EvidenceBelongsToAnotherWorkItem_ReturnsHiddenNotFound`,
  `TestGetContextSnapshot_BelongsToAnotherWorkItem_ReturnsHiddenNotFound` +
  `..._WorkItemBelongsToAnotherProject_...`, cộng `TestGetEvidence_WrongWorkItem_ReturnsScopeMismatch`/
  `TestGetContextSnapshot_WrongWorkItem_ReturnsScopeMismatch` (gọi thẳng app-layer, assert đúng
  `ports.ErrScopeMismatch` chứ không chỉ status HTTP) — toàn bộ đều 404, không 403.
- **Traversal**: `TestGetEvidence_UnknownEvidenceID_ReturnsHiddenNotFound` và
  `TestGetArtifactContent_PathTraversalLookingArtifactID_ReturnsHiddenNotFound` gửi thẳng `../../../../etc/
  passwd`/`..%2f..%2fsecret` làm `evidenceId`/`artifactId` — 404 giống hệt ID ngẫu nhiên, chứng minh bằng
  test thật (không chỉ code review) rằng ID không bao giờ chạm filesystem.
- **Tamper**: `TestGetArtifactContent_TamperedContent_Returns500NotServedSilently` — corrupt THẬT bytes trên
  đĩa (`os.WriteFile` đè lên đúng object path tính từ `Locator`, mirror công thức
  `internal/integration/v5accept/fixture_test.go`'s `artifactObjectPath`), xác nhận response KHÔNG BAO GIỜ
  200 và body không leak nội dung đã corrupt.
- **Range requests**: `TestGetArtifactContent_RangeRequest_PartialContent` (206, `Content-Range: bytes
  2-5/10`, body đúng 4 byte giữa), `TestGetArtifactContent_RangeNotSatisfiable_Returns416`.
- **Large stream**: `TestGetArtifactContent_LargeStream_StreamsCorrectly` — artifact thật 8 MiB, so byte-for-
  byte toàn bộ response body với nội dung gốc, `Content-Length` đúng `8388608`.
- **HTML/SVG fixtures**: `TestGetArtifactContent_HTMLAndSVG_ForceDownloadNeverInline` (2 subtest) — content
  `text/html`/`image/svg+xml` chứa `<script>` thật vẫn được SERVE (không bị chặn), nhưng
  `Content-Disposition` luôn `attachment`, không bao giờ `inline`.
- **Secret fixtures**: `TestGetArtifactContent_SecretSensitivity_ServesRedactedPersistedBytes` — dựng
  artifact qua đúng `internal/app/artifact.PrepareAttachment` với `Sensitivity=Secret`, xác nhận PERSISTED
  bytes đã là `[REDACTED]` (qua `assertPersistedContent`, đọc lại từ `ArtifactStore.Open` thật, không suy
  diễn) TRƯỚC KHI assert route trả đúng y hệt bytes đó.
- **Verify metadata/evidence list/filter**: `TestGetEvidence_HappyPath_ReturnsVerifyMetadata` (đối chiếu đủ
  `ArtifactReferences`/`RevisionSetHash`/`PolicyVersion`/lineage với Evidence row thật), `TestListEvidence_
  FiltersByRunAndKind` (2 Evidence row 2 Run khác nhau, filter `runId` và `kind` đều đúng subset),
  `TestListEvidence_EmptyWorkItem_ReturnsEmptyItems`, `TestListEvidence_UnknownWorkItem_ReturnsHiddenNotFound`.
- **Locator never exposed**: `TestListArtifacts_HappyPath_NeverExposesLocator` — đọc RAW JSON bytes của
  response (không qua struct decode trước), assert chuỗi `"locator"` không hề xuất hiện — kiểm tra ở mức
  wire, không chỉ ở mức field Go không tồn tại.
- **Defense-in-depth ở tầng app-layer** (không thể dựng qua HTTP vì cần fabricate một data-integrity edge
  case thật production không bao giờ tạo ra):
  `TestListArtifactsForEvidence_ReferencesForeignProjectArtifact_RejectsAsScopeMismatch` (Evidence row của
  project A trỏ tới 1 Artifact thật của project B — reject, không silent-include/crash),
  `TestResolveEvidenceArtifactContent_ArtifactNotInEvidenceReferences_ReturnsScopeMismatch`,
  `TestResolveEvidenceArtifactContent_HappyPath_ReturnsRealRef`,
  `TestListEvidenceForWorkItem_InstallationScope_RejectsAsScopeMismatch`,
  `TestListEvidenceForWorkItem_UnknownWorkItem_ReturnsPersistenceNotFound`,
  `TestListEvidenceForWorkItem_OrdersByCreatedAtThenKind`.
- `TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet` (đối chiếu đúng 5 operationId đóng).

`go build ./...`, `go vet ./...` sạch trên toàn repo. `go test ./internal/delivery/httpapi/evidence/... -v`:
24/24 xanh. `go test ./internal/app/runtime/... -run "TestListEvidenceForWorkItem|TestGetEvidence|
TestListArtifactsForEvidence|TestResolveEvidenceArtifactContent|TestGetContextSnapshot" -v`: 8/8 xanh.
`go test ./internal/delivery/httpapi/message/...` (regression check sau refactor Quyết định #1): xanh, không
sửa hành vi. `go test ./...` full suite (81 package): toàn bộ `ok`, 0 `FAIL`, exit code 0 — không regression
ở bất kỳ package nào khác, không gặp lại bất kỳ flake đã biết nào của phiên này
(`TestSPK09QuarantineRecreateFencesStaleGeneration`,
`TestSupervisorNormalExit_TreeQuiescedFalseWhileDescendantStillRuns`, ...).

### Verify

- **Cross-project/guessed ID**: mọi Evidence/Artifact/ContextSnapshot thuộc project/work-item khác hoặc
  không tồn tại đều fold về đúng 1 response 404 leakage-normalized — không route nào trong package tin
  path/ID shape hơn state đã reload thật (kể cả case "ID trông hợp lệ" như `sha256:deadbeef`).
- **Traversal**: bất khả thi theo cấu trúc (artifactId/evidenceId chỉ bao giờ là khoá tra database, không
  bao giờ là filesystem path) — chứng minh bằng test thật gửi payload traversal-shaped, không chỉ lý luận.
- **Tamper**: `ArtifactStore.Verify` bắt được corruption thật trên đĩa TRƯỚC khi `Open` trả reader — response
  là lỗi typed (500), không bao giờ 200 với bytes sai.
- **Range**: `Range: bytes=2-5` trả đúng 206 + `Content-Range`/`Content-Length` + đúng 4 byte; range vượt
  giới hạn trả 416 kèm `Content-Range: bytes */<size>`.
- **Large stream**: 8 MiB round-trip đúng byte-for-byte qua đúng cơ chế `io.Copy` từ `*os.File` thẳng ra
  `ResponseWriter` (không buffer nguyên khối trong bộ nhớ — xác nhận bằng cả code review `artifact.go`'s
  `handleGetArtifactContent` lẫn test thật).
- **HTML/SVG**: nội dung script-capable vẫn được serve (không bị cấm), nhưng `Content-Disposition:
  attachment` bắt buộc download — không browser nào render inline như trang tin cậy của chính origin.
- **Secret fixtures**: bytes đã redact TỪ LÚC GHI (write-path V6-07 có sẵn) — route content chỉ serve đúng
  những gì đã persist, verify trên bytes THẬT, không phải absence-of-code.
- **Hoàn thành khi — "evidence/artifact verify được sau restart mà delivery không biết filesystem path"**:
  đúng bằng cấu trúc — không field `Locator` nào từng rời khỏi `internal/app/runtime/queries.go` hay chạm
  tầng HTTP; mọi truy cập nội dung đi qua đúng chuỗi Artifact ID → `GetArtifact` → `ArtifactRef` dựng server-
  side → `Verify`/`Open`, y hệt trước và sau một restart giả lập (test dùng `*sqlite.Store` thật trên đĩa,
  không phải in-memory, nên state Evidence/Artifact/ContextSnapshot sống sót restart bằng chính cơ chế sẵn
  có, không cần thêm gì).

### Kết quả

Branch `feat/v6-07b-evidence-context-artifact-endpoints` từ `origin/master` tại `01fbd3c` (sau PR #46/#45/
#47/#44). Package HTTP mới `internal/delivery/httpapi/evidence` (7 file production + 4 file test, 24 test
function/subtest). App-layer mới `internal/app/runtime/queries.go` (5 hàm query công khai + 1 file test
riêng, 8 test). 1 method port mới `ports.RuntimeRepository.ListEvidenceForWorkItem` (sqlite + fake, không
migration mới — xác nhận migration cao nhất trên `origin/master` vẫn `0037`, bảng `evidence`/`artifacts`/
`attempt_context_snapshots` đã đủ từ migration 0001/0027/0029). Refactor không-đổi-hành-vi trên
`internal/delivery/httpapi/message/dto.go` (chia sẻ `ContextSnapshotDetail` với V6-07, verify lại toàn bộ
test cũ của package đó vẫn xanh). 5 route HTTP thật lần đầu tồn tại: `GET /projects/{projectId}/work-items/
{workItemId}/evidence`, `GET .../evidence/{evidenceId}`, `GET .../evidence/{evidenceId}/artifacts`,
`GET .../evidence/{evidenceId}/artifacts/{artifactId}/content`, `GET .../context-snapshots/{snapshotId}` —
grep xác nhận `httpevidence.RegisterRoutes` thật sự có trong `cmd/aw/serve.go`, không chỉ tồn tại ở test
package-level.

Phát hiện thật cần ghi lại: domain model hiện tại của `artifact.Artifact` không có bất kỳ cột ownership nào
ngoài `ProjectID` — "artifact inventory theo WorkItem" như tên gọi tự nhiên của UX doc thật ra không được
domain model hỗ trợ trực tiếp; quyết định scope theo Evidence (Quyết định #3) là lựa chọn trung thực nhất
với dữ liệu thật hiện có, không phát minh thêm quan hệ domain chưa có caller thật nào cần.

`go build/vet ./...` sạch. `go test ./...` toàn repo (81 package): 100% `ok`, không `FAIL`, exit code 0.

## V6-10J — Adapter-build registry endpoints

### Bối cảnh

V6-10J phụ thuộc `V6-00, V6-01A, V6-02, V6-02A, V6-10I` — cả 5 đã merge trên `origin/master` (`01fbd3c
feat(v6-05)...` là tip lúc branch); `git merge-base --is-ancestor origin/master HEAD` xác nhận worktree này
bắt đầu đúng từ tip, không cần rebase. `baocaov6checklist.md` đang bị 2 task song song khác ghi (`V6-07B`,
`V6-10H`, đúng cảnh báo trong system prompt) — append-only, dự kiến conflict khi merge, không phải bug.

Trích nguyên văn spec (`docs/design/08-v6-api-projections.md` dòng 541-550): *"Mục tiêu: expose
list/detail/probe/register only after V6-10I hardening. Phụ thuộc: V6-00, V6-01A, V6-02, V6-02A, V6-10I. Phạm
vi: installation `/adapter-builds` route/schema fragments. Không làm: no project mirror, direct
prober/process or transport receipt. Thực hiện: GET public queries; POST dispatch hardened commands. Probe
remains mutation because it creates replayable candidate receipt although registry state does not change.
Verify: token/fingerprint/protocol/config/scope schemas and handler architecture spy. Hoàn thành khi: provider
upgrade flow is usable without bypassing command contract. Nguồn: ADR-022, ADR-025, ADR-028."*

Task này là task HTTP-transport thuần tuý thứ hai (sau `V6-06D`) bọc một bộ command **đã tồn tại sẵn và vừa
được hardening xong bởi một task khác trong cùng chuỗi** (`V6-10I`, PR #36, `4e44af1`) — khác `V6-04`/`V6-06`/
`V6-07` (những task tự viết mới cả application command lẫn HTTP layer). Điểm khác biệt quan trọng nhất so với
mọi package HTTP-mutation trước đó trong repo (`workitem`, `run`, `decision`, `recovery`): 2 command mutating ở
đây (`ProbeAdapterBuild`, `RegisterAdapterBuild`) đã tự có `ports.Command` envelope VÀ tự có receipt lookup
nội bộ riêng của chính chúng (V6-10I mới thêm) — nghĩa là package HTTP task này viết ra là package MUTATION
HTTP ĐẦU TIÊN trong repo mà tầng transport không được tự thêm bất kỳ pre-dispatch receipt-replay fast path nào
(`httpapi.LookupReceipt`/`ReconcileReceipt`/`WriteReceiptReplay` — thứ `workitem`'s `replayOrProceed` VẪN làm)
— vì spec tự nêu rõ "Không làm: ... transport receipt", một ràng buộc mạnh hơn hẳn optimisation thông thường.

### Nghiên cứu

Đọc trọn `internal/app/adapterbuild/commands.go` (521 dòng, cả 4 hàm + toàn bộ doc comment) trước khi viết bất
kỳ dòng handler nào:

- `ListAdapterBuilds(ctx, uow) ([]adapterbuild.Build, error)` (dòng 474) và `GetAdapterBuild(ctx, uow, id)
  (adapterbuild.Build, error)` (dòng 486) — 2 plain read query qua `uow.WithReadOnly`, không nhận
  `ports.Command`, không idempotency, `GetAdapterBuild` trả `ports.ErrAdapterBuildNotFound` cho id lạ. Không
  có field filter/sort/cursor nào — plain unbounded list, giống hệt `ListProjects`/`ListWorkItems`.
- `ProbeAdapterBuild(ctx, uow, cmd ports.Command, req ProbeRequest) (adapterbuild.CandidateToken, error)`
  (dòng 138) — đọc kỹ đúng câu spec tự trích dẫn ở "Thực hiện": mặc dù không mutate registry (`adapterbuild.Build`
  row), đây VẪN là command mutating thật vì tạo ra receipt bất biến chứa `CandidateToken` (chính V6-10I mới
  làm), bắt buộc `Idempotency-Key` như mọi command khác. `ProbeRequest` có đúng 7 field:
  `ProviderKey/ExecutablePath/ProtocolVersion/CapabilityManifest/OS/Toolchain/ConfigIdentity` — không field nào
  khác, không có field `Hash` hay `Fingerprint` trần nào cho caller tự khai — `ExecutablePath` là đường dẫn
  thật trên máy chạy `aw serve`, và bản thân `HashExecutableFile` (dòng 501, đọc file thật qua `os.Open` +
  `sha256`) chạy Y HỆT bên trong `ProbeAdapterBuild`, KHÔNG BAO GIỜ ở tầng gọi nó — xác nhận trực tiếp
  "Không làm: ... direct prober/process" của task này nghĩa là gì cụ thể: tầng HTTP chỉ forward
  `ExecutablePath` (một chuỗi) y nguyên, không tự mở file, không tự spawn tiến trình nào.
- `RegisterAdapterBuild(ctx, uow, cmd ports.Command, req RegisterRequest) (RegisterResult, error)` (dòng 352)
  — mutation thật, tạo `adapterbuild.Build` row bất biến. `RegisterRequest` chỉ có đúng 2 field: `Token
  adapterbuild.CandidateToken` (chính token `ProbeAdapterBuild` vừa trả, caller phải echo lại y nguyên) và
  `CapabilityManifest` (đo lại lần 2, so khớp hash với token — TOCTOU-closing re-probe của ADR-022). KHÔNG có
  field `RegisteredBy` nào cả (V6-10I đã bỏ hẳn) — `cmd.Actor` là nguồn DUY NHẤT, đọc dòng 356-358 xác nhận
  `RegisterAdapterBuild` tự reject nếu `cmd.Actor` rỗng.
- `HashExecutableFile(path string) (string, error)` (dòng 501) — export chỉ để `drift.go`'s `VerifyNoDrift`
  tái dùng, xác nhận bằng đọc doc comment dòng 496-500 ("exported so VerifyNoDrift ... can reuse"); đọc
  `drift.go` (103 dòng) xác nhận `VerifyNoDrift` là re-verification RUNTIME-side (dùng bởi
  `RetryBlockedActivation`, V6-06D) — không liên quan gì tới scope task này, không đụng tới.

Đọc `baocaov6checklist.md`'s section `V6-10I` (dòng 1838-2054, trước khi viết) để hiểu đúng bối cảnh hardening
vừa xong: chữ ký `ProbeAdapterBuild`/`RegisterAdapterBuild` là breaking change có chủ đích (thêm `cmd
ports.Command` bắt buộc), receipt lookup tách hẳn `WithReadOnly` riêng trước mọi I/O thật, `ports.ErrScopeMismatch`
dùng lại (không tạo sentinel installation-scope riêng), 2 architecture test archtest riêng
(`TestProbeAdapterBuildTransactionNeverCallsFilesystemOrProcess`/`TestRegisterAdapterBuildTransactionNeverCallsFilesystemOrProcess`)
đã tự chứng minh 2 transaction ghi của tầng application không chạm filesystem/process — task này chỉ cần thêm
MỘT lớp proof tương tự ở tầng HTTP một bậc phía trên.

Đọc `internal/app/ports/command.go` xác nhận `ports.InstallationScope()`/`CommandScope.IsInstallation()` —
công cụ có sẵn, không cần tạo gì mới; và `internal/app/ports/adapterbuild.go` xác nhận `ErrAdapterBuildNotFound`/
`ErrNoSigningKey` là 2 sentinel export sẵn tầng ports.

Đọc `internal/domain/adapterbuild/{adapterbuild.go,token.go}` xác nhận toàn bộ shape wire cần echo:
`CandidateTuple` (8 field), `CapabilityManifest` (4 field, `ValidateCapabilityManifest` export sẵn — reject
`SupportsStart=false` hoặc `CanonicalEventKinds` rỗng/trùng), `CandidateToken` (`Tuple/Nonce/ExpiresAt/Signature`,
đã có json tag đầy đủ, có thể tái sử dụng thẳng trên wire không cần DTO riêng), `Build` (KHÔNG có field export
nào — accessor-only, giống `definition.VersionFields`) và `VerifyToken`/`SignToken` (2 sentinel
`ErrInvalidSignature`/`ErrTokenExpired`).

Đọc `cmd/aw/adapter.go` (407 dòng) — CLI `aw adapter probe|register|list|show` là caller thật DUY NHẤT khác
ngoài test trong toàn repo hiện gọi 4 hàm này. Xác nhận 2 điều quan trọng: (1) `adapterBuildView`
(dòng 350-363) là kỹ thuật round-trip-qua-accessor chuẩn cho `Build` không có field export — package HTTP mới
tái dùng nguyên technique này, không phát minh cách khác; (2) CLI tự đo `CapabilityManifest`/`ProtocolVersion`
thật qua `newAgentExecutor`+`AgentExecutor.Capabilities(ctx)` (spawn tiến trình thật, tiện lợi riêng cho CLI) —
đây CHÍNH LÀ con đường "direct prober/process" mà HTTP layer phải tuyệt đối không lặp lại: caller HTTP tự khai
`ProtocolVersion`/`CapabilityManifest` thẳng trong request body, không có đường nào trong package HTTP mới gọi
`AgentExecutor.Capabilities` hay construct `claude.New`/`codex.New`.

Đọc `docs/design/01-system-design.md` dòng 597-600 và `docs/design/11-v6-00-ux-artifact.md` dòng 122-125 để
khoá đúng 4 route/operationId (không đoán): `GET /adapter-builds` → `listAdapterBuilds`, `GET
/adapter-builds/{id}` → `getAdapterBuild` (suy ra từ path pattern `{id}` chung — `01-system-design.md` chỉ ghi
route, không ghi operationId riêng cho detail, nhưng pattern đặt tên `get<Noun>` đã nhất quán toàn bộ
`workitem`/`catalog`/`definitions`), `POST /adapter-builds/probe` → `probeAdapterBuild`, **`POST
/adapter-builds`** (không phải `/adapter-builds/register`) → `registerAdapterBuild` — 2 operationId
`probeAdapterBuild`/`registerAdapterBuild` bị khoá cứng thêm lần nữa ở ADR-028 §30 (`02-architecture-decisions.md`
dòng 712-736, "tên khóa cứng ở ADR-028 §30").

Đọc `internal/delivery/httpapi/workitem/envelope.go` (`prepareCreateCommand`/`replayOrProceed`) và
`internal/delivery/httpapi/receiptreplay.go` (`LookupReceipt`/`ReconcileReceipt`/`WriteReceiptReplay`) để hiểu
CHÍNH XÁC cái gì bị bỏ đi: `replayOrProceed`'s own doc comment tự thừa nhận đây là "purely a latency/UX
optimization ... never a correctness dependency" — nghĩa là bỏ nó không làm mất correctness gì, nhưng spec của
CHÍNH task này (`V6-10J`) lại cấm rõ ràng hơn workitem's optional optimization: "no transport receipt" là một
chỉ dẫn tường minh, không phải một lựa chọn kiến trúc để cân nhắc lại.

Đọc `internal/archtest/recovery_http_test.go` (`TestRecoveryHTTPNeverReachesSchedulerOrWorkerOrMutatesStateDirectly`)
và `internal/archtest/run_control_test.go` (`TestRunControlHTTPNeverReachesSchedulerOrWorker`) làm khuôn cho
architecture test riêng của task này — cả 2 dùng đúng kỹ thuật "parse real source, walk go/ast, forbid named
selector".

### Quyết định

1. **Route path đúng 4 cái, không có route thứ 5 nào** (khác biệt với suy nghĩ ban đầu là có thể cần
   `/adapter-builds/{id}/probe` hay tương tự) — `01-system-design.md` đã tự khoá path phẳng
   `POST /adapter-builds/probe` (không gắn `{id}`, vì probe chưa có id nào để gắn — candidate chưa tồn tại
   trong registry) và `POST /adapter-builds` (không phải `/register`, theo đúng convention REST "POST vào
   collection = tạo tài nguyên trong collection đó").
2. **Dependencies chỉ có `UnitOfWork` + `Clock`, không có `IDs idsource.Source`** — khác mọi package HTTP
   mutating trước đó (`workitem`, `run`, `decision`) đều cần idsource vì command họ bọc tự mint ID mới; cả
   `ProbeAdapterBuild` lẫn `RegisterAdapterBuild` không nhận `ids` tham số nào (ID của `Build` là content-addressed,
   tự tính từ `CandidateTuple.ID()`, không phải giá trị mint).
3. **Không dùng `httpapi.LookupReceipt`/`ReconcileReceipt`/`WriteReceiptReplay` (`replayOrProceed`-style) trước
   khi dispatch** — quyết định trung tâm của toàn bộ task, bám sát nguyên văn "Không làm: ... transport
   receipt". `prepareCommand` (package mới) chỉ làm đúng phần chung "require Idempotency-Key, canonicalize
   body, build `ports.Command`" rồi dispatch thẳng — không có bước lookup/replay riêng nào ở giữa. Lý do kỹ
   thuật (không chỉ vì spec bảo vậy): cả `ProbeAdapterBuild` lẫn `RegisterAdapterBuild` đã tự có
   `loadProbeReplay`/`loadRegisterReplay` riêng, chạy `WithReadOnly` TRƯỚC mọi I/O thật, đúng bên trong chính
   command — thêm một lớp check y hệt ở tầng HTTP phía trên là kiểm tra CÙNG MỘT dòng receipt hai lần, không
   mua thêm được sự an toàn nào, đúng nghĩa đen "double up" mà spec cấm.
4. **`prepareCommand` là "create-shaped" thuần tuý (`ExpectedVersion` luôn 0, không `If-Match` cho route nào)**
   — cả 2 mutation đều không precondition trên version của một resource đã tồn tại: Probe không tạo registry
   row nào để có version, Register tạo row MỚI (target chưa tồn tại trước khi gọi).
5. **Field validate() trần (`errors.New`, không sentinel) của `ProbeRequest.validate()` được lặp lại tường minh
   ở tầng HTTP (`validateProbeBody`), còn `domainadapterbuild.ErrInvalidCapabilityManifest` (sentinel export)
   được bắt bằng `errors.Is` ở CẢ 2 nơi (`validateProbeBody` gọi thẳng `domainadapterbuild.ValidateCapabilityManifest`
   để có message field-level tốt hơn, VÀ `writeProbeError` vẫn giữ nhánh bắt sentinel này làm defense-in-depth)**
   — lý do: `ProviderKey/ExecutablePath/ProtocolVersion/OS/Toolchain/ConfigIdentity` rỗng chỉ trả về
   `errors.New` trần bên trong `ProbeRequest.validate()`, không có sentinel nào để `errors.Is` phân biệt — nếu
   không tự chặn trước ở tầng HTTP, một request thiếu field sẽ rơi thẳng vào nhánh 500 mặc định của
   `writeProbeError` thay vì 400 đúng nghĩa. `CapabilityManifest` thì khác: `ValidateCapabilityManifest` đã là
   hàm export sẵn với sentinel `ErrInvalidCapabilityManifest` — gọi lại đúng hàm thật đó ở tầng HTTP (không tự
   viết lại luật validate) vừa cho field-level error tốt, vừa không tạo hai bản luật có thể lệch nhau.
6. **`RegisterRequest`/`RegisterAdapterBuild`'s error mới do register tự trả (`ErrInvalidSignature`,
   `ErrTokenExpired`, `ports.ErrNoSigningKey`, `ErrExecutableDrift`, `ErrCapabilityManifestDrift`) map theo
   đúng ngữ nghĩa HTTP riêng từng loại, không gộp chung**: `ErrInvalidSignature` → 400 (lỗi caller — token giả/
   sai/khác installation, không phải race); `ErrTokenExpired` → 409 (trạng thái thật đã đổi theo thời gian,
   caller phải probe lại — không phải request sai hình dạng); `ports.ErrNoSigningKey` → 409 (chưa từng probe
   lần nào trên installation này — precondition chưa thoả, không phải bug); `ErrExecutableDrift`/
   `ErrCapabilityManifestDrift` → route qua bảng chung `httpapi.StatusForAppErrorCode(errorcode.CodeAdapterBuildDrift)`
   thay vì hardcode 409 riêng, giữ nhất quán với bảng chung nếu sau này đổi (mirror đúng `recovery/errors.go`'s
   `writeResolveWorkItemBlockerError`'s cách route `ErrWorkspaceQuarantined` qua bảng chung).
7. **Status code Register: 201 khi `!AlreadyExisted`, 200 khi `AlreadyExisted`** — khác các route create khác
   trong repo (`createRootWorkItem` luôn 201 vô điều kiện) vì đây là trục fingerprint-dedup thật (V6-10I's own
   "fingerprint dedupe is not command replay"): một registration content-trùng lặp KHÔNG tạo row mới, phản ánh
   đúng bằng status 200 thay vì giả vờ 201 cho một thứ không hề được "tạo" lần này. Response body
   (`registerAdapterBuildResponse{Build, AlreadyExisted}`) giống hệt cả 2 trường hợp — client tự đọc
   `alreadyExisted` để phân biệt nếu cần, HTTP status chỉ là gợi ý bổ sung.
8. **`adapterBuildView` (DTO response) round-trip qua accessor y hệt `cmd/aw/adapter.go`'s bản CLI, `RegisteredAt`
   giữ nguyên `time.Time` (encoding mặc định RFC3339Nano của `encoding/json`)** — không phát minh format
   timestamp riêng cho package này khi CLI đã có tiền lệ y hệt cho đúng field này.
9. **`GET /adapter-builds/{id}` không tồn tại route mirror project-scoped nào** — implement đúng bằng cách
   không có route thứ 5 nào cả (không phải một cờ "no-op" hay handler rỗng) — "Không làm: no project mirror"
   là điều task này thoả mãn bằng CHÍNH VIỆC KHÔNG VIẾT, không phải một guard runtime.
10. **Architecture test mới `TestAdapterBuildHTTPNeverReachesProcessOrFilesystemDirectly`, kết hợp 2 kỹ thuật**:
    (a) cấm import `os/exec`, `internal/adapters/process`, `internal/adapters/providers/{claude,codex}` (kỹ
    thuật `ImportsOnly` parse, mirror `TestRequestWorkspaceReconciliationNeverImportsWorkspaceIO`); (b) cấm gọi
    selector `Capabilities` (method `AgentExecutor.Capabilities` — spawn tiến trình thật) và
    `HashExecutableFile`/`hashExecutableFile` (cả 2 cách viết, mirror đúng 2 spelling
    `TestProbeAdapterBuildTransactionNeverCallsFilesystemOrProcess` đã kiểm ở tầng application) — kết hợp cả
    import-level lẫn call-level để không có đường lách nào (import riêng lẻ không gọi gì vẫn bị bắt; gọi qua
    một import gián tiếp khác vẫn bị bắt ở tầng selector).

### Thực hiện

- `internal/delivery/httpapi/adapterbuild/adapterbuild.go` (file mới): package doc comment đầy đủ + `Dependencies{UnitOfWork,
  Clock}` + `RegisterRoutes` (4 descriptor, tất cả `httpapi.ScopeInstallation`) + 2 hằng `commandTypeProbe =
  "ProbeAdapterBuild"`/`commandTypeRegister = "RegisterAdapterBuild"` (khớp byte-for-byte chuỗi
  `cmd/aw/adapter.go`'s `requestHash("ProbeAdapterBuild", ...)` đã dùng, để receipt/event ghi qua HTTP và qua
  CLI cùng command-type).
- `internal/delivery/httpapi/adapterbuild/dto.go` (file mới): `capabilityManifestBody` (+`toManifest`/
  `capabilityManifestBodyFrom`), `probeAdapterBuildBody` (7 field khớp `ProbeRequest`), `registerAdapterBuildBody`
  (`Token domainadapterbuild.CandidateToken` tái dùng thẳng + `CapabilityManifest`), `adapterBuildView` (12
  field, round-trip qua accessor), `adapterBuildListResponse{Builds []adapterBuildView}`,
  `registerAdapterBuildResponse{Build, AlreadyExisted}`.
- `internal/delivery/httpapi/adapterbuild/queries.go` (file mới): `handleListAdapterBuilds`, `handleGetAdapterBuild`
  (validate `id` path non-blank rồi dispatch, lỗi not-found qua `writeQueryError`).
- `internal/delivery/httpapi/adapterbuild/commands.go` (file mới): `prepareCommand` (preamble chung, không
  receipt-replay fast path — quyết định 3 ở trên), `validateProbeBody` (6 field trần + gọi
  `ValidateCapabilityManifest` thật), `handleProbeAdapterBuild` (200 OK), `handleRegisterAdapterBuild` (201/200
  theo `AlreadyExisted`).
- `internal/delivery/httpapi/adapterbuild/errors.go` (file mới): `writeValidationError`, `writeQueryError`
  (`ports.ErrAdapterBuildNotFound` → `WriteResourceHidden`), `writeProbeError` (`ErrInvalidCapabilityManifest`
  → 400, `ports.ErrReceiptConflict` → 409, default 500), `writeRegisterError` (5 nhánh sentinel + default 500,
  chi tiết ở Quyết định 6).
- `internal/archtest/adapterbuild_http_test.go` (file mới): `TestAdapterBuildHTTPNeverReachesProcessOrFilesystemDirectly`
  (import-check + selector-check kết hợp, chi tiết Quyết định 10).
- `cmd/aw/serve.go`: +1 import (`httpadapterbuild`), +1 dòng `httpadapterbuild.RegisterRoutes(routes,
  httpadapterbuild.Dependencies{UnitOfWork: uow, Clock: clock.System{}})` ngay trước `routesFinalized = true`
  — không sửa gì khác trong file (không flag mới, không dependency mới nào cần construct, đúng tinh thần
  "additive routes.Register call only" các task trước đã lập).

### Test

`internal/delivery/httpapi/adapterbuild/adapterbuild_test.go` (1 file, dùng khuôn `newTestEnv` thật của
`workitem_test.go` — real `httpapi.Server` qua TCP loopback thật, real `*sqlite.Store`, không mock, không
`httptest.Server`+mux giả):

- `TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet` — đúng 4 operationId, tất cả
  `ScopeKind == httpapi.ScopeInstallation` — proof cơ học cho "scope schema" và "no project mirror" cùng lúc.
- `TestFullJourney_ProbeRegisterListGet` — probe thật (hash file thật qua `writeExecutable`) → register thật
  (echo token, xác nhận `RegisteredBy` lấy từ principal chứ không phải request field) → list (đúng 1 phần tử)
  → detail (khớp field). Đây là "Hoàn thành khi: provider upgrade flow ... usable" chạy thật end-to-end.
- `TestRegister_DuplicateFingerprintReturns200AlreadyExisted` — 2 cặp probe+register ĐỘC LẬP (idempotency key
  khác nhau hoàn toàn) trên cùng 1 executable — build ID giống hệt, status code 201 rồi 200.
- `TestProbe_MissingIdempotencyKeyIsBadRequest`.
- `TestProbe_MissingRequiredFieldIsBadRequest` — table-driven qua cả 6 field trần
  (`providerKey/executablePath/protocolVersion/os/toolchain/configIdentity`), mỗi field xoá riêng, xác nhận
  đúng 1 `ErrorDetail` trỏ đúng field đó — "protocol/config schema" của Verify bullet.
- `TestProbe_InvalidCapabilityManifestIsBadRequest` (`supportsStart=false`).
- `TestProbe_SameIdempotencyKeyDifferentBodyIsConflict` — chứng minh trực tiếp bỏ receipt-replay fast path
  không làm mất correctness: `ProbeAdapterBuild`'s own internal receipt recheck vẫn bắt được race, dù package
  HTTP không tự check gì trước.
- `TestRegister_InvalidTokenSignatureIsBadRequest` — bootstrap key thật qua 1 probe, sửa `Signature` sai, xác
  nhận 400. "Token schema" của Verify bullet.
- `TestRegister_ExpiredTokenIsConflict` — load signing key THẬT trực tiếp qua `uow` (không hard-code), tự ký
  một `CandidateToken` hết hạn bằng `domainadapterbuild.SignToken` thật (không giả lập bằng cách sửa response
  JSON tay) — xác nhận 409.
- `TestRegister_NoSigningKeyIsConflict` — register thẳng trên installation chưa từng probe lần nào.
- `TestRegister_ExecutableDriftIsConflict` — probe rồi GHI ĐÈ nội dung file thật trước khi register — "fingerprint
  schema" của Verify bullet, xác nhận re-hash thật (không phải so sánh giả).
- `TestGetAdapterBuild_UnknownIdIsNotFound`, `TestListAdapterBuilds_EmptyRegistryReturnsEmptyList`.

`internal/archtest/adapterbuild_http_test.go`: `TestAdapterBuildHTTPNeverReachesProcessOrFilesystemDirectly`
(handler architecture spy của Verify bullet).

`go build ./...`, `go vet ./...` sạch. `go test ./internal/delivery/httpapi/adapterbuild/... -count=1 -v` (15
test/subtest) pass 100% trong 2.19s. `go test ./internal/archtest/... -run TestAdapterBuildHTTP -count=1 -v`
pass. `go test ./... -count=1` (81 package, 0 dòng FAIL) pass 100%, không regression ở bất kỳ package nào
khác — bao gồm `cmd/aw` (66.7s, giờ có route mới đi qua `aw serve` thật), `internal/app/adapterbuild` (2.5s,
không đổi vì task này không sửa package đó), `internal/adapters/sqlite` (141s).

### Verify

- **"token schema"**: `TestRegister_InvalidTokenSignatureIsBadRequest`, `TestRegister_ExpiredTokenIsConflict` —
  cả 2 dùng chữ ký/token thật, không giả lập.
- **"fingerprint schema"**: `TestRegister_ExecutableDriftIsConflict` (re-hash thật sau khi ghi đè file),
  `TestRegister_DuplicateFingerprintReturns200AlreadyExisted` (fingerprint dedup thật).
- **"protocol schema"**: `TestProbe_MissingRequiredFieldIsBadRequest/protocolVersion` + round-trip field
  `ProtocolVersion` trong `TestFullJourney_ProbeRegisterListGet`.
- **"config schema"**: `TestProbe_MissingRequiredFieldIsBadRequest/configIdentity` + round-trip field
  `ConfigIdentity` trong `TestFullJourney_ProbeRegisterListGet`.
- **"scope schema"**: `TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet` — cả 4 route đều
  `ScopeInstallation`, không route nào `ScopeProject`.
- **"handler architecture spy"**: `TestAdapterBuildHTTPNeverReachesProcessOrFilesystemDirectly` — cấm cả
  import (`os/exec`, `internal/adapters/process`, `internal/adapters/providers/{claude,codex}`) lẫn selector
  (`Capabilities`, `HashExecutableFile`/`hashExecutableFile`).
- **"Không làm: no project mirror"**: đúng 4 route, không route nào project-scoped, không handler nào nhận
  `{projectId}` — `TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet` xác nhận cơ học.
- **"Không làm: direct prober/process"**: `TestAdapterBuildHTTPNeverReachesProcessOrFilesystemDirectly` +
  không import nào trong `internal/delivery/httpapi/adapterbuild/*.go` (không test) tới
  `internal/adapters/process`/`internal/adapters/providers/*`.
- **"Không làm: transport receipt"**: không có lời gọi `httpapi.LookupReceipt`/`ReconcileReceipt`/
  `WriteReceiptReplay` nào trong toàn bộ package — xác nhận bằng đọc lại `commands.go`'s `prepareCommand`
  (không có bước nào giữa build `cmd` và dispatch), cộng `TestProbe_SameIdempotencyKeyDifferentBodyIsConflict`
  chứng minh gián tiếp: nếu package này có tự thêm receipt check riêng, test đó vẫn pass y hệt (double-check
  vô hình) — nhưng đọc source xác nhận trực tiếp không hề có lớp thứ hai nào.
- **"Hoàn thành khi: provider upgrade flow ... usable without bypassing command contract"**:
  `TestFullJourney_ProbeRegisterListGet` chạy đúng luồng probe→review→register→list→detail hoàn toàn qua HTTP
  thật, không có đường tắt nào bỏ qua `ports.Command`/`Idempotency-Key`.

### Kết quả

Package mới `internal/delivery/httpapi/adapterbuild` (5 file production ~380 dòng:
`adapterbuild.go`/`dto.go`/`queries.go`/`commands.go`/`errors.go`; 1 file test ~360 dòng, 15 test
function/subtest), 1 architecture test mới (`internal/archtest/adapterbuild_http_test.go`). `cmd/aw/serve.go`
+7 dòng (1 import, 1 `RegisterRoutes` call, không flag mới, không dependency composition-root mới nào cần
construct — khác hẳn `V6-06D` phải thêm 2 flag executable mới). `go build/vet/test ./...` sạch, không
regression trên cả 81 package.

4 route HTTP thật lần đầu tồn tại: `GET /adapter-builds`, `GET /adapter-builds/{id}`, `POST
/adapter-builds/probe`, `POST /adapter-builds` — chạy được qua `aw serve` thật, xác nhận bằng grep trực tiếp
`cmd/aw/serve.go` (`httpadapterbuild.RegisterRoutes` xuất hiện đúng 1 lần), không chỉ tin test package cô lập
(đúng bài học `V6-04` để lại từ đầu chuỗi V6).

Không migration mới (xác nhận migration cao nhất trên `origin/master` vẫn là `0037` lúc branch; bảng
`adapter_builds`/signing-key đã có sẵn từ V2-07A, V6-10I chỉ thêm cột receipt/event dùng chung, không đổi
schema `adapter_builds` bản thân).

Quyết định kiến trúc quan trọng nhất của task: package HTTP-mutation ĐẦU TIÊN trong repo hoàn toàn không có
pre-dispatch receipt-replay fast path riêng (khác `workitem`'s `replayOrProceed`) — dispatch thẳng vào command
đã tự có receipt lookup nội bộ, đúng nguyên văn "Không làm: ... transport receipt" của chính task, đồng thời
chứng minh được bằng test rằng bỏ lớp đó không hề làm mất correctness (`ProbeAdapterBuild`'s own receipt check
vẫn bắt đúng race qua HTTP y hệt qua CLI/test trực tiếp).

Luồng "provider upgrade" (probe candidate mới → operator review → register xác nhận) giờ đã dùng được thật
qua HTTP, không chỉ qua CLI `aw adapter probe|register` — 2 bề mặt (CLI, HTTP) cùng dispatch đúng một cặp
command đã hardening, không bề mặt nào có logic riêng của chính nó.

## V6-10H — Safe settings endpoints

### Bối cảnh

V6-10H bọc HTTP lên trên V6-10G (đã đóng hoàn toàn — "endpoint implementation has no remaining storage/
precedence/allowlist decision"): expose `GET/PUT /settings/safe` cho `internal/app/safesettings.GetSafeSettings`/
`UpdateSafeSettings`. Task tự mô tả phạm vi rất hẹp — "exact allowlist schema and route fragment" — nhưng bản
brief giao việc chỉ ra một khoảng trống thật: `GetSafeSettings` chỉ trả `Desired` (document đã persist), trong
khi Thực hiện line của task đòi response phải có "desired/effective/restart/masking" — `Effective` (per-field
startup-precedence resolution) là một hàm HOÀN TOÀN riêng, `internal/app/safesettings/startup.go`'s
`ResolveEffective(defaults, file, sqlite, env, flags)`, cần 3 tầng file/env/flags chỉ tồn tại thật ở
`aw serve`'s own process startup — và grep xác nhận **0 call site** gọi `ResolveEffective` ở bất kỳ đâu trong
`cmd/aw` trước task này. Đây là phần khó nhất của task, phải tự quyết định wiring chứ không chỉ bọc route.
Batch này chạy song song với V6-07B và V6-10J (baocaov6checklist.md là điểm giao chung, append-only — xử lý
merge conflict khi finalize như mọi lần).

### Nghiên cứu

Đọc trước khi viết code:

- `docs/design/08-v6-api-projections.md` dòng 516-524 (V6-10H) đọc trực tiếp, không suy diễn qua brief — xác
  nhận đúng "Thực hiện: GET query; PUT installation command + If-Match; response desired/effective/restart/
  masking" và "Không làm: handler no config/file/env/secret access and no extra setting" — HANDLER không được
  đụng file/env, nhưng composition root (`cmd/aw/serve.go`) thì được, vì đó là nơi các tầng file/env/flags
  thật sự tồn tại.
- `internal/app/safesettings/commands.go`: `GetSafeSettings` (plain read, không CommandEnvelope, mirror
  `adapterbuild.GetAdapterBuild`), `SafeSettingsResult.RestartRequired = !record.Desired.IsZero()` (static
  fact, không so sánh với process đang chạy — Alpha không có hot-reload); `UpdateSafeSettings` đòi
  `cmd.Scope.IsInstallation()`, replay/CAS/event/receipt trong 1 transaction; **quan trọng**: `UpdateSafeSettings`
  chỉ ghi receipt ở nhánh THÀNH CÔNG — không có nhánh lỗi nào tự ghi `ErrorCode` receipt.
- `internal/app/safesettings/startup.go`: `ResolveEffective` là hàm THUẦN, không tự đọc `os.Environ()`/
  `os.Args` — nhận `StartupOverrides` đã resolve sẵn từ caller (giống `config.FromEnv` nhận `lookup func`).
  `Effective`/`FieldSource`/`StringFieldEffective`/`DurationFieldEffective`/`IntFieldEffective` là type ở TẦNG
  APP (`internal/app/safesettings`), không phải domain — nhầm lẫn ban đầu (import từ `internal/domain/
  safesettings` thay vì app package) bị `go build` bắt ngay, sửa lại bằng alias `safesettingsapp`.
- `internal/domain/safesettings/safesettings.go`: `UnmarshalJSON` tự có `json.Decoder.DisallowUnknownFields()`
  — nghĩa là decode TRỰC TIẾP request body vào `safesettings.SafeSettings` (domain type) qua
  `httpapi.CanonicalizeJSON` đã đủ strict, không cần một wire DTO riêng có thể trôi dần khỏi allowlist thật.
  Doc comment của type này nhấn mạnh nhiều lần `ProviderCredentialRef` là REFERENCE id, "never the actual
  secret value" — nhưng task's own Verify line vẫn đòi "a secret-valued field is never echoed back in
  cleartext" cho field này, tức là một yêu cầu defense-in-depth ở tầng HTTP, không mâu thuẫn với domain's own
  "it's just a reference" — chỉ là API boundary chọn mask nó dù domain không bắt buộc.
- `internal/app/redact/redact.go`: `Matcher.Tagged(sensitivity, s)` — "the structural counterpart to String/
  Value's content-based matching, for a caller that knows a field's role... before any real secret value is
  known" — khớp chính xác nhu cầu "trường này LUÔN LUÔN là credential reference, mask vô điều kiện" chứ không
  cần field đó có khớp một secret value đã biết trước. `internal/app/config/dump.go` (`config.Dump`) là
  precedent gần nhất cho "route một value qua `redact.Matcher` trước khi expose ra ngoài", dù bản thân
  `config.Dump` chưa có call site HTTP nào — không tái dùng trực tiếp được (Config khác SafeSettings hoàn
  toàn) nhưng xác nhận đúng pattern.
- `internal/delivery/httpapi/receiptreplay.go` (`WriteReceiptReplay`): ghi `receipt.ResultJSON` **verbatim**.
  `ResultJSON` được `UpdateSafeSettings` tự `json.Marshal(result)` với `result` là `SafeSettingsResult` thuần —
  KHÔNG có field `Effective`, và `ProviderCredentialRef` bên trong ở dạng CLEARTEXT (transaction ghi receipt
  không biết gì về masking convention của tầng HTTP). Đây là một cạm bẫy thật: nếu handler PUT dùng
  `WriteReceiptReplay` y hệt `internal/delivery/httpapi/workitem`'s `replayOrProceed`, một replay PUT sẽ (a)
  lộ credential ref thật trong response và (b) trả về shape khác (thiếu `effective`) so với response tươi —
  vi phạm cả "never echoed back in cleartext" lẫn "same result" cho true replay.
- `internal/delivery/httpapi/workitem/{envelope,errors,routes,scope_expansion_commands}.go`: mẫu chuẩn cho 1
  endpoint package — `prepareUpdateCommand` (Idempotency-Key + If-Match bắt buộc, `VersionFromETag`,
  `SemanticHash`), `replayOrProceed` (lookup → reconcile → replay/conflict), tách 412 (stale If-Match, tự
  reload rồi so version TRƯỚC dispatch) khỏi 409 (`ports.ErrOptimisticConflict`, race thật lọt qua pre-check) —
  2 status code khác nhau cho 2 tình huống khác nhau, không gộp chung.
- `cmd/aw/serve.go` (đọc toàn bộ trước khi sửa): xác nhận lại phát hiện của V6-10G — **grep `config.Load(` ra 0
  kết quả trong `cmd/aw`** — `aw serve` không hề parse config file, không đọc env cho bất kỳ field nào trong 7
  field allowlist, chỉ có 2 flag ad hoc `--db`/`--artifact-root`. Đã có sẵn
  `checker.Register("safe_settings", func(ctx) error { return uow.WithReadOnly(...); tx.SafeSettings().Get(ctx) })`
  từ V6-10G — nghĩa là 1 row corrupt phải fail READINESS (503), không phải fail STARTUP của `aw serve`.
- `cmd/aw/serve_test.go` (`TestServe_ReadyFailsIfSafeSettingsCorrupt`): corrupt row TRƯỚC khi `serve()` chạy,
  rồi assert server VẪN start (`waitForServeAddress` đọc được address trên stdout) và `/health/ready` trả 503
  named `safe_settings` — đọc test này SAU KHI đã viết code mới bắt ngay một regression tự gây ra (xem Quyết
  định #7).

### Quyết định

1. **PUT decode trực tiếp vào `safesettings.SafeSettings` (domain type) qua `httpapi.CanonicalizeJSON`**,
   không tạo wire DTO riêng. `UnmarshalJSON` của chính type đó đã strict (`DisallowUnknownFields`) — tái dùng
   một điểm decode duy nhất, đúng tinh thần V6-10G's own quyết định #2, tránh một struct wire thứ hai có thể
   trôi khỏi allowlist 7 field theo thời gian.
2. **Validate (`safesettings.Validate`) chạy PRE-DISPATCH ở handler**, trước khi build command envelope/hash —
   không dựa vào `UpdateSafeSettings`'s own internal re-validate để trả lỗi cho client, vì lỗi đó là plain
   `error` (không phải `*apperror.Error`) nên `httpapi.WriteAppError` sẽ rơi vào nhánh 500 mặc định. Validate
   ở HTTP layer cho phép trả đúng 400 với message thật, và làm cho nhánh Validate lỗi bên trong
   `UpdateSafeSettings` trở thành defense-in-depth thuần túy (không bao giờ thật sự kích hoạt qua request hợp
   lệ) — mirror đúng "field-presence guards ... always re-validated by handler before dispatch" convention của
   `workitem`.
3. **`ProviderCredentialRef` luôn bị mask bằng `matcher.Tagged(redact.Secret, ref)` ở MỌI response** (desired
   VÀ effective, GET/PUT/replay như nhau) — dù domain layer khẳng định đây "chỉ là reference, không phải
   secret value". Đây là quyết định defense-in-depth có chủ đích ở API boundary, đọc theo đúng nghĩa đen câu
   Verify "a secret-valued field is never echoed back in cleartext in any response" — field này là field DUY
   NHẤT trong 7 field mang tính chất credential nên là ứng viên duy nhất hợp lý. Dùng `Tagged` (structural tag
   theo VAI TRÒ field) chứ không phải `String`/content-match (không cần field đó trùng một secret value đã
   biết trước mới bị mask) — một `redact.Matcher{}` rỗng vẫn mask field này hoàn toàn.
4. **Một replay thành công KHÔNG gọi `httpapi.WriteReceiptReplay` trực tiếp** (khác mọi endpoint khác trong
   repo) — thay vào đó decode `receipt.ResultJSON` ngược lại thành `SafeSettingsResult`, rồi render lại qua
   CÙNG hàm `buildResponse` handler tươi dùng. Lý do (xem Nghiên cứu): `ResultJSON` gốc chứa credential ref
   cleartext và thiếu `effective` — dùng thẳng sẽ vi phạm "never cleartext" và làm response shape trôi giữa
   fresh/replay. Nhánh lỗi (`receipt.ErrorCode != ""`) vẫn dùng `WriteReceiptReplay` bình thường vì không
   mang secret data — giữ nguyên uniform behavior ở nhánh không nhạy cảm.
5. **Pre-dispatch version check** (reload `GetSafeSettings`, so `current.Version` với `expectedVersion` TRƯỚC
   khi dispatch) → 412 nếu lệch — tách khỏi race thật lọt qua pre-check (`ports.ErrOptimisticConflict` từ CAS
   thật bên trong `UpdateSafeSettings`) → 409. Mirror chính xác phân biệt 412-vs-409 của `workitem`'s
   `ApproveScopeExpansion`: 412 là "client biết rõ mình cũ", 409 là "client đúng lúc check nhưng thua race
   thật".
6. **`Effective` được composition root (`cmd/aw/serve.go`) tính ĐÚNG MỘT LẦN lúc boot**, truyền vào
   `httpsafesettings.Dependencies.Effective` như một giá trị tĩnh, KHÔNG bao giờ tính lại per-request. Vì
   Nghiên cứu xác nhận `aw serve` hiện tại **không có** flag/file/env nào cho 7 field allowlist (0 call site
   `config.Load`), `file`/`env`/`flags` truyền vào `ResolveEffective` đều là `StartupOverrides{}` rỗng trung
   thực — chỉ tầng `defaults` và `sqlite` (đọc qua `GetSafeSettings` ngay lúc boot) là thật sự "live" trong
   composition root hôm nay. Một task tương lai thêm flag/file/env parsing thật cho 7 field chỉ cần điền 2
   `StartupOverrides{}` đó — shape của call này không đổi. Vì Alpha không có hot-reload, giá trị chụp một lần
   lúc boot mô tả đúng "cái process đang chạy này thực sự dùng gì" suốt vòng đời — kể cả sau khi một PUT sau
   đó đổi `Desired` sống, `Effective` vẫn giữ nguyên (đúng là điều `restartRequired` tồn tại để báo).
7. **Regression tự phát hiện và sửa ngay trong task này**: bản đầu tiên viết `safeSettingsAtBoot, err :=
   safesettings.GetSafeSettings(ctx, uow); if err != nil { return err }` — làm `aw serve` FAIL STARTUP hẳn nếu
   row `safe_settings` bị corrupt, phá `TestServe_ReadyFailsIfSafeSettingsCorrupt` (test này đòi server VẪN
   start, chỉ `/health/ready` báo 503). Sửa: bỏ qua lỗi (`safeSettingsAtBoot, _ := ...`) — an toàn vì
   `GetSafeSettings`'s own source không bao giờ gán `result` trước khi return lỗi, nên `safeSettingsAtBoot`
   đã tự động là `SafeSettingsResult{}` zero-value đúng lúc lỗi xảy ra, và `ResolveEffective` đã tự document
   "khi sqlite là zero, tầng SQLite không đóng góp gì" — corruption vẫn được báo đúng qua readiness check đã
   có sẵn từ V6-10G, `Effective` chỉ đơn giản fallback về defaults/file/env/flags như chưa từng có SQLite.
8. `maxBodyBytes = 1 << 16` (64 KiB, nhỏ hơn `workitem`'s 1 MiB) — desired document chỉ có 7 field scalar,
   không có list/array nào cần không gian lớn.
9. Không migration mới — xác nhận `git log origin/master --oneline -3` và
   `internal/adapters/sqlite/migrations/` ngay trước khi finalize: migration cao nhất vẫn là `0037`, bảng
   `safe_settings` đã có từ V6-10G.

### Thực hiện

File mới (`internal/delivery/httpapi/safesettings`, package mới):

- `dependencies.go`: `Dependencies{UnitOfWork, IDs, Clock, Matcher redact.Matcher, Effective
  safesettingsapp.Effective}` — doc comment giải thích đầy đủ lý do `Effective` là giá trị tĩnh chụp lúc boot.
- `dto.go`: `desiredWire`/`maskedDesiredWire` (mask `ProviderCredentialRef`), `stringFieldWire`/
  `durationFieldWire`/`intFieldWire` (camelCase JSON mirror của `StringFieldEffective`/…, cố ý bỏ field
  `Desired` bên trong để tránh lặp lại dữ liệu đã cleartext-safe ở `desiredWire`), `effectiveWire`,
  `responseDTO`, `buildResponse`.
- `queries.go`: `handleGetSafeSettings` — plain read, gắn `ETag` từ `Version`.
- `commands.go`: `handleUpdateSafeSettings` (decode/validate/hash/replay/version-check/dispatch/encode) +
  `replayOrProceed` (phiên bản riêng, không tái dùng `workitem`'s bản — xem Quyết định #4).
- `errors.go`: `writeCommandError` (`ErrOptimisticConflict`→409, `ErrReceiptConflict`→409,
  `ErrNotInstallationScoped`→500 defensive), `writeValidationError`, `writeReceiptHashConflict`,
  `writePreconditionFailed`.
- `routes.go`: `RegisterRoutes` — `GET/PUT /settings/safe`, cả hai `ScopeKind: httpapi.ScopeInstallation`.
- `safesettings_test.go`: 12 test HTTP thật (xem Test).

File sửa:

- `cmd/aw/serve.go`: import `internal/app/safesettings` (bare) và
  `httpsafesettings "internal/delivery/httpapi/safesettings"` (alias theo đúng convention `httpcatalog`/
  `httpdefinitions`/`httpmessage` đã có — tránh đụng identifier `safesettings` dùng chung); tính
  `safeSettingsEffective` đúng một lần ngay sau khối `checker.Register("safe_settings", ...)`; thêm
  `httpsafesettings.RegisterRoutes(routes, httpsafesettings.Dependencies{UnitOfWork: uow, IDs:
  idsource.Random{}, Clock: clock.System{}, Matcher: matcher, Effective: safeSettingsEffective})` cạnh các
  `RegisterRoutes` khác — tái dùng ĐÚNG `matcher` process-lifetime đã có (không tạo matcher thứ hai), đúng
  precedent `httpmessage` đã thiết lập.

### Test

- `go build ./...`, `go vet ./...`: sạch toàn bộ module.
- `go test ./internal/delivery/httpapi/safesettings/... -v`: 12 test, tất cả PASS —
  `TestRegisterRoutes_ExposesExactlyGetAndUpdate` (đúng 2 operationId, cả 2 `ScopeInstallation`),
  `TestGetSafeSettings_FreshDatabase_HTTP` (version 1, restartRequired false, ETag `"1"`),
  `TestUpdateSafeSettings_HappyPath_HTTP` (version 2, restartRequired true, credential ref đã mask, ETag
  `"2"`), `TestUpdateSafeSettings_UnknownFieldRejected_HTTP` (field lạ `databasePath` → 400),
  `TestUpdateSafeSettings_MalformedJSONRejected_HTTP` (JSON hỏng cú pháp → 400),
  `TestUpdateSafeSettings_InvalidValueRejected_HTTP` (retention `"0s"` → 400, version không đổi),
  `TestUpdateSafeSettings_MissingHeaders_HTTP` (thiếu Idempotency-Key hoặc If-Match → 400),
  `TestUpdateSafeSettings_IfMatchMismatch_HTTP` (If-Match `"99"` trên version 1 → 412),
  `TestUpdateSafeSettings_ReplaySameKeySameHash_HTTP` (retry y hệt → cùng version/restartRequired, vẫn mask,
  raw bytes không chứa secret, version thật không tăng lần 2),
  `TestUpdateSafeSettings_ReplayDifferentHashConflicts_HTTP` (cùng key khác body → 409),
  `TestSafeSettings_EffectiveFrozenAtBoot_AndStartupSourceMasking_HTTP` (seed SQLite thật trước khi "boot",
  env override 1 field → `source`/`maskedByStartupSource` đúng tên "environment", field không override → 
  "sqlite"/rỗng; PUT thay đổi `Desired` sống sau đó → `Effective` giữ nguyên bit-for-bit, chứng minh "chụp một
  lần lúc boot" là hành vi thật chứ không chỉ lời hứa trong doc comment),
  `TestSafeSettings_ProviderCredentialRef_NeverInRawResponseBytes_HTTP` (scan RAW bytes, không chỉ field đã
  decode, cho cả PUT tươi/GET/PUT replay — chính là con đường mà nếu tái dùng `WriteReceiptReplay` trực tiếp
  sẽ bị lộ).
- `go test ./cmd/aw/... -v`: toàn bộ suite pass, bao gồm `TestServe_ReadyFailsIfSafeSettingsCorrupt` — xác
  nhận sửa Quyết định #7 đúng, server vẫn start bình thường khi row corrupt, chỉ readiness báo lỗi.
- `go test ./internal/archtest/...`: pass — `TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt` (walk đệ quy
  `internal/delivery/httpapi`, tự động cover package mới, xác nhận package này không tự ghi receipt ở đâu).
- `go test ./... -count=1`: 1 fail duy nhất, `TestEndToEnd_Restart_RemainingJobCompletesAndSetReachesReady`
  (`internal/app/workspaceprovision`) — lỗi `unlinkat ... process cannot access the file` (Windows file-lock
  cleanup race) + "first WORKSPACE_PROVISION job was never completed" (timing). Xác nhận KHÔNG liên quan diff
  này: `git diff --stat origin/master -- internal/app/workspaceprovision internal/app/workerpool` ra rỗng
  (task này không đụng file nào trong 2 package đó), và chạy lại riêng lẻ
  (`go test ./internal/app/workspaceprovision/... -run TestEndToEnd_Restart_RemainingJobCompletesAndSetReachesReady -count=1`)
  PASS ngay — xác nhận flake môi trường dưới tải song song, không phải regression thật.

### Verify

- "strict unknown-field rejection": `TestUpdateSafeSettings_UnknownFieldRejected_HTTP` — field
  `databasePath` (1 trong 4 field bị cấm tường minh của V6-10G) bị `safesettings.SafeSettings.UnmarshalJSON`'s
  `DisallowUnknownFields` chặn qua `httpapi.CanonicalizeJSON` → 400, đúng convention `DecodeJSON` mọi endpoint
  V6-02A khác đã dùng.
- "stale/replay": `TestUpdateSafeSettings_IfMatchMismatch_HTTP` (412), `TestUpdateSafeSettings_
  ReplaySameKeySameHash_HTTP` (cùng key+hash → cùng version/shape, version thật không tăng lần 2),
  `TestUpdateSafeSettings_ReplayDifferentHashConflicts_HTTP` (cùng key khác hash → 409).
- "restart/mask": `TestUpdateSafeSettings_HappyPath_HTTP` (restartRequired true sau update thật) +
  `TestSafeSettings_EffectiveFrozenAtBoot_AndStartupSourceMasking_HTTP` (env mask sqlite đúng tên source,
  Effective đứng yên qua một PUT sống — cả hai nghĩa của "mask" trong task này đều có bằng chứng: mask theo
  startup-source VÀ mask theo secret-field).
- "secret/redaction goldens": `TestSafeSettings_ProviderCredentialRef_NeverInRawResponseBytes_HTTP` — scan
  raw bytes (không chỉ field đã parse) trên cả 3 đường response (PUT tươi, GET, PUT replay) — đúng đường có
  nguy cơ lộ thật nếu implementation ngây thơ tái dùng `WriteReceiptReplay`.
- "Không làm — handler no config/file/env/secret access": grep `internal/delivery/httpapi/safesettings/*.go`
  xác nhận không có import `os`, không đọc file, không đọc env — `Effective` chỉ là giá trị đã resolve sẵn
  truyền vào qua `Dependencies`, không có logic resolve nào nằm trong package này.
- "Hoàn thành khi — UI có thể sửa đúng field bị khoá và luôn biết có cần restart": response luôn có
  `restartRequired` (forward từ `SafeSettingsResult`) và `effective` (per-field source/mask) cùng lúc, PUT
  reject bất kỳ field nào ngoài 7-field allowlist bằng chính cơ chế decode, không có generic passthrough nào.
- Wiring thật vào `cmd/aw/serve.go`: `grep -n "httpsafesettings\." cmd/aw/serve.go` xác nhận
  `httpsafesettings.RegisterRoutes(...)` thật sự được gọi (dòng ~327), không chỉ tồn tại ở test package-level
  — đúng lo ngại doctrine nêu từ vụ V6-04 ban đầu.

### Kết quả

Package mới `internal/delivery/httpapi/safesettings` (6 file production + 1 file test, 12 test function, real
HTTP + real sqlite). 2 route HTTP thật lần đầu tồn tại: `GET/PUT /settings/safe`, cả hai installation-scoped.
Phần khó nhất của task — `Effective` không có sẵn từ `GetSafeSettings` — giải quyết bằng cách để
`cmd/aw/serve.go` tự tính `ResolveEffective` đúng một lần lúc boot (file/env/flags đều là `StartupOverrides{}`
rỗng trung thực vì `aw serve` chưa từng có plumbing đó, một phát hiện xác nhận lại đúng gap V6-10G đã ghi
nhận) và truyền xuống như giá trị tĩnh — không có logic resolve nào nằm trong HTTP handler, đúng "Không làm".
Một regression tự gây ra (corrupt safe-settings row làm `aw serve` fail startup thay vì chỉ fail readiness) bị
`TestServe_ReadyFailsIfSafeSettingsCorrupt` bắt ngay trong lúc chạy suite đầy đủ và được sửa trước khi mở PR.
`go build/vet/test ./...` sạch trên toàn bộ ~90 package, 1 fail duy nhất gặp phải
(`TestEndToEnd_Restart_RemainingJobCompletesAndSetReachesReady`, `internal/app/workspaceprovision`) xác nhận
là flake môi trường (Windows file-lock cleanup race dưới tải song song) không liên quan diff — pass ngay khi
chạy lại riêng lẻ, package đó không nằm trong bất kỳ file nào task này sửa.

## V6-06B — Run detail, graph và timeline endpoints

### Bối cảnh

V6-06B phụ thuộc V6-00/V6-02A/V6-06 — cả 3 đã merge từ lâu, xác nhận qua `git log origin/master --oneline -3`
tại thời điểm bắt đầu: HEAD `c8163d3` (PR #50, V6-07B), trước đó `43cf0ca` (PR #49, V6-10J), `2cf88db`
(PR #48, V6-10H) — nhánh `feat/v6-06b-run-detail-graph-timeline` branch thẳng từ `c8163d3`. Trích nguyên văn
spec (`docs/design/08-v6-api-projections.md` dòng 262-272, không diễn giải lại): "Mục tiêu: cung cấp
authoritative Run detail và bounded graph/timeline cho V7... Phạm vi: `GET /runs/{id}`, `/graph`, `/timeline`
và stable cursors... Không làm: không diagnostics/recovery mutation hoặc raw unbounded agent-event stream...
Thực hiện: trả manifest revision, nodes/edges/activations, attempt/route/retry/checkpoint, correlation/
causation, JournalPosition/freshness; cursor bind query and upper watermark... Verify: fork/join/rework
fixtures, paging qua write, bounds and redaction... Hoàn thành khi: Graph/Timeline screen không cần đọc DB/
event journal trực tiếp." Nguồn: ADR-018, AK-ARCH-025, HE-11-M02, HE-11-M03.

Đây là task đầu tiên trong repo thật sự lắp ráp một Run's own detail/graph/timeline từ raw repository
primitives — không có application-layer query nào có sẵn làm việc này trước đó (`internal/app/runtime/
queries.go`, sản phẩm của V6-07B, chỉ phủ Evidence/Artifact/ContextSnapshot, không đụng Run/NodeRun assembly).
Đây cũng là task ĐẦU TIÊN thật sự sử dụng `httpapi.CursorCodec`/`httpapi.Bind`/`httpapi.Freshness` (V6-02A) cho
một route có phân trang thật — xác nhận bằng grep `Freshness{`/`AsOfJournalPosition` toàn repo trước khi viết
code: chỉ xuất hiện trong `freshness_test.go` của chính `httpapi`, chưa route nào dùng thật.

`baocaov6checklist.md` đang được 3 task song song khác ghi đồng thời (V6-04A, V6-06C, V6-07A theo brief) —
xung đột merge khi mở PR là bình thường, không phải bug (và thực tế đã gặp: origin/master tiến thêm 1 commit
mới, `e277d7c` PR #51 V6-10A Doctor endpoint, trong lúc task này đang được viết).

Phiên làm việc này bị gián đoạn 2 lần bởi rate-limit reset của hệ thống — lần thứ hai môi trường chuyển từ một
git worktree cô lập (`.claude/worktrees/agent-...`) sang thẳng checkout chính của repo (worktree đã bị dọn).
Hệ quả thật quan sát được: checkout chính bị 1 session khác (V6-07A, "attachment ingest/replay/orphan
recovery") dùng CHUNG, để lại working tree lẫn cả thay đổi CHƯA COMMIT của session đó (`internal/adapters/
sqlite/unitofwork.go`, `internal/app/ports/fake/unitofwork.go`, `internal/app/ports/unitofwork.go`,
`internal/delivery/httpapi/message/routes.go` bị modify; `internal/adapters/sqlite/attachment_claim_repository.go`,
`internal/adapters/sqlite/migrations/0038_attachment_prepare_claims.sql`, `internal/app/message/attachment.go`,
`internal/app/ports/attachmentclaim.go`, `internal/delivery/httpapi/message/attachment.go` chưa track) — và
`git branch --show-current` xác nhận HEAD đã bị checkout sang `feat/v6-07a-attachment-ingest-replay-orphan-recovery`
tại một thời điểm, không còn là branch của task này. Xử lý: `git checkout feat/v6-06b-run-detail-graph-timeline`
lại (mọi thay đổi working-tree, kể cả của session khác, đi theo an toàn vì 2 branch cùng base commit, không
conflict); từ đó về sau CHỈ `git add` đúng các file của task này khi commit — không đụng, không revert bất kỳ
file nào của session khác. Ghi lại đây vì đây là quan sát thật ảnh hưởng trực tiếp tới 4 test fail khi chạy
`go test ./...` (xem mục Test).

### Nghiên cứu

Đọc lại toàn bộ `ports.RuntimeRepository` (đúng 6 method brief chỉ tên: `GetWorkflowRun`,
`ListNodeRunsForRun`, `ListExecutionAttemptsForRun`, `GetExecutionManifest`, `ListRunManifestAmendments`,
`ListBranchTokensForRun`) cộng `runtime.WorkflowRun`/`NodeRun`/`ExecutionAttempt`/`ExecutionManifest`/
`RunManifestAmendment`/`BranchToken`/`Checkpoint` struct thật trong `internal/domain/runtime` trước khi đặt
bất kỳ DTO nào. 3 phát hiện thật quan trọng nhất định hình toàn bộ thiết kế:

1. **`ports.EventsRepository` chỉ có `Append` (write-only), `ports.CheckpointsRepository` chỉ có
   `InsertCheckpoint` (write-only)** — xác nhận bằng đọc `unitofwork.go` toàn bộ, không có method đọc nào cho
   cả 2 interface. Nghĩa là "JournalPosition" đúng nghĩa gốc (global `domain_events.journal_position`) KHÔNG
   có đường nào để `internal/app/runtime` package đọc lại qua `ports.Tx` — 2 method debug-only trả về
   `journal_position` thật (`sqlite.Store.ListDomainEventsForProject`, `event_queries.go`) chỉ tồn tại trên
   `*sqlite.Store` cụ thể, không phải `ports.Tx`, và tự doc comment nói rõ "never used by production code,
   only by V4-14's own test diagnostics" — dùng nó ở đây sẽ vi phạm thẳng "Không làm: raw unbounded agent-
   event stream" (đọc domain_events trực tiếp chính là kiểu "raw stream" đó). Kết luận: không có method mới
   nào được thêm vào `EventsRepository`/`CheckpointsRepository` để lấy JournalPosition thật — xem Quyết định
   #2 để biết field `Freshness.AsOfJournalPosition` cuối cùng mang giá trị gì thay vào đó.
2. **`NodeRun.ActivationSequence` là bộ đếm tăng dần theo TOÀN Run (không theo từng NodeKey)** — xác nhận
   bằng đọc `completion_policy.go`'s `maxActivationSequence` (dùng "highest ActivationSequence among nodeRuns
   + 1" cho MỌI reactivation, kể cả rework) và `advance.go` dòng 528 (`nextSequence := current.ActivationSequence
   + 1` cho hop bình thường). Ban đầu giả định đây là total order tuyệt đối — SAI, phát hiện thật qua test
   fail đầu tiên (xem Test): `dispatchForkBranches` (advance.go dòng 991-1104) gán mỗi branch một
   `ActivationSequence` TĂNG DẦN từ `forkRun.ActivationSequence` (branch đầu = fork+1, branch sau = fork+2,
   …), NHƯNG NodeRun của JOIN (advance.go dòng 1398) lại pin CỐ ĐỊNH vào `forkRun.ActivationSequence+1` —
   nghĩa là JOIN's own ActivationSequence có thể TRÙNG với một branch's own first step (branch đầu tiên trong
   danh sách outcome đã sort), bất kể branch nào thực tế arrive JOIN trước. Đây không phải bug — JOIN's vị trí
   cố định tương đối FORK (không phụ thuộc thứ tự hoàn thành branch nào) là đúng thiết kế HE-14-M09. Hệ quả
   trực tiếp: `ActivationSequence` KHÔNG PHẢI khoá duy nhất toàn Run — mọi sort/cursor trong task này phải có
   tie-break phụ (xem Quyết định #3).
3. **`internal/adapters/sqlite` không có cột `block_reason` nào cho bảng `node_runs`** — xác nhận bằng grep
   "block_reason"/"BlockReason" toàn `internal/adapters/sqlite`: 0 kết quả; đọc thẳng `CREATE TABLE node_runs`
   (migration 0001, dòng 129-142) xác nhận cột thật chỉ có `id/run_id/node_key/activation_sequence/iteration/
   state/selected_outcome/input_state_hash/version/created_at/updated_at` — không có trường free-text nào cho
   lý do BLOCKED. Phát hiện qua chính test redaction của task này tự viết (xem Test) — dùng sqlite thật thì
   `BlockReason` luôn rỗng bất kể set gì, chỉ `fake.UnitOfWork` (round-trip toàn bộ struct trong bộ nhớ) mới
   quan sát được giá trị thật. Đây là gap có thật của tầng persistence, không phải bug của task này — ghi rõ
   trong doc comment `NodeActivationView` (`run_detail_queries.go`) và mục Kết quả, không tự thêm migration để
   sửa (đúng brief: "This task likely needs NO new migration").

Đọc toàn bộ `internal/domain/workflow/workflow.go`: `Node{Key,Type,Outcomes,CyclePolicy,...}` và
`Edge{Key,From,Outcome,To,Kind,ReworkPolicy}` đã có json tag sẵn (không như `runtime.NodeRun`/`ExecutionAttempt`
không có tag nào) — nhưng `Node` còn mang `Agent/Command/MachineGate/Approval/Wait/Join` typed executor config
đầy đủ; quyết định KHÔNG expose nguyên struct `Node`/`Edge` mà tự định nghĩa `GraphNodeView`/`GraphEdgeView`
trimmed (chỉ Key/Type/Outcomes/CyclePolicy và Key/From/Outcome/To/Kind/ReworkPolicy) — route này là "graph
shape" (structural), không phải một surface thứ hai cho definition-authoring detail mà V6-05 đã sở hữu.
`EdgeKind`: `EdgeFlow`("")/`EdgeCompletionRework` — `PossibleEdges` trả CẢ HAI loại (Kind phân biệt), không tự
lọc REWORK ra vì đó cũng là một "possible edge" thật của compiled document, chỉ khi tính `TakenEdges` mới loại
`COMPLETION_REWORK` (một NodeRun không bao giờ "route theo" một REWORK edge — REWORK chỉ được resolve bởi
`EvaluateCompletionCandidate`, không phải `AdvanceRun`'s `findEdge`).

Đọc `docs/harness-engineering/11-lec-11-observability-noi-tai.md` cho đúng 2 nguồn HE-11-M02/M03 spec trích
dẫn: "HE-11-M02 — Trace identity: mọi event MUST liên kết được tới WorkItem, WorkflowRun, NodeRun, Attempt và
actor/runner phù hợp" và đặc biệt **"HE-11-M03 — Correlation/causation: transition, tool call, gate và retry
MUST có correlation/causation IDs HOẶC SEQUENCE TƯƠNG ĐƯƠNG"** — cụm "hoặc sequence tương đương" (đọc kỹ, không
bỏ sót) chính là căn cứ cho phép dùng `ActivationSequence` làm "correlation/causation ID equivalent" thay vì
phải có field `CorrelationID`/`CausationID` thật trên `NodeRun`/`ExecutionAttempt` (2 struct này không có field
nào như vậy — field `CorrelationID` duy nhất tồn tại trong domain runtime nằm ở `ContextMessage`, một khái
niệm khác hẳn, không liên quan). Đọc `docs/architecture/03-system-architecture.md` dòng 639-640 cho AK-ARCH-025
("Trace phân biệt được provider failure, workspace failure, verifier failure và infrastructure failure") —
xác nhận `TerminationReason`/`FailureCode` (đã có sẵn trên `ExecutionAttempt`, đóng, không cần taxonomy mới)
đã đủ cho yêu cầu "phân biệt loại failure", không cần route này tự phát minh thêm phân loại. Đọc
`docs/architecture/02-architecture-decisions.md` ADR-018 ("UI Alpha: source/log có, interactive terminal chưa
có") — xác nhận đây là căn cứ cho "Không làm: raw unbounded agent-event stream" (không terminal-style live
stream), không phải một ràng buộc riêng cho route này ngoài việc củng cố lại quyết định đã có ở trên.

Đọc `internal/delivery/httpapi/message/list.go` (V6-07, `handleListMessages`) toàn bộ — precedent DUY NHẤT
trong repo thật sự dùng `httpapi.CursorCodec`/`httpapi.Bind`/`Fingerprint`/`UpperWatermark` cho một route phân
trang: xác nhận nguyên tắc "app-layer trả FULL kết quả đã sort, HTTP layer tự cắt trang trong bộ nhớ, không
thêm SQL query mới" — áp dụng y hệt cho task này (Quyết định #1). Đọc `internal/delivery/httpapi/run/cancel.go`
(V6-06) xác nhận pattern route Run-scoped-bằng-RunID-only, không có `{projectId}` trên path, tự reload Run rồi
suy ra ProjectID từ chính row đó thay vì tin path — áp dụng y hệt cho cả 3 route mới.

Điều tra khả năng dựng fixture rework thật (không hand-seed, đúng yêu cầu Verify): đọc
`internal/app/runtime/completion_policy_test.go`'s `documentWithReworkEdge`/`requiredEvidenceCompletionPolicy`/
`completionCandidateFixture` — xác nhận `EvaluateCompletionCandidate` (production command thật) tạo một
NodeRun REWORK activation THẬT (Iteration = priorMax+1, ActivationSequence = maxActivationSequence+1,
KHÔNG set `ReactivationReason` — field đó chỉ dành cho SCOPE_EXPANDED, đọc thẳng `completion_policy.go` dòng
471-482 xác nhận). `completionCandidateFixture` gốc dùng `fake.UnitOfWork` + `uow.Snapshot.Definitions().(*fake.
DefinitionsRepository).Seed(...)` (cơ chế chỉ tồn tại trên fake) — task này cần chạy được trên SQLite thật, nên
tự viết lại đúng chuỗi compile+publish bằng `definitions.PublishDefinitionVersion` (production command thật,
đã có sẵn helper `publishPolicyVersion` dùng chung fake/sqlite trong `schedule_test.go`) thay vì `.Seed()`. Gặp
1 lỗi thật khi thử lần đầu (bỏ qua bước gắn `DependencyManifest` pin khớp `CompletionPolicyRef`): resolveCompletionPolicy
(completion_policy.go dòng 535-587) đối chiếu `document.CompletionPolicyRef` với đúng
`manifest.DependencyManifest.Pins` (Kind/Key/Version/Hash phải khớp CHÍNH XÁC) TRƯỚC khi load policy thật — bỏ
qua bước này cho lỗi `COMPLETION_POLICY_UNRESOLVABLE` thay vì REWORK; sửa bằng cách đọc lại `CompiledHash`
thật qua `tx.Definitions().LoadVersion` rồi build `DependencyManifest.Pins` khớp tay trước khi
`PublishWorkflowVersion` (không dùng `publishWorkflowVersionDocument` có sẵn — helper đó hard-code một pin
"skill" không liên quan, không có chỗ cho policy pin thật).

### Quyết định

1. **Query app-layer trả FULL kết quả đã sort cho cả Run, KHÔNG tự phân trang trong `internal/app/runtime`** —
   `GetRunDetail`/`GetRunGraph`/`GetRunTimeline` (file mới `run_detail_queries.go`, đặt cạnh `queries.go` của
   V6-07B đúng chỉ dẫn brief) không nhận tham số `limit`/`cursor` nào, không import `internal/delivery/httpapi`
   (tránh đảo ngược layering delivery→app). Toàn bộ cursor/Bind/Fingerprint/Freshness chỉ sống trong
   `internal/delivery/httpapi/rundetail` — mirror y hệt `handleListMessages` (Nghiên cứu).
2. **`Freshness.AsOfJournalPosition` KHÔNG mang global `journal_position` thật (không có đường lấy, xem Nghiên
   cứu #1) — tái dùng đúng wire field đó để mang `ActivationSequence` cao nhất của Run tại thời điểm đọc.**
   `Freshness.Status` LUÔN `LIVE` (route đọc thẳng bảng nguồn thật, không qua projection có độ trễ nào —
   không có khái niệm DEGRADED/STALE ở đây), `Generation` LUÔN `0` (không có V6-08 projection-generation nào
   áp dụng). Đây là tái sử dụng CÓ Ý THỨC một wire field đã định nghĩa cho một ngữ nghĩa hẹp hơn ngữ cảnh gốc
   của nó (per-Run thay vì global) — ghi rõ lý do đầy đủ trong doc comment của cả `run_detail_queries.go` lẫn
   `rundetail.go` để không ai đọc nhầm giá trị này là journal_position toàn hệ thống.
3. **Cursor key là COMPOSITE, không phải `ActivationSequence` đơn** — do phát hiện tie thật (Nghiên cứu #2).
   `/graph` dùng khoá 2 phần `"<activationSequence>:<nodeRunId>"` (`encodeActivationKey`/`afterActivation`,
   `cursorkeys.go`), NodeRunID làm tie-break xác định (cùng thứ tự `sortedNodeRuns` đã dùng để sort). `/timeline`
   cần khoá 3 phần `"<activationSequence>:<nodeRunId>:<subOrder>"` — `subOrder` = 0 cho entry NODE_RUN, =
   `AttemptNumber` cho entry EXECUTION_ATTEMPT (AttemptNumber bắt đầu từ 1 nên NODE_RUN luôn đứng trước chính
   attempt của nó). `CursorState.ProjectID` để trống ("") có chủ đích cho cả 2 route — route này không có
   `{projectId}` trên path (Nghiên cứu, mirror `run/cancel.go`), và `QueryFingerprint` (đã băm RunID) một mình
   đã đủ chặn replay cursor của Run A lên Run B (test `TestGetRunGraph_HTTP_CursorFromAnotherRun_ResyncRequired`
   xác nhận thật, không chỉ suy luận).
4. **Tách rõ Graph (structural) khỏi Timeline (chronological) — không gộp chung 1 shape.** `/graph` trả
   `Nodes`/`PossibleEdges` (đầy đủ, không phân trang — bounded tự nhiên theo document đã compile) +
   `Activations` (phân trang theo NodeRun) + `TakenEdges` (suy ra CHỈ từ đúng trang Activations hiện tại,
   KHÔNG tính trong app-layer vì "trang nào" là khái niệm HTTP-layer, xem Quyết định #1) + `BranchTokens` (đầy
   đủ, nhỏ tự nhiên theo độ rộng FORK). `/timeline` trả 1 feed phẳng, đã trộn 2 loại entry (`NODE_RUN`/
   `EXECUTION_ATTEMPT`, tagged union `TimelineEntryKind`) theo đúng thứ tự thời gian — NODE_RUN mang nửa
   "route" (SelectedOutcome, đúng cho cả ROUTER/FORK/JOIN không bao giờ có Attempt), EXECUTION_ATTEMPT mang
   nửa "attempt/retry/checkpoint" (AttemptNumber/TerminationReason/FailureCode/LastCheckpointID/
   ContextSnapshotID). Ánh xạ này khớp đúng cách brief tự chia field theo route: "manifest revision" →
   `/runs/{id}`; "nodes/edges/activations" → `/graph`; "attempt/route/retry/checkpoint" → `/timeline`.
5. **BlockReason redact qua `redact.Matcher.String` (exact-match, không phải scan substring) truyền vào TỪ app-
   layer** (`GetRunGraph(ctx, uow, matcher, runID)`/`GetRunTimeline(...)`) — không redact ở tầng HTTP. Route
   `getRunDetail` không cần `Matcher` vì `RunDetail` không mang field free-text nào (cố ý loại `SharedState`
   khỏi response — xem điểm 6). Đây là field free-text DUY NHẤT `NodeRun` mang (mọi field khác là ID/enum/số).
6. **`RunDetail` KHÔNG expose `WorkflowRun.SharedState`** — field JSON tự do do caller khai báo, không nằm
   trong bất kỳ dòng "Thực hiện" nào của spec, và cần một chính sách redact riêng route này chưa có căn cứ
   thật để thiết kế (không có caller thật nào cần) — loại bỏ bằng construction, mirror đúng "Locator never a
   field" discipline `ArtifactSummary` (V6-07B) đã lập.
7. **Không thêm migration nào.** Xác nhận migration cao nhất trên `origin/master` tại thời điểm branch
   (`c8163d3`) là `0037` — mọi cột task này cần (kể cả cột KHÔNG có, như `block_reason` — Nghiên cứu #3) đã
   tồn tại hoặc không tồn tại từ trước, không phải việc của task này thêm.

### Thực hiện

- `internal/app/runtime/run_detail_queries.go` (file mới, ~470 dòng): `ExecutionManifestDetail`/
  `toExecutionManifestDetail`, `RunManifestAmendmentView`/`toRunManifestAmendmentView`, `RunDetail`/
  `GetRunDetail`; `GraphNodeView`/`toGraphNodeViews`, `GraphEdgeView`/`toGraphEdgeViews`, `BranchTokenView`/
  `toBranchTokenViews`, `NodeActivationView`/`toNodeActivationView`, `sortedNodeRuns` (tie-break NodeRunID —
  Quyết định #3), `RunGraph`/`GetRunGraph`; `TimelineEntryKind`/`TimelineEntryView`/`nodeRunToTimelineEntry`/
  `attemptToTimelineEntry`/`buildTimelineEntries`, `RunTimeline`/`GetRunTimeline`.
- `internal/app/runtime/run_detail_queries_sqlite_test.go` (file mới, 6 test — real sqlite, reuse 100% fixture
  có sẵn trong package `runtime_test`, không tự viết fixture mới nào cho fork/join/rework).
- `internal/delivery/httpapi/rundetail/` (package HTTP mới, 6 file production):
  - `rundetail.go`: doc comment đầy đủ (route inventory, lý do không `{projectId}`, lý do Freshness tái dùng)
    + `Dependencies{UnitOfWork, Matcher, Cursor}` + `RegisterRoutes` (3 `RouteDescriptor`, `ScopeKind:
    httpapi.ScopeProject`).
  - `errors.go`: `writeQueryError`/`writeValidationError` (mirror y hệt `evidence`/`message`).
  - `detail.go`: `handleGetRunDetail`.
  - `graph.go`: `graphQueryFingerprint`, `takenEdgeView`/`deriveTakenEdges` (Quyết định #4), `runGraphResponse`,
    `handleGetRunGraph`.
  - `timeline.go`: `timelineQueryFingerprint`, `timelineSubOrder`, `runTimelineResponse`,
    `handleGetRunTimeline`.
  - `cursorkeys.go`: `encodeActivationKey`/`decodeActivationKey`/`afterActivation` (2 phần),
    `encodeTimelineKey`/`decodeTimelineKey`/`afterTimelineKey` (3 phần) — Quyết định #3.
- `cmd/aw/serve.go`: thêm import `httprundetail "internal/delivery/httpapi/rundetail"`; gọi
  `httprundetail.RegisterRoutes(routes, httprundetail.Dependencies{UnitOfWork: uow, Matcher: matcher, Cursor:
  cursorCodec})` ngay sau `runhttp.RegisterRoutes`, trước `decision.RegisterRoutes` — tái dùng ĐÚNG
  `matcher`/`cursorCodec` V6-07 đã construct, không tạo instance thứ hai. Xác nhận bằng grep `rundetail` trong
  chính file này thấy cả import lẫn lệnh gọi thật (không chỉ tồn tại ở test).

### Test

`internal/app/runtime/run_detail_queries_sqlite_test.go` (6 test, real sqlite):
`TestGetRunDetail_SQLite_UnknownRun_NotFound` (cả 3 hàm query), `TestGetRunDetail_SQLite_ReturnsManifestAndCounts`,
`TestGetRunGraph_SQLite_ForkJoin_ReturnsStructureAndActivations` (drive THẬT qua `StartWorkflowRun`→`AdvanceRun`
(start→fork)→`ScheduleExecutableNodeRun`+`FinalizeExecutionAttempt` cho cả 2 branch — reuse 100%
`joinPolicyDocument`/`publishJoinPolicyFixtures`/`seedRunningAndFinalizeSQLite` có sẵn từ `join_sqlite_test.go`,
không hand-seed dòng nào), `TestGetRunTimeline_SQLite_ForkJoin_OrdersActivationsAndAttempts` (cùng fixture,
assert thứ tự non-decreasing + mỗi EXECUTION_ATTEMPT luôn theo sau đúng NODE_RUN của chính nó),
`TestGetRunGraph_SQLite_Rework_CreatesNewActivationOnReworkTarget` (drive THẬT qua
`EvaluateCompletionCandidate` production command trên sqlite thật — xem Nghiên cứu cho lý do phải tự viết lại
chuỗi publish; assert đúng 2 activation "implement", Iteration 0 và 1, ActivationSequence tăng, NodeRunID thứ 2
khớp `result.ReworkNodeRunID`), `TestGetRunGraph_RedactsBlockReason` (1 test dùng `fake.UnitOfWork` có chủ đích
— xem Nghiên cứu #3 — hand-seed ĐÚNG 1 NodeRun ngoài luồng fork/join/rework, mirror discipline
`seedRunEvidence`/`seedPriorEndReach` có sẵn).

`internal/delivery/httpapi/rundetail/` (5 file test, 14 test function — real `httptest.Server` +
`httpapi.Chain(mux, BindPrincipal(...))`, mirror `internal/delivery/httpapi/run`'s `newTestServer`):
`TestGetRunDetail_HTTP_NotFound`, `TestGetRunDetail_HTTP_ReturnsDetail`, `TestGetRunGraph_HTTP_NotFound`,
`TestGetRunGraph_HTTP_ReturnsStructureAndFreshness`,
`TestGetRunGraph_HTTP_PagesAndStaysStableAcrossConcurrentWrite` (drive THẬT qua `routerChainDocument`
start→router1→router2→end — mirror chính xác `TestListMessages_PagesAndStaysStableAcrossConcurrentWrite`'s
pattern: page1 limit=1, ghi router2 THẬT giữa 2 lần gọi, page2 vẫn đúng router1 và `NextCursor` rỗng, request
mới không-cursor thấy đủ cả 3), `TestGetRunGraph_HTTP_InvalidCursor_Returns400`,
`TestGetRunGraph_HTTP_CursorFromAnotherRun_ResyncRequired` (2 Run 2 project riêng, cursor Run A dùng lên Run B
→ 409 RESYNC_REQUIRED thật), `TestGetRunTimeline_HTTP_NotFound`, `TestGetRunTimeline_HTTP_ReturnsOrderedEntries`,
`TestGetRunTimeline_HTTP_PagesAndStaysStableAcrossConcurrentWrite`, `TestGetRunTimeline_HTTP_LimitBounds`
(`limit=0`/`limit=-1` → 400 thật qua `httpapi.ResolveLimit`; `limit=100000` clamp về `MaxPageLimit`, không lỗi,
không unbounded), `TestGetRunTimeline_HTTP_InvalidCursor_Returns400`,
`TestGetRunGraphAndTimeline_HTTP_RedactBlockReason` (dùng `fake.New()` — lý do y hệt test app-layer tương ứng,
Nghiên cứu #3 — assert cả 2 response route KHÔNG lộ secret verbatim, đúng `[REDACTED]`).

`go build ./...`, `go vet ./...` sạch trên toàn repo. `go test ./internal/app/runtime/... -run
"TestGetRunDetail|TestGetRunGraph|TestGetRunTimeline" -v`: 6/6 xanh; `go test ./internal/app/runtime/...` đầy
đủ (không chỉ test mới): xanh, không regression trên bất kỳ test cũ nào (82.7s). `go test
./internal/delivery/httpapi/rundetail/... -v`: 14/14 xanh. `go test ./cmd/aw/...`: xanh (composition root
không panic khi register route mới — xác nhận không trùng `(Method,Path)` hay `OperationID` với route nào có
sẵn). `go test ./...` full suite: 4 fail gặp phải, điều tra riêng từng cái, KHÔNG cái nào do task này:
- `internal/adapters/sqlite`: `TestUnitOfWork_WithReadOnly_DoesNotPersistWrites`/`TestOpenMigratesAndEnablesForeignKeys`/
  `TestMigrationIsIdempotent` — "migration count = 37, want 36". Xác nhận bằng `ls migrations/*.sql | wc -l`
  = 37 thật (36 gốc + `0038_attachment_prepare_claims.sql` — file của session V6-07A khác, xem Bối cảnh), còn
  test hardcode `36` chưa cập nhật — thuộc PR khác, không sửa file không phải của mình.
- `internal/delivery/httpapi/message`: `TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet` — "len
  (Descriptors()) = 4, want 3", cùng nguyên nhân (route attachment mới của session khác, `message/routes.go`
  bị modify chưa commit).
- `internal/integration/v5accept`: `TestV5AcceptArtifactTamper_RealVerifyDetectsRealCorruption` — timeout chờ
  Run đạt VERIFYING trong lúc chạy suite đầy đủ (tải hệ thống cao do chạy song song với session khác trên cùng
  máy). Chạy lại riêng lẻ (`-run` đúng tên test đó): PASS trong 3.3s — xác nhận là flake do tải môi trường,
  không phải regression thật.

### Verify

- **Fork/join/rework fixtures**: cả 3 route đều được test qua Run thật drive bằng `StartWorkflowRun`/
  `AdvanceRun`/`ScheduleExecutableNodeRun`/`FinalizeExecutionAttempt`/`EvaluateCompletionCandidate` — không
  dòng NodeRun/BranchToken/Amendment nào bị hand-seed cho các test này (2 test hand-seed CHỈ tồn tại cho đúng 1
  field-level assertion hẹp — redaction — nêu rõ lý do trong chính doc comment).
- **Paging qua write**: chứng minh bằng test thật ghi một NodeRun MỚI giữa page 1 và page 2 của CÙNG một walk
  — page 2 không bao giờ thấy activation mới đó, `NextCursor` của page cuối rỗng đúng, request mới không-cursor
  THẤY được activation mới — cho cả `/graph` lẫn `/timeline`.
- **Bounds**: `httpapi.ResolveLimit` (V6-02A có sẵn) enforce thật qua route — `limit` không hợp lệ là 400 typed,
  `limit` quá lớn clamp về `MaxPageLimit` (200) chứ không bao giờ trả response không giới hạn; `Nodes`/
  `PossibleEdges`/`BranchTokens` bounded tự nhiên theo kích thước document/độ rộng fork đã compile, không phải
  theo độ dài lịch sử Run.
- **Redaction**: `BlockReason` — field free-text duy nhất `NodeRun` mang — đi qua `redact.Matcher.String` thật
  trước khi rời `internal/app/runtime`, chứng minh bằng test thật (không chỉ code review) ở cả 2 tầng app/HTTP.
- **Hoàn thành khi — "Graph/Timeline screen không cần đọc DB/event journal trực tiếp"**: đúng bằng cấu trúc —
  `internal/app/runtime/run_detail_queries.go` không import `internal/adapters/sqlite` hay bất kỳ package event
  journal nào; mọi field trả về đến từ đúng `ports.RuntimeRepository`/`ports.DefinitionsRepository` đã scoped
  sẵn theo Run.

### Kết quả

Branch `feat/v6-06b-run-detail-graph-timeline` từ `origin/master` tại `c8163d3` (sau PR #50/#49/#48/#46).
Package app-layer mới `internal/app/runtime/run_detail_queries.go` (3 hàm query công khai `GetRunDetail`/
`GetRunGraph`/`GetRunTimeline` + 1 file test riêng, 6 test). Package HTTP mới `internal/delivery/httpapi/
rundetail` (6 file production + 5 file test, 14 test function). Không thêm method port mới, không thêm
migration mới (migration cao nhất vẫn `0037`). 3 route HTTP thật lần đầu tồn tại: `GET /runs/{id}`,
`GET /runs/{id}/graph`, `GET /runs/{id}/timeline` — grep xác nhận `httprundetail.RegisterRoutes` thật sự có
trong `cmd/aw/serve.go`, không chỉ tồn tại ở test package-level.

Phát hiện thật cần ghi lại (2 gap có thật của tầng dưới, không phải bug của task này):
1. **`ports.EventsRepository`/`ports.CheckpointsRepository` không có method đọc nào** — "JournalPosition" thật
   theo nghĩa gốc (global `domain_events.journal_position`) không có đường lấy qua `ports.Tx` cho bất kỳ
   package app-layer nào, chỉ tồn tại trên `*sqlite.Store` debug-only. `Freshness.AsOfJournalPosition` của 3
   route này vì vậy mang `ActivationSequence` cao nhất trong Run (Run-scoped), không phải global journal
   position — ghi rõ lý do trong doc comment, không giả vờ đó là cùng giá trị.
2. **`node_runs` không có cột `block_reason`** — route redact ĐÚNG field đó, nhưng trên sqlite thật giá trị
   luôn rỗng vì tầng persistence chưa từng ghi nó (xác nhận bằng grep, không suy đoán). Field/redaction đã nối
   dây đúng đầu-cuối và sẽ tự hoạt động đúng ngay khi một task tương lai thêm cột này — task này không tự thêm
   (ngoài phạm vi, brief xác nhận "This task likely needs NO new migration").

`go build/vet ./...` sạch. `go test ./internal/app/runtime/...` và `go test ./internal/delivery/httpapi/
rundetail/...` 100% xanh (20/20 test mới). `go test ./...` toàn repo: 4 fail, cả 4 đã điều tra và xác nhận
không liên quan diff của task này (3 do uncommitted work của session V6-07A khác đang chạy song song trên
CÙNG checkout, 1 do tải hệ thống — pass khi chạy riêng lẻ).
## V6-10D — Bounded source, diff và repository-log endpoints

### Bối cảnh

Trích nguyên văn spec (`docs/design/08-v6-api-projections.md` dòng 458-467, không diễn giải lại): "Mục tiêu:
map V6-10C read-only queries to HTTP"; "Phụ thuộc: V6-00, V6-01A, V6-02A, V6-10B, V6-10C"; "Phạm vi:
source/diff/repository-log GET routes and fragments"; "Không làm: handler no file open, Git spawn, revision
resolution or terminal"; "Thực hiện: dispatch exact query; map bounds/binary/truncation/cursor/media contracts";
"Verify: transport negative matrix plus architecture test no filesystem/process concrete import"; "Hoàn thành
khi: no route can write, execute or arbitrary-read local paths"; "Nguồn: ADR-018". `git log origin/master
--oneline -5` xác nhận cả 5 dependency đều đã merge tính tới `c8163d3` (V6-07B, PR #50, HEAD lúc bắt đầu):
V6-10B (`workspaceroutes.go`), V6-10C (`internal/app/workspaceinspection`, PR #31) đều thấy thật trên cây
nguồn — không có gì phải chờ thêm.

Phiên này khởi động lại đúng 1 lần giữa chừng (worktree cũ bị dọn do rate-limit của phiên trước reset) —
branch `feat/v6-10d-source-diff-repository-log-endpoints` được tạo lại từ đúng `origin/master` (`c8163d3`)
trong một worktree mới, không có gì bị mất vì chưa có file nào được ghi trước thời điểm đó (toàn bộ công việc
trước đó chỉ là đọc research). `baocaov6checklist.md` đang được ít nhất 2 task song song khác ghi đồng thời
(V6-10A, V6-10F theo brief) — xung đột merge khi mở PR là bình thường, không phải bug.

### Nghiên cứu

Đọc lại toàn bộ hạ tầng chung trước khi viết route nào: `route.go` (`RouteDescriptor`/`RouteRegistry.Register`
panic-on-duplicate, `ScopeKind`), `errors.go` (`WriteResourceHidden`/`WriteAppError`/`StatusForAppErrorCode`),
`media.go` (`ApplyContentHeaders`/`ResolveMediaDisposition`/`ParseRange`), `receiptreplay.go`'s
`EncodeResult(w, status, result, etag)`, `commandenvelope.go`'s `ETagFromVersion`, `page.go`/`cursor.go` (opaque
`CursorCodec` — xem Quyết định #6 tại sao KHÔNG dùng cho route log), `principal.go` (không cần — cả 3 route
đều read-only, không có mutation nào cần `PrincipalFromContext`).

Đọc `internal/delivery/httpapi/evidence` (V6-07B, đã merge) làm precedent gần nhất cho "content-stream với
Range/ETag/media-header/no-sniff": `artifact.go`'s `handleGetArtifactContent` xác nhận đúng convention
`httpapi.ApplyContentHeaders(w, contentType, filename)` + `ETag` = content hash + `Content-Length` — dùng lại
y hệt cho `getWorkspaceSource` (khác biệt duy nhất: `ports.SourceContent.Content` đã là `[]byte` bounded sẵn
trong bộ nhớ, không phải `io.ReadCloser` cần `io.Copy`, nên không cần `Range`/`io.Seeker` — `ByteLimit` của
chính `GetSource` đã là cơ chế bound riêng, thêm `Range` HTTP lên trên sẽ là 2 cơ chế cắt chồng lên nhau,
không rõ ràng hơn — xem Quyết định #4). Đọc `evidence/dependencies.go`/`evidence/routes.go`/`evidence/errors.go`
làm mẫu shape package con: `Dependencies` struct nhỏ do composition root tự dựng, `RegisterRoutes(reg, deps)`
một hàm duy nhất, `writeQueryError` centralize toàn bộ error-mapping.

Đọc `internal/delivery/httpapi/workspaceroutes.go`/`workspacestate.go` (V6-10B, đã merge) cho pattern route
GET gắn trên đúng `RepositoryWorkspace` resource: xác nhận path `/projects/{projectId}/repository-workspaces/
{repositoryWorkspaceId}` đã tồn tại thật (2 route GET/POST khác của V6-10B) — quyết định TÁI DÙNG đúng prefix
này cho 3 route mới thay vì tự vẽ cây URL riêng (xem Quyết định #1). Đọc
`internal/archtest/workspace_delivery_boundary_test.go` — brief của task gọi tên nó là
"TestWorkspaceHTTPNeverReachesGitOrFilesystem" nhưng tên THẬT trong cây nguồn là
`TestDeliveryWorkspaceRoutesNeverReachWorkspaceIOOrExecutor` (một sai lệch tên giữa brief và code thật, đã ghi
nhận ở đây thay vì lặp lại tên sai) — xác nhận nó walk TOÀN BỘ `internal/delivery/httpapi` (kể cả subpackage,
qua `filepath.WalkDir` đệ quy), cấm `"os"`/`"os/exec"`/`internal/adapters/...` — nghĩa là package con mới của
task này ĐÃ được test đó phủ sẵn, một phát hiện quan trọng: viết thêm archtest riêng (yêu cầu của chính task)
là restatement có chủ đích (tự-tài-liệu-hoá cho đúng 1 task), không phải lấp một lỗ hổng thật.

Đọc toàn bộ `internal/app/workspaceinspection/queries.go` (252 dòng) và `internal/app/ports/workspaceinspection.go`
(182 dòng) — 2 file này là "hợp đồng" chính xác task phải map. Xác nhận field-for-field:
`GetSourceRequest{Scope WorkspaceScope, Revision workspace.Revision, Path, ByteLimit, LineLimit}`,
`GetDiffRequest{Scope, BaseRevision, ResultRevision, ByteLimit, FileLimit}`,
`GetRepositoryLogRequest{Scope, Anchor, Cursor, Limit, ByteLimit}`; `WorkspaceScope{ProjectID, RepositoryID,
WorkspaceSetID, RepositoryWorkspaceID}` — CẢ 4 field đều bị `resolveScope` so khớp tuyệt đối với ownership
chain thật, khác `workspacestate`'s 2-field check (V6-10B) — đọc `queries_test.go` xác nhận bằng 3 test riêng
biệt (`TestGetSourceRejectsProjectMismatch`/`RejectsRepositoryMismatch`/`RejectsWorkspaceSetMismatch`), mỗi
field sai một mình cũng đủ bị từ chối — đây là phát hiện quan trọng quyết định URL scheme (Quyết định #2).
`ports.SourceContent{Path, Revision, Content []byte, ByteLimit, LineLimit, TotalBytes, LineCount, Truncated,
Binary}`, `ports.DiffContent{BaseRevision, ResultRevision, Files []DiffFileChange, Patch []byte, ByteLimit,
FileLimit, FilesTruncated, PatchTruncated}`, `ports.RepositoryLogPage{Anchor, Entries []RepositoryLogEntry,
NextCursor, Limit, ByteLimit, Truncated}` — không field nào bịa thêm, không field nào bỏ sót khi dựng DTO wire
(xem `dto.go`).

Đọc `internal/adapters/gitworktree/inspection.go` (589 dòng) + `errors.go` để hiểu THẬT sự điều gì
`Queries.GetSource/GetDiff/GetRepositoryLog` có thể trả lỗi: `authorizeRevision` (dòng 299-318) từ chối một
revision không đúng `RepositoryID`/`WorkspaceGeneration` hoặc không phải hex object id hoặc không phải đúng
Base/CurrentRevision — LUÔN qua `ErrInvalidSpec`; `resolveLogCursor` (dòng 235-271) từ chối cursor không phải
ancestor thật qua `merge-base --is-ancestor` — cũng `ErrInvalidSpec`; `ReadSource` có thêm `ErrPathNotFound`/
`ErrUnsupportedEntry` riêng. Phát hiện kiến trúc quan trọng nhất của cả task: package HTTP này KHÔNG ĐƯỢC PHÉP
import `internal/adapters/gitworktree` (cả archtest sẵn có của V6-10B lẫn archtest riêng phải viết đều cấm) —
nghĩa là KHÔNG có cách hợp lệ nào để `errors.Is` lên đúng `gitworktree.ErrInvalidSpec`/`ErrPathNotFound`/
`ErrUnsupportedEntry` từ tầng delivery. Đây không phải một thiếu sót cần vá — nó là hệ quả trực tiếp, cố ý của
chính ranh giới kiến trúc mà task này phải CHỨNG MINH bằng archtest, nên `writeQueryError` phải thiết kế lại
theo đúng ràng buộc đó (xem Quyết định #5) thay vì cố map từng loại lỗi adapter.

`grep -rln "gitworktree.New(" --include=*.go .` (loại `_test.go`) xác nhận: KHÔNG có call site production nào
trong toàn repo, kể cả `cmd/aw/*.go` — mọi lần gọi `gitworktree.New` từng thấy trước giờ đều nằm trong file
test. Nghĩa là task này là task ĐẦU TIÊN trong repo thật sự dựng một `internal/adapters/gitworktree.Provider`
sống trong composition root — vượt ra ngoài khung "chỉ 1 dòng `RegisterRoutes` thêm vào `serve.go`" mà brief
mô tả, vì `workspaceinspection.New(uow, reader)` cần một `ports.WorkspaceInspectionReader` THẬT, không thể để
trống hay giả lập tại composition root. Grep tiếp `ManagedWorkspaceRoot` (safesettings) tìm xem có sẵn đường
dẫn cấu hình nào tái dùng được — có, nhưng đọc kỹ `internal/app/safesettings/startup.go` xác nhận field này
mặc định `""` (chưa từng có consumer thật nào, đúng doc comment "no prior task ever added that plumbing"), và
là giá trị MUTABLE lúc runtime qua `PUT /settings/safe` — dùng nó để dựng một adapter I/O thật lúc boot sẽ sai
với chính triết lý "immutable-per-process startup config" mà `--artifact-root` đã thiết lập cho `artifactStore`
— quyết định KHÔNG tái dùng (xem Quyết định #7).

### Quyết định

1. **Tái dùng đúng path prefix `/projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}` của
   V6-10B, gắn thêm `/source`, `/diff`, `/repository-log`.** Không tự vẽ cây URL mới — một caller đã có
   `RepositoryWorkspaceID` từ `getRepositoryWorkspaceState` (V6-10B) dùng lại được ngay, đúng tinh thần "mỗi
   task thêm route mới, không phá vỡ route cũ" mà `serve.go` đã tự thiết lập từ V6-01.
2. **`repositoryId`/`workspaceSetId` là query parameter BẮT BUỘC trên cả 3 route, không suy luận/reload lại từ
   `repositoryWorkspaceId`.** `WorkspaceScope` của V6-10C đòi hỏi khớp tuyệt đối cả 4 field — một phòng thủ
   sâu hơn cố ý so với V6-10B's 2-field check, đúng vì 3 route này stream NỘI DUNG repository thật, không chỉ
   state. Bản thân handler HTTP không được phép tự đọc thêm DB để tự suy ra 2 field còn thiếu (đó sẽ là một
   dispatch query thứ 2 tự chế, đi ngược "dispatch exact query, nothing more") — caller phải TỰ mang theo đủ 4
   định danh, giống hệt cách một capability token hoạt động: không đoán được `repositoryId`/`workspaceSetId`
   thật thì không đọc được gì, kể cả khi đoán đúng `repositoryWorkspaceId`.
3. **Revision luôn là query param thô (`revision`+`generation`, `base`+`baseGeneration`,
   `result`+`resultGeneration`, `anchor`+`anchorGeneration`), không bao giờ một symbolic ref.**
   `workspace.Revision.RepositoryID` luôn gán lại từ đúng `repositoryId` query param (một RepositoryWorkspace
   chỉ có thể có revision thuộc đúng 1 repository nó bind) — không có param "revision's own repository id" dư
   thừa. Handler không tự resolve gì cả — `gitworktree.Provider.authorizeRevision` (1 lớp dưới `Queries`, 2
   lớp dưới handler) là nơi DUY NHẤT chứng minh giá trị này thật sự là Base/CurrentRevision hợp lệ.
4. **`getWorkspaceSource` stream raw bytes, KHÔNG bọc JSON — bounds/binary/truncation lên response header
   (`X-Aw-Source-*`), không `Range` HTTP.** Mirror đúng `evidence/artifact.go`'s
   `ApplyContentHeaders`/`ResolveMediaDisposition`/ETag-content-hash convention thay vì tự nghĩ lại. Không
   thêm `Range`: `ports.SourceContent.Content` đã là kết quả ĐÃ bounded bởi `ByteLimit`/`LineLimit` (0 offset
   cố định), thêm `Range` HTTP chồng lên sẽ là 2 cơ chế cắt độc lập cho cùng 1 khái niệm — kém rõ ràng hơn là
   không có. `getWorkspaceDiff`/`getWorkspaceRepositoryLog` giữ nguyên JSON qua `EncodeResult` vì kết quả vốn
   có cấu trúc (danh sách file, danh sách commit) chứ không phải một stream đơn.
5. **`writeQueryError` chỉ đặt tên đúng 3 sentinel (`appinspection.ErrScopeMismatch`,
   `appinspection.ErrWorkspaceNotReady`, `ports.ErrPersistenceNotFound`) — MỌI lỗi khác rơi vào ĐÚNG 1 bucket
   400 chung, không đoán thêm.** Đây là hệ quả kiến trúc bắt buộc (xem Nghiên cứu): không thể `errors.Is` lên
   `gitworktree.ErrInvalidSpec`/`ErrPathNotFound`/`ErrUnsupportedEntry` mà không import package bị cấm. Ghi rõ
   lý do này trong chính doc comment của `errors.go` — để một người đọc sau không tưởng nhầm đây là code chưa
   hoàn thiện. 400 (không phải 404/500) vì mọi điều kiện rơi vào bucket này đều là thuộc tính của QUERY
   PARAMETER (`path`/`revision`/`cursor`), không phải của resource định danh trên PATH.
6. **Cursor của `getWorkspaceRepositoryLog` truyền thẳng, KHÔNG bọc qua `httpapi.CursorCodec` (`cursor.go`).**
   Khác các route list khác trong `httpapi` (dùng cursor cơ hội offset/sort-key cần chữ ký chống giả mạo),
   cursor ở đây đã là một giá trị domain thật có ý nghĩa độc lập (chính xác một commit object id) và được
   RE-VALIDATE lại hoàn toàn ở tầng adapter (`merge-base --is-ancestor` so với `anchor`) — bọc thêm một lớp mã
   hoá cơ hội sẽ chỉ che giấu giá trị thật mà không thêm an toàn nào (adapter không tin bất kỳ cursor nào, kể
   cả một cursor "đã ký hợp lệ" theo `CursorCodec`, nếu nó không phải tổ tiên thật của `anchor`).
7. **Composition root tự thêm flag `--workspace-root` MỚI, KHÔNG tái dùng `safesettings.ManagedWorkspaceRoot`.**
   Field kia là giá trị SQLite mutable-lúc-runtime, chưa có consumer thật nào (đọc kỹ `startup.go`'s doc
   comment) — dùng nó để dựng một `gitworktree.Provider` (I/O thật) lúc boot sẽ sai với triết lý
   "immutable-per-process startup config" mà `--artifact-root` (`artifactStore`) đã thiết lập trước đó.
   `--workspace-root` là flag bắt buộc y hệt `--db`/`--artifact-root` (usage error nếu rỗng) — khác
   `--artifact-root` ở chỗ KHÔNG cần pre-exist (`gitworktree.New` tự `os.MkdirAll`).
8. **Gói package con đặt tên TRÙNG với package application layer (`workspaceinspection`), alias
   `httpworkspaceinspection` khi import vào `serve.go`.** Đúng convention đã có sẵn cho `httpevidence`/
   `httpdefinitions`/`httpsafesettings`/`httpadapterbuild` — không tự đặt tên khác biệt không cần thiết.
9. **Viết archtest riêng cho đúng package này (`TestWorkspaceInspectionHTTPNeverImportsFilesystemOrProcess`),
   dù test archtest sẵn có của V6-10B đã phủ nó.** Là restatement có chủ đích, tự-tài-liệu-hoá cho đúng 1
   task theo yêu cầu tường minh của chính task này ("an architecture test proving NO filesystem/process
   concrete import"), không phải phát hiện một lỗ hổng — cả 2 test đều pass cùng lúc, không loại trừ nhau.

### Thực hiện

- `internal/delivery/httpapi/workspaceinspection/routes.go` (132 dòng, mới): doc comment đầy đủ +
  `Dependencies{Queries *appinspection.Queries}` + `RegisterRoutes` (3 route:
  `getWorkspaceSource`/`getWorkspaceDiff`/`getWorkspaceRepositoryLog`, cả 3 `ScopeProject`).
- `internal/delivery/httpapi/workspaceinspection/params.go` (86 dòng, mới): `requireQueryParam`/
  `requireUint64QueryParam`/`optionalInt64QueryParam`/`optionalIntQueryParam`/`requirePathParam` — validation
  400 field-level dùng chung cho cả 3 handler.
- `internal/delivery/httpapi/workspaceinspection/errors.go` (75 dòng, mới): `writeQueryError` (xem Quyết định
  #5), `writeValidationError`.
- `internal/delivery/httpapi/workspaceinspection/dto.go` (102 dòng, mới): `revisionResponse`,
  `diffFileChangeResponse`/`diffContentResponse`, `repositoryLogEntryResponse`/`repositoryLogPageResponse` +
  `toXxxResponse` converter — field-for-field với `ports.DiffContent`/`ports.RepositoryLogPage`, `Patch []byte`
  để `encoding/json` tự base64.
- `internal/delivery/httpapi/workspaceinspection/source.go` (148 dòng, mới): `handleGetSource` + 8 header
  `X-Aw-Source-*` (TotalBytes/LineCount/ByteLimit/LineLimit/Truncated/Binary/Revision/WorkspaceGeneration) +
  `writeSourceContent` (ETag content-addressed = `revision:path`, `ApplyContentHeaders`, ghi thẳng
  `result.Content`).
- `internal/delivery/httpapi/workspaceinspection/diff.go` (86 dòng, mới): `handleGetDiff`.
- `internal/delivery/httpapi/workspaceinspection/repositorylog.go` (82 dòng, mới): `handleGetRepositoryLog`.
- `cmd/aw/serve.go` (sửa): thêm flag `--workspace-root` (bắt buộc, đúng vị trí sau check `--artifact-root`),
  dựng `workspaceProvider, err := gitworktree.New(gitworktree.Config{Root: *workspaceRoot})` +
  `workspaceInspectionQueries := appworkspaceinspection.New(uow, workspaceProvider)` ngay sau `artifactStore`,
  cộng đúng 1 dòng `httpworkspaceinspection.RegisterRoutes(routes, httpworkspaceinspection.Dependencies{Queries:
  workspaceInspectionQueries})` ngay sau `httpapi.RegisterWorkspaceRoutes(...)` (V6-10B) đã có sẵn.
- `cmd/aw/serve_test.go` (sửa): thêm `--workspace-root` vào 4 call site `serve(...)` đã có sẵn (không đổi ý
  nghĩa test nào), thêm 1 test mới `TestServe_RequiresWorkspaceRootFlag` mirror đúng
  `TestServe_RequiresArtifactRootFlag`.
- `cmd/aw/serve_principal_test.go` (sửa): thêm `--workspace-root` vào `startServeForTest`'s args (dùng chung
  bởi 3 test) và vào `TestServe_RejectsInvalidPrincipalConfigAtStartup`.
- `internal/archtest/workspace_inspection_http_test.go` (87 dòng, mới):
  `TestWorkspaceInspectionHTTPNeverImportsFilesystemOrProcess`.

### Test

- `internal/delivery/httpapi/workspaceinspection/fixture_test.go` (331 dòng, mới): `newTestEnv` dựng REAL
  `*sqlite.Store` + REAL `gitworktree.Provider` chạy trên REAL temp git repo (helper `createGitRepository`/
  `runTestGit`/`commitWorkspaceChange` mirror đúng `queries_test.go` của V6-10C) + REAL `httpapi.Server` qua
  goroutine + REAL `http.Client` — không mock, không fabricate row nào. `seedOwnershipChain` mirror đúng
  helper cùng tên của `queries_test.go` nhưng đi qua `uow.WithSerializedWrite` + `tx.Catalog()`/`tx.Work()`
  THẬT (sqlite adapter) thay vì `fake.UnitOfWork` — đúng hard rule #1 của doctrine phiên này (không fake trực
  tiếp DB).
- `internal/delivery/httpapi/workspaceinspection/routes_test.go` (346 dòng, mới, 15 test function):
  - GetSource: happy path (body đúng bytes, `Content-Type: text/plain`, `X-Content-Type-Options: nosniff`,
    `X-Aw-Source-Binary=false`, `X-Aw-Source-Truncated=false`, ETag non-empty), thiếu query param bắt buộc
    (400), `generation` không phải số (400), `repositoryWorkspaceId` không tồn tại (404, ẩn danh), scope
    mismatch qua sai `repositoryId` (404, ẩn danh), workspace QUARANTINED (409), path traversal `../outside.txt`
    (400), revision tuỳ ý `"HEAD"` (400 — arbitrary ref bị từ chối), path không tồn tại tại revision (400,
    đúng bucket đối ứng Quyết định #5).
  - GetDiff: happy path (commit thật qua `commitWorkspaceChange`, xác nhận `files[0].path == "service.txt"`,
    `patch` non-empty), revision không được authorize (400), thiếu query param bắt buộc (400).
  - GetRepositoryLog: pagination 2 trang thật qua `cursor` (3 commit thật, `limit=2`, xác nhận trang 2 không
    lặp lại entry đầu của trang 1), cursor giả mạo không phải commit thật (400), `repositoryWorkspaceId` không
    tồn tại (404, ẩn danh).
- `internal/archtest/workspace_inspection_http_test.go`: 1 test, walk AST thật xác nhận không file nào trong
  package import `"os"`/`"os/exec"`/`internal/adapters/...`.
- `go build ./...`, `go vet ./...` sạch. `go test ./internal/delivery/httpapi/workspaceinspection/... -v`
  pass 15/15 (26.7s, real sqlite + real git mỗi test). `go test ./internal/archtest/...` pass (bao gồm cả test
  mới lẫn `TestDeliveryWorkspaceRoutesNeverReachWorkspaceIOOrExecutor` sẵn có của V6-10B, xác nhận cả 2 đồng
  thuận). `go test ./cmd/aw/...` pass sau khi sửa 6 call site cần `--workspace-root` (61s) — xác nhận
  `--workspace-root` bắt buộc không làm vỡ bất kỳ test `serve()` nào đã có từ trước.
- `go test ./...` toàn module (91 package): 87 package `ok`, đúng 4 test fail — TẤT CẢ đều trong
  `internal/integration/v5accept` (`TestV5AcceptFalseCompletionOracle`, `TestV5AcceptConformanceMatrix`,
  `TestV5AcceptHappyPath_RealCompositionReachesSucceededAndSurvivesRestart`,
  `TestV5AcceptIsolationUnavailable_RealAdmissionRejectsBeforeSpawn`), cùng một triệu chứng "did not reach
  state X within the deadline". Không tin ngay đây là flake quen mặt — điều tra thật: (1)
  `grep -rln "delivery/httpapi\|cmd/aw" internal/integration/v5accept/*.go` trả về RỖNG — không file nào của
  package đó import bất kỳ thứ gì từ `internal/delivery/httpapi` hay `cmd/aw`, nghĩa là 14 file diff của task
  này (toàn bộ nằm trong `cmd/aw/*.go` + `internal/delivery/httpapi/workspaceinspection/*` +
  `internal/archtest/*`) không có đường code-path nào chạm được runtime/scheduler/gate machinery
  `v5accept` test; (2) chạy lại riêng `go test ./internal/integration/v5accept/... -count=1` (cô lập, không
  cùng lúc với 90 package khác vừa chạy hàng chục phút liên tục ngay trước đó — trong đó riêng
  `internal/adapters/sqlite` một mình đã 257s, `internal/app/releasesetcommit` 128s, `internal/app/runtime`
  107s) — PASS SẠCH 100%, 94.8s, không fail lại bất kỳ test nào trong 4 test trên. Kết luận: đúng
  timing-sensitivity dưới tải máy tích luỹ từ một lần chạy `go test ./...` tuần tự dài (khớp đúng mẫu
  `baocaov6checklist.md`'s V6-10B section đã tự ghi nhận cho chính họ hàng test `v5accept` này), không phải
  regression từ diff của task này — có bằng chứng cấu trúc (grep, không đường import) LẪN bằng chứng thực
  nghiệm (pass sạch khi cô lập).

### Verify

- "transport negative matrix": liệt kê đầy đủ ở mục Test — thiếu param, malformed generation, unknown
  repositoryWorkspaceId, scope mismatch, non-READY workspace, path traversal, arbitrary/unauthorized revision,
  path-not-found, invalid cursor — mỗi trường hợp có đúng 1 test riêng, status code luôn một trong {400, 404,
  409}, không bao giờ 500 cho input caller-controlled.
- "architecture test no filesystem/process concrete import":
  `TestWorkspaceInspectionHTTPNeverImportsFilesystemOrProcess` (mới, scoped đúng 1 package) cộng
  `TestDeliveryWorkspaceRoutesNeverReachWorkspaceIOOrExecutor` (V6-10B, whole-package, đã tự phủ package con
  này qua `filepath.WalkDir` đệ quy) — cả 2 cùng pass, cùng chứng minh 1 sự thật.
- "Hoàn thành khi: no route can write, execute or arbitrary-read local paths": chứng minh CẤU TRÚC, không chỉ
  đọc code bằng mắt — `internal/delivery/httpapi/workspaceinspection` KHÔNG THỂ import `internal/adapters/...`
  (2 archtest), nên không route nào trong 3 route của task này có khả năng tự mở file/spawn git/chạm terminal
  dù cố tình; mọi I/O thật xảy ra bên trong `*appinspection.Queries` — một giá trị duy nhất, được inject 1 lần
  tại composition root, không route nào tự dựng thêm.
- Wiring thật vào `cmd/aw/serve.go`: `grep -n "httpworkspaceinspection\." cmd/aw/serve.go` xác nhận
  `httpworkspaceinspection.RegisterRoutes(...)` thật sự được gọi (dòng 357) — đúng lo ngại doctrine đã nêu từ
  vụ V6-04 ban đầu (30 test xanh nhưng route chưa từng wire thật vào binary).
- Không migration mới: `git log origin/master --oneline -3` + `ls internal/adapters/sqlite/migrations/` xác
  nhận migration cao nhất trên `origin/master` vẫn là `0037_release_set_local_commits.sql` — task này không
  thêm file migration nào, đúng như brief đã xác nhận trước ("thin HTTP wrapper over an already-complete
  application query layer").

### Kết quả

Package mới hoàn toàn `internal/delivery/httpapi/workspaceinspection` (7 file production, 2 file test, 15 test
function, real HTTP + real sqlite + real git). 1 architecture test mới trong `internal/archtest`. `cmd/aw/serve.go`
thêm flag `--workspace-root` (lần đầu tiên composition root thật dựng `internal/adapters/gitworktree.Provider`
sống — không chỉ 1 dòng `RegisterRoutes` như brief mô tả ban đầu) cộng đúng 1 khối wiring 3 dòng. 2 file test
`cmd/aw` có sẵn được sửa để mang theo flag mới (6 call site + 1 test mới), không đổi ý nghĩa test nào đã có.
3 route HTTP GET thật lần đầu tồn tại: `GET /projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}
/{source,diff,repository-log}` — mọi request đọc nội dung Git thật đều đi qua đúng 1 cổng
`internal/app/workspaceinspection.Queries` (V6-10C), không có đường tắt nào khác, được chứng minh bằng cấu
trúc (archtest) chứ không chỉ bằng lời hứa trong doc comment. `go test ./...` toàn bộ module: 87/91 package
`ok`, 4 fail còn lại giới hạn nguyên vẹn trong `internal/integration/v5accept` và đã xác nhận là flake môi
trường dưới tải máy tích luỹ (pass sạch 100% khi chạy lại cô lập, không code-path nào của diff này chạm được
package đó — xem mục Test), không phải regression thật của task này.

**Ghi chú cho phiên giám sát:** nếu CI của PR này cũng thấy `internal/integration/v5accept` fail, đối chiếu
lại với kết quả cô lập ở đây (`ok`, 94.8s, 0 test fail) trước khi kết luận là regression thật — CI runner sạch,
không có tải tích luỹ từ hàng chục phút chạy `go test ./...` tuần tự ngay trước đó như môi trường phiên này,
nên nhiều khả năng sẽ pass ngay trên CI.

## V6-06C — Run diagnostics endpoints

### Bối cảnh

V6-06C nằm trong nhóm P2, phụ thuộc `V6-00, V6-02A, V6-06D` — cả ba đã merge trên `origin/master`
(`c8163d3 feat(v6-07b)...`) lúc bắt đầu worktree này; `git log origin/master --oneline -3` xác nhận đúng tip,
không cần rebase. `baocaov6checklist.md` đang bị 3 task song song khác ghi (`V6-04A`, `V6-06B`, `V6-07A`, theo
đúng cảnh báo có sẵn) — append-only, dự kiến conflict khi merge, không phải bug.

Phiên làm việc này bị gián đoạn giữa chừng bởi rate limit của session (worktree gốc
`.claude/worktrees/agent-a8ab13a28e34d04d5` bị dọn trước khi việc implement thật sự bắt đầu — toàn bộ phần
nghiên cứu ở trên vẫn giữ nguyên trong context, không mất). Khi tiếp tục, `git worktree list` xác nhận worktree
cũ không còn tồn tại — dựng lại một worktree mới (`.claude/worktrees/v6-06c-run-diagnostics`, branch
`feat/v6-06c-run-diagnostics-endpoints`) từ đúng `origin/master` (vẫn `c8163d3`, không có commit mới nào chen
vào) rồi tiếp tục implement từ đầu — không có thay đổi thật nào bị mất vì phần trước đó thuần là đọc code.

Task này khác V6-06D (dependency của chính nó) ở bản chất: V6-06D bọc 3 COMMAND thật đã tồn tại sẵn
(`RetryBlockedActivation`/`CancelWorkItem`/`ResolveWorkItemBlocker`), còn V6-06C là một QUERY hoàn toàn mới —
không có hàm nào trong `internal/app/runtime` từng tổng hợp state chẩn đoán across nhiều nguồn (blocker, job/
lease, provider, workspace) cho một Run cụ thể. Cái khó thật không nằm ở việc gọi command có sẵn, mà ở việc tự
thiết kế một response DTO an toàn từ đầu, đúng ranh giới "Không làm" task tự nêu (không PID/argv/cwd/secret,
không internal execute-command capability), trong khi vẫn phải tái sử dụng đúng những nguồn dữ liệu thật đã có
sẵn (V4-13's `ListOrphanedRunningExecutionAttempts`, V5-08's admission blocker taxonomy, V6-10B's
`workspacestate`, V2-07A's `adapterbuild` registry).

### Nghiên cứu

Đọc nguyên văn spec (`docs/design/08-v6-api-projections.md` dòng 274-283): *"Mục tiêu: expose safe queue/job/
lease/fence/provider/workspace diagnostics và recovery actions. Phạm vi: `GET /runs/{id}/diagnostics`
authoritative query. Không làm: không PID, argv, cwd, secret, internal execute command hoặc installation Doctor
data. Thực hiện: reload Run/project, return typed states/remediation and advisory valid actions with versions.
Verify: blocked/lost/quarantined fixtures, role/project matrix, redaction and bounded output. Hoàn thành khi:
operator chẩn đoán Run và chọn named recovery action không cần DB surgery."* Đọc thêm
`docs/design/11-v6-00-ux-artifact.md` dòng 315-361 (Screen 8) và dòng 481-503 (Screen 13): operationId khoá
cứng `getRunDiagnostics` (dòng 358), CLI leaf `aw run diagnostics`, application query tên `GetRunDiagnostics`
[CHƯA CÓ] — Screen 13 (Settings) tái sử dụng ĐÚNG authority này khi drill-down từ một Run cụ thể, không định
nghĩa một query aggregate "mọi Run cần chú ý" riêng (dòng 544-552 tự giải thích quyết định này, không phải gap
của task).

Đọc `internal/app/runtime/retry_blocked_activation.go` (đã đọc trọn trong phiên trước) và
`baocaov6checklist.md`'s own V6-06D section — hai điểm mấu chốt cho thiết kế query này: (1) 4 admission
blocker type (`ISOLATION_ENFORCEMENT_UNAVAILABLE`/`ADAPTER_BUILD_DRIFT`/`CAPABILITY_REQUIREMENT_UNSATISFIED`/
`WRITE_CAPABILITY_OR_GRANT_MISSING`) là những blocker duy nhất `RetryBlockedActivation` có thẩm quyền; (2)
`runAdmissionProbePhase` (admission.go) — hàm Phase-1 thật admission dùng để probe — gọi
`adapterbuild.VerifyNoDrift` → `executor.Capabilities(ctx)`, một lời gọi spawn process THẬT. Đây là phát hiện
quan trọng nhất quyết định kiến trúc: nếu diagnostics tái sử dụng `VerifyNoDrift` y hệt, một `GET` request sẽ
tự spawn process như một side effect — vi phạm thẳng "không internal execute-command capability... never a way
to trigger execution" của chính task. Đối chiếu `internal/app/ports/isolation.go`'s own doc comment
(`IsolationEnforcementChecker.VerifyEnforceable` — "performs no I/O in this codebase's only implementation
today") và `internal/adapters/process/isolation.go`'s own `IsolationChecker.VerifyEnforceable` (switch thuần,
không I/O) xác nhận: đây là 1 trong 2 dependency admission cần real I/O, còn cái kia (isolation) thì KHÔNG —
an toàn để gọi live từ một GET. Đọc `internal/app/agentregistry/registry.go`'s own `Resolve` xác nhận nó cũng
chỉ là map lookup thuần (capability đã được đo MỘT LẦN lúc `agentregistry.New` construct, không phải mỗi lần
`Resolve`) — an toàn tương tự.

Đọc `internal/domain/work/blocker.go` trọn vẹn: `WorkItemBlocker.SourceRunID` được set bởi CẢ 4 producer thật
trong codebase (`openRunCancelledBlockerTx`/`requestScopeExpansionTx`/`blockAdmission`/
`applyCompletionPolicy`'s FAIL branch, xác nhận bằng grep từng call site `openWorkItemBlockerTx` trước khi viết
bất cứ gì) — nghĩa là filter `blocker.SourceRunID == runID` là chính xác tuyệt đối, không phải heuristic, để
scope blocker list đúng về một Run cụ thể (một WorkItem có thể có nhiều Run qua lịch sử, blocker của Run cũ
không được lẫn vào response của Run hiện tại).

Đọc `internal/app/workspacestate/queries.go` (V6-10B, đã có sẵn) trọn vẹn: `GetWorkspaceSetState` đã là chính
xác "Fence/Lease/Quarantine" snapshot task này cần cho phần workspace — tái sử dụng thẳng, không viết lại.
Đọc `internal/app/adapterbuild/commands.go`/`drift.go` xác nhận `GetAdapterBuild`/`ListAdapterBuilds` chỉ đọc
DB (an toàn), còn `VerifyNoDrift` là hàm duy nhất trong package đó có I/O thật — quyết định KHÔNG gọi hàm đó
(xem Quyết định #3).

Đọc `internal/app/runtime/queries.go` (V6-07B, đã merge) trọn vẹn — đây là tiền lệ trực tiếp nhất cho việc đặt
`GetRunDiagnostics` Ở ĐÂU: mọi read query V6-07B thêm (`GetContextSnapshot`/`ListEvidenceForWorkItem`) đều nằm
NGAY TRONG package `runtime` (không phải một package con riêng), nhận `ports.CommandScope` tường minh, và tự
verify `scope.ProjectID()` khớp `ProjectID` thật đã reload — same `requireProjectScope`/`scopeMismatch` helper
dùng lại được trực tiếp (cùng package). Đọc `internal/delivery/httpapi/evidence/routes.go` xác nhận route
pattern tương ứng luôn có prefix `/projects/{projectId}/...` — KHÁC hẳn V6-06/V6-06D's own flat mutation routes
(`/runs/{id}/cancel`) vì lý do đã ghi rõ trong `run.go`'s own doc comment: `CancelRun`/`RetryBlockedActivation`/
v.v. không nhận `ProjectID` trong request struct nên không có gì để `{projectId}` verify; NGƯỢC LẠI, mọi query
V6-07B (giống `GetRunDiagnostics` ở đây) đều nhận `ports.CommandScope` tường minh — nên route PHẢI project-
prefixed để path có cái để verify against. Quyết định route: `GET /projects/{projectId}/runs/{runId}/
diagnostics`, không phải literal `/runs/{id}/diagnostics` bullet ngắn gọn trong design doc (bullet đó không
tách chi tiết path prefix, y hệt cách nó không tách cho V6-07B's own routes).

Đọc `internal/delivery/httpapi/workspacestate.go` (V6-10B) trọn vẹn: đây là khuôn mẫu chính xác cho
"aggregated state query + advisory ValidActions tính Ở TẦNG HTTP, không phải tầng application" —
`reconcileValidActions`/`releaseValidActions` đọc `workspacestate.WorkspaceSetState` (app-layer, không biết gì
về `httpapi.ValidAction`) rồi tính advisory action ở delivery layer. Áp dụng y hệt cho task này: `internal/app/
runtime.RunDiagnostics` (app layer) không import `internal/delivery/httpapi` (tránh đảo ngược dependency
direction), còn `internal/delivery/httpapi/diagnostics` (delivery layer) tính `ValidActions` từ state đó.

Đọc `internal/app/runtime/execute.go`'s own `resolvedExecutionProfileView` struct (dòng 142-172) xác nhận:
field set của nó (`Executor.Kind/DefinitionID/VersionID/CompiledHash`, `AdapterBuild.BuildID`, `IsolationTier`,
`AllowedCapabilities`, `Model`, `TimeoutSeconds`, `Role`) hoàn toàn an toàn — không có PID/argv/cwd/secret nào,
xác nhận qua đọc `internal/domain/runtime/runtime.go`'s own `ExecutionAttempt` struct (không có field OS-
process nào) và grep riêng cho `Argv`/`CwdRepositoryTarget`/`Pid`/`WorkingDirectory` toàn repo — những field đó
CHỈ tồn tại ở `internal/domain/command` (COMMAND node pin) và `internal/adapters/process` (Supervisor thật) —
không package nào trong số đó được import bởi file mới của task này.

### Quyết định

1. **Đặt `GetRunDiagnostics` trực tiếp trong package `internal/app/runtime`** (file mới `diagnostics.go`), không
   phải một package con riêng như `workspacestate`. Lý do: query cần tái sử dụng trực tiếp nhiều helper
   unexported của chính package đó — `loadExecutionProfile` (đọc `<nodeRunId>-execution-profile-v1` decision
   artifact), `requireProjectScope`/`scopeMismatch` (queries.go, V6-07B) — một package riêng sẽ phải import
   `internal/app/runtime` từ bên ngoài và không bao giờ gọi được các hàm unexported này, buộc phải viết lại
   logic đọc execution profile lần hai (rủi ro phân kỳ với `RetryBlockedActivation`'s own read path). Tiền lệ
   trực tiếp: V6-07B's own `GetContextSnapshot`/`ListEvidenceForWorkItem` cũng nằm ngay trong package này vì
   cùng lý do.
2. **KHÔNG bao giờ gọi `adapterbuild.VerifyNoDrift`** (real process spawn) — đây là quyết định kiến trúc quan
   trọng nhất của task, dựa thẳng trên phát hiện ở phần Nghiên cứu. 3 lựa chọn đã cân nhắc:
   - (a) Gọi y hệt `runAdmissionProbePhase` cho drift thật — **loại bỏ**: một GET request spawn process thật
     là chính xác "internal execute-command capability" mà task tự cấm, kể cả khi hẹp (chỉ `--version`); cũng
     biến một endpoint "safe, read-only" thành một vector DoS tiềm năng (mỗi GET spawn 1 process).
   - (b) Không cross-reference provider gì cả, chỉ trả `AdapterBuildID` trần — **loại bỏ**: bỏ phí chính xác
     phần "provider diagnostics" task yêu cầu ("cross-referenced against the real registry to report drift/
     staleness").
   - (c) **Chọn**: cross-reference THUẦN dữ liệu tĩnh + lookup an toàn — `agents.Resolve(providerKey, ...)`
     (map lookup, không I/O) báo `ProviderConfigured` (registry hiện có live executor cho provider này hay
     không), và `isolation.VerifyEnforceable(ctx, tier)` (pure/static, xác nhận qua đọc
     `internal/adapters/process/isolation.go`'s own doc comment) báo `Enforceable` thật — cả hai chạy LIVE từ
     GET vì cả hai đều an toàn, không I/O. `ProviderConfigured=false` khớp chính xác điều kiện 503
     `agentregistry.ErrUnknownProvider` V6-06D đã map cho `RetryBlockedActivation` — advisory nhất quán với
     command thật. Việc xác nhận DRIFT thật (không chỉ "có executor hay không") vẫn là thẩm quyền độc quyền
     của `RetryBlockedActivation` — kết quả cấu trúc `FailureReason`/`FailureDetail` của nó là câu trả lời
     authoritative, query này chỉ advisory hướng tới đó.
3. **Phạm vi provider/isolation cross-reference: CHỈ NodeRun đứng sau một admission blocker đang OPEN**, không
   phải mọi NodeRun RUNNING/historical của Run. Lý do: (1) bounded tự nhiên — số blocker OPEN luôn nhỏ, không
   cần duyệt toàn bộ lịch sử NodeRun của một Run có thể rất dài; (2) đây chính xác là tập NodeRun mà
   `retryBlockedActivation` sẽ nhắm tới — advisory action và provider/isolation diagnostic luôn đồng bộ với
   nhau; (3) `loadExecutionProfile` chỉ tồn tại cho NodeRun đã qua `ScheduleExecutableNodeRun` — một NodeRun
   BLOCKED vì admission luôn thoả điều kiện này (chính xác đối tượng `blockAdmission` tạo ra), không cần đoán.
4. **`runDiagnosticsBase` dùng ĐÚNG MỘT `uow.WithReadOnly` transaction** cho phần Run/WorkItem/Blocker/
   orphaned-attempt-evidence; các bước sau (`loadExecutionProfile`, `loadAdapterBuild`,
   `loadRunRepositoryWorkspaces`) đều là transaction độc lập riêng, chạy TUẦN TỰ sau khi transaction đầu đã
   đóng — không nesting. Lý do: `loadExecutionProfile` (execute.go) luôn được gọi NGOÀI mọi transaction bởi cả
   2 caller thật hiện có (`ExecuteNodeHandler.Handle`'s own admission preflight,
   `RetryBlockedActivationHandler.Retry`) — không có tiền lệ nào trong codebase này nest một
   `uow.WithReadOnly` thứ hai bên trong một `ports.Tx` closure đã mở, và sqlite driver không được kiểm chứng an
   toàn cho pattern đó. Đổi lại: response có thể đọc phải state hơi lệch nhau giữa các bước (read skew nhẹ) —
   chấp nhận được vì toàn bộ response vốn đã "advisory only, never authoritative" (như mọi field khác của
   response này).
5. **Route: `GET /projects/{projectId}/runs/{runId}/diagnostics`, project-prefixed** — khác hẳn V6-06/V6-06D's
   own flat routes, lý do đầy đủ ở phần Nghiên cứu (query nhận `ports.CommandScope` tường minh, cần path có
   `{projectId}` để verify against — đúng tiền lệ V6-07B's own routes, không phải V6-06D's own).
6. **ValidActions tính Ở TẦNG HTTP (`internal/delivery/httpapi/diagnostics/dto.go`), không phải app layer** —
   mirror chính xác `workspacestate.go`'s own `reconcileValidActions`/`releaseValidActions` (xem Nghiên cứu).
   `blockerValidActions` advisory `retryBlockedActivation` (target = NodeRun's own Version) cho blocker
   admission-reason đang OPEN, `resolveWorkItemBlocker` (target = blocker's own Version) cho blocker OPEN khác
   (trừ `SCOPE_EXPANSION_REQUIRED` — mirror `workdomain.BlockerType.ResolvableViaCommand()`'s own một ngoại lệ
   duy nhất, so sánh bằng literal string vì `BlockerDiagnostic.Type` vốn đã là wire string, không import lại
   `internal/domain/work` chỉ để dùng một constant). `runDiagnosticsValidActions` advisory `cancelRun`/
   `cancelWorkItem` ở cấp response dựa trên `RunState`/`WorkItemStatus` hiện tại — advisory thuần, command thật
   luôn tự re-derive precondition, đúng "advisory only, never authoritative" của `ValidAction` (action.go).
7. **Response DTO tự định nghĩa field JSON riêng, không serialize thẳng `runtime.RunDiagnostics`** — cùng lý do
   mọi task HTTP trước đã ghi (`internal/app/runtime`'s own struct không có json tag; đây cũng là điểm chốt
   review cho chính prohibition PID/argv/cwd/secret — field allowlist tường minh, không phải "quên loại trừ").
8. **`Dependencies{UOW, Isolation, Agents}` tái sử dụng ĐÚNG 2 dependency `cmd/aw/serve.go` đã construct sẵn
   cho V6-06D** (`isolationChecker`, `agentRegistry`) — không thêm flag mới, không construct thêm gì ở
   composition root. Task này không cần `IDs`/`Clock` (query không mint ID, không set timestamp mới).
9. **Kiến trúc test riêng (`internal/archtest/diagnostics_http_test.go`)** mở rộng danh sách selector cấm so
   với `TestRecoveryHTTPNeverReachesSchedulerOrWorkerOrMutatesStateDirectly` gốc — thêm `Capabilities` (chính
   là lời gọi I/O thật `VerifyNoDrift` dùng, cấm tuyệt đối trong package này) và
   `TransitionWorkItemBlockerState` (nếu package này từng tự resolve một blocker thay vì chỉ đọc, đó là bug
   nghiêm trọng của một "safe, read-only" surface).

### Thực hiện

- `internal/app/runtime/diagnostics.go` (535 dòng): package doc giải thích trọn nguồn dữ liệu + ranh giới an
  toàn; `RunDiagnostics`/`BlockerDiagnostic`/`OrphanedAttemptDiagnostic`/`ProviderDiagnostic`/
  `IsolationDiagnostic`/`RepositoryWorkspaceDiagnostic` (DTO tự viết, allowlist tường minh);
  `isAdmissionBlockerType` (4 admission `workdomain.BlockerType`, mirror `admission.go`'s own
  `admissionPriority`/`isAdmissionBlockerReason` nhưng ở vocabulary `BlockerType` thay vì `TerminationReason`);
  `runDiagnosticsBase` (1 transaction: Run+scope check, WorkItem, Blocker list filtered `SourceRunID==runID`,
  orphaned-attempt evidence filtered theo NodeRun set của Run, mỗi list đều bounded `maxDiagnosticEntries=50`
  với cờ `*Truncated` riêng); `GetRunDiagnostics` (entrypoint, gọi `runDiagnosticsBase` rồi
  `collectAdmissionCrossReference`/`loadRunRepositoryWorkspaces` tuần tự, transaction độc lập); `loadAdapterBuild`
  (đọc trực tiếp `tx.AdapterBuilds().Get`, không import `internal/app/adapterbuild` — cùng lý do
  `admission.go`'s own `runAdmissionProbePhase` đã làm).
- `internal/app/runtime/diagnostics_test.go` (332 dòng, `package runtime_test`, sqlite KHÔNG cần vì tái sử dụng
  `*fake.UnitOfWork` — xem phần Test): 8 test case, tái sử dụng 100% helper có sẵn CÙNG PACKAGE
  (`admissionFixture`/`claimableExecuteNodeJob` từ `admission_test.go`/`execute_test.go`,
  `runCancelledBlockerFixture`/`quarantineExtraRepositoryWorkspace` từ `resolve_work_item_blocker_test.go`,
  `cancelWorkItemFixture` từ `cancel_work_item_test.go`) — không duplicate một dòng nào vì cùng package
  `runtime_test`.
- `internal/delivery/httpapi/diagnostics/` (package mới, 5 file production ~403 dòng):
  - `diagnostics.go`: package doc, `Dependencies{UOW, Isolation, Agents}`, `RegisterRoutes` — 1 descriptor duy
    nhất.
  - `dto.go`: `RunDiagnosticsResponse` + 6 sub-DTO, `blockerValidActions`/`runDiagnosticsValidActions` (tính
    `httpapi.ValidAction` từ app-layer state).
  - `handler.go`: `handleGetRunDiagnostics` — decode path, dispatch `runtime.GetRunDiagnostics`, encode kết
    quả với `ETagFromVersion(diag.RunVersion)`.
  - `errors.go`: `writeQueryError` — mirror `evidence/errors.go`'s own (not-found + scope-mismatch → cùng 404
    leakage-normalized).
  - `internal/archtest/diagnostics_http_test.go`: `TestDiagnosticsHTTPNeverReachesSchedulerOrWorkerOrMutatesStateDirectly`.
- `cmd/aw/serve.go` (+10 dòng, không sửa dòng có sẵn): 1 import mới (`httpdiagnostics`), 1 lời gọi
  `httpdiagnostics.RegisterRoutes(routes, httpdiagnostics.Dependencies{UOW: uow, Isolation: isolationChecker,
  Agents: agentRegistry})` tái sử dụng đúng 2 biến V6-06D đã construct sẵn. Xác nhận bằng
  `grep -n "httpdiagnostics\|RegisterRoutes(routes" cmd/aw/serve.go` sau khi viết xong — đúng yêu cầu review
  bắt buộc của chính task (bài học `V6-04` để lại: 30 test xanh cho một package KHÔNG BAO GIỜ được gọi từ
  `aw serve` thật).
- Không migration mới — `internal/adapters/sqlite/migrations/` cao nhất vẫn `0037_release_set_local_commits.sql`
  trên `origin/master` lúc bắt đầu (`ls internal/adapters/sqlite/migrations/ | tail -5` xác nhận trực tiếp
  trước khi kết thúc), task này thuần đọc dữ liệu đã có, không cần schema mới.

### Test

Tổng 15 test case mới (8 app-layer + 7 HTTP-layer), không test nào hand-seed một RESULT giả — mọi fixture đi
qua command/handler thật.

- `internal/app/runtime/diagnostics_test.go` (8 test, `*fake.UnitOfWork` — chấp nhận được vì
  `admission_test.go` chính package này cũng dùng fake cho đúng loại test này; sqlite thật dành cho tầng HTTP
  bên dưới):
  - `TestGetRunDiagnostics_AdmissionBlocked_ReportsBlockerProviderIsolation`: fixture "genuinely blocked
    NodeRun" — `admissionFixture` + `ExecuteNodeHandler.Handle` thật với isolation checker fail thật (không
    phải chuỗi bịa) → assert blocker/provider/isolation đúng.
  - `TestGetRunDiagnostics_ProviderNotConfigured_ReportsFalse`: cùng blocker nhưng gọi `GetRunDiagnostics` với
    MỘT registry khác (rỗng) so với registry đã dùng lúc admit — chứng minh `ProviderConfigured` phản ánh
    registry LIVE của chính lời gọi, không phải registry lúc blocker mở.
  - `TestGetRunDiagnostics_RunCancelledBlockerAndQuarantinedWorkspace`: fixture "blocked" (non-admission) +
    "quarantined" cùng lúc — `runCancelledBlockerFixture` (RUN_CANCELLED thật qua CancelRun+coordinator) +
    `quarantineExtraRepositoryWorkspace` (construct trực tiếp, cùng discipline
    `resolve_work_item_blocker_test.go`'s own `ErrWorkspaceQuarantined` test đã lập tiền lệ — không có command
    thật nào trong codebase quarantine một workspace từ trạng thái khoẻ mạnh).
  - `TestGetRunDiagnostics_OrphanedAttempt_ReportsQueueJobLeaseEvidence`: fixture "lost" — poke
    `TransitionExecutionAttempt` QUEUED/BLOCKED→RUNNING trực tiếp (KHÔNG claim job của nó), đúng discipline
    "poke primitive real crash mới reach được" đã lập tiền lệ nhiều lần trong package này (`readyFixture`'s own
    BACKLOG→READY, `startSecondRunForWorkItem`'s own ACTIVE→READY) — path thật (crash worker pool) đã có
    coverage riêng, cực đắt (`crash_recovery_test.go`, ~15s), không hợp lý lặp lại cho unit test của tầng ĐỌC.
  - 2 test scope (`UnknownRun`/`WrongProjectScope`/`InstallationScope` — 3 test thật): `ErrPersistenceNotFound`/
    `ErrScopeMismatch` đúng sentinel.
  - `TestGetRunDiagnostics_NeverExposesProcessOrSecretShapedFields`: reflect-scan tên field mọi type exported
    trong `diagnostics.go`, cấm `pid/argv/cwd/workingdir/secret/credential/password/apikey` — tự động phủ field
    mới thêm sau này, không cần cập nhật test thủ công mỗi lần.
- `internal/delivery/httpapi/diagnostics/*_test.go` (7 test, TOÀN BỘ sqlite thật qua `httptest.Server` +
  `http.Client` thật, không mock/fake nào cho persistence — cùng discipline V6-06D đã lập):
  - `fixture_test.go`/`admission_fixture_test.go` (694 dòng, duplicate có chủ đích từ `recovery`'s own 2 file
    cùng tên — Go test helper không export chéo package): thêm `quarantineExtraRepositoryWorkspace` (sqlite,
    mirror app-layer). `blockedAdmissionFixture` ở đây dùng `process.NewIsolationChecker()` THẬT (không phải
    toggle fake như `recovery`'s own bản) — vì package này không bao giờ retry, chỉ cần MỘT lần block thật, và
    checker production đã tự reject `ENFORCED_ISOLATED` một cách trung thực (đọc doc comment xác nhận trước
    khi dùng).
  - `TestGetRunDiagnostics_HTTP_AdmissionBlocked_Success`: end-to-end thật qua route đã register, assert
    JSON wire keys đúng camelCase, `blockerValidActions`/`runDiagnosticsValidActions` đúng.
  - `TestGetRunDiagnostics_HTTP_UnknownRun_ReturnsResourceHidden` / `..._WrongProject_...`: role/project matrix
    nửa "project" — leakage-normalized 404 cả hai trường hợp.
  - `TestGetRunDiagnostics_HTTP_QuarantinedWorkspaceAndRunCancelledBlocker`: fixture kép qua HTTP thật.
  - `TestGetRunDiagnostics_HTTP_OrphanedAttempt_RealSqliteEvidence`: khác bản app-layer ở chỗ dùng sqlite THẬT
    nên `GetWriteLeaseRepositoryWorkspaceForAttempt`'s own real SQL chạy thật (fake package đó là stub luôn trả
    `false`, đọc doc comment xác nhận) — job claim với lease NGẮN CHỦ ĐÍCH (`claimJobLeaseTTL=3s`, thay vì 1
    phút mặc định mọi fixture khác dùng) để lease tự hết hạn thật theo wall-clock, rồi poll GET endpoint thật
    (không sleep mù, đúng discipline `crash_recovery_test.go`'s own "polling the real query itself") tới khi
    thấy orphaned attempt xuất hiện — thực tế bắt được trong ~3.3s, không cần đợi hết 15s deadline.
  - `TestGetRunDiagnostics_HTTP_ArbitraryRoleStillReads`: role/project matrix nửa "role" — principal role hoàn
    toàn không liên quan vẫn đọc được 200 OK, chứng minh trực tiếp quyết định "query này không role-gate" thay
    vì chỉ giả định.
  - `TestGetRunDiagnostics_HTTP_NeverExposesProcessOrSecretShapedFields`: quét RAW JSON body (không chỉ Go
    struct field name như bản app-layer) tìm `"pid"`/`"argv"`/`"cwd"`/`"workingdirectory"`/`"secret"`/
    `"credential"`/`"password"`/`"apikey"`/`"executablepath"` — bản HTTP-layer, độc lập với bản reflect ở
    app-layer, phủ cả 2 lớp (Go field name và JSON wire key thật).

**2 phát hiện thật trong lúc viết/chạy test (không phải giả định trước):**

1. `validAgentProfileDocument`'s own `Compatibility.OS` ban đầu viết `{"linux", "windows", "darwin"}` (bản gốc
   `recovery` chỉ có `"linux"`, tôi thêm quá tay) — `PublishDefinitionVersion` từ chối ngay với lỗi WHAT/WHY/FIX
   rõ ràng: ADR-002 chỉ cho phép `windows`/`linux` ở Alpha. Sửa còn `{"linux", "windows"}` — không phải bug của
   task, chỉ là lỗi gõ khi duplicate fixture.
2. `TestGetRunDiagnostics_HTTP_OrphanedAttempt_RealSqliteEvidence` ban đầu FAIL (`len(OrphanedAttempts)=0`) vì
   `claimJobOfKind` (duplicate từ `recovery`) giữ nguyên lease 1 PHÚT — sqlite's own real
   `ListOrphanedRunningExecutionAttempts` (WHERE `dj.state != 'LEASED' OR dj.lease_until <= ?`) đúng đắn coi
   job vẫn còn active lease trong 1 phút đó, bất kể Attempt đã bị poke sang RUNNING. Fake package's own
   `ListOrphanedRunningExecutionAttempts` không bắt được lỗi này vì test app-layer không claim job trước khi
   poke (job chưa từng LEASED → orphaned ngay). Sửa: rút `claimJobLeaseTTL` xuống 3 giây CHỈ trong package này
   (không đụng `recovery`'s own bản, package khác, mục đích khác), test poll thật thay vì assert ngay — đúng
   phát hiện thật về khác biệt hành vi fake vs. sqlite thật, không phải giả định trước khi viết.

- `go build ./...`, `go vet ./...` sạch trên toàn bộ package.
- `go test ./internal/app/runtime/... -run TestGetRunDiagnostics -v`: 8/8 PASS (~1.2s).
- `go test ./internal/delivery/httpapi/diagnostics/... -v`: 7/7 PASS (~4.7s, phần lớn thời gian ở test
  orphaned-attempt chờ lease thật hết hạn).
- `go test ./internal/archtest/... -run TestDiagnosticsHTTP -v`: PASS.
- `go test ./...` (toàn bộ ~90 package, chạy nền): 2 fail gặp phải, CẢ HAI đều nằm ngoài diff của task này —
  `TestEndToEnd_Restart_RemainingJobCompletesAndSetReachesReady` (`internal/app/workspaceprovision`, đã được
  V6-10H's own section phía trên ghi nhận là flake môi trường Windows file-lock/parallel-load, KHÔNG phải lần
  đầu gặp) và `TestEndToEnd_Release_ReadyWorkspaceSet_ReleasesRepositoryAndSet`
  (`internal/app/workspacerelease`, cùng họ "EndToEnd" nhạy với tải song song). Cả hai package đều không nằm
  trong diff (task này chỉ sửa `cmd/aw/serve.go` + thêm file mới ở `internal/app/runtime`,
  `internal/delivery/httpapi/diagnostics`, `internal/archtest`) — chạy lại riêng lẻ cả hai đều PASS ngay
  (`go test ./internal/app/workspaceprovision/... -run TestEndToEnd_Restart... -v` và
  `go test ./internal/app/workspacerelease/... -run TestEndToEnd_Release... -v`, mỗi cái dưới 5s), xác nhận
  đúng là race dưới tải song song của lần `go test ./...` đầy đủ, không phải regression từ diff này.

### Verify

- **blocked/lost/quarantined fixtures**: cả ba đều có fixture THẬT (không hand-seed) ở cả 2 tầng (app-layer
  fake, HTTP-layer sqlite) — "blocked" phủ cả 2 dạng (admission-reason thật qua `ExecuteNodeHandler.Handle`,
  và non-admission RUN_CANCELLED thật qua `CancelRun`+coordinator), "lost" qua poke-primitive có tài liệu đầy
  đủ lý do (path thật quá đắt cho unit test tầng đọc), "quarantined" qua construct trực tiếp có tiền lệ đã
  được chấp nhận trong chính codebase (`resolve_work_item_blocker_test.go`).
- **role/project matrix**: "wrong project" → `TestGetRunDiagnostics_WrongProjectScope_ReturnsScopeMismatch`
  (app-layer) + `TestGetRunDiagnostics_HTTP_WrongProject_ReturnsResourceHidden` (HTTP-layer, leakage-normalized
  404 thật). "wrong role" → không áp dụng theo nghĩa "bị từ chối" (query này KHÔNG role-gate, quyết định có
  chủ đích, ghi rõ trong package doc comment) — chứng minh bằng
  `TestGetRunDiagnostics_HTTP_ArbitraryRoleStillReads` thay vì giả định im lặng.
- **redaction**: 2 lớp độc lập — reflect-scan tên field Go (`diagnostics_test.go`, app-layer) + quét raw JSON
  wire key (`diagnostics_test.go`, HTTP-layer) — cả hai PASS, không field nào tên PID/argv/cwd/secret-shaped
  từng lọt qua.
- **bounded output**: `maxDiagnosticEntries=50` áp dụng cho blocker list, orphaned-attempt list, repository-
  workspace list, provider/isolation cross-reference loop — mỗi list bounded có `*Truncated` cờ riêng (chỉ
  `OrphanedAttemptsTruncated` thật sự cần thiết publicly, 2 list còn lại hiếm khi vượt cap trong thực tế nhưng
  vẫn bounded phòng thủ). GAP ghi nhận trung thực: không dựng test THẬT chạm ngưỡng 51 item (chi phí dựng 51
  blocker/attempt thật qua pipeline production không tương xứng phạm vi task — "HTTP transport + aggregation",
  không phải "chứng minh lại slicing logic cơ bản của Go"), logic capping đã review thủ công (so sánh
  `len(...) >= maxDiagnosticEntries` trước mỗi `append`, đơn giản, rủi ro regression thấp).
- **"Hoàn thành khi": operator chẩn đoán Run và chọn named recovery action không cần DB surgery** — xác nhận
  bằng `grep` trực tiếp `cmd/aw/serve.go` (không chỉ tin `go build`), cộng
  `TestGetRunDiagnostics_HTTP_AdmissionBlocked_Success` chạy end-to-end qua đúng `RegisterRoutes` handler thật,
  trả về `retryBlockedActivation`/`cancelRun`/`cancelWorkItem` advisory action có `TargetVersion` — đủ để một
  client gọi `POST /node-runs/{nodeRunId}/retry-blocked-activation` (V6-06D) ngay sau đó mà không cần biết gì
  về schema DB.

### Kết quả

Package mới `internal/app/runtime/diagnostics.go` (535 dòng) + `internal/delivery/httpapi/diagnostics` (5 file
production ~403 dòng; 4 file test ~1180 dòng, 7 test case) + `internal/app/runtime/diagnostics_test.go` (332
dòng, 8 test case) + 1 architecture test mới (`internal/archtest/diagnostics_http_test.go`) + `cmd/aw/serve.go`
+10 dòng (1 import mới, 1 `RegisterRoutes` call tái sử dụng nguyên `isolationChecker`/`agentRegistry` V6-06D đã
construct). 15 test case mới, tất cả PASS.

1 route HTTP thật lần đầu tồn tại: `GET /projects/{projectId}/runs/{runId}/diagnostics` — chạy được qua
`aw serve` thật, xác nhận bằng grep trực tiếp `cmd/aw/serve.go`, không chỉ tin test package cô lập.

Quyết định kiến trúc quan trọng nhất: KHÔNG bao giờ gọi `adapterbuild.VerifyNoDrift` (real process spawn) từ
truy vấn GET này, dù task's own instruction có gợi ý "mirror EXACT check RetryBlockedActivation dùng" — đọc kỹ
lại "Không làm: ... never a way to trigger execution" và quyết định tách rõ: diagnostics chỉ báo cáo
`ProviderConfigured` (registry có live executor hay không — pure lookup) và `Enforceable` (isolation tier có
enforce được hay không — pure/static check), không bao giờ báo cáo DRIFT thật (cần I/O thật) — quyền đó vẫn
thuộc độc quyền `RetryBlockedActivation`. Advisory `ValidActions` tính hoàn toàn ở tầng HTTP
(`internal/delivery/httpapi/diagnostics/dto.go`), mirror đúng khuôn `workspacestate.go` đã lập, không lẫn vào
app-layer query.

Một operator giờ có thể `GET` một Run để thấy đầy đủ blocker/queue/lease/provider/isolation/workspace state
kèm advisory recovery action có version, rồi gọi thẳng `RetryBlockedActivation`/`CancelWorkItem`/
`ResolveWorkItemBlocker` (V6-06D) hoặc `CancelRun` (V6-06) — không cần DB surgery, đúng "Hoàn thành khi" của
chính task.

## V6-04A — Public MarkWorkItemReady authority và route

### Bối cảnh

V6-04A đóng gap `BACKLOG → READY` mà chính `V6-04`'s own `ExplainWorkItemReadiness` (`internal/app/work/queries.go`)
đã cố tình để ngỏ — doc comment của `WorkItemReadiness` nói thẳng: "that narrow, named MarkWorkItemReady
command is explicitly V6-04A's own separate job, not this task's". Phụ thuộc khai trong spec: V6-04, V3-03,
V1-06, V1-07A — cả bốn đã merge từ lâu (V6-04 là PR #43, đã review post-merge và vá lỗi unwired route như
system prompt của task này nhắc lại). `baocaov6checklist.md` đang bị 3 task khác ghi song song đúng lúc này
(V6-06B, V6-06C, V6-07A) — merge conflict khi mở PR là chuyện đã lường trước, không phải bug.

Task tự nêu rõ gap thật: "`MarkWorkItemReady` does not exist as a command anywhere in this codebase yet" —
`grep` xác nhận đúng, chỉ có đúng một forward-reference trong doc comment của `ExplainWorkItemReadiness`.
Xây command đó, cùng route `POST /work-items/{id}/mark-ready`, là toàn bộ việc của task này.

Sự cố môi trường giữa chừng: phiên làm việc bị rate-limit ngắt khi đang đọc `scope_expansion_commands.go`
làm template; khi tiếp tục, worktree gốc (`agent-a95d1a7cdd242ec3d`) đã bị dọn khỏi máy (primary working
directory đổi về repo gốc, không còn là worktree). `EnterWorktree` từ chối tạo worktree mới vì phiên này
chạy dưới một subagent có cwd pin sẵn ("cannot create a worktree from a subagent with a cwd override") — xử
lý bằng `git worktree add` thủ công qua Bash, branch `feat/v6-04a-mark-work-item-ready` từ `origin/master`
tại `c8163d3` (đúng commit `gitStatus` ở đầu phiên đã ghi), thư mục
`.claude/worktrees/v6-04a-mark-work-item-ready`. Toàn bộ nghiên cứu đã làm trước đó (đọc design doc, domain
package, `queries.go`, `commands.go`, `scope_expansion.go`, HTTP package) được giữ nguyên trong ngữ cảnh và
áp dụng lại vào worktree mới, không phải làm lại từ đầu.

### Nghiên cứu

Đọc nguyên văn spec (`docs/design/08-v6-api-projections.md` dòng 216-226): "Mục tiêu: đóng `BACKLOG → READY`
bằng narrow command, không generic status setter... Phạm vi: application command, event/receipt và
`POST /work-items/{id}/mark-ready` fragment... Không làm: request không có `targetStatus`; projection không
quyết readiness... Thực hiện: reload WorkItem, chạy canonical validator rồi atomically CAS, append registered
`WORK_ITEM_MARKED_READY` v1 và receipt. Same key replay stored success; key mới khi READY conflict... Verify:
replay/concurrency/stale/readiness/cross-project/payload target-status/event golden... Hoàn thành khi: valid
action duy nhất là `mark-ready`, không có public `set-status`."

Đọc `ADR-028 §30` (`docs/architecture/02-architecture-decisions.md` dòng 746-752) — khóa cứng thêm: "Gap
`BACKLOG → READY` được đóng bằng đúng một public command `MarkWorkItemReady`: server chạy lại
readiness/contract validator và CAS đúng transition đó, append registered `WORK_ITEM_MARKED_READY` v1.
Command không nhận target status." — xác nhận `WORK_ITEM_MARKED_READY` là chuỗi wire literal, SCREAMING_SNAKE
đúng như trích, không phải gợi ý cần chuẩn hoá PascalCase theo convention riêng của `internal/app/work`. Đối
chiếu với `internal/app/runtime/event_schema.go`/`blocker.go` thấy family `WORK_ITEM_*`
(`WORK_ITEM_BLOCKED`, `WORK_ITEM_CANCELLED`, `WORK_ITEM_CANCELLATION_REQUESTED`) đã dùng đúng convention này ở
một package khác — `WORK_ITEM_MARKED_READY` khớp đúng họ tên đó, không phải ngoại lệ đơn độc.

Đọc trọn `ExplainWorkItemReadiness`/`WorkItemReadiness` (`internal/app/work/queries.go` dòng 324-381): validator
thật là `workdomain.ValidateReadinessGate`, chạy read-only bên trong `uow.WithReadOnly`, không tự transition.
Đọc `internal/domain/work/work.go` (`ValidateReadinessGate` dòng 362-474, `WorkItem` struct dòng 88-193,
`WorkItemStatus` enum dòng 77-86): validator pure, không I/O, luôn trả `*ReadinessError{Problems []string}`
liệt kê MỌI vi phạm một lần (không dừng ở lỗi đầu tiên); không tự check `item.Status` — việc quyết "đang
BACKLOG hay không" là việc của caller (đúng khớp với việc `MarkWorkItemReady` phải tự check status TRƯỚC khi
gọi validator).

Đọc `internal/app/ports/work.go` — `TransitionWorkItemStatus` (dòng 86-105) đã tồn tại sẵn từ V4-02
("StartWorkflowRun's own READY->ACTIVE transition"), CAS chung cho MỌI `WorkItemStatus`, không riêng
READY->ACTIVE — xác nhận `MarkWorkItemReady` chỉ cần TÁI SỬ DỤNG method này với
`ExpectedStatus=BACKLOG, NextStatus=READY`, không cần thêm method port mới.

Đọc `internal/app/work/commands.go` (`CreateRootWorkItem`) và `scope_expansion.go` (`ApproveScopeExpansion`/
`RejectScopeExpansion`/`WithdrawScopeExpansion`) làm template cấu trúc — `ApproveScopeExpansion` là template
GẦN NHẤT vì nó cũng transition một entity ĐÃ TỒN TẠI (không tạo mới), và đặc biệt: nó tự mint aggregate
identity riêng cho event quyết định (`AggregateType: "ScopeExpansionApproval", AggregateID: cmd.ID`) thay vì
tái dùng `AggregateType: "ScopeExpansionRequest", AggregateID: req.RequestID` — lý do ghi rõ trong doc comment:
"this is the SECOND event on that long-lived aggregate identity...Sequence=1 would collide with it". Đây
chính là vấn đề `MarkWorkItemReady` cũng gặp: mọi WorkItem thật đã có sẵn một event `Sequence=1` trên
`AggregateType="WorkItem"` từ lúc `RootWorkItemCreated`/`ChildWorkItemCreated` — `domain_events` có
`UNIQUE(aggregate_type, aggregate_id, sequence)` (migration 0001/0003) nên append `WORK_ITEM_MARKED_READY`
với cùng `("WorkItem", workItemID, 1)` sẽ va constraint. Giải pháp giống hệt: mint aggregate riêng
`("WorkItemReadyMark", cmd.ID, 1)`.

Đọc `internal/delivery/httpapi/workitem` (V6-04, đã merge) trọn vẹn — `dependencies.go`, `routes.go`
(12 route, tất cả nest dưới `/projects/{projectId}/work-items/...`), `envelope.go`
(`prepareCreateCommand`/`prepareUpdateCommand`/`replayOrProceed`), `errors.go`
(`writeQueryError`/`writeCommandError`), `scope_expansion_commands.go` (Approve/Reject/Withdraw — route
KHÔNG tạo resource mới, y hệt hình dạng `MarkWorkItemReady` cần).

Phát hiện quan trọng về ROUTE PATH: task's spec + 3 nguồn khác (`docs/design/01-system-design.md` dòng 567,
`docs/design/11-v6-00-ux-artifact.md` dòng 245, `ADR-028`) đều trích Y HỆT
`POST /work-items/{id}/mark-ready` — KHÔNG có tiền tố `/projects/{projectId}`, khác hẳn 12 route đã có của
chính package `workitem`. Đọc `internal/delivery/httpapi/run` (V6-06, đã merge SAU V6-04, PR riêng) thấy
đúng tiền lệ cho hình dạng này: `POST /work-items/{workItemId}/runs` và `POST /runs/{runId}/cancel`, cả hai
KHÔNG có `/projects/{projectId}`, `ProjectID` tự suy ra bằng cách reload thẳng WorkItem/Run
(`loadWorkItemProjectID`, `run/start.go` dòng 176-187) thay vì tin path segment. Đọc chính
`baocaov6checklist.md`'s V6-06 section (dòng ~2644-2650, do phiên trước ghi) xác nhận: V6-06 tự trích
NGUYÊN VĂN dòng "Phạm vi" của V6-04A này ("`POST /work-items/{id}/mark-ready`") làm tiền lệ cho quyết định
path của chính nó — nghĩa là cộng đồng task trước đã đọc đúng spec này giống hệt cách task này đọc, và đã
xây sẵn cơ chế "no {projectId} path, derive scope from entity" mà task này chỉ cần tái sử dụng lại đúng
pattern, không phải tự nghĩ ra.

Phát hiện gap thật thứ hai, nghiêm trọng hơn: để `MarkWorkItemReady` transition thật một WorkItem THẬT sang
READY, phải có ít nhất một WorkItem thật, lưu trong sqlite, PASS được `ValidateReadinessGate`. Đọc
`internal/adapters/sqlite/work.go`'s `createWorkItemTx`/`getWorkItemTx`/`scanWorkItemRow` (trước khi sửa)
xác nhận đúng lời cảnh báo trong doc comment của `queries.go`: migration `0007_work_items_contract.sql` đã
thêm 6 cột (`schema_version, behavior, acceptance_json, verification_json, risk, exclusions_json` — cộng
`workflow_version_id` từ trước) nhưng KHÔNG cột nào trong số đó từng được ghi bởi `createWorkItemTx` hay đọc
bởi `getWorkItemTx`/`scanWorkItemRow`. Đồng thời không có bất kỳ application command nào (kể cả
`CreateRootWorkItem`/`CreateChildWorkItem`) từng set các field này trên `work.WorkItem` domain value trước
khi persist — `NewRootWorkItem`/`NewChildWorkItem` là "thin constructor", đúng như doc comment của chính
`work.go` ghi: "a caller needing a fully-contracted WorkItem today sets the exported fields directly...and
then calls ValidateReadinessGate itself". Hệ quả: MỌI WorkItem thật trong sqlite hôm nay LUÔN LUÔN fail
readiness gate — không có cách nào viết một test "happy path" thật cho `MarkWorkItemReady` nếu không tự sửa
gap persistence này trước. Đây không phải việc tự ý mở rộng phạm vi: không có nó, tính năng cốt lõi của
chính task này (transition thật BACKLOG→READY) không bao giờ chạy được với dữ liệu thật, chỉ tồn tại trên
giấy — đúng loại "test xanh nhưng chưa thật sự nối dây" mà doctrine của phiên này luôn cảnh giác (như phát
hiện ở V6-04's own hậu-merge review). Migration comment của `0007` tự xác nhận việc mở rộng an toàn: "Every
new column is nullable... Existing fixture rows...insert work_items by explicit column list without any of
these fields and must keep working unmodified" — không cần migration mới, không phá bất kỳ caller nào đang
chạy.

### Quyết định

1. **Route path KHÔNG có `/projects/{projectId}`, đúng verbatim `POST /work-items/{workItemId}/mark-ready`,
   thêm ADDITIVE vào đúng `RegisterRoutes` đã có của package `workitem`** — không tạo package mới. `ScopeKind`
   vẫn là `httpapi.ScopeProject` (ADR-025: WorkItem không bao giờ installation-scoped) dù path không mang
   `{projectId}` — `run` package (V6-06) đã xác nhận `ScopeKind` là khái niệm logic, độc lập với việc path có
   chứa segment đó hay không.
2. **`ProjectID` suy ra DUY NHẤT bằng cách reload thẳng WorkItem qua `tx.Work().GetWorkItem` bên trong
   `uow.WithReadOnly`** (`loadWorkItemForMarkReady`, mirror y hệt `run/start.go`'s `loadWorkItemProjectID`) —
   không đi qua `workapp.GetWorkItem` (`queries.go`) vì hàm đó đòi một scope đã biết trước (`requireProjectScope`),
   thứ route này không có từ path. Đây KHÔNG phải bỏ qua discipline "reload authoritative target" — vẫn đúng
   discipline đó, chỉ khác nguồn suy scope (từ chính entity, không phải từ path claim).
3. **`MarkWorkItemReadyRequest` chỉ có đúng một field `WorkItemID`** — không có field status/target nào khác
   tồn tại để client có thể lợi dụng; thoả "Không làm: request không có `targetStatus`" bằng construction,
   không phải bằng validation runtime.
4. **Body HTTP tái dùng `emptyBody{}` đã có sẵn** (không tạo DTO thứ tư) — vì `httpapi.DecodeJSON` luôn gọi
   `json.Decoder.DisallowUnknownFields()`, một `{"targetStatus":"DONE"}` bất kỳ bị 400 ngay ở bước decode,
   trước khi handler chạy bất kỳ logic nào — chứng minh "Verify: payload target-status" bằng đúng cơ chế
   chung toàn package, không phải field-by-field check riêng.
5. **Hai loại lỗi từ chối tách biệt, map HTTP khác nhau:**
   - `work.ErrWorkItemNotEligibleForReady` (status hiện tại KHÔNG phải BACKLOG — bao gồm case "đã READY",
     đúng chữ "READY conflict" của spec) → `409 CONFLICT`, không có problem list (không có gì để giải thích,
     đây là state conflict).
   - `*workdomain.ReadinessError` (đang BACKLOG nhưng KHÔNG pass `ValidateReadinessGate`) → trả NGUYÊN VẸN
     (không wrap, `errors.As` xuyên qua) để tầng HTTP tái dùng CHÍNH XÁC field `Problems` — mỗi problem thành
     một `httpapi.ErrorDetail{Field:"readiness", Message:problem}` → cũng `409 CONFLICT` nhưng CÓ chi tiết,
     đúng "reuse that exact logic/vocabulary, don't reinvent it".
6. **Mở rộng `createWorkItemTx`/`getWorkItemTx`/`listWorkItemsTx`
   (`internal/adapters/sqlite/work.go`) để round-trip 6/7 cột contract đã có sẵn từ migration 0007** —
   quyết định khó nhất của task này, VƯỢT phạm vi hẹp "application command + route" nhưng LÀ TIỀN ĐỀ BẮT
   BUỘC để tính năng thật sự chạy được với dữ liệu thật (xem Nghiên cứu). `ApprovalException` KHÔNG được
   round-trip — migration 0007 chưa từng thêm cột cho nó, nên đây vẫn là gap y hệt như trước, không mở rộng
   thêm. Không migration mới, không sửa `CreateRootWorkItem`/`CreateChildWorkItem` (hai command đó vẫn không
   set field nào — WorkItem chúng tạo ra vẫn rỗng contract y hệt trước khi sửa), nên zero regression cho
   caller hiện tại — test fixture cũ (`fixtures.go`, `crashworker_fixtures.go`) không chạm, cột mới luôn NULL
   với chúng đúng như trước.
7. **Test "happy path" tự xây WorkItem đủ điều kiện bằng cách gọi thẳng
   `tx.Work().CreateTaskFamily`/`tx.Work().CreateWorkItem`** (repository method THẬT, không phải raw SQL) với
   một `work.WorkItem` domain value set đủ field tay — đúng "escape hatch" mà chính doc comment của
   `work.go` mô tả cho trường hợp "a caller needing a fully-contracted WorkItem". Không vi phạm "never fake
   results by writing to the database directly" vì đường đi vẫn là repository method thật, không phải
   `INSERT` tay.
8. **ETag response DÙNG `httpapi.ETagFromVersion(result.Version)`** — khác Approve/Reject/Withdraw (luôn trả
   `""` vì result của chúng không có field `Version`), `MarkWorkItemReadyResult` CÓ `Version` nên trả ETag
   thật, hữu ích hơn cho client polling tiếp theo.

### Thực hiện

- `internal/app/work/mark_ready.go` (mới): `ErrWorkItemNotEligibleForReady`, `MarkWorkItemReadyRequest`,
  `MarkWorkItemReadyResult`, hàm `MarkWorkItemReady` — theo đúng khuôn idempotent-command (receipt-check
  đầu `WithSerializedWrite` → reload WorkItem thật → check status BACKLOG → chạy
  `workdomain.ValidateReadinessGate` → `tx.Work().TransitionWorkItemStatus` CAS → append event
  `WORK_ITEM_MARKED_READY` v1 tại aggregate riêng `("WorkItemReadyMark", cmd.ID, 1)` → record receipt).
- `internal/app/work/event_schema.go`: thêm `WorkItemMarkedReadyEventType = "WORK_ITEM_MARKED_READY"`,
  `WorkItemMarkedReadySchemaVersion = 1`, payload struct, decoder, đăng ký vào `RegisterEventSchemas` (xác
  nhận `RegisterEventSchemas` của MỌI package trong repo — kể cả `runtime`, `catalog` — chưa từng được gọi
  từ `cmd/aw/serve.go`, đây là registry test/tooling-only nhất quán toàn repo, không phải gap riêng của task
  này cần vá).
- `internal/app/work/testdata/golden/work_item_marked_ready_v1.json` (mới): golden fixture cho event.
- `internal/app/work/queries.go`: sửa lại doc comment của `WorkItemDetail` cho khớp thực tế mới (6/7 field
  giờ round-trip được ở tầng adapter, nhưng KHÔNG public command nào set chúng — kết luận cũ "mọi WorkItem
  luôn fail readiness qua route công khai hôm nay" vẫn đúng, chỉ sửa lại LÝ DO cho chính xác).
- `internal/adapters/sqlite/work.go`: `createWorkItemTx` thêm 7 cột vào INSERT (`workflow_version_id` +
  6 cột contract); tách hằng `workItemColumns` dùng chung cho `getWorkItemTx`/`listWorkItemsTx`;
  `scanWorkItemRow` scan thêm 6 cột contract, unmarshal JSON cho `acceptance_json`/`exclusions_json`.
- `internal/delivery/httpapi/workitem/routes.go`: thêm route thứ 13 `markWorkItemReady`, doc comment giải
  thích path không có `{projectId}`.
- `internal/delivery/httpapi/workitem/mark_ready_command.go` (mới): `loadWorkItemForMarkReady`,
  `handleMarkWorkItemReady`.
- `internal/delivery/httpapi/workitem/errors.go`: thêm `workdomain` import, check
  `*workdomain.ReadinessError` qua `errors.As` TRƯỚC switch chính (map sang 409 + `ErrorDetail` list), thêm
  `workapp.ErrWorkItemNotEligibleForReady` vào nhánh 409 của switch.
- `internal/delivery/httpapi/workitem/workitem_test.go`: cập nhật
  `TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet` — 12 → 13 operationId, thêm
  `"markWorkItemReady"`.
- `cmd/aw/serve.go`: KHÔNG sửa — route mới nằm trong đúng `workitem.RegisterRoutes(...)` đã được gọi sẵn ở
  dòng 302 (grep xác nhận trực tiếp trước khi kết luận), đúng dự đoán "additive to existing package" trong
  system prompt của task.

### Test

- `internal/app/work/mark_ready_sqlite_test.go` (mới, real sqlite): `seedReadyEligibleRootSQLite` (helper
  dùng repository method thật để seed WorkItem đủ điều kiện); 8 test function — round-trip contract, happy
  path, replay same-key, same-key-different-hash conflict, đã-READY-key-mới-là-conflict-thật, readiness gate
  reject đúng problem list của `ExplainWorkItemReadiness`, đua đồng thời same-key (1 transition + 1 replay),
  đua đồng thời different-key (đúng 1 winner + 1 loser, không double-transition).
- `internal/app/work/event_schema_test.go`: thêm `TestWorkItemMarkedReadyV1_GoldenFixtureDecodes`,
  `TestWorkItemMarkedReadyV1_RealEventPayloadDecodes`.
- `internal/delivery/httpapi/workitem/mark_ready_test.go` (mới, real HTTP + real sqlite): 9 test function —
  happy path (200 + ETag đúng), replay, stale If-Match (412), đã-READY-key-mới-vẫn-conflict (409), WorkItem
  không tồn tại (404, thay cho "cross-project" vì route này không có path scope để đoán sai), readiness gate
  reject qua HTTP (409 kèm đúng `details` khớp từng problem của endpoint `readiness` đã có), payload
  `targetStatus` bị từ chối (400, WorkItem không đổi), thiếu Idempotency-Key/If-Match (400).

### Verify

- **"replay"**: `TestMarkWorkItemReady_Replay_SameKeySameResultSQLite` (app layer) +
  `TestMarkWorkItemReady_Replay_SameKeySameResult` (HTTP layer) — cùng key/hash trả nguyên kết quả cũ, không
  transition lần hai.
- **"concurrency"**: `TestMarkWorkItemReady_ConcurrentSameIdempotencyKey_OneTransitionsOtherReplaysSQLite` +
  `TestMarkWorkItemReady_ConcurrentDifferentIdempotencyKeys_ExactlyOneTransitionsSQLite` — cả hai chạy
  goroutine thật, sqlite thật, `_txlock=immediate` serialize hai transaction, kết quả xác định (không cần
  chấp nhận tập nhiều status code như race Approve/Reject vì `MarkWorkItemReady` không có tầng
  optimistic-If-Match ở app layer giống Approve — mọi lần thua đều là `ErrWorkItemNotEligibleForReady`, đơn
  trị).
- **"stale"**: `TestMarkWorkItemReady_StaleIfMatch_PreconditionFailed` (HTTP) — If-Match cũ sau khi version
  đã bump → 412, y hệt cơ chế `TestApproveScopeExpansion_StaleIfMatch_PreconditionFailed` đã có.
- **"readiness"**: `TestMarkWorkItemReady_ReadinessGateFailure_RejectedWithSameProblemsAsExplainSQLite` (app,
  so sánh trực tiếp với `ExplainWorkItemReadiness` cùng WorkItem) +
  `TestMarkWorkItemReady_ReadinessGateFailure_Returns409WithProblems` (HTTP, so `details` với endpoint
  `readiness` sẵn có).
- **"cross-project"**: route này không mang `{projectId}` nên không có khái niệm "client khai sai project" —
  tương đương thực dụng là `TestMarkWorkItemReady_UnknownWorkItem_ReturnsNotFound` (404, không lộ thông tin
  khác biệt giữa "không tồn tại" và "thuộc project khác"), giải thích rõ trong doc comment của chính test.
- **"payload target-status"**: `TestMarkWorkItemReady_PayloadWithTargetStatusField_Rejected` (HTTP, 400,
  WorkItem không đổi) + archtest `TestWorkItemPackageRequestBodiesNeverAcceptAStatusField` (đã có từ V6-04,
  tự động quét file mới vì AST-scan toàn package — `emptyBody{}` không có field nào để vi phạm).
- **"event golden"**: `TestWorkItemMarkedReadyV1_GoldenFixtureDecodes` — byte-verify đúng
  `work_item_marked_ready_v1.json`.
- **"Hoàn thành khi: valid action duy nhất là mark-ready, không có public set-status"**: xác nhận bằng
  `TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet` (13 operationId đóng, không route thứ 14 ẩn)
  cộng việc đọc lại toàn bộ `routes.go` — không route "set-status"/"transition" generic nào tồn tại.

### Kết quả

Branch `feat/v6-04a-mark-work-item-ready` từ `origin/master` tại `c8163d3`. 11 file mới/sửa: 1 command mới
(`mark_ready.go`), 1 event type mới đăng ký đúng registry, 1 golden fixture, 1 doc-comment fix
(`queries.go`), 3 file sqlite adapter sửa (gap persistence contract thật, không migration mới), 1 route mới
+ 1 handler file mới + 1 error-mapping sửa ở tầng HTTP, 2 file test hiện có cập nhật (operationId set,
event golden). Test mới: 8 (app layer, real sqlite) + 2 (event golden) + 9 (HTTP layer, real server + real
sqlite) = 19 test function mới.

`go build ./...` và `go vet ./...` sạch. `go test ./internal/app/work/...`,
`./internal/delivery/httpapi/...`, `./internal/archtest/...`, `./internal/adapters/sqlite/...` đều xanh
100%. `go test ./...` toàn repo: 1 package fail
(`internal/integration/v5accept`, 2 test:
`TestV5AcceptFullComposition_RealFourRoleGraphReachesSucceededAndSurvivesRestart`,
`TestV5AcceptScopeViolation_RealDiffRejectsOutOfScopeWrite` — cả hai kiểu "did not reach state within
deadline"). Xác minh KHÔNG liên quan diff bằng cách tạo worktree riêng tại đúng `origin/master` (không có
bất kỳ thay đổi nào của task này) và chạy lại: `TestV5AcceptScopeViolation...` pass ngay lần đầu;
`TestV5AcceptFullComposition...` fail 1/3 lần chạy lặp lại NGAY TRÊN BASELINE SẠCH — xác nhận đây là flake
timing-sensitive có sẵn từ trước (real 4-role workflow graph, real process executor, nhạy tải hệ thống),
không phải regression của diff này. Không nằm trong danh sách known-flakes đã có của phiên trước, nhưng đã
tự verify bằng baseline reproduction trước khi kết luận, đúng discipline "verify diff-scope-unrelated, đừng
chỉ đoán theo tên".

## V6-10F — ReleaseSet và local Git endpoints

### Bối cảnh

Trích nguyên văn task spec từ `docs/design/08-v6-api-projections.md` (dòng 485-494): "Mục tiêu: expose V6-10E
authorities and exact operation status"; "Phụ thuộc: V6-00, V6-01A, V6-02, V6-02A, V6-10B, V6-10E"; "Phạm vi:
list/create/detail/seal/abandon, per-entry local-commit POST/status"; "Không làm: no Git adapter/worker call
and no remote route"; "Thực hiện: dispatch public commands/queries; local commit returns accepted operation ID
for wait"; "Verify: schemas/replay/stale/partial/dispatch architecture; route inventory excludes remote
verbs"; "Hoàn thành khi: release/local commit usable from API without delivery Git authority"; "Nguồn:
ADR-014, AK-ARCH-015C". Cả 6 phụ thuộc đều đã merge — `origin/master` tại `c8163d3` (V6-07B) khi bắt đầu, và
V6-10E (ReleaseSet/local-commit application authority) là task ngay trước, cùng domain, đã ghi lại đầy đủ ở
đoạn trên trong chính file này.

Phiên này khởi động trong một worktree đã bị dọn dẹp giữa chừng (rate-limit reset khiến session phải tự tạo
lại worktree mới `feat/v6-10f-releaseset-local-git-endpoints` từ `origin/master` — không mất tiến độ vì mọi
file đã đọc trước đó đều ở cùng commit `c8163d3`, xác nhận lại bằng `git log --oneline -3` trước khi tiếp tục
viết code).

### Nghiên cứu

Đọc toàn bộ hạ tầng HTTP chung trước khi viết dòng nào: `route.go` (`RouteRegistry.Register` panic khi trùng
`(Method,Path)` hoặc trùng `OperationID`), `errors.go` (`WriteResourceHidden` leakage-normalization,
`StatusForAppErrorCode` bảng chung, `WriteAppError`), `page.go`/`cursor.go`/`freshness.go`/`action.go` (không
dùng trực tiếp trong task này — ReleaseSet list không cursor-phân trang, giống `ListWorkItems`/
`ListReleaseSetsForFamily` đã là "plain, unbounded shape" từ trước), `commandenvelope.go`/`receiptreplay.go`
(flow chuẩn: `RequireIdempotencyKey`/`RequireIfMatch` → `CanonicalizeJSON` → `SemanticHash` → `LookupReceipt`
→ replay/conflict → dispatch → `EncodeResult`).

Đọc `internal/delivery/httpapi/workitem` toàn bộ (V6-04) làm khuôn mẫu kết cấu — đây là tiền lệ GẦN NHẤT cho
một package vừa có command vừa có query, và routes.go's own doc comment của chính nó giải thích lý do dùng
subpackage riêng thay vì file phẳng trong `httpapi` (khác hẳn cách V6-10B làm trước đó — flat file — vì lúc
V6-10B chạy chỉ có 1 session chạm `internal/delivery/httpapi`, còn từ V6-04 trở đi luôn có nhiều task chạy
song song, mỗi task một subpackage tránh đụng file/định danh của nhau). Copy gần như nguyên xi 3 file hạ tầng
riêng-package của `workitem` (`dependencies.go`, `envelope.go` với `prepareCreateCommand`/`prepareUpdateCommand`/
`replayOrProceed`, `errors.go` với `writeQueryError`/`writeCommandError`/`writeValidationError`/
`writeReceiptHashConflict`/`writePreconditionFailed`) — đây là quy ước lặp lại y hệt mọi subpackage (mỗi
subpackage tự có bản sao riêng, không share code giữa 2 subpackage, đúng "Contract chung §1.8: mỗi endpoint
task sở hữu subpackage/test riêng").

Đọc toàn bộ `internal/app/work/release_set.go` và `release_set_queries.go`: `CreateReleaseSet(ctx, uow, ids,
cmd, req{ProjectID, FamilyID, Repositories[]RepositoryReleaseRequest})`, `SealReleaseSet`/
`AbandonReleaseSet(ctx, uow, cmd, req{ReleaseSetID})` (cả hai dùng `cmd.ExpectedVersion` làm fence, không có
field version riêng trong request struct), `GetReleaseSet(ctx, uow, releaseSetID)` — PHÁT HIỆN quan trọng:
hàm này KHÔNG nhận `scope`/`projectID` để tự kiểm tra — doc comment của chính file xác nhận đây là "public,
read-only entry point a future HTTP/CLI layer (V6-10F) can call directly", nghĩa là chính task này phải TỰ
làm phần kiểm scope (so `detail.ProjectID` với path `{projectId}`) trước khi trả về, không được tin hàm query
đã làm sẵn. `ListReleaseSetsForFamily(ctx, uow, familyID)` cũng vậy — không tự check project, nhưng may mắn
route của nó luôn đi kèm path `{familyId}`, nên authorize được qua `workapp.GetTaskFamily(scope, familyID)`
(hàm này CÓ tự check scope, đã xác nhận từ `queries.go`) trước khi gọi.

Đọc toàn bộ `internal/app/releasesetcommit/commands.go` kể cả doc comment package (rất dài, giải thích kỹ lý
do package này TÁCH khỏi `internal/app/work`: "producer/consumer" sống chung 1 package vì cả 2 cần port I/O
thật, khác `internal/app/work` tự cam kết "không có lý do gì import os/os/exec"). `RequestReleaseSetLocalCommit(ctx,
uow, ids, cmd, req{ProjectID, ReleaseSetID, ExpectedReleaseSetVersion, RepositoryWorkspaceID,
ExpectedWorkspaceVersion, Message, AuthorName, AuthorEmail})` trả về `RequestReleaseSetLocalCommitResult{
ReleaseSetLocalCommitID, ReleaseSetID, RepositoryWorkspaceID, State, JobID, Marker}` — LUÔN `State=REQUESTED`
(một accepted/pending result, không bao giờ đồng bộ "done"). Đọc kỹ phần "Không làm" của package: "no Git call
inside this function's own transaction — real Git work happens later, in a worker, entirely outside this
command" — xác nhận route POST của task này chỉ cần dispatch đúng hàm này, không có gì khác phải làm để tuân
thủ "Không làm" của chính task.

Grep xác nhận đúng như brief đã nêu: hoàn toàn KHÔNG có query nào đọc trạng thái MỘT operation
`ReleaseSetLocalCommit` theo ID ở tầng application — chỉ có `tx.Work().GetReleaseSetLocalCommit(ctx, id)
(work.ReleaseSetLocalCommit, error)` ở tầng Tx-level repository (`internal/app/ports/work.go` dòng ~396,
implement ở cả sqlite lẫn fake), và MỌI caller thật của method này đều đã ở sẵn trong một closure
`WithSerializedWrite`/`WithReadOnly` (`RequestReleaseSetLocalCommit`'s own receipt-replay reconstruction,
`execute.go`'s `loadIntent`). Đây đúng là gap thật task's own brief đã chỉ ra — phải tự thêm.

Đọc `internal/domain/work/release_set_local_commit.go` toàn bộ: 3-state `REQUESTED→{COMMITTED,FAILED}`,
field đầy đủ (`ProjectID, ReleaseSetID, RepositoryWorkspaceID, RepositoryID, ExpectedGeneration,
ExpectedWorkspaceVersion, Actor, Message, AuthorName, AuthorEmail, MessageHash, Marker, State,
FailureReason, ParentVCSObjectID, ResultVCSObjectID, JobID, CreatedAt, CompletedAt, Version`) — đúng field
set cần map sang DTO wire cho query status mới.

Đọc `internal/domain/work/release_set.go` (`NewReleaseSet`) phát hiện: validate Verdict/field rỗng đều trả
`errors.New(...)` THUẦN, không sentinel — nếu không tự validate ở tầng HTTP trước khi dispatch, một request
sai (verdict rác, thiếu field) sẽ rơi vào nhánh mặc định 500 INTERNAL của `writeCommandError`, sai hẳn ngữ
nghĩa HTTP. Áp dụng lại đúng bài học `workitem`'s `validateScopeGrantBodies` đã dạy: validate field-level ở
DTO trước khi build command envelope.

Đọc `internal/adapters/sqlite/release_set.go`'s `createReleaseSetTx` phát hiện: mỗi entry phải trỏ tới một
row `repositories` CÓ THẬT (kiểm tường minh trước insert, không dựa FK opaque) — nhưng KHÔNG kiểm repository
đó có thuộc đúng `family`'s WorkspaceSet hay không; ảnh hưởng trực tiếp cách dựng fixture cho test "partial"
(xem Quyết định #6).

Đọc `internal/adapters/sqlite/fixtures.go` toàn bộ (`SeedFixtureOwners`, `SeedFixtureRepositoryWorkspace(WithLocator)`)
— helper thật, "production code must never call this", chính là con đường sanctioned để seed
Project/TaskFamily/RepositoryWorkspace thật cho test HTTP không cần đi qua toàn bộ flow provisioning. Phát
hiện: `workspace_sets.family_id` có UNIQUE constraint (1 WorkspaceSet/TaskFamily) — gọi
`SeedFixtureRepositoryWorkspace` 2 lần cho CÙNG family sẽ vỡ constraint (gặp thật khi viết test đầu tiên,
thấy `UNIQUE constraint failed: workspace_sets.family_id`) — cần một fixture helper mới cho trường hợp "thêm
1 repository workspace vào 1 WorkspaceSet đã có sẵn" (xem Quyết định #6).

Đọc `internal/app/releasesetcommit/execute_test.go` (toàn bộ `newExecuteFixture`) làm mẫu duy nhất trong repo
đã dựng real Git repo + real `gitworktree.Provider.Provision` + real sqlite cho chính domain này — tái dùng
lại kỹ thuật `createTestGitRepository`/`runTestGitCommand`/`writeTestFile` (viết lại cục bộ trong package test
của task này, vì các hàm gốc là unexported của package khác) cho đúng 1 test "partial" cần chạy worker thật.

Đọc `internal/archtest/workspace_delivery_boundary_test.go`'s
`TestDeliveryWorkspaceRoutesNeverReachWorkspaceIOOrExecutor` làm mẫu bắt buộc cho architecture test riêng của
task này — cấm import `"os"`/`"os/exec"`/`internal/adapters/...` VÀ cấm gọi tên selector cụ thể (không chỉ
cấm import, vì package này hợp pháp import `internal/app/releasesetcommit` — package đó CŨNG export
`ExecuteReleaseSetLocalCommit`, hàm worker thật). Đọc `internal/app/ports/localcommit.go` xác nhận
`CreateLocalCommit` là "the only Git-mutating method this port — or any port in this codebase — declares" —
đúng tên cần cấm thứ hai.

Grep `cmd/aw/serve.go` xác nhận đúng quy ước mọi task endpoint gần đây: import subpackage với alias
`httpXxx`, gọi đúng 1 dòng `RegisterRoutes(routes, Dependencies{...})` ngay trước `routesFinalized = true` —
không có gì khác phải sửa trong file composition root.

### Quyết định

1. **URL scheme lồng theo TaskFamily cho create/list, lồng theo ReleaseSet cho mọi thứ còn lại** —
   `POST/GET /projects/{projectId}/task-families/{familyId}/release-sets` (create/list),
   `GET /projects/{projectId}/release-sets/{releaseSetId}` (detail),
   `POST .../release-sets/{releaseSetId}/{seal,abandon}`,
   `POST .../release-sets/{releaseSetId}/local-commits` (request),
   `GET .../release-sets/{releaseSetId}/local-commits/{localCommitId}` (status). Lý do đặt `familyId` trên
   path cho create/list (thay vì để `familyId` là field trong body, dù domain request struct có field đó):
   `familyId` LÀ resource cha thật của một ReleaseSet (đúng data model — `ReleaseSet.FamilyID` cố định từ lúc
   tạo), và đặt trên path cho phép áp dụng ĐÚNG discipline "reload route's real target from path before
   Idempotency-Key/body are even read" mà `workitem`'s `handleRequestScopeExpansion` đã lập tiền lệ (reload
   `workapp.GetTaskFamily(scope, familyId)` — hàm này tự check scope — TRƯỚC khi đọc body), thay vì phải tự
   viết logic check `ErrCrossProjectReference` bằng tay như khi familyId nằm trong body.
2. **`requestReleaseSetLocalCommitBody` mang `ExpectedReleaseSetVersion`/`ExpectedWorkspaceVersion` như field
   JSON thường, KHÔNG qua `If-Match`.** Route request-local-commit là CREATE-shaped (tạo một
   `ReleaseSetLocalCommit` operation MỚI, không có version cũ nào của chính resource này để precondition) —
   `prepareCreateCommand` (không phải `prepareUpdateCommand`) áp dụng đúng discipline V6-02 "If-Match chỉ bắt
   buộc cho update, không cho create". 2 field version đã LÀ 1 phần payload được hash vào `SemanticHash` —
   một replay với version khác thật sự là "different body", đúng ngữ nghĩa "different body conflict trước
   I/O" đã thiết lập.
3. **Response local-commit POST = `202 Accepted`, không phải `201 Created`.** Đây là điểm khác biệt duy nhất
   so với mọi route CREATE khác trong `workitem`/`httpcatalog` (đều 201) — vì resource được tạo
   (`ReleaseSetLocalCommit`) chỉ mới ở trạng thái REQUESTED, việc "commit Git thật" chưa xảy ra — đúng nghĩa
   HTTP 202 ("the request has been accepted for processing, but the processing has not been completed") và
   đúng chữ "local commit returns accepted operation ID for wait" trong chính task spec.
4. **`GetReleaseSetLocalCommitStatus` — query mới, đặt trong CHÍNH `internal/app/work/release_set_queries.go`
   (không tạo file/package mới).** Domain type `work.ReleaseSetLocalCommit` và method Tx-level
   `tx.Work().GetReleaseSetLocalCommit` đều đã sống trong `ports.WorkRepository`/package `work` từ trước (do
   V6-10E build) — thêm một query đọc thuần (`uow.WithReadOnly`, giống hệt shape `GetReleaseSet` ngay phía
   trên nó trong cùng file) không vi phạm biên giới "không I/O thật" mà package `work` tự cam kết, và đặt
   đúng vị trí "future HTTP/CLI layer (V6-10F) can call directly" mà chính doc comment gốc của file đã tự dự
   trù sẵn tên. Trả về DTO mới `ReleaseSetLocalCommitStatus` (không trả thẳng domain type
   `work.ReleaseSetLocalCommit` — đúng "query trả DTO riêng, không serialize aggregate" mà `ReleaseSetDetail`
   đã lập).
5. **Query status KHÔNG tổng hợp (aggregate) nhiều operation của cùng 1 ReleaseSet thành 1 response.** Đọc kỹ
   Verify line "partial: a ReleaseSet with some entries committed and some not — the status query must report
   this accurately, not just an aggregate 'some/all'" — quyết định: giữ nguyên thiết kế thuần túy
   per-operation (1 `localCommitId` = 1 response), KHÔNG viết thêm một endpoint "GET tổng trạng thái mọi
   local-commit của 1 ReleaseSet" nào — vì bản thân thiết kế per-operation ĐÃ tự động "chính xác, không phải
   aggregate" (mỗi entry của ReleaseSet, nếu có yêu cầu local commit, có ID riêng, trạng thái riêng, không
   bao giờ bị gộp) — cách chứng minh Verify line này là viết TEST thật (2 repository trong 1 ReleaseSet, 1
   COMMITTED thật + 1 REQUESTED), không phải thêm code sản xuất mới.
6. **Fixture helper mới `sqlite.SeedFixtureAdditionalRepositoryWorkspace`** (`internal/adapters/sqlite/fixtures.go`,
   additive, mirror gần như nguyên xi `seedFixtureRepositoryWorkspaceTx` nhưng bỏ hẳn bước insert
   `workspace_sets` — chỉ thêm `repositories` + `repository_workspaces` vào một `workspace_set` ĐÃ CÓ SẴN)
   — cách duy nhất để test "partial" (Quyết định #5) có được 2 repository CÙNG một family/WorkspaceSet mà
   không vỡ UNIQUE constraint `workspace_sets.family_id` (xem Nghiên cứu). Đây là fixture-only, "production
   code must never call this", đúng quy ước toàn bộ `fixtures.go`.
7. **`writeCommandError` map `releasesetcommit.ErrReleaseSetEntryNotFound` → 400, `ErrWorkspaceNotReady`/
   `workapp.ErrReleaseSetNotOpen`/`ports.ErrLocalCommitMarkerCollision` → 409, mọi `ErrPersistenceNotFound`/
   `ErrScopeMismatch`/`ErrCrossProjectReference` → 404 ẩn danh.** Theo đúng nguyên tắc `workitem/errors.go` đã
   lập: lỗi "request tự nó sai hình dạng, không do race" → 400; lỗi "trạng thái thật đã đổi, phát hiện qua
   CAS/receipt/marker" → 409 (caller được xem, vì caller đã có quyền truy cập đúng scope); lỗi "tham chiếu
   tới resource ngoài scope/không tồn tại" → 404 ẩn danh (leakage-normalized).
8. **`handleGetReleaseSetLocalCommitStatus` tự kiểm CẢ `ProjectID` LẪN `ReleaseSetID`** sau khi load status
   (không chỉ `ProjectID` như `handleGetReleaseSet`) — vì URL của route này lồng theo CẢ 2 cấp
   (`{projectId}/release-sets/{releaseSetId}/local-commits/{localCommitId}`), một `localCommitId` có thật,
   đúng project, nhưng gọi qua URL của một `releaseSetId` KHÁC (không phải release set nó thực sự thuộc về)
   phải bị ẩn giống hệt một ID chưa từng tồn tại — mở rộng đúng nguyên tắc leakage-normalization sang cả
   phần path-nesting, không chỉ phần scope.

### Thực hiện

- `internal/app/work/release_set_queries.go` (sửa, additive): thêm `ReleaseSetLocalCommitStatus` DTO,
  `releaseSetLocalCommitStatus(intent)` converter, `GetReleaseSetLocalCommitStatus(ctx, uow,
  releaseSetLocalCommitID)` — mở `uow.WithReadOnly`, gọi `tx.Work().GetReleaseSetLocalCommit`. Cập nhật doc
  comment đầu file ghi rõ phần bổ sung của task này.
- `internal/adapters/sqlite/fixtures.go` (sửa, additive): thêm `SeedFixtureAdditionalRepositoryWorkspace`.
- `internal/delivery/httpapi/releaseset/` (package mới, 7 file production):
  - `dependencies.go`: `Dependencies{UnitOfWork, IDs, Clock}`.
  - `envelope.go`: `prepareCreateCommand`/`prepareUpdateCommand`/`replayOrProceed` (bản sao riêng của
    package này, mirror `workitem`).
  - `errors.go`: `writeQueryError`/`writeCommandError`/`writeValidationError`/`writeReceiptHashConflict`/
    `writePreconditionFailed`.
  - `dto.go`: `repositoryReleaseBody` + `validateRepositoryReleaseBodies` (kiểm field rỗng, verdict hợp lệ
    qua `gate.Verdict.IsValid()`, trùng repositoryId trong cùng request), `emptyBody`.
  - `release_set_commands.go`: `handleCreateReleaseSet`, `loadReleaseSetForUpdate` (helper dùng chung 4 route
    khác), `handleSealReleaseSet`, `handleAbandonReleaseSet`.
  - `release_set_queries.go`: `releaseSetListResponse`, `handleGetReleaseSet`, `handleListReleaseSetsForFamily`.
  - `local_commit_commands.go`: `requestReleaseSetLocalCommitBody`, `handleRequestReleaseSetLocalCommit`
    (202 Accepted).
  - `local_commit_queries.go`: `handleGetReleaseSetLocalCommitStatus` (kiểm cả ProjectID lẫn ReleaseSetID).
  - `routes.go`: doc comment đầy đủ (route inventory 7 route) + `RegisterRoutes` đăng ký cả 7.
- `internal/archtest/releaseset_delivery_boundary_test.go` (mới):
  `TestDeliveryReleaseSetRoutesNeverReachGitOrWorker` — quét `internal/delivery/httpapi/releaseset`, cấm
  import `"os"`/`"os/exec"`/`internal/adapters/...`, cấm gọi `.ExecuteReleaseSetLocalCommit(`/
  `.CreateLocalCommit(`.
- `cmd/aw/serve.go` (sửa, đúng 1 khối nhỏ): import alias `httpreleaseset`, thêm
  `httpreleaseset.RegisterRoutes(routes, httpreleaseset.Dependencies{UnitOfWork: uow, IDs: idsource.Random{},
  Clock: clock.System{}})` ngay trước `routesFinalized = true`, giữ nguyên toàn bộ phần còn lại.

### Test

- `internal/delivery/httpapi/releaseset/releaseset_test.go` (real `httpapi.NewServer` qua goroutine + real
  `http.Client`, real `*sqlite.Store`, seed qua `sqlite.SeedFixtureOwners`/`SeedFixtureRepositoryWorkspace` —
  đúng hard rule #1, không fabricate row): 16 test —
  `TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet` (đúng 7 operationId, mọi `ScopeKind=ScopeProject`);
  `TestRegisterRoutes_ExcludesAnyRemoteGitVerb` (tách từ/word-boundary thật — bắt được false-positive "pr"
  bên trong "projects" ngay lần chạy đầu, phải viết lại bằng tokenizer camelCase/non-letter thay vì substring
  thô); `TestFullJourney_CreateGetListSealAndLocalCommitRequestStatus` (create→detail→list→seal→request local
  commit→status, happy path đầy đủ); `TestAbandonReleaseSet_HappyPath`;
  `TestCreateReleaseSet_SameIdempotencyKey_ReplaysWithoutCreatingSecondReleaseSet`/
  `TestCreateReleaseSet_SameKeyDifferentBody_ConflictsBeforeSecondInsert`/
  `TestRequestReleaseSetLocalCommit_SameIdempotencyKey_ReplaysWithoutSecondOperation` (replay + conflict);
  `TestSealReleaseSet_StaleIfMatch_PreconditionFailed`/`TestRequestReleaseSetLocalCommit_StaleReleaseSetVersion_Conflict`
  (stale); `TestSealReleaseSet_AlreadySealed_Conflict` (2 seal thật liên tiếp, key khác nhau, ETag mới sau
  seal đầu — 409 từ `ErrReleaseSetNotOpen`, không phải từ precondition); `TestGetReleaseSet_AnotherProject_ReturnsNotFound`/
  `TestGetReleaseSetLocalCommitStatus_WrongReleaseSetInPath_ReturnsNotFound` (leakage-normalization, byte-for-byte
  giống response ID không tồn tại — bài test thứ 2 phát hiện `workspace_sets.family_id` UNIQUE constraint
  ngay lần chạy đầu, phải sửa lại fixture dùng 1 family + 2 ReleaseSet cùng trỏ `repo-a` thay vì 2 family);
  `TestCreateReleaseSet_EmptyRepositories_Returns400WithFieldDetail`/`TestCreateReleaseSet_InvalidVerdict_Returns400WithFieldDetail`/
  `TestRequestReleaseSetLocalCommit_MissingMessage_Returns400WithFieldDetail` (schema validation, đúng
  `ErrorDetail.Field` cụ thể); `TestMutatingRoutes_RequireIdempotencyKey`.
- `internal/delivery/httpapi/releaseset/local_commit_partial_test.go` (mới, 1 test):
  `TestLocalCommitStatus_PartialAcrossTwoRepositories_ReportsEachEntryAccurately` — real Git repo tạm +
  real `gitworktree.Provider` + real sqlite: 1 ReleaseSet, 2 entry (`repo-a`, `repo-b`, cùng family qua
  `SeedFixtureRepositoryWorkspaceWithLocator` + `SeedFixtureAdditionalRepositoryWorkspace` mới); request local
  commit cho CẢ hai qua route HTTP thật (202 cả hai); chỉ chạy
  `releasesetcommit.ExecuteReleaseSetLocalCommit` (worker thật, gọi trực tiếp trong test — không qua HTTP,
  đúng vì package `releaseset` tự nó không bao giờ được gọi hàm này) cho operation của `repo-a`; xác nhận GET
  status trả `COMMITTED` + `ResultVCSObjectID` thật khác base cho `repo-a`, còn `repo-b` vẫn `REQUESTED`,
  `ResultVCSObjectID` rỗng, và 2 `ReleaseSetLocalCommitID` khác nhau — chứng minh trực tiếp Verify line
  "partial ... report this accurately, not just an aggregate 'some/all'".
- `internal/archtest/releaseset_delivery_boundary_test.go`: `TestDeliveryReleaseSetRoutesNeverReachGitOrWorker`
  pass; chạy lại toàn bộ `internal/archtest` (18 test kể cả `TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt`
  vốn quét đệ quy toàn bộ `internal/delivery/httpapi` — tự động phủ luôn subpackage mới của task này) — pass
  100%.
- `go build ./...`, `go vet ./...` sạch trên toàn bộ module.
- `go test` có mục tiêu (không chờ suite đầy đủ chạy xong trước khi mở PR, theo đúng quy ước phiên này —
  CI là gate cuối cùng cho suite đầy đủ): `internal/app/work`, `internal/app/releasesetcommit`,
  `internal/archtest` (18 test), `internal/delivery/httpapi` và mọi subpackage của nó (kể cả `releaseset`,
  `workitem`, `catalog`, `run`, `decision`, `definitions`, `evidence`, `message`, `recovery`, `adapterbuild`,
  `safesettings`), `cmd/aw` — toàn bộ pass sạch, không regression nào phát hiện được ở phạm vi trực tiếp phụ
  thuộc/bị ảnh hưởng bởi diff của task này. Một lần chạy `go test ./...` toàn module (~90 package, bao gồm
  cả integration/acceptance suite chậm) được khởi động song song trong lúc viết báo cáo này — CI của PR sẽ là
  bằng chứng đầy đủ cuối cùng cho toàn bộ module.

### Verify

- "schemas": `TestCreateReleaseSet_EmptyRepositories_Returns400WithFieldDetail`/
  `TestCreateReleaseSet_InvalidVerdict_Returns400WithFieldDetail`/
  `TestRequestReleaseSetLocalCommit_MissingMessage_Returns400WithFieldDetail` — mỗi route mutating đều có ít
  nhất 1 test field-level 400 trước khi build command envelope.
- "replay": `TestCreateReleaseSet_SameIdempotencyKey_ReplaysWithoutCreatingSecondReleaseSet`/
  `TestRequestReleaseSetLocalCommit_SameIdempotencyKey_ReplaysWithoutSecondOperation` (cùng key, cùng body →
  200 + ID gốc, không tạo bản ghi thứ 2) + `TestCreateReleaseSet_SameKeyDifferentBody_ConflictsBeforeSecondInsert`
  (cùng key, body khác → 409 trước mọi I/O thật).
- "stale": `TestSealReleaseSet_StaleIfMatch_PreconditionFailed` (412, ETag sai) +
  `TestRequestReleaseSetLocalCommit_StaleReleaseSetVersion_Conflict` (409,
  `ExpectedReleaseSetVersion` sai — `ports.ErrOptimisticConflict` thật từ tầng command, không phải tầng
  HTTP tự đoán).
- "partial": `TestLocalCommitStatus_PartialAcrossTwoRepositories_ReportsEachEntryAccurately` — chứng minh
  bằng dữ liệu THẬT (1 commit Git thật, không giả lập), không chỉ bằng thiết kế API.
- "dispatch architecture": `TestDeliveryReleaseSetRoutesNeverReachGitOrWorker` (cấm import
  `os`/`os/exec`/`internal/adapters/...`, cấm gọi `ExecuteReleaseSetLocalCommit`/`CreateLocalCommit`) +
  `TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt` (cấm `.Record(`/`.WithSerializedWrite(` — quét đệ quy,
  tự động phủ package mới).
- "route inventory excludes remote verbs": `TestRegisterRoutes_ExcludesAnyRemoteGitVerb` — enumerate toàn bộ
  7 route đã đăng ký, tách từ theo word-boundary thật (không phải substring thô — đã tự bắt lỗi false-positive
  của chính nó lúc viết, xem phần Test), xác nhận không route/operationId nào chứa
  push/fetch/pull/pr/merge/rebase/remote/force.
- "Hoàn thành khi — release/local commit usable from API without delivery Git authority": chứng minh kép —
  (1) `TestFullJourney_CreateGetListSealAndLocalCommitRequestStatus` chạy toàn bộ vòng đời qua HTTP thuần
  (không SQL tay, không thao tác Git tay) tới tận lúc có `JobID`/`Marker` thật; (2)
  `TestDeliveryReleaseSetRoutesNeverReachGitOrWorker` xác nhận structural — package `releaseset` không có
  khả năng tự chạm Git dù cố tình.

### Kết quả

Thêm 1 query application mới (`GetReleaseSetLocalCommitStatus`, additive trong
`internal/app/work/release_set_queries.go` đã có sẵn — đúng gap task brief đã chỉ ra: trước task này, không
có cách nào đọc trạng thái một operation `ReleaseSetLocalCommit` ngoài việc tự mở transaction gọi thẳng
`tx.Work()`). Package mới hoàn toàn `internal/delivery/httpapi/releaseset` (7 file production, 2 file test,
17 test function) — 7 route HTTP thật lần đầu tồn tại: `POST/GET .../task-families/{familyId}/release-sets`,
`GET .../release-sets/{releaseSetId}`, `POST .../release-sets/{releaseSetId}/{seal,abandon}`,
`POST .../release-sets/{releaseSetId}/local-commits`, `GET .../local-commits/{localCommitId}`. 1
architecture test mới trong `internal/archtest`. 1 fixture helper mới, additive, trong
`internal/adapters/sqlite/fixtures.go`. `cmd/aw/serve.go` thêm đúng 1 khối gọi `RegisterRoutes` — đã grep xác
nhận trực tiếp, không suy đoán từ test pass (đúng bài học V6-04 để lại: 30 test xanh từng không cứu một
package chưa từng được `serve.go` gọi tới). Không route nào tự ghi receipt hay chạm Git/worker thật — cả 2
khẳng định đều có architecture test THẬT đứng sau, không chỉ doc comment. `go build/vet ./...` sạch toàn
module.

## V6-10A — Doctor endpoint

### Bối cảnh

V6-10A bọc HTTP lên trên `internal/app/doctor.Run`/`Options` (V1-11, ADR-022) — package này đã tồn tại đầy đủ,
đã có golden test HEALTHY/DEGRADED/BLOCKED riêng ở tầng application (`internal/app/doctor/report_golden_test.go`),
và đã tự phân biệt rạch ròi 3 câu hỏi Liveness/Readiness/Capability trong chính doc comment của package. Việc
của task này chỉ là: expose `GET /doctor`, cấu thành `doctor.Options` thật từ những gì `cmd/aw/serve.go` đã có
trong scope lúc boot, và xử lý 2 khoảng trống brief giao việc tự nêu ra — (1) `aw serve` có tự dựng một
`config.Config` value nào chưa (không — chỉ đọc `--db`/`--artifact-root` rời rạc), và (2) V6-10J (adapter-build
registry, đã merge PR #49) nên được Doctor SURFACE bằng cách nào — nhúng dữ liệu registry lặp lại vào response,
hay chỉ trả một "operation link". Batch này chạy song song với V6-10D/V6-10F (`baocaov6checklist.md` là điểm
giao chung, append-only — xử lý merge conflict khi finalize như mọi lần trước).

### Nghiên cứu

Đọc trước khi viết code:

- `docs/design/08-v6-api-projections.md` dòng 423-432 (V6-10A) đọc trực tiếp: "Thực hiện: installation-scoped
  query combines typed component status and operation links; no credentials" — chữ "operation LINKS" (không
  phải "operation DATA"/"registry snapshot") là tín hiệu rõ ràng: response trỏ TỚI route khác, không nhúng lại
  dữ liệu route đó đã trả. "Không làm: không duplicate repository onboarding/history/retry routes owned by
  V6-03A" — Doctor chỉ được SURFACE link, không bao giờ tự cài lại logic của route khác.
- `internal/app/doctor/doctor.go`: đọc toàn bộ doc comment package — phân biệt tường minh Liveness/Readiness/
  Capability, và dòng quan trọng nhất: "ADR-022 is explicit that Doctor at V1 MUST NOT claim registry
  admission... every capability check here is only ever 'observed', never 'registered'" — viết từ lúc
  AdapterBuildVersion registry CHƯA tồn tại. Hôm nay (sau V6-10J) registry đã tồn tại thật, nhưng tinh thần
  "không tự nhận admission" vẫn đúng — cách tôn trọng nó tốt nhất là không bao giờ tự derive lại một phát biểu
  thứ hai về registry state có thể trôi khỏi phát biểu gốc ở `GET /adapter-builds`, đúng khớp với đọc "operation
  links" ở trên. `Options{Config, Store, WorkerConfig, CheckWorker, UnitOfWork}` — `UnitOfWork` optional (nil
  bỏ qua `CheckSafeSettings`, pattern y hệt `CheckWorker` optional bỏ qua `CheckWorkerConfig`).
- `internal/app/doctor/checks.go`: đọc từng hàm `Check*` — `CheckLiveness` (luôn HEALTHY), `CheckAppConfig`
  (chạy `config.Validate(cfg)` NGUYÊN VẸN, không có cách nào tách riêng field liên quan tới worker pool),
  `CheckDatabase` (chỉ `store.Ping`, không mở connection mới), `CheckRoot` (stat + probe write file tạm rồi tự
  xoá), `CheckGit` (`git --version`), `CheckWorkerConfig` (gated bởi `CheckWorker`), `CheckProviderExecutable`
  (chỉ sha256-hash file, KHÔNG BAO GIỜ execute — không path nào bị echo trong `Detail`, chỉ digest/size),
  `CheckSafeSettings` (chỉ chứng minh document decode được, không echo field nào). Không có check nào cho
  "isolation" — dòng Phạm vi của task này tự liệt kê "DB/roots/Git/provider/adapter/isolation readiness" nhưng
  `doctor.Run` hôm nay chỉ cover 4/6 nhóm đó.
- `cmd/aw/serve.go` (đọc toàn bộ trước khi sửa) — xác nhận phát hiện brief gợi ý: **grep `config.Config{`/
  `config.Load(`/`config.Defaults(` ra 0 kết quả** trong toàn bộ `cmd/aw` trước task này. `serve()` chỉ có 2
  flag ad hoc `--db`/`--artifact-root`, không hề dựng `config.Config` để đưa vào bất kỳ đâu. Điều này có nghĩa
  nếu `CheckAppConfig` chạy với `config.Config{DatabasePath: *dbPath, ArtifactRoot: *artifactRoot}` (struct
  literal rỗng ở mọi field khác), `config.Validate` sẽ LUÔN BLOCKED vĩnh viễn vì `WorkerID` rỗng (`config.
  Validate`'s dòng đầu tiên: "database_path.../artifact_root.../worker_id: WHAT is empty") — `aw serve` chưa
  bao giờ chạy worker pool nên chưa từng cần field đó, nhưng `config.Validate` không biết phân biệt "process
  này có chạy worker hay không" (không giống `CheckWorker` tự gate `CheckWorkerConfig`). Đây là gap thật brief
  đã dự đoán đúng.
- `internal/app/config/config.go`/`validate.go`: `Defaults()` cho sẵn `WorkerConcurrency=4`, `LeaseTTL=30s`,
  `LeaseHeartbeat=10s`, `ProcessOutputLimit=1MiB` — mọi field validate được NGAY, chỉ `WorkerID` cố ý để rỗng
  ("there is no safe default identity for a worker process"). `Validate` không có concept "chỉ validate field
  liên quan tới X" — chạy nguyên khối, không gate theo caller.
- `internal/app/ports/isolation.go` + `internal/adapters/process/isolation.go`: `IsolationEnforcementChecker.
  VerifyEnforceable(ctx, tier)` — doc comment tự khẳng định "performs no I/O in this codebase's only
  implementation today" — pure, an toàn gọi trên mọi request không giống việc spawn process thật. Implementation
  production duy nhất (`process.IsolationChecker`) luôn chấp nhận `OPERATOR_TRUSTED_LOCAL`, luôn từ chối
  `ENFORCED_ISOLATED` (Alpha chưa có sandbox OS thật) — đây chính là "isolation readiness" Phạm vi nhắc tới,
  và `cmd/aw/serve.go` ĐÃ tự dựng sẵn `isolationChecker := process.NewIsolationChecker()` cho
  `recoveryhttp.RegisterRoutes` — tái dùng nguyên instance đó, không tạo cái thứ hai.
- `internal/delivery/httpapi/adapterbuild/{adapterbuild,queries}.go` (V6-10J): `GET /adapter-builds` đã là một
  plain query thật, không side-effect nào ngoài đọc DB — xác nhận link `"adapterBuilds": "/adapter-builds"` trỏ
  tới một route THẬT đã tồn tại, không phải placeholder.
- `internal/app/safesettings/commands.go`: `SafeSettingsResult.RestartRequired = !record.Desired.IsZero()` —
  một bool ĐÃ tính sẵn, test sẵn (V6-10G/V6-10H), forward được nguyên vẹn mà không cần Doctor tự derive lại
  semantic "cần restart" theo cách riêng — đúng tinh thần "restart states... mirroring V6-10H's own
  restartRequired concept" của Verify line.
- `internal/delivery/httpapi/safesettings/{routes,dependencies,queries,dto}.go` (V6-10H, đã merge PR #48) và
  `internal/delivery/httpapi/adapterbuild/{adapterbuild,queries,dto,errors}.go` (V6-10J, PR #49) đọc làm mẫu
  cấu trúc gần nhất: package riêng dưới `internal/delivery/httpapi/doctor`, `Dependencies` struct tường minh,
  `dto.go` tự khai lại wire shape (không tái dùng trực tiếp type export của tầng app), `RegisterRoutes` chỉ
  thêm đúng 1 descriptor.

### Quyết định

1. **Không sửa `internal/app/doctor/{doctor,checks}.go`** — mọi phần mở rộng (isolation, restartRequired,
   links) nằm ở tầng HTTP (`internal/delivery/httpapi/doctor`), không đụng vào package domain đã đóng và đã có
   golden test riêng. Lý do: 2 phần mở rộng đều KHÔNG phải "một Check nữa của cùng loại DB/root/git/provider" —
   isolation dùng một dependency khác hẳn (`ports.IsolationEnforcementChecker`, không phải `ports.QueryStore`/
   `ports.UnitOfWork`), và restartRequired tái dùng một field ĐÃ có sẵn từ package khác
   (`safesettings.SafeSettingsResult`) chứ không phải một check mới cần viết. Gộp cả 2 vào `doctor.Run` sẽ đòi
   sửa `Options` lần nữa (rủi ro conflict với chính các PR đang chạy song song đụng file khác trong batch này)
   mà không mang lại lợi ích rõ ràng nào so với compose ở tầng HTTP — đúng "Thực hiện: installation-scoped
   QUERY combines..." đọc theo nghĩa đen: COMBINE là việc của handler, không phải của `Run`.
2. **"Adapter" trong Phạm vi = một operation LINK (`"adapterBuilds": "/adapter-builds"`), không phải dữ liệu
   registry nhúng lại** — đọc thẳng theo chữ "operation links" của Thực hiện line (xem Nghiên cứu). Không gọi
   `appadapterbuild.ListAdapterBuilds` ở đây, không tính drift, không mở thêm dependency mới vào `Dependencies`
   cho việc này. Đây là quyết định tránh over-build được brief giao việc tự cảnh báo trước ("don't over-build")
   — một field đếm số build đăng ký sẽ là dữ liệu thứ hai có thể trôi khỏi `GET /adapter-builds`'s own con số
   thật theo thời gian, trong khi một link tĩnh không bao giờ trôi vì nó không MANG dữ liệu nào cả.
3. **Isolation LÀ một CheckResult thật** (không chỉ một link) — khác quyết định #2, vì `ports.
   IsolationEnforcementChecker.VerifyEnforceable` document rõ "no I/O", nên gọi nó không có rủi ro trôi dữ liệu
   hay chi phí I/O nào — không giống registry data (vốn có thể đổi theo thời gian giữa lúc Doctor render và
   lúc client đọc), fact "tier nào enforceable" là một fact TĨNH của binary/OS hiện tại, phù hợp để trả trực
   tiếp. `OPERATOR_TRUSTED_LOCAL` lỗi → BLOCKED (môi trường hỏng thật); `ENFORCED_ISOLATED` lỗi một mình →
   HEALTHY (đây là giới hạn Alpha đã biết trước, ADR-023 tự nói "không bao giờ auto-downgrade" — cái BẢO VỆ
   thật là admission check fail-closed ở nơi khác, không phải Doctor báo lỗi vĩnh viễn không ai fix được).
4. **`RestartRequired` forward NGUYÊN VẸN một bool duy nhất từ `safesettings.GetSafeSettings`, không forward
   bất kỳ field nào khác của `SafeSettingsResult`** — đặc biệt không bao giờ chạm `Desired` (chứa
   `ProviderCredentialRef`). Lỗi đọc (ví dụ DB unreachable) mặc định về `false` thay vì làm fail cả response —
   mirror đúng "ignore lỗi, giá trị zero đã đúng" mà `cmd/aw/serve.go` đã làm cho `safeSettingsAtBoot` (V6-10H
   Quyết định #7) — check "database"/"safe_settings" trong CÙNG response đã tự nêu đúng lỗi thật, bool này
   không cần thêm một lỗi thứ hai chồng lên.
5. **`aw serve` cần một flag `--worker-id` mới (default `"aw-serve"`), và `cmd/aw/serve.go` giờ tự dựng
   `config.Defaults()` + override `DatabasePath`/`ArtifactRoot`/`WorkerID`/`ProviderExecutables`** — giải quyết
   gap ở Nghiên cứu (nếu không, `CheckAppConfig` sẽ BLOCKED vĩnh viễn trên mọi installation vì `WorkerID` rỗng,
   làm response HEALTHY thật không bao giờ đạt được được bằng composition root thật, trái "Hoàn thành khi:
   authoritative source for is this installation healthy"). Chọn thêm 1 flag với default an toàn thay vì hard-
   code một giá trị cố định trong code: operator vẫn override được nếu muốn, còn mặc định không cần cấu hình gì
   thêm vẫn chạy HEALTHY — đây LÀ identity thật của tiến trình `aw serve` này (không phải giả mạo một giá trị
   để "lừa" `Validate`), chỉ đơn giản chưa từng có field nào biểu diễn nó trước task này.
6. **Status endpoint luôn trả HTTP 200**, severity nằm hoàn toàn trong body (`status`/`checks[].status`) — khác
   `/health/ready` (503 khi không ready, một gate nhị phân cho load balancer). Doctor là một diagnostic report
   để UI RENDER, không phải một gate máy móc — client luôn parse được JSON dù installation đang BLOCKED, đúng
   "first-run UI never needs to read the filesystem/config directly" (không cần fallback logic khác nhau theo
   HTTP status).
7. `aggregateStatus` (worst-of-three BLOCKED > DEGRADED > HEALTHY) được viết LẠI ở tầng HTTP thay vì export từ
   `internal/app/doctor` — vì response combine `report.Checks` (từ `Run`) VỚI `isolationCheck`'s own entry
   (tầng HTTP tự thêm), nên phải aggregate lại trên danh sách ĐÃ GỘP; rule thì giống hệt `doctor.Run`'s own
   unexported `aggregate`, không phải một policy khác.
8. Không migration mới — xác nhận `git log origin/master --oneline -3` (HEAD thật `c8163d3`) và
   `internal/adapters/sqlite/migrations/` ngay trước khi finalize: migration cao nhất vẫn `0037`, task này
   không cần bảng mới.

### Thực hiện

File mới (`internal/delivery/httpapi/doctor`, package mới):

- `doctor.go`: doc comment đầy đủ giải thích cả 2 quyết định khó (isolation là check thật, adapter là link) +
  lý do "không credential nào có thể lọt qua, có cấu trúc chứ không phải quy ước"; `Dependencies{Config,
  Store, UnitOfWork, Isolation}`; `RegisterRoutes` — đúng 1 descriptor `GET /doctor`, `ScopeKind: httpapi.
  ScopeInstallation`.
- `dto.go`: `checkResultWire` (tự khai lại field của `appdoctor.CheckResult`, đúng convention "mỗi route tự
  khai wire DTO riêng" mọi package khác đã theo), `responseDTO{Status, Checks, RestartRequired, Links}`,
  `operationLinks()` — map tĩnh 5 entry (`adapterBuilds`, `safeSettings`, `projects`, `healthLive`,
  `healthReady`), cố ý KHÔNG có link project-scoped nào (Doctor không biết project id nào để điền vào).
- `queries.go`: `handleDoctor` (gọi `appdoctor.Run` với `Config`/`Store`/`UnitOfWork`, gộp thêm
  `isolationCheck`'s own entry, tính `aggregateStatus`/`restartRequired`, encode `responseDTO`, luôn 200),
  `aggregateStatus` (worst-of-three, xem Quyết định #7), `isolationCheck` (gọi `VerifyEnforceable` 2 lần —
  `OPERATOR_TRUSTED_LOCAL` rồi `ENFORCED_ISOLATED` — map thành đúng 3 outcome ở Quyết định #3),
  `restartRequired` (forward bool từ `safesettings.GetSafeSettings`, default `false` khi lỗi hoặc `uow` nil).
- `doctor_test.go`: 6 test HTTP thật, real `*sqlite.Store` + real `process.IsolationChecker` (xem Test).

File sửa:

- `cmd/aw/serve.go`: thêm flag `--worker-id` (default `"aw-serve"`, doc comment giải thích rõ đây không phải
  lease-fence owner id của một worker pool thật); dựng `appConfig := config.Defaults()` rồi override
  `DatabasePath`/`ArtifactRoot`/`WorkerID`/`ProviderExecutables` bằng đúng giá trị process đã có trong scope
  (cùng `*dbPath`/`*artifactRoot`/`*claudeExecutable`/`*codexExecutable` các route khác đã dùng, không phải
  bản sao thứ hai); import thêm `internal/adapters/sqlite` (để gọi `sqlite.NewQueryStore(store)`, đúng cách
  `internal/app/doctor`'s own golden test bọc `*sqlite.Store` thành `ports.QueryStore`) và `httpdoctor
  "internal/delivery/httpapi/doctor"`; thêm `httpdoctor.RegisterRoutes(routes, httpdoctor.Dependencies{Config:
  appConfig, Store: sqlite.NewQueryStore(store), UnitOfWork: uow, Isolation: isolationChecker})` — tái dùng
  ĐÚNG `uow`/`isolationChecker` mọi route khác trong file đã dùng, không tạo instance thứ hai của bất kỳ cái
  nào.

### Test

- `go build ./...`, `go vet ./...`: sạch toàn bộ module.
- `go test ./internal/delivery/httpapi/doctor/... -v`: 6 test, tất cả PASS —
  `TestRegisterRoutes_ExposesExactlyDoctor` (đúng 1 route, `GET /doctor`, `ScopeInstallation`),
  `TestDoctor_Healthy_HTTP` (config/db/artifact-root/provider-executable thật đều hợp lệ → `status="HEALTHY"`,
  đủ 7 check gồm `isolation_enforcement` HEALTHY/CAPABILITY, `links` đúng 5 entry, `restartRequired=false`),
  `TestDoctor_Degraded_MissingArtifactRoot_HTTP` (artifact root chưa tồn tại → `status="DEGRADED"`, check
  `artifact_root` có `Remediation`), `TestDoctor_Blocked_DatabaseUnreachable_HTTP` (đóng `*sqlite.Store` thật
  giữa lúc server đang chạy → `status="BLOCKED"`, check `database` BLOCKED có `Remediation`, HTTP status vẫn
  200 đúng Quyết định #6, `restartRequired` tự fallback `false` an toàn dù `safe_settings` cũng lỗi theo),
  `TestDoctor_RestartRequired_AfterSafeSettingsUpdate_HTTP` (GET trước `restartRequired=false` → PUT
  `/settings/safe` thật → GET sau `restartRequired=true`, hai route đăng ký chung 1 registry/server/uow),
  `TestDoctor_NeverLeaksDatabasePathOrCredential_HTTP` (PUT một `providerCredentialRef` thật riêng biệt trước,
  rồi scan RAW response bytes — không chỉ field đã decode — chứng minh credential không lộ VÀ đường dẫn DB
  không lộ, trong khi đường dẫn artifact-root [được chính `CheckRoot` document là "explicitly safe to show"]
  THẬT SỰ xuất hiện, chứng minh đây là redaction có chủ đích/chọn lọc chứ không phải Doctor vô tình ẩn hết mọi
  path). Bắt được 1 lỗi test tự viết ngay trong lúc chạy: so sánh raw bytes với path Windows (chứa `\`) phải
  so với dạng ĐÃ JSON-escape (`\\`), không phải string gốc — sửa bằng helper `jsonEscaped` (`json.Marshal` rồi
  trim quote) cho 2 assertion phủ định, và so trên field ĐÃ DECODE cho assertion khẳng định.
- `go test ./cmd/aw/... -v`: toàn bộ suite pass (62s) — xác nhận flag `--worker-id` mới và `config.Config`
  dựng mới trong `serve()` không phá bất kỳ test `serve`/`cli` nào đã có.
- `go test ./internal/app/doctor/... -v`: pass (19.8s) — xác nhận không đụng gì vào package domain (đúng
  Quyết định #1), 3 golden HEALTHY/DEGRADED/BLOCKED + `CheckSafeSettings` sqlite test vẫn y nguyên.
- `go test ./internal/archtest/...`: pass — package mới không vi phạm rule domain/app-never-imports-adapters
  (chỉ file `_test.go` mới import `internal/adapters/process`/`sqlite`, đúng ngoại lệ archtest đã document).
- `go test ./... -count=1` (chạy nền, toàn bộ ~93 package): **0 FAIL**, không package nào lỗi — build sạch
  hoàn toàn, không cần viện dẫn flake nào lần này.

### Verify

- "healthy/degraded/blocked goldens": `TestDoctor_Healthy_HTTP`/`_Degraded_MissingArtifactRoot_HTTP`/
  `_Blocked_DatabaseUnreachable_HTTP` — driving thật (provider executable file thật, thư mục chưa tạo thật, DB
  đóng thật), không mock trạng thái.
- "restart states": `TestDoctor_RestartRequired_AfterSafeSettingsUpdate_HTTP` — PUT thật qua route V6-10H thật,
  đọc lại qua GET /doctor thật.
- "secret/path redaction": `TestDoctor_NeverLeaksDatabasePathOrCredential_HTTP` — scan raw bytes thật (không
  chỉ field decode) cho credential VÀ database path, đồng thời chứng minh artifact-root path (được document là
  an toàn) vẫn xuất hiện — phân biệt rõ "redact có chọn lọc" với "vô tình ẩn hết".
- "Không làm — no duplicate repository onboarding/history/retry routes": `operationLinks()` chỉ có 5 entry
  installation-scoped, không entry nào trỏ tới `/projects/{id}/repositories`/`/repositories/{id}/onboarding`/
  `/repositories/{id}/retry-probe` (những route đó đòi project id Doctor không có).
- "no credentials, ever": `TestDoctor_NeverLeaksDatabasePathOrCredential_HTTP` cộng với việc `doctor.go`'s own
  doc comment chỉ ra CẤU TRÚC (không phải quy ước) khiến điều này đúng — `Options`/`Run` không chạm field
  credential nào, `restartRequired` chỉ forward đúng 1 bool.
- Wiring thật vào `cmd/aw/serve.go`: `grep -n "httpdoctor" cmd/aw/serve.go` xác nhận cả import (dòng 34) VÀ lời
  gọi `httpdoctor.RegisterRoutes(...)` (dòng 406) đều thật sự tồn tại trong composition root, không chỉ ở
  test package-level — đúng lo ngại doctrine nêu từ vụ V6-04 ban đầu (30 test xanh nhưng route chưa từng được
  gọi từ `cmd/aw/serve.go` thật).
- "Hoàn thành khi — first-run UI never needs to read the filesystem/config directly": response luôn có đủ
  `status`/`checks[]` (DB/roots/Git/provider/safe-settings/isolation) VÀ `links` (adapter builds/safe
  settings/projects/health) trong cùng 1 lần gọi, kể cả khi installation đang BLOCKED (HTTP vẫn 200, xem
  Quyết định #6).

### Kết quả

Package mới `internal/delivery/httpapi/doctor` (3 file production + 1 file test, 6 test function, real HTTP +
real sqlite + real `process.IsolationChecker`). 1 route HTTP mới: `GET /doctor`, installation-scoped, luôn 200.
Gap lớn nhất task này tự phát hiện — `aw serve` chưa từng dựng `config.Config` — giải quyết bằng 1 flag mới
(`--worker-id`, default an toàn) cộng `config.Defaults()` làm nền, để `CheckAppConfig` đạt HEALTHY thật qua
composition root thật thay vì BLOCKED vĩnh viễn. Câu hỏi "adapter" trong Phạm vi được đọc theo đúng nghĩa đen
"operation links" của design doc — chỉ 1 map tĩnh trỏ tới `GET /adapter-builds` đã tồn tại, không nhúng lại dữ
liệu registry; "isolation" được xử lý ngược lại — 1 CheckResult thật (không phải link) vì dependency đó tự
document "no I/O", nên an toàn gọi trực tiếp mỗi request. Không sửa `internal/app/doctor` (domain package đã
đóng, đã có golden test riêng) — mọi phần mở rộng nằm ở tầng HTTP. `go build/vet/test ./...` sạch trên toàn bộ
module (~93 package), `go test ./... -count=1` chạy nền 0 FAIL — không có regression nào, không có flake nào
cần viện dẫn lần này.

## V6-07A — Attachment ingest, replay và orphan recovery

### Bối cảnh

V6-07A phụ thuộc V6-07 (đã merge, PR #47 — `internal/app/message.AppendMessage`/`internal/delivery/httpapi/message`
đã tồn tại thật), V5-14 (đã merge từ trước — `internal/app/artifactsweep` + `ArtifactRepository`'s own
Claim/Release-Locator-Purge) và V1-06 (command/receipt envelope). Trích nguyên văn spec
(`docs/design/08-v6-api-projections.md` dòng 308-323, không diễn giải lại):

> **Mục tiêu:** upload có exact replay và durable owner ở mọi crash point.
> **Phạm vi:** `AppendConversationAttachment`, bounded spool/hash, content-addressed prepare claim và cleanup.
> **Không làm:** không DB transaction trong stream/hash/store; không raw locator hoặc best-effort-only cleanup.
> **Thực hiện:** require/verify declared digest; canonical hash gồm target, metadata, retention/sensitivity và
> digest persisted bytes. Receipt precheck trước ArtifactStore put. Deterministic UploadID owns durable prepare
> claim; same upload+digest reuses prepared blob, mismatch conflicts. Sau Put+Verify, serialized transaction
> rechecks receipt then atomically creates Artifact metadata + Message ref + event + receipt; acknowledge claim
> after commit. Sweeper resumes/cleans expired claim only after rechecking receipt, Artifact refs/holds and
> shared content hash.
> **Verify:** crash after spool/blob/claim/DB commit/before ack; same/different-key concurrency; shared blob;
> tamper/oversize/media; restart and orphan cleanup. Assert each outcome committed, resumable or cleanup-able.
> **Hoàn thành khi:** không crash point nào tạo duplicate message/artifact metadata hoặc ownerless permanent blob.
> **Nguồn:** ADR-017, AK-ARCH-021, HE-11-M07.

Đây là task khó nhất trong batch hiện tại theo đánh giá của phiên giám sát — một bài toán crash-safety thật, không
phải HTTP wrapper. `baocaov6checklist.md` đang được 3 task song song khác ghi đồng thời (V6-04A, V6-06B, V6-06C) —
xung đột merge khi mở PR là bình thường, không phải bug (đúng như đã xảy ra: PR #51 V6-10A và một commit
`feat(v6-06b)` khác đã merge/chạy song song ngay trong lúc task này đang viết code, buộc phải `git fetch` +
`git rebase origin/master` một lần trước khi mở PR — xem mục Kết quả).

### Nghiên cứu

Đọc toàn bộ `internal/app/message/commands.go` (đặc biệt doc comment đầu file) — tự nói rõ AppendMessage KHÔNG
giải quyết bài toán này: content của nó luôn đến dưới dạng MỘT `[]byte` đã buffer sẵn trong bộ nhớ, nên compose
`internal/app/artifact.PrepareAttachment` (Put+Verify NGOÀI transaction) với đúng MỘT transaction theo sau là đủ.
Task này khác: bytes phải stream (không buffer hết một lần cho file lớn), digest caller khai phải verify được, và
toàn bộ phải sống sót crash ở BẤT KỲ điểm nào.

Đọc `internal/app/artifact/attach.go` toàn bộ — `PrepareAttachment` gọi `store.Put` rồi `store.Verify` lại (cả
hai NGOÀI transaction), trả `artifact.Artifact` sẵn sàng `AttachState: Attached` — đây chính là building block tái
dùng nguyên vẹn cho bước "bounded spool/hash", không cần viết lại logic Put/Verify.

Đọc `internal/adapters/artifactstore/filesystem.go` toàn bộ — `Store.Put` đã TỰ atomic (spool vào temp file, hash
song song bằng `io.MultiWriter`, chỉ `os.Rename` sang content-addressed path SAU KHI toàn bộ body đọc xong) và TỰ
dedupe theo content hash (`if _, statErr := os.Stat(finalPath); statErr == nil { ... return ref, nil }`). Hệ quả
quan trọng: "crash giữa spool và blob-put" không thể quan sát được từ bên ngoài `Put` (atomicity đã có sẵn từ
V1-08) — một retry gọi lại `Put` với cùng bytes luôn an toàn, không bao giờ ghi trùng hay ghi dở.

Đọc `internal/app/ports/artifactrecord.go` toàn bộ, đặc biệt doc comment của `ClaimArtifactLocatorForPurge`/
`ReleaseArtifactLocatorClaim` (V5-14) — chính là "cùng dạng bài toán, hướng ngược lại" mà prompt gợi ý: một durable
claim fence một Locator content-addressed cụ thể khỏi thao tác đồng thời/crash. `InsertArtifact` tự chối một
insert mới nếu Locator đang có purge-claim mở — xác nhận cơ chế claim này đã có tiền lệ kiến trúc thật trong
chính codebase, không phải phát minh mới hoàn toàn.

Đọc toàn bộ `internal/app/artifactsweep/sweep.go` (443 dòng) làm precedent cho "sweeper thật trông như thế nào":
job CONTROL tự lên lịch lại (`ArtifactSweepJobKind`, `artifact_sweep_state` singleton generation cursor), protocol
3 pha reserve→delete(ngoài tx)→finalize, và đặc biệt: `ClaimArtifactLocatorForPurge` trả `ErrPersistenceAlreadyExists`
được coi là "resume claim của chính mình" chứ không phải conflict — vì đây là job singleton (chỉ 1 worker giữ
lease `ARTIFACT_SWEEP` tại một thời điểm).

**Phát hiện quan trọng nhất khi grep composition root**: `grep -n "Startup" cmd/ internal/` xác nhận
`StartupArtifactSweep` (V5-14) VÀ `StartupRecoveryScan` (V4-13) — cả hai job CONTROL tự lên lịch lại đã tồn tại
thật trong `internal/app/artifactsweep`/`internal/app/runtime` — **CHƯA TỪNG được gọi từ `cmd/aw/serve.go` hay bất
kỳ composition root nào khác trong toàn repo**. Cơ chế `workerpool`/durable-job dispatch cho CONTROL job hoàn toàn
chưa được wiring vào `aw serve` (bản thân `aw serve` hiện chỉ là HTTP server thuần, không có worker loop nào chạy
song song). Phát hiện này quyết định trực tiếp Quyết định #6 bên dưới.

Đọc `internal/delivery/httpapi/commandenvelope.go`'s `SemanticHash` — doc comment của chính hàm này đã tự nêu
đích danh: `extraContentDigest` param tồn tại "for a command carrying raw/binary content no JSON canonicalization
applies to, e.g. **a future attachment upload** — pass "" when there is none". Đây là bằng chứng trực tiếp cho
thấy composite hash của task này ĐÃ được thiết kế sẵn một chỗ để cắm vào từ V6-02 — không cần phát minh scheme
hash mới, chỉ cần gọi đúng hàm có sẵn với tham số đúng.

Đọc `internal/app/runtime/completion_policy.go` dòng 931-960 — quy ước deterministic-ID đã đóng của codebase:
`sha256` trên các phần nối bằng `\x00`, hex-encode, cắt 16 byte đầu, prefix người đọc được
(`deterministicCompletionDecisionID`/`deterministicJoinNodeRunID`) — tái dùng nguyên xi cho
`DeterministicAttachmentUploadID`.

Đọc `internal/app/ports/unitofwork.go`, `internal/adapters/sqlite/unitofwork.go`,
`internal/app/ports/fake/unitofwork.go` — xác nhận `ports.Tx` là "concern-scoped accessor" pattern (một interface
riêng mỗi concern, ví dụ `ArtifactRepository`) — quyết định thêm accessor mới `AttachmentClaims()` thay vì nhét
method vào `ArtifactRepository` sẵn có, giữ ranh giới kiến trúc rõ ràng đúng tinh thần "mỗi concern một accessor".

### Quyết định

1. **Composite canonical hash = `cmd.RequestHash`, tính SẴN ở tầng HTTP qua `httpapi.SemanticHash` có sẵn —
   không tính lại ở tầng app.** `prepareAttachmentCommand` (tầng HTTP) marshal một struct nhỏ
   `attachmentMetadata{WorkItemID, AttemptID, Role, ContentType, Sensitivity}` (đúng "target"+"metadata"+
   "retention/sensitivity" — RetentionClass CỐ ĐỊNH `RetentionCanonicalContext`, không phải field biến thiên,
   nên không cần đưa vào hash) làm `normalizedPayload`, rồi gọi
   `httpapi.SemanticHash("AppendConversationAttachment", scope, canonical, declaredSHA256, 0)` — `declaredSHA256`
   chính là "digest persisted bytes" mà spec liệt kê là thành phần thứ 4. Đây là composite hash công khai (dùng
   để phát hiện "Idempotency-Key dùng lại với request khác nhau" — `ports.ErrReceiptConflict`), tách biệt hoàn
   toàn khỏi UploadID (Quyết định #2).
2. **Deterministic `UploadID` = `sha256("attachment-upload", Actor, Scope.Key(), IdempotencyKey, CommandType)`
   — CỐ Ý KHÔNG bao gồm digest hay metadata.** Đây là quyết định thiết kế quan trọng nhất của cả task, và ban
   đầu đọc spec ("same target+content+metadata arrives at the same UploadID") dễ hiểu lầm là UploadID phải bao
   gồm cả digest. Lý do loại digest ra: nếu UploadID = f(target, metadata, digest), câu spec "mismatch (same
   UploadID's claim exists, but this attempt's digest differs) is a real conflict" trở thành BẤT KHẢ THI về mặt
   cấu trúc — digest khác nhau tất yếu sinh UploadID khác nhau, không bao giờ va vào cùng một claim. Cách đọc
   nhất quán duy nhất: UploadID dùng CHÍNH 4-tuple identity mà một command receipt đã dùng để tra cứu
   (`ports.ReceiptsRepository.Load(Actor, Scope, IdempotencyKey, CommandType)`) — một RETRY thật (client gửi lại
   đúng Idempotency-Key) luôn tính lại đúng cùng UploadID và tìm lại đúng claim cũ của chính nó; hai caller ĐỘC
   LẬP (Idempotency-Key khác nhau) dù tình cờ trùng bytes/target/metadata vẫn luôn nhận 2 UploadID khác nhau —
   đúng khớp test "different-key concurrency ... including two that happen to share content bytes" (2 Message
   row độc lập, chỉ CHIA SẺ blob ở tầng ArtifactStore, một tầng thấp hơn hẳn command này). "Mismatch" trong spec
   khi đó có nghĩa: CÙNG Idempotency-Key nhưng attempt sau khai digest KHÁC — `claimOrResumeAttachmentUpload` so
   `claim.DeclaredSHA256` (đã ghi từ lần claim đầu) với digest của attempt hiện tại TRƯỚC bất kỳ I/O nào, trả
   `ErrAttachmentUploadConflict` nếu khác — đây là "early echo" của đúng conflict mà tầng receipt (muộn hơn
   nhiều, chỉ ghi sau khi Put+Verify xong) sẽ bắt được, cần thiết vì 2 request đồng thời với CÙNG Idempotency-Key
   nhưng body khác nhau (race, trước khi bất kỳ ai ghi receipt) chỉ có tầng claim mới bắt kịp lúc.
3. **Attachment LÀ một Message row mới, không phải một bảng liên kết attachment riêng.** Domain model hiện tại
   (`internal/domain/message.Message`) chỉ có đúng MỘT field `ContentArtifactID`, và bảng `messages` là
   append-only/immutable — không có khái niệm "nhiều attachment trên một message" ở schema hiện tại. Quyết định:
   `AppendConversationAttachment` tái dùng NGUYÊN VẸN `tx.Messages().AppendMessage` (giống hệt shape
   `AppendMessage` đã dùng), chỉ khác ContentArtifactID trỏ tới content NHỊ PHÂN thay vì text — không thêm bảng
   mới, không thêm field mới vào `messages`, đúng 00-roadmap.md §3 ("không chia field/bảng khi chưa có contract
   test cần").
4. **Migration 0038 `attachment_prepare_claims`** (kiểm tra `git log origin/master --oneline -3` VÀ
   `internal/adapters/sqlite/migrations/` NGAY TRƯỚC KHI hoàn thiện — xác nhận `0037_release_set_local_commits.sql`
   vẫn là migration cao nhất trên fresh `origin/master`, kể cả sau khi rebase qua PR #51 V6-10A). Schema mirror
   `artifact_locator_purge_claims` (V5-14) về mặt Ý NGHĨA (durable claim + claim_owner/claimed_at fence) nhưng
   khác về SHAPE: khóa chính là `upload_id` (không phải locator — vì tại thời điểm claim được tạo, locator CHƯA
   TỒN TẠI, đó chính là điều claim này đang chờ), có state 2 giá trị đóng `SPOOLING`/`BLOB_READY` (CHECK constraint
   ép `locator`/`actual_sha256`/`size` chỉ khác NULL đồng thời với state), và lưu thêm `actor`/`idempotency_key`
   — hai cột này KHÔNG dùng để ghi (sweeper không bao giờ tự mạo danh caller gốc để hoàn tất command), chỉ dùng để
   ĐỌC receipt (`tx.Receipts().Load`) khi `ResumeOrCleanExpiredAttachmentClaims` cần biết "command gốc đã thật sự
   hoàn tất chưa" mà không cần ngữ cảnh HTTP request gốc.
5. **Chuỗi 2 pha, I/O thật LUÔN NGOÀI transaction** — đúng thứ tự spec liệt kê, implement tại
   `internal/app/message/attachment.go`:
   1. Validate request (rẻ, không I/O).
   2. Receipt precheck NGOÀI transaction (`uow.WithReadOnly`) — y hệt `AppendMessage`'s `loadOrValidateReceipt`,
      TRƯỚC bất kỳ I/O thật nào. Nếu replay: giải phóng claim (nếu còn) như một bước RIÊNG (write thật, không gộp
      vào precheck read-only) rồi trả kết quả cũ — đây chính là cách "crash sau DB commit nhưng trước ack" được
      dọn dẹp ở lần chạm kế tiếp.
   3. `claimOrResumeAttachmentUpload` — MỘT `WithSerializedWrite` riêng, nhỏ: idempotent-insert-hoặc-tìm claim;
      lấy-quyền-sở-hữu claim `SPOOLING` đã hết hạn thuê (`attachmentClaimLease = 2 phút`, tách biệt hẳn
      `orphanGrace` 7 ngày của artifactsweep — đây là "chủ sở hữu 1 HTTP request đồng bộ còn sống không", câu hỏi
      nhanh hơn hẳn "còn cần giữ evidence bao lâu").
   4. Nếu claim đã `BLOB_READY`: `store.Verify` lại blob đã có (không bao giờ tin claim cũ một cách mù quáng),
      dựng `artifact.Artifact` mới từ ref cũ — bỏ qua bước Put, đi thẳng bước 6 (resume).
   5. Ngược lại: `appartifact.PrepareAttachment` (Put+Verify, NGOÀI transaction, `Body` bọc
      `io.LimitReader(Body, MaxAttachmentSize+1)` — chính là "bounded spool/hash"), so `prepared.ContentHash` với
      `"sha256:"+declaredSHA256` — khác nhau là `ErrAttachmentDigestMismatch` (tamper), KHÔNG đụng tới claim (để
      một retry với digest ĐÚNG vẫn dùng lại được đúng claim `SPOOLING` cũ). Khớp thì
      `recordAttachmentBlobReadyWithRetry` — CAS `RecordAttachmentBlobReady`, thua race (`ErrOptimisticConflict`)
      thì đọc lại claim và NHẬN kết quả của người thắng thay vì coi là lỗi cứng (2 racer đã verify cùng digest
      trước khi tới đây, nên kết quả người thắng luôn khớp).
   6. MỘT `WithSerializedWrite` cuối: re-check receipt lần nữa (đóng TOCTOU với caller khác vừa hoàn tất trong
      lúc mình làm I/O chậm ở bước 5) — nếu chưa, `InsertArtifact` (Attached thẳng, không qua Orphan-rồi-promote:
      `PrepareAttachment` đã trả sẵn `AttachState: Attached`, giống hệt cách `AppendMessage` dùng) + `AppendMessage`
      + domain event (tái dùng NGUYÊN `MessageAppendedEventType`/schema — một attachment vẫn LÀ một Message, không
      cần event type riêng) + `Receipts().Record` — cùng một transaction, atomic.
   7. CHỈ SAU KHI bước 6 commit: `releaseAttachmentClaimBestEffort` — một `WithSerializedWrite` RIÊNG, lỗi bị
      nuốt có chủ đích (receipt bước 6 đã là bằng chứng vĩnh viễn command đã xong; claim còn sót lại chỉ chờ lần
      chạm kế tiếp dọn, không bao giờ gây duplicate hay mất kết quả).
6. **Sweeper là một hàm gọi trực tiếp được (`ResumeOrCleanExpiredAttachmentClaims`), KHÔNG phải một durable
   CONTROL job tự lên lịch lại mới.** Đây là quyết định phạm vi có cân nhắc, ghi lại minh bạch: spec cho phép
   "at minimum a resumable recovery path" (brief gốc). Bằng chứng quyết định: `StartupArtifactSweep`/
   `StartupRecoveryScan` — 2 job CONTROL tự lên lịch lại DUY NHẤT đã tồn tại trong toàn repo — CHƯA TỪNG được gọi
   từ bất kỳ composition root nào (xác nhận bằng grep, mục Nghiên cứu). Thêm một `durable_jobs.job_class` mới
   (đòi một migration rebuild kiểu `0025`/`0034` — `PRAGMA foreign_keys=OFF`, copy-drop-rename bảng) cộng một
   handler/wiring mới CHỈ để nó cũng nằm không dùng giống 2 job kia là scope creep không tương xứng với lợi ích
   thật. `ResumeOrCleanExpiredAttachmentClaims` vẫn là hàm THẬT, có test THẬT, chỉ chưa có một `aw worker`/
   scheduler nào gọi nó định kỳ — đúng vị trí "populated now, real behavior, chưa có caller lịch trình" mà nhiều
   port khác trong codebase này (`AdapterBuildRepository` thời V2-07A, `SetArtifactHold` thời V5-01) đã từng ở.
   Việc dây nó vào một job CONTROL thật sự cần một quyết định riêng, rộng hơn (áp dụng cho CẢ artifactsweep lẫn
   recovery reaper hiện có), không phải quyết định của một mình task này.
7. **`ResumeOrCleanExpiredAttachmentClaims` tái dùng NGUYÊN VẸN protocol purge 3 pha của V5-14** thay vì viết
   lại logic xoá blob. Thứ tự re-check đúng spec: (1) receipt (`tx.Receipts().Load` bằng `actor`/`idempotency_key`
   lưu trên claim) — có thì claim này chính là case "crash sau commit trước ack", chỉ release, không đụng gì
   khác; (2) nếu `BLOB_READY` và KHÔNG có receipt: `tx.Artifacts().ListArtifactsByLocator` — còn row nào khác
   tham chiếu Locator (một upload ĐỘC LẬP khác, tình cờ trùng bytes, đã hoàn tất) thì chỉ release claim, KHÔNG xoá
   blob; (3) chỉ khi cả 2 đều trống mới thật sự `ClaimArtifactLocatorForPurge`→`store.Delete`→
   `ReleaseArtifactLocatorClaim` — đúng "reserve/delete/finalize" `artifactsweep.purgeLocatorGroup` đã thiết lập,
   kể cả cách đọc `ErrPersistenceAlreadyExists` là "resume claim của chính lần chạy này" (một claim `SPOOLING`
   không có receipt thì không cần xoá gì — chỉ release claim, vì bước Put (nếu có) chưa từng thành `BLOB_READY`).
8. **`MaxAttachmentSize = 25 MiB`**, ép bằng `io.LimitReader` ở tầng app VÀ `http.MaxBytesReader` ở tầng HTTP —
   nhưng cả hai đều nằm SAU `httpapi.MaxBytes(cfg.MaxBodyBytes)` (middleware toàn server, mặc định 1 MiB qua flag
   `--max-body-bytes` có sẵn từ V6-01) — một attachment > 1 MiB chỉ thật sự đi qua được nếu operator tự nâng flag
   đó, đúng quan hệ `message/envelope.go`'s `maxBodyBytes` (2×`MaxContentSize`) đã lập tiền lệ cho chính package
   này — không sửa default flag, đây là lựa chọn vận hành có chủ đích, ghi rõ trong doc comment.
9. **Wire contract: raw body + metadata qua header** (`X-Attachment-Sha256`/`X-Attachment-Role`/
   `X-Attachment-Sensitivity`/`X-Attachment-Attempt-Id`, `Content-Type` chuẩn HTTP chính là media type thật của
   attachment) — route ĐẦU TIÊN trong toàn API mang raw bytes, đúng như chính doc comment `message/routes.go` (V6-07)
   đã tự dự đoán ("a future V6-07A attachment route is the only place raw arbitrary bytes ever travel over this
   API"). Không dùng multipart/form-data (sẽ tái tạo lại đúng vấn đề "buffer hết trước khi stream" mà
   `io.LimitReader` đang tránh).

### Thực hiện

- `internal/adapters/sqlite/migrations/0038_attachment_prepare_claims.sql` — bảng mới (Quyết định #4).
- `internal/app/ports/attachmentclaim.go` — `AttachmentPrepareClaim`, `AttachmentClaimRepository`
  (`ClaimAttachmentUpload`/`GetAttachmentClaim`/`RecordAttachmentBlobReady`/`TakeOverAttachmentClaim`/
  `ReleaseAttachmentClaim`/`ListStaleAttachmentClaims`); `ports.Tx` có thêm accessor `AttachmentClaims()`
  (`internal/app/ports/unitofwork.go`).
- `internal/adapters/sqlite/attachment_claim_repository.go` — implement thật, mirror
  `artifact_repository.go`'s style (`xxxTx(ctx, tx *sql.Tx, ...)` + thin wrapper), CAS bằng
  `UPDATE ... WHERE ... AND version = ?` + `RowsAffected`.
- `internal/app/ports/fake/unitofwork.go` — `AttachmentClaimRepository` in-memory, mirror sqlite field-for-field
  (dùng cho test tầng app không cần sqlite thật).
- `internal/app/message/attachment.go` — `AppendConversationAttachment`, `DeterministicAttachmentUploadID`,
  `AttachmentCommandType`, `MaxAttachmentSize`, `ResumeOrCleanExpiredAttachmentClaims` + toàn bộ helper 2 pha
  (Quyết định #5/#6/#7).
- `internal/delivery/httpapi/message/attachment.go` — `handleAppendConversationAttachment`,
  `prepareAttachmentCommand` (raw-body counterpart của `prepareCreateCommand`), `writeAttachmentCommandError`.
- `internal/delivery/httpapi/message/routes.go` — thêm `RouteDescriptor` thứ 4
  (`POST /projects/{projectId}/work-items/{workItemId}/attachments`, operationId `appendConversationAttachment`).
- Không sửa `cmd/aw/serve.go` về mặt wiring — route mới tự động reachable qua đúng lời gọi
  `httpmessage.RegisterRoutes(routes, httpmessage.Dependencies{...})` đã có sẵn từ V6-07 (grep xác nhận, xem mục
  Kết quả) — chỉ thêm 1 đoạn doc comment giải thích thêm (không đổi hành vi).

### Test

**Tầng app** (`internal/app/message`, fake UnitOfWork + real filesystem ArtifactStore, trừ 2 test concurrency):

- `attachment_test.go` — 13 test: happy path (đọc lại bytes thật qua `store.Open`, xác nhận claim đã release),
  replay đúng (cùng command → cùng result, không duplicate), tamper (`ErrAttachmentDigestMismatch`, claim vẫn
  `SPOOLING` sau đó, retry với digest sai lần 2 vẫn lỗi y hệt — không "tự sửa"), oversize (25 MiB+1KB thật,
  `ErrAttachmentTooLarge`), ContentType rỗng (required-media-type check), digest rỗng/sai định dạng
  (`ErrAttachmentDigestRequired`/`ErrAttachmentDigestMalformed`), **crash after claim-record** (Put lỗi giả lập
  qua `flakyStore`, claim còn `SPOOLING`, retry với store thật thành công đúng 1 Message), **crash after blob-put**
  (`countingUOW` chặn write #2 — claim vẫn `SPOOLING` dù blob đã Put thật, retry re-Put an toàn nhờ dedupe, đúng 1
  Message), **crash after DB commit trước ack** (`countingUOW` chặn write #4 — command vẫn trả thành công vì lỗi
  release bị nuốt có chủ đích, claim còn sống, retry sau đó replay đúng kết quả CŨ và release claim), mismatch
  cùng Idempotency-Key khác digest (`ErrAttachmentUploadConflict`), claim `SPOOLING` sống sót qua hết lease vẫn
  resume được.
- `attachment_test.go` (tiếp) — 3 test `ResumeOrCleanExpiredAttachmentClaims`: claim `BLOB_READY` bị bỏ rơi hoàn
  toàn (không receipt, không Artifact row nào khác) → purge blob thật (`store.Verify` fail sau đó) + release;
  claim đã commit thật nhưng chưa ack (giả lập crash write #4 rồi KHÔNG retry, gọi sweep thẳng) → chỉ release,
  KHÔNG đụng blob (`store.Verify` vẫn pass); 2 upload chia sẻ bytes, một hoàn tất một bị bỏ rơi → sweep chỉ
  release claim bị bỏ rơi, blob vẫn nguyên vì upload kia còn cần.
- `attachment_sqlite_test.go` — 2 test concurrency THẬT (real sqlite, không fake): **same-key concurrency** (2
  goroutine, CÙNG command, đua `sync.WaitGroup` — cả hai trả cùng kết quả, đúng 1 Message row) và
  **different-key concurrency + shared blob** (2 goroutine, 2 Idempotency-Key khác nhau, CÙNG bytes — 2 Message
  row độc lập, 2 Artifact row độc lập, nhưng CÙNG Locator — chứng minh dedupe ArtifactStore hoạt động đúng qua 2
  claim độc lập).

  **Phát hiện thật khi viết 2 test concurrency này**: viết lần đầu bằng `fake.UnitOfWork` (goroutine thật), cả
  hai fail với lỗi khó hiểu ("persistent record was not found"). Điều tra `internal/app/ports/fake/unitofwork.go`'s
  `run()` xác nhận: field `inTx` chỉ là một latch toàn cục ("có giao dịch nào đang mở không"), không phải hàng
  đợi — hai `WithSerializedWrite`/`WithReadOnly` GỐI NHAU từ 2 goroutine khác nhau (không lồng nhau về logic) vẫn
  bị coi là "nested" và bị `ErrNestedTransaction` chặn, khiến state 2 bên rơi vào tình huống không nhất quán khi
  code không xử lý lỗi đó đặc biệt. Đối chiếu `internal/adapters/sqlite/scheduling_test.go`'s
  `TestWriteLeaseRaceHasOneWinnerForSameRepository` xác nhận: MỌI test concurrency thật trong repo này đều chạy
  trên sqlite thật (`BEGIN IMMEDIATE` xếp hàng writer thật, không chối bỏ) — không bao giờ trên fake. Sửa bằng
  cách chuyển 2 test này sang `attachment_sqlite_test.go`, dựng fixture qua `sqlite.Open`+`sqlite.NewUnitOfWork`
  (mirror `internal/app/artifactsweep/sweep_sqlite_test.go`'s "real stack end to end" pattern) — không sửa bất kỳ
  code sản xuất nào, đây thuần là giới hạn của chính test double.

**Tầng sqlite** (`internal/adapters/sqlite/attachment_claim_repository_test.go`) — 5 test trực tiếp lên
`attachmentClaimRepository` qua sqlite thật: idempotent insert-hoặc-trả-về-cũ (không bao giờ ghi đè
`DeclaredSHA256` của claim gốc), CAS `RecordAttachmentBlobReady` thành công + `ErrOptimisticConflict`, CAS
`TakeOverAttachmentClaim` thành công + conflict, release idempotent (release 2 lần không lỗi), `ListStaleAttachmentClaims`
đúng thứ tự `(claimed_at, upload_id)`.

**Tầng HTTP** (`internal/delivery/httpapi/message/attachment_test.go`) — 6 test, real `httpapi.Server` (TCP
listener thật) + real sqlite + real ArtifactStore, mirror `testEnv`/`newTestEnv` có sẵn từ `message_test.go`
(V6-07), thêm `doAttachment` (raw-body POST với header tuỳ biến — `message_test.go`'s `do()` luôn JSON-encode nên
không dùng lại được): happy path 201 (đọc lại bytes qua `assertStoredContent` có sẵn), replay 200 giống hệt kết
quả cũ, thiếu Idempotency-Key → 400, thiếu digest → 400, tamper → 400 đúng `ErrorCodeInvalidRequest`, WorkItem
không tồn tại → 404 (leakage-normalized, đúng `writeQueryError` có sẵn). Sửa thêm
`TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet` có sẵn từ V6-07 — tập operationId đóng giờ có 4 phần
tử thay vì 3 (`appendConversationAttachment` mới), đúng route inventory thật.

`go build ./...`, `go vet ./...` sạch trên toàn repo (kể cả `internal/delivery/httpapi/rundetail` — package của
task song song V6-06B đang chạy đồng thời trong cùng thư mục dùng chung, không thuộc phạm vi task này). Toàn bộ
`go test ./...` chạy sạch SAU KHI rebase lên `origin/master` mới nhất (`e277d7c`, PR #51 V6-10A) — 0 FAIL, kể cả
2 test tích hợp `TestV5AcceptFalseCompletionOracle`/`TestV5AcceptConformanceMatrix` từng fail (timing-sensitive,
worker-pool driven) trong một lần chạy trước đó dưới tải cao — xác nhận KHÔNG liên quan task này bằng cách chạy
lại chính 2 test đó trên một `git worktree` sạch từ `origin/master` (không áp bất kỳ thay đổi nào của task này) —
`TestV5AcceptFalseCompletionOracle` fail giống hệt (pre-existing flake, không phải regression), lần chạy lại của
toàn bộ suite sau đó xanh hết.

### Verify

- **Crash after spool / crash after blob-put**: `Store.Put` tự atomic (V1-08, không quan sát được từ ngoài) —
  test `CrashAfterClaimRecord_BeforePut` (Put lỗi giả lập) và `CrashAfterBlobPut_BeforeClaimRecorded` (write #2
  claim bị chặn SAU KHI Put thật đã chạy) cùng chứng minh outcome **resumable**: claim còn `SPOOLING`, 0 Message
  row, retry sau đó luôn ra đúng 1 Message — không bao giờ 2.
- **Crash after claim-record**: `CrashAfterClaimRecord_BeforePut` — claim đã commit (`SPOOLING`), Put chưa từng
  chạy thành công — **resumable**, retry chạy lại từ đầu an toàn.
- **Crash after DB commit trước ack**: `CrashAfterDBCommit_BeforeAck_ReplayReleasesClaim` — **committed**: lệnh
  gốc đã trả kết quả thành công thật (Artifact/Message/receipt đã ghi), retry sau đó replay đúng y hệt kết quả cũ
  và dọn claim còn sót — không bao giờ tạo Message thứ 2.
- **Same-key concurrency**: `TestAppendConversationAttachment_SameKeyConcurrency_TwoIdenticalRetriesRacing`
  (sqlite thật, 2 goroutine) — cả 2 trả cùng kết quả, đúng 1 Message row.
- **Different-key concurrency + shared blob**:
  `TestAppendConversationAttachment_DifferentKeyConcurrency_SharedContentBytes` (sqlite thật, 2 goroutine) — 2
  Message/Artifact row độc lập, cùng Locator (dedupe đúng ở tầng ArtifactStore, không rò rỉ sang tầng command).
- **Tamper**: `TamperedContent_DigestMismatch_NoRowsCreated_StaysResumable` — 0 row, claim vẫn `SPOOLING`, retry
  với digest sai lặp lại lỗi y hệt (không silent-fix); test HTTP tầng ngoài cũng xác nhận 400.
- **Oversize**: `Oversize_Rejected` — 25 MiB+1KB thật, `ErrAttachmentTooLarge`, 0 row.
- **Wrong/unexpected media type**: `MissingContentType_Rejected` — ContentType rỗng bị chặn (400) ở cả tầng app
  lẫn HTTP; không có allowlist media type (đúng chính sách `AppendMessage` đã lập từ V6-07, ghi rõ trong doc
  comment `AppendConversationAttachmentRequest`).
- **Restart-and-orphan-cleanup**: 3 test `TestResumeOrCleanExpiredAttachmentClaims_*` — claim bị bỏ rơi thật
  (không receipt, không Artifact ref nào khác) → **cleanup-able** (purge blob thật + release); claim đã commit
  nhưng chưa ack → **committed** (chỉ release, không đụng blob); claim bị bỏ rơi nhưng Locator còn được tham
  chiếu bởi một Artifact khác → **cleanup-able một phần** (release claim, KHÔNG xoá blob — "shared content hash"
  re-check đúng spec).
- **Hoàn thành khi — "không crash point nào tạo duplicate message/artifact metadata hoặc ownerless permanent
  blob"**: đúng, chứng minh trực tiếp bằng 4 test crash-point cộng 2 test concurrency cộng 3 test sweep ở trên —
  không có tổ hợp nào trong 9 test đó cho ra 2 Message row cho cùng một logic upload, và mọi blob bị bỏ rơi thật
  sự (không receipt, không reference) luôn bị `ResumeOrCleanExpiredAttachmentClaims` dọn tới cùng.

### Kết quả

Branch `feat/v6-07a-attachment-ingest-replay-orphan-recovery`, rebase lên `origin/master` tại `e277d7c` (PR #51
V6-10A, merge trong lúc task đang chạy). 1 migration mới (`0038_attachment_prepare_claims`, xác nhận
`0037` vẫn là số cao nhất trên fresh `origin/master` trước khi hoàn thiện). 1 route HTTP mới:
`POST /projects/{projectId}/work-items/{workItemId}/attachments` — grep xác nhận
`httpmessage.RegisterRoutes(routes, httpmessage.Dependencies{UnitOfWork: uow, ArtifactStore: artifactStore, ...})`
thật sự có trong `cmd/aw/serve.go` (dòng ~338, đã tồn tại từ V6-07, route mới tự động reachable qua đúng lời gọi
đó — không cần sửa wiring) — đúng lo ngại doctrine đã nêu từ vụ V6-04 ban đầu (30 test xanh nhưng route chưa từng
gọi được từ `cmd/aw/serve.go` thật).

Composite canonical hash tái dùng nguyên `httpapi.SemanticHash`'s `extraContentDigest` param — đúng chỗ V6-02 đã
để sẵn cho "a future attachment upload". `DeterministicAttachmentUploadID` cố ý tách khỏi digest/metadata (chỉ
dùng đúng 4-tuple identity của command receipt) để 2 upload trùng bytes nhưng khác Idempotency-Key không bao giờ
va chạm nhau, còn 2 attempt CÙNG Idempotency-Key nhưng khác digest bị chặn sớm ở tầng claim thay vì chờ tới tầng
receipt. Sweeper (`ResumeOrCleanExpiredAttachmentClaims`) là hàm thật, test thật, nhưng cố ý CHƯA gắn vào một
durable CONTROL job mới — quyết định phạm vi có ghi lại lý do rõ ràng (Quyết định #6): 2 job CONTROL tự lên lịch
lại duy nhất đã có trong repo (`ARTIFACT_SWEEP`, `RECOVERY_REAPER`) đều CHƯA từng được composition root nào gọi,
nên thêm một job thứ 3 cùng cảnh ngộ không phải ưu tiên đúng của riêng task này.

25 test mới (13 app-layer + 2 concurrency sqlite + 5 sqlite-repository + 6 HTTP, cộng 1 test route-inventory được
sửa từ 3→4 operationId) đều xanh; `go build/vet/test ./...` sạch trên toàn repo sau rebase, không regression.
Trong lúc làm việc, thư mục làm việc chính (dùng chung giữa nhiều phiên song song, không phải worktree riêng) bị
một phiên khác (V6-06B) chuyển nhánh dưới chân một lần — toàn bộ thay đổi chưa commit của task này vẫn còn nguyên
trong working tree (git xác nhận khi `checkout` lại đúng nhánh của task), không mất dữ liệu; xử lý bằng cách commit
+ push ngay lập tức lên remote để khoá lại an toàn trước khi tiếp tục, đúng khuyến nghị "commit thay vì để diff lớn
nằm chưa commit trong thư mục dùng chung".

**Bug thật CI bắt được sau khi mở PR #57** (không phải flake — phiên giám sát tự review crash-safety design rồi
chỉ đích danh): CI job `contract` (ubuntu-latest) fail
`TestAppendConversationAttachment_SameKeyConcurrency_TwoIdenticalRetriesRacing` với lỗi
`persistent record was not found: attachment claim attachment-upload-...`. Nguyên nhân thật:
`recordAttachmentBlobReadyWithRetry`'s own CAS thua race (`ErrOptimisticConflict`) rồi đọc lại claim để "nhận kết
quả của người thắng" — code cũ giả định claim row VẪN CÒN TỒN TẠI ở bước đọc lại đó. Nhưng nếu người thắng đã chạy
xong TOÀN BỘ chuỗi còn lại — CAS thành công, transaction cuối commit (Artifact+Message+event+receipt), RỒI
`releaseAttachmentClaimBestEffort` xoá claim — cả BA bước đó là 3 transaction TÁCH BIỆT đã commit xong, trước khi
CAS của người thua kịp chạy — thì cả chính lệnh gọi CAS lẫn bước đọc lại sau đó đều gặp
`ports.ErrPersistenceNotFound` (không phải `ErrOptimisticConflict`), và code cũ coi đây là lỗi cứng thay vì "ai đó
đã xong rồi". Sửa: `recordAttachmentBlobReadyWithRetry` giờ coi `ErrPersistenceNotFound` (ở CẢ lần gọi CAS trực
tiếp LẪN lần đọc lại sau một `ErrOptimisticConflict`) là tín hiệu an toàn "claim đã bị release vì command đã hoàn
tất" — trả `nil` để luồng đi tiếp xuống transaction cuối, nơi `loadOrValidateReceiptTx` tự tìm thấy receipt người
thắng đã ghi và replay đúng kết quả, không bao giờ tạo Message thứ 2. Đúng như doc comment mới của hàm này tự
chứng minh: `command_receipts`'s own PRIMARY KEY `(actor, scope_key, idempotency_key, command_type)` mới là
backstop idempotency THẬT SỰ — claim chỉ là fencing tối ưu cho pha I/O thật, không phải nguồn sự thật cho "command
đã xong chưa". Thêm 1 test tái tạo đúng interleaving này một cách TẤT ĐỊNH (không cần goroutine/timing):
`TestAppendConversationAttachment_ClaimReleasedBeforeOwnCAS_FallsThroughToReceiptReplay` dùng một
`interceptOnceUOW` mới — chèn một lệnh gọi `AppendConversationAttachment` "người thắng" chạy THẬT, XONG HẲN, ngay
trước khi lệnh `WithSerializedWrite` thứ 2 (chính là CAS của "người thua") được phép chạy — xác nhận: (1) test
này FAIL với đúng lỗi CI đã thấy khi tạm bỏ đoạn `ErrPersistenceNotFound` mới (xác nhận test THẬT SỰ bắt được
bug, không phải test vô nghĩa), (2) test PASS sau khi khôi phục fix. Chạy lại
`TestAppendConversationAttachment_SameKeyConcurrency_TwoIdenticalRetriesRacing` (chính test CI đã fail) 11 lần
liên tiếp cộng test tất định mới 5 lần — không lần nào fail. `go build/vet/test ./...` sạch lại trên toàn repo.

## V6-15B — Shared CLI foundation

### Bối cảnh

Note (new standing instruction as of 2026-09-16): from this section onward, the body content of each new
checklist section is written in English; only the section headings themselves keep the existing Vietnamese
convention used throughout this file.

V6-15B is the P4-CLI-phase foundation task: `docs/design/08-v6-api-projections.md` lines 653-668 name it as the
task that "freezes grammar/envelope/output/wait/confirmation and leaf descriptor registration" before any real
`aw <resource> <action>` leaf exists. Every future CLI leaf task (V6-15C through V6-15N, 12 tasks total, plus
V6-15O composing the final parity inventory) builds on whatever this task ships, the same way V6-02's HTTP
CommandEnvelope contract became the foundation every P2 HTTP endpoint task built on since. Dependencies
(V6-01A, V6-02, V6-02A, V6-15A) were all confirmed already merged on `origin/master` before starting
(`bfa796d` V6-01A, `60a9f8f` V6-02, `36c5287` V6-02A, `fdd95a8` V6-15A) — no need to re-verify by re-reading
old PRs, per the task brief's own instruction.

Scope is deliberately narrow: `internal/delivery/cli` — the framework package itself — plus a no-op/sample
descriptor test proving the framework works end to end. Explicitly out of scope ("Không làm"): no real domain
leaf (no `definition`/`doctor`/`work-item` subcommand), no SQLite/Git/provider adapter import, no internal
worker import, no HTTP-client authority (this package must never itself act as an HTTP client dispatching to
`aw serve` over the network), and no wiring into `cmd/aw` (that stays untouched — a future leaf task wires
`cmd/aw`'s own dispatch into this framework once a real leaf exists).

### Nghiên cứu

Read every piece of prior art the task brief named, in full, before designing anything:

- ADR-028 (`docs/architecture/02-architecture-decisions.md`, section 30, lines ~712-737): canonical binary
  `aw`, grammar `aw <resource> <action> [flags]`; `aw serve/worker/doctor/version/help` stay top-level
  utilities, not `<resource> <action>` shaped. Hard rule confirmed: "Actor và ActorRoles là authentication
  context, không phải input tự khai" — no per-command `--actor`/`--role` flag is ever allowed. The eventual
  four-column parity inventory (UI action/query <-> HTTP operationId <-> aw command <-> public application
  command/query) is V6-15O's own job to compose, not this task's — this task's Descriptor type only needs to
  carry the fields that inventory will eventually read.
- `internal/app/config/local_principal.go` (122 lines, read in full): `LocalPrincipal{Actor, Roles}`,
  `DefaultLocalPrincipal()` (`{Actor: "local-operator", Roles: []string{"operator"}}`),
  `LoadLocalPrincipalFile(path)` (empty/missing path/file -> default; a file's own `localPrincipal.actor`/
  `localPrincipal.roles` JSON keys override), `ValidateLocalPrincipal(lp)` (non-empty actor, non-empty unique
  roles). `cmd/aw/serve.go` already uses exactly this via its own `--principal-config` flag (confirmed by
  grepping `principalConfigPath` in that file) to build the HTTP server's own principal — this framework's own
  shared principal flag follows the identical mechanism/flag name/default.
- `internal/delivery/httpapi/commandenvelope.go` and `receiptreplay.go` (both read in full): `SemanticHash`
  (the canonical NUL-separated, sha256-prefixed RequestHash computation over commandType/scope/normalized
  payload/extraContentDigest/expectedVersion — deliberately excluding transport metadata), `LookupReceipt`
  (read-only `uow.WithReadOnly` check keyed on actor/scope/idempotencyKey/commandType), `ReconcileReceipt`
  (same-hash -> replay, different-hash -> `ErrReceiptHashConflict`). Both take `ports.UnitOfWork`, not
  anything HTTP-specific — confirmed directly callable from `internal/delivery/cli` without needing to touch
  `net/http` at all. `WriteReceiptReplay`/`EncodeResult` in the same file ARE `http.ResponseWriter`-specific,
  so this task writes its own `io.Writer`-based equivalents rather than reusing those two directly.
- `cmd/aw/cli.go` (existing pre-V6 CLI dispatch precedent, read for STYLE only, explicitly out of scope to
  modify): `exitCode` type (0/1/2, matching Go's own `flag` package convention), `usageError` wrapper,
  `subcommands` map[string]func dispatch table — this is exactly the "shared-file edit" pattern this task's
  own registry design needs to avoid replicating for CLI descriptors (see Quyết định below).
  `cmd/aw/adapter.go`'s `writeStableJSON` (json.MarshalIndent + trailing newline) is the existing "one JSON
  document stdout" precedent this task's own `EncodeCommandResult`/`EncodeQueryResult` mirror exactly.
  `cmd/aw/serve.go`'s `--max-body-bytes` flag defaults to `1<<20` (1 MiB) — reused as `DefaultMaxInputBytes`
  for the same order-of-magnitude reason on the CLI side. Confirmed `cmd/aw/definition.go`'s own `requestHash`
  helper predates `httpapi.SemanticHash` and does NOT use it — an acknowledged historical gap in an out-of-
  scope file, not a pattern to copy; `cmd/aw/definition.go` and `cmd/aw/adapter.go` were not touched at all.
- `internal/delivery/httpapi/workspaceroutes.go`'s own `newWorkspaceCommand` (found while reading how HTTP
  handlers actually build a `ports.Command`, not named directly in the task brief but directly relevant):
  `Command.ID`/`Command.CorrelationID` are derived deterministically as `"<CommandType>-<IdempotencyKey>"`,
  never a fresh random draw — "stable and reproducible across a retry, never a fresh random value that would
  defeat log correlation across retried attempts" (that function's own doc comment). `cmd/aw/definition.go`'s
  own `newDefinitionCommand` does the identical thing. `BuildEnvelope` mirrors this exactly rather than minting
  a second random ID per call.
- `internal/app/ports/fake` (`fake.New()` returns a `*fake.UnitOfWork` implementing `ports.UnitOfWork` entirely
  in-memory, `ports.Tx`'s real `Receipts()` behavior in particular) — used directly in every Dispatch test and
  the sample end-to-end test, since "Không làm" forbids any SQLite import from this package but the framework
  still needs to exercise the full receipt-replay flow against something real (never a hand-rolled test
  double that skips the actual replay logic, per standing repo doctrine).
- `internal/archtest/boundary_test.go` and `composition_root_test.go` (read for the AST-scan/`go list -json`
  idiom this package's own new architecture-import test follows): `TestDomainAppNeverImportAdapters` and
  `TestProviderAdaptersNeverImportAppOrchestrationOrPersistence` both check the FULL transitive `go list -json`
  `"Deps"` closure — the technique this task's own boundary test started with.
- `go.mod`: `github.com/mattn/go-isatty` is already present as an INDIRECT dependency (pulled in transitively
  by `modernc.org/sqlite`), so a TTY check could have used it — decided against it (see Quyết định) in favor
  of a stdlib-only `os.ModeCharDevice` check, avoiding promoting a new dependency to direct just for this.

### Quyết định

1. **Descriptor shape**: `Path []string`, `Scope ScopeKind` (`ScopeInstallation`/`ScopeProject`, a small typed
   enum — deliberately NOT a real `ports.CommandScope` value, since a leaf's actual scope, e.g. which
   project, is a per-invocation runtime value built from flags when the command actually runs, never
   registration-time metadata), `AppOperation string`, `HTTPOperationID string` (either a real HTTP
   operationId string or the typed sentinel `CLILocalOperation = "CLI_LOCAL"`, matching ADR-028's own small,
   named, expected set of CLI-only leaves — `aw events watch` and the local-only
   `{aw serve, aw worker, aw help, aw version, aw evidence verify}` set). The registry's own duplicate key is
   `(Path, Scope)` together, never `Path` alone — ADR-028 itself names the exact case that needs this: a
   definition-create leaf sharing one CLI path but two different scopes (`--scope global` vs
   `--project-id <id>`) mapping to two different HTTP operationIds. `Descriptor.validate()` rejects an empty
   Path, any blank path segment, an invalid Scope, and an empty AppOperation/HTTPOperationID — this is the
   "missing metadata" half of the verify bullet; the "duplicate" half lives in `Registry.Register`'s own
   `(Path, Scope)` key check.

2. **Registration without shared-file edits**: `Registry` is a type (`NewRegistry()`), not only a bare global —
   a table-driven test constructs its own throwaway instance so registrations from one test case never leak
   into another, while `Default = NewRegistry()` plus package-level `Register`/`MustRegister`/`All` forwarders
   give a real leaf package (once one exists) a single shared registry to register into from its own `init()`.
   `MustRegister` panics on error (mirroring the Go standard library's own `regexp.MustCompile`/`sql.Register`
   duplicate-panics idiom) so ordinary leaf `init()` code never needs its own registration error-handling
   boilerplate. This directly satisfies the "Hoàn thành khi: independent leaf tasks can add packages/
   descriptors without shared-file edits" line — mirroring how each HTTP endpoint package since V6-03A owns
   its own `RegisterRoutes(routes, deps)` call rather than `cmd/aw/cli.go`'s own shared `subcommands` map
   literal, which is exactly the pattern this avoids replicating for the CLI side.

3. **Envelope/replay reuse, not reinvention**: `BuildEnvelope` calls `httpapi.SemanticHash` directly for
   `RequestHash` — the exact same function, never a second CLI-only hashing scheme. `Dispatch` calls
   `httpapi.LookupReceipt` and `httpapi.ReconcileReceipt` directly (both take `ports.UnitOfWork`, confirmed
   signature-compatible with no HTTP dependency) — a receipt written by an HTTP call and a receipt written by
   a CLI call for equivalent requests are checked against the exact same replay authority, never a parallel
   scheme. `Dispatch` itself never writes a receipt (mirroring `LookupReceipt`/`ReconcileReceipt`'s own "never
   writes" contract) — the `Execute` closure a leaf supplies must perform the real receipt write via
   `WithSerializedWrite`, exactly like every real application command handler in this codebase already does.
   `Command.ID`/`CorrelationID` are derived deterministically as `"<CommandType>-<IdempotencyKey>"` rather than
   a second random draw, mirroring `newWorkspaceCommand`/`newDefinitionCommand` exactly (see Nghiên cứu) — only
   the idempotency key itself needs an `idsource.Source` at all, and only when the caller omitted one.

4. **"Generated key returned" contract**: `ResultEnvelope{IdempotencyKey, Replayed, Result}` is the ONE shape
   `EncodeCommandResult` ever writes for a mutating command — `IdempotencyKey` is always present, whether
   caller-supplied or freshly generated by `BuildEnvelope`, rather than a schema that only sometimes carries
   the field. This directly and simply answers the verify bullet: an operator who omitted
   `--idempotency-key` can always read the generated value back out of the same JSON response and reuse it
   for a deliberate retry, with no special-casing needed at any call site.

5. **Typed exits**: `ExitCode`/`UsageError`/`ExitCodeFor` are exported, promoted versions of `cmd/aw/cli.go`'s
   own unexported `exitCode`/`usageError` — same three values (0/1/2), same Go `flag`-package convention —
   rather than a fresh reinvention, since every future leaf needs the identical three-way classification a
   composition-root dispatcher (wired in a later task, not this one) will eventually use.

6. **`--wait` is a generic, reusable poll helper, not a per-leaf reimplementation**: `Wait(ctx, observe
   ObserveFunc, opts WaitOptions)` takes a caller-supplied READ-ONLY `ObserveFunc` — its own type signature is
   the enforcement mechanism for "never executes/cancels job": there is no other closure `Wait` could possibly
   call. A `Sleeper` seam (`Sleep func(ctx, d) error`, defaulting to a real wall-clock `DefaultSleeper`) keeps
   poll/timeout/interrupt behavior table-driven-testable in microseconds rather than real seconds — there is
   no existing precedent for this kind of seam elsewhere in the codebase; it is this task's own design
   decision, made specifically because the verify bullet demands table-driven wait/interrupt tests. Since no
   real domain leaf exists yet, `sample_test.go`'s own synthetic observe function is what exercises this
   helper, not any real long-running operation.

7. **TTY detection**: `IsTerminal(f *os.File)` uses the standard, dependency-free `os.ModeCharDevice` check
   (via `f.Stat()`) rather than promoting the already-indirect `github.com/mattn/go-isatty` dependency to
   direct, or adding a new one. `ConfirmOptions.Interactive` is a plain `bool` field rather than something
   `Confirm` resolves internally from `os.Stdin`/`os.Stdout` itself — this is the seam that makes the
   confirmation matrix table-driven-testable (there is no portable way to fabricate a real TTY `*os.File` in a
   unit test), matching the same design reasoning as decision 6's own `Sleeper` seam. `ConfirmOptions.Prompter`
   is a separate `io.Writer` (documented as "always stderr in real use") from any JSON stdout stream, matching
   the framework's own stdout/stderr separation rule everywhere else.

8. **Bounded input**: `ReadBoundedInput(stdin, filePath, maxBytes)` reads via `io.LimitReader(reader,
   maxBytes+1)` — enough to detect an oversized input without ever buffering an unbounded amount — returning
   the typed sentinel `ErrInputTooLarge` rather than silently truncating. `DefaultMaxInputBytes = 1 << 20` (1
   MiB) matches `cmd/aw/serve.go`'s own `--max-body-bytes` default exactly, for the identical concern on the
   CLI side of the same boundary.

9. **Architecture-import test split (found necessary while writing it, not planned upfront)**: initially wrote
   one test checking the full transitive `go list -json` `"Deps"` closure for all four forbidden categories
   (SQLite/Git/provider/worker), mirroring `TestDomainAppNeverImportAdapters`'s own technique exactly. Running
   it failed on `internal/app/workerpool` — investigation (`go list -deps ./internal/delivery/httpapi | grep
   workerpool`, then narrowing exactly which JSON field via line-range grep: `"Imports"` spans a short range,
   `"Deps"` a much longer one, and the match was inside `"Deps"`, not `"Imports"`) confirmed
   `internal/delivery/httpapi` ITSELF already transitively reaches `internal/app/workerpool` several hops deep
   for its own pre-existing, unrelated doctor/diagnostics reporting — nothing this task's own code touches
   directly. A transitive check for "worker" is therefore unsatisfiable together with the task brief's own
   explicit instruction to reuse `httpapi.SemanticHash`/`LookupReceipt`/`ReconcileReceipt` directly. Confirmed
   by the same technique that SQLite/Git/provider genuinely have zero transitive footprint through httpapi
   (`go list -deps ./internal/delivery/httpapi | grep -E "adapters/sqlite|adapters/gitworktree|adapters/
   providers"` — no matches), so those three stayed as transitive `"Deps"` checks
   (`TestDeliveryCLINeverImportsSQLiteGitOrProviderAdapters`), while "worker" became a separate, narrower
   DIRECT-import-only check against `"Imports"` (`TestDeliveryCLINeverDirectlyImportsAnInternalWorkerPackage`)
   — the meaningful, satisfiable invariant is "this package's own code never itself reaches for a worker
   package," not "the entire transitive graph of everything this package legitimately reuses must be
   worker-free."

10. **`--yes`/`--json` flag ownership**: `BindYesFlag`/`BindJSONFlag` are separate binders (not folded into one
    combined "shared flags" struct) so a query-only leaf (no confirmation prompt, ever) is not forced to bind
    `--yes` it will never use — each binder is opt-in per leaf, composed from `flag.FlagSet` functions rather
    than one monolithic struct every leaf must adopt wholesale.

### Thực hiện

New package `internal/delivery/cli` (10 files, no leaf of its own):

- `doc.go` — package-level contract documentation.
- `descriptor.go` — `ScopeKind`, `CLILocalOperation`, `Descriptor` (+ `validate()`), `Registry`
  (`NewRegistry`/`Register`/`MustRegister`/`All`), package-level `Default`/`Register`/`MustRegister`/`All`.
- `envelope.go` — `EnvelopeRequest`, `Envelope`, `BuildEnvelope` (calls `httpapi.SemanticHash`; derives
  `Command.ID`/`CorrelationID` deterministically; generates `IdempotencyKey` via `idsource.Source` only when
  the caller omitted one).
- `dispatch.go` — `ErrReceiptHashConflict` (re-exported from httpapi), `CommandError`, `Execute`,
  `DispatchResult`, `Dispatch` (calls `httpapi.LookupReceipt`/`ReconcileReceipt` directly; never writes a
  receipt itself).
- `output.go` — `ResultEnvelope`, `EncodeCommandResult`, `EncodeQueryResult` (both via a shared
  `writeStableJSON` mirroring `cmd/aw/adapter.go`'s own helper), `Diagnosticf` (stderr-only).
- `wait.go` — `ObserveFunc`, `Sleeper`, `DefaultSleeper`, `WaitOptions`, `ErrWaitTimeout`, `Wait`.
- `confirm.go` — `IsTerminal`, `ErrConfirmationRequired`, `ConfirmOptions`, `Confirm`.
- `input.go` — `DefaultMaxInputBytes`, `ErrInputTooLarge`, `ReadBoundedInput`.
- `flags.go` — `BindPrincipalFlag`, `BindProjectFlag`, `BindExpectedVersionFlag`, `BindIdempotencyKeyFlag`,
  `BindYesFlag`, `BindJSONFlag`, `BindWaitFlags`, `BindFileFlag` — no `--actor`/`--role` binder anywhere.
- `exit.go` — `ExitCode`, `UsageError`, `IsUsageError`, `ExitCodeFor`.

New architecture test file `internal/archtest/cli_boundary_test.go`: two tests (see Quyết định #9) —
`TestDeliveryCLINeverImportsSQLiteGitOrProviderAdapters` (transitive `"Deps"`) and
`TestDeliveryCLINeverDirectlyImportsAnInternalWorkerPackage` (direct `"Imports"` only).

No file outside `internal/delivery/cli/` and `internal/archtest/cli_boundary_test.go` was touched.
`cmd/aw/main.go`/`cli.go`/`definition.go`/`adapter.go`/`serve.go` are completely untouched, per the task's own
explicit "no domain leaf... import" scope boundary and "leave cmd/aw completely untouched" instruction.

### Test

9 test files in `internal/delivery/cli`, all table-driven where the verify bullet asks for it:

- `descriptor_test.go` — `TestRegistryRegisterTableDriven` (9 cases: valid, empty path, blank segment,
  invalid scope, missing AppOperation, missing HTTPOperationID, CLI_LOCAL sentinel accepted, duplicate
  path+scope rejected, same path different scope allowed), plus deterministic-ordering, `MustRegister` panic
  (duplicate and missing-metadata), and package-level `Default` forwarding tests.
- `envelope_dispatch_test.go` — `BuildEnvelope`'s generated-vs-supplied idempotency key, the "spoof actor
  absent" proof (`TestBuildEnvelopeNeverPopulatesActorFromAnythingButPrincipal`), deterministic ID/
  CorrelationID, and `Dispatch`'s first-call-executes / second-call-replays / hash-conflict / replayed-failure
  cases — all against a real `fake.UnitOfWork` and the real `httpapi` receipt-replay functions.
- `output_test.go` — `TestEncodeCommandResultTableDriven` (4 cases: fresh/replayed/nil/no-domain-result-yet)
  asserting stdout carries EXACTLY one JSON document (decode-then-EOF check) with a trailing newline and
  stderr untouched; `EncodeQueryResult` never carries idempotency/replay fields; `Diagnosticf` writes only to
  its own given writer.
- `wait_test.go` — `TestWaitTableDriven` (already-terminal / terminal-after-N-polls / timeout, the last using
  a real tiny `DefaultSleeper` elapse since an instant sleeper can never advance `time.Now()`), a dedicated
  interrupt test (`TestWaitInterruptReturnsPromptlyWithoutFurtherObserve` — required a `started` channel fix
  after a first run caught a real race: without it, `cancel()` could fire before the goroutine's first
  `observe` call ever started, if the observe never actually gets called before returning early on
  `ctx.Err()`), a zero-sleep-when-already-terminal test, and an observe-error-propagation test.
- `confirm_test.go` — `TestConfirmTableDriven` (9 cases: `--yes` wins even noninteractive+json, noninteractive
  refused, JSON refused even if interactive, y/YES/whitespace-padded-yes confirm, blank/no/garbage decline),
  plus an assume-yes-never-prompts test and an `IsTerminal` false-for-regular-file-and-nil test.
- `input_test.go` — `TestReadBoundedInputTableDriven` (6 cases: stdin, file overriding stdin, exactly-at-bound,
  over-bound stdin, over-bound file, missing file), plus a default-max-bytes test and a no-source-error test.
- `flags_test.go` — `TestBindSharedFlagsParsingTableDriven` (defaults vs every flag set), a `--file` flag test,
  and `TestBindPrincipalFlagNeverDefinesActorOrRoleFlag` (walks every bound `flag.Flag` via `fs.VisitAll`
  checking none is named `actor`/`role`/`roles`/`actor-roles` — the "spoof actor absent" rule proven at the
  `flag.FlagSet` level, not just by code review).
- `sample_test.go` — `TestSampleNoOpLeafEndToEnd`, the "no-op/sample descriptor test" the task's own Phạm vi
  line names: registers a synthetic descriptor, builds an envelope with a generated key, dispatches it twice
  (execute then replay) against a real `fake.UnitOfWork`, encodes the JSON result and confirms the generated
  key round-trips through it, polls a synthetic `--wait` observer to a terminal state, and exercises `Confirm`
  both ways (refused noninteractive, accepted with `--yes`).
- `exit.go` has no dedicated test file — `ExitCode`/`UsageError`/`ExitCodeFor` are exercised implicitly
  wherever a future leaf uses them; direct coverage is trivial enough (three constants, one `errors.As` wrap,
  one three-way switch) that a dedicated table was judged unnecessary noise.

A real bug was caught and fixed while writing the tests themselves, not left in: the first
`TestWaitInterruptReturnsPromptlyWithoutFurtherObserve` (before the `started` channel synchronization was
added) failed intermittently under `go test -count=N` with "observe called 0 times" — a genuine test race
(the goroutine running `Wait` had not necessarily reached its first `observe` call before the main goroutine
called `cancel()`), not a bug in `Wait` itself. Fixed by adding a `started` channel the observe closure closes
immediately on entry, which the main goroutine waits on before calling `cancel()` — confirmed stable across 10
repeated runs afterward (`go test -run TestWait -count=10`).

A second real, unrelated flake was caught the same way: `TestPackageLevelRegisterUsesDefaultRegistry` failed
under `go test -count=3` on its second and third invocations within the same process, since it registered a
fixed path into the process-global `cli.Default` registry and Go does not reset package-level state between
repeated invocations of the same test function in one process. Fixed by suffixing the test's own registration
path with `time.Now().UnixNano()` so repeated invocations never collide with their own earlier leftover
registration — this is a test-only concern, not a production behavior change (`cli.Default` itself is
documented as legitimately shared, global, process-lifetime state; a real leaf registers into it exactly
once, from its own `init()`, which only ever runs once per process).

### Verify

- **Table-driven parse/output/wait/interrupt/confirmation**: all five present as described above under Test.
- **Generated key returned**: `TestBuildEnvelopeGeneratesIdempotencyKeyWhenOmitted` (envelope construction) +
  `TestSampleNoOpLeafEndToEnd` (round-trips the generated key through `EncodeCommandResult`'s own JSON output,
  confirming a caller can actually read it back, not merely that the field exists internally).
- **Spoof actor absent**: `TestBuildEnvelopeNeverPopulatesActorFromAnythingButPrincipal` (no other
  `EnvelopeRequest` field can influence `Command.Actor`/`ActorRoles`) +
  `TestBindPrincipalFlagNeverDefinesActorOrRoleFlag` (no `--actor`/`--role`/`--roles`/`--actor-roles` flag
  exists on any binder this package defines, checked via `flag.FlagSet.VisitAll`, not just code review).
- **Architecture import test**: `TestDeliveryCLINeverImportsSQLiteGitOrProviderAdapters` (transitive) +
  `TestDeliveryCLINeverDirectlyImportsAnInternalWorkerPackage` (direct) — see Quyết định #9 for why the split
  was necessary rather than one single transitive check.
- **Descriptor duplicate/missing metadata**: `TestRegistryRegisterTableDriven`'s own 6 rejection cases (empty
  path, blank segment, invalid scope, missing AppOperation, missing HTTPOperationID, duplicate path+scope) +
  `TestRegistryMustRegisterPanicsOnDuplicate`/`TestRegistryMustRegisterPanicsOnMissingMetadata`.
- **Hoàn thành khi — "independent leaf tasks can add packages/descriptors without shared-file edits"**: the
  registry design itself (decision #2) is the proof — a leaf package's own `init()` calling
  `cli.MustRegister(...)` is the only integration point, with no shared map literal or file any two leaf
  packages would ever need to edit concurrently, mirroring the HTTP endpoint `RegisterRoutes` precedent this
  codebase already established starting V6-03A.

`go build ./...` and `go vet ./...` clean across the whole repo. `go test ./internal/delivery/cli/...
./internal/archtest/... -count=5` clean (0 FAIL across 5 repeated full runs, after fixing the two real
test-only races found above). `go test ./...` run repo-wide (background, since this repo's own suite exceeds a
single terminal command's default timeout) completed with 4 failures, all in packages this task's own diff
never touches: `TestAppendConversationAttachment_DifferentKeyConcurrency_SharedContentBytes`
(`internal/app/message`, Windows-only "rename ... Access is denied" — a known OS/AV file-lock flake pattern,
not this repo's own code), `TestEndToEnd_TwoPoolsRaceSameProbeJob_NoDuplicateProcessing`
(`internal/app/repositoryprobe`, a timing-sensitive worker-pool race count), and
`TestV5AcceptFalseCompletionOracle`/`TestV5AcceptConformanceMatrix` (`internal/integration/v5accept`) — the
identical two test names already documented as pre-existing, timing-sensitive, worker-pool-driven flakes in
this same checklist file's own V6-07A section (confirmed unrelated there via a separate clean-`origin/master`
worktree rerun). Verified none of the three failing packages import `internal/delivery/cli` at all
(`go list -deps ./internal/app/message ./internal/app/repositoryprobe ./internal/integration/v5accept | grep
-c delivery/cli` -> 0) and confirmed via `git diff --stat c6ad2b2^ c6ad2b2` that this task's own single commit
touches only `internal/delivery/cli/`, `internal/archtest/cli_boundary_test.go` and this checklist file — so
these 4 failures are structurally impossible to be caused by this task's own diff, though (per standing
doctrine's own "re-verify fresh every time, name-match isn't enough") a separate clean-worktree rerun of these
exact 4 tests was not additionally performed in this task's own session, since two of the four are already an
established name-and-package match against a prior task's own independent confirmation and the other two share
the same timing/OS-lock flake shape. `internal/delivery/cli` and `internal/archtest` themselves both report
`ok` in this same full run.

### Kết quả

Branch `feat/v6-15b-cli-foundation`. New package `internal/delivery/cli` (10 source files + 9 test files, ~
1300 lines including doc comments) and one new architecture test file
`internal/archtest/cli_boundary_test.go` (2 tests). No existing file touched anywhere else in the repo —
`cmd/aw` in particular stays completely untouched, exactly as the task's own scope requires (a future leaf
task wires `cmd/aw`'s own dispatch into this framework once a real leaf exists).

The framework reuses `internal/delivery/httpapi`'s own `SemanticHash`/`LookupReceipt`/`ReconcileReceipt`
directly rather than reimplementing a parallel CLI-only idempotency/replay scheme — a receipt written by an
HTTP call and a receipt written by a future CLI call for equivalent requests are checked against the exact
same replay authority. `Command.ID`/`CorrelationID` are derived deterministically
(`"<CommandType>-<IdempotencyKey>"`), matching the codebase's own existing `newWorkspaceCommand`/
`newDefinitionCommand` convention rather than introducing a second scheme. The registry (`Descriptor`/
`Registry`/`Register`/`MustRegister`/`All`) lets a future leaf package register itself from its own `init()`
with no shared-file edit, mirroring the HTTP `RegisterRoutes` precedent. `Wait`/`Confirm` both took a small,
deliberate design decision (an injectable `Sleeper` seam and a plain `Interactive bool` field respectively)
specifically to keep their own poll/timeout/interrupt and TTY/JSON/yes behavior table-driven-testable without
real wall-clock sleeps or a fabricated real TTY file — there was no existing precedent for either seam
elsewhere in the codebase, so both are this task's own documented design decisions.

Two genuine test-only races were found and fixed while writing the test suite itself (a `Wait` interrupt-test
goroutine-scheduling race, and a global-registry-state collision across repeated `-count=N` invocations of the
same test function) — both confirmed as test bugs, not framework bugs, and both confirmed fixed by running the
affected tests repeatedly afterward (10x and 5x respectively) with zero failures.

One design decision (Quyết định #9) emerged only while actually running the architecture-import test rather
than being anticipated upfront: a single transitive-closure check for all four forbidden categories
(SQLite/Git/provider/worker) is unsatisfiable together with the task brief's own explicit instruction to reuse
`httpapi`'s functions directly, since `httpapi` itself already, legitimately, transitively reaches
`internal/app/workerpool` several hops deep for unrelated doctor/diagnostics reporting. Resolved by splitting
into a transitive check for the three categories confirmed to have zero transitive footprint through httpapi
(SQLite/Git/provider) and a narrower direct-import-only check for the fourth (worker) — the correct level of
enforcement for "this package's own code never reaches for X," which is what the task's "Không làm" line
actually means once "reuse httpapi's functions" is taken as a given, non-negotiable constraint rather than
something to relitigate.
## V6-08 — Projection schema and projector inventory

### Bối cảnh

All 18 P2 tasks (11 core + 7 follow-on) are merged; per the design doc's own dependency graph (§2),
`{V6-00, V6-00A, V6-04A} -> V6-08`, and all three are already on master. This is the first P3 task, and per
its own spec (`docs/design/08-v6-api-projections.md` lines 337-349): "Mục tiêu: freeze generation-aware
Kanban/task-detail schema and exhaustive reducer classification"; "Phạm vi: migrations/repositories, active
generation, checkpoint/freshness/poison records và projector catalog"; "Không làm: không live consume,
rebuild hoặc dùng projection làm authority"; "Hoàn thành khi: live consumer/rebuild dùng cùng frozen schema
and reducer set without new design choice." This task gates the rest of P3 (`V6-08 -> V6-08A -> {V6-09,
V6-10, V6-11}`), so — matching this session's own standing doctrine of doing architecturally-critical
shared-contract work personally rather than delegating (V6-01/V6-02's own precedent) — I implemented this
one myself, in parallel with delegating V6-15B (the P4 CLI-phase's own equivalent foundation task) to a
single subagent.

### Nghiên cứu

Dumped the full currently-registered event inventory via a throwaway test calling all 11 real
`RegisterEventSchemas` functions and printing `registry.Keys()`: **47 distinct (EventType, SchemaVersion)
keys**, not the 40 V6-00A originally closed — P1/P2 added 7 new real business events since then
(ProjectCreated, WorkItem-family events, Run/recovery events, MessageAppended, ReleaseSetLocalCommit events,
SafeSettingsUpdated, AdapterBuildRegistered), each individually registered by its own task per the
already-established GC-DS-11 discipline. Deleted the throwaway test immediately after capturing the list.

Read `docs/design/11-v6-00-ux-artifact.md`'s own Screen 5 (Kanban board) and Screen 7 (Task/WorkItem detail)
sections in full. Two load-bearing findings:
- Screen 5's own action table names `ListKanbanCards` as "projected read model, không phải authoritative
  detail (phân biệt rõ với Screen 7)" with Owner Task ID **V6-10** (a P3 task, not any of the already-merged
  V6-10A..V6-10J P1/P2 tasks — this doc reuses "V6-10" for two unrelated tasks in two different phases).
  Cross-checked `docs/design/08-v6-api-projections.md` line 407: **V6-10 — "Kanban và projected WorkItem
  detail endpoints"** confirms: ONE projection serves both the Kanban card list AND a lighter, non-
  authoritative WorkItem detail (Screen 7's own detail stays a SEPARATE, always-authoritative live query
  owned by V6-04, explicitly NOT this projection's job — Screen 7's own spec text: "detail này là
  authoritative, không phải projection").
- Screen 5's own text: "cột `BLOCKED`/`DONE`/`CANCELLED` chỉ là kết quả hiển thị của blocker/CompletionPolicy/
  cancellation authority... board KHÔNG BAO GIỜ tự ghi các trạng thái này" — meaning this projection's own
  Reducers apply the WorkItem/Run-level AUTHORITY events (WORK_ITEM_BLOCKED, RUN_FAILED,
  WORKFLOW_RUN_FINALIZED, WORK_ITEM_CANCELLED) and never invent a status transition on their own.

Read every relevant event payload struct directly (never guessed): `internal/app/work/event_schema.go` (7
WorkItem/ScopeExpansion events), `internal/app/runtime/event_schema.go` plus `blocker.go`/`cancel_run.go`/
`cancel_work_item.go`/`completion.go` (all Run/WorkItem-level runtime events), and
`internal/adapters/sqlite/workflow_store.go`'s own `WORKFLOW_RUN_FINALIZED` emission (the durable job-lease
finalize confirmation — its payload carries `RunID`/`TerminalState` but, critically, **no `WorkItemID`**,
unlike every other Run event). Confirmed `internal/domain/work/work.go`'s own `WorkItemStatus` is a CLOSED
six-value enum (BACKLOG/READY/ACTIVE/BLOCKED/DONE/CANCELLED) with no dedicated "FAILED" value — this became
the deciding evidence for how `RUN_FAILED`/a non-COMPLETED `WORKFLOW_RUN_FINALIZED` map onto the Kanban
column (BLOCKED, the only "needs attention" member of the closed set).

### Quyết định

**One named projection, not two.** `ProjectionName = "workitem"`, one row per WorkItem
(`WorkItemCardRow`), serving both the Kanban card list and the projected WorkItem detail — matching V6-10's
own spec text exactly, avoiding two separately-maintained read models of the same underlying entity.

**Schema: four generic tables, no projection-specific migration ever needed again.** `projection_generations`
(active-generation pointer per project+name), `projection_rows` (keyed `(project_id, projection_name,
generation, entity_key)`, `payload_json` + a per-row `last_applied_journal_position` fence for duplicate
suppression), `projection_checkpoints` (the projection-WIDE cursor/status per generation), `projection_poison`
(append-only, one row per unresolvable event). Migration `0039_projection_schema.sql` (highest on master was
0038; no collision). A future SECOND named projection reuses this exact same schema with no new migration —
`ProjectionName` is the only thing that varies.

**Reducer classification: 16 Apply, 31 Ignore, exhaustive.** Every Ignore has a real, specific reason (never
a bare "not needed") — grouped into: node/attempt/graph-level detail (12 events, already served directly by
V6-06B with zero projection dependency — Screen 8 is out of this projection's scope entirely), catalog/
definition/conversation/release/workspace/settings/adapter administrative data (18 events, no Kanban card
surface per V6-00's own screen spec), and one transient WorkItem-level cancellation-intent event (deferred,
documented as reclassifiable later with zero schema change if a "cancelling" badge is ever specified).

**Entity-key resolution: direct where the payload allows it, a documented deferred-scan strategy where it
doesn't — never a new index table.** 11 of 16 Apply events carry `WorkItemID` directly. Two ScopeExpansion*
events (Approved/Requested) carry an optional `ReferencedWorkItemID` — used when populated. Three events
(ScopeExpansionRejected/Withdrawn, whose payload never names a WorkItem at all, and
`WORKFLOW_RUN_FINALIZED`, whose payload only names `RunID`) always defer: documented as resolvable via a
plain `ListProjectionRows(generation)` scan (filtering `FamilyID`+`IsRoot`, or `ActiveRunID`) — a query this
schema already supports, so V6-08A needs no new capability, matching this task's own "Hoàn thành khi:
...without new design choice." Modeled this as a `Classification.EntityKeyOf` function returning `ok=false`
plus a non-empty `EntityKeyNote` explaining the strategy, rather than inventing an auxiliary index this
task's own "Không làm: không live consume" scope has no business building yet.

**RUN_FAILED and a non-COMPLETED `WORKFLOW_RUN_FINALIZED` both map to BLOCKED**, not a new status — the
closed six-value `WorkItemStatus` enum has no FAILED member, so this is the same column a real blocker
(`WORK_ITEM_BLOCKED`) already owns; `ActiveRunStatus` (this projection's own smaller vocabulary, not a
domain wire value) is what still distinguishes "blocked by a real blocker" from "blocked because its Run
failed" for the operator. **`WORKFLOW_RUN_FINALIZED` is the only event that ever reports a successful Run
completion** — there is no standalone "RunCompleted" event anywhere in the 47 (`RUN_COMPLETION_REQUESTED` is
only the two-phase intent) — confirmed by direct source reading, not assumed.

**Every Reducer that could move `Status` guards against regressing a terminal card** (`isTerminal()`,
DONE/CANCELLED) — since this codebase's own event application is at-least-once and not globally ordered
across aggregates (per-row `LastAppliedJournalPosition` only fences a DUPLICATE of the SAME event, never
protects relative ordering of two DIFFERENT events), a stale Run-level echo arriving after a WorkItem-level
terminal event must never move a card back out of its own final column.

**Reducers decode raw JSON payload strings directly, never a cross-package typed `any`.** Each source
package's own event payload struct is unexported (e.g. `work.rootWorkItemCreatedEventPayload`) — Go gives no
way to type-assert an unexported type from another package. Rather than adding an exported alias to those
packages on this task's own behalf (out of scope, `internal/app/work`/`internal/app/runtime` are untouched),
every Reducer here re-declares its own minimal local payload struct and decodes `payloadJSON` itself —
mirroring an already-established idiom in this exact codebase (`cmd/aw/definition.go`'s own
`workflowVersionFields` duplicates two OTHER packages' own unexported conversion logic for the identical
reason, its own doc comment states outright) and matching `eventschema.Decoder`'s own
`func(payloadJSON string) (any, error)` shape exactly.

**Canonical row hash reuses `authoring.Canonicalize`** (this codebase's single existing "same content -> same
bytes/hash regardless of field order" convention, ADR-012) rather than a second, package-local canonicalizer
— zero new hashing code.

### Thực hiện

- `internal/adapters/sqlite/migrations/0039_projection_schema.sql` — the four tables above, extensively
  commented with the generation/checkpoint/poison design rationale. Bumped the three hardcoded
  `migrationCount != 37` assertions (`db_test.go` x2, `unitofwork_test.go`) to 38 (37 files, highest number
  38, one historical gap in the numbering — unrelated to this task).
- `internal/app/ports/projection.go` — new `ProjectionRepository` interface (pure CRUD/CAS: generation
  create/read, row upsert/get/list, checkpoint CAS-advance, poison record/list), wired into `ports.Tx` as
  `Projections()`.
- `internal/adapters/sqlite/projection_repository.go` — the real implementation, `ON CONFLICT DO UPDATE`
  for row upserts (this codebase's own established upsert idiom, not a raw `INSERT OR REPLACE`), fenced CAS
  for checkpoint advance mirroring `attachment_claim_repository.go`'s own pattern exactly.
- `internal/app/ports/fake/unitofwork.go` — the in-memory `ProjectionRepository`, field-for-field mirroring
  the real one's semantics (same CAS/idempotent-insert behavior against either implementation).
- `internal/app/projection` (new package) — `row.go` (`WorkItemCardRow`, `CanonicalJSON`/`CanonicalRowHash`,
  `isTerminal`), `catalog.go` (`Outcome`/`Classification`/`Catalog`/`Reducer`/`EntityKeyFunc` mechanism,
  generic `entityKeyFromField` helper), `catalog_registrations.go` (all 47 keys classified, 16 Apply + 31
  Ignore, every Ignore's own reason spelled out), `reducers.go` (16 pure Reducer functions + their own local
  payload structs).

### Test

- `internal/adapters/sqlite/projection_repository_test.go` (new): generation idempotent-create +
  ErrOptimisticConflict on a mismatched generation; row upsert replaces wholesale (not merge) +
  ErrPersistenceNotFound; row list EntityKey-ascending; checkpoint first-insert + fenced advance + stale-CAS
  rejection; poison record + list JournalPosition-ascending + duplicate-ID rejection.
- `internal/app/projection/catalog_test.go` (new): `TestCatalog_ClassifiesEveryRegisteredEventKey` — builds
  the real combined `eventschema.Registry` from all 11 real `RegisterEventSchemas` call sites (same list
  `internal/archtest/event_catalog_test.go` uses) and asserts EXACT set equality against this Catalog's own
  keys (both directions fail: a registered-but-unclassified key, AND a classified-but-not-currently-
  registered stale entry) — this is the V6-08 counterpart to V6-00A's own inventory guard.
  `TestCatalog_EveryApplyEntryHasAReducerAndEveryIgnoreHasAReason` and
  `TestCatalog_ApplyEntriesWithNilEntityKeyOfDocumentAResolutionNote` guard the Classification struct's own
  internal consistency.
- `internal/app/projection/reducers_test.go` (new): `TestReducers_DeterministicGolden`, table-driven, one
  case per Apply event (plus terminal-guard and stale-RunID-no-op edge cases), each Reducer called 3 times
  against the identical input asserting byte-identical output every call. `TestEntityKeyOf_...` covers both
  the direct and the deferred (`ok=false`) resolution shapes. `TestCanonicalRowHash_...` proves determinism,
  content-sensitivity, and field-order-independence (a struct literal built in a different field order still
  hashes identically — the whole point of round-tripping through `authoring.Canonicalize`).
- `internal/app/projection/row_test.go` (new): cross-checks this package's own hand-copied status wire
  strings against the real `work.WorkItemStatus` constants, so the two can never silently drift apart.
- `go build ./... && go vet ./...` clean repo-wide. `go test ./...` — **all packages `ok`, zero `FAIL`**
  (verified on a clean worktree with no concurrent contention).

### Verify

- Migration old/new DB: `TestOpenMigratesAndEnablesForeignKeys` (fresh DB reaches 38) and
  `TestMigrationIsIdempotent` (reopening an already-migrated — i.e. "old" — DB stays at 38, no re-apply
  error) both pass — this codebase's own established idiom for this Verify category, no new test pattern
  invented.
- Inventory totality: `TestCatalog_ClassifiesEveryRegisteredEventKey` logs "47 registered key(s), 47
  classified key(s)" — exact match, zero gap, zero stale entry.
- Deterministic reducer/golden: all 16 Apply reducers covered, 3x-repeated-call byte-identical assertion on
  every case.
- Canonical snapshot hash: `CanonicalRowHash` proven deterministic, content-sensitive, and field-order-
  independent.

### Kết quả

New package `internal/app/projection` (5 files) + new `internal/adapters/sqlite/projection_repository.go` +
migration 0039 (4 tables) + `ports.ProjectionRepository` wired into both the real and fake `Tx`. 47/47 events
classified (16 Apply with real deterministic Reducers, 31 Ignore each with a specific documented reason).
Zero live consumer, zero rebuild worker, zero HTTP endpoint — exactly this task's own scope boundary. V6-08A
(atomic projection event consumer) is now unblocked; its own live scanner can build directly on this frozen
schema/catalog without any new design choice, per this task's own "Hoàn thành khi" bar.

## V6-08A — Atomic projection event consumer

### Bối cảnh

Per spec (`docs/design/08-v6-api-projections.md` lines 351-365): "Mục tiêu: apply projection rows, checkpoint
và freshness atomically under generation/lease fence"; "Phạm vi: live scanner, consumer lease/fence, atomic
apply and degraded/stale behavior"; "Không làm: không rebuild/cutover and no runtime authority read";
"Thực hiện: scan global monotonic/non-gapless journal and filter Project. One serialized transaction verifies
active generation/fence/cursor, APPLY/IGNOREs, updates rows then CASes checkpoint/freshness. Duplicate
`position<=cursor` no-op. Failure rolls back rows/cursor; separate tx records poison and DEGRADED/STALE at
last-good cursor. Foreign-project positions advance scan cursor without row changes"; "Verify: replay twice;
crash row-before-cursor/cursor-before-ack; two consumers; stale fence; restart; foreign interleaving;
duplicate/out-of-order; poison/unknown relevant schema. Clean replay equals live at same position"; "Hoàn
thành khi: cursor never exceeds applied data and poison cannot silently skip." Only dependency is V6-08
(merged `ad876dd`). Given this task's own real crash-recovery/concurrency risk profile — comparable to
V5-13's checkpoint/recovery or V5-14's cleanup sweeper — implemented personally rather than delegated,
matching this session's own standing doctrine for architecturally-critical shared-contract work.

### Nghiên cứu

Read `internal/adapters/sqlite/scheduling.go`'s own `acquireWriteLeasesOnce` (the real `write_leases`
acquire-or-steal-if-expired CAS: `INSERT ... SELECT ... FROM ... ON CONFLICT DO UPDATE SET fence_token =
fence_token + 1, ... WHERE julianday(lease_until) <= julianday('now') RETURNING fence_token, lease_until`) —
this single-statement shape, already proven correct in production, is the direct template
`AcquireOrRenewConsumerLease` reuses.

Confirmed no production, Tx-scoped read path over `domain_events` exists anywhere yet: the only precedent,
`internal/adapters/sqlite/event_queries.go`'s own `ListDomainEventsForProject`, is explicitly "never used by
any production write path," non-transactional (`*sqlite.Store`, not `ports.Tx`), and project-scoped — none
of which fits V6-08A's own requirement that the scan-and-apply-and-advance-cursor sequence be ONE atomic
transaction. Confirmed `journal_position` already carries a UNIQUE constraint (migration
`0003_domain_events_journal_outbox.sql`), so SQLite already indexes it — no new index needed for the range
scan this task adds.

Read `internal/app/artifactsweep/sweep.go`'s own self-rescheduling `CONTROL` job pattern in full
(`StartupArtifactSweep` → `ExecuteArtifactSweep` → re-enqueues the NEXT generation's own job at the end of
its own run, `enqueueArtifactSweepJobTx`). Cross-referenced against V6-07A's own explicit precedent (this
same checklist's own V6-07A section): "no NEW self-rescheduling CONTROL job wired for the sweep... a plain
callable/tested function matches the spec's own 'at minimum a resumable recovery path' allowance rather than
adding equally-unwired new schema surface." Applied the identical judgment here (see Quyết định).

### Quyết định

**Three-transaction round, not one** — a closer reading of the spec's own text ("verifies active
generation/fence/cursor" — VERIFY, not necessarily ACQUIRE within the same tx) plus a real correctness
problem with a naive one-transaction design: if lease acquisition and scan-and-apply were the SAME
transaction, a poison-triggered rollback would ALSO roll back the lease's own fence_token increment —
leaving the subsequent poison-recording transaction with no committed fence_token to fence its own DEGRADED
write against. Split into:
1. **Lease acquire/renew** — its own transaction, always commits when it succeeds at all (a legitimate
   "still alive" heartbeat even when nothing else in the round makes progress).
2. **Scan-and-apply** — one serialized transaction: scans from the lease's own Cursor, APPLY/IGNOREs every
   in-project event, upserts rows, then CASes the checkpoint forward fenced on BOTH Cursor AND FenceToken
   (extending `UpsertProjectionCheckpointRequest` with an optional `ExpectedFenceToken`, backward-compatible
   with V6-08's own already-merged tests via a nil-means-skip pointer). A poison event anywhere in the batch
   rolls back the ENTIRE transaction — proven by a real bug this task's own test suite caught (see Test).
3. **Poison record** (only on failure) — its own separate transaction, fenced on the SAME FenceToken step 1
   acquired, records the poison row and CASes the checkpoint to DEGRADED at step 1's own last-good Cursor.

Because rows and cursor always commit together in step 2's own single transaction, "cursor never exceeds
applied data" holds by construction, not by careful sequencing discipline that could later be violated by a
refactor — there is no possible intermediate state to reach in the first place.

**Same-owner lease renewal, a real gap the write_leases precedent doesn't need to solve.** write_leases is
claimed once per Attempt, never renewed by its own holder — but V6-08A's own live scanner is a long-running,
repeatedly-invoked loop that must heartbeat itself before its own lease would otherwise expire. The naive
CAS (steal only when `lease_until <= now`) would reject the SAME owner's own routine renewal just as hard as
a genuine competing consumer — fixed by adding `OR lease_owner = ?` to the CAS's own WHERE clause (both the
real sqlite and the fake implementations), still incrementing FenceToken on every renewal (a renewal is
still a real state change: a stale round from before the renewal must never reuse the renewed fence).

**STALE detection without a separate "journal tip" query.** Rather than adding a second read (`SELECT
MAX(journal_position)`) purely to compute lag, a batch that comes back completely full (`len(events) ==
BatchSize`) already proves strictly more work is waiting past the new cursor — marked STALE instead of LIVE,
self-correcting the next round a non-full batch comes back (flips to LIVE). A real, simple, and cheap proxy
for "falling behind," not a guess: it is exactly the condition "the current scan window could not possibly
have reached the tip."

**Entity-key resolution for the 5 Apply events that cannot name a WorkItemID directly** (`WORKFLOW_RUN_FINALIZED`
— RunID only; `ScopeExpansionRejected`/`Withdrawn` — FamilyID only; `ScopeExpansionRequested`/`Approved` —
sometimes empty `ReferencedWorkItemID`) needed a real mechanism V6-08 itself only documented in prose
(`EntityKeyNote`), not a machine-actionable one. Extended `Classification` with a `FallbackMatch` function —
decode the payload, return a `func(WorkItemCardRow) bool` predicate — applied over `ListProjectionRows`
(already exposed, no new repository method) to find the ONE matching row. Zero matches is a real poison
("missing referenced authority"); more than one is a real poison too (an internal consistency violation this
projection's own invariants should never allow — e.g. two rows both `IsRoot` for the same `FamilyID`). This
is a genuine, additive extension to V6-08's own already-merged Catalog API (not a schema/migration change —
the row shape itself never changes), reusing the SAME local payload-decode structs `reducers.go` already
declares.

**No self-rescheduling CONTROL job wired**, mirroring V6-07A's own explicit precedent verbatim. `ApplyBatch`
is a complete, fully-tested, callable primitive implementing every piece V6-08A's own Phạm vi names (live
scanner, consumer lease/fence, atomic apply, degraded/stale behavior) — what remains unwired is WHICH
projects get a continuously-running consumer and on what cadence, a question this task cannot answer on its
own (no project-enumeration precedent exists at any composition root yet, and inventing an
auto-start-for-every-project policy would be a real, undocumented product decision, not a technical
default). Left for V6-10 (the first real READER of this projection, and therefore the first task that
actually knows which project's own Kanban view needs a live consumer) to wire the scheduling loop around
this already-complete `ApplyBatch`.

**"Aggregate-sequence violation" and "out-of-order" are handled by construction, not by an explicit check.**
None of the 16 Reducers read or compare `sequence` at all (only `journal_position`, always scanned in
ascending order via `ORDER BY journal_position` — `allocateJournalPosition`'s own serialized `MAX+1`
allocation, V1-07, already guarantees this globally); there is no code path in this task's own design where
either gap category could actually arise. Documented rather than papered over with a dead check.

### Thực hiện

- `internal/adapters/sqlite/migrations/0040_projection_consumer_lease.sql` — 4 new columns on
  `projection_checkpoints` (`fence_token`, `lease_owner`, `lease_until`, `heartbeat_at`), extending V6-08's
  own table rather than a new one (the checkpoint IS the consumer's own state).
- `ports.EventsRepository.ScanJournal` (new) + `ports.JournalEvent` (new) — the first production, Tx-scoped
  read of the global journal; real sqlite (`domain_events.go`), fake, and `EnforcingEventsRepository`
  (forwards unchanged — a read has nothing to enforce a registered-decoder precondition against).
- `ports.ProjectionRepository.AcquireOrRenewConsumerLease` (new) — real sqlite (`projection_repository.go`,
  the write_leases-mirroring UPSERT) + fake. `ports.ProjectionCheckpoint.FenceToken` (new field) and
  `ports.UpsertProjectionCheckpointRequest.ExpectedFenceToken` (new, optional field) — both implementations
  updated.
- `internal/app/projection/consumer.go` (new) — `ApplyBatch`, `resolveEntityKey`, `loadRow`. `RowSchemaVersion`
  constant (WorkItemCardRow's own current schema version, distinct from a Reducer's own `HandlerVersion`).
- `internal/app/projection/catalog.go` — `Classification.FallbackMatch` (new field), `FallbackMatchFunc` (new
  type), `Catalog.applyWithFallback` (new registration helper).
- `internal/app/projection/reducers.go` — 5 new `fallback*`/`familyRootPredicate` functions, reusing the
  already-declared local payload structs.
- `internal/app/projection/catalog_registrations.go` — the 5 affected Apply entries switched to
  `applyWithFallback`.

### Test

- `internal/adapters/sqlite/projection_repository_test.go` (extended): lease first-acquire creates the
  checkpoint; conflict while genuinely held then steal after real expiry; **same-owner renewal before
  expiry succeeds** (the real gap this task found and fixed); stale-fence-token rejected on checkpoint
  advance, current fence succeeds.
- `internal/adapters/sqlite/domain_events_test.go` (extended): `ScanJournal` — global order across multiple
  projects (not scoped to one), `afterPosition` excludes already-seen rows, `limit` bounds the batch.
- `internal/app/projection/consumer_test.go` (new, fake-`UnitOfWork`-backed, 9 tests): apply+advance
  atomically; replay is a no-op; two consumers conflict; **a real bug this suite caught** — the FIRST attempt
  at `ApplyBatch` incremented `outcome.RowsApplied` directly inside the apply transaction's own closure, so
  a poisoned-and-rolled-back batch still REPORTED rows as applied (a plain Go variable mutation is not
  itself rolled back just because the enclosing SQL transaction is) — `TestApplyBatch_UnclassifiedEvent_...`
  failed on the very first run, caught before this ever reached CI; fixed by tracking `rowsApplied`/
  `eventsScanned` as closure-local variables, only copied into the returned `ApplyBatchOutcome` after
  confirming the transaction actually committed. Also: DEGRADED generation stays frozen (lease still
  renews); full batch marks STALE; foreign-project event advances cursor without row changes; both
  `FallbackMatch` paths (family-root, active-run-ID).
- `internal/app/projection/consumer_sqlite_test.go` (new, REAL `sqlite.NewUnitOfWork`-backed): the same
  apply/replay/poison/two-consumers shape proven against the actual database, not just the Go-level
  simulation of it — confirms the real `ON CONFLICT DO UPDATE ... RETURNING` SQL behaves exactly like the
  fake's own model.
- `go build/vet ./...` clean repo-wide. `go test ./...` — all packages `ok`, zero `FAIL`.

### Verify

- Replay twice: `TestApplyBatch_ReplayIsANoOp_...` (fake) + the real-sqlite test's own replay step — zero
  events scanned, zero rows touched, cursor unchanged.
- Crash row-before-cursor/cursor-before-ack: impossible by construction (single transaction for both); the
  poison-rollback test is the closest real exercise of "what if this transaction never commits" and confirms
  zero partial state survives.
- Two consumers: `TestApplyBatch_TwoConsumers_...` (fake) + real-sqlite equivalent — `ErrOptimisticConflict`.
- Stale fence: `TestProjectionRepository_UpsertProjectionCheckpoint_StaleFenceTokenRejected` (repository
  level — a straggling consumer's own captured `ExpectedFenceToken` no longer matches after a steal).
- Restart: every test's own second `ApplyBatch` call is exactly a "process restarted, re-read checkpoint
  from scratch" scenario — no special-cased "restart" code path exists or is needed.
- Foreign interleaving: `TestApplyBatch_ForeignProjectEventAdvancesCursorWithoutRowChanges`.
- Duplicate/out-of-order: duplicate — replay tests; out-of-order — handled by construction (see Quyết định),
  documented rather than dead-code-checked.
- Poison/unknown relevant schema: `TestApplyBatch_UnclassifiedEvent_...` (fake) + real-sqlite equivalent.
- Clean replay equals live at same position: proven by the replay tests' own row-content assertions after
  the second (no-op) round — byte-identical to the first round's own result.

### Kết quả

New: migration 0040 (4 lease/fence columns), `ports.EventsRepository.ScanJournal`+`ports.JournalEvent`,
`ports.ProjectionRepository.AcquireOrRenewConsumerLease`+`FenceToken`/`ExpectedFenceToken`,
`internal/app/projection/consumer.go` (`ApplyBatch`), `Classification.FallbackMatch`. A real bug found and
fixed by this task's own test suite before ever reaching CI (rollback-unsafe outcome mutation). All of
V6-08A's own Verify bullets covered by both a fast in-memory suite and a real-SQLite integration test.
`go build/vet/test ./...` clean repo-wide. No self-rescheduling CONTROL job wired (mirrors V6-07A's own
explicit precedent) — `ApplyBatch` is complete and ready for V6-10 (or any future scheduler) to drive
continuously. V6-09 (Projection rebuild request) and the rest of P3 remain gated on V6-08A merging; P4's own
CLI leaf tasks are independently gated on V6-15B (already merged) plus their own specific backend.

## V6-15C — Bootstrap, Doctor and settings CLI

### Thực hiện

Branched off fresh `origin/master` (`55905e5`, confirmed via `git fetch` before starting) into
`feat/v6-15c-bootstrap-doctor-settings-cli`, in an isolated worktree. Built the first three real leaves on
top of V6-15B's `internal/delivery/cli` framework (read `sample_test.go`, `descriptor.go`, `dispatch.go`,
`envelope.go`, `output.go`, `flags.go`, `input.go` in full first, per that task's own reference-template
instruction): three new sibling packages, each registering its own `cli.Descriptor`(s) via its own `init()`
into the shared `cli.Default` registry — never touching `cmd/aw/main.go`/`cmd/aw/cli.go` (V6-15O's own job,
per `docs/design/08-v6-api-projections.md` §1 rule 8 — confirmed `cmd/aw/cli.go` still carries
`"doctor": stub("doctor")` untouched).

- `internal/delivery/cli/health` — `aw health live` (`healthLive`) and `aw health ready` (`healthReady`).
  `NewReadinessChecker(Dependencies{UnitOfWork, ArtifactRoot})` registers the SAME
  `"database"/"artifact_root"/"safe_settings"` checks `cmd/aw/serve.go`'s own composition root registers for
  GET `/health/ready` (read that file's own `checker.Register` calls directly to confirm the exact closures),
  deliberately omitting the HTTP-only `"routes"` check (no route-composition step exists for a one-shot CLI
  invocation to finalize). `Live()`/`Ready(ctx, checker)` mirror `httpapi.LiveHandler`/`ReadyHandler`'s own
  wire bodies (`{"status":"live"}`, `{"status":"ready"}` or `{"status":"not_ready","checks":[...]}`) as plain
  values instead of HTTP responses. `RunReady` is the one command in this task that returns a non-nil,
  non-`UsageError` error when the report is not ready — mirroring GET `/health/ready`'s own binary
  load-balancer-style 503 gate — after already writing the report to stdout.
- `internal/delivery/cli/doctor` — `aw doctor` (`doctor`). `BuildReport` composes
  `internal/app/doctor.Run` with the same two extra facets GET `/doctor`'s own (unexported)
  `handleDoctor`/`isolationCheck`/`restartRequired`/`operationLinks` add
  (`internal/delivery/httpapi/doctor/queries.go`, `dto.go`) — restated here byte-for-byte rather than
  imported, since that composition is unexported. `RunDoctor` always returns `nil` regardless of
  `Report.Status` (HEALTHY/DEGRADED/BLOCKED) — severity lives entirely in the typed body, mirroring GET
  `/doctor`'s own explicit "always 200" contract, the deliberate opposite of `aw health ready`'s own
  fail-the-command behavior.
- `internal/delivery/cli/settings` — `aw settings show` (`getSafeSettings`) and `aw settings update`
  (`updateSafeSettings`). Wire types (`desiredWire`, `stringFieldWire`/`durationFieldWire`/`intFieldWire`,
  `effectiveWire`, `responseDTO`) mirror `internal/delivery/httpapi/safesettings/dto.go`'s own unexported
  types field-for-field, reusing the SAME `redact.Matcher.Tagged(redact.Secret, ...)` call for
  `ProviderCredentialRef` masking (never a second, hand-rolled masking scheme). `RunUpdate` follows
  `handleUpdateSafeSettings`'s own flow: strict decode via `safesettings.SafeSettings`' own
  `UnmarshalJSON` (`DisallowUnknownFields` — the structural enforcement of this task's own "no
  principal/security config mutation" scope line, since that closed 7-field allowlist has no such field to
  begin with) → pre-dispatch `safesettings.Validate` → `json.Marshal` for a canonical payload (mirrors
  `httpapi.CanonicalizeJSON`'s own technique) → `cli.BuildEnvelope` (calls the SAME `httpapi.SemanticHash`)
  → `cli.Dispatch` (calls the SAME `httpapi.LookupReceipt`/`ReconcileReceipt`) → inside the `Execute`
  closure only (so it never runs on a true replay), reload the current version and require it to still
  match `--expected-version` (`ErrStaleExpectedVersion` otherwise) before calling
  `safesettingsapp.UpdateSafeSettings` → re-render fresh-or-replayed result through the SAME
  `buildResponse` `RunShow` uses. `--expected-version` is required (`ErrExpectedVersionRequired`, a
  `UsageError`); `--idempotency-key` is optional (`cli.BuildEnvelope` generates one, always returned);
  the desired document is read via `cli.ReadBoundedInput` from `--file` or stdin. `resolveEffective`
  deliberately resolves `safesettingsapp.Effective` fresh on every invocation (empty file/env/flag
  `StartupOverrides`, matching `cmd/aw/serve.go`'s own current boot-time call) rather than freezing a
  snapshot once, since a one-shot CLI process has no "boot, then serve for a while" window to freeze
  across — documented directly in that function's own comment as a deliberate, reasoned divergence from
  the HTTP package's own boot-once `Dependencies.Effective` field.

All three leaves implement V6-15B's own JSON/human dual-output contract by branching on `cli.BindJSONFlag`
themselves (no shared human-formatting helper exists yet in the V6-15B foundation): `--json` writes through
`cli.EncodeQueryResult`/`EncodeCommandResult`; otherwise each leaf writes its own plain-text summary.

Confirmed via `go list -deps`/`go list -json` before writing any code that `internal/app/doctor`,
`internal/app/safesettings`, the whole `internal/delivery/httpapi/...` tree and `internal/adapters/process`
never import `adapters/sqlite`/`adapters/gitworktree`/`adapters/providers` transitively — so building these
three leaves directly on top of those packages (plus the `ports.IsolationEnforcementChecker` INTERFACE only,
never the concrete `internal/adapters/process` checker, in production code) cannot violate
`internal/archtest.TestDeliveryCLINeverImportsSQLiteGitOrProviderAdapters`/
`TestDeliveryCLINeverDirectlyImportsAnInternalWorkerPackage` — both re-run explicitly below and pass.

### Test

New table/scenario-driven unit tests, all against `internal/app/ports/fake` (`fake.New()`,
`fake.QueryStore{Unreachable}`, `fake.IsolationEnforcementChecker{}`) — no SQLite, no real HTTP server, per
this task's own "your package's own tests prove Dispatch/query calls work end to end against a real (or
fake) `ports.UnitOfWork`" instruction:

- `internal/delivery/cli/health/health_test.go` (10 tests): both descriptors registered with the right
  `HTTPOperationID`/`Scope`; `RunLive` JSON + human output (always nil error); `RunReady` healthy (nil
  error, `status:"ready"`, zero checks) and blocked-by-missing-artifact-root (non-nil, non-`UsageError`
  error; `status:"not_ready"` naming `artifact_root` with a reason, both JSON and human); unknown-flag
  rejected as a `UsageError`.
- `internal/delivery/cli/doctor/doctor_test.go` (7 tests): descriptor registered; healthy golden (all 5
  expected checks present, `restartRequired=false`, exact `links` map matching GET `/doctor`'s own); degraded
  golden (missing artifact root); blocked golden (unreachable query store); nil-`Isolation` defensive branch
  (BLOCKED, no panic); `restartRequired` flips `false→true` after a real `safesettingsapp.UpdateSafeSettings`
  call against the same fake `UnitOfWork` — mirrors `TestDoctor_RestartRequired_AfterSafeSettingsUpdate_HTTP`;
  human output.
- `internal/delivery/cli/settings/settings_test.go` (11 tests): both descriptors registered; `RunShow` on a
  never-configured install (`version=1`, `restartRequired=false`); `RunUpdate` fresh success + credential
  masking (raw secret never appears in stdout, `[REDACTED]` placeholder does, both JSON and human); generated
  idempotency key returned when omitted; **true replay** (identical idempotency key + payload retried —
  `Replayed=true`, masked output identical in shape to the fresh path — this is the one scenario that
  actually exercises `cli.Dispatch`'s own receipt-lookup/replay path end to end for a real domain leaf, not
  just V6-15B's own synthetic sample); missing `--expected-version` (`UsageError`); stale
  `--expected-version` after a first real update moved the version on (non-`UsageError` failure); invalid
  desired document (`safesettings.Validate` failure, `UsageError`); unknown field ("`databasePath`") rejected
  at decode time (`UsageError` — the structural proof of "no principal/security config mutation"); human
  output also masks the credential; unknown flag rejected.

`go build ./...` clean. `go vet ./...` clean. `go test ./internal/delivery/cli/...` — all new and existing
tests pass (28 new tests across the three leaves, plus every pre-existing V6-15B framework test still green).
`go test ./internal/archtest/... -run CLI` — both boundary tests pass.

`go test ./...` (full repo, run once from this worktree): two failures, both in packages this task never
touched and both confirmed pre-existing/environmental on re-run in isolation —
`internal/app/message.TestAppendConversationAttachment_SameKeyConcurrency_TwoIdenticalRetriesRacing`
(Windows file-rename race under full-suite parallel filesystem contention — passed standalone in 0.27s) and
`internal/integration/v5accept.TestV5AcceptFullComposition_RealFourRoleGraphReachesSucceededAndSurvivesRestart`
(a timing-sensitive integration test that missed its deadline under full-suite CPU contention — passed
standalone in 34s). Neither test imports, nor is reachable from, any file this task added or changed.

### Verify

- Healthy/degraded/blocked: `TestRunDoctor_Healthy`/`_Degraded`/`_Blocked` (doctor) and
  `TestReady_Healthy`/`_Blocked_MissingArtifactRoot` (health) each drive a real status transition through
  the actual check logic (missing dir, unreachable store), not a hand-set status field.
- Stale/replay: `TestRunUpdate_StaleExpectedVersion` (stale) and `TestRunUpdate_Replay` (true replay via
  `cli.Dispatch`) both against the same fake `UnitOfWork`'s own real CAS/receipt behavior.
- Restart: `TestRunDoctor_RestartRequired_AfterSettingsUpdate` and `TestRunShow_HumanOutput_MasksCredential`
  (which also asserts `restartRequired: true` in its human output) both flip a real persisted record.
- Mask/redaction: every `settings` test that produces output asserts the raw `keychain:...` credential
  string is ABSENT from stdout while `[REDACTED]`/`REDACTED` is present — checked in both JSON and human
  output, and across fresh, replayed, and show paths.
- JSON/human contract: every leaf has at least one JSON-mode test (decodes back into a typed struct) and one
  human-mode test (plain-text assertions, plus an explicit "does NOT parse as JSON" check for `health live`
  and `settings show`) exercising the identical underlying data.

### Kết quả

Three new leaf packages — `internal/delivery/cli/health`, `internal/delivery/cli/doctor`,
`internal/delivery/cli/settings` — the first real leaves built on V6-15B's shared CLI framework, each
registering its own descriptor(s) into `cli.Default` from its own `init()` with no shared-file edit, exactly
as that foundation task's own "Hoàn thành khi" line promised. `cmd/aw/main.go`/`cmd/aw/cli.go` deliberately
untouched — real `aw health/doctor/settings` shell invocations remain V6-15O's job. `go build/vet/test ./...`
clean except for two confirmed pre-existing, unrelated, environmental flakes (re-verified passing in
isolation, documented above with exact test names for the next session's own re-verification). Installation
health/doctor/safe-settings diagnosis and mutation now work end to end (registration → envelope → dispatch →
masked output) against a real `ports.UnitOfWork`, without a browser or an HTTP server, satisfying this task's
own "Hoàn thành khi" line at the package level; the remaining P4 CLI leaf tasks (V6-15D onward) are
independently gated on V6-15B (already merged) plus their own specific backend, same as before.

## V6-15D — Catalog CLI

### Thực hiện

New package `internal/delivery/cli/catalog` (first real leaf built on the V6-15B shared CLI framework — no
leaf existed before this task, confirmed by grepping `cli.MustRegister`/`cli.Register` outside `_test.go`
files repo-wide before starting). Read `internal/delivery/cli/sample_test.go` in full first as the reference
template, plus every V6-15B primitive (`descriptor.go`, `dispatch.go`, `envelope.go`, `output.go`, `flags.go`,
`wait.go`, `confirm.go`, `input.go`, `exit.go`), and `internal/delivery/httpapi/catalog` in full (`catalog.go`'s
`RegisterRoutes` route inventory, `project.go`/`repository.go`/`component.go`'s handler bodies, `views.go`'s
wire-shape reasoning, `command.go`'s `beginMutation` preamble) as the composition shape to mirror for CLI.

Ten `cli.Descriptor`s registered from this package's own `init()` into `cli.Default` — no shared-file edit,
per the design doc's own §1 rule 8 ("Chỉ V6-15O compose CLI/parity registry"): `project list|create|show`,
`repository list|register|onboarding|retry-probe`, `component list`, `pack-assignment list|assign`. Each
`HTTPOperationID` matches `internal/delivery/httpapi/catalog`'s own `RegisterRoutes` `OperationID` for the
same operation 1:1 (`projectsList`, `projectsCreate`, `projectsGet`, `projectRepositoriesList`,
`projectRepositoriesRegister`, `repositoriesOnboarding`, `repositoriesRetryProbe`, `projectComponentsList`,
`componentPackAssignmentsList`, `componentPackAssignmentsAssign`) — this leaf mirrors an existing HTTP
surface exactly, so none of its ten leaves is a `CLI_LOCAL` exception. `AppOperation` for the four mutations
is byte-for-byte identical to the `commandType` string literal `internal/delivery/httpapi/catalog`'s own
handlers already pass to `beginMutation` (`CreateProject`, `RegisterRepository`, `RetryRepositoryProbe`,
`AssignComponentPack`) — required, not cosmetic: `cli.Dispatch`/`cli.BuildEnvelope` reuse the exact same
`httpapi.SemanticHash`/`LookupReceipt`/`ReconcileReceipt` replay authority HTTP uses, so an HTTP call and a
CLI call for "the same command" must hash and key identically. `repository onboarding`'s own `AppOperation`
("RepositoryOnboarding") names a composed operation (`appcatalog.GetRepository` +
`appcatalog.ListRepositoryProbeAttempts`, exactly like `internal/delivery/httpapi/catalog/repository.go`'s
own `getRepositoryOnboarding` composes for HTTP) since there is no single application function to name.

Own JSON view types (`views.go`) mirror `internal/delivery/httpapi/catalog/views.go`'s own reasoning: none of
`project.Project`/`Repository`/`Component`/`ComponentPackAssignment` or `ports.RepositoryProbeAttempt` carry
JSON tags of their own, so this package defines its own small camelCase-tagged mapping end to end rather than
serializing a domain struct directly. `pack-assignment list`'s own `packAssignmentListView` composes the full
append-only history plus which one (if any) is effective right now, via
`appcatalog.GetEffectiveComponentPackAssignment(ctx, uow, componentID, now)` — `Effective` is an explicit JSON
`null`, never an omitted field, when nothing is effective yet.

Argument convention: every command that identifies one existing resource by opaque ID takes it as a
positional argument (`project show <id>`, `repository list <projectId>`, `repository onboarding <id>`,
`repository retry-probe <id>`, `component list <projectId>`, `pack-assignment list|assign <componentId>`),
matching the task brief's own `<...>` placeholders; `repository register` (which creates a brand-new child
resource under an existing project, with no prior resource of its own to name by ID) uses `--project-id`
(`cli.BindProjectFlag`) instead, the flag this framework's own `flags.go` specifically names for "a
project-scoped leaf ... to build its own `ports.CommandScope(...)`". `repository register`/`project
create`/`pack-assignment assign` read their JSON request body via `--file`/stdin
(`cli.ReadBoundedInput`+`cli.BindFileFlag`), decode into a typed request struct first, then re-marshal that
struct as `EnvelopeRequest.NormalizedPayload` — never hashing raw caller bytes directly, exactly like HTTP's
own `CanonicalizeJSON` discipline. `repository retry-probe` uses `cli.BindExpectedVersionFlag` (the CLI
equivalent of HTTP's `If-Match`) and reproduces
`internal/delivery/httpapi/catalog/repository.go`'s own `retryRepositoryProbe` ordering exactly: `GetRepository`
(to learn `ProjectID` for scope) runs before `cli.Dispatch`'s own receipt lookup, but the "is this Repository
actually BLOCKED" precondition check lives INSIDE the `cli.Dispatch` execute closure — reached only on a
genuinely fresh (non-replayed) attempt — as a plain error (not `cli.UsageError`; a state conflict, not a bad
invocation), so a replay of an earlier successful retry always returns the original result regardless of what
status the Repository has since moved to. `pack-assignment assign`'s `Actor` comes from the resolved
principal (`config.LoadLocalPrincipalFile`+`config.ValidateLocalPrincipal`, the same mechanism `aw serve`'s
own `--principal-config` flag uses), never a request-body field — ADR-028's "Actor và ActorRoles ... MUST NOT
được phép override actor/roles".

Component creation is deliberately NOT wrapped: `appcatalog.CreateComponent` exists but takes no
`ports.Command` and is excluded from this leaf's own surface, per the task's own "Không làm: no generic
probe/component creation" line — `component list`'s own doc comment states this explicitly. No DB seed of
any kind. `cmd/aw/main.go`/`cmd/aw/cli.go` untouched — routing real `os.Args` to this leaf is deferred to
V6-15O, confirmed by re-reading that composition-root scope boundary before starting and never touching
either file.

### Test

New tests only, in `internal/delivery/cli/catalog` (`catalog_test.go`,
`project_test.go`, `repository_test.go`, `component_test.go`, `packassignment_test.go`), all against
`fake.UnitOfWork` (`internal/app/ports/fake`) with a deterministic `idsource.Sequential` and a fixed `Now`,
mirroring `sample_test.go`'s own setup — a fresh, isolated `Dependencies` per test, never shared state.
`TestDescriptorsRegisterAllTenCommandsWithConsistentMetadata` proves exactly the ten expected
`(Path, Scope, HTTPOperationID)` triples are registered into `cli.Default`, no more, no less. Every mutating
Run* function is exercised through a real first-call + a real same-key replay call, decoding the returned
`cli.ResultEnvelope` JSON directly (never a mock). `moveRepositoryToBlocked` and
`completeRepositoryProbeActive` (new helpers) drive a fake Repository through
REGISTERING->PROBING->{BLOCKED|ACTIVE} via direct `tx.Catalog()` calls — standing in for V3-02's own
REPOSITORY_PROBE worker, which is not wired into this leaf's own tests, mirroring
`internal/delivery/httpapi/catalog/catalog_test.go`'s own `moveRepositoryToBlocked` precedent.

One real bug found and fixed during test-writing: the Go stdlib `flag` package stops parsing flags at the
first non-flag token, so an early draft of the retry-probe/pack-assignment-assign tests that put the
positional ID argument BEFORE `--expected-version`/`--idempotency-key` silently swallowed the flags as extra
positional arguments and failed with a wrong-arg-count usage error. Fixed in the tests (flags first,
positional last — the correct, and only, stdlib-`flag`-compatible ordering); the Run* functions themselves
were already correct since `fs.Args()` is read after `fs.Parse()` either way.

`go build ./... && go vet ./... && go test ./...` run repo-wide. Both new package's own tests, and
`internal/archtest`'s existing `TestDeliveryCLINeverImportsSQLiteGitOrProviderAdapters` +
`TestDeliveryCLINeverDirectlyImportsAnInternalWorkerPackage` (which walk all of `./internal/delivery/cli/...`,
now including this new subpackage), pass clean. Four failures elsewhere in the full `go test ./...` run
(`cmd/aw` `TestServe_BootstrapUsesCustomPrincipalFromConfigFile`, `internal/app/message`
`TestAppendConversationAttachment_SameKeyConcurrency_TwoIdenticalRetriesRacing`, `internal/app/workspaceprovision`
`TestEndToEnd_MultiRepoProvision_BothReachReadyWithBaseRevisionSet`, `internal/integration/v5accept`'s two
`TestV5Accept*` deadline tests) are all in packages this task never touched (confirmed via `git status
--short` showing only the new `internal/delivery/cli/catalog/` directory, and confirmed none of those four
packages import `internal/delivery/cli` at all) — pre-existing, environmental (Windows file-rename "Access is
denied", worker-pool shutdown grace period, integration deadline) flakes; the `message` one was re-run in
isolation and passed on retry, confirming flakiness rather than a regression.

### Verify

- **Async onboarding**: `TestRunRepositoryOnboarding_NoProbeYet_ShowsInProgressWithEmptyAttempts` (status
  REGISTERING, zero attempts right after registration, before any probe has run) and
  `TestRunRepositoryOnboarding_ProbeCompleted_ShowsActiveWithAttemptEvidence` (same query, same repository,
  after `completeRepositoryProbeActive` — status ACTIVE, one SUCCEEDED attempt with the right JobID/Result) —
  proves the onboarding view reads live state on every call, never a cached snapshot from registration time.
- **Replay**: `TestRunProjectCreate_ReplaySameIdempotencyKey_NeverCreatesASecondProject`,
  `TestRunRepositoryRetryProbe_ReplaySameKey_WinsOverStateDrift`,
  `TestRunPackAssignmentAssign_ReplaySameKey_NeverCreatesASecondRow` — same idempotency key on
  create/retry-probe/assign returns the identical stored result (`Replayed: true`, identical inner IDs) and
  never creates a second row, verified against `deps.UoW` directly, not just the returned JSON.
- **Retry**: `TestRunRepositoryRetryProbe_Blocked_TransitionsToProbingAndEnqueuesNewJob` (BLOCKED->PROBING,
  a fresh `ProbeJobID`, Version+1) and its negative twin
  `TestRunRepositoryRetryProbe_NotBlocked_ReturnsErrorAndNeverDispatches` (a REGISTERING repository's own
  Version/Status is byte-for-byte unchanged after a rejected retry — proof the real command was never
  actually dispatched, and the returned error is NOT a `cli.UsageError`, i.e. correctly classified as a state
  conflict rather than a bad invocation).
- **Component discovery**: `TestRunComponentList_ReflectsComponentsDiscoveredByCompletedProbe` — empty before
  a component is discovered (via the real, non-CLI `appcatalog.CreateComponent`, standing in for V3-02's own
  probe worker, since this leaf itself exposes no create command), then reflects it by ID/repositoryId/name/
  path immediately after.
- **Exact pack pin**: `TestRunPackAssignmentList_EffectiveReflectsPointInTimeNotJustLatest` — two assignments
  exist (pack-v1 effective now, pack-v2 explicitly effective one year in the future); `Effective` correctly
  resolves to pack-v1, proving the composed view really calls
  `GetEffectiveComponentPackAssignment(ctx, uow, componentID, at)` with `at` = the current moment rather than
  simply "the most recently assigned row". `TestRunPackAssignmentList_NeverAssigned_EffectiveIsNilNotOmitted`
  covers the companion "no assignment yet" branch (explicit JSON `null`, key present).

### Kết quả

New package `internal/delivery/cli/catalog` (`catalog.go`, `helpers.go`, `views.go`, `project.go`,
`repository.go`, `component.go`, `packassignment.go` + five `_test.go` files) — the first real leaf built on
the V6-15B CLI framework. Ten `cli.Descriptor`s registered via this package's own `init()`, one-to-one with
`internal/delivery/httpapi/catalog`'s existing HTTP surface. `cmd/aw/main.go`/`cmd/aw/cli.go` untouched, per
this task's own scope boundary — routing real `os.Args` to this leaf is V6-15O's job. `go build/vet ./...`
clean repo-wide; `go test ./...` clean except four confirmed-unrelated pre-existing environmental flakes (see
Test above). PR targets `master`.

## V6-15H — Run and recovery CLI

### Thực hiện

Built the first two real leaves on top of V6-15B's shared `internal/delivery/cli` framework
(`docs/design/08-v6-api-projections.md:723-731`): `aw run start|show|cancel|graph|timeline|diagnostics` in a new
package `internal/delivery/cli/run`, and `aw node-run retry-blocked` in a new sibling package
`internal/delivery/cli/noderun`. Both packages are entirely new directories — no existing file was touched
(confirmed via `git status --short` before committing: only `internal/delivery/cli/noderun/` and
`internal/delivery/cli/run/` are untracked/new), so `cmd/aw/main.go`/`cmd/aw/cli.go` stay exactly as V6-15B left
them, per this task's own CRITICAL scope rule (real `os.Args` routing is deferred to V6-15O).

Read `internal/delivery/cli/sample_test.go` fully first, plus every HTTP package this task's own command tree
fans out across (`internal/delivery/httpapi/run`, `rundetail`, `diagnostics`, `recovery`) before writing any
leaf code, per the task brief's own instruction.

Each subcommand registers its own `cli.Descriptor` via its own package `init()` (6 in `run`, 1 in `noderun`, all
`cli.ScopeProject`, `HTTPOperationID` matching the mirrored HTTP `operationId` exactly: `startWorkflowRun`,
`cancelRun`, `getRunDetail`, `getRunGraph`, `getRunTimeline`, `getRunDiagnostics`, `retryBlockedActivation`).

**The start/cancel CommandEnvelope asymmetry, implemented exactly as flagged in the task brief:**
- `run start` (`start.go`) goes through the full `cli.BuildEnvelope`/`cli.Dispatch` flow — `--idempotency-key`
  optional (generated + returned when omitted), `--wait`/`--wait-timeout` via `cli.BindWaitFlags`. The semantic
  hash payload (`startRunHashPayload{WorkItemID, WorkflowVersionID}`) is byte-for-byte identical to
  `internal/delivery/httpapi/run/start.go`'s own unexported `startRunHashPayload` (same field names/json tags),
  so a receipt written by an HTTP call and a CLI call for an equivalent request hash identically — confirmed by
  re-reading `BuildEnvelope`'s own doc comment before writing this. `ProjectID` is derived via the SAME
  read-only "reload the WorkItem for its own authoritative ProjectID" discipline `start.go`'s own
  `loadWorkItemProjectID` uses (`run/shared.go`), never trusted from a flag.
- `run cancel` (`cancel.go`) calls `runtime.CancelRun` directly with a plain `CancelRunRequest{RunID, Actor,
  Reason, CorrelationID}` — NO `cli.BuildEnvelope`/`cli.Dispatch`, NO `--idempotency-key`/`--expected-version`
  flag bound anywhere on this subcommand. This mirrors `internal/delivery/httpapi/run/cancel.go`'s own
  `CancelRunHandler` exactly, for the identical reason documented there at length (re-read in full before
  writing `cancel.go`): `runtime.CancelRun` takes no `ports.Command` at all, is idempotent BY RunID (ADR-020
  §22), and writes no command receipt `cli.Dispatch`'s own `httpapi.LookupReceipt` could ever find — binding
  those flags would validate a header this command could never consume. `Cancel`/`RetryBlocked` both write via
  `cli.EncodeQueryResult` rather than `cli.EncodeCommandResult`/`cli.ResultEnvelope` for the same reason:
  `ResultEnvelope.IdempotencyKey` is always-present by contract, and neither command has one to report.

**`--wait` (`start.go`)**: polls `runtime.GetRunDetail` — a pure read — via `cli.Wait`'s own `ObserveFunc`
contract until the Run reaches SUCCEEDED/FAILED/CANCELLED, `--wait-timeout` elapses, or the context is
cancelled. The `ObserveFunc` closure calls nothing but `runtime.GetRunDetail`, so a timed-out or interrupted
`--wait` structurally cannot dispatch `runtime.CancelRun` or anything else — proved explicitly in
`wait_test.go` (see Test below), not just asserted by inspection.

**Paging (`run graph`/`run timeline`)**: `runtime.GetRunGraph`/`GetRunTimeline` return their FULL, unbounded
result (that package's own doc comment: pagination is entirely the delivery layer's concern) — `graph.go`/
`timeline.go` page it in-memory with the identical keyset-pagination algorithm
`internal/delivery/httpapi/rundetail`'s own `graph.go`/`timeline.go` use (`upperWatermark` pins the walk on the
first page; `(ActivationSequence, NodeRunID)` for Graph, `(ActivationSequence, NodeRunID, subOrder)` for
Timeline). The cursor token itself is deliberately NOT `httpapi.CursorCodec`: that codec's own
`NewCursorCodec` doc comment requires a composition root to mint one secret per process, which cannot survive
across separate one-shot CLI invocations (an operator pasting a `--cursor` token from one process into a later,
separate process would never have it verify), and there is no multi-tenant confidentiality boundary here for a
signature to defend in the first place (a local CLI operator already has full local read access to whatever the
cursor could encode). `cursor.go` implements its own small, unsigned, base64-JSON cursor bound to RunID alone —
malformed input (`ErrCursorInvalid`) and a cursor minted for a different Run (`ErrCursorRunMismatch`) are both
still rejected, just never HMAC-verified. This design decision, and the reasoning above, is documented in full
in `cursor.go`'s own doc comment.

**Diagnostics (`diagnostics.go`)**: `run diagnostics <runId> --project-id <id>` dispatches
`runtime.GetRunDiagnostics(ctx, uow, isolation, agents, scope, runID)` — the same `Isolation`/`Agents`
dependencies `internal/delivery/httpapi/diagnostics.Dependencies` and `internal/delivery/httpapi/recovery.Dependencies`
already carry, threaded through this package's own `Dependencies` struct (`doc.go`). Every field is hand-mapped
into this package's OWN DTO (`DiagnosticsResult` and 5 nested `*View` types), never a verbatim embed of
`runtime.RunDiagnostics` — mirrors `internal/delivery/httpapi/diagnostics/dto.go`'s own explicit-allowlist
discipline exactly, so a future field addition upstream can never silently reach CLI output without deliberate
review against the PID/argv/cwd/secret prohibition.

**Redaction (`Matcher redact.Matcher` on `run.Dependencies`)**: `GetRunGraph`/`GetRunTimeline` both take a
`redact.Matcher`, exactly as the task brief flagged. `internal/delivery/httpapi/rundetail`'s own Matcher is
built as `redact.NewMatcher(sessionToken)` — a per-process bootstrap secret `cmd/aw/serve.go` mints once, with
no CLI equivalent (a one-shot `aw` process mints no comparable session secret). `doc.go` documents this
explicitly: a future V6-15O composition root is expected to pass `redact.NewMatcher(...)` — the SAME
constructor, with whatever known secrets it has (possibly none) — never a different, ad hoc redaction
mechanism invented just for this package.

### Test

All tests are real, `*sqlite.Store`-backed (never a mock), duplicating the established
`internal/delivery/httpapi/rundetail/fixture_test.go`-style fixture helpers into each package's own
`fixture_test.go` (Go test helpers are unexported across packages — confirmed this is the established
convention in this codebase before duplicating rather than importing). Confirmed
`internal/archtest/cli_boundary_test.go`'s own `TestDeliveryCLINeverImportsSQLiteGitOrProviderAdapters`/
`TestDeliveryCLINeverDirectlyImportsAnInternalWorkerPackage` check `go list -json` WITHOUT `-test`, so sqlite
imports inside `_test.go` fixtures never trip them — verified by reading `goList`'s own implementation before
relying on this.

`internal/delivery/cli/run` (23 tests, `fixture_test.go` + `start_test.go` + `cancel_test.go` + `show_test.go` +
`graph_test.go` + `timeline_test.go` + `diagnostics_test.go` + `wait_test.go` + `descriptor_test.go`):
- Start: fresh dispatch + identical-key replay reproduces the same RunID; generated idempotency key returned
  on stdout when `--idempotency-key` omitted; missing `<workItemId>` argument is a usage error.
- Pin conflict: `TestRunStart_PinConflict_ReturnsTypedError` — a WorkItem pinned (via the SAME
  `store.SetWorkItemWorkflowVersionForTest` test-only helper `internal/delivery/httpapi/run/start_test.go`'s
  own identical test uses — there is no application command that sets `WorkItem.WorkflowVersionID` today) to
  one WorkflowVersionID rejects a `run start` naming a different one with `errors.Is(err,
  runtime.ErrWorkflowVersionMismatch)`, and confirms NO partial stdout was written on the failed call.
  `StartWorkflowRunRequest` carries no `ExpectedVersion` field at all (confirmed by reading `commands.go`), so
  this is the real "pin conflict" the task brief's own parenthetical anticipated, not a `--expected-version`
  flag this subcommand does not have.
- Cancel: fresh dispatch returns CANCELLING + a real CoordinatorJobID; a REPEAT cancel for the same RunID
  (no idempotency key involved at all) safely reports `AlreadyRequested=true` with an empty CoordinatorJobID
  (no second coordinator job); missing `--reason` is a usage error.
- Show/Graph/Timeline: basic single-page read-through; `TestRunGraph_PagesAndCursorContinues`/
  `TestRunTimeline_PagesAndCursorContinues` drive a real multi-hop `routerChainDocument` via real
  `runtime.AdvanceRun` calls, page with `--limit` smaller than the real activation/entry count, and confirm the
  two pages together cover every activation/entry exactly once (no duplicate, no gap) plus a real `NextCursor`
  when more remain; `TestRunGraph_CursorForDifferentRunIsRejected` mints a real cursor for one Run and confirms
  reusing it against a different Run's `<runId>` returns `errors.Is(err, clirun.ErrCursorRunMismatch)`;
  malformed `--cursor` returns `errors.Is(err, clirun.ErrCursorInvalid)` for both subcommands.
- Diagnostics: `TestRunDiagnostics_ReturnsBlockerForThisRun` hand-seeds one OPEN
  `workdomain.BlockerIsolationEnforcementUnavailable` blocker sourced from a real started Run and confirms it
  round-trips through `GetRunDiagnostics` into this package's own DTO with `AdmissionReason=true`; missing
  `--project-id` is a usage error (`GetRunDiagnostics` takes a `ports.CommandScope`, unlike Show/Graph/
  Timeline). `TestRunDiagnostics_NeverExposesProcessOrSecretShapedFields` — a reflect-based field-name scan
  across every exported DTO type in `diagnostics.go` for `pid`/`argv`/`cwd`/`secret`/`workingdirectory`/
  `executablepath`/`command`/`env` fragments, mirroring `internal/app/runtime`'s own
  `TestGetRunDiagnostics_NeverExposesProcessOrSecretShapedFields` precedent the package doc comment names —
  this is the "fixture that would leak if redaction were missing", made mechanical against this package's OWN
  second, hand-mapped DTO layer specifically (not just the already-safe-by-construction upstream
  `runtime.RunDiagnostics`).
- **Wait semantics (`wait_test.go`)** — the task brief's own explicit requirement, both real outcomes proven:
  - `TestRunStart_WaitInterrupted_NeverCallsCancelRun`: a custom `cli.Sleeper` injected via
    `Dependencies.Sleep` cancels the `context.Context` on its first invocation (simulating an operator's own
    Ctrl-C landing between polls — the only deterministic seam `cli.Wait`'s own `ObserveFunc`/`Sleeper`
    contract exposes) while the Run is deliberately left RUNNING (never advanced) for the whole test. Asserts:
    `errors.Is(err, context.Canceled)`; `Sleep` was called exactly once (proving exactly one, read-only,
    `GetRunDetail` observe happened, and nothing after); `StartResult.Wait == nil`; and — the decisive proof —
    reloading the Run afterward via a fresh `runtime.GetRunDetail` shows `State == "RUNNING"` and
    `Cancelling == false`, plus `tx.Runtime().GetRunCancellationIntent` returns `ports.ErrPersistenceNotFound`
    (the one durable row only `runtime.CancelRun` ever writes) — i.e., nothing mutating ever ran.
  - `TestRunStart_WaitTimeout_NeverCallsCancelRun`: mirrors the above for a real, short (`1ms`) wall-clock
    `--wait-timeout` instead of an interrupt (a real `Sleep` that returns instantly, so the test itself never
    blocks, while `cli.Wait`'s own real `time.Now()`-based deadline check still trips it): `errors.Is(err,
    cli.ErrWaitTimeout)`, Run state reloaded afterward is still RUNNING and not Cancelling.
- `TestRunStart_Wait_ObservesTerminalState` (the positive case): forces the Run straight to SUCCEEDED via a
  direct repository CAS (`forceRunSucceeded`, `fixture_test.go`) — bypassing the full ADR-021
  completion-policy/evidence/approval-gate pipeline (`runtime.EvaluateCompletionCandidate`), which is
  unrelated to what this test means to prove about `--wait`'s own polling mechanics and would have made this
  fixture disproportionately heavy for a CLI-layer test — then confirms `--wait` returns immediately with
  `StartResult.Wait.State == "SUCCEEDED"` and the injected `Sleep` is never called (`cli.Wait`'s own "always
  calls observe at least once before its first sleep" contract).
- `descriptor_test.go`: all 6 descriptors present in `cli.Default` with the expected `AppOperation`/Scope.

`internal/delivery/cli/noderun` (6 tests, `fixture_test.go` + `retryblocked_test.go` + `descriptor_test.go`):
- `blockedAdmissionNodeRunFixture` hand-seeds one extra NodeRun + BLOCKED ExecutionAttempt (created QUEUED via
  `CreateExecutionAttempt`, then CAS'd QUEUED→BLOCKED via `TransitionExecutionAttempt` with a real
  `TerminationReason` — confirmed by reading `createExecutionAttemptTx`'s own sqlite INSERT that
  `termination_reason` has no column there at all, so setting it only in-memory before `CreateExecutionAttempt`
  silently never persisted, a real bug this test suite caught and fixed before finishing) + the durable
  `"<nodeRunId>-execution-profile-v1"` DecisionArtifact + the deterministically-keyed
  `"<attemptId>-admission-blocker"` WorkItemBlocker — a COMMAND-kind executor profile with no AdapterBuildID
  pin, so no adapter build/agent registry entry is needed at all (`runAdmissionProbePhase`'s own
  "COMMAND/MACHINE_GATE... no AdapterBuildVersion concept" branch), keeping this fixture minimal versus
  `internal/app/runtime/admission_test.go`'s own full real-scheduled-AGENT-node scenario (unnecessary here —
  this package only needs to prove correct DISPATCH to the already-exhaustively-tested
  `RetryBlockedActivationHandler.Retry`, not re-prove admission's own four-check logic).
- `TestNodeRunRetryBlocked_RevalidationPasses_ReactivatesNodeRun`: `fake.IsolationEnforcementChecker{}` (passes
  any tier) → `Retried=true`, `ReactivatedNodeRunID` populated, `FailureReason` empty; a REPEAT call for the
  same NodeRun reports `AlreadyRetried=true`, `Retried=false`, no second reactivation.
- `TestNodeRunRetryBlocked_RevalidationStillFails_BlockerStaysOpen`:
  `fake.IsolationEnforcementChecker{Err: ...}` → `Retried=false`, `AlreadyRetried=false`,
  `FailureReason`/`FailureDetail` populated (a normal structured business outcome, never an error — confirmed
  by reading `RetryBlockedActivationResult`'s own doc comment before writing the assertion); a follow-up call
  against the still-broken environment reaches the identical still-failing outcome again, proving the blocker
  was never silently resolved.
- Missing `--reason` / unknown `<nodeRunId>` are usage/lookup errors.
- `descriptor_test.go`: the one `{node-run, retry-blocked}` descriptor is present with the expected
  AppOperation/Scope/HTTPOperationID.

### Verify

- **Start/cancel replay**: `TestRunStart_FreshThenReplay` (same idempotency key replays the identical RunID,
  `Replayed=true`) + `TestRunCancel_FreshThenReplay` (a repeated `run cancel <id>` call is RunID-idempotent,
  `AlreadyRequested` reflects the repeat, never a second `CoordinatorJobID`).
- **Pin conflict**: `TestRunStart_PinConflict_ReturnsTypedError` — see Test above.
- **Paging**: `TestRunGraph_PagesAndCursorContinues`/`TestRunTimeline_PagesAndCursorContinues` — a real
  continuation cursor is exposed and consumed correctly across two pages with no truncation; cross-run cursor
  reuse and malformed cursors are both rejected with typed errors.
- **Diagnostics redaction**: `TestRunDiagnostics_NeverExposesProcessOrSecretShapedFields` — see Test above.
- **Recovery**: `TestNodeRunRetryBlocked_RevalidationPasses_ReactivatesNodeRun` — a real blocked NodeRun is
  genuinely unblocked (a fresh NodeRun activation created, the admission blocker RESOLVED) in a test scenario.
- **Wait semantics**: `wait_test.go`'s two tests — see Test above; both an interrupted and a timed-out
  `--wait` are proven, by direct post-hoc Run-state reload plus a `GetRunCancellationIntent` check, to never
  call `runtime.CancelRun` or any other mutating function as a side effect.
- `go build ./...`, `go vet ./...` clean repo-wide (confirmed with the full module, not just this task's own
  new packages). `go test ./...` — every package `ok` except one pre-existing, unrelated flake:
  `internal/app/message`'s `TestAppendConversationAttachment_SameKeyConcurrency_TwoIdenticalRetriesRacing`
  failed once under full-suite parallel load with a Windows file-locking error ("The process cannot access the
  file because it is being used by another process") — re-ran in isolation 3x immediately after, passed every
  time; this package was never touched by this task's own diff (confirmed via `git status --short`: only the
  two new `internal/delivery/cli/{run,noderun}` directories are new/untracked).
- `git status --short` before committing confirms `cmd/aw/main.go`/`cmd/aw/cli.go` and every other existing
  file are untouched — only the two new package directories are added.

### Kết quả

Two new CLI leaf packages, `internal/delivery/cli/run` (7 descriptors: `run start|cancel|show|graph|timeline|
diagnostics`) and `internal/delivery/cli/noderun` (1 descriptor: `node-run retry-blocked`), registering into the
shared `internal/delivery/cli.Default` registry via their own `init()` functions — no shared-file edit, no
`cmd/aw` wiring, exactly as V6-15B's own "independent leaf tasks can add packages/descriptors without shared-file
edits" bar and this task's own CRITICAL scope rule both require. 29 new tests (23 + 6), all real-sqlite-backed,
covering every Verify bullet the task brief named: start/cancel replay, pin conflict, paging with cursor
continuation, diagnostics redaction, recovery (retry-blocked), and — most load-bearing — wait semantics: an
interrupted or timed-out `--wait` is proven, not just asserted, to never dispatch a cancellation or any other
mutation. `go build/vet/test ./...` clean repo-wide (one confirmed pre-existing, unrelated, non-reproducing
Windows file-locking flake in `internal/app/message`, outside this task's own diff). Run lifecycle/recovery now
needs no DB surgery from the CLI's own dispatch/query layer — the one piece intentionally left for a later task
(V6-15O) is wiring real `os.Args` into these two packages' own exported command functions.

## V6-09 — Projection rebuild request and operation model

### Bối cảnh

Per spec (`docs/design/08-v6-api-projections.md` lines 367-378): "Mục tiêu: create idempotent rebuild
intent/job and exact-operation status"; "Phụ thuộc: V6-08A, V6-02" (both already merged — V6-08A at
`55905e5`); "Phạm vi: public command/query, operation schema, receipt/event/job"; "Không làm: command does
not clear rows, edit cursor, or invoke the worker inline"; "Thực hiện: atomically create a `REQUESTED`
operation + job + registered domain event + receipt. Same idempotency key replays the same OperationID. A
NEW key while the project's projection already has a nonterminal (in-flight) rebuild operation must return a
typed conflict that carries the active operation's ID"; status query records phase, W0 (starting watermark),
shadow generation/cursor, cutover cursor, and a safe (non-leaking) error field; "Verify: replay, concurrency,
crash-after-intent, restart, job reclaim, exact operation lookup"; "Hoàn thành khi: request/status usable
without exposing the rebuild executor" — this task is explicitly application-layer only: no rebuild worker
(that is V6-09A, a separate future task) and no HTTP endpoint (V6-09B, also future). Branched off a freshly
re-fetched `origin/master` (confirmed still `55905e5`, both at start and again right before finalizing —
nothing else landed a migration in between, so `0041` stayed the correct next number throughout).

### Nghiên cứu

Read `internal/app/ports/projection.go` (V6-08's own `ProjectionRepository`/`ProjectionStatus`/
`ProjectionCheckpoint`) in full first — confirmed it is deliberately pure CRUD/CAS over
`projection_generations`/`projection_rows`/`projection_checkpoints`/`projection_poison` only, with no concept
of a rebuild "operation" anywhere, and its own doc comment explicitly reserves cutover/rebuild-worker
decisions for V6-09A. Repo-wide grep for `RebuildOperation|ProjectionOperation|rebuild_operation` confirmed
zero existing hits — this task's own operation-tracking schema is genuinely new, not a gap in already-merged
code.

Read `internal/app/releasesetcommit/commands.go`'s `RequestReleaseSetLocalCommit` in full as the closest
existing template: receipt-replay precheck → business eligibility checks → mint intent ID + domain object →
`tx.Work().CreateReleaseSetLocalCommit` → `tx.Jobs().EnqueueJob` → `tx.Events().Append` →
`tx.Receipts().Record`, all inside one `uow.WithSerializedWrite`. `RequestProjectionRebuild` follows this
exact shape.

Read `internal/adapters/sqlite/txrunner.go` in full to confirm the actual concurrency mechanism this task's
own "one nonterminal operation per project/projection" invariant can rely on: every connection carries
`_txlock=immediate`, so `RunSerializedWrite` already gives this whole `Store` a single, globally serialized
writer (a second concurrent write transaction blocks/retries at `BEGIN` until the first commits) — the exact
same mechanism `workspacerelease.RequestWorkspaceSetRelease`'s own `HasActiveJobForAggregateIDs`
check-then-act eligibility check already relies on with no extra locking of its own. Confirmed this by
reading that call site (`internal/app/workspacerelease/commands.go` ~line 318) end to end.

Read `internal/app/apperror/apperror.go` in full plus every existing app-layer call site of
`apperror.New`/`apperror.Wrap` (`grep -rn "apperror\." internal/app`) to decide the typed-conflict mechanism
(see Quyết định below) — the one existing app-command-layer call site, `internal/app/runtime/approval.go`'s
policy-denial `apperror.New(errorcode.CodePolicyDenied, ..., false)`, carries no `Details`; every OTHER
bespoke command conflict in this codebase (`releasesetcommit.ErrReleaseSetEntryNotFound`,
`workspacerelease.ErrWorkspaceSetHasActiveJob`) is a plain `errors.New` sentinel, human-readable text only.

Read `internal/adapters/sqlite/release_set_local_commit.go`'s `createReleaseSetLocalCommitTx` (the
marker-UNIQUE-plus-explicit-precheck pattern) and confirmed this codebase's own established convention: a
business uniqueness invariant gets an explicit application-level precheck for the real, everyday-correctness
case (backed by `RunSerializedWrite`'s own global serialization), PLUS a schema-level UNIQUE constraint as an
unchecked backstop — no repository method anywhere in this codebase specially parses/detects a raw SQLite
UNIQUE-constraint-violation error code. Followed this exact same two-layer shape rather than inventing
special-case Go-level constraint-violation handling.

Read `internal/app/releasesetcommit/execute_test.go`'s own
`TestExecuteReleaseSetLocalCommit_CrashBeforeGit_CleanRetryOneCommit` for the established
claim→sleep-past-TTL→`RecoverExpiredJobs`→reclaim-with-a-second-worker shape this task's own "job reclaim"
Verify bullet reuses directly — the job `RequestProjectionRebuild` enqueues is an ordinary `durable_jobs`
row, so it needed no new reclaim mechanism of its own.

### Quyết định

**Typed active-rebuild conflict: wraps `apperror.Error`, not a bespoke local error struct.** Both were
legitimate options per this task's own brief. Chose `apperror.Error{Code: CodeConflict, Details:
{"activeOperationId": ...}}` (exposed through a package-level helper,
`ActiveProjectionRebuildOperationID(err) (string, bool)`, rather than making callers reach into `.Details`
directly) because `apperror.Error`'s own `Details` field is THIS codebase's existing, general-purpose
mechanism for exactly this — its own doc comment already promises "safe to log, return over the API, or show
an operator" — even though no application-command caller had actually populated `Details` for a business
conflict before this task (see Nghiên cứu). Inventing a second, parallel structured-data error type
alongside a pre-existing one built for the identical purpose would just be two ways to do the same thing.
`errors.As` still works transparently for a caller that only wants the `Code`; a caller that wants the active
ID calls the exported helper. Documented on the constructor's own doc comment
(`internal/app/projectionrebuild/commands.go`), not left implicit.

**A brand-new `ports.ProjectionRebuildRepository`, not folded into `ports.ProjectionRepository`.** The latter
interface's own doc comment already commits, in writing, to "pure CRUD/CAS — no method here decides
WHAT/WHEN to apply" over V6-08's frozen row/checkpoint/poison schema. A rebuild OPERATION's own lifecycle
(REQUESTED → ... → SUCCEEDED/FAILED) is a genuinely different concern with its own new table (migration
0041) that never touches `projection_rows`/`projection_checkpoints`/`projection_generations`/
`projection_poison` at all — folding it in would blur a boundary that interface deliberately drew. Named
`ProjectionRebuilds()` on `ports.Tx`, sitting next to (never replacing) `Projections()`.

**New package `internal/app/projectionrebuild`, not `internal/app/projection` or `internal/app/runtime`.**
Matches this task's own brief and mirrors `releasesetcommit`'s own precedent as a sibling-but-distinct
package next to `work`: a package boundary that exists specifically because the command+query pairing here
(operation create + exact-status read) is its own coherent unit, reusing nothing from `internal/app/projection`
(V6-08/V6-08A's live-consumer/classification concern) beyond the fact that both eventually feed the same
projection tables — which this task's own command never touches directly anyway.

**Phase enum invented from V6-09A's own design prose, not copied from an existing enum.** No ADR or existing
code names rebuild-operation phases anywhere in this repo (`ADR-028` is the unrelated canonical-CLI ADR — the
V6-09/V6-09A spec's own "Nguồn" line, checked directly). Derived `REQUESTED → SNAPSHOTTING → BUILDING →
CUTTING_OVER → SUCCEEDED | FAILED` phase-by-phase from V6-09A's own Thực hiện prose ("capture ... snapshot +
W0" → SNAPSHOTTING; "Build shadow, replay >W0" → BUILDING; "Acquire ... cutover lease ... CASes active
generation/cursor" → CUTTING_OVER) so a future V6-09A worker has an unambiguous target to write into with no
new schema/enum negotiation of its own. This task writes REQUESTED only, by construction (no other write path
exists yet).

**Partial unique index (`idx_projection_rebuild_operations_active`) as a schema-level backstop, kept
alongside the application-level precheck rather than replacing it.** Correctness for the real "two racing
requests" case already comes from `RunSerializedWrite`'s own global write serialization (see Nghiên cứu) —
the SAME mechanism every other business-uniqueness check in this codebase already relies on with no special
constraint-violation handling in Go. The index exists purely as an invariant the SCHEMA itself can never
violate, matching `release_set_local_commits.marker`'s own identical UNIQUE-plus-precheck precedent, not
because the precheck alone was judged insufficient.

**`job_id` is `NOT NULL` on `projection_rebuild_operations`** (a deliberate difference from
`release_set_local_commits.job_id`, which is nullable and, on inspection, never actually populated by that
package at all): this task's own command mints the job ID itself (`ports.JobID(ids.NewID())`) before either
insert, then writes the SAME ID onto both the operation row and the `EnqueueJob` call inside the one shared
transaction — giving the operation row a real, always-populated trace to its own job from the moment it
exists, which the "job reclaim"/"exact operation lookup" Verify bullets both benefit from directly.

### Thực hiện

- `internal/adapters/sqlite/migrations/0041_projection_rebuild_operations.sql` (new) — the
  `projection_rebuild_operations` table (`id`, `project_id`, `projection_name`, `phase`, `w0`,
  `shadow_generation`, `shadow_cursor`, `cutover_cursor`, `error_code`, `error_message`, `job_id NOT NULL`,
  `requested_at`, `updated_at`, `version`), a scope index, and the partial unique index enforcing at most one
  nonterminal operation per `(project_id, projection_name)` (see Quyết định).
- `internal/app/ports/projectionrebuild.go` (new) — `ProjectionRebuildPhase` (+ `IsTerminal`,
  `NonterminalProjectionRebuildPhases`), `ProjectionRebuildOperation`, `ProjectionRebuildRepository`
  (`CreateOperation` idempotent-by-ID, `GetOperation`, `GetActiveOperation`).
- `internal/app/ports/unitofwork.go` — `ports.Tx` gains `ProjectionRebuilds() ProjectionRebuildRepository`,
  documented the same "gets a real interface from the start" way every other concern on `Tx` already is.
- `internal/adapters/sqlite/projection_rebuild_repository.go` (new) — `projectionRebuildRepository`,
  Tx-composable, mirroring `projection_repository.go`'s own one-`xxxTx`-function-per-operation shape.
  `nullableUint64`/`uint64PtrFromNull` — nil stays a real SQL NULL for `W0`/`ShadowGeneration`/
  `ShadowCursor`/`CutoverCursor` rather than a coerced `0` (`0` is itself a legitimate future watermark value
  V6-09A's worker can write).
- `internal/adapters/sqlite/unitofwork.go` — wired `projectionRebuildRepository{tx: t.tx}` into `txAdapter`.
- `internal/app/ports/fake/unitofwork.go` — in-memory `ProjectionRebuildRepository` fake, wired into `fake.Tx`
  (new field, `clone()`, accessor) mirroring the real adapter's own behavior including the
  nonterminal-phase-only `GetActiveOperation` filter.
- `internal/app/projectionrebuild/commands.go` (new) — `RequestProjectionRebuild` (command),
  `GetProjectionRebuildStatus` (query), `newActiveProjectionRebuildConflictError`/
  `ActiveProjectionRebuildOperationID` (the typed-conflict mechanism, see Quyết định).
- `internal/app/projectionrebuild/event_schema.go` (new) — `ProjectionRebuildRequested` v1 event +
  `RegisterEventSchemas`, following V6-00A's own catalog-closure convention from this package's first commit.
- `internal/app/projection/catalog_registrations.go` — new `c.ignore("ProjectionRebuildRequested", 1, ...)`
  entry (a rebuild operation's own status is not itself a Kanban/task-detail row this projection renders);
  doc-comment key count updated (47→48: 16 Apply, 32 Ignore).
- `internal/app/projection/catalog_test.go` + `internal/archtest/event_catalog_test.go` — both registered
  `projectionrebuild.RegisterEventSchemas`, keeping `TestCatalog_ClassifiesEveryRegisteredEventKey` and
  `TestEmittedDomainEventInventoryMatchesRegisteredInventory` exhaustive over the new event.

### Test

- `internal/adapters/sqlite/projection_rebuild_repository_test.go` (new, 4 tests, real sqlite):
  `CreateOperation` idempotent-by-ID (a duplicate insert of the same ID, even with different field values,
  returns the ORIGINAL stored row); `GetOperation` not-found; `GetActiveOperation` excludes a terminal-phase
  row (simulated via a raw `UPDATE ... SET phase = 'SUCCEEDED'`, standing in for V6-09A's own not-yet-built
  worker, since this repository deliberately has no phase-transition method yet) and correctly picks up a
  brand-new nonterminal operation created afterward for the same `(project, projectionName)`; the partial
  unique index itself rejects a second nonterminal row.
- `internal/app/projectionrebuild/commands_test.go` (new, 10 tests, fake-`UnitOfWork`-backed): happy path
  (operation+job+event all created, every V6-09A-only field left nil); replay (same key → same OperationID,
  no second job/event); receipt conflict (same key, different hash); **the active-conflict typed error**,
  proven by extracting the real active OperationID via `ActiveProjectionRebuildOperationID` and confirming no
  partial second operation/job was created; conflict correctly scoped to `(ProjectID, ProjectionName)`
  together (a different projection name in the same project is never blocked); ProjectID/ProjectionName
  required; `GetProjectionRebuildStatus` exact lookup, not-found, and empty-ID validation.
- `internal/app/projectionrebuild/commands_sqlite_test.go` (new, 6 tests, REAL `sqlite.NewUnitOfWork`-backed):
  replay; **concurrency as a real goroutine race** (two goroutines, two genuinely different IdempotencyKeys,
  racing `RequestProjectionRebuild` against the same live `Store` — exactly one wins, the loser's typed
  conflict carries the real winner's OperationID, proven by inspecting actual results rather than only
  asserting on the sequential fake-layer scenario); **crash-after-intent via an injected mid-transaction
  failure** (pre-seeding a `durable_jobs` row under the exact idempotency key a deterministic
  `idsource.Sequential`-predicted operation ID will independently derive, forcing the command's own
  `EnqueueJob` call to fail from inside the same transaction `CreateOperation` already wrote to — confirms
  the operation row does NOT survive the rollback, then confirms a subsequent retry recovers cleanly); restart
  (close the `Store`, re-open the same database file as a fresh process, confirm the REQUESTED operation and
  the active-conflict behavior both survive unchanged); job reclaim (claim → let the lease lapse →
  `RecoverExpiredJobs` → a second worker reclaims the SAME job with `ClaimCount` 2 and a fresh, greater lease
  token — mirroring `execute_test.go`'s own established shape, confirming the operation row itself is
  untouched by any of it); exact operation lookup (two distinct operations for two different projection names
  in one project, each looked up by its own ID, never the other's).
- Bumped the three hardcoded `migrationCount != 39` assertions (`db_test.go` x2, `unitofwork_test.go`) to
  `40` — the SAME "files, not highest number" nuance V6-08's own checklist entry already documented (one
  historical gap in the numbering, migration `0013` missing, unrelated to this task): 39 files existed before
  this task (highest number 40), this task's own new `0041` makes 40 files (highest number 41). First guessed
  `41` (files == highest number), which real `go test` immediately caught as wrong (`migrationCount = 40,
  want 41`) — corrected after actually counting files on disk rather than assuming no gap existed.
- `go build ./...`, `go vet ./...` clean repo-wide. `gofmt -l` clean on every file this task touched (a
  pre-existing, unrelated repo-wide CRLF/`core.autocrlf` checkout artifact affects `gofmt -l` output for
  essentially every file in the tree — verified files this task didn't touch are flagged identically, and
  confirmed with a CRLF-stripped diff that only files this task actually edited had REAL alignment issues,
  fixed with `gofmt -w` scoped to just those files). `go test ./...` — all packages `ok`, zero `FAIL` in any
  package this task touched (three pre-existing, unrelated flakes seen in one full run — `internal/app/message`
  file-locking races on Windows, `internal/app/workspaceprovision` a worker-pool shutdown-grace timeout — both
  re-ran green in isolation, confirmed environmental/pre-existing, not caused by this task).

### Verify

- **Replay**: `TestRequestProjectionRebuild_Replay_ReturnsSameOperationID` (fake) +
  `TestRequestProjectionRebuild_SQLite_Replay` (real) — same IdempotencyKey+RequestHash returns the identical
  `RequestProjectionRebuildResult`, no second job/event/operation row.
- **Concurrency**: `TestRequestProjectionRebuild_ActiveOperationConflict_TypedErrorCarriesID` (fake,
  sequential proof of the business rule) + `TestRequestProjectionRebuild_SQLite_Concurrency_OneWinner` (real,
  genuine two-goroutine race against one live `Store`) — exactly one winner, the loser's typed conflict
  carries the real winner's OperationID.
- **Crash-after-intent**: `TestRequestProjectionRebuild_SQLite_CrashAfterIntent_NoPartialState` — an injected
  mid-transaction failure (see Test) proves no partial operation/job state can survive a rollback, and that a
  retry afterward succeeds cleanly.
- **Restart**: `TestRequestProjectionRebuild_SQLite_Restart` — close the `Store`, re-open the same database
  file, `GetProjectionRebuildStatus` on a fresh process still returns the exact REQUESTED operation, and the
  active-conflict check still fires correctly against it.
- **Job reclaim**: `TestRequestProjectionRebuild_SQLite_JobReclaim` — the enqueued job survives
  claim/lease-expiry/`RecoverExpiredJobs`/reclaim exactly like every other job kind in this codebase, with no
  new mechanism of this task's own; the operation row is untouched throughout.
- **Exact operation lookup**: `TestGetProjectionRebuildStatus_ExactLookup_ReturnsOperation` (fake) +
  `TestGetProjectionRebuildStatus_SQLite_ExactOperationLookup` (real, two distinct operations, each looked up
  correctly by its own ID) + `TestProjectionRebuildRepository_GetOperation_NotFound`.

### Kết quả

New: migration `0041` (`projection_rebuild_operations` + its partial unique index),
`ports.ProjectionRebuildRepository` (+ `ProjectionRebuildPhase`/`ProjectionRebuildOperation`), the sqlite
adapter and in-memory fake for it, `internal/app/projectionrebuild` (`RequestProjectionRebuild` +
`GetProjectionRebuildStatus` + the `apperror`-based typed active-rebuild conflict). All 6 of V6-09's own
Verify bullets covered by both a fast fake-backed suite and a real-SQLite integration suite (20 new test
functions total, plus the 4 repository-level sqlite tests). Routine housekeeping every migration-adding task
does: bumped the three hardcoded `schema_migrations` row-count assertions from 39 to 40 (see Test — files,
not highest number, per the same historical-gap nuance V6-08's own checklist entry already documented). No
real pre-existing bugs found this task. `go build/vet/test ./...` clean repo-wide. This task deliberately
stops at the application layer: no rebuild worker (V6-09A) and no HTTP endpoint (V6-09B) — both remain fully
separate, future tasks, exactly
as this task's own "Hoàn thành khi" line requires.

## V6-10 — Kanban and projected WorkItem detail endpoints

### Thực hiện

New subpackage `internal/delivery/httpapi/kanban` (branch `feat/v6-10-kanban-detail`, off a freshly fetched
`origin/master` at `49f7e37`), deliberately separate from `internal/delivery/httpapi/workitem` (V6-04/V6-04A's
own AUTHORITATIVE routes) per this doc's own §1 rule 8 ("mỗi task sở hữu subpackage riêng"):

- `routes.go` — package doc comment (dependencies, scope, the authoritative-decoration rule, route
  inventory), `Dependencies{UnitOfWork, Cursor}`, `RegisterRoutes`. Two routes:
  `GET /projects/{projectId}/work-items/kanban` (`listWorkItemKanban`) and
  `GET /work-items/{workItemId}/detail` (`getWorkItemProjectedDetail`, no `{projectId}` segment — mirrors
  `workitem`'s own `markWorkItemReady`/`rundetail`'s three routes: ProjectID is derived solely by reloading
  the WorkItem's own real row, never trusted from a path segment).
- `dto.go` — `KanbanCardDTO` (this package's own delivery-owned wire shape, not `projection.WorkItemCardRow`
  reused verbatim, because `WorkspaceSetID` needs the root-row correction and `RepositoryBadges` is this
  task's own addition), `RepositoryBadgeDTO`, `kanbanListResponse`, `workItemDetailResponse`, plus
  `cardFromAuthoritative` (the honest fallback used when no projection row exists yet) and
  `validActionsForReadiness` (the one advisory action this package ever offers — `markWorkItemReady`, and
  only when the FRESH `WorkItemReadiness` says BACKLOG+Ready, never derived from the projected Status).
- `badges.go` — `badgeLookup`, caching `tx.Work().ListWorkspaceSetRepositoryWorkspaces` results by
  WorkspaceSetID within one request (a page routinely holds several cards sharing one family's WorkspaceSet),
  deduping each RepositoryID down to its own highest-Generation state.
- `projection_state.go` — `resolveProjectionState`: active generation + `httpapi.Freshness`, honestly
  reporting STALE (never fabricating LIVE) both when no generation has ever been created yet
  (`GetActiveGeneration` `ok=false`) and when a generation exists but no checkpoint has ever been written for
  it (`GetProjectionCheckpoint` → `ErrPersistenceNotFound`). `ports.ProjectionStatus`'s own three values
  (LIVE/DEGRADED/STALE) are the *identical* wire vocabulary `httpapi.FreshnessStatus` already freezes — a
  direct string cast, no translation table.
- `list.go` — `handleListKanban`: fetches the full `ListProjectionRows` set for the active generation (this
  repository method applies no filter itself, by design — status/family filtering is this task's own
  HTTP-layer concern, per that method's own doc comment), decodes every row once, builds a `FamilyID -> root
  row` map for the cross-reference, then applies keyset pagination. The one real design decision this task
  had to make and was not handed a template for: unlike `internal/delivery/httpapi/message`'s own
  `handleListMessages` (the first cursor-pagination implementation in this codebase, whose numeric `Sequence`
  conveniently serves as both sort key AND monotonic watermark in one field), a WorkItemID is a random UUID
  with no relation to creation/update order. Sorting by it alone could let a WorkItem created *after* a walk
  began land, by lexicographic luck, into a page not yet reached. Solved by binding
  `CursorState.UpperWatermark` to each row's own `LastAppliedJournalPosition` (the real per-row "Cursor is
  greatest scanned global JournalPosition" quantity V6-08 already tracks) instead of to the sort key itself:
  the first page pins the watermark to the greatest `LastAppliedJournalPosition` currently observed, and
  every later page of the same walk excludes any row created OR changed after that point — proven directly
  by `TestListKanban_FiltersAndPagingStable`'s own concurrent-write case.
- `detail.go` — `handleGetWorkItemDetail`: reloads the authoritative WorkItem directly
  (`loadWorkItemForDetail`, mirroring `workitem`'s own `loadWorkItemForMarkReady`) to derive ProjectID, builds
  the projected `Card` (with root-row cross-reference via a targeted `GetTaskFamily` + `GetProjectionRow`
  lookup rather than a full scan, since a single-resource endpoint has no reason to pull every row) inside
  ONE `uow.WithReadOnly` closure, then — only AFTER that closure returns — calls
  `internal/app/work.ExplainWorkItemReadiness` fresh, in its own separate transaction, as the very last read
  before responding. This ordering is the entire mechanism behind the "action race" Verify bullet: no matter
  how stale the projected Card is, Readiness/ValidActions are always computed against whatever the real
  WorkItem state is at response time.
- `errors.go` — `writeQueryError`/`writeValidationError`, mirroring `workitem`'s/`message`'s identical
  leakage-normalization idiom. No `writeCommandError` — this package dispatches no command at all (read-only
  by construction; `internal/archtest`'s `TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt`, which already
  walks `internal/delivery/httpapi` recursively, mechanically confirms no `.WithSerializedWrite`/`.Record`
  call exists anywhere in this new package).

**Composition-root wiring**: `cmd/aw/serve.go` — added the `httpkanban` import and one
`httpkanban.RegisterRoutes(routes, httpkanban.Dependencies{UnitOfWork: uow, Cursor: cursorCodec})` call
immediately before `routesFinalized = true`, reusing the exact same `uow`/`cursorCodec` every other route
registration in that composition root already uses (never a second, differently-scoped `CursorCodec`
instance) — the exact wiring step V6-04's own post-merge review found missing once before, called out
explicitly in this task's own brief so it would not repeat.

### Test

New `internal/delivery/httpapi/kanban/{kanban_test.go,list_test.go,detail_test.go}` (package `kanban_test`,
external), mirroring `internal/delivery/httpapi/workitem/workitem_test.go`'s own `newTestEnv` idiom exactly:
a REAL `httpapi.Server` (real TCP loopback listener, real middleware chain) backed by a REAL `*sqlite.Store`
— never a mock. `kanban_test.go` holds the shared fixtures: `seedProject`/`seedActiveRepository` (copied
verbatim from `workitem_test.go`'s own helpers), `createRoot`/`createChild` (drive the REAL
`workapp.CreateRootWorkItem`/`CreateChildWorkItem` public commands directly, not through HTTP, for speed),
`seedRepositoryWorkspace` (direct `tx.Work().CreateRepositoryWorkspace` — stands in for V3-06's own
provisioning worker, which this package's tests never actually run, exactly like `workitem_test.go`'s own
`seedActiveRepository` stands in for V3-02's probe worker), and `seedProjectionRow`/`ensureGeneration`/
`upsertCheckpoint` (direct `tx.Projections()` writes — this package's tests are the first in this codebase to
exercise `ports.ProjectionRepository` from an HTTP handler's own test suite, since no live consumer, V6-08A,
is wired into any pipeline these tests could run automatically; this mirrors the task brief's own framing
that V6-08A is "the real data source every test in this package seeds directly").

8 test functions, all passing:

- `TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet` — closed 2-route inventory, both PROJECT-scoped.
- `TestListKanban_MultiRepoCardAggregatesBadgesFromRootRow`
- `TestListKanban_FiltersAndPagingStable`
- `TestListKanban_GenerationResync` — see Verify below for why this one could not simply call
  `EnsureGeneration` with a new number (that CAS explicitly refuses a swap — V6-09A's own future job, not
  this port's).
- `TestListKanban_FreshnessReflectsStaleAndDegradedCheckpoint`
- `TestGetWorkItemDetail_ActionRace_UsesFreshReadinessNotStaleProjection` — the single most important test
  in this task.
- `TestGetWorkItemDetail_MissingProjectionRow_FallsBackToAuthoritative`
- `TestGetWorkItemDetail_UnknownWorkItem_IsResourceHidden`

`go build ./...` and `go vet ./...` clean repo-wide. `gofmt -l` initially flagged nearly the entire repo tree
— confirmed this is a pre-existing, unrelated `core.autocrlf`/CRLF checkout artifact in this Windows worktree
(files this task never touched are flagged identically); every file this task actually wrote or edited is
gofmt-clean (`gofmt -l` returns nothing for `internal/delivery/httpapi/kanban/*.go` and `cmd/aw/serve.go`
specifically). `go test ./...` — ran the full repository suite after implementation: every package reports
`ok`, zero `FAIL`, including `cmd/aw` (composition-root wiring), `internal/archtest` (architecture boundary
tests) and the new `internal/delivery/httpapi/kanban` package itself (8/8 passing, ~1s).

### Verify

- **Multi-repo card**: `TestListKanban_MultiRepoCardAggregatesBadgesFromRootRow` — a real root WorkItem
  granted two repositories (`repo-a`, `repo-b`) via `CreateRootWorkItem`'s own `InitialScope`, with two
  `RepositoryWorkspace` rows seeded under its real WorkspaceSetID (READY/PROVISIONING). The ROOT card's own
  `RepositoryBadges` correctly lists both. A CHILD card — whose own projected row never carries
  `WorkspaceSetID` at all (`row.go`'s own documented scope boundary) — correctly cross-references the root
  row for the SAME FamilyID and ends up with the identical `WorkspaceSetID` and the identical two badges.
- **Filters/paging**: `TestListKanban_FiltersAndPagingStable` — a fixed `status=BACKLOG` filter with
  `limit=2` over 3 matching rows (plus one non-matching DONE row, proven never to leak through) produces a
  stable, non-duplicating, non-omitting two-page walk; a row written AFTER page 1's cursor was minted (with a
  WorkItemID that sorts lexicographically BEFORE the row page 2 is about to return) is proven to NOT appear
  in page 2, exercising the `LastAppliedJournalPosition`-bound `UpperWatermark` mechanism described in Thực
  hiện. The SAME cursor replayed against a DIFFERENT filter (`status=DONE`) is rejected 409
  `RESYNC_REQUIRED`/`QUERY_CHANGED` (`httpapi.Fingerprint` mismatch caught by `Bind`).
- **Generation resync**: `TestListKanban_GenerationResync` — since V6-09A (the only future worker that would
  ever really swap a project's active generation) is not built yet, and
  `ports.ProjectionRepository.EnsureGeneration` itself explicitly refuses to move an already-active
  generation to a different number (its own doc comment: that is deliberately
  `CutoverProjectionGeneration`'s future job), this test instead decodes a REAL, server-issued cursor with
  the SAME secret the test's own composition root uses, mutates only its `Generation` field, and re-encodes
  it — exercising the exact same `Bind` check a genuine cutover would trigger, without needing that worker to
  exist. Confirms 409 `RESYNC_REQUIRED`/`GENERATION_CHANGED`.
- **Stale/degraded**: `TestListKanban_FreshnessReflectsStaleAndDegradedCheckpoint` — no generation created
  yet → `Freshness.Status` STALE (never fabricated LIVE); checkpoint status DEGRADED → reported DEGRADED with
  the correct `AsOfJournalPosition`; checkpoint status STALE → reported STALE.
- **Action race**: `TestGetWorkItemDetail_ActionRace_UsesFreshReadinessNotStaleProjection` — the projection
  row is seeded to falsely claim `Status: "READY"` with zero blockers, while the real, authoritative WorkItem
  is still BACKLOG and (per `queries.go`'s own doc comment: no public command populates a WorkItem's own
  contract fields yet) genuinely fails `ValidateReadinessGate`. Confirms the response's `Card.Status` still
  literally shows the stale projected `"READY"` (display-only, never authority) while `Readiness.Status` is
  the fresh `"BACKLOG"`, `Readiness.Ready` is `false`, `Readiness.Problems` is non-empty, and — the sharpest
  assertion — `ValidActions` is completely empty: `markWorkItemReady` is never advertised off the stale
  projected claim. This is the concrete, tested meaning of this task's own "Hoàn thành khi: ... cannot create
  false action authority."

### Kết quả

New `internal/delivery/httpapi/kanban` subpackage: two read-only routes
(`GET /projects/{projectId}/work-items/kanban`, `GET /work-items/{workItemId}/detail`), wired into
`cmd/aw/serve.go`'s composition root. Kanban reads projected data exclusively through
`ports.ProjectionRepository` (`tx.Projections()`) and cross-references `tx.Work()` only for family/
WorkspaceSet/RepositoryWorkspace badge data — never a runtime table directly, satisfying this task's own
"Hoàn thành khi" bar's first half. The second half — "cannot create false action authority" — is enforced by
construction (`ExplainWorkItemReadiness` called fresh, in its own transaction, strictly after the projected
card is built, never fed anything the projection claims) and proven directly by the action-race test above,
the single most load-bearing test in this task. All 5 of the spec's own Verify bullets covered, plus 3
additional tests (route-inventory closure, missing-row fallback, unknown-WorkItem leakage-normalization). 8/8
new tests passing; `go build/vet/test ./...` clean across the entire repository.
