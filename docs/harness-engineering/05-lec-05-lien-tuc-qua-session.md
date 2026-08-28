# Lec 05 — Liên tục qua session

> Nguồn: [Lecture 05 — Keeping Context Alive Across Sessions](https://github.com/walkinglabs/learn-harness-engineering/blob/77e7a3e21469dcbece2558086c8d91657abeaa40/docs/en/lectures/lecture-05-why-long-running-tasks-lose-continuity/index.md)

## Luận điểm

Context window và provider session đều hữu hạn. Độ liên tục không đến từ việc giữ một cuộc hội thoại vô tận, mà từ việc externalize đúng state để lần chạy sạch có thể phục hồi “đã làm gì, vì sao, bằng chứng nào, đang kẹt gì và bước tiếp theo”.

Transcript là lịch sử. `ContextSnapshot` và checkpoint mới là input phục hồi có kiểm soát.

## Các lớp state cần tách

- `Conversation/Message`: trao đổi gốc và attachment.
- `DecisionArtifact`: quyết định hệ trọng, lý do và alternative bị loại.
- `ContextSnapshot`: resource/message/revision thực sự đưa vào attempt.
- `Checkpoint`: trạng thái engine sau transition bền vững.
- `Artifact/Evidence`: diff, command output, test report, review.
- `AgentSession`: opaque provider session ID và capability metadata.

## Tiêu chí bắt buộc

- **HE-05-M01 — Platform-owned continuity:** resume MUST không phụ thuộc duy nhất vào Claude/Codex session ID.
- **HE-05-M02 — Atomic checkpoint:** state transition và checkpoint/event liên quan MUST được persist atomically trước khi dispatch bước kế tiếp.
- **HE-05-M03 — Handoff content:** checkpoint bàn giao MUST chứa current node/outcome, completed work, blockers, decisions, evidence refs và next action.
- **HE-05-M04 — Exact context manifest:** mỗi attempt MUST ghi message/resource/version/repo revision đã nạp.
- **HE-05-M05 — Side-effect recovery:** sau crash, engine MUST phân biệt side effect đã commit, chưa commit và không biết; không auto-retry mù thao tác ghi code.
- **HE-05-M06 — Fresh-context checker:** evaluator độc lập MUST dùng context tối thiểu cần thiết và không kế thừa reasoning riêng của maker.
- **HE-05-M07 — Context retention policy:** raw transcript/log MUST có redaction, size và retention policy; summary không được xóa provenance về message gốc.

## Tiêu chí nên có

- **HE-05-S01:** Project SHOULD định nghĩa recovery SLO; baseline ban đầu có thể là runner/session mới đến trạng thái hành động được trong dưới 3–5 phút.
- **HE-05-S02:** Long-running node SHOULD checkpoint theo round hoặc milestone, không đợi tới khi context gần hết.
- **HE-05-S03:** Adapter SHOULD dùng native session resume khi có, nhưng luôn có đường reconstruct session mới.
- **HE-05-S04:** Context compaction SHOULD giữ decision/rationale, blockers và evidence; không chỉ giữ summary “đã sửa file X”.
- **HE-05-S05:** Hệ thống SHOULD chạy recovery drill định kỳ bằng provider session mới.

## Task family và shared worktree

- Parent task và descendants cùng task family chia sẻ một worktree lineage.
- Mỗi subtask có NodeRun/Attempt/ContextSnapshot riêng dù cùng workspace.
- Write lease bảo đảm một writer mặc định; path lease cho phép song song có kiểm soát.
- Handoff của subtask phải ghi base/head revision hoặc workspace generation hiện tại.
- Parent không hoàn thành cho tới khi child policy, integration gate và clean handoff đều thỏa.

## Evidence và chỉ số

- Recovery/rebuild time.
- Tỷ lệ resume không cần đọc transcript toàn bộ.
- Duplicate-work rate sau session boundary.
- Decision reversal do thiếu rationale.
- Context growth theo round.
- Crash recovery success và duplicate side-effect rate.
- Tỷ lệ checkpoint thiếu evidence/next action.

## Ánh xạ vào Agent Kit

- Durable event journal và checkpoint store trong SQLite/PostgreSQL.
- ArtifactStore: filesystem ở alpha, object storage ở beta.
- Context assembler và summary versioning.
- Provider adapter giữ external session ref như metadata tùy chọn.
- UI timeline/chat hiển thị message nào đã được áp dụng vào attempt nào.

## Anti-patterns

- Lưu cả task trong một chuỗi chat duy nhất.
- Dùng summary tự do không có source IDs.
- Retry CLI sau timeout mà không xác định process cũ còn ghi hay không.
- Session mới đọc tất cả task cũ dù chỉ cần task đang active.
- Coi commit là đủ cho handoff nhưng không lưu test/blocker/next action.

## Phép thử chấp nhận

1. Xóa external provider session ID rồi resume task bằng checkpoint và context snapshot.
2. Kill worker sau khi command tạo side effect nhưng trước ACK; fencing/idempotency phải ngăn kết quả attempt cũ ghi đè.
3. Fresh checker chỉ nhận requirement, diff và evidence; không nhận chain-of-thought/maker transcript.
4. Session mới xác định được next action và baseline verification trong recovery SLO.
