# Thiết kế hệ thống Agent Kit Alpha

> Trạng thái: DRAFT — chờ product owner review.
>
> Authority: ADR-001…010 và Go core spec. Tài liệu này chi tiết hóa Alpha, không supersede ADR.
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
Browser
  -> localhost HTTP API + SSE
       -> application commands / queries
       -> orchestrator / scheduler
       -> SQLite transaction + durable jobs + events
       -> embedded worker loop
            -> Git worktree adapter
            -> process supervisor
            -> Claude/Codex adapter
            -> command/gate executor
            -> filesystem artifact store
```

Một binary `agentkit` có ba mode:

- `serve`: HTTP API, projection consumer và worker cùng process; đây là mode Alpha mặc định.
- `worker`: chạy worker loop riêng trên cùng máy/SQLite để fault test; không phải distributed mode.
- `definition|doctor|evidence`: CLI command dùng cùng application/composition contracts.

Không có fast path từ HTTP handler sang Git/process/provider. Mọi side effect đi qua durable job,
ExecutionAttempt và fencing protocol dù tất cả module nằm cùng process.

## 3. Boundary module và package Go

```text
cmd/agentkit/                   composition root và CLI
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

Mỗi kind có mutable `Definition` và immutable `Version`. Publish thực hiện strict decode,
canonicalize, validate dependency, compute SHA-256 và insert version trong một transaction. Cùng
definition/hash trả version hiện có; hash khác tăng `version_no` đúng một.

### 4.2 Workflow schema Alpha

Authoring hỗ trợ YAML và JSON, strict unknown-field/duplicate-key rejection. Canonical snapshot là
JSON UTF-8. Node type:

- `START`, `END`;
- `AGENT`, `COMMAND`, `MACHINE_GATE`;
- `APPROVAL`, `WAIT`, `ROUTER`;
- `FORK`, `JOIN`.

Không có preset bắt buộc. Publish phải pin exact version/hash của Block, Agent Profile, Command,
Gate, Skill/Layer/Pack và policy được tham chiếu. Version range chỉ được dùng lúc authoring resolution;
runtime manifest không giữ range.

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

Root tạo family/workspace; child reuse family/workspace và chỉ thu hẹp scope. Một run pin
WorkflowVersion, scope version, ExecutionManifest và base RevisionSet.

### 5.2 Transition chuẩn

`WorkItem`:

```text
BACKLOG -> READY -> ACTIVE -> BLOCKED -> ACTIVE -> DONE
                   |             |
                   +----------> CANCELLED
```

- `READY` cần behavior, acceptance, verification và valid scope.
- `ACTIVE` cần WorkspaceSet ready và run started.
- `DONE` chỉ từ completion service với terminal run, evidence/policy/join/clean gate đạt.

`WorkflowRun`:

```text
CREATED -> RUNNING <-> WAITING
             |  ^       |
             v  |       v
           BLOCKED ----+
             |
             +-> SUCCEEDED | FAILED | CANCELLED
```

`NodeRun`: `PENDING -> READY -> QUEUED -> RUNNING`, sau đó `WAITING|BLOCKED|SUCCEEDED|FAILED|
SKIPPED|CANCELLED`. Rework tạo activation mới; retry kỹ thuật tạo Attempt mới.

`ExecutionAttempt`: `QUEUED -> RUNNING -> SUCCEEDED|FAILED|TIMED_OUT|CANCELLED|LOST|INDETERMINATE`.
Terminal attempt không quay lại RUNNING. Mutating attempt mất ownership là `INDETERMINATE` và
workspace `QUARANTINED` cho tới reconcile.

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

## 6. SQLite data design

Mọi timestamp là RFC3339Nano UTC; ID là application-generated text; mutable aggregate có `version`;
foreign key luôn bật. JSON column lưu canonical validated JSON text. Artifact/log/diff lớn không nằm
inline trong state table.

### 6.1 Catalog và work

| Table | Key/field chính | Constraint quan trọng |
|---|---|---|
| `projects` | id, name, status, version, timestamps | name không rỗng |
| `repositories` | id, project_id, name, local_locator, default_ref, status, version | unique project+name |
| `components` | id, project_id, repository_id, name, path, kind, version | path chuẩn `/`; unique repo+path |
| `work_items` | id, project_id, parent_id, family_id, kind, title, behavior, acceptance_json, status, version | parent/family cùng project |
| `task_families` | id, project_id, root_work_item_id, workspace_set_id, scope_version, status, version | unique root và workspace set |
| `family_repository_scopes` | family/project/repository/scope_version/access/paths/reason/actor/time | append-only, add-only |
| `work_item_effective_scopes` | work_item/repository/access/paths/scope_version | subset family scope |
| `blockers` | id, work_item/run/node, type, reason, status, created/resolved | append resolution audit |
| `scope_expansion_requests` | id, family, requested grants, reason, status, actor/version | only add/upgrade; approval required |

### 6.2 Definitions

Mỗi definition kind dùng cặp `<kind>_definitions` và `<kind>_versions`; không dồn mọi payload vào một
bảng polymorphic khó enforce. Alpha có các cặp cho workflow, block, skill, layer, engineering pack,
agent profile, command, gate và policy.

Definition table: `id, project_id nullable, name, status, version, timestamps`, unique scope+name.
Version table: `id, definition_id, version_no, schema_version, canonical_content, content_hash,
dependency_manifest_json, published_by, published_at`, unique definition+version_no và
definition+hash; không có update/delete public operation.

`definition_dependency_pins` materialize `owner_version_id, dependency_kind, dependency_version_id,
content_hash` để audit/query và reject cross-project/incompatible pin.

### 6.3 Workspace/runtime

| Table | Nội dung chính |
|---|---|
| `workspace_sets` | family unique, state/version |
| `repository_workspaces` | workspace/repo/generation/opaque locator/base/current revision/state/version; unique family+repo+generation |
| `workflow_runs` | project/work/version/family/scope/state/shared-state/version/times |
| `execution_manifests` | run unique, dependency pins, execution/context/policy hashes, base RevisionSet; immutable |
| `node_runs` | run/node/activation/iteration/state/effective scope/input/profile/outcome/version |
| `branch_tokens` | run/fork/branch/current node/state/version; unique run+fork+branch |
| `execution_attempts` | node/attempt number/state/provider/profile/context/input revision/checkpoint/termination/version |
| `durable_jobs` | aggregate/payload ref/state/schedule/claim/lease/idempotency/version |
| `write_leases` | workspace generation/fence/job token/attempt/owner/expiry |
| `checkpoints` | attempt/sequence/context/revisions/shared-state/artifact refs; immutable |
| `agent_events` | attempt/sequence/kind/schema/redacted payload/artifact refs; append-only |
| `approvals` | target/decision/actor/reason/evidence/time; append-only |

### 6.4 Conversation/evidence/audit

| Table | Nội dung chính |
|---|---|
| `conversations` | project/work item, created time |
| `conversation_messages` | conversation/attempt/actor/role/content artifact/time; append-only |
| `context_snapshots` | attempt unique active snapshot, message/resource/revision manifest/hash; immutable |
| `artifacts` | project/kind/URI/hash/size/media/sensitivity/redaction/retention/time |
| `evidence` | assertion/verdict/work/run/node/attempt/policy/revision/artifact manifest/time |
| `domain_events` | aggregate sequence/type/schema/correlation/causation/redacted payload/time |
| `outbox` | event/topic/payload/state/availability/lease; unique event |
| `command_receipts` | actor/project/idempotency/type/request hash/result/error/time |
| `projection_cursors` | projection name, event cursor/version/time |
| `kanban_projection` | project/work/status/title/repository summary/blocker/version |
| `task_detail_projection` | work item, materialized summary JSON, source event cursor |

### 6.5 Migration rules

- Chuyển migration inline hiện tại thành numbered SQL files trước schema Alpha mới.
- Migration đã apply không sửa; startup verify checksum.
- Migration test chạy database trống, database ở version trước và restart idempotency.
- Cross-project invariant kiểm bằng composite key/FK khi hợp lý và transaction validation khi SQLite
  không biểu diễn được constraint.
- Không `INSERT OR REPLACE`, không hard-delete audit/runtime history.

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

## 8. Artifact, context và evidence

Artifact store dùng content-addressed path dưới configured root, write temp + fsync/atomic rename,
SHA-256 verify và metadata SQLite. Locator là opaque, API chỉ mở artifact theo ID đã authorize.

Context assembler nhận WorkItem, node, effective scope, pinned definitions, messages và RevisionSet;
resolve selector/priority/conflict/budget; persist exact manifest trước provider start. Hai hard
constraint cùng scope mâu thuẫn làm node `BLOCKED/NEEDS_INFO`, không last-wins.

Evidence verdict: `PASS|FAIL|ERROR|NOT_RUN|NOT_APPLICABLE`. `NOT_APPLICABLE` cần policy và reason.
Command evidence giữ executable/version, argv đã redact, cwd target, exit code, time, tool version,
RevisionSet và output artifact hash. Completion chỉ đọc durable evidence metadata đã verify.

Retention Alpha: evidence/artifact local 7 ngày theo ADR; sweeper chỉ xóa artifact hết hạn không còn
retention hold và không xóa state/audit metadata. Secret fixture phải được redact trước persist.

## 9. HTTP API Alpha

Base path `/api/v1`. JSON dùng camelCase, UTC timestamp, opaque string IDs. Mutation yêu cầu
`Idempotency-Key`; update aggregate yêu cầu `If-Match: <version>`. Error envelope:

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

Route chính:

| Method/path | Contract |
|---|---|
| `GET /health/live`, `/health/ready` | process và DB/migration/worker readiness |
| `GET/POST /projects` | list/create project |
| `GET/POST /projects/{id}/repositories` | list/register local repository |
| `GET/POST /projects/{id}/components` | catalog/discover component |
| `GET/POST /projects/{id}/work-items` | Kanban list/create root task |
| `POST /work-items/{id}/children` | child cùng family, subset scope |
| `GET /work-items/{id}` | authoritative detail projection |
| `POST /work-items/{id}/runs` | start run với workflow version |
| `POST /runs/{id}/cancel` | durable cancel intent |
| `GET /runs/{id}/graph` | pinned graph + runtime overlay |
| `POST /families/{id}/scope-expansions` | create request |
| `POST /scope-expansions/{id}/approve|reject` | typed decision |
| `POST /approvals/{id}/approve|reject` | resolve approval node |
| `GET/POST /work-items/{id}/messages` | canonical task chat |
| `GET /work-items/{id}/evidence` | evidence query |
| `GET /artifacts/{id}/content` | verified streaming content |
| `GET /definitions/{kind}` | list definitions/versions |
| `POST /definitions/{kind}/validate` | validate without publish |
| `POST /definitions/{kind}/publish` | publish immutable version |
| `GET /events/stream?projectId=...` | SSE projection invalidation/runtime events |

SSE chỉ mang redacted summary và IDs; artifact/output lớn được fetch riêng. Reconnect dùng event ID.
UI refresh query sau invalidation; không coi browser event cache là authority.

## 10. Provider và executable protocol

`AgentExecutor` giữ capability discovery, `Start`, `Cancel`; interface có thể giữ `Resume` để adapter
contract tương lai nhưng Alpha orchestrator luôn `Start` Attempt mới. Execution request pin attempt,
context snapshot, mounts, scope, execution profile, deadline, env allowlist và idempotency key.

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

## 11. Projection và UI screens

Projection consumer đọc domain events theo cursor và cập nhật read models idempotently. Có command
rebuild toàn bộ; orchestrator không query projection để quyết định.

Screen Alpha framework-neutral:

1. **First-run/Doctor:** database/artifact/workspace roots, Git, Claude/Codex capability và lỗi sửa được.
2. **Project list/detail:** repositories, components, health và link Kanban.
3. **Kanban:** BACKLOG/READY/ACTIVE/BLOCKED/DONE/CANCELLED; filter repository/component; drag/drop
   phát typed command và có thể bị policy từ chối.
4. **Create WorkItem:** behavior, acceptance, workflow version, READ/WRITE repository/path scope.
5. **Task detail — Overview:** intent, status, blocker, family, run, next valid actions.
6. **Task detail — Graph/Timeline:** pinned graph overlay, node/attempt, route, retry, checkpoint.
7. **Task detail — Workspace:** repository tabs, base/current revision, diff, lease, quarantine.
8. **Task detail — Evidence:** criteria/verdict, command output, artifacts và exact RevisionSet.
9. **Task detail — Chat:** canonical messages; approve/cancel/scope actions là control riêng, không
   suy từ message text.
10. **Definitions:** list/version/diff, YAML/JSON editor, validate errors có location, publish confirm.
11. **Skills/Layers/Packs/Agents/Executables:** version registry và dependency/compatibility view.
12. **Run diagnostics:** queue/job/lease/provider failure, recovery action và correlation IDs.
13. **Settings:** local roots, retention, provider executable/model defaults; secret chỉ nhập bằng
    reference, không hiển thị lại value.

UI phải hỗ trợ keyboard cơ bản, focus/error state, loading/empty/stale projection state và không
render raw artifact như trusted HTML.

## 12. Configuration, security và local operations

Precedence: safe defaults < config file < environment < CLI flags. Startup validate toàn bộ. Config
gồm SQLite path, roots, worker concurrency, lease TTL/heartbeat, provider executable/argv/model,
process/output limits, retention và log level.

- Bind localhost mặc định; external bind không nằm trong Alpha.
- Deny executable/network/secret ngoài published policy.
- Repository locator được canonicalize và không nằm dưới managed workspace root.
- Artifact/workspace paths phải nằm trong configured root; chống traversal/symlink/reparse escape.
- Remote Git mutation không được cung cấp trong Alpha workflow executor.
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
9. Xóa projection rồi rebuild cho cùng view tại cùng event cursor.
10. Tamper artifact làm verify fail; secret fixture không xuất hiện trong DB/event/search artifact.
11. Toàn journey vận hành được từ browser, không cần sửa SQLite/Git thủ công.
12. Semantic suite pass Windows/Linux và race detector pass ở CI hỗ trợ.
