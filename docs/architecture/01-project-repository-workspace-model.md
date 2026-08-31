# Mô hình Project, Repository, TaskFamily và WorkspaceSet

> Trạng thái: baseline domain đã thống nhất cho Agent Kit.
>
> Phạm vi: mô hình đa repository, ownership của worktree, scope thực thi và boundary UI.
> Release policy, quyền push/merge và mở rộng scope giữa run được quyết định ở ADR riêng.

## 1. Quyết định nền

Agent Kit coi dự án nhiều repository là trường hợp bình thường:

    Project: Social Network
      |- user-service
      |- feed-service
      |- notification-service
      |- web-app
      `- deployment

UI quản lý task ở cấp Project. Source, diff và log read-only có thể focus một repository tại một
thời điểm, nhưng đây chỉ là cách hiển thị; domain và runtime không bị giới hạn một repository. Alpha
không có interactive terminal.

## 2. Meta-model

    Project
      -> Repository[]
           -> Component[]

    Root WorkItem
      -> TaskFamily
           -> RepositoryScope[]
           -> WorkspaceSet
                -> RepositoryWorkspace[]
                     -> Worktree
           -> ReleaseSet

    Child WorkItem
      -> same TaskFamily
      -> effective RepositoryScope là tập con của family scope

    WorkflowRun
      -> NodeRun
           -> ExecutionAttempt
                -> effective RepositoryScope[]
                -> RevisionSet

| Object | Trách nhiệm |
|---|---|
| Project | Boundary sản phẩm chứa một hoặc nhiều Repository |
| Repository | Identity ổn định của một Git repository thuộc Project |
| WorkItem | Card nghiệp vụ; root tạo family, child kế thừa family |
| TaskFamily | Boundary ownership của root task và toàn bộ descendant |
| RepositoryScope | Repository/path được phép đọc hoặc ghi |
| WorkspaceSet | Logical workspace đa repository của TaskFamily |
| RepositoryWorkspace | Worktree mutable của đúng một repository trong family |
| RevisionSet | Tập repository_id và exact revision dùng cho context/evidence |
| ReleaseSet | Kết quả local đã correlate/seal của từng repository; không phải transaction Git xuyên repo |

## 3. Semantics của scope và ownership

RepositoryScope tối thiểu có repository_id, access READ/WRITE, path_scope tùy chọn và reason.
Project có thể chứa nhiều repository hơn scope của task. WorkspaceSet chỉ provision repository thực
sự cần cho family, không tạo worktree cho toàn bộ Project.

Root WorkItem tạo đúng một TaskFamily và một WorkspaceSet. Child WorkItem:

- giữ nguyên family_id;
- dùng lại mapping RepositoryWorkspace của family;
- có effective scope là tập con của family scope;
- không tự tạo branch/worktree chỉ vì task được phân rã.

Một feature xuyên nhiều service nên được chia thành subtask một-repository khi có thể. Đây là cách
giảm shared mutable state, không phải giới hạn của domain.

## 4. RepositoryWorkspace và RevisionSet

RepositoryWorkspace cần lưu tối thiểu:

| Field | Ý nghĩa |
|---|---|
| workspace_set_id | WorkspaceSet sở hữu |
| repository_id | Repository tương ứng |
| worktree_locator | Locator do WorkspaceProvider quản lý |
| branch_ref | Branch làm việc nếu có |
| base_revision | Revision lúc cấp workspace |
| current_revision | Revision/checkpoint gần nhất |
| generation | Tăng khi recreate/rebase/reset có kiểm soát |
| state | provisioning, ready, blocked, releasing, released hoặc failed |

Unique identity logic là family_id + repository_id + generation.

RevisionSet là snapshot logic xuyên repository:

    [
      { repository_id: user-service, revision: abc123 },
      { repository_id: web-app, revision: def456 }
    ]

RevisionSet không hứa atomicity giữa các Git repository. Nó chỉ ghi chính xác tập revision mà
context, gate hoặc evidence đã sử dụng.

## 5. Invariant bắt buộc

1. Mọi Repository trong WorkspaceSet phải thuộc cùng Project với TaskFamily.
2. Root WorkItem tạo family; mọi descendant giữ nguyên family_id.
3. WorkspaceSet chỉ chứa repository nằm trong family RepositoryScope.
4. Mỗi family_id + repository_id + generation chỉ có một RepositoryWorkspace writable.
5. Hai TaskFamily không dùng chung mutable worktree dù cùng repository.
6. Scope child/node/attempt phải là tập con của family scope.
7. Mọi command, diff, artifact và evidence liên quan code phải ghi repository_id.
8. Evidence kiểm chứng phải pin exact RevisionSet.
9. Thay workspace generation làm lease và fencing token cũ mất hiệu lực.
10. Completion parent phải xét toàn bộ repository bắt buộc trong scope.
11. Git operation xuyên repository không được mô tả như transaction nguyên tử.
12. Repository identity không được suy từ path, slug, cwd hoặc remote URL.

## 6. Lifecycle của WorkspaceSet

### Tạo root task

1. Tạo WorkItem và TaskFamily trong cùng application transaction.
2. Resolve RepositoryScope ban đầu.
3. Tạo WorkspaceSet.
4. Provision một RepositoryWorkspace cho từng repository nằm trong scope.
5. Pin base RevisionSet trước khi bắt đầu WorkflowRun.

### Tạo subtask

1. Tạo child WorkItem với cùng family_id.
2. Resolve effective scope là tập con của family scope.
3. Reuse RepositoryWorkspace hiện có.
4. Không tạo workspace riêng cho child.

### Kết thúc family

1. Dừng hoặc đợi mọi job và lease còn active.
2. Chạy integration/release gate trên RevisionSet cuối.
3. Giữ artifact/evidence cần thiết.
4. Thực hiện release theo policy.
5. Seal ReleaseSet local hoặc ghi explicit abandon decision theo policy.
6. Chỉ cleanup khi không còn nhu cầu recovery và ReleaseSet đã seal hoặc abandon.

## 7. Concurrency, lease và runner

Cần phân biệt JobLease của durable job với WriteLease của mutable workspace.

WriteLease alpha mặc định exclusive theo family_id + repository_id + generation. Vì vậy:

- hai sibling ghi hai repository khác nhau có thể chạy song song;
- hai sibling cùng ghi một RepositoryWorkspace phải serialize;
- lease có TTL, heartbeat và fencing token;
- token cũ không được commit sau expiry hoặc generation change;
- checker read-only dùng pinned revision/snapshot và chỉ ghi scratch ngoài source workspace.

Mặc định một mutating attempt mount repository mục tiêu read-write và các repository tham chiếu
read-only. Runner kiểm diff sau execution không vượt write scope.

Integration attempt cần ghi nhiều repository phải khai báo capability
`INTEGRATION_MULTI_REPOSITORY_WRITE` và acquire batch lease.
PathScope alpha dùng để giới hạn quyền và kiểm diff; concurrent writers cùng một worktree để sau.

## 8. Boundary UI alpha

### Project và Kanban

- Hiển thị mọi WorkItem trong Project.
- Có filter repository/component nhưng filter không đổi scope task.
- Task multi-repository vẫn là một card, có badge repository.

### Tạo task và task detail

- Repository selector mặc định theo repository đang focus nhưng cho chọn nhiều repository.
- Hiển thị READ/WRITE scope, WorkspaceSet, revision, lease và blocker theo repository.
- Graph hiển thị repository scope của từng node.

### Source, diff, log và chat

- Mỗi panel focus/tab một repository tại một thời điểm.
- Chuyển tab không ảnh hưởng runtime hoặc scope.
- File/context đưa vào chat luôn giữ repository_id.
- UI không trực tiếp chạy Git hoặc agent CLI; chỉ gửi application command.
- UI Alpha không có interactive browser terminal theo ADR-018; terminal capability cần policy/isolation
  và audit riêng trước khi được bổ sung.

## 9. Failure và recovery

| Failure | Hành vi bắt buộc |
|---|---|
| Provision một repository thất bại | WorkspaceSet chưa ready; không chạy node cần repository đó |
| Worker chết khi giữ lease | Lease hết hạn; token cũ bị fence; read-only Attempt `LOST`, mutating Attempt `INDETERMINATE` |
| Worktree bị recreate | Tăng generation; lease/cache cũ mất hiệu lực |
| Multi-repo operation fail một phần | Ghi partial outcome; không tuyên bố atomic rollback |
| Diff vượt write scope | Attempt fail/block và giữ evidence vi phạm |
| Revision không khớp | Revalidate/rebase bằng operation riêng |
| Cleanup gặp job/lease active | Từ chối cleanup |

Recovery dựa trên database state, workspace generation và RevisionSet; không dựa vào current
directory hay provider session còn tồn tại.

## 10. Acceptance criteria

1. Một Project đăng ký được ít nhất hai repository.
2. Root task scope hai repository tạo một family và WorkspaceSet có hai worktree.
3. Hai child cùng family nhìn thấy cùng WorkspaceSet.
4. Child scope một repository không được ghi repository còn lại.
5. Hai root task cùng repository nhận hai worktree khác nhau.
6. Sibling khác repository có thể giữ WriteLease đồng thời.
7. Sibling cùng RepositoryWorkspace không thể cùng giữ WriteLease.
8. Fencing token cũ không thể commit.
9. Evidence xuyên repository chứa exact RevisionSet.
10. Kanban hiển thị task multi-repository; source/diff/log panel focus từng repository.
11. UI không cần spawn Git/CLI.
12. Restart process dựng lại được family, WorkspaceSet, lease state và provenance.
13. Checker không ghi được source workspace; multi-repository writer thiếu integration capability bị chặn.
14. Cleanup bị chặn cho tới khi ReleaseSet local đã seal hoặc có abandon decision.

## 11. Ranh giới với tài liệu tiếp theo

Các architecture decision hiện hành đã quyết định:

- mở rộng RepositoryScope bằng append-only RunManifestAmendment và NodeRun activation mới;
- mutating node mặc định một repository, multi-repository chỉ integration capability;
- Alpha có ReleaseSet/local commit, không có push/PR/merge executor;
- authoring/publish WorkflowDefinition dùng compiled snapshot hash;
- conversation/context có retention class riêng; core hỗ trợ Windows/Linux;
- UI Alpha có source/diff/log read-only, không có interactive terminal.

Go core spec phải triển khai đúng model và invariant này. Spike phải chứng minh workspace isolation,
lease/fencing và RevisionSet trước khi bắt đầu alpha UI/runtime.
