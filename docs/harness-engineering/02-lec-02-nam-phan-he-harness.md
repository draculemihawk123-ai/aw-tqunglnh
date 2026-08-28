# Lec 02 — Harness là năm phân hệ

> Nguồn: [Lecture 02 — What a Harness Actually Is](https://github.com/walkinglabs/learn-harness-engineering/blob/77e7a3e21469dcbece2558086c8d91657abeaa40/docs/en/lectures/lecture-02-what-a-harness-actually-is/index.md)

## Luận điểm

Một prompt hoặc skill đơn lẻ không phải harness. Harness hoàn chỉnh cần phối hợp instruction, tools/capabilities, environment, state và feedback. Thiếu một phân hệ sẽ tạo “khoảng trống vận hành” mà model phải tự đoán hoặc không thể hành động.

## Năm phân hệ và contract

| Phân hệ | Input | Output tối thiểu |
|---|---|---|
| Instruction | WorkItem, block, project/component | context bundle có ưu tiên và provenance |
| Tools/Capabilities | yêu cầu node, policy | tập capability khả dụng và quyền hiệu lực |
| Environment | repo revision, task family, toolchain | workspace sẵn sàng, health evidence |
| State | events, outputs, messages | checkpoint và projection có thể khôi phục |
| Feedback | acceptance/gate definitions, runtime signals | outcome, evidence và remediation |

## Tiêu chí bắt buộc

- **HE-02-M01 — Five-subsystem manifest:** mỗi NodeRun MUST lưu snapshot hoặc reference bất biến tới cấu hình hiệu lực của cả năm phân hệ.
- **HE-02-M02 — Capability check:** scheduler MUST kiểm tra runner/provider có capability cần thiết trước khi claim job.
- **HE-02-M03 — Least privilege:** quyền filesystem, command, network và secret MUST được resolve theo policy; instruction không thể tự nâng quyền.
- **HE-02-M04 — Reproducible environment:** toolchain version, dependency lock, repo revision và startup/verification commands MUST có provenance.
- **HE-02-M05 — External state:** dữ liệu phải sống qua attempt/session MUST thuộc platform-owned state hoặc artifact store, không chỉ nằm trong provider transcript.
- **HE-02-M06 — Authoritative feedback:** transition MUST dựa trên gate/evaluator/human approval có nguồn rõ, không dựa trên self-report.
- **HE-02-M07 — Harness version:** thay đổi instruction, tool policy, environment recipe, state schema hoặc feedback policy MUST tạo revision có thể audit.

## Tiêu chí nên có

- **HE-02-S01:** Project dashboard SHOULD có health score riêng cho từng phân hệ; điểm tổng không được che mất phân hệ đang ở mức nguy hiểm.
- **HE-02-S02:** Hệ thống SHOULD hỗ trợ ablation experiment: tắt một thành phần trên benchmark cô lập để đo đóng góp biên.
- **HE-02-S03:** Component có impact thấp SHOULD được xem xét là dư thừa, thiết kế sai hoặc chưa được benchmark chạm tới; không tự động xóa.
- **HE-02-S04:** Feedback subsystem SHOULD được ưu tiên sớm vì thường cho giá trị rõ và dễ kiểm chứng nhất.

## Ranh giới Skill và executor

Trong Agent Kit:

- Skill/Engineering Pack cung cấp instruction/resource.
- Tool capability nói agent có thể yêu cầu hành động nào.
- Executor thực hiện hành động và chịu sandbox/audit.
- Feedback/gate xác minh kết quả executor.

Một file shell nằm trong resource nhưng chưa bao giờ được phép gọi thì vẫn là tài liệu tham khảo. Khi hệ thống cho phép gọi file đó, nó phải được đăng ký thành executable extension với permission và provenance riêng.

## Evidence và chỉ số

- Tỷ lệ NodeRun có đủ five-subsystem snapshot.
- Capability mismatch bị bắt trước khi chạy.
- Environment readiness success rate.
- Recovery rate sau mất provider session.
- Gate coverage trên các tiêu chí completion.
- Hiệu quả benchmark khi ablate từng subsystem.

## Ánh xạ vào module

- Instruction: `ContextAssembler`, `SkillResolver`, `EngineeringPackResolver`.
- Tools: `CapabilityRegistry`, `PolicyEngine`, CLI adapters.
- Environment: `Runner`, `WorkspaceProvider`, toolchain detector.
- State: WorkItem/Run repositories, checkpoint, event log, ArtifactStore.
- Feedback: GateRunner, Approval service, Evaluator, evidence model.

## Anti-patterns

- Gọi một `AGENTS.md` dài là “harness hoàn chỉnh”.
- Cấp shell/network toàn quyền chỉ vì agent cần chạy một command.
- Adapter CLI vừa chạy model vừa trực tiếp đổi business state.
- Đóng gói build/test command vào prompt mà không thu exit code thật.
- Tạo health score trung bình khiến một subsystem bằng 0 bị che bởi bốn subsystem tốt.

## Phép thử chấp nhận

1. Node yêu cầu `filesystem.write.scoped`; runner chỉ có read capability: scheduler từ chối trước invoke.
2. Mất session CLI: attempt mới dựng lại được bằng platform state.
3. Gate output phải truy được về command/tool version/workspace/revision.
4. Chạy benchmark với feedback subsystem bị tắt phải tạo experiment riêng và không làm thay đổi cấu hình production.
