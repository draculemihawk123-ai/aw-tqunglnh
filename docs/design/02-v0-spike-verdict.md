# V0 — Hoàn tất Go spike và đưa ra verdict

> Entry: spike report đang `IN PROGRESS`.
>
> Exit: SPK-01…14 pass offline trên Windows/Linux, race/stability pass, evidence verify được và report
> ghi `GO`; nếu gate fail hoặc môi trường chưa đủ thì vẫn chạy assessment và ghi
> `REWORK|STOP|CHƯA ĐỦ EVIDENCE`.
>
> Không được làm: Alpha API/UI, production feature, hạ gate hoặc thay PASS bằng local unit pass.

> Phạm vi task mặc định: kế thừa mục 3 của `00-roadmap.md`; task không ghi `Phạm vi` riêng vẫn chỉ
> được sửa đúng code/test/fixture/doc cần cho mục tiêu và bước thực hiện của task đó.

## V0-01 — Khóa schema acceptance result theo từng SPK

- **Mục tiêu:** runner phát một result có `spkId`, assertions, correlation IDs, platform, timing và
  artifact refs; baseline `go test` không còn được hiểu là verdict SPK.
- **Phụ thuộc:** hiện trạng spike.
- **Phạm vi:** `internal/spikeacceptance`, CLI acceptance, fixture/result tests.
- **Thực hiện:** định nghĩa typed result/manifest; reject thiếu SPK-01…14 hoặc ID trùng; giữ backward
  verify cho bundle baseline chỉ dưới nhãn non-verdict.
- **Verify:** unit test manifest thiếu/trùng/đủ 14 SPK và `go test ./internal/spikeacceptance`.
- **Hoàn thành khi:** machine có thể phân biệt rõ suite baseline với full SPK verdict.

## V0-01A — Đăng ký đủ 14 scenario vào full-suite runner

- **Mục tiêu:** mỗi SPK-01…14 có scenario runnable thật, không chỉ có constant/result ID trong manifest.
- **Phụ thuộc:** V0-01.
- **Phạm vi:** registry, CLI dispatcher và negative fixture; không implement behavior còn thiếu của SPK.
- **Thực hiện:** map chính xác từng `spkId` tới scenario entrypoint; reject missing/duplicate/no-op
  handler; full suite phải gọi đủ 14 và thu result thực của từng handler.
- **Verify:** test registry thiếu/trùng/no-op, spy execution count và clean full-suite dry run.
- **Hoàn thành khi:** manifest đủ 14 nhưng thiếu một wiring runnable luôn fail trước verdict.

## V0-02 — Nối checkpoint/context vào hard-crash flow SPK-03

- **Mục tiêu:** process worker thật bị kill sau checkpoint; replacement Attempt luôn gọi `Start` từ
  ContextSnapshot durable.
- **Phụ thuộc:** V0-01.
- **Phạm vi:** crash fixture, worker recovery, SQLite checkpoint/context; không sửa provider domain.
- **Thực hiện:** persist snapshot trước spawn, checkpoint sequence, kill child worker, expire/recover
  lease, tạo Attempt mới và assert node committed không chạy lại.
- **Verify:** test subprocess riêng cho SPK-03; assert Attempt IDs khác và `Resume` call count bằng 0.
- **Hoàn thành khi:** SPK-03 có evidence timeline/checkpoint/context/terminal đầy đủ.

## V0-03 — Fault sau process exit trước outcome commit

- **Mục tiêu:** đóng fault point đầu còn thiếu của SPK-04.
- **Phụ thuộc:** V0-02.
- **Phạm vi:** named fault hook và crash integration test.
- **Thực hiện:** kill worker sau external exit nhưng trước finalize; read-only attempt phải thành
  `LOST`, mutating attempt phải thành `INDETERMINATE` rồi reconcile bằng evidence; tuyệt đối không suy
  exit code 0 thành success.
- **Verify:** chạy test hai lần với side effect read-only và mutating.
- **Hoàn thành khi:** không duplicate terminal/outbox và kết quả recovery có typed reason.

## V0-04 — Fault sau node complete trước next dispatch

- **Mục tiêu:** chứng minh transition/outbox atomically giữ bước tiếp theo.
- **Phụ thuộc:** V0-03.
- **Phạm vi:** scheduler/outbox fixture; không thêm production scheduler.
- **Thực hiện:** kill đúng boundary sau commit node; restart dispatcher; deduplicate transition và tạo
  downstream job đúng một lần.
- **Verify:** subprocess crash test đếm node activation, event sequence và job idempotency.
- **Hoàn thành khi:** boundary này pass và có artifact riêng trong ma trận sáu crash boundary.

## V0-04A — Hai crash boundary quanh transaction intent/job

- **Mục tiêu:** đóng hai boundary còn thiếu trước và ngay sau transaction tạo intent/job.
- **Phụ thuộc:** V0-04.
- **Phạm vi:** fault hooks tại application transaction, subprocess fixture và assertion job/outbox.
- **Thực hiện:** (1) kill trước commit: state/job/event cùng không tồn tại; (2) kill sau commit job nhưng
  trước claim: state/job/event cùng tồn tại và replacement claim đúng một lần.
- **Verify:** subprocess crash tests trên SQLite thật, đếm receipt/event/job/claim và chạy lặp.
- **Hoàn thành khi:** cả sáu boundary trong Go core spec có named hook, runnable scenario và evidence.

## V0-05 — Invalid provider session trong crash flow SPK-12

- **Mục tiêu:** xóa/invalidate ProviderSessionRef sau crash mà run vẫn tiếp tục bằng fresh Start.
- **Phụ thuộc:** V0-02.
- **Phạm vi:** fake Claude/Codex fixtures và recovery acceptance.
- **Thực hiện:** cả hai fake adapter trả invalid-session nếu Resume bị gọi; assert orchestrator không
  gọi Resume, pinned RevisionSet/context hash không đổi.
- **Verify:** cùng contract scenario chạy Claude và Codex fake.
- **Hoàn thành khi:** SPK-12 có run-level evidence thay vì test persistence rời.

## V0-06 — End-to-end scope enforcement SPK-07

- **Mục tiêu:** nối provider process, workspace mounts, diff scanner và fenced finalizer.
- **Phụ thuộc:** V0-02.
- **Phạm vi:** temp Git repos, fake provider/helper, scopeguard/finalizer integration.
- **Thực hiện:** WRITE một repo/path, READ repo còn lại; helper cố sửa ngoài scope; record pre/post
  RevisionSet và diff violation.
- **Verify:** process test trên fixture multi-repo; assert runtime/evidence authority không nhận
  current revision vi phạm.
- **Hoàn thành khi:** SPK-07 pass ở infrastructure boundary thật; OS mount limitation được ghi rõ.

## V0-07 — Quarantine, recreate và stale generation SPK-09

- **Mục tiêu:** hoàn tất recovery của mutating writer mất lease.
- **Phụ thuộc:** V0-06.
- **Phạm vi:** workspace lifecycle, write lease validation, reconciliation fixture.
- **Thực hiện:** làm lease expire khi process có thể ghi; quarantine; chặn writer mới; reconcile hoặc
  recreate tăng generation; thử finalize bằng token/generation cũ.
- **Verify:** integration test assert stale token bị fence, cleanup/release bị chặn khi quarantine.
- **Hoàn thành khi:** chỉ generation hiện hành có thể nhận writer và evidence authority.

## V0-08 — Run-level evidence assembler

- **Mục tiêu:** mỗi SPK bundle truy được project → family → run → node → attempt → repo/revision.
- **Phụ thuộc:** V0-01, V0-02, V0-06, V0-07.
- **Phạm vi:** evidence integration; không thay retention 7 ngày.
- **Thực hiện:** emit workflow/runtime/workspace/provider/process/assertion artifacts theo spike plan;
  validate required correlation fields theo artifact kind.
- **Verify:** positive/negative manifest contract tests.
- **Hoàn thành khi:** thiếu một identity/revision bắt buộc làm suite verdict fail.

## V0-09 — Tamper/redaction ở cấp run SPK-14

- **Mục tiêu:** nâng integrity test từ bundle adapter lên full run bundle.
- **Phụ thuộc:** V0-08.
- **Phạm vi:** acceptance evidence verify và secret fixtures.
- **Thực hiện:** verify sealed run; mutate declared artifact và thêm unmanifested file; inject token/env
  secret; kiểm output retained đã redact exact value.
- **Verify:** SPK-14 positive bundle pass, hai tamper case fail đúng error code, secret search bằng 0.
- **Hoàn thành khi:** SPK-14 có machine verdict và negative evidence.

## V0-10 — Platform semantic normalizer SPK-13

- **Mục tiêu:** so kết quả Windows/Linux mà bỏ đúng metadata platform-specific.
- **Phụ thuộc:** V0-08.
- **Phạm vi:** semantic result exporter/diff; không normalize domain difference.
- **Thực hiện:** giữ ID/hash/state/event/final content; chỉ bỏ locator/path separator/PID/platform
  timing được allowlist; unknown difference làm fail.
- **Verify:** golden fixtures Windows/Linux equivalent và intentional domain mismatch.
- **Hoàn thành khi:** semantic diff deterministic, không dùng broad JSON field deletion.

## V0-10A — Nối scenario thật cho registry và tách CI assessment khỏi GO verdict

> Task được product owner thêm giữa V0-10 và V0-11 sau khi phát hiện `DefaultScenarios()` vẫn trả
> `Passed: false` cho cả 14 SPK trong khi `runFullSuite` exit 0 nếu registry chạy thành công — nghĩa là
> CI matrix job có thể "xanh" dù SPK gate thực tế là 0/14. V0-11 tự nó không được coi "hai matrix jobs
> xanh" là bằng chứng SPK pass.

- **Mục tiêu:** registry phản ánh đúng trạng thái SPK thật (không còn `notYetProven` cho SPK đã có
  evidence thật), và CLI phân biệt rõ "harness/evidence hoàn chỉnh" với "toàn bộ SPK pass".
- **Phụ thuộc:** V0-10.
- **Phạm vi:** `internal/spikeacceptance` (registry/scenario wiring, CLI mode), `cmd/fake-claude`,
  `cmd/fake-codex`, `cmd/spike-helper` (binary mới, wrapper mỏng dùng chung fixture logic đã có, không
  sao chép hành vi), extract shared provider-fixture logic nếu cần để tránh trùng lặp. Không sửa domain
  invariant, không thêm gate mới ngoài những gì liệt kê.
- **Thực hiện:**
  1. Wire 11 SPK có evidence thật thành `ScenarioFunc` chạy đúng Arrange/Act/Assert và ghi evidence qua
     `EvidenceWriter`/`ScenarioContext` — không được chỉ gọi `go test` rồi chuyển exit code thành
     `Passed: true`: SPK-01, 02, 05, 06, 07, 08, 09, 10, 11, 12, 14.
  2. SPK-03 và SPK-04 giữ `Passed: false` với lý do cụ thể, chính xác theo code hiện tại — không sửa
     hành vi của hai SPK này trong task này. SPK-04 giao rõ cho **V0-10B** (cần `cmd/spike-worker` hợp
     nhất bốn crash-worker flow hiện có, chưa tồn tại); SPK-03 giao rõ cho **V0-10C** (interrupted
     `execution_attempts` bị bỏ ở `RUNNING` thay vì chuyển `LOST/INDETERMINATE`).
  3. SPK-13 không tự kết luận trong lần chạy một-platform: ghi `Passed: false` kèm assertion nêu rõ
     "PENDING_PEER_PLATFORM". Kết quả authoritative của SPK-13 chỉ do job so sánh cross-platform (V0-11)
     sinh ra sau khi có cả hai manifest Windows/Linux.
  4. CLI `agentkit-spike acceptance --full` tách hai mode: `--assessment` (exit 0 nếu harness/evidence
     hoàn chỉnh dù có SPK `false`, in rõ N/14, không claim GO) và `--require-all-pass` (exit non-zero
     nếu bất kỳ SPK nào fail). Lỗi harness/handler thiếu/manifest sai/evidence hỏng luôn exit non-zero ở
     cả hai mode.
  5. Thêm CLI `agentkit-spike semantic-diff --left <manifest> --right <manifest>`: chạy `SemanticDiff`
     cho các SPK khác SPK-13, sinh SPK-13 result authoritative kèm cross-platform evidence.
  6. Chạy full suite local (Windows) thành công — nghĩa là harness/evidence hoàn chỉnh theo mode
     `--assessment` — trước khi đưa YAML CI lên (V0-11).
- **Verify:** `go build/vet` sạch; test cho từng scenario mới; `acceptance --full --assessment` chạy
  local thật, in đúng 11/14 và ghi 14 bundle sealed/verified; `acceptance --full --require-all-pass` exit
  non-zero đúng như kỳ vọng (SPK-03/04/13 chưa qua); `semantic-diff` demo trên hai manifest từ hai lần
  chạy riêng biệt.
- **Hoàn thành khi:** registry không còn "0/14 thật" bị CI report nhầm thành xanh; SPK-03/04/13 có lý do
  `false` chính xác, không phải placeholder cũ; V0-11 có thể dùng `--assessment` làm gate CI mà không tự
  nhận nhầm là GO verdict.

## V0-10B — `cmd/spike-worker` và SPK-04 thật

- **Mục tiêu:** đóng sáu crash boundary của SPK-04 thành một scenario thật trong registry, không chỉ
  bốn integration test rời từng re-invoke test binary riêng.
- **Phụ thuộc:** V0-10A.
- **Phạm vi:** extract fixture logic dùng chung từ bốn file
  `internal/adapters/sqlite/crash_resume_*_integration_test.go`; `cmd/spike-worker` (binary mới); wiring
  SPK-04 trong `internal/spikeacceptance`. Không sửa domain/app crash-recovery invariant đã có, không mở
  rộng ra ngoài sáu boundary đã định nghĩa trong spike plan §9/§10.
- **Thực hiện:**
  1. Extract toàn bộ crash-worker flow từ năm file `crash_resume_*_integration_test.go` (base
     claim/finalize, checkpoint join, node dispatch, intent dispatch x2, attempt termination x2 —
     đủ tám mode, khớp sáu boundary chuẩn cộng hai mode dùng chung với SPK-02/SPK-03) thành fixture
     logic dùng chung (`internal/adapters/sqlite/crashworker.go` + `crashworker_fixtures.go`) — cùng
     nguyên tắc "không sao chép hành vi" đã áp dụng cho `internal/adapters/providers/fixtures.go`.
  2. Thêm `cmd/spike-worker`: wrapper mỏng gọi lại fixture logic trên, chạy được như tiến trình độc lập
     (không cần `go test`/`-test.run`), hỗ trợ chọn fault point qua flag/env var (khớp
     `agentkit-spike worker start --id <id> --fault <fault-point>` trong spike plan §7).
  3. Wire SPK-04 thành `ScenarioFunc` thật: spawn `spike-worker` cho từng fault point trong sáu boundary
     chuẩn, kill đúng ranh giới, khởi động worker thay thế, assert theo đúng "Assert chung" của SPK-04
     trong spike plan §9.
  4. Ghi evidence đủ: fault point, state/event/outbox sequence, attempt/process correlation, invariant
     check cho từng boundary — theo đúng mục "Evidence" của SPK-04 trong spike plan.
- **Verify:** `go build/vet` sạch; scenario SPK-04 chạy qua `agentkit-spike acceptance --full
  --assessment` (binary thật, không qua `go test`) và báo `Passed: true`; bốn integration test cũ vẫn
  pass sau khi refactor dùng chung fixture logic.
- **Hoàn thành khi:** SPK-04 chuyển từ `notYetProven` sang `Passed: true` thật trong registry; local
  assessment không còn liệt SPK-04 là lý do fail.

## V0-10C — Đóng SPK-03: interrupted attempt LOST/INDETERMINATE thật

- **Mục tiêu:** interrupted `ExecutionAttempt` sau crash chuyển đúng `LOST` hoặc `INDETERMINATE` thay vì
  bị bỏ ở `RUNNING`, rồi wire SPK-03 thành scenario thật.
- **Phụ thuộc:** V0-10B (có thể dùng lại `cmd/spike-worker`).
- **Phạm vi:** nối `worker.ClassifyInterruptedAttempt`/`TerminateInterruptedAttempt` (đã có từ trước)
  vào crash-recovery flow thật; mutating path phải quarantine/reconcile qua các primitive đã có từ V0-07
  (`QuarantineRepositoryWorkspace`/`RecreateRepositoryWorkspace`); wiring SPK-03 trong
  `internal/spikeacceptance`. Không tạo primitive mới nếu primitive cần thiết đã tồn tại.
- **Thực hiện:**
  1. Trong flow crash-recovery thật (worker thay thế sau hard kill), gọi
     `ClassifyInterruptedAttempt` rồi `TerminateInterruptedAttempt` để chuyển attempt bị gián đoạn khỏi
     `RUNNING` sang đúng `LOST` (read-only) hoặc `INDETERMINATE` (mutating).
  2. Với attempt `INDETERMINATE`, nối `ReconcileMutatingAttempt` rồi quarantine/reconcile workspace
     đúng như SPK-09 đã chứng minh (V0-07), không suy exit code thành success.
  3. Wire SPK-03 thành `ScenarioFunc` thật, có thể dùng lại `cmd/spike-worker` (V0-10B) làm worker bị
     crash.
- **Verify:** `go build/vet` sạch; scenario SPK-03 báo `Passed: true` qua `acceptance --full
  --assessment` thật; `crash_resume_checkpoint_integration_test.go` cập nhật lại, không còn ghi "Known
  limitation" cho phần LOST/INDETERMINATE.
- **Hoàn thành khi:** local assessment đạt 13/14, chỉ còn SPK-13 `PENDING_PEER_PLATFORM` — mọi SPK khác
  đã `Passed: true` thật, không còn `notYetProven` nào ngoài SPK-13.

> **Kết quả:** triển khai gộp bốn primitive trên vào một hàm điều phối duy nhất,
> `worker.ReconcileInterruptedAttempt` (`internal/app/worker/interruption.go`) — không phải primitive
> mới, chỉ nối `ClassifyInterruptedAttempt` → `TerminateInterruptedAttempt` → (nếu `INDETERMINATE`)
> `ReconcileMutatingAttempt` → (nếu không sạch) `QuarantineRepositoryWorkspace` thành một flow tái dùng
> được cho worker thay thế thật. `crash_resume_checkpoint_integration_test.go` và scenario SPK-03 mới
> (`internal/spikeacceptance/spk03_scenario.go`, tái dùng `cmd/spike-worker` chế độ
> `checkpoint-then-hang`) đều gọi hàm này và xác nhận attempt bị gián đoạn chuyển `RUNNING@1` →
> `LOST@2`. Xác minh qua binary `agentkit-spike acceptance --full --assessment` thật: 13/14 SPK pass,
> chỉ còn SPK-13 `PENDING_PEER_PLATFORM`.

## V0-11A — Windows SQLite contention stabilization (blocker của V0-11)

- **Mục tiêu:** đóng lỗi `SQLITE_BUSY` ("database is locked") xuất hiện thật trên runner
  `windows-latest` của GitHub Actions (chưa từng tái hiện trên máy dev cục bộ), phát hiện khi chạy PR
  kiểm chứng V0-11 draft. Lỗi chặn đúng job `contract (windows-latest)`, mà `spike-acceptance` và
  `semantic-diff` đều phụ thuộc — nên chặn cả tiêu chí "hai matrix jobs xanh" của V0-11.
- **Phụ thuộc:** không phụ thuộc V0-10 nào; đây là lỗi trong code SQLite adapter có từ trước (V0-02…V0-09),
  chỉ mới lộ ra khi chạy trên CI thật.
- **Phạm vi:** `internal/adapters/sqlite/db.go` (connection DSN); không đổi schema, không đổi domain logic.
- **Chẩn đoán (bằng chứng cụ thể, không phải đoán):**
  1. `modernc.org/sqlite@v1.57.0` bật `extended_result_codes` mặc định cho mọi connection
     (`conn.go:113`). Message log CI `database is locked (5) (SQLITE_BUSY)` chỉ có suffix
     `"(SQLITE_BUSY)"` khi mã lỗi đúng bằng 5 (`conn.go:872`, so sánh chính xác) — xác nhận đây là
     `SQLITE_BUSY` thường (5), **không phải** `SQLITE_BUSY_SNAPSHOT` (517) hay `SQLITE_LOCKED` (6).
  2. `busy_timeout(5000)` được áp lại cho **mọi** connection mới trong pool qua `applyQueryParams`
     (chạy trong `newConn`, không chỉ lần mở đầu tiên) — không phải lỗi cấu hình pool bị áp thiếu.
  3. Test lỗi (`TestWriteLeaseRaceHasOneWinnerForSameRepository`, 100 iteration) chỉ chạy 1.09s trên CI
     — nếu busy_timeout thật sự chờ 5s mỗi lần thì con số này vô lý. Khớp đúng hành vi SQLite đã biết:
     khi hai transaction **cùng đã giữ SHARED (read) lock** rồi cùng giành nâng cấp lên WRITE lock,
     SQLite bỏ qua busy-handler cho một bên để tránh livelock — `busy_timeout` không áp dụng cho case
     này.
  4. `acquireWriteLeasesOnce` (`scheduling.go`) đúng pattern gây lỗi: `SELECT` (lập read lock) trước
     `INSERT...RETURNING` (cần write lock) trong cùng một `BeginTx` DEFERRED mặc định.
  5. Kiểm tra cả 13 điểm gọi `BeginTx` trong package: tất cả dùng `&sql.TxOptions{}` trần, tất cả là
     transaction ghi thật (Terminate/Dispatch/Acquire/Publish/Quarantine/...) — không điểm nào là
     read-only transaction bị ảnh hưởng oan nếu đổi transaction mode.
- **Sửa:** thêm `_txlock=immediate` vào DSN (`db.go`) — mọi transaction trên connection giành
  write-intent (RESERVED) lock ngay từ `BEGIN`, loại bỏ hoàn toàn tình huống "hai bên cùng đọc trước
  rồi giành nâng cấp"; contention giờ đi qua đường mà `busy_timeout` xử lý đúng.
- **Verify:** cục bộ `TestWriteLeaseRaceHasOneWinnerForSameRepository -count=20` (2000 lần race) và
  `TestDefaultScenariosFormAValidRegistryAndCleanRun -count=5` đều pass, không regress. Thêm bước
  stress thật trên `contract (windows-latest)` (không chỉ 1 lần rồi rerun-until-green): 20 lần race
  test + 5 lần full registry test, chạy trên chính runner đã tái hiện lỗi.
- **Hoàn thành khi:** stress step trên `windows-latest` xanh nhiều lần liên tiếp trên CI thật (không
  phải 1 lần ăn may), thời gian chờ mỗi transaction vẫn bounded (không xuất hiện block nhiều giây bất
  thường), và không có lỗi rò ra ngoài adapter dưới dạng khác `ErrWriteLeaseConflict`/`ErrJobLeaseLost`
  đã định nghĩa.

> **Phần SQLITE_BUSY: đạt.** CI run thật
> [33506869479](https://github.com/taQuangLing/agent-workflow/actions/runs/33506869479):
> `contract (windows-latest)` xanh trong 3m59s bao gồm bước stress. 20 lần
> `TestWriteLeaseRaceHasOneWinnerForSameRepository` (2000 iteration race) = 34.0s tổng, không lần nào
> SQLITE_BUSY. Cả hai đều xa dưới timeout đã đặt — thời gian chờ bounded thật, không phải "may mắn né
> được" trong 1 lần chạy. `_txlock=immediate` chưa bị bác bỏ bởi bất kỳ lỗi nào sau đó.
>
> **Phát hiện thêm (run tiếp theo, commit tài liệu thuần túy 31528a6 → CI run
> [33507762210](https://github.com/taQuangLing/agent-workflow/actions/runs/33507762210)): một race
> thời gian khác, không phải SQLITE_BUSY.** `TestSPK09QuarantineRecreateFencesStaleGeneration` fail với
> `ErrWriteLeaseConflict` thật (không phải "database is locked"). Nguyên nhân: job lease và write lease
> của W1 cùng TTL 300ms nhưng được tạo ở hai thời điểm khác nhau (job lease trước, write lease sau vài
> mili giây) — hai thời điểm hết hạn tuyệt đối khác nhau. `waitForRecoveredJob`/vòng lặp tương đương chỉ
> đợi job lease hết hạn (`RecoverExpiredJobs`), không đợi write lease riêng; W2 gọi `AcquireWriteLeases`
> ngay sau đó có thể va phải write lease của W1 còn hiệu lực vài mili giây. Cùng pattern lặp lại ở
> `internal/spikeacceptance/spk09_scenario.go` (viết từ V0-10A, dùng chung logic đợi).
>
> **Sửa (V0-11A, phần 2):** thêm `waitPastLeaseUntil` (test, `scheduling_test.go`) và
> `waitPastWriteLeaseUntil` (scenario, `spk04_scenario.go`, dùng chung trong package `spikeacceptance`)
> — đợi xác định tới đúng `WriteLeaseGrant.LeaseUntil` đã ghi nhận, không suy đoán khoảng cách TTL. Áp
> dụng ở `spk09_workspace_quarantine_test.go` và `spk09_scenario.go`. Thêm
> `TestSPK09QuarantineRecreateFencesStaleGeneration -count=30` vào bước stress Windows trong CI.
>
> Cục bộ: 30 lần chạy `TestSPK09QuarantineRecreateFencesStaleGeneration` sau sửa đều pass (lỗi này chưa
> từng tái hiện cục bộ trước đó nên đây chỉ là kiểm tra không-regress, không phải bằng chứng lỗi đã
> đóng — bằng chứng thật phải đến từ CI thật).
>
> **Đóng — bằng chứng CI thật:** run
> [33511037773](https://github.com/taQuangLing/agent-workflow/actions/runs/33511037773) (commit
> `b23dacb`, đúng HEAD hiện tại của PR #1): 30 lần
> `TestSPK09QuarantineRecreateFencesStaleGeneration` trên chính `contract (windows-latest)` = 18.6s
> tổng, không lần nào fail. Xem chi tiết đầy đủ (cả ba test trong bước stress) dưới mục V0-11.

## V0-11 — Chạy full offline suite trên Windows và Ubuntu CI

- **Mục tiêu:** SPK-01…14 thực thi thật trên cả hai OS.
- **Phụ thuộc:** V0-01A, V0-03…V0-10C, V0-04A và **V0-11A** (contention Windows phải ổn định trước khi
  coi hai matrix job có thể xanh thật). Local assessment (V0-10C) phải đạt 13/14 trước khi V0-11 được
  coi là sẵn sàng chạy chính thức/đóng gate — chỉ còn SPK-13 chờ job cross-platform.
- **Phạm vi:** CI workflow, toolchain setup và evidence upload; không cần network provider.
- **Thực hiện:** build helper/provider binaries; chạy `acceptance --full --assessment` trên cả hai OS;
  verify bundle; upload platform manifests với `if: always()`; job thứ ba (Ubuntu, phụ thuộc cả hai
  matrix job) tải hai manifest rồi chạy `semantic-diff` để sinh SPK-13 result authoritative.
- **Verify:** hai matrix jobs xanh từ clean checkout. **Đạt tại HEAD hiện tại** — xem cập nhật cuối mục
  này (đã đối chiếu `git rev-parse ci/v0-11-draft` khớp đúng `gh pr view --json headRefOid` trước khi
  ghi nhận, không dựa vào run cũ đã bị commit sau vượt qua).
- **Hoàn thành khi:** SPK-13 pass và report liên kết được CI run/evidence ID, tại HEAD hiện tại của PR.
  **Đạt** — xem cập nhật cuối mục này.

> **Trạng thái hiện tại: draft đã push, chờ CI thật xác nhận, chưa đóng.** `.github/workflows/spike-gate.yml`
> đã cập nhật để khớp phạm vi trên: job `spike-acceptance` (matrix windows-latest/ubuntu-latest,
> `needs: contract`) build năm binary (`agentkit-spike`, `fake-claude`, `fake-codex`, `spike-helper`,
> `spike-worker`) rồi chạy `acceptance --full --assessment`, upload evidence với `if: always()`; job
> `semantic-diff` (ubuntu-latest, `needs: spike-acceptance`) tải hai manifest rồi chạy `semantic-diff`
> để sinh SPK-13 authoritative result.
>
> Sau một vòng review độc lập, đã sửa thêm trước khi push:
> - `push:` trigger trỏ nhầm nhánh `main` (repo chỉ có `master`) — đã sửa.
> - `runSemanticDiff` (`cmd/agentkit-spike/main.go`) giờ re-validate cả hai manifest qua
>   `NewSPKManifest` (không chỉ `json.Unmarshal`), assert `left.GOOS != right.GOOS` (chặn đúng lỗi so
>   sánh Windows-vs-Windows mà tôi mắc phải khi dry-run cục bộ — đã tái hiện và xác nhận bằng test thật),
>   re-verify từng evidence bundle hai bên qua `VerifySuite` trước khi diff (đã tái hiện: bundle bị
>   tamper sau download bị chặn đúng, không âm thầm trôi qua), và seal kết quả SPK-13 thành evidence
>   bundle thật (`--evidence-dir`, cùng cơ chế Finalize+Verify như mọi SPK khác) thay vì chỉ ghi JSON rời.
> - `actions/checkout`, `actions/setup-go`, `actions/upload-artifact`, `actions/download-artifact` đã
>   pin bằng full commit SHA (kèm comment version) thay vì tag di động `@v4`/`@v6`.
> - Artifact retention đổi 14→7 ngày, khớp TTL 7 ngày mặc định cho evidence/raw (`docs/00-start-here.md`,
>   ADR-017); thêm `if-no-files-found: error` và `timeout-minutes` cho mọi job.
>
> Đã kiểm cục bộ: YAML hợp lệ (PyYAML), `actionlint` 0 lỗi, chạy tay toàn bộ pipeline (build 5 binary →
> hai lượt `acceptance --full --assessment` → `semantic-diff --evidence-dir`) đúng như CI sẽ chạy, cả
> case dương tính lẫn hai case âm tính (so sánh cùng platform bị chặn; bundle bị tamper bị chặn).
> Nhánh `ci/v0-11-draft` đã push, PR kiểm chứng đã mở
> ([taQuangLing/agent-workflow#1](https://github.com/taQuangLing/agent-workflow/pull/1)) nhắm vào
> `docs/alpha-design-adr-020-025`, không nhắm `master`. **Chưa** coi V0-11 là đóng cho tới khi CI thật
> trên GitHub Actions xác nhận xanh và có CI run ID/evidence ID thật ghi lại.
>
> **Cập nhật sau lần chạy CI thật đầu tiên:** `contract (windows-latest)` fail thật với `SQLITE_BUSY`
> trong `TestWriteLeaseRaceHasOneWinnerForSameRepository` và SPK-08 — lỗi chưa từng tái hiện cục bộ, chỉ
> lộ ra trên runner Windows thật của GitHub. Đã tách thành task riêng **V0-11A** (xem mục ngay trên),
> chẩn đoán root cause cụ thể và sửa (`_txlock=immediate`), thêm bước stress thật (20x race test + 5x
> registry test) trên chính `contract (windows-latest)`.
>
> **Một run trung gian đã xanh cả 6 job (bằng chứng cơ chế hoạt động đúng, không phải trạng thái cuối
> của PR):** CI run
> [33506869479](https://github.com/taQuangLing/agent-workflow/actions/runs/33506869479) (commit
> `b105522`) — `contract` (Windows 3m59s bao gồm bước stress, Ubuntu 37s), `spike acceptance` (Windows
> 1m22s, Ubuntu 36s), `Linux race and stability` (3m22s), `cross-platform semantic diff` (21s) đều xanh.
> `semantic-diff` chạy trên hai manifest thật (Windows runner thật vs Ubuntu runner thật):
> `SPK-13 authoritative passed=true (0 unexplained difference(s) across 13 SPK(s), left=
> full-20260901t122228.133407700z/windows right=full-20260901t122157.335508398z/linux)`. Bundle SPK-13
> đã seal+verify, upload thành artifact `spk13-authoritative-result` (artifact ID 9800147596).
>
> **Nhưng commit tài liệu thuần túy ngay sau đó (`31528a6`) tạo CI run
> [33507762210](https://github.com/taQuangLing/agent-workflow/actions/runs/33507762210), và run này
> FAIL** tại `contract (windows-latest)` — một race thời gian khác (SPK-09 write-lease timing, xem cập
> nhật trong V0-11A ở trên), không phải SQLITE_BUSY. `spike acceptance`, `Linux race and stability`,
> `semantic-diff` đều bị skip do phụ thuộc `contract`. **PR #1 tại HEAD hiện tại đang đỏ, không xanh.**
> Đã sửa (`waitPastLeaseUntil`/`waitPastWriteLeaseUntil`), đã push, đang chờ CI thật xác nhận — không
> tự nhận "hai matrix jobs xanh" cho tới khi có run mới xanh tại HEAD hiện tại.
>
> **Cập nhật sau khi sửa V0-11A phần 2 (`waitPastLeaseUntil`/`waitPastWriteLeaseUntil`), push commit
> `b23dacb`: CI run
> [33511037773](https://github.com/taQuangLing/agent-workflow/actions/runs/33511037773) — cả 6 job
> xanh: `contract` (Windows 4m9s bao gồm bước stress đủ ba test, Ubuntu 39s), `spike acceptance`
> (Windows 1m28s, Ubuntu 38s), `Linux race and stability` (3m26s), `cross-platform semantic diff`
> (37s). Bước stress Windows: 20 lần race test = 22.7s, **30 lần
> `TestSPK09QuarantineRecreateFencesStaleGeneration` = 18.6s (không lần nào fail — bằng chứng thật cho
> lỗi V0-11A phần 2 đã đóng)**, 5 lần full registry test = 101.4s — cả ba đều xa dưới timeout.
> `semantic-diff`: `SPK-13 authoritative passed=true (0 unexplained difference(s) across 13 SPK(s),
> left=full-20260901t130749.783821200z/windows right=full-20260901t130709.729703778z/linux)`, bundle
> seal+verify, artifact `spk13-authoritative-result` (ID 9801860568).
>
> **Đã đối chiếu đây thật sự là HEAD hiện tại, không phải run cũ:** `git rev-parse ci/v0-11-draft` =
> `b23dacb80c41d84c9b0c60ffcfba3ebd30aaef04`, khớp đúng `gh pr view 1 --json headRefOid`. `gh pr view 1`
> báo `state=OPEN mergeable=MERGEABLE`, mọi status check `conclusion=SUCCESS`, không có check nào đỏ.
>
> **Vẫn giữ đúng ranh giới đã thống nhất:** "hai matrix jobs
> xanh" theo nghĩa harness/evidence hoàn chỉnh + SPK-13 cross-platform pass thật, **không** phải tuyên bố
> `GO` (chỉ V0-14 kết luận GO) và không đồng nghĩa toàn bộ nhánh `docs/alpha-design-adr-020-025` đã sẵn
> sàng merge vào `master` — PR #1 chỉ nhắm vào nhánh feature đó để kiểm chứng CI, chưa phải quyết định
> merge.

> Làm rõ theo quyết định product owner (cùng lúc thêm V0-10A): "hai matrix jobs xanh" nghĩa là clean
> checkout/build/test pass, full-suite dispatcher chạy đủ 14 handler, manifest hợp lệ, evidence bundle
> verify được và artifact upload thành công — **không** đồng nghĩa 14/14 SPK pass, và không phải verdict
> `GO`. `GO` chỉ do V0-14 kết luận. V0-11 dùng mode `--assessment` (xem V0-10A) làm gate CI, không dùng
> `--require-all-pass`. SPK-13 pass ở đây nghĩa là job so sánh cross-platform (mục 3, Thực hiện) sinh ra
> được authoritative result — không phải điều kiện tiên quyết trước khi V0-11 được phép chạy.

## V0-12 — Race và stability gate

- **Mục tiêu:** đạt race detector và 10 full suites không flaky.
- **Phụ thuộc:** V0-11.
- **Phạm vi:** CI Linux hỗ trợ CGO/race, test timeout và deterministic fixtures.
- **Thực hiện:** `go test -race -count=1 ./...`; full offline suite 10 lần; SPK-08 ít nhất 100 race
  iterations mỗi suite; không retry-until-green.
- **Verify:** CI log và aggregated stability report.
- **Hoàn thành khi:** 10/10 pass; mọi failure được sửa root cause và chạy lại chuỗi từ đầu.

> **Đạt.** `linux-race-and-stability` job trong `.github/workflows/spike-gate.yml` viết lại để sinh
> `stability-report/v0-12-stability-report.json` (race detector pass/fail+duration, SPK-08 iteration
> count kiểm tĩnh qua `grep -P` phải >= 100, cả 10 lần chạy full suite với pass/fail+duration riêng
> từng lần), upload làm artifact `v0-12-stability-report`. Job fail (exit 1) nếu bất kỳ phần nào fail —
> không che giấu, không retry-until-green ở cấp CI.
>
> **Bằng chứng CI thật, đã tải artifact và đọc trực tiếp** (không chỉ đọc log): run
> [33516182656](https://github.com/taQuangLing/agent-workflow/actions/runs/33516182656) (commit
> `a2779d3`, đối chiếu `git rev-parse ci/v0-11-draft` = `gh pr view 1 --json headRefOid` khớp đúng
> trước khi ghi nhận). `v0-12-stability-report.json` thật:
> `raceDetector: {passed: true, durationSeconds: 84}`,
> `spk08WriteLeaseRace: {iterationCount: 100, meetsMinimum100: true}`,
> `fullSuiteRuns`: cả 10 lần `passed: true` (9–11s mỗi lần), `allTenRunsStable: true`. Cả 6 job trong
> run đều xanh; `gh pr view 1`: `state=OPEN mergeable=MERGEABLE`, mọi status check `SUCCESS`.
>
> **Phát hiện gap khi soát lại cho V0-14 (không phải "Đạt" đầy đủ như ghi trên):** `10x full offline
> suite` dựa vào `go test -count=1 ./...` pass/fail — nhưng
> `TestDefaultScenariosFormAValidRegistryAndCleanRun` (bài test mà "full suite" tin cậy để chứng minh
> cả 14 SPK) trước đây **không kiểm `result.Passed`**, chỉ kiểm có assertion, có artifact, bundle
> verify được. Một SPK thật sự fail (`Passed:false`) nhưng vẫn có assertion/artifact/bundle hợp lệ sẽ
> lọt qua — nghĩa là `allTenRunsStable:true` mới chỉ chứng minh "harness/evidence hoàn chỉnh 10 lần",
> **chưa** chứng minh "SPK-01…14 đều genuinely pass 10 lần" như mục tiêu gate yêu cầu. Đã tái hiện bằng
> vi phạm thật: ép `SPK-01` trả `Passed:false` (vẫn giữ nguyên assertion/artifact) — test cũ sẽ pass
> nhầm; sau khi sửa test, xác nhận catch đúng lỗi rồi revert violation.
>
> **Sửa:** `TestDefaultScenariosFormAValidRegistryAndCleanRun`
> (`internal/spikeacceptance/registry_test.go`) giờ bắt buộc SPK-01…12 và SPK-14 phải `Passed:true`;
> SPK-13 phải đúng `Passed:false` kèm lý do `PENDING_PEER_PLATFORM` — không chấp nhận `Passed:true` hay
> `Passed:false` vì lý do khác cho SPK-13. Thêm bước CI riêng mỗi vòng lặp: chạy `-v` chỉ test này,
> ghi log `spk-summary-run-N.log` (13 PASS + 1 PENDING per run) làm bằng chứng tường minh, không suy từ
> boolean pass/fail của cả suite.
>
> **Trạng thái sau sửa: draft đã push, chờ chạy lại nguyên chuỗi 10 lần từ đầu trên CI thật, chưa đóng
> lại.** Run 33516182656 ở trên không còn đủ làm bằng chứng cho tiêu chí gốc — cần run mới với test đã
> sửa. Không tính là REWORK (không có invariant nào sai — SPK-01…14 registry code vẫn đúng, luôn đúng);
> đây là **thiếu evidence trong chính bài kiểm** (semantics gap), đã sửa hẹp đúng chỗ.
>
> **Đóng lại — chuỗi 10 lần chạy lại từ đầu, bằng chứng thật đúng semantics.** Run
> [33524345962](https://github.com/taQuangLing/agent-workflow/actions/runs/33524345962) (commit
> `f954582`, đối chiếu `git rev-parse` = `headRefOid` khớp đúng). `Linux race and stability` mất 6m22s
> (tăng từ 3m22s do thêm 10 lần chạy `-v` riêng bài registry test). Đã tải artifact và đọc trực tiếp cả
> `v0-12-stability-report.json` lẫn 10 file `spk-summary-run-N.log`: **cả 10/10 lần đều ghi rõ ràng
> "13 SPKs genuinely Passed:true, 1 correctly PENDING_PEER_PLATFORM (SPK-13, single-platform) — 14
> total"** — không còn suy từ boolean `allTenRunsStable` nữa, mà là bằng chứng tường minh từng lần.
> `raceDetector: {passed: true, durationSeconds: 104}`, `spk08WriteLeaseRace: {iterationCount: 100,
> meetsMinimum100: true}`. Cả 6 job xanh; PR `state=OPEN mergeable=MERGEABLE`, mọi status check
> `SUCCESS`.

## V0-13 — Boundary/dependency report

- **Mục tiêu:** chứng minh domain/app không phụ thuộc adapter/OS/provider.
- **Phụ thuộc:** V0-12.
- **Phạm vi:** import check và report generated.
- **Thực hiện:** kiểm import graph, shell-string absence ở process port, provider branch absence trong
  domain/orchestrator, mutable workflow version operation absence.
- **Verify:** automated architecture test và report artifact. **Đạt** — xem cập nhật cuối mục này.
- **Hoàn thành khi:** violation làm acceptance fail, không chỉ warning. **Đạt** — đã kiểm chứng bằng bốn
  vi phạm thật lần lượt, mỗi lần test FAIL đúng, không chỉ warning (xem cập nhật cuối mục này).

> **Đạt.** Package `internal/archtest` mới, bốn
> test thật: `TestDomainAppNeverImportAdapters` (`go list -json` trên import graph thật, không phải
> text search — bắt được cả dependency transitive), `TestProcessSpecHasNoShellStringField` (reflection
> trên `ports.ProcessSpec`, cấm field `Command`/`Shell`/... , bắt buộc `Argv []string`),
> `TestNoProviderBranchingOutsidePorts` (AST walk thật qua `go/parser`+`go/ast`, chỉ bắt if/switch
> **branch** trên `ProviderClaude`/`ProviderCodex` ngoài `internal/app/ports` và ngoài `_test.go` — phân
> biệt được với việc dùng hằng số đó làm giá trị/tham số, ví dụ registry lookup ở
> `internal/app/agentregistry` hay test assertion, vốn không phải vi phạm), `TestWorkflowVersionHasNoMutatingMethods`
> (reflection so method set value-type vs pointer-type, bắt method pointer-receiver-only — dấu hiệu duy
> nhất của mutator tại chỗ). Đã kiểm tra thư viện hiện tại: cả bốn PASS, không có vi phạm sẵn có (codebase
> vốn đã sạch, task này chỉ khóa lại bằng test thật).
>
> **Đã kiểm chứng cả bốn test thật sự bắt được vi phạm** (không chỉ pass vì viết sai): tạo lần lượt bốn
> vi phạm thật trên file tạm (domain import adapter thật — kể cả case gây import cycle lẫn case không;
> thêm field `Command string` vào `ProcessSpec`; thêm if-branch thật trên `ProviderClaude` trong
> `internal/app/worker`; thêm method con trỏ thật trên `WorkflowVersion`), mỗi lần xác nhận test FAIL
> đúng dòng/đúng lý do, rồi xoá file tạm, xác nhận `git status` sạch lại và cả bốn test PASS trở lại.
>
> Thêm bước CI `Boundary/dependency report (V0-13)` trong `contract (ubuntu-latest)` (checks không phụ
> thuộc OS nên chỉ chạy một lần): chạy `go test -v ./internal/archtest/...`, upload log làm artifact
> `v0-13-boundary-report`. Enforcement thật sự đã có sẵn từ trước: vì đây là gói `go test` thật, một vi
> phạm đã tự làm fail bước "Offline contract suite" trên cả hai OS — bước report chỉ thêm truy vết độc
> lập, không phải cơ chế chặn duy nhất. Đã dry-run cục bộ script CI y hệt, pass sạch.
>
> **Bằng chứng CI thật, đã tải artifact và đọc trực tiếp:** run
> [33519463314](https://github.com/taQuangLing/agent-workflow/actions/runs/33519463314) (commit
> `0ba3b2b`, đối chiếu `git rev-parse ci/v0-11-draft` = `gh pr view 1 --json headRefOid` khớp đúng). Cả
> 6 job xanh; `v0-13-boundary-report` artifact tải về và đọc trực tiếp: cả bốn test
> (`TestDomainAppNeverImportAdapters`, `TestProcessSpecHasNoShellStringField`,
> `TestNoProviderBranchingOutsidePorts`, `TestWorkflowVersionHasNoMutatingMethods`) đều `PASS` thật trên
> runner Ubuntu thật. `gh pr view 1`: `state=OPEN mergeable=MERGEABLE`, mọi status check `SUCCESS`.

## V0-14 — Ghi verdict và đóng gate

- **Mục tiêu:** luôn tạo assessment cuối và cập nhật report/start-here bằng kết luận evidence-backed,
  kể cả khi một execution gate fail.
- **Phụ thuộc:** V0-09, V0-11, V0-12 và V0-13 đã tạo assessment artifact/trạng thái; không yêu cầu
  chúng phải PASS để chạy task này.
- **Phạm vi:** `docs/spikes/02-go-core-spike-report.md`, `docs/00-start-here.md`; không sửa ADR để hợp
  thức hóa failure.
- **Thực hiện:** cập nhật từng SPK, environment/evidence IDs, limits/live-smoke status và verdict
  `GO|REWORK|STOP|CHƯA ĐỦ EVIDENCE` đúng gate; không đổi failure thành deferred.
- **Verify:** link/path evidence tồn tại, bundle verify command pass, doc không còn claim mâu thuẫn.
- **Hoàn thành khi:** assessment matrix đầy đủ; chỉ verdict `GO` mới đổi V1 thành allowed, mọi verdict
  khác ghi blocker và next narrow task.

> **Đạt — verdict `GO`.** Đối chiếu Definition of done (`docs/00-start-here.md` mục 7) với toàn bộ
> evidence tích luỹ từ V0-07…V0-13, tất cả trên CI run
> [33525614475](https://github.com/taQuangLing/agent-workflow/actions/runs/33525614475) (commit
> `d7be9fa`, dùng HEAD hiện tại của PR #1, cả 6 job xanh): SPK-01…14 pass thật Windows+Linux (13 trực
> tiếp mỗi platform, SPK-13 qua `semantic-diff` cross-platform authoritative `passed=true`); evidence
> verify được (`agentkit-spike evidence verify` PASS, bundle SPK-13 sealed+verified); 10/10 suite runs
> không flaky **đúng semantics** (mỗi lần có bằng chứng tường minh "13 Passed:true + 1
> PENDING_PEER_PLATFORM", không suy từ exit code — gap này tự phát hiện khi soát lại cho task này, đã
> sửa và chạy lại nguyên chuỗi trước khi ghi verdict, xem V0-12); `-race` PASS trên CI (82s).
>
> Đã viết verdict `GO` vào `docs/spikes/02-go-core-spike-report.md` (rewrite đầy đủ: kết luận, ma trận
> 14 SPK không còn PARTIAL, evidence ID cụ thể, 10 finding gồm cả finding mới từ V0-11A/V0-12) và
> `docs/00-start-here.md` (mục 4 `GO`, mục 5 `ĐƯỢC PHÉP BẮT ĐẦU`, mục 6 lệnh chạy `--full`/`--assessment`
> thật thay vì `--offline`, mục 7 xác nhận đã ghi `GO`). Không sửa ADR nào. `docs/00-start-here.md` và
> spike report đồng bộ, không còn claim mâu thuẫn (đã soát lại toàn bộ hai file).

## Exit evidence V0

- Full SPK result manifests Windows/Linux.
- Registry chứng minh đủ 14 scenario runnable và ma trận đủ sáu crash boundary.
- Race và 10-run stability reports.
- Verified/tamper-negative evidence bundle.
- Dependency boundary report.
- Spike report có verdict và `docs/00-start-here.md` đồng bộ.

## SPK → criterion coverage map (V1-00B)

Task V0 được miễn trường `Nguồn` (`00-roadmap.md` §3) vì đã có traceability riêng bằng SPK ID, nhưng
V1-00B yêu cầu map SPK-01…14 sang criterion tương ứng để coverage checker (V1-00C) vẫn tính được các
criterion mà V0 đã chứng minh. Mapping dưới đây lấy từ Arrange/Assert thật của từng SPK ở
`docs/spikes/01-go-core-spike-plan.md` §9, không suy diễn.

| SPK | Criterion đã chứng minh | Vì sao |
|---|---|---|
| SPK-01 | AK-ARCH-001, GC-ACC-02 | Publish idempotent theo canonical hash; graph sai/terminal unreachable bị reject. |
| SPK-02 | GC-ACC-01 | Run pin version xuyên qua publish mới và restart. |
| SPK-03 | GC-ACC-03, GC-ACC-04, GC-ACC-05, GC-ACC-11, GC-ACC-14 | Node đã commit không chạy lại; read-only LOST, mutating INDETERMINATE/quarantine; recover từ SQLite không cần transcript; không tự suy DONE từ crash/exit. |
| SPK-04 | GC-ACC-05, GC-ACC-16 | Sáu fault point transaction boundary chuẩn đều có fixture; không tạo hai terminal transition. |
| SPK-05 | GC-ACC-07 | Family scope hai repository tạo đúng một WorkspaceSet, hai RepositoryWorkspace; child reuse. |
| SPK-06 | GC-ACC-06 | Hai root family cùng repository nhận hai worktree/branch độc lập. |
| SPK-07 | GC-ACC-10 | Diff vượt scope bị phát hiện, attempt không pass, current revision không cập nhật. |
| SPK-08 | GC-ACC-08, GC-ACC-15 | Lease exclusivity đúng repository qua ≥100 race iteration; một phần của SQLite lease contract suite. |
| SPK-09 | GC-ACC-09, GC-ACC-15, AK-ARCH-009 | Fencing token cũ bị từ chối sau TTL/generation change. |
| SPK-10 | GC-ACC-15, AK-ARCH-008 | Optimistic concurrency/CAS trên Attempt/NodeRun state; đúng một transition thắng. |
| SPK-11 | GC-ACC-12, GC-ACC-13, AK-ARCH-016 | Cùng scenario qua fake Claude/Codex không đổi domain code; unknown capability bị reject trước execution. |
| SPK-12 | GC-ACC-11 | Mất ProviderSessionRef vẫn recover context từ platform state; `Resume` call count bằng 0. |
| SPK-13 | AK-ARCH-020 | Cùng semantic suite pass trên Windows và Linux; authoritative qua semantic-diff (V0-11). |
| SPK-14 | AK-ARCH-021 | Evidence bundle tự verify hash/correlation; bundle bị sửa fail verify — chứng minh nhánh evidence của traceability chain. |

`GC-ACC-15` (SQLite repository/lease/transaction contract suite, race test và migration test) không
thuộc riêng một SPK — nó là kết luận tổng hợp từ SPK-08/09/10 cộng
`internal/adapters/sqlite/*_test.go` chạy trong cùng CI job. `GC-ACC-16` được SPK-04 chứng minh trực
tiếp vì đó chính là nội dung sáu fault point của nó.

Các GC-INV/GC-ACC/GC-DS không xuất hiện ở bảng trên (ví dụ GC-INV liên quan CompletionPolicy, cancel
coordinator, WorkItem cancellation — các invariant thuộc V4/V5) không được V0 chứng minh; chúng chờ
owner ở Task ID V4/V5 tương ứng đã gán Nguồn ở trên, không phải nợ của V0.
