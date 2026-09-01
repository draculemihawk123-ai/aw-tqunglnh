# Báo cáo thực thi Go core spike

> Trạng thái: **GO** — gate SPK-01…SPK-14 đã đạt, được phép bắt đầu Alpha UI/runtime (mục 5,
> `docs/00-start-here.md`).
>
> Cập nhật: 2026-09-01 (V0-14, đóng gate).
>
> Phạm vi đối chiếu: [kế hoạch spike](01-go-core-spike-plan.md).

## 1. Kết luận hiện tại

**GO.** Toàn bộ 14 acceptance scenario (SPK-01…SPK-14) đã chạy thật, không phải wrapper quanh exit
code của `go test`: mỗi scenario tự Arrange/Act/Assert và ghi evidence bundle sealed+verify riêng, qua
binary chuẩn `agentkit-spike acceptance --full`. 13/14 SPK pass trực tiếp trên cả Windows và Linux
(CI thật, GitHub-hosted runner, không phải máy dev cục bộ); SPK-13 (Windows/Linux parity) không thể
kết luận từ một platform đơn lẻ theo đúng thiết kế — được kết luận bởi job `semantic-diff` riêng, so
sánh trực tiếp hai manifest thật, và đã pass.

Race detector sạch, 10/10 lần chạy full suite không flaky với bằng chứng tường minh từng lần (không
chỉ suy từ exit code), dependency boundary (domain/app không phụ thuộc adapter/OS/provider) được khoá
bằng test thật. Đủ bốn tiêu chí "Definition of done" đã ghi tại `docs/00-start-here.md` mục 7.

Không có primitive nào bị chứng minh sai trong suốt quá trình — mọi gap phát hiện được đều là thiếu
wiring/thiếu evidence, đã đóng bằng task hẹp đúng chỗ (xem mục 5 và 6).

## 2. Quyết định product owner đã chốt

1. Full gate giữ nguyên SPK-01 đến SPK-14; không có alpha rút gọn.
2. Mỗi crash recovery luôn tạo Attempt/execution mới bằng `Start` từ `ContextSnapshot`; provider
   session không là recovery path và Alpha orchestrator không gọi `Resume`.
3. Raw spike/evidence payload local mặc định 7 ngày; canonical context/audit metadata không dùng
   blanket TTL. Alpha không mã hóa at-rest và vẫn phải redact secret trước persist.
4. Windows là platform chạy/đạt trước; Linux chạy tiếp theo và vẫn bắt buộc để full gate PASS — cả
   hai đã đạt (mục 3).
5. "Hai matrix job xanh" (CI) nghĩa là harness/evidence hoàn chỉnh trên cả hai OS, không đồng nghĩa
   14/14 SPK pass và không phải verdict `GO` tự động — verdict `GO` chỉ do task này (V0-14) kết luận,
   dựa trên toàn bộ evidence tích luỹ, không phải một CI run đơn lẻ.

## 3. Bằng chứng đã chạy

CI thật ([spike-gate.yml](../../.github/workflows/spike-gate.yml)). Evidence dưới đây được tạo bởi
run [33525614475](https://github.com/taQuangLing/agent-workflow/actions/runs/33525614475) (commit
`d7be9fa`, nhánh `ci/v0-11-draft`) — run này chạy **trước** khi verdict được viết vào chính tài liệu
này, nên là nguồn evidence, không phải run xác nhận tài liệu. Commit chứa verdict (`cfc906b`, cùng
nhánh/PR) có run xác nhận bổ sung riêng —
[33527255158](https://github.com/taQuangLing/agent-workflow/actions/runs/33527255158) — xanh cả 6 job
tại đúng HEAD chứa văn bản verdict này. Cả 6 job của run gốc `33525614475` xanh:

- `contract` (windows-latest, ubuntu-latest): `go vet ./...`, `go test -count=1 ./...` PASS cả hai OS.
- `spike acceptance` (windows-latest, ubuntu-latest): `agentkit-spike acceptance --full --assessment`
  chạy thật (không qua `go test`), build đủ năm binary (`agentkit-spike`, `fake-claude`, `fake-codex`,
  `spike-helper`, `spike-worker`), 13/14 SPK PASS trực tiếp mỗi platform, evidence bundle sealed+verify,
  upload artifact `spike-evidence-{windows-latest,ubuntu-latest}`.
- `Linux race and stability` (V0-12): `go test -race -count=1 ./...` PASS (82s); 10 lần chạy full suite
  liên tiếp, **mỗi lần đều xác nhận tường minh "13 SPK genuinely Passed:true, 1 correctly
  PENDING_PEER_PLATFORM"** (`internal/spikeacceptance/registry_test.go`, không suy từ exit code); SPK-08
  100 vòng race iteration xác nhận qua kiểm tĩnh. Artifact `v0-12-stability-report`.
- `cross-platform semantic diff (SPK-13)`: tải hai manifest thật (Windows suite
  `full-20260901t153014.790686600z`, Linux suite `full-20260901t152929.811677888z`), re-verify từng
  bundle sau khi tải (13 bundle mỗi bên), diff 0 khác biệt trên 13 SPK còn lại →
  `SPK-13 authoritative passed=true`. Kết quả seal thành evidence bundle riêng, upload artifact
  `spk13-authoritative-result`.
- `Boundary/dependency report (V0-13)`: bốn test kiến trúc thật (`internal/archtest`) đều PASS.

Bằng chứng cục bộ bổ sung (Windows, máy dev, cùng ngày): suite
`full-20260901t144719.281342800z` tại `docs/spikes/evidence/` (bị Git ignore, retention 7 ngày theo
chính sách evidence/raw) — 13/14 PASS trực tiếp, mỗi bundle verify được qua
`agentkit-spike evidence verify`.

```text
go vet ./...
go test -count=1 ./...                          # PASS, cả Windows và Linux (CI)
go test -race -count=1 ./...                    # PASS trên Linux CI (CGO đủ điều kiện)
agentkit-spike acceptance --full --assessment    # 13/14 PASS trực tiếp, cả hai OS
agentkit-spike semantic-diff --left --right --out --evidence-dir   # SPK-13 authoritative PASS
agentkit-spike evidence verify                   # PASS, bundle sealed/verify được
```

`-race` trước đây bị chặn cục bộ do Go portable bundle có `CGO_ENABLED=0`; CI Linux (ubuntu-latest,
CGO đủ điều kiện mặc định) đã đóng đúng gap này, không phải work item còn treo.

## 4. Primitive đã kiểm chứng

| Primitive | Bằng chứng code/test | Kết quả |
|---|---|---|
| Workflow snapshot bất biến | compiler canonical JSON/SHA-256; SQLite publish/load verify lại snapshot | PASS (Windows+Linux CI) |
| Pin version qua restart | đóng/mở SQLite, R1 vẫn pin v1 sau publish v2 | PASS (Windows+Linux CI) |
| CAS runtime | concurrent transition chỉ một winner | PASS (Windows+Linux CI) |
| Crash/restart, đủ sáu fault-point boundary | `cmd/spike-worker` thật bị `Process.Kill` tại từng boundary chuẩn (before/after intent commit, claim, checkpoint, process-exit read-only/mutating, node-dispatch); worker thay thế recover/reclaim đúng | PASS — SPK-04, V0-10B |
| Checkpoint/context recovery + interrupted-attempt LOST/INDETERMINATE | `ContextSnapshot`/checkpoint sống sót crash; `worker.ReconcileInterruptedAttempt` chuyển đúng `RUNNING→LOST/INDETERMINATE`, không suy từ exit code; replacement attempt luôn `Start`, không `Resume` | PASS — SPK-03, V0-10C |
| Durable JobLease | claim, heartbeat, TTL/reclaim và stale completion fencing | PASS |
| WriteLease | batch all-or-none; 100 race iterations; stale job token làm write grant mất hiệu lực; race-vs-busy-handler root cause đã đóng (`_txlock=immediate`) | PASS — SPK-08, V0-11A |
| Workspace quarantine/recreate | stale-generation write bị quarantine, generation mới độc lập, cleanup/write bị chặn đúng trên generation cũ | PASS — SPK-09, V0-07 |
| Terminal authority | transaction kiểm JobLease/WriteLease, transition run, audit event và ack job | PASS |
| WorkspaceSet Git | local Git worktree thật, đa repo, reuse child, isolation root family, diff/release/generation | PASS |
| Provider abstraction | Claude/Codex adapter chung `AgentExecutor`, fake CLI là child OS process thật | PASS |
| Scope diff guard | diff READ/out-of-path bị chặn trước persistence finalization | PASS |
| Evidence integrity | bundle immutable, checksum, redaction exact-secret, tamper/unmanifested-file detection ở cấp full-run bundle; cleanup chỉ xoá bundle sealed/verify được quá 7 ngày | PASS — SPK-14, V0-09 |
| Platform semantic parity | `SemanticDiff` so sánh field-by-field (loại timing/PID không xác định), re-verify bundle hai phía sau transfer trước khi kết luận | PASS — SPK-13, V0-11 |
| Domain/app boundary | import graph, process port argv-based, provider branch absence, workflow version immutability — khoá bằng test thật, tự kiểm chứng bắt được vi phạm | PASS — V0-13 |

## 5. Ma trận SPK

| Test | Trạng thái | Ghi chú |
|---|---|---|
| SPK-01 | PASS | canonical publish/hash/immutable load |
| SPK-02 | PASS | pin R1 v1 qua restart rồi publish v2 |
| SPK-03 | PASS | interrupted attempt chuyển đúng `LOST` (read-only, không giữ WriteLease); `worker.ReconcileInterruptedAttempt` đóng gap "Known limitation" cũ (V0-10C) |
| SPK-04 | PASS | đủ sáu crash boundary chuẩn qua `cmd/spike-worker` thật, không re-invoke `go test` binary (V0-10B) |
| SPK-05 | PASS | multi-repo WorkspaceSet và child reuse |
| SPK-06 | PASS | root family worktree isolation |
| SPK-07 | PASS | scope guard chặn thật qua helper process riêng, trước fenced SQLite mutation |
| SPK-08 | PASS | 100 vòng same-repository lease race; root cause SQLITE_BUSY (busy-handler bypass khi hai transaction cùng nâng cấp write lock) đã đóng bằng `_txlock=immediate` (V0-11A) |
| SPK-09 | PASS | full workspace recreate/quarantine recovery (V0-07) |
| SPK-10 | PASS | runtime CAS conflict |
| SPK-11 | PASS | fake Claude/Codex process contract |
| SPK-12 | PASS | invalid provider session trong crash flow (V0-10) |
| SPK-13 | PASS | Windows và Linux semantic run thật, kết luận bởi job `semantic-diff` riêng (0 diff/13 SPK còn lại); không thể/không được kết luận từ một platform đơn lẻ theo đúng thiết kế |
| SPK-14 | PASS | tamper/redaction detection ở cấp full-run evidence bundle (V0-09) |

Không còn `PARTIAL` hay `notYetProven` nào trong registry (`internal/spikeacceptance/DefaultScenarios`).

## 6. Findings đã sửa trong quá trình spike

1. SQLite absolute file URI trên Windows từng coi drive letter là URI authority; adapter nay tạo
   `file:///C:/...` đúng dạng.
2. `filepath.EvalSymlinks` có thể bị `Access Denied` khi test root nằm dưới profile Windows. Git
   workspace adapter nay canonicalize handle directory qua Windows API, vẫn giữ kiểm soát reparse
   point/path safety.
3. SQLite writer race ban đầu có thể trả `SQLITE_BUSY` thay vì conflict nghiệp vụ. Adapter retry
   có giới hạn cho lỗi transient này, sau đó caller nhận semantic grant/conflict.
4. WriteLease từng chưa gắn với token của JobLease. Grant hiện mang token gốc và validation kiểm
   đồng thời JobLease còn active, workspace generation/state và attempt state.
5. Terminal run transition từng có thể gọi CAS trực tiếp. Worker terminal transition nay bắt buộc
   fenced finalization transaction; stale worker không đổi run, event hay durable job.
6. Stress run phát hiện test fencing dùng JobLease 5ms nên baseline có thể hết hạn trước assert trên
   Windows. Test nay dùng khoảng baseline 300ms rồi mới xác minh takeover; expiry semantics vẫn được
   kiểm bằng test TTL riêng.
7. Stress run cũng phát hiện `FinalizeWorkflowRun` có thể lộ `SQLITE_BUSY` khi hai worker finalise
   đồng thời. Adapter nay retry hữu hạn một transaction đã rollback hoàn toàn; kết quả cuối chỉ là
   commit, CAS conflict hoặc lease loss có nghĩa nghiệp vụ.
8. **(V0-11A)** Windows CI runner thật lộ `SQLITE_BUSY` mà `busy_timeout` không xử lý được: khi hai
   transaction cùng giữ SHARED lock rồi cùng giành nâng cấp WRITE lock, SQLite bỏ qua busy-handler để
   tránh livelock. Root cause xác nhận bằng bằng chứng cụ thể (extended result code, message driver,
   pattern SELECT-trước-INSERT), không phải đoán. Sửa bằng `_txlock=immediate` trong DSN
   (`internal/adapters/sqlite/db.go`) — mọi transaction giành write-intent lock ngay từ `BEGIN`.
9. **(V0-11A, phần 2)** Job lease và write lease của cùng một worker dùng TTL bằng nhau nhưng khởi tạo
   ở hai thời điểm khác nhau (vài mili giây), nên thời điểm hết hạn tuyệt đối khác nhau; chỉ đợi job
   lease hết hạn có thể va phải write lease còn hiệu lực vài mili giây. Sửa bằng chờ xác định tới đúng
   `WriteLeaseGrant.LeaseUntil` đã ghi nhận (`waitPastLeaseUntil`/`waitPastWriteLeaseUntil`), không suy
   đoán khoảng cách TTL.
10. **(V0-12)** `TestDefaultScenariosFormAValidRegistryAndCleanRun` — bài test mà chuỗi "10 lần chạy
    full suite không flaky" dựa vào — trước đây không kiểm `result.Passed` cho từng SPK, chỉ kiểm có
    assertion/artifact/bundle verify được. Một SPK thật sự fail vẫn có thể có đủ ba thứ đó và lọt qua.
    Tái hiện bằng vi phạm thật (ép SPK-01 trả `Passed:false`, xác nhận test cũ pass nhầm) rồi sửa: bắt
    buộc SPK-01…12/14 phải `Passed:true`, SPK-13 phải đúng `PENDING_PEER_PLATFORM`.

## 7. Theo dõi sau GO

Không có blocker nào còn treo trước khi bắt đầu mục 5 (Alpha UI/runtime). Các điểm sau không chặn `GO`
nhưng đáng lưu ý cho công việc tiếp theo:

- CI hiện chạy trên nhánh `ci/v0-11-draft` (PR #1 nhắm `docs/alpha-design-adr-020-025`, chưa merge
  `master`); cần merge nhánh mang toàn bộ V0 work (bao gồm workflow CI) vào `master` trước khi CI thật
  bảo vệ nhánh chính.
- Evidence bundle CI có retention 7 ngày (khớp policy) — nếu cần tra cứu evidence cũ hơn, phải dựa vào
  ID/log đã trích dẫn trong báo cáo này, không dựa vào artifact GitHub Actions còn tồn tại.
- Registry `DefaultScenarios` hiện có 14/14 scenario thật; bất kỳ thay đổi domain/app trong tương lai
  đều phải giữ `internal/archtest` xanh (V0-13) và không được quay lại pattern chỉ kiểm exit code thay
  vì `result.Passed` khi viết stability/regression test mới (V0-12 finding #10).
