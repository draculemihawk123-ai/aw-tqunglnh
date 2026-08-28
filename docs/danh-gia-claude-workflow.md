# Kinh nghiệm từ claude-workflow để xây Agent Kit

> Đây là tài liệu hồi cứu kỹ thuật. Mục tiêu là giữ lại những bài học bền vững đã rút ra từ
> prototype claude-workflow, không phải đánh giá lại mã nguồn cũ, lựa chọn ngôn ngữ triển khai
> hay mô tả kiến trúc cuối cùng.
>
> Các tiêu chí Harness Engineering đầy đủ nằm tại
> [Bộ tiêu chí nền tảng cho Agent Kit](harness-engineering/00-tong-quan.md).

## 1. Phạm vi tài liệu

claude-workflow có giá trị như một prototype khám phá vấn đề: nó giúp nhìn thấy những gì xảy ra
khi task tracking, quy trình, CLI agent, trạng thái repository và verification được ghép vào cùng
một hệ thống.

Tài liệu này chỉ giữ:

- bài học có thể chuyển thành invariant hoặc guardrail cho Agent Kit;
- ranh giới domain cần đúng từ đầu để không phải thay core khi mở rộng;
- tài sản tri thức có thể tái sử dụng.

Tài liệu này cố ý không chứa:

- audit file, hàm hoặc bug cụ thể của prototype;
- tranh luận sửa prototype hay xây lại;
- lựa chọn Go hoặc cấu trúc package triển khai;
- roadmap, spike plan hoặc backlog;
- bản sao ma trận tiêu chí của 14 lecture.

## 2. Mô hình lõi phải đúng loại sản phẩm

Một task tracker có state machine cố định không phải là workflow runtime. Agent Kit cần tách rõ:

    WorkItem
      -> WorkflowRun (pin WorkflowVersion)
           -> NodeRun (pin BlockVersion và execution profile)
                -> ExecutionAttempt
                     -> AgentSession / CommandExecution

- WorkItem là đối tượng nghiệp vụ hiển thị trên Kanban.
- WorkflowDefinition/Version là graph bất biến sau khi publish.
- WorkflowRun là một lần áp dụng đúng một version cho WorkItem.
- NodeRun biểu diễn một node đang chờ, chạy, block hoặc đã có outcome.
- ExecutionAttempt giữ từng lần thử; retry không được ghi đè lịch sử cũ.
- AgentSession là metadata của provider, không phải trạng thái workflow chuẩn.

Run đang chạy phải tiếp tục trên version đã pin. Publish workflow mới chỉ ảnh hưởng run mới, trừ
khi có một migration operation được mô hình hóa và audit riêng.

UI, CLI adapter và model không được tự chuyển trạng thái domain. Mọi mutation phải đi qua
application command và invariant của orchestrator.

## 3. Project đa repository cần workspace ownership rõ ràng

Agent Kit phải coi dự án nhiều repository là trường hợp bình thường:

    Project
      -> Repository[]
           -> Component[]

    WorkItem
      -> TaskFamily
           -> RepositoryScope[]
           -> WorkspaceSet
                -> RepositoryWorkspace[]
                     -> Worktree

Các semantics cần giữ:

- Kanban và task detail hoạt động ở cấp Project, có thể tổng hợp task từ mọi repository.
- UI có thể focus một repository trong file/log/diff panel, nhưng focus không làm giới hạn domain.
- Một WorkItem có thể scope vào một hoặc nhiều repository.
- Mỗi root WorkItem sở hữu một TaskFamily và một WorkspaceSet riêng.
- Trong WorkspaceSet, mỗi repository có worktree và revision identity riêng.
- Subtask kế thừa WorkspaceSet của task cha; subtask một-repository chỉ thao tác trên phần tương ứng.
- Hai root task độc lập không dùng chung mutable worktree.
- Mọi lease, artifact, evidence và context về code phải mang repository_id cùng revision cụ thể.

Với feature xuyên nhiều service, nên phân rã thành subtask một-repository khi có thể. Đây là cách
giảm shared mutable state, không phải giới hạn bắt buộc của domain.

## 4. Execution phải bền vững và tách khỏi UI

UI chỉ phát command và đọc projection. API/control plane quyết định mutation, tạo durable job; worker
mới được chạy agent CLI, command, gate hoặc thao tác workspace.

Một execution path tối thiểu cần có:

1. Validate command và ghi intent.
2. Tạo job bền vững gắn WorkItem/Run/NodeRun.
3. Worker claim job bằng lease có thời hạn và fencing token.
4. Tạo ExecutionAttempt trước khi gây side effect.
5. Stream canonical event và lưu artifact/evidence.
6. Commit outcome cùng checkpoint.
7. Scheduler quyết định node tiếp theo, retry, block hoặc escalation.

Process chết không được làm mất vị trí workflow. Khi khởi động lại, hệ thống phải phân biệt được:

- side effect chưa bắt đầu;
- side effect đang chạy nhưng chưa rõ outcome;
- side effect đã hoàn tất và có evidence;
- checkpoint đã commit.

Raw CLI hoặc shell command không đảm bảo exactly-once. Harness phải dùng idempotency key khi adapter
hỗ trợ, lưu intent/outcome và đưa trạng thái không chắc chắn về recovery/escalation thay vì chạy lại
mù quáng.

## 5. Completion phải dựa trên evidence và fail-closed

Agent có thể đề xuất rằng công việc đã xong, nhưng không có quyền tự quyết định DONE.

Kết quả verification phải phân biệt tối thiểu:

- NOT_RUN: chưa thực thi;
- NOT_APPLICABLE: không áp dụng và có lý do/policy cho phép;
- PASS: đã chạy và evidence hợp lệ;
- FAIL: đã chạy, tiêu chí không đạt;
- ERROR: verifier không thể đưa ra verdict đáng tin cậy.

Không có gate, thiếu cấu hình gate bắt buộc hoặc mất evidence không được suy thành PASS.

Evidence phải truy ngược được:

    WorkItem -> WorkflowRun -> NodeRun -> Attempt
             -> Gate/Command -> Artifact -> Repository revision

Completion policy cần kiểm tra Definition of Done, required gates, approval, artifact và repository
state. Maker và checker nên tách vai trò đối với thay đổi có rủi ro.

## 6. Agent runtime phải độc lập provider

Claude CLI và Codex CLI chỉ là adapter của một contract chung. Domain không được chứa tên file,
session format, event type hoặc assumption riêng của provider.

Contract AgentExecutor cần biểu diễn các capability như:

- start/resume/cancel execution;
- capability discovery;
- canonical event stream;
- usage và termination reason;
- provider session reference;
- structured outcome và artifact reference.

Adapter chịu trách nhiệm chuyển event riêng của provider về canonical event. Contract test phải chạy
cùng một scenario trên adapter giả lập Claude và Codex để chứng minh orchestrator không phụ thuộc
provider.

Conversation, selected messages, context manifest và checkpoint thuộc platform. Provider session có
thể giúp resume nhanh, nhưng mất session không được làm mất khả năng dựng lại context cần thiết.

## 7. Skill và Layer là tri thức thụ động

Skill/Layer có thể chứa:

- instruction, rule, convention và checklist;
- reference, example và template;
- metadata về version, selector, dependency, conflict và owner;
- source code hoặc script dưới dạng resource để đọc hoặc dùng làm template.

Cài Skill/Layer không được tự động cấp quyền chạy code, gọi network, sửa workspace, đọc secret hoặc
thêm verification gate.

Một script resource chỉ là bytes cho đến khi được đăng ký riêng thành executable object, ví dụ:

- CommandDefinition;
- GateDefinition;
- ScaffoldDefinition;
- provider/tool adapter.

Executable object phải pin content hash/version và khai báo argv/input, working-directory policy,
permission, sandbox, timeout, resource limit, network/secret policy, output contract và audit
evidence. Side effect luôn đi qua worker/executor được kiểm soát.

## 8. Boundary dữ liệu và API phải đúng từ alpha

Domain/application không được phụ thuộc vào transaction API hoặc SQL shape của SQLite. SQLite và
PostgreSQL là persistence adapter của cùng application semantics, không phải hai hệ thống được nối
bằng cách giả lập database transaction qua HTTP.

Các nguyên tắc:

- client gọi application API; chỉ server truy cập persistence repository;
- mutation có command boundary và transaction boundary rõ;
- event, job và checkpoint được ghi atomically khi cùng thuộc một quyết định;
- projection/Kanban là read model có thể dựng lại, không phải nguồn trạng thái độc lập;
- schema migration và repository contract test tồn tại từ alpha;
- mọi query/mutation beta phải scope rõ organization, project và repository;
- optimistic concurrency hoặc expected version bảo vệ mutation cạnh tranh.

Alpha có thể là modular monolith dùng SQLite và embedded worker. Các ranh giới application, worker,
provider, workspace, artifact và persistence vẫn phải tồn tại để beta có thể tách process và dùng
PostgreSQL mà không đổi domain semantics. Alpha và beta không cần cơ chế đồng bộ dữ liệu.

## 9. Source of truth, observability và an toàn vận hành

Mỗi concern có một nguồn authoritative:

- Git repository tại revision cụ thể: code, cấu hình và convention nằm trong repo.
- Agent Kit database: WorkItem, workflow runtime, job, lease, approval và audit event.
- Artifact store: log, diff, report và output lớn; database giữ metadata/hash.
- Platform conversation/context state: dữ liệu chuẩn để tiếp tục công việc.
- Provider session: metadata tối ưu resume, không phải canonical context.

Trace tối thiểu phải nối được:

    Project/Repository
      -> WorkItem/TaskFamily
      -> WorkflowRun/NodeRun/Attempt
      -> Agent/Command/Gate event
      -> Artifact/Evidence/Revision

Log cần có correlation ID, timestamp, actor, policy/version và outcome; đồng thời phải redact secret.
Artifact lớn hoặc dữ liệu nhạy cảm không được đẩy mù vào Markdown hay Git history.

Concurrency cần lease có TTL, heartbeat và fencing token. Mặc định mỗi RepositoryWorkspace chỉ có
một writer; chỉ mở path-scoped parallel write khi engine có thể chứng minh phạm vi không giao nhau.
Timeout, cancel, retry budget, no-progress detector và escalation là capability của runtime, không
phải convention trong prompt.

## 10. Tài sản tri thức nên mang sang

Những phần có giá trị từ prototype nên được giữ như input, fixture hoặc reference:

- cách mô tả lỗi theo WHAT / WHY / FIX;
- template SPEC, DESIGN, TASK và checklist;
- rule, skill, snippet sau khi chuẩn hóa thành resource thụ động;
- golden scenario và fixture cho verification;
- discovery về stream/event/session của Claude CLI;
- bố cục Kanban, task detail, graph, evidence và chat như UI reference;
- các failure case đã quan sát để chuyển thành automated regression test.

Giữ tri thức không đồng nghĩa port nguyên core, schema, API hoặc state machine cũ.

## 11. Guardrail dùng khi xây Agent Kit

Một thay đổi core chỉ được coi là đi đúng hướng khi:

1. Không hard-code một workflow nghiệp vụ vào domain.
2. Run pin definition version và retry tạo Attempt mới.
3. Process có thể chết rồi resume từ durable state.
4. UI không trực tiếp chạy CLI hoặc sửa runtime state.
5. Project nhiều repository và WorkspaceSet có identity rõ.
6. Root task có workspace riêng; subtask kế thừa workspace family.
7. Provider mới được thêm bằng adapter và contract test.
8. Skill/Layer không tự có executable permission.
9. Completion fail-closed và luôn dựa trên evidence.
10. Trace truy được đến exact repository revision.
11. SQLite/PostgreSQL không làm thay đổi application semantics.
12. Mỗi slice có test cho success, failure, crash/recovery và concurrency liên quan.

Những guardrail này là đầu vào cho architecture/spec và acceptance test. Chi tiết triển khai phải
được mô tả ở tài liệu thiết kế riêng, theo từng version và subtask đủ nhỏ để hoàn thành, kiểm chứng và
handoff trong một session.
