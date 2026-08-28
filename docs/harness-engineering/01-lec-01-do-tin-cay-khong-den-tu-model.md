# Lec 01 — Độ tin cậy không đến từ model mạnh hơn

> Nguồn: [Lecture 01 — Strong Models Don't Mean Reliable Execution](https://github.com/walkinglabs/learn-harness-engineering/blob/77e7a3e21469dcbece2558086c8d91657abeaa40/docs/en/lectures/lecture-01-why-capable-agents-still-fail/index.md)

## Luận điểm

Năng lực model và độ tin cậy khi thực thi là hai đại lượng khác nhau. Một model đủ khả năng vẫn thất bại nếu yêu cầu mơ hồ, convention vô hình, môi trường hỏng, không có verification hoặc mất state giữa session.

Mọi thất bại nên được coi trước tiên là một tín hiệu chẩn đoán harness. Chỉ quy lỗi cho model sau khi đã loại trừ các khiếm khuyết có cấu trúc.

## Failure modes cần nhận diện

- Task thiếu hành vi quan sát được hoặc thiếu out-of-scope.
- Agent phải đoán convention hay business rule không nhìn thấy.
- Runner không tái lập được môi trường.
- Agent dùng cảm giác tự tin thay cho verification.
- Session mới lặp lại khám phá hoặc đảo ngược quyết định cũ.
- Thất bại được ghi chung chung là “model kém”, không có root-cause category.

## Tiêu chí bắt buộc

- **HE-01-M01 — Failure taxonomy:** mọi run thất bại MUST được gán ít nhất một nhóm: `TASK_SPEC`, `CONTEXT`, `ENVIRONMENT`, `FEEDBACK`, `STATE`, `MODEL_CAPABILITY`, `EXTERNAL_DEPENDENCY` hoặc `POLICY`.
- **HE-01-M02 — Executable DoD:** WorkItem MUST có acceptance criteria và ít nhất một verification recipe trước khi chuyển sang trạng thái sẵn sàng thực thi; ngoại lệ phải qua human approval.
- **HE-01-M03 — Assumption surface:** agent output MUST có danh sách assumption/question có cấu trúc; assumption hệ trọng chưa giải quyết làm node chuyển `WAITING/NEEDS_INFO`.
- **HE-01-M04 — Evidence over confidence:** confidence, summary và câu “đã xong” của agent MUST NOT tạo transition `PASS/DONE`.
- **HE-01-M05 — Diagnostic event:** mỗi failure MUST lưu attempt, input snapshot, error/evidence refs, failure category và next diagnostic action.
- **HE-01-M06 — Same-model comparison:** khi đánh giá thay đổi harness, benchmark MUST giữ model, task set và environment ổn định để tránh gán nhầm cải thiện cho model.

## Tiêu chí nên có

- **HE-01-S01:** UI SHOULD cho phép xem failure theo layer và xu hướng qua thời gian.
- **HE-01-S02:** Harness SHOULD đề xuất remediation theo category, nhưng không tự đổi rule khi chưa có review.
- **HE-01-S03:** Hệ thống SHOULD lưu baseline run để so sánh trước/sau khi thêm gate, skill hoặc context policy.
- **HE-01-S04:** Chỉ số “model capability failure” SHOULD chỉ được dùng sau khi task đã qua completeness check và environment/gate đều khỏe.

## Evidence và chỉ số

- Verified completion rate.
- Tỷ lệ agent tuyên bố done nhưng gate fail.
- Rework time do assumption sai.
- Số failure không phân loại được.
- Tỷ lệ failure lặp lại cùng root cause sau khi đã sửa harness.
- Context discovery time trước khi bắt đầu thay đổi code.

## Ánh xạ vào Agent Kit

| Nhu cầu | Capability |
|---|---|
| Phân loại lỗi | `FailureReason` và diagnostic event schema |
| Kiểm tra độ đủ | Frame/completeness gate trước execution |
| Thu assumption | Structured result schema của AI activity |
| So sánh harness | Benchmark run và experiment label |
| Học từ failure | Trang diagnostic, rule/gate improvement backlog |

## Anti-patterns

- Đổi model ngay khi một task fail.
- Tăng prompt mà không biết thiếu layer nào.
- Dùng một câu “tests passed” của agent làm bằng chứng.
- Gộp tool error, test failure và requirement ambiguity thành cùng trạng thái `FAILED` không có reason.
- Đo chất lượng bằng số file hoặc số dòng code được tạo.

## Phép thử chấp nhận

1. Tạo task không có verification recipe: engine phải chặn hoặc yêu cầu phê duyệt ngoại lệ.
2. Cho agent trả `completed=true` trong khi gate command exit khác 0: task không được hoàn thành.
3. Chạy cùng task/model trước và sau khi thêm context rule: hệ thống xuất được so sánh có cùng baseline.
4. Một failure đã gán `CONTEXT` phải liên kết được tới resource còn thiếu và hành động sửa harness.
