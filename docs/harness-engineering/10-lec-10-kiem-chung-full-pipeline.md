# Lec 10 — Kiểm chứng full pipeline

> Nguồn: [Lecture 10 — Only a Full Pipeline Run Counts as Real Verification](https://github.com/walkinglabs/learn-harness-engineering/blob/77e7a3e21469dcbece2558086c8d91657abeaa40/docs/en/lectures/lecture-10-why-end-to-end-testing-changes-results/index.md)

## Luận điểm

Unit tests có chủ ý cô lập dependency nên không bắt được interface mismatch, state propagation, resource lifecycle và environment differences. Khi task đi qua nhiều component, verification phải chạy đường hành vi thật phù hợp với scope.

“Full pipeline” là pipeline cần thiết để chứng minh behavior của WorkItem, không mặc định là chạy toàn bộ test của toàn doanh nghiệp cho mọi thay đổi nhỏ.

## Tiêu chí bắt buộc

- **HE-10-M01 — Risk-based verification:** `VerificationPolicyVersion` cùng WorkItem risk/scope MUST xác định khi nào unit, integration, startup và E2E là bắt buộc. Skill/Engineering Pack chỉ cung cấp tri thức đầu vào, không cấu hình gate.
- **HE-10-M02 — Cross-component gate:** thay đổi chạm nhiều component/boundary MUST có verification đi qua các boundary liên quan.
- **HE-10-M03 — Environment fidelity:** pipeline evidence MUST nói rõ environment/profile và external dependencies được dùng thật, giả lập hay stub.
- **HE-10-M04 — Architectural invariants:** constraint doanh nghiệp quan trọng và kiểm được bằng máy MUST được hiện thực thành lint/static/architecture test hoặc gate.
- **HE-10-M05 — Review feedback promotion:** lỗi review lặp lại MUST tạo candidate improvement; khi ổn định phải được nâng thành rule/gate có owner.
- **HE-10-M06 — Agent-oriented errors:** automated check MUST cung cấp WHAT/WHY/FIX và location khi xác định được.
- **HE-10-M07 — Evidence provenance:** report MUST gắn exact code revision, test selection, tool versions, exit status và artifacts.
- **HE-10-M08 — Flake semantics:** flaky/infrastructure error MUST khác functional failure; retry policy không được biến flake thành pass im lặng.

## Tiêu chí nên có

- **HE-10-S01:** Pipeline SHOULD chạy từ check rẻ/nhanh tới đắt/chậm để feedback sớm.
- **HE-10-S02:** E2E SHOULD tập trung critical user journeys và boundary risk, không nhân rộng test chậm thiếu giá trị.
- **HE-10-S03:** Project SHOULD có golden journeys versioned theo product behavior.
- **HE-10-S04:** Architecture gate SHOULD kiểm invariant thay vì khóa chi tiết implementation không cần thiết.
- **HE-10-S05:** Review-to-rule workflow SHOULD yêu cầu evidence issue lặp và kiểm false-positive trước publish.

## Engineering Pack đóng góp gì

Một pack chỉ đóng góp instruction/resource thụ động giúp agent hoặc người thiết kế hiểu cách verify stack đó:

```text
java + spring-boot
  -> tài liệu build/test chuẩn
  -> checklist boundary cần kiểm
  -> mô tả architectural invariants
  -> template fixture hoặc test example

angular
  -> tài liệu type/lint/unit chuẩn
  -> golden-journey specification
  -> accessibility/visual guidance
```

Command, selector có thể thực thi, gate definition và risk policy là các object versioned riêng do workflow/policy tham chiếu và `GateRunner` thực hiện. Có thể sinh proposal cho các object đó từ tài liệu của Pack, nhưng phải đăng ký/publish riêng; cài Pack không được âm thầm cấp quyền hoặc thêm executable gate.

## Evidence và chỉ số

- Boundary defects chỉ E2E/integration mới bắt được.
- Defect escape rate theo verification level.
- Gate duration và feedback latency.
- Flake rate/retry count.
- Review-comment recurrence trước/sau promotion.
- False positive của architecture rules.
- Tỷ lệ cross-component task thiếu full-pipeline evidence.

## Ánh xạ vào Agent Kit

- `VerificationRecipeVersion` và risk selector.
- `GateRunner`/`CommandExecutor` chạy trên server worker.
- `GoldenJourney` resource theo component/project.
- `ReviewFinding -> HarnessImprovementCandidate`.
- Evidence artifact viewer trong task detail.

## Anti-patterns

- Chạy unit tests rồi gọi là full verification.
- Bắt mọi task chạy toàn bộ E2E suite không xét scope/risk.
- Lưu architecture rule trong prose nhưng không có enforcement.
- Dùng một dòng grep mong manh làm security invariant mà không test false positive/negative.
- Retry test đến khi xanh rồi bỏ qua lịch sử flake.

## Phép thử chấp nhận

1. Task thay API contract và frontend consumer: policy phải yêu cầu integration/E2E qua cả hai boundary.
2. Architecture violation phải trả file/location cùng hướng sửa.
3. Gate chạy cùng code revision khác environment phải sinh evidence riêng, không tái sử dụng mù.
4. Một review finding lặp lại được chuyển thành candidate, benchmark và publish gate version mới có audit.
