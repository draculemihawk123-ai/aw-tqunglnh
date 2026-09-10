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
- **Nguồn:** AK-ARCH-021, GC-INV-20, HE-10-M07.

## V5-02 — Conversation và Message authority

- **Mục tiêu:** task chat append-only thuộc platform.
- **Phụ thuộc:** V5-01.
- **Thực hiện:** conversation/message schema, content/attachment artifact refs, actor/correlation,
  append/list commands; verified-before-attach, size/media/redaction policy.
- **Verify:** ordering/idempotency/attempt linkage/secret tests.
- **Hoàn thành khi:** provider transcript không phải canonical message store.
- **Nguồn:** HE-05-M07, HE-02-M05.

## V5-03 — Resource registry và ContextAssembler

- **Mục tiêu:** resolve đúng Skill/Layer/Pack/messages/resources theo selector/priority/budget.
- **Phụ thuộc:** V2-06, V5-02.
- **Thực hiện:** applicability, conflict detection, relevance order, reserved budget, exact provenance/reason.
- **Verify:** component/task/block/risk selector matrix, conflict và deterministic manifest tests.
- **Hoàn thành khi:** resolver không last-wins hard constraint và không nạp mọi resource mặc định.
- **Nguồn:** HE-04-M05, HE-04-S03, HE-13-M08.

## V5-04 — Persist ContextSnapshot trước dispatch

- **Mục tiêu:** mỗi Attempt có immutable message/resource/RevisionSet manifest.
- **Phụ thuộc:** V5-03, V4-04.
- **Thực hiện:** snapshot schema/repository, canonical hash, attempt binding, dispatch precondition.
- **Verify:** tamper/mismatch/restart/missing resource tests.
- **Hoàn thành khi:** provider không start nếu snapshot chưa durable.
- **Nguồn:** HE-04-M06, GC-INV-08, HE-02-M01, HE-03-M02, HE-05-M04.

## V5-05 — Production ProcessSupervisor hardening

- **Mục tiêu:** argv-only spawn có timeout/cancel/output bounds/process-tree behavior và khai đúng
  execution isolation profile trên Windows/Linux.
- **Phụ thuộc:** V1 config.
- **Thực hiện:** env allowlist, cwd validation, stdout/stderr bounded sinks, graceful then force cancel,
  termination classification; `ENFORCED_ISOLATED` chỉ khi filesystem/network enforcement thực sự có,
  còn fallback phải là `OPERATOR_TRUSTED_LOCAL` và vẫn hậu kiểm diff. Task này sở hữu production
  implementation của `ports.RuntimeExecutionConfigProvider` (ADR-027, port định nghĩa ở V4-04) — wiring
  thật từ `internal/app/config.Config` (đã có `ProcessOutputLimit`/`ProviderExecutables` từ V1-03) sang
  `RuntimeExecutionConfigSnapshotV1`, không đổi port/type mà V4-04 đã khóa.
- **Verify:** helper binary argument injection, timeout, cancel descendants, oversized output; profile
  enforced có negative filesystem/network tests, trusted-local không được tự nhận là sandbox; spy
  assertion process spawn count bằng 0 khi enforcement không khả dụng.
- **Hoàn thành khi:** không có shell string hoặc inherited environment mặc định; không có đường code nào
  auto-downgrade `ENFORCED_ISOLATED` xuống `OPERATOR_TRUSTED_LOCAL`, và profile không cưỡng chế được trả
  `ISOLATION_ENFORCEMENT_UNAVAILABLE` **trước** `ProcessSupervisor.Start`.
- **Nguồn:** ADR-013, ADR-023, ADR-027, HE-02-M03, GC-INV-30, GC-DS-09.

## V5-06 — Claude adapter production contract

- **Mục tiêu:** map configured Claude CLI protocol vào canonical AgentExecutor.
- **Phụ thuộc:** V5-04, V5-05.
- **Thực hiện:** capability/version probe, immutable AdapterBuildVersion registration/admission,
  Start/event/result/cancel, malformed JSONL fail-closed, ProviderSessionRef diagnostic-only.
- **Verify:** recorded/offline contract suite và fake child process.
- **Hoàn thành khi:** adapter không import app orchestrator/persistence và raw metadata không route state.
- **Nguồn:** GC-INV-23, HE-05-M01.

## V5-07 — Codex adapter production contract

- **Mục tiêu:** cùng contract/semantics như Claude với protocol riêng.
- **Phụ thuộc:** V5-06.
- **Thực hiện:** capability/version probe, immutable AdapterBuildVersion registration/admission,
  Start/event/result/cancel, normalized differences allowlist.
- **Verify:** chạy nguyên suite V5-06 và semantic event diff.
- **Hoàn thành khi:** thêm Codex không tạo provider branch trong domain/app.
- **Nguồn:** AK-ARCH-016, GC-ACC-12, HE-11-M08.

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
- **Nguồn:** ADR-012, ADR-013, ADR-022, ADR-023, AK-ARCH-020A, GC-INV-24, GC-DS-03, HE-02-M02.

## V5-08A — AgentEvent contract, checkpoint batching và diff capture

- **Mục tiêu:** production contract của `agent_events` và checkpoint cho AGENT node, trên nền generic
  schema mà V4-01 sở hữu.
- **Phụ thuộc:** V5-08.
- **Thực hiện:** normalize event kind/sequence/schema version qua registry V1-07A, batching có bound,
  checkpoint sequence immutable, diff capture theo effective scope, redaction trước persist.
- **Verify:** event ordering/duplicate/oversized payload, checkpoint restart, diff vượt scope, secret
  fixture search bằng 0.
- **Hoàn thành khi:** raw provider metadata không route state và mọi event emit đều qua registry.
- **Nguồn:** ADR-005, ADR-017, HE-01-M03.

## V5-08B — Fenced finalize cho AGENT node

- **Mục tiêu:** worker chỉ propose outcome; commit chỉ xảy ra khi toàn bộ fencing còn hợp lệ.
- **Phụ thuộc:** V5-08A.
- **Thực hiện:** validate proposed outcome theo allow-list, kiểm JobLease/WriteLease/generation/attempt
  version/diff scope/evidence trong finalize transaction; replacement Attempt luôn `Start` từ snapshot.
- **Verify:** success/fail/lease loss/scope violation/provider loss E2E; assert Resume call count bằng 0
  và stale token không commit được.
- **Hoàn thành khi:** exit code 0 không tự tạo node success và stale finalize bị từ chối.
- **Nguồn:** ADR-005, ADR-011, AK-ARCH-009, GC-INV-18.

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
- **Nguồn:** ADR-020, GC-DS-06.

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
- **Nguồn:** ADR-020, ADR-022, GC-INV-34.

## V5-09 — Command executor và COMMAND handler

- **Mục tiêu:** chạy published CommandVersion, không free-form shell.
- **Phụ thuộc:** V2-05, V5-05, V5-08C, V3-09, V4-05.
- **Trạng thái trên master (`39fb39c`, 2026-09-10):** PR #8 đã merge tại `2269cd8`.
  `CommandNodeExecutor`, `SecretResolver` dùng OS environment, executable materializer và test suite đã
  tồn tại. Executor reload exact CommandVersion/resource hash, resolve placeholder/cwd theo
  RepositoryID trong EffectiveScope, spawn trực tiếp bằng `ProcessSupervisor`, lấy timeout chặt hơn,
  capture output, dựng RevisionSet/diff evidence và dùng lại fencing/cancellation V5-08C.
- **Những gì implementation đã khóa:** argv không qua shell; secret chỉ được đưa vào explicit process
  environment; unknown placeholder/cwd/secret fail trước spawn; nonzero/timeout/cancel/scope violation
  có typed failure path; execution-started/finished dùng chung event sink và finalization evidence.
- **Khoảng trống phải đóng trước khi coi task hoàn tất:**
  1. `ProcessResult.OutputTruncated` chưa được kiểm tra, nên output thiếu vẫn có thể đi tới success.
  2. Secret vừa resolve chưa được bổ sung vào redaction matcher; captured stdout/stderr hiện được lưu
     nguyên và artifact có thể mang `Redacted=false`.
  3. Output được insert thẳng `ATTACHED` với retention `CANONICAL_CONTEXT` trong transaction riêng trước
     fenced finalize. Finalizer chỉ chép `OutputArtifactRefs` vào checkpoint, chưa load/verify/promote
     các ID này. Cần lifecycle idempotent `RAW_OUTPUT_TEMP`/ORPHAN → attach hoặc protocol tương đương
     để finalize failure/replay không chấp nhận foreign/missing ref hay tạo row không owner/duplicate.
  4. `CommandDocument.PolicyRefs` chưa được đọc ở execution time; compatibility OS/toolchain và
     `NetworkAccess` chưa được verify/enforce. PolicyRefs của workflow node không thay thế contract của
     chính CommandVersion.
  5. Chưa có production composition/router chọn executor theo `ExecutorKind`, test
     ExecuteNodeHandler→CommandNodeExecutor→fenced finalize hoặc real cross-platform launch smoke.
- **Kết luận dữ kiện:** **CORE ĐÃ MERGE; ACCEPTANCE PARTIAL**. Các quyết định placeholder, secret,
  timeout và process-result cơ bản đã đủ cụ thể để V5-10 reuse. Năm gap trên là work còn lại đã xác định
  bằng code, không còn là câu hỏi thiết kế mở; chưa được dùng PR #8 để claim toàn bộ acceptance V5-09.
- **Verify:** giữ các test injection, env/secret, nonzero/timeout, pre-spawn fail và mutating cancel hiện
  có; bổ sung truncation, echoed-secret redaction, artifact finalize/replay, policy/compatibility và
  end-to-end executor routing trên Windows/Linux.
- **Kết quả kỳ vọng:** worker reload và đối chiếu exact DefinitionID/VersionID/CompiledHash, verify
  `ExecutableRef.ContentHash`, rồi spawn đúng một executable với `[]argv`; admission/precondition fail
  cho spawn count 0. Cwd chỉ từ WorkspaceHandle thuộc EffectiveScope; env/network/toolchain là giao của
  mọi policy pin; secret không xuất hiện trong DB/event/artifact/log. Output bounded, redacted, tamper
  checked và attach atomically dưới fence cùng exact final RevisionSet. Truncation, nonzero, timeout,
  mất lease, diff vượt scope và cancel mơ hồ không thể commit success; mutating cancel đi đúng
  quiesce/reconcile/quarantine của V5-08C.
- **Hoàn thành khi:** resource script không chạy nếu thiếu exact CommandVersion/policy grant, và mọi
  success đi qua production handler với output/evidence đầy đủ, không rò secret hoặc artifact mồ côi.
- **Nguồn:** AK-ARCH-017, AK-ARCH-014, HE-07-M04.

## V5-10 — Gate runner và criteria-level Evidence

- **Mục tiêu:** MACHINE_GATE tạo verdict authoritative với provenance.
- **Phụ thuộc:** V5-01, V5-09.
- **Trạng thái trên master (`39fb39c`, 2026-09-10):** PR #9 đã merge. `GateNodeExecutor` reload/verify
  GateVersion và CommandVersion/resource đã pin, ép mount metadata sang READ_ONLY, tạo scratch cwd,
  kiểm tra live revision trước spawn, parse JSON theo EvidenceKey và map verdict deterministically.
  Nonzero/timeout/spawn/malformed tạo ERROR; thiếu key tạo NOT_RUN; N/A thiếu reason tạo ERROR; GateResult
  artifact được persist cho mọi verdict. ReleaseSet được hoãn đúng sang V5-10A; input hiện tại là exact
  V5 RevisionSet từ ContextSnapshot.
- **Ranh giới đã triển khai:** gate runner và verdict protocol lõi đã có test cho PASS/FAIL/ERROR/
  NOT_RUN/NOT_APPLICABLE, stale revision, scratch/read-only metadata và cancellation.
- **Khoảng trống phải đóng:**
  1. Chưa có domain `Evidence`, EvidenceRepository/Tx accessor hay SQLite writer/query; bảng `evidence`
     vẫn chưa được dùng. GateResult artifact là execution output, không thay thế criteria-level Evidence.
  2. `GateResult` chưa có schema version hoặc full lineage WorkItem/Run/NodeRun/Attempt, Gate/Command/
     policy pins, exact RevisionSet và artifact hashes. Ở non-PASS, artifact ID bị bỏ; row được insert
     thẳng ATTACHED nhưng không có ownership link tới Attempt/Evidence.
  3. Artifact persistence dùng ID mới trong transaction riêng, chưa idempotent và chưa atomically coupled
     với fenced Attempt finalize. `OutputArtifactRefs` cũng chưa được finalizer load/verify/promote;
     replay/crash có thể tạo duplicate, unlinked artifact hoặc chấp nhận missing/foreign ref.
  4. NOT_APPLICABLE hiện chỉ cần reason, chưa cần pinned policy authority. Exact DefinitionID của
     CommandRef cũng chưa được đối chiếu sau khi load version.
  5. `forceReadOnlyMounts` chỉ downgrade metadata. Evaluator vẫn nhận host path thật qua argv, diff guard
     dùng EffectiveScope gốc có thể cho WRITE, và chưa có mutation test chứng minh gate không sửa source
     trong trusted-local mode. OS/network gaps của V5-09 cũng truyền sang gate.
  6. `OutputTruncated` chưa fail closed; Gate có `hasWriteMount=false` nên cũng có thể bỏ qua
     `TreeQuiesced=false`. Detail/reason từ stdout chưa được redaction với secret vừa resolve.
  7. Chưa có production composition/router và ExecuteNodeHandler→GateNodeExecutor integration test.
- **Kết luận dữ kiện:** **GATE CORE ĐÃ MERGE; CRITERIA EVIDENCE PARTIAL**. Đủ dữ kiện để V5-10A định
  nghĩa ReleaseSet additive và giữ nguyên verdict semantics; chưa đủ authority để seal ReleaseSet hoặc
  làm completion dựa trên GateResult như thể criteria-level Evidence đã hoàn thành.
- **Verify:** giữ ma trận verdict hiện có; bổ sung Evidence-row lineage/idempotency, PASS và non-PASS
  crash/replay, truncation/quiescence, echoed-secret redaction, N/A policy, tamper, source mutation,
  CommandRef mismatch và end-to-end routing.
- **Kết quả kỳ vọng:** gate reload/verify exact GateVersion+CommandVersion, dùng source tại exact
  RevisionSet thực sự read-only và scratch riêng. Mỗi EvidenceKey tạo đúng một Evidence idempotent chứa
  full lineage, policy/version pins, RevisionSet và artifact ID+hash cho cả PASS lẫn non-PASS. Missing,
  stale, tampered, ERROR hoặc NOT_RUN không thể PASS; N/A chỉ hợp lệ khi pinned policy cho phép kèm
  reason. Put+Verify xảy ra trước transaction; Evidence+attach+terminal finalize commit dưới cùng fence,
  replay không nhân đôi và source workspace không đổi. Sau V5-10A, ReleaseSet ID/hash được thêm vào
  provenance mà không đổi nghĩa verdict.
- **Hoàn thành khi:** error/missing evidence không thể PASS và mọi criterion result đều truy ngược được
  qua Evidence authority sau restart.
- **Nguồn:** GC-INV-13, GC-INV-25, HE-09-M03, AK-ARCH-015, HE-08-M04, HE-09-M05, HE-11-M06.

## V5-10A — ReleaseSet và typed local Git operation

- **Mục tiêu:** có authority local đa repository đủ cho completion/cleanup mà không giả transaction
  xuyên repository hoặc mở remote Git authority.
- **Phụ thuộc:** V3-11, V5-10.
- **Phạm vi:** ReleaseSet, local commit và application policy; không push/PR/merge/force-push.
- **Nền đã có trên master:** RevisionSet, WorkspaceSet/generation, local Git workspace provider,
  command envelope/receipt/event và `RequestWorkspaceSetRelease` đã có. V5-10 gate core đã merge, nhưng
  criteria-level Evidence authority còn thiếu nên chưa thể làm seal input. `ReleaseEligibilityAuthority`
  hiện chỉ là port dùng fake; chưa có ReleaseSet aggregate/table/repository hay `CreateLocalCommit` port.
- **Thực hiện:** schema/commands create-seal-abandon, exact base/result/verdict từng repository,
  content hash; `CreateLocalCommit` typed/audited; từ chối mọi remote operation trước Git adapter.
- **Dữ kiện phải khóa trước khi code:** uniqueness của ReleaseSet theo FamilyID+ManifestRevision; nguồn
  tập repository bắt buộc; state/partial semantics; canonical hash có bao gồm evidence refs; CAS/replay
  của seal/abandon; local-commit request/result, cleanliness/fence và audit. Task **CHƯA ĐỦ DỮ KIỆN**
  cho tới khi các điểm này có schema và transition table.
- **Verify:** partial result, stale revision, duplicate seal, cleanup eligibility, local commit và spy
  adapter chứng minh remote mutation call count bằng 0.
- **Kết quả kỳ vọng:** `CreateReleaseSet` idempotently tạo DRAFT từ exact WorkspaceSet/base RevisionSet
  và entries sort theo RepositoryID. `CreateLocalCommit` chỉ chạy trên WorkspaceHandle local, dưới
  expected version/revision/scope/fence, ghi commit OID và result revision có audit; không có remote verb
  nào tới Git adapter. Seal CAS chỉ thành công khi mọi repository bắt buộc có exact result revision và
  fresh accepted Evidence; partial/missing/stale giữ DRAFT. SEALED/ABANDONED immutable, abandon cần
  actor+reason+time, content hash verify được, và repository-backed authority nối thẳng vào cleanup.
- **Hoàn thành khi:** ReleaseSet sealed/abandoned là input có provenance cho completion và cleanup.
- **Nguồn:** AK-ARCH-015C, GC-DS-04.

## V5-10B — Completion rework route schema

- **Mục tiêu:** cho WorkflowVersion khai được một "rework route" nguồn từ END mà CompletionPolicy
  (V5-11) dùng khi trả outcome REWORK — tách khỏi V5-11 để giữ transaction quyết định
  (fencing-critical, đọc/ghi CompletionDecision) không lẫn với thay đổi authoring-schema/compiler/
  validator.
- **Phụ thuộc:** không có (thuần schema/validation trong `internal/domain/workflow`, publish-time
  only; không chạm runtime engine).
- **Phạm vi:** `Edge.Kind` (`FLOW`|`COMPLETION_REWORK`, mặc định FLOW khi rỗng) và `Edge.ReworkPolicy`
  (iteration budget); validation đúng một route/END, target tồn tại và không phải END, outcome rỗng,
  budget dương; compiler/hash tự động bao gồm route qua marshal document chuẩn, không cần code hash
  riêng. KHÔNG bao gồm: runtime evaluator đọc route, CAS activation theo REWORK, ghi
  CompletionDecision — các phần đó thuộc V5-11.
- **Nền đã có trên master:** ADR-009 đã định nghĩa "business rework là edge tường minh trong graph"
  từ baseline (§10), nhưng `validateNormalizedDocument` cấm MỌI outgoing edge từ END không phân biệt
  loại — gap thật khiến GC-INV-29 ("REWORK đòi rework edge có trong WorkflowVersion đã pin") không có
  gì để pin. Phát hiện trong "Rà soát V5-09…V5-15 trên committed master" (2026-09-10) khi scoping
  V5-11.
- **Thực hiện:** `EdgeKind`/`ReworkPolicy` field mới trên `Edge` (additive, `omitempty` — không đổi
  content hash của version đã publish trước task này); `validateNormalizedDocument` tách nhánh kiểm
  tra theo Kind; COMPLETION_REWORK edge bị loại khỏi các map `outgoing`/`incoming` dùng cho
  reachability/bounded-cycle/fork-join — nó là routing table riêng của CompletionPolicy, không phải
  đồ thị scheduler thật sự duyệt qua; `CycleMembership` (dùng lại bởi V4-07's escalation-edge check,
  `advance.go`) loại trừ tương tự để không lẫn component.
- **Verify:** publish reject FLOW-edge-từ-END, COMPLETION_REWORK-không-từ-END, target=END,
  outcome khác rỗng, thiếu/bằng-0 budget, hai route cùng một END; publish accept đúng một route hợp
  lệ và Kind="FLOW" tường minh tương đương Kind rỗng; CycleMembership không gộp END/target vào cùng
  component qua route; `WorkflowVersion.Document()`/clone deep-copy `ReworkPolicy` đúng (không leak
  con trỏ giữa hai lần gọi).
- **Hoàn thành khi:** một WorkflowVersion đã publish có thể khai đúng một rework route cho mỗi END
  node, sẵn sàng cho V5-11 load mà không cần đổi gì thêm ở schema/compiler/validator.
- **Nguồn:** ADR-009, ADR-021, GC-INV-10, GC-INV-29.

## V5-11 — CompletionPolicy service

- **Mục tiêu:** externalize WorkflowRun/WorkItem completion bằng một authority duy nhất.
- **Phụ thuộc:** V5-10, V5-10A, V5-10B, V4-12.
- **Nền đã có trên master:** END đã chỉ đưa Run tới `VERIFYING` và phát
  `RUN_COMPLETION_REQUESTED`; WorkItem vẫn `ACTIVE`. `DecisionArtifact`, approval repository, typed
  blocker `COMPLETION_POLICY_FAILED`, UoW/CAS và cancel-race fixtures đã có. Chưa có completion service,
  CompletionDecision/event hoặc production Evidence/ReleaseSet reader.
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
- **Dữ kiện phải khóa trước khi code — cập nhật 2026-09-10, 3/5 đã chốt (xem
  `baocaov5checklist.md`'s "V5-11 scoping" cho câu trả lời đầy đủ, đây chỉ là bản tóm tắt cho design
  doc):**
  1. **CHƯA CHỐT** — nơi pin đúng một CompletionPolicyVersion cho Run; hiện WorkItem/Workflow/END
     chưa có ref.
  2. **CHỐT:** mở rộng `CompletionRules` (`internal/domain/policy`) tại chỗ — thêm `AssuranceLevel`
     (enum có thứ tự cố định trong code, không tin thứ tự JSON), `AssuranceRequirement`
     (Level+RequiredEvidenceKinds+RequiredApprovals) và `RequiredAssurance []AssuranceRequirement`
     cạnh `RequiredEvidenceKinds` hiện có (giữ nguyên, chỉ dùng cho policy V1). V1 flat và V2 ladder
     mutually exclusive trên một document; level tích lũy (E2E PASS không bù UNIT thiếu); HUMAN đọc
     Approval record đã pin, không phải boolean; N/A cần policy authority+reason, waiver là authority
     riêng; evidence phải đúng exact Attempt/RevisionSet/ReleaseSet và còn fresh; không suy level từ
     tên EvidenceKind. `ApprovalRequirement` chưa tồn tại — V5-11 tự định nghĩa khi code.
  3. **CHỐT, giải quyết bởi task riêng V5-10B (xem mục ngay trên):**
     `Edge.Kind=COMPLETION_REWORK`+`Edge.ReworkPolicy` đã cho phép khai đúng một rework route
     mỗi END. V5-11 chỉ còn: load route đã publish cho END node của Run, ghi CompletionDecision, CAS
     tạo đúng một activation khi outcome REWORK, chuyển BLOCK nếu route thiếu/invalid.
  4. **CHỐT:** "join" = cơ chế FORK/JOIN branch-token đã có (`evaluateJoinTx`, V4-10/11) — đã tự thoả
     mãn trước khi Run tới END/VERIFYING (Run chỉ tới VERIFYING khi không còn activation live/blocked
     và END hợp lệ đã đạt). V5-11 KHÔNG reconcile evidence giữa nhiều Run của cùng WorkItem — chỉ
     đánh giá exact Run đang VERIFYING cùng END NodeRun của nó; phát hiện Run khác cùng WorkItem còn
     non-terminal là invariant violation → BLOCK với typed reason
     (`WORK_ITEM_RUN_STATE_INCONSISTENT` hoặc tương đương). Multi-Run-đồng-thời thật sự (RunSet,
     winner/supersession) là task/ADR riêng, ngoài phạm vi V5-11. `ParentJoinPolicy` giữa parent/child
     WorkItem là gap khác, chưa có evaluator, không gộp vào đây.
  5. **CHƯA CHỐT** — idempotency và transaction boundary của DecisionArtifact+transition+event+
     blocker/activation.

  V5-11 vẫn **CHƯA ĐỦ DỮ KIỆN** cho tới khi (1) và (5) có schema/transition table — nhưng không còn bị
  chặn bởi rework-edge hay assurance-ladder/join ambiguity như trước.
- **Verify:** maker claim vs gate matrix, stale evidence, required-level skip, approval missing; ma trận
  bốn outcome × transition; test `REWORK` khi graph không có rework edge trả `BLOCK`; test `FAIL` tạo
  đúng một blocker `COMPLETION_POLICY_FAILED` và không tự reactivate WorkItem; test restart giữa decision
  và transition.
- **Kết quả kỳ vọng:** completion command chỉ nhận identity/expected-version guard; service tự load
  exact policy pin, Evidence/Approval/JOIN/workspace/RevisionSet/SEALED ReleaseSet, caller không truyền
  PASS. Mỗi evaluation ghi đúng một `COMPLETION_DECISION_V1` gồm toàn bộ input IDs+hashes, policy,
  selected route và reason. DecisionArtifact, state/event và side effect commit hoặc rollback cùng một
  transaction, replay không nhân đôi. `PASS` atomically đặt Run `SUCCEEDED`+WorkItem `DONE` cùng terminal
  timestamps; `REWORK` tạo đúng một activation theo route+budget đã pin; `BLOCK` khóa cả hai; `FAIL` đặt
  Run `FAILED`, WorkItem `BLOCKED` và đúng một blocker. Không outcome nào rời `VERIFYING` khi thiếu
  DecisionArtifact hoặc khi cancel fence đã thắng.
- **Hoàn thành khi:** chỉ service có command path chuyển cả Run SUCCEEDED và WorkItem DONE, và không
  outcome nào rời `VERIFYING` mà thiếu DecisionArtifact.
- **Nguồn:** ADR-011, ADR-021, AK-ARCH-005A, GC-INV-21, GC-INV-29, GC-DS-01, GC-DS-07, HE-02-M06, HE-09-M02, HE-09-M06, HE-13-M02, HE-14-M10.

## V5-12 — Maker/checker isolation

- **Mục tiêu:** checker dùng fresh context tối thiểu, không maker transcript/reasoning.
- **Phụ thuộc:** V5-03, V5-10.
- **Nền đã có trên master:** V5 ContextSnapshot immutable đã bind Attempt và exact RevisionSet;
  scheduler tạo Attempt/Snapshot riêng, request assembler reverify pin; workspace mount có
  `READ_ONLY|READ_WRITE` và recovery dùng `Start`, không `Resume`. Tuy nhiên scheduler hiện đưa toàn bộ
  WorkItem messages vào mọi snapshot và giữ WRITE mount theo EffectiveScope.
- **Thực hiện:** checker profile/context route chỉ requirement/diff/evidence; separate Attempt/session;
  read-only exact revision mounts, scratch ngoài source; criteria-level result.
- **Dữ kiện phải khóa trước khi code:** typed role `MAKER|CHECKER` được pin ở AgentProfile/Workflow;
  checker input allowlist và typed Evidence/Diff refs (ContextSnapshot hiện chỉ có MessageRef/
  ResourceRef); scratch handle; enforcement semantics. Với `OPERATOR_TRUSTED_LOCAL`, platform chỉ có thể
  hậu kiểm mutation rồi fail/quarantine; muốn ngăn write trước process phải yêu cầu
  `ENFORCED_ISOLATED`. Task **CHƯA ĐỦ DỮ KIỆN** cho tới khi ba contract này được chốt.
- **Verify:** snapshot manifest asserts forbidden maker resources absent; write source/local commit bị
  policy/fence từ chối.
- **Kết quả kỳ vọng:** CHECKER không được suy từ tên node; nó có NodeRun/Attempt/ContextSnapshot/session
  độc lập dù dùng cùng provider/model. Snapshot là allowlist của requirement/acceptance, exact RevisionSet/
  ReleaseSet và typed diff/evidence refs, không chứa maker Message, AgentEvent, raw transcript hay
  reasoning; provenance inclusion/exclusion đủ audit. Tất cả source mount là exact-revision READ_ONLY,
  scratch nằm ngoài source, không acquire WriteLease và `CreateLocalCommit` bị từ chối trước Git adapter.
  Trusted-local mutation quan sát được phải fail+quarantine và không thể sinh PASS.
- **Hoàn thành khi:** same provider/model vẫn có independent attempt/context identity.
- **Nguồn:** HE-09-M04, HE-05-M06, HE-14-M08.

## V5-13 — Checkpoint/handoff và recovery integration

- **Mục tiêu:** crash real execution để lại structured status/blocker/evidence/next action.
- **Phụ thuộc:** V5-08…V5-12 và V5-10A.
- **Nền đã có trên master:** recovery reaper đã detect orphan, classify RETRY/FRESH_START/ESCALATE,
  reconcile/quarantine và ghi `RecoveryDecision`; RETRY đã tạo Attempt/job mới. Agent event/checkpoint,
  fencing và primitive `worker.StartFreshFromLatestCheckpoint` đã có. Nhưng primitive này còn đọc legacy
  `runtime.ContextSnapshot`, trong khi dispatch thật dùng V5 `contextsnapshot.Snapshot` bind duy nhất với
  Attempt và hai model không có bridge.
- **Thực hiện:** canonical checkpoint batches, recovery classifications, quarantine reconciliation,
  fresh context rebuild, no-progress/budget escalation. Tiêu thụ V4-13's own FRESH_START recovery
  decision (durable record: interrupted AttemptID, checkpoint/ContextSnapshot đã chọn, recovery
  generation, reason/failure category) và thực hiện flow ba pha thật: Tx claim decision đúng một lần,
  reserve Attempt mới, tạo **V5 Snapshot mới bind replacement Attempt** và enqueue `EXECUTE_NODE`
  RUN_WORK có typed recovery refs → ngoài transaction, load/reverify checkpoint + V5 Snapshot, assemble
  request bình thường, set `RecoveryCheckpoint` cho AGENT rồi gọi `Start` (không `Resume`) hoặc executor
  COMMAND/GATE tương ứng → Tx finalize có fencing bằng JobLease/WriteLease. V5-13 phải thay/bridge
  `worker.StartFreshFromLatestCheckpoint`; không được dùng nguyên primitive legacy để reuse snapshot ID
  của Attempt cũ. Không bao giờ đặt lời gọi provider thật vào một CONTROL job hoặc bên trong một
  transaction `ports.UnitOfWork`.
- **Dữ kiện phải khóa trước khi code:** persistent `RecoveryExecution`/CAS key nối decision, interrupted
  Attempt, generation, replacement Attempt/job/snapshot; recovery payload/idempotency; cách rebuild V5
  snapshot và handoff artifact; completed-vs-unverified work, blocker/decision/evidence refs; no-progress/
  time/cost/attempt budget; behavior riêng cho AGENT/COMMAND/MACHINE_GATE và precondition workspace
  generation đã reconcile. Do mâu thuẫn snapshot và thiếu consumer exactly-once, task hiện **CHƯA ĐỦ
  DỮ KIỆN**.
- **Verify:** six fault boundaries với agent/command/gate fixtures.
- **Kết quả kỳ vọng:** mỗi FRESH_START decision được consume đúng một lần; concurrency/replay chỉ tạo một
  replacement Attempt, Snapshot và RUN_WORK job. Recovery gọi `Start=1`, `Resume=0`, không cần transcript/
  cwd/session cũ; stale/tampered/missing ref hoặc fence không commit. Handoff V1 persist run/node/attempt,
  completed và unverified work, blockers/decisions, Evidence+Artifact IDs/hashes, revisions/generation,
  failure category, next action và budget basis. Sáu boundary chuẩn đều chứng minh không mất transition,
  không lặp side effect/success, không blind-retry mutation; hết budget/no-progress tạo blocker typed.
- **Hoàn thành khi:** session mới không cần raw transcript hoặc cwd cũ.
- **Nguồn:** HE-05-M03, HE-13-M05, AK-ARCH-010, HE-12-M02, HE-12-M06.

## V5-14 — Cleanup/retention sweeper

- **Mục tiêu:** dọn owned temp/orphan/expired artifact an toàn.
- **Phụ thuộc:** V5-01, V5-13.
- **Nền đã có trên master:** Artifact metadata có retention class, hold, ORPHAN/ATTACHED, expiry và CAS;
  repository list được orphan. Workspace provider có idempotent release; public
  `RequestWorkspaceSetRelease` đã ghi intent+job với per-repository IDs. `ArtifactStore` chưa có delete,
  metadata chưa có payload-purged state, reference/liveness query hay sweeper; release job chưa có handler.
- **Thực hiện:** durable sweep job, typed retention class/holds, atomic reference recheck,
  sealed/verified checks, dry-run report, idempotent cleanup; không release active/quarantined workspace
  hoặc hard-delete canonical conversation. Task này cũng sở hữu internal `ExecuteWorkspaceSetRelease` —
  phần thực thi filesystem/Git của `RequestWorkspaceSetRelease` mà V3-11 chỉ ghi intent.
- **Dữ kiện phải khóa trước khi code:** purge port và owned-locator containment; giữ audit row/hash sau
  payload deletion bằng state/timestamp; liveness/refcount khi nhiều row dùng cùng content-addressed
  locator; reserve→delete ngoài Tx→finalize crash protocol; query attached-expired artifacts;
  per-repository release result/retry schema. Task **CHƯA ĐỦ DỮ KIỆN** nếu chưa có các contract này.
- **Verify:** run twice, active hold/reference, canonical message/context, unknown file và 7-day raw
  artifact boundary tests.
- **Kết quả kỳ vọng:** dry-run và actual run cùng tạo durable sweep manifest. Chỉ ORPHAN quá grace hoặc
  `RAW_OUTPUT_TEMP` expired được reserve khi không hold, không canonical/recovery/evidence/ReleaseSet ref
  và không còn row sống chung locator/hash; canonical, held, unknown, active và quarantine đều được giữ.
  Purge ba pha chịu crash, không gọi filesystem trong Tx và giữ metadata/hash `PURGED` cho audit.
  `ExecuteWorkspaceSetRelease` recheck SEALED/ABANDONED authority cùng no-active-attempt/job/lease và
  no-quarantine, release từng repo idempotently, persist partial result và chỉ đặt WorkspaceSet RELEASED
  sau khi mọi entry thành công; restart tiếp tục phần còn thiếu.
- **Hoàn thành khi:** cleanup không xóa material cần recovery/evidence/audit.
- **Nguồn:** AK-ARCH-025B, HE-12-M05, HE-12-M04.

## V5-15 — Execution/evidence acceptance gate

- **Mục tiêu:** behavior thật chỉ complete bằng independent evidence.
- **Phụ thuộc:** V5-01…V5-14, V5-08A…V5-08D và V5-10A.
- **Nền đã có trên master:** integration engine SQLite/workerpool, real filesystem/Git/process adapters,
  recorded provider contracts, crash fixtures và CI Windows/Linux/race đã có từng lớp. Scenario runtime
  hiện vẫn dùng scripted fake NodeExecutor và cố ý dừng ở `VERIFYING`. Committed master `39fb39c` đã có
  core `CommandNodeExecutor` và `GateNodeExecutor` từ PR #8/#9, nhưng chưa có production composition,
  chưa đóng các gap acceptance V5-09/V5-10 và chưa có V5-10A…V5-14.
- **Thực hiện:** workflow agent→command→gate→checker→ReleaseSet→end/completion; inject claim done,
  gate fail, crash, provider loss, scope violation, checker write, adapter drift, isolation enforcement
  unavailable, cancel giữa mutating attempt và artifact tamper.
- **Dữ kiện triển khai:** đủ để dựng scenario manifest/oracle ngay; V5-08D đã đáp ứng, nhưng chưa thể chạy
  acceptance thật trước khi các gap V5-09/V5-10 cùng V5-10A…V5-14 được đóng. Fixture phải khai rõ lớp
  nào real, recorded fake hoặc spy; không được dùng một scripted NodeExecutor để tự chứng minh toàn chain.
- **Verify:** full/race/Windows/Linux offline; optional manual live smoke ghi `UNVERIFIED_LIVE` nếu chưa chạy.
- **Kết quả kỳ vọng:** một fixture offline đã đăng ký chạy real app handlers+SQLite+filesystem/Git/
  ProcessSupervisor và recorded provider, tạo trace
  WorkItem→Run→NodeRun→Attempt→V5 Snapshot→RevisionSet→Evidence/Artifact IDs+hashes→ReleaseSet→
  CompletionDecision verify lại được sau restart. Happy path chứng minh maker/exit 0 chỉ tạo candidate;
  chỉ fresh independent PASS trên exact revision + SEALED ReleaseSet mới atomically tạo
  Run `SUCCEEDED`/WorkItem `DONE`. Mỗi injection và sáu crash boundary cho typed non-success/block/
  escalation/quarantine, false-completion oracle đếm `DONE` sai bằng 0, side effect không lặp. Offline
  suite pass Windows/Linux, Linux race và repeated semantic comparison; live Claude/Codex chỉ bổ trợ và
  mang `UNVERIFIED_LIVE` nếu chưa thực hiện.
- **Hoàn thành khi:** trace WorkItem→revision/evidence đầy đủ và false completion bằng 0 trong fixtures.
- **Nguồn:** AK-ARCH-005, GC-ACC-14, HE-10-M02, HE-10-M03.
