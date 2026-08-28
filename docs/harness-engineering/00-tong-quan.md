# Bộ tiêu chí nền tảng cho Agent Kit

> Trạng thái: bản cơ sở để thảo luận và kiểm chứng, chưa phải thiết kế triển khai cuối cùng.
>
> Nguồn tổng hợp: [walkinglabs/learn-harness-engineering tại commit `77e7a3e`](https://github.com/walkinglabs/learn-harness-engineering/tree/77e7a3e21469dcbece2558086c8d91657abeaa40). Bộ tài liệu diễn giải lại bằng ngôn ngữ của Agent Kit; không sao chép nguyên văn lecture và không coi các con số minh họa trong khóa học là cam kết hiệu năng.
>
> Bài học từ prototype hiện có: [Kinh nghiệm từ `claude-workflow` để xây Agent Kit](../danh-gia-claude-workflow.md).
>
> Mô hình domain đã thống nhất: [Project, Repository, TaskFamily và WorkspaceSet](../architecture/01-project-repository-workspace-model.md).
>
> Các lựa chọn cần xác nhận trước Go spec: [Architecture decisions cho Agent Kit](../architecture/02-architecture-decisions.md).

## 1. Mục đích

Bộ tài liệu này biến triết lý của 14 lecture thành tiêu chí có thể dùng để:

- thiết kế domain và kiến trúc Agent Kit;
- quyết định một capability thuộc instruction, state, environment, feedback hay orchestration;
- viết acceptance criteria cho từng phần của harness;
- đánh giá một run bằng evidence thay vì lời tự khai của agent;
- chẩn đoán thất bại và cải tiến harness theo vòng lặp có số liệu.

Các từ khóa chuẩn:

- **MUST**: thiếu tiêu chí này thì capability chưa được coi là hoàn chỉnh.
- **SHOULD**: mặc định phải có; chỉ bỏ khi ghi rõ lý do và đánh đổi.
- **MAY**: tùy chọn, chỉ thêm khi giá trị lớn hơn chi phí vận hành.

## 2. Các quyết định sản phẩm đã xác nhận

1. Không xây theo BPMN và không đặt mục tiêu tương thích/import/export BPMN.
2. Alpha là sản phẩm local phục vụ cá nhân. Beta là một sản phẩm triển khai tập trung riêng; không có đồng bộ hai chiều với alpha.
3. Ở beta, Codex CLI, Claude CLI và runtime tương lai chạy trên hạ tầng server/worker của hệ thống.
4. `Skill` và `Engineering Pack` chỉ chứa instruction/resource. Code hoặc script có thể thực thi thuộc executor/block/plugin được kiểm soát riêng.
5. Hệ thống cho phép nhiều task hoặc nhánh chạy song song.
6. Mỗi task gốc có một worktree riêng. Các subtask sinh từ task gốc kế thừa cùng một worktree, tạo thành một **task family**.
7. Trong cùng task family, nhiều subtask chỉ được ghi song song khi phạm vi path không giao nhau và engine cấp lease phù hợp; mặc định là một writer tại một thời điểm.
8. Domain và runtime hỗ trợ `Project -> Repository[]` ngay từ alpha. Kanban/task detail hoạt động ở cấp Project; source/diff/log/terminal panel có thể focus một repository tại một thời điểm.
9. Một WorkItem/TaskFamily có thể scope nhiều repository. Mỗi root family sở hữu một `WorkspaceSet` gồm một worktree cho từng repository trong scope; các subtask kế thừa cùng WorkspaceSet.

## 3. Định nghĩa harness dùng trong Agent Kit

Harness là toàn bộ hệ thống bên ngoài model giúp một agent run trở nên có thể vận hành, kiểm chứng, tiếp tục và kiểm soát được.

Năm phân hệ nền tảng:

| Phân hệ | Trả lời câu hỏi | Thành phần dự kiến |
|---|---|---|
| Instructions | Agent phải hiểu và tuân theo điều gì? | skill, resource, rule, context route |
| Tools/Capabilities | Agent được phép làm gì? | CLI adapter, tool policy, capability registry |
| Environment | Agent chạy ở đâu và trên trạng thái nào? | runner, worktree, runtime/toolchain, secret reference |
| State | Điều gì phải sống qua lần chạy/session? | work item, run, checkpoint, context snapshot, artifact |
| Feedback | Điều gì chứng minh đúng/sai và bước tiếp theo? | gate, evidence, evaluator, actionable diagnostics |

`Scope`, `Lifecycle`, `Security`, `Observability` và `Orchestration` là các mối quan tâm xuyên suốt năm phân hệ, không phải prompt bổ sung.

## 4. Meta-model tối thiểu

```text
Workspace
  -> Project
       -> Repository[]
            -> Component[]
                 -> EngineeringPackVersion[]

WorkItem
  -> TaskFamily
       -> RepositoryScope[] -> WorkspaceSet
       -> WorkflowRun (pins WorkflowVersion)
            -> NodeRun (pins Block/Skill/Pack/Agent profile snapshot)
                 -> ExecutionAttempt
                      -> AgentSession / CommandExecution
```

Các khái niệm không được đồng nhất:

- `WorkItem`: card nghiệp vụ trên Kanban.
- `WorkflowRun`: một lần WorkItem đi qua graph.
- `NodeRun`: vị trí thực thi hoặc chờ hiện tại.
- `ExecutionAttempt`: một lần thử kỹ thuật; retry tạo attempt mới.
- `AgentSession`: session riêng của provider, chỉ là metadata runtime.
- `Job`: đơn vị queue/lease nội bộ, không phải task nghiệp vụ.

## 5. Definition plane và runtime plane

### Definition plane

- `BlockDefinition` và `BlockVersion`.
- `WorkflowDefinition` và `WorkflowVersion`.
- `SkillVersion`.
- `EngineeringPackVersion`.
- `AgentProfileVersion`.
- Context, gate, permission và verification policies.

Version đã publish phải bất biến. Sửa definition tạo version mới. Run đang chạy không tự đổi sang version mới.

### Runtime plane

- WorkItem, quan hệ cha-con và target component/path.
- WorkflowRun, NodeRun, Attempt, job và lease.
- Event, transition, blocker và approval.
- Conversation, Message và ContextSnapshot.
- Artifact và Evidence.
- Runner, workspace/worktree và provider session.

Chỉ engine được ghi trạng thái authoritative. Agent có thể trả signal hoặc đề nghị outcome, nhưng không được tự chuyển task sang `DONE` hay tự khai gate `PASS`.

## 6. Skill, resource và executable extension

### Skill/resource

Skill trong phạm vi hiện tại là nội dung thụ động:

- instruction và quy trình;
- convention/rule;
- ví dụ, template, checklist;
- tài liệu tham khảo và metadata chọn context.

Skill không tự chạy command, sửa file, gọi network hay truy cập database.

### Executable extension

Nếu một package có code/script có thể scaffold, format, migrate, chạy test, gọi API hoặc tạo side effect thì nó là executable extension. Loại này cần trust, permission, sandbox, timeout, checksum/signature, audit, secret policy và idempotency. Agent Kit sẽ mô hình hóa chúng dưới block/executor riêng như `COMMAND`, `MACHINE_GATE`, `SERVICE_CALL` hoặc CLI adapter, không trộn vào Skill.

## 7. Chính sách concurrency và worktree

```text
Project -> Repository A, Repository B, ...

Task A family -> WorkspaceSet
   |- Repository A -> Worktree A-user
   `- Repository B -> Worktree A-web

Subtasks cùng family dùng lại WorkspaceSet A
  |- Subtask A1
  |- Subtask A2
  `- Subtask A3

Task B family -> WorkspaceSet riêng
   |- Repository A -> Worktree B-user
   `- Repository B -> Worktree B-web
```

Quy tắc nền:

- Các task family khác nhau có thể chạy song song vì có worktree riêng.
- Subtask trong cùng family dùng chung repository state và lịch sử thay đổi.
- Mặc định chỉ một subtask có write lease trên worktree tại một thời điểm.
- Có thể mở write song song khi engine chứng minh các path scope không giao nhau.
- Read-only analysis/review có thể chạy song song với writer nếu dùng snapshot/commit ổn định.
- Merge, rebase, release worktree và completion của parent là thao tác do orchestrator quản lý.
- Mọi workspace, lease, revision và evidence đều mang `repository_id`; không suy repository từ path hoặc process hiện tại.
- Write lease mặc định theo từng `RepositoryWorkspace`; sibling có thể ghi song song ở hai repository khác nhau nhưng cùng repository phải serialize.

WIP=1 trong Lecture 07 được áp dụng theo **execution lane có shared mutable state**, không áp dụng thành “toàn hệ thống chỉ chạy một task”.

## 8. Alpha và beta

### Alpha

```text
Local Web UI -> Local API/Control Plane -> durable job -> Local Runner
                                              |          -> CLI adapter
                                           SQLite
                                              |
                                       filesystem artifacts
```

Alpha nên là modular monolith nhưng có ranh giới API, runner và persistence port rõ ràng. Có thể chạy ở mode `all`, song core không được truy cập CLI trực tiếp bỏ qua runner.

### Beta

- PostgreSQL là runtime source of truth.
- Object storage lưu log/artifact lớn.
- API/control plane stateless ở mức có thể scale ngang.
- Server worker pool chạy CLI trong workspace cô lập.
- Login, organization/workspace, team, role và permission.
- Không có replication hoặc conflict resolution với database alpha.

## 9. Nguồn sự thật theo từng concern

Không dùng khẩu hiệu “một nguồn duy nhất” để ép mọi dữ liệu vào cùng một nơi. Mỗi concern có đúng một nguồn authoritative:

| Concern | Nguồn authoritative |
|---|---|
| Code, kiến trúc, convention của project | Git repository tại commit cụ thể |
| Workflow runtime, assignment, approval, audit | Agent Kit database |
| Log/output/file lớn | Artifact store, DB giữ metadata và hash |
| Provider session | Provider adapter metadata; không phải nguồn context chuẩn |
| Trạng thái hiển thị Kanban | Projection từ WorkItem/Run/NodeRun trong DB |

File task/progress trong repo, nếu tạo, là artifact hoặc projection; không được cùng DB sửa độc lập một trường trạng thái.

## 10. Chuẩn hoàn chỉnh xuyên suốt

Một harness hoàn chỉnh MUST:

1. Có intent và Definition of Done có cấu trúc trước khi dispatch.
2. Lắp đủ instruction, capability, environment, state và feedback.
3. Resolve context theo nhu cầu và lưu manifest của context thực tế.
4. Chạy trong workspace/worktree có provenance rõ.
5. Persist checkpoint đủ để khôi phục sau process crash.
6. Có retry budget, timeout, no-progress detector và đường escalation.
7. Tách worker/maker khỏi checker đối với completion có rủi ro.
8. Chỉ chuyển pass/done khi evidence authoritative hợp lệ.
9. Ghi event và trace xuyên suốt WorkItem -> Run -> Node -> Attempt -> Tool/Gate.
10. Để lại trạng thái có thể tiếp tục và không làm suy giảm baseline của repository.
11. Có benchmark cố định để đánh giá thay đổi harness.
12. Ghi version/hash của workflow, block, skill, pack, policy, adapter và source revision.

## 11. Bộ phép thử chấp nhận toàn hệ thống

Một implementation chỉ được gọi là “harness hoàn chỉnh” khi vượt qua ít nhất các phép thử sau:

- **Fresh-run test**: runner sạch hiểu project, khởi động và tìm được verification command mà không cần giải thích miệng.
- **Evidence test**: agent nói “done” nhưng thiếu evidence thì WorkItem không chuyển trạng thái.
- **Crash-resume test**: kill worker giữa node; worker mới tiếp tục từ checkpoint mà không lặp side effect đã commit.
- **Provider-loss test**: mất provider session ID vẫn dựng lại được context từ platform-owned state.
- **Parallel isolation test**: hai task family sửa repo đồng thời không ghi vào cùng worktree.
- **Family coordination test**: sibling subtasks dùng chung worktree không ghi chồng path khi chưa có lease.
- **Context audit test**: xem được chính xác resource/message/version nào đã đi vào một attempt.
- **Gate provenance test**: evidence chứa command, exit code, revision, cwd/target, timestamps và artifact hash.
- **Clean handoff test**: task family có thể bàn giao cho runner/session mới mà không cần đọc transcript cũ.
- **Definition version test**: publish workflow version mới không làm thay đổi run đang thực thi.

## 12. Chỉ số nền

Không dùng số dòng code hoặc số token sinh ra làm chỉ số thành công. Theo dõi:

- verified completion rate;
- premature-completion rate;
- defect escape rate;
- scope-drift rate;
- recovery/rebuild time;
- retry và no-progress rounds;
- queue/run/wait/approval time;
- context tokens và tỷ lệ context thực sự liên quan;
- cost trên mỗi WorkItem đã được xác minh;
- stale knowledge/evidence rate;
- human review load và merge-conflict rate.

## 13. Danh mục 14 lecture

1. [Lec 01 — Độ tin cậy không đến từ model mạnh hơn](01-lec-01-do-tin-cay-khong-den-tu-model.md)
2. [Lec 02 — Harness là năm phân hệ](02-lec-02-nam-phan-he-harness.md)
3. [Lec 03 — Repository và nguồn sự thật](03-lec-03-repository-va-nguon-su-that.md)
4. [Lec 04 — Progressive disclosure cho instruction](04-lec-04-progressive-disclosure.md)
5. [Lec 05 — Liên tục qua session](05-lec-05-lien-tuc-qua-session.md)
6. [Lec 06 — Khởi tạo là một phase riêng](06-lec-06-khoi-tao-la-phase-rieng.md)
7. [Lec 07 — Kiểm soát scope và WIP](07-lec-07-kiem-soat-scope-va-wip.md)
8. [Lec 08 — Work item là primitive điều phối](08-lec-08-work-item-la-primitive.md)
9. [Lec 09 — Không cho agent tự tuyên bố hoàn thành](09-lec-09-khong-tu-tuyen-bo-hoan-thanh.md)
10. [Lec 10 — Kiểm chứng full pipeline](10-lec-10-kiem-chung-full-pipeline.md)
11. [Lec 11 — Observability nằm trong harness](11-lec-11-observability-noi-tai.md)
12. [Lec 12 — Clean handoff và chống entropy](12-lec-12-clean-handoff.md)
13. [Lec 13 — Vòng lặp tự động](13-lec-13-vong-lap-tu-dong.md)
14. [Lec 14 — Đồ thị điều phối](14-lec-14-do-thi-dieu-phoi.md)

## 14. Cách dùng bộ tài liệu khi build

Mỗi feature thiết kế của Agent Kit cần chỉ ra:

1. Nó hiện thực tiêu chí `HE-xx-*` nào.
2. Evidence nào chứng minh tiêu chí đã được đáp ứng.
3. Failure mode nào vẫn còn mở.
4. Tiêu chí nào bị hoãn sang beta và vì sao.
5. Benchmark hoặc recovery drill nào sẽ phát hiện hồi quy.

Không thêm capability chỉ vì một lecture nhắc tới. Capability chỉ được đưa vào roadmap khi có failure mode thật, acceptance test rõ và chi phí vận hành chấp nhận được.
