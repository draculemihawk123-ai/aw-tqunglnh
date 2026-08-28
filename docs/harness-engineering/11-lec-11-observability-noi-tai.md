# Lec 11 — Observability nằm trong harness

> Nguồn: [Lecture 11 — Making the Agent's Runtime Observable](https://github.com/walkinglabs/learn-harness-engineering/blob/77e7a3e21469dcbece2558086c8d91657abeaa40/docs/en/lectures/lecture-11-why-observability-belongs-inside-the-harness/index.md)

## Luận điểm

Harness cần quan sát được cả:

- **Runtime:** hệ thống, CLI, command và code thực tế đã làm gì.
- **Process:** mục tiêu, scope, route và tiêu chí nào khiến kết quả được chấp nhận hoặc từ chối.

Log nhiều hơn không tự tạo observability. Dữ liệu phải có schema, correlation, provenance và phục vụ được câu hỏi chẩn đoán.

## Tiêu chí bắt buộc

- **HE-11-M01 — Canonical events:** harness MUST tự phát event chuẩn hóa; không phụ thuộc agent tự `console.log` hay tóm tắt.
- **HE-11-M02 — Trace identity:** mọi event MUST liên kết được tới WorkItem, WorkflowRun, NodeRun, Attempt và actor/runner phù hợp.
- **HE-11-M03 — Correlation/causation:** transition, tool call, gate và retry MUST có correlation/causation IDs hoặc sequence tương đương.
- **HE-11-M04 — Minimum execution record:** attempt MUST ghi start/end, outcome, duration, provider/executor, input/output refs, error và evidence refs.
- **HE-11-M05 — Task contract:** scope, acceptance, exclusions và verification policy MUST tồn tại trước BUILD và xem được trong trace.
- **HE-11-M06 — Criteria-level verdict:** evaluator/gate MUST chỉ ra criterion nào pass/fail và evidence tương ứng.
- **HE-11-M07 — Data safety:** log/context/artifact MUST có redaction, size limit, content type, retention và access policy.
- **HE-11-M08 — Normalized adapters:** Claude/Codex adapters MUST phát event schema chung; domain không parse log riêng tùy tiện để điều khiển state.

## Tiêu chí nên có

- **HE-11-S01:** Trace hierarchy SHOULD là Run -> Node -> Attempt -> CLI/Tool/Gate spans.
- **HE-11-S02:** Event schema SHOULD có khả năng export sang OpenTelemetry hoặc hệ observability tiêu chuẩn ở beta.
- **HE-11-S03:** Project SHOULD có golden workload/journey để so trace và failure qua harness versions.
- **HE-11-S04:** Evaluator rubric SHOULD có anchor, threshold và aggregation policy; không chỉ thang điểm chung chung.
- **HE-11-S05:** UI SHOULD truy vấn/filter theo project, workflow, block, provider, failure reason, duration, cost và blocker.
- **HE-11-S06:** Artifact lớn SHOULD ở ArtifactStore; DB chỉ giữ metadata, preview, hash và access rules.

## Canonical event envelope tối thiểu

```text
event_id, schema_version, sequence
occurred_at
workspace/project/work_item/workflow_run/node_run/attempt IDs
actor_type + actor_id
correlation_id + causation_id
event_type + redacted payload
artifact/evidence refs
```

## Evidence và chỉ số

- Trace completeness rate.
- Time to root cause.
- Retry-to-fix ratio và no-progress retries.
- Queue, execution, wait và approval time.
- Token/cost theo node/provider.
- Gate coverage và defect escape.
- Evaluator-human agreement, false pass/fail.
- Log truncation/redaction/retention violations.

## Ánh xạ vào Agent Kit/UI

Task detail cần ít nhất:

- graph overlay với node đã qua/đang chạy/chờ;
- timeline event theo attempt;
- branch/rollback decision và lý do;
- gate evidence/output thật;
- context manifest;
- changed artifacts/diff;
- chat và hành động approve/resume/cancel;
- cost/duration/retry/blocker.

Kanban là projection tổng hợp; kéo thả card chỉ phát command và vẫn phải qua transition/gate/permission rules.

## Anti-patterns

- Lưu toàn bộ prompt/source/output thô trong DB.
- Agent tự quyết format log mỗi session.
- Rubric không có anchor hoặc evidence.
- Trace chỉ có “node bắt đầu/node xong” mà thiếu tool/gate cause.
- Downstream tiếp tục khi upstream trả output rỗng nhưng technical status là success.
- Dùng số liệu minh họa của lecture làm SLO production khi chưa benchmark workload thật.

## Phép thử chấp nhận

1. Từ một gate failure, operator truy được WorkItem -> Node -> Attempt -> command -> artifact và route quay lại.
2. Adapter Claude và Codex tạo cùng canonical event types cho lifecycle chung.
3. Secret trong stdout bị redact khỏi event/search nhưng raw artifact được bảo vệ theo policy hoặc không lưu.
4. Hai evaluator chấm khác nhau: UI chỉ ra criterion/evidence gây bất đồng.
