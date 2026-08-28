# Lec 06 — Khởi tạo là một phase riêng

> Nguồn: [Lecture 06 — Make the Agent Initialize Before Every Work Session](https://github.com/walkinglabs/learn-harness-engineering/blob/77e7a3e21469dcbece2558086c8d91657abeaa40/docs/en/lectures/lecture-06-why-initialization-needs-its-own-phase/index.md)

## Luận điểm

Khởi tạo môi trường và triển khai tính năng có mục tiêu tối ưu khác nhau. Trộn chúng khiến agent vừa sửa hạ tầng vừa viết business code, làm baseline không rõ và tích lũy code chưa thể kiểm chứng.

Trong Agent Kit cần tách hai khái niệm:

- **Project onboarding:** khám phá/cấu hình repository, component, pack và verification recipes; thực hiện khi thêm project hoặc thay đổi nền tảng lớn.
- **Run initialization:** dựng worktree, checkout revision, resolve toolchain/context, kiểm baseline; thực hiện trước mỗi task family/attempt cần workspace mới.

## Tiêu chí bắt buộc

- **HE-06-M01 — Dedicated initialization:** business implementation MUST không bắt đầu trước khi run initialization đạt trạng thái ready.
- **HE-06-M02 — Canonical commands:** project/component MUST có setup, start/health và verification recipes xác định được.
- **HE-06-M03 — Baseline evidence:** runner MUST chạy readiness/baseline checks và lưu output thật trước khi cho writer hoạt động.
- **HE-06-M04 — Toolchain pinning:** runtime, package manager, dependencies và required services MUST có version/provenance.
- **HE-06-M05 — Component discovery:** onboarding MUST tạo hoặc xác nhận topology Repository -> Component -> EngineeringPack.
- **HE-06-M06 — Clean base:** task family worktree MUST bắt đầu từ revision đã biết và trạng thái repository có thể giải thích.
- **HE-06-M07 — Failure separation:** environment/setup failure MUST được phân biệt với implementation/test failure.
- **HE-06-M08 — Server readiness:** beta worker MUST có khả năng tạo workspace mới mà không dựa vào state thủ công của một máy developer.

## Tiêu chí nên có

- **HE-06-S01:** Onboarding SHOULD có một smoke test/example check chứng minh test harness thật sự hoạt động.
- **HE-06-S02:** Template/scaffold SHOULD cung cấp infrastructure chuẩn, nhưng version/template source phải được ghi lại.
- **HE-06-S03:** Cache dependency/build SHOULD được phép để tăng tốc, song readiness check vẫn phải chạy trên workspace thực.
- **HE-06-S04:** Hệ thống SHOULD lưu readiness profile để phân biệt lỗi mới với baseline đã biết.
- **HE-06-S05:** Fresh-run test SHOULD xác nhận runner tìm được commands từ repo/pack mà không cần lời giải thích ngoài hệ thống.

## Startup readiness contract

Một component được coi là sẵn sàng khi:

1. Checkout/setup thành công.
2. Toolchain và dependency phù hợp.
3. Standard start hoặc health command chạy được nếu task cần runtime.
4. Baseline verification có kết quả rõ.
5. Target component/path và scope được resolve.
6. Worktree, lease và artifact directories được cấp.
7. Context bundle và permission policy đã compile.

## Evidence và chỉ số

- Initialization pass rate.
- Time to ready và time to first passing verification.
- Số phút agent tiêu vào environment repair trong implementation node.
- Tỷ lệ baseline fail trước khi code thay đổi.
- Reproducibility trên worker sạch.
- Cache hit rate đi kèm cache-caused failure rate.

## Ánh xạ vào Agent Kit

- `PROJECT_ONBOARDING` workflow hoặc admin command.
- `WORKSPACE_PREPARE`, `ENV_PROBE`, `BASELINE_GATE` block types.
- `EnvironmentProfileVersion` và `ReadinessEvidence`.
- Runner image/toolchain registry cho beta.
- UI phân biệt “project chưa sẵn sàng” với “task implementation fail”.

## Anti-patterns

- Agent tự cài/chọn toolchain giữa implementation mà không ghi lại.
- Baseline test đỏ nhưng agent vẫn tiếp tục và gán mọi lỗi cho thay đổi mới.
- Chỉ kiểm file config tồn tại, không chạy command.
- Dùng một server workspace lâu dài cho mọi task.
- Onboarding vừa tạo hạ tầng vừa triển khai feature đầu tiên.

## Phép thử chấp nhận

1. Worker sạch clone repo, resolve component/pack và chạy baseline mà không cần thao tác tay.
2. Làm hỏng package-manager version: failure phải xuất hiện ở initialization, không ở BUILD.
3. Baseline đỏ trước thay đổi: task dừng với reason rõ hoặc policy ngoại lệ, không tạo evidence giả rằng agent gây lỗi.
4. Hai task family khởi tạo đồng thời phải nhận worktree riêng.
