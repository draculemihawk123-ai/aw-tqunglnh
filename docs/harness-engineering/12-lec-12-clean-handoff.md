# Lec 12 — Clean handoff và chống entropy

> Nguồn: [Lecture 12 — Leave a Clean Handoff at the End of Every Session](https://github.com/walkinglabs/learn-harness-engineering/blob/77e7a3e21469dcbece2558086c8d91657abeaa40/docs/en/lectures/lecture-12-why-every-session-must-leave-a-clean-state/index.md)

## Luận điểm

Kết thúc session và release/merge task family là các boundary giao dịch khác nhau. Session boundary cần checkpoint có thể tiếp tục; release boundary mới cần clean-state gate đầy đủ. Một node chỉ trở thành clean boundary khi policy của node khai báo như vậy.

Entropy là mặc định: agent sẽ sao chép pattern đang tồn tại, kể cả pattern xấu. Cần cleanup tức thời và vòng maintenance định kỳ.

## Clean-state contract

Một handoff sạch gồm:

1. Build/test/verification bắt buộc đạt policy hoặc không suy giảm so với baseline được phê duyệt.
2. Completed, unverified, blocked và next action được ghi có cấu trúc.
3. Không còn temporary/debug/secret artifact ngoài policy.
4. Startup/readiness path vẫn hoạt động.
5. Worktree/revision/diff/evidence có provenance.
6. Workspace có thể resume hoặc release mà không ảnh hưởng worktree khác.

Với legacy repo đã có test đỏ, “clean” không giả vờ tất cả xanh. Hệ thống phải pin known baseline, chứng minh không tạo regression mới và ghi rõ debt còn tồn tại.

## Tiêu chí bắt buộc

- **HE-12-M01 — Dual release gate:** terminal completion/release của task family, và mutating node được khai báo là handoff boundary, MUST yêu cầu cả task verification và clean-state policy. Node trung gian/read-only không mặc định chạy full clean gate.
- **HE-12-M02 — Structured handoff:** status, verified/unverified work, blockers, decisions, evidence và next action MUST được persist.
- **HE-12-M03 — Baseline-aware health:** build/test/startup outcome MUST được so với baseline hoặc policy; regression không được che bởi lỗi có sẵn.
- **HE-12-M04 — Temporary artifact policy:** debug code, temp files, secrets và generated artifacts MUST được scan theo rule có scope và false-positive handling.
- **HE-12-M05 — Idempotent cleanup:** cleanup executor MUST có thể chạy lại an toàn và chỉ tác động resource thuộc ownership của attempt/task family.
- **HE-12-M06 — Isolated failure:** session lỗi MUST để lại checkpoint rõ trong task-family worktree. Không được discard riêng “worktree của child” vì các sibling dùng chung; chỉ orchestrator được release/discard toàn family sau khi không còn sibling hoạt động, đã giữ evidence cần thiết và có quyết định của parent/policy.
- **HE-12-M07 — Worktree release gate:** orchestrator MUST không release/merge task-family worktree trước integration và clean-handoff checks.
- **HE-12-M08 — Evidence-derived state:** progress/handoff projection MUST sinh từ canonical events/evidence, không chỉ prose agent tự viết.

## Tiêu chí nên có

- **HE-12-S01:** Có cleanup tại mutating handoff/release boundary và cleanup/quality scan định kỳ toàn project; session trung gian chỉ cần checkpoint nếu cleanup có thể làm hỏng công việc sibling.
- **HE-12-S02:** Project SHOULD theo dõi quality trend theo component, không chỉ một điểm toàn repo.
- **HE-12-S03:** Cleanup thay đổi code SHOULD chạy fixed benchmark slice sau sửa.
- **HE-12-S04:** Harness SHOULD định kỳ ablate một component trên benchmark; deprecate phần không còn tạo giá trị.
- **HE-12-S05:** Review feedback lặp SHOULD được đưa vào improvement pipeline thay vì tích lũy manual cleanup.
- **HE-12-S06:** Auto-delete MAY chỉ áp dụng cho artifact chắc chắn thuộc run; trường hợp mơ hồ phải báo cáo/approval.

## Task family semantics

- Child subtask có thể handoff nội bộ mà chưa release shared worktree.
- Parent/integration node chịu trách nhiệm baseline tổng hợp và cleanup cuối family.
- Nếu một child fail, sibling state không bị rollback mù; event/evidence chỉ ra phần nào cần invalidate.
- Cleanup không được dùng destructive reset trên checkout hoặc thay đổi không thuộc ownership của family.

## Evidence và chỉ số

- Clean-handoff pass rate.
- Startup/recovery time của runner mới.
- Build/test regression rate.
- Stale/temp/secret artifact count.
- Dirty-worktree và undocumented-unverified work rate.
- Cleanup cost so với diagnostic/rework time tiết kiệm.
- Quality trend và benchmark delta sau cleanup.
- Harness components giữ/xóa sau ablation.

## Ánh xạ vào Agent Kit

- `CleanStatePolicyVersion`, `CleanStateGate`, `SessionClosePolicy`.
- Baseline snapshot/evidence theo component/task family.
- Cleanup executor chạy ngoài agent skill.
- Quality findings và scheduled maintenance WorkItems.
- UI tách “behavior verified” khỏi “handoff clean”.

## Anti-patterns

- “Clean” đồng nghĩa xóa mọi file untracked mà không xét ownership.
- TODO/FIXME nào cũng bị coi là lỗi terminal.
- Scanner presence-only được dùng làm authoritative gate.
- Agent tự tick checklist mà không có output.
- Bỏ cleanup sang task sau.
- Fast merge được dùng như quy tắc mặc định dù defect cost cao.

## Phép thử chấp nhận

1. Task behavior pass nhưng để secret/temp file: task chưa được hoàn thành/release.
2. Legacy baseline có hai failing tests, attempt vẫn phải chứng minh không thêm failure mới và lưu baseline debt.
3. Chạy cleanup hai lần cho kết quả tương đương và không đụng file ngoài task family.
4. Runner mới tiếp tục từ handoff mà không cần đọc transcript cũ.
