# V8 — Alpha hardening và release verdict

> Entry: V7 browser journey pass.
>
> Exit: Alpha có reproducible build, recovery/security/performance evidence, operator docs và verdict
> phát hành; không mở rộng sang Beta.

> Phạm vi task mặc định: kế thừa mục 3 của `00-roadmap.md`.

## V8-01 — Golden workload và trace completeness

- **Mục tiêu:** cố định workload multi-repo/graph/provider/gate làm regression baseline.
- **Phụ thuộc:** V7.
- **Thực hiện:** deterministic fixture và assertions cho trace IDs, event sequence, revision/evidence links.
- **Verify:** trace completeness report; missing link fail.
- **Hoàn thành khi:** workload chạy clean checkout không cần secret/network.
- **Nguồn:** AK-ARCH-021, HE-11-S03, HE-01-M06, HE-10-M05.

## V8-02 — Full fault-injection matrix Alpha

- **Mục tiêu:** lặp six crash boundaries với topology serve/worker thực.
- **Phụ thuộc:** V8-01.
- **Thực hiện:** kill API/worker/provider/command ở đủ sáu boundary, artifact failure, projection poison/
  interruption, periodic lease expiry và workspace corruption.
- **Verify:** mỗi failure map fresh-Start/retry/reconcile/escalate; no duplicate authoritative outcome
  hoặc committed side effect.
- **Hoàn thành khi:** unknown outcome không bị biến thành PASS/FAIL giả.
- **Nguồn:** GC-ACC-16.

## V8-03 — Concurrency/race/stability soak

- **Mục tiêu:** nhiều root families/workers nhưng invariant lease/workspace/projection giữ đúng.
- **Phụ thuộc:** V8-02.
- **Thực hiện:** bounded workload parallel, same/different repository, duplicate commands/events, 10 runs.
- **Verify:** `go test -race`, UI/API suite, leak/flake report Windows/Linux.
- **Hoàn thành khi:** 10/10 pass và stale fence success bằng 0.
- **Nguồn:** AK-ARCH-009, GC-ACC-09, HE-10-M08.

> V8-04A…V8-04D là các suite nhỏ chạy độc lập; V8-04E là aggregate gate trên kết quả của chúng.
> Aggregate mang hậu tố E để nó đứng sau các suite theo thứ tự Task ID.
>
> **Có thể song song:** V8-04A…V8-04D đều chỉ phụ thuộc V8-01, nằm ở bốn test package khác nhau và
> không sửa chung contract/schema. V8-04E phải chạy sau cả bốn.

## V8-04A — Filesystem và path abuse suite

- **Mục tiêu:** đóng lớp path boundary trên cả hai OS.
- **Phụ thuộc:** V8-01.
- **Thực hiện:** traversal, symlink/reparse escape, repo nằm dưới workspace root, artifact/workspace root
  containment, cleanup chỉ chạm owned path.
- **Verify:** negative suite Windows/Linux với fixture thật.
- **Hoàn thành khi:** mọi từ chối có safe diagnostic và evidence.
- **Nguồn:** AK-ARCH-027.

## V8-04B — Process, executable và isolation abuse suite

- **Mục tiêu:** đóng lớp thực thi.
- **Phụ thuộc:** V8-01.
- **Thực hiện:** argv/env injection, oversized output, adapter build drift, multi-repo write thiếu
  capability, checker source-write, remote Git, và isolation-profile lie gồm auto-downgrade lẫn
  `ISOLATION_ENFORCEMENT_UNAVAILABLE`.
- **Verify:** negative suite; spawn count bằng 0 cho case isolation; remote mutation call count bằng 0.
- **Hoàn thành khi:** không có đường nào thực thi vượt policy đã publish.
- **Nguồn:** ADR-013, ADR-023, AK-ARCH-015B, AK-ARCH-020A.

## V8-04C — Local HTTP trust boundary suite

- **Mục tiêu:** đóng lớp transport của ADR-016.
- **Phụ thuộc:** V8-01.
- **Thực hiện:** external bind, DNS-rebinding Host, foreign/missing Origin, missing/wrong session token,
  CORS deny-by-default, content/MIME injection.
- **Verify:** automated negative suite; assert token không xuất hiện trong URL/log/durable state.
- **Hoàn thành khi:** mọi mutation không hợp lệ bị từ chối trước khi chạm application command.
- **Nguồn:** ADR-016, AK-ARCH-025A.

## V8-04D — Content, redaction và secret-scan suite

- **Mục tiêu:** đóng lớp dữ liệu giữ lại.
- **Phụ thuộc:** V8-01.
- **Thực hiện:** malicious artifact/YAML, redaction corpus, retained-data secret scan trên DB/event/log/
  artifact.
- **Verify:** secret fixture search bằng 0; artifact không execute như trusted HTML.
- **Hoàn thành khi:** không có sink nào bỏ qua redactor dùng chung.
- **Nguồn:** ADR-017, AK-ARCH-024.

## V8-04E — Security aggregate gate

- **Mục tiêu:** một verdict duy nhất trên bốn suite trên.
- **Phụ thuộc:** V8-04A…V8-04D.
- **Thực hiện:** chạy toàn bộ suite, tổng hợp kết quả theo criterion và ghi evidence.
- **Verify:** aggregate report; một suite fail làm gate fail.
- **Hoàn thành khi:** deny-by-default failures có safe diagnostic/evidence và không suite nào bị bỏ qua.
- **Nguồn:** ADR-013, ADR-016, ADR-017, ADR-023.

## V8-05 — Retention-class, cleanup và disk-pressure behavior

- **Mục tiêu:** TTL 7 ngày của raw output/evidence tạm không lan sang canonical conversation/context,
  không phá recovery evidence và báo disk full rõ.
- **Phụ thuộc:** V8-02, V8-04E.
- **Thực hiện:** fake clock aging theo class, holds/references/orphans, canonical Message/context/audit,
  low-disk simulation, cleanup recovery/idempotency.
- **Verify:** before/after manifest và active bundle preservation.
- **Hoàn thành khi:** cleanup chỉ xóa owned eligible payload; conversation, referenced context, hold và
  metadata audit giữ nguyên.
- **Nguồn:** AK-ARCH-025B.

## V8-06 — SQLite backup/restore và corruption diagnostics

- **Mục tiêu:** local operator sao lưu/khôi phục consistent DB + artifact manifest.
- **Phụ thuộc:** V8-05.
- **Thực hiện:** explicit maintenance command, SQLite safe backup API, artifact inventory/hash, version check;
  không hứa sync/merge hai installs.
- **Verify:** backup active-disabled window/policy, restore temp root, corrupt/missing artifact cases.
- **Hoàn thành khi:** restored system mở/read/rebuild projection và nhận biết missing evidence.
- **Nguồn:** ROADMAP-§7.

## V8-07 — Performance budgets và large-state checks

- **Mục tiêu:** đo trước khi phát hành, không lấy số lecture làm SLO.
- **Phụ thuộc:** V8-01.
- **Thực hiện:** baseline project/task/event/artifact sizes; đo startup, Kanban, task detail, projection
  rebuild, scheduler latency, UI large graph/diff; ghi hardware/profile rồi freeze numeric threshold
  cùng owner/reason trước khi đo release candidate cuối.
- **Verify:** reproducible benchmark report; pre-frozen threshold pass/fail, không đặt ngưỡng sau khi xem RC.
- **Hoàn thành khi:** không có unbounded query/render/memory path trong Alpha workload.
- **Nguồn:** ROADMAP-§6.

## V8-08 — Reproducible cross-platform build

- **Mục tiêu:** tạo Alpha artifacts Windows/Linux với pinned Go/UI dependencies.
- **Phụ thuộc:** V7-17, V8-03.
- **Thực hiện:** build UI và đóng gói vào binary `aw`/`aw.exe`, build binary, version/schema/adapter manifest,
  checksums. Việc `aw serve` phục vụ được UI đã là điều kiện của V7-02; task này chỉ lo
  reproducibility, cross-platform packaging và release smoke.
- **Verify:** clean CI build twice và compare bằng allowlist field cụ thể được version-control; unknown
  difference fail; smoke each artifact.
- **Hoàn thành khi:** binary chạy doctor/serve và UI journey tối thiểu trên cả OS.
- **Nguồn:** AK-ARCH-020, HE-02-M04, ADR-028.

## V8-09 — First-run/operator documentation

- **Mục tiêu:** người dùng cài, cấu hình, đăng ký repo, publish workflow, chạy task và recover failure.
- **Phụ thuộc:** V8-08.
- **Thực hiện:** quickstart, config reference, authoring schema examples, provider/isolation setup,
  đầy đủ `aw` command/flag/JSON/exit-code reference, ReleaseSet/local-only Git, source/diff/log (không
  interactive browser terminal), evidence/retention classes, backup/restore, troubleshooting; chỉ ghi
  capability đã verify.
- **Verify:** fresh-session test trả lời WHAT/WHERE/HOW/DONE/out-of-scope bằng source.
- **Hoàn thành khi:** clean machine path không cần giải thích miệng ngoài docs.
- **Nguồn:** HE-03-M05, HE-06-S05, HE-03-M03, HE-04-M01, ADR-028.

## V8-10 — Upgrade and rollback rehearsal

- **Mục tiêu:** nâng DB/config/artifact từ release candidate trước mà không sửa migration cũ.
- **Phụ thuộc:** V8-06, V8-08.
- **Thực hiện:** fixture previous-version, upgrade, restart, inspect runs/evidence; rollback binary chỉ khi schema
  compatible, nếu không fail với hướng restore backup.
- **Verify:** upgrade matrix và failure rollback evidence.
- **Hoàn thành khi:** unsupported downgrade không làm hỏng DB im lặng.
- **Nguồn:** ROADMAP-§7.

## V8-11 — Alpha release acceptance

- **Mục tiêu:** luôn tạo assessment đầy đủ cho toàn bộ system journeys và MUST criteria trong scope
  Alpha, kể cả khi suite có failure.
- **Phụ thuộc:** V8-01…V8-10.
- **Thực hiện:** tổng hợp evidence lên coverage map đã dựng từ V1-00A…V1-00C cho ADR-001…028, model workspace,
  Go core MUST/acceptance, system-design journeys, HE criteria và V0–V7 gates →
  test/evidence/failure/nhãn phase. V8 **không** được phân loại lại criteria: nhãn phase đã chốt ở V1-00A
  theo ADR-024. Không dùng `deferred` cho một `ALPHA_MUST` và không implement Beta.
- **Verify:** full Go/UI/API/E2E/fault/security/race/platform suites và evidence verify; cộng gate cuối
  bắt buộc: cancel-vs-claim chạy cả hai thứ tự commit, route inventory khớp OpenAPI theo hai chiều, mọi
  recovery command có đủ owner core/API/UI/CLI, parity inventory UI↔operationId↔`aw`↔application
  operation không có debt, checker báo SourceRef debt bằng 0, rồi `git diff --check`,
  `go test ./...` và `go vet ./...`.
- **Hoàn thành khi:** assessment matrix hoàn chỉnh và machine-readable `gatePass` được tính; mọi
  `ALPHA_MUST` phải PASS để `gatePass=true`, thiếu môi trường là `CHƯA ĐỦ EVIDENCE`, và criterion mang
  nhãn Beta được báo cáo là ngoài phạm vi Alpha chứ không phải failure.
- **Nguồn:** ADR-024.

## V8-12 — Ghi Alpha verdict và handoff

- **Mục tiêu:** công bố `ALPHA_READY|REWORK|STOP|CHƯA ĐỦ EVIDENCE` trung thực và cập nhật entrypoint.
- **Phụ thuộc:** V8-11 đã tạo assessment matrix/trạng thái; không yêu cầu `gatePass=true` để chạy verdict.
- **Thực hiện:** release report, known limitations, live provider compatibility, checksums, install artifacts,
  update `docs/00-start-here.md`; không tạo Beta backlog chi tiết.
- **Verify:** links/evidence/checksums tồn tại, docs/status nhất quán, fresh install smoke.
- **Hoàn thành khi:** `ALPHA_READY` chỉ khi `gatePass=true`; verdict khác ghi blocker và next narrow
  rework/evidence task.
- **Nguồn:** ADR-024, ROADMAP-§3.
