# V5 — Execution, context, verification và evidence

> Entry: V4 fake-executor engine pass.
>
> Exit: AGENT/COMMAND/MACHINE_GATE chạy thật qua worker; context/evidence/recovery/completion đạt
> authority contract; Claude/Codex live smoke vẫn optional.

## V5-01 — Artifact metadata và retention migration

- **Mục tiêu:** gắn ArtifactStore V1 vào runtime bằng durable metadata/retention hold.
- **Phụ thuộc:** V4, V1 artifact store.
- **Thực hiện:** artifacts table/repository, attach transaction, orphan state, 7-day expiry, sensitivity.
- **Verify:** attach/restart/orphan/tamper/retention tests.
- **Hoàn thành khi:** evidence bắt buộc không commit trước artifact durable/hash verified.

## V5-02 — Conversation và Message authority

- **Mục tiêu:** task chat append-only thuộc platform.
- **Phụ thuộc:** V5-01.
- **Thực hiện:** conversation/message schema, content artifact, actor/correlation, append/list commands;
  size/media/redaction policy.
- **Verify:** ordering/idempotency/attempt linkage/secret tests.
- **Hoàn thành khi:** provider transcript không phải canonical message store.

## V5-03 — Resource registry và ContextAssembler

- **Mục tiêu:** resolve đúng Skill/Layer/Pack/messages/resources theo selector/priority/budget.
- **Phụ thuộc:** V2-06, V5-02.
- **Thực hiện:** applicability, conflict detection, relevance order, reserved budget, exact provenance/reason.
- **Verify:** component/task/block/risk selector matrix, conflict và deterministic manifest tests.
- **Hoàn thành khi:** resolver không last-wins hard constraint và không nạp mọi resource mặc định.

## V5-04 — Persist ContextSnapshot trước dispatch

- **Mục tiêu:** mỗi Attempt có immutable message/resource/RevisionSet manifest.
- **Phụ thuộc:** V5-03, V4-04.
- **Thực hiện:** snapshot schema/repository, canonical hash, attempt binding, dispatch precondition.
- **Verify:** tamper/mismatch/restart/missing resource tests.
- **Hoàn thành khi:** provider không start nếu snapshot chưa durable.

## V5-05 — Production ProcessSupervisor hardening

- **Mục tiêu:** argv-only spawn có timeout/cancel/output bounds/process-tree behavior Windows/Linux.
- **Phụ thuộc:** V1 config.
- **Thực hiện:** env allowlist, cwd validation, stdout/stderr bounded sinks, graceful then force cancel,
  termination classification.
- **Verify:** helper binary argument injection, timeout, cancel descendants, oversized output tests.
- **Hoàn thành khi:** không có shell string hoặc inherited environment mặc định.

## V5-06 — Claude adapter production contract

- **Mục tiêu:** map configured Claude CLI protocol vào canonical AgentExecutor.
- **Phụ thuộc:** V5-04, V5-05.
- **Thực hiện:** capability/version probe, Start/event/result/cancel, malformed JSONL fail-closed,
  ProviderSessionRef diagnostic-only.
- **Verify:** recorded/offline contract suite và fake child process.
- **Hoàn thành khi:** adapter không import app orchestrator/persistence và raw metadata không route state.

## V5-07 — Codex adapter production contract

- **Mục tiêu:** cùng contract/semantics như Claude với protocol riêng.
- **Phụ thuộc:** V5-06.
- **Thực hiện:** capability/version probe, Start/event/result/cancel, normalized differences allowlist.
- **Verify:** chạy nguyên suite V5-06 và semantic event diff.
- **Hoàn thành khi:** thêm Codex không tạo provider branch trong domain/app.

## V5-08 — AGENT node worker handler

- **Mục tiêu:** nối registry/profile/context/mounts/provider/fencing vào runtime engine.
- **Phụ thuộc:** V5-04, V5-06, V5-07, V3-09, V4-05.
- **Thực hiện:** capability admission, immutable envelope, event batching/checkpoint, diff capture, proposed
  outcome validation và fenced finalize.
- **Verify:** success/fail/cancel/lease loss/scope violation/provider loss E2E.
- **Hoàn thành khi:** replacement Attempt luôn Start từ snapshot; Resume call bằng 0.

## V5-09 — Command executor và COMMAND handler

- **Mục tiêu:** chạy published CommandVersion, không free-form shell.
- **Phụ thuộc:** V2-05, V5-05, V3-09, V4-05.
- **Thực hiện:** resolve argv placeholders/cwd/env/secret refs, permission admission, output artifact,
  exit/timeout mapping và diff/fence.
- **Verify:** injection, forbidden env/path/network-shaped policy, nonzero/timeout/lease-loss tests.
- **Hoàn thành khi:** resource script không chạy nếu thiếu exact CommandVersion/policy grant.

## V5-10 — Gate runner và criteria-level Evidence

- **Mục tiêu:** MACHINE_GATE tạo verdict authoritative với provenance.
- **Phụ thuộc:** V5-01, V5-09.
- **Thực hiện:** execute GateVersion, map criteria, PASS/FAIL/ERROR/NOT_RUN/NOT_APPLICABLE, revision freshness,
  evidence rows/artifacts.
- **Verify:** exit mapping, missing output, stale revision, N/A policy và tamper tests.
- **Hoàn thành khi:** error/missing evidence không thể PASS.

## V5-11 — CompletionPolicy service

- **Mục tiêu:** externalize NodeRun/WorkItem completion.
- **Phụ thuộc:** V5-10, V4-12.
- **Thực hiện:** required assurance levels, approvals, join, clean state, exact RevisionSet; emit typed
  pass/fail/rework/block decisions.
- **Verify:** maker claim vs gate matrix, stale evidence, required-level skip, approval missing tests.
- **Hoàn thành khi:** chỉ service có command path chuyển WorkItem DONE.

## V5-12 — Maker/checker isolation

- **Mục tiêu:** checker dùng fresh context tối thiểu, không maker transcript/reasoning.
- **Phụ thuộc:** V5-03, V5-10.
- **Thực hiện:** checker profile/context route chỉ requirement/diff/evidence; separate Attempt/session;
  criteria-level result.
- **Verify:** snapshot manifest asserts forbidden maker resources absent.
- **Hoàn thành khi:** same provider/model vẫn có independent attempt/context identity.

## V5-13 — Checkpoint/handoff và recovery integration

- **Mục tiêu:** crash real execution để lại structured status/blocker/evidence/next action.
- **Phụ thuộc:** V5-08…V5-12.
- **Thực hiện:** canonical checkpoint batches, recovery classifications, quarantine reconciliation,
  fresh context rebuild, no-progress/budget escalation.
- **Verify:** six fault boundaries với agent/command/gate fixtures.
- **Hoàn thành khi:** session mới không cần raw transcript hoặc cwd cũ.

## V5-14 — Cleanup/retention sweeper

- **Mục tiêu:** dọn owned temp/orphan/expired artifact an toàn.
- **Phụ thuộc:** V5-01, V5-13.
- **Thực hiện:** durable sweep job, holds, sealed/verified checks, dry-run report, idempotent cleanup;
  không release active/quarantined workspace.
- **Verify:** run twice, active hold, unknown file, 7-day boundary tests.
- **Hoàn thành khi:** cleanup không xóa material cần resume/evidence.

## V5-15 — Execution/evidence acceptance gate

- **Mục tiêu:** behavior thật chỉ complete bằng independent evidence.
- **Phụ thuộc:** V5-01…V5-14.
- **Thực hiện:** workflow agent→command→gate→checker→end; inject claim done, gate fail, crash, provider loss,
  scope violation và artifact tamper.
- **Verify:** full/race/Windows/Linux offline; optional manual live smoke ghi `UNVERIFIED_LIVE` nếu chưa chạy.
- **Hoàn thành khi:** trace WorkItem→revision/evidence đầy đủ và false completion bằng 0 trong fixtures.
