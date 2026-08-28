# Lec 14 — Đồ thị điều phối

> Nguồn: [Lecture 14 — From Single Loops to Graph Engineering](https://github.com/walkinglabs/learn-harness-engineering/blob/77e7a3e21469dcbece2558086c8d91657abeaa40/docs/en/lectures/lecture-14-graph-engineering/index.md)

## Luận điểm

Khi specialization, parallelism, shared state, verification, approval và recovery trở thành yêu cầu, các quyết định từng bị giấu trong một loop cần được biểu diễn bằng graph có kiểu.

Agent Kit dùng graph nội bộ riêng, không theo BPMN. Giá trị không nằm ở hình vẽ mà ở khả năng version, validate, checkpoint, quan sát, resume và sửa cục bộ.

## Bốn thành phần

1. **Node:** work unit deterministic, agent-driven hoặc human.
2. **Edge:** handoff cùng outcome/condition.
3. **Shared state:** dữ liệu có schema được các node trao đổi.
4. **Routing:** quy tắc chọn edge kế tiếp.

Mỗi agent node có private context/session. Các node không chia toàn bộ transcript; chúng trao đổi qua typed state, artifact và evidence refs.

## Node types nền

- `START`, `END`.
- `AGENT` hoặc agent-loop.
- `COMMAND/SERVICE_CALL` deterministic.
- `MACHINE_GATE`.
- `HUMAN_TASK/APPROVAL`.
- `ROUTER`.
- `FORK`, `JOIN`.
- `WAIT/SIGNAL`.
- `SPAWN_WORK_ITEMS`.
- `SUBFLOW` khi có nhu cầu thật.

## Tiêu chí bắt buộc

- **HE-14-M01 — Immutable graph version:** WorkflowVersion đã publish MUST bất biến và pin toàn bộ dependency versions dùng khi run.
- **HE-14-M02 — Typed node contract:** mỗi node MUST có type, responsibility, input/output schema, allowed outcomes, tool/permission policy, timeout và done condition.
- **HE-14-M03 — Declared business routes:** mọi route nghiệp vụ, rework, rollback và terminal path MUST đi qua edge/outcome đã khai báo; agent không tự chọn node tùy ý. Retry kỹ thuật của cùng một node nằm trong `AttemptPolicy` có budget/idempotency và không cần biến thành graph edge.
- **HE-14-M04 — Shared-state schema:** mỗi field MUST có type, owner/writers/readers và merge rule; artifact lớn lưu bằng reference.
- **HE-14-M05 — Durable checkpoint:** engine MUST persist state/event sau transition bền vững; checkpoint không chỉ nằm trong RAM.
- **HE-14-M06 — Graph validation:** publish MUST kiểm start/end, reachability, dead ends, schema compatibility, cycles/budgets, route coverage và join semantics.
- **HE-14-M07 — Deterministic transition boundary:** model output MUST được normalize/validate thành typed outcome; engine mới áp routing rule.
- **HE-14-M08 — Independent verifier context:** verify node MUST dùng fresh/private context và chỉ nhận requirement, artifact/diff và evidence cần thiết.
- **HE-14-M09 — Parallel safety:** parallel write nodes MUST tuân worktree/task-family lease/path ownership và fencing.
- **HE-14-M10 — External anchor:** graph completion MUST gắn machine truth, user outcome, human approval hoặc ground-truth source bên ngoài self-consistent agent graph.

## Fork/join và task family

Fork phải khai báo đơn vị song song là:

- Các node đọc/đánh giá cùng snapshot.
- Child WorkItems trong cùng task family/worktree.

Một WorkItem được tách từ task cha luôn ở cùng task family và kế thừa worktree. Muốn có worktree khác thì phải tạo một root WorkItem độc lập, không gọi nó là child/subtask của family hiện tại.

Join phải khai báo `ALL`, `ANY`, `QUORUM` hoặc policy tùy chỉnh có version. Với child cùng worktree:

- write paths không giao nhau hoặc chỉ một writer;
- shared files/integration thuộc integration node;
- join chạy verification trên trạng thái tổng hợp hiện tại;
- failure của một child không rollback mù thay đổi đã verified của child khác.

## Khi graph là đáng giá

Graph SHOULD chỉ được dùng khi có nhiều tín hiệu sau:

1. Work có thể phân rã thành đơn vị độc lập.
2. Có branch, rollback hoặc approval paths đáng biểu diễn.
3. Intermediate state đáng checkpoint/resume.
4. Node outcomes có thể verify.
5. Lợi ích specialization/parallelism lớn hơn coordination và review cost.

Pipeline tuyến tính dài chưa chắc cần graph phức tạp; graph designer không phải mục tiêu alpha đầu tiên.

## Tiêu chí nên có

- **HE-14-S01:** Definition view và runtime execution SHOULD có conformance test; diagram/projection không được lệch graph version.
- **HE-14-S02:** Model-assisted routing SHOULD chỉ đề xuất một outcome trong allow-list; policy deterministic quyết định edge cuối.
- **HE-14-S03:** Human node SHOULD có requested evidence, authorized roles, timeout và escalation.
- **HE-14-S04:** Engine SHOULD đo orchestration tax trước khi tăng fan-out.
- **HE-14-S05:** Visual editor MAY đến sau declarative definition/compiler và runtime semantics ổn định.
- **HE-14-S06:** Running graph migration MAY được hỗ trợ sau, nhưng phải là thao tác explicit, audit và có node/state mapping; mặc định run cũ không migrate.

## Failure modes cấu trúc cần tránh

- Tối ưu metric nội bộ nhưng không cải thiện outcome thật.
- Không có node/role hỏi goal hay metric còn đúng không.
- Nhiều loops tối ưu mục tiêu xung đột.
- Shared state không có merge semantics.
- Verify dùng cùng context với maker.
- Checkpoint trong memory nên process chết là mất.
- Dùng agent cho bước deterministic đơn giản.
- Parallelism tăng nhanh hơn human review capacity.

## Evidence và chỉ số

- Node success/failure/retry/rollback rate.
- Route distribution và unhandled outcome count.
- Checkpoint recovery success/time.
- Graph-definition/runtime conformance.
- Parallel speedup trừ queue/coordination/reconciliation cost.
- Path collision/merge conflict rate.
- Human wait/reject rate.
- Review queue depth.
- Correlation giữa graph metric và external anchor.

## Ánh xạ luồng `00-luong.md`

- BRAINSTORM/SPEC/DESIGN/FRAME/PLAN/BUILD/SYNC -> activity nodes.
- Gate A/B và Gate 1/2 -> human hoặc machine gate nodes.
- Fast/Full -> router.
- Chia thành N task -> spawn child WorkItems.
- NEEDS_INFO -> standardized wait outcome/reason từ bất kỳ executable node.
- Context routing -> ContextAssembler, không phải node.
- Feature và Task -> WorkItem cha-con chạy WorkflowVersion khác nhau.

## Anti-patterns

- Vẽ graph trước khi có event/evidence/checkpoint đúng.
- Mọi node là một prompt tự do.
- Shared state chứa toàn bộ source code/transcript.
- Dynamic edge theo free-text model output.
- Retry cycle không budget.
- In-memory checkpointer được quảng bá là crash recovery.
- Thêm nhiều agent vì node rẻ nhưng bỏ qua review bottleneck.

## Phép thử chấp nhận

1. Publish graph có unreachable node, missing route hoặc unbounded cycle phải fail validation.
2. Kill server worker sau node completion; run resume từ durable checkpoint, không chạy lại node đã commit.
3. Hai parallel siblings cùng yêu cầu write lease trên path giao nhau: một node phải chờ hoặc definition bị từ chối theo policy.
4. Model trả outcome ngoài allow-list: node fail validation, không tự nhảy edge.
5. Publish WorkflowVersion mới không thay graph snapshot của run đang chạy.
