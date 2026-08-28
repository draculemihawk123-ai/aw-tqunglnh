# Lec 08 — Work item là primitive điều phối

> Nguồn: [Lecture 08 — Use Feature Lists to Constrain What the Agent Does](https://github.com/walkinglabs/learn-harness-engineering/blob/77e7a3e21469dcbece2558086c8d91657abeaa40/docs/en/lectures/lecture-08-why-feature-lists-are-harness-primitives/index.md)

## Luận điểm

Danh sách feature không chỉ là ghi chú. Nó là dữ liệu vận hành chung cho scheduler, verifier, handoff và progress UI. Trong Agent Kit, primitive tương ứng là `WorkItem` có schema, không phải một file JSON do agent tự sửa.

Ba thuộc tính tối thiểu của một đơn vị công việc vẫn được giữ:

1. Hành vi quan sát được.
2. Cách xác minh.
3. Trạng thái có thẩm quyền.

## WorkItem contract đề xuất

```text
identity + type + parent/task_family
behavior + acceptance
target components/paths + out-of-scope
workflow/version
business phase + runtime projection
blockers + next action
evidence refs + verified revision
ownership/priority
```

## Tiêu chí bắt buộc

- **HE-08-M01 — Structured primitive:** WorkItem MUST có schema version và các field behavior, acceptance, state, scope và provenance bắt buộc.
- **HE-08-M02 — One runtime authority:** Agent Kit database MUST là nguồn authoritative cho WorkItem/Run state; file trong repo chỉ là artifact/projection.
- **HE-08-M03 — Engine-controlled transition:** agent MUST không trực tiếp đặt state thành passing/done; nó chỉ submit result/evidence candidate.
- **HE-08-M04 — Evidence-bound verification:** trạng thái verified MUST gắn gate result, code/config revision và verification policy version.
- **HE-08-M05 — Shared consumers:** scheduler, verifier, handoff và Kanban MUST đọc cùng canonical WorkItem/Run model.
- **HE-08-M06 — Granularity check:** WorkItem thực thi MUST có hành vi đủ độc lập để verify và nằm trong execution budget hợp lý.
- **HE-08-M07 — Parent-child semantics:** child WorkItem MUST gắn source NodeRun/parent và parent completion/join policy.
- **HE-08-M08 — State audit:** mọi transition MUST ghi actor, cause, previous/new state, time và evidence/approval refs.

## Điều chỉnh so với lecture

Lecture trình bày pass-state như không thể đảo ngược. Trong hệ thống thực, evidence chỉ đúng cho một revision và policy cụ thể. Vì vậy:

- Không sửa/xóa lịch sử `VERIFIED` cũ.
- Khi code, dependency, workflow, pack hoặc verification policy liên quan thay đổi, projection hiện tại có thể thành `STALE` hoặc yêu cầu revalidation.
- Revalidation tạo evidence mới; không viết lại evidence cũ.

## Tiêu chí nên có

- **HE-08-S01:** WorkItem publish/activation SHOULD fail nếu behavior hoặc verification rỗng.
- **HE-08-S02:** JSON/schema validation SHOULD diễn ra ở API và definition compiler.
- **HE-08-S03:** Evidence freshness SHOULD được tính theo dependency/revision graph, không chỉ timestamp.
- **HE-08-S04:** UI SHOULD hiển thị khác nhau giữa “agent báo hoàn thành”, “gate verified” và “verified nhưng đã stale”.
- **HE-08-S05:** Handoff summary SHOULD được sinh từ state/evidence, không yêu cầu agent tự kể lại toàn bộ.

## Evidence và chỉ số

- Tỷ lệ WorkItem đủ behavior + verification + scope.
- Pass-without-evidence count; mục tiêu bằng 0.
- Stale-evidence rate.
- Duplicate/contradictory work item rate.
- Time từ active tới verified.
- Tỷ lệ scheduler/verifier/UI projection lệch canonical state.

## Ánh xạ vào Agent Kit

- `WorkItem`, `WorkItemRelation`, `Blocker`, `NextAction`.
- `VerificationSpec` và `Evidence` có version.
- `StateTransition` append-only.
- Projection Kanban và parent-child tree.
- `SPAWN_WORK_ITEMS` và join policy.
- Optional repo export chỉ đọc hoặc do engine materialize.

## Anti-patterns

- Dùng free-form note “gần xong”.
- Cho agent sửa `passes: true` và tự ghi evidence bằng prose.
- Một file feature list là scheduler DB, audit log và product spec cùng lúc.
- PASS tồn tại vĩnh viễn dù revision đã thay đổi.
- Work item quá lớn hoặc chỉ là thao tác kỹ thuật không có behavior.

## Phép thử chấp nhận

1. Submit WorkItem thiếu verification: API/activation phải từ chối.
2. Agent gửi `outcome=completed` không evidence: projection không thành verified.
3. Gate pass ở commit A; worktree đổi sang commit B liên quan target: evidence A được giữ nhưng current verification bị stale.
4. Parent task chỉ hoàn thành khi child join policy và integration evidence đều đạt.
