# Kiến trúc hệ thống Agent Kit

> Trạng thái: BASELINE ĐÃ QUYẾT ĐỊNH — đồng bộ ADR-001 đến ADR-028; là đầu vào bắt buộc cho Go core
> spec, spike và system design.
>
> Phạm vi: alpha modular monolith chạy local và beta control plane/worker chạy trên server.

Tài liệu liên quan:

- [Start here / handoff hiện tại](../00-start-here.md)
- [Mô hình Project, Repository, TaskFamily và WorkspaceSet](01-project-repository-workspace-model.md)
- [Architecture decisions cho Agent Kit](02-architecture-decisions.md)
- [Bộ tiêu chí Harness Engineering](../harness-engineering/00-tong-quan.md)
- [Kinh nghiệm từ claude-workflow](../danh-gia-claude-workflow.md)

## 1. Mục đích và phạm vi

Kiến trúc này xác định các ranh giới ổn định để Agent Kit có thể:

- author và publish workflow dưới dạng graph có version;
- điều phối WorkItem qua node, attempt, approval, retry và rework;
- chạy agent CLI, command và gate trong workspace được kiểm soát;
- quản lý Project nhiều repository và TaskFamily dùng chung WorkspaceSet;
- sống qua process crash, provider session loss và worker replacement;
- chứng minh completion bằng evidence thay vì lời tự khai của agent;
- giữ nguyên domain semantics khi chuyển từ SQLite local sang PostgreSQL/server worker.

Tài liệu không chọn UI framework, không định nghĩa cú pháp workflow file, không thiết kế chi tiết
schema/API và không chọn công nghệ sandbox cụ thể. Những nội dung đó thuộc spec hoặc design record
sau spike.

## 2. Architecture drivers

Các driver chi phối mọi lựa chọn thành phần:

1. **Durability trước automation:** trạng thái có thể phục hồi quan trọng hơn việc chạy nhanh nhưng
   chỉ tồn tại trong memory hoặc provider session.
2. **Evidence trước completion:** engine chỉ kết luận dựa trên policy, checkpoint và evidence có
   provenance.
3. **Definition bất biến khi runtime:** run pin snapshot đã publish; authoring state không tác động
   ngầm đến run đang chạy.
4. **Project đa repository là trường hợp chuẩn:** repository identity và revision xuất hiện xuyên
   suốt workspace, context, command và evidence.
5. **Side effect nằm sau worker boundary:** UI và application service không trực tiếp spawn CLI,
   chạy Git hay command dự án.
6. **At-least-once có kiểm soát:** không giả định exactly-once cho process, filesystem, Git hoặc
   provider; outcome không chắc chắn phải được biểu diễn thành state.
7. **Provider và executable là adapter:** domain không phụ thuộc Claude, Codex, shell hay một hệ
   điều hành cụ thể.
8. **Alpha và beta dùng chung core contracts:** deployment topology và adapter thay đổi, domain
   invariant không đổi.
9. **Progressive disclosure:** context được resolve theo node/attempt và được lưu manifest, không
   nạp toàn bộ instruction/resource vào mọi session.
10. **Tách write model khỏi read model:** runtime state ra quyết định; Kanban và dashboard là
    projection có thể rebuild.

## 3. System context và trust boundary

```text
Operator
   |
   v
Agent Kit UI/CLI client
   |
   v
Application API / Control Plane
   |        |             |
   |        |             `---- Definition and artifact stores
   |        `------------------ Runtime database and projections
   `---- durable jobs ----> Worker Runtime
                              |      |        |
                              |      |        `---- Toolchains / commands / gates
                              |      `------------- Claude, Codex, future providers
                              `-------------------- Git repositories / worktrees
```

### Tác nhân và hệ thống ngoài

| Tác nhân/hệ thống | Tương tác được phép |
|---|---|
| Operator | Author/publish definition, tạo task, chat, approve, cancel, xem evidence; push/PR chỉ beta/future |
| UI/CLI client | Gọi application command/query; không truy cập database hoặc spawn executor trực tiếp |
| Git provider/local Git | Fetch, worktree, commit và operation được policy cho phép; remote mutation cần authority rõ |
| Agent provider | Nhận context/input qua adapter, phát event/outcome; không sở hữu canonical workflow state |
| Toolchain/service | Chạy sau executable policy và worker sandbox; output trở thành artifact/evidence |
| Identity provider | Chỉ beta; xác thực principal và organization/team claims |

Alpha đặt API, database, artifact store và worker trên máy cá nhân, nhưng vẫn giữ boundary logic.
Beta đưa control plane và worker vào các trust zone tách biệt: client không nhìn thấy database;
worker không mặc nhiên có mọi secret/project; tenant này không được truy cập workspace hoặc artifact
của tenant khác.

## 4. Hai mặt phẳng của hệ thống

### 4.1 Definition plane

Definition plane quản lý nội dung có thể author, validate, review và publish:

- workflow, block và typed edge;
- skill, layer/engineering pack và resource thụ động;
- agent profile, context policy và selector;
- executable, gate, permission, retry và completion policy;
- compatibility metadata cho provider, OS và toolchain.

Authoring object có thể mutable. Publish pipeline resolve dependency, validate graph và policy,
canonicalize nội dung, tính `SourceHash`, resolve exact dependency manifest rồi tính
`CompiledSnapshotHash` để tạo version bất biến. Hai source giống nhau nhưng resolve dependency khác
nhau là hai compiled snapshot khác nhau. Runtime chỉ tham chiếu version đã publish; không đọc trực
tiếp file authoring để quyết định execution.

### 4.2 Runtime plane

Runtime plane quản lý thực tế vận hành:

- Project, Repository, WorkItem, TaskFamily và WorkspaceSet;
- WorkflowRun, NodeRun, ExecutionAttempt và transition;
- job, lease, fencing token, checkpoint và recovery decision;
- RunManifestAmendment, ReleaseSet và local Git operation đã typed;
- conversation, message, context snapshot và provider session reference;
- command/gate execution, artifact, evidence và approval;
- event journal có JournalPosition, outbox và projection.

Chỉ runtime/application core được ghi state authoritative. Worker trả event, observation và proposed
outcome; orchestrator mới kiểm invariant và commit transition.

### 4.3 Giao điểm giữa hai mặt phẳng

Khi tạo run, hệ thống tạo một **Execution Manifest** pin tối thiểu:

- WorkflowVersion, `SourceHash` và `CompiledSnapshotHash`;
- exact dependency manifest;
- BlockVersion của các node liên quan;
- policy, agent profile, skill/layer/resource version đã resolve;
- executable version, immutable `AdapterBuildVersion` và capability yêu cầu;
- RepositoryScope, base RevisionSet và context selection policy.

Manifest là cầu nối audit giữa “đã định nghĩa gì” và “thực tế đã chạy gì”. Definition mới không
thay manifest cũ. Scope được phê duyệt thêm được ghi bằng `RunManifestAmendment` append-only, không
ghi đè manifest ban đầu. Runtime không copy quyền thực thi từ một resource chỉ vì resource đó chứa
script.

## 5. Logical component boundaries

### 5.1 Control plane

| Thành phần | Sở hữu | Không được làm |
|---|---|---|
| Application API | Command/query boundary, authentication context, idempotency key | Chạy CLI, Git hoặc command dự án |
| Work Management | WorkItem, quan hệ cha-con, TaskFamily, assignment và business state | Suy runtime state từ Kanban projection |
| Definition Registry | Authoring metadata, publish/validate, immutable versions và dependency resolution | Sửa version đã publish |
| Orchestrator | Run graph, NodeRun, transition, retry/rework/join, completion và escalation | Tin proposed outcome mà không kiểm policy/evidence |
| Scheduler | Xác định node ready, tạo durable job, fairness và execution admission | Giữ queue chỉ trong memory |
| Policy & Approval | Authorization, capability, scope expansion, approval và completion decision | Để prompt tự cấp quyền |
| Conversation & Context | Canonical message, context resolution, ContextSnapshot và token/resource budget | Coi provider transcript là nguồn chuẩn |
| Projection Service | Kanban, task detail, graph status, blocker và timeline read model | Trở thành nguồn quyết định transition |

### 5.2 Execution plane

| Thành phần | Sở hữu | Không được làm |
|---|---|---|
| Worker Runtime | Claim job, heartbeat, attempt lifecycle, cancel, event normalization | Tự chuyển WorkItem sang DONE |
| Workspace Manager | Provision/reuse/release WorkspaceSet, Git adapter, revision và WriteLease | Suy repository từ current directory |
| Agent Executor | Contract start/cancel, future resume capability và canonical event | Rò provider semantics vào domain |
| Executable Runner | Command/gate/scaffold/service-call policy, isolation profile và resource limits | Chạy resource thụ động chưa đăng ký |
| Verification Service | Thu verdict/evidence, kiểm required gate và provenance | Suy không có gate thành PASS |
| Artifact Service | Content-addressed payload, metadata, hash, retention và redaction status | Nhét output lớn/secret vào event hoặc Markdown |

### 5.3 Infrastructure adapters

- Persistence repository và transaction manager.
- Durable job/outbox transport.
- SQLite/PostgreSQL adapter.
- Filesystem/object-storage artifact adapter.
- Git/worktree adapter cho Windows/Linux.
- Claude/Codex process adapter và adapter tương lai.
- Clock, ID generator, secret broker, process supervisor và telemetry exporter.

Dependency đi từ delivery/infrastructure vào application/domain ports. Domain không import SQL,
HTTP, OS path, process format, provider event hoặc UI model.

## 6. Runtime ownership và lifecycle

```text
WorkItem
  `- TaskFamily
       |- WorkspaceSet
       `- WorkflowRun (pins Execution Manifest)
            `- NodeRun
                 `- ExecutionAttempt
                      |- Job / JobLease
                      |- optional WriteLease
                      |- ContextSnapshot
                      |- AgentSession or CommandExecution
                      `- Artifact / Evidence / Checkpoint
```

Các object có trách nhiệm khác nhau:

- WorkItem mang intent nghiệp vụ, Definition of Done và trạng thái người dùng quan tâm.
- TaskFamily là boundary ownership của root task và descendant.
- WorkflowRun là execution của một graph version, không đồng nhất với card Kanban.
- NodeRun giữ trạng thái logic của node; retry không tạo NodeRun mới nếu vẫn là cùng lần đi qua node.
- ExecutionAttempt là lịch sử bất biến của từng lần thử kỹ thuật.
- Job là đơn vị dispatch nội bộ và có thể được giao lại; nó không phải WorkItem/subtask.
- Checkpoint là durable commit point để recovery; log mới nhất không tự động là checkpoint.

Business rework đi theo graph edge và có thể tạo NodeRun occurrence mới. Technical retry tạo Attempt
mới dưới cùng NodeRun, theo policy đã pin. Join chỉ ready khi join policy được thỏa mãn, không dựa
vào việc queue tạm thời trống.

## 7. Các luồng end-to-end

### 7.1 Author và publish definition

1. Operator sửa file declarative hoặc dùng client authoring.
2. Definition Registry parse và resolve reference/version constraint.
3. Validator kiểm schema, graph, terminal path, cycle budget, capability và policy compatibility.
4. Publisher canonicalize, tính hash và ghi version bất biến cùng audit event.
5. Projection cập nhật danh sách version khả dụng; không tác động run hiện hữu.

Publish thất bại không được để lại version có thể dispatch. Warning và error phải có location cùng
hướng sửa cụ thể.

### 7.2 Tạo root WorkItem và khởi động run

1. Application kiểm Project, RepositoryScope, workflow version và principal authority.
2. Cùng một transaction, tạo root WorkItem, TaskFamily, WorkspaceSet intent, RepositoryScope,
   provision job/outbox và event. Không có public command nào được để lại root WorkItem
   mồ côi trước transaction này.
3. Scheduler tạo job provision cho các RepositoryWorkspace thực sự nằm trong scope.
4. Workspace Manager tạo worktree riêng cho family, pin base RevisionSet và báo evidence provision.
5. Khi workspace bắt buộc đều ready, orchestrator tạo Execution Manifest và kích hoạt entry node.

Nếu provision một repository lỗi, run không đi tiếp qua node cần repository đó. Retry provision phải
tôn trọng generation/fencing và không vô tình reuse worktree không rõ trạng thái.

### 7.3 Dispatch một agent/command node

1. Orchestrator xác nhận NodeRun ready và policy/capability có thể thỏa mãn.
2. Scheduler ghi durable job cùng runtime event trong transaction.
3. Worker claim JobLease, tạo Attempt rồi resolve ContextSnapshot và RepositoryScope hiệu lực.
4. Mutating attempt acquire WriteLease cần thiết; worker dựng execution envelope và sandbox.
5. Adapter chạy provider hoặc executable, stream canonical event và artifact reference.
6. Worker thu outcome, diff, revision, usage và evidence; kiểm scope trước khi đề xuất commit.
7. Application commit Attempt outcome và checkpoint; orchestrator quyết định transition kế tiếp.

ContextSnapshot phải được persist trước khi dispatch external process để lần chạy có thể audit và tái
dựng ngay cả khi provider chết lúc khởi động.

### 7.4 Tương tác chat trong một task

1. Operator gửi Message gắn WorkItem và có thể gắn NodeRun/Attempt đang block.
2. Conversation service append message và ghi actor/correlation.
3. Orchestrator đánh giá message là context bổ sung, approval hay command; không suy bằng text mơ hồ.
4. Khi cần chạy agent, context resolver tạo snapshot mới. Alpha luôn tạo Attempt mới và gọi
   `Start` bằng snapshot đó; orchestrator không gọi provider-native `Resume`.

Chat không sửa thẳng state. Một thao tác như approve, cancel hoặc expand scope phải đi qua command có
kiểu và audit riêng dù được khởi phát từ khung chat.

### 7.5 Verify và hoàn tất

1. Maker outcome chỉ làm node chuyển sang trạng thái chờ verification nếu policy yêu cầu.
2. Checker/gate chạy trên exact RevisionSet và tạo verdict cùng evidence provenance.
3. Verification Service phân biệt PASS, FAIL, ERROR, NOT_RUN và NOT_APPLICABLE hợp lệ.
4. END chỉ đưa WorkflowRun vào `VERIFYING`; Orchestrator kiểm Definition of Done, required gate,
   approval, RepositoryScope, RevisionSet/ReleaseSet và join policy.
5. Chỉ `CompletionPolicy` mới được chuyển cả WorkflowRun sang `SUCCEEDED` và WorkItem sang `DONE`.

Evidence thiếu, stale hoặc không khớp revision là không đủ để PASS. Agent tự ghi “done” chỉ là một
observation.

### 7.6 Crash, lease expiry và recovery

1. Heartbeat dừng làm JobLease hết hạn; recovery reaper chạy định kỳ đánh dấu job cần recovery,
   không chỉ quét một lần lúc process khởi động.
2. Recovery service đọc durable state, checkpoint, artifact/evidence và trạng thái external side
   effect, không đọc transcript như nguồn duy nhất.
3. Nếu side effect chưa bắt đầu, job có thể dispatch lại.
4. Nếu outcome đã commit, delivery lặp bị bỏ qua bằng expected version/idempotency record.
5. Nếu side effect có thể đã xảy ra nhưng outcome chưa biết, Attempt thành INDETERMINATE và đi vào
   reconcile hoặc human escalation; không retry mù.
6. Worker mới chỉ tiếp tục bằng lease/fencing token mới và workspace generation còn hợp lệ.

### 7.7 Mở rộng scope và thao tác đa repository

Scope expansion và multi-repository mutation tuân theo [ADR baseline](02-architecture-decisions.md).
Request được ghi thành approval; Attempt hiện tại kết thúc `BLOCKED`. Sau khi được duyệt và provision
xong, hệ thống append `RunManifestAmendment` rồi tạo **NodeRun activation mới** với
`ReactivationReason=SCOPE_EXPANDED`. Không sửa scope của Attempt/NodeRun cũ và không tự mở rộng quyền
cho sibling. Adapter không được tự mount thêm repository dựa trên yêu cầu trong prompt.

## 8. Durable job, lease và checkpoint protocol

### 8.1 Durable job

Durable job phải lưu được loại operation, aggregate target, expected version, required capability,
priority, availability time, attempt budget và idempotency key. Các trạng thái tối thiểu phải phân
biệt pending, leased, completed, failed, cancelled và recovery-required.

Một application transaction tạo quyết định runtime, audit event và job/outbox tương ứng. Vì vậy
crash không tạo trường hợp “state nói phải chạy nhưng job chưa từng tồn tại”. Delivery có thể lặp;
consumer phải kiểm expected version và idempotency record.

### 8.2 Hai loại lease độc lập

- **JobLease** trao quyền xử lý một durable job trong thời hạn giới hạn.
- **WriteLease** trao quyền sửa một RepositoryWorkspace cụ thể.

Giữ JobLease không mặc nhiên cho quyền ghi workspace. Cả hai có owner, TTL, heartbeat và fencing
token. Mọi commit outcome có side effect phải chứng minh token còn mới; recreate workspace làm tăng
generation và fence mọi token cũ.

### 8.3 Checkpoint

Checkpoint mô tả một ranh giới đã commit, gồm tối thiểu:

- run/node/attempt và expected state version;
- execution phase cùng normalized outcome;
- ContextSnapshot và Execution Manifest được dùng;
- RevisionSet trước/sau nếu có code mutation;
- artifact/evidence reference và hash;
- external operation/idempotency reference;
- quyết định có thể retry/fresh-Start, reconcile hay cần escalation.

Event stream và stdout có thể đến nhiều lần hoặc thiếu đoạn; checkpoint không được suy chỉ từ dòng
log cuối. Checkpoint commit phải cùng transaction với state transition mà nó chứng minh. Payload lớn
được lưu ở artifact store, database giữ reference đã xác minh hash.

### 8.4 Semantics side effect

Hệ thống không tuyên bố exactly-once. Với side effect không transactional, runtime theo dõi intent,
dispatch, observation và committed outcome. Adapter nên hỗ trợ idempotency/reconcile khi provider cho
phép. Khi không thể chứng minh outcome, fail-closed thành INDETERMINATE/BLOCKED thay vì giả định thất bại
rồi chạy lại.

## 9. WorkspaceSet đa repository

Chi tiết domain và invariant nằm tại
[mô hình Project–Repository–WorkspaceSet](01-project-repository-workspace-model.md). Kiến trúc áp dụng
mô hình đó bằng các boundary sau:

1. Workspace Manager là thành phần duy nhất ánh xạ repository identity sang worktree locator.
2. Một TaskFamily sở hữu một WorkspaceSet; child WorkItem chỉ nhận effective scope, không tạo
   worktree riêng.
3. Mỗi RepositoryWorkspace có base/current revision, generation, lifecycle state và lease state.
4. Execution envelope mount đúng repository/path với READ hoặc WRITE mode theo attempt.
5. Mặc định một mutating attempt có đúng một repository `READ_WRITE`; chỉ capability
   `INTEGRATION_MULTI_REPOSITORY_WRITE` mới được batch-acquire nhiều WriteLease.
6. Sau execution, diff scanner kiểm actual write set không vượt scope trước khi accept outcome.
7. Context, command, log, artifact và evidence liên quan source đều mang repository_id cùng revision.
8. Checker/gate chỉ đọc exact RevisionSet/ReleaseSet; scratch output của checker nằm ngoài source
   workspace và không được nhập vào release result.
9. ReleaseSet liên kết kết quả nhiều repository nhưng không mô tả chúng như transaction nguyên tử.
   Alpha có thể tạo local commit qua typed operation; không push, mở PR, merge hay force-push.

UI alpha vẫn quản lý một Project có nhiều repository. Panel source/diff/log read-only chỉ focus một
repository tại một thời điểm là presentation choice, không phải giới hạn của API hay domain. Alpha
không cung cấp browser terminal tương tác.

## 10. Provider và executable boundaries

### 10.1 AgentExecutor

AgentExecutor là outbound port có semantics chung cho start, cancel, capability discovery, event
stream và final outcome. Port có thể giữ `Resume` như capability dành cho phiên bản sau, nhưng Alpha
orchestrator không gọi nó. Input là execution envelope đã resolve; output là canonical event,
provider session reference, usage, termination reason, artifact reference và proposed outcome.

Claude/Codex adapter chịu trách nhiệm:

- phát hiện version/capability và từ chối combination không hỗ trợ;
- đăng ký immutable AdapterBuildVersion; admission pin đúng build trước khi dispatch;
- chuyển provider request/event/session về contract chung;
- quản lý process/signal khác nhau theo OS;
- không tự chuyển domain state, mở scope hoặc cấp filesystem/network permission;
- bảo toàn raw provider output dưới dạng artifact có retention/redaction policy khi cần debug.

ProviderSessionRef chỉ là dữ liệu quan sát/optimization. Recovery Alpha luôn tạo Attempt mới, dựng
ContextSnapshot từ platform state và gọi `Start`; không phụ thuộc provider-native session resume.

### 10.2 Executable Registry và Runner

Executable Definition là object versioned độc lập với Skill/Layer. Nó khai loại capability,
content/hash, input/output contract, working-directory policy, OS/toolchain compatibility, timeout,
resource limit, filesystem/network/secret permission và evidence requirement.

Executable Runner chỉ chạy definition đã publish và được policy cho phép. Script nằm trong resource
vẫn là dữ liệu thụ động cho đến khi có executable definition tham chiếu exact hash. Command, gate,
scaffold và service call có thể dùng runner khác nhau nhưng chia sẻ admission, audit, cancellation và
artifact contract.

### 10.3 Execution envelope

Mọi provider/executable invocation nhận một envelope bất biến cho attempt, gồm identity/correlation,
manifest version, context snapshot, workspace mounts, environment allowlist, secret reference,
deadline, cancel token, output/evidence contract và idempotency reference. Adapter không tự truy vấn
thêm database để mở rộng quyền ngoài envelope.

## 11. Persistence, events, artifacts và projections

### 11.1 Runtime state

Normalized state tables là authority cho aggregate hiện tại. Mutation dùng expected version và
transaction boundary tại application service. Repository contract không lộ SQL/SQLite transaction
object ra domain hoặc qua HTTP.

Các nhóm state cần có boundary riêng:

- definition/version và dependency manifest;
- work/run/node/attempt, RunManifestAmendment và transition;
- project/repository/scope/workspace/revision;
- job/lease/checkpoint/idempotency;
- conversation/context/provider session;
- approval/policy decision;
- artifact/evidence metadata;
- adapter build registry, ReleaseSet và local Git operation result.

### 11.2 Event journal và outbox

Mỗi quyết định quan trọng append audit/domain event có aggregate version, actor, correlation,
causation, policy/definition version, timestamp và một `JournalPosition` tăng đơn điệu nhưng không cần
gapless trong phạm vi database. Aggregate version vẫn là concurrency token riêng. Event journal phục vụ audit, projection
và diagnostics; không phải nguồn duy nhất để replay toàn domain.

Outbox được ghi cùng transaction với state change. Dispatcher có thể phát trùng nên handler cần
idempotent. Thứ tự chỉ được cam kết trong phạm vi aggregate/stream đã định nghĩa, không suy global
ordering giữa mọi Project.

### 11.3 Artifact và evidence

Artifact payload dùng content hash, immutable locator và size/media metadata. Evidence liên kết
artifact với assertion, verifier, command/provider version, time window và exact RevisionSet. Upload
ngoài database có thể tạo orphan khi crash; retention sweeper được phép dọn orphan chưa attach nhưng
không xóa evidence đang được policy giữ. TTL 7 ngày mặc định chỉ áp dụng cho raw output/evidence tạm
theo retention class; canonical Message, referenced resource/context và metadata audit không bị xóa
theo blanket TTL. Hold hợp lệ chặn sweeper và Alpha không hard-delete conversation.

### 11.4 Projection

Kanban, task detail, workflow graph, blocker, workspace status và timeline là projection. Projection
consumer quét global JournalPosition theo thứ tự rồi lọc Project, chấp nhận delivery lặp và có lệnh rebuild. Rebuild khởi tạo read model
từ authoritative state tại watermark rồi replay journal sau watermark; poison event làm projection
`DEGRADED/STALE` với lỗi có thể xử lý, không được âm thầm nhảy cursor. UI phải thấy freshness hoặc lag;
orchestrator không dùng projection eventual-consistent để quyết định readiness, completion hay
authorization.

## 12. Security và governance

### 12.1 Nguyên tắc

- Deny by default cho executable, network, secret và mutation ngoài scope.
- Principal, service identity và worker identity tách biệt.
- Capability được cấp theo project/task/node/attempt, không theo nội dung prompt.
- Secret truyền bằng reference/short-lived material; không persist trong context, log hoặc artifact
  chưa redact.
- Definition, adapter và executable pin version/hash; thay đổi supply chain phải tạo version mới.
- Mọi approval và privileged operation có actor, reason, target, expiry và audit event.
- Hệ thống khai báo trung thực execution profile: `ENFORCED_ISOLATED` khi filesystem/network được
  cưỡng chế, hoặc `OPERATOR_TRUSTED_LOCAL` khi chỉ có admission và hậu kiểm. Diff scanner không được
  gọi là sandbox.

### 12.2 Alpha

Alpha là single-user local nên không giả vờ có multi-tenant authorization hoàn chỉnh. API chỉ bind
loopback, từ chối external bind, validate `Host`/`Origin` và yêu cầu local session token sinh mỗi lần
khởi động cho mutation. Token chỉ được inject vào no-store/CSP bootstrap HTML cho Host hợp lệ, UI giữ
trong memory; token không đi trong URL, log, browser storage hoặc durable state. Startup config cung cấp
một `LocalPrincipalSnapshot {Actor, Roles[]}` được bind với session token; one-shot `aw` resolve cùng
snapshot, và không transport nào cho caller override actor/roles qua payload/flag. Principal config chỉ
đổi sau restart và không thuộc safe-settings mutation. Alpha vẫn thực thi hoặc
khai báo rõ execution profile, executable allowlist, secret redaction, remote Git authority và UI/
worker boundary. Local mode không đồng nghĩa mọi script trong repository đều đáng tin.

### 12.3 Beta

Beta thêm organization/workspace/team/role, tenant-scoped query/mutation, worker admission và secret
broker. Workspace, cache, artifact và job phải mang tenant/project identity; worker chỉ nhận capability
cho attempt đã claim. API authorization và worker sandbox là hai lớp riêng, không thay thế nhau.

## 13. Observability nội tại

Trace chuẩn phải nối được:

```text
Project / Repository
  -> WorkItem / TaskFamily
    -> WorkflowRun / NodeRun / Attempt
      -> Job / Lease
        -> Provider or Executable invocation
          -> Artifact / Evidence / RevisionSet
```

Mỗi event/log/metric liên quan execution cần correlation ID, aggregate IDs, attempt, worker, adapter
version, phase, timestamps và normalized outcome. Raw output ở artifact; structured event chỉ giữ dữ
liệu cần tìm kiếm, đã redact.

Chỉ số nền gồm queue/wait/run/approval time, retry và no-progress rounds, lease loss, recovery time,
context size/relevance, provider usage/cost, gate result, verified completion, scope violation và
projection lag. Dashboard phải phân biệt failure của agent, provider, executable, workspace,
verification và infrastructure để tránh “đổi model” cho một lỗi harness.

Operator phải nhìn thấy node đang chờ gì, ai/cái gì giữ lease, checkpoint gần nhất, revision/evidence
nào được dùng và hành động khôi phục hợp lệ. Observability không chỉ là log cho developer.

## 14. Alpha và beta deployment mapping

| Logical capability | Alpha modular monolith | Beta server/worker |
|---|---|---|
| UI/client | Local web UI | Web UI qua authenticated API |
| Application API | Local API module | Stateless/scalable API instances |
| Definition/Orchestrator | Cùng executable, module boundary rõ | Control-plane services/modules |
| Scheduler/job | Durable SQLite job table + embedded dispatcher | PostgreSQL-backed durable queue/outbox hoặc transport tương thích contract |
| Worker | Embedded/local worker, vẫn claim job | Worker pool tách process/host, mặc định Linux container |
| Workspace | Local filesystem/worktree | Server worker volume/workspace isolation |
| Persistence | SQLite qua repository ports | PostgreSQL qua cùng contract semantics |
| Artifact | Local content-addressed filesystem | Object storage + metadata database |
| Identity | Local principal | Organization/team/user/service identity |
| Provider | Local Claude/Codex process adapter | Claude/Codex CLI chạy trên worker server |
| Telemetry | Structured local logs/metrics | Centralized logs, traces, metrics và audit |

Alpha có thể đóng gói thành một binary/process và chạy module in-process. Mọi execution vẫn đi qua
durable job và worker interface; không tạo “đường tắt local” từ API sang CLI. Beta tách deployment
theo tải và trust boundary, không viết lại workflow semantics.

Không có sync alpha–beta. Nếu sau này cần import/export, đó là explicit snapshot/package operation,
không phải replication giữa SQLite và PostgreSQL.

## 15. Failure containment và recovery policy

| Failure | Boundary chứa lỗi | Kết quả kiến trúc bắt buộc |
|---|---|---|
| API process crash sau command | Transaction + durable job/outbox | State và job cùng tồn tại hoặc cùng không tồn tại |
| Worker chết giữa attempt | JobLease + checkpoint | Reclaim, reconcile hoặc INDETERMINATE; không retry mù |
| Provider session mất | ContextSnapshot + provider adapter | Tái dựng context; session ref không là blocker vĩnh viễn |
| Worktree hỏng/recreate | Workspace generation + fencing | Token/cache cũ vô hiệu; provision/recovery operation riêng |
| Command vượt scope | Sandbox + diff scanner | Fail/block attempt và giữ evidence vi phạm |
| Gate thiếu/không chạy | Verification policy | Không PASS |
| Artifact upload lỗi | Artifact boundary | Outcome không commit nếu evidence bắt buộc chưa durable |
| Projection dừng | Rebuildable read model | Runtime tiếp tục; UI báo lag thay vì đoán state |
| Multi-repo partial failure | RevisionSet/ReleaseSet | Ghi partial outcome và compensation/block rõ ràng |
| Adapter version không tương thích | Capability admission | Từ chối trước dispatch hoặc block có actionable reason |
| Beta worker mất kết nối DB | Lease/heartbeat | Ngừng nhận việc; commit muộn bị fencing/expected version từ chối |

No-progress detector, timeout, retry budget và escalation edge phải chặn vòng lặp vô hạn. Cleanup
workspace/artifact là durable operation có policy, không chạy khi còn active job/lease hoặc khả năng
recovery cần dữ liệu đó. Recovery reaper chạy định kỳ trong suốt vòng đời process và cùng một expired
lease không được tạo nhiều recovery job còn hiệu lực.

## 16. Architecture invariants

1. Runtime không đọc authoring file mutable để thay semantics của run đã bắt đầu.
2. UI/client không trực tiếp chạy provider, executable hoặc Git mutation.
3. Mọi side effect đi qua durable job, Attempt và worker boundary.
4. Mọi mutation authoritative dùng expected version và ghi audit event.
5. Job delivery lặp không được tạo transition hoặc side effect đã commit lần hai.
6. JobLease và WriteLease là hai authority độc lập, đều có fencing.
7. Provider session, raw log và projection không phải runtime source of truth.
8. Skill/Layer/resource không tự cấp executable capability.
9. WorkItem DONE cần evidence và completion policy đạt trên exact revision.
10. Repository không bao giờ được suy chỉ từ path, cwd, slug hoặc remote URL.
11. Child WorkItem không sở hữu mutable workspace ngoài TaskFamily của nó.
12. Alpha và beta không có domain behavior khác nhau chỉ vì persistence/deployment adapter.
13. Unknown external outcome được biểu diễn và reconcile/escalate, không biến thành PASS/FAIL giả.
14. Secret không xuất hiện trong canonical conversation, structured event hoặc artifact chưa redact.
15. Projection có thể xóa và rebuild mà không mất authoritative runtime state.
16. Manifest ban đầu bất biến; scope bổ sung chỉ đi qua RunManifestAmendment và NodeRun activation mới.
17. WorkflowRun không `SUCCEEDED` trước khi CompletionPolicy atomically quyết định cả run và WorkItem.
18. Attempt pin CompiledSnapshotHash và AdapterBuildVersion; dependency hoặc adapter drift bị từ chối.
19. Mỗi mutating attempt chỉ có một repository ghi, trừ capability integration được cấp rõ.
20. Checker/gate không sửa source workspace hoặc ReleaseSet mà nó đang xác minh.
21. Alpha chỉ có local ReleaseSet/local commit; mọi remote Git mutation nằm ngoài authority Alpha.
22. Execution profile, API exposure và retention class phải phản ánh đúng cơ chế thực tế.

## 17. Architecture acceptance criteria

### Definition và orchestration

- **AK-ARCH-001:** Publish cùng canonical source và exact dependency manifest tạo cùng SourceHash/
  CompiledSnapshotHash; version đã publish không sửa được.
- **AK-ARCH-002:** Publish version mới không thay Execution Manifest của run đang chạy.
- **AK-ARCH-003:** Invalid graph, cycle thiếu budget hoặc join thiếu policy bị từ chối trước dispatch.
- **AK-ARCH-004:** Technical retry tạo Attempt mới; business rework tạo graph transition được audit.
- **AK-ARCH-005:** Agent proposed DONE nhưng thiếu evidence không làm WorkItem DONE.
- **AK-ARCH-005A:** END chỉ tạo completion candidate/`VERIFYING`; một CompletionPolicy transaction
  mới chuyển cả WorkflowRun và WorkItem sang trạng thái thành công.
- **AK-ARCH-005B:** Cùng SourceHash nhưng dependency manifest khác tạo CompiledSnapshotHash khác và
  không bị deduplicate nhầm.

### Durability và recovery

- **AK-ARCH-006:** Kill API ngay sau accepted command không làm mất durable job tương ứng.
- **AK-ARCH-007:** Kill worker tại từng execution phase cho kết quả retry/fresh-Start, reconcile hoặc escalation
  xác định; không lặp side effect đã commit.
- **AK-ARCH-008:** Job delivery lặp bị deduplicate bằng idempotency/expected version.
- **AK-ARCH-009:** Lease/fencing token cũ không commit được sau expiry hoặc reassignment.
- **AK-ARCH-010:** Mất provider session vẫn dựng lại được attempt tiếp theo từ platform state.
- **AK-ARCH-010A:** Periodic recovery reaper phát hiện lease hết hạn sau khi process đã chạy lâu và
  không tạo recovery job trùng cho cùng generation.

### Multi-repository workspace

- **AK-ARCH-011:** Project hai repository tạo được TaskFamily/WorkspaceSet với hai worktree độc lập.
- **AK-ARCH-012:** Child cùng family reuse WorkspaceSet; hai root family không dùng chung mutable
  worktree.
- **AK-ARCH-013:** Sibling ở hai repository giữ WriteLease đồng thời; sibling cùng một
  RepositoryWorkspace bị serialize theo baseline.
- **AK-ARCH-014:** Write ngoài effective scope bị từ chối và có evidence chỉ rõ repository/path.
- **AK-ARCH-015:** Gate đa repository pin và hiển thị exact RevisionSet.
- **AK-ARCH-015A:** Scope được duyệt thêm không đổi quyền của Attempt cũ; activation mới pin đúng
  RunManifestAmendment và sibling không tự nhận quyền.
- **AK-ARCH-015B:** Multi-repository write bị từ chối nếu thiếu capability integration; checker sửa
  source workspace luôn bị chặn.
- **AK-ARCH-015C:** ReleaseSet local lưu exact result từng repository; Alpha từ chối push/PR/merge/
  force-push trước khi adapter remote được gọi.

### Boundary và extensibility

- **AK-ARCH-016:** Cùng một orchestrator contract scenario chạy qua Claude fake adapter và Codex fake
  adapter mà không có provider branch trong domain.
- **AK-ARCH-017:** Resource chứa script không chạy được nếu thiếu published executable definition và
  policy grant.
- **AK-ARCH-018:** UI/API process không spawn CLI/Git; mọi execution quan sát được qua job/attempt.
- **AK-ARCH-019:** Repository contract suite cho SQLite và PostgreSQL chứng minh cùng application
  semantics.
- **AK-ARCH-020:** Core contract test pass trên Windows và Linux; OS-specific behavior chỉ nằm sau
  adapter.
- **AK-ARCH-020A:** AdapterBuildVersion không đúng manifest bị admission từ chối trước dispatch.

### Evidence, projection, security và observability

- **AK-ARCH-021:** Evidence truy ngược được WorkItem → Run → NodeRun → Attempt → invocation → artifact
  → exact repository revision.
- **AK-ARCH-022:** Xóa projection rồi rebuild từ authoritative watermark + JournalPosition cho cùng
  Kanban/task-detail state; poison event đưa projection sang `DEGRADED/STALE` thay vì bỏ qua.
- **AK-ARCH-023:** Projection lag không làm orchestrator dispatch hoặc complete sai.
- **AK-ARCH-024:** Secret fixture bị redact khỏi event, log tìm kiếm, conversation và retained artifact
  theo policy.
- **AK-ARCH-025:** Trace phân biệt được provider failure, workspace failure, verifier failure và
  infrastructure failure, đồng thời đưa ra recovery action hợp lệ.
- **AK-ARCH-025A:** Local API từ chối external bind, Host/Origin sai và mutation thiếu session token.
- **AK-ARCH-025B:** Retention sweeper không xóa canonical conversation, held artifact hoặc metadata
  audit; UI Alpha không mở được interactive terminal session.

### Alpha–beta parity

- **AK-ARCH-026:** Một workflow fixture cho kết quả domain tương đương ở alpha topology và beta test
  topology, ngoại trừ identity/deployment metadata đã định nghĩa.
- **AK-ARCH-027:** Không có local fast path bỏ qua durable job, Attempt, scope hoặc evidence contract.
- **AK-ARCH-028:** Chuyển persistence/artifact/worker adapter không yêu cầu thay WorkItem, WorkflowRun,
  NodeRun hoặc Attempt semantics.

### Phase classification

Theo ADR-024, mỗi criterion mang đúng một nhãn phase và nhãn được chốt **trước** khi V1 bắt đầu. Mặc
định của mọi AK-ARCH trong tài liệu này là `ALPHA_MUST`; các ngoại lệ dưới đây là danh sách đóng.

| Criterion | Nhãn | Phần Alpha vẫn phải làm |
|---|---|---|
| AK-ARCH-019 | `BETA_ADAPTER_GATE` | Xây reusable repository/transaction/lease contract harness và chạy nó trên SQLite; không viết adapter hay test PostgreSQL. |
| AK-ARCH-026 | `BETA_PARITY_GATE` | Không có phần Alpha; cần hai topology mới kiểm được. |
| AK-ARCH-028 | `CROSS_PHASE_GUARD` | Alpha kiểm port/import boundary bằng architecture test; adapter parity đầy đủ thuộc Beta. |

Nhãn `NOT_APPLICABLE` chỉ hợp lệ kèm authority reason. V8 không được phân loại lại criteria và không
được dùng `deferred` cho một `ALPHA_MUST`.

Các acceptance criteria này là test target cho Go core spec và spike. Tiêu chí chưa được spike chứng
minh phải được ghi rõ theo nhãn phase ở trên; không được suy “kiến trúc đúng” chỉ từ việc demo happy
path chạy thành công.
