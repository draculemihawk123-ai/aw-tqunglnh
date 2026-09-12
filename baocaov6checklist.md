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
