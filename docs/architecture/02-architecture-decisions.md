# Architecture decisions cho Agent Kit

> Trạng thái: ACCEPTED — product owner xác nhận ADR-001…010 ngày 2026-08-28, ủy quyền chốt
> ADR-011…019 ngày 2026-08-29 và chốt ADR-020…025 ngày 2026-08-31 sau review bộ thiết kế Alpha.
> ADR-026 chốt ngày 2026-09-05, phát hiện và quyết định trực tiếp trong lúc user review code V4-03.
>
> Ngày lập baseline hiện hành: 2026-08-31 (ADR-001…025); 2026-09-05 (ADR-026).

## 1. Các ràng buộc đã xác nhận

Những quyết định này đã được thống nhất và không còn là câu hỏi mở:

1. Không xây BPMN và không đặt mục tiêu tương thích BPMN.
2. Alpha là sản phẩm local cho cá nhân; beta là deployment tập trung riêng, không đồng bộ alpha.
3. Beta chạy Claude CLI, Codex CLI và provider tương lai trên server worker.
4. Skill/Layer chỉ chứa instruction/resource; executable capability được đăng ký riêng.
5. Nhiều root task có thể chạy song song và có workspace riêng.
6. Subtask kế thừa TaskFamily/WorkspaceSet của root task.
7. Project chứa nhiều Repository; TaskFamily có thể scope nhiều RepositoryWorkspace.
8. UI quản lý ở cấp Project; baseline ban đầu cho source/diff/log/terminal focus từng repository
   (phần terminal đã bị ADR-018 supersede).

## 2. ADR-001 — Authoring và publish workflow definition

**Quyết định:** authoring bằng file declarative có thể review bằng Git; publish qua CLI/API để validate,
canonicalize, hash và lưu một WorkflowVersion bất biến trong database.

- File authoring là nguồn để con người review/chỉnh sửa.
- Published snapshot trong database là runtime authority.
- WorkflowRun luôn pin version_id và content hash.
- Sửa file không thay đổi run đang chạy cho đến khi publish version mới.
- UI workflow designer sau này gọi cùng publish contract, không tạo semantics khác.

Lý do: vừa giữ review/diff tốt ở alpha, vừa cho runtime bền vững và quản lý tập trung ở beta.

## 3. ADR-002 — Mở rộng repository scope trong khi run đang chạy

**Quyết định:** cho phép add-only bằng operation tường minh; không tự thêm và không remove repository
khỏi TaskFamily đang chạy.

Flow:

1. Agent/node phát ScopeExpansionRequested kèm lý do và READ/WRITE access cần thiết.
2. Orchestrator block node.
3. Operator phê duyệt.
4. Hệ thống provision RepositoryWorkspace, tăng scope version và tạo RevisionSet mới.
5. Node retry bằng attempt mới.

Mở rộng scope là mở rộng quyền và context nên không được ẩn trong prompt hay adapter.

## 4. ADR-003 — Mutating attempt xuyên repository

**Quyết định:** mặc định mỗi mutating attempt chỉ có một RepositoryWorkspace read-write; các repository
khác trong effective scope được expose read-only.

Một integration node có thể ghi nhiều repository khi:

- definition khai báo multi-repository write capability;
- policy cho phép;
- worker acquire batch WriteLease cho toàn bộ repository đích;
- output tạo RevisionSet và evidence riêng cho từng repository.

Không dùng prompt để thay thế filesystem permission và diff enforcement.

## 5. ADR-004 — Release xuyên repository và Git authority

**Quyết định:** dùng correlated ReleaseSet, không giả định atomic commit/merge xuyên Git repository.

Mỗi repository có branch/commit/pull request và gate riêng. Parent chỉ DONE khi mọi repository bắt
buộc đạt release policy. Partial failure đưa family về BLOCKED hoặc compensation flow; không ghi
nhãn rollback thành công nếu chưa có evidence.

Quyền alpha:

- worker được tạo local branch/commit khi workflow policy cho phép;
- push và tạo pull request chỉ từ operator command rõ ràng (phần này đã bị ADR-014 supersede cho Alpha);
- merge luôn cần human approval;
- không auto-force-push hoặc rewrite remote history.

## 6. ADR-005 — Conversation và context ownership

**Quyết định:** platform giữ canonical conversation message và context snapshot; provider transcript
chỉ là artifact hỗ trợ debug.

- Message là append-only, có actor, timestamp và correlation với WorkItem/Attempt.
- ContextSnapshot lưu manifest chính xác của message/resource/revision được chọn.
- Alpha luôn khởi động agent mới từ ContextSnapshot; không resume provider session dù ref còn sống.
- ProviderSessionRef chỉ là diagnostic/correlation artifact và có thể mất.
- Evidence alpha lưu local disk trong 7 ngày rồi cleanup theo policy (retention wording đã bị ADR-017
  supersede); không mã hóa at-rest ở alpha.
- Beta áp retention policy theo organization; raw provider event có TTL riêng.

Mất provider session không được làm mất khả năng dựng lại context từ platform state.

## 7. ADR-006 — Operating systems

**Quyết định:** core contract hỗ trợ Windows và Linux từ đầu.

- Spike chạy Windows trước là môi trường/gate thực thi đầu tiên.
- Process, path, signal và worktree nằm sau adapter; domain không chứa OS path semantics.
- Sau khi Windows pass, cùng semantic suite vẫn bắt buộc pass trên Linux trước khi full gate alpha đạt.
- Beta worker target mặc định là Linux container.

Không yêu cầu mọi shell/toolchain layer chạy đa nền tảng; layer phải khai báo compatibility.

## 8. ADR-007 — Provider và executable extension

**Quyết định:** dùng versioned process/protocol adapter, không dùng Go dynamic plugin làm extension
boundary chính.

- AgentExecutor chuẩn hóa start/resume/cancel/event/outcome; Alpha không gọi resume theo ADR-005.
- Claude/Codex adapter chỉ dịch provider protocol.
- Command/Gate/Scaffold executor chạy ngoài UI/API process.
- Adapter khai capability và version; domain không có nhánh if provider == Claude/Codex.
- Skill/Layer resource không có quyền thực thi mặc định.

## 9. ADR-008 — Persistence, event và projection

**Quyết định:** state tables là runtime authority, kèm append-only audit/domain events và durable job/
outbox ghi cùng transaction; không áp full event sourcing cho alpha.

- SQLite adapter ở alpha, PostgreSQL adapter ở beta.
- Repository contract suite chạy cho cả hai implementation.
- Kanban/task detail là projection/read model có thể rebuild.
- Expected version bảo vệ optimistic concurrency.
- HTTP API nằm ở application boundary, không mô phỏng database Store qua HTTP.

Cách này giữ recovery/audit mà không buộc toàn bộ domain phải replay event để dựng state.

## 10. ADR-009 — Workflow graph, retry và rework

**Quyết định:** WorkflowVersion là typed directed graph.

- Technical retry nằm trong AttemptPolicy của node và luôn tạo attempt mới.
- Business rework là edge tường minh trong graph.
- Cycle chỉ hợp lệ khi có iteration budget và escalation edge.
- Fork/join phải khai join policy rõ; không suy hoàn thành từ việc queue trống.
- Definition publish phải reject node/edge/type không hợp lệ và unreachable terminal path.

## 11. ADR-010 — UI technology

**Quyết định:** cố ý chưa chọn Angular/React hay UI stack trước khi Go spike đạt.

Các contract được khóa trước:

- UI chỉ gọi application API;
- Kanban là projection của WorkItem/Run/NodeRun;
- task detail hiển thị graph, WorkspaceSet, evidence, blocker và chat;
- UI không spawn CLI/Git.

Sau spike sẽ làm một UI decision riêng dựa trên tốc độ MVP, component library và năng lực team.
Prototype cũ chỉ là wireframe/reference, không khóa API hay framework.

## 12. Baseline đã xác nhận

Các ADR-001 đến ADR-010 là baseline bắt buộc cho Go core spec và spike. Thay đổi sau này phải tạo
ADR superseding, không sửa lịch sử quyết định âm thầm.

Baseline đã chốt:

1. File declarative để author; database giữ published immutable version.
2. Scope expansion add-only, cần operator approval.
3. Một repository read-write mỗi attempt; multi-repo write chỉ ở integration node.
4. ReleaseSet không atomic; remote push/PR/merge wording lịch sử đã bị ADR-014 supersede cho Alpha.
5. Platform sở hữu conversation/context; provider session chỉ là optimization.
6. Core Windows/Linux; beta worker Linux.
7. Process/protocol adapters; không Go dynamic plugin.
8. State tables + append-only event/outbox, không full event sourcing.
9. Retry kỹ thuật thuộc AttemptPolicy; business rework thuộc graph.
10. Hoãn chọn UI framework đến khi spike đạt.

## 13. ADR-011 — Scope amendment và completion candidate

**Quyết định:** supersede bước “retry cùng NodeRun” của ADR-002 và mọi diễn giải rằng scope của run
không bao giờ có revision mới. `ExecutionManifest` ban đầu vẫn bất biến; scope expansion được ghi
bằng một `RunManifestAmendment` append-only, có approval, family `ScopeVersion`, `RevisionSet` và
content hash riêng.

- Attempt đang yêu cầu mở scope phải kết thúc `BLOCKED`; không đổi scope của Attempt đang chạy.
- Sau approval/provision, orchestrator tạo một `NodeRun` activation mới với
  `ReactivationReason=SCOPE_EXPANDED`, pin amendment và effective scope mới.
- Sibling/descendant khác không tự được widen effective scope chỉ vì family scope đã mở rộng.
- END chỉ tạo completion candidate. `WorkflowRun` chuyển `VERIFYING`, chưa phải `SUCCEEDED`.
- Chỉ CompletionPolicy trên exact RevisionSet mới được chuyển Run thành `SUCCEEDED` và WorkItem thành
  `DONE` trong cùng quyết định có audit.

Lý do: giữ lịch sử input/scope của NodeRun bất biến, đồng thời vẫn thực hiện được add-only expansion
đã duyệt mà không sửa manifest cũ.

## 14. ADR-012 — Identity của compiled snapshot, resource và adapter

**Quyết định:** tách hash của source authoring khỏi identity runtime đã resolve.

- `SourceHash` là hash canonical của nội dung authoring.
- `CompiledSnapshotHash` là hash canonical của payload runtime cộng exact dependency manifest; publish
  deduplicate theo `DefinitionID + CompiledSnapshotHash`, không chỉ theo source.
- Version range chỉ tồn tại ở authoring. Registry thay đổi làm dependency resolve khác phải tạo compiled
  snapshot/version mới dù source không đổi.
- Resource thụ động nằm trong Skill/Layer/Pack version và có identity
  `owner_version_id + resource_key + content_hash`; không cần top-level ResourceDefinition riêng.
- Context route là một loại `PolicyVersion`, pin selector/order/budget và resource identities.
- Provider/executable adapter có `AdapterBuildVersion` bất biến gồm adapter key, semantic version,
  protocol version, build/content hash và capability manifest. Run pin expected version; Attempt ghi
  version worker thực dùng và bị admission từ chối nếu không khớp.

## 15. ADR-013 — Execution isolation và multi-repository write

**Quyết định:** permission admission và enforcement là hai bước riêng; post-run diff không được gọi là
sandbox. Alpha có hai trust tier tường minh:

- `ENFORCED_ISOLATED`: worker phải thực thi filesystem/network/secret profile bằng cơ chế OS/container;
  thiếu enforcement làm dispatch fail closed.
- `OPERATOR_TRUSTED_LOCAL`: chỉ operator cấp cho executable/provider đã tin cậy; UI/evidence phải hiển
  thị rằng filesystem hoặc network không được cô lập hoàn toàn. Tier này không được dùng để tuyên bố
  least-privilege đã được chứng minh.

Mọi mutating Attempt mặc định có đúng một `READ_WRITE` RepositoryWorkspace. Nhiều repository chỉ hợp
lệ khi BlockVersion khai capability `INTEGRATION_MULTI_REPOSITORY_WRITE`, policy grant, compiler xác
nhận scope, worker batch-acquire lease all-or-none và evidence tách theo repository. Checker/gate luôn
dùng exact pinned RevisionSet, source mount read-only và scratch output riêng.

## 16. ADR-014 — ReleaseSet và Git authority của Alpha

**Quyết định:** Alpha triển khai `ReleaseSet` local; remote Git mutation được defer.

- ReleaseSet có per-repository base/head revision, branch/local-commit ref, gate result và partial state.
- Local commit chỉ được tạo khi workflow policy cho phép hoặc operator phát typed command.
- `push`, tạo pull request, merge, force-push và rewrite remote history không có executor/API trong
  Alpha. Phần remote authority của ADR-004 chỉ có hiệu lực khi capability được bổ sung bằng ADR sau.
- Parent multi-repository chỉ DONE khi CompletionPolicy xác nhận mọi repository bắt buộc đạt release
  policy hoặc policy tường minh cho phép kết quả uncommitted/patch-only.
- Release WorkspaceSet là cleanup sau khi ReleaseSet đã sealed hoặc operator ghi quyết định abandon;
  nó không thay thế ReleaseSet.

## 17. ADR-015 — Journal position, projection rebuild và recovery reaper

**Quyết định:** aggregate sequence và delivery cursor là hai identity khác nhau.

- Mỗi committed domain event nhận `JournalPosition` monotonic nhưng không cần gapless từ persistence
  transaction. Alpha dùng một SQLite journal order; beta phải giữ observable per-project order tương đương.
- Projection bootstrap từ authoritative state tại một transaction-consistent watermark, sau đó replay
  event có JournalPosition lớn hơn watermark. Cursor không advance qua gap/poison event.
- SSE `Last-Event-ID` dùng project-scoped journal position, không dùng aggregate sequence.
- Consumer đọc journal theo global position rồi lọc Project; vị trí của project khác được phép bị bỏ
  qua. “Gap” chỉ là missing/corrupt record đã được tham chiếu hoặc vi phạm aggregate sequence/schema,
  không phải `next_position != current + 1`.
- Projection lỗi chuyển `DEGRADED/STALE`, giữ last-good cursor và không ảnh hưởng orchestrator.
- Lease expiry được xử lý bởi periodic/durable recovery reaper dùng database time và CAS/fencing;
  startup scan chỉ là tối ưu, không phải đường recovery duy nhất.

## 18. ADR-016 — Local HTTP trust boundary

**Quyết định:** Alpha HTTP server chỉ bind loopback và vẫn cần chống hostile browser origin.

- External bind bị reject, không chỉ tắt mặc định.
- Server validate `Host` và `Origin`; CORS deny-by-default không thay thế các kiểm tra này.
- Mutation cần per-start local session token cùng idempotency/optimistic-concurrency contract; request
  browser không có token bị từ chối để chống DNS rebinding/CSRF.
- Token không được ghi vào URL, log, artifact hoặc persisted config.
- Server chỉ đưa token vào bootstrap HTML được phục vụ cho Host loopback hợp lệ, với
  `Cache-Control: no-store` và CSP chặt; UI giữ token trong memory và gửi bằng header. Không có endpoint cross-origin
  đọc token hay lưu token vào local/session storage.

## 19. ADR-017 — Retention class cho evidence và canonical context

**Quyết định:** TTL 7 ngày áp cho evidence payload/raw provider output mặc định, không áp mù cho mọi
Artifact.

- Canonical Message, attachment/resource và payload được ContextSnapshot của run còn khả năng recovery
  hoặc còn retention requirement tham chiếu giữ `CANONICAL_CONTEXT`/retention hold.
- Context/evidence metadata và hash được giữ để audit dù payload evidence đã hết hạn.
- Alpha chưa có hard-delete conversation. Cleanup conversation sau này phải là explicit audited
  operation và làm task tương ứng mất khả năng recovery một cách hiển thị được.
- Sweeper chỉ xóa owned artifact đúng retention class, không còn hold và đã qua integrity/state check.

## 20. ADR-018 — UI Alpha: source/log có, interactive terminal chưa có

**Quyết định:** UI Alpha có read-only source, diff và structured/raw-redacted log views theo từng
repository. Interactive browser terminal được defer cho đến khi có TerminalSession capability với
permission, isolation, audit, timeout và secret policy riêng.

UI vẫn không spawn Git/CLI; mọi source/log lấy qua query/artifact API. Việc defer terminal supersede
phần mô tả terminal trong boundary UI Alpha của tài liệu mô hình workspace, không làm thay đổi domain
multi-repository.

## 21. ADR-019 — Repository onboarding và atomic root WorkItem

**Quyết định:** repository onboarding và root task creation là hai transaction protocol rõ ràng.

- `RegisterRepository` tạo Repository trạng thái `REGISTERING`, onboarding record và durable probe job
  trong cùng transaction; worker chuyển `PROBING` rồi `ACTIVE` hoặc `BLOCKED` với evidence. Git probe
  không chạy trong application transaction.
- `DISABLED` chỉ dành cho repository đã từng active nhưng bị operator vô hiệu hóa; không dùng làm
  trạng thái pending.
- WorkItem contract/readiness có thể validate trước, nhưng public `CreateRootWorkItem` phải persist
  WorkItem + TaskFamily + WorkspaceSet intent + initial scope + provision jobs atomically. Không có
  public command tạo root WorkItem orphan.

## 22. ADR-020 — Cancellation contract và tập terminal state của Attempt

**Quyết định:** cancellation là một protocol quiesce có durable intent, không phải một CAS đơn lẻ.
`ExecutionAttempt` có thêm terminal state `BLOCKED` để tách business/admission blocker khỏi technical
failure.

Tập terminal state của `ExecutionAttempt` là `SUCCEEDED`, `FAILED`, `TIMED_OUT`, `CANCELLED`, `LOST`,
`INDETERMINATE` và `BLOCKED`. Mọi terminal state MUST ghi `TerminationReason` typed.

### Ma trận state–reason

`TerminationReason` là enum riêng của runtime, **không** phải `AppError.Code`. Một admission failure có
thể sinh ra cả hai giá trị và chúng không được coi là đồng nhất: error code mô tả vì sao command/dispatch
thất bại, `TerminationReason` mô tả vì sao Attempt kết thúc.

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

`BLOCKED` có hai **nhóm** đường vào, cả hai là business/admission blocker và không tiêu thụ budget
technical retry:

1. **Runtime blocker** — `RUNNING → BLOCKED`: node đang chạy phát hiện cần mở scope (ADR-011).
2. **Admission blocker** — `QUEUED → BLOCKED`: dispatch bị từ chối trước spawn. Nhóm này mở cho mọi
   admission check fail-closed, không riêng isolation: enforcement không khả dụng (ADR-023), adapter
   build khác exact pin (ADR-012/022), capability yêu cầu không được thỏa, và thiếu write capability
   hoặc grant (ADR-013).

- Cả `QUEUED → BLOCKED` và `QUEUED → CANCELLED` MUST NOT set `StartedAt` và MUST giữ process spawn count
  bằng 0; Attempt chưa bao giờ `RUNNING` nên không có transport/execution result để phân loại.
- Attempt đã BLOCKED không bao giờ được hồi sinh. Command `RetryBlockedActivation` tạo NodeRun activation
  và Attempt **mới** sau khi exact pin được revalidate; nó không hồi sinh Attempt cũ và không repin run
  sang dependency khác. Nếu revalidate vẫn fail thì activation mới lại `BLOCKED` với cùng reason.
- Dùng `FAILED` cho các tình huống này bị cấm vì nó trộn business/admission blocker với technical failure
  và làm AttemptPolicy retry sai.

### Cancellation protocol

Run có thể bị cancel từ `CREATED`: một run vừa tạo mà chưa dispatch vẫn phải hủy được, và đường đó đi
qua `CANCELLING` như mọi đường khác.

`WorkflowRun` có thêm state `CANCELLING` giữa intent và terminal:

1. `CancelRun` atomically ghi durable cancel intent, chuyển Run sang `CANCELLING`, append event và
   enqueue job; command là idempotent theo run.
2. Scheduler ngừng tạo activation, technical retry và rework mới ngay khi intent đã commit.
3. WAIT, APPROVAL, durable job chưa claim và NodeRun chưa chạy chuyển `CANCELLED`. Attempt đã tạo cùng
   job nhưng chưa khởi động chuyển `QUEUED → CANCELLED` với `RUN_CANCELLED_BEFORE_START`; không Attempt
   nào được để mắc kẹt ở `QUEUED`.
4. Attempt đang chạy nhận cancellation token; process tree bị terminate theo grace policy rồi force.
5. WriteLease chỉ được release sau khi worker xác nhận process đã dừng.
6. Mutating attempt không chứng minh được kết quả phải thành `INDETERMINATE` và workspace
   `QUARANTINED`; không được ghi nhãn `CANCELLED` cho outcome chưa biết.
7. Run chỉ chuyển `CANCELLED` sau khi execution đã quiesce hoặc reconciliation hoàn tất.
8. Cancel không tự cleanup workspace và không tự abandon ReleaseSet; hai việc đó là quyết định riêng.

### Cancel fence: `cancel_epoch`

Cancel intent phải chặn được job claim, nếu không worker vẫn claim được job sau khi intent commit nhưng
trước khi coordinator xử lý. Fence dùng một epoch, không dùng boolean, để phân biệt "chưa từng cancel"
với "đã cancel":

Fence chỉ áp cho job *làm việc*, không áp cho job *điều khiển việc dừng* — nếu không, cancellation
coordinator job do chính `CancelRun` enqueue sẽ bị fence bởi transaction sinh ra nó và không bao giờ
được claim. `DurableJob` vì vậy mang thêm `RunID?` và `JobClass`:

| JobClass | Ví dụ | Bị cancel fence |
|---|---|---|
| `RUN_WORK` | node attempt, retry, rework, wait timer | Có |
| `CONTROL` | cancellation coordinator, workspace reconciliation, workspace/ReleaseSet release, recovery reaper | Không |

`JobClass` **không phải** một cột tự do worker tự điền: nó là hàm của `Kind`. Mapping `Kind → JobClass`
là một bảng tĩnh trong code, được cưỡng chế bằng check constraint hoặc validation ở repository, và mọi
`Kind` không nằm trong allow-list `CONTROL` MUST là `RUN_WORK`. Kind chưa biết mặc định `RUN_WORK` —
fail-closed, vì gán nhầm `CONTROL` nghĩa là job đó vượt qua cancel fence.

Allow-list `CONTROL` hiện tại là đóng: `CANCEL_RUN_COORDINATOR`, `WORKSPACE_RECONCILE`,
`WORKSPACE_SET_RELEASE`, `RECOVERY_REAPER`. Thêm kind mới vào đây là một quyết định tường minh cần ADR
hoặc ít nhất một thay đổi có test phủ; không được thêm bằng cách sửa một literal.

- `WorkflowRun.cancel_epoch`: `NULL → 1` khi cancel intent commit.
- Cùng transaction đó: mọi job `RUN_WORK` `AVAILABLE` của Run → `CANCELLED`; mọi job `RUN_WORK`
  nonterminal nhận `cancel_epoch=1`; mọi Attempt còn `QUEUED` → `CANCELLED` với
  `RUN_CANCELLED_BEFORE_START`. Job `CONTROL` không bị đụng tới.
- `RunID` là quan hệ trực tiếp để fence theo Run mà không phải suy từ `AggregateRef`.
- Claim CAS: `RUN_WORK` claim `WHERE job_class='RUN_WORK' AND state='AVAILABLE' AND cancel_epoch IS NULL`;
  `CONTROL` claim `WHERE job_class='CONTROL' AND state='AVAILABLE'`. Hai partial index riêng. Fence nằm
  trên chính job row nên claim không phải join sang `workflow_runs`.
- Enqueue job mới MUST CAS rằng Run còn non-cancelling; không được thêm việc vào một Run đang quiesce.
- Worker re-check epoch tại hai chốt: ngay trước `QUEUED → RUNNING`, và ngay trước
  `ProcessSupervisor.Start`. Khoảng giữa hai chốt đủ dài để cancel chen vào.
- Nếu `RUNNING` commit trước cancel: đi qua active cancellation (terminate process tree, quiesce). Nếu
  cancel commit trước: process spawn count MUST bằng 0.

### Cancel race: chỉ Run terminal mới làm cancel thành no-op

Phân xử bằng thứ tự **commit**, và điều kiện no-op là **Run đã terminal**, không phải "một transaction
nào đó đã commit trước". Attempt terminal hay END không làm Run terminal, nên cancel vẫn phải được chấp
nhận:

| Transaction commit trước | State của Run khi đó | Kết quả `CancelRun` |
|---|---|---|
| Attempt finalize `SUCCEEDED` | `RUNNING` | Chấp nhận; Run vào `CANCELLING` |
| END → `VERIFYING` | `VERIFYING` | Chấp nhận; cancel từ `VERIFYING` |
| CompletionDecision `BLOCK` | `BLOCKED` | Chấp nhận |
| CompletionDecision `REWORK` | `RUNNING` | Chấp nhận |
| CompletionDecision `PASS` | `SUCCEEDED` | No-op idempotent, trả terminal result |
| CompletionDecision `FAIL` | `FAILED` | No-op idempotent, trả terminal result |

- Cancel intent commit **trước**: mọi authoritative outcome đến muộn bị CAS từ chối — finalize, routing
  và completion sau đó MUST NOT tạo `SUCCEEDED`, activation mới, technical retry hay rework.
- External success đến muộn sau cancel intent chỉ được lưu như observation/evidence để reconcile; nó
  không phải authority chuyển state.
- Mutating outcome không chắc chắn vẫn là `INDETERMINATE` + workspace `QUARANTINED`, kể cả khi external
  process báo thành công.

### WorkItem sau cancel

`CancelRun` hủy một run, không hủy task. Ba command tách biệt, mỗi command có precondition riêng:

- `CancelRun` → Run `CANCELLED`, WorkItem `BLOCKED` kèm blocker `RUN_CANCELLED`.
- `CancelWorkItem` → WorkItem `CANCELLED`. Một intent đang pending **fence** hai đường vào bằng CAS:
  `ResolveWorkItemBlocker` không được đưa WorkItem về `READY`, và `StartWorkflowRun` không được tạo Run
  mới cho WorkItem đó. Nếu không, giữa lúc intent commit và lúc coordinator terminalize, task có thể
  được mở khóa rồi khởi động một run mới — đúng thứ mà cancel vừa yêu cầu dừng.
  Gặp Run còn active thì MUST ghi một **WorkItem-level**
  cancel intent bền vững — intent theo Run là không đủ vì một task có thể có nhiều run — rồi dùng
  **cùng** quiesce protocol ở trên cho từng active Run; WorkItem chỉ terminal sau khi mọi active Run đã
  dừng. Không có đường terminalize WorkItem trong khi run còn chạy. Nếu Completion `PASS` commit trước
  và WorkItem đã `DONE`, `CancelWorkItem` là no-op idempotent trả terminal result; nếu cancel intent
  commit trước, Completion đến muộn bị CAS từ chối như mọi authoritative outcome khác.
- `ResolveWorkItemBlocker` → WorkItem `BLOCKED → READY`. Command này **chính là** thứ chuyển blocker
  sang `RESOLVED|WAIVED`; nó không đòi blocker đã resolved từ trước. Nó revalidate điều kiện thực tế
  của blocker, authorize quyết định waive nếu có, rồi atomically đổi trạng thái blocker và WorkItem
  trong cùng transaction. Precondition thật là: blocker tồn tại và đang `OPEN`, không còn Run
  nonterminal, và không workspace nào của family đang `QUARANTINED`. Blocker đã `RESOLVED|WAIVED` là
  no-op idempotent, không phải lỗi.

  Xử lý blocker và mở khóa WorkItem là **hai** điều kiện, không phải một. Blocker luôn được chuyển
  trạng thái; nhưng WorkItem chỉ `BLOCKED → READY` khi số blocker `OPEN` còn lại bằng 0. Một WorkItem
  có ba blocker mà resolve một cái thì vẫn ở `BLOCKED` — ghép cứng hai việc này sẽ mở khóa task còn
  đang bị chặn bởi lý do khác.

Hai resolution mode có nghĩa khác nhau và không thay thế nhau. Command payload MUST chọn mode tường
minh; không có mode mặc định:

- `RESOLVED` — điều kiện gây blocker đã thực sự biến mất và được revalidate. Cần reason.
- `WAIVED` — điều kiện có thể còn đó nhưng operator chấp nhận rủi ro. Cần actor, reason, policy grant
  và persist một `DecisionArtifact`; nó là một quyết định có audit, không phải một nút bỏ qua.

| Blocker type | `RESOLVED` | `WAIVED` |
|---|---|---|
| `RUN_CANCELLED` | Có | Không cần |
| `COMPLETION_POLICY_FAILED` | Có, sau khi evidence đạt | Có, cần policy grant + DecisionArtifact |
| `SCOPE_EXPANSION_REQUIRED` | Chỉ qua approval flow của ADR-011 | **Không bao giờ** |
| `ISOLATION_ENFORCEMENT_UNAVAILABLE` | Có, sau revalidate | **Không bao giờ** |
| `ADAPTER_BUILD_DRIFT` | Có, sau khi build được đăng ký lại | **Không bao giờ** |
| `CAPABILITY_REQUIREMENT_UNSATISFIED` | Có, sau revalidate | **Không bao giờ** |
| `WRITE_CAPABILITY_OR_GRANT_MISSING` | Có, sau khi grant được cấp | **Không bao giờ** |

Bốn admission reason và scope expansion **không waive được** vì waive chúng đúng bằng việc vô hiệu hóa
enforcement mà ADR-011, ADR-013, ADR-022 và ADR-023 dựng lên: waive `ISOLATION_ENFORCEMENT_UNAVAILABLE`
là chạy không cô lập, waive `ADAPTER_BUILD_DRIFT` là chạy sai build đã pin. Muốn đổi hành vi thì đổi
definition/policy rồi republish, không phải waive một blocker.

CompletionDecision `BLOCK` để Run ở `BLOCKED`. Alpha không có đường resume một Run đã BLOCKED; đường
chuẩn là `CancelRun` → resolve toàn bộ blocker → start một run mới.

### `RetryBlockedActivation`

Attempt `BLOCKED` vì admission là dead-end nếu không có command thoát. `RetryBlockedActivation` là
đường đó, với semantics chặt:

- Precondition tường minh: Run ở `RUNNING|BLOCKED`, WorkItem ở `ACTIVE|BLOCKED`, NodeRun ở `BLOCKED`
  với Attempt cuối `BLOCKED` mang reason thuộc nhóm admission, expected version khớp, và cancel fence
  chưa được set — không retry một Run đang `CANCELLING` hay đã terminal.
- Admission blocker được lưu thành một row `blockers` với type bằng chính `TerminationReason`, nên UI và
  valid-action query đọc được mà không phải suy từ Attempt.
- Revalidate exact pin trước, trong một bước riêng. **Thành công** thì atomically tạo NodeRun activation
  và Attempt mới. **Thất bại** thì giữ nguyên blocker hiện tại và MUST NOT tạo thêm một blocked
  activation nữa — nếu không mỗi lần bấm retry sẽ sinh một chuỗi Attempt `BLOCKED` vô hạn.
- Không bao giờ repin Run sang dependency khác. Khi adapter drift không khôi phục được exact pin (build
  cũ đã biến mất), valid action trả về là `CancelRun` rồi publish/republish, **không** phải repin.

Lý do: exit code hoặc timeout của một process không đủ để tuyên bố side effect đã không xảy ra; nhãn
`CANCELLED` sai sẽ che mất workspace cần reconcile. Và việc một attempt thành công không có nghĩa
người vận hành mất quyền hủy run.

## 23. ADR-021 — CompletionDecision outcome và transition từ `VERIFYING`

**Quyết định:** CompletionPolicy phát đúng bốn outcome typed, mỗi outcome có một transition hợp lệ duy
nhất; `VERIFYING` không phải trạng thái chỉ đi tới thành công.

| Outcome | WorkflowRun | WorkItem |
|---|---|---|
| `PASS` | `VERIFYING → SUCCEEDED` | `→ DONE` trong cùng transaction |
| `REWORK` | `VERIFYING → RUNNING` kèm activation mới theo rework edge đã publish | giữ `ACTIVE` |
| `BLOCK` | `VERIFYING → BLOCKED` | `→ BLOCKED` |
| `FAIL` | `VERIFYING → FAILED` | `→ BLOCKED` kèm blocker `COMPLETION_POLICY_FAILED` |

- `REWORK` chỉ hợp lệ khi WorkflowVersion đã pin có rework edge tương ứng. Không có edge hợp lệ thì
  CompletionPolicy MUST trả `BLOCK`; orchestrator không được tự dựng route không có trong graph.
- `FAIL` không có đích terminal cho WorkItem trong Alpha: tập state WorkItem là
  `BACKLOG|READY|ACTIVE|BLOCKED|DONE|CANCELLED` và **không** có `FAILED`. WorkItem chuyển `BLOCKED` cùng
  một blocker typed `COMPLETION_POLICY_FAILED`. Muốn thử lại phải có operator command hoặc một run mới;
  hệ thống không tự đưa WorkItem về `ACTIVE`. Không dùng `CANCELLED` vì đây không phải quyết định hủy.
- Mọi outcome persist một `DecisionArtifact` với policy version, input evidence và exact
  RevisionSet/ReleaseSet đã xét.
- ADR-011 vẫn giữ: END chỉ tạo completion candidate và chỉ CompletionPolicy transaction mới tạo Run
  `SUCCEEDED` cùng WorkItem `DONE`.

## 24. ADR-022 — AdapterBuildVersion là operational registry, không phải DefinitionKind

**Quyết định:** `AdapterBuildVersion` thuộc mặt phẳng operational/supply-chain, không nằm trong tập
DefinitionKind authoring (Workflow, Block, Skill, Layer, Engineering Pack, Agent Profile, Command,
Gate, Policy). Nó vẫn là dependency có identity bất biến được compiled manifest pin theo ADR-012.

Flow đăng ký bắt buộc:

1. Probe executable đã cấu hình.
2. Hiển thị candidate fingerprint, protocol version và capability manifest quan sát được.
3. Operator xác nhận, hệ thống đăng ký một `AdapterBuildVersion` bất biến.
4. Workflow/Agent Profile cần build mới phải được republish để pin build đó.
5. Run đang chạy không bao giờ được tự động repin sang build mới.

Probe và register là hai thao tác tách rời nên có khoảng TOCTOU. Một candidate token chứa fingerprint
**không** đóng được khoảng đó: token chỉ chứng minh "lúc probe file từng có hash này", không chứng minh
file vẫn còn nguyên lúc register. Re-hash bên trong transaction cũng không hợp lệ vì §11.1 cấm gọi
filesystem/process trong application transaction.

Thứ tự bắt buộc của `RegisterAdapterBuild`:

1. `ProbeAdapterBuild` phát một candidate token do server ký, có expiry ngắn. Token MUST bind toàn bộ
   tuple mà operator xác nhận, không chỉ một fingerprint: provider key, canonical executable path và
   content hash, protocol version, hash của capability manifest, OS/toolchain/config identity, một nonce
   và expiry. Thiếu bất kỳ thành phần nào thì operator đang xác nhận một thứ khác với thứ được đăng ký.
2. `RegisterAdapterBuild` **re-probe/re-hash executable ngoài database transaction**, ngay trước commit.
3. So sánh kết quả đo mới với token; mismatch hoặc token hết hạn thì reject, không đăng ký.
4. Transaction chỉ persist giá trị server vừa đo ở bước 2 — không persist giá trị trong token và không
   persist bất kỳ capability nào do client gửi lên.

Signing key của candidate token là per-installation, sinh lúc khởi tạo, giữ ngoài repository và xoay
được bằng thao tác vận hành tường minh. Vì `probe` và `register` là hai lần gọi CLI/API riêng, key MUST
bền qua tiến trình — không dùng key chỉ tồn tại trong bộ nhớ một lần chạy. Xoay key làm mọi token đang
lưu hành mất hiệu lực, và đó là hành vi đúng.

Khoảng TOCTOU còn lại giữa bước 2 và commit là không thể loại bỏ bằng registration; nó được bắt ở tầng
sau: execution admission vẫn hash lại executable trước dispatch, nên drift xảy ra *sau* registration bị
từ chối bằng `ADAPTER_BUILD_DRIFT`.

Surface bắt buộc trong Alpha: CLI `agentkit adapter probe|register|list|show`, API list/detail/probe/
register, và Doctor UI hiển thị registered/unregistered kèm action đăng ký. Thiếu surface này thì việc
nâng cấp CLI provider sẽ khóa toàn bộ workflow có AGENT node mà không có lối thoát trong sản phẩm.

Doctor trước khi registry tồn tại chỉ được báo observed executable fingerprint/capability; nó không
được tuyên bố registry admission.

## 25. ADR-023 — Isolation enforcement fail-closed trước spawn

**Quyết định:** profile isolation đã pin không bao giờ được auto-downgrade; kiểm tra enforcement xảy ra
trước khi process được spawn.

- Worker không được hạ `ENFORCED_ISOLATED` xuống `OPERATOR_TRUSTED_LOCAL` trong bất kỳ hoàn cảnh nào.
- Không cưỡng chế được profile đã pin thì admission trả typed error
  `ISOLATION_ENFORCEMENT_UNAVAILABLE` và Attempt bị block trước `ProcessSupervisor.Start`.
- Contract test MUST assert process spawn count bằng 0 trong trường hợp này.
- `OPERATOR_TRUSTED_LOCAL` chỉ hợp lệ khi definition/policy pin tường minh, không phải là fallback ngầm.

Đây là bước cưỡng chế còn thiếu của ADR-013: ADR-013 khai báo hai tier, ADR này khóa thời điểm và cách
từ chối.

## 26. ADR-024 — Phase classification của acceptance criteria

**Quyết định:** mọi acceptance criterion (AK-ARCH, HE và Go core MUST) được phân loại phase **trước**
khi V1 bắt đầu, không phải khi tổng hợp verdict.

Nhãn hợp lệ:

- `ALPHA_MUST` — phải PASS để Alpha đạt gate.
- `BETA_ADAPTER_GATE` — chỉ kiểm được khi adapter thứ hai tồn tại.
- `BETA_PARITY_GATE` — cần hai topology để so sánh.
- `CROSS_PHASE_GUARD` — Alpha kiểm phần boundary/port, phần còn lại thuộc Beta.
- `NOT_APPLICABLE` — kèm authority reason.

Phân loại đã chốt cho các trường hợp gây mâu thuẫn:

- `AK-ARCH-019` là `BETA_ADAPTER_GATE`; Alpha chỉ xây reusable repository contract harness và chạy nó
  trên SQLite.
- `AK-ARCH-026` là `BETA_PARITY_GATE`.
- `AK-ARCH-028` là `CROSS_PHASE_GUARD`; Alpha kiểm port/import boundary, adapter parity đầy đủ để sau.

V8 không được tự phân loại lại criteria và không được dùng `deferred` cho một `ALPHA_MUST`.

## 27. ADR-025 — Command scope: installation và project

**Quyết định:** `CommandEnvelope.ProjectID` bắt buộc là sai với các thao tác vốn có bản chất
installation-global. Envelope đổi sang một scope phân biệt:

```text
CommandScope = INSTALLATION | PROJECT(ProjectID)
```

Tập installation là danh sách đóng, liệt kê tường minh:

| Loại | Installation scope |
|---|---|
| Command | `CreateProject`, safe-settings mutation, `ProbeAdapterBuild`, `RegisterAdapterBuild` |
| Query | health/doctor, `ListProjects`, safe-settings read, adapter-build list/detail |

Mọi thứ khác là project scope. Đặc biệt: run, job, workspace và release diagnostics đều **project**-
scoped — chỉ health/doctor ở mức installation mới là installation-scoped. Không dùng cụm gộp
"Doctor/diagnostics", vì nó che mất ranh giới này.

Adapter build registry là installation-global và giữ nguyên như vậy: một Project không được có build
riêng, vì build là thuộc tính của máy đang chạy chứ không phải của dự án. Route `/adapter-builds` do đó
nằm ngoài cây `/projects/{id}`.

Envelope đổi kéo theo hai chỗ phải đổi cùng. Handler load aggregate theo `Scope + ID` thay cho
`ProjectID + ID`. Và `command_receipts` lưu một **`scope_key` non-null** — `installation` hoặc
`project:<id>` — chứ không phải cặp `scope_kind + scope_id`: installation scope không có ID, nên một
`scope_id` nullable trong unique tuple sẽ cho phép nhiều row NULL trên SQLite và duplicate installation
command sẽ lọt qua idempotency.

Query contract cũng bỏ ràng buộc "mọi query scope bằng ProjectID": query installation-scoped tồn tại và
được liệt kê tường minh ở bảng trên. Mọi query project-scoped vẫn MUST scope bằng ProjectID — nới lỏng này chỉ áp
cho tập installation đã liệt kê, không phải mặc định mới.

Handler MUST validate scope khớp command type; command project-scoped thiếu ProjectID và command
installation-scoped mang ProjectID đều bị reject.

## 28. ADR-026 — ROUTER giới hạn một outcome cho Alpha

**Bối cảnh:** phát hiện trong lúc review V4-03 (`docs/design/06-v4-runtime-engine.md`, sau khi PR đã
merge): go-core-spec §6 mô tả `ROUTER` là "chọn outcome từ typed state bằng rule deterministic", và
`docs/harness-engineering/14-lec-14-do-thi-dieu-phoi.md`/`09-lec-09-...` đều giữ nguyên vocabulary
`ROUTER` từ lecture gốc mà không tự giới hạn số outcome. Nhưng V2-08 (authoring schema) không cho
`ROUTER` bất kỳ config field nào (`internal/domain/workflow/node_config.go` không có
`RouterNodeConfig`) — không ADR, design doc hay lecture nào định nghĩa "rule deterministic" đó thực sự
là gì khi `ROUTER` khai báo từ hai outcome trở lên. Trước ADR này, compiler vẫn chấp nhận publish một
`ROUTER` nhiều outcome; chỉ runtime (`internal/app/runtime.AdvanceRun`, V4-03) từ chối, và chỉ khi
không có outcome được cung cấp từ bên ngoài — nghĩa là một workflow publish hợp lệ vẫn có thể deadlock
vĩnh viễn tại `ROUTER` đó lúc chạy, không có lỗi nào được báo ở thời điểm publish.

**Quyết định:** Alpha giới hạn `ROUTER` chỉ được khai báo **đúng một** outcome. Publish một `ROUTER`
với từ hai outcome trở lên là lỗi validation (compile-time), không phải hành vi runtime âm thầm
deadlock. Với đúng một outcome, `ROUTER` là pass-through xác định (deterministic) — không cần rule gì
khác, tương đương một node structural như `START`.

Multi-outcome `ROUTER` (chọn outcome từ typed shared-state bằng một rule thật) là khả năng hoãn lại:
cần một ADR riêng định nghĩa chính xác hình dạng của rule đó (biểu thức trên field nào, kiểu so sánh
gì, ai/khi nào evaluate) và một `RouterNodeConfig` mới trong authoring schema (V2-08's kế nhiệm) trước
khi runtime có thể tự resolve. Không phát minh rule đó trong V4.

**Hệ quả code (đã thực hiện cùng ADR này):**
- `internal/domain/workflow/validation.go`: publish-time reject `ROUTER` có > 1 outcome.
- `internal/app/runtime.AdvanceRun` (V4-03) giữ nguyên hành vi tự resolve `ROUTER` một-outcome, giữ
  `ErrOutcomeRequired` làm defense-in-depth cho trường hợp không qua compiler thật (không còn reachable
  qua `workflow.Compile` sau ADR này, nhưng vẫn đúng nếu một `WorkflowVersion` được dựng theo cách khác
  trong tương lai).

## 29. Baseline sau review thiết kế

ADR-001…026 là baseline hiện hành. Các mục ADR-001…010 giữ lịch sử quyết định ban đầu; khi đọc phải áp
dụng ma trận sau:

- ADR-011 supersede retry cùng NodeRun trong ADR-002 và bổ sung completion candidate;
- ADR-012 refine identity/hash của ADR-001 và adapter version của ADR-007;
- ADR-013 refine enforcement/capability của ADR-003;
- ADR-014 supersede toàn bộ remote Git authority Alpha trong ADR-004;
- ADR-015 refine cursor/rebuild/recovery của ADR-008;
- ADR-017 supersede blanket “evidence local 7 ngày” trong ADR-005;
- ADR-018 supersede terminal panel Alpha trong ràng buộc/UI baseline cũ;
- ADR-016 và ADR-019 thêm trust/transaction protocol chưa được ADR cũ khóa;
- ADR-020 refine ADR-011 bằng `TerminationReason` typed và tập terminal Attempt gồm `BLOCKED`, đồng
  thời thêm state `CANCELLING` chưa được ADR cũ khóa;
- ADR-021 refine completion candidate của ADR-011 bằng bốn outcome và transition tương ứng;
- ADR-022 refine ADR-012 bằng cách tách AdapterBuildVersion khỏi DefinitionKind và khóa surface đăng ký;
- ADR-023 refine ADR-013 bằng thời điểm và cách từ chối khi enforcement không khả dụng;
- ADR-024 thêm phase classification cho acceptance criteria, chưa được ADR cũ khóa;
- ADR-025 supersede ràng buộc `ProjectID` bắt buộc trong command envelope và "mọi query scope bằng
  ProjectID" của Go core spec §8/§9;
- ADR-026 refine mô tả `ROUTER` của Go core spec §6 bằng ràng buộc Alpha "đúng một outcome", đóng gap
  runtime-only-reject phát hiện lúc review V4-03.

Thay đổi semantics tiếp theo vẫn cần ADR mới; không sửa âm thầm lịch sử quyết định.
