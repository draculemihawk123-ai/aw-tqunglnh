# Báo cáo tiến độ V0 và checklist cần quyết định trước V0-11

> Phiên làm việc tiếp nối `docs/design/02-v0-spike-verdict.md`. Đã hoàn thành V0-07, V0-08 (mở rộng
> sau khi phát hiện thiếu), V0-09, V0-10. Toàn bộ thay đổi đang nằm ở working tree, **chưa commit**.

## 1. Đã hoàn thành trong phiên này

### V0-07 — Quarantine, recreate và stale generation SPK-09

- File mới: `internal/app/ports/workspacelifecycle.go`, `internal/adapters/sqlite/workspace_lifecycle.go`,
  `internal/adapters/sqlite/spk09_workspace_quarantine_test.go`.
- Nội dung: fenced CAS cho `RepositoryWorkspace` (READY→QUARANTINED, READY→RELEASED có chặn khi đang
  QUARANTINED, recreate sinh generation mới độc lập, generation cũ giữ QUARANTINED vĩnh viễn).
- Verify: `go build`/`go vet` sạch; test tích hợp mới chạy lặp 10 lần liên tiếp không flaky; toàn bộ
  `go test ./...` pass.

### V0-08 — Run-level evidence assembler (đã bổ sung sau khi anh xác nhận)

- **Vế 1 (ban đầu):** `internal/spikeacceptance/manifest.go` + test — validate correlation field bắt
  buộc theo từng artifact kind (runtime/providers/processes cần project/family/run/node/attempt;
  workspace cần project/family/repository/revision; workflow/assertions không yêu cầu thêm).
- **Vế 2 (bổ sung):** phát hiện comment trong `cmd/agentkit-spike/main.go` (viết từ chính commit V0-01A)
  ghi rõ việc ghi evidence bundle thật cho full-suite run là phần việc của V0-08 nhưng chưa làm. Sau khi
  anh xác nhận, đã đổi `ScenarioFunc` nhận thêm `EvidenceWriter`; `Registry.RunAll` tạo/finalize/verify
  một evidence bundle **riêng cho từng SPK** (`<suiteID>-<spkId>`, vì artifact path như
  `runtime/run.json` không có prefix theo SPK nên dùng chung 1 bundle sẽ đụng nhau); 14 stub trong
  `scenarios.go` giờ ghi bundle thật (`assertions/report.json`); CLI `agentkit-spike acceptance --full`
  giờ ghi evidence thật thay vì chỉ in console.
- Verify: `go test ./internal/spikeacceptance/... -v` 26/26 pass; đã build binary CLI thật và chạy
  `acceptance --full --evidence-dir <tmp>`, tạo đúng 14 bundle, inspect `manifest.json` và
  `evidence.Verify()` thủ công — thành công.

### V0-09 — Tamper/redaction ở cấp run SPK-14

- File mới: `internal/adapters/evidence/spk14_run_bundle_test.go` — bundle đầy đủ 6 section theo spike
  plan §12 (workflow/runtime/workspace/providers/processes/assertions), 3 test: positive (verify +
  secret không rò rỉ ra bất kỳ file nào), tamper mutate artifact, tamper thêm file không khai báo.
- Verify: 3/3 pass, chạy lặp 5 lần không flaky.

### V0-10 — Platform semantic normalizer SPK-13

- File mới: `internal/spikeacceptance/semantic_diff.go` + test — so sánh hai `SPKResult` (Windows vs
  Linux) theo allowlist cố định: bỏ qua `Platform`/`Timing`; artifact kind `processes` bỏ qua
  hash/size (PID/timing thật); mọi field còn lại (SPKID, Passed, Correlation, Assertions, artifact
  kind/path/hash/size khác) so chính xác — khác biệt không nằm trong allowlist thì fail. Không dùng
  JSON field deletion, thuần struct Go typed.
- Verify: 7 golden-fixture test pass (equivalent case, domain-mismatch case, correlation/assertion/hash
  mismatch case, missing-artifact case, determinism case).

**Toàn bộ đã chạy `go build ./...`, `go vet ./...`, `go test ./... -count=1` sạch nhiều lần trong phiên.
Chưa commit gì.**

## 2. Checklist cần anh quyết định trước khi làm V0-11

V0-11 khác bản chất các task trên: đây là task CI/infrastructure (sửa
`.github/workflows/spike-gate.yml`), không phải code Go thuần, nên em **không tự verify được** "hai
matrix jobs xanh từ clean checkout" như các task trước — việc đó cần push/trigger CI thật.

- [ ] Có cho em soạn draft workflow YAML ngay bây giờ không (build binary, chạy `acceptance --full`,
      `evidence verify`, upload artifact, thêm bước so sánh semantic)? Em soạn xong vẫn cần anh xác nhận
      nội dung trước khi push.
- [ ] Ai sẽ là người push để trigger CI thật? Em sẽ không tự `git push` nếu chưa được anh đồng ý rõ
      ràng, theo nguyên tắc an toàn về hành động ảnh hưởng shared state/CI.
- [ ] "build helper/provider binaries" trong V0-11 nghĩa là gì với codebase hiện tại? Spike plan gốc
      hình dung `cmd/fake-claude/`, `cmd/fake-codex/`, `cmd/spike-helper/` là binary riêng, nhưng thực
      tế hiện tại (ví dụ test SPK-07) dùng pattern re-invoke chính test binary
      (`os.Executable()` + `-test.run=...`) thay vì build binary `cmd/` riêng. Giữ nguyên pattern này
      hay cần tạo `cmd/` riêng cho V0-11?
- [ ] Bước "so sánh semantic results" (dùng `SemanticDiff` mới) có cần là một CI job thứ ba (tải cả hai
      artifact Windows/Linux về rồi so sánh tự động) hay để dành review thủ công sau khi tải artifact?
- [ ] Có commit các thay đổi V0-07 → V0-10 ngay bây giờ không, hay để dồn tới khi xong V0-11? Hiện chưa
      commit gì.

## 3. Toàn cảnh các task còn lại (theo `docs/design/02-v0-spike-verdict.md`)

| Task | Phụ thuộc | Trạng thái |
|---|---|---|
| V0-11 — CI Windows/Ubuntu | V0-01A, V0-03…V0-10, V0-04A | Toàn bộ dependency đã xong ở working tree; bản thân V0-11 chưa bắt đầu |
| V0-12 — Race và stability gate | V0-11 | Chưa bắt đầu |
| V0-13 — Boundary/dependency report | V0-12 | Chưa bắt đầu |
| V0-14 — Ghi verdict và đóng gate | V0-09, V0-11, V0-12, V0-13 | Chưa bắt đầu; đây là task duy nhất được cập nhật `docs/spikes/02-go-core-spike-report.md` và `docs/00-start-here.md`, theo đúng pattern đã quan sát ở V0-06 (commit chỉ đổi đúng 1 file test, không đụng report) |
