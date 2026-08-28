# Lec 07 — Kiểm soát scope và WIP

> Nguồn: [Lecture 07 — Draw Clear Task Boundaries for Agents](https://github.com/walkinglabs/learn-harness-engineering/blob/77e7a3e21469dcbece2558086c8d91657abeaa40/docs/en/lectures/lecture-07-why-agents-overreach-and-under-finish/index.md)

## Luận điểm

Agent dễ mở rộng phạm vi vì chi phí nảy sinh thêm ý tưởng gần như bằng không. Nhiều thay đổi nửa vời làm giảm verified completion. Harness phải tạo completion pressure: một lane hoàn thành hành vi hiện tại bằng evidence trước khi mở thêm mutable work.

WIP=1 không có nghĩa Agent Kit chỉ chạy một task. Quy tắc được áp theo nơi chia sẻ mutable state:

- Nhiều task family/worktree: có thể song song.
- Cùng task family/worktree: mặc định một writer.
- Sibling subtasks: song song khi path scopes không giao nhau hoặc là read-only.

## Tiêu chí bắt buộc

- **HE-07-M01 — Explicit scope:** WorkItem MUST có target repository/component/path, out-of-scope và observable behavior.
- **HE-07-M02 — Executable completion:** mỗi work unit MUST có verification recipe hoặc approval criterion trước activation.
- **HE-07-M03 — Workspace WIP policy:** mỗi task family MUST có concurrency policy và write-lease semantics.
- **HE-07-M04 — Scope guard:** changed paths, dependency/config changes và spawned work MUST được đối chiếu scope trước transition.
- **HE-07-M05 — No hidden extras:** agent MUST không tự kích hoạt work item khác chỉ vì phát hiện việc liên quan; nó tạo proposal/finding riêng.
- **HE-07-M06 — Dependency guard:** subtask MUST không chạy khi prerequisite chưa đạt outcome mà edge/parent policy yêu cầu.
- **HE-07-M07 — Controlled decomposition:** task quá lớn MUST được chia thành child WorkItems có ownership, acceptance và parent join policy rõ.
- **HE-07-M08 — Integration ownership:** khi siblings cùng worktree, parent/integration node MUST sở hữu verification toàn family.

## Tiêu chí nên có

- **HE-07-S01:** Work unit SHOULD hoàn thành được trong một execution budget/session điển hình.
- **HE-07-S02:** Engine SHOULD tính path overlap trước khi cấp parallel write leases.
- **HE-07-S03:** Out-of-scope change SHOULD bị chặn hoặc yêu cầu human approval, không chỉ cảnh báo trong prompt.
- **HE-07-S04:** Hệ thống SHOULD theo dõi activated-to-verified ratio thay vì số dòng/file tạo ra.
- **HE-07-S05:** Parent SHOULD giới hạn số child đang active theo review/runner capacity.

## Scope schema tối thiểu

```text
behavior
acceptance/verification
target_components[]
allowed_paths[]
forbidden_paths[]
dependencies[]
parent_work_item_id
task_family_id
workspace_policy
```

## Evidence và chỉ số

- Verified completion rate.
- Activated/verified work ratio.
- Scope-drift rate và changed-files outside scope.
- Cycle time theo work unit.
- Sibling path-collision và merge-conflict rate.
- Worktree write-lease wait time.
- Số proposal ngoài scope được tách thành WorkItem thay vì sửa lén.

## Ánh xạ vào Agent Kit

- WorkItem scope contract.
- Resource lease theo `task_family/worktree/path`.
- `ScopeGate` so diff với allowed/forbidden paths.
- `SPAWN_WORK_ITEMS` block tạo child có schema.
- Parent join/integration gate.
- Kanban hiển thị parent-child và writer đang giữ lease.

## Anti-patterns

- Một prompt “xây toàn bộ social network”.
- Dùng tổng line count như năng suất.
- Cho siblings cùng sửa shared files song song không lease.
- Mọi phát hiện liên quan đều được “tiện thể sửa”.
- Chia task theo file kỹ thuật quá nhỏ mà không có hành vi quan sát được.
- Toàn hệ thống WIP=1 dù các worktree độc lập.

## Phép thử chấp nhận

1. Task chỉ cho phép `services/user/**`; agent sửa `infra/k8s/**`: ScopeGate phải fail.
2. Hai task family sửa cùng path nhưng khác worktree: được chạy song song.
3. Hai sibling subtasks trong một family yêu cầu cùng path: writer thứ hai phải chờ lease.
4. Agent phát hiện refactor ngoài scope: hệ thống tạo finding/proposal mà không đổi code.
