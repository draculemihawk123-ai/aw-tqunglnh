# Thiết kế hệ thống Agent Kit Alpha

> Trạng thái: BASELINE ĐÃ QUYẾT ĐỊNH — product owner đã ủy quyền chốt ngày 2026-08-29.
>
> Authority: ADR-001…028 và Go core spec. Tài liệu này chi tiết hóa Alpha, không supersede ADR.
>
> Cập nhật: 2026-09-06.
>
> Phạm vi: modular monolith local, SQLite, filesystem artifacts, embedded worker, local web UI,
> Claude CLI và Codex CLI.

## 1. Mục tiêu thiết kế

Thiết kế khóa contract đủ để các session implementation không phải tự quyết lại:

- component/package boundary;
- definition và runtime model;
- state transition;
- SQLite schema;
- command/query và HTTP API;
- worker/provider/executable protocol;
- context, evidence và recovery;
- screen/interaction của UI Alpha.

Không khóa UI framework. Không thiết kế PostgreSQL; chỉ giữ persistence port không phụ thuộc SQLite.

## 2. Topology Alpha

```text
Browser -> loopback HTTP API + local session token + SSE --+
Terminal -> `aw` CLI delivery adapter ---------------------+-> application commands / queries
                                                               -> orchestrator / scheduler
                                                               -> SQLite transaction + durable jobs + events
                                                               -> embedded worker loop
                                                                    -> Git worktree adapter
                                                                    -> process supervisor
                                                                    -> Claude/Codex adapter
                                                                    -> command/gate executor
                                                                    -> filesystem artifact store
```

Một binary sản phẩm canonical `aw` có ba nhóm mode:

- `serve`: HTTP API, projection consumer và worker cùng process; đây là mode Alpha mặc định.
- `worker`: chạy worker loop riêng trên cùng máy/SQLite để fault test; không phải distributed mode.
- operator CLI theo grammar `aw <resource> <action> [flags]`, cộng top-level `aw doctor`, `aw version`
  và `aw help`; toàn bộ dùng cùng application/composition contracts với HTTP/UI.

`agentkit-spike` là binary regression/evidence riêng của V0, không phải tên cũ được alias sang `aw`.

Không có fast path từ HTTP hoặc CLI handler sang SQLite/Git/process/provider. Mọi side effect đi qua
public application command, durable job, ExecutionAttempt và fencing protocol dù tất cả module nằm
cùng process.

## 3. Boundary module và package Go

```text
cmd/aw/                         composition root và operator CLI
internal/domain/
  project/                     Project, Repository, Component
  work/                        WorkItem, TaskFamily, scope, blocker
  definition/                  versioned definition identity/metadata
  workflow/                    graph document/compiler/snapshot
  runtime/                     Run, NodeRun, Attempt, checkpoint
  workspace/                   WorkspaceSet, RevisionSet
  conversation/                Message và context manifest value objects
  evidence/                    Evidence/verdict/artifact metadata
  policy/                      policy value objects và decisions
internal/app/
  command/                     public command envelope + handlers
  query/                       query DTO/services
  definition/                  validate/publish/resolve dependencies
  work/                        Project/WorkItem/scope use cases
  orchestrator/                activation, routing, retry/rework/join/completion
  scheduler/                   ready work -> durable job
  worker/                      claim/execute/checkpoint/finalize/recover
  context/                     resource/message selection và snapshot
  verification/                gate/evidence/completion authority
  projection/                  event consumer và rebuild
  ports/                       UoW, repositories, executors, workspace, artifacts
internal/adapters/
  sqlite/                      migrations, UoW, repositories, jobs, projections
  artifactfs/                  content-addressed local artifact store
  gitworktree/                 workspace adapter Windows/Linux
  process/                     argv-only process supervisor
  providers/claude/
  providers/codex/
internal/delivery/httpapi/      route, DTO, middleware, SSE
internal/delivery/cli/          `aw` commands, formatter và typed exit-code mapping
internal/platform/              config, IDs, clock, logging, redaction
migrations/sqlite/              immutable numbered SQL migrations
testdata/                        workflow/provider/repository/golden fixtures
web/                             UI source sau UI decision
```

Luật phụ thuộc:

```text
domain <- app <- adapters/delivery/platform <- cmd
```

- Domain chỉ dùng standard library.
- App không import SQLite, HTTP, Git, `os/exec`, Claude hoặc Codex.
- HTTP DTO không được dùng làm domain type.
- Adapter không gọi adapter khác; composition root nối chúng.
- Không tạo `Store`, `Manager`, `common`, `utils` tổng hợp nhiều concern.

## 4. Definition plane

### 4.1 Loại definition Alpha

| Kind | Nội dung publish | Quyền thực thi |
|---|---|---|
| Workflow | typed graph và dependency manifest | Không trực tiếp |
| Block | input/output, outcomes, executor ref, policies | Qua executor ref |
| Skill | instruction/resource selectors | Không |
| Layer | stack convention/resource selectors | Không |
| Engineering Pack | dependency set Skill/Layer/resource | Không |
| Agent Profile | provider/model/context/tool policy refs | Qua provider policy |
| Command | executable + argv template + cwd/env/timeout/output | Có, khi policy grant |
| Gate | command/evaluator ref + verdict/evidence mapping | Có, khi policy grant |
| Policy | attempt, completion, permission, context hoặc cleanup | Chỉ semantics tương ứng |

`AdapterBuildVersion` **không** nằm trong bảng trên: theo ADR-022 nó thuộc mặt phẳng operational/
supply-chain, có registry và surface đăng ký riêng (§9), dù vẫn được compiled manifest pin như một
dependency có identity bất biến.

Mỗi kind có mutable `Definition` và immutable `Version`. Publish thực hiện strict decode,
canonicalize source, tính `SourceHash`, resolve exact dependency manifest, tạo compiled snapshot và
tính `CompiledSnapshotHash` trước khi insert version trong một transaction. Chỉ cùng
definition/compiled hash mới trả version hiện có; dependency resolve khác phải tăng `version_no` dù
source hash trùng.

### 4.2 Workflow schema Alpha

Authoring hỗ trợ YAML và JSON, strict unknown-field/duplicate-key rejection. Canonical snapshot là
JSON UTF-8. Node type:

- `START`, `END`;
- `AGENT`, `COMMAND`, `MACHINE_GATE`;
- `APPROVAL`, `WAIT`, `ROUTER`;
- `FORK`, `JOIN`.

Không có preset bắt buộc. Publish phải pin exact version/hash của Block, Agent Profile, Command,
Gate, Skill/Layer/Pack, context-route policy và immutable AdapterBuildVersion được tham chiếu.
Resource pin dùng `owner_version + resource_key + content_hash`, không dùng path làm identity. Version
range chỉ được dùng lúc authoring resolution; runtime manifest không giữ range.

Validator bắt buộc kiểm: single START, terminal path, reachability, route coverage, typed outcome,
schema compatibility, bounded cycle, fork/join policy, static scope conflict, capability/OS
compatibility và dependency hash.

## 5. Runtime aggregates và state

### 5.1 Ownership

```text
Project -> Repository[] -> Component[]
Root WorkItem -> TaskFamily -> WorkspaceSet -> RepositoryWorkspace[]
WorkItem -> WorkflowRun -> NodeRun activation -> ExecutionAttempt[]
Attempt -> JobLease + optional WriteLease[] + ContextSnapshot + Checkpoint[] + Evidence[]
```

Public root-create atomically tạo WorkItem, TaskFamily, WorkspaceSet intent, scope, provision job/
outbox và event; không có WorkItem mồ côi. Child reuse family/workspace và chỉ thu hẹp
scope. Một run pin WorkflowVersion, CompiledSnapshotHash, initial ExecutionManifest,
AdapterBuildVersion và base RevisionSet; scope bổ sung dùng RunManifestAmendment append-only.

### 5.2 Transition chuẩn

`WorkItem`:

```text
BACKLOG -> READY -> ACTIVE -> BLOCKED -> ACTIVE -> DONE
                   |             |
                   +----------> CANCELLED
```

- `READY` cần behavior, acceptance, verification và valid scope.
- `BACKLOG → READY` chỉ qua public `MarkWorkItemReady`, command chạy lại validator và không nhận target
  status tùy ý.
- `ACTIVE` cần WorkspaceSet ready và run started.
- `DONE` chỉ từ completion service với run `VERIFYING`, evidence/policy/join/clean gate đạt.
- `BLOCKED → READY` chỉ qua `ResolveWorkItemBlocker`; `→ CANCELLED` chỉ qua `CancelWorkItem`. `CancelRun`
  không đưa WorkItem sang `CANCELLED` mà sang `BLOCKED` kèm blocker `RUN_CANCELLED`.

`WorkflowRun`:

```text
CREATED -> RUNNING <-> WAITING
             |  ^
             v  |
           BLOCKED

RUNNING -> VERIFYING
VERIFYING -> SUCCEEDED | RUNNING | BLOCKED | FAILED   (theo CompletionDecision)
CREATED|RUNNING|WAITING|BLOCKED|VERIFYING -> CANCELLING -> CANCELLED
RUNNING|WAITING|BLOCKED -> FAILED                     (khi policy cho phép)
```

Bốn outcome của CompletionPolicy (ADR-021) có đúng một transition mỗi loại:

| CompletionDecision | WorkflowRun | WorkItem |
|---|---|---|
| `PASS` | `VERIFYING → SUCCEEDED` | `→ DONE` cùng transaction |
| `REWORK` | `VERIFYING → RUNNING` + activation mới theo rework edge đã publish | giữ `ACTIVE` |
| `BLOCK` | `VERIFYING → BLOCKED` | `→ BLOCKED` |
| `FAIL` | `VERIFYING → FAILED` | `→ BLOCKED` kèm blocker `COMPLETION_POLICY_FAILED` |

`REWORK` chỉ hợp lệ khi WorkflowVersion đã pin có rework edge tương ứng; không có edge hợp lệ thì
decision phải là `BLOCK`. Orchestrator không tự dựng route ngoài graph.

WorkItem không có state `FAILED`, nên `FAIL` đưa WorkItem về `BLOCKED` kèm blocker typed
`COMPLETION_POLICY_FAILED`. Thử lại cần operator command hoặc một run mới; hệ thống không tự đưa
WorkItem về `ACTIVE`, và không dùng `CANCELLED` vì đây không phải quyết định hủy.

`CANCELLING` là pha quiesce bắt buộc; Run không đi thẳng từ `RUNNING` sang `CANCELLED`.

`NodeRun`: `PENDING -> READY -> QUEUED -> RUNNING`, sau đó `WAITING|BLOCKED|SUCCEEDED|FAILED|
SKIPPED|CANCELLED`. Rework tạo activation mới; retry kỹ thuật tạo Attempt mới.

`ExecutionAttempt`:

```text
QUEUED -> RUNNING -> SUCCEEDED|FAILED|TIMED_OUT|CANCELLED|LOST|INDETERMINATE|BLOCKED
QUEUED -> BLOCKED                                  (admission từ chối, chưa từng RUNNING)
QUEUED -> CANCELLED                                (run bị cancel trước khi khởi động)
```

Terminal attempt không quay lại RUNNING và luôn ghi `TerminationReason` typed. Mutating attempt mất
ownership là `INDETERMINATE` và workspace `QUARANTINED` cho tới reconcile.

`BLOCKED` là terminal business/admission blocker, tách hẳn khỏi technical failure: nó không tính vào
retry budget của AttemptPolicy và Attempt đã BLOCKED không được hồi sinh. Hai đường vào:

| Nhóm | Transition | TerminationReason |
|---|---|---|
| Runtime blocker | `RUNNING → BLOCKED` | `SCOPE_EXPANSION_REQUIRED` |
| Admission blocker | `QUEUED → BLOCKED` | `ISOLATION_ENFORCEMENT_UNAVAILABLE`, `ADAPTER_BUILD_DRIFT`, `CAPABILITY_REQUIREMENT_UNSATISFIED`, `WRITE_CAPABILITY_OR_GRANT_MISSING` |

Nhóm admission mở cho mọi fail-closed check trước spawn, không riêng isolation. `QUEUED → BLOCKED` và
`QUEUED → CANCELLED` đều không set `StartedAt` và giữ spawn count bằng 0. Dùng `FAILED` cho các đường
này bị cấm.

`TerminationReason` là enum runtime riêng, không suy từ `AppError.Code`; ma trận state–reason đầy đủ nằm
ở Go core spec §4.5. `RetryBlockedActivation` tạo activation/Attempt mới sau khi revalidate exact pin,
không hồi sinh Attempt cũ và không repin run.

`DurableJob`: `AVAILABLE -> LEASED -> SUCCEEDED|FAILED|DEAD|CANCELLED`; lease expiry đi qua recovery
rồi tạo claim token mới, không sửa token cũ.

### 5.3 Scheduler semantics

- Scheduler là deterministic application service, không gọi filesystem/process/provider.
- Transition + event + downstream job/outbox commit atomically.
- Readiness dựa trên pinned graph, persisted branch token và authoritative state, không dựa queue rỗng.
- Technical retry tuân max attempts/error allowlist/backoff/timeout.
- Cycle/rework tuân iteration budget và escalation edge.
- JOIN dùng `ALL|ANY|QUORUM`; branch token được persist.
- Proposed outcome ngoài allow-list fail validation và không route.
- END chỉ tạo completion candidate/`VERIFYING`; CompletionPolicy transaction mới chuyển cả Run
  `SUCCEEDED` và WorkItem `DONE` trên exact RevisionSet/ReleaseSet.
- Scope expansion kết thúc Attempt cũ `BLOCKED`; sau approval/provision, amendment được append và một
  NodeRun activation mới nhận quyền. Sibling và activation cũ không đổi scope.
- Cancel intent commit làm scheduler ngừng tạo activation, technical retry và rework mới ngay lập tức;
  scheduler không chờ process dừng để ra quyết định đó.

## 6. SQLite data design

Mọi timestamp là RFC3339Nano UTC; ID là application-generated text; mutable aggregate có `version`;
foreign key luôn bật. JSON column lưu canonical validated JSON text. Artifact/log/diff lớn không nằm
inline trong state table.

### 6.1 Catalog và work

| Table | Key/field chính | Constraint quan trọng |
|---|---|---|
| `projects` | id, name, status, version, timestamps | name không rỗng |
| `repositories` | id, project_id, name, local_locator, default_ref, onboarding state/error, version | `REGISTERING|PROBING|ACTIVE|BLOCKED|DISABLED`; unique project+name |
| `repository_probe_attempts` | repository/job/state/result/error/time | append-only; active probe idempotent |
| `components` | id, project_id, repository_id, name, path, kind, version | path chuẩn `/`; unique repo+path |
| `work_items` | id, project_id, parent_id, family_id, schema_version, kind, title, behavior, acceptance_json, verification_json, risk, exclusions_json, workflow_version_id, status, version | parent/family/workflow cùng project; public root create nguyên tử |
| `task_families` | id, project_id, root_work_item_id, scope_version, status, version | unique root; không giữ FK ngược sang WorkspaceSet |
| `family_repository_scopes` | family/project/repository/scope_version/access/paths/reason/actor/time | append-only, add-only |
| `work_item_effective_scopes` | work_item/repository/access/paths/scope_version | subset family scope |
| `blockers` | id, work_item/run/node, type, reason, status `OPEN\|RESOLVED\|WAIVED`, resolution mode/actor/reason/`decision_artifact_id?`, created/resolved | append resolution audit; type typed, gồm `COMPLETION_POLICY_FAILED`, `RUN_CANCELLED`, `SCOPE_EXPANSION_REQUIRED` và bốn admission reason `ISOLATION_ENFORCEMENT_UNAVAILABLE`, `ADAPTER_BUILD_DRIFT`, `CAPABILITY_REQUIREMENT_UNSATISFIED`, `WRITE_CAPABILITY_OR_GRANT_MISSING` |
| `scope_expansion_requests` | id, family, requested grants, reason, status, actor/version | only add/upgrade; approval required |

### 6.2 Definitions

Mỗi definition kind dùng cặp `<kind>_definitions` và `<kind>_versions`; không dồn mọi payload vào một
bảng polymorphic khó enforce. Alpha có các cặp cho workflow, block, skill, layer, engineering pack,
agent profile, command, gate và policy.

Definition table: `id, project_id nullable, name, status, version, timestamps`, unique scope+name.
Version table: `id, definition_id, version_no, schema_version, canonical_source, source_hash,
compiled_snapshot, compiled_snapshot_hash, dependency_manifest_json, published_by, published_at`,
unique definition+version_no và definition+compiled hash; không có update/delete public operation.

`definition_dependency_pins` materialize `owner_version_id, dependency_kind, dependency_version_id,
resource_key nullable, content_hash` để audit/query và reject cross-project/incompatible pin.
`adapter_build_versions` lưu immutable provider/build identity, executable hash, protocol và capability
manifest. `component_pack_assignments` lưu component, pack version, effective time và actor để resolved
configuration không phải suy ngầm từ UI.

### 6.3 Workspace/runtime

| Table | Nội dung chính |
|---|---|
| `workspace_sets` | `family_id UNIQUE` là authority duy nhất của quan hệ family-workspace, state/version |
| `repository_workspaces` | workspace/repo/generation/opaque locator/base/current revision/state/version; unique family+repo+generation |
| `workflow_runs` | project/work/version/family/initial scope/manifest revision/state/shared-state/`cancel_epoch`/version/times |
| `execution_manifests` | run unique, compiled/dependency/adapter/execution/context/policy hashes, base RevisionSet; immutable |
| `run_manifest_amendments` | run/revision/previous/scope approval/reason/hash; append-only, unique run+revision |
| `node_runs` | run/node/activation/iteration/state/effective scope/input/profile/outcome/version |
| `branch_tokens` | run/fork/branch/current node/state/version; unique run+fork+branch |
| `execution_attempts` | node/attempt number/state/provider/adapter build/profile/isolation/context/input revision/checkpoint/termination/version |
| `durable_jobs` | `run_id?`/`job_class`/aggregate/payload ref/state/schedule/claim/lease/idempotency/`cancel_epoch`/version; hai partial claim index: `RUN_WORK` lọc `cancel_epoch IS NULL`, `CONTROL` không lọc |
| `write_leases` | workspace generation/fence/job token/attempt/owner/expiry |
| `wait_registrations` | run/node activation, signal schema/key, due time, state/version, consumed signal ref; unique active registration per activation |
| `wait_signals` | signal identity/idempotency key, payload artifact ref/hash, actor/time; immutable, unique signal identity |
| `run_cancellation_intents` | run/actor/reason/requested time/state; append-only, idempotent theo run |
| `work_item_cancellation_intents` | work_item/actor/reason/requested time/state; append-only, idempotent theo work item; một task nhiều run nên intent theo Run là không đủ |
| `checkpoints` | attempt/sequence/context/revisions/shared-state/artifact refs; immutable |
| `agent_events` | attempt/sequence/kind/schema/redacted payload/artifact refs; append-only |
| `approvals` | target/decision/actor/reason/evidence/time; append-only |
| `decision_artifacts` | typed policy/completion/recovery input, policy version và result; immutable |
| `release_sets` / `release_set_entries` | family/manifest/state/hash và exact base/result/local commit/verdict từng repo |

### 6.4 Conversation/evidence/audit

| Table | Nội dung chính |
|---|---|
| `conversations` | project/work item, created time |
| `conversation_messages` | conversation/attempt/actor/role/content artifact/time; append-only |
| `context_snapshots` | attempt unique active snapshot, message/resource/revision manifest/hash; immutable |
| `artifacts` | project/kind/URI/hash/size/media/sensitivity/redaction/retention/time |
| `evidence` | assertion/verdict/work/run/node/attempt/policy/revision/artifact manifest/time |
| `domain_events` | JournalPosition tăng đơn điệu + aggregate sequence/type/schema/correlation/causation/redacted payload/time |
| `event_schema_registry` | `(event_type, schema_version)` đã đăng ký, decoder/upcaster ref, golden fixture ref, deprecated flag |
| `outbox` | event/topic/payload/state/availability/lease; unique event |
| `command_receipts` | actor/`scope_key` (non-null: `installation` hoặc `project:<id>`)/idempotency/type/request hash/result/error/time; unique tuple chỉ dùng cột non-null |
| `projection_checkpoints` | projection/project/JournalPosition/watermark/status/error/time; tên canonical, không dùng tên thứ hai cho cùng contract |
| `kanban_projection` | project/work/status/title/repository summary/blocker/version |
| `task_detail_projection` | work item, materialized summary JSON, source JournalPosition |

Quan hệ TaskFamily–WorkspaceSet không dùng hai foreign key vòng: `workspace_sets.family_id UNIQUE` là
nguồn chuẩn duy nhất. Mọi table project-owned giữ `project_id` khi cần để chặn reference chéo Project.

`wait_registrations` cộng `wait_signals` là authority của WAIT, không phải `durable_jobs`: job chỉ đánh
thức timer đến hạn, còn consume-once đến từ signal identity unique cộng CAS trên registration.

### 6.5 Migration rules

- Chuyển migration inline hiện tại thành numbered SQL files trước schema Alpha mới.
- Migration đã apply không sửa; startup verify checksum.
- Migration test chạy database trống, database ở version trước và restart idempotency.
- Cross-project invariant kiểm bằng composite key/FK khi hợp lý và transaction validation khi SQLite
  không biểu diễn được constraint.
- Không `INSERT OR REPLACE`, không hard-delete audit/runtime history.

### 6.6 Connection và transaction policy

Đây là contract bắt buộc, không phải chi tiết tùy adapter. Spike đã hai lần vấp đúng lớp này
(`SQLITE_BUSY` rò ra ngoài ở lease race và ở finalize đồng thời), nên nó được khóa trước V1:

- `PRAGMA foreign_keys = ON` trên **mọi** connection, không chỉ connection migration.
- WAL được bật và được verify lúc startup, không giả định mặc định.
- `busy_timeout` lấy từ config, không hard-code trong adapter.
- Durability mode được khai rõ trong config dump và không được hạ xuống `OFF`.
- Flow claim job, cấp version và cấp JournalPosition dùng immediate-write transaction semantics.
- `SQLITE_BUSY` và `SQLITE_LOCKED` không được rò ra ngoài adapter, và mapping là xác định: sau bounded
  retry, lock contention thuần túy → `UNAVAILABLE` (retryable); `CONFLICT` chỉ khi reload xác nhận
  expected-version/CAS đã thua. Application không phân loại lỗi bằng cách đọc message.

Ranh giới quan trọng: **application không biết `BEGIN IMMEDIATE`**. Nó yêu cầu một semantic transaction
option (ví dụ `SerializedWrite`) và SQLite adapter ánh xạ option đó sang cơ chế cụ thể; PostgreSQL
adapter sau này có thể ánh xạ sang row-level lock mà không đổi call site.

Contract test cho lớp này dùng nhiều connection thật để tái hiện contention, không mock.

### 6.7 Event schema compatibility

`domain_events.schema` chỉ có giá trị nếu có registry cưỡng chế. Alpha bắt buộc:

- registry `(event_type, schema_version)`; emit event chưa đăng ký bị từ chối tại boundary append;
- breaking payload phải tăng schema version, giữ raw event bất biến, và có deterministic decoder/
  upcaster cộng golden fixture;
- projection rebuild phải replay được mọi event version lịch sử, không chỉ version mới nhất;
- CI fail khi một decoder/upcaster đang được golden fixture tham chiếu bị xóa.

## 7. Transaction, lease và recovery protocol

Application mutation:

```text
validate command/idempotency/expected version
  -> load aggregate in UoW
  -> validate invariant/policy
  -> compare-and-swap state
  -> append domain event
  -> enqueue outbox/job if needed
  -> store command receipt
  -> commit
```

Worker chạy side effect ngoài transaction. Finalize transaction kiểm JobLease, mọi WriteLease,
workspace generation, attempt version, outcome schema, diff scope và evidence. Bất kỳ check nào fail
thì không commit success.

Sau crash:

- intent chưa dispatch: claim lại;
- read-only attempt mất lease: LOST, retry bằng Attempt mới;
- mutating process có thể đã chạy: INDETERMINATE + quarantine + reconcile;
- outcome đã commit nhưng ack mất: duplicate finalize bị expected-version/idempotency từ chối;
- provider session mất: Attempt mới gọi `Start` từ ContextSnapshot; Alpha không gọi `Resume`.

Cancellation là protocol quiesce, không phải một CAS:

1. `CancelRun` atomically ghi durable cancel intent, chuyển Run `CANCELLING`, append event và enqueue
   job; command idempotent theo run.
2. Scheduler ngừng tạo activation, technical retry và rework mới ngay khi intent commit.
3. WAIT, APPROVAL, durable job chưa claim và NodeRun chưa chạy chuyển `CANCELLED`.
4. Attempt đang chạy nhận cancellation token; process tree bị terminate theo grace rồi force.
5. WriteLease chỉ release **sau khi** worker xác nhận process đã dừng.
6. Mutating attempt không chứng minh được kết quả là `INDETERMINATE` + workspace `QUARANTINED`; không
   được ghi nhãn `CANCELLED` cho outcome chưa biết.
7. Run chỉ thành `CANCELLED` sau khi execution đã quiesce hoặc reconciliation hoàn tất.
8. Cancel không tự cleanup workspace và không tự abandon ReleaseSet.

Fence chống race với job claim dùng `cancel_epoch`, không dùng boolean:

- `WorkflowRun.cancel_epoch` đi `NULL → 1` khi cancel intent commit;
- `DurableJob` mang `RunID?` và `JobClass = RUN_WORK | CONTROL`. `JobClass` được suy từ `Kind` bằng
  mapping tĩnh có check constraint, không phải cột tự do: allow-list `CONTROL` gồm đúng
  `CANCEL_RUN_COORDINATOR`, `WORKSPACE_RECONCILE`, `WORKSPACE_SET_RELEASE`, `RECOVERY_REAPER`; mọi Kind
  khác — kể cả Kind chưa biết — là `RUN_WORK`;
- cùng transaction: mọi job **`RUN_WORK`** `AVAILABLE` của Run → `CANCELLED`, mọi job `RUN_WORK`
  nonterminal nhận `cancel_epoch=1`, mọi Attempt còn `QUEUED` → `CANCELLED`. Job `CONTROL` không bị
  đụng — nếu không, cancellation coordinator job do chính `CancelRun` enqueue sẽ bị fence bởi transaction
  sinh ra nó và không bao giờ được claim;
- claim CAS tách theo class: `RUN_WORK` đòi `cancel_epoch IS NULL`, `CONTROL` thì không. Fence nằm trên
  job row nên claim không join sang `workflow_runs`;
- enqueue `RUN_WORK` mới CAS rằng Run còn non-cancelling;
- worker re-check epoch ngay trước `QUEUED → RUNNING` và ngay trước `ProcessSupervisor.Start`;
- `RUNNING` commit trước cancel → active cancellation; cancel commit trước → spawn count bằng 0.

Cancel cạnh tranh được phân xử bằng thứ tự **commit**, và điều kiện no-op là **Run đã terminal** —
không phải "có transaction nào đó commit trước". Attempt terminal và END đều không làm Run terminal:

| Commit trước | Run khi đó | `CancelRun` |
|---|---|---|
| Attempt finalize `SUCCEEDED` | `RUNNING` | Chấp nhận, vào `CANCELLING` |
| END → `VERIFYING` | `VERIFYING` | Chấp nhận, cancel từ `VERIFYING` |
| CompletionDecision `BLOCK` | `BLOCKED` | Chấp nhận |
| CompletionDecision `REWORK` | `RUNNING` | Chấp nhận |
| CompletionDecision `PASS` | `SUCCEEDED` | No-op idempotent |
| CompletionDecision `FAIL` | `FAILED` | No-op idempotent |

- cancel intent commit trước → mọi authoritative outcome đến muộn bị CAS từ chối; không finalize/routing/
  completion nào được tạo `SUCCEEDED`, activation, technical retry hay rework;
- external success đến muộn chỉ lưu như observation/evidence để reconcile, không phải authority state;
- mutating outcome không chắc chắn vẫn là `INDETERMINATE` + `QUARANTINED`.

`CancelRun` hủy run chứ không hủy task: nó đưa WorkItem về `BLOCKED` kèm blocker `RUN_CANCELLED`.
`CancelWorkItem` đưa WorkItem sang `CANCELLED`, nhưng gặp Run còn active thì phải ghi một **WorkItem-
level** cancel intent bền vững (intent theo Run không đủ vì một task có thể có nhiều run) và đi qua
**cùng** quiesce protocol cho từng active Run; WorkItem chỉ terminal sau khi mọi active Run đã dừng.
Completion `PASS` commit trước làm `CancelWorkItem` thành no-op idempotent; cancel intent commit trước
thì Completion đến muộn bị CAS từ chối.

`ResolveWorkItemBlocker` đưa `BLOCKED → READY`. Nó **chính là** command chuyển blocker
`OPEN → RESOLVED|WAIVED` trong cùng transaction, không đòi blocker đã resolved từ trước. Precondition
thật: blocker tồn tại và đang `OPEN`, không còn Run nonterminal, không workspace nào đang `QUARANTINED`.
Blocker đã `RESOLVED|WAIVED` là no-op idempotent, không phải lỗi.

Chuyển blocker và mở khóa WorkItem là hai điều kiện tách rời: blocker target luôn được chuyển trạng
thái, nhưng WorkItem chỉ `BLOCKED → READY` khi số blocker `OPEN` còn lại bằng 0. Ngoài ra một
`work_item_cancellation_intent` đang pending fence cả `ResolveWorkItemBlocker` lẫn `StartWorkflowRun`
bằng CAS — task đang bị hủy không được mở khóa và không được nhận Run mới.

Payload MUST chọn resolution mode tường minh, không có mặc định. `RESOLVED` nghĩa là điều kiện đã biến
mất và được revalidate. `WAIVED` nghĩa là operator chấp nhận rủi ro: cần actor, reason, policy grant và
một `DecisionArtifact`. Bốn admission reason và `SCOPE_EXPANSION_REQUIRED` **không bao giờ** waive được
— waive chúng đúng bằng vô hiệu hóa enforcement của ADR-011/013/022/023; muốn đổi hành vi thì sửa
definition/policy rồi republish. Bảng đầy đủ nằm ở ADR-020.

CompletionDecision `BLOCK` để Run ở `BLOCKED`; Alpha không resume Run đã BLOCKED. Đường chuẩn là
`CancelRun` → resolve toàn bộ blocker → start run mới.

`RetryBlockedActivation` thoát khỏi Attempt `BLOCKED` do admission. Precondition: Run `RUNNING|BLOCKED`,
WorkItem `ACTIVE|BLOCKED`, NodeRun `BLOCKED` với Attempt cuối `BLOCKED` mang reason thuộc nhóm admission,
expected version khớp và cancel fence chưa set. Revalidate exact pin trước; thành công mới tạo
activation/Attempt mới, thất bại giữ nguyên blocker và không sinh thêm blocked activation. Không repin
Run; adapter drift không khôi phục được exact pin thì valid action là `CancelRun`. Admission blocker
được lưu thành row `blockers` với type bằng chính `TerminationReason`, nên valid-action query và UI đọc
được mà không phải suy từ Attempt.

Recovery reaper chạy định kỳ bằng database time trong suốt vòng đời process, không chỉ quét startup.
Idempotency key `job_id + lease_token` bảo đảm một expired generation chỉ có một recovery job còn hiệu
lực. Hai worker cạnh tranh phải cho đúng một recovery authority.

## 8. Artifact, context và evidence

Artifact store dùng content-addressed path dưới configured root, write temp + fsync/atomic rename,
SHA-256 verify và metadata SQLite. Locator là opaque, API chỉ mở artifact theo ID đã authorize.

Context assembler nhận WorkItem, node, effective scope, pinned definitions, messages và RevisionSet;
resolve selector/priority/conflict/budget; persist exact manifest trước provider start. Hai hard
constraint cùng scope mâu thuẫn làm node `BLOCKED/NEEDS_INFO`, không last-wins.

Evidence verdict: `PASS|FAIL|ERROR|NOT_RUN|NOT_APPLICABLE`. `NOT_APPLICABLE` cần policy và reason.
Command evidence giữ executable/version, argv đã redact, cwd target, exit code, time, tool version,
RevisionSet và output artifact hash. Completion chỉ đọc durable evidence metadata đã verify.

Retention Alpha dùng class: raw provider output và evidence tạm mặc định 7 ngày; canonical Message,
referenced resource/context và audit metadata không dùng blanket TTL. Sweeper chỉ xóa payload hết hạn
không còn reference/hold sau atomic recheck; Alpha không hard-delete conversation. Secret fixture phải
được redact trước persist.

## 9. HTTP API Alpha

Base path `/api/v1`. JSON dùng camelCase, UTC timestamp, opaque string IDs. Mutation yêu cầu
`Idempotency-Key`, `X-AgentKit-Session` và Origin hợp lệ; update aggregate yêu cầu
`If-Match: <version>`. Server chỉ bind loopback, validate Host/Origin và từ chối external bind. Session
token sinh mới mỗi startup, không đặt trong URL/log/durable state. Error envelope:

```json
{
  "error": {
    "code": "CONFLICT",
    "message": "safe message",
    "correlationId": "...",
    "details": [{"field": "...", "reason": "..."}]
  }
}
```

Server inject token vào bootstrap HTML chỉ khi Host là `localhost`, `127.0.0.1` hoặc `[::1]` đúng port
đang listen; response có `Cache-Control: no-store` và CSP chặt. UI giữ token trong memory, không
local/session storage. API không có endpoint công khai trả token.

Khi startup, composition root resolve một `LocalPrincipalSnapshot {Actor, Roles[]}` từ trusted config.
HTTP session token được bind trong memory với snapshot đó; one-shot `aw` resolve cùng config. Delivery
layer điền `Command.Actor/ActorRoles`, tuyệt đối không decode chúng từ HTTP body/header hay CLI flag.
Principal config là restart-required và không thuộc `PUT /settings/safe`; Alpha không xây role database.
Keys là `localPrincipal.actor`/`localPrincipal.roles`; nếu thiếu toàn bộ dùng
`local-operator`/`[operator]`. Giá trị phải non-empty, role unique/case-sensitive; chỉ global config-file
selection được phép, không có per-command impersonation flag.

Route chính:

| Method/path | Contract |
|---|---|
| `GET /health/live`, `/health/ready`, `/doctor` | process readiness và chẩn đoán DB/root/Git/provider/isolation |
| `GET/POST /projects` | list/create project (installation scope) |
| `GET /projects/{id}` | project detail |
| `GET/POST /projects/{id}/repositories` | list/register; POST trả repository `REGISTERING` + probe job |
| `GET /repositories/{id}/onboarding` | trạng thái/error/probe history có thể hành động |
| `POST /repositories/{id}/retry-probe` | dispatch `RetryRepositoryProbe` khi `BLOCKED`; không có generic probe mutation |
| `GET /projects/{id}/components` | catalog component đã được repository onboarding/probe discover |
| `GET/POST /components/{id}/pack-assignments` | list/assign exact Engineering Pack version |
| `GET/POST /projects/{id}/work-items` | Kanban list/create root task |
| `POST /work-items/{id}/children` | child cùng family, subset scope |
| `GET /work-items/{id}/readiness` | contract/readiness diagnostics, không mutate |
| `POST /work-items/{id}/mark-ready` | dispatch `MarkWorkItemReady`; revalidate rồi chỉ `BACKLOG → READY` |
| `POST /work-items/{id}/cancel` | dispatch `CancelWorkItem`; quiesce active Run trước khi terminal |
| `POST /blockers/{id}/resolve` | dispatch `ResolveWorkItemBlocker`; `BLOCKED → READY` |
| `GET /work-items/{id}` | materialized detail kèm JournalPosition/freshness |
| `POST /work-items/{id}/runs` | start run với workflow version |
| `GET /runs/{id}` | run detail, state và manifest revision hiện hành |
| `POST /runs/{id}/cancel` | durable cancel intent; Run chuyển `CANCELLING` |
| `POST /node-runs/{id}/retry-blocked-activation` | dispatch `RetryBlockedActivation`; revalidate pin rồi tạo activation mới |
| `GET /runs/{id}/graph` | pinned graph + runtime overlay |
| `GET /runs/{id}/timeline` | node/attempt/route/retry/checkpoint theo stable cursor, kèm correlation/causation và JournalPosition/freshness |
| `GET /runs/{id}/diagnostics` | job/lease/fence/provider/workspace state và valid recovery actions; không lộ PID, argv, cwd hay secret |
| `POST /families/{id}/scope-expansions` | create request |
| `POST /scope-expansions/{id}/approve|reject` | typed decision |
| `POST /scope-expansions/{id}/withdraw` | typed `WithdrawScopeExpansion`; chỉ hợp lệ khi `PENDING`, idempotent, không tạo grant/amendment |
| `POST /approvals/{id}/approve|reject` | resolve approval node |
| `GET/POST /work-items/{id}/messages` | canonical task chat |
| `POST /work-items/{id}/attachments` | `AppendConversationAttachment`: upload artifact rồi append verified ref |
| `POST /waits/{id}/signals` | typed durable signal cho WAIT node |
| `GET /work-items/{id}/evidence`, `/evidence/{id}` | evidence inventory/detail |
| `GET /context-snapshots/{id}` | authorized immutable ContextSnapshot detail |
| `GET /artifacts/{id}`, `/artifacts/{id}/content` | artifact metadata và authorized verified bounded streaming |
| `GET /workspace-sets/{id}` | repo/revision/lease/quarantine và valid actions |
| `POST /workspace-sets/{id}/release` | dispatch public `RequestWorkspaceSetRelease`; thực thi là internal `ExecuteWorkspaceSetRelease` |
| `GET /repository-workspaces/{id}/source|diff|log` | read-only, exact revision, bounded output |
| `POST /repository-workspaces/{id}/reconcile` | dispatch public `RequestWorkspaceReconciliation`; thực thi là internal command riêng |
| `GET/POST /families/{id}/release-sets` | query/create local ReleaseSet |
| `GET /release-sets/{id}` | detail, per-repository verdict và partial state |
| `POST /release-sets/{id}/seal|abandon` | typed release decision |
| `POST /release-sets/{id}/entries/{repositoryId}/local-commit` | ghi durable local-commit intent; trả exact operation ID, không remote mutation |
| `GET /release-sets/{id}/local-commits/{operationId}` | trạng thái đúng local-commit operation để UI/CLI `--wait` |
| `GET /adapter-builds` | list AdapterBuildVersion đã đăng ký (installation scope, ngoài cây `/projects`) |
| `GET /adapter-builds/{id}` | detail fingerprint/protocol/capability manifest |
| `POST /adapter-builds/probe` | probe executable đã cấu hình, trả candidate chưa đăng ký |
| `POST /adapter-builds` | operator xác nhận đăng ký immutable AdapterBuildVersion |
| `GET/POST /definitions/{kind}` và `/projects/{projectId}/definitions/{kind}` | list/create global hoặc project Definition |
| `GET /definitions/{kind}/{id}` và `GET /projects/{projectId}/definitions/{kind}/{id}` | definition detail |
| `GET /definitions/{kind}/{id}/versions` và project-scoped path tương ứng | version list của một Definition |
| `POST /definitions/{kind}/{id}/validate|publish` và `/projects/{projectId}/definitions/{kind}/{id}/validate|publish` | validate without publish / publish immutable version |
| `GET /definition-versions/{id}`, `/definition-versions/{leftId}/diff/{rightId}` và các path tương ứng dưới `/projects/{projectId}` | exact version detail/diff |
| `GET/PUT /settings/safe` | allow-listed non-secret settings với version/validation |
| `GET /projects/{id}/projection` | projection/rebuild status và freshness |
| `POST /projects/{id}/projection/rebuild` | dispatch public `RequestProjectionRebuild`; không chạy rebuild inline |
| `GET /projects/{id}/projection-rebuilds/{operationId}` | status chính xác của một rebuild operation, không suy từ lần mới nhất |
| `GET /events/stream?projectId=...` | `WatchProjectEvents`: SSE projection invalidation/runtime events |

Operator CLI là surface song song của cùng contract, không phải HTTP client bắt buộc và không phải
authority mới. Inventory canonical được V6-15O kiểm theo đúng bốn chiều
`UI action/query ↔ HTTP operationId ↔ aw command ↔ public application command/query`. Bootstrap/static
asset của browser là ngoại lệ không cần CLI; SSE map thành `aw events watch`. Mọi public operation mới
do ma trận UX V6-00 phát hiện phải có lệnh `aw` trước khi V6 đóng. Không có `aw ... set-status`: Kanban
chỉ dispatch named valid action do server trả về, và CLI gọi đúng action đó qua cùng command handler.

CLI mutation giữ nguyên scope, expected version và idempotency envelope. Nếu operator bỏ
`--idempotency-key`, `aw` sinh key trước dispatch và luôn trả lại để retry; key tường minh được giữ nguyên.
`--wait` chỉ theo dõi query/event sau khi command bất đồng bộ được accept; nó không chạy worker inline.
Không `--wait` trả đúng một accepted envelope; có `--wait` chỉ trả một final envelope. Timeout/Ctrl-C
chỉ dừng observer, trả operation reference + last observed state và không dispatch cancellation. Lệnh hữu hạn có
`--json` machine-readable ổn định; artifact stream dùng `--output`, SSE-equivalent dùng NDJSON, còn
process/help không tạo JSON giả. Secret không đi qua argv/output. CLI không expose scheduler/worker
command internal và không có push/PR/merge/force-push.

Trong `--json`, success/error đều phát đúng một document trên stdout; diagnostic/progress ở stderr.
Human mode dùng stdout cho result, stderr cho error/progress. Command có impact cao chỉ được prompt ở
TTY human mode; non-interactive/`--json` phải truyền `--yes`, nếu thiếu thì fail trước dispatch bằng
typed `PRECONDITION_FAILED` với detail `confirmation=required`.

Theo ADR-025 và refinement ADR-028, tập installation-scoped là danh sách đóng: `GET/POST /projects`,
`/health/*`, `/doctor`, `/settings/safe`, `/adapter-builds*` và **nhánh global** `/definitions*`.
Nhánh definition dưới `/projects/{id}` là project-scoped; caller không được gửi scope mâu thuẫn với
route/Definition target. Mọi route còn lại là project-scoped — bao gồm `/runs/{id}/diagnostics`, vốn
là diagnostics của một Run thuộc một Project chứ không phải của installation.

`GET /runs/{id}/timeline` và `/diagnostics` là read models bounded: pagination bằng stable cursor,
không stream toàn bộ agent event, và không trả PID/argv/cwd/secret. Đăng ký adapter build không bao giờ
repin run đang chạy; muốn dùng build mới phải republish Workflow/Agent Profile.

SSE chỉ mang redacted summary và IDs; artifact/output lớn được fetch riêng. Reconnect dùng
JournalPosition project-scoped làm event ID. Cursor quá cũ trả typed resync response; gap/poison event
không bị bỏ qua âm thầm. UI refresh query sau invalidation; không coi browser event cache là authority.

## 10. Provider và executable protocol

`AgentExecutor` giữ capability discovery, `Start`, `Cancel`; interface có thể giữ `Resume` để adapter
contract tương lai nhưng Alpha orchestrator luôn `Start` Attempt mới. Execution request pin attempt,
context snapshot, mounts, scope, execution profile, immutable AdapterBuildVersion, isolation profile,
deadline, env allowlist và idempotency key. Build drift hoặc capability mismatch bị admission từ chối
trước spawn.

Claude/Codex adapter:

- spawn executable trực tiếp với argv, không shell string;
- parse bounded JSONL/protocol stream;
- normalize lifecycle/tool/message/usage/diagnostic event;
- giữ raw redacted output ở ArtifactStore khi policy cho phép;
- không route workflow, mở scope hoặc ghi domain state;
- cancel theo OS process tree policy;
- fail closed khi malformed output, event sequence sai hoặc capability mismatch.

Command/Gate definition không nhận free-form shell. Template chỉ thay placeholder đã khai báo thành
argv element riêng. Cwd phải resolve từ WorkspaceHandle + repository target. Environment là allowlist;
secret chỉ resolve ở worker ngay trước spawn và không persist.

Mỗi mutating Attempt mặc định có đúng một mount `READ_WRITE`; chỉ capability
`INTEGRATION_MULTI_REPOSITORY_WRITE` mới cho nhiều mount ghi. Checker/gate chỉ có read-only exact
RevisionSet/ReleaseSet và scratch directory tách source. `ENFORCED_ISOLATED` chỉ dùng khi filesystem/
network được cưỡng chế; nếu môi trường chỉ hậu kiểm diff thì profile phải là `OPERATOR_TRUSTED_LOCAL`,
và profile đó chỉ hợp lệ khi definition/policy pin tường minh.

Worker không bao giờ auto-downgrade profile đã pin. Không cưỡng chế được profile thì admission trả
`ISOLATION_ENFORCEMENT_UNAVAILABLE` và Attempt bị block **trước** `ProcessSupervisor.Start`; test assert
process spawn count bằng 0.

## 11. Projection và UI screens

Projection consumer quét domain events theo global, monotonic, non-gapless JournalPosition rồi lọc
Project và cập nhật read models idempotently.
Rebuild snapshot authoritative state tại watermark rồi replay event sau watermark. Poison event đưa
projection sang `DEGRADED/STALE` kèm error/action; không nhảy cursor. Orchestrator không query
projection để quyết định.

Screen Alpha framework-neutral:

1. **First-run/Doctor:** gọi Doctor API cho database/artifact/workspace roots, Git, Claude/Codex,
   adapter build, isolation profile và lỗi sửa được. Hiển thị trạng thái registered/unregistered của
   từng adapter build và cung cấp action probe/đăng ký; observed fingerprint không được trình bày như
   registry admission.
2. **Project list/detail:** repositories có trạng thái onboarding/probe/retry, components, health và
   link Kanban.
3. **Kanban:** BACKLOG/READY/ACTIVE/BLOCKED/DONE/CANCELLED; filter repository/component; drag/drop
   phát typed command và có thể bị policy từ chối.
4. **Create WorkItem:** behavior, acceptance, workflow version, READ/WRITE repository/path scope.
5. **Task detail — Overview:** intent, status, blocker, family, run, next valid actions.
6. **Task detail — Graph/Timeline:** pinned graph overlay, node/attempt, route, retry, checkpoint.
7. **Task detail — Workspace:** repository tabs, source/diff/log read-only, base/current revision,
   lease, quarantine/reconcile và ReleaseSet/local commit. Không có interactive terminal.
8. **Task detail — Evidence:** criteria/verdict, command output, artifacts và exact RevisionSet.
9. **Task detail — Chat:** canonical messages và verified attachment; approve/cancel/scope actions là
   control riêng, không suy từ message text.
10. **Definitions:** list/version/diff, YAML/JSON editor, validate errors có location, publish confirm.
11. **Skills/Layers/Packs/Agents/Executables:** version registry và dependency/compatibility view.
12. **Run diagnostics:** queue/job/lease/fence/provider/workspace state, recovery action và correlation
    IDs từ `GET /runs/{id}/diagnostics`; không hiển thị PID, argv, cwd hay secret.
13. **Settings:** gọi safe settings API cho local roots, retention class, provider executable/model
    defaults; secret chỉ nhập bằng reference, không hiển thị lại value.

UI phải hỗ trợ keyboard cơ bản, focus/error state, loading/empty/stale projection state và không
render raw artifact như trusted HTML.

## 12. Configuration, security và local operations

Bootstrap resolve `DatabasePath` trước theo `safe defaults < config file < environment < CLI flags`, rồi
open/migrate SQLite. Với field thuộc safe-settings allowlist, startup merge theo precedence
`safe defaults < config file < SQLite desired settings < environment < CLI flags`; environment/flag
override được báo bằng `maskedByStartupSource`. Effective config bất biến trong một process; PUT chỉ đổi
desired version và báo `restartRequired`, restart mới áp dụng. LocalPrincipal, session/signing key,
DatabasePath, WorkerID và raw secret value không thuộc mutable surface. Config gồm SQLite path, roots,
worker concurrency, lease TTL/heartbeat, provider executable/argv/model, process/output limits, retention
và log level.

- Chỉ bind loopback; external bind bị từ chối, Host/Origin được validate và mutation cần per-start
  local session token không xuất hiện trong URL/log/durable state.
- Deny executable/network/secret ngoài published policy.
- Khai trung thực `ENFORCED_ISOLATED` hoặc `OPERATOR_TRUSTED_LOCAL`; diff hậu kiểm không phải sandbox.
- Repository locator được canonicalize và không nằm dưới managed workspace root.
- Artifact/workspace paths phải nằm trong configured root; chống traversal/symlink/reparse escape.
- Remote Git mutation không được cung cấp trong Alpha workflow executor.
- UI không cung cấp interactive terminal; source/diff/log đều read-only qua bounded API.
- Log structured, redacted, có correlation; không dùng log để recovery.
- Graceful shutdown dừng claim mới, cancel/await theo grace period, giữ lease/checkpoint hợp lệ.

## 13. System acceptance journeys

1. Publish workflow V1, publish V2, run cũ tiếp tục V1.
2. Tạo Project hai repo; hai root task có worktree riêng; child reuse family workspace.
3. Chạy agent mutating node, kill worker ở fault boundary, restart và reconcile không duplicate side
   effect.
4. Agent nói done nhưng required gate thiếu/fail: WorkItem không DONE.
5. Provider session ref bị mất: Attempt mới dựng context và tiếp tục bằng Start.
6. Claude/Codex fake contract tạo cùng normalized domain result.
7. Scope violation bị chặn trước fenced finalize và thấy evidence trong UI.
8. Approval/rework/fork/join giữ đúng route qua restart.
9. Xóa projection rồi rebuild từ watermark + JournalPosition cho cùng view; poison event hiện
   `DEGRADED/STALE` thay vì bị bỏ qua.
10. Tamper artifact làm verify fail; secret fixture không xuất hiện trong DB/event/search artifact.
11. Toàn journey vận hành được từ browser, không cần sửa SQLite/Git thủ công.
12. Semantic suite pass Windows/Linux và race detector pass ở CI hỗ trợ.
13. Host/Origin sai, mutation thiếu session token và external bind đều bị từ chối.
14. Scope expansion chỉ cấp activation mới; multi-repo write thiếu capability và checker write source
    đều bị chặn.
15. END dừng ở `VERIFYING`; CompletionPolicy kiểm exact ReleaseSet đã seal rồi mới atomically hoàn tất
    Run và WorkItem. Alpha tạo local commit nhưng từ chối push/PR/merge/force-push trước Git adapter.
15A. CompletionDecision `REWORK` không có rework edge hợp lệ trả `BLOCK`; không có route nào được dựng
    ngoài graph đã pin.
16. Periodic recovery reaper phát hiện lease hết hạn sau startup mà không tạo recovery job trùng.
17. Retention sweep không xóa canonical conversation, referenced context, held artifact hoặc audit
    metadata.
18. `CancelRun` giữa mutating attempt: Run đi qua `CANCELLING`, process tree dừng, WriteLease chỉ
    release sau xác nhận, outcome chưa xác định thành `INDETERMINATE` + `QUARANTINED`, và workspace/
    ReleaseSet không bị tự cleanup hay abandon.
19. WAIT signal gửi hai lần chỉ được consume một lần; timer job đến hạn không thay thế signal authority.
20. Nâng cấp provider CLI: build mới bị admission từ chối cho tới khi operator probe/đăng ký và
    republish; run đang chạy giữ nguyên build đã pin.
21. Môi trường không cưỡng chế được `ENFORCED_ISOLATED` trả `ISOLATION_ENFORCEMENT_UNAVAILABLE` với
    process spawn count bằng 0; không có auto-downgrade.
22. Event schema version cũ vẫn replay được sau khi schema mới được đăng ký; xóa decoder đang được
    fixture tham chiếu làm CI fail.
