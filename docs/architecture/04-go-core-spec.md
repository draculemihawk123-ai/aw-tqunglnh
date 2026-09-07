# Đặc tả Go core cho Agent Kit

> Trạng thái: BASELINE ĐÃ QUYẾT ĐỊNH — đồng bộ ADR-001 đến ADR-028; chưa phải mã triển khai.
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

ADR-011…019 được phản ánh trong type/contract để implementation Alpha không tạo schema hoặc API trái
baseline. Tuy nhiên **V0 chỉ bị chặn bởi acceptance gate được ghi rõ ở mục 22**. Repository onboarding,
RunManifestAmendment, ReleaseSet/local commit, projection/HTTP security và retention-class đầy đủ được
triển khai ở V2–V6 theo roadmap; sự hiện diện của contract trong spec này không biến chúng thành code
bắt buộc của spike V0.

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
  aw/                        composition root và operator CLI sản phẩm
  agentkit-spike/            V0 regression/evidence CLI, không phải alias của aw
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
  DefaultRef, Status(REGISTERING|PROBING|ACTIVE|BLOCKED|DISABLED),
  LastProbeErrorCode?, Version
}
```

Một Project có thể có nhiều Repository. `RemoteLocator` là cấu hình của adapter và phải được redact
khi có credential; domain không sử dụng nó làm identity. `RegisterRepository` atomically tạo record
`REGISTERING` và probe job/outbox; worker chuyển qua `PROBING` rồi `ACTIVE` hoặc `BLOCKED`. Chỉ
repository từng `ACTIVE` mới có thể được operator chuyển `DISABLED`.

### 4.2 WorkItem, TaskFamily và scope

```text
WorkItem {
  ID, ProjectID, Kind(ROOT|CHILD), ParentID?, FamilyID,
  SchemaVersion, Title, Behavior, AcceptanceCriteria[], VerificationSpec,
  RiskLevel, Exclusions[], WorkflowVersionID?,
  Status(BACKLOG|READY|ACTIVE|BLOCKED|DONE|CANCELLED), Version
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
  CanonicalSource, SourceHash, CompiledSnapshot, CompiledSnapshotHash,
  DependencyManifest,
  PublishedBy, PublishedAt
}
```

`WorkflowVersion` không có update operation. `DependencyManifest` pin version/hash của Block,
CommandDefinition, GateDefinition, execution profile, context-route policy, Skill/Layer resource và
immutable `AdapterBuildVersion` mà graph sử dụng. Resource identity là
`owner_version + resource_key + content_hash`; path hiển thị không phải identity.

### 4.5 Runtime

```text
WorkflowRun {
  ID, ProjectID, WorkItemID, WorkflowVersionID,
  FamilyID, InitialScopeVersion, ManifestRevision, State,
  SharedState, CancelEpoch?, Version, StartedAt?, FinishedAt?
}

RunManifestAmendment {
  ID, RunID, Revision, PreviousRevision, ApprovedScopeVersion,
  Reason, ApprovedBy, ApprovedAt, ContentHash
}

NodeRun {
  ID, RunID, NodeKey, ActivationSequence, Iteration,
  State, EffectiveScope, InputStateHash, BlockVersionID?, ExecutionProfileHash,
  SelectedOutcome?, BlockReason?, Version
}

ExecutionAttempt {
  ID, NodeRunID, AttemptNumber,
  State, ProviderKey?, AdapterBuildVersion?, ExecutionProfileHash,
  ContextSnapshotID?, InputRevisionSet?, LastCheckpointID?,
  StartedAt?, FinishedAt?, TerminationReason?, Version
}

ReleaseSet { // Alpha extension ở V5; không bắt buộc trong V0
  ID, FamilyID, ManifestRevision, State(DRAFT|SEALED|ABANDONED),
  Entries[]{RepositoryID, BaseRevision, ResultRevision, LocalCommitID?, Verdict},
  ContentHash, SealedBy?, SealedAt?, Version
}
```

State chuẩn:

- `WorkflowRun`: `CREATED`, `RUNNING`, `WAITING`, `BLOCKED`, `VERIFYING`, `CANCELLING`, `SUCCEEDED`,
  `FAILED`, `CANCELLED`. Mọi state không terminal, gồm cả `CREATED`, đều có đường sang `CANCELLING`.
- `NodeRun`: `PENDING`, `READY`, `QUEUED`, `RUNNING`, `WAITING`, `BLOCKED`, `SUCCEEDED`, `FAILED`,
  `SKIPPED`, `CANCELLED`.
- `ExecutionAttempt`: `QUEUED`, `RUNNING`, `SUCCEEDED`, `FAILED`, `TIMED_OUT`, `CANCELLED`, `LOST`,
  `INDETERMINATE`, `BLOCKED`.

`INDETERMINATE` là tên canonical cho tình huống thường được mô tả là `UNKNOWN`: side effect có thể đã
xảy ra nhưng core chưa có đủ evidence để kết luận.

Theo ADR-020, mọi terminal Attempt MUST ghi `TerminationReason` typed. `TerminationReason` là enum riêng
của runtime và MUST NOT được suy ra từ `AppError.Code`; hai tập giá trị độc lập.

| Transition | TerminationReason hợp lệ |
|---|---|
| `RUNNING → SUCCEEDED` | `COMPLETED` |
| `RUNNING → FAILED` | `EXECUTION_FAILED`, `OUTCOME_REJECTED`, `SCOPE_VIOLATION` |
| `RUNNING → TIMED_OUT` | `DEADLINE_EXCEEDED` |
| `RUNNING → CANCELLED` | `RUN_CANCELLED` |
| `QUEUED → CANCELLED` | `RUN_CANCELLED_BEFORE_START` |
| `RUNNING → LOST` | `LEASE_LOST` |
| `RUNNING → INDETERMINATE` | `OWNERSHIP_LOST_MUTATING`, `RECONCILIATION_REQUIRED` |
| `RUNNING → BLOCKED` | `SCOPE_EXPANSION_REQUIRED` |
| `QUEUED → BLOCKED` | `ISOLATION_ENFORCEMENT_UNAVAILABLE`, `ADAPTER_BUILD_DRIFT`, `CAPABILITY_REQUIREMENT_UNSATISFIED`, `WRITE_CAPABILITY_OR_GRANT_MISSING` |

`BLOCKED` là terminal business/admission blocker, không phải technical failure: nó không tính vào budget
retry của `AttemptPolicy` và Attempt đã BLOCKED không được hồi sinh. Hai nhóm đường vào là runtime
blocker (`RUNNING → BLOCKED`) và admission blocker (`QUEUED → BLOCKED`); nhóm admission mở cho mọi
fail-closed check trước spawn, không riêng isolation.

`QUEUED → BLOCKED` và `QUEUED → CANCELLED` MUST NOT set `StartedAt` và MUST giữ process spawn count bằng
0. `RetryBlockedActivation` tạo activation/Attempt mới sau khi revalidate exact pin; nó không hồi sinh
Attempt cũ và không repin run.

`CANCELLING` là pha quiesce của Run: sau khi cancel intent commit, scheduler ngừng tạo activation/retry/
rework mới; Run chỉ tới `CANCELLED` khi execution đã quiesce hoặc reconciliation hoàn tất. Mutating
attempt không chứng minh được kết quả phải là `INDETERMINATE` với workspace `QUARANTINED`, không được
ghi nhãn `CANCELLED`.

Transition rời `VERIFYING` do CompletionPolicy quyết theo ADR-021: `PASS → SUCCEEDED` (WorkItem `DONE`
cùng transaction), `REWORK → RUNNING` theo rework edge đã publish, `BLOCK → BLOCKED`, `FAIL → FAILED`.
Không có rework edge hợp lệ trong graph đã pin thì kết quả MUST là `BLOCK`.

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

Conversation/context thuộc platform. Provider session reference chỉ là metadata quan sát được mã hóa;
nó không thay thế `ContextSnapshot` và Alpha không dùng nó để resume.

## 5. Invariant bắt buộc

Ngoài invariant trong tài liệu mô hình workspace, Go core MUST giữ các điều sau:

1. **GC-INV-01:** Root WorkItem tạo đúng một TaskFamily; child có cùng `ProjectID` và `FamilyID` với parent.
2. **GC-INV-02:** Mọi RepositoryScope/RepositoryWorkspace/Revision của family thuộc cùng Project.
3. **GC-INV-03:** Mỗi `WorkspaceSetID + RepositoryID + Generation` có tối đa một RepositoryWorkspace.
4. **GC-INV-04:** Hai TaskFamily không chia sẻ mutable RepositoryWorkspace.
5. **GC-INV-05:** Child/node/attempt không được mở rộng quyền vượt scope family đã phê duyệt.
6. **GC-INV-06:** WorkflowRun pin WorkflowVersion, CompiledSnapshotHash và dependency manifest trong suốt lifetime;
   initial manifest không đổi, scope mới chỉ được append bằng RunManifestAmendment.
7. **GC-INV-07:** Publish version mới không thay graph hoặc dependency của run cũ.
8. **GC-INV-08:** Mỗi NodeRun pin input hash, effective scope và execution profile trước attempt đầu tiên.
9. **GC-INV-09:** Retry kỹ thuật luôn tạo attempt mới; dữ liệu attempt cũ là append-only ngoại trừ state transition.
10. **GC-INV-10:** Business rework chỉ đi qua edge có trong WorkflowVersion.
11. **GC-INV-11:** Outcome từ agent/command phải thuộc allow-list của node trước khi routing.
12. **GC-INV-12:** Node/attempt success không tự làm WorkItem DONE; completion policy và required evidence quyết định.
13. **GC-INV-13:** `NOT_RUN`, thiếu evidence hoặc verifier error không được quy thành `PASS`.
14. **GC-INV-14:** Mutation từ UI, provider hoặc adapter không được ghi domain table trực tiếp.
15. **GC-INV-15:** Mọi state transition tạo domain event trong cùng database transaction.
16. **GC-INV-16:** Transition tạo side effect phải enqueue durable job/outbox trong cùng transaction.
17. **GC-INV-17:** Kết quả worker chỉ được accept khi JobLease và mọi WriteLease liên quan còn đúng fencing token.
18. **GC-INV-18:** Worker mất lease không được commit success dù external process trả exit code 0.
19. **GC-INV-19:** Multi-repository write lease được acquire all-or-none theo thứ tự RepositoryID ổn định.
20. **GC-INV-20:** Mọi evidence liên quan code pin exact RevisionSet và artifact content hash.
21. **GC-INV-21:** END chỉ chuyển run sang `VERIFYING`; CompletionPolicy mới atomically chuyển WorkflowRun
    `SUCCEEDED` và WorkItem `DONE`.
22. **GC-INV-22:** Scope expansion kết thúc Attempt hiện tại `BLOCKED` và tạo NodeRun activation mới; Attempt,
    NodeRun hoặc sibling cũ không được mở rộng quyền.
23. **GC-INV-23:** Attempt pin immutable AdapterBuildVersion; version khác bị từ chối trước dispatch.
24. **GC-INV-24:** Mutating attempt có đúng một repository `READ_WRITE`, trừ khi capability
    `INTEGRATION_MULTI_REPOSITORY_WRITE` được pin và cấp.
25. **GC-INV-25:** Checker/gate chỉ đọc exact RevisionSet/ReleaseSet; mọi scratch output nằm ngoài source workspace.
26. **GC-INV-26:** Alpha không có remote Git mutation; ReleaseSet local phải được seal hoặc abandon trước cleanup.
27. **GC-INV-27:** Attempt terminal luôn có `TerminationReason` typed thuộc ma trận state–reason ở §4.5; `BLOCKED`
    không tiêu thụ retry budget và không được tái sử dụng làm nhãn cho technical failure.
28. **GC-INV-28:** Cancel là durable intent qua `CANCELLING`; WriteLease chỉ release sau khi process được xác nhận đã
    dừng, và outcome chưa xác định của mutating attempt là `INDETERMINATE`, không phải `CANCELLED`.
    `CancelRun` chỉ là no-op khi **Run** đã terminal; Attempt terminal hay END không làm cancel no-op.
    Attempt còn `QUEUED` khi cancel MUST chuyển `CANCELLED`, không được để mắc kẹt.
29. **GC-INV-29:** Rời `VERIFYING` chỉ qua bốn outcome CompletionPolicy; `REWORK` đòi rework edge có trong
    WorkflowVersion đã pin, nếu không thì kết quả là `BLOCK`.
30. **GC-INV-30:** Profile isolation đã pin không được auto-downgrade; không cưỡng chế được thì admission trả
    `ISOLATION_ENFORCEMENT_UNAVAILABLE` và không có process nào được spawn.
31. **GC-INV-31:** WAIT signal chỉ được consume đúng một lần bằng signal identity unique cộng CAS trên registration;
    durable job chỉ đánh thức timer và không phải authority của signal.
32. **GC-INV-32:** Cancel intent set `CancelEpoch` và fence job `RUN_WORK` của Run trong cùng
    transaction; claim `RUN_WORK` đòi `cancel_epoch IS NULL` nên không workload job nào của Run đang
    quiesce được claim, còn job `CONTROL` vẫn claim được. Enqueue `RUN_WORK` mới CAS rằng Run còn
    non-cancelling.
33. **GC-INV-33:** Worker re-check cancel epoch ngay trước `QUEUED → RUNNING` và ngay trước
    `ProcessSupervisor.Start`; cancel commit trước `RUNNING` cho spawn count bằng 0.
34. **GC-INV-34:** `RetryBlockedActivation` revalidate exact pin trước; thất bại giữ nguyên blocker và
    không tạo thêm blocked activation, thành công mới tạo activation/Attempt mới. Không repin Run.
35. **GC-INV-35:** Idempotency tuple của command receipt dùng `scope_key` non-null
    (`installation` hoặc `project:<id>`); không dùng cột nullable làm thành phần unique, vì SQLite cho
    phép nhiều NULL và duplicate installation command sẽ lọt.
36. **GC-INV-36:** `CancelWorkItem` ghi WorkItem-level cancel intent bền vững và quiesce mọi active Run
    trước khi WorkItem terminal; `ResolveWorkItemBlocker` tự chuyển blocker `OPEN → RESOLVED|WAIVED`
    trong cùng transaction, không đòi blocker đã resolved từ trước. WorkItem chỉ `BLOCKED → READY` khi
    blocker target đã xử lý **và** số blocker `OPEN` còn lại của WorkItem bằng 0; resolve một blocker
    không mở khóa một WorkItem còn blocker khác.
37. **GC-INV-37:** `JobClass` được suy từ `Kind` bằng mapping tĩnh có constraint; Kind ngoài allow-list
    `CONTROL` luôn là `RUN_WORK`, và Kind chưa biết mặc định `RUN_WORK`.
38. **GC-INV-38:** Bốn admission reason và `SCOPE_EXPANSION_REQUIRED` không bao giờ được `WAIVED`; waive
    chúng tương đương vô hiệu hóa enforcement của ADR-011/013/022/023. `WAIVED` cần actor, reason,
    policy grant và `DecisionArtifact`.
39. **GC-INV-39:** Một `work_item_cancellation_intent` đang pending fence cả `ResolveWorkItemBlocker` và
    `StartWorkflowRun` bằng CAS: WorkItem đang bị hủy không được đưa về `READY` và không được nhận Run
    mới, kể cả trước khi coordinator kịp terminalize nó.

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
| `ROUTER` | Chọn outcome từ typed state bằng rule deterministic — Alpha giới hạn đúng một outcome khai báo (ADR-026); multi-outcome rule thật hoãn tới ADR/authoring schema riêng |
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
12. Canonical source hoặc compiled snapshot/hash không tái tạo giống nhau từ cùng semantic input và
    cùng exact dependency manifest.

Canonicalization MUST có golden test Windows/Linux. Map key được sort; semantic list giữ thứ tự;
set-like list được normalize/sort; timestamp/publisher metadata không nằm trong hash. `SourceHash`
định danh canonical authoring source; `CompiledSnapshotHash` bao gồm source đã compile và exact
dependency manifest. Chỉ publish cùng `DefinitionID + CompiledSnapshotHash` mới trả lại version hiện
có; dependency resolve khác phải cấp VersionNumber monotonic mới dù SourceHash trùng.

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
9. END hợp lệ chuyển Run sang `VERIFYING`; CompletionPolicy kiểm evidence, exact RevisionSet/
   ReleaseSet và atomically chuyển Run `SUCCEEDED` cùng WorkItem `DONE`, hoặc trả `BLOCKED/FAILED`.
10. Sau scope approval/provision, scheduler append RunManifestAmendment rồi tạo NodeRun activation mới
    với `ReactivationReason=SCOPE_EXPANDED`; activation cũ không được chạy tiếp.

Không có background loop quét toàn bộ graph để đoán state. Scheduler MAY dùng durable `ADVANCE_RUN`
job; handler phải idempotent theo run version/transition ID.

## 8. Application command contract

Mọi command có envelope chung:

```text
CommandEnvelope {
  CommandID, IdempotencyKey, Actor, ActorRoles[], CorrelationID,
  Scope, ExpectedVersion?, RequestedAt
}

CommandScope = INSTALLATION | PROJECT(ProjectID)
```

Theo ADR-025 và ADR-028, `ProjectID` không còn bắt buộc cho mọi command. Installation scope dùng cho
`CreateProject`, installation health/doctor, safe settings và adapter build registry; definition
global dùng installation scope còn definition thuộc Project dùng project scope. Mọi command runtime/
catalog còn lại là project scope, kể cả run/job/workspace/release diagnostics. Handler MUST reject
command project-scoped thiếu ProjectID, command installation-scoped mang ProjectID không hợp lệ, và
definition item/publish/version command có scope không khớp Definition target đã lưu. Collection
create/list lấy scope từ route hoặc CLI invocation shape rồi persist/query đúng scope đó.

`Actor`/`ActorRoles` là authentication context do delivery boundary lấy từ trusted
`LocalPrincipalSnapshot`, không thuộc request payload và không thuộc `RequestHash`. HTTP body/header và
CLI flag không được override chúng. Trong Alpha, session token của `aw serve` và one-shot `aw` cùng resolve
một startup-config snapshot; Beta thay resolver chứ không thay command contract.
Snapshot dùng config `localPrincipal.actor`/`localPrincipal.roles`; mặc định an toàn cho single-user
Alpha là `local-operator`/`[operator]`. Actor/role non-empty, roles unique và matching case-sensitive.

Command public target của core (V0 chỉ cần subset được scenario mục 22 gọi):

| Command | Kết quả chính |
|---|---|
| `CreateProject` | Project mới |
| `RegisterRepository` | Repository `REGISTERING` + probe job/outbox nguyên tử |
| `RetryRepositoryProbe` | Probe lại repository `BLOCKED` |
| `CreateDefinition` | Tạo Definition `DRAFT`; global dùng installation scope, project definition dùng project scope |
| `CreateRootWorkItem` | WorkItem + TaskFamily + WorkspaceSet + scope + provision jobs nguyên tử |
| `CreateChildWorkItem` | Child cùng family, effective scope là tập con |
| `MarkWorkItemReady` | Revalidate contract rồi CAS duy nhất `BACKLOG → READY`, append registered `WORK_ITEM_MARKED_READY` v1; không nhận target status |
| `PublishDefinitionVersion` | Publish immutable compiled snapshot cho mọi DefinitionKind, gồm Workflow |
| `StartWorkflowRun` | Run pin workflow/scope/dependency |
| `RequestScopeExpansion` | Block node và tạo request add-only |
| `ApproveScopeExpansion` | Tăng ScopeVersion, provision rồi append amendment/reactivate |
| `RejectScopeExpansion` | Ghi decision, route block/escalation |
| `WithdrawScopeExpansion` | Thu hồi request đang `PENDING`; idempotent, không tạo grant/amendment |
| `RetryBlockedActivation` | Revalidate exact pin rồi tạo activation/Attempt mới sau admission blocker; không hồi sinh Attempt cũ |
| `CancelWorkItem` | Ghi WorkItem-level cancel intent, quiesce mọi active Run rồi mới WorkItem `→ CANCELLED`; khác `CancelRun` |
| `ResolveWorkItemBlocker` | Chuyển blocker `OPEN → RESOLVED\|WAIVED`; WorkItem `BLOCKED → READY` chỉ khi không còn blocker `OPEN` nào khác |
| `RequestWorkspaceReconciliation` | Public intent yêu cầu reconcile; enqueue job, không tự thực thi |
| `ProbeAdapterBuild` | Installation scope; đo fingerprint/protocol/capability và phát candidate token có ký/expiry; không mutate registry |
| `RegisterAdapterBuild` | Installation scope; re-probe ngoài transaction, đối chiếu token rồi persist giá trị vừa đo |
| `ApproveNode` / `RejectNode` | Resolve APPROVAL bằng actor/evidence |
| `CancelRun` | Durable cancel intent đưa Run vào `CANCELLING`; no-op chỉ khi Run đã terminal |
| `AppendConversationMessage` | Canonical message append-only |
| `AppendConversationAttachment` | Public application use case: persist artifact qua ArtifactStore rồi append verified reference; không nhận filesystem locator |
| `SignalWait` | Ghi typed signal để đánh thức WAIT node bền vững |
| `CreateReleaseSet` / `SealReleaseSet` / `AbandonReleaseSet` | Quản lý kết quả local đa repository |
| `CreateLocalCommit` | Typed local commit; không có remote mutation |
| `RequestWorkspaceSetRelease` | Public; kiểm eligibility, ghi intent và enqueue job. Chỉ khi không còn active job/lease và ReleaseSet đã seal/abandon |
| `UpdateSafeSettings` | Installation scope; allow-listed field, secret chỉ theo reference |
| `AssignComponentPack` | Pin exact PackVersion cho một Component kèm effective time/actor |
| `RequestProjectionRebuild` | Project-scoped operator intent; enqueue rebuild job và trả operation ID, không sửa cursor/read-model trực tiếp |

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
- `ExecuteWorkspaceReconciliation` (thực thi của `RequestWorkspaceReconciliation`);
- `ExecuteWorkspaceSetRelease` (thực thi filesystem/Git của `RequestWorkspaceSetRelease`);
- `CancelQueuedAttempt`;
- `FenceJobsForCancelledRun`.

`CreateRootWorkItem` là public boundary duy nhất để tạo root; contract validation có thể chạy trước
transaction nhưng không được persist WorkItem mồ côi. Handler MUST validate authorization/policy trước mutation, load aggregate theo `Scope + ID` (installation
hoặc project tùy `CommandScope`), áp expected version, ghi command receipt và trả result ổn định khi cùng
idempotency key được gửi lại.
Push/PR/merge/force-push không tồn tại trong Alpha; phiên bản sau phải thêm command và authority riêng,
không được ẩn trong `CompleteAttempt` hoặc `CreateLocalCommit`.

## 9. Query contract

Query không mutate state và không giữ domain aggregate sống lâu. Tối thiểu gồm:

- `GetProject`, `ListProjectRepositories`, `ListComponents`, `ListComponentPackAssignments`;
- `ListWorkItems` với project/repository/status filter;
- `GetWorkItemDetail` gồm family, scope, active run và blocker;
- `GetRun`, `GetRunGraph` gồm pinned version, NodeRun/Attempt và route đã chọn;
- `GetWorkspaceSet` gồm state/generation/revision/lease theo repository;
- `GetRepositoryOnboarding`, `ListReleaseSets`, `GetReleaseSet`, `GetSource`, `GetDiff`,
  `GetRepositoryLog` read-only;
- `ListEvidence`, `ListArtifacts`;
- `GetArtifactContent` qua authorized bounded stream; không trả raw filesystem locator;
- `GetConversation`, `GetContextSnapshot`;
- `GetJobDiagnostics`, `GetRunTimeline`, `GetRunDiagnostics` (project-scoped) cho operator/spike;
- `ListProjects`, `GetHealth`, `GetDoctorReport`, `GetSafeSettings`, `ListAdapterBuilds`,
  `GetAdapterBuild` (installation-scoped);
- `GetProjectionStatus`, `GetProjectionRebuildStatus` (project-scoped);
- `GetWorkItemReadiness` trả validator diagnostics mà không mutate;
- `WatchProjectEvents` là redacted project-scoped event stream theo JournalPosition;
- `ListDefinitions`, `GetDefinition`, `ListDefinitionVersions`, `GetDefinitionVersion`,
  `DiffDefinitionVersions`, `ValidateDefinitionDraft` dùng scope của Definition target; global
  definition là installation-scoped, project definition là project-scoped.

Theo ADR-028, cuối V6 mọi query/command public mà UI dùng có một `aw` command tương ứng; CLI không
expose danh sách command internal ở trên và không được gọi repository concrete như một fast path.

Query installation-scoped là danh sách đóng: health/doctor, `ListProjects`, safe-settings read,
adapter-build list/detail và definition query khi target là global. Mọi query khác là project-scoped và
MUST scope bằng ProjectID — kể cả definition thuộc Project cùng run/job/workspace/release diagnostics.
Projection dùng JournalPosition và rebuild từ authoritative state tại watermark rồi replay event sau
watermark. UI không được suy domain transition từ projection.

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
- `AdapterBuildRegistry`: resolve/admit immutable adapter build và capability đã pin;
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
- Mỗi event còn có `JournalPosition` tăng đơn điệu nhưng không cần gapless trong database để
  projection/SSE làm cursor; consumer quét global order rồi lọc Project. Field này không thay aggregate
  sequence hay expected-version concurrency.
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
ID, ProjectID, RunID?, Kind, JobClass, AggregateRef, PayloadRef, State,
AvailableAt, Priority, ClaimCount, MaxClaims, CancelEpoch?,
LeaseOwner?, LeaseToken, LeaseUntil?, HeartbeatAt?,
IdempotencyKey, LastErrorCode?, CreatedAt, UpdatedAt, Version

JobClass = RUN_WORK | CONTROL
```

State: `AVAILABLE`, `LEASED`, `SUCCEEDED`, `FAILED`, `DEAD`, `CANCELLED`.

Claim semantics:

- Claim CAS phụ thuộc `JobClass`: `RUN_WORK` dùng
  `WHERE job_class='RUN_WORK' AND state='AVAILABLE' AND available_at <= now AND cancel_epoch IS NULL`;
  `CONTROL` dùng `WHERE job_class='CONTROL' AND state='AVAILABLE' AND available_at <= now`. Hai partial
  index riêng. Cancel fence chỉ áp cho `RUN_WORK`, nếu không cancellation coordinator job sẽ bị fence
  bởi chính transaction sinh ra nó. Recovery chuyển lease hết hạn qua flow riêng. Xem GC-INV-32.

`JobClass` là hàm của `Kind`, không phải cột tự do:

| Kind | JobClass |
|---|---|
| `CANCEL_RUN_COORDINATOR` | `CONTROL` |
| `WORKSPACE_RECONCILE` | `CONTROL` |
| `WORKSPACE_SET_RELEASE` | `CONTROL` |
| `RECOVERY_REAPER` | `CONTROL` |
| mọi Kind khác, gồm Kind chưa biết | `RUN_WORK` |

Mapping này MUST được cưỡng chế bằng check constraint hoặc validation ở repository, không phải bằng quy
ước. Mặc định fail-closed là `RUN_WORK`: gán nhầm `CONTROL` nghĩa là job đó vượt qua cancel fence.
- Periodic recovery reaper chạy suốt vòng đời process, dùng database time và idempotency key theo
  `JobID + LeaseToken`; không chỉ quét một lần khi startup.
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

## 13. Checkpoint và crash recovery

Checkpoint là record bất biến có sequence trong attempt:

```text
Checkpoint {
  ID, RunID, NodeRunID, AttemptID, Sequence,
  CanonicalEventSequence, ContextSnapshotID,
  RevisionSet, SharedStateHash, ArtifactRefs,
  ProviderSessionRef?, RecoveryMetadataRef?, CreatedAt
}
```

- Provider session/recovery metadata nhạy cảm được mã hóa hoặc lưu qua secret/artifact reference.
- Checkpoint tiến độ chỉ được accept bằng active job fence; checkpoint completion được commit cùng
  Attempt/NodeRun transition, event và downstream job.
- Worker có thể flush canonical events/checkpoint theo batch, nhưng sequence unique và monotonic.
- Restart đọc database để dựng vị trí run; không dựa vào cwd, process memory hay provider transcript.
- Transition đã commit không chạy lại. Event có nhưng projection thiếu thì rebuild projection.
- Job có intent nhưng chưa có side effect có thể claim an toàn.
- Read-only attempt mất lease có thể thành `LOST`, sau đó retry bằng attempt mới theo policy.
- Mutating attempt mất lease trở thành `INDETERMINATE`; workspace bị quarantine và reconciliation phải
  xác định revision/diff trước khi retry, accept hoặc recreate generation.
- Alpha luôn dựng request mới từ canonical ContextSnapshot và gọi `Start` cho Attempt mới. Nó không
  gọi provider-native `Resume`, kể cả khi adapter khai capability đó; session ref chỉ phục vụ audit.

Không đổi một Attempt đã `LOST/INDETERMINATE` trở lại `RUNNING`. Mọi lần tiếp tục thực thi sau mất
ownership là Attempt mới để giữ lịch sử chính xác.

## 14. AgentExecutor trung lập provider

`AgentExecutionRequest` tối thiểu gồm:

```text
AttemptID, ProviderKey, AdapterBuildVersion,
InstructionArtifact, ContextSnapshot,
WorkspaceMounts[], EffectiveScope, ExecutionProfileHash,
IsolationProfile(ENFORCED_ISOLATED|OPERATOR_TRUSTED_LOCAL),
AllowedCapabilities, Timeout, CancellationToken,
RecoveryCheckpoint?, IdempotencyKey
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

Adapter build khác version đã pin bị admission từ chối. `ENFORCED_ISOLATED` chỉ được khai khi
filesystem/network thực sự được cưỡng chế; `OPERATOR_TRUSTED_LOCAL` phải hiển thị cảnh báo và vẫn hậu
kiểm diff. Theo ADR-023, worker MUST NOT auto-downgrade profile đã pin: nếu enforcement không khả dụng
thì admission trả `ISOLATION_ENFORCEMENT_UNAVAILABLE` và Attempt bị block **trước**
`ProcessSupervisor.Start`; contract test assert process spawn count bằng 0. Adapter dịch event provider sang event canonical. Raw event MAY lưu thành artifact có retention riêng,
nhưng domain/scheduler không đọc raw event để routing. Token delta tần suất cao MAY batch thành artifact;
canonical sequence vẫn deterministic trong một attempt.

`AgentExecutionResult` gồm process/provider termination, usage, provider session ref, artifact refs và
typed proposed outcome. Các lớp được tách rõ:

1. `transport result`: process/protocol có chạy được không;
2. `execution result`: provider kết thúc vì complete/error/timeout/cancel;
3. `node outcome`: output đã qua schema/allow-list validation;
4. `domain completion`: gate/policy quyết định NodeRun/Run/WorkItem.

Exit code 0 hoặc provider nói “done” không tự tạo node success. Claude/Codex adapter phải pass cùng
contract suite: capability discovery, start, event ordering, cancel, timeout, malformed output và lost
session. Test có thể xác nhận adapter quảng bá Resume, nhưng Alpha orchestrator không gọi method đó.

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
WriteLease. Một Attempt bình thường chỉ mount một repository `READ_WRITE`; ghi nhiều repository đòi
capability `INTEGRATION_MULTI_REPOSITORY_WRITE`. Read-only checker dùng pinned revision/snapshot,
không đọc mutable HEAD ngầm và chỉ ghi scratch directory ngoài source workspace.

Alpha có `ReleaseSet` local và typed `CreateLocalCommit`. ReleaseSet pin base/result revision cùng
verdict từng repository; nó không hứa atomic commit xuyên repository. Push, PR, merge và force-push bị
từ chối ở application policy trước khi Git adapter được gọi.

Cleanup bị từ chối khi còn active JobLease/WriteLease, attempt không terminal, ReleaseSet chưa
seal/abandon hoặc workspace quarantined chưa reconcile. Release phải xác minh locator nằm trong
workspace root được cấu hình; không nhận path tùy ý từ user/provider.

## 16. Persistence schema tối thiểu

SQLite schema target của core gồm các nhóm bảng sau. V0 chỉ cần subset trực tiếp phục vụ acceptance
gate mục 22; các bảng onboarding/amendment/ReleaseSet/projection được thêm bằng migration ở version
roadmap tương ứng. Khi một bảng xuất hiện, key/constraint/semantics dưới đây không được mất.

| Bảng | Dữ liệu/constraint chính |
|---|---|
| `projects` | PK id, status, version, timestamps |
| `repositories` | project_id, identity/config, onboarding state/error; unique project+name |
| `repository_probe_attempts` | repository/job/state/result/error; append-only |
| `work_items` | project/parent/family, schema/behavior/acceptance/verification/risk/exclusions/workflow provenance, kind/status/version |
| `task_families` | project_id, unique root_work_item_id, scope_version; không giữ FK ngược sang WorkspaceSet |
| `family_repository_scopes` | family/project/repository/scope_version/access/path JSON; add-only |
| `workspace_sets` | unique family_id là authority duy nhất của quan hệ family-workspace, state/version |
| `repository_workspaces` | workspace_set/repository/generation/locator/revisions/state; unique triple |
| `workflow_definitions` | project nullable, name/status/version |
| `workflow_versions` | source/source_hash/compiled_snapshot/compiled_hash/exact dependency manifest; immutable; unique definition+version_no và definition+compiled_hash |
| `adapter_build_versions` | provider/build identity, executable hash, protocol/capability manifest; immutable |
| `workflow_runs` | project/work item/version/family/initial scope/manifest revision/state/shared-state/cancel_epoch/version |
| `run_manifest_amendments` | run/revision/previous/scope/approval/hash; append-only, unique run+revision |
| `node_runs` | run/node key/activation/iteration/state/outcome/input hash/version |
| `execution_attempts` | node_run/attempt_no/state/profile/context/revision/checkpoint/version |
| `durable_jobs` | run_id?/kind/job_class (check constraint theo mapping Kind→JobClass ở §12)/ref/payload/state/schedule/lease/token/idempotency/cancel_epoch/version; hai partial index claim: `RUN_WORK` lọc `cancel_epoch IS NULL`, `CONTROL` không lọc |
| `write_leases` | workspace+generation current holder/fence/expiry; one active row per workspace |
| `wait_registrations` | run/node activation, signal schema/key, due time, state/version, consumed signal ref; unique active registration per activation |
| `wait_signals` | immutable signal identity/idempotency key, payload ref/hash, actor/time; unique signal identity |
| `checkpoints` | attempt/sequence/manifests/refs; unique attempt+sequence, immutable |
| `agent_events` | attempt/sequence/kind/schema/payload/ref; unique attempt+sequence, append-only |
| `artifacts` | project/kind/URI/hash/size/sensitivity metadata |
| `evidence` | work/run/node/attempt/verdict/policy/revision/artifact manifest |
| `conversations` | project/work item identity |
| `conversation_messages` | conversation/attempt/actor/role/content artifact; append-only |
| `context_snapshots` | work/attempt/manifest/hash/revision; immutable |
| `approvals` | target/decision/actor/evidence/timestamp; append-only |
| `blockers` | work_item/run/node, type, status `OPEN\|RESOLVED\|WAIVED`, resolution mode/actor/reason/decision_artifact?; append resolution audit |
| `decision_artifacts` | typed policy/completion/recovery decision, inputs, policy version, result; immutable |
| `release_sets` / `release_set_entries` | family/manifest/state/hash và exact result/local commit từng repository |
| `domain_events` | journal_position tăng đơn điệu, aggregate/sequence/type/schema/payload/timestamp; unique aggregate sequence |
| `outbox` | event_id/topic/payload/status/available/lease; unique event_id |
| `projection_checkpoints` | projection/project/journal_position/status/error/watermark; đây là tên canonical, không dùng tên thứ hai cho cùng contract |
| `command_receipts` | actor/scope_key (non-null: `installation` hoặc `project:<id>`)/key/type/request hash/result/error; unique idempotency tuple trên cột non-null |
| `schema_migrations` | version/checksum/applied_at |

`wait_registrations` cộng `wait_signals` là authority của WAIT, không phải `durable_jobs`: job chỉ đánh
thức timer, còn consume-once đến từ signal identity unique và CAS trên registration.

Không tạo hai FK vòng cho quan hệ TaskFamily–WorkspaceSet: `workspace_sets.family_id UNIQUE` là
authority duy nhất. Mọi row project-owned SHOULD có `project_id` trực tiếp để query scope và defense-in-depth. Schema dùng
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

SQLite write transaction dùng thời gian ngắn. Application MUST NOT biết `BEGIN IMMEDIATE`: nó yêu cầu
một semantic transaction option (ví dụ `SerializedWrite` cho claim, version allocation và journal
position allocation), rồi adapter ánh xạ option đó sang cơ chế cụ thể. PostgreSQL implementation sau
này có thể dùng row-level lock nhưng phải pass cùng repository/transaction/lease contract tests.

`SQLITE_BUSY` và `SQLITE_LOCKED` MUST NOT rò ra ngoài adapter. Mapping là xác định, không tùy adapter:
sau bounded retry, lock contention thuần túy map sang `UNAVAILABLE` (retryable); `CONFLICT` chỉ được trả
khi adapter reload và xác nhận expected-version/CAS đã thua. Application không bao giờ phân loại lỗi
bằng cách đọc message. Contract
test cho lớp này MUST dùng nhiều connection để tái hiện contention thật, không giả lập bằng mock.

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
- `ISOLATION_ENFORCEMENT_UNAVAILABLE`, `ADAPTER_BUILD_DRIFT`;
- `UNAVAILABLE`;
- `PROVIDER_UNAVAILABLE`, `EXECUTION_FAILED`, `TIMEOUT`, `CANCELLED`;
- `INDETERMINATE`, `RETRY_EXHAUSTED`, `INTERNAL`.

`ISOLATION_ENFORCEMENT_UNAVAILABLE` là admission failure fail-closed: nó chặn Attempt ở `QUEUED` và
không bao giờ được đổi thành retryable technical failure hay auto-downgrade profile.

`UNAVAILABLE` là retryable infrastructure contention (ví dụ lock contention còn lại sau bounded retry).
`CONFLICT` chỉ dùng khi reload cho thấy expected-version/CAS đã thua thật sự. Adapter MUST NOT map lock
contention thuần túy thành `CONFLICT`, vì làm vậy biến một tình huống retry được thành lỗi nghiệp vụ giả.

Chỉ error code/policy quyết định retry; không parse message provider/SQL. Error trả UI/CLI không chứa
SQL, argv secret, environment, token hoặc raw provider output. `INDETERMINATE` luôn fail-closed và cần
recovery/escalation, không được tự đổi thành retryable.

## 19. Configuration và execution profile

Configuration được load một lần tại composition root theo precedence rõ ràng: default an toàn < file
< environment < CLI flag. Startup validate toàn bộ config và fail-fast; không có mutable global config.

Nhóm cấu hình tối thiểu:

- database DSN/path, migration policy, busy timeout;
- local principal actor/roles và validation; không có per-command impersonation override;
- worker ID, concurrency, poll interval, shutdown grace;
- JobLease/WriteLease TTL và heartbeat (`heartbeat interval <= TTL / 3`);
- workspace root, allowed repository schemes, cleanup/quarantine policy;
- artifact root, size limit, retention và sensitivity policy;
- provider executable/argv template/protocol version/capability allow-list;
- process timeout, output limit, environment/network/secret policy;
- event batching, log level/redaction và telemetry sink.

Secret config chứa reference, không chứa secret value trong database/log/event. Execution config hiệu
lực được resolve thành immutable `ExecutionProfile`, canonicalize/hash và pin vào NodeRun/Attempt.
Profile phải khai `ENFORCED_ISOLATED` hoặc `OPERATOR_TRUSTED_LOCAL`; chỉ profile đầu được mô tả là
sandbox. Diff scanner chỉ là hậu kiểm. Layer phải khai OS/toolchain compatibility nhưng vẫn là
resource thụ động.

Retention dùng typed class. TTL 7 ngày mặc định chỉ dành cho raw provider output/evidence tạm;
canonical Message, referenced resource/context và audit metadata không dùng blanket TTL. Hold ngăn
sweeper; Alpha không hard-delete conversation. Sweeper phải check reference/hold atomically trước xóa.

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
- projection JournalPosition/lag/status và periodic recovery scan/result.

Audit phải trả lời được ai phát command nào, workflow/dependency version nào chạy, worker/provider nào
thực thi, repository revision nào được đọc/ghi và evidence nào cho phép transition.

## 21. Chiến lược kiểm thử bắt buộc

### 21.1 Unit và property test

- Aggregate transition table: mọi state hợp lệ/không hợp lệ.
- Scope subset/add-only, RevisionSet uniqueness/hash và TaskFamily ownership.
- Graph compiler golden tests, malformed fixtures, cycle/fork/join/property/fuzz tests.
- Retry/backoff/iteration budget bằng fake Clock/IDSource.
- Canonicalization/hash giống nhau bất kể map order và OS.
- SourceHash giống nhưng exact dependency manifest khác phải tạo CompiledSnapshotHash khác.

### 21.2 Contract test

- Persistence repository/UoW/optimistic concurrency/idempotency.
- JobQueue/WriteLease claim, heartbeat, expiry, fencing và batch all-or-none.
- ArtifactStore content hash/corruption.
- WorkspaceProvider trên temporary Git repositories.
- AgentExecutor dùng một fake protocol fixture, rồi chạy cùng suite cho Claude/Codex adapter.
- Adapter build admission/pinning và Alpha `Start`-only recovery.
- ReleaseSet/local commit policy; remote Git operation bị từ chối trước adapter.
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

Fault suite còn phải chứng minh periodic recovery reaper bắt lease hết hạn sau startup, checker không
ghi source workspace, multi-repo write thiếu capability bị chặn và scope amendment chỉ cấp quyền cho
activation mới.

## 22. Acceptance gate của Go spike

Spike chỉ đạt khi có evidence tự động hoặc reproducible cho toàn bộ tiêu chí:

1. **GC-ACC-01:** Publish workflow V1, start run, publish V2; run cũ vẫn chạy graph/hash V1.
2. **GC-ACC-02:** Graph có unreachable node, missing outcome, unbounded cycle hoặc JOIN sai bị reject.
3. **GC-ACC-03:** Restart binary sau một node completion; node đã commit không chạy lại và downstream tiếp tục.
4. **GC-ACC-04:** Kill worker giữa read-only attempt; attempt cũ LOST, retry tạo attempt mới đúng budget.
5. **GC-ACC-05:** Kill worker giữa mutating attempt; stale token không finalize được và workspace bị quarantine trước
   khi cấp writer mới.
6. **GC-ACC-06:** Hai root task cùng repository nhận hai worktree khác nhau.
7. **GC-ACC-07:** Một family scope hai repository tạo WorkspaceSet hai worktree; child reuse đúng WorkspaceSet.
8. **GC-ACC-08:** Hai sibling ghi hai repository khác nhau chạy song song; cùng repository bị serialize.
9. **GC-ACC-09:** Batch lease multi-repository là all-or-none; token cũ bị fence sau regrant/generation change.
10. **GC-ACC-10:** Diff vượt repository/path write scope làm attempt fail/block và lưu evidence.
11. **GC-ACC-11:** Process restart dựng lại run/node/attempt/job/checkpoint/context/revision từ SQLite, không cần provider
    transcript hay cwd cũ.
12. **GC-ACC-12:** Cùng workflow scenario chạy qua fake, Claude và Codex adapter mà scheduler/domain không đổi code.
13. **GC-ACC-13:** Provider output malformed/outcome ngoài allow-list không route graph.
14. **GC-ACC-14:** Exit code 0 nhưng thiếu required evidence không thể làm WorkItem DONE.
15. **GC-ACC-15:** SQLite repository/lease/transaction contract suite, race test và migration test đều pass.
16. **GC-ACC-16:** Cả sáu crash boundary có fixture được đăng ký/runnable.

### 22.1 Downstream Alpha compliance targets — không thuộc verdict V0

Các target sau bắt buộc ở version roadmap được nêu, nhưng không được tính như SPK mới hoặc điều kiện
`GO` V0:

1. **GC-DS-01:** **V4/V5:** END đưa Run vào `VERIFYING`; chỉ CompletionPolicy transaction mới tạo Run `SUCCEEDED` +
   WorkItem `DONE` trên exact RevisionSet/ReleaseSet.
2. **GC-DS-02:** **V4:** Scope expansion giữ nguyên Attempt/NodeRun cũ và tạo activation mới pin amendment đã duyệt.
3. **GC-DS-03:** **V2/V5:** Adapter build drift, multi-repository write thiếu capability và checker write đều bị
   fail-closed.
4. **GC-DS-04:** **V5:** ReleaseSet local seal/abandon điều khiển cleanup; mọi remote Git mutation bị từ chối.
5. **GC-DS-05:** **V1/V4:** Periodic recovery reaper phát hiện lease hết hạn sau startup và không tạo recovery job
   trùng cho cùng lease generation.
6. **GC-DS-06:** **V4/V5:** `CancelRun` đi qua `CANCELLING` và quiesce thật; outcome chưa xác định của mutating
   attempt là `INDETERMINATE` + workspace `QUARANTINED`, không phải `CANCELLED`.
7. **GC-DS-07:** **V4/V5:** Bốn outcome CompletionPolicy có transition đúng; `REWORK` không có edge hợp lệ trả
   `BLOCK` thay vì tự dựng route.
8. **GC-DS-08:** **V2/V5:** AdapterBuildVersion có surface probe/register/list/show cho operator; run đang chạy không
   bị tự động repin.
9. **GC-DS-09:** **V5:** Profile isolation không auto-downgrade; `ISOLATION_ENFORCEMENT_UNAVAILABLE` chặn trước spawn
   với spawn count bằng 0.
10. **GC-DS-10:** **V1:** `SQLITE_BUSY/LOCKED` không rò khỏi adapter; application chỉ dùng semantic transaction
    option, không biết `BEGIN IMMEDIATE`.
11. **GC-DS-11:** **V1/V6:** Registry `(event_type, schema_version)` chặn emit event chưa đăng ký và bảo đảm mọi
    event version lịch sử còn replay được.

Live smoke test Claude/Codex có thể phụ thuộc CLI/credential của môi trường, nhưng adapter contract và
recorded protocol fixtures MUST chạy tự động. Live smoke SHOULD được thực hiện cho từng CLI trên môi
trường được hỗ trợ; khi chưa chạy, báo cáo phải ghi adapter compatibility là `UNVERIFIED_LIVE`, không
được diễn đạt như đã kiểm chứng với dịch vụ thật.

### Phase classification

Theo ADR-024, mỗi `GC-INV-*` (§5), `GC-ACC-*` (§22) và `GC-DS-*` (§22.1) mang đúng một nhãn phase,
chốt **trước** khi V1 bắt đầu. Mặc định của mọi tiêu chí trong ba mục này là `ALPHA_MUST`.

Rà soát V1-00A không tìm thấy ngoại lệ nào: không mục nào trong §5/§22/§22.1 đòi một adapter persistence
thứ hai (PostgreSQL) tồn tại để kiểm, và không mục nào đòi so sánh hai deployment topology — kể cả
GC-ACC-12 (chạy qua fake/Claude/Codex adapter) chỉ cần hai provider CLI đã có sẵn trong alpha, không
phải một persistence adapter thứ hai như AK-ARCH-019. `GC-DS-*` tuy gắn nhãn version lộ trình (V1, V2,
V4, V5, V6) nhưng toàn bộ nằm trong phạm vi **Alpha** theo tiêu đề §22.1; nhãn version chỉ nói *khi nào*
trong V1..V8, không nói *có thuộc alpha hay không*. Vì vậy danh sách ngoại lệ của mục này là danh sách
đóng và hiện rỗng:

| Criterion | Nhãn | Phần Alpha vẫn phải làm |
|---|---|---|

Nhãn `NOT_APPLICABLE` chỉ hợp lệ kèm authority reason. V8 không được phân loại lại criteria và không
được dùng `deferred` cho một `ALPHA_MUST`.

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
