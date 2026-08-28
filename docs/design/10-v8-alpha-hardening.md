# V8 — Alpha hardening và release verdict

> Entry: V7 browser journey pass.
>
> Exit: Alpha có reproducible build, recovery/security/performance evidence, operator docs và verdict
> phát hành; không mở rộng sang Beta.

## V8-01 — Golden workload và trace completeness

- **Mục tiêu:** cố định workload multi-repo/graph/provider/gate làm regression baseline.
- **Phụ thuộc:** V7.
- **Thực hiện:** deterministic fixture và assertions cho trace IDs, event sequence, revision/evidence links.
- **Verify:** trace completeness report; missing link fail.
- **Hoàn thành khi:** workload chạy clean checkout không cần secret/network.

## V8-02 — Full fault-injection matrix Alpha

- **Mục tiêu:** lặp six crash boundaries với topology serve/worker thực.
- **Phụ thuộc:** V8-01.
- **Thực hiện:** kill API/worker/provider/command, artifact failure, projection interruption, workspace corruption.
- **Verify:** mỗi failure map resume/reconcile/escalate; no duplicate committed side effect.
- **Hoàn thành khi:** unknown outcome không bị biến thành PASS/FAIL giả.

## V8-03 — Concurrency/race/stability soak

- **Mục tiêu:** nhiều root families/workers nhưng invariant lease/workspace/projection giữ đúng.
- **Phụ thuộc:** V8-02.
- **Thực hiện:** bounded workload parallel, same/different repository, duplicate commands/events, 10 runs.
- **Verify:** `go test -race`, UI/API suite, leak/flake report Windows/Linux.
- **Hoàn thành khi:** 10/10 pass và stale fence success bằng 0.

## V8-04 — Security abuse suite

- **Mục tiêu:** kiểm path/process/content/secret boundary Alpha.
- **Phụ thuộc:** V8-01.
- **Thực hiện:** traversal/symlink/reparse, argv/env injection, malicious artifact/YAML, oversized output,
  repo under workspace root, external bind/CORS, redaction corpus.
- **Verify:** automated negative suite và retained-data secret scan.
- **Hoàn thành khi:** deny-by-default failures có safe diagnostic/evidence.

## V8-05 — Retention, cleanup và disk-pressure behavior

- **Mục tiêu:** 7-day policy không phá active/recovery evidence và báo disk full rõ.
- **Phụ thuộc:** V8-02, V8-04.
- **Thực hiện:** fake clock aging, holds/orphans, low-disk simulation, cleanup resume/idempotency.
- **Verify:** before/after manifest và active bundle preservation.
- **Hoàn thành khi:** cleanup chỉ xóa owned eligible artifact; metadata audit giữ nguyên.

## V8-06 — SQLite backup/restore và corruption diagnostics

- **Mục tiêu:** local operator sao lưu/khôi phục consistent DB + artifact manifest.
- **Phụ thuộc:** V8-05.
- **Thực hiện:** explicit maintenance command, SQLite safe backup API, artifact inventory/hash, version check;
  không hứa sync/merge hai installs.
- **Verify:** backup active-disabled window/policy, restore temp root, corrupt/missing artifact cases.
- **Hoàn thành khi:** restored system mở/read/rebuild projection và nhận biết missing evidence.

## V8-07 — Performance budgets và large-state checks

- **Mục tiêu:** đo trước khi phát hành, không lấy số lecture làm SLO.
- **Phụ thuộc:** V8-01.
- **Thực hiện:** baseline project/task/event/artifact sizes; measure startup, Kanban, task detail, projection
  rebuild, scheduler latency, UI large graph/diff; record hardware/profile.
- **Verify:** reproducible benchmark report và regression threshold do data hiện tại thiết lập.
- **Hoàn thành khi:** không có unbounded query/render/memory path trong Alpha workload.

## V8-08 — Reproducible cross-platform build

- **Mục tiêu:** tạo Alpha artifacts Windows/Linux với pinned Go/UI dependencies.
- **Phụ thuộc:** V7-17, V8-03.
- **Thực hiện:** build UI, embed/static serve, build binary, version/schema/adapter manifest, checksums.
- **Verify:** clean CI build twice và compare allowed differences; smoke each artifact.
- **Hoàn thành khi:** binary chạy doctor/serve và UI journey tối thiểu trên cả OS.

## V8-09 — First-run/operator documentation

- **Mục tiêu:** người dùng cài, cấu hình, đăng ký repo, publish workflow, chạy task và recover failure.
- **Phụ thuộc:** V8-08.
- **Thực hiện:** quickstart, config reference, authoring schema examples, provider setup, evidence/retention,
  backup/restore, troubleshooting; chỉ ghi capability đã verify.
- **Verify:** fresh-session test trả lời WHAT/WHERE/HOW/DONE/out-of-scope bằng source.
- **Hoàn thành khi:** clean machine path không cần giải thích miệng ngoài docs.

## V8-10 — Upgrade and rollback rehearsal

- **Mục tiêu:** nâng DB/config/artifact từ release candidate trước mà không sửa migration cũ.
- **Phụ thuộc:** V8-06, V8-08.
- **Thực hiện:** fixture previous-version, upgrade, restart, inspect runs/evidence; rollback binary chỉ khi schema
  compatible, nếu không fail với hướng restore backup.
- **Verify:** upgrade matrix và failure rollback evidence.
- **Hoàn thành khi:** unsupported downgrade không làm hỏng DB im lặng.

## V8-11 — Alpha release acceptance

- **Mục tiêu:** chạy toàn bộ system journeys và MUST criteria trong scope Alpha.
- **Phụ thuộc:** V8-01…V8-10.
- **Thực hiện:** traceability matrix ADR/AK-ARCH/HE criterion → test/evidence/deferred reason; no Beta implementation.
- **Verify:** full Go/UI/API/E2E/fault/security/race/platform suites và evidence verify.
- **Hoàn thành khi:** mọi MUST Alpha có PASS evidence; thiếu môi trường là `CHƯA ĐỦ EVIDENCE`.

## V8-12 — Ghi Alpha verdict và handoff

- **Mục tiêu:** công bố `ALPHA_READY|REWORK|STOP` trung thực và cập nhật entrypoint.
- **Phụ thuộc:** V8-11.
- **Thực hiện:** release report, known limitations, live provider compatibility, checksums, install artifacts,
  update `docs/00-start-here.md`; không tạo Beta backlog chi tiết.
- **Verify:** links/evidence/checksums tồn tại, docs/status nhất quán, fresh install smoke.
- **Hoàn thành khi:** `ALPHA_READY` chỉ khi V8-11 pass; verdict khác có next narrow rework task.
