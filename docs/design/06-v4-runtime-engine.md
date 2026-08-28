# V4 — Durable workflow runtime engine

> Entry: V3 gate pass.
>
> Exit: compiled workflow chạy deterministic bằng fake executors qua retry, rework, wait, approval,
> fork/join và restart; chưa tích hợp completion authority đầy đủ với CLI/gates thật.

## V4-01 — Runtime schema và ExecutionManifest

- **Mục tiêu:** persist run/node/attempt/branch token/manifest theo design.
- **Phụ thuộc:** V3, V2.
- **Thực hiện:** migration, immutable manifest pins, repository contract, cross-project/version constraints.
- **Verify:** upgrade/restart/tamper pin tests.
- **Hoàn thành khi:** run không thể thay workflow/scope/dependency manifest sau create.

## V4-02 — StartWorkflowRun transaction

- **Mục tiêu:** tạo run, manifest, START activation, checkpoint/event và advance job atomically.
- **Phụ thuộc:** V4-01.
- **Thực hiện:** validate WorkItem READY/workspace ready/version compatibility; idempotent command.
- **Verify:** rollback/duplicate/concurrent start tests.
- **Hoàn thành khi:** một WorkItem policy chỉ có số active run cho phép và không orphan job.

## V4-03 — Deterministic activation/router core

- **Mục tiêu:** persisted transition kích đúng downstream node từ allow-listed outcome.
- **Phụ thuộc:** V4-02.
- **Thực hiện:** branch token-independent linear/router routing, shared-state typed writes, activation sequence.
- **Verify:** table/property tests order, missing/outside outcome, restart.
- **Hoàn thành khi:** scheduler không parse free text hoặc query authoring file.

## V4-04 — NodeRun/Attempt scheduling transaction

- **Mục tiêu:** executable node tạo attempt + durable job đúng một lần.
- **Phụ thuộc:** V4-03.
- **Thực hiện:** resolve effective scope/profile/context inputs; QUEUED states; idempotency by activation.
- **Verify:** duplicate ADVANCE_RUN và concurrent scheduler tests.
- **Hoàn thành khi:** queue delivery lặp không tạo duplicate attempt.

## V4-05 — Generic worker execution envelope với fake executor

- **Mục tiêu:** test engine không phụ thuộc provider/command adapter thật.
- **Phụ thuộc:** V4-04.
- **Thực hiện:** job claim, attempt RUNNING, optional write leases, fake typed events/result, fenced finalize.
- **Verify:** success/failure/timeout/cancel/lease-loss tests.
- **Hoàn thành khi:** worker chỉ propose outcome; orchestrator quyết transition.

## V4-06 — Technical retry policy

- **Mục tiêu:** retryable failure tạo Attempt mới dưới cùng NodeRun với budget/backoff.
- **Phụ thuộc:** V4-05.
- **Thực hiện:** typed error allowlist, max attempts, scheduled time, exhaustion outcome/escalation.
- **Verify:** fake clock tests retry/nonretry/timeout/exhaustion/restart.
- **Hoàn thành khi:** không parse message và không retry INDETERMINATE.

## V4-07 — Business rework/cycle budget

- **Mục tiêu:** rework chỉ qua edge và tạo NodeRun activation mới.
- **Phụ thuộc:** V4-03, V4-06.
- **Thực hiện:** iteration counter, max iteration, escalation route, audit causation.
- **Verify:** bounded cycle pass/exhaust/restart tests.
- **Hoàn thành khi:** không có infinite graph loop hoặc overwrite activation history.

## V4-08 — WAIT và signal semantics

- **Mục tiêu:** node chờ durable signal/timer không giữ worker/job lease.
- **Phụ thuộc:** V4-03.
- **Thực hiện:** wait registration, typed signal/idempotency, due-time job, cancel/timeout route.
- **Verify:** duplicate/early/wrong signal và restart timer tests.
- **Hoàn thành khi:** WAIT sống qua process restart và signal chỉ consume một lần.

## V4-09 — APPROVAL semantics

- **Mục tiêu:** approval node chỉ resolve bằng typed operator command.
- **Phụ thuộc:** V4-08.
- **Thực hiện:** approval request/evidence/actor/reason/timeout; approve/reject outcomes; chat không tự resolve.
- **Verify:** unauthorized-shaped input, duplicate decision, timeout/restart tests.
- **Hoàn thành khi:** decision append-only và route audit đủ.

## V4-10 — FORK branch tokens

- **Mục tiêu:** phát persisted branch identity không dựa queue order.
- **Phụ thuộc:** V4-03.
- **Thực hiện:** create tokens atomically, activate branches idempotently, static write-scope admission.
- **Verify:** duplicate dispatch, partial crash, branch failure tests.
- **Hoàn thành khi:** mỗi declared branch có đúng một live token/terminal record.

## V4-11 — JOIN ALL/ANY/QUORUM

- **Mục tiêu:** join theo persisted tokens và declared policy.
- **Phụ thuộc:** V4-10.
- **Thực hiện:** readiness/short-circuit/cancel remaining policy, shared-state merge validation, integration scope.
- **Verify:** policy matrix, restart, impossible quorum và duplicate completion tests.
- **Hoàn thành khi:** queue empty không ảnh hưởng join verdict.

## V4-12 — Run terminal và WorkItem projection proposal

- **Mục tiêu:** END tạo terminal candidate, chưa tự DONE WorkItem.
- **Phụ thuộc:** V4-07…V4-11.
- **Thực hiện:** terminal path validation, run SUCCEEDED/FAILED/CANCELLED, emit completion-request event;
  WorkItem chỉ giữ ACTIVE/BLOCKED cho tới verification service.
- **Verify:** agent proposed done/no evidence test.
- **Hoàn thành khi:** exit code/outcome không có đường trực tiếp tới WorkItem DONE.

## V4-13 — Recovery coordinator

- **Mục tiêu:** quyết định resume/retry/reconcile/escalate từ durable state.
- **Phụ thuộc:** V4-05…V4-12.
- **Thực hiện:** startup scan expired jobs/running attempts, read-only LOST, mutating INDETERMINATE,
  stale finalize rejection và next diagnostic action.
- **Verify:** kill tại six fault boundaries trong Go core spec.
- **Hoàn thành khi:** mỗi crash state có typed recovery, không blind retry.

## V4-14 — Runtime engine acceptance gate

- **Mục tiêu:** chạy graph chứa tất cả node types bằng fake executors qua restart.
- **Phụ thuộc:** V4-01…V4-13.
- **Thực hiện:** fixture có retry, rework, approval, wait, fork/join, terminal candidate; inject crash.
- **Verify:** deterministic event/activation golden, race test, full test/vet Windows/Linux.
- **Hoàn thành khi:** same input/decisions tạo same domain result trừ IDs/timestamps cho phép.
