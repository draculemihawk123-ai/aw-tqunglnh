# V5 — Execution, context, verification và evidence

> Entry: V4 fake-executor engine pass.
>
> Exit: AGENT/COMMAND/MACHINE_GATE chạy thật qua worker; context/evidence/recovery/completion đạt
> authority contract; Claude/Codex live smoke vẫn optional.

> Phạm vi task mặc định: kế thừa mục 3 của `00-roadmap.md`.

## V5-01 — Artifact metadata và retention migration

- **Mục tiêu:** gắn ArtifactStore V1 vào runtime bằng durable metadata/retention hold.
- **Phụ thuộc:** V4, V1 artifact store.
- **Thực hiện:** artifacts table/repository, attach transaction, orphan state, typed retention class,
  sensitivity và hold. Chỉ raw output/evidence tạm mặc định 7 ngày; canonical Message/context/resource
  và audit metadata không nhận blanket TTL.
- **Verify:** attach/restart/orphan/tamper/retention tests.
- **Hoàn thành khi:** evidence bắt buộc không commit trước artifact durable/hash verified.

## V5-02 — Conversation và Message authority

- **Mục tiêu:** task chat append-only thuộc platform.
- **Phụ thuộc:** V5-01.
- **Thực hiện:** conversation/message schema, content/attachment artifact refs, actor/correlation,
  append/list commands; verified-before-attach, size/media/redaction policy.
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

- **Mục tiêu:** argv-only spawn có timeout/cancel/output bounds/process-tree behavior và khai đúng
  execution isolation profile trên Windows/Linux.
- **Phụ thuộc:** V1 config.
- **Thực hiện:** env allowlist, cwd validation, stdout/stderr bounded sinks, graceful then force cancel,
  termination classification; `ENFORCED_ISOLATED` chỉ khi filesystem/network enforcement thực sự có,
  còn fallback phải là `OPERATOR_TRUSTED_LOCAL` và vẫn hậu kiểm diff.
- **Verify:** helper binary argument injection, timeout, cancel descendants, oversized output; profile
  enforced có negative filesystem/network tests, trusted-local không được tự nhận là sandbox; spy
  assertion process spawn count bằng 0 khi enforcement không khả dụng.
- **Hoàn thành khi:** không có shell string hoặc inherited environment mặc định; không có đường code nào
  auto-downgrade `ENFORCED_ISOLATED` xuống `OPERATOR_TRUSTED_LOCAL`, và profile không cưỡng chế được trả
  `ISOLATION_ENFORCEMENT_UNAVAILABLE` **trước** `ProcessSupervisor.Start`.
- **Nguồn:** ADR-013, ADR-023.

## V5-06 — Claude adapter production contract

- **Mục tiêu:** map configured Claude CLI protocol vào canonical AgentExecutor.
- **Phụ thuộc:** V5-04, V5-05.
- **Thực hiện:** capability/version probe, immutable AdapterBuildVersion registration/admission,
  Start/event/result/cancel, malformed JSONL fail-closed, ProviderSessionRef diagnostic-only.
- **Verify:** recorded/offline contract suite và fake child process.
- **Hoàn thành khi:** adapter không import app orchestrator/persistence và raw metadata không route state.

## V5-07 — Codex adapter production contract

- **Mục tiêu:** cùng contract/semantics như Claude với protocol riêng.
- **Phụ thuộc:** V5-06.
- **Thực hiện:** capability/version probe, immutable AdapterBuildVersion registration/admission,
  Start/event/result/cancel, normalized differences allowlist.
- **Verify:** chạy nguyên suite V5-06 và semantic event diff.
- **Hoàn thành khi:** thêm Codex không tạo provider branch trong domain/app.

## V5-08 — AGENT admission và execution envelope

- **Mục tiêu:** dựng envelope bất biến và chặn mọi thứ không đủ điều kiện trước khi provider được spawn.
- **Phụ thuộc:** V5-04, V5-06, V5-07, V3-09, V4-05.
- **Phạm vi:** admission + envelope; event/checkpoint/diff thuộc V5-08A, finalize thuộc V5-08B.
- **Thực hiện:** exact AdapterBuildVersion/capability admission, isolation profile admission theo
  ADR-023, immutable envelope với đúng một `READ_WRITE` mount mặc định và mounts read-only còn lại.
- **Verify:** mỗi admission check fail-closed phải kết thúc Attempt bằng `QUEUED → BLOCKED` với đúng
  `TerminationReason`, `StartedAt` rỗng, spawn count bằng 0 và không tiêu thụ retry budget:
  isolation → `ISOLATION_ENFORCEMENT_UNAVAILABLE`; adapter drift → `ADAPTER_BUILD_DRIFT`; capability
  mismatch → `CAPABILITY_REQUIREMENT_UNSATISFIED`; multi-repo write thiếu grant →
  `WRITE_CAPABILITY_OR_GRANT_MISSING`. Thêm test `RetryBlockedActivation` tạo activation/Attempt mới sau
  khi pin được revalidate và không hồi sinh Attempt cũ.
- **Hoàn thành khi:** không request nào tới được ProcessSupervisor nếu thiếu một pin/grant bắt buộc,
  admission blocker không bị ghi nhầm thành `FAILED`, và mọi reason đều thuộc ma trận state–reason ở Go
  core spec §4.5.
- **Nguồn:** ADR-012, ADR-013, ADR-022, ADR-023, AK-ARCH-020A.

## V5-08A — AgentEvent contract, checkpoint batching và diff capture

- **Mục tiêu:** production contract của `agent_events` và checkpoint cho AGENT node, trên nền generic
  schema mà V4-01 sở hữu.
- **Phụ thuộc:** V5-08.
- **Thực hiện:** normalize event kind/sequence/schema version qua registry V1-07A, batching có bound,
  checkpoint sequence immutable, diff capture theo effective scope, redaction trước persist.
- **Verify:** event ordering/duplicate/oversized payload, checkpoint restart, diff vượt scope, secret
  fixture search bằng 0.
- **Hoàn thành khi:** raw provider metadata không route state và mọi event emit đều qua registry.
- **Nguồn:** ADR-005, ADR-017.

## V5-08B — Fenced finalize cho AGENT node

- **Mục tiêu:** worker chỉ propose outcome; commit chỉ xảy ra khi toàn bộ fencing còn hợp lệ.
- **Phụ thuộc:** V5-08A.
- **Thực hiện:** validate proposed outcome theo allow-list, kiểm JobLease/WriteLease/generation/attempt
  version/diff scope/evidence trong finalize transaction; replacement Attempt luôn `Start` từ snapshot.
- **Verify:** success/fail/lease loss/scope violation/provider loss E2E; assert Resume call count bằng 0
  và stale token không commit được.
- **Hoàn thành khi:** exit code 0 không tự tạo node success và stale finalize bị từ chối.
- **Nguồn:** ADR-005, ADR-011, AK-ARCH-009.

## V5-08C — Cancellation execution path

- **Mục tiêu:** phần thực thi của ADR-020: dừng process thật, rồi mới trả quyền cho orchestrator.
- **Phụ thuộc:** V4-12B, V5-05, V5-08B.
- **Phạm vi:** worker/process/provider cancellation; orchestration semantics đã thuộc V4-12B.
- **Thực hiện:** truyền cancellation token vào Attempt đang chạy; terminate process tree theo grace rồi
  force; chỉ release WriteLease **sau khi** xác nhận process đã dừng; mutating attempt không chứng minh
  được kết quả thành `INDETERMINATE` với workspace `QUARANTINED`.
- **Verify:** cancel giữa mutating AGENT attempt; child process bướng bỉnh; crash worker ngay sau
  terminate; assert không attempt nào bị gán `CANCELLED` khi outcome chưa xác định, và WriteLease không
  release sớm. COMMAND node dùng lại chính đường này và được verify ở V5-09.
- **Hoàn thành khi:** Run tới `CANCELLED` chỉ sau quiesce thật, và không có side effect nào bị tuyên bố
  sai trạng thái.
- **Nguồn:** ADR-020.

## V5-08D — RetryBlockedActivation handler

- **Mục tiêu:** Attempt `BLOCKED` do admission phải có đường thoát trong sản phẩm; thiếu task này một
  Attempt blocked vì adapter drift là dead-end.
- **Phụ thuộc:** V5-08.
- **Phạm vi:** application handler và revalidation semantics; route thuộc V6, action UI thuộc V7-12.
- **Thực hiện:** precondition Run/WorkItem, expected version và cancel fence (không retry Run đang
  `CANCELLING`); revalidate exact pin trong bước riêng; thành công thì atomically tạo NodeRun activation
  và Attempt mới; thất bại giữ nguyên blocker hiện tại và không tạo thêm blocked activation. Không repin
  Run — adapter drift không khôi phục được exact pin trả valid action `CancelRun`.
- **Verify:** revalidate thành công tạo đúng một activation mới; revalidate thất bại lặp 5 lần vẫn chỉ có
  một blocker và không sinh chuỗi blocked activation; retry trên Run `CANCELLING` bị từ chối; assert
  không đường nào repin Run sang build khác.
- **Hoàn thành khi:** mọi reason trong nhóm admission blocker đều có đường thoát hoặc một valid action
  thay thế tường minh.
- **Nguồn:** ADR-020, ADR-022.

## V5-09 — Command executor và COMMAND handler

- **Mục tiêu:** chạy published CommandVersion, không free-form shell.
- **Phụ thuộc:** V2-05, V5-05, V5-08C, V3-09, V4-05.
- **Thực hiện:** resolve argv placeholders/cwd/env/secret refs, permission admission, output artifact,
  exit/timeout mapping và diff/fence; nhiều repository `READ_WRITE` chỉ khi CommandVersion pin
  `INTEGRATION_MULTI_REPOSITORY_WRITE` và grant hiệu lực.
- **Verify:** injection, forbidden env/path/network-shaped policy, nonzero/timeout/lease-loss tests; và
  cancel giữa một mutating command dùng đúng đường V5-08C, không có đường terminate riêng.
- **Hoàn thành khi:** resource script không chạy nếu thiếu exact CommandVersion/policy grant.

## V5-10 — Gate runner và criteria-level Evidence

- **Mục tiêu:** MACHINE_GATE tạo verdict authoritative với provenance.
- **Phụ thuộc:** V5-01, V5-09.
- **Thực hiện:** execute GateVersion trên exact read-only RevisionSet/ReleaseSet, map criteria,
  PASS/FAIL/ERROR/NOT_RUN/NOT_APPLICABLE, revision freshness, evidence rows/artifacts; scratch output
  nằm ngoài source workspace.
- **Verify:** exit mapping, missing output, stale revision, N/A policy và tamper tests.
- **Hoàn thành khi:** error/missing evidence không thể PASS.

## V5-10A — ReleaseSet và typed local Git operation

- **Mục tiêu:** có authority local đa repository đủ cho completion/cleanup mà không giả transaction
  xuyên repository hoặc mở remote Git authority.
- **Phụ thuộc:** V3-11, V5-10.
- **Phạm vi:** ReleaseSet, local commit và application policy; không push/PR/merge/force-push.
- **Thực hiện:** schema/commands create-seal-abandon, exact base/result/verdict từng repository,
  content hash; `CreateLocalCommit` typed/audited; từ chối mọi remote operation trước Git adapter.
- **Verify:** partial result, stale revision, duplicate seal, cleanup eligibility, local commit và spy
  adapter chứng minh remote mutation call count bằng 0.
- **Hoàn thành khi:** ReleaseSet sealed/abandoned là input có provenance cho completion và cleanup.

## V5-11 — CompletionPolicy service

- **Mục tiêu:** externalize WorkflowRun/WorkItem completion bằng một authority duy nhất.
- **Phụ thuộc:** V5-10, V5-10A, V4-12.
- **Thực hiện:** required assurance levels, approvals, join, clean state, exact RevisionSet/ReleaseSet;
  persist DecisionArtifact cho **mọi** outcome. Bốn outcome có transition cố định theo ADR-021:
  `PASS` → Run `VERIFYING→SUCCEEDED` cùng WorkItem `→DONE` atomically; `REWORK` → Run `VERIFYING→RUNNING`
  kèm activation mới theo rework edge đã publish, WorkItem giữ `ACTIVE`; `BLOCK` → Run và WorkItem cùng
  `BLOCKED`; `FAIL` → Run `VERIFYING→FAILED` và WorkItem `→ BLOCKED` kèm blocker typed
  `COMPLETION_POLICY_FAILED` — WorkItem không có state `FAILED`, và không dùng `CANCELLED` vì đây không
  phải quyết định hủy. Thử lại sau `FAIL` cần operator command hoặc một run mới; hệ thống không tự đưa
  WorkItem về `ACTIVE`.
  `REWORK` không có rework edge hợp lệ trong graph đã pin MUST trở thành `BLOCK`; orchestrator không
  được tự dựng route.
- **Verify:** maker claim vs gate matrix, stale evidence, required-level skip, approval missing; ma trận
  bốn outcome × transition; test `REWORK` khi graph không có rework edge trả `BLOCK`; test `FAIL` tạo
  đúng một blocker `COMPLETION_POLICY_FAILED` và không tự reactivate WorkItem; test restart giữa decision
  và transition.
- **Hoàn thành khi:** chỉ service có command path chuyển cả Run SUCCEEDED và WorkItem DONE, và không
  outcome nào rời `VERIFYING` mà thiếu DecisionArtifact.
- **Nguồn:** ADR-011, ADR-021, AK-ARCH-005A.

## V5-12 — Maker/checker isolation

- **Mục tiêu:** checker dùng fresh context tối thiểu, không maker transcript/reasoning.
- **Phụ thuộc:** V5-03, V5-10.
- **Thực hiện:** checker profile/context route chỉ requirement/diff/evidence; separate Attempt/session;
  read-only exact revision mounts, scratch ngoài source; criteria-level result.
- **Verify:** snapshot manifest asserts forbidden maker resources absent; write source/local commit bị
  policy/fence từ chối.
- **Hoàn thành khi:** same provider/model vẫn có independent attempt/context identity.

## V5-13 — Checkpoint/handoff và recovery integration

- **Mục tiêu:** crash real execution để lại structured status/blocker/evidence/next action.
- **Phụ thuộc:** V5-08…V5-12 và V5-10A.
- **Thực hiện:** canonical checkpoint batches, recovery classifications, quarantine reconciliation,
  fresh context rebuild, no-progress/budget escalation.
- **Verify:** six fault boundaries với agent/command/gate fixtures.
- **Hoàn thành khi:** session mới không cần raw transcript hoặc cwd cũ.

## V5-14 — Cleanup/retention sweeper

- **Mục tiêu:** dọn owned temp/orphan/expired artifact an toàn.
- **Phụ thuộc:** V5-01, V5-13.
- **Thực hiện:** durable sweep job, typed retention class/holds, atomic reference recheck,
  sealed/verified checks, dry-run report, idempotent cleanup; không release active/quarantined workspace
  hoặc hard-delete canonical conversation. Task này cũng sở hữu internal `ExecuteWorkspaceSetRelease` —
  phần thực thi filesystem/Git của `RequestWorkspaceSetRelease` mà V3-11 chỉ ghi intent.
- **Verify:** run twice, active hold/reference, canonical message/context, unknown file và 7-day raw
  artifact boundary tests.
- **Hoàn thành khi:** cleanup không xóa material cần recovery/evidence/audit.

## V5-15 — Execution/evidence acceptance gate

- **Mục tiêu:** behavior thật chỉ complete bằng independent evidence.
- **Phụ thuộc:** V5-01…V5-14, V5-08A…V5-08D và V5-10A.
- **Thực hiện:** workflow agent→command→gate→checker→ReleaseSet→end/completion; inject claim done,
  gate fail, crash, provider loss, scope violation, checker write, adapter drift, isolation enforcement
  unavailable, cancel giữa mutating attempt và artifact tamper.
- **Verify:** full/race/Windows/Linux offline; optional manual live smoke ghi `UNVERIFIED_LIVE` nếu chưa chạy.
- **Hoàn thành khi:** trace WorkItem→revision/evidence đầy đủ và false completion bằng 0 trong fixtures.
