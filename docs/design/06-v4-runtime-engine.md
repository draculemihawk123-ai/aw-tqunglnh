# V4 — Durable workflow runtime engine

> Entry: V3 gate pass.
>
> Exit: compiled workflow chạy deterministic bằng fake executors qua retry, rework, wait, approval,
> fork/join và restart; chưa tích hợp completion authority đầy đủ với CLI/gates thật.

> Phạm vi task mặc định: kế thừa mục 3 của `00-roadmap.md`.

## V4-01 — Runtime schema và ExecutionManifest

- **Mục tiêu:** persist run/node/attempt/branch token, immutable initial manifest và append-only
  RunManifestAmendment theo design.
- **Phụ thuộc:** V3, V2.
- **Phạm vi:** V4 sở hữu evolution Alpha của `checkpoints`, generic `agent_events`, `decision_artifacts`
  và `cancellation_intents`. `checkpoints`/`agent_events` đã tồn tại từ migration spike và được V1-04
  bảo toàn; task này là nơi chúng tiến hóa cho Alpha. Production AgentEvent contract thuộc V5-08A;
  completion decision semantics thuộc V5-11; bảng WAIT thuộc V4-08.
- **Thực hiện:** migration, immutable manifest pins, repository contract, cross-project/version
  constraints; `execution_attempts` mang terminal `BLOCKED` và `TerminationReason` typed; `workflow_runs`
  mang state `CANCELLING`. `decision_artifacts` phải tồn tại trước recovery/completion, không đợi V5.
- **Verify:** upgrade/restart/tamper pin tests; test khẳng định `BLOCKED` và `CANCELLING` persist và
  reload đúng qua restart.
- **Hoàn thành khi:** workflow/compiled dependency/adapter pins không đổi; scope mới chỉ xuất hiện ở
  amendment revision kế tiếp, không overwrite initial manifest.
- **Nguồn:** ADR-011, ADR-020, ADR-021, GC-INV-06, HE-03-M08, HE-05-M02, HE-14-M05.

## V4-02 — StartWorkflowRun transaction

- **Mục tiêu:** tạo run, manifest, START activation, checkpoint/event và advance job atomically.
- **Phụ thuộc:** V4-01.
- **Thực hiện:** validate WorkItem READY/workspace ready/version compatibility; idempotent command; CAS
  kiểm không có `work_item_cancellation_intent` đang pending — WorkItem đang bị hủy không được nhận Run
  mới, kể cả trước khi coordinator kịp terminalize nó.
- **Verify:** rollback/duplicate/concurrent start tests; race `StartWorkflowRun`-vs-`CancelWorkItem` cả
  hai thứ tự commit (intent trước → start bị CAS từ chối; start trước → Run mới được quiesce cùng các
  Run khác).
- **Hoàn thành khi:** một WorkItem policy chỉ có số active run cho phép và không orphan job.
- **Nguồn:** GC-INV-39.

## V4-03 — Deterministic activation/router core

- **Mục tiêu:** persisted transition kích đúng downstream node từ allow-listed outcome.
- **Phụ thuộc:** V4-02.
- **Thực hiện:** branch token-independent linear/router routing, shared-state typed writes, activation sequence.
- **Verify:** table/property tests order, missing/outside outcome, restart.
- **Hoàn thành khi:** scheduler không parse free text hoặc query authoring file.
- **Nguồn:** ADR-026, GC-INV-11, HE-14-M07.

## V4-04 — NodeRun/Attempt scheduling transaction

- **Mục tiêu:** executable node tạo attempt + durable job đúng một lần.
- **Phụ thuộc:** V4-03.
- **Thực hiện:** resolve effective scope/profile/context inputs và exact manifest revision; QUEUED
  states; idempotency by `NodeRun activation`, không reuse key giữa activation scope-expanded. Profile
  resolution dùng `ResolvedExecutionProfileV1` (correction round 2 của V4-03) — `RuntimeExecutionConfigHash`
  của nó đến từ `ports.RuntimeExecutionConfigProvider` mới (ADR-027): task này định nghĩa port + type
  snapshot + canonicalization/hash + fake provider, KHÔNG tự resolve composition-root config thật (đó là
  V5-05's scope) và KHÔNG nhận hash trần từ bất kỳ caller nào — chỉ nhận snapshot đã resolve rồi tự tính
  hash. Thiếu provider hoặc snapshot không hợp lệ phải fail closed, không tạo Attempt/job.
  Implementation thật (`internal/app/runtime/schedule.go`, `ScheduleExecutableNodeRun`): V4-03's
  `AdvanceRun` được mở rộng để enqueue một job mới `SCHEDULE_NODE_RUN` (thay vì để NodeRun PENDING
  không có consumer) bất cứ khi nào downstream node là AGENT/COMMAND/MACHINE_GATE — atomically cùng
  transaction tạo NodeRun activation đó, giữ đúng discipline "một hop một job" của V4-03.
  `NodeSchedulingHandler` (workerpool.Handler) claim job này, gọi `ScheduleExecutableNodeRun`:
  resolve `RuntimeExecutionConfigSnapshotV1` ngoài transaction, mở transaction, re-check NodeRun còn
  PENDING (không thì no-op idempotent), resolve `ResolvedExecutorRef`/`Policies`/`AdapterBuild` qua
  `tx.Definitions().LoadVersion`/`tx.AdapterBuilds().Get`, resolve effective scope qua
  `ListWorkItemEffectiveScopes` và manifest revision qua `ListRunManifestAmendments`, ghi một
  `DecisionArtifact` (Kind `EXECUTION_PROFILE_V1`) chứa canonical profile cho audit, gọi
  `tx.Runtime().ScheduleNodeRun` (CAS PENDING->QUEUED, pin EffectiveScope/ExecutionProfileHash/
  ManifestRevision) rồi `CreateExecutionAttempt` (AttemptNumber=1), append event `NODE_SCHEDULED`
  (đăng ký ngay trong eventschema registry, không hoãn như NODE_ROUTED lần đầu) và enqueue job
  `EXECUTE_NODE` cho V4-05's fake executor tương lai, idempotency key theo NodeRun.ID (không reuse
  giữa activation).
  **Quyết định fail-closed bổ sung** (mục 22 ở `docs/00-start-here.md`): một node executable không pin
  đúng một Policy category ATTEMPT (timeout) hoặc PERMISSION (isolation tier) bị từ chối lên lịch
  (`ErrAttemptPolicyRequired`/`ErrPermissionPolicyRequired`) — `ResolvedExecutionProfileV1` không cho
  phép timeout=0 hay isolation tier rỗng, nên một profile thiếu policy tương ứng không thể hợp lệ.
  Một `AdapterBuildID` được khai báo nhưng không resolve được cũng fail closed
  (`ErrAdapterBuildUnresolved`, theo đúng quyết định round-1 của V4-03).
- **Verify:** duplicate ADVANCE_RUN và concurrent scheduler tests. Đã triển khai: fake+sqlite tests cho
  happy path (AGENT), idempotent replay, và mọi fail-closed path kể trên (`schedule_test.go`,
  `schedule_sqlite_test.go`, `internal/app/runtime`).
- **Hoàn thành khi:** queue delivery lặp không tạo duplicate attempt — chứng minh bằng
  `TestScheduleExecutableNodeRun_ReplayAfterAlreadyScheduled_IsNoOp` (fake) và replay sau restart thật
  trong `TestScheduleExecutableNodeRun_SQLite_SchedulesAttemptAndJob`.
- **Nguồn:** ADR-027, AK-ARCH-008, GC-INV-08.

## V4-05 — Generic worker execution envelope với fake executor

- **Mục tiêu:** test engine không phụ thuộc provider/command adapter thật.
- **Phụ thuộc:** V4-04.
- **Thực hiện:** job claim, attempt RUNNING, optional write leases, fake typed events/result, fenced
  finalize. Dùng fake `ports.RuntimeExecutionConfigProvider` V4-04 đã định nghĩa (ADR-027) — không đổi
  port, không tự resolve config thật.
  Implementation thật (`internal/app/runtime/execute.go` + `finalize.go`), theo đúng kiến trúc user đã
  chốt trước khi code: **`FinalizeExecutionAttempt`** là application service DUY NHẤT được phép phối hợp
  fenced finalize — chạy trong đúng một `WithSerializedWrite`, bên trong: (1) `tx.Jobs().ValidateActiveJob`
  kiểm JobLease còn LEASED đúng owner/token/chưa hết hạn VÀ đúng aggregate (`ExecutionAttempt`, AttemptID —
  xem P1 bên dưới); (2) load Attempt, cross-check NodeRunID; (3) `tx.Runtime().ValidateWriteLeaseFencing`
  cho từng WriteLease (tái dùng nguyên SQL `validateActiveWriteLeaseInTx` đã có từ `FinalizeWorkflowRun`
  spike-era, không copy lại — chỉ trích thành helper dùng chung); (4) CAS Attempt RUNNING→terminal qua
  `tx.Runtime().TransitionExecutionAttempt`; (5) append event `EXECUTION_ATTEMPT_FINALIZED` cùng
  transaction (GC-INV-15); (6) nếu SUCCEEDED, gọi `advanceRunTx` (helper Tx-composable trích ra từ
  `AdvanceRun` — `AdvanceRun` public giờ chỉ là wrapper `WithSerializedWrite` mỏng quanh nó) NGAY TRONG
  CÙNG transaction để route NodeRun tiếp — không mở transaction thứ hai sau khi finalize commit (tránh
  crash gap giữa "Attempt SUCCEEDED" và "NodeRun routed"); (7) `tx.Jobs().CompleteJob` (Tx-composable,
  mới) hoàn tất job bằng đúng JobLease, cuối cùng để bất kỳ bước nào fail đều rollback toàn bộ.
  `ExecuteNodeHandler` (workerpool.Handler cho `EXECUTE_NODE`) chỉ là "worker": claim job → CAS cả NodeRun
  và Attempt QUEUED→RUNNING (unfenced — job đã LEASED hợp lệ là đủ) → chạy `ports.NodeExecutor` dưới một
  `context.WithDeadline` tự tạo từ `TimeoutSeconds` đã pin (đọc lại từ `DecisionArtifact` V4-04 ghi ở ID
  tất định `"<nodeRunId>-execution-profile-v1"`) → gọi `FinalizeExecutionAttempt` đúng một lần với kết quả.
  **Quyết định cancel/timeout đã chốt với user trước khi code:** chỉ đúng 3 reason
  COMPLETED/EXECUTION_FAILED/DEADLINE_EXCEEDED thuộc phạm vi V4-05 (đúng như `termination.go`'s own doc
  comment đã phân công); "timeout" nghĩa là context tự tạo từ AttemptPolicy deadline hết hạn (phân biệt
  bằng `errors.Is(attemptCtx.Err(), context.DeadlineExceeded)` trên chính context đó, không phải context
  ngoài); "cancel" nghĩa là context ngoài (pool shutdown/heartbeat loss) bị huỷ vì lý do khác — cả hai
  trường hợp đều KHÔNG finalize, để Attempt RUNNING cho V4-12B/V4-13 xử lý sau bằng authority riêng của
  họ; không bao giờ tự ghi RUN_CANCELLED hay LEASE_LOST/OWNERSHIP_LOST_MUTATING.
  Write-lease ACQUISITION (không phải validation) chưa implement thật trong V4-05 — fenced finalize đã
  chấp nhận và validate `WriteLeaseGrant` end-to-end, nhưng chưa có real executor nào cần ghi vào
  repository thật để cần acquire; để lại cho V5's real executor tự resolve `NodeRun.EffectiveScope`'s
  WRITE grant thành `WorkspaceLeaseTarget` và gọi `AcquireWriteLeases` trước Execute.
  **P1 sửa code V4-04 đã merge (do user phát hiện khi review thiết kế trước khi tôi viết code):**
  `EXECUTE_NODE` job's `AggregateType`/`AggregateID` đổi từ `"NodeRun"`/NodeRunID sang
  `"ExecutionAttempt"`/AttemptID — giữ nguyên aggregate cũ sẽ không chứng minh được job thuộc đúng Attempt
  một khi V4-06 tạo nhiều Attempt dưới cùng NodeRun (retry). `DecisionArtifact`'s ID cũng đổi từ
  `ids.NewID()` sang tất định `"<nodeRunId>-execution-profile-v1"` để V4-05 đọc lại được `TimeoutSeconds`
  mà không cần thêm back-reference nào.
- **Verify:** success/failure/timeout/cancel/lease-loss tests. Đã triển khai: fake tests (happy path,
  failure, timeout, cancel-context, idempotent replay, validation) + sqlite tests bắt buộc cho deep
  fencing (expired lease, wrong owner/token, WriteLease sai fence token, concurrent finalize một-winner) —
  `execute_test.go`, `finalize_execution_attempt_sqlite_test.go`.
- **Hoàn thành khi:** worker chỉ propose outcome; orchestrator quyết transition — `ExecuteNodeHandler`
  chỉ đưa `NodeExecutionResult` (State/SelectedOutcome) cho `FinalizeExecutionAttempt`, không tự CAS/route
  gì; mọi acceptance quyết định bởi fencing bên trong `FinalizeExecutionAttempt`.
- **Nguồn:** GC-INV-17, GC-INV-18, HE-09-M01.

## V4-06 — Technical retry policy

- **Mục tiêu:** retryable failure tạo Attempt mới dưới cùng NodeRun với budget/backoff.
- **Phụ thuộc:** V4-05.
- **Thực hiện:** typed error allowlist, max attempts, scheduled time, exhaustion outcome/escalation.
  **Prerequisite bắt buộc chốt với user trước khi code:** V4-05 chưa lưu bền `AppError.Code` nào trên
  Attempt/kết quả thực thi, nên retry không thể áp `RetryableErrorCodes` bền vững qua restart nếu chỉ có
  `TerminationReason` (một vocabulary thô hơn — loại CAS RUNNING→terminal, không phải AppError code cụ
  thể). Bổ sung: `runtime.ExecutionAttempt.FailureCode` (mới) lưu đúng code phân loại cho Attempt FAILED/
  TIMED_OUT, `ports.NodeExecutionResult.ErrorCode` (mới, bắt buộc khi `State=FAILED`) cho executor tự
  phân loại, `ports.TransitionExecutionAttemptRequest.FailureCode` xuyên qua sqlite/fake — tuyệt đối
  không phân loại bằng cách parse error message.
  **Kiến trúc error-code chốt với user trước khi code** (đã chỉnh: go-core-spec §18 có 22 mã, không phải
  20 như tôi đếm nhầm lần đầu — `apperror.Code` cũ chỉ có 5): tạo `internal/domain/errorcode.Code` làm
  enum chuẩn 22 giá trị duy nhất (không tạo `runtime.ErrorCode` song song) — `apperror.Code` trở thành
  type alias (`type Code = errorcode.Code`) re-export 5 hằng cũ để ~80 call site hiện có không phải đổi;
  giữ đúng luật domain <- app vì enum nằm ở domain layer. `policy.AttemptRules.RetryableErrorCodes` đổi
  từ `[]string` sang `[]errorcode.Code`, validate bằng `errorcode.Code.Valid()`/`.NeverRetryable()` thay
  vì hai map riêng đã xoá (`knownErrorCodes`/`nonRetryableErrorCodes`). Timeout do chính execution
  envelope xác định và ghi `CodeTimeout` (V4-05's own derived-deadline context), không do executor tự đề
  xuất — `NodeExecutionResult.ErrorCode` không bao giờ tự nhận TIMED_OUT.
  **Semantics retry/exhaustion chốt với user trước khi code:** `MaxAttempts` tính cả Attempt đầu tiên.
  Retryable (code nằm trong `RetryableErrorCodes` đã pin VÀ không `NeverRetryable()`) và còn budget
  (`AttemptNumber < MaxAttempts`) → tạo Attempt mới (`AttemptNumber+1`, cùng `ExecutionProfileHash`/
  `ProviderKey`/`InputRevisionSet`) với job `EXECUTE_NODE` mới có `AvailableAt = clock.Now() +
  BackoffSeconds` (threading `clock.Clock` — package này có sẵn từ V1-02 nhưng chưa từng có real caller
  nào trước V4-06). Retryable nhưng hết budget → CAS NodeRun `RUNNING → FAILED`. Non-retryable → NodeRun
  → FAILED ngay, không chờ hết budget. Cả hai trường hợp exhaustion/non-retryable đều append event mới
  `NODE_RUN_FAILED` (đăng ký cùng changeset, không deferred) với payload typed: `failureKind`
  (`RETRY_EXHAUSTED` | `NON_RETRYABLE_FAILURE`), `lastAttemptId`, `attemptsUsed`, `maxAttempts`,
  `lastErrorCode`, `attemptPolicyVersionId` — Attempt cuối vẫn giữ nguyên `TerminationReason`/
  `FailureCode` gốc (vd `EXECUTION_FAILED`/`PROVIDER_UNAVAILABLE`), không ghi đè `RETRY_EXHAUSTED` lên
  Attempt vì exhaustion là phân loại cấp NodeRun, không phải cấp Attempt. Không tạo blocker giả (bảng
  `blockers` chưa tồn tại, thuộc phạm vi V4-12A/V4-12C) và không dùng `NodeRun.BLOCKED` (một state khác
  hẳn — scope-amendment flow của ADR-011/V4-12A). Không tự chuyển WorkflowRun → FAILED — tổng hợp thất
  bại cấp Run thuộc V4-12. `BLOCKED`/`INDETERMINATE` không đi qua logic này chút nào:
  `isFinalizableExecutionAttemptState` (đã có từ V4-05, không đổi) chỉ chấp nhận
  SUCCEEDED/FAILED/TIMED_OUT/CANCELLED làm `NextState` — gọi `FinalizeExecutionAttempt` với `BLOCKED` bị
  từ chối bằng `ErrUnsupportedFinalizeState` trước khi chạm tới bất kỳ logic retry nào, nên BLOCKED không
  thể tiêu thụ budget hay tự sinh Attempt kế tiếp bằng cấu trúc, không phải bằng một check runtime riêng.
  Transition Attempt cuối, quyết định retry/exhaustion, CAS NodeRun, append event và hoàn tất job đều
  nằm trong cùng một `WithSerializedWrite` transaction bên trong `FinalizeExecutionAttempt` (không mở
  transaction thứ hai) để không tạo crash gap.
  `resolvePinnedAttemptRules` (helper mới, `finalize.go`) đọc lại đúng `DecisionArtifact`
  `"<nodeRunId>-execution-profile-v1"` V4-04 đã ghi, tìm `ResolvedPolicyRef` category ATTEMPT, rồi
  `tx.Definitions().LoadVersion` + `decodeCompiledPolicy` (tái dùng helper `schedule.go` đã có) — an toàn
  để đọc lại vì PolicyVersion published là immutable/content-hashed, không lệch so với giá trị đã pin.
- **Verify:** fake clock tests retry/nonretry/timeout/exhaustion/restart; test khẳng định Attempt
  `BLOCKED` không tiêu thụ retry budget và không sinh Attempt kế tiếp tự động. Đã triển khai:
  `TestExecuteNodeHandler_RetryableFailure_CreatesNextAttemptWithBackoff` (clock.Fixed chứng minh
  AvailableAt = now + BackoffSeconds chính xác), `TestExecuteNodeHandler_RetryableFailure_
  BudgetExhausted_FailsNodeRun` (payload NODE_RUN_FAILED/RETRY_EXHAUSTED đầy đủ, Attempt cuối không đổi),
  `TestExecuteNodeHandler_Failure_NonRetryableCode_FailsNodeRun` và
  `TestExecuteNodeHandler_AttemptDeadlineFinalizesTimedOut` (cả hai cùng chứng minh non-retryable/timeout
  fail NodeRun ngay khi policy không khai báo code retryable),
  `TestFinalizeExecutionAttempt_BlockedState_RejectedNeverConsumesRetryBudget`, và
  `TestFinalizeExecutionAttempt_SQLite_RetryChain_PersistsAcrossRestart` (đóng/mở lại sqlite.Store thật,
  chứng minh Attempt/job retry sống qua restart) — `finalize_retry_test.go`, `execute_test.go`.
- **Hoàn thành khi:** không parse message, không retry INDETERMINATE và không retry BLOCKED.
- **Nguồn:** ADR-020, GC-INV-09, GC-INV-27, HE-13-M01, HE-13-M04.

## V4-07 — Business rework/cycle budget

- **Mục tiêu:** rework chỉ qua edge và tạo NodeRun activation mới.
- **Phụ thuộc:** V4-03, V4-06.
- **Thực hiện:** iteration counter, max iteration, escalation route, audit causation.
  Rework tự nó không cần cơ chế mới — `advanceRunTx` (V4-03) đã luôn tạo một NodeRun activation mới cho
  BẤT KỲ edge target nào, kể cả một node key đã từng activate trước đó trong cùng Run (GC-INV-10's own
  "business rework chỉ đi qua edge có trong WorkflowVersion" vốn đã được thoả). Việc còn thiếu thật sự
  (xác nhận qua code, không phải qua đọc doc) là RUNTIME chưa từng enforce `CyclePolicy.MaxIterations`:
  `NodeRun.Iteration` (cột đã có từ `0001_initial_schema.sql`, field đã có trên domain type từ V4-01)
  mọi caller đều hardcode 0; `validateBoundedCycles` (compiler, `internal/domain/workflow/validation.go`)
  chỉ CHỨNG MINH tại publish-time rằng một escape route tồn tại — không có gì tại RUNTIME từng đếm hay ép
  đi escape route đó cả, nên một workflow published hợp lệ vẫn có thể loop vô hạn nếu không sửa.
  **Hai câu hỏi chốt với user trước khi code** (đã hỏi qua AskUserQuestion, đề xuất Recommended của tôi
  cho cả hai được chọn, kèm semantics khoá thêm chi tiết hơn đề xuất gốc):
  1. *Cơ chế forced-escalation:* budget check chạy trong `advanceRunTx`, SAU khi xác thực outcome/edge
     của node upstream nhưng TRƯỚC khi tạo activation downstream bình thường — kiểm tra trên
     `downstreamNode` (edge target sắp được activate), không phải trên node vừa hoàn thành. Activation
     đầu tiên của một NodeKey có Iteration=0; các vòng rework hợp lệ là Iteration 1..MaxIterations; một
     candidate MaxIterations+1 là exhausted. Khi exhausted: tạo NodeRun cho downstreamNode ở state
     `SKIPPED` (giá trị đã có trong domain enum từ V4-01, docs/design/01-system-design.md dòng 213-214 tự
     liệt SKIPPED cạnh "Rework tạo activation mới" — chưa task nào từng dùng trước V4-07) với
     `SelectedOutcome=CyclePolicy.EscalationOutcome`, KHÔNG tạo Attempt/execution job; rồi NGAY trong cùng
     transaction route tiếp qua escalation edge sang target thật của nó — special-case tối đa HAI hop
     (downstreamNode -> SKIPPED, rồi ngay lập tức -> escalation target), không mở recursion tự do (target
     của escalation edge không tự bị re-check exhaustion). Upstream NodeRun (node vừa hoàn thành, vd
     maker's own "done") giữ nguyên SUCCEEDED với outcome thật của nó — một scheduler hết cycle budget
     không bao giờ biến một execution thật thành lỗi.
  2. *Phạm vi đếm Iteration:* per (RunID, NodeKey), KHÔNG per-SCC — nhưng KHÔNG được định nghĩa bằng
     `COUNT(*)` mọi NodeRun row cho key đó (chỉnh lý user tự nêu thêm, tôi chưa nghĩ tới trong đề xuất
     gốc): một `V4-12A` tương lai (scope-expansion reactivation) sẽ tạo NodeRun MỚI cho một key đã visit
     mà KHÔNG phải business rework — nó copy Iteration cũ forward thay vì tăng. Vì vậy `Iteration` là một
     "business cycle generation number", không phải row-sequence-number; nguồn sự thật là
     `MAX(iteration)` qua lịch sử durable của đúng (RunID, NodeKey), không phải đếm số hàng. Method mới
     `ports.RuntimeRepository.GetMaxNodeIteration(ctx, runID, nodeKey) (uint32, bool, error)` — Tx-
     composable, `SELECT MAX(iteration) ... WHERE run_id=? AND node_key=?` (sqlite) / linear scan (fake).
     Không cần port `stronglyConnectedComponents` (compiler, private) sang runtime cho việc đếm — nhưng
     RIÊNG bước "khi exhausted, verify escalation edge thật sự thoát khỏi cycle" (defensive, phòng một
     WorkflowVersion bị hỏng/ngoại lai, cùng tinh thần `ErrRouteNotFound` đã có) VẪN cần biết SCC
     membership, nên `stronglyConnectedComponents` được export dạng mới `workflow.CycleMembership(document)
     map[string]int` (tái dùng logic gốc qua hàm private cũ, không viết lại — tránh drift).
  Event mới `NODE_CYCLE_EXHAUSTED` (đăng ký cùng changeset, `advance.go`/`event_schema.go`) — payload typed
  đúng như user yêu cầu: `maxIterations`, `attemptedIteration`, triggering edge (`triggeringNodeRunId`/
  `triggeringNodeKey`/`triggeringOutcome` — causation, node vừa hoàn thành gây ra hop này), escalation
  outcome/edge (`escalationOutcome`/`escalationNodeRunId`/`escalationNodeKey`). `NODE_ROUTED` của chính
  hop đó (event đã có từ V4-03) vẫn mô tả trung thực edge target THẬT (downstreamNode's own key), trỏ vào
  hàng SKIPPED khi exhausted — không âm thầm viết lại thành escalation target; `AdvanceRunResult`'s own
  `Next*` fields (dùng cho dispatch: `NextAutoAdvanced`/`NextJobID`/`NextScheduleJobID`) thì mô tả node
  CÒN CẦN xử lý tiếp (escalation target khi exhausted) — hai bộ field khác nhau cho hai mục đích khác
  nhau, `CycleExhausted`/`SkippedNodeRunID`/`SkippedNodeKey` là field mới lộ rõ trường hợp này cho caller/
  test mà không đè lên field cũ.
- **Verify:** bounded cycle pass/exhaust/restart tests. Đã triển khai:
  `TestAdvanceRun_BoundedCycle_PassesUntilExhaustedThenForcesEscalation` (fake, đi hết 3 vòng rework hợp
  lệ trong budget MaxIterations=2 rồi chứng minh vòng thứ 4 bị chặn, kèm assert đầy đủ payload
  NODE_CYCLE_EXHAUSTED và NODE_ROUTED, và Attempt/NodeRun upstream không bị viết đè), và
  `TestAdvanceRun_SQLite_CycleExhaustion_PersistsAcrossRestart` (đóng/mở lại sqlite.Store thật, chứng
  minh cả hàng SKIPPED lẫn chính `GetMaxNodeIteration` observe đúng giá trị sau restart) —
  `cycle_test.go`.
- **Hoàn thành khi:** không có infinite graph loop hoặc overwrite activation history.
- **Nguồn:** AK-ARCH-004, GC-INV-10, HE-09-M07, HE-13-M03.

## V4-08 — WAIT persistence và signal semantics

- **Mục tiêu:** node chờ durable signal/timer không giữ worker/job lease, và signal chỉ được consume
  đúng một lần kể cả khi delivery lặp hoặc process restart giữa chừng.
- **Phụ thuộc:** V4-03.
- **Phạm vi:** V4-08 sở hữu migration và repository của `wait_registrations` cùng `wait_signals`.
- **Thực hiện:** `wait_registrations` giữ run/node activation, signal schema/key, due time, state/version
  và consumed signal ref, unique một registration active mỗi activation; `wait_signals` là immutable
  signal identity/idempotency key kèm payload ref/hash và actor/time, unique theo signal identity.
  Consume-once đến từ unique signal identity cộng CAS trên registration — `durable_jobs` chỉ đánh thức
  timer đến hạn và **không** là authority của signal. Public command `SignalWait`, cancel/timeout route.
  **Hai câu hỏi chốt với user trước khi code:**
  1. *Outcome khi timeout:* `workflow.WaitNodeConfig` (V2-08) không có field nào tên outcome fire khi
     TIMEOUT, khác `ApprovalNodeConfig.EscalationOutcome` đã có. Chọn mở rộng authoring schema, không
     dùng convention/magic string: thêm `CompletionOutcome`/`TimeoutOutcome` (cả hai optional khi node
     chỉ có đúng một outcome — suy luận được; bắt buộc khai báo tường minh khi có nhiều outcome, reject
     ngay lúc compile/publish nếu thiếu). `TimeoutOutcome` chỉ hợp lệ với SIGNAL có `TimeoutSeconds>0`;
     với DURATION, hết thời lượng luôn là `CompletionOutcome` (never a timeout). `SignalWait`/timer job
     không tự chọn outcome — chỉ tranh CAS trên registration, bên thắng dùng outcome đã pin sẵn trong
     WorkflowVersion.
  2. *Signal identity:* `SignalWaitRequest` có field riêng `SignalKey` (opaque, caller-supplied), KHÔNG
     dùng `cmd.IdempotencyKey`. Unique trên `wait_signals` là (WaitRegistrationID, SignalKey) — không lặp
     thêm cột Run/NodeRun/SignalName vì WaitRegistrationID đã định danh đúng activation đó.
     `cmd.IdempotencyKey` bảo vệ một lần gọi command + receipt; `SignalKey` bảo vệ cùng một external
     event gửi qua nhiều command invocation khác nhau (actor/idempotency key khác nhau) — hai lớp
     dedupe tách biệt, cột đặt tên `signal_key` chứ không phải `idempotency_key` để không nhập nhằng.
     Cùng SignalKey + cùng payload → idempotent replay (không consume lại); cùng SignalKey + payload khác
     → `errorcode.CodeIdempotencyConflict` (lần đầu package đó có real caller ngoài test); hai SignalKey
     khác nhau đến đồng thời → CAS registration quyết định đúng một winner. Timer timeout không tạo
     `wait_signals` row nào cả.
  **Phát hiện thêm khi code (không phải câu hỏi, nhưng cần một state mới ngoài dự kiến ban đầu):**
  `WaitRegistrationState` cần 4 giá trị, không phải 3 — ACTIVE, CONSUMED (SIGNAL resolve bởi signal thật,
  luôn có `ConsumedSignalID`), **ELAPSED** (DURATION đến hạn — hoàn thành bình thường, route qua
  `CompletionOutcome`, KHÔNG dùng CONSUMED vì tên đó ngụ ý có signal thật, và KHÔNG dùng TIMED_OUT vì đó
  không phải thất bại), TIMED_OUT (SIGNAL hết `TimeoutSeconds` mà không có signal — route qua
  `TimeoutOutcome`). Timer handler tự phân biệt DURATION/SIGNAL bằng `registration.SignalName == ""`
  (đúng discriminator `validateWaitConfig` đã enforce — SignalName luôn rỗng cho DURATION).
  `advanceRunTx`'s own idempotent-replay guard (V4-03) nới từ `current.State != RUNNING` thành chấp nhận
  cả `RUNNING` lẫn `WAITING` — một WAIT NodeRun nằm ở WAITING (không bao giờ RUNNING, không có Attempt
  nào cả) và được route đi từ đó theo đúng cách một RUNNING NodeRun được route, tái dùng nguyên
  `advanceRunTx` cho `SignalWait`/timer handler thay vì viết lại pipeline routing riêng.
  `Tx` gains accessor mới `Wait() WaitRepository` (mirror `AdapterBuilds()`/`Readiness()` — "gets a real
  interface from the start" cho concern task này sở hữu trọn vẹn).
- **Verify:** duplicate/early/wrong signal, restart timer, hai signal đồng thời cùng identity, và test
  khẳng định xóa/replay timer job không consume thêm lần nào. Đã triển khai (`wait_test.go`,
  `wait_sqlite_test.go`): fake tests cho happy path DURATION/SIGNAL, duplicate signal khác command
  invocation (idempotent replay), same-key-different-payload (IDEMPOTENCY_CONFLICT), wrong-run reject,
  signal đến sau khi đã CONSUMED (Won=false, không re-route), timer replay sau khi đã CONSUMED (no-op) —
  cộng 2 test sqlite bắt buộc: `TestWaitTimeoutHandler_SQLite_PersistsAcrossRestart` (đóng/mở lại
  sqlite.Store thật, timer job vẫn claim và fire đúng sau restart) và
  `TestSignalWait_SQLite_ConcurrentSameSignalKey_ExactlyOneWinner` (5 goroutine gọi `SignalWait` thật sự
  đồng thời, cùng SignalKey/payload, khác command invocation — đúng 1 winner, registration CONSUMED đúng
  một lần).
- **Hoàn thành khi:** WAIT sống qua process restart và signal chỉ consume một lần.
- **Nguồn:** GC-INV-31, HE-13-M06.

## V4-09 — APPROVAL semantics

- **Mục tiêu:** approval node chỉ resolve bằng typed operator command.
- **Phụ thuộc:** V4-08.
- **Thực hiện:** approval request/evidence/actor/reason/timeout; approve/reject outcomes; chat không tự resolve.
  Khác V4-08's own WAIT (SignalWait's own signal không mang thông tin outcome nào cả), quyết định của
  operator TỰ mang outcome (GC-INV-11's own allow-list check, `advanceRunTx`, xác thực nó) — nên
  `ApprovalNodeConfig` không cần thêm field pin outcome kiểu CompletionOutcome/TimeoutOutcome của WAIT;
  chỉ `EscalationOutcome` (đã có từ V2-08) là cần pin, cho đường timeout.
  **Một câu hỏi chốt với user trước khi code:** làm sao lệnh resolve-approval biết actor có thuộc
  `AuthorizedRoles` hay không, khi `ports.Command.Actor` trước giờ luôn là chuỗi identity thuần, chưa
  từng được re-authenticate ở bất kỳ layer nào, và không có cơ chế role/permission thật nào trong toàn bộ
  codebase? Chọn: `ports.Command` (struct dùng chung cho MỌI command trong repo) thêm field
  `ActorRoles []string` — **authentication context** do transport/API layer tạo từ local session, KHÔNG
  decode từ request body, KHÔNG thuộc `RequestHash` (vì đó là context, không phải payload). Handler chỉ
  check intersection chính xác/case-sensitive giữa `cmd.ActorRoles` và `AuthorizedRoles`; rỗng hoặc không
  giao nhau → `errorcode.CodePolicyDenied` (typed, dùng thẳng enum V4-06), reject TRƯỚC khi chạm state
  của request (kể cả khi request đã DECIDED). Ghi actor VÀ role đã match vào request để audit
  (HE-08-M08's own "MUST ghi actor, cause, previous/new state, time"). Alpha dùng local-session principal
  tối giản; Beta thay bằng identity provider thật mà không đổi semantics V4-09.
  **Phát hiện thêm khi code (siết một validation cũ, không phải câu hỏi mới):** `ApprovalNodeConfig.
  EscalationOutcome` (V2-08) được document là "Optional" — nhưng `TimeoutSeconds` lại LUÔN bắt buộc và
  dương (không như WAIT's own optional ceiling). Một timeout luôn xảy ra mà không có đường thoát sẽ tái
  tạo đúng cái unbounded-wait mà chính TimeoutSeconds tồn tại để ngăn — và không có "standardized timeout
  outcome" convention nào (dù comment cũ hứa hẹn) từng thực sự được xây ở đâu trong repo. Sửa:
  `validateApprovalConfig` giờ BẮT BUỘC `EscalationOutcome`, không còn optional.
- **Verify:** unauthorized-shaped input, duplicate decision, timeout/restart tests. Đã triển khai
  (`approval_test.go`, `approval_sqlite_test.go`, 8 test): happy path approve+route, unauthorized actor
  (role sai và không có role nào) reject bằng `POLICY_DENIED`/không ghi gì/không route, duplicate decision
  (command invocation khác, sau khi đã DECIDED) Won=false/không re-route/không đè outcome đã thắng, timer
  route qua EscalationOutcome, timer replay sau khi đã DECIDED (no-op) — cộng
  `TestApprovalTimeoutHandler_SQLite_PersistsAcrossRestart` (đóng/mở lại sqlite.Store thật, timer job vẫn
  claim/fire đúng sau restart).
- **Hoàn thành khi:** decision append-only và route audit đủ.
- **Nguồn:** HE-14-S03, GC-INV-11, HE-08-M08.

## V4-10 — FORK branch tokens

- **Mục tiêu:** phát persisted branch identity không dựa queue order.
- **Phụ thuộc:** V4-03.
- **Thực hiện:** create tokens atomically, activate branches idempotently, static write-scope admission.
  **Hai câu hỏi chốt với user trước khi code:**
  1. *Branch identity theo dõi thế nào:* chọn thêm `BranchTokenID *BranchTokenID` vào `NodeRun` (V4-01's
     own schema/domain already scaffolded `BranchToken`, chưa có consumer thật). Branch identity phải đi
     cùng activation thực tế, không được suy từ queue hoặc `CurrentNodeKey`. Khóa semantics: trước FORK,
     `BranchTokenID=nil`. FORK dispatch tạo atomically một token và NodeRun đầu tiên cho mỗi branch (hoặc,
     khi branch's own first edge target đã là JOIN, mark token đó SUCCEEDED ngay, không tạo NodeRun nào).
     Mỗi hop thông thường trong branch kế thừa cùng `BranchTokenID`, đồng thời CAS
     `BranchToken.CurrentNodeKey` trong cùng transaction. Khi branch tới JOIN: không gắn một token duy
     nhất vào JOIN NodeRun (nhiều branch cùng hội tụ) — chỉ mark token đến nơi SUCCEEDED,
     `CurrentNodeKey=<join key>`; JOIN's own NodeRun là activation dùng chung, V4-11 mới đánh giá tập
     token. Sau JOIN, downstream trở lại `BranchTokenID=nil`. NodeRun trong branch FAILED/CANCELLED phải
     terminalize token tương ứng trong cùng transaction. Mọi update token dùng expected version để chống
     stale/duplicate hop.
     **Lỗi schema tự phát hiện khi thiết kế câu trả lời này (không phải câu hỏi riêng, nhưng sửa cùng
     lúc):** unique constraint gốc của V4-01 là `(RunID, ForkKey, BranchKey)` — không hỗ trợ cùng một FORK
     node key được kích hoạt lại trong một cycle (V4-07). Token phải gắn với đúng lần FORK activation đó,
     không phải node key tĩnh: thêm `ForkNodeRunID NodeRunID` vào `BranchToken`, đổi unique thành
     `UNIQUE(ForkNodeRunID, BranchKey)` (giữ `ForkKey` lại chỉ để query/audit). Không sửa điểm này thì lần
     FORK thứ hai trong cùng Run sẽ đụng token của lần activation trước.
  2. *"Static write-scope admission" nghĩa là gì trong Alpha:* V4-10 không thể kiểm tra path overlap tĩnh
     vì schema hiện tại không có scope riêng cho từng node/branch — không phát minh một kiểm tra giả.
     Trong Alpha, phần việc này chỉ gồm: mỗi branch giữ nguyên Run/TaskFamily/WorkspaceSet/manifest
     authority hiện có, không tự thêm repository, không tự nâng READ→WRITE, không tự mở rộng PathScope.
     Khi một executable NodeRun trong branch được schedule, nó pin effective scope từ authority hiện có
     (không copy literal `FORK.NodeRun.EffectiveScope`, vì trường đó có thể còn rỗng — FORK là node cấu
     trúc, chưa từng thật sự được schedule). Nhiều branch cùng có quyền WRITE trên một
     RepositoryWorkspace vẫn được dispatch — execution mới cần serialize bằng exclusive WriteLease; hai
     branch ghi hai repository khác nhau nhận hai lease và chạy song song. Việc đổi tên chính xác hơn:
     "scope-containment admission + repository-level write serialization **contract**" — V4-04/V4-05 mới
     có lease contract và fenced-finalize, `ExecuteNodeHandler` hiện chưa thật sự acquire WriteLease; việc
     caller acquisition end-to-end thuộc V5. V4-10 chỉ giữ scope ceiling và không mint quyền mới; không
     claim rằng write concurrency đã được enforce hoàn chỉnh ở task này.
  Kết quả: `NodeRun.BranchTokenID` set post-construction bởi caller (như `.State` đã làm), không thêm
  tham số vào `NewNodeRun`. `ports.RuntimeRepository` thêm `GetBranchTokenByID`/`TransitionBranchToken`
  (fenced CAS) bên cạnh `CreateBranchToken`/`GetBranchToken` đã đổi khóa sang `(forkNodeRunID, branchKey)`.
  `advanceRunTx` (V4-03): khi downstream node là JOIN và upstream NodeRun mang một `BranchTokenID`, CAS
  token đó SUCCEEDED và trả `ReachedJoin`/`JoinBranchTokenID`/`JoinNodeKey` — không tạo NodeRun nào cho
  JOIN. Khi downstream node là FORK, một hàm mới `dispatchForkBranches` fan-out atomically: với mỗi
  outcome khai báo trên FORK (= một branch), tạo token, và hoặc terminalize ngay (branch's own first edge
  đã là JOIN) hoặc tạo branch's own first NodeRun (gắn `BranchTokenID`) rồi dispatch nó bằng đúng logic
  autoAdvance/executable/WAIT/APPROVAL routing path chính đã có — cố ý DUPLICATE một phần nhỏ logic đó
  thay vì tổng quát hoá route chính (đã test rất kỹ qua V4-03…V4-09) thành một hàm đệ quy chung, đổi lấy
  việc giữ nguyên path chính không đổi. Một branch's own first node là FORK khác (nested fork) bị bỏ
  PENDING có chủ đích — cùng kỷ luật "enqueue only, task sau sở hữu" đã dùng cho mọi node type chưa có
  owner; thực ra `validateForkJoinTopology` (V2-08) đã reject nested fork/join ở compile time nên nhánh
  này defensive/unreachable trên document hợp lệ. `decideRetryOrExhaustion` (V4-06,
  `internal/app/runtime/finalize.go`) mở rộng: khi CAS NodeRun sang FAILED (non-retryable hoặc hết
  budget) và NodeRun đó mang `BranchTokenID`, CAS token đó FAILED trong CÙNG transaction. FORK's own
  NodeRun tự CAS thẳng SUCCEEDED với `SelectedOutcome` rỗng ngay khi fan-out xong (một FORK nhận mọi
  outcome khai báo cùng lúc, không chỉ một). Domain event mới `NODE_FORKED` (registered, golden fixture)
  ghi lại toàn bộ fan-out một lần cho mỗi FORK activation.
- **Verify:** duplicate dispatch, partial crash, branch failure tests. Đã triển khai (`fork_test.go`,
  `fork_sqlite_test.go`, 4 test): fan-out atomically tạo đúng BranchToken/NodeRun cho mỗi declared branch
  (kể cả zero-hop "branch's own first edge là chính JOIN") cộng một `NODE_FORKED` event
  (`TestAdvanceRun_Fork_FansOutToEveryBranchAtomically`); redelivered job trên chính NodeRun đã route đi
  không tạo token/NodeRun/job/event thứ hai nào (`advanceRunTx`'s own idempotent-replay guard đã có từ
  V4-03, không cần logic mới) — `TestAdvanceRun_Fork_DuplicateDispatch_IsIdempotent`; một branch's own
  NodeRun thật sự chạy tới FAILED (non-retryable) CAS đúng token của nó sang FAILED trong cùng transaction
  mà không đụng token của branch khác —
  `TestExecuteNodeHandler_Fork_BranchFailure_TerminalizesOwnBranchToken`; cộng
  `TestAdvanceRun_SQLite_Fork_PersistsAcrossRestart` (đóng/mở lại `sqlite.Store` thật giữa fan-out và lần
  đọc lại — chứng minh cả FORK's own NodeRun lẫn hai BranchToken quan sát đúng state sau restart, vì cả
  fan-out cam kết trong đúng một transaction nên chỉ có thể quan sát "toàn bộ" hoặc "chưa gì cả").
- **Hoàn thành khi:** mỗi declared branch có đúng một live token/terminal record.
- **Nguồn:** HE-14-M09.

## V4-11 — JOIN ALL/ANY/QUORUM

- **Mục tiêu:** join theo persisted tokens và declared policy.
- **Phụ thuộc:** V4-10.
- **Thực hiện:** readiness/short-circuit/cancel remaining policy, shared-state merge validation, integration scope.
  **Hai câu hỏi chốt với user trước khi code:**
  1. *Verdict tính từ token set thế nào, và khi bất khả thi thì JOIN đi đâu:* chọn hướng inline, không đổi
     schema, nhưng user siết lại công thức và semantics chính xác hơn đề xuất ban đầu của tôi. Token set
     luôn thuộc đúng MỘT lần chạy FORK (`ForkNodeRunID`), không gom theo RunID hay ForkNodeKey — mỗi lần
     FORK bị V4-07 cycle kích hoạt lại là một tập token độc lập. Công thức khoá: ALL — thành công khi
     S=N, bất khả thi khi có FAILED hoặc CANCELLED; ANY — thành công khi S≥1, bất khả thi khi S=0 &&
     ACTIVE=0; QUORUM(q) — thành công khi S≥q, bất khả thi khi S+ACTIVE<q (chính là "impossible quorum"
     Verify line yêu cầu). JOIN NodeRun dùng identity tất định từ (ForkNodeRunID, JoinNodeKey) qua một
     content hash (không nối chuỗi tuỳ ý — tránh va chạm với format ID thật khác trong repo), get-or-create
     tự nhiên idempotent cho nhiều branch cùng tới. Branch ĐẦU TIÊN tới JOIN tạo NodeRun đó ở WAITING,
     KHÔNG SUCCEEDED thẳng. Mỗi lần token đổi trạng thái (branch tới JOIN, hoặc branch NodeRun FAILED giữa
     chừng) đều phải, trong CÙNG transaction: CAS token có fencing → đọc lại toàn bộ token set tươi → tính
     verdict → get-or-create JOIN NodeRun → nếu bất khả thi, CAS WAITING→FAILED (không outcome, không
     route tiếp, không tạo blocker, chưa tự fail WorkflowRun — V4-12 aggregate cấp Run) → nếu thoả mãn VÀ
     mọi token đã terminal, CAS WAITING→SUCCEEDED rồi route outcome trong cùng transaction. JOIN phải khai
     đúng MỘT outcome — compiler reject 0 hoặc nhiều hơn 1 (`validateJoinConfig` siết thêm, không phải chỉ
     runtime tự suy luận). Event riêng `JOIN_DECIDED` (mode, quorum, đủ 4 count SUCCEEDED/FAILED/
     CANCELLED/ACTIVE, verdict, reason=`JOIN_POLICY_UNSATISFIABLE` khi FAILED) — không giả làm
     RETRY_EXHAUSTED của V4-06. Không thêm FailureOutcome vào JoinNodeConfig: user từ chối hướng đó vì
     biến một lỗi kết hợp song song thành business route mới là mở rộng semantics chưa có authority nào
     trong tài liệu hiện tại.
  2. *Nhánh còn ACTIVE khi verdict đã quyết thì xử lý sao:* đây là điểm user SỬA sâu nhất so với đề xuất
     ban đầu của tôi (tôi đề xuất CAS chúng sang CANCELLED ngay khi ANY/QUORUM đạt ngưỡng sớm). User chỉ
     ra: dù threshold có thể tính được sớm (mathematically locked in), Alpha KHÔNG được tự ý cancel — worker
     có thể vẫn đang chạy hoặc ghi dữ liệu thật, và Alpha chưa có branch-level cancellation/quiescence
     fencing nào. Chốt policy: có thể tính được success sớm, nhưng JOIN vẫn ở WAITING và KHÔNG route
     downstream cho tới khi MỌI branch của fork đó đạt trạng thái terminal — chỉ nhánh FAILED mới được
     short-circuit sớm, vì FAILED không bao giờ route đi đâu cả nên không có gì "lãng phí" khi quyết định
     sớm (không có downstream nào chạy song song với branch còn sống để mà race). Không có early branch
     cancellation nào trong V4-11 — muốn route sớm thật sự (khi ANY/QUORUM đã chắc chắn) thì phải thiết kế
     cancellation/quiescence có fencing riêng trước, việc đó nằm ngoài phạm vi task này.
  Kết quả: `advanceRunTx`'s own JOIN-arrival block (V4-10) và `finalize.go`'s own `decideRetryOrExhaustion`
  (V4-06, khi CAS một branch NodeRun sang FAILED) đều gọi chung một hàm mới `evaluateJoinTx` — hàm này tái
  dùng `advanceRunTx` chính nó để route JOIN's own NodeRun đi khi thành công (không viết một dispatch
  switch thứ hai). `dispatchForkBranches`'s own zero-hop "branch's own first edge đã là JOIN" case (V4-10)
  cũng phải gọi `evaluateJoinTx` — tự phát hiện khi code: một FORK mà MỌI branch đều zero-hop tới JOIN sẽ
  không bao giờ có hop bình thường nào để trigger evaluation, nếu bỏ sót điểm này JOIN's own NodeRun sẽ
  không bao giờ được tạo. `ports.RuntimeRepository` thêm `ListBranchTokensForFork` (scoped đúng một fork
  occurrence, khác `ListBranchTokensForRun` đã có từ V4-10) cho việc đọc token set tươi.
- **Verify:** policy matrix, restart, impossible quorum và duplicate completion tests. Đã triển khai
  (`join_test.go`, `join_sqlite_test.go`, 9 test): ALL thành công khi cả hai branch SUCCEEDED và route
  đúng "end"; ALL fail ngay khi một branch FAILED mà không chờ branch còn lại (token branch kia vẫn ACTIVE,
  không bị đụng); ANY chờ đủ MỌI branch terminal dù threshold đã đạt sớm (branch A SUCCEEDED trước, JOIN
  vẫn chưa quyết cho tới khi branch B cũng terminal); ANY fail khi mọi branch đều FAILED; QUORUM(2)/3
  branch — "impossible quorum" fail ngay khi 2 branch đã FAILED (branch thứ 3 vẫn ACTIVE, không bị đụng) —
  đúng test Verify line yêu cầu; QUORUM thành công đạt ngưỡng sớm nhưng vẫn chờ branch cuối terminal; test
  "duplicate completion" đúng nghĩa đen — branch B hoàn thành THẬT sau khi JOIN đã FAILED từ branch A, token
  B vẫn SUCCEEDED bình thường nhưng JOIN không bị re-decide/re-event/re-route; test shared-state — hai
  branch khác nhau patch hai field khác nhau, cả hai cùng có mặt trong SharedState cuối khi JOIN route đi,
  chứng minh cơ chế `applySharedStatePatch`+CAS (HE-14-M04, V4-03) đã có sẵn áp dụng đúng cho hội tụ FORK/
  JOIN mà không cần machinery mới — cộng `TestAdvanceRun_SQLite_Join_PersistsAcrossRestart` (đóng/mở lại
  `sqlite.Store` thật GIỮA lúc branch A đã hoàn thành thật qua pipeline Schedule/Finalize và branch B chưa
  chạy, verify JOIN NodeRun WAITING trước restart rồi SUCCEEDED/route đúng "end" sau restart trên UnitOfWork
  hoàn toàn mới).
- **Hoàn thành khi:** queue empty không ảnh hưởng join verdict.
- **Nguồn:** HE-14-M04, HE-07-M06.

## V4-12 — Run completion candidate và WorkItem projection proposal

- **Mục tiêu:** END chuyển run sang `VERIFYING`, chưa tự `SUCCEEDED` hoặc DONE WorkItem.
- **Phụ thuộc:** V4-07…V4-11.
- **Thực hiện:** terminal path validation, run `VERIFYING|FAILED`, emit completion-request event;
  WorkItem chỉ giữ ACTIVE/BLOCKED cho tới verification service V5. Task này **không** tạo đường tới
  `CANCELLED`: cancellation chỉ thuộc V4-12B và luôn đi qua `CANCELLING`.
  **Hai câu hỏi chốt với user trước khi code:**
  1. *"run VERIFYING|FAILED" nghĩa là gì — FAILED chỉ là nhánh phòng thủ của riêng END, hay V4-12 phải
     xây Run-level failure aggregation thật:* task text tiếng Việt mơ hồ; đọc kỹ ADR-011/ADR-021/V5-11
     (CompletionPolicy service) xác nhận PASS/REWORK/BLOCK/FAIL từ `VERIFYING` hoàn toàn thuộc V5-11,
     KHÔNG phải V4-12. Nhưng V4-06's own `decideRetryOrExhaustion` và V4-11's own `evaluateJoinTx` đều có
     sẵn comment "never an automatic WorkflowRun failure — V4-12's own scope" / "V4-12 sẽ aggregate trạng
     thái cấp Run" — forward-reference đã ghi sẵn trong code đã merge. Hỏi thẳng user: đây là yêu cầu thật
     hay chỉ là ghi chú dự định. User xác nhận: **yêu cầu thật**, có bằng chứng rõ (V4-06/V4-11 đều chủ
     động dừng ở NodeRun FAILED và giao việc tổng hợp cho V4-12; không xử lý thì một node hết retry budget
     sẽ để Run mắc vĩnh viễn ở RUNNING dù không còn job hay đường tiến nào) — nhưng SỬA sâu đề xuất ban đầu
     của tôi ở 3 điểm: (a) không chỉ gọi aggregation lúc NodeRun chuyển FAILED — phải chạy sau MỌI
     transaction có thể loại bỏ live activation cuối mà không chắc chắn tạo downstream (NodeRun FAILED,
     JOIN FAILED, branch cuối hoàn tất SAU KHI JOIN đã fail, WAIT/APPROVAL terminal, END dispatch) — dùng
     MỘT reducer chung, không phải một query gọi riêng lẻ từng nơi; (b) BLOCKED không phải "non-terminal"
     lẫn "failure" — phải phân loại RIÊNG (live/blocked/terminal, 3 nhóm), vì blocker/retry/cancel protocol
     (V4-12A/V4-12B) sở hữu BLOCKED, không được tự fail Run chỉ vì còn BLOCKED; (c) END không có "hai
     outcome bình thường" — reducer phải có priority order rõ: Run không RUNNING/WAITING → no-op; có live
     → giữ nguyên; không live nhưng có BLOCKED → không fail; END hợp lệ đã đạt → VERIFYING; không live/
     BLOCKED/END nhưng có FAILED → FAILED (reason RUN_FAILED); còn lại (không gì cả) → FAILED (reason
     TERMINAL_PATH_INVALID, nhánh phòng thủ). Reducer phải chạy SAU khi downstream activation/job đã được
     tạo trong cùng transaction, tránh quan sát "zero live" giả giữa hai bước.
  2. *Điều kiện chính xác "Run không thể tiến thêm":* tôi đề xuất "zero NodeRun còn non-terminal", user
     CHỌN đúng hướng đó (không cần graph reachability đầy đủ — publish-time validation đã đảm bảo mọi node
     có đường TỚI END trên document, không có nghĩa Run THẬT SỰ còn tiến được sau khi một node cụ thể đã
     fail và không có outcome để route) nhưng sửa lại mô tả chính xác: Run đang RUNNING/WAITING AND không
     có NodeRun live (PENDING/READY/QUEUED/RUNNING/WAITING) AND không có NodeRun BLOCKED AND chưa có END
     completion candidate hợp lệ AND tồn tại failure/dead-end authoritative (NodeRun FAILED, JOIN FAILED
     hoặc terminal-path invariant vi phạm) → Run FAILED. Cần query theo NHÓM
     (`RunNodeStateSummary{LiveCount, BlockedCount, FailedCount, ReachedEndNodeRunID}`), không phải một
     boolean mơ hồ.
  **Tự phát hiện một BUG THẬT của V4-11 khi viết test cho task này (không phải câu hỏi mới):**
  `dispatchForkBranches`'s own zero-hop "branch's own first edge đã là JOIN" case (V4-10, gọi
  `evaluateJoinTx` từ V4-11) đánh giá JOIN's own policy dựa trên token set TẠI THỜI ĐIỂM ĐÓ trong vòng lặp
  — nhưng `workflow.Compile` CANONICALIZE (sort alphabetically) `Node.Outcomes`, nên thứ tự
  `forkNode.Outcomes` sau compile KHÔNG PHẢI thứ tự authoring. Một FORK có 1 branch zero-hop-tới-join
  ("shortcut") và 1 branch AGENT thật ("to_implement") — sau sort, "shortcut" < "to_implement" nên được xử
  lý TRƯỚC — tại thời điểm đó "to_implement"'s own token CHƯA được tạo, nên `ListBranchTokensForFork` chỉ
  thấy 1 token (shortcut, SUCCEEDED) → ALL mode tính sai S=1/N=1 → JOIN quyết SUCCEEDED sớm và route tới
  END ngay, dù "to_implement" còn chưa chạy. Bug này ĐÃ CÓ trong V4-11 đã merge, chỉ chưa bị test nào bắt
  được (V4-11's own test dùng `joinPolicyDocument`, không có branch zero-hop nào). Sửa: tách
  `dispatchForkBranches` thành HAI pass — pass 1 tạo MỌI BranchToken trước (không đánh giá gì), pass 2 mới
  dispatch/evaluate từng branch — đúng nghĩa đen HE-14-M09's own "create tokens atomically".
  Phạm vi code: `internal/app/runtime/completion.go` (mới) — `RunNodeStateSummary`,
  `computeRunNodeStateSummary`, `reconcileRunTerminalityTx` (reducer chung), event mới
  `RUN_COMPLETION_REQUESTED`/`RUN_FAILED` (registered, golden fixture, round-trip test). `advance.go`:
  `isEndNode` state assignment (END → SUCCEEDED ngay, không case riêng trong switch — reconciler tail call
  generic đã đủ); reconciler gọi ở CẢ HAI exit point của `advanceRunTx` (dispatch switch chính, JOIN-arrival
  early-return) và ở cuối `decideRetryOrExhaustion` (`finalize.go`, document giờ load UNCONDITIONALLY, không
  chỉ khi có BranchTokenID). `ports.RuntimeRepository` thêm `ListNodeRunsForRun`/`TransitionWorkflowRunState`
  (sqlite + fake).
- **Verify:** agent proposed done/no evidence test; assert END không có transition nào tới `CANCELLED`. Đã
  triển khai (`completion_test.go`, `completion_sqlite_test.go`, `completion_internal_test.go`, 9 test):
  END reached → Run VERIFYING + `RUN_COMPLETION_REQUESTED`, WorkItem KHÔNG đổi (vẫn ACTIVE) — đúng test
  "agent proposed done/no evidence" (AK-ARCH-005/GC-INV-12); NodeRun FAILED không còn live nào khác (cả
  dạng thường lẫn qua FORK branch) → Run FAILED; branch A fail trong khi branch B còn ACTIVE → Run vẫn
  RUNNING, chỉ FAILED khi branch B sau đó tự terminate thật (chứng minh reducer chạy đúng ở MỌI exit point
  liên quan, không chỉ lần đầu); BLOCKED sibling không bao giờ tự fail Run (dù là NodeRun duy nhất còn lại)
  — reuse `TransitionNodeRun` trực tiếp để seed BLOCKED (chưa có producer thật nào); restart thật giữa lúc
  Run vừa VERIFYING (đóng/mở `sqlite.Store`, verify cả Run.State lẫn END NodeRun lẫn WorkItem chưa đổi sau
  restart) — cộng 2 white-box test nội bộ (`package runtime`, mirror `event_schema_test.go`'s own tiền lệ)
  cho nhánh TERMINAL_PATH_INVALID (không có producer thật nào cho CANCELLED để trigger qua public API) và
  "Run đã quyết rồi thì reducer không re-fire".
- **Hoàn thành khi:** exit code/outcome/END không có đường trực tiếp tới Run SUCCEEDED, WorkItem DONE
  hoặc Run CANCELLED.
- **Nguồn:** ADR-011, ADR-020, AK-ARCH-005A, GC-INV-12.

## V4-12A — Scope amendment và NodeRun reactivation

- **Mục tiêu:** áp scope expansion đã duyệt mà không sửa quyền lịch sử hoặc mở quyền cho sibling.
- **Phụ thuộc:** V3-08, V4-04, V4-12.
- **Phạm vi:** orchestrator/manifest/runtime schema và fake executor; không thêm UI/API transport.
- **Thực hiện:** Attempt hiện tại kết thúc terminal `BLOCKED` với
  `TerminationReason=SCOPE_EXPANSION_REQUIRED` — không dùng `FAILED`, vì `FAILED` trộn business blocker
  với technical failure và làm AttemptPolicy retry sai. Sau provision append amendment, tạo activation
  mới với `ReactivationReason=SCOPE_EXPANDED`, pin revision mới; sibling/activation/attempt cũ giữ scope
  cũ và Attempt đã BLOCKED không được hồi sinh.
  **Hai câu hỏi chốt với user trước khi code:**
  1. *Cơ chế nào để một Attempt thực sự raise được một request thật và giữ đúng liên kết:* đề xuất ban
     đầu của tôi là "widen `NodeExecutionResult` + một job riêng gọi `work.RequestScopeExpansion`" — user
     CHỌN đúng hướng đó nhưng sửa một chi tiết chốt: `ScopeExpansionRequestID` phải được **RESERVE ngay
     trong transaction finalize BLOCKED**, không được backfill sau khi handler tạo request xong — chỉ ra
     đúng race/crash-window: "`RequestScopeExpansion` tạo request thành công. Process crash trước khi ghi
     RequestID vào runtime link. Operator có thể approve request mà V4-12A chưa tìm được Attempt nguồn."
     User chốt luôn state/field matrix chặt: `NodeExecutionResult.RequestedScopeExpansion` chỉ hợp lệ khi
     `State=BLOCKED` + `TerminationReason=SCOPE_EXPANSION_REQUIRED`; sai hình dạng (SUCCEEDED/FAILED có
     kèm proposal, hoặc BLOCKED thiếu proposal) → `OUTCOME_REJECTED`, không bao giờ tạo scope request.
     `requestScopeExpansionTx` (trong transaction finalize) phải: validate JobLease/WriteLease/cancel
     fence như bình thường, validate + canonicalize proposal, RESERVE RequestID, tạo
     `attempt_scope_expansion_origins` (attempt_id PK, request_id UNIQUE NOT NULL, proposal_hash,
     reactivated_node_run_id UNIQUE NULL), CAS Attempt rồi NodeRun sang BLOCKED, enqueue job
     `REQUEST_SCOPE_EXPANSION` (`IdempotencyKey=scope-expansion:<attempt-id>`) — **BLOCKED tuyệt đối
     không đi qua retry policy**. Handler của job đó gọi `work.RequestScopeExpansion` thật trong transaction
     RIÊNG của nó, truyền đúng RequestID đã reserve (không tự mint mới) — actor là `system:runtime`, không
     phải identity của executor (executor chỉ đề xuất, không tự authenticate) và cũng không phải một
     operator thật (không ai quyết định raise request này, chính Attempt BLOCKED tự làm). Crash giữa lúc
     request đã tạo và job chưa complete: redelivery replay đúng cùng request qua idempotency key, không
     tạo request thứ hai, không mất liên kết. User dứt khoát từ chối phương án hẹp hơn ("chỉ build
     reactivate, không build đường raise request") vì "để thiếu chính đường tạo request mà ADR-002 đã yêu
     cầu và không thể kiểm thử flow end-to-end của V4-12A".
  2. *V4-12A biết khi nào request đã approved VÀ đã provision xong bằng cách nào:* user chọn job tự
     lên lịch lại (self-rescheduling reconcile job) nhưng sửa: KHÔNG polling ngay từ lúc request còn
     PENDING chờ người duyệt — job đầu tiên chỉ được enqueue **khi đã approved**, vì "`WAIT_TIMER`/
     `APPROVAL_TIMER` hiện chỉ chạy một lần tại `DueAt`; chúng không phải tiền lệ chính xác cho polling
     lặp." Chốt flow: `ApproveScopeExpansion` (V3-08, không sửa hành vi cũ) trong CÙNG transaction đã bump
     ScopeVersion/ghi grant/enqueue WORKSPACE_PROVISION, giờ thêm bước enqueue ĐÚNG MỘT job
     `SCOPE_EXPANSION_RECONCILE` **khi và chỉ khi** request có origin runtime thật (tra
     `GetScopeExpansionOriginByRequestID`, `ErrPersistenceNotFound` là trường hợp bình thường cho request
     tạo thủ công/UI, không phải lỗi). Handler mỗi lần claim đọc state authoritative: request PENDING là
     nhánh phòng thủ/không thể xảy ra (no-op, vì approve luôn enqueue job mới); REJECTED/WITHDRAWN → origin
     REJECTED, không reactivate; APPROVED + WorkspaceSet REQUESTED/PROVISIONING → enqueue successor với
     backoff tăng dần có trần rồi complete job hiện tại; APPROVED + WorkspaceSet READY → còn phải verify
     `BaseRevisionSet` thực sự phủ MỌI repository đã request (không tin `State=READY` một mình, vì đó có
     thể là snapshot cũ/thiếu) rồi mới append amendment + tạo activation MỚI trong cùng transaction;
     WorkspaceSet BLOCKED/RELEASING/RELEASED → không polling vô hạn, đánh dấu origin NEEDS_RECOVERY và dừng.
     Kỷ luật job: `SCOPE_EXPANSION_RECONCILE` là `RUN_WORK` (không phải `CONTROL`) vì có thể tạo activation
     mới nên phải chịu cancel fence (khái niệm V4-12B); enqueue successor và complete job hiện tại phải
     cùng một transaction, `IdempotencyKey=scope-expansion-reconcile:<attempt-id>:<generation>` với CAS
     trên `poll_generation` để duplicate delivery không bao giờ mint hai successor. Transaction reactivate
     cuối cùng phải: check Run không CANCELLING/terminal, check request còn APPROVED, append
     RunManifestAmendment, tạo ĐÚNG MỘT activation mới, copy Iteration (không tăng cycle budget), **nếu
     node gốc nằm trong FORK branch thì copy luôn BranchTokenID, nếu không JOIN sẽ chờ sai branch mãi
     mãi**, pin scope/manifest revision mới, set `ReactivationReason=SCOPE_EXPANDED`, CAS
     `reactivated_node_run_id` để hai lần approve/reconcile trùng nhau chỉ bao giờ tạo đúng một activation.
     User từ chối phương án hẹp hơn ("chỉ hỗ trợ trường hợp không cần provision mới") vì "nó để hở chính
     trường hợp quan trọng nhất của scope expansion: thêm repository mới."
  Phạm vi code: `internal/domain/runtime/scope_expansion.go` (mới) —
  `ScopeGrantProposal`/`ScopeExpansionProposal` (`Validate`/`CanonicalJSON` sort theo RepositoryID để hash
  ổn định), `ScopeExpansionReconcileStatus` (PENDING/REACTIVATED/REJECTED/NEEDS_RECOVERY),
  `ScopeExpansionOrigin`/`NewScopeExpansionOrigin` (luôn khởi tạo PENDING/pollGeneration=0, hash
  sha256 canonical proposal). Migration `0022_scope_expansion_reactivation.sql` —
  `attempt_scope_expansion_origins` (attempt_id PK, request_id UNIQUE NOT NULL, reactivated_node_run_id
  UNIQUE NULL, poll_generation, version) + `node_runs.reactivation_reason` (`NOT NULL DEFAULT ''`).
  `ports.RuntimeRepository` thêm bốn method (`CreateScopeExpansionOrigin`,
  `GetScopeExpansionOriginByAttemptID`, `GetScopeExpansionOriginByRequestID`,
  `TransitionScopeExpansionOrigin`) — sqlite (`scope_expansion_origin.go`) + fake. `ports.NodeExecutionResult`
  thêm `RequestedScopeExpansion *runtime.ScopeExpansionProposal`. `finalize.go`: `BLOCKED` giờ nằm trong
  `isFinalizableExecutionAttemptState`, validation state/field matrix ở tầng finalize (không chỉ tầng
  domain), `requestScopeExpansionTx` (CAS BLOCKED, reserve RequestID, tạo origin, enqueue job, rồi gọi
  `reconcileRunTerminalityTx` — V4-12's own reducer — ở cuối, vì BLOCKED loại bỏ live activation cuối
  giống hệt FAILED). `execute.go`: `ExecuteNodeHandler.Handle` tách nhánh dịch kết quả thành switch theo
  `State`, thêm case `ExecutionAttemptBlocked` dịch đúng "sai hình dạng → OUTCOME_REJECTED". `internal/app/
  work/scope_expansion.go`: `RequestScopeExpansionRequest.RequestID` (optional, chỉ runtime path nội bộ
  truyền; public/UI luôn để trống), `ApproveScopeExpansion` thêm bước tra origin + enqueue
  `SCOPE_EXPANSION_RECONCILE` đúng một lần. `internal/app/runtime/scope_expansion.go` (mới) —
  `RequestScopeExpansionHandler` (gọi `work.RequestScopeExpansion` thật với RequestID đã reserve, actor
  `system:runtime`), `ScopeExpansionReconcileHandler` (state machine đầy đủ ở trên),
  `workspaceSetCoversEveryGrant` (kiểm tra `BaseRevisionSet` phủ đủ), `enqueueScopeExpansionReconcileSuccessorTx`
  (backoff tăng dần trần 300s, CAS `poll_generation`), `reactivateBlockedNodeRunTx` (tạo activation mới,
  copy NodeKey/Iteration/BranchTokenID, copy DecisionArtifact profile sang ID mới, enqueue
  `ScheduleNodeRunJobKind` — tái dùng nguyên `ScheduleExecutableNodeRun` pipeline, không tự resolve
  EffectiveScope/ManifestRevision lần hai).
- **Verify:** concurrent approval, duplicate delivery, restart và negative sibling/effective-scope tests.
  Đã triển khai (`scope_expansion_test.go`, `scope_expansion_sqlite_test.go`, 6 test): base transition
  (BLOCKED không spawn attempt retry, tạo đúng origin + job); end-to-end pipeline THẬT hoàn toàn — dùng
  `workspaceprovision.Handler` thật (không mock) để provision repo-2 THẬT MỚI, chứng minh
  reconcile job tự reschedule khi còn PROVISIONING rồi mới reactivate khi READY, activation mới có
  `ReactivationReason=SCOPE_EXPANDED` + NodeKey/Iteration copy đúng + EffectiveScope family có thêm
  repo-2, Attempt/NodeRun gốc BLOCKED vĩnh viễn; duplicate delivery của job reconcile đã REACTIVATED là
  no-op tuyệt đối (không tạo activation thứ hai); REJECTED path — origin REJECTED, không bao giờ
  reactivate; sibling test — mọi NodeRun khác đã tồn tại trước reactivation (START, activation BLOCKED
  gốc) không bị đụng, chỉ đúng một activation mới xuất hiện với BranchTokenID copy đúng (so sánh theo giá
  trị, không theo con trỏ, vì fake repository clone() mỗi lần round-trip); restart thật giữa lúc origin
  còn PENDING (đóng/mở `sqlite.Store`, verify cả `GetScopeExpansionOriginByAttemptID` lẫn
  `GetScopeExpansionOriginByRequestID` đọc đúng sau restart, Attempt/NodeRun vẫn BLOCKED).
- **Hoàn thành khi:** chỉ activation mới nhận grant và audit nối request→approval→amendment→activation.
- **Nguồn:** ADR-011, ADR-020, AK-ARCH-015A, GC-INV-22, GC-DS-02.

## V4-12B — Cancellation coordinator

- **Mục tiêu:** `CancelRun` là protocol quiesce có durable intent, không phải một CAS đặt Run thành
  `CANCELLED`. V4 sở hữu orchestration semantics; V5-08C sở hữu process/provider cancellation; V6 chỉ
  expose transport.
- **Phụ thuộc:** V4-08, V4-09, V4-11, V4-12.
- **Phạm vi:** orchestrator/scheduler, `cancellation_intents`, cancel fence `cancel_epoch` và fake
  executor; không terminate process thật và không thêm route HTTP.
- **Thực hiện:** `CancelRun` atomically ghi durable intent, chuyển Run `CANCELLING`, append event và
  enqueue job, idempotent theo run; scheduler ngừng tạo activation/technical retry/rework mới ngay khi
  intent commit; WAIT, APPROVAL, durable job chưa claim và NodeRun chưa chạy chuyển `CANCELLED`; Attempt
  đã tạo cùng job nhưng chưa khởi động chuyển `QUEUED → CANCELLED` với
  `TerminationReason=RUN_CANCELLED_BEFORE_START`, không set `StartedAt`, spawn count bằng 0 — không
  Attempt nào được để mắc kẹt ở `QUEUED`. Run chỉ tới `CANCELLED` sau khi execution quiesce hoặc
  reconciliation xong. Cancel **không** tự cleanup workspace và **không** tự abandon ReleaseSet; nó đưa
  WorkItem về `BLOCKED` kèm blocker `RUN_CANCELLED`, còn `CancelWorkItem` và `ResolveWorkItemBlocker` là
  hai command riêng.
  Cancel fence dùng `cancel_epoch` chứ không phải boolean, và **chỉ áp cho `JobClass=RUN_WORK`**:
  `WorkflowRun.cancel_epoch` đi `NULL → 1` khi intent commit; cùng transaction fence mọi job `RUN_WORK`
  nonterminal của Run bằng `cancel_epoch=1` và chuyển job `RUN_WORK` `AVAILABLE` sang `CANCELLED`. Job
  `CONTROL` không bị đụng — nếu fence cả chúng thì coordinator job do chính `CancelRun` enqueue sẽ bị
  fence bởi transaction sinh ra nó và cancel không bao giờ chạy. Claim CAS tách theo class: `RUN_WORK`
  dùng `WHERE job_class='RUN_WORK' AND state='AVAILABLE' AND cancel_epoch IS NULL`, `CONTROL` dùng
  `WHERE job_class='CONTROL' AND state='AVAILABLE'`; hai partial index riêng. Enqueue `RUN_WORK` mới CAS
  rằng Run còn non-cancelling. Worker re-check epoch tại hai chốt: trước `QUEUED → RUNNING` và ngay
  trước `ProcessSupervisor.Start`. `DurableJob` nhận thêm `RunID?` và `JobClass`, trong đó `JobClass`
  suy từ `Kind` bằng mapping tĩnh có constraint chứ không phải cột tự do. Run bị cancel từ `CREATED`
  cũng đi qua `CANCELLING`.
  Điều kiện no-op là **Run đã terminal**, không phải "có transaction nào đó commit trước": Attempt
  finalize `SUCCEEDED` và END → `VERIFYING` đều để Run ở trạng thái còn cancel được, nên `CancelRun` vẫn
  phải được chấp nhận. Chỉ `PASS` và `FAIL` làm Run terminal và biến cancel thành no-op idempotent;
  `BLOCK` và `REWORK` thì không. Cancel intent commit trước thì mọi authoritative outcome đến muộn bị CAS
  từ chối, và external success đến muộn chỉ lưu thành observation/evidence để reconcile.
  **Hai câu hỏi chốt với user trước khi code:**
  1. *Job coordinator CancelRun tự enqueue thực sự làm gì, Run đi tới CANCELLED bằng đường nào:* đề xuất
     "Recommended" của tôi (một job CONTROL `CANCEL_RUN_COORDINATOR`, sweep đồng bộ MỘT LẦN — CAS mọi WAIT
     ACTIVE/APPROVAL PENDING/Attempt+NodeRun QUEUED/NodeRun chưa chạy khác sang CANCELLED trong đúng một
     transaction — rồi hoặc CAS thẳng Run CANCELLING→CANCELLED nếu hết live, hoặc để lại CANCELLING chờ
     Attempt RUNNING cuối cùng tự finalize; mở rộng `reconcileRunTerminalityTx` — V4-12 — thêm nhánh
     CANCELLING-và-zero-live→CANCELLED làm điểm đóng DUY NHẤT, gọi từ cả coordinator lẫn mọi call site cũ)
     được CHỌN — nhưng câu trả lời thứ hai của user cho câu hỏi này bị lỗi công cụ AskUserQuestion lặp lại
     y hệt nội dung một câu hỏi KHÁC (về hotfix SPK-13 trước đó) hai lần liên tiếp, dù đã hỏi lại riêng.
     Không hỏi lại lần ba (tránh lặp lỗi và làm phiền user thêm) — tự quyết định tiến tới với phương án
     "Recommended" vì đó là lựa chọn xuất hiện ĐẦU TIÊN trong nội dung bị lặp lại (dấu hiệu gần nhất với
     một lựa chọn thật) và tự nó biện minh được chặt chẽ nhất từ chính text task (Verify line đòi test
     "cancellation job CONTROL vẫn claim được" — vô nghĩa nếu không có job CONTROL nào thực sự cần chạy)
     và từ tiền lệ codebase (sweep một lần trong một transaction, giống hệt `dispatchForkBranches`/
     `reconcileRunTerminalityTx` chính nó) — đã báo minh bạch với user đây là quyết định do lỗi công cụ,
     không phải do user thật sự xác nhận, mời sửa nếu sai trước khi merge.
  2. *JobClass CONTROL allow-list xử lý sao khi ADR/design doc dùng tên lệch code thật:* user chốt dùng
     ĐÚNG ba hằng số thật `CANCEL_RUN_COORDINATOR`/`WorkspaceReconciliationJobKind`
     ("WORKSPACE_RECONCILIATION", không phải `WORKSPACE_RECONCILE` như doc cũ)/`WorkspaceSetReleaseJobKind`
     ("WORKSPACE_SET_RELEASE"), loại hẳn `RECOVERY_REAPER` khỏi allow-list (không phải placeholder, không
     mở rộng scope biến nó thành job thật) vì `Store.RecoverExpiredJobs` là một thao tác bảo trì toàn cục
     do ticker của `workerpool.Pool` gọi trực tiếp — không có row/lease/claim/RunID riêng nên
     JobClass/cancel_epoch không có gì để gắn vào; và khóa bằng test rằng ba kind trên phân loại CONTROL,
     mọi kind khác (kể cả `RECOVERY_REAPER`/`WORKSPACE_RECONCILE` chính nó) mặc định `RUN_WORK`, và caller
     không có field nào để tự khai `JobClass=CONTROL`.
  **Tự phát hiện 3 vấn đề khi code (không phải câu hỏi mới):**
  (a) *Migration count off-by-one:* file `0013_*.sql` đã bị thiếu từ lịch sử trước (chỉ có 0001-0012 rồi
  nhảy thẳng 0014) — nghĩa là SỐ FILE migration luôn kém SỐ HIỆU cao nhất đúng 1. Bump ban đầu
  `migrationCount != 21` lên `!= 23` (theo đúng số hiệu file mới `0023`) sai — con số ĐÚNG là `22` (21 file
  cũ + 1 file mới, không phải theo số hiệu). Sửa lại `db_test.go`/`unitofwork_test.go` đúng `22`.
  (b) *RUNNING structural node bị bỏ sót trong sweep:* thiết kế ban đầu chỉ sweep PENDING/READY/QUEUED/
  WAITING, loại hẳn RUNNING (lý do: "Alpha không force-stop được Attempt thật đang chạy"). Nhưng viết test
  `TestCancelRun_CreatedRun_MovesToCancellingThenCancelled` phát hiện: một node CẤU TRÚC (START/ROUTER/
  FORK/END, auto-advance) được gán RUNNING NGAY LÚC TẠO và KHÔNG BAO GIỜ có ExecutionAttempt thật đi kèm —
  đường tiến duy nhất của nó là một job `ADVANCE_RUN` mà `FenceAndCancelRunJobs` đã fence từ trước, nên nếu
  sweep bỏ qua nó, NodeRun đó mắc kẹt RUNNING vĩnh viễn không ai đóng. Sửa: sweep thêm RUNNING nhưng CHỈ
  khi NodeRun đó không có ExecutionAttempt nào (phân biệt qua tập NodeRunID có Attempt, xây từ
  `ListExecutionAttemptsForRun`'s own kết quả) — một AGENT node RUNNING có Attempt thật vẫn được bỏ qua
  nguyên vẹn.
  (c) *Stale read giữa các lần lặp trong cùng transaction:* sweep NodeRun ban đầu dùng snapshot MỘT LẦN từ
  `ListNodeRunsForRun` cho cả vòng lặp. Viết test FORK+JOIN(ALL) phát hiện: khi branch thứ nhất bị cancel,
  `terminalizeBranchTokenForCancelledNodeRunTx`'s own `evaluateJoinTx` có thể NGAY LẬP TỨC CAS JOIN's own
  NodeRun sang FAILED (ALL-mode infeasible ngay khi có 1 token CANCELLED) — nhưng vòng lặp vẫn cầm bản
  snapshot CŨ (WAITING, version cũ) của đúng NodeRun đó, nên khi tới lượt xử lý nó sẽ CAS nhầm
  `ExpectedVersion` đã lỗi thời, ra `ErrOptimisticConflict` làm SẬP TOÀN BỘ transaction sweep. Sửa:
  `GetNodeRun` lại (fresh) ngay trước mỗi lần quyết định/CAS trong vòng lặp, không tin snapshot ban đầu.
- **Verify:** cancel khi đang WAIT/APPROVAL/fork branch/retry backoff; duplicate `CancelRun`; cancel rồi
  restart process; test khẳng định không activation/retry/rework mới nào được tạo sau intent; test khẳng
  định Run không nhảy thẳng `RUNNING → CANCELLED`. Bốn race test bắt buộc: cancel-vs-attempt-success,
  cancel-vs-END, cancel-vs-CompletionPolicy `PASS`, và stale finalize sau cancel intent — mỗi test chạy
  cả hai thứ tự commit, cộng cancel-vs-BLOCK và cancel-vs-REWORK để chứng minh hai trường hợp đó
  **không** biến cancel thành no-op; và test cancel khi Attempt còn `QUEUED` phải cho
  `RUN_CANCELLED_BEFORE_START` với `StartedAt` rỗng. Thêm **cancel-vs-claim** chạy cả hai thứ tự commit:
  cancel commit trước thì claim phải trượt CAS và spawn count bằng 0; `RUNNING` commit trước thì đi qua
  active cancellation. Thêm test enqueue `RUN_WORK` mới trên Run đang `CANCELLING` bị CAS từ chối, test
  **cancellation job `CONTROL` vẫn claim được trong khi workload job của cùng Run thì không**, và test
  cancel một Run đang `CREATED`. Thêm negative test cho mapping: mọi `Kind` ngoài allow-list `CONTROL`
  phải ra `RUN_WORK`, `Kind` chưa biết mặc định `RUN_WORK`, và ghi thẳng `job_class='CONTROL'` cho một
  Kind không thuộc allow-list bị constraint từ chối.
  Đã triển khai (`internal/app/ports/job_class_test.go`,
  `internal/adapters/sqlite/cancel_run_test.go`, `internal/app/runtime/cancel_run_test.go`,
  `cancel_run_race_test.go`, 30 test): `ClassifyJobKind` — 3 kind CONTROL thật phân loại đúng, mọi kind
  khác (kể cả `RECOVERY_REAPER`/doc-typo `WORKSPACE_RECONCILE`/kind lạ/rỗng) mặc định `RUN_WORK`; CHECK
  constraint sqlite từ chối cả hai chiều sai (CONTROL cho kind lạ, RUN_WORK cho kind CONTROL thật) và chấp
  nhận cặp đúng; `EnqueueJob` tự tính đúng `job_class` từ `Kind`, không có field nào cho caller tự khai;
  enqueue RUN_WORK cho Run đang CANCELLING/CANCELLED bị `ErrRunCancelling`, mọi state khác (CREATED/
  RUNNING/WAITING/BLOCKED/VERIFYING) vẫn được chấp nhận; claim CAS thật — job CONTROL vẫn claim được trong
  khi job RUN_WORK cùng Run đã bị fence không bao giờ claim lại được nữa (kể cả sau khi lease hết hạn và
  `RecoverExpiredJobs` đưa nó về AVAILABLE); restart thật giữa lúc đã fence (đóng/mở `sqlite.Store`, verify
  `workflow_runs.cancel_epoch` và `durable_jobs.run_id/job_class/cancel_epoch` đọc đúng sau restart). Ở
  tầng app (fake): cancel một Run CREATED (không NodeRun nào live/RUNNING) đi qua CANCELLING rồi tự đóng
  CANCELLED; cancel một Run đã SUCCEEDED/FAILED bị `ErrRunAlreadyTerminal`; duplicate `CancelRun` idempotent
  (không tạo intent/coordinator job thứ hai); cancel khi đang WAIT — registration CANCELLED, NodeRun
  CANCELLED, Run đóng CANCELLED; cancel khi đang APPROVAL — tương tự; cancel một Attempt còn QUEUED —
  đúng `RUN_CANCELLED_BEFORE_START` với `StartedAt` rỗng; cancel giữa một FORK (JOIN ALL, một branch
  zero-hop đã SUCCEEDED, branch còn lại QUEUED) — branch còn lại CANCELLED, BranchToken CANCELLED, JOIN tự
  đóng (FAILED hoặc CANCELLED tùy thứ tự sweep — cả hai đều là outcome terminal hợp lệ, không bao giờ treo
  WAITING mãi); test khẳng định không activation/retry/job mới nào được tạo sau khi intent đã commit (một
  outcome SUCCEEDED muộn của Attempt đang RUNNING vẫn được chấp nhận như sự kiện lịch sử thật nhưng không
  tạo NodeRun/job mới nào); cancel-vs-claim cả hai thứ tự (cancel trước → `claimRunning` từ chối tiến
  QUEUED→RUNNING, để lại cho coordinator; RUNNING trước → đi qua active cancellation); cancel-vs-BLOCK
  (BLOCKED không bao giờ bị sweep, Run ở lại CANCELLING đúng như thiết kế, không bị coi là no-op) và
  cancel-vs-REWORK (một hop cycle-exhaustion forced-escalation đang dở dang vẫn để Run tiến tới
  CANCELLING/CANCELLED, không bị cancel bỏ qua). Bốn race bắt buộc, mỗi cái cả hai thứ tự commit:
  cancel-vs-attempt-success, cancel-vs-END (Run không bao giờ dừng ở VERIFYING một khi đã cancelling),
  cancel-vs-CompletionPolicy PASS (giả lập bằng đúng CAS `TransitionWorkflowRunState` V5-11 sẽ dùng —
  cancel trước thì PASS bị `ErrOptimisticConflict` tự nhiên, không cần code đặc biệt; PASS trước thì
  `CancelRun` bị `ErrRunAlreadyTerminal`), và stale finalize sau cancel intent (cả nhánh FAILED muộn lẫn
  một lần finalize lặp lại sau khi Run đã CANCELLED hẳn, bị chặn đúng bởi `ErrJobLeaseLost` — tầng fencing
  JobLease sẵn có, không cần nhánh code riêng cho "Run đã cancelled").
- **Hoàn thành khi:** mọi đường tới `CANCELLED` đều đi qua `CANCELLING`, cancel không tự sinh quyết định
  cleanup/abandon nào, và không race nào tạo được `SUCCEEDED` sau khi cancel intent đã commit.
- **Nguồn:** ADR-020, GC-INV-32, GC-INV-33, GC-INV-37, GC-INV-28.

## V4-12C — WorkItem cancellation và blocker commands

- **Mục tiêu:** `CancelWorkItem` và `ResolveWorkItemBlocker` có application authority thật; hiện chúng
  chỉ được nhắc như "command riêng" mà không task nào sở hữu handler.
- **Phụ thuộc:** V4-12A, V4-12B.
- **Phạm vi:** WorkItem-level cancel intent, blocker lifecycle và hai handler; route thuộc V6-06B.
- **Thực hiện:** thêm `work_item_cancellation_intents` (intent theo Run không đủ vì một task có thể có
  nhiều run) và trạng thái blocker `OPEN|RESOLVED|WAIVED`. `CancelWorkItem` ghi intent bền vững rồi
  quiesce từng active Run bằng đúng protocol V4-12B; WorkItem chỉ terminal sau khi mọi Run đã dừng.
  `ResolveWorkItemBlocker` **tự** chuyển blocker `OPEN → RESOLVED|WAIVED` — nó không đòi blocker đã
  resolved từ trước; precondition thật là blocker đang `OPEN`, không còn Run nonterminal và không
  workspace nào `QUARANTINED`; blocker đã `RESOLVED|WAIVED` là no-op idempotent. Chuyển blocker và mở
  khóa WorkItem là **hai** điều kiện tách rời: blocker target luôn được chuyển, nhưng WorkItem chỉ
  `BLOCKED → READY` khi số blocker `OPEN` còn lại bằng 0 — một WorkItem ba blocker mà resolve một cái
  thì vẫn `BLOCKED`. Cả command này lẫn `StartWorkflowRun` đều CAS kiểm không có
  `work_item_cancellation_intent` đang pending, để task đang bị hủy không bị mở khóa rồi khởi động run
  mới trong khoảng giữa intent và terminalize. Payload MUST chọn resolution mode tường minh, không có mặc định: `RESOLVED` cần
  reason và revalidate điều kiện; `WAIVED` cần actor, reason, policy grant và một `DecisionArtifact`.
  Bốn admission reason và `SCOPE_EXPANSION_REQUIRED` không bao giờ waive được (bảng ở ADR-020). Bốn
  admission reason được lưu thành blocker type để valid-action query đọc được. Intent bền vững phải
  recover được: V4-13 quét intent còn dở sau restart.
  **Hai câu hỏi chốt với user trước khi code:**
  1. *V4-12B (đã merge) chưa hề tạo blocker hay đụng `WorkItem.Status` khi Run đóng `CANCELLED` — task đó
     đúng theo Phạm vi của chính nó ("orchestrator/scheduler, cancel_epoch") không nhắc WorkItem. V4-12C
     có nên mở rộng ngược lại `transitionRunToCancelledTx` để tạo blocker `RUN_CANCELLED` + chuyển
     WorkItem sang `BLOCKED` tại đúng thời điểm đó không?* User chốt **có** — nếu chỉ tạo schema/command
     rồi seed blocker trong test thì contract ADR-020 vẫn chưa được triển khai end-to-end; đây chính là
     task đầu tiên có blocker persistence và WorkItem authority nên là thời điểm đúng để hoàn thiện hậu
     điều kiện còn thiếu của V4-12B. Kèm một sửa sâu thêm: `reconcileCancellingRunTx` (V4-12B) đòi cả
     `LiveCount==0` VÀ `BlockedCount==0` — có thể làm Run mắc kẹt CANCELLING vĩnh viễn vì không gì trong
     protocol từng resume một activation BLOCKED. Điều kiện đóng đúng: `LiveCount==0` một mình; NodeRun/
     Attempt BLOCKED giữ nguyên làm lịch sử, không cản Run đóng. Trong cùng transaction đóng Run: CAS
     CANCELLING→CANCELLED, tạo blocker tất định (Type=RUN_CANCELLED, State=OPEN, liên kết SourceRunID,
     idempotent theo Run), WorkItem ACTIVE→BLOCKED (hoặc chỉ thêm blocker nếu đã BLOCKED), append event.
     Ngoại lệ: nếu Run được quiesce do `CancelWorkItem` (đã có `work_item_cancellation_intent`, bất kỳ
     state nào), KHÔNG tạo blocker mở — WorkItem đã trên đường tới CANCELLED, không được đẩy ngược về
     BLOCKED.
  2. *Bốn admission reason và `SCOPE_EXPANSION_REQUIRED` chưa có producer thật trong codebase hôm nay.
     V4-12C có cần tự xây producer thật cho loại nào trong số này không, hay chỉ RUN_CANCELLED có blocker
     thật?* User chốt wiring thật thêm cho SCOPE_EXPANSION_REQUIRED (dùng producer có sẵn từ V4-12A),
     nhưng phải nối đủ cả vòng đời chứ không chỉ tạo blocker: blocker mở trong CÙNG transaction Attempt/
     NodeRun chuyển BLOCKED (`requestScopeExpansionTx`); approval đơn thuần CHƯA resolve blocker (workspace
     có thể còn provisioning); chỉ khi SCOPE_EXPANSION_RECONCILE xác nhận approved + WorkspaceSet READY +
     tạo activation mới xong, TRONG CÙNG transaction: blocker OPEN→RESOLVED, WorkItem BLOCKED→ACTIVE
     (không phải READY — cùng Run tiếp tục, chưa từng dừng). `ResolveWorkItemBlocker` công khai KHÔNG được
     tự resolve/waive SCOPE_EXPANSION_REQUIRED ở bất kỳ mode nào — authority chỉ thuộc approval/reconcile
     flow (ADR-011). Bốn admission reason + `COMPLETION_POLICY_FAILED` chỉ ở tầng type/matrix (producer
     thuộc V5-08/V5-11); test cho các loại này seed blocker trực tiếp, chỉ khoá authority matrix (WAIVED
     luôn từ chối cho admission + scope-expansion; RESOLVED generic cho scope-expansion cũng bị từ chối).
- **Verify:** `CancelWorkItem` với 0/1/nhiều active Run; race `CancelWorkItem`-vs-Completion `PASS` cả
  hai thứ tự commit (PASS trước → no-op idempotent; intent trước → PASS bị CAS từ chối);
  `ResolveWorkItemBlocker` khi blocker `OPEN` (**thành công**), khi đã `RESOLVED|WAIVED` (no-op), khi còn
  Run nonterminal, khi workspace `QUARANTINED`; ma trận resolution mode × blocker type theo bảng ADR-020,
  gồm `WAIVED` trên bốn admission reason và `SCOPE_EXPANSION_REQUIRED` đều bị từ chối, và `WAIVED` thiếu
  policy grant hoặc `DecisionArtifact` bị từ chối; payload thiếu mode bị reject. Thêm test WorkItem có
  nhiều blocker: resolve từng cái một, WorkItem giữ `BLOCKED` cho tới khi cái cuối được xử lý mới sang
  `READY`. Thêm race `ResolveWorkItemBlocker`-vs-`CancelWorkItem` cả hai thứ tự commit (intent trước →
  resolve không đưa được về `READY`; resolve trước → intent vẫn quiesce và terminalize đúng). Assert
  không đường nào terminalize WorkItem trong khi Run còn chạy.
  **Tự phát hiện 1 bug thật khi viết test:** `ResolveWorkItemBlocker`'s own result ban đầu tính
  `WorkItemUnblocked` bằng `finalStatus != BLOCKED` — dương tính giả khi WorkItem đã rời BLOCKED vì một lý
  do KHÁC (CancelWorkItem đóng nó thành CANCELLED trong khi blocker vẫn còn OPEN). Sửa: `closeWorkItemBlockerTx`
  trả về tín hiệu unblocked thật, tính một lần bên trong hàm nơi hai điều kiện tách rời được kiểm, không
  bao giờ suy lại từ status cuối ở tầng gọi.
  Đã triển khai (`internal/app/runtime/cancel_work_item_test.go`,
  `resolve_work_item_blocker_test.go` + mở rộng `scope_expansion_test.go`/`cancel_run_test.go`, 27 test
  mới): 0/1/nhiều active Run đều quiesce đúng qua `cancelRunTx` dùng lại từ V4-12B (tách khỏi `CancelRun`
  thành hàm tx-scoped riêng để không mở transaction lồng); idempotent duplicate; đã DONE/CANCELLED bị
  `ErrWorkItemAlreadyTerminal`; race intent-trước-PASS mô phỏng CAS thật của một CompletionPolicy tương
  lai (chưa tồn tại trong codebase) → `ErrOptimisticConflict` tự nhiên, không cần code đặc biệt; toàn bộ
  ma trận resolution-mode × blocker-type 11 case; race resolve-vs-cancel cả hai thứ tự; blocker
  SCOPE_EXPANSION_REQUIRED thật resolve đúng + WorkItem về ACTIVE khi reactivation thật xảy ra (mở rộng
  test end-to-end V4-12A có sẵn); viết lại một test V4-12B cũ (`TestFinalizeExecutionAttempt_BlockedThenCancel_...`)
  vì hành vi CHỦ Ý thay đổi — Run giờ đóng CANCELLED dù NodeRun còn BLOCKED.
- **Hoàn thành khi:** mọi WorkItem `BLOCKED` có ít nhất một đường thoát có authority, và không command
  nào có precondition vòng tròn.
- **Nguồn:** ADR-020, GC-INV-36, GC-INV-38, GC-INV-39.

## V4-13 — Recovery coordinator

- **Mục tiêu:** quyết định retry/fresh-Start/reconcile/escalate từ durable state.
- **Phụ thuộc:** V4-05…V4-12, V4-12B và V4-12C.
- **Thực hiện:** startup scan và periodic recovery reaper cho expired jobs/running attempts, read-only
  LOST, mutating INDETERMINATE, stale finalize rejection và next diagnostic action. Reaper cũng quét
  `run_cancellation_intents` và `work_item_cancellation_intents` còn dở: một intent đã commit nhưng
  coordinator chết trước khi quiesce xong phải được tiếp tục, không để Run mắc kẹt ở `CANCELLING` hay
  WorkItem mắc kẹt chờ terminal. Reaper job là `JobClass=CONTROL` nên không tự bị cancel fence.
- **Verify:** kill tại sáu fault boundary; lease hết hạn sau startup và hai coordinator tranh recovery
  vẫn chỉ tạo một recovery job cho generation; kill coordinator giữa lúc quiesce rồi restart phải hoàn
  tất cả cancel Run lẫn cancel WorkItem, không tạo intent trùng.
- **Hoàn thành khi:** mỗi crash state có typed recovery, không blind retry.
- **Nguồn:** GC-ACC-04, GC-ACC-05, HE-05-M05, AK-ARCH-006, AK-ARCH-007, HE-01-M01, HE-01-M05.

## V4-14 — Runtime engine acceptance gate

- **Mục tiêu:** chạy graph chứa tất cả node types bằng fake executors qua restart.
- **Phụ thuộc:** V4-01…V4-13, V4-12A, V4-12B và V4-12C.
- **Thực hiện:** fixture có retry, rework, approval, wait, fork/join, terminal candidate, scope-expansion
  BLOCKED và một cancel giữa chừng; inject crash.
- **Verify:** deterministic event/activation golden, race test, full test/vet Windows/Linux.
- **Hoàn thành khi:** same input/decisions tạo same domain result trừ IDs/timestamps cho phép.
- **Nguồn:** GC-ACC-03.
