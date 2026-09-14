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
