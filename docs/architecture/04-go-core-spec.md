# Đặc tả Go core cho Agent Kit

> Trạng thái: specification baseline cho Go spike; chưa phải mã triển khai.
>
> Từ khóa `MUST`, `MUST NOT`, `SHOULD`, `MAY` mang nghĩa bắt buộc, cấm, khuyến nghị và tùy chọn.
> Nếu tài liệu này xung đột với ADR đã accepted thì ADR có quyền cao hơn và thay đổi phải đi qua ADR
> superseding.

Tài liệu này cụ thể hóa:

- [Start here / handoff hiện tại](../00-start-here.md);
- [Mô hình Project, Repository, TaskFamily và WorkspaceSet](01-project-repository-workspace-model.md);
- [Architecture decisions](02-architecture-decisions.md);
- [Kiến trúc hệ thống Agent Kit](03-system-architecture.md).

Tài liệu `03-system-architecture.md` mô tả topology tổng thể; spec này chỉ khóa contract có thể triển
khai và kiểm thử của Go core/spike. Spec không phụ thuộc việc UI, HTTP transport hay beta control
plane đã tồn tại.

## 1. Mục tiêu và phạm vi spike

Go core phải chứng minh được năm năng lực trước khi bắt đầu alpha UI/runtime:

1. Publish `WorkflowVersion` bất biến và chạy đúng version đã pin.
2. Dừng process rồi tiếp tục từ durable state mà không chạy lại transition đã commit.
3. Quản lý `Project -> Repository[]` và `TaskFamily -> WorkspaceSet` đúng invariant.
4. Chạy song song an toàn bằng durable job, lease, fencing và workspace isolation.
5. Chạy Claude CLI và Codex CLI qua cùng một `AgentExecutor` contract.

Spike là một modular monolith: một binary có application service, scheduler và embedded worker;
SQLite là persistence adapter. Boundary phải đủ rõ để beta tách API/control plane, worker và đổi sang
PostgreSQL mà không đổi domain semantics.

Ngoài phạm vi spike:

- UI, đăng nhập, organization/team và phân quyền beta;
- visual workflow editor;
- đồng bộ dữ liệu alpha với beta;
- push, pull request hoặc merge tự động;
- distributed scheduler production-grade;
- migration một run đang chạy sang WorkflowVersion khác;
- Go dynamic plugin và thực thi code nằm trong Skill/Layer.

## 2. Cấu trúc module và luật phụ thuộc

Module Go dự kiến là `github.com/taQuangLing/agent-workflow`. Nếu repository/module đổi tên, chỉ module
path được đổi; package boundary dưới đây vẫn giữ nguyên.

```text
cmd/
  agentkit/                  composition root, CLI cho spike
internal/
  domain/
    project/                 Project, Repository
    work/                    WorkItem, TaskFamily, scope
    workflow/                definition/version/compiler
    runtime/                 WorkflowRun, NodeRun, Attempt, routing
    workspace/               WorkspaceSet, RepositoryWorkspace, RevisionSet
    conversation/            message/context snapshot
    evidence/                artifact/evidence metadata
  app/
    command/                 command DTO và handlers
    query/                   query DTO và handlers
    ports/                   persistence, executor, workspace, clock, IDs
    scheduler/               readiness, retry, rework, completion
    worker/                  claim/execute/checkpoint/finalize job
  adapters/
    persistence/sqlite/
    persistence/postgres/    có thể chỉ là contract-test target sau spike
    workspace/gitworktree/
    process/
    provider/claude/
    provider/codex/
    artifact/filesystem/
  protocol/
    agent/v1/                canonical provider protocol nếu tách process
migrations/
  sqlite/
  postgres/
testdata/
  workflows/
  providers/
  repositories/
```

Luật phụ thuộc bắt buộc:

```text
domain <- app <- adapters <- cmd
```

- `domain` chỉ dùng Go standard library; không import SQL, Git, OS process, CLI provider hoặc HTTP.
- `app` chỉ phụ thuộc domain và interface trong `app/ports`.
- Adapter implement port; adapter này không import adapter khác, trừ composition code tại `cmd`.
- `cmd` là nơi duy nhất chọn SQLite, filesystem artifact store, Git adapter và provider adapter cụ thể.
- Không tạo package `common`, `utils`, `models` hoặc một `Store` tổng hợp che giấu mọi concern.
- Chỉ export type thực sự đi qua package boundary; implementation để trong `internal`.
- Domain không chứa path theo OS, SQL JSON shape, tên `.claude`, provider session format hoặc nhánh
  `if provider == ...`.

## 3. Quy ước identity, time và version

Mỗi identity là một named type riêng trong Go, ví dụ `ProjectID`, `RepositoryID`, `WorkItemID`; không
truyền ID dưới dạng `string` chung giữa domain API. ID được tạo ở application boundary qua `IDSource`
và lưu dạng text ổn định. Spike MAY dùng ULID/UUID nhưng domain không phụ thuộc thuật toán tạo ID.

- Timestamp là UTC instant; adapter serialize SQLite bằng RFC3339Nano và PostgreSQL bằng `timestamptz`.
- So sánh lease expiry dùng database time, không dùng đồng hồ riêng của worker.
- Mọi aggregate mutable có `Version uint64`, bắt đầu từ 1 và tăng đúng một sau mỗi mutation thành công.
- Mọi command mutation mang `CommandID`, `IdempotencyKey`, `Actor`, `CorrelationID` và
  `ExpectedVersion` khi sửa aggregate đã tồn tại.
- Repository identity là ID đã đăng ký, MUST NOT suy từ path, slug, current directory hoặc remote URL.
- Hash canonical dùng `sha256:<lowercase-hex>`.

## 4. Domain type tối thiểu

### 4.1 Project và Repository

```text
Project {
  ID, Name, Status(ACTIVE|ARCHIVED), Version
}

Repository {
  ID, ProjectID, Name, VCSKind(GIT), RemoteLocator,
  DefaultRef, Status(ACTIVE|DISABLED), Version
}
```

Một Project có thể có nhiều Repository. `RemoteLocator` là cấu hình của adapter và phải được redact
khi có credential; domain không sử dụng nó làm identity.

### 4.2 WorkItem, TaskFamily và scope

```text
WorkItem {
  ID, ProjectID, Kind(ROOT|CHILD), ParentID?, FamilyID,
  Title, Status(BACKLOG|READY|ACTIVE|BLOCKED|DONE|CANCELLED), Version
}

TaskFamily {
  ID, ProjectID, RootWorkItemID,
  ScopeVersion, Status(ACTIVE|BLOCKED|COMPLETED|CANCELLED), Version
}

RepositoryScope {
  FamilyID, AddedInScopeVersion, RepositoryID, Access(READ|WRITE),
  PathScope[], Reason, AddedBy, AddedAt
}
```

`PathScope` là path tương đối chuẩn hóa bằng dấu `/`, không chứa `..`, path tuyệt đối hoặc symlink
escape. Scope family là add-only khi family đang chạy; nâng READ thành WRITE hoặc mở rộng path tạo
grant mới ở `AddedInScopeVersion`, không sửa grant lịch sử. Scope hiệu lực của child/node/attempt
luôn là tập con của scope family tại một `ScopeVersion` cụ thể.

### 4.3 Workspace

```text
WorkspaceSet {
  ID, ProjectID, FamilyID,
  State(REQUESTED|PROVISIONING|READY|BLOCKED|RELEASING|RELEASED|FAILED), Version
}

RepositoryWorkspace {
  ID, WorkspaceSetID, RepositoryID, Generation,
  Locator, BranchRef?, BaseRevision, CurrentRevision?,
  State(PROVISIONING|READY|QUARANTINED|RELEASING|RELEASED|FAILED), Version
}

Revision {
  RepositoryID, VCSObjectID, WorkspaceGeneration
}

RevisionSet {
  Entries[]Revision, ContentHash
}
```

`Locator` là opaque value chỉ workspace adapter hiểu; domain không nối locator thành filesystem path.
`RevisionSet` bất biến, sắp xếp duy nhất theo `RepositoryID`, không có hai entry cho cùng repository và
luôn pin exact VCS object ID hoặc một content snapshot ID do adapter phát hành. Nó không biểu diễn
transaction nguyên tử xuyên repository.

### 4.4 Workflow authoring và version

```text
WorkflowDefinition {
  ID, ProjectID?, Name, Status(DRAFT|ACTIVE|ARCHIVED), Version
}

WorkflowVersion {
  ID, DefinitionID, VersionNumber, SchemaVersion,
  CanonicalContent, ContentHash, DependencyManifest,
  PublishedBy, PublishedAt
}
```

`WorkflowVersion` không có update operation. `DependencyManifest` pin version/hash của Block,
CommandDefinition, GateDefinition, execution profile và Skill/Layer resource mà graph sử dụng.

### 4.5 Runtime

```text
WorkflowRun {
  ID, ProjectID, WorkItemID, WorkflowVersionID,
  FamilyID, ScopeVersion, State,
  SharedState, Version, StartedAt?, FinishedAt?
}

NodeRun {
  ID, RunID, NodeKey, ActivationSequence, Iteration,
  State, EffectiveScope, InputStateHash, BlockVersionID?, ExecutionProfileHash,
  SelectedOutcome?, BlockReason?, Version
}

ExecutionAttempt {
  ID, NodeRunID, AttemptNumber,
  State, ProviderKey?, ExecutionProfileHash,
  ContextSnapshotID?, InputRevisionSet?, LastCheckpointID?,
  StartedAt?, FinishedAt?, TerminationReason?, Version
}
```

State chuẩn:

- `WorkflowRun`: `CREATED`, `RUNNING`, `WAITING`, `BLOCKED`, `SUCCEEDED`, `FAILED`, `CANCELLED`.
- `NodeRun`: `PENDING`, `READY`, `QUEUED`, `RUNNING`, `WAITING`, `BLOCKED`, `SUCCEEDED`, `FAILED`,
  `SKIPPED`, `CANCELLED`.
- `ExecutionAttempt`: `QUEUED`, `RUNNING`, `SUCCEEDED`, `FAILED`, `TIMED_OUT`, `CANCELLED`, `LOST`,
  `INDETERMINATE`.

`INDETERMINATE` là tên canonical cho tình huống thường được mô tả là `UNKNOWN`: side effect có thể đã
xảy ra nhưng core chưa có đủ evidence để kết luận.

Một node có thể được activate nhiều lần do rework cycle; mỗi activation tạo `NodeRun` mới với
`ActivationSequence` mới. Technical retry giữ nguyên `NodeRun` nhưng MUST tạo `ExecutionAttempt` mới
và tăng `AttemptNumber`.

### 4.6 Conversation, context, artifact và evidence

```text
ConversationMessage {
  ID, ProjectID, WorkItemID, AttemptID?, Actor, Role,
  ContentArtifactID, CreatedAt
}

ContextSnapshot {
  ID, ProjectID, WorkItemID, AttemptID,
  MessageRefs[], ResourceRefs[], RevisionSet,
  ManifestHash, CreatedAt
}

ArtifactRef {
  ID, ProjectID, Kind, URI, ContentHash, Size, MediaType,
  Sensitivity, CreatedAt
}

Evidence {
  ID, ProjectID, WorkItemID, RunID, NodeRunID, AttemptID,
  Kind, Verdict, ArtifactRefs[], RevisionSet, PolicyVersion, CreatedAt
}
```

Conversation/context thuộc platform. Provider session reference chỉ là metadata mã hóa để tối ưu
resume; nó không thay thế `ContextSnapshot`.

## 5. Invariant bắt buộc

Ngoài invariant trong tài liệu mô hình workspace, Go core MUST giữ các điều sau:

1. Root WorkItem tạo đúng một TaskFamily; child có cùng `ProjectID` và `FamilyID` với parent.
2. Mọi RepositoryScope/RepositoryWorkspace/Revision của family thuộc cùng Project.
3. Mỗi `WorkspaceSetID + RepositoryID + Generation` có tối đa một RepositoryWorkspace.
4. Hai TaskFamily không chia sẻ mutable RepositoryWorkspace.
5. Child/node/attempt không được mở rộng quyền vượt scope family đã phê duyệt.
6. WorkflowRun pin một WorkflowVersion, ScopeVersion và dependency manifest trong suốt lifetime.
7. Publish version mới không thay graph hoặc dependency của run cũ.
8. Mỗi NodeRun pin input hash, effective scope và execution profile trước attempt đầu tiên.
9. Retry kỹ thuật luôn tạo attempt mới; dữ liệu attempt cũ là append-only ngoại trừ state transition.
10. Business rework chỉ đi qua edge có trong WorkflowVersion.
11. Outcome từ agent/command phải thuộc allow-list của node trước khi routing.
12. Node/attempt success không tự làm WorkItem DONE; completion policy và required evidence quyết định.
13. `NOT_RUN`, thiếu evidence hoặc verifier error không được quy thành `PASS`.
14. Mutation từ UI, provider hoặc adapter không được ghi domain table trực tiếp.
15. Mọi state transition tạo domain event trong cùng database transaction.
16. Transition tạo side effect phải enqueue durable job/outbox trong cùng transaction.
17. Kết quả worker chỉ được accept khi JobLease và mọi WriteLease liên quan còn đúng fencing token.
18. Worker mất lease không được commit success dù external process trả exit code 0.
19. Multi-repository write lease được acquire all-or-none theo thứ tự RepositoryID ổn định.
20. Mọi evidence liên quan code pin exact RevisionSet và artifact content hash.

## 6. Workflow document và compiler

Authoring file dùng YAML hoặc JSON nhưng compiler phải decode vào cùng một strict schema. Unknown
field, duplicate key, implicit type mơ hồ và reference không resolve đều là publish error. Runtime chỉ
đọc canonical snapshot trong database, không đọc lại authoring file.

Node type nền của spike:

| Type | Ý nghĩa |
|---|---|
| `START` | Điểm vào duy nhất, không thực thi |
| `END` | Terminal có outcome và completion requirement |
| `AGENT` | Chạy qua AgentExecutor |
| `COMMAND` | Chạy CommandDefinition đã đăng ký |
| `MACHINE_GATE` | Verifier deterministic, tạo Evidence |
| `APPROVAL` | Chờ operator decision |
| `ROUTER` | Chọn outcome từ typed state bằng rule deterministic |
| `FORK` | Phát hành các branch token |
| `JOIN` | Gom token theo `ALL`, `ANY` hoặc `QUORUM` |
| `WAIT` | Chờ signal/timer bền vững |

`SERVICE_CALL`, `SPAWN_WORK_ITEMS` và `SUBFLOW` được để sau spike; khi thêm phải là node type versioned,
không nhét semantics vào free-text config.

Mỗi executable node khai báo tối thiểu:

- stable `nodeKey`, type và responsibility;
- input binding, output schema và shared-state fields được phép ghi;
- allowed outcomes và edge tương ứng;
- `AttemptPolicy`: max attempts, retryable error codes, backoff, timeout;
- effective repository selector và access mode;
- execution profile/permission profile đã pin;
- required evidence/completion condition;
- với cycle: iteration budget và escalation outcome;
- với JOIN: policy, branch set và quorum nếu dùng.

### 6.1 Validation khi publish

Compiler MUST reject graph nếu vi phạm bất kỳ điều nào:

1. ID/node key/edge key trùng hoặc reference không tồn tại.
2. Không có đúng một START, không có END, START có incoming hoặc END có outgoing edge.
3. Có node không reachable từ START hoặc node reachable không có đường đến terminal/escalation.
4. Edge dùng outcome node không khai báo, route trùng hoặc thiếu route cho outcome bắt buộc.
5. Input/output/shared-state schema không tương thích hoặc có nhiều writer mà không có merge rule.
6. Node type thiếu config đặc thù, timeout, permission profile hoặc dependency pin.
7. Strongly connected component có cycle nhưng không có iteration budget và escalation exit.
8. FORK/JOIN không cân bằng, branch identity mơ hồ hoặc quorum ngoài khoảng hợp lệ.
9. Parallel static write scope giao nhau mà graph/policy không serialize hoặc khai integration node.
10. Skill/Layer resource được tham chiếu như executable capability.
11. Command/Gate/provider capability không resolve hoặc không tương thích OS/worker target.
12. Canonical representation/hash không tái tạo giống nhau từ cùng semantic input.

Canonicalization MUST có golden test Windows/Linux. Map key được sort; semantic list giữ thứ tự;
set-like list được normalize/sort; timestamp/publisher metadata không nằm trong content hash. Publish
cùng `DefinitionID + ContentHash` trả lại version hiện có; publish nội dung khác cấp VersionNumber
monotonic mới trong một transaction.

## 7. Runtime scheduling semantics

Scheduler là application service deterministic, không gọi provider hoặc filesystem.

1. `StartWorkflowRun` tạo run, START NodeRun và checkpoint khởi đầu cùng transaction.
2. Scheduler consume persisted transition/event, tính node sẵn sàng từ graph snapshot và branch token.
3. Với executable node, scheduler tạo NodeRun/Attempt/job cùng transaction.
4. Worker thực thi attempt; kết quả được normalize và validate.
5. Commit outcome cập nhật Attempt, NodeRun, shared state, checkpoint, event và downstream job atomically.
6. Retryable technical failure tạo attempt/job mới nếu còn budget.
7. Business outcome chọn edge tường minh; re-entry tạo NodeRun activation mới.
8. JOIN hoàn thành theo policy và persisted branch token, không suy từ queue đang rỗng.
9. Run chỉ `SUCCEEDED` khi đi tới END hợp lệ và completion policy/evidence pass.

Không có background loop quét toàn bộ graph để đoán state. Scheduler MAY dùng durable `ADVANCE_RUN`
job; handler phải idempotent theo run version/transition ID.

## 8. Application command contract

Mọi command có envelope chung:

```text
CommandEnvelope {
  CommandID, IdempotencyKey, Actor, CorrelationID,
  ProjectID, ExpectedVersion?, RequestedAt
}
```

Command public tối thiểu:

| Command | Kết quả chính |
|---|---|
| `CreateProject` | Project mới |
| `RegisterRepository` | Repository thuộc Project |
| `CreateRootWorkItem` | WorkItem + TaskFamily + requested WorkspaceSet |
| `CreateChildWorkItem` | Child cùng family, effective scope là tập con |
| `PublishWorkflowVersion` | Immutable compiled snapshot |
| `StartWorkflowRun` | Run pin workflow/scope/dependency |
| `RequestScopeExpansion` | Block node và tạo request add-only |
| `ApproveScopeExpansion` | Tăng ScopeVersion, enqueue provision job |
| `RejectScopeExpansion` | Ghi decision, route block/escalation |
| `ApproveNode` / `RejectNode` | Resolve APPROVAL bằng actor/evidence |
| `CancelRun` | Durable cancel intent, enqueue cancel job nếu cần |
| `AppendConversationMessage` | Canonical message append-only |
| `ReleaseWorkspaceSet` | Chỉ khi không còn active job/lease |

Command internal của scheduler/worker:

- `ScheduleNodeAttempt`;
- `RecordAgentEvents`;
- `CommitAttemptCheckpoint`;
- `CompleteAttempt`;
- `FailAttempt`;
- `MarkAttemptLost`;
- `AdvanceRun`;
- `RecordWorkspaceProvisioned`;
- `QuarantineWorkspace`;
- `ReconcileWorkspace`.

Handler MUST validate authorization/policy trước mutation, load aggregate theo `ProjectID + ID`, áp
expected version, ghi command receipt và trả result ổn định khi cùng idempotency key được gửi lại.
Push/PR/merge sau spike phải là command riêng; không được ẩn trong `CompleteAttempt`.

## 9. Query contract

Query không mutate state và không giữ domain aggregate sống lâu. Tối thiểu gồm:

- `GetProject`, `ListProjectRepositories`;
- `ListWorkItems` với project/repository/status filter;
- `GetWorkItemDetail` gồm family, scope, active run và blocker;
- `GetRunGraph` gồm pinned version, NodeRun/Attempt và route đã chọn;
- `GetWorkspaceSet` gồm state/generation/revision/lease theo repository;
- `ListEvidence`, `ListArtifacts`;
- `GetConversation`, `GetContextSnapshot`;
- `GetJobDiagnostics` cho operator/spike.

Query luôn scope bằng ProjectID. Projection có cursor/version và có thể rebuild từ authoritative state
tables + events. UI không được suy domain transition từ projection.

## 10. Application ports

Interface dưới đây mô tả trách nhiệm, không khóa tên method cuối cùng.

```go
type UnitOfWork interface {
    Within(ctx context.Context, opts TxOptions, fn func(Tx) error) error
}

type Tx interface {
    Projects() ProjectRepository
    Work() WorkRepository
    Workflows() WorkflowRepository
    Runtime() RuntimeRepository
    Workspaces() WorkspaceRepository
    Conversations() ConversationRepository
    Evidence() EvidenceRepository
    Jobs() JobRepository
    Events() EventRepository
    Receipts() CommandReceiptRepository
}
```

Repository port thao tác aggregate/value object, không expose SQL row, connection, dynamic method hoặc
SQLite transaction. Ngoài transaction có `QueryStore` cho read model.

Các port side-effect:

```go
type AgentExecutor interface {
    Capabilities(ctx context.Context) (AgentCapabilities, error)
    Execute(ctx context.Context, req AgentExecutionRequest, sink AgentEventSink) (AgentExecutionResult, error)
    Cancel(ctx context.Context, ref ProviderExecutionRef) error
}

type WorkspaceProvider interface {
    Provision(ctx context.Context, spec ProvisionSpec) (WorkspaceHandle, error)
    Inspect(ctx context.Context, handle WorkspaceHandle) (WorkspaceInspection, error)
    CaptureRevision(ctx context.Context, handle WorkspaceHandle) (Revision, error)
    Diff(ctx context.Context, handle WorkspaceHandle, base Revision) (ArtifactRef, error)
    Quarantine(ctx context.Context, handle WorkspaceHandle, reason string) error
    Release(ctx context.Context, handle WorkspaceHandle) error
}

type ArtifactStore interface {
    Put(ctx context.Context, meta ArtifactMetadata, body io.Reader) (ArtifactRef, error)
    Open(ctx context.Context, ref ArtifactRef) (io.ReadCloser, error)
    Verify(ctx context.Context, ref ArtifactRef) error
}
```

Port hạ tầng khác:

- `JobQueue`: claim/heartbeat/complete/fail bằng lease token;
- `WriteLeaseManager`: acquire batch/heartbeat/release/validate fence;
- `ProcessSupervisor`: argv array, cwd handle, env allow-list, timeout, cancel và stream;
- `ExecutionProfileResolver`: resolve manifest bất biến thành profile/hash;
- `IDSource`, `Clock`, `SecretResolver`, `EventPublisher`;
- `DefinitionRegistry`: resolve Command/Gate/Block/Skill/Layer version đã pin.

`ProcessSupervisor` MUST NOT nhận shell command string. Nó nhận executable + argv riêng, environment
allow-list và OS-specific cancellation policy. Secret chỉ được resolve tại worker ngay trước execution.

## 11. Transaction và idempotency semantics

### 11.1 Application transaction

Một quyết định domain phải commit trong một database transaction:

```text
load aggregate at expected version
  -> validate invariant/policy
  -> write state rows with compare-and-swap version
  -> append domain events
  -> enqueue durable jobs/outbox
  -> store command receipt/result
commit
```

- Không gọi Git, provider, process, filesystem artifact store hoặc network trong transaction.
- Repository method không tự mở nested transaction.
- Update dùng `WHERE id = ? AND version = ?`; affected row khác 1 là `CONFLICT`.
- Duplicate idempotency key cùng actor/scope/command type trả stored result; cùng key nhưng payload hash
  khác trả `IDEMPOTENCY_CONFLICT`.
- Event sequence monotonic trong từng aggregate; unique `(aggregate_type, aggregate_id, sequence)`.
- Job/outbox được ghi cùng transition tạo ra nó; publisher/executor xử lý ngoài transaction.
- External side effect không có exactly-once guarantee. Core chỉ bảo đảm một fenced result được chấp
  nhận tối đa một lần.

### 11.2 Worker transaction

Claim lease là transaction ngắn. Worker thực thi side effect ngoài transaction. Finalize là transaction
mới và MUST kiểm:

1. Job ID, owner, lease token và lease expiry còn hợp lệ.
2. Attempt đang `RUNNING` đúng expected version.
3. Với mutation: mọi workspace generation/write fencing token còn hợp lệ.
4. Output schema, allowed outcome, scope diff và evidence policy hợp lệ.

Nếu bất kỳ kiểm tra nào fail, kết quả không được đổi NodeRun/Run sang success. Artifact MAY được lưu
với nhãn untrusted/orphan để điều tra.

## 12. Durable job, lease và fencing

`DurableJob` tối thiểu có:

```text
ID, ProjectID, Kind, AggregateRef, PayloadRef, State,
AvailableAt, Priority, ClaimCount, MaxClaims,
LeaseOwner?, LeaseToken, LeaseUntil?, HeartbeatAt?,
IdempotencyKey, LastErrorCode?, CreatedAt, UpdatedAt, Version
```

State: `AVAILABLE`, `LEASED`, `SUCCEEDED`, `FAILED`, `DEAD`, `CANCELLED`.

Claim semantics:

- Chỉ claim `AVAILABLE` đến hạn; recovery chuyển lease hết hạn qua flow riêng.
- Adapter dùng database time và tăng `LeaseToken` atomically mỗi lần cấp quyền.
- Heartbeat/complete/fail có compare-and-swap theo `JobID + LeaseOwner + LeaseToken`.
- Job payload chỉ chứa ID/ref/hash, không chứa secret hoặc artifact lớn.
- Job handler idempotent theo JobID/AttemptID; side effect chưa biết kết quả không được retry mù.

`WriteLease` exclusive theo `RepositoryWorkspaceID + Generation`:

```text
RepositoryWorkspaceID, Generation, FenceToken,
HolderJobID, HolderAttemptID, LeaseOwner, LeaseUntil, HeartbeatAt
```

- Acquire nhiều workspace sort theo RepositoryID và thành công all-or-none trong một transaction.
- Mỗi grant mới tăng FenceToken; token cũ vĩnh viễn không hợp lệ.
- Start mutating process và commit outcome đều cần `WorkspaceFenceProof`.
- Diff sau execution MUST nằm trong declared write repository/path scope.
- Mất JobLease hoặc WriteLease khi process có thể còn ghi sẽ đưa workspace vào `QUARANTINED`.
- Workspace quarantined không cấp writer mới cho đến khi supervisor xác nhận process đã dừng và
  reconciliation pin lại revision/diff. Nếu recreate/reset có kiểm soát thì tăng Generation.

SQLite claim MAY dùng `BEGIN IMMEDIATE` + compare-and-swap. PostgreSQL MAY dùng row lock/
`SKIP LOCKED`; hai adapter phải cho cùng observable semantics.

## 13. Checkpoint, crash recovery và resume

Checkpoint là record bất biến có sequence trong attempt:

```text
Checkpoint {
  ID, RunID, NodeRunID, AttemptID, Sequence,
  CanonicalEventSequence, ContextSnapshotID,
  RevisionSet, SharedStateHash, ArtifactRefs,
  ProviderSessionRef?, ResumeMetadataRef?, CreatedAt
}
```

- Provider session/resume metadata nhạy cảm được mã hóa hoặc lưu qua secret/artifact reference.
- Checkpoint tiến độ chỉ được accept bằng active job fence; checkpoint completion được commit cùng
  Attempt/NodeRun transition, event và downstream job.
- Worker có thể flush canonical events/checkpoint theo batch, nhưng sequence unique và monotonic.
- Restart đọc database để dựng vị trí run; không dựa vào cwd, process memory hay provider transcript.
- Transition đã commit không chạy lại. Event có nhưng projection thiếu thì rebuild projection.
- Job có intent nhưng chưa có side effect có thể claim an toàn.
- Read-only attempt mất lease có thể thành `LOST`, sau đó retry bằng attempt mới theo policy.
- Mutating attempt mất lease trở thành `INDETERMINATE`; workspace bị quarantine và reconciliation phải
  xác định revision/diff trước khi retry, accept hoặc recreate generation.
- Resume provider là optimization: attempt mới MAY dùng previous checkpoint/session ref khi adapter
  khai capability; nếu không, ContextAssembler dựng request mới từ canonical ContextSnapshot.

Không đổi một Attempt đã `LOST/INDETERMINATE` trở lại `RUNNING`. Mọi lần tiếp tục thực thi sau mất
ownership là Attempt mới để giữ lịch sử chính xác.

## 14. AgentExecutor trung lập provider

`AgentExecutionRequest` tối thiểu gồm:

```text
AttemptID, ProviderKey, ProviderAdapterVersion,
InstructionArtifact, ContextSnapshot,
WorkspaceMounts[], EffectiveScope, ExecutionProfileHash,
AllowedCapabilities, Timeout, CancellationToken,
PreviousCheckpoint?, IdempotencyKey
```

Mỗi `WorkspaceMount` chỉ rõ RepositoryID, revision/generation và `READ_ONLY` hoặc `READ_WRITE`. Adapter
không được tự mount thêm repository, đọc secret ngoài allow-list hoặc mở rộng quyền từ nội dung prompt.

Canonical event envelope:

```text
AgentEvent {
  AttemptID, Sequence, Kind, SchemaVersion,
  ObservedAt, Payload, ArtifactRefs[], ProviderMetadata?
}
```

Kind nền:

- `EXECUTION_STARTED`, `STATUS_CHANGED`;
- `ASSISTANT_MESSAGE`, `TOOL_CALL_STARTED`, `TOOL_CALL_FINISHED`;
- `ARTIFACT_PRODUCED`, `USAGE_REPORTED`, `CHECKPOINT_PROPOSED`;
- `DIAGNOSTIC`, `EXECUTION_FINISHED`.

Adapter dịch event provider sang event canonical. Raw event MAY lưu thành artifact có retention riêng,
nhưng domain/scheduler không đọc raw event để routing. Token delta tần suất cao MAY batch thành artifact;
canonical sequence vẫn deterministic trong một attempt.

`AgentExecutionResult` gồm process/provider termination, usage, provider session ref, artifact refs và
typed proposed outcome. Các lớp được tách rõ:

1. `transport result`: process/protocol có chạy được không;
2. `execution result`: provider kết thúc vì complete/error/timeout/cancel;
3. `node outcome`: output đã qua schema/allow-list validation;
4. `domain completion`: gate/policy quyết định NodeRun/Run/WorkItem.

Exit code 0 hoặc provider nói “done” không tự tạo node success. Claude/Codex adapter phải pass cùng
contract suite: capability discovery, start, event ordering, cancel, timeout, malformed output, lost
session và resume-supported/unsupported.

## 15. Workspace/Git contract

`ProvisionSpec` gồm RepositoryID, verified remote locator, base ref/revision, FamilyID, WorkspaceSetID,
Generation và desired branch policy. Adapter Git worktree phải:

1. resolve base ref thành exact commit trước khi tạo worktree;
2. tạo location riêng cho từng TaskFamily + Repository + Generation;
3. không reuse mutable worktree của family khác;
4. trả opaque handle và exact base Revision;
5. kiểm path traversal/symlink escape trước mount;
6. hỗ trợ inspect, diff, capture revision, quarantine và idempotent release;
7. không push, merge, force-reset hoặc xóa branch remote trong spike.

Subtask không provision worktree. Nó resolve cùng RepositoryWorkspace từ WorkspaceSet của family.
Hai sibling ghi repository khác nhau có thể chạy đồng thời; cùng repository phải serialize bằng
WriteLease. Read-only checker dùng pinned revision/snapshot và không đọc mutable HEAD ngầm.

Cleanup bị từ chối khi còn active JobLease/WriteLease, attempt không terminal hoặc workspace
quarantined chưa reconcile. Release phải xác minh locator nằm trong workspace root được cấu hình;
không nhận path tùy ý từ user/provider.

## 16. Persistence schema tối thiểu

SQLite migration của spike phải tạo ít nhất các nhóm bảng sau. Tên cột có thể điều chỉnh nhưng key,
constraint và semantics không được mất.

| Bảng | Dữ liệu/constraint chính |
|---|---|
| `projects` | PK id, status, version, timestamps |
| `repositories` | project_id, identity/config; unique project+name |
| `work_items` | project_id, parent_id, family_id, kind/status/version |
| `task_families` | project_id, unique root_work_item_id, workspace_set_id, scope_version |
| `family_repository_scopes` | family/project/repository/scope_version/access/path JSON; add-only |
| `workspace_sets` | unique family_id, state/version |
| `repository_workspaces` | workspace_set/repository/generation/locator/revisions/state; unique triple |
| `workflow_definitions` | project nullable, name/status/version |
| `workflow_versions` | definition/version_no/content/hash/dependency manifest; immutable; unique definition+version_no và definition+hash |
| `workflow_runs` | project/work item/version/family/scope/state/shared-state/version |
| `node_runs` | run/node key/activation/iteration/state/outcome/input hash/version |
| `execution_attempts` | node_run/attempt_no/state/profile/context/revision/checkpoint/version |
| `durable_jobs` | kind/ref/payload/state/schedule/lease/token/idempotency/version |
| `write_leases` | workspace+generation current holder/fence/expiry; one active row per workspace |
| `checkpoints` | attempt/sequence/manifests/refs; unique attempt+sequence, immutable |
| `agent_events` | attempt/sequence/kind/schema/payload/ref; unique attempt+sequence, append-only |
| `artifacts` | project/kind/URI/hash/size/sensitivity metadata |
| `evidence` | work/run/node/attempt/verdict/policy/revision/artifact manifest |
| `conversations` | project/work item identity |
| `conversation_messages` | conversation/attempt/actor/role/content artifact; append-only |
| `context_snapshots` | work/attempt/manifest/hash/revision; immutable |
| `approvals` | target/decision/actor/evidence/timestamp; append-only |
| `domain_events` | aggregate/sequence/type/schema/payload/timestamp; unique aggregate sequence |
| `outbox` | event_id/topic/payload/status/available/lease; unique event_id |
| `command_receipts` | actor/scope/key/type/request hash/result/error; unique idempotency tuple |
| `schema_migrations` | version/checksum/applied_at |

Mọi row project-owned SHOULD có `project_id` trực tiếp để query scope và defense-in-depth. Schema dùng
composite unique/FK hoặc trigger/transaction validation để ngăn reference chéo Project; chỉ FK theo
global ID là chưa đủ. Foreign key bật bắt buộc.

JSON lưu `TEXT` ở SQLite và `JSONB` ở PostgreSQL nhưng luôn qua cùng codec/schema validation. Artifact
lớn, transcript, diff và log không lưu inline trong state table. Không dùng SQLite rowid làm domain ID,
không dùng `INSERT OR REPLACE`, không hard delete audit/runtime history trong spike.

## 17. SQLite contract sẵn sàng cho PostgreSQL

SQLite adapter khởi tạo mỗi connection với tối thiểu:

- `PRAGMA foreign_keys = ON`;
- WAL mode;
- configured `busy_timeout`;
- durability mode được ghi rõ và không hạ xuống `OFF`;
- migration checksum verification.

Contract chung:

1. ID do application tạo, không phụ thuộc auto-increment/last insert ID.
2. Boolean, timestamp, enum và JSON có codec thống nhất.
3. Unique/check/FK violation map về typed error giống nhau.
4. Expected-version compare-and-swap có cùng kết quả.
5. Command idempotency, event ordering và job claim có cùng semantics.
6. Không phụ thuộc SQLite row ordering, permissive typing hoặc connection-local state.
7. Không đặt HTTP adapter giả làm database repository; beta API gọi application command/query.

SQLite write transaction dùng thời gian ngắn và SHOULD dùng `BEGIN IMMEDIATE` ở flow cần serialize
claim/version allocation. PostgreSQL implementation sau này có thể dùng row-level lock nhưng phải pass
cùng repository/transaction/lease contract tests.

## 18. Error model

Core dùng typed error với các field tối thiểu:

```text
Code, SafeMessage, Retryable, Details, CorrelationID, Cause(private)
```

Mã lỗi chuẩn:

- `INVALID_ARGUMENT`, `NOT_FOUND`, `ALREADY_EXISTS`;
- `CONFLICT`, `IDEMPOTENCY_CONFLICT`, `PRECONDITION_FAILED`;
- `VALIDATION_FAILED`, `POLICY_DENIED`, `SCOPE_VIOLATION`;
- `LEASE_LOST`, `FENCE_REJECTED`, `WORKSPACE_QUARANTINED`;
- `PROVIDER_UNAVAILABLE`, `EXECUTION_FAILED`, `TIMEOUT`, `CANCELLED`;
- `INDETERMINATE`, `RETRY_EXHAUSTED`, `INTERNAL`.

Chỉ error code/policy quyết định retry; không parse message provider/SQL. Error trả UI/CLI không chứa
SQL, argv secret, environment, token hoặc raw provider output. `INDETERMINATE` luôn fail-closed và cần
recovery/escalation, không được tự đổi thành retryable.

## 19. Configuration và execution profile

Configuration được load một lần tại composition root theo precedence rõ ràng: default an toàn < file
< environment < CLI flag. Startup validate toàn bộ config và fail-fast; không có mutable global config.

Nhóm cấu hình tối thiểu:

- database DSN/path, migration policy, busy timeout;
- worker ID, concurrency, poll interval, shutdown grace;
- JobLease/WriteLease TTL và heartbeat (`heartbeat interval <= TTL / 3`);
- workspace root, allowed repository schemes, cleanup/quarantine policy;
- artifact root, size limit, retention và sensitivity policy;
- provider executable/argv template/protocol version/capability allow-list;
- process timeout, output limit, environment/network/secret policy;
- event batching, log level/redaction và telemetry sink.

Secret config chứa reference, không chứa secret value trong database/log/event. Execution config hiệu
lực được resolve thành immutable `ExecutionProfile`, canonicalize/hash và pin vào NodeRun/Attempt.
Layer phải khai OS/toolchain compatibility nhưng vẫn là resource thụ động.

## 20. Observability và audit tối thiểu

Mọi log/event có `CorrelationID`, ProjectID, WorkItemID, RunID, NodeRunID, AttemptID, JobID khi có.
Không dùng log làm nguồn recovery.

Metric spike tối thiểu:

- job queue depth, claim latency, lease expiry/fence rejection;
- attempt duration/outcome/retry/lost/indeterminate;
- checkpoint commit/recovery latency;
- workspace provision/quarantine, concurrent writer rejection;
- canonical event count và provider termination;
- workflow route, validation error và completion gate verdict.

Audit phải trả lời được ai phát command nào, workflow/dependency version nào chạy, worker/provider nào
thực thi, repository revision nào được đọc/ghi và evidence nào cho phép transition.

## 21. Chiến lược kiểm thử bắt buộc

### 21.1 Unit và property test

- Aggregate transition table: mọi state hợp lệ/không hợp lệ.
- Scope subset/add-only, RevisionSet uniqueness/hash và TaskFamily ownership.
- Graph compiler golden tests, malformed fixtures, cycle/fork/join/property/fuzz tests.
- Retry/backoff/iteration budget bằng fake Clock/IDSource.
- Canonicalization/hash giống nhau bất kể map order và OS.

### 21.2 Contract test

- Persistence repository/UoW/optimistic concurrency/idempotency.
- JobQueue/WriteLease claim, heartbeat, expiry, fencing và batch all-or-none.
- ArtifactStore content hash/corruption.
- WorkspaceProvider trên temporary Git repositories.
- AgentExecutor dùng một fake protocol fixture, rồi chạy cùng suite cho Claude/Codex adapter.
- SQLite là target bắt buộc của spike; PostgreSQL adapter khi xuất hiện phải chạy nguyên suite, không
  fork expected behavior.

### 21.3 Integration và fault injection

Test phải kill/restart tại ít nhất các ranh giới:

1. trước commit intent/job;
2. sau commit job nhưng trước claim;
3. sau claim nhưng trước external process;
4. giữa canonical event/checkpoint;
5. sau external side effect nhưng trước finalize;
6. sau finalize nhưng trước scheduler chạy tiếp.

Kiểm chứng không mất transition đã commit, không accept stale fence, không tạo hai success outcome và
không blind-retry mutation indeterminate. Chạy `go test -race` cho scheduler/worker in-process. Workspace
contract suite phải pass Windows và Linux trước khi alpha được coi usable.

## 22. Acceptance gate của Go spike

Spike chỉ đạt khi có evidence tự động hoặc reproducible cho toàn bộ tiêu chí:

1. Publish workflow V1, start run, publish V2; run cũ vẫn chạy graph/hash V1.
2. Graph có unreachable node, missing outcome, unbounded cycle hoặc JOIN sai bị reject.
3. Restart binary sau một node completion; node đã commit không chạy lại và downstream tiếp tục.
4. Kill worker giữa read-only attempt; attempt cũ LOST, retry tạo attempt mới đúng budget.
5. Kill worker giữa mutating attempt; stale token không finalize được và workspace bị quarantine trước
   khi cấp writer mới.
6. Hai root task cùng repository nhận hai worktree khác nhau.
7. Một family scope hai repository tạo WorkspaceSet hai worktree; child reuse đúng WorkspaceSet.
8. Hai sibling ghi hai repository khác nhau chạy song song; cùng repository bị serialize.
9. Batch lease multi-repository là all-or-none; token cũ bị fence sau regrant/generation change.
10. Diff vượt repository/path write scope làm attempt fail/block và lưu evidence.
11. Process restart dựng lại run/node/attempt/job/checkpoint/context/revision từ SQLite, không cần provider
    transcript hay cwd cũ.
12. Cùng workflow scenario chạy qua fake, Claude và Codex adapter mà scheduler/domain không đổi code.
13. Provider output malformed/outcome ngoài allow-list không route graph.
14. Exit code 0 nhưng thiếu required evidence không thể làm WorkItem DONE.
15. SQLite repository/lease/transaction contract suite, race test và migration test đều pass.

Live smoke test Claude/Codex có thể phụ thuộc CLI/credential của môi trường, nhưng adapter contract và
recorded protocol fixtures MUST chạy tự động. Live smoke SHOULD được thực hiện cho từng CLI trên môi
trường được hỗ trợ; khi chưa chạy, báo cáo phải ghi adapter compatibility là `UNVERIFIED_LIVE`, không
được diễn đạt như đã kiểm chứng với dịch vụ thật.

## 23. Definition of Done của từng implementation slice

Mỗi slice sau này chỉ được hoàn tất khi:

1. Thay đổi nằm trong đúng package boundary và không làm domain phụ thuộc adapter.
2. Invariant/error/transaction semantics liên quan có test.
3. Success, failure và ít nhất một crash/concurrency path được kiểm chứng phù hợp scope.
4. Migration/fixture/config mẫu được version cùng code nếu schema/contract thay đổi.
5. Event/evidence/correlation đủ để chẩn đoán mà không đọc process memory.
6. Không thêm executable authority cho Skill/Layer.
7. Tài liệu spec/ADR được cập nhật hoặc có ADR superseding nếu semantics đổi.
8. Handoff ghi rõ evidence, phần chưa làm và next smallest task; không để một session ôm nhiều vertical
   concern chưa kiểm chứng.

Spec này là đầu vào trực tiếp cho kế hoạch spike và bộ thiết kế chia version/subtask. Nó không cấp phép
viết alpha UI trước khi acceptance gate ở mục 22 đạt.
