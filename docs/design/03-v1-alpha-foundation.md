# V1 — Alpha foundation

> Entry: V0 verdict `GO`.
>
> Exit: `agentkit serve` khởi động local modular monolith với config, SQLite migrations, artifact root,
> durable worker lifecycle và typed application boundary; chưa có product workflow execution.

## V1-01 — Tách spike command khỏi Alpha composition root

- **Mục tiêu:** thêm `cmd/agentkit` mà không phá `cmd/agentkit-spike` dùng làm regression evidence.
- **Phụ thuộc:** V0 GO.
- **Thực hiện:** tạo subcommand skeleton `serve`, `worker`, `doctor`, `definition`, `evidence`; wiring chỉ
  ở composition root; exit code typed.
- **Verify:** CLI help/golden tests; spike CLI vẫn build/test.
- **Hoàn thành khi:** Alpha command không import package test fixture/spikeacceptance ngoài subcommand evidence.

## V1-02 — Typed ID, clock và error nền

- **Mục tiêu:** thống nhất application-generated IDs, UTC time và safe error envelope.
- **Phụ thuộc:** V1-01.
- **Thực hiện:** named ID types theo aggregate; `IDSource`, `Clock`; error code/retryable/details/private
  cause; bỏ parse message SQL/provider khỏi application decisions.
- **Verify:** unit/property tests cho zero ID, UTC, wrapping và redaction.
- **Hoàn thành khi:** handler mới không dùng raw string ID hoặc `time.Now()` trực tiếp trong domain/app.

## V1-03 — Config loader và startup validation

- **Mục tiêu:** cấu hình immutable theo precedence đã thiết kế.
- **Phụ thuộc:** V1-02.
- **Thực hiện:** safe defaults < file < env < flags; validate paths, TTL/heartbeat, output limit,
  worker ID/concurrency và provider executable; config dump luôn redact secret/reference.
- **Verify:** table tests precedence, invalid combination, secret redaction.
- **Hoàn thành khi:** startup fail-fast với WHAT/WHY/FIX và correlation ID.

## V1-04 — Chuyển migration thành numbered SQL assets

- **Mục tiêu:** không tiếp tục mở rộng một string `migrationV1` trong code.
- **Phụ thuộc:** V1-03.
- **Thực hiện:** giữ checksum/semantics schema hiện có; loader theo version; transaction apply; cấm sửa
  migration đã ghi checksum.
- **Verify:** DB trống, DB migration V1 cũ, checksum tamper và restart idempotency.
- **Hoàn thành khi:** dữ liệu fixture hiện có mở được và migration failure rollback toàn bộ.

## V1-05 — UnitOfWork và repository ports theo concern

- **Mục tiêu:** thay `WorkflowPersistence` spike bằng UoW/repository nhỏ, không lộ SQLite.
- **Phụ thuộc:** V1-02, V1-04.
- **Thực hiện:** Tx exposes catalog/work/definitions/runtime/jobs/events/receipts; query store tách read;
  không nested transaction hoặc external call trong UoW.
- **Verify:** fake UoW handler tests và architecture import test.
- **Hoàn thành khi:** app service có thể test không SQLite và không tồn tại Store tổng hợp public.

## V1-06 — Command envelope, idempotency và expected version

- **Mục tiêu:** mọi mutation dùng cùng command boundary.
- **Phụ thuộc:** V1-05.
- **Thực hiện:** command ID, key, actor, correlation, project, expected version, requested time; receipt
  request hash; duplicate trả stored result, same key/different payload conflict.
- **Verify:** SQLite contract tests duplicate/concurrent/CAS.
- **Hoàn thành khi:** một handler mẫu commit state+event+receipt atomically.

## V1-07 — Domain event và outbox nền

- **Mục tiêu:** không có state quyết định side effect nhưng thiếu durable delivery.
- **Phụ thuộc:** V1-06.
- **Thực hiện:** append aggregate sequence/correlation/causation; outbox same transaction; idempotent
  dispatcher cursor; payload size/redaction limits.
- **Verify:** crash after commit before dispatch, duplicate delivery và sequence conflict tests.
- **Hoàn thành khi:** delivery lặp không tạo hai logical effects.

## V1-08 — Filesystem ArtifactStore production port

- **Mục tiêu:** content-addressed immutable artifact có metadata/hash.
- **Phụ thuộc:** V1-03, V1-05.
- **Thực hiện:** opaque locator, temp write + atomic finalize, size/media/sensitivity/redaction flags,
  traversal defense và verify-open.
- **Verify:** corruption, duplicate content, interrupted write, traversal, Windows/Linux path tests.
- **Hoàn thành khi:** artifact lớn không cần inline SQLite và tamper bị phát hiện.

## V1-09 — Structured logging và redaction pipeline

- **Mục tiêu:** log chẩn đoán có correlation nhưng không trở thành state/evidence giả.
- **Phụ thuộc:** V1-02, V1-03.
- **Thực hiện:** JSON/text local sinks, identity fields, typed failure category, bounded values, shared
  redactor cho event/log/artifact preview.
- **Verify:** secret fixture search bằng 0; correlation fields present tests.
- **Hoàn thành khi:** error safe/public tách raw private cause.

## V1-10 — Embedded worker lifecycle

- **Mục tiêu:** `serve` và `worker` dùng cùng claim/heartbeat/shutdown protocol.
- **Phụ thuộc:** V1-07.
- **Thực hiện:** worker pool bounded, graceful stop nhận việc mới, lease heartbeat, shutdown grace,
  recovery scan startup; handler registry theo job kind.
- **Verify:** cancel shutdown, expired lease reclaim và handler panic containment tests.
- **Hoàn thành khi:** worker crash không crash control loop và job không mất.

## V1-11 — Health/doctor contracts

- **Mục tiêu:** phân biệt liveness, readiness và capability diagnostics.
- **Phụ thuộc:** V1-03, V1-04, V1-08, V1-10.
- **Thực hiện:** kiểm DB/migration, roots, Git, provider executable/capability và lease config; không tạo
  mutation ngoài temp owned fixture.
- **Verify:** doctor JSON golden cho healthy/degraded/blocked.
- **Hoàn thành khi:** mỗi failure có remediation và không in credential/path nhạy cảm quá mức.

## V1-12 — Foundation integration gate

- **Mục tiêu:** khóa V1 bằng clean-start/restart contract.
- **Phụ thuộc:** V1-01…V1-11.
- **Thực hiện:** temp config start serve/worker, apply migration, store artifact, commit event/outbox,
  kill/restart và drain job bằng fake handler.
- **Verify:** `go test ./...`, `go vet ./...`, integration test Windows/Linux CI.
- **Hoàn thành khi:** no adapter import trong domain/app, no schema drift và all foundation evidence pass.
