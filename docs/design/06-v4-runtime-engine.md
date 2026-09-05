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
- **Verify:** bounded cycle pass/exhaust/restart tests.
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
- **Verify:** duplicate/early/wrong signal, restart timer, hai signal đồng thời cùng identity, và test
  khẳng định xóa/replay timer job không consume thêm lần nào.
- **Hoàn thành khi:** WAIT sống qua process restart và signal chỉ consume một lần.
- **Nguồn:** GC-INV-31, HE-13-M06.

## V4-09 — APPROVAL semantics

- **Mục tiêu:** approval node chỉ resolve bằng typed operator command.
- **Phụ thuộc:** V4-08.
- **Thực hiện:** approval request/evidence/actor/reason/timeout; approve/reject outcomes; chat không tự resolve.
- **Verify:** unauthorized-shaped input, duplicate decision, timeout/restart tests.
- **Hoàn thành khi:** decision append-only và route audit đủ.
- **Nguồn:** HE-14-S03, GC-INV-11, HE-08-M08.

## V4-10 — FORK branch tokens

- **Mục tiêu:** phát persisted branch identity không dựa queue order.
- **Phụ thuộc:** V4-03.
- **Thực hiện:** create tokens atomically, activate branches idempotently, static write-scope admission.
- **Verify:** duplicate dispatch, partial crash, branch failure tests.
- **Hoàn thành khi:** mỗi declared branch có đúng một live token/terminal record.
- **Nguồn:** HE-14-M09.

## V4-11 — JOIN ALL/ANY/QUORUM

- **Mục tiêu:** join theo persisted tokens và declared policy.
- **Phụ thuộc:** V4-10.
- **Thực hiện:** readiness/short-circuit/cancel remaining policy, shared-state merge validation, integration scope.
- **Verify:** policy matrix, restart, impossible quorum và duplicate completion tests.
- **Hoàn thành khi:** queue empty không ảnh hưởng join verdict.
- **Nguồn:** HE-14-M04, HE-07-M06.

## V4-12 — Run completion candidate và WorkItem projection proposal

- **Mục tiêu:** END chuyển run sang `VERIFYING`, chưa tự `SUCCEEDED` hoặc DONE WorkItem.
- **Phụ thuộc:** V4-07…V4-11.
- **Thực hiện:** terminal path validation, run `VERIFYING|FAILED`, emit completion-request event;
  WorkItem chỉ giữ ACTIVE/BLOCKED cho tới verification service V5. Task này **không** tạo đường tới
  `CANCELLED`: cancellation chỉ thuộc V4-12B và luôn đi qua `CANCELLING`.
- **Verify:** agent proposed done/no evidence test; assert END không có transition nào tới `CANCELLED`.
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
- **Verify:** concurrent approval, duplicate delivery, restart và negative sibling/effective-scope tests.
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
- **Hoàn thành khi:** mọi đường tới `CANCELLED` đều đi qua `CANCELLING`, cancel không tự sinh quyết định
  cleanup/abandon nào, và không race nào tạo được `SUCCEEDED` sau khi cancel intent đã commit.
- **Nguồn:** ADR-020, GC-INV-32, GC-INV-33, GC-INV-37, GC-INV-28.

## V4-12C — WorkItem cancellation và blocker commands

- **Mục tiêu:** `CancelWorkItem` và `ResolveWorkItemBlocker` có application authority thật; hiện chúng
  chỉ được nhắc như "command riêng" mà không task nào sở hữu handler.
- **Phụ thuộc:** V4-12B.
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
