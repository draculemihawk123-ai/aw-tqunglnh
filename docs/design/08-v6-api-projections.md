# V6 — Local HTTP API, SSE, projections và operator CLI

> Entry: V5 core journey và execution/evidence acceptance gate pass.
>
> Exit: browser client và terminal `aw` dùng stable command/query contract trên cùng public application
> authority; projection live/rebuild được; HTTP/CLI không có execution, Git, provider, filesystem hoặc
> SQLite fast path. `V6-15P` là gate cuối: sản phẩm vận hành đầy đủ bằng terminal trước khi làm UI V7.
>
> Phạm vi task mặc định kế thừa `00-roadmap.md` §3. Một session chỉ làm một Task ID; các Task ID trong
> cùng nhóm song song được chạy ở các session/worktree riêng sau khi dependency của chính task đó pass.

## 1. Contract chung đã khóa

1. **Một authority cho mutation.** HTTP và `aw` chỉ tạo `CommandEnvelope` rồi gọi public application
   command. Command receipt trong application UnitOfWork là replay authority duy nhất; delivery không
   cache response, ghi receipt hoặc gọi internal worker command.
2. **Replay và concurrency.** Sau authenticate/authorize và canonical decode, application lookup receipt
   trước current-version/state precondition và trước external I/O. Same key + same semantic hash replay
   committed result; same key + khác hash conflict; key mới phải kiểm `ExpectedVersion`. Transaction
   recheck receipt rồi commit CAS + event/outbox/job + receipt + result atomically.
3. **Scope và authorization.** Installation scope là danh sách đóng trong ADR-025. Mọi item route reload
   authoritative target để suy Project/scope và authorize; không tin ID shape, payload hoặc projection.
   Authorization chạy lại cả khi receipt replay, nên role bị thu hồi không thể dùng replay để đọc/mutate.
4. **Shared delivery schema.** Error, ETag, page/cursor, `ValidAction`, `Freshness`, SSE envelope và route
   descriptor được freeze trước endpoint work. `ValidAction` chỉ là gợi ý kèm target version; command
   luôn reload và revalidate dưới race.
5. **Projection không là authority.** Projection chỉ phục vụ read UI. Mỗi event được machine-classify
   `APPLY(handlerVersion)` hoặc `IGNORE(reason)`. Rows + generation + cursor + freshness commit atomically;
   runtime readiness/completion/authorization không đọc projection.
6. **External I/O có durable protocol.** Attachment, adapter probe/register, projection rebuild và local
   commit không giữ DB transaction trong lúc làm filesystem/process/Git. Mỗi flow có intent/operation,
   lease/fence, exact replay và crash matrix ghi ngay trong task sở hữu nó.
7. **Safe settings có authority riêng.** Mutable safe settings là versioned SQLite desired-state overlay;
   startup config vẫn bất biến trong một process. Chỉ allowlist đã khóa được sửa, secret chỉ bằng reference,
   và response phân biệt desired/effective/restart-required.
8. **Parallel work không sửa registry chung.** Mỗi endpoint/CLI task sở hữu subpackage, descriptor/schema
   fragment và test riêng. Chỉ V6-12 compose HTTP router/OpenAPI; chỉ V6-15O compose CLI/parity registry.
   Event task sở hữu event fragment; V6-08 compose projector inventory. Schema task phải reserve migration
   ID trước khi chạy song song và không cùng sửa một migration file.

## 2. Dependency và nhóm triển khai song song

Nhóm dưới đây không phải global barrier: một task bắt đầu ngay khi toàn bộ dependency ghi trong chính task
đã pass. Các task cùng dấu ngoặc nhọn có thể chạy song song vì sở hữu file/package riêng.

```text
P0 — ngay sau V5:
  {V6-00, V6-00A, V6-01, V6-10C}

P1 — foundation tách nhánh:
  sau V6-01:  {V6-01A, V6-02A, V6-15A}
  sau V6-00A: {V6-03, V6-10E, V6-10G, V6-10I}
  V6-01A -> V6-02
  {V6-02, V6-02A, V6-15A} -> V6-15B khi từng dependency đạt

P2 — application/HTTP slices sau V6-00 + V6-01A + V6-02 + V6-02A:
  {V6-03A, V6-04, V6-05, V6-06, V6-06A, V6-06D,
   V6-07, V6-07B, V6-10B, V6-10H, V6-10J}
  V6-04 -> V6-04A
  V6-06 -> V6-06B; V6-06D -> V6-06C
  V6-07 -> V6-07A
  {V6-03A, V6-10J} -> V6-10A
  {V6-10B, V6-10C} -> V6-10D
  {V6-10B, V6-10E} -> V6-10F

P3 — projection:
  {V6-00, V6-00A, V6-04A} -> V6-08 -> V6-08A
  sau V6-08A: {V6-09, V6-10, V6-11}
  V6-09 -> V6-09A -> V6-09B

P4 — CLI leaf:
  sau V6-15B, mỗi task V6-15C…V6-15N bắt đầu ngay khi backend riêng đã pass;
  các leaf đủ dependency chạy song song và không chờ V6-14.

P5 — freeze và acceptance:
  all HTTP fragments -> V6-12 -> V6-13 -> V6-14
  sau V6-14: {V6-14A, V6-14B} -> V6-14C
  {V6-12, V6-15C…V6-15N} -> V6-15O
  {V6-14C, V6-15O} -> V6-15P
```

## V6-00 — UX artifact framework-neutral

- **Mục tiêu:** đặc tả hành vi cho 13 screen trước khi freeze API; không chọn UI framework.
- **Phụ thuộc:** V5.
- **Phạm vi:** wireframe và screen/action/query/state inventory trong `docs/design/`.
- **Không làm:** không viết production UI, endpoint hay domain command.
- **Thực hiện:** mô tả loading/empty/stale/error/blocked, keyboard/accessibility và Graph/Timeline
  fork/join/rework/checkpoint. Mỗi action/query ghi proposed operationId, `aw` leaf, public operation hoặc
  một gap có owner Task ID cụ thể; không tự tạo generic status setter.
- **Verify:** checker bảo đảm đủ 13 screen, mọi ô có mapping hoặc owner, không có gap chưa gán và không
  trùng authority. Zero gap chỉ là precondition của V6-12, không phải precondition để V6-00 hoàn thành.
- **Hoàn thành khi:** inventory đủ để endpoint task biết dữ liệu/interaction cần làm mà không hỏi lại
  product decision; mọi gap đều có Task ID chịu trách nhiệm.
- **Nguồn:** ADR-010, ADR-018, ADR-028.

## V6-00A — Domain-event catalog closure

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

## V6-04A — Public `MarkWorkItemReady` authority và route

- **Mục tiêu:** đóng `BACKLOG → READY` bằng narrow command, không generic status setter.
- **Phụ thuộc:** V6-04, V3-03, V1-06, V1-07A.
- **Phạm vi:** application command, event/receipt và `POST /work-items/{id}/mark-ready` fragment.
- **Không làm:** request không có `targetStatus`; projection không quyết readiness.
- **Thực hiện:** reload WorkItem, chạy canonical validator rồi atomically CAS, append registered
  `WORK_ITEM_MARKED_READY` v1 và receipt. Same key replay stored success; key mới khi READY conflict.
- **Verify:** replay/concurrency/stale/readiness/cross-project/payload target-status/event golden.
- **Hoàn thành khi:** valid action duy nhất là `mark-ready`, không có public `set-status`.
- **Nguồn:** ADR-028, HE-01-M02, HE-08-M03, GC-DS-11.

## V6-05 — Definition authoring endpoints

- **Mục tiêu:** create/validate/publish/list/detail/version/diff cho global và project definitions.
- **Phụ thuộc:** V6-00, V6-01A, V6-02, V6-02A.
- **Phạm vi:** bounded text/file payload và exact immutable version queries.
- **Không làm:** không persist invalid draft thành runtime version hoặc tin scope từ payload.
- **Thực hiện:** route derives scope; item/version reload authoritative Definition; publish trả source/
  compiled hash và exact pins; diff operands phải cùng scope.
- **Verify:** isolated schemas, location diagnostics, replay, version/diff và global/project negative matrix.
- **Hoàn thành khi:** UI/CLI author→validate→publish→inspect không seed SQLite.
- **Nguồn:** AK-ARCH-001, ADR-028.

## V6-06 — Run start và cancellation controls

- **Mục tiêu:** expose start/cancel Run bằng typed commands và quiesce semantics.
- **Phụ thuộc:** V6-00, V6-01A, V6-02, V6-02A.
- **Phạm vi:** start Run, cancel Run và mutation valid actions.
- **Không làm:** không graph/timeline/diagnostics, approval/WAIT hoặc inline worker execution.
- **Thực hiện:** start pins manifest/version; cancel trả `CANCELLING`, không giả `CANCELLED`; handler chỉ dispatch.
- **Verify:** replay/pinned conflict/cancel twice/cancel race; accepted response không terminal tức thì.
- **Hoàn thành khi:** Run control không có scheduler/worker fast path.
- **Nguồn:** ADR-020, ADR-022.

## V6-06A — Approval và typed WAIT decision endpoints

- **Mục tiêu:** expose approve/reject và typed WAIT signal tách khỏi free-text conversation.
- **Phụ thuộc:** V6-00, V6-01A, V6-02, V6-02A.
- **Phạm vi:** approval decisions, WAIT signals và DecisionArtifact references.
- **Không làm:** không map message text thành control và không nhận actor/roles từ payload.
- **Thực hiện:** reload target/scope, enforce role, unique signal identity and expected version.
- **Verify:** unauthorized role, spoof, concurrent decision, duplicate signal, stale/cross-project target.
- **Hoàn thành khi:** mọi human decision có typed audited route độc lập với chat.
- **Nguồn:** ADR-020, HE-08-M08.

## V6-06B — Run detail, graph và timeline endpoints

- **Mục tiêu:** cung cấp authoritative Run detail và bounded graph/timeline cho V7.
- **Phụ thuộc:** V6-00, V6-02A, V6-06.
- **Phạm vi:** `GET /runs/{id}`, `/graph`, `/timeline` và stable cursors.
- **Không làm:** không diagnostics/recovery mutation hoặc raw unbounded agent-event stream.
- **Thực hiện:** trả manifest revision, nodes/edges/activations, attempt/route/retry/checkpoint, correlation/
  causation, JournalPosition/freshness; cursor bind query and upper watermark.
- **Verify:** fork/join/rework fixtures, paging qua write, bounds and redaction.
- **Hoàn thành khi:** Graph/Timeline screen không cần đọc DB/event journal trực tiếp.
- **Nguồn:** ADR-018, AK-ARCH-025, HE-11-M02, HE-11-M03.

## V6-06C — Run diagnostics endpoints

- **Mục tiêu:** expose safe queue/job/lease/fence/provider/workspace diagnostics và recovery actions.
- **Phụ thuộc:** V6-00, V6-02A, V6-06D.
- **Phạm vi:** `GET /runs/{id}/diagnostics` authoritative query.
- **Không làm:** không PID, argv, cwd, secret, internal execute command hoặc installation Doctor data.
- **Thực hiện:** reload Run/project, return typed states/remediation and advisory valid actions with versions.
- **Verify:** blocked/lost/quarantined fixtures, role/project matrix, redaction and bounded output.
- **Hoàn thành khi:** operator chẩn đoán Run và chọn named recovery action không cần DB surgery.
- **Nguồn:** AK-ARCH-025, HE-11-M04.

## V6-06D — Recovery command endpoints

- **Mục tiêu:** transport cho `RetryBlockedActivation`, `CancelWorkItem`, `ResolveWorkItemBlocker`.
- **Phụ thuộc:** V6-00, V6-01A, V6-02, V6-02A, V4-12C, V5-08D.
- **Phạm vi:** đúng ba command và route/descriptor riêng.
- **Không làm:** không gọi internal recovery worker hoặc tự sửa blocker/Run state.
- **Thực hiện:** mỗi route dispatch một public command; resolve open blocker hợp lệ, resolved/waived replay
  no-op theo core; cancellation active Run trả quiesce state.
- **Verify:** precondition matrix, resolution mode, replay/concurrency, no duplicate activation and import test.
- **Hoàn thành khi:** Attempt/WorkItem blocked có named recovery action gọi được qua API.
- **Nguồn:** ADR-020.

## V6-07 — Conversation message endpoints

- **Mục tiêu:** append/list canonical text messages và bounded context-used metadata.
- **Phụ thuộc:** V6-00, V6-01A, V6-02, V6-02A.
- **Phạm vi:** text `AppendMessage`, list messages, ContextSnapshot/message reference metadata.
- **Không làm:** không binary upload, approval inference hoặc raw provider transcript.
- **Thực hiện:** reload project-owned conversation; return bounded canonical refs and pagination.
- **Verify:** replay, scope, paging, context refs, redaction and free-text-control negative tests.
- **Hoàn thành khi:** chat history là canonical platform state và không trở thành control plane.
- **Nguồn:** AK-ARCH-021, HE-11-M07.

## V6-07A — Attachment ingest, replay và orphan recovery

- **Mục tiêu:** upload có exact replay và durable owner ở mọi crash point.
- **Phụ thuộc:** V6-07, V5-14, V1-06.
- **Phạm vi:** `AppendConversationAttachment`, bounded spool/hash, content-addressed prepare claim và cleanup.
- **Không làm:** không DB transaction trong stream/hash/store; không raw locator hoặc best-effort-only cleanup.
- **Thực hiện:** require/verify declared digest; canonical hash gồm target, metadata, retention/sensitivity và
  digest persisted bytes. Receipt precheck trước ArtifactStore put. Deterministic UploadID owns durable
  prepare claim; same upload+digest reuses prepared blob, mismatch conflicts. Sau Put+Verify, serialized
  transaction rechecks receipt then atomically creates Artifact metadata + Message ref + event + receipt;
  acknowledge claim after commit. Sweeper resumes/cleans expired claim only after rechecking receipt,
  Artifact refs/holds and shared content hash.
- **Verify:** crash after spool/blob/claim/DB commit/before ack; same/different-key concurrency; shared blob;
  tamper/oversize/media; restart and orphan cleanup. Assert each outcome committed, resumable or cleanup-able.
- **Hoàn thành khi:** không crash point tạo duplicate message/artifact metadata hoặc ownerless permanent blob.
- **Nguồn:** ADR-017, AK-ARCH-021, HE-11-M07.

## V6-07B — Evidence, ContextSnapshot và artifact query endpoints

- **Mục tiêu:** list/inspect evidence/context/artifact và stream authorized content an toàn.
- **Phụ thuộc:** V6-00, V6-01A, V6-02A.
- **Phạm vi:** evidence list/detail/verify metadata, artifact inventory, `GetArtifactContent`, ContextSnapshot detail.
- **Không làm:** không expose locator, trusted HTML hoặc authorize từ projection/ID shape.
- **Thực hiện:** reload owning WorkItem/Run/Evidence/Message; range/size/media headers, content-hash ETag,
  no-sniff/download policy and sensitivity redaction.
- **Verify:** cross-project/guessed ID, traversal, tamper, range, large stream, HTML/SVG and secret fixtures.
- **Hoàn thành khi:** evidence/artifact verify được sau restart mà delivery không biết filesystem path.
- **Nguồn:** AK-ARCH-021, HE-11-M07, ADR-017.

## V6-08 — Projection schema và projector inventory

- **Mục tiêu:** freeze generation-aware Kanban/task-detail schema và exhaustive reducer classification.
- **Phụ thuộc:** V6-00, V6-00A, V6-04A.
- **Phạm vi:** migrations/repositories, active generation, checkpoint/freshness/poison records và projector catalog.
- **Không làm:** không live consume, rebuild hoặc dùng projection làm authority.
- **Thực hiện:** rows key `(ProjectID, ProjectionName, Generation, EntityKey)`. Catalog map mọi registered
  event version sang `APPLY(handlerVersion)` hoặc `IGNORE(reason)`; no implicit default. Cursor is greatest
  scanned global JournalPosition; event project khác không phải gap. Gap chỉ là missing referenced authority,
  aggregate-sequence violation, corrupt payload, relevant unknown schema hoặc deterministic reducer failure.
- **Verify:** migration old/new DB, inventory totality, deterministic reducer/golden, canonical snapshot hash.
- **Hoàn thành khi:** live consumer/rebuild dùng cùng frozen schema and reducer set without new design choice.
- **Nguồn:** AK-ARCH-022, AK-ARCH-023, GC-DS-11.

## V6-08A — Atomic projection event consumer

- **Mục tiêu:** apply projection rows, checkpoint và freshness atomically under generation/lease fence.
- **Phụ thuộc:** V6-08.
- **Phạm vi:** live scanner, consumer lease/fence, atomic apply and degraded/stale behavior.
- **Không làm:** không rebuild/cutover and no runtime authority read.
- **Thực hiện:** scan global monotonic/non-gapless journal and filter Project. One serialized transaction
  verifies active generation/fence/cursor, APPLY/IGNOREs, updates rows then CASes checkpoint/freshness.
  Duplicate `position<=cursor` no-op. Failure rolls back rows/cursor; separate tx records poison and
  `DEGRADED/STALE` at last-good cursor. Foreign-project positions advance scan cursor without row changes.
- **Verify:** replay twice; crash row-before-cursor/cursor-before-ack; two consumers; stale fence; restart;
  foreign interleaving; duplicate/out-of-order; poison/unknown relevant schema. Clean replay equals live at
  same position.
- **Hoàn thành khi:** cursor never exceeds applied data and poison cannot silently skip.
- **Nguồn:** AK-ARCH-022, AK-ARCH-023, HE-12-M08.

## V6-09 — Projection rebuild request và operation model

- **Mục tiêu:** create idempotent rebuild intent/job and exact-operation status.
- **Phụ thuộc:** V6-08A, V6-02.
- **Phạm vi:** public command/query, operation schema, receipt/event/job.
- **Không làm:** command không clear rows, edit cursor or invoke worker inline.
- **Thực hiện:** atomically create `REQUESTED` operation + job + registered event + receipt. Same key replays
  same OperationID; new key while project projection has nonterminal operation returns typed conflict with
  active ID. Status records phase, W0, shadow generation/cursor, cutover cursor and safe error.
- **Verify:** replay/concurrency/crash-after-intent/restart/job reclaim/exact operation lookup.
- **Hoàn thành khi:** request/status usable without exposing rebuild executor.
- **Nguồn:** AK-ARCH-022, ADR-028.

## V6-09A — Rebuild worker, fenced cutover và recovery

- **Mục tiêu:** build shadow from consistent snapshot and atomically cut over without mixed generation.
- **Phụ thuộc:** V6-09.
- **Phạm vi:** snapshot/watermark, shadow build/replay, cutover lease/fence, resume and shadow cleanup.
- **Không làm:** không modify authority/events or clear active generation first.
- **Thực hiện:** capture authoritative snapshot + W0 in one SQLite read snapshot; persist operation/generation/
  schema/projector version. Build shadow, replay `>W0`; checkpoint progress. Acquire per-project cutover
  lease shared with live consumer, catch up bounded delta to W1, then one tx CASes active generation/cursor,
  operation and job under lease fence. Stale worker cannot swap. Reader sees old or new; cursor bound old
  generation returns resync. Poison keeps old active. Resume exact shadow or discard only owned orphan after grace.
- **Verify:** crash snapshot/build/replay/before-during-after swap; two workers; lease expiry; live writes;
  poison; reader paging; cleanup. Canonical old/new diff and no mixed generation.
- **Hoàn thành khi:** restart/race always leaves one complete active generation with exact cursor.
- **Nguồn:** AK-ARCH-022, AK-ARCH-023, ADR-028.

## V6-09B — Projection rebuild HTTP endpoints

- **Mục tiêu:** expose projection status, request and exact rebuild-operation status.
- **Phụ thuộc:** V6-00, V6-01A, V6-02, V6-02A, V6-09A.
- **Phạm vi:** `GET /projects/{id}/projection`, POST rebuild, GET operation status.
- **Không làm:** handler không call worker/row store or infer latest operation.
- **Thực hiện:** route only dispatches V6-09 command/queries; POST returns accepted OperationID.
- **Verify:** isolated schemas, scope/replay/conflict and architecture dispatch spy.
- **Hoàn thành khi:** UI/CLI can request/wait exact rebuild without cursor surgery.
- **Nguồn:** AK-ARCH-022, ADR-028.

## V6-10 — Kanban và projected WorkItem detail endpoints

- **Mục tiêu:** bounded project views with filters, badges, blockers and freshness.
- **Phụ thuộc:** V6-00, V6-02A, V6-04A, V6-08A.
- **Phạm vi:** projected card/detail only; authoritative action query may decorate result.
- **Không làm:** projection không decide readiness/ValidAction or mutate state.
- **Thực hiện:** stable filter/sort/cursor, multi-repo badge aggregation, projection lag/status; authoritative
  service recomputes valid actions with target version before response.
- **Verify:** multi-repo card, filters/paging/generation resync/stale/degraded and action race.
- **Hoàn thành khi:** Kanban reads no runtime tables directly and cannot create false action authority.
- **Nguồn:** AK-ARCH-022, HE-08-M03, HE-08-M05.

> V6-10A…V6-10J are independent contract suites after their own prerequisites. Handler tasks own separate
> files/subpackages and fragments; none edits root router. `{V6-10B, V6-10H, V6-10J}` can run in parallel;
> after B, `{V6-10D, V6-10F}` can run in parallel because C/E authorities are already complete.

## V6-10A — Doctor endpoint

- **Mục tiêu:** first-run installation diagnosis with typed remediation.
- **Phụ thuộc:** V6-00, V6-01A, V6-02A, V6-03A, V6-10J.
- **Phạm vi:** `/doctor` aggregation for DB/roots/Git/provider/adapter/isolation readiness.
- **Không làm:** không duplicate repository onboarding/history/retry routes owned by V6-03A.
- **Thực hiện:** installation-scoped query combines typed component status and operation links; no credentials.
- **Verify:** healthy/degraded/blocked goldens, restart states and secret/path redaction.
- **Hoàn thành khi:** first-run UI needs no filesystem/config direct read.
- **Nguồn:** ADR-019, ADR-025.

## V6-10B — Workspace state, lease và reconcile endpoints

- **Mục tiêu:** expose WorkspaceSet/repository-workspace state, lease/fence/quarantine and request actions.
- **Phụ thuộc:** V6-00, V6-01A, V6-02, V6-02A, V6-03.
- **Phạm vi:** state queries, `RequestWorkspaceSetRelease`, `RequestWorkspaceReconciliation` routes.
- **Không làm:** no internal Execute command, Git/filesystem direct call or writer grant from HTTP.
- **Thực hiện:** project ownership reload; release/reconcile write intent+job via public commands; valid actions advisory.
- **Verify:** quarantine/writer refusal, replay/stale generation, scope and architecture import/dispatch tests.
- **Hoàn thành khi:** recovery request possible without Git/DB surgery and delivery has no executor authority.
- **Nguồn:** AK-ARCH-009.

## V6-10C — Bounded workspace inspection application queries

- **Mục tiêu:** public `GetSource`, `GetDiff`, `GetRepositoryLog` before HTTP/CLI exposure.
- **Phụ thuộc:** V5.
- **Phạm vi:** typed read-only port, project/revision/path authorization and bounded result DTO.
- **Không làm:** no HTTP, OS path, arbitrary ref expression/argv, mutable unpinned working tree or write.
- **Thực hiện:** reload workspace/repository/project, resolve exact authorized revision. Normalized relative path;
  diff exact base/result; log exact anchor+cursor. Adapter fixed operations reject traversal, symlink/reparse,
  option injection. App+adapter enforce byte/line/file/commit limits; binary and truncation typed.
- **Verify:** scope, arbitrary ref, traversal/reparse, binary/large, stable cursor, mismatch, cancellation; no path leak.
- **Hoàn thành khi:** named application queries bound output before delivery serialization.
- **Nguồn:** ADR-018, AK-ARCH-021.

## V6-10D — Bounded source, diff và repository-log endpoints

- **Mục tiêu:** map V6-10C read-only queries to HTTP.
- **Phụ thuộc:** V6-00, V6-01A, V6-02A, V6-10B, V6-10C.
- **Phạm vi:** source/diff/repository-log GET routes and fragments.
- **Không làm:** handler no file open, Git spawn, revision resolution or terminal.
- **Thực hiện:** dispatch exact query; map bounds/binary/truncation/cursor/media contracts.
- **Verify:** transport negative matrix plus architecture test no filesystem/process concrete import.
- **Hoàn thành khi:** no route can write, execute or arbitrary-read local paths.
- **Nguồn:** ADR-018.

## V6-10E — ReleaseSet/local-commit application authority

- **Mục tiêu:** complete public ReleaseSet queries/commands and crash-safe job-backed local commit.
- **Phụ thuộc:** V6-00A, V5-10A, V5-08C, V1-06, V1-07A.
- **Phạm vi:** list/get/create/seal/abandon and `RequestReleaseSetLocalCommit` operation/worker.
- **Không làm:** no push/fetch/PR/merge/rebase/force-push; no Git in command transaction; no implicit commit on seal.
- **Thực hiện:** request pins ReleaseSet entry/version, workspace generation/fence, exact parent/tree/result,
  actor/message hash and deterministic operation marker; atomically intent+job+event+receipt. Worker lease +
  write lease, revalidates, calls `LocalCommitCreator` outside tx, and reconciles existing exact marker before
  create. Finalize verifies both fences then stores commit/result/event. Write lease releases only after terminal
  commit, idempotent/fence-aware. Crash after Git before finalize reuses exact commit; mismatch blocks/quarantines.
- **Verify:** replay/concurrency/stale, seal/abandon, crash before/after Git/finalize, lease loss, drift, two workers,
  marker collision and remote-call spy zero.
- **Hoàn thành khi:** replay cannot create two local commits and no remote Git executor is reachable.
- **Nguồn:** ADR-014, AK-ARCH-015C, GC-INV-26.

## V6-10F — ReleaseSet và local Git endpoints

- **Mục tiêu:** expose V6-10E authorities and exact operation status.
- **Phụ thuộc:** V6-00, V6-01A, V6-02, V6-02A, V6-10B, V6-10E.
- **Phạm vi:** list/create/detail/seal/abandon, per-entry local-commit POST/status.
- **Không làm:** no Git adapter/worker call and no remote route.
- **Thực hiện:** dispatch public commands/queries; local commit returns accepted operation ID for wait.
- **Verify:** schemas/replay/stale/partial/dispatch architecture; route inventory excludes remote verbs.
- **Hoàn thành khi:** release/local commit usable from API without delivery Git authority.
- **Nguồn:** ADR-014, AK-ARCH-015C.

## V6-10G — Versioned safe-settings authority

- **Mục tiêu:** define persistence, precedence and desired/effective behavior before endpoint work.
- **Phụ thuộc:** V6-00A, V1-06, V1-07A.
- **Phạm vi:** installation `Get/UpdateSafeSettings`, SQLite aggregate/migration/event/receipt and startup merge.
- **Không làm:** no DatabasePath, WorkerID, LocalPrincipal, session/signing key, raw secret value or live mutation
  of immutable process config.
- **Thực hiện:** closed allowlist: managed workspace/artifact roots, evidence/raw-output retention, process output
  limit, provider executable path, provider default model and provider credential-reference ID. All Alpha changes
  are restart-required. Store full desired document + version; update CAS + `SAFE_SETTINGS_UPDATED` event +
  receipt. Startup resolves DatabasePath first, opens DB, then applies allowed precedence
  `defaults < config file < SQLite safe settings < environment < flags`; env/flag masking is returned explicitly.
  Response has desired/effective/version/restartRequired/maskedByStartupSource; never secret value. Existing
  published pins/runs are never repinned.
- **Verify:** migration/replay/concurrency/event golden; unknown/startup-security field; traversal/root overlap;
  invalid retention/model/provider ref; current process unchanged; restart applies unmasked value; env/flag mask;
  corrupt persisted settings fail readiness/Doctor typed.
- **Hoàn thành khi:** endpoint implementation has no remaining storage/precedence/allowlist decision.
- **Nguồn:** ADR-016, ADR-017, ADR-025, ADR-028.

## V6-10H — Safe settings endpoints

- **Mục tiêu:** expose `GET/PUT /settings/safe` over V6-10G.
- **Phụ thuộc:** V6-00, V6-01A, V6-02, V6-02A, V6-10G.
- **Phạm vi:** exact allowlist schema and route fragment.
- **Không làm:** handler no config/file/env/secret access and no extra setting.
- **Thực hiện:** GET query; PUT installation command + `If-Match`; return desired/effective/restart/masking.
- **Verify:** strict unknown field, stale/replay, restart/mask and secret/redaction goldens.
- **Hoàn thành khi:** UI can edit only locked safe settings and knows when restart is required.
- **Nguồn:** ADR-016, ADR-017, ADR-025, ADR-028.

## V6-10I — Adapter-build command envelope và receipt hardening

- **Mục tiêu:** harden Probe/Register before any HTTP/CLI exposure.
- **Phụ thuộc:** V6-00A, V1-06, V1-07A, V2-07A, V2-07B.
- **Phạm vi:** installation application commands, token replay and registered event.
- **Không làm:** no HTTP, ProjectID or process/file I/O inside transaction.
- **Thực hiện:** receipt/hash lookup before external I/O; probe replay exact stored candidate even expired; new
  key probes anew. Register re-probes outside tx then atomically insert-if-absent + event + receipt; Actor supplies
  RegisteredBy. Candidate binds executable/provider/protocol/capability/OS/config/nonce/expiry.
- **Verify:** replay/hash/concurrency/expired replay/new token/crash-after-commit, drift/token mismatch, event golden,
  installation scope and no I/O-in-tx architecture test.
- **Hoàn thành khi:** no public legacy call lacks CommandEnvelope and fingerprint dedupe is not command replay.
- **Nguồn:** ADR-022, ADR-025, GC-INV-35, GC-DS-11.

## V6-10J — Adapter-build registry endpoints

- **Mục tiêu:** expose list/detail/probe/register only after V6-10I hardening.
- **Phụ thuộc:** V6-00, V6-01A, V6-02, V6-02A, V6-10I.
- **Phạm vi:** installation `/adapter-builds` route/schema fragments.
- **Không làm:** no project mirror, direct prober/process or transport receipt.
- **Thực hiện:** GET public queries; POST dispatch hardened commands. Probe remains mutation because it creates
  replayable candidate receipt although registry state does not change.
- **Verify:** token/fingerprint/protocol/config/scope schemas and handler architecture spy.
- **Hoàn thành khi:** provider upgrade flow is usable without bypassing command contract.
- **Nguồn:** ADR-022, ADR-025, ADR-028.

## V6-11 — SSE project event stream

- **Mục tiêu:** redacted project invalidation/runtime summaries with lossless reconnect semantics.
- **Phụ thuộc:** V6-00, V6-01A, V6-02A, V6-08A.
- **Phạm vi:** `WatchProjectEvents`, replay cursor, heartbeat, retention/resync and slow-client policy.
- **Không làm:** no artifacts/log bodies/secrets, browser event cache authority or cross-project events.
- **Thực hiện:** authorize project from authoritative store before open/reopen. Event ID is relevant global
  JournalPosition; foreign positions need not emit. Fetch response exposes observed cursor; stream from that cursor
  emits all later relevant summaries. Heartbeat has no event ID and never advances cursor. Too-old cursor returns
  typed full-resync; bounded buffer disconnects slow client with last safe cursor.
- **Verify:** query→subscribe race, reconnect/duplicate, foreign interleaving, heartbeat, retained-min resync,
  role downgrade/restart, cross-project leak, slow client and bounded shutdown.
- **Hoàn thành khi:** reconnect cannot silently miss invalidation and stream contains only authorized summaries.
- **Nguồn:** AK-ARCH-023, HE-11-M07.

## V6-12 — Machine-readable API contract và router composition

- **Mục tiêu:** compose/freeze route fragments and prove runtime router equals checked schema both ways.
- **Phụ thuộc:** V6-00, V6-03A, V6-04, V6-04A, V6-05, V6-06, V6-06A, V6-06B, V6-06C,
  V6-06D, V6-07, V6-07A, V6-07B, V6-09B, V6-10, V6-10A, V6-10B, V6-10D, V6-10F,
  V6-10H, V6-10J, V6-11.
- **Phạm vi:** root router wiring, OpenAPI/equivalent artifact, compatibility and four-way parity inventory seed.
- **Không làm:** no new endpoint/DTO/authority and no retrofit metadata missing from leaf.
- **Thực hiện:** require V6-00 gap list empty; compose fragments once; stable unique operationId; reverse-check
  served routes and schemas; generate inventory `UX ↔ operationId ↔ aw ↔ public operation`.
- **Verify:** schema golden validation, breaking-diff gate, exact route inventory and dependency/reference checker.
- **Hoàn thành khi:** every served endpoint/error/example documented and every schema operation served exactly once.
- **Nguồn:** HE-04-M07, ADR-028.

## V6-13 — Bộ kiểm thử bảo mật API và ranh giới authority

- **Mục tiêu:** chứng minh scope/security mặc định từ chối và HTTP không có đường tắt thực thi.
- **Phụ thuộc:** V6-12.
- **Phạm vi:** architecture/security matrix for every route class, nested ID, artifact and SSE.
- **Không làm:** no security decision from projection and no provider/Git/SQLite concrete in handler.
- **Thực hiện:** test loopback/Host/Origin/token/CORS, reload target ownership before receipt/stream, current role
  on replay, unauthorized/not-found leakage, path/content injection, MIME and cancellation. Include cross-project
  guessed Run/WorkItem/blocker/workspace/ReleaseSet/artifact IDs and SSE foreign-event absence.
- **Verify:** `go test ./internal/archtest/... ./internal/delivery/httpapi/...`; automated route×scope×role matrix.
- **Hoàn thành khi:** every route has explicit scope proof and handler dependency graph ends at public ports.
- **Nguồn:** AK-ARCH-018, AK-ARCH-025A, AK-ARCH-027, GC-INV-14, HE-10-M04.

## V6-14 — Acceptance happy path HTTP/projection từ DB sạch

- **Mục tiêu:** chạy core journey qua HTTP của `aw serve` cuối cùng và quan sát bằng projection/SSE.
- **Phụ thuộc:** V6-12, V6-13, V6-15A.
- **Phạm vi:** one real composition fixture from clean DB through local commit/rebuild.
- **Không làm:** no SQLite/Git state seeding after bootstrap and no scripted handler bypass.
- **Thực hiện:** health/doctor/settings, adapter, project/repo, definition, WorkItem/run, message/attachment,
  approval/WAIT/scope, evidence, workspace/source, ReleaseSet/local commit, completion and projection rebuild.
- **Verify:** black-box process test, durable trace IDs/hashes and post-restart query equality.
- **Hoàn thành khi:** full happy path works only through public HTTP and workers.
- **Nguồn:** AK-ARCH-018, AK-ARCH-023.

## V6-14A — HTTP fault, replay, restart và concurrency acceptance

- **Mục tiêu:** prove cross-layer failure boundaries cannot duplicate side effects, leak scope or false-complete.
- **Phụ thuộc:** V6-14.
- **Phạm vi:** crash/race/security matrix over accepted journey.
- **Không làm:** no unit fake as final evidence.
- **Thực hiện:** crash after receipt commit/attachment put/Git commit/projection row/rebuild cutover; concurrent
  same/different keys, role downgrade, cancel `CANCELLING→CANCELLED`, poison projection and slow SSE.
- **Verify:** full black-box fault suite + supported race detector; exact side-effect/event/receipt counts.
- **Hoàn thành khi:** restart converges, false completion and duplicate external side effect count are zero.
- **Nguồn:** AK-ARCH-018, AK-ARCH-023, HE-10-M02, HE-10-M03.

## V6-14B — Acceptance HTTP đa nền tảng

- **Mục tiêu:** cùng một semantic suite pass trên Windows/Linux với lifecycle hữu hạn.
- **Phụ thuộc:** V6-14.
- **Phạm vi:** platform CI jobs/artifacts and semantic comparison.
- **Không làm:** unavailable platform is not silently marked PASS.
- **Thực hiện:** run clean journey, source/path, local Git, SSE/rebuild, install/start/shutdown fixtures on both OS.
- **Verify:** recorded Windows/Linux commands, logs and normalized result diff; missing evidence = CHƯA ĐỦ EVIDENCE.
- **Hoàn thành khi:** both platform artifacts pass same contract version.
- **Nguồn:** ADR-006, AK-ARCH-018.

## V6-14C — Gate cuối API/projection

- **Mục tiêu:** phát một verdict từ evidence happy/fault/platform đầy đủ trước gate terminal.
- **Phụ thuộc:** V6-14A, V6-14B.
- **Phạm vi:** rerun aggregate suite and publish evidence index/verdict.
- **Không làm:** no waiver for failed/missing required scenario.
- **Thực hiện:** verify OpenAPI hash, binary commit, fixture IDs, platform/race outputs and zero-debt counts.
- **Verify:** one reproducible gate command over all artifacts.
- **Hoàn thành khi:** verdict PASS; otherwise REWORK/CHƯA ĐỦ EVIDENCE with exact failing Task ID.
- **Nguồn:** AK-ARCH-018, AK-ARCH-023, ROADMAP-§3.

## V6-15A — Composition root canonical `aw`

- **Mục tiêu:** rename production executable `agentkit` to `aw` before HTTP black-box gate.
- **Phụ thuộc:** V6-01, V2-07B, V2-11.
- **Phạm vi:** `cmd/agentkit` → `cmd/aw`, serve/worker/version/help wiring and build references.
- **Không làm:** no alias `agentkit`; keep `agentkit-spike`; no leaf CLI redesign in this task.
- **Thực hiện:** `cmd/aw` is only composition root importing concrete adapters; move legacy behavior without
  changing semantics; HTTP/worker lifecycle runs from final binary name.
- **Verify:** `go build ./cmd/aw`; old production path absent; spike builds; serve/worker startup/shutdown smoke.
- **Hoàn thành khi:** later HTTP acceptance tests final production composition and no second root exists.
- **Nguồn:** ADR-028, AK-ARCH-018.

## V6-15B — Nền tảng CLI dùng chung

- **Mục tiêu:** freeze grammar/envelope/output/wait/confirmation and leaf descriptor registration.
- **Phụ thuộc:** V6-01A, V6-02, V6-02A, V6-15A.
- **Phạm vi:** `internal/delivery/cli` framework and no-op/sample descriptor test.
- **Không làm:** no domain leaf, SQLite/Git/provider/internal worker import or HTTP-client authority.
- **Thực hiện:** `aw <resource> <action>`; shared principal, project/version/idempotency flags; bounded stdin/file;
  typed exits; finite JSON one-document stdout; diagnostics stderr. `--wait` only observes and never executes/
  cancels job. High-impact prompts only TTY; noninteractive/JSON require `--yes`. Leaf descriptor records path,
  scope, app operation, HTTP operationId or typed `CLI_LOCAL`; fragments do not edit root registry.
- **Verify:** table-driven parse/output/wait/interrupt/confirmation, generated key returned, spoof actor absent,
  architecture import test and descriptor duplicate/missing metadata.
- **Hoàn thành khi:** independent leaf tasks can add packages/descriptors without shared-file edits.
- **Nguồn:** ADR-028, HE-04-M07.

> Sau V6-15B, V6-15C…V6-15N chạy song song khi backend riêng pass. Chỉ V6-15O compose registry.

## V6-15C — Bootstrap, Doctor và settings CLI

- **Mục tiêu:** vận hành health, Doctor và safe settings từ terminal với cùng installation authority.
- **Phụ thuộc:** V6-15B, V6-01, V6-10A, V6-10H.
- **Thực hiện:** `aw health live|ready`, `aw doctor`, `aw settings show|update` với version,
  desired/effective/masking/restart output.
- **Không làm:** no principal/security config mutation.
- **Verify:** healthy/degraded/blocked, stale/replay/restart/mask/redaction and JSON/human contract.
- **Hoàn thành khi:** installation diagnosis/settings work without browser.
- **Nguồn:** ADR-019, ADR-025, ADR-028.

## V6-15D — Catalog CLI

- **Mục tiêu:** bootstrap Project/repository/component/pack catalog từ terminal.
- **Phụ thuộc:** V6-15B, V6-03A.
- **Thực hiện:** `aw project list|create|show`; `repository list|register|onboarding|retry-probe`;
  `component list`; `pack-assignment list|assign` with exact project/scope/version.
- **Không làm:** no generic probe/component creation or DB seed.
- **Verify:** async onboarding, replay, retry, component discovery and exact pack pin.
- **Hoàn thành khi:** clean installation builds catalog entirely from terminal.
- **Nguồn:** ADR-019, ADR-025, ADR-028.

## V6-15E — Definition CLI

- **Mục tiêu:** author/validate/publish/inspect immutable definitions từ terminal.
- **Phụ thuộc:** V6-15B, V6-05.
- **Thực hiện:** definition list/create/show/versions/validate/publish and version show/diff for
  exact global/project scopes.
- **Không làm:** no direct registry/store access.
- **Verify:** scope negative matrix, diagnostics, replay and diff/pin output.
- **Hoàn thành khi:** definitions bootstrap without SQLite seed.
- **Nguồn:** ADR-028, AK-ARCH-001.

## V6-15F — Adapter-build CLI

- **Mục tiêu:** vận hành immutable AdapterBuild registry từ terminal qua hardened commands.
- **Phụ thuộc:** V6-15B, V6-10J.
- **Thực hiện:** `aw adapter list|show|probe|register` over hardened application commands.
- **Không làm:** no legacy no-envelope call or ProjectID.
- **Verify:** exact token replay/expiry, drift/capability/config mismatch, concurrency and redaction.
- **Hoàn thành khi:** provider executable upgrade can be completed from terminal.
- **Nguồn:** ADR-022, ADR-025, ADR-028.

## V6-15G — WorkItem và blocker CLI

- **Mục tiêu:** quản lý WorkItem readiness/lifecycle và blocker bằng named commands.
- **Phụ thuộc:** V6-15B, V6-04, V6-04A, V6-06D, V6-10.
- **Thực hiện:** work-item list/show/create/create-child/readiness/mark-ready/cancel and blocker resolve.
- **Không làm:** no set-status or projected authorization.
- **Verify:** subset/readiness, replay/concurrency/stale, cancel quiesce and resolution-mode matrix.
- **Hoàn thành khi:** every WorkItem valid action is callable by named terminal command.
- **Nguồn:** ADR-020, ADR-028, HE-08-M03.

## V6-15H — Run và recovery CLI

- **Mục tiêu:** start, inspect, cancel và recover Run từ terminal.
- **Phụ thuộc:** V6-15B, V6-06, V6-06B, V6-06C, V6-06D.
- **Thực hiện:** run start/show/cancel/graph/timeline/diagnostics and node-run retry-blocked.
- **Không làm:** no inline worker; wait timeout/Ctrl-C never dispatch cancellation.
- **Verify:** start/cancel replay, pin conflict, paging, diagnostics redaction, recovery and wait semantics.
- **Hoàn thành khi:** Run lifecycle/recovery needs no DB surgery.
- **Nguồn:** ADR-020, ADR-022, ADR-028.

## V6-15I — Scope, approval và WAIT CLI

- **Mục tiêu:** expose mọi human decision bằng typed audited terminal control.
- **Phụ thuộc:** V6-15B, V6-04, V6-06A.
- **Thực hiện:** scope-expansion request/approve/reject/withdraw; approval approve/reject; wait signal.
- **Không làm:** no free-text control or actor/role flags.
- **Verify:** HTTP/CLI auth parity, spoof, concurrent decision/signal and withdraw race.
- **Hoàn thành khi:** every human decision is typed and audited from terminal.
- **Nguồn:** ADR-020, ADR-028, HE-08-M08.

## V6-15J — Conversation và attachment CLI

- **Mục tiêu:** append/inspect canonical conversation và upload verified attachment từ terminal.
- **Phụ thuộc:** V6-15B, V6-07, V6-07A.
- **Thực hiện:** message list/append and attachment upload with bounded stdin/file and digest.
- **Không làm:** no approval inference or locator output.
- **Verify:** replay/crash/tamper/size, context metadata and stdout/stderr separation.
- **Hoàn thành khi:** canonical chat/attachments work from terminal.
- **Nguồn:** ADR-028, AK-ARCH-021.

## V6-15K — Evidence và artifact CLI

- **Mục tiêu:** inspect/verify evidence, ContextSnapshot và artifact content từ terminal.
- **Phụ thuộc:** V6-15B, V6-07B.
- **Thực hiện:** evidence list/verify, context-snapshot show and artifact get `--output <path|->`.
- **Không làm:** no raw locator; offline verify is typed `CLI_LOCAL` through injected verifier.
- **Verify:** exact bytes/hash/range, tamper, refs, binary stdout and secret redaction.
- **Hoàn thành khi:** evidence can be audited without filesystem knowledge.
- **Nguồn:** ADR-028, AK-ARCH-021, HE-11-M07.

## V6-15L — Workspace và bounded source CLI

- **Mục tiêu:** inspect/recover workspace và bounded source/diff/log từ terminal.
- **Phụ thuộc:** V6-15B, V6-10B, V6-10D.
- **Thực hiện:** workspace-set show/release; repository-workspace source/diff/log/reconcile.
- **Không làm:** no Git command, arbitrary path or interactive terminal.
- **Verify:** quarantine/lease/generation/replay, traversal/binary/truncation/stale revision and architecture.
- **Hoàn thành khi:** workspace inspection/recovery needs no manual Git/filesystem edit.
- **Nguồn:** ADR-018, ADR-028, AK-ARCH-009.

## V6-15M — ReleaseSet và local-commit CLI

- **Mục tiêu:** vận hành local multi-repository release và local commit an toàn từ terminal.
- **Phụ thuộc:** V6-15B, V6-10F.
- **Thực hiện:** release-set list/create/show/seal/abandon/local-commit with exact entry/revision and wait.
- **Không làm:** no push/fetch/PR/merge/rebase/force-push.
- **Verify:** replay/stale/partial/crash-after-Git and remote mutation spy zero.
- **Hoàn thành khi:** local multi-repo release works from terminal without duplicate commit.
- **Nguồn:** ADR-014, ADR-028, AK-ARCH-015C.

## V6-15N — Projection và event-stream CLI

- **Mục tiêu:** observe/rebuild projection và watch project events từ terminal.
- **Phụ thuộc:** V6-15B, V6-09B, V6-11.
- **Thực hiện:** projection status/rebuild/rebuild-status and events watch NDJSON; exact OperationID.
- **Không làm:** no cursor/row mutation or latest-operation inference.
- **Verify:** rebuild interruption/replay, resync, duplicate/slow consumer/shutdown and continuous NDJSON parse.
- **Hoàn thành khi:** operator observes/rebuilds projection without internal access.
- **Nguồn:** ADR-028, AK-ARCH-022, AK-ARCH-023.

## V6-15O — Checker parity UI/API/CLI/application

- **Mục tiêu:** prove one-to-one authority/scope/confirmation mapping before terminal acceptance.
- **Phụ thuộc:** V6-12, V6-15C, V6-15D, V6-15E, V6-15F, V6-15G, V6-15H, V6-15I,
  V6-15J, V6-15K, V6-15L, V6-15M, V6-15N.
- **Phạm vi:** compose CLI registry and four-way machine checker.
- **Không làm:** no new leaf/route. `CLI_LOCAL` closed set is serve/worker/help/version/evidence verify.
- **Thực hiện:** compare UX inventory, OpenAPI descriptors, public operation registry and CLI descriptors both
  directions; reject missing/duplicate/scope mismatch/internal command/remote Git and confirmation mismatch.
- **Verify:** injected parity debt fixtures and same semantic input/principal HTTP↔CLI normalized result tests.
- **Hoàn thành khi:** parity debt zero and every CLI leaf has public authority or allowed typed exception.
- **Nguồn:** ADR-025, ADR-028, HE-04-M07.

## V6-15P — Gate acceptance cuối V6 bằng terminal

- **Mục tiêu:** chứng minh operator hoàn thành core journey/recovery qua các process `aw` cuối trên cả hai nền tảng.
- **Phụ thuộc:** V6-14C, V6-15O.
- **Phạm vi:** `aw serve` + `aw worker` background processes, one-shot CLI journey, platform/race/build gate.
- **Không làm:** no SQLite/Git surgery, inline worker, UI dependency or missing-evidence PASS.
- **Thực hiện:** clean DB journey covers Doctor/settings/catalog/definition/adapter/WorkItem/Run/human decisions/
  conversation/evidence/cancel/recovery/workspace/source/ReleaseSet/local commit/projection/events. `--wait`
  observes durable operations only; timeout leaves operation running. JSON emits one result document; progress
  stderr. Run cross-process contention and bounded shutdown, then re-run V6-14 HTTP suite via same root.
- **Verify:** `go build ./cmd/aw`; `go test ./...`; `go vet ./...`; full offline SPK gate; supported race
  suite; Windows/Linux offline artifacts; `agentkit-spike` still builds. Missing required platform evidence
  yields `CHƯA ĐỦ EVIDENCE`.
- **Hoàn thành khi:** final verdict PASS, parity debt/false completion/duplicate side effects zero, product usable
  from terminal without V7 UI.
- **Nguồn:** ADR-025, ADR-028, AK-ARCH-018, HE-04-M07, ROADMAP-§2.
