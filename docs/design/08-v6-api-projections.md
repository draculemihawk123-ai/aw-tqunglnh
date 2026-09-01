# V6 — Local HTTP API, SSE và projections

> Entry: V5 core journey pass.
>
> Exit: browser client có stable `/api/v1` command/query contract và rebuildable read models; HTTP
> process không spawn Git/CLI hay truy cập SQLite ngoài application ports.

> Phạm vi task mặc định: kế thừa mục 3 của `00-roadmap.md`.

## V6-00 — UX artifact framework-neutral

- **Mục tiêu:** 13 screen của `01-system-design.md` §11 có đặc tả hành vi trước khi API bị freeze, để
  V6-12 không khóa một contract thiếu dữ liệu mà UI cần. Task này không chọn framework.
- **Phụ thuộc:** V5.
- **Phạm vi:** artifact tài liệu trong `docs/design/`; không viết code UI.
- **Thực hiện:** low-fidelity wireframe từng screen; ma trận screen × action × state; đặc tả
  loading/empty/stale/error/blocked; hành vi keyboard/accessibility; và một đặc tả riêng cho
  Graph/Timeline gồm fork/join/rework/checkpoint, selection synchronization, filters, large-graph
  behavior và accessible list fallback.
- **Verify:** mỗi action trong ma trận map tới đúng một endpoint hoặc được ghi là còn thiếu; danh sách
  “còn thiếu” phải rỗng trước khi V6-12 chạy.
- **Hoàn thành khi:** V7 có đủ đặc tả hành vi để implement mà không phải tự quyết interaction, và mọi dữ
  liệu screen cần đều có endpoint tương ứng.
- **Nguồn:** ADR-010, ADR-018.

## V6-01 — HTTP server và middleware nền

- **Mục tiêu:** loopback-only server có lifecycle/correlation/body limit/content type/error mapping và
  local-browser mutation protection.
- **Phụ thuộc:** V5.
- **Thực hiện:** route composition, graceful shutdown, request ID, safe error envelope, JSON strict
  decode; reject external bind, validate exact loopback Host/port và Origin, sinh per-start session
  token cho mutation; inject token chỉ vào no-store/CSP bootstrap HTML và không đưa token vào URL/log/
  browser storage/durable state.
- **Verify:** malformed/oversized/cancel/panic/error-code; external bind, DNS-rebinding Host, foreign/
  missing Origin và missing/wrong token tests.
- **Hoàn thành khi:** handler không expose private cause/SQL/provider output; tồn tại một static/
  bootstrap handler tối thiểu để V7-02 gắn production UI build vào — không có UI nào của Alpha được
  phục vụ ngoài binary; và tồn tại **route registration interface** để các task endpoint đăng ký handler
  mà không cùng sửa một route table, làm điều kiện cho parallel group ở V6-10.
- **Nguồn:** ADR-016.

## V6-02 — Idempotency/optimistic concurrency HTTP contract

- **Mục tiêu:** mutation bắt buộc `Idempotency-Key`, update dùng `If-Match`.
- **Phụ thuộc:** V6-01, V1-06.
- **Thực hiện:** middleware/DTO mapping, replay stored response, conflict status, version ETag.
- **Verify:** duplicate/same-key-different-body/stale version/concurrent requests.
- **Hoàn thành khi:** retry browser không tạo duplicate aggregate/job.
- **Nguồn:** AK-ARCH-008.

## V6-03 — Project/repository/component endpoints

- **Mục tiêu:** expose V3 catalog/onboarding commands/queries.
- **Phụ thuộc:** V6-02.
- **Thực hiện:** register trả `REGISTERING`; query trạng thái/probe history/actionable error và typed
  retry từ `BLOCKED`; component pack-assignment list/create pin exact version; không giả synchronous
  success trước probe.
- **Verify:** OpenAPI/contract fixtures success/validation/not-found/conflict.
- **Hoàn thành khi:** local path chỉ xuất ở view được phép và repository ID là authority.
- **Nguồn:** ROADMAP-§2.

## V6-04 — WorkItem/family/scope endpoints

- **Mục tiêu:** create/list/detail/child/readiness/scope expansion/decision.
- **Phụ thuộc:** V6-03.
- **Thực hiện:** map DTO sang public root-create nguyên tử, child subset command, readiness query và
  scope request/approve/reject; không expose setters cho family/workspace/status authority.
- **Verify:** multi-repo filters, subset violations, approval and version conflict contracts.
- **Hoàn thành khi:** client không thể trực tiếp set DONE hoặc family/workspace IDs.
- **Nguồn:** HE-08-M03.

## V6-05 — Definition validate/publish endpoints

- **Mục tiêu:** expose V2 compiler bằng text/file payload có bounded size.
- **Phụ thuộc:** V6-02.
- **Thực hiện:** strict content type/size, validate-only response có location, publish command trả
  SourceHash/CompiledSnapshotHash/exact pins và list/version/diff queries.
- **Verify:** location diagnostics, duplicate publish, version list/diff contracts.
- **Hoàn thành khi:** API không lưu invalid draft thành runtime version.
- **Nguồn:** AK-ARCH-001.

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
- **Thực hiện:** upload artifact trước rồi attach verified reference atomically vào message; range/size/
  media headers, sensitivity policy, no trusted HTML, content hash ETag.
- **Verify:** traversal/unauthorized-shaped ID/tamper/large stream/redaction tests.
- **Hoàn thành khi:** raw filesystem locator không xuất API.
- **Nguồn:** AK-ARCH-021, HE-11-M07.

## V6-08 — Projection event consumer

- **Mục tiêu:** idempotently build Kanban và task detail read models theo JournalPosition.
- **Phụ thuộc:** V1-07, V6-04.
- **Thực hiện:** consumer quét global monotonic/non-gapless JournalPosition rồi lọc Project; aggregate
  sequence vẫn là concurrency token riêng. Vị trí của project khác không phải gap; detect missing/
  corrupt referenced record, aggregate sequence violation, duplicate/out-of-order/poison và freshness.
- **Verify:** replay twice, stop/restart, gap and poison event tests.
- **Hoàn thành khi:** runtime không query projection cho readiness/completion; poison event đưa
  projection sang `DEGRADED/STALE`, không nhảy cursor.
- **Nguồn:** AK-ARCH-022, AK-ARCH-023, HE-12-M08.

## V6-09 — Projection rebuild command

- **Mục tiêu:** xóa/rebuild read model cho cùng result tại JournalPosition mà không giả event journal
  là full event-sourced authority.
- **Phụ thuộc:** V6-08.
- **Thực hiện:** snapshot authoritative tables tại watermark, build shadow model rồi replay event có
  JournalPosition sau watermark trước atomic swap; progress/status/error diagnostics.
- **Verify:** before/after canonical projection diff và interrupted rebuild recovery.
- **Hoàn thành khi:** authoritative tables/events không bị sửa.
- **Nguồn:** AK-ARCH-022.

## V6-10 — Kanban/task detail query endpoints

- **Mục tiêu:** project-level views, repository/component filters và freshness.
- **Phụ thuộc:** V6-08.
- **Thực hiện:** bounded pagination/sort/filter DTO, repository badge aggregation, blocker/valid-action
  summary và projection JournalPosition/status trong response.
- **Verify:** pagination/filter/sort/stale projection contracts.
- **Hoàn thành khi:** task multi-repo vẫn là một card với repository badges.
- **Nguồn:** ROADMAP-§2, AK-ARCH-023, HE-08-M05.

> V6-10A…V6-10F thay cho một umbrella task duy nhất: mỗi task dưới đây là một contract suite verify
> được độc lập, theo nguyên tắc kích thước task ở `00-roadmap.md` §3.
>
> **Có thể song song:** {V6-10A, V6-10E, V6-10F} không phụ thuộc lẫn nhau; sau khi V6-10B xong thì
> {V6-10C, V6-10D} cũng có thể chạy song song. V6-10B phải xong trước hai task đó vì cả hai đọc
> workspace/lease contract của nó.
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
- **Thực hiện:** create/seal/abandon ReleaseSet; `POST /release-sets/{id}/entries/{repositoryId}/
  local-commit`; per-repository verdict/partial state.
- **Verify:** duplicate seal, stale revision, partial result; spy adapter chứng minh remote mutation call
  count bằng 0; assert không route nào cho push/PR/merge/force-push.
- **Hoàn thành khi:** release decision thực hiện được từ API và remote Git vẫn không có executor.
- **Nguồn:** ADR-014, AK-ARCH-015C.

## V6-10E — Safe settings endpoints

- **Mục tiêu:** sửa được cấu hình không nhạy cảm mà không mở đường rò secret.
- **Phụ thuộc:** V6-02.
- **Thực hiện:** `GET/PUT /settings/safe` với allowlist tường minh, version validation, secret chỉ nhập
  bằng reference; giá trị secret không bao giờ round-trip.
- **Verify:** field ngoài allowlist bị từ chối; stale version conflict; assert response không chứa giá
  trị secret.
- **Hoàn thành khi:** setting cần restart được báo rõ thay vì áp dụng im lặng.
- **Nguồn:** ADR-016, ADR-017.

## V6-10F — Adapter build registry endpoints

- **Mục tiêu:** đưa flow probe/register của ADR-022 lên API để Doctor UI dùng được.
- **Phụ thuộc:** V2-07A, V2-07B, V6-02.
- **Thực hiện:** `GET /adapter-builds`, `GET /adapter-builds/{id}`, `POST /adapter-builds/probe` (không
  mutate, trả candidate token có ký/expiry) và `POST /adapter-builds` (re-probe ngoài transaction, đối
  chiếu token rồi đăng ký immutable build). Theo ADR-025 đây là route installation-scoped: không nhận
  ProjectID và không nằm dưới `/projects/{id}`.
- **Verify:** probe không ghi registry; register idempotent theo fingerprint; token hết hạn/sai chữ ký/
  executable đổi/protocol, capability-hash hoặc OS-config mismatch đều bị reject; token sống qua process
  restart; xoay signing key làm token cũ mất hiệu lực; assert run đang chạy không bị repin; assert registry không
  lộ ra như một DefinitionKind; assert route từ chối ProjectID.
- **Hoàn thành khi:** nâng cấp provider CLI xử lý được hoàn toàn qua API/UI.
- **Nguồn:** ADR-022.

## V6-11 — SSE event stream

- **Mục tiêu:** UI nhận invalidation/runtime summary và reconnect theo event ID.
- **Phụ thuộc:** V6-08.
- **Thực hiện:** project filter, heartbeat, bounded client buffer, slow-client disconnect, redacted
  payload; Last-Event-ID là JournalPosition, cursor quá cũ trả typed resync action.
- **Verify:** reconnect/duplicate/lag/slow consumer/shutdown tests.
- **Hoàn thành khi:** SSE không chứa artifact/log lớn hoặc secret fixture.
- **Nguồn:** AK-ARCH-023, HE-11-M07.

## V6-12 — Machine-readable API contract

- **Mục tiêu:** version schema cho DTO/error/routes để UI không dựa implementation detail.
- **Phụ thuộc:** V6-00, V6-03…V6-11, V6-06A, V6-06B và V6-10A…V6-10F. V6-00 phải hoàn tất trước task này: API
  không được freeze khi ma trận screen/action còn mục “thiếu endpoint”.
- **Thực hiện:** compose router tuần tự từ các handler đã đăng ký qua registration interface của V6-01 —
  đây là bước wiring duy nhất và không chạy song song; generate/maintain OpenAPI hoặc equivalent checked
  artifact từ authoritative route schemas; compatibility diff gate.
- **Verify:** schema validation của golden requests/responses; breaking diff fail; **route inventory
  test** assert tập route mà router thực sự phục vụ khớp đúng OpenAPI — không route thiếu, không route
  trùng path+method, không handler đăng ký mà không được nối.
- **Hoàn thành khi:** every public endpoint/example/error code documented, và router khớp OpenAPI theo
  cả hai chiều.
- **Nguồn:** HE-04-M07.

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
- **Phụ thuộc:** V6-00…V6-13.
- **Thực hiện:** doctor, adapter probe/register, async repo onboarding, create definition/task/run/
  message+attachment/approval/wait signal; cancel một run và quan sát `CANCELLING → CANCELLED`; inspect
  source/diff/log/timeline/diagnostics/evidence/ReleaseSet; local commit; await SSE; rebuild projection.
- **Verify:** black-box test from clean DB, full/race/Windows/Linux.
- **Hoàn thành khi:** không có test setup sửa SQLite/Git runtime state thủ công.
- **Nguồn:** AK-ARCH-018, AK-ARCH-023.
