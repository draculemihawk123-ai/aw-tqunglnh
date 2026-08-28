# V6 — Local HTTP API, SSE và projections

> Entry: V5 core journey pass.
>
> Exit: browser client có stable `/api/v1` command/query contract và rebuildable read models; HTTP
> process không spawn Git/CLI hay truy cập SQLite ngoài application ports.

## V6-01 — HTTP server và middleware nền

- **Mục tiêu:** localhost server có lifecycle/correlation/body limit/content type/error mapping.
- **Phụ thuộc:** V5.
- **Thực hiện:** route composition, graceful shutdown, request ID, safe error envelope, JSON strict decode.
- **Verify:** malformed/oversized/cancel/panic/error-code tests.
- **Hoàn thành khi:** handler không expose private cause/SQL/provider output.

## V6-02 — Idempotency/optimistic concurrency HTTP contract

- **Mục tiêu:** mutation bắt buộc `Idempotency-Key`, update dùng `If-Match`.
- **Phụ thuộc:** V6-01, V1-06.
- **Thực hiện:** middleware/DTO mapping, replay stored response, conflict status, version ETag.
- **Verify:** duplicate/same-key-different-body/stale version/concurrent requests.
- **Hoàn thành khi:** retry browser không tạo duplicate aggregate/job.

## V6-03 — Project/repository/component endpoints

- **Mục tiêu:** expose V3 catalog/onboarding commands/queries.
- **Phụ thuộc:** V6-02.
- **Verify:** OpenAPI/contract fixtures success/validation/not-found/conflict.
- **Hoàn thành khi:** local path chỉ xuất ở view được phép và repository ID là authority.

## V6-04 — WorkItem/family/scope endpoints

- **Mục tiêu:** create/list/detail/child/readiness/scope expansion/decision.
- **Phụ thuộc:** V6-03.
- **Verify:** multi-repo filters, subset violations, approval and version conflict contracts.
- **Hoàn thành khi:** client không thể trực tiếp set DONE hoặc family/workspace IDs.

## V6-05 — Definition validate/publish endpoints

- **Mục tiêu:** expose V2 compiler bằng text/file payload có bounded size.
- **Phụ thuộc:** V6-02.
- **Verify:** location diagnostics, duplicate publish, version list/diff contracts.
- **Hoàn thành khi:** API không lưu invalid draft thành runtime version.

## V6-06 — Run/approval/cancel endpoints

- **Mục tiêu:** start run, graph query, approve/reject, cancel và diagnostics.
- **Phụ thuộc:** V6-04, V6-05.
- **Verify:** invalid state/pinned version/duplicate command contracts.
- **Hoàn thành khi:** handler chỉ dispatch typed application command.

## V6-07 — Conversation/evidence/artifact endpoints

- **Mục tiêu:** append/list message, list evidence, stream verified artifact.
- **Phụ thuộc:** V6-02.
- **Thực hiện:** range/size/media headers, sensitivity policy, no trusted HTML, content hash ETag.
- **Verify:** traversal/unauthorized-shaped ID/tamper/large stream/redaction tests.
- **Hoàn thành khi:** raw filesystem locator không xuất API.

## V6-08 — Projection event consumer

- **Mục tiêu:** idempotently build Kanban và task detail read models từ domain events.
- **Phụ thuộc:** V1-07, V6-04.
- **Thực hiện:** cursor transaction, duplicate/out-of-order handling, projection lag/freshness metadata.
- **Verify:** replay twice, stop/restart, gap and poison event tests.
- **Hoàn thành khi:** runtime không query projection cho readiness/completion.

## V6-09 — Projection rebuild command

- **Mục tiêu:** xóa/rebuild read model cho cùng result tại cursor.
- **Phụ thuộc:** V6-08.
- **Thực hiện:** exclusive rebuild operation, shadow tables/swap hoặc safe truncate/replay, progress diagnostics.
- **Verify:** before/after canonical projection diff và interrupted rebuild recovery.
- **Hoàn thành khi:** authoritative tables/events không bị sửa.

## V6-10 — Kanban/task detail query endpoints

- **Mục tiêu:** project-level views, repository/component filters và freshness.
- **Phụ thuộc:** V6-08.
- **Verify:** pagination/filter/sort/stale projection contracts.
- **Hoàn thành khi:** task multi-repo vẫn là một card với repository badges.

## V6-11 — SSE event stream

- **Mục tiêu:** UI nhận invalidation/runtime summary và reconnect theo event ID.
- **Phụ thuộc:** V6-08.
- **Thực hiện:** project filter, heartbeat, bounded client buffer, slow-client disconnect, redacted payload.
- **Verify:** reconnect/duplicate/lag/slow consumer/shutdown tests.
- **Hoàn thành khi:** SSE không chứa artifact/log lớn hoặc secret fixture.

## V6-12 — Machine-readable API contract

- **Mục tiêu:** version schema cho DTO/error/routes để UI không dựa implementation detail.
- **Phụ thuộc:** V6-03…V6-11.
- **Thực hiện:** generate/maintain OpenAPI hoặc equivalent checked artifact từ authoritative route schemas;
  compatibility diff gate.
- **Verify:** schema validation của golden requests/responses; breaking diff fail.
- **Hoàn thành khi:** every public endpoint/example/error code documented.

## V6-13 — API security and boundary tests

- **Mục tiêu:** chứng minh HTTP process không có execution fast path.
- **Phụ thuộc:** V6-12.
- **Thực hiện:** import/architecture test, localhost default, CORS deny default, path/content injection,
  request cancellation và artifact MIME safety.
- **Verify:** automated security/boundary suite.
- **Hoàn thành khi:** no handler imports Git/process/provider/SQLite concrete package.

## V6-14 — API/projection acceptance gate

- **Mục tiêu:** vận hành core journey chỉ qua HTTP và quan sát qua projection/SSE.
- **Phụ thuộc:** V6-01…V6-13.
- **Thực hiện:** create project/definition/task/run/message/approval; await SSE; inspect evidence; rebuild projection.
- **Verify:** black-box test from clean DB, full/race/Windows/Linux.
- **Hoàn thành khi:** không có test setup sửa SQLite/Git runtime state thủ công.
