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
