# Báo cáo thực thi Go core spike

> Trạng thái: IN PROGRESS — chưa đạt gate để bắt đầu alpha UI/runtime.
>
> Cập nhật: 2026-08-29
>
> Phạm vi đối chiếu: [kế hoạch spike](01-go-core-spike-plan.md).

## 1. Kết luận hiện tại

Go phù hợp để tiếp tục spike: các primitive trọng yếu đã có implementation và test chạy thật trên
Windows/SQLite/Git/OS process. Checkpoint/context provider-neutral đã pass ở mức persistence và fresh
start cục bộ, nhưng chưa được nối vào cùng một crash acceptance flow. Chưa được phép tuyên bố `GO`
cho alpha vì chưa có end-to-end evidence bundle cho toàn bộ 14 acceptance tests và chưa chạy Linux.

Không bắt đầu UI alpha từ báo cáo này.

## 2. Quyết định product owner đã chốt

1. Full gate giữ nguyên SPK-01 đến SPK-14; không có alpha rút gọn.
2. Mỗi crash recovery luôn tạo Attempt/execution mới bằng `Start` từ `ContextSnapshot`; provider
   session không là recovery path và Alpha orchestrator không gọi `Resume`.
3. Raw spike/evidence payload local mặc định 7 ngày; canonical context/audit metadata không dùng
   blanket TTL. Alpha không mã hóa at-rest và vẫn phải redact secret trước persist.
4. Windows là platform chạy/đạt trước; Linux chạy tiếp theo và vẫn bắt buộc để full gate PASS.

## 3. Bằng chứng đã chạy

```text
go test -count=10 ./internal/adapters/sqlite
go test -count=1 ./...  # lặp 10 lần liên tiếp
go vet ./...
```

Ba lệnh trên đã PASS trên Windows; full offline suite pass 10/10 sau khi sửa hai flake được phát
hiện trong lần chạy stress. CI [spike-gate](../../.github/workflows/spike-gate.yml) đã được thêm để
chạy cùng contract trên Windows/Ubuntu, `-race` và 10 vòng stability trên Linux khi thay đổi được push.
`-race` chưa chạy được với Go portable bundle hiện tại vì
`CGO_ENABLED=0`; đây là một work item của CI Linux/Windows, không được coi là đã pass race gate.

Baseline acceptance harness đã chạy thật trên Windows và tạo bundle sealed/verify được:

```text
agentkit-spike acceptance --offline --evidence-dir docs/spikes/evidence --go <go.exe>
agentkit-spike evidence verify --evidence-dir docs/spikes/evidence --suite <suite-id>
```

Kết quả hiện có là `offline-20260828t125032.676738100z`. Bundle generated bị Git ignore và là
evidence cho baseline `go test`; nó chưa là verdict cho SPK-01 đến SPK-14. Harness redact giá trị
credential môi trường, seal checksum và chỉ prune bundle sealed/verify được quá 7 ngày.

## 4. Primitive đã kiểm chứng

| Primitive | Bằng chứng code/test | Kết quả |
|---|---|---|
| Workflow snapshot bất biến | compiler canonical JSON/SHA-256; SQLite publish/load verify lại snapshot | PASS cục bộ |
| Pin version qua restart | đóng/mở SQLite, R1 vẫn pin v1 sau publish v2 | PASS cục bộ |
| CAS runtime | concurrent transition chỉ một winner | PASS cục bộ |
| Crash/restart | worker OS process thật bị `Process.Kill`; worker mới recover/reclaim lease | PASS một fault path |
| Checkpoint/context recovery | ContextSnapshot canonical + checkpoint immutable qua SQLite đóng/mở; replacement attempt luôn gọi `Start`, không gọi `Resume` | PASS cục bộ |
| Durable JobLease | claim, heartbeat, TTL/reclaim và stale completion fencing | PASS cục bộ |
| WriteLease | batch all-or-none; 100 race iterations; stale job token làm write grant mất hiệu lực | PASS cục bộ |
| Terminal authority | transaction kiểm JobLease/WriteLease, transition run, audit event và ack job | PASS cục bộ |
| WorkspaceSet Git | local Git worktree thật, đa repo, reuse child, isolation root family, diff/release/generation | PASS cục bộ |
| Provider abstraction | Claude/Codex adapter chung `AgentExecutor`, fake CLI là child OS process | PASS cục bộ |
| Scope diff guard | diff READ/out-of-path bị chặn trước persistence finalization | PASS unit |
| Evidence integrity | bundle immutable, checksum, redaction exact-secret, tamper/unmanifested-file detection; cleanup chỉ xoá bundle sealed/verify được quá 7 ngày | PASS adapter |

## 5. Ma trận SPK

| Test | Trạng thái | Ghi chú ngắn |
|---|---|---|
| SPK-01 | PASS cục bộ | canonical publish/hash/immutable load |
| SPK-02 | PASS cục bộ | pin R1 v1 qua restart rồi publish v2 |
| SPK-03 | PARTIAL | hard kill + recover, checkpoint/context SQLite restart và fresh start pass; chưa nối cùng một crash acceptance flow |
| SPK-04 | PARTIAL | hai boundary cục bộ đã có; full registry/evidence cho đủ sáu crash boundary chuẩn chưa hoàn tất |
| SPK-05 | PASS cục bộ | multi-repo WorkspaceSet và child reuse |
| SPK-06 | PASS cục bộ | root family worktree isolation |
| SPK-07 | PARTIAL | scope guard chặn trước fenced SQLite mutation pass; chưa có mount isolation/process-provider end-to-end |
| SPK-08 | PASS cục bộ | 100 vòng same-repository lease race |
| SPK-09 | PARTIAL | job/write token fencing pass; chưa full workspace recreate/quarantine recovery |
| SPK-10 | PASS cục bộ | runtime CAS conflict |
| SPK-11 | PASS cục bộ | fake Claude/Codex process contract |
| SPK-12 | PARTIAL | ContextSnapshot/checkpoint survive restart và replacement luôn `Start`; chưa inject provider session invalid trong crash flow |
| SPK-13 | PARTIAL | Windows pass; Linux semantic run chưa có |
| SPK-14 | PARTIAL | bundle verify/tamper pass; chưa nối vào run thực |

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

## 7. Việc phải hoàn tất trước Go/REWORK decision

1. Nối `ContextSnapshot`, checkpoint sequence và reconciliation policy vào một acceptance flow có
   `Process.Kill`; thêm SPK-03/04/12 với process kill thật ở mọi fault point và invalid provider session.
2. Dựng offline acceptance runner/evidence integration để mỗi run ghi manifest tương quan từ
   project/family/run/attempt/repository/revision, rồi verify/tamper ở cấp run.
3. Nối workspace provider, scope guard, provider adapter và worker finalizer trong một end-to-end
   test không mock infrastructure boundary.
4. Test workspace recreate/quarantine và stale generation; hoàn tất scope/mount enforcement.
5. Chạy cùng semantic suite trên Linux, đồng thời bật `-race` trong CI có CGO/toolchain phù hợp.
6. Chỉ sau các mục trên mới cập nhật báo cáo thành `GO`, `REWORK` hoặc `STOP`.
