# Architecture decisions cho Agent Kit

> Trạng thái: ACCEPTED — product owner xác nhận ADR-001…010 ngày 2026-08-28, ủy quyền chốt
> ADR-011…019 ngày 2026-08-29 và chốt ADR-020…025 ngày 2026-08-31 sau review bộ thiết kế Alpha.
> ADR-026 và ADR-027 chốt ngày 2026-09-05, phát hiện và quyết định trực tiếp trong lúc user review code
> V4-03 và scoping V4-04.
>
> ADR-028 chốt ngày 2026-09-06 theo quyết định của product owner về operator CLI cuối V6.
>
> ADR-029 chốt ngày 2026-09-24, mở đầu V7 (UI Alpha) — quyết định framework/toolchain bằng evidence
> theo yêu cầu V7-01.
>
> ADR-030…033 chốt ngày 2026-10-01 cho V9 (`docs/design/12-v9-harness-alignment.md`, task V9-00): orchestrator
> viết và chốt theo ủy quyền của product owner; product owner duyệt ở PR của V9-00.
>
> ADR-034…038 chốt ngày 2026-10-10 cho V11-01…V11-05 (`docs/design/14-v11-bugfix.md`): thiết kế do product owner duyệt, orchestrator
> viết ADR khi triển khai.
>
> Ngày lập baseline hiện hành: 2026-08-31 (ADR-001…025); 2026-09-05 (ADR-026, ADR-027);
> 2026-09-06 (ADR-028); 2026-09-24 (ADR-029); 2026-10-01 (ADR-030…033); 2026-10-10 (ADR-034…038).

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

Surface bắt buộc trong Alpha: CLI `aw adapter probe|register|list|show` (tên canonical theo ADR-028),
API list/detail/probe/register, và Doctor UI hiển thị registered/unregistered kèm action đăng ký. Thiếu surface này thì việc
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

## 29. ADR-027 — RuntimeExecutionConfigProvider: authority nội bộ, không nhận hash từ caller

**Bối cảnh:** phát hiện trong lúc scoping V4-04 (`docs/design/06-v4-runtime-engine.md`):
`ResolvedExecutionProfileV1.RuntimeExecutionConfigHash` (ADR đóng gap tại correction round 2 của V4-03)
cần một giá trị thật đại diện cho composition-root Configuration (go-core-spec §19: process timeout,
output limit, environment/network/secret-reference policy) một execution thực sự chạy dưới. Repo đã có
`internal/app/config.Config` thật (V1-03, `ProcessOutputLimit`/`ProviderExecutables` đã validate), nhưng
chưa có projection nào từ `Config` sang một execution snapshot theo node — thiếu resolver, không thiếu
hoàn toàn dữ liệu.

Phương án đầu tiên được cân nhắc — nhận thẳng một chuỗi hash làm tham số đầu vào cho transaction lên
lịch của V4-04 — bị bác: một hash do caller tự khai không thể verify được là tính đúng từ config thật
hay không; nếu API/provider/agent có quyền tự khai hash, authority bị đảo ngược (đúng như cách
`ProjectID` luôn được tải lại và kiểm chứng từ database thay vì tin ở giá trị caller gửi lên).

**Quyết định:** V4-04 không nhận hash trần. Thay vào đó:

1. `internal/domain/runtime.RuntimeExecutionConfigSnapshotV1` — type domain thuần, canonicalize/hash
   giống `ResolvedExecutionProfileV1`, chỉ core (`NewRuntimeExecutionConfigSnapshotV1`) mới được tính
   `RuntimeExecutionConfigHash`, không ai được truyền hash đó trực tiếp.
2. `ports.RuntimeExecutionConfigProvider` — port bắt buộc V4-04's scheduling transaction phụ thuộc vào,
   trả về snapshot đã resolve (structured data, không phải hash).
3. Luồng: resolve snapshot **ngoài** database transaction (I/O thật, giống cách `workspaceprovision`'s
   provider chạy I/O ngoài Tx) → core validate/normalize/hash snapshot → mở scheduling transaction, kiểm
   tra lại Run/NodeRun/definition pins (phòng stale trong khoảng ngoài-Tx) → đưa hash vào
   `ResolvedExecutionProfileV1` → pin `ExecutionProfileHash` (không phải `RuntimeExecutionConfigHash`
   riêng — tránh hai authority độc lập) vào NodeRun và Attempt → tạo job/event atomically. Provider thiếu
   hoặc snapshot không hợp lệ → fail closed, không tạo Attempt/job.
4. `RuntimeExecutionConfigHash` không bao giờ xuất hiện trong HTTP/request DTO nào — chỉ nội bộ giữa
   Provider và core.

**Phân chia ownership giữa các task:**
- **V4-04** (task này): định nghĩa port `RuntimeExecutionConfigProvider`, type
  `RuntimeExecutionConfigSnapshotV1`, canonicalization/hash, và một fake provider cho test — không tự
  resolve config thật.
- **V4-05** (Generic worker execution envelope với fake executor): dùng fake provider cùng fake
  executor, không đổi port.
- **V5-05** (Production ProcessSupervisor hardening): sở hữu production provider thật + composition-root
  wiring từ `internal/app/config.Config` — vì task đó đã sở hữu output bound/environment/network/
  secret/isolation enforcement thật trên Windows/Linux.

## 30. ADR-028 — Canonical operator CLI `aw` và parity với UI

**Bối cảnh:** Alpha có nhiều public application command/query trước khi UI được xây. Nếu CLI chỉ giữ
`definition|doctor|evidence`, người dùng chưa thể vận hành sản phẩm từ terminal sau V6; đồng thời UI và
CLI rất dễ tạo hai tập capability hoặc hai đường authority khác nhau. Tên binary Alpha ban đầu
`agentkit` cũng dài và đã bị dùng lẫn với binary bằng chứng V0 `agentkit-spike`.

**Quyết định:** executable sản phẩm canonical là `aw` (`aw.exe` trên Windows), với grammar
`aw <resource> <action> [flags]`; mode/utility được giữ ở top-level (`aw serve`, `aw worker`,
`aw doctor`, `aw version`, `aw help`). V6-15A migrate composition root `cmd/agentkit` sang `cmd/aw`. Đây là
đổi tên trước release nên không duy trì alias production `agentkit`; binary `agentkit-spike` vẫn giữ
nguyên như live V0 regression/evidence gate và không phải alias của `aw`.

Đến cuối V6, mọi public query/mutation mà V7 UI dùng MUST có đúng một lệnh `aw` tương ứng. Ngoại lệ duy
nhất là bootstrap/static asset của browser; SSE được biểu diễn bằng `aw events watch`. Quan hệ này được
khóa bằng inventory machine-readable bốn cột:

```text
UI action/query <-> HTTP operationId <-> aw command <-> public application command/query
```

Inventory MUST được kiểm theo cả hai chiều: thiếu hoặc trùng mapping đều fail gate. Mọi action mới thêm
vào ma trận UX V6-00 phải có public application operation, HTTP operationId và **reserved CLI invocation
shape** trước khi API freeze ở V6-12; implementation CLI hoàn tất trong V6-15A…V6-15P. Không được ghi
“UI-only”.
Kanban drag/drop chỉ là presentation của một **named valid action** đã tồn tại trong inventory, không
tạo `set-status` generic và không cho client tự đưa WorkItem sang `DONE`.

Khóa parity dùng **invocation shape** (command path + scope discriminator), không chỉ chuỗi verb:
nhánh definition global và project có thể cùng path CLI nhưng lần lượt là `--scope global` và
`--project-id <id>`, map tới hai operationId khác nhau. Tập local-only `{aw serve, aw worker, aw help,
aw version, aw evidence verify}` không có và không được tạo endpoint/UI giả; chúng nằm ngoài inventory
bốn chiều nhưng vẫn chịu boundary, output, redaction và platform tests của V6-15P.

Gap `BACKLOG → READY` được đóng bằng đúng một public command `MarkWorkItemReady`: server chạy lại
readiness/contract validator và CAS đúng transition đó, append registered `WORK_ITEM_MARKED_READY` v1.
Command không nhận target status. Mọi transition
khác vẫn thuộc authority chuyên biệt đã có: `StartWorkflowRun`, blocker/scope handling,
CompletionPolicy và cancellation. Component discovery không có operator mutation riêng; nó tiếp tục
thuộc repository onboarding/probe, nên UI/CLI chỉ query Component đã được discover thay vì expose helper
`CreateComponent` không có command receipt/event.

ADR này cũng refine danh sách scope đóng của ADR-025 cho definition plane đã tồn tại: `CreateDefinition`
và `PublishDefinitionVersion` dùng global `INSTALLATION` hoặc `PROJECT(ProjectID)`. Với collection
create/list, route (`/definitions/...` hay `/projects/{id}/definitions/...`) là authority chọn scope;
create persist đúng scope đó. Với item/publish/version operation, handler reload Definition rồi
cross-check scope từ route/CLI; caller không được chọn khác target đã lưu. Đây là ngoại lệ phân biệt
theo target, không phải quyền biến mọi command thành installation-scoped.

CLI là delivery adapter ngang hàng với HTTP, gọi cùng application command/query contract. Nó MUST NOT
ghi SQLite, gọi Git/process/provider trực tiếp hoặc expose command internal của scheduler/worker như
`AdvanceRun`, `ExecuteWorkspaceReconciliation` hay `ExecuteWorkspaceSetRelease`. Composition root được
phép wire concrete adapters; handler CLI chỉ phụ thuộc application ports/use cases. Không có remote Git
mutation trong CLI Alpha.

`Actor` và `ActorRoles` là authentication context, không phải input tự khai. Alpha dùng một
`LocalPrincipalSnapshot {Actor, Roles[]}` đọc từ trusted startup config: `aw serve` bind snapshot đó với
per-start session token trong memory, còn one-shot `aw` resolve cùng snapshot qua composition root.
HTTP body/header và CLI flag MUST NOT được phép override actor/roles. Thay đổi principal là
restart-required và không nằm trong safe-settings mutation. Nhờ vậy HTTP và CLI kiểm
`AuthorizedRoles` bằng cùng principal; Beta có thể thay resolver bằng identity provider mà không đổi
command handler hay RequestHash.

Config keys canonical là `localPrincipal.actor` và `localPrincipal.roles`; thiếu toàn bộ thì dùng
`actor=local-operator`, `roles=[operator]`. Actor/role phải non-empty, roles unique và so khớp
case-sensitive. “Không override bằng CLI flag” nghĩa là không có per-command `--actor`/`--role`; global
composition option chọn một trusted config file vẫn được phép.

Mọi mutation giữ nguyên `CommandEnvelope`, scope, expected version và idempotency semantics; CLI cung
cấp/propagate idempotency key thay vì tạo fast path. `--idempotency-key` là optional cho thao tác tay:
nếu thiếu, `aw` tạo key trước dispatch và luôn trả key đó trong human/JSON envelope; automation/retry
SHOULD truyền key tường minh. Job bất đồng bộ trả aggregate/job ID và trạng thái
được accept; tùy chọn `--wait` chỉ quan sát query/event cho tới terminal hoặc timeout, không thực thi
side effect thay worker. Lệnh hữu hạn có human output và `--json` ổn định; stream artifact dùng
`--output <path|->`, event stream dùng NDJSON, còn process/help không bị ép vào JSON giả. Exit code typed
và diagnostic an toàn; secret không được nhận bằng argv hay in lại trong output.

Không có `--wait`: stdout phát đúng một accepted/result envelope. Có `--wait`: stdout chỉ phát đúng một
final envelope chứa idempotency key, operation reference và state cuối quan sát được; progress đi stderr.
Timeout hoặc interrupt trả typed error/exit cùng operation reference + last observed state nhưng **không**
cancel durable operation và không dispatch `CancelRun`, `CancelWorkItem` hay command hủy nào khác.
Trong `--json`, success hoặc typed failure đều là đúng một JSON document trên stdout; stderr chỉ dành
cho diagnostic/progress. Human mode giữ result ở stdout, error/progress ở stderr. Command có impact cao
chỉ prompt khi stdin là TTY và human mode; `--json`/non-interactive không prompt, phải có `--yes` hoặc
trả typed `PRECONDITION_FAILED` với detail `confirmation=required` trước dispatch.

Trước parity gate, `ProbeAdapterBuild` và `RegisterAdapterBuild` lịch sử phải nhận installation-scoped
`CommandEnvelope` và có receipt semantics. Probe same-key trả exact candidate token đã lưu, không tự gia
hạn; Register chỉ append registered `ADAPTER_BUILD_REGISTERED` v1 khi insert build mới thật sự. Mọi
probe/re-probe/hash vẫn ở ngoài database transaction theo ADR-022.

Public projection rebuild là operator operation typed, project-scoped và job-backed; HTTP/CLI chỉ ghi
request rồi quan sát tiến trình. Nó không cho client sửa projection cursor/read-model row trực tiếp.

## 31. ADR-029 — UI framework cho V7 Alpha: React 19 + Vite

**Bối cảnh:** V7-01 yêu cầu chọn UI framework/toolchain bằng evidence trước khi scaffold V7-02 — so
tốc độ, ecosystem, bundle, khả năng bảo trì team — không được chọn theo sở thích ngầm. Production UI
build phải là static bundle được chính `aw serve` phục vụ qua static handler của V6-01 (không SSR,
không server Node riêng, theo ADR-016).

**Ứng viên và bằng chứng:**
- **React 19 + Vite 8 + TypeScript + Tailwind 4:** đã có prototype QA-reviewed thật (nhánh
  `codex/ui-design-qa`, thư mục `web/`), hiện thực đủ shell (`LeftNav`, `TopBar`) và 9 screen khớp
  `docs/design/09a-v7-ui-component-spec.md` (Kanban, TaskDetail, Definitions, Doctor, Projects,
  AdapterBuilds, RunDiagnostics, Settings, CreateWorkItem, RepairAudit). Build đã verify sạch —
  `pnpm install` + `pnpm build`: 1849 module, 439ms, output 358.41 kB JS (gzip 98.73 kB) + 25.47 kB CSS
  (gzip 5.91 kB), phù hợp một SPA static-served cho single-operator local tool.
- **Angular:** router/forms/DI built-in đầy đủ nhưng runtime/CLI nặng hơn nhiều so với nhu cầu thực tế,
  và không có prototype nào tận dụng được — chọn Angular đồng nghĩa viết lại toàn bộ 9 screen chỉ để có
  lại tính năng ứng viên kia đã có sẵn.
- **Solid.js:** bundle runtime nhỏ hơn (không virtual DOM), JSX gần giống React, nhưng ecosystem/tooling
  test (Testing Library, DevTools) kém trưởng thành hơn; vẫn phải viết lại toàn bộ prototype hiện có.
- **Vanilla/htmx:** không đủ cho state phức tạp V7 cần — SSE reconnect/backoff và stale/degraded
  indicator (V7-04), drag/drop Kanban (V7-06), form schema validation nhiều screen — sẽ phải tự dựng lại
  chính các pattern mà một framework cung cấp sẵn.

**Quyết định:** chọn **React 19 + Vite 8 + TypeScript 5 + Tailwind CSS 4**, kiến trúc SPA thuần (không
SSR, không meta-framework Next.js/Remix), giữ path alias `@` → `src`. Toàn bộ source UI chuyển từ
`UI draft/` sang `web/` theo layout đã khóa ở `01-system-design.md` (`web/ — UI source sau UI quyết
định`).

**Ma trận quyết định:**

| Tiêu chí | React+Vite | Angular | Solid.js | Vanilla/htmx |
|---|---|---|---|---|
| Bằng chứng thật (chạy được) | Có — 9 screen, build đo được | Không, phải viết lại | Không, phải viết lại | Không, phải viết lại |
| Ecosystem (router/forms/test/SSE) | Trưởng thành: TanStack Query, RHF+Zod | Đầy đủ nhưng nặng hơn cần thiết | Nhỏ hơn, ít mature hơn | Thiếu, phải tự dựng |
| Bundle đo được | 358 kB / 98 kB gzip | Chưa đo, thường lớn hơn | Chưa đo, ước tính nhỏ hơn | Nhỏ nhất nhưng không đủ tính năng |
| Maintainability | Cao — code có sẵn, convention quen | Learning curve DI/RxJS mới | Learning curve mới, cộng đồng nhỏ | Không scale cho 9+ screen state phức tạp |

**Hệ quả:**
- V7-02 scaffold thêm trên nền này: router (`wouter` hoặc `react-router`), `@tanstack/react-query` cho
  SSE reconnect/query invalidation, `react-hook-form` + `zod` cho form schema, Vitest + Testing Library
  cho unit/component test, Playwright cho E2E — đúng yêu cầu "strict TypeScript/lint/unit/component/E2E"
  của V7-02.
- Prototype `web/` (nhánh `codex/ui-design-qa`) đã được dọn cruft riêng của nền tảng Figma Make (script
  deploy/langserver, `AGENTS.md` mô tả môi trường hosted của họ) trước khi dùng làm nền V7-02;
  `vite.config.ts` còn giữ hai plugin dev-only cần review lại lúc scaffold thật (`figmaMakeKitPlugin`
  phục vụ preview riêng Figma, không cần cho `aw serve`).
- Không xây song song prototype Angular/Solid để so trực tiếp: chi phí viết lại toàn bộ 9 screen chỉ để
  lấy thêm số đo không tương xứng lợi ích. Quyết định dựa trên số đo thật của ứng viên đã chọn cộng đánh
  giá định tính có căn cứ (ecosystem, learning curve, yêu cầu không-SSR) cho các ứng viên còn lại.
- **Nguồn:** V7-01 (`docs/design/09-v7-alpha-ui.md`).

## 32. ADR-030 — Read-only của attempt đo so với trạng thái worktree lúc attempt bắt đầu

**Bối cảnh:** V5-12 khóa rằng checker nhận "requirement/diff/evidence" của maker và chạy với context riêng. Nhưng
read-only của CHECKER và MACHINE_GATE hiện được kiểm bằng `validateStrictlyReadOnlyDiffs`
(`internal/app/runtime/agent_node_executor_resources.go`). Hàm này coi attempt vi phạm scope khi
`WorkspaceDiff.Files` khác rỗng. `Files` lấy từ `git status` so với `HEAD` (`internal/adapters/gitworktree/provider.go`,
`Diff`), còn thay đổi của maker chỉ được commit ở bước ReleaseSet/local commit (ADR-014). Hệ quả: đặt CHECKER hoặc
MACHINE_GATE sau một MAKER trong cùng run thì attempt read-only **luôn** bị `SCOPE_VIOLATION`, dù checker không sửa
gì. Điều này đã tái hiện khi chạy thật (G1 trong `docs/harness-engineering/15-doi-chieu-v9.md`), và làm HE-09-M04,
HE-05-M06, HE-14-M08 không đạt được trong một workflow.

**Quyết định:**

1. Trước khi spawn process của một attempt read-only (AGENT role `CHECKER`, `MACHINE_GATE`), engine chụp `InputTree`
   cho từng RepositoryWorkspace được mount. `InputTree` là tree object nội dung của working tree: mọi file tracked và
   mọi file untracked không bị ignore. Nó được tạo bằng một index tạm riêng (`GIT_INDEX_FILE`), nên không chạm index
   của worktree, và không tạo commit, ref hay branch nào.
2. Việc chụp là I/O git nên chạy ngoài mọi transaction. Kết quả được ghi **một lần** (set-once, kiểm fencing theo job
   lease) vào bản ghi đầu vào của attempt trong một transaction ngắn, trước khi spawn. Nếu attempt đã có `InputTree`
   thì dùng lại, không chụp lại. Attempt retry hoặc attempt do recovery tạo cho **cùng NodeRun** thừa kế `InputTree`
   của attempt trước ngay trong transaction tạo attempt. Nhờ vậy, thay đổi mà một attempt read-only để lại trước khi
   crash không thành input của attempt sau. `InputTree` **không** được ghi vào `workspace.Revision`, vì
   `Revision.VCSObjectID` luôn là commit.
3. Sau khi xác nhận quiescence, engine chụp `OutputTree` theo cùng cách. Attempt read-only đạt khi
   `OutputTree == InputTree` trên mọi mount. Nếu khác, kết quả vẫn là `SCOPE_VIOLATION` như hiện nay, nhưng kèm danh
   sách path khác nhau giữa hai tree.
4. Diff mà checker hoặc gate nhận làm evidence là diff giữa base revision đã commit của run và `InputTree`, tức đúng
   phần thay đổi chưa commit mà các node trước để lại.
5. Kiểm tra scope của MAKER không đổi: vẫn đo so với revision đã commit, theo `pathScopes`.
6. Port workspace thêm hai thao tác: `SnapshotTree(handle) → treeID` và `DiffTrees(handle, a, b) → []path`. Adapter
   `gitworktree` hiện thực bằng plumbing: `add -A` vào index tạm, `write-tree`, `diff-tree`.

**Hệ quả:**

- Workflow `maker → checker` và `maker → machine gate` chạy được trong một run; read-only vẫn fail-closed.
- Tree object không có ref nên có thể bị `git gc` dọn sau thời hạn prune. Điều này chấp nhận được vì tree chỉ được
  dùng trong vòng đời một NodeRun. Nếu tree object của `InputTree` không còn, attempt fail kỹ thuật với mã lỗi rõ ràng
  rồi đi retry/`FAILED` như lỗi kỹ thuật khác. Engine không chụp lại và không đoán; người vận hành chạy lại theo
  ADR-033.
- Tương thích ngược: trên worktree sạch, `InputTree` bằng tree của `HEAD`, nên kết quả trùng với cách kiểm cũ.

## 33. ADR-031 — Outcome cho kết quả fail chức năng của COMMAND và MACHINE_GATE

**Bối cảnh:** COMMAND thoát mã khác 0 trả Attempt `FAILED` với `CodeExecutionFailed` và **không** persist evidence
(`internal/app/runtime/command_node_executor.go`). MACHINE_GATE có verdict khác PASS thì persist evidence, nhưng
Attempt cũng `FAILED` (`gate_node_executor.go`). Sau khi hết lượt retry, NodeRun `FAILED` làm cả run `FAILED`.
Outcome của COMMAND/MACHINE_GATE hiện chỉ được suy ra khi node có đúng một outcome (`resolveSelectedOutcome`). Vì
vậy không có cách khai báo cạnh "kiểm tra fail → quay lại maker" có giới hạn (HE-09-M07, HE-14-M03), và lần fail
không để lại gì cho maker đọc (HE-09-M03, HE-09-M08). Đây là G2 trong `15-doi-chieu-v9.md`. ADR-026 chỉ giới hạn
`ROUTER`, không nói gì về node kiểm tra.

**Quyết định:**

1. `CommandNodeConfig` và `MachineGateNodeConfig` có thêm trường tùy chọn `failureOutcome`.
2. Khi khai `failureOutcome`, các outcome mà kết quả kiểm tra chọn được, tức mọi outcome đã khai trừ
   `escalationOutcome` của `cyclePolicy` trên chính node nếu có, phải là **đúng hai**: `failureOutcome` và một outcome
   thành công. Publish từ chối nếu khác. Quy tắc này giống cách AGENT loại `escalationOutcome` khỏi outcome do agent
   chọn. Edge từ `failureOutcome` tuân theo quy tắc vòng lặp hiện có: vòng phải có `cyclePolicy` trên một node bất kỳ
   trong vòng, với edge `escalationOutcome` dẫn ra ngoài vòng.
3. Kết quả của node kiểm tra được phân ba loại, và không loại nào được map sang loại khác:
   - **Thành công:** COMMAND thoát mã 0 và output không bị cắt; MACHINE_GATE verdict `PASS`. Node chọn outcome thành
     công, như hiện nay.
   - **Fail chức năng:** COMMAND tự kết thúc (không timeout, không bị kill) với mã khác 0, kể cả khi output bị cắt;
     khi đó evidence mang cờ "đã cắt". MACHINE_GATE `OverallVerdict = FAIL`.
     - Có `failureOutcome`: Attempt `SUCCEEDED`, NodeRun `SUCCEEDED` với outcome `failureOutcome`, evidence verdict
       `FAILED`/`FAIL`. Không retry theo ATTEMPT policy, vì đây là kết quả xác định.
     - Không có `failureOutcome`: giữ hành vi hiện nay (Attempt `FAILED` → retry → NodeRun `FAILED`).
   - **Lỗi kỹ thuật:** timeout, tiến trình bị kill, không spawn được, thoát mã 0 nhưng output bị cắt (không tin được
     là thành công), vi phạm scope, mất lease,
     MACHINE_GATE `ERROR`/`NOT_RUN`. Giữ nguyên hành vi hiện nay (retry, `FAILED` hoặc `INDETERMINATE`); **không bao
     giờ** map sang `failureOutcome`.
4. COMMAND **luôn** persist evidence `COMMAND_EXECUTION` khi tiến trình kết thúc, kể cả khi mã thoát khác 0. Evidence
   gồm argv, cwd/target, exit code, thời gian, revision, và artifact stdout/stderr đã redact, giới hạn theo
   `maxOutputBytes` và có cờ "đã cắt". Exit khác 0 có verdict `FAILED` (thêm hằng `EvidenceVerdictFailed`). Quy tắc
   này áp dụng cả khi node không khai `failureOutcome`, vì nó chỉ thêm dữ liệu, không đổi transition.
5. Completion giữ nguyên: chỉ xét evidence của lần kích hoạt mới nhất của mỗi node
   (`gatherCompletionCandidateEvidence`), và chỉ verdict `SUCCEEDED`/`PASS`/`NOT_APPLICABLE` thỏa yêu cầu. Vòng
   "fail → sửa → pass" vì vậy hoàn thành bình thường; một node kết thúc bằng fail sẽ không thỏa completion.
6. ADR-026 không đổi: `ROUTER` vẫn một outcome. Định tuyến theo kết quả kiểm tra đi qua chính node kiểm tra.

**Hệ quả:** workflow vẽ được `build → test --failed--> build` với `cyclePolicy`. Maker ở vòng sau nhận evidence của
lần fail. Workflow đã publish không khai `failureOutcome` giữ nguyên transition.

## 34. ADR-032 — Instruction artifact schema v2: ưu tiên, outcome hợp lệ và thứ tự

**Bối cảnh:** instruction gửi agent hiện có dạng `{taskContract, messages, resources}`
(`internal/app/runtime/assemble_execution_request.go`). Mỗi resource chỉ có
`ownerVersionId/resourceKey/contentHash/content`, không có priority. Resource HARD_CONSTRAINT đứng sau toàn bộ
message, cuối prompt là REFERENCE, và prompt không nói outcome nào hợp lệ dù node nhiều outcome bắt buộc marker. Agent
vì vậy không phân biệt được luật bắt buộc với gợi ý (HE-04-M03 ở phía agent), luật quan trọng nằm giữa prompt
("lost in the middle" của lecture 04), và người vận hành phải tự chép danh sách outcome vào Skill (HE-14-M02,
HE-14-M07). Đây là G3. V5-08B0 khóa rằng cùng một snapshot phải cho cùng instruction và cùng hash.

**Quyết định:**

1. Thêm schema v2 cho instruction artifact, với thứ tự trường cố định:
   1. `schemaVersion: 2`;
   2. `hardConstraints[]`: các resource HARD_CONSTRAINT;
   3. `taskContract`: `workItemId`, `title`, `behavior`, `acceptanceCriteria`, `verificationSpec`, `riskLevel`,
      `allowedOutcomes[]`, và `outcomeProtocol` khi node có hơn một outcome;
   4. `checkFailures[]`: chỉ có khi maker được gửi lại qua `failureOutcome` của một node kiểm tra (ADR-031 quyết
      định 6); mỗi phần tử gồm node kiểm tra, evidence id và `what`/`why`/`fix`. Bỏ hẳn key khi rỗng. Đặt ngay sau
      `taskContract` vì đây là thông tin maker cần hành động trước tiên ở vòng sửa;
   5. `resources[]`: các resource còn lại theo thứ tự REQUIRED_PROCEDURE → GUIDANCE → REFERENCE, mỗi phần tử có
      `priority`;
   6. `messages[]`;
   7. `closingChecklist`: `resourceKey` của mọi HARD_CONSTRAINT và `allowedOutcomes`.
2. Schema được chọn theo ContextSnapshot. Snapshot tạo sau thay đổi này ghi `instructionSchemaVersion = 2` (migration
   mới). Snapshot không có trường đó được lắp theo v1 y như trước. Cùng một snapshot luôn cho cùng artifact và cùng
   hash. Version là một phần của `ManifestHash` của snapshot (bỏ qua khi không có, nên hash của snapshot v1 không
   đổi), và snapshot clone cho attempt retry/recovery giữ nguyên version của snapshot gốc.
3. `priority` lấy từ resource đã pin, vốn đã nằm trong content hash; `ResourceRef` không thêm trường.
4. `outcomeProtocol` là văn bản cố định do engine sở hữu, mô tả marker `<agentkit-outcome>`; cú pháp và parser marker
   không đổi. Marker sai (outcome ngoài `allowedOutcomes`, lặp, hoặc hỏng) là câu trả lời sai của agent, nên attempt
   kết thúc `OUTCOME_REJECTED` (mã `VALIDATION_FAILED`) giống trường hợp thiếu marker, không phải
   `PROVIDER_UNAVAILABLE`.
5. Provider adapter vẫn đưa JSON qua stdin; adapter chỉ gắn thêm lỗi có kiểu cho marker sai để engine phân loại đúng.

**Không làm:** chưa thêm mô tả tùy biến cho từng outcome; chưa đổi cú pháp marker. Hai việc này cần ADR riêng nếu có
nhu cầu.

**Bổ sung 2026-10-03 (V9-07, giữ nguyên các quyết định trên):** khi CONTEXT policy của node khai `messages`
(`{maxBytes, keepLatest}`) và có message bị loại khỏi prompt, artifact v2 có thêm `omittedMessages[]` ngay sau
`messages[]` (mục 1 giữa bước 6 và 7): mỗi phần tử gồm `messageId`, `sequence`, `actor`, `role`, `createdAt`, `reason`,
không có nội dung. Bỏ hẳn key khi không có message nào bị loại, nên artifact của snapshot không dùng budget này không
đổi byte nào. Danh sách message bị loại (`OmittedMessageRefs`, mỗi phần tử có `reason`) là một phần của
`ManifestHash` của snapshot theo cùng quy tắc "bỏ qua khi rỗng" của mục 2, và snapshot clone cho retry/recovery giữ
nguyên cả message được giữ lẫn message bị loại. Message được ghim (`pinned`, đặt khi append, không đổi) không bao giờ
bị loại.

**Bổ sung 2026-10-04 (V9-13b, giữ nguyên các quyết định trên):** dòng `fix` của `checkFailures[]` là văn bản do engine
sở hữu, không phải cấu trúc của schema. Lần chạy Claude CLI thật (V9-11) cho thấy lời cũ ("Address the cause…") khiến
model đọc lại file của mình, thấy khớp đề rồi tuyên bố xong, hoặc cãi rằng check mâu thuẫn với đề; lời mới nói check là
thẩm quyền cuối, phải làm theo điều `why` mô tả và không tranh luận. Chỉ artifact **v2** dùng lời mới. Artifact v1
(snapshot không có `instructionSchemaVersion`) giữ nguyên từng byte như mục 2 đã hứa, và test
`TestRenderInstructionV1_ByteIdenticalToTheArtifactBeforeV903` giữ điều đó. Không có hash nào của artifact đã render được
lưu hay kiểm, nên đổi lời không đổi hash nào của snapshot.

## 35. ADR-033 — Run thất bại mở blocker `RUN_FAILED` để chạy lại trên cùng WorkItem

**Bối cảnh:** `transitionRunToFailedTx` (`internal/app/runtime/completion.go`) chuyển run sang `FAILED` và phát
`RUN_FAILED` nhưng **không mở blocker**. WorkItem ở lại `ACTIVE`, trong khi `StartWorkflowRun` đòi `READY`
(`commands.go`). Đường duy nhất còn lại là hủy WorkItem rồi tạo WorkItem mới với contract gần như y hệt; lịch sử của
cùng một việc vì vậy bị tách ra nhiều WorkItem (HE-08-M02, HE-08-M05; G6). Ngược lại, run bị hủy đã mở blocker
`RUN_CANCELLED`, và `ResolveWorkItemBlocker` với mode `RESOLVED` đưa WorkItem về `READY`
(`resolve_work_item_blocker.go`), nên chạy lại được trên cùng WorkItem.

**Quyết định:**

1. Thêm `BlockerType` `RUN_FAILED`. `transitionRunToFailedTx` mở blocker này trong cùng transaction, trừ khi run thuộc
   một WorkItem đang bị hủy (cùng quy tắc với `openRunCancelledBlockerTx`). Mở blocker chuyển WorkItem
   `ACTIVE → BLOCKED` như mọi blocker khác.
2. `RUN_FAILED` resolve được qua command (`RESOLVED`) nhưng **không** waivable. Muốn bỏ việc thì dùng `CancelWorkItem`.
3. Resolve dùng đúng `ResolveWorkItemBlocker` hiện có, với các precondition giữ nguyên: không có run non-terminal và
   không có workspace `QUARANTINED`. Sau đó WorkItem về `READY`.
4. Run mới dùng đúng `workflowVersionId` đã pin trong contract. Không repin, cùng nguyên tắc với
   `RetryBlockedActivation`. Đổi workflow version hoặc contract vẫn phải tạo WorkItem mới.
5. Message và evidence của run trước giữ nguyên trên WorkItem. Message tiếp tục đi vào context của run mới, theo giới
   hạn của V9-07 nếu policy khai. Completion chỉ xét evidence của run hiện tại.
6. Worktree giữ nguyên trạng thái mà run trước để lại; người vận hành có thể hoàn tác trước khi resolve.

**Hệ quả:** vòng làm việc khi fail là `aw blocker resolve <id> --mode RESOLVED --reason …` rồi `aw run start`, trên
cùng WorkItem. Kanban hiện WorkItem ở cột BLOCKED thay vì kẹt ở ACTIVE. Không cần trạng thái WorkItem mới.

## 36. ADR-034 — Commit ngoài aw: chẩn đoán có kiểu, lệnh nhận có audit, chặn commit và run trên HEAD lệch

**Bối cảnh:** người dùng tự `git commit` (IDE, terminal) trong worktree do aw quản lý làm HEAD thật lệch khỏi
`RepositoryWorkspace.CurrentRevision`, thứ duy nhất aw ghi. Ca tái hiện trong `docs/spikes/v11-01-external-commit/`:
một `MACHINE_GATE` sau commit tay thất bại `VALIDATION_FAILED` không nêu nguyên nhân (`staleMountRevision` so HEAD
với revision đã ghim và trả mã trần); `commit-task.sh` báo "không có gì để commit" vì commit tay đã lấy hết thay đổi;
và `CreateLocalCommit` chạy trên HEAD lệch sẽ đưa các commit ngoài vào lịch sử mà aw coi là của mình, không qua
cổng nào. `CurrentRevision` chỉ tiến lên bằng local commit của ReleaseSet (V9-16); không đường nào để aw biết commit
ngoài.

**Quyết định:**

1. **Chẩn đoán có kiểu.** Khi `MACHINE_GATE` thấy HEAD lệch revision đã ghim, attempt vẫn thất bại fail-closed với
   `VALIDATION_FAILED`, kèm một sự kiện `DIAGNOSTIC` mã `WORKSPACE_HEAD_DRIFT` ghi vào `agent_events` của attempt
   (cùng đường V9-09 dùng cho danh sách vi phạm scope): repository, revision đã ghim, HEAD thật, các commit nằm ngoài
   aw (hash, tác giả, tiêu đề, giới hạn 5 commit mới nhất) và cách xử lý. `GetRunTimeline` đọc nó thành
   `failureDetail` của attempt, nên `aw run timeline`, `show-run.sh` và UI hiện nó. Không thêm mã lỗi vào enum đóng
   của go-core-spec §18; tên kiểu là mã của diagnostic. Việc đọc Git đi qua port hẹp mới
   `ports.WorkspaceHeadAuditor` (`gitworktree.Provider` thỏa bằng structural typing); provider không kiểm được thì
   diagnostic chỉ nói HEAD khác.
2. **Lệnh nhận commit ngoài, tường minh và có audit:** `AdoptWorkspaceHead` — CLI `aw repository-workspace adopt-head
   --project-id P --expected-version N [--idempotency-key K] <repositoryWorkspaceId>`, HTTP `POST
   /projects/{projectId}/repository-workspaces/{id}/adopt-head` (If-Match theo version của workspace, operationId
   `adoptWorkspaceHead`). Đồng bộ, không có job nền: đọc Git ngoài transaction, một transaction ghi ngắn kiểm lại mọi
   hàng rào. Chỉ chạy khi workspace `READY` (`QUARANTINED` bị từ chối với `WORKSPACE_QUARANTINED`), không attempt nào
   giữ write lease, worktree sạch, HEAD là hậu duệ tuyến tính của `CurrentRevision` và khác nó. Khi đó ghi
   `CurrentRevision = HEAD`, `version + 1`, sự kiện `WorkspaceHeadAdopted` (commit, tác giả, người thao tác) và biên nhận
   idempotent. HEAD sau `reset`/`rebase` bị từ chối, hướng dẫn đưa worktree về revision đã ghi hoặc reconcile. Nhận
   commit ngoài là việc của người vận hành, không bao giờ tự động; gate của các run sau vẫn chạy trên mã đó.
3. **Run đang chờ duyệt không đổi pin.** `ExecutionManifest` bất biến (ADR-011) và `run_manifest_amendments` chỉ ghi
   phiên bản scope đã duyệt, không ghi revision; mở rộng nó để cập nhật revision cần migration và đổi ngữ nghĩa
   ghim. Vì vậy sau `adopt-head`, run đã bắt đầu giữ revision cũ, và gate của nó vẫn báo `WORKSPACE_HEAD_DRIFT`; diagnostic
   nói rõ run phải được hủy và bắt đầu lại.
4. **Run mới kiểm trước, mặc định từ chối.** `POST /work-items/{id}/runs` và `aw run start` gọi
   `CheckFamilyWorkspaceHeads` trước `StartWorkflowRun`: mỗi repository có HEAD khác `CurrentRevision` thì từ chối
   (HTTP 409, mã `CONFLICT`, thông báo `WORKSPACE_HEAD_DRIFT: …` nêu repository, hai revision, số commit, tác giả commit mới
   nhất và đúng lệnh `adopt-head` kèm `--expected-version`). Không có cờ tự nhận. `aw run start` kiểm khi có
   `--workspace-root`/`AW_WORKSPACE_ROOT`; không có thì chạy như trước. Worktree không đọc được thì bỏ qua repository đó
   (best effort): `StartWorkflowRun` và gate vẫn giữ kiểm tra của chúng.
5. **Local commit chặn trên HEAD lệch.** `ExecuteReleaseSetLocalCommit` so HEAD với `CurrentRevision` trước khi tạo commit;
   lệch thì operation kết thúc `FAILED` lý do `HEAD_DRIFT` (migration 0049 mở rộng CHECK của
   `release_set_local_commits.failure_reason`), không `git commit`, job hoàn tất (xác định, không retry). Lý do là typed
   reason của operation như `NO_CHANGES` (ADR đã dùng ở V9-09), không phải mã lỗi mới.

**Hệ quả:** commit ngoài không còn gây hỏng khó hiểu: lỗi nói nguyên nhân và lệnh sửa; không đường nào đưa commit
ngoài vào lịch sử aw mà không có lệnh tường minh và sự kiện audit. Cạnh bất biến cần biết: một lệnh `adopt-head`
chạy khi một local commit của chính aw đang giữa chừng có thể nhận commit của aw; `Advance` của local commit khi đó
thất bại version fence và job retry theo marker. Không đổi hành vi cho workflow không bị commit ngoài.

## 37. ADR-035 — Hết hạn mức của nhà cung cấp là `PROVIDER_QUOTA_EXCEEDED`, không bao giờ retry

**Bối cảnh:** khi đo B0/B1 (2026-10-07), CLI Claude trả "You've hit your session limit · resets 5:30pm (UTC)". Adapter ghi
diagnostic `PROVIDER_REPORTED_FAILURE` (V9-20) rồi attempt kết thúc `FAILED` / `EXECUTION_FAILED`, và attempt policy retry
mã này: ở mọi node agent chạy song song, hai lần thử cháy hết trong vài giây rồi run `FAILED`. Hạn mức reset sau vài giờ, nên
thử lại ngay không bao giờ có ích. Cả hai CLI chỉ báo lý do bằng văn bản (`result` của Claude, `error.message` của
Codex); không có mã hay thời điểm reset đọc được bằng máy.

**Quyết định:**

1. Thêm mã lỗi `PROVIDER_QUOTA_EXCEEDED` vào tập đóng của go-core-spec §18 (22 → 23) và vào `neverRetryable` cùng
   `ISOLATION_ENFORCEMENT_UNAVAILABLE` và `INDETERMINATE`: publish policy liệt kê nó trong `retryableErrorCodes` bị từ chối,
   và `decideRetryOrExhaustion` không bao giờ thử lại nó.
2. Adapter nhận diện từ lời của CLI (`ports.IsProviderQuotaMessage`: "hit your limit", "session limit", "usage limit",
   "rate limit", "quota", "too many requests", "credit balance"…; không khớp "context limit" hay "output token limit") và báo
   `AgentExecutionResult.TerminationReason = "provider_quota_exceeded"`, **trước** kiểm tra mã thoát của tiến trình (CLI có
   thể thoát mã khác 0 sau kết quả lỗi). `AgentNodeExecutor` ánh xạ nó sang attempt `FAILED` /
   `EXECUTION_FAILED` (termination) với mã `PROVIDER_QUOTA_EXCEEDED`. Lỗi không phải hạn mức vẫn là `EXECUTION_FAILED`.
3. Lời của CLI, gồm giờ reset, vẫn nằm trong diagnostic `PROVIDER_REPORTED_FAILURE` và hiện ở `failureDetail` của timeline
   (V9-20); `show-run.sh` in thêm một dòng hướng dẫn.
4. **Dừng có thể tiếp tục = đường sẵn có.** Attempt đầu kết thúc node, run `FAILED` mở blocker `RUN_FAILED` (ADR-033), và
   người vận hành chạy lại trên cùng WorkItem sau giờ reset (`retry-task.sh`). Không thêm loại blocker hay trạng thái `BLOCKED`
   mới: thiết kế V11 đề xuất blocker `retry-blocked` sau giờ reset, nhưng giờ reset không đọc được bằng máy nên aw không có thời
   điểm để hẹn, và `retry-blocked` dành cho admission chặn trước khi attempt chạy, không cho attempt đã chạy giữa chừng.

**Hệ quả:** hạn mức không còn đốt ngân sách retry; mã phân biệt hạn mức với lỗi thực thi thường ở timeline, Kanban và event
`NODE_RUN_FAILED`. Không đổi hành vi cho lỗi khác. Giới hạn: phân loại theo văn bản của CLI, nên một câu báo hạn mức mà CLI diễn
đạt khác sẽ vẫn là `EXECUTION_FAILED` cho đến khi thêm cụm từ.

## 38. ADR-036 — Cạnh escalation của mọi node có `cyclePolicy` phải rời vòng; lỗi định nghĩa làm run thất bại có lý do

**Bối cảnh:** `wf-task-delivery-plus` có node `simplify` với `cyclePolicy` và cạnh `escalated → quality`, mà `quality` nằm trong
cùng vòng (`quality` quay lại `build`). Compiler (`validateBoundedCycles`) chỉ đòi **một node nào đó** của thành phần liên thông
có `cyclePolicy` và cạnh escalation rời vòng (ở đây là `build`), nên định nghĩa được publish. Khi `simplify` hết ngân sách,
`advanceRunTx` từ chối cạnh (`ErrCycleEscalationNotBounded`) **bên trong** transaction của `FinalizeExecutionAttempt`, nên cả
finalize của node đứng trước bị rollback: attempt đó vẫn `RUNNING` mất lease, run đứng ở `RUNNING`, và 30 giây sau recovery reaper
đánh dấu attempt `INDETERMINATE (OWNERSHIP_LOST_MUTATING)`, không có thông báo nào.

**Quyết định:**

1. **Compiler kiểm theo từng node.** `validateBoundedCycles` giữ luật cũ (vòng phải có ít nhất một node có ngân sách và lối ra),
   thêm: mỗi node nằm trên một vòng và có `cyclePolicy.maxIterations > 0` phải có cạnh `escalationOutcome` dẫn **ra ngoài** vòng
   của nó. Thông báo nêu node, cạnh, đích và vòng.
2. **Version đã lưu vẫn đọc được.** Persistence nạp version bằng cách dựng lại qua compiler, nên luật mới sẽ làm mọi run, đồ thị
   và lịch sử của version cũ không đọc được. Thêm `workflow.Rehydrate` (dùng ở loader của sqlite): mọi kiểm tra của `Compile`
   **trừ** luật mới. Luật chỉ siết việc **publish**.
3. **Lỗi lúc chạy phải rõ.** `advanceRunTx` kiểm trước khi ghi bất cứ thứ gì (`checkCycleEscalationRoutable`) và trả
   `*RoutingDefectError` (`ErrWorkflowRoutingDefect`) khi node sắp được kích hoạt hết ngân sách mà cạnh escalation thiếu hoặc
   không rời vòng. `FinalizeExecutionAttempt` bắt lỗi này: attempt và NodeRun của node vừa xong được ghi `SUCCEEDED`
   (việc đã làm xong thật), rồi khi không còn node nào sống hoặc bị chặn thì run `FAILED` với lý do
   `WORKFLOW_DEFINITION_DEFECT: …` (hiện ở blocker `RUN_FAILED` và `aw run diagnostics`). Còn nhánh khác đang chạy thì việc
   đóng run để cho reconcile thông thường. Các đường khác gọi `advanceRunTx` (duyệt, chờ tín hiệu) vẫn trả lỗi cho người gọi,
   tức đã thấy được, không treo.

**Hệ quả:** định nghĩa như `simplify → quality` bị từ chối lúc publish với lý do nêu tên; version cũ đã publish không còn làm run
treo âm thầm. Workflow mẫu của kit không đổi (mọi cạnh escalation đã rời vòng).

## 39. ADR-037 — `checkFailures` đi qua CHECKER chẩn đoán nằm giữa kiểm tra và maker

**Bối cảnh:** ADR-031 quyết định 6 cho MAKER vừa bị `failureOutcome` của một node kiểm tra gửi lại nhận `checkFailures`, và V9-17 cho MAKER bị
CHECKER gửi lại nhận `reviewerFeedback`. Trong `wf-task-delivery-plus`, `gate1 (failed) → debug (CHECKER) → build` rơi vào khoảng trống giữa hai
đường: prompt của `debug` không có `checkFailures` (assembler bỏ qua mọi CHECKER), và prompt của `build` sau đó chỉ có báo cáo của `debug`.
Agent chẩn đoán phải tự tái hiện lỗi mà kit đang dặn, và maker không thấy lại đầu ra của chính kiểm tra đã gửi nó đi.

**Quyết định:** đưa `checkFailures` đi tiếp, chỉ dọc theo cạnh do kiểm tra thất bại kích hoạt.

1. Một CHECKER AGENT được kích hoạt bằng cạnh `failureOutcome` của một node kiểm tra nhận `checkFailures` của kiểm tra đó, cùng nhãn
   WHAT/WHY/FIX, cùng giới hạn 4 KiB, cùng ghi chú baseline. Chỉ kiểm tra đã kích hoạt NodeRun này được tính (`activatingFailedCheck`),
   không phải mọi bằng chứng mà snapshot của CHECKER ghim cho allowlist đầu vào của V5-12. Snapshot của CHECKER không đổi (nó đã ghim
   evidence của node đứng trước), nên không hash nào đổi; chỉ phần dựng prompt đổi.
2. Một MAKER được kích hoạt bằng cạnh của một CHECKER mà chính CHECKER đó được kích hoạt bằng cạnh `failureOutcome` của một kiểm tra nhận thêm evidence của
   kiểm tra gốc (`gatherCheckFailureEvidenceRefs` nhìn xuyên một CHECKER). Nó có cả `reviewerFeedback` (chẩn đoán) lẫn `checkFailures` (kiểm
   tra gốc). Chẩn đoán giải thích lỗi, không thay thế kiểm tra, vốn vẫn là thẩm quyền cuối (ADR-032, V9-13b). Snapshot của maker này có
   thêm evidence ref nên hash của nó khác trước, chỉ cho kích hoạt này.
3. Một CHECKER không đi qua kiểm tra thất bại (reviewer sau kiểm tra đạt), và maker mà nó gửi lại, không có `checkFailures`; prompt của họ không đổi.
   Chỉ đi xuyên một CHECKER, không đệ quy xa hơn.

**Hệ quả:** workflow mẫu `wf-task-delivery-plus` không cần sửa; agent `debug` đọc được lỗi thật của `gate1` thay vì tái hiện nó, và kit có thể
rút gọn dặn dò "tự tái hiện" khi cần. Hành vi prompt của workflow không có CHECKER nằm giữa không đổi.

## 40. ADR-038 — Cô lập cấu hình của CLI Claude bằng `--safe-mode` (tùy chọn của worker)

**Bối cảnh:** spike V10-17 đo (`docs/spikes/v10-17-claude-skills/README.md`) rằng aw gọi `claude -p …` không chọn nguồn cấu hình, nên CLI nạp
`~/.claude` của máy chạy worker và `.claude/` cùng `CLAUDE.md` của repository: skill, agent, slash command, hook, MCP server nằm **ngoài** ContextSnapshot
(không băm, không audit, không tái hiện); hook ghi file vào worktree làm attempt MAKER hỏng `SCOPE_VIOLATION`; CHECKER cũng trả tiền cho danh sách
skill. Thiết kế V11 đề xuất hai hướng: cô lập cấu hình, hoặc ghi băm cây `.claude/` vào manifest, sau khi kiểm cờ của CLI.

**Quyết định:**

1. Làm hướng cô lập, bằng cờ `--safe-mode` của CLI (không phải `CLAUDE_CONFIG_DIR`: đổi thư mục cấu hình mất thông tin đăng nhập; không phải `--bare`: nó
   không đọc OAuth/keychain nên hỏng với người dùng đăng nhập bằng tài khoản). Kiểm với Claude Code 2.1.288 bằng sự kiện `system init`, không gọi API: có skill
   cấp người dùng, skill của repository và hook `SessionStart` thì không cờ liệt kê cả hai skill và hook chạy (2 sự kiện); có `--safe-mode` thì không skill nào
   trong hai và 0 sự kiện hook. Đăng nhập và model dùng bình thường.
2. Tùy chọn của worker `aw worker --claude-safe-mode` (mặc định tắt), trường `claude.Config.SafeMode`: `--safe-mode` có trên **mọi** lần gọi CLI, start và resume, giống
   effort và ngân sách (V9-13a). Mặc định tắt vì nó đổi cái CLI thấy (mất `~/.claude` và `CLAUDE.md` của repository) và cần CLI biết cờ này.
3. Không làm hướng băm `.claude/`: với `--safe-mode` bật, không có tri thức ngoài manifest để băm; còn khi tắt, hành vi cũ giữ nguyên (nạp, không kiểm). Không đổi
   phép kiểm scope: hook không còn chạy nên không còn ghi vào worktree.
4. Không đổi AdapterBuild: cờ là cấu hình của worker như `--permission-mode`, không thuộc định nghĩa hay snapshot.

**Hệ quả:** cài đặt muốn tái hiện được dùng `--claude-safe-mode` và đưa quy ước vào Layer/Skill của kit (đi qua ContextSnapshot). Cảnh báo kích thước file hướng dẫn
(`--instruction-file-warn-bytes`, V9-10) vẫn ghi file mà CLI sẽ nạp; dưới `--safe-mode` CLI không nạp chúng nên cảnh báo chỉ còn tính chất thông tin. Codex chưa có cờ tương đương.

## 41. ADR-039 — Diff của working tree là chế độ `result=WORKING_TREE` của route diff hiện có, chỉ đọc

**Bối cảnh:** ở cổng `APPROVAL` agent đã ghi file vào worktree nhưng chưa commit, nên `currentRevision` chưa tiến. API diff chỉ so hai commit
(`git diff <base> <result>`), tab Diff hiện "No changes between the base and current revision" và người duyệt quyết định mà không thấy agent đã sửa gì.
Xem thay đổi bằng terminal lại là thao tác Git ngoài aw, chính là lỗi của V11-01.

**Quyết định:**

1. Không thêm route mới: `GET .../repository-workspaces/{id}/diff` nhận `result=WORKING_TREE` (thay cho revision đích). Giữ 1:1 giữa parity registry, CLI
   (`aw repository-workspace diff --working-tree`), contract và UI; phản hồi thêm `uncommitted`, `totalFiles`, `changeSetDigest`, và mỗi file có `status`,
   `untracked`, `symlink`, `sizeBytes`, `contentHash`. Diff giữa hai commit không có các trường này (test khẳng định).
2. Chỉ đọc, kể cả với repository đang dùng: `GIT_OPTIONAL_LOCKS=0` cho mọi lệnh git của adapter (cả đọc commit), không `git add`/`reset`, không ghi index,
   HEAD không đổi. Tracked đọc bằng `git diff <base> --`, untracked bằng `git ls-files --others --exclude-standard` rồi `git diff --no-index` với file rỗng,
   từng file, có giới hạn byte/file/dòng của `ReadDiff`. Không đi theo symlink ra ngoài worktree; file nhị phân chỉ trả tên và kích thước; bỏ `.git`.
3. Từ chối khi có attempt ghi (`409`, giữ lease ghi): worktree đang đổi thì kết quả nửa chừng còn tệ hơn không có kết quả. Quá 20000 đường dẫn đổi thì `422`
   thay vì mô tả sai.
4. `base` vẫn phải là revision đã được ủy quyền của workspace (cùng `authorizeRevision` như diff giữa hai commit).
5. `contentHash` từng file và `changeSetDigest` là đầu vào cho V11-07 (commit đúng cái đã duyệt); V11-06 chỉ tính và trả, không đối chiếu.
6. UI: mặc định tab Workspace hiện thay đổi chưa commit khi worktree có thay đổi, nếu không thì hiện diff commit và nói rõ là "No committed changes";
   thẻ duyệt ở tab Graph có dòng tóm tắt và nút mở tab Workspace; lỗi đọc (vd 409) hiện lý do thay vì "không có thay đổi".

**Hệ quả:** cổng duyệt có cái để quyết định. Không có thao tác ghi nào từ UI (stage, sửa) — nằm ngoài V11-06; commit đúng cái đã duyệt là V11-07.

## 42. ADR-040 — Duyệt và commit là một thao tác: ý định commit nằm trong transaction của quyết định, Git chạy ở job nền

**Bối cảnh:** ADR-014 cho phép local commit "khi workflow policy cho phép hoặc operator phát typed command", nhưng chỉ có đường thứ hai, và nó gom MỌI thay đổi
(`git add -A`). Hệ quả khi chạy issue-tracker: người duyệt phải rời web để chạy `commit-task.sh`; quên bước này thì task sau bị `SCOPE_VIOLATION` mà thông báo không nói
nguyên nhân; không commit một phần được; worktree thuộc về family chứ không phải một task nên commit đóng dấu cả phần việc chưa duyệt của task khác. §11.1 của
go-core-spec cấm gọi Git trong transaction nên không thể gọi `git commit` ngay trong `ResolveApproval`.

**Quyết định:**

1. Node APPROVAL có `approval.release {commit: DISABLED|OFFERED|REQUIRED, outcomes}`; vắng = DISABLED (định nghĩa cũ không đổi hash, không đổi hành vi). Compiler chỉ cho
   outcome mà đường đi từ nó chỉ tới `END` hoặc node không ghi worktree (AGENT khác CHECKER, và COMMAND, bị coi là ghi), vì commit chạy nền sẽ đua với node ghi.
   Kit chỉ bật `OFFERED`; `REQUIRED` với tập thay đổi rỗng được ghi `NOTHING_TO_COMMIT`, không phải lỗi.
2. `ResolveApproval` nhận `commit`. Trong **cùng transaction** với quyết định: kiểm policy và danh tính tác giả TRƯỚC khi ghi gì (bị từ chối thì cổng vẫn PENDING), ghi quyết
   định, rồi tạo ReleaseSet (base = result = revision đã ghi, PASS), seal, và một `ReleaseSetLocalCommit` REQUESTED + job + selection cho từng repository. Lỗi ở bất cứ bước nào
   thì quyết định cũng rollback; mất race thì không commit; replay trả lại đúng các operation. Quyền commit = quyền duyệt (`authorizedRoles`); không có role release riêng.
3. **Tác giả lấy từ `git config` của máy chạy aw** (cấu hình repository đè toàn cục), đọc NGOÀI transaction (`ports.GitIdentityReader`) và truyền vào lệnh như giá trị; job dùng
   đúng giá trị đã lưu, không đọc lại. Không có ô nhập tay. Thiếu thì từ chối cả lệnh bằng `COMMIT_IDENTITY_MISSING` (nằm trong `details` của lỗi 422, không thêm mã vào enum lỗi
   đóng của §18) kèm lệnh cấu hình.
4. **Chọn theo file, và duyệt đúng cái đã thấy.** Request mang `{path, status, contentHash}` của các file được chọn; adapter tính lại dưới write lease và từ chối
   (`CHANGESET_DRIFT`, determinate, không commit gì) nếu khác. Staging bằng `git --literal-pathspecs add -A --pathspec-from-file=- --pathspec-file-nul` sau `git reset -q`
   (chỉ index), đọc lại index và cây commit để bảo đảm đúng bằng tập đã chọn. Tập thay đổi (`collectChangeSet`) là MỘT định nghĩa dùng chung với diff của V11-06.
5. **File không chọn: `KEEP` (mặc định) hoặc `PARK`.** PARK ghi phần còn lại thành commit dưới `refs/agentkit/parked/<releaseSetId>` bằng index tạm rồi dọn đúng các đường dẫn đó;
   idempotent và chạy cả trên commit tái dùng sau crash (trả lại danh sách đã cất nếu ref đã có). Khôi phục từ chối ghi đè mọi thứ đã đổi và từ chối khi có attempt ghi. Không có
   "xóa hẳn" (muốn bỏ thì PARK).
6. **Commit lỗi sau quyết định không rollback quyết định**; nó đóng operation `FAILED` (+ `CHANGESET_DRIFT` mới) và mở blocker `RELEASE_FAILED` (RESOLVED-only) trên WorkItem trong
   cùng transaction (hook `OnFailed`, nối ở worker để `releasesetcommit` không import `runtime`). "Thử commit lại" là commit mới từ tập thay đổi hiện tại (đường thủ công có `--path`),
   rồi resolve blocker. Số đếm "attempt ordinal" của marker (V9-09) tính cả HEAD_DRIFT và CHANGESET_DRIFT vì cả hai đóng trước `git commit`.
7. Bằng chứng của cái đã duyệt: file + hash + digest + patch working tree (có giới hạn 1 MiB) lưu trong `release_set_local_commit_selections` (migration 0050), đọc lại bằng
   `getApprovalRelease`. Route mới: `getApprovalRelease`, `getFamilyReleaseContext`, `restoreParkedChanges`; CLI tương ứng; `getRunDetail` báo `release` của node.

**Hệ quả:** quy trình mặc định của kit không còn cần `commit-task.sh` ở cổng duyệt của workflow kết thúc ở `END`; workflow có node ghi sau `approved` (delivery `sync`) vẫn commit thủ công.
Không push/PR/merge (ADR-014 giữ nguyên). Chưa có: chọn file cho commit thủ công trên UI (backend/CLI/HTTP có), ghép cặp đổi tên (đổi tên là một xóa + một thêm, chọn độc lập), patch lọc riêng
từng file đã chọn (lưu patch cả working tree lúc duyệt; file + hash là bằng chứng chính xác phần được chọn).

## 43. Baseline sau review thiết kế

ADR-001…040 là baseline hiện hành. Các mục ADR-001…010 giữ lịch sử quyết định ban đầu; khi đọc phải áp
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
  runtime-only-reject phát hiện lúc review V4-03;
- ADR-027 thêm `RuntimeExecutionConfigProvider` là port bắt buộc cho V4-04's scheduling transaction,
  chưa được ADR cũ khóa — resolve config ngoài Tx, core tự tính hash, không nhận hash trần từ caller.
- ADR-028 đổi executable sản phẩm canonical thành `aw`, khóa UI/API/CLI parity trên cùng public
  application authority ở gate cuối V6, thêm named readiness action thay generic status setter và
  refine scope của definition global/project; `agentkit-spike` vẫn là binary regression riêng của V0.
- ADR-029 chọn framework/toolchain UI cho V7 Alpha (React 19 + Vite 8) bằng evidence từ prototype thật,
  chưa được ADR cũ khóa — không đổi authority/contract nào đã chốt ở ADR-001…028, chỉ khóa lựa chọn kỹ
  thuật phía client mở đầu V7.

- ADR-030 refine read-only của CHECKER/MACHINE_GATE (V5-12): đo so với `InputTree` lúc attempt bắt đầu thay vì
  revision đã commit, để checker/gate đặt được sau maker trong cùng run; scope của MAKER không đổi.
- ADR-031 bổ sung outcome `failureOutcome` cho fail chức năng của COMMAND/MACHINE_GATE và evidence bắt buộc khi
  COMMAND thoát mã khác 0; không đổi ADR-026 (`ROUTER` vẫn một outcome) và không đổi completion.
- ADR-032 thêm instruction artifact schema v2 (priority, outcome hợp lệ, thứ tự đầu/cuối), chọn theo
  ContextSnapshot nên snapshot cũ vẫn lắp ra đúng artifact/hash cũ. V9-07 bổ sung `omittedMessages[]` cho message bị
  ngân sách `messages` của CONTEXT policy loại (ghi trong ContextSnapshot, bỏ qua khi rỗng).
- ADR-033 thêm blocker `RUN_FAILED` để run thất bại đi qua đúng đường `ResolveWorkItemBlocker` sẵn có và chạy lại
  trên cùng WorkItem, không repin.
- ADR-034 (V11-01) thêm lệnh `AdoptWorkspaceHead`, chẩn đoán `WORKSPACE_HEAD_DRIFT`, kiểm HEAD trước khi start run và lý do
  `HEAD_DRIFT` của local commit; không đổi ADR-014 (vẫn không push/PR/merge) và không đổi ADR-011 (manifest bất biến).
- ADR-035 (V11-02) thêm mã lỗi `PROVIDER_QUOTA_EXCEEDED` (tập mã §18: 22 → 23), không bao giờ retry; dừng có thể tiếp tục
  qua `RUN_FAILED` của ADR-033, không thêm blocker mới.
- ADR-036 (V11-03) siết luật vòng có chặn theo từng node (chỉ lúc publish, `workflow.Rehydrate` giữ version cũ đọc được) và
  làm lỗi định tuyến do định nghĩa thành run `FAILED` có lý do `WORKFLOW_DEFINITION_DEFECT`.
- ADR-037 (V11-04) bổ sung ADR-031 quyết định 6 và V9-17: `checkFailures` đi tiếp qua CHECKER chẩn đoán nằm giữa kiểm tra và maker.
- ADR-038 (V11-05) thêm tùy chọn worker `--claude-safe-mode` (CLI Claude chạy `--safe-mode`, không nạp cấu hình người dùng và repository); mặc định tắt.
- ADR-039 (V11-06) thêm chế độ `result=WORKING_TREE` cho route diff của workspace (thay đổi chưa commit, chỉ đọc, từ chối khi attempt đang ghi).
- ADR-040 (V11-07) hoàn thiện ADR-014: quyết định duyệt kèm yêu cầu commit chọn file trong cùng transaction (policy `approval.release`, danh tính từ git config, drift theo hash, KEEP/PARK, blocker `RELEASE_FAILED`).

Thay đổi semantics tiếp theo vẫn cần ADR mới; không sửa âm thầm lịch sử quyết định.
