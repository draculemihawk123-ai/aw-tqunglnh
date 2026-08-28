# Lec 04 — Progressive disclosure cho instruction

> Nguồn: [Lecture 04 — Split Instructions Across Files](https://github.com/walkinglabs/learn-harness-engineering/blob/77e7a3e21469dcbece2558086c8d91657abeaa40/docs/en/lectures/lecture-04-why-one-giant-instruction-file-fails/index.md)

## Luận điểm

Instruction entrypoint phải là bản đồ, không phải bách khoa toàn thư. Nạp mọi rule cho mọi task làm giảm signal-to-noise, tiêu tốn context, tích lũy mâu thuẫn và chôn constraint quan trọng.

Progressive disclosure nghĩa là resolve đúng resource khi task, component, block và risk profile cần nó; không phải chia một file lớn thành nhiều file rồi vẫn nạp tất cả.

## Failure modes

- Global instruction phình không có giới hạn.
- Hard constraint và lời khuyên mềm có cùng mức ưu tiên.
- Rule lịch sử không còn đúng nhưng không ai dám xóa.
- Cùng một rule bị copy vào nhiều skill/pack/component.
- Context route không có điều kiện áp dụng nên mọi resource đều được nạp.
- Hai pack đưa ra giá trị đơn mâu thuẫn và hệ thống âm thầm dùng “last wins”.

## Tiêu chí bắt buộc

- **HE-04-M01 — Router entry:** entry instruction MUST chỉ chứa overview, lệnh nền, hard constraints toàn cục và route tới resource chi tiết.
- **HE-04-M02 — Applicability selector:** mọi resource không toàn cục MUST khai báo khi nào áp dụng: component/path tag, task kind, block kind hoặc risk class.
- **HE-04-M03 — Priority class:** instruction MUST phân biệt `HARD_CONSTRAINT`, `REQUIRED_PROCEDURE`, `GUIDANCE`, `REFERENCE`.
- **HE-04-M04 — Provenance lifecycle:** mỗi rule MUST có source/owner, applicability và điều kiện review/expiry hoặc `last_verified`.
- **HE-04-M05 — Conflict detection:** resolver MUST phát hiện rule/config mâu thuẫn; giá trị đơn xung đột không được tự “last wins”.
- **HE-04-M06 — Context manifest:** attempt MUST lưu danh sách resource thực sự được nạp, thứ tự, version/hash và lý do chọn.
- **HE-04-M07 — Single canonical rule:** cùng một invariant MUST có đúng một definition authoritative; nơi khác chỉ tham chiếu.

## Tiêu chí nên có

- **HE-04-S01:** Entry resource SHOULD được lint theo trần kích thước và số hard constraint do tổ chức cấu hình; baseline khởi đầu có thể dùng 50–200 dòng và tối đa khoảng 15 hard constraints.
- **HE-04-S02:** Constraint không được phép rơi SHOULD nằm trong kênh ưu tiên cao do adapter hỗ trợ, không trông chờ vị trí ngẫu nhiên trong prompt dài.
- **HE-04-S03:** Context assembler SHOULD tối ưu theo relevance và budget, đồng thời giữ reserved budget cho task, code và tool output.
- **HE-04-S04:** Hệ thống SHOULD có audit định kỳ cho rule stale, duplicate, unused và contradictory.
- **HE-04-S05:** Rule được sử dụng thường xuyên SHOULD được nâng thành lint/gate nếu có thể kiểm bằng máy.

## Engineering Pack resolution

Ví dụ profile hiệu lực:

```text
enterprise-baseline
+ java
+ spring-boot
+ rest-api
+ payment-service
```

Merge policy:

- Instruction được hợp nhất theo priority và scope xác định trước.
- Resource list được hợp nhất, khử trùng theo stable ID/version và sắp theo thứ tự xác định.
- Hai hard constraint áp dụng đồng thời nhưng mâu thuẫn gây configuration error; resolver không tự chọn bên thắng.
- Pack không tham gia merge permission, toolchain hay gate; ba concern đó được resolve bởi policy/environment/verification registry riêng.
- Kết quả context resolve được snapshot/hash vào NodeRun.

## Evidence và chỉ số

- Instruction tokens trên mỗi attempt.
- Tỷ lệ resource đã nạp thực sự được tham chiếu hoặc ảnh hưởng quyết định.
- Số conflict/duplicate/stale rule.
- Hard-constraint compliance rate.
- Thời gian tìm resource cần thiết.
- So sánh benchmark trước/sau khi tách context.

## Ánh xạ vào Agent Kit

- `ContextAssembler` và `ContextRoute`.
- `KnowledgeResource` metadata.
- `EngineeringPackResolver` với dependency/conflict graph.
- `EffectiveContextProfile` bất biến theo attempt; execution profile được resolve ở concern khác.
- Trang UI giải thích “vì sao resource này được nạp”.

## Anti-patterns

- Pack chỉ là một prompt khổng lồ.
- Chia file theo topic nhưng entry import tất cả ngay từ đầu.
- Quy tắc không có owner hoặc ngày kiểm chứng.
- Chôn security constraint giữa resource dài.
- Tự đo chất lượng bằng “đã nạp nhiều context”.

## Phép thử chấp nhận

1. Task chỉ sửa React component không được nạp database migration rules.
2. Hai pack cùng scope đưa ra hai hard constraint bất tương thích về Java coding baseline phải làm context resolution fail trước khi invoke; Java runtime thực tế vẫn do EnvironmentProfile quyết định.
3. Từ một attempt, UI truy được resource, version và selector đã khiến nó được chọn.
4. Xóa một resource không bao giờ được route tới không làm benchmark suy giảm; hệ thống ghi được kết quả ablation.
