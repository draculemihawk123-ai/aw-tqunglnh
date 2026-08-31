# V1 — Alpha foundation

> Entry: V0 verdict `GO`.
>
> Exit: `agentkit serve` khởi động local modular monolith với config, SQLite migrations, artifact root,
> durable worker lifecycle và typed application boundary; chưa có product workflow execution.

> Phạm vi task mặc định: kế thừa mục 3 của `00-roadmap.md`.

## V1-00A — Criterion inventory và stable ID (pre-V1 gate)

> Ba task V1-00A…C là **pre-V1 gate**. ADR-024 yêu cầu phân loại xong *trước* khi V1 bắt đầu, nên không
> task nào khác ngoài chính chúng được bắt đầu trước khi V1-00C pass.

- **Mục tiêu:** mọi criterion có một ID ổn định để tham chiếu được; hiện AK-ARCH và HE đã có ID nhưng
  invariant/acceptance của Go core mới chỉ là danh sách đánh số, không phân giải được.
- **Phụ thuộc:** V0 GO.
- **Phạm vi:** `docs/architecture/` và `docs/harness-engineering/`; chỉ gán ID và nhãn, không sửa nội
  dung criterion.
- **Thực hiện:** gán `GC-INV-NN` cho từng invariant Go core spec §5, `GC-ACC-NN` cho từng mục §22 và
  `GC-DS-NN` cho từng downstream target §22.1; gán
  nhãn phase `ALPHA_MUST|BETA_ADAPTER_GATE|BETA_PARITY_GATE|CROSS_PHASE_GUARD|NOT_APPLICABLE` cho toàn
  bộ AK-ARCH, HE và GC-*; `NOT_APPLICABLE` phải kèm authority reason.
- **Verify:** mỗi criterion có đúng một ID và đúng một nhãn; ba ngoại lệ đã chốt (AK-ARCH-019, 026, 028)
  khớp bảng trong `03-system-architecture.md` §17.
- **Hoàn thành khi:** mọi `SourceRef` hợp lệ đều phân giải được tới một mục có thật.
- **Nguồn:** ADR-024.

## V1-00B — Mapping và backfill `Nguồn` (pre-V1 gate)

- **Mục tiêu:** mọi task V1…V8 khai nguồn của mình, và V0 vẫn nằm trong coverage qua SPK.
- **Phụ thuộc:** V1-00A.
- **Phạm vi:** `docs/design/`; không sửa criterion.
- **Thực hiện:** backfill trường `Nguồn` cho toàn bộ Task ID V1…V8 theo grammar `SourceRef` ở
  `00-roadmap.md` §3; map SPK-01…14 sang criterion tương ứng để V0 được tính coverage dù task V0 không
  mang trường này.
- **Verify:** không task V1…V8 nào thiếu `Nguồn`; mọi SPK map được sang ít nhất một criterion.
- **Hoàn thành khi:** debt `Nguồn` bằng 0 theo báo cáo của checker, không theo con số ghi tay.
- **Nguồn:** ADR-024, ROADMAP-§3.

## V1-00C — Coverage checker và pre-V1 aggregate gate

- **Mục tiêu:** biến coverage thành thứ máy kiểm được, thay vì một tuyên bố trong tài liệu.
- **Phụ thuộc:** V1-00A, V1-00B.
- **Phạm vi:** checker chạy trong CI; không sửa code sản phẩm.
- **Thực hiện:** dựng reverse coverage index từ các file version; **tokenize toàn bộ** trường `Nguồn` và
  reject nếu **bất kỳ** token nào sai grammar — không chấp nhận "có ít nhất một prefix hợp lệ"; phân
  biệt nguồn quyết định (`ADR`, `ROADMAP`) với criterion mang nhãn phase (`AK-ARCH`, `HE`, `GC-*`);
  sinh báo cáo debt hiện tại thay vì so với một con số hard-code.
- **Verify:** checker fail khi (a) criterion thiếu nhãn phase; (b) `ALPHA_MUST` không có owner; (c) task
  V1…V8 thiếu `Nguồn`; (d) token `SourceRef` sai grammar hoặc trỏ tới mục không tồn tại; (e) criterion
  mang hai nhãn; (f) `NOT_APPLICABLE` thiếu authority reason; (g) SPK không map được.
- **Hoàn thành khi:** checker chạy **thành công** trong CI với debt bằng 0. Chỉ sau mốc này bộ thiết kế
  mới được gọi là execution-ready.
- **Nguồn:** ADR-024, ROADMAP-§3.

## V1-01 — Tách spike command khỏi Alpha composition root

- **Mục tiêu:** thêm `cmd/agentkit` mà không phá `cmd/agentkit-spike` dùng làm regression evidence.
- **Phụ thuộc:** V1-00C (pre-V1 gate) — đây là task code đầu tiên của Alpha.
- **Thực hiện:** tạo subcommand skeleton `serve`, `worker`, `doctor`, `definition`, `evidence`; wiring chỉ
  ở composition root; exit code typed.
- **Verify:** CLI help/golden tests; spike CLI vẫn build/test.
- **Hoàn thành khi:** Alpha command không import package test fixture/spikeacceptance ngoài subcommand evidence.
- **Handoff:** `agentkit-spike` là live regression gate suốt V1 (roadmap §5B), không phải code đóng
  băng; task nào phá build/test của nó phải sửa trong cùng task, không được retire ngầm.
- **Nguồn:** ADR-006.

## V1-02 — Typed ID, clock và error nền

- **Mục tiêu:** thống nhất application-generated IDs, UTC time và safe error envelope.
- **Phụ thuộc:** V1-01.
- **Thực hiện:** named ID types theo aggregate; `IDSource`, `Clock`; error code/retryable/details/private
  cause; bỏ parse message SQL/provider khỏi application decisions.
- **Verify:** unit/property tests cho zero ID, UTC, wrapping và redaction.
- **Hoàn thành khi:** handler mới không dùng raw string ID hoặc `time.Now()` trực tiếp trong domain/app.

## V1-02A — Redactor dùng chung trước mọi persistence sink

- **Mục tiêu:** secret không thể lọt vào config dump, event, receipt, log hoặc artifact metadata ngay
  từ các task nền đầu tiên.
- **Phụ thuộc:** V1-02.
- **Phạm vi:** redaction port/policy, fixture và test helper; chưa triển khai logging sink.
- **Thực hiện:** typed sensitivity, exact secret fixture matcher, bounded recursive redaction và safe
  preview; mọi sink sau phải nhận dữ liệu qua cùng contract.
- **Verify:** property/table tests nested value, argv/env/error/detail và false-positive allowlist.
- **Hoàn thành khi:** config/event/artifact/log task có một redactor nền bắt buộc để reuse.

## V1-03 — Config loader và startup validation

- **Mục tiêu:** cấu hình immutable theo precedence đã thiết kế.
- **Phụ thuộc:** V1-02, V1-02A.
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

## V1-04A — SQLite connection/transaction policy và error mapping

- **Mục tiêu:** khóa lớp đã làm spike vấp hai lần (`SQLITE_BUSY` rò ra ngoài ở lease race và ở finalize
  đồng thời) trước khi có repository nào được viết trên nó.
- **Phụ thuộc:** V1-03, V1-04.
- **Phạm vi:** `internal/adapters/sqlite` connection factory, transaction runner và error mapper; chưa
  viết repository nghiệp vụ.
- **Thực hiện:** `foreign_keys=ON` trên mọi connection; bật và verify WAL lúc startup; `busy_timeout` từ
  config; khai rõ durability mode và cấm hạ xuống `OFF`; expose semantic transaction option
  (`SerializedWrite` cho claim/version/JournalPosition allocation) để application không biết
  `BEGIN IMMEDIATE`; bounded retry rồi map `SQLITE_BUSY|SQLITE_LOCKED` theo quy tắc xác định: lock
  contention thuần túy → `UNAVAILABLE` (retryable), còn `CONFLICT` chỉ khi reload xác nhận
  expected-version/CAS đã thua thật sự.
- **Verify:** contract test dùng nhiều connection thật tái hiện contention; assert không error nào mang
  chuỗi SQLite rò ra ngoài adapter; assert WAL/pragma thực sự áp cho connection mới; assert contention
  thuần túy trả `UNAVAILABLE` chứ không phải `CONFLICT`, và CAS thua thật trả `CONFLICT`.
- **Hoàn thành khi:** không call site nào ngoài adapter nhắc tới `BEGIN IMMEDIATE` hoặc phân loại lỗi
  bằng message, và PostgreSQL adapter tương lai chỉ cần ánh xạ lại cùng option.
- **Nguồn:** ADR-008, GC-DS-10.

## V1-05 — UnitOfWork và repository ports theo concern

- **Mục tiêu:** thay `WorkflowPersistence` spike bằng UoW/repository nhỏ, không lộ SQLite.
- **Phụ thuộc:** V1-02, V1-04, V1-04A.
- **Thực hiện:** Tx exposes catalog/work/definitions/runtime/jobs/events/receipts; query store tách read;
  không nested transaction hoặc external call trong UoW; UoW nhận semantic transaction option của
  V1-04A, không nhận SQL/pragma.
- **Verify:** fake UoW handler tests và architecture import test.
- **Hoàn thành khi:** app service có thể test không SQLite và không tồn tại Store tổng hợp public.

## V1-06 — Command envelope, idempotency và expected version

- **Mục tiêu:** mọi mutation dùng cùng command boundary.
- **Phụ thuộc:** V1-05.
- **Thực hiện:** command ID, key, actor, correlation, `CommandScope = INSTALLATION | PROJECT(ProjectID)`,
  expected version, requested time; `command_receipts` lưu **`scope_key` non-null** (`installation` hoặc
  `project:<id>`) thay cho `project_id` — không dùng cột nullable trong unique tuple vì SQLite cho phép
  nhiều NULL và duplicate installation command sẽ lọt; handler load aggregate theo `Scope + ID`; receipt request hash; duplicate trả stored result, same
  key/different payload conflict.
- **Verify:** SQLite contract tests duplicate/concurrent/CAS; command project-scoped thiếu ProjectID và
  command installation-scoped mang ProjectID đều bị reject; **duplicate và concurrent installation
  command** cùng idempotency key phải trả stored result chứ không tạo hai receipt.
- **Hoàn thành khi:** một handler mẫu commit state+event+receipt atomically.

## V1-07 — Domain event và outbox nền

- **Mục tiêu:** không có state quyết định side effect nhưng thiếu durable delivery.
- **Phụ thuộc:** V1-02A, V1-06.
- **Thực hiện:** append aggregate sequence/correlation/causation và JournalPosition database-monotonic;
  outbox same transaction; idempotent
  dispatcher cursor; payload size/redaction limits.
- **Verify:** crash after commit before dispatch, duplicate delivery và sequence conflict tests.
- **Hoàn thành khi:** delivery lặp không tạo hai logical effects.
- **Nguồn:** ADR-008, ADR-015.

## V1-07A — Registry schema của domain event

- **Mục tiêu:** `domain_events.schema` chỉ có giá trị khi có registry cưỡng chế; projection rebuild ở V6
  phải replay được mọi event version lịch sử, không riêng version mới nhất.
- **Phụ thuộc:** V1-07.
- **Phạm vi:** event registry, decoder/upcaster contract và golden fixtures; chưa viết projection.
- **Thực hiện:** registry `(event_type, schema_version)`; từ chối emit event chưa đăng ký ngay tại
  boundary append; breaking payload phải tăng schema version, giữ raw event bất biến và có deterministic
  decoder/upcaster kèm golden fixture.
- **Verify:** emit unregistered type/version bị reject; decode golden fixture của mọi version đã đăng ký;
  test fail khi decoder đang được fixture tham chiếu bị xóa.
- **Hoàn thành khi:** CI chặn được thay đổi payload phá replay, thay vì phát hiện lúc rebuild ở V6-09.
- **Nguồn:** ADR-008, ADR-015, AK-ARCH-022.

## V1-08 — Filesystem ArtifactStore production port

- **Mục tiêu:** content-addressed immutable artifact có metadata/hash.
- **Phụ thuộc:** V1-02A, V1-03, V1-05.
- **Thực hiện:** opaque locator, temp write + atomic finalize, size/media/sensitivity/redaction flags,
  traversal defense và verify-open.
- **Verify:** corruption, duplicate content, interrupted write, traversal, Windows/Linux path tests.
- **Hoàn thành khi:** artifact lớn không cần inline SQLite và tamper bị phát hiện.

## V1-09 — Structured logging và redaction pipeline

- **Mục tiêu:** log chẩn đoán có correlation nhưng không trở thành state/evidence giả.
- **Phụ thuộc:** V1-02A, V1-03.
- **Thực hiện:** JSON/text local sinks, identity fields, typed failure category, bounded values; bắt
  buộc reuse redactor V1-02A cho event/log/artifact preview.
- **Verify:** secret fixture search bằng 0; correlation fields present tests.
- **Hoàn thành khi:** error safe/public tách raw private cause.

## V1-10 — Embedded worker lifecycle

- **Mục tiêu:** `serve` và `worker` dùng cùng claim/heartbeat/shutdown protocol.
- **Phụ thuộc:** V1-07.
- **Thực hiện:** worker pool bounded, graceful stop nhận việc mới, lease heartbeat, shutdown grace,
  startup scan và periodic recovery reaper dùng database time/idempotency; handler registry theo kind.
- **Verify:** cancel shutdown, lease hết hạn sau startup, hai worker tranh recovery, không tạo recovery
  job trùng và handler panic containment tests.
- **Hoàn thành khi:** worker crash không crash control loop và job không mất.

## V1-11 — Health/doctor contracts

- **Mục tiêu:** phân biệt liveness, readiness và capability diagnostics.
- **Phụ thuộc:** V1-03, V1-04, V1-08, V1-10.
- **Thực hiện:** kiểm DB/migration, roots, Git, provider executable/capability, isolation profile,
  loopback API config và lease/reaper config; không tạo mutation ngoài temp owned fixture. Ở V1, Doctor
  chỉ báo **observed** executable fingerprint/capability và MUST NOT tuyên bố registry admission —
  AdapterBuildVersion registry chỉ tồn tại từ V2-07A.
- **Verify:** doctor JSON golden cho healthy/degraded/blocked.
- **Hoàn thành khi:** mỗi failure có remediation, không in credential/path nhạy cảm quá mức, và output
  phân biệt rõ observed fingerprint với registered build.
- **Nguồn:** ADR-022.

## V1-12 — Foundation integration gate

- **Mục tiêu:** khóa V1 bằng clean-start/restart contract.
- **Phụ thuộc:** V1-00A…V1-00C, V1-01…V1-11, V1-02A, V1-04A và V1-07A.
- **Thực hiện:** temp config start serve/worker, apply migration, store artifact, commit event/outbox,
  kill/restart và drain job bằng fake handler; chạy lại full offline SPK-01…14 sau toàn bộ refactor V1.
- **Verify:** `go test ./...`, `go vet ./...`, integration test Windows/Linux CI, và
  `agentkit-spike acceptance --offline` với evidence verify pass.
- **Hoàn thành khi:** no adapter import trong domain/app, no schema drift, all foundation evidence pass,
  và SPK-01…14 vẫn xanh — SPK fail là blocker của V1, không được retire spike để đóng version.
- **Nguồn:** ROADMAP-§5B.
