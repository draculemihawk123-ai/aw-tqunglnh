# Architecture decisions cho Agent Kit

> Trạng thái: PROPOSED — chờ product owner xác nhận trước khi viết Go core spec.
>
> Ngày lập baseline: 2026-08-28.

## 1. Các ràng buộc đã xác nhận

Những quyết định này đã được thống nhất và không còn là câu hỏi mở:

1. Không xây BPMN và không đặt mục tiêu tương thích BPMN.
2. Alpha là sản phẩm local cho cá nhân; beta là deployment tập trung riêng, không đồng bộ alpha.
3. Beta chạy Claude CLI, Codex CLI và provider tương lai trên server worker.
4. Skill/Layer chỉ chứa instruction/resource; executable capability được đăng ký riêng.
5. Nhiều root task có thể chạy song song và có workspace riêng.
6. Subtask kế thừa TaskFamily/WorkspaceSet của root task.
7. Project chứa nhiều Repository; TaskFamily có thể scope nhiều RepositoryWorkspace.
8. UI quản lý ở cấp Project; source/diff/log/terminal panel focus từng repository.

## 2. ADR-001 — Authoring và publish workflow definition

**Đề xuất:** authoring bằng file declarative có thể review bằng Git; publish qua CLI/API để validate,
canonicalize, hash và lưu một WorkflowVersion bất biến trong database.

- File authoring là nguồn để con người review/chỉnh sửa.
- Published snapshot trong database là runtime authority.
- WorkflowRun luôn pin version_id và content hash.
- Sửa file không thay đổi run đang chạy cho đến khi publish version mới.
- UI workflow designer sau này gọi cùng publish contract, không tạo semantics khác.

Lý do: vừa giữ review/diff tốt ở alpha, vừa cho runtime bền vững và quản lý tập trung ở beta.

## 3. ADR-002 — Mở rộng repository scope trong khi run đang chạy

**Đề xuất:** cho phép add-only bằng operation tường minh; không tự thêm và không remove repository
khỏi TaskFamily đang chạy.

Flow:

1. Agent/node phát ScopeExpansionRequested kèm lý do và READ/WRITE access cần thiết.
2. Orchestrator block node.
3. Operator phê duyệt.
4. Hệ thống provision RepositoryWorkspace, tăng scope version và tạo RevisionSet mới.
5. Node retry bằng attempt mới.

Mở rộng scope là mở rộng quyền và context nên không được ẩn trong prompt hay adapter.

## 4. ADR-003 — Mutating attempt xuyên repository

**Đề xuất:** mặc định mỗi mutating attempt chỉ có một RepositoryWorkspace read-write; các repository
khác trong effective scope được expose read-only.

Một integration node có thể ghi nhiều repository khi:

- definition khai báo multi-repository write capability;
- policy cho phép;
- worker acquire batch WriteLease cho toàn bộ repository đích;
- output tạo RevisionSet và evidence riêng cho từng repository.

Không dùng prompt để thay thế filesystem permission và diff enforcement.

## 5. ADR-004 — Release xuyên repository và Git authority

**Đề xuất:** dùng correlated ReleaseSet, không giả định atomic commit/merge xuyên Git repository.

Mỗi repository có branch/commit/pull request và gate riêng. Parent chỉ DONE khi mọi repository bắt
buộc đạt release policy. Partial failure đưa family về BLOCKED hoặc compensation flow; không ghi
nhãn rollback thành công nếu chưa có evidence.

Quyền alpha:

- worker được tạo local branch/commit khi workflow policy cho phép;
- push và tạo pull request chỉ từ operator command rõ ràng;
- merge luôn cần human approval;
- không auto-force-push hoặc rewrite remote history.

## 6. ADR-005 — Conversation và context ownership

**Đề xuất:** platform giữ canonical conversation message và context snapshot; provider transcript
chỉ là artifact hỗ trợ debug.

- Message là append-only, có actor, timestamp và correlation với WorkItem/Attempt.
- ContextSnapshot lưu manifest chính xác của message/resource/revision được chọn.
- ProviderSessionRef chỉ tối ưu resume và có thể mất.
- Alpha giữ dữ liệu local cho đến khi người dùng cleanup/project deletion.
- Beta áp retention policy theo organization; raw provider event có TTL riêng.

Mất provider session không được làm mất khả năng dựng lại context từ platform state.

## 7. ADR-006 — Operating systems

**Đề xuất:** core contract hỗ trợ Windows và Linux từ đầu.

- Spike đầu chạy trên Windows là môi trường hiện tại.
- Process, path, signal và worktree nằm sau adapter; domain không chứa OS path semantics.
- Trước khi alpha được coi là usable, core test suite phải pass trên Windows và Linux.
- Beta worker target mặc định là Linux container.

Không yêu cầu mọi shell/toolchain layer chạy đa nền tảng; layer phải khai báo compatibility.

## 8. ADR-007 — Provider và executable extension

**Đề xuất:** dùng versioned process/protocol adapter, không dùng Go dynamic plugin làm extension
boundary chính.

- AgentExecutor chuẩn hóa start/resume/cancel/event/outcome.
- Claude/Codex adapter chỉ dịch provider protocol.
- Command/Gate/Scaffold executor chạy ngoài UI/API process.
- Adapter khai capability và version; domain không có nhánh if provider == Claude/Codex.
- Skill/Layer resource không có quyền thực thi mặc định.

## 9. ADR-008 — Persistence, event và projection

**Đề xuất:** state tables là runtime authority, kèm append-only audit/domain events và durable job/
outbox ghi cùng transaction; không áp full event sourcing cho alpha.

- SQLite adapter ở alpha, PostgreSQL adapter ở beta.
- Repository contract suite chạy cho cả hai implementation.
- Kanban/task detail là projection/read model có thể rebuild.
- Expected version bảo vệ optimistic concurrency.
- HTTP API nằm ở application boundary, không mô phỏng database Store qua HTTP.

Cách này giữ recovery/audit mà không buộc toàn bộ domain phải replay event để dựng state.

## 10. ADR-009 — Workflow graph, retry và rework

**Đề xuất:** WorkflowVersion là typed directed graph.

- Technical retry nằm trong AttemptPolicy của node và luôn tạo attempt mới.
- Business rework là edge tường minh trong graph.
- Cycle chỉ hợp lệ khi có iteration budget và escalation edge.
- Fork/join phải khai join policy rõ; không suy hoàn thành từ việc queue trống.
- Definition publish phải reject node/edge/type không hợp lệ và unreachable terminal path.

## 11. ADR-010 — UI technology

**Đề xuất:** cố ý chưa chọn Angular/React hay UI stack trước khi Go spike đạt.

Các contract được khóa trước:

- UI chỉ gọi application API;
- Kanban là projection của WorkItem/Run/NodeRun;
- task detail hiển thị graph, WorkspaceSet, evidence, blocker và chat;
- UI không spawn CLI/Git.

Sau spike sẽ làm một UI decision riêng dựa trên tốc độ MVP, component library và năng lực team.
Prototype cũ chỉ là wireframe/reference, không khóa API hay framework.

## 12. Confirmation gate

Go core spec chỉ bắt đầu sau khi các ADR-001 đến ADR-010 được xác nhận hoặc sửa.

Baseline được đề nghị chốt là:

1. File declarative để author; database giữ published immutable version.
2. Scope expansion add-only, cần operator approval.
3. Một repository read-write mỗi attempt; multi-repo write chỉ ở integration node.
4. ReleaseSet không atomic; push/PR operator-triggered, merge human-approved.
5. Platform sở hữu conversation/context; provider session chỉ là optimization.
6. Core Windows/Linux; beta worker Linux.
7. Process/protocol adapters; không Go dynamic plugin.
8. State tables + append-only event/outbox, không full event sourcing.
9. Retry kỹ thuật thuộc AttemptPolicy; business rework thuộc graph.
10. Hoãn chọn UI framework đến khi spike đạt.
