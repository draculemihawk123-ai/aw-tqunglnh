# V6 — Local HTTP API, SSE, projections và operator CLI

> Entry: V5 core journey pass.
>
> Exit: browser client và terminal `aw` có stable command/query contract trên cùng application
> authority, projections rebuild được; HTTP/CLI handler không spawn Git/provider hay truy cập SQLite
> ngoài application ports. Hoàn tất V6-15K là mốc sản phẩm dùng được bằng terminal mà chưa cần UI.

> Phạm vi task mặc định: kế thừa mục 3 của `00-roadmap.md`.

## V6-00 — UX artifact framework-neutral

- **Mục tiêu:** 13 screen của `01-system-design.md` §11 có đặc tả hành vi trước khi API bị freeze, để
  V6-12 không khóa một contract thiếu dữ liệu mà UI cần. Task này không chọn framework.
- **Phụ thuộc:** V5.
- **Phạm vi:** artifact tài liệu trong `docs/design/`; không viết code UI.
- **Thực hiện:** low-fidelity wireframe từng screen; ma trận screen × action/query × state; đặc tả
  loading/empty/stale/error/blocked; hành vi keyboard/accessibility; và một đặc tả riêng cho
  Graph/Timeline gồm fork/join/rework/checkpoint, selection synchronization, filters, large-graph
  behavior và accessible list fallback. Mỗi ô ghi proposed HTTP operationId và `aw` leaf command; đây
  là đầu vào của parity inventory ADR-028, không phải nơi tự tạo domain command mới.
- **Verify:** mỗi action/query trong ma trận map tới đúng một named public application operation, một
  endpoint và một `aw` command hoặc được ghi là còn thiếu; danh sách “còn thiếu” phải rỗng trước khi
  V6-12 chạy. Bootstrap/static asset là ngoại lệ duy nhất; Kanban không có generic status setter.
- **Hoàn thành khi:** V7 có đủ đặc tả hành vi để implement mà không phải tự quyết interaction, và mọi dữ
  liệu screen cần đều có endpoint tương ứng.
- **Nguồn:** ADR-010, ADR-018, ADR-028.

## V6-00A — Domain-event catalog closure

> Draft 2026-09-12, tiếng Anh nguyên văn theo user — sẽ chuẩn hoá sang tiếng Việt khi user bổ sung hoàn
> chỉnh phần còn lại của bản rewrite V6.

- **Mục tiêu:** mọi event đã persist từ V1…V5 đều decode/classify được trước khi projection đọc journal.
- **Phụ thuộc:** V5, V1-07A.
- **Phạm vi:** typed constants, decoder/upcaster, scope metadata, golden fixtures và CI inventory guard.
- **Không làm:** không đổi business transition, sửa raw history hoặc viết projector.
- **Thực hiện:** tạo machine-readable catalog `(EventType, SchemaVersion)` cho catalog, definition,
  message, work/scope, workspace/release/recovery, runtime/completion/checkpoint, evidence và ReleaseSet.
  Append boundary reject event chưa đăng ký. Mỗi emitter mới phải cung cấp decoder và golden trong chính
  task sinh event; installation/project scope được ghi rõ.
- **Verify:** `go test ./internal/app/eventschema/...`; replay toàn bộ golden; emitted-key inventory bằng
  registered-key inventory; unknown version fail-closed; old DB fixture vẫn decode sau restart.
- **Hoàn thành khi:** không còn historical event hợp lệ nhưng thiếu decoder/golden/scope classification.
- **Nguồn:** GC-DS-11, AK-ARCH-022.

## V6-01 — HTTP lifecycle, health và route registration

- **Mục tiêu:** production loopback HTTP server có lifecycle, health, bounded decode/error và registration
  hook để endpoint task không sửa router chung.
- **Phụ thuộc:** V5.
- **Phạm vi:** composition HTTP, bootstrap/static tối thiểu, `/health/live`, `/health/ready`, correlation,
  graceful shutdown, strict JSON/body limit, panic recovery và route registration primitive.
- **Không làm:** không implement business endpoint, browser security token hoặc receipt store.
- **Thực hiện:** `live` chỉ chứng minh process/event loop còn phục vụ; `ready` gọi installation-scoped
  health query và chỉ pass sau config, migration, UnitOfWork, artifact root và route composition sẵn sàng.
  Route fragment mang `{Method, Path, OperationID, ScopeKind, RequestSchema, ResponseSchema, Handler}` và
  reject duplicate/missing metadata ngay khi register.
- **Verify:** `go test ./internal/delivery/httpapi/...`; malformed/oversized/cancel/panic/shutdown; live vẫn
  pass khi dependency degraded; ready fail typed trước readiness và pass sau startup; descriptor trùng fail.
- **Hoàn thành khi:** server/health chạy production và task endpoint có thể thêm fragment riêng.
- **Nguồn:** ADR-016, ADR-025, ADR-028.

## V6-01A — Local-browser HTTP security và principal snapshot

- **Mục tiêu:** browser mutation chỉ đến từ bootstrap loopback hợp lệ và actor/roles không do caller khai.
- **Phụ thuộc:** V6-01.
- **Phạm vi:** bind/Host/Origin/CORS, per-start token, CSP/no-store bootstrap và `LocalPrincipalSnapshot`.
- **Không làm:** không durable-persist token, nhận actor/role từ body/header hoặc authorize bằng projection.
- **Thực hiện:** reject external bind; validate exact loopback Host/port và Origin; CORS deny default. Sinh
  token mỗi start, bind in-memory với principal từ trusted startup config, inject chỉ vào bootstrap HTML
  no-store/CSP; cấm URL/log/browser storage/artifact. Principal config đổi chỉ có hiệu lực sau restart.
- **Verify:** external bind, DNS rebinding, foreign/missing Origin, missing/wrong token, actor/role spoof,
  role downgrade sau restart và secret scan trên log/DB/bootstrap cache.
- **Hoàn thành khi:** caller không thể tự chọn identity và mutation thiếu browser proof bị chặn trước dispatch.
- **Nguồn:** ADR-016, ADR-025.

## V6-02 — HTTP CommandEnvelope, idempotency và optimistic concurrency

- **Mục tiêu:** mọi HTTP mutation dùng application receipt và concurrency contract duy nhất.
- **Phụ thuộc:** V6-01A, V1-06.
- **Phạm vi:** header-to-command mapping, canonical semantic hash, receipt replay, ETag/`If-Match`.
- **Không làm:** middleware không ghi/cache receipt, chạy business validation hoặc expose legacy mutation
  thiếu `CommandEnvelope`.
- **Thực hiện:** `Idempotency-Key` bắt buộc; update bắt buộc strong `If-Match`. Semantic hash gồm command
  type, scope/target, normalized payload, exact content digest và expected version; loại JSON formatting,
  request/correlation ID, session token và transport metadata. Flow: authenticate/authorize → canonical
  decode → receipt lookup → replay/conflict → nếu absent mới kiểm current version/external prework/dispatch;
  command transaction recheck receipt. Same-key committed replay thắng ETag/state drift nhưng vẫn phải qua
  current authorization. HTTP deterministically encode stored application result/ETag/operation reference.
- **Verify:** `go test ./internal/delivery/httpapi/... ./internal/app/...`; reordered JSON same hash; same-key
  replay sau restart và crash-after-commit; different body conflict trước I/O; stale key mới; concurrent
  keys; HTTP↔`aw` same-key result; architecture test delivery không ghi receipt/repository transaction.
- **Hoàn thành khi:** retry không duplicate aggregate/job/external operation và không có transport receipt authority.
- **Nguồn:** AK-ARCH-008, ADR-025, ADR-028.

## V6-02A — Shared HTTP DTO, cursor và schema-fragment contract

- **Mục tiêu:** endpoint song song dùng cùng error/query/action/stream vocabulary và tự cung cấp schema fragment.
- **Phụ thuộc:** V6-01.
- **Phạm vi:** error envelope, page/limit, opaque cursor, `Freshness`, `ValidAction`, range/media và SSE envelope.
- **Không làm:** không compose root router/OpenAPI và không định nghĩa domain transition.
- **Thực hiện:** cursor bind project, query/filter/sort, projection generation, upper watermark và last key;
  mismatch/swap trả typed resync. `ValidAction` chứa operationId/scope/targetVersion nhưng chỉ advisory.
  Normalize unauthorized/not-found theo leakage policy. Mỗi route task sở hữu descriptor/schema/golden và
  isolated-router test; V6-12 chỉ aggregate/reverse-check.
- **Verify:** round-trip/tamper cursor; bounded defaults/max; stable paging qua write; generation swap resync;
  duplicate operationId/schema omission fail; shared error/freshness/SSE golden.
- **Hoàn thành khi:** endpoint task không phải tự quyết DTO/cursor/error/action convention.
- **Nguồn:** ADR-028, HE-04-M07, AK-ARCH-023.

## V6-03 — Public Project authority

- **Mục tiêu:** bổ sung public `CreateProject`, `ListProjects`, `GetProject` còn thiếu trước HTTP/CLI.
- **Phụ thuộc:** V6-00A, V1-06, V1-07A.
- **Phạm vi:** installation-scoped create/list và authoritative project detail query.
- **Không làm:** không để delivery gọi `Tx.Catalog().CreateProject` hoặc tạo Component thủ công.
- **Thực hiện:** `CreateProject` recheck receipt rồi atomically create Project, append registered
  `PROJECT_CREATED` v1 và store result/receipt; first execution generates ID, replay returns exact ID.
  List/Get apply installation/project scope from ADR-025.
- **Verify:** command replay/different hash/concurrency/crash-after-commit, event golden, list/get scope.
- **Hoàn thành khi:** clean installation tạo Project qua một named public application authority.
- **Nguồn:** ADR-025, ADR-028, GC-DS-11, ROADMAP-§2.

## V6-03A — Project, repository và component HTTP endpoints

- **Mục tiêu:** expose Project catalog, repository onboarding và discovered component/pack assignment.
- **Phụ thuộc:** V6-00, V6-01A, V6-02, V6-02A, V6-03.
- **Phạm vi:** project create/list/detail; repository register/list/detail/onboarding/probe-history/retry;
  component query và exact pack assignment.
- **Không làm:** không duplicate Doctor, expose helper `CreateComponent`, hoặc giả sync success trước probe.
- **Thực hiện:** register trả `REGISTERING`; retry chỉ map `RetryRepositoryProbe` khi `BLOCKED` và leaf/route
  canonical dùng `retry-probe`. Component chỉ đọc topology do probe discover; assignment pin exact version.
- **Verify:** isolated route/schema goldens; async state/retry/replay/scope/redaction; handler dispatch spy.
- **Hoàn thành khi:** catalog/onboarding có một route owner và repository ID là authority.
- **Nguồn:** ADR-019, ADR-025, ADR-028, ROADMAP-§2.

## V6-04 — WorkItem, family, readiness và scope endpoints

> ⚠️ Overlap tạm thời với V6-06 (chưa được rewrite): `WithdrawScopeExpansion` hiện được ghi ở CẢ task này
> lẫn V6-06 phía dưới. Giữ nguyên cho tới khi user bổ sung bản rewrite V6-05 trở đi rồi hợp nhất lại một
> chỗ duy nhất — không tự ý xoá bên nào trước khi có xác nhận.

- **Mục tiêu:** expose root/child WorkItem, family/readiness và toàn bộ scope-expansion lifecycle.
- **Phụ thuộc:** V6-00, V6-01A, V6-02, V6-02A, V6-03.
- **Phạm vi:** create/list/authoritative detail/child/readiness; scope request/approve/reject/withdraw.
- **Không làm:** không generic status/family/workspace setter; không dùng projected detail để authorize.
- **Thực hiện:** root-create atomic, child subset, readiness criteria explanation; `WithdrawScopeExpansion`
  thuộc task này, chỉ khi pending và không tạo grant/amendment. Authoritative detail phân biệt với projected
  card/detail của V6-10.
- **Verify:** multi-repo/subset/scope negative matrix, concurrent decisions, withdrawal race, no direct DONE.
- **Hoàn thành khi:** mọi WorkItem/scope control gọi named application command và client không set state.
- **Nguồn:** ADR-019, ADR-020, HE-08-M03.

## V6-04A — Public `MarkWorkItemReady` authority

- **Mục tiêu:** đóng đúng gap `BACKLOG → READY` cho API/UI/CLI mà không tạo generic status setter.
- **Phụ thuộc:** V6-04, V3-03, V1-06, V1-07A.
- **Thực hiện:** thêm project-scoped command `MarkWorkItemReady` trong application layer. Command reload
  WorkItem, chạy lại canonical readiness validator, rồi trong cùng serialized transaction CAS duy nhất
  `BACKLOG → READY`, append registered `WORK_ITEM_MARKED_READY` schema v1 và lưu command receipt. Task
  đăng ký decoder + golden fixture ngay theo GC-DS-11. Request không có `targetStatus`;
  `POST /work-items/{id}/mark-ready` chỉ dispatch command này với CommandEnvelope/If-Match.
- **Verify:** same-key replay; concurrent same-key và different-key; stale expected version; criterion
  thiếu; cross-project ID; payload có target status; registered-event round trip. Chỉ same-key replay
  stored success; key mới khi đã `READY` trả typed `CONFLICT`. Assert đúng một successful transition/event,
  mỗi key tối đa một receipt và loser concurrency không append event.
- **Hoàn thành khi:** Kanban/API/CLI chỉ có narrow valid action `mark-ready`; không tồn tại
  `set-status`/`TransitionWorkItemStatus` public.
- **Nguồn:** ADR-028, HE-01-M02, HE-08-M03, GC-DS-11.

## V6-05 — Definition validate/publish endpoints

- **Mục tiêu:** expose trọn vòng đời authoring V2 bằng text/file payload có bounded size.
- **Phụ thuộc:** V6-02.
- **Thực hiện:** create Definition `DRAFT`, strict content type/size, validate-only response có
  location, publish command trả SourceHash/CompiledSnapshotHash/exact pins và definition/version
  list/detail/diff queries. Route global nằm dưới `/definitions/{kind}...`; route project-scoped mirror
  dưới `/projects/{projectId}/definitions/{kind}...`. Collection create/list derive scope từ route;
  item/publish/version operation reload Definition rồi cross-check `CommandScope` theo ADR-028, không
  tin scope tùy ý trong payload.
- **Verify:** location diagnostics, duplicate publish, version list/diff contracts; negative scope
  matrix: global route từ chối project Definition/version; project route từ chối global target hoặc
  target của Project khác; hai operand của diff phải cùng scope.
- **Hoàn thành khi:** API không lưu invalid draft thành runtime version và UI có thể
  create→validate→publish→inspect/diff mà không cần seed Definition qua SQLite/CLI.
- **Nguồn:** AK-ARCH-001, ADR-028.

## V6-06 — Run/approval/signal/cancel endpoints

- **Mục tiêu:** start run, graph query, approve/reject, typed WAIT signal, cancel và diagnostics.
- **Phụ thuộc:** V6-04, V6-05.
- **Thực hiện:** route/DTO cho typed application commands, idempotency/If-Match, valid-action query và
  DecisionArtifact references; free-text message không được map thành approval/signal/cancel. `cancel`
  trả trạng thái `CANCELLING` chứ không giả lập `CANCELLED` ngay; thêm typed `WithdrawScopeExpansion`
  chỉ hợp lệ khi request đang `PENDING`, idempotent và không tạo grant/amendment. `WithdrawScopeExpansion`
  là public application command trong Go core §8 — route này chỉ dispatch nó, không tự định nghĩa một
  command ngoài core contract.
- **Verify:** invalid state/pinned version/duplicate command contracts; cancel hai lần; withdraw sau khi
  đã approve bị từ chối; assert response cancel không bao giờ trả `CANCELLED` tức thì.
- **Hoàn thành khi:** handler chỉ dispatch typed application command.
- **Nguồn:** ADR-020.

## V6-06A — Run detail, timeline và diagnostics endpoints

- **Mục tiêu:** đóng dữ liệu mà screen Graph/Timeline và Run diagnostics cần; thiếu task này V7-12 và
  V7-16 không có API.
- **Phụ thuộc:** V6-06, V6-00.
- **Thực hiện:** `GET /runs/{id}` detail/state/manifest revision; `GET /runs/{id}/timeline` với stable
  cursor, node/attempt/route/retry/checkpoint, correlation/causation và JournalPosition/freshness;
  `GET /runs/{id}/diagnostics` trả job/lease/fence/provider/workspace state cùng valid recovery actions.
- **Verify:** pagination bằng cursor ổn định qua ghi mới; bounded response; contract test khẳng định
  response không chứa PID, argv, cwd hay secret; fork/join/rework fixture cho timeline.
- **Hoàn thành khi:** hai screen tương ứng không cần dữ liệu nào ngoài các endpoint này.
- **Nguồn:** ADR-018, AK-ARCH-025, HE-11-M02, HE-11-M03, HE-11-M04.

## V6-06B — Recovery command endpoints

- **Mục tiêu:** ba recovery command của ADR-020 có transport; thiếu task này chúng tồn tại trong core và
  UI nhưng không có đường gọi.
- **Phụ thuộc:** V6-06, V4-12C, V5-08D.
- **Thực hiện:** `POST /node-runs/{id}/retry-blocked-activation` → `RetryBlockedActivation`;
  `POST /work-items/{id}/cancel` → `CancelWorkItem`; `POST /blockers/{id}/resolve` →
  `ResolveWorkItemBlocker`. Mỗi route chỉ dispatch đúng một typed application command với
  idempotency/If-Match; valid-action query trả đúng ba action này khi precondition thỏa.
- **Verify:** contract test cho precondition fail của từng command: `RetryBlockedActivation` trên Run
  `CANCELLING` hoặc đã terminal; `ResolveWorkItemBlocker` khi còn Run nonterminal hoặc workspace
  `QUARANTINED`. Blocker đang `OPEN` là trường hợp **hợp lệ** — command này chính là thứ resolve nó — còn
  blocker đã `RESOLVED|WAIVED` trả no-op idempotent, không phải lỗi. Thêm test payload thiếu resolution
  mode bị reject, và `WAIVED` trên một admission reason bị từ chối. Retry lặp không sinh chuỗi blocked
  activation; `CancelWorkItem` gặp active Run trả trạng thái quiesce chứ không terminal ngay.
- **Hoàn thành khi:** mọi Attempt `BLOCKED` và WorkItem `BLOCKED` đều có ít nhất một valid action gọi
  được từ API.
- **Nguồn:** ADR-020.

## V6-07 — Conversation/evidence/artifact endpoints

- **Mục tiêu:** append/list message, upload verified attachment, list evidence và stream artifact.
- **Phụ thuộc:** V6-02.
- **Thực hiện:** public application use case `AppendConversationAttachment` upload artifact qua
  ArtifactStore ngoài DB transaction rồi append verified reference bằng canonical message command;
  orphan cleanup/idempotency phải tường minh. Message query kèm bounded context-used metadata/
  ContextSnapshot reference mà V7-15 cần; evidence query kèm artifact inventory/reference mà V7-14
  dùng với authorized query `GetArtifactContent`; range/size/media headers, sensitivity policy, no
  trusted HTML, content hash ETag.
- **Verify:** traversal/unauthorized-shaped ID/tamper/large stream/redaction tests.
- **Hoàn thành khi:** raw filesystem locator không xuất API.
- **Nguồn:** AK-ARCH-021, HE-11-M07.

## V6-08 — Projection event consumer

- **Mục tiêu:** idempotently build Kanban và task detail read models theo JournalPosition.
- **Phụ thuộc:** V1-07A, V6-04A.
- **Thực hiện:** consumer quét global monotonic/non-gapless JournalPosition rồi lọc Project; aggregate
  sequence vẫn là concurrency token riêng. Vị trí của project khác không phải gap; detect missing/
  corrupt referenced record, aggregate sequence violation, duplicate/out-of-order/poison và freshness.
  Consume `WORK_ITEM_MARKED_READY` v1 để Kanban/readiness projection phản ánh transition đã commit;
  unknown/unregistered version fail-closed thay vì đoán payload.
- **Verify:** replay twice, stop/restart, gap and poison event tests.
- **Hoàn thành khi:** runtime không query projection cho readiness/completion; poison event đưa
  projection sang `DEGRADED/STALE`, không nhảy cursor.
- **Nguồn:** AK-ARCH-022, AK-ARCH-023, HE-12-M08, GC-DS-11.

## V6-09 — Projection rebuild command

- **Mục tiêu:** xóa/rebuild read model cho cùng result tại JournalPosition mà không giả event journal
  là full event-sourced authority.
- **Phụ thuộc:** V6-08.
- **Thực hiện:** public project-scoped `RequestProjectionRebuild` ghi intent + enqueue job; expose
  `GET /projects/{id}/projection` và `POST /projects/{id}/projection/rebuild`. Worker snapshot
  authoritative tables tại watermark, build shadow model rồi replay event có JournalPosition sau
  watermark trước atomic swap; `GET /projects/{id}/projection-rebuilds/{operationId}` trả progress/
  status/error của đúng operation để `--wait` không quan sát nhầm một rebuild mới hơn. HTTP/CLI không
  được gọi internal rebuild executor hoặc sửa cursor/read-model row trực tiếp.
- **Verify:** before/after canonical projection diff và interrupted rebuild recovery.
- **Hoàn thành khi:** authoritative tables/events không bị sửa.
- **Nguồn:** AK-ARCH-022, ADR-028.

## V6-10 — Kanban/task detail query endpoints

- **Mục tiêu:** project-level views, repository/component filters và freshness.
- **Phụ thuộc:** V6-08, V6-04A.
- **Thực hiện:** bounded pagination/sort/filter DTO, repository badge aggregation, blocker/valid-action
  summary và projection JournalPosition/status trong response. Named valid action `mark-ready` chỉ xuất
  khi authoritative readiness query hiện tại pass và WorkItem đang `BACKLOG`; response map đúng
  `MarkWorkItemReady`, không phát generic target-status action.
- **Verify:** pagination/filter/sort/stale projection contracts.
- **Hoàn thành khi:** task multi-repo vẫn là một card với repository badges.
- **Nguồn:** ROADMAP-§2, AK-ARCH-023, HE-08-M05, ADR-028.

> V6-10A…V6-10G thay cho một umbrella task duy nhất: mỗi task dưới đây là một contract suite verify
> được độc lập, theo nguyên tắc kích thước task ở `00-roadmap.md` §3.
>
> **Có thể song song:** {V6-10A, V6-10E, V6-10F} không phụ thuộc lẫn nhau; sau khi V6-10B xong thì
> {V6-10C, V6-10D} cũng có thể chạy song song. V6-10B phải xong trước hai task đó vì cả hai đọc
> workspace/lease contract của nó. V6-10G chạy tuần tự sau V6-10F vì harden chính hai handler adapter
> build mà V6-10F expose.
>
> Điều kiện để song song an toàn: mọi endpoint hiện đều nằm trong `internal/delivery/httpapi`, nên mỗi
> task sở hữu **file handler riêng** (`doctor.go`, `workspace.go`, `source.go`, `release.go`,
> `settings.go`, `adapterbuild.go`) và **không** task nào tự sửa route table dùng chung. Việc nối route
> vào router là bước tuần tự của V6-12; task song song chỉ đăng ký handler qua registration hook.

## V6-10A — Doctor và repository onboarding endpoints

- **Mục tiêu:** first-run screen và onboarding retry có API typed.
- **Phụ thuộc:** V6-02, V6-03.
- **Thực hiện:** Doctor trả DB/roots/Git/provider/adapter/isolation readiness kèm remediation; repository
  onboarding status/probe history; typed retry từ `BLOCKED`.
- **Verify:** healthy/degraded/blocked golden; retry idempotency; assert không lộ credential.
- **Hoàn thành khi:** V7-05 và V7-06 không cần đọc filesystem hay config trực tiếp.
- **Nguồn:** ADR-019.

## V6-10B — Workspace state, lease và reconcile endpoints

- **Mục tiêu:** expose WorkspaceSet/lease/quarantine và typed recovery operation.
- **Phụ thuộc:** V6-02, V6-10A.
- **Thực hiện:** workspace/repository-workspace state, revision, lease holder/fence, quarantine flag và
  valid actions; `POST /workspace-sets/{id}/release` dispatch public `RequestWorkspaceSetRelease` (không
  gọi internal `ExecuteWorkspaceSetRelease`); `POST /repository-workspaces/{id}/reconcile` dispatch public
  `RequestWorkspaceReconciliation` (ghi intent + enqueue job) chứ không gọi internal
  `ExecuteWorkspaceReconciliation` — API không được expose command internal của worker.
- **Verify:** quarantined workspace từ chối cấp writer; reconcile idempotent; stale generation contract;
  architecture test khẳng định handler không tham chiếu command internal nào.
- **Hoàn thành khi:** recovery action thực hiện được từ API, không cần sửa Git thủ công.
- **Nguồn:** AK-ARCH-009.

## V6-10C — Bounded read-only source, diff và log endpoints

- **Mục tiêu:** đọc code/diff/log tại exact revision với giới hạn rõ, không mở đường thực thi.
- **Phụ thuộc:** V6-10B.
- **Thực hiện:** `GET /repository-workspaces/{id}/source|diff|log` pin exact revision, bounded output,
  truncation indicator, binary/large handling.
- **Verify:** traversal, revision không khớp, file nhị phân/lớn, output bound; endpoint terminal trả
  not-found.
- **Hoàn thành khi:** không có endpoint nào cho phép ghi hoặc chạy lệnh trong workspace.
- **Nguồn:** ADR-018.

## V6-10D — ReleaseSet và local Git endpoints

- **Mục tiêu:** expose ReleaseSet local và local commit, từ chối mọi remote mutation.
- **Phụ thuộc:** V6-10B.
- **Thực hiện:** list/create và `GET /release-sets/{id}` detail; seal/abandon ReleaseSet;
  `POST /release-sets/{id}/entries/{repositoryId}/local-commit`; per-repository verdict/partial state.
- **Verify:** duplicate seal, stale revision, partial result; spy adapter chứng minh remote mutation call
  count bằng 0; assert không route nào cho push/PR/merge/force-push.
- **Hoàn thành khi:** release decision thực hiện được từ API và remote Git vẫn không có executor.
- **Nguồn:** ADR-014, AK-ARCH-015C.

## V6-10E — Safe settings endpoints

- **Mục tiêu:** sửa được cấu hình không nhạy cảm mà không mở đường rò secret.
- **Phụ thuộc:** V6-02.
- **Thực hiện:** `GET/PUT /settings/safe` với allowlist tường minh, version validation, secret chỉ nhập
  bằng reference; giá trị secret không bao giờ round-trip. `LocalPrincipalSnapshot`/actor/roles không
  thuộc allowlist runtime này và chỉ đổi qua trusted startup config + restart.
- **Verify:** field ngoài allowlist bị từ chối; stale version conflict; assert response không chứa giá
  trị secret.
- **Hoàn thành khi:** setting cần restart được báo rõ thay vì áp dụng im lặng.
- **Nguồn:** ADR-016, ADR-017, ADR-028.

## V6-10F — Adapter build registry endpoints

- **Mục tiêu:** đưa flow probe/register của ADR-022 lên API để Doctor UI dùng được.
- **Phụ thuộc:** V2-07A, V2-07B, V6-02.
- **Thực hiện:** `GET /adapter-builds`, `GET /adapter-builds/{id}`, `POST /adapter-builds/probe` (không
  mutate registry/domain aggregate, trả candidate token có ký/expiry) và `POST /adapter-builds` (re-probe ngoài transaction, đối
  chiếu token rồi đăng ký immutable build). Theo ADR-025 đây là route installation-scoped: không nhận
  ProjectID và không nằm dưới `/projects/{id}`.
- **Verify:** probe không ghi registry; register idempotent theo fingerprint; token hết hạn/sai chữ ký/
  executable đổi/protocol, capability-hash hoặc OS-config mismatch đều bị reject; token sống qua process
  restart; xoay signing key làm token cũ mất hiệu lực; assert run đang chạy không bị repin; assert registry không
  lộ ra như một DefinitionKind; assert route từ chối ProjectID.
- **Hoàn thành khi:** nâng cấp provider CLI xử lý được hoàn toàn qua API/UI.
- **Nguồn:** ADR-022.

## V6-10G — Adapter-build command envelope và receipt hardening

- **Mục tiêu:** đưa `ProbeAdapterBuild`/`RegisterAdapterBuild` cũ vào command contract installation-scoped
  trước khi HTTP/CLI parity dựa vào chúng; fingerprint-idempotency không thay thế command idempotency.
- **Phụ thuộc:** V6-10F, V1-06, V1-07A, V2-07B.
- **Thực hiện:** cả hai public operation nhận `CommandEnvelope` với `scope_key=installation` và dùng
  command receipt. Cả hai lookup receipt + request hash trước external I/O để replay đã commit không bị
  kết quả môi trường hiện tại làm thay đổi; nếu chưa có receipt mới tiếp tục. Probe/re-probe/hash/process/file I/O luôn ở ngoài transaction; transaction chỉ
  bootstrap/read signing material theo contract, persist registry/event cần thiết và receipt. Probe
  same-key replay trả **đúng candidate token đã lưu**, kể cả token nay đã hết hạn; caller muốn token mới
  phải dùng key mới. Register kiểm receipt trước, nếu chưa có mới re-probe ngoài transaction rồi atomically
  insert-if-absent + receipt; khi build mới thật sự được insert, append registered
  `ADAPTER_BUILD_REGISTERED` v1 trong cùng transaction. Probe không emit domain event vì không đổi domain
  aggregate; lazy signing-key bootstrap chỉ là infrastructure state. `RegisteredBy` lấy từ
  `Command.Actor`, không nhận từ payload. Task đăng ký decoder/golden cho event mới theo GC-DS-11.
- **Verify:** same-key same-hash replay, same-key different-hash conflict, concurrent duplicate cho cả
  probe/register, expired replay không tự gia hạn token, new-key probe phát token mới, process crash sau
  commit trước response, installation scope không có ProjectID; architecture test cấm filesystem/process
  trong transaction; assert đúng một build/event khi hai key cùng fingerprint chạy concurrent, mỗi key
  tối đa một receipt và registered-event golden round trip.
- **Hoàn thành khi:** HTTP và `aw` dùng chung hai handler có receipt semantics, không còn đường legacy
  gọi use case thiếu CommandEnvelope.
- **Nguồn:** ADR-022, ADR-025, GC-INV-35, GC-DS-11.

## V6-11 — SSE event stream

- **Mục tiêu:** UI nhận invalidation/runtime summary và reconnect theo event ID.
- **Phụ thuộc:** V6-08.
- **Thực hiện:** public streaming query `WatchProjectEvents`; project filter, heartbeat, bounded client
  buffer, slow-client disconnect, redacted payload; Last-Event-ID là JournalPosition, cursor quá cũ
  trả typed resync action.
- **Verify:** reconnect/duplicate/lag/slow consumer/shutdown tests.
- **Hoàn thành khi:** SSE không chứa artifact/log lớn hoặc secret fixture.
- **Nguồn:** AK-ARCH-023, HE-11-M07.

## V6-12 — Machine-readable API contract

- **Mục tiêu:** version schema cho DTO/error/routes để UI không dựa implementation detail.
- **Phụ thuộc:** V6-00, V6-03…V6-11, V6-04A, V6-06A, V6-06B và V6-10A…V6-10G. V6-00 phải hoàn tất trước task này: API
  không được freeze khi ma trận screen/action còn mục “thiếu endpoint”.
- **Thực hiện:** compose router tuần tự từ các handler đã đăng ký qua registration interface của V6-01 —
  đây là bước wiring duy nhất và không chạy song song; generate/maintain OpenAPI hoặc equivalent checked
  artifact từ authoritative route schemas; mọi leaf operation có stable unique `operationId`;
  compatibility diff gate. Khởi tạo parity inventory ADR-028 từ ma trận V6-00 với bốn cột
  `UI action/query ↔ operationId ↔ aw command ↔ public application operation` để V6-15K kiểm.
- **Verify:** schema validation của golden requests/responses; breaking diff fail; **route inventory
  test** assert tập route mà router thực sự phục vụ khớp đúng OpenAPI — không route thiếu, không route
  trùng path+method, không handler đăng ký mà không được nối.
- **Hoàn thành khi:** every public endpoint/example/error code documented, và router khớp OpenAPI theo
  cả hai chiều.
- **Nguồn:** HE-04-M07, ADR-028.

## V6-13 — API security and boundary tests

- **Mục tiêu:** chứng minh HTTP process không có execution fast path.
- **Phụ thuộc:** V6-12.
- **Thực hiện:** import/architecture test, loopback-only bind, Host/Origin/session-token enforcement,
  CORS deny default, path/content injection, request cancellation và artifact MIME safety.
- **Verify:** automated security/boundary suite.
- **Hoàn thành khi:** no handler imports Git/process/provider/SQLite concrete package.
- **Nguồn:** AK-ARCH-018, AK-ARCH-025A, AK-ARCH-027, GC-INV-14, HE-10-M04.

## V6-14 — API/projection acceptance gate

- **Mục tiêu:** vận hành core journey chỉ qua HTTP và quan sát qua projection/SSE.
- **Phụ thuộc:** V6-00…V6-13 và V6-04A.
- **Thực hiện:** doctor, adapter probe/register, async repo onboarding, create definition/task/run/
  message+attachment/approval/wait signal; cancel một run và quan sát `CANCELLING → CANCELLED`; inspect
  source/diff/log/timeline/diagnostics/evidence/ReleaseSet; local commit; await SSE; rebuild projection.
- **Verify:** black-box test from clean DB, full/race/Windows/Linux.
- **Hoàn thành khi:** không có test setup sửa SQLite/Git runtime state thủ công.
- **Nguồn:** AK-ARCH-018, AK-ARCH-023.

## V6-15A — Canonical `aw` executable và CLI foundation

- **Mục tiêu:** đổi production executable từ tên bootstrap `agentkit` sang canonical `aw`, đồng thời tạo
  delivery boundary đủ sạch để các nhóm leaf triển khai độc lập.
- **Phụ thuộc:** V6-14, V2-07B, V2-11.
- **Phạm vi:** migrate composition root `cmd/agentkit` → `cmd/aw`, build/package/help references và
  production invocation. Đây là breaking rename trước release, không duy trì alias `agentkit`. Giữ
  nguyên `cmd/agentkit-spike` cùng mọi V0 evidence command.
- **Thực hiện:** grammar `aw <resource> <action> [flags]`, cộng `aw serve|worker|version|help`. Refactor
  mọi legacy leaf hiện nằm trong `cmd/agentkit` sang `internal/delivery/cli` nhận application/local-utility
  interfaces được inject; việc move legacy definition/adapter/evidence ở đây chỉ bảo toàn behavior,
  canonical descriptor/semantics thuộc các task sau. `cmd/aw` là composition root duy nhất được import
  concrete adapters để wiring; handler package không được làm vậy. Tạo registration hook và descriptor
  cho mỗi leaf gồm path, scope, app operation, HTTP operationId hoặc `CLI_LOCAL` reason; các task B…J sở
  hữu registry fragment riêng. Cùng `LocalPrincipalSnapshot` với HTTP điền Actor/ActorRoles; không có
  `--actor`/`--role`. Chuẩn hóa `--project-id`, `--expected-version`, correlation, bounded file/stdin,
  typed exits và optional `--idempotency-key` tự sinh trước dispatch rồi luôn trả lại trong envelope.
  Descriptor ghi confirmation policy: command impact cao chỉ prompt trong TTY human mode;
  non-interactive/`--json` bắt buộc `--yes` và thiếu nó fail trước dispatch.
- **Verify:** build/package/install Windows/Linux; production path/name cũ không còn; spike binary vẫn
  độc lập. Architecture test áp chính xác lên `internal/delivery/cli`: không import SQLite/Git/process/
  provider concrete hay internal worker command; `cmd/aw` chỉ được reference chúng ở wiring code.
- **Hoàn thành khi:** `aw` khởi động đúng composition root, legacy behavior không mất và B…J có thể thêm
  handler/descriptor mà không sửa root registry.
- **Nguồn:** ADR-028, AK-ARCH-018, HE-04-M07.

> **Có thể song song:** sau V6-15A, {V6-15B…V6-15J} không phụ thuộc lẫn nhau ngoài dependency riêng ghi
> trong từng task. Mỗi task sở hữu handler/descriptor package và table-driven contract suite riêng;
> compose root registry/parity chỉ diễn ra tuần tự trong V6-15K.

## V6-15B — Bootstrap, settings và catalog CLI

- **Mục tiêu:** vận hành installation/project/repository catalog từ terminal.
- **Phụ thuộc:** V6-15A, V6-03, V6-10A, V6-10E.
- **Thực hiện:** `aw health live|ready`; `aw doctor`; `aw settings show|update`;
  `aw project list|create|show`; `aw repository list|register|onboarding|probe`; `aw component list`;
  `aw pack-assignment list|assign`. Component chỉ query topology do onboarding/probe discover. Principal
  config không xuất hiện trong safe-settings mutation. Mọi project-scoped leaf dùng `--project-id`.
- **Verify:** healthy/degraded/blocked, registration async state, retry probe, component discovery,
  exact PackVersion assignment, setting stale-version/restart-required, same-key replay và redaction.
- **Hoàn thành khi:** clean installation tạo được project/catalog mà không seed SQLite.
- **Nguồn:** ADR-019, ADR-025, ADR-028.

## V6-15C — Definition và adapter-build CLI

- **Mục tiêu:** vận hành definition plane và immutable adapter-build registry từ terminal.
- **Phụ thuộc:** V6-15A, V6-05, V6-10G.
- **Thực hiện:** `aw definition list|create|show|versions|validate|publish`;
  `aw definition-version show|diff`; `aw adapter list|show|probe|register`. Definition global bắt buộc
  `--scope global`, project definition bắt buộc `--project-id`; reload target để chặn cross-scope ID.
  Tách pre-release semantics V2 cũ: `definition list/show` thao tác Definition, còn `versions` và
  `definition-version show/diff` thao tác immutable version. Adapter commands dùng installation-scoped
  envelope/receipt của V6-10G, không gọi legacy use case thiếu envelope.
- **Verify:** global/project negative matrix, validate diagnostics, publish replay/diff, probe exact-token
  replay, candidate expiry/protocol/capability/OS-config mismatch và register drift/concurrency.
- **Hoàn thành khi:** operator bootstrap được toàn bộ exact definition/adapter pins bằng `aw`.
- **Nguồn:** ADR-022, ADR-025, ADR-028.

## V6-15D — WorkItem và blocker CLI

- **Mục tiêu:** quản lý task/family readiness và blocker bằng named authority.
- **Phụ thuộc:** V6-15A, V6-04A, V6-06B, V6-10.
- **Thực hiện:** `aw work-item list|show|create|create-child|readiness|mark-ready|cancel` và
  `aw blocker resolve`; mọi leaf project-scoped dùng `--project-id`. Không có `set-status`;
  `mark-ready` gọi đúng `MarkWorkItemReady`, còn cancel/resolve dùng lifecycle command chuyên biệt.
- **Verify:** scope subset/readiness errors, same-key/different-key concurrency, stale version,
  mark-ready event/projection, cancel quiesce và blocker resolution-mode matrix.
- **Hoàn thành khi:** mọi valid action của WorkItem/Kanban gọi được từ terminal mà client không tự set state.
- **Nguồn:** ADR-020, ADR-028, HE-08-M03.

## V6-15E — Run và recovery CLI

- **Mục tiêu:** start, inspect, cancel và recover runtime từ terminal.
- **Phụ thuộc:** V6-15A, V6-06A, V6-06B.
- **Thực hiện:** `aw run start|show|cancel|graph|timeline|diagnostics` và
  `aw node-run retry-blocked`; graph/timeline giữ stable cursor/freshness, diagnostics chỉ trả safe typed
  action. Async invocation dùng output/`--wait` contract chung và không chạy worker inline.
- **Verify:** start/cancel idempotency, pinned-version conflict, admission blocker retry, timeline paging,
  diagnostics redaction; timeout/Ctrl-C của `--wait` không dispatch bất kỳ cancellation command nào.
- **Hoàn thành khi:** Run lifecycle và recovery action gọi được bằng `aw`, không cần DB surgery.
- **Nguồn:** ADR-020, ADR-022, ADR-028.

## V6-15F — Scope, approval và WAIT decision CLI

- **Mục tiêu:** mọi human decision có typed terminal control riêng, không dùng free-text message.
- **Phụ thuộc:** V6-15A, V6-04, V6-06.
- **Thực hiện:** `aw scope-expansion request|approve|reject|withdraw`; `aw approval approve|reject`;
  `aw wait signal`. Approval lấy Actor/ActorRoles từ shared `LocalPrincipalSnapshot`, tuyệt đối không
  nhận `--actor`/`--role`; scope/wait payload vẫn vào RequestHash theo contract.
- **Verify:** HTTP/CLI cùng principal cho cùng authorization result; spoof role bị reject, unauthorized
  role không mutate; concurrent approval, duplicate signal identity, scope decision/withdraw races.
- **Hoàn thành khi:** approval/signal/scope controls đều có audit actor/role đúng và không lẫn chat.
- **Nguồn:** ADR-020, ADR-028, HE-08-M08.

## V6-15G — Conversation, evidence và artifact CLI

- **Mục tiêu:** append/inspect canonical conversation và evidence an toàn từ terminal.
- **Phụ thuộc:** V6-15A, V6-07.
- **Thực hiện:** `aw message list|append`; `aw attachment upload`; `aw evidence list|verify`;
  `aw artifact get`. Finite leaf có human/`--json`; `artifact get --output <path|->` stream raw bytes.
  `evidence verify` là offline `CLI_LOCAL`, đi qua injected local verifier interface chứ không cho
  handler import concrete filesystem adapter; các leaf khác map public application operation.
- **Verify:** attachment traversal/tamper/size/idempotency, context-used metadata, evidence artifact refs,
  exact artifact bytes/hash/range, stdout/stderr separation và secret redaction.
- **Hoàn thành khi:** evidence/artifact kiểm chứng được mà không lộ raw filesystem locator.
- **Nguồn:** ADR-028, AK-ARCH-021, HE-11-M07.

## V6-15H — Workspace và bounded source CLI

- **Mục tiêu:** quan sát và phục hồi workspace bằng typed local operations.
- **Phụ thuộc:** V6-15A, V6-10B, V6-10C.
- **Thực hiện:** `aw workspace-set show|release` và
  `aw repository-workspace source|diff|log|reconcile`. Release/reconcile chỉ dispatch public request
  command; source/diff/log pin revision, bounded và read-only. Không có interactive terminal.
- **Verify:** quarantine/lease/generation, reconcile replay, async release `--wait`, traversal/binary/
  truncation/stale revision và architecture test không gọi internal execute command.
- **Hoàn thành khi:** operator xử lý workspace không cần chạy Git hoặc sửa filesystem thủ công.
- **Nguồn:** ADR-018, ADR-028, AK-ARCH-009.

## V6-15I — ReleaseSet và local-commit CLI

- **Mục tiêu:** hoàn tất release local đa repository mà không mở remote mutation.
- **Phụ thuộc:** V6-15A, V6-10D.
- **Thực hiện:** `aw release-set list|create|show|seal|abandon|local-commit`; local-commit yêu cầu exact
  ReleaseSet/repository entry/revision và confirm phù hợp human mode. Không có push/PR/merge/force-push.
- **Verify:** duplicate seal, stale revision, partial per-repository result, same-key replay và spy
  chứng minh remote Git mutation call count bằng 0.
- **Hoàn thành khi:** mọi ReleaseSet/local-only Git action của UI có CLI tương ứng.
- **Nguồn:** ADR-014, ADR-028, AK-ARCH-015C.

## V6-15J — Projection và event-stream CLI

- **Mục tiêu:** quan sát freshness, yêu cầu rebuild và theo dõi event từ terminal.
- **Phụ thuộc:** V6-15A, V6-09, V6-11.
- **Thực hiện:** `aw projection status|rebuild|rebuild-status` và `aw events watch`; rebuild trả exact
  operation ID, `rebuild-status` không suy từ operation mới nhất. `events watch` phát NDJSON trên stdout,
  diagnostics/heartbeat trên stderr và reconnect bằng JournalPosition.
- **Verify:** rebuild replay/interruption, exact operation lookup, stale cursor typed resync, duplicate/
  slow consumer/shutdown và NDJSON parse liên tục.
- **Hoàn thành khi:** operator theo dõi/rebuild projection mà không chạm cursor/read-model row trực tiếp.
- **Nguồn:** ADR-028, AK-ARCH-022, AK-ARCH-023.

## V6-15K — UI/API/CLI parity và terminal acceptance gate

- **Mục tiêu:** chứng minh `aw` là surface vận hành đầy đủ của cùng application authority và là gate
  cuối V6 trước V7.
- **Phụ thuộc:** V6-15B…V6-15J.
- **Thực hiện:** compose CLI registry tuần tự. Checker bốn chiều đọc UX inventory V6-00, OpenAPI,
  application registry và CLI descriptors; fail khi thiếu/trùng/scope lệch. Ngoại lệ duy nhất được khai
  báo `CLI_LOCAL` là `{aw serve, aw worker, aw help, aw version, aw evidence verify}`; bootstrap/static
  browser assets không cần CLI. Reverse checker cấm leaf map tới `AdvanceRun`,
  `ExecuteWorkspaceReconciliation`, `ExecuteWorkspaceSetRelease`, job handler hoặc remote Git mutation.
  Với finite `--json`, không `--wait` phát đúng một accepted/result document; có `--wait` phát đúng một
  final document chứa idempotency key + operation reference + last state. Timeout/interrupt trả typed
  error document/exit và để durable operation tiếp tục chạy; success/error document ở stdout,
  diagnostics/progress chỉ ở stderr. Parity checker cũng kiểm confirmation metadata; `--json` hoặc
  non-interactive không được prompt và command impact cao thiếu `--yes` phải fail trước dispatch.
- **Verify:**
  1. mỗi UI action/query có đúng một public application operation, HTTP operationId và leaf `aw`;
     reverse checker cũng từ chối CLI leaf không có authority hoặc ngoại lệ typed;
  2. cùng semantic input và `LocalPrincipalSnapshot` qua HTTP/CLI tạo cùng authorization/domain result
     sau khi normalize duy nhất ID/timestamp/transport metadata được phép khác;
  3. khởi chạy `aw serve` và thêm `aw worker` như process nền dùng cùng config/DB, rồi chạy one-shot
     commands; chứng minh async jobs tiến triển, wait observer không chạy/hủy job, cross-process
     contention và shutdown bounded. Chạy lại V6-14 HTTP black-box qua renamed composition root;
  4. clean-DB terminal journey: doctor, adapter, project/repository/component, definition, WorkItem/run,
     approval/wait/scope, conversation/evidence, cancel/recovery, workspace/source, ReleaseSet/local
     commit, projection rebuild/status và event watch trên Windows/Linux, không sửa SQLite/Git thủ công;
  5. `go build ./cmd/aw`, `go test ./...`, `go vet ./...` và full offline SPK gate; assert
     `agentkit-spike` vẫn build/chạy độc lập.
- **Hoàn thành khi:** parity debt bằng 0 và operator hoàn thành core journey/recovery từ terminal bằng
  `aw`, không cần V7 UI và không có CLI fast path.
- **Nguồn:** ADR-025, ADR-028, AK-ARCH-018, HE-04-M07, ROADMAP-§2.
