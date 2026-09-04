# Lec 09 — Không cho agent tự tuyên bố hoàn thành

> Nguồn: [Lecture 09 — Preventing Agents from Declaring Victory Too Early](https://github.com/walkinglabs/learn-harness-engineering/blob/77e7a3e21469dcbece2558086c8d91657abeaa40/docs/en/lectures/lecture-09-why-agents-declare-victory-too-early/index.md)

## Luận điểm

Agent đánh giá dựa trên hiểu biết và reasoning đã tạo ra code nên có thiên lệch tự xác nhận. Completion phải được externalize thành policy và evidence. Maker có thể tự kiểm tra để sửa nhanh, nhưng không phải authority cuối.

## Assurance ladder

Không phải mọi task đều cần E2E giống nhau. Verification policy resolve theo risk:

1. Static/build/type/lint.
2. Unit/component tests.
3. Integration/startup/health checks.
4. End-to-end user behavior.
5. Independent evaluator hoặc human approval cho tiêu chí chủ quan/rủi ro cao.

Task chỉ được hoàn thành khi tất cả level bắt buộc của policy hiệu lực đều pass.

## Tiêu chí bắt buộc

- **HE-09-M01 — External termination:** CompletionPolicy MUST do engine/gate service đánh giá, không do maker session.
- **HE-09-M02 — Required assurance levels:** WorkItem và `CompletionPolicyVersion` MUST resolve rõ verification levels bắt buộc và thứ tự chạy. Skill/Engineering Pack không có quyền cấu hình gate.
- **HE-09-M03 — Machine evidence:** command gate MUST lưu command/argv, cwd/target, exit code, timestamps, tool version, revision và output/artifact hash.
- **HE-09-M04 — Maker/checker isolation:** checker có thẩm quyền MUST dùng session/context độc lập; không kế thừa lập luận tự biện hộ của maker.
- **HE-09-M05 — Typed verdict:** evaluator/human result MUST map vào verdict có cấu trúc như `PASS`, `FAIL`, `CHANGES_REQUESTED`, `NEEDS_INFO`, kèm criteria-level evidence.
- **HE-09-M06 — No skipped level:** level sau không bù cho level bắt buộc trước bị fail, trừ policy ngoại lệ được audit.
- **HE-09-M07 — Bounded correction:** fail quay lại maker theo business-rework edge có iteration/budget; không retry vô hạn.
- **HE-09-M08 — Actionable diagnostics:** gate failure MUST trả `WHAT`, `WHY`, `FIX` và evidence location khi có thể.

## Tiêu chí nên có

- **HE-09-S01:** Functional acceptance SHOULD được verify trước refactor/optimization ngoài phạm vi.
- **HE-09-S02:** Checker SHOULD được calibration trên benchmark/human verdict; “luôn tìm lỗi” không phải mục tiêu.
- **HE-09-S03:** Local gate và CI gate SHOULD có assurance level khác nhau, tránh coi local pass là bằng CI authoritative trong mọi project.
- **HE-09-S04:** Failure SHOULD chỉ rerun phần cần thiết nhưng final completion vẫn chạy policy tổng hợp.
- **HE-09-S05:** Agent confidence MAY được lưu để đo calibration, nhưng không ảnh hưởng transition authoritative.

## Evidence và chỉ số

- Premature-completion rate.
- Claimed-pass/actual-fail gap.
- False-pass và false-fail của evaluator.
- Defect escape sau verified.
- Số correction rounds và no-progress rate.
- Tỷ lệ gate failure có remediation hữu ích.
- Human override rate và nguyên nhân.

## Ánh xạ vào Agent Kit

- `CompletionPolicyVersion`, `VerificationLevel`, `GateRun`.
- Maker/Checker `AgentProfileVersion` riêng.
- `Evaluation` theo rubric/criteria.
- Router `PASS -> next`, `FAIL -> rework`, `NEEDS_INFO -> wait` (vocabulary minh họa từ lecture gốc;
  Alpha's own `ROUTER` node type hiện chỉ hỗ trợ đúng một outcome khai báo — ADR-026 — nên một mapping
  ba nhánh như ví dụ này cần một `MACHINE_GATE`/`APPROVAL` thật quyết định outcome, hoặc chờ multi-
  outcome `ROUTER` rule ở phiên bản sau).
- UI tách agent claim khỏi authoritative verdict.

## Anti-patterns

- Unit tests xanh được mặc định là feature hoàn chỉnh.
- Cùng session vừa implement vừa approve.
- Checker nhận toàn bộ maker transcript.
- Error chỉ ghi “test failed”.
- Gate dựa trên việc file test tồn tại.
- Reviewer được thưởng theo số lỗi tìm thấy, tạo false positive.

## Phép thử chấp nhận

1. Maker báo confidence 100% nhưng integration gate fail: NodeRun quay rework.
2. Checker verdict pass nhưng machine gate fail: machine invariant chặn completion.
3. Gate fail phải chỉ ra criterion, evidence và route tiếp theo.
4. Ba round liên tiếp không có delta/evidence tiến triển phải dừng/escalate theo budget.
