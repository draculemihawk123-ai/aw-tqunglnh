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

## V0-11 — Chạy full offline suite trên Windows và Ubuntu CI

- **Mục tiêu:** SPK-01…14 thực thi thật trên cả hai OS.
- **Phụ thuộc:** V0-01A, V0-03…V0-10C và V0-04A. Local assessment (V0-10C) phải đạt 13/14 trước khi
  V0-11 được coi là sẵn sàng chạy chính thức/đóng gate — chỉ còn SPK-13 chờ job cross-platform.
- **Phạm vi:** CI workflow, toolchain setup và evidence upload; không cần network provider.
- **Thực hiện:** build helper/provider binaries; chạy `acceptance --full --assessment` trên cả hai OS;
  verify bundle; upload platform manifests với `if: always()`; job thứ ba (Ubuntu, phụ thuộc cả hai
  matrix job) tải hai manifest rồi chạy `semantic-diff` để sinh SPK-13 result authoritative.
- **Verify:** hai matrix jobs xanh từ clean checkout.
- **Hoàn thành khi:** SPK-13 pass và report liên kết được CI run/evidence ID.

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

## V0-13 — Boundary/dependency report

- **Mục tiêu:** chứng minh domain/app không phụ thuộc adapter/OS/provider.
- **Phụ thuộc:** V0-12.
- **Phạm vi:** import check và report generated.
- **Thực hiện:** kiểm import graph, shell-string absence ở process port, provider branch absence trong
  domain/orchestrator, mutable workflow version operation absence.
- **Verify:** automated architecture test và report artifact.
- **Hoàn thành khi:** violation làm acceptance fail, không chỉ warning.

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

## Exit evidence V0

- Full SPK result manifests Windows/Linux.
- Registry chứng minh đủ 14 scenario runnable và ma trận đủ sáu crash boundary.
- Race và 10-run stability reports.
- Verified/tamper-negative evidence bundle.
- Dependency boundary report.
- Spike report có verdict và `docs/00-start-here.md` đồng bộ.
