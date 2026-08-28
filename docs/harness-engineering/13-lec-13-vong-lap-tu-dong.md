# Lec 13 — Vòng lặp tự động

> Nguồn: [Lecture 13 — From Manual Prompting to Autonomous Loops](https://github.com/walkinglabs/learn-harness-engineering/blob/77e7a3e21469dcbece2558086c8d91657abeaa40/docs/en/lectures/lecture-13-loop-engineering/index.md)

## Luận điểm

Harness làm một lần chạy đáng tin; loop làm nhiều lần chạy có thể tự tiếp tục. Loop tối thiểu cần:

1. Goal/state đích.
2. Verification độc lập.
3. Stop/budget conditions.

Con người chuyển từ nhắc từng bước sang định nghĩa mục tiêu, constraint, escalation và review output.

## Các loại loop/trigger

- **Goal-driven:** tích lũy tiến triển tới một trạng thái kết thúc.
- **Time-driven:** một kiểm tra nhỏ lặp theo lịch; mỗi run thường độc lập.
- **Event-driven:** phản ứng với CI failure, issue, webhook hoặc system event.
- **Human-turn-driven:** người kích từng lượt, phù hợp khám phá nhỏ.

Scheduler/trigger là adapter. Nó không được trộn với semantics goal/verification/state của loop.

## Loop ở đâu trong Agent Kit

Có hai mức:

- **Agent-loop trong block:** maker thử, tự chạy check cơ bản, sửa trong budget rồi trả structured result.
- **Business rework trên graph:** gate/reviewer fail thì engine đi theo edge khai báo về node phù hợp.

Critical routing không được giấu hoàn toàn trong transcript của một agent-loop. Những gì ảnh hưởng audit, human approval, rollback hoặc cross-node state phải ở graph level.

## Tiêu chí bắt buộc

- **HE-13-M01 — Loop contract:** LoopDefinition MUST có goal, scope/hands-off, acceptance, verifier, trigger type và stop/budget policy.
- **HE-13-M02 — Independent authority:** maker MUST không tự tạo terminal success; checker/machine gate/human policy quyết định.
- **HE-13-M03 — Round state:** sau mỗi round MUST persist delta, outcome, evidence, blocker, next action và workspace generation/revision.
- **HE-13-M04 — Finite bounds:** loop MUST có max rounds/time/cost và no-progress threshold; “chạy mãi” không phải mặc định production.
- **HE-13-M05 — Escalation:** budget hết, capability thiếu hoặc no-progress MUST tạo typed blocker/escalation, không tiếp tục mù.
- **HE-13-M06 — Idempotent triggers:** time/event runs MUST có idempotency/correlation key và duplicate handling.
- **HE-13-M07 — Safe workspace:** code-writing loop MUST chạy trong task-family worktree có lease/fencing; technical retry không được lặp side effect đã commit.
- **HE-13-M08 — Context control:** loop MUST dùng checkpoint/summary/context selection; không replay toàn transcript tăng vô hạn.

## Tiêu chí nên có

- **HE-13-S01:** Sau nhiều round cùng failure signature, loop SHOULD đổi strategy hoặc escalate.
- **HE-13-S02:** Maker và checker SHOULD có role/tool policy/context riêng; checker được calibration theo evidence chứ không theo số lỗi tìm thấy.
- **HE-13-S03:** Loop SHOULD ghi progress units để no-progress detector dựa trên evidence delta, không chỉ text khác nhau.
- **HE-13-S04:** Parallel loops SHOULD dùng task family/worktree riêng; review capacity và runner quotas giới hạn fan-out.
- **HE-13-S05:** Ratchet strategy MAY giữ chỉ những thay đổi chứng minh tốt hơn, nhưng rollback phải an toàn trong worktree và không dùng destructive reset trên dữ liệu ngoài ownership.
- **HE-13-S06:** Self-feeding loop/fleet MAY chỉ bật sau khi single-loop benchmark đạt chuẩn ổn định.

## Sáu primitives được chuyển hóa

| Primitive từ lecture | Agent Kit |
|---|---|
| Automation | Trigger/Scheduler adapter |
| Worktree | WorkspaceProvider + task-family lease |
| Skill | Instruction/resource registry |
| Connector | Tool/service adapter có permission |
| Sub-agent | AgentProfile + Node/Attempt |
| External state | DB checkpoint/event + ArtifactStore |

## Bốn chi phí âm thầm

- **Verification debt:** output tăng nhanh hơn evidence.
- **Comprehension rot:** con người không còn hiểu architecture/code do fleet sinh.
- **Cognitive surrender:** operator ngừng đưa ra judgment.
- **Token blowout:** context và retries tăng mà không tạo tiến triển.

Mỗi loop policy cần budget/metric tương ứng để các chi phí này nhìn thấy được.

## Evidence và chỉ số

- Autonomous verified completion rate.
- Rounds/time/cost tới pass.
- Progress-producing round rate.
- Human intervention và escalation rate.
- False-pass/false-fail của checker.
- Stuck/runaway/duplicate-trigger rate.
- Context tokens theo round.
- Recovery success sau worker restart.
- Review queue depth do loops tạo ra.

## Ánh xạ vào Agent Kit

- `LoopDefinitionVersion -> LoopRun -> Round/Attempt`.
- Budget, no-progress và escalation policies.
- Maker/checker profiles, GateRefs và task-family workspace ref.
- Scheduler ngoài core workflow semantics.
- UI hiển thị round timeline, evidence delta, budget và lý do dừng.

## Anti-patterns

- Goal “làm tốt nhất có thể” không có verification/stop.
- Timer loop dùng cho task cần tiến triển tích lũy nhưng không lưu state.
- Checker được lệnh phải tìm lỗi bằng mọi giá.
- Infinite retry cùng failure signature.
- Full transcript là memory.
- Child agent tự spawn fleet không giới hạn.
- Retry code-writing trên shared worktree không fencing.

## Phép thử chấp nhận

1. Goal loop không có stop/budget phải bị definition validator từ chối.
2. Ba rounds cùng failure signature và không có evidence delta phải dừng/escalate.
3. Scheduler gửi duplicate event ID: chỉ một logical run được tạo.
4. Kill worker giữa rounds; worker mới resume đúng checkpoint và budget còn lại.
