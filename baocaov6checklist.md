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
