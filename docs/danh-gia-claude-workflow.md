# Đánh giá `claude-workflow`: sửa tiếp hay xây core mới bằng Go

> Trạng thái: **đề xuất để thảo luận, chưa phải quyết định triển khai đã chốt**.
>
> Project được đánh giá: `D:\Documents new\project\claude-workflow`, nhánh `master`, commit
> `63410f9c1e0e8ac7561f9ee5ce988978ec5cb742`, worktree sạch tại thời điểm đọc.
>
> Thước đo: bộ [15 tài liệu Harness Engineering](harness-engineering/00-tong-quan.md), tổng hợp từ
> [walkinglabs/learn-harness-engineering tại commit `77e7a3e`](https://github.com/walkinglabs/learn-harness-engineering/tree/77e7a3e21469dcbece2558086c8d91657abeaa40).

## 1. Kết luận sơ bộ

Với phạm vi sản phẩm đã nêu, nên **xây một core mới bằng Go**, đồng thời dùng `claude-workflow` làm
nguồn tri thức và compatibility corpus. Đây là **greenfield runtime, brownfield knowledge**:

- không tiếp tục kéo Python prototype thành workflow server;
- không vứt tài liệu, rule, skill, template, fixture và các phát hiện thực nghiệm;
- không big-bang thay mọi thứ trong một lần;
- dựng từng vertical slice mới và dùng hành vi hữu ích của prototype làm regression input.

Lý do quyết định không phải “Python không scale”. Python vẫn có thể chạy server tốt. Lý do là
**mô hình lõi hiện tại không cùng loại với sản phẩm đích**:

```text
claude-workflow hiện tại
  = scaffold cho một repo + task tracker 9 trạng thái + Claude launcher local

Agent Kit cần có
  = definition runtime + workflow execution engine + worker/workspace isolation
    + provider-neutral agents + evidence/context/conversation platform
```

Để đạt đích bằng refactor, gần như toàn bộ phần tạo ra danh tính của core hiện tại đều phải đổi:
domain model, schema, service boundary, executor, concurrency, provider adapter và UI state model.
Giữ nguyên ngôn ngữ không còn giúp giảm rủi ro đủ nhiều.

## 2. Phạm vi và cách đánh giá

Đã đọc:

- `AGENTS.md`, `CLAUDE.md`, `README.md` và toàn bộ `HANDOFF.md`;
- thiết kế V1–V4 và 23 acceptance scenarios của V4 alpha;
- toàn bộ Python core, SQLite schema, service, projection, layer compiler;
- backend/UI local và đường gọi Claude CLI;
- layer manifests, rules, skills, fixtures và lịch sử commit gần nhất.

Không sửa file nào trong `claude-workflow`. Không chạy code/plugin từ layer vì đó chính là một bề
mặt thực thi cần đánh giá, không phải dependency đáng tin mặc định.

Một số số liệu cấu trúc của prototype:

- 25 file Python, khoảng 4.058 dòng trong `harness/`;
- 12 file JavaScript, khoảng 1.721 dòng;
- không có suite `test_*.py`; việc kiểm chứng hiện nằm chủ yếu trong tài liệu, fixture và golden
  command được chạy thủ công;
- core không có `WorkflowRun`, `NodeRun`, `ExecutionAttempt`, worktree manager hay provider interface;
- 23/25 file Python có tham chiếu `claude`/`.claude`; không có Codex adapter.

## 3. Những tài sản có giá trị nên giữ

`claude-workflow` là một prototype tốt cho discovery. Các phần sau có giá trị cao:

1. **Kỷ luật “kiểm chứng, không suy đoán”.** `HANDOFF.md` ghi cả giả định sai, cách dựng sandbox và
   các lỗi chỉ lộ khi bấm/chạy thật.
2. **Nguyên tắc agent không tự tuyên bố hoàn thành.** Gate và human approval được tách khỏi lời kể
   của Claude.
3. **Thông báo WHAT/WHY/FIX.** Đây là format tốt cho agent tự sửa và cho operator chẩn đoán.
4. **Fixture/golden cross-shell.** Chúng có thể trở thành compatibility tests cho importer hoặc
   CLI mới.
5. **Tài liệu/rule/skill/template theo stack.** Sau khi tách code thực thi, đây là seed tốt cho
   passive Engineering Pack.
6. **Kinh nghiệm gọi Claude headless.** Các phát hiện về session ID, `stream-json`, non-interactive
   shell và đường dẫn executable là dữ liệu đầu vào quý cho Claude adapter.
7. **UI prototype.** Token CSS, bố cục task detail, evidence/timeline và chat cho thấy nhu cầu sản
   phẩm; có thể tái dùng làm wireframe hoặc một phần presentation layer tạm thời.
8. **Ý thức về DB authority và event cùng transaction.** Hướng tư duy đúng, dù schema hiện tại chưa
   đủ cho workflow runtime.

Những tài sản này nên được chuyển thành test case, resource và ADR; không nhất thiết port nguyên mã.

## 4. Các blocker kiến trúc

### P0 — core không có workflow runtime

`state.py` cố định một máy trạng thái 9 bước. `task-board.js` và `flow-tracking.js` cũng hard-code
chính chín trạng thái và tọa độ graph. Không tồn tại các khái niệm:

- `WorkflowDefinition/Version`;
- `BlockDefinition/Version`;
- `WorkflowRun`, `NodeRun`, `ExecutionAttempt`;
- typed node input/output và edge outcome;
- durable checkpoint cho node;
- fork/join, wait/signal, rework policy theo workflow.

Vì vậy “thêm một workflow mới” hiện đồng nghĩa sửa core, schema và UI. Đây là ngược với yêu cầu
định nghĩa block rồi cắm vào nhiều luồng.

### P0 — boundary `Store` không thể đưa alpha lên beta bằng đổi adapter

Thiết kế beta nói chỉ đổi `store=HttpStore(...)`. Nhưng `Store` thực tế chỉ khai hai hàm
`init_schema()` và `tx()`. Service mở transaction rồi gọi hàng chục method không nằm trong interface
trên object `_SqliteTx`.

Một HTTP client không thể giữ cùng transaction boundary và row-object semantics như kết nối SQLite
bằng cách “đổi adapter”. Beta cần một API ở **application/service boundary**, không phải mô phỏng
remote database transaction ở storage boundary.

Hệ quả: đường beta hiện tại không phải một adapter nhỏ; nó là thay lại service protocol, auth,
authorization, transaction/idempotency và error semantics.

### P0 — completion có thể pass mà không có evidence

Ba đường hiện tại kết hợp thành false pass:

- `gate.py` dùng `all([])`, nên `verify=[]` cho kết quả `passed=True`;
- API duyệt task chỉ chặn gate có giá trị `FAIL`; gate rỗng hoặc `NOT_RUN` không chặn;
- wrapper Stop trả exit 0 khi thiếu `harness.json` với chủ ý fail-open.

Điều này trái với nguyên tắc trung tâm của chính project: thiếu verification không thể trở thành
`DONE`. Policy cần phân biệt `REQUIRED`, `NOT_APPLICABLE`, `NOT_RUN`, `PASS`, `FAIL`, `ERROR` và
chỉ checker có thẩm quyền mới phát terminal outcome.

### P0 — execution nằm trong UI process và không có isolation

UI trực tiếp `subprocess.Popen` Claude trong repo đang mở. Gate trực tiếp chạy chuỗi command từ
`harness.json`. Check loader import và thực thi Python từ `.claude/checks/*.py` ngay trong process.

Hiện không có:

- worker/job/lease;
- worktree manager hoặc task-family ownership;
- sandbox, resource quota, cancellation và process-tree cleanup;
- capability negotiation;
- secret/redaction policy cho stdout;
- idempotency key hoặc fencing token;
- command/evidence provenance đầy đủ;
- giới hạn fan-out và backpressure.

Đây là blocker cho cả chạy song song local lẫn chạy CLI trên server.

### P0 — chưa có provider-neutral contract

Session map, config, subprocess arguments và event parsing đều gắn trực tiếp với Claude. Task UUID
là khóa duy nhất của `harness-sessions.json`, nên hai project có cùng UUID còn có thể dùng nhầm
session. Không có interface cho `start/resume/cancel/stream/capabilities`, không có normalized event
và không có platform-owned conversation/context snapshot.

Thêm Codex vào cấu trúc hiện tại sẽ tạo một nhánh `if provider == ...` xuyên UI và core, không phải
một adapter độc lập.

### P0 — boundary dự án chưa an toàn cho beta

`TaskService` và `DocService` nhận `project_id`, nhưng `get_task(task_id)`, `transition(task_id)` và
`get_doc(doc_id)` không query kèm project. Một service được dựng cho project A có thể update record
của project B rồi ghi event dưới project A nếu biết ID.

Các foreign key cũng không bảo đảm `active_task.project_id`, `task_id`, `design_doc_id` và event refs
thuộc cùng project. Đây là lỗi tenant isolation ở domain/storage layer; UI filter không thể sửa nó.

### Dẫn chứng mã nguồn nhanh

| Nhận định | Vị trí |
|---|---|
| Máy trạng thái là enum cố định | [`state.py`](../../claude-workflow/docs/ai-workflow/02-scaffold/dot-claude/harness/state.py:35) |
| UI hard-code chín cột | [`task-board.js`](../../claude-workflow/docs/ai-workflow/02-scaffold/dot-claude/harness/ui/static/areas/task-board.js:16) |
| Graph UI hard-code tọa độ/edge | [`flow-tracking.js`](../../claude-workflow/docs/ai-workflow/02-scaffold/dot-claude/harness/ui/static/areas/flow-tracking.js:21) |
| `Store` chỉ có `init_schema/tx` | [`store/__init__.py`](../../claude-workflow/docs/ai-workflow/02-scaffold/dot-claude/harness/store/__init__.py:16) |
| Thiết kế cho rằng beta chỉ đổi `HttpStore` | [`V4-ke-hoach.md`](../../claude-workflow/docs/ai-workflow/versions/V4-ke-hoach.md:137) |
| Gate rỗng được `all([])` coi là pass | [`harness/gate.py`](../../claude-workflow/docs/ai-workflow/02-scaffold/dot-claude/harness/gate.py:182) |
| Thiếu config ở Stop hook trả success | [`scripts/gate.py`](../../claude-workflow/docs/ai-workflow/02-scaffold/dot-claude/scripts/gate.py:46) |
| UI chỉ chặn verdict `FAIL` | [`ui/api.py`](../../claude-workflow/docs/ai-workflow/02-scaffold/dot-claude/harness/ui/api.py:344) |
| Claude được spawn trực tiếp trong UI backend | [`claude_proc.py`](../../claude-workflow/docs/ai-workflow/02-scaffold/dot-claude/harness/ui/claude_proc.py:179) |
| Layer khai executable checks | [`java-spring/layer.json`](../../claude-workflow/docs/ai-workflow/02-scaffold/dot-claude/layers/java-spring/layer.json:25) |
| Apply copy executable checks vào repo | [`apply.py`](../../claude-workflow/docs/ai-workflow/02-scaffold/dot-claude/harness/apply.py:271) |
| Service lấy task/doc không kèm project scope | [`service/__init__.py`](../../claude-workflow/docs/ai-workflow/02-scaffold/dot-claude/harness/service/__init__.py:115) |

## 5. Các khoảng trống quan trọng khác

### Layer đang vượt ranh giới instruction/resource

`java-spring/layer.json` chứa build/verify command, runtime/toolchain, executable Python checks và
file scaffold. `apply.py` copy checks vào `.claude/checks`, rồi check loader import chúng như code.

Điều này trái quyết định mới: Skill/Layer chỉ chứa instruction/resource. Cần tách:

- **Layer/Pack:** rule, convention, reference, checklist, template, example;
- **Command/Gate/Scaffold definition:** object executable riêng, được publish và cấp quyền riêng;
- **Executor:** process tin cậy thực hiện side effect, thu evidence và audit.

Một file `.py` hoặc `.sh` có thể nằm trong Pack như **resource để đọc**. Nó chỉ trở thành executable
khi được đăng ký riêng, pin hash/version, qua policy rồi được executor gọi. Extension thực thi cần
sandbox, timeout, permissions, secret policy, audit, idempotency và ownership của file sinh ra.

### Layer model giả định một repo chỉ có một stack

`java-spring` khai xung đột với `angular` và `nextjs`. Điều này không phù hợp project lớn có backend,
micro frontend, mobile, Docker/Kubernetes và automation tests cùng tồn tại.

Pack phải resolve theo `Repository -> Component -> path/task selector`. Java và Angular không xung
đột nếu áp cho hai component khác nhau. Chỉ hai giá trị đơn áp vào **cùng scope** mới là conflict.

### SQLite schema là task tracker, chưa phải execution schema

13 bảng hiện tại chưa có:

- workflow/block/skill/pack/profile versions;
- workflow run, node run, attempt và route decision;
- job, lease, heartbeat, retry budget và idempotency;
- workspace, repository, component, branch/worktree và task family;
- conversation, message, context snapshot và provider session;
- artifact/evidence với hash, revision, command, cwd và retention;
- approval policy, permission grant và canonical execution event.

`task_gate` còn ghi đè kết quả mới nhất thay vì giữ từng GateRun/evidence. `schema_version` chỉ phát
hiện “đã có bảng” rồi dừng; chưa có migration runner. SQLite cũng chưa bật WAL/busy timeout, chưa có
optimistic version hay lease để xử lý writer cạnh tranh.

### Repo/DB projection vẫn tạo hai bề mặt dễ lệch

Thiết kế nói DB authoritative, nhưng nội dung AC/Target/Out-of-scope vẫn do file sở hữu và runtime
không tự ingest lại. Task tạo mới không có API hoàn chỉnh để cập nhật `lane`, `design`, objective và
AC; `migrate` cho task tồn tại cũng không đồng bộ mọi trường. `HANDOFF.md` đã ghi nhiều giới hạn này.

Projection có thể giữ như artifact/resume view, nhưng không nên là contract bắt buộc để runtime
hoạt động. Runtime phải đọc platform state và repository resources tại revision cụ thể.

### Identity và approval chỉ đủ cho demo local

- UI nhận tên người duyệt từ body đối với document và có thể tạo member theo tên đó.
- Task approval kiểm body có tên, nhưng authoritative writer lại lấy identity file cục bộ; hai tên
  có thể khác nhau.
- Chạy lại `whoami.py --init` sinh member UUID mới; nếu username đã tồn tại, DB giữ ID cũ còn file
  identity giữ ID mới, dễ gây foreign-key failure lần ghi sau.

Beta cần principal do server xác thực, membership/role theo tenant và authorization ở command
handler. Không nhận actor identity từ request payload.

### Observability chưa đủ để vận hành workflow

Event hiện chỉ mô tả task/gate/doc ở mức cao. Không có trace
`WorkItem -> Run -> Node -> Attempt -> Tool/Gate`. Gate không persist stdout artifact/hash, command
version, cwd, code revision và environment. Một phần output còn được nối vào Markdown, có rủi ro
đưa secret vào git.

### Bộ test chưa bảo vệ core như một product runtime

Golden/manual acceptance đã bắt được nhiều lỗi thật và nên giữ. Tuy nhiên không có automated unit/
integration suite chạy liên tục cho domain invariants, crash recovery, concurrent writers, process
supervision, tenant isolation và security boundaries. Việc nhiều bug chỉ lộ ở phiên sau là tín hiệu
rằng test corpus tốt nhưng test harness chưa được sản phẩm hóa.

## 6. Đối chiếu 15 phần Harness Engineering

| Phần | Hiện trạng | Khoảng trống để đạt Agent Kit |
|---|---|---|
| Tổng quan | Có prototype local và tài liệu tốt | Chưa có definition/runtime/execution planes |
| Lec 01 — failure diagnosis | Có nhật ký lỗi và văn hóa kiểm chứng | Failure chưa thành taxonomy/event runtime chuẩn |
| Lec 02 — năm phân hệ | Có mầm instruction, tool, env, state, gate | Không có resolved manifest, capability/permission boundary |
| Lec 03 — source of truth | Đã chuyển status vào DB | AC/config/projection còn lệch; project identity chưa đủ |
| Lec 04 — disclosure | Có rules theo path và layer compiler | Không có context snapshot/provenance; Pack đang chứa executable |
| Lec 05 — continuity | Có task Markdown và Claude session map | Không có checkpoint/context/conversation do platform sở hữu |
| Lec 06 — initialization | Có `init_check.py` | Chưa tách project onboarding, run bootstrap và runner readiness |
| Lec 07 — scope/WIP | Có Target/Out-of-scope trong Task | Không có scope enforcement, worktree, lease hay task family |
| Lec 08 — work item | Có Task + AC + events trong DB | Không có cha-con/family, WorkflowRun/NodeRun/Attempt |
| Lec 09 — external completion | Có machine gate và human approval | Empty/NOT_RUN có thể pass; checker chưa độc lập và evidence thiếu |
| Lec 10 — full pipeline | Có danh sách verify command | Mapping theo vị trí; không risk-based/component-aware/evidence-rich |
| Lec 11 — observability | Có append-only event | Không có canonical trace/tool events/artifact policy |
| Lec 12 — clean handoff | Có projection/log/golden | Không có clean-state policy thực thi hay workspace ownership |
| Lec 13 — autonomous loops | Chưa có | Thiếu loop state, budgets, no-progress, trigger/idempotency |
| Lec 14 — graph orchestration | UI vẽ graph cố định | Thiếu versioned typed graph, checkpoint, fork/join và routing engine |

## 7. Ma trận quyết định

| Tiêu chí | Sửa tiếp Python core | Core mới bằng Go |
|---|---|---|
| Giữ demo cá nhân hiện tại | Tốt nhất | Cần dựng lại một vertical slice |
| Nhiều workflow/block versioned | Phải thay domain/schema/UI lõi | Thiết kế đúng ngay từ domain |
| Claude + Codex + provider tương lai | Cần tháo coupling xuyên nhiều module | Đặt port `AgentExecutor` từ đầu |
| Task family + worktree + parallelism | Gần như thêm mới hoàn toàn | Đặt workspace/lease là primitive |
| Server worker + PostgreSQL | `HttpStore` hiện tại không phải boundary đúng | API/service/worker boundary được thiết kế trước |
| Crash resume và evidence | Thay phần lớn persistence/runtime | Đưa Run/Node/Attempt/Event vào schema đầu tiên |
| Multi-repo/multi-component stack | Layer conflict và repo model phải đổi | Component-scoped Pack từ đầu |
| Tận dụng code hiện tại | Cao ở ngắn hạn, thấp sau khi đổi core | Thấp với Python code, cao với docs/fixtures/resources |
| Rủi ro legacy constraint | Cao | Thấp hơn nếu giữ scope alpha nhỏ |
| Thời gian tới product đúng kiến trúc | Dễ nhanh lúc đầu, chậm dần vì “ship of Theseus” | Chậm hơn ở slice đầu, ít phải tháo lại |

**Khi nào nên sửa tiếp:** nếu sản phẩm được thu hẹp về một người, một repo, một workflow 9 trạng
thái, chỉ Claude và không cần worker/worktree.

**Với yêu cầu hiện tại:** xây mới hợp lý hơn. Sửa tiếp cuối cùng vẫn là rewrite, nhưng rewrite bị
ràng bởi API/schema cũ và khó biết đoạn nào còn an toàn.

## 8. Vì sao Go phù hợp — và Go không tự giải quyết điều gì

Go phù hợp với core mới vì:

- dễ đóng gói local thành một binary;
- hợp với HTTP control plane, durable worker và process supervision;
- concurrency/cancellation/context phù hợp job, lease và streaming event;
- cùng domain/application layer có thể dùng SQLite ở alpha và PostgreSQL ở beta;
- interface rõ cho provider, workspace, artifact và persistence adapter.

Nhưng Go không tự mang lại scale hoặc an toàn. Vẫn phải thiết kế đúng:

- không dùng Go plugin động làm cơ chế extension chính; dùng interface nội bộ và protocol/process
  adapter có version;
- không để raw shell command chạy trong API process;
- không giả vờ SQLite transaction và HTTP request là cùng một storage adapter;
- không tách microservice sớm; alpha nên là modular monolith với embedded worker;
- không để provider session thay platform state.

## 9. Bộ khung Go đề xuất để thảo luận

```text
cmd/
  agentkit               local all-in-one alpha
  agentkit-server        beta control plane
  agentkit-worker        beta execution worker

internal/domain/
  definitions            Workflow/Block/Skill/Pack/Profile versions
  work                    WorkItem/TaskFamily/Scope
  execution               Run/NodeRun/Attempt/Outcome/Budget
  evidence                GateRun/Artifact/Evidence
  workspace               Project/Repository/Component/WorkspaceSet/Lease
  conversation            Conversation/Message/ContextSnapshot

internal/application/
  commands                authoritative mutations
  queries                 projections/read models
  scheduler               durable jobs, leases, retry/escalation

internal/ports/
  persistence, agent_executor, command_executor, workspace,
  artifact_store, context_assembler, clock, id_generator

internal/adapters/
  sqlite, postgres, git_worktree, local_artifact,
  claude_cli, codex_cli, local_process

web/                      management UI; chỉ nói chuyện qua API
```

Alpha có thể chạy tất cả trong một process/binary, nhưng vẫn đi qua application ports. Beta tách
worker thành process/server khác mà không đổi domain semantics.

Không có BPMN. Workflow là typed directed graph nội bộ, được version, validate và checkpoint.

## 10. Semantics worktree/task family cần khóa trong core mới

```text
Project -> Repository A, Repository B, ...

Root WorkItem A -> TaskFamily A -> WorkspaceSet[Repository A -> Worktree A]
  |- child A1
  |- child A2
  `- child A3

Root WorkItem B -> TaskFamily B -> WorkspaceSet[Repository A -> Worktree B]
```

- Domain hỗ trợ `Project -> Repository[]` ngay từ alpha.
- UI alpha chỉ giữ một `active_repository_id` và chỉ hiển thị/thao tác trên repository đó.
- Alpha policy giới hạn `RepositoryScope`/`WorkspaceSet` của một WorkItem/TaskFamily có đúng một repository; collection shape được giữ từ đầu để không phải đổi core khi mở multi-repository execution.
- Hai root family chạy song song trên hai worktree.
- Child/subtask luôn kế thừa family/worktree của cha.
- Mặc định một write lease trên family.
- Chỉ cho sibling ghi song song khi path scopes không giao nhau và có fencing token.
- Read-only checker dùng immutable revision/snapshot khi có thể.
- Parent/integration node quản merge, full verification và release worktree.

Các object cần có từ alpha: `TaskFamily`, `RepositoryScope`, `WorkspaceSet`, `RepositoryWorkspace`,
`WorkspaceGeneration`, `Lease`, `PathScope`, `RepositoryRevision`. Mọi object runtime/evidence liên quan
workspace phải mang `repository_id`. Đợi tới beta mới thêm sẽ buộc sửa scheduler và evidence model.

## 11. Ranh giới Skill/Layer và executable trong core mới

### Passive package

Skill/Layer được phép chứa:

- Markdown instruction, rule, checklist và reference;
- template source code/config;
- example command dưới dạng tài liệu;
- metadata selector, dependency, conflict và version/hash.

Chúng không được tự chạy, tự copy file, cấp permission hoặc thêm gate.

### Executable objects

- `ScaffoldDefinition`: mô tả input/output và template refs; `ScaffoldExecutor` mới được ghi file.
- `CommandDefinition`: argv, cwd policy, env allow-list, timeout, capability và output contract.
- `GateDefinition`: command/evaluator refs, required evidence và verdict policy.
- `AgentExecutor`: Claude/Codex adapter chạy dưới worker policy.

Nếu một resource chứa script, nó vẫn chỉ là bytes để đọc. Muốn chạy phải đăng ký thành
`CommandDefinition` riêng, pin content hash và qua trust/permission policy. Nhờ vậy “cài layer”
không đồng nghĩa “cho code trong layer quyền chạy trên server”.

## 12. Phần nào giữ, port hay bỏ

| Tài sản từ `claude-workflow` | Xử lý |
|---|---|
| `HANDOFF.md`, nhật ký chẩn đoán, quyết định đã kiểm chứng | Giữ làm research record/ADR input |
| `00-luong.md`, SPEC/DESIGN/TASK templates | Giữ làm requirement corpus; không biến enum thành core |
| Rules/Skills/snippets | Chuẩn hóa thành passive resources, bỏ phần BPM khỏi baseline chung |
| `ArchitectureTest.java`, Python checks | Giữ như resource/fixture; đăng ký gate riêng nếu muốn thực thi |
| Golden outputs và fixtures | Port thành compatibility/integration tests tự động |
| WHAT/WHY/FIX messages | Giữ làm error contract |
| CSS tokens và bố cục UI | Có thể tái dùng; task graph/data fetching phải viết lại |
| Claude `stream-json` discoveries | Chuyển thành contract tests cho `claude_cli` adapter |
| `Task` SQLite data | Chỉ cân nhắc importer một lần cho dữ liệu cần giữ; không xây sync alpha↔beta |
| Python `Harness`, `Store`, `TaskService`, fixed state machine | Không port nguyên mã; dùng làm negative/behavior reference |
| `HttpStore` beta idea | Bỏ; client gọi application API, server dùng PostgreSQL repository |
| `apply.py` và active layer config | Thay bằng passive package resolver + controlled scaffold/gate registry |
| UI spawn Claude trực tiếp | Bỏ; UI tạo command/job, worker thực thi |

## 13. Lộ trình tránh big-bang

### Chặng 0 — khóa contracts trước khi code

- Chốt glossary/domain IDs và boundary Definition/Runtime/Execution.
- Chọn định dạng definition alpha và versioning rule.
- Chuyển các scenario giá trị từ `claude-workflow` thành test corpus độc lập.
- Ghi rõ dữ liệu alpha không sync sang beta; schema portability không đồng nghĩa data replication.

### Chặng 1 — vertical slice local nhỏ nhất

- Một WorkflowVersion tuyến tính 3 node: AI -> machine gate -> human approval.
- WorkItem/Run/NodeRun/Attempt/Event trên SQLite.
- Một root task tạo worktree, durable job chạy mock executor, crash-resume được.
- API và CLI cùng gọi application service; chưa cần visual editor.

### Chặng 2 — provider và evidence

- Claude CLI và Codex CLI qua cùng `AgentExecutor` contract.
- Canonical event normalization, conversation/message/context snapshot.
- Command/Gate executor thu argv, cwd, revision, exit, duration, output artifact và hash.
- Empty verification không thể tạo PASS.

### Chặng 3 — task family và parallelism

- Parent/child WorkItem, shared family worktree.
- Family write lease trước; path-scoped parallel write chỉ thêm sau benchmark.
- Fork/join, retry budget, no-progress và escalation.

### Chặng 4 — package/context và UI

- Passive Skill/Layer registry cùng context manifest.
- Kanban, task detail, graph overlay, evidence, chat và operator actions.
- Có thể dùng UI prototype cũ làm wireframe, không giữ API shape cũ.

### Chặng 5 — beta server

- PostgreSQL repository, authentication/authorization, organization/team/workspace.
- Server worker pool với isolated workspace, secret policy và quotas.
- Object storage cho artifact lớn.
- Không có đồng bộ với alpha; beta là deployment/data plane riêng.

## 14. Decision gate trước khi cam kết full build

Chỉ nên chốt kiến trúc sau một Go spike vượt qua các bài sau:

1. Publish workflow version mới không đổi run đang chạy.
2. Kill process sau node commit; process mới resume, không lặp side effect đã commit.
3. Một Project chứa nhiều repository; alpha UI chỉ mở một repository, hai root tasks trên repository
   đang active vẫn có worktree riêng và chạy song song.
4. Hai sibling cùng family tranh write lease; chỉ một writer được cấp.
5. Claude/Codex mock adapters tạo cùng canonical event sequence.
6. Provider session bị mất nhưng platform dựng lại context từ checkpoint/message state.
7. Agent tự trả `done=true`, hoặc gate list rỗng: WorkItem vẫn không `DONE`.
8. Gate evidence truy được command -> attempt -> node -> run -> WorkItem và exact revision.
9. Pack chứa file script không thể chạy nếu chưa có executable registration/policy.
10. Cùng application test suite chạy với SQLite; repository contract suite sẵn cho PostgreSQL.

Nếu spike không đạt, sửa model trước khi xây UI hay thêm provider.

## 15. Quyết định đã chốt và những câu còn mở

1. **Đã chốt:** domain hỗ trợ `Project -> Repository[]`; UI alpha chỉ mở một repository. Alpha giới
   hạn một WorkItem/TaskFamily vào một repository nhưng dùng `RepositoryScope`/`WorkspaceSet` dạng
   collection từ đầu. Multi-repository task execution chưa nằm trong phạm vi alpha.
2. Definition ban đầu nên nằm trong file declarative có review bằng git, hay tạo trong DB qua API?
   Đề xuất khởi đầu bằng file declarative + publish/compile, UI designer đến sau.
3. Khi root task hoàn tất, Agent Kit chỉ tạo commit/branch để người merge, hay được phép tự merge
   theo policy?
4. Chat canonical cần giữ toàn bộ message hay chỉ message đã chọn + ContextSnapshot? Provider raw
   transcript giữ bao lâu?
5. Alpha ưu tiên Windows trước hay phải chạy đồng đều Windows/Linux ngay từ slice đầu?
6. UI mới có cần giữ HTML/JS thuần để ra nhanh, hay chọn framework ngay khi API/domain ổn định?

Khuyến nghị hiện tại là **greenfield Go core + tái sử dụng tri thức có chọn lọc**. Chỉ thay đổi
khuyến nghị này nếu phạm vi sản phẩm được thu nhỏ đáng kể về đúng mô hình prototype hiện tại.
