# V11 — Sửa lỗi (core engine được phép sửa)

> Trạng thái: **ĐANG SOẠN — chờ product owner duyệt, chưa triển khai.** File này chia task; chưa task nào được thực hiện.
> Khi triển khai: mỗi task một commit, merge vào `master` khi product owner duyệt.
>
> Entry: sau khi nhánh V10 (`claude/ecstatic-lovelace-me0xg7`) được merge vào `master`. Nếu muốn làm V11 trước khi merge V10, tạo nhánh
> V11 từ nhánh V10 hiện tại; product owner chọn.
>
> Exit: gate V11 ở cuối file.

> Phạm vi task mặc định: kế thừa mục 3 của `00-roadmap.md`. Khác V10: **V11 được phép sửa core engine** (`internal/`, `cmd/`,
> `web/`) vì các lỗi dưới đây nằm ở đó.

## Bối cảnh

V11 là nơi gom **lỗi** (không phải tính năng mới) phát hiện khi làm và đo V10. Mỗi lỗi có bằng chứng tái hiện được. Lỗi nào
chưa được xác minh trong mã thì ghi rõ "cần xác minh" và task bắt đầu bằng khảo sát.

## Nguyên tắc của V11

- **Tái hiện trước, sửa sau.** Mỗi task bắt đầu bằng một test thất bại (hoặc script tái hiện đã có) chứng minh lỗi, rồi sửa
  cho test xanh, rồi giữ test đó làm test hồi quy.
- **Chỉ sửa lỗi của task.** Không thêm tính năng ngoài phạm vi từng task. Nếu cách sửa đúng cần thêm một lệnh hay một mã lỗi
  công khai, task ghi rõ và có mục tài liệu cho nó.
- **Fail-closed vẫn là mặc định.** Không sửa bằng cách bỏ một kiểm tra; sửa bằng cách làm kiểm tra nói rõ nguyên nhân hoặc
  thêm đường xử lý có kiểm soát và có audit.
- Đổi hành vi công khai (mã lỗi, trạng thái, lệnh CLI) phải có ADR mới trong `docs/architecture/02-architecture-decisions.md`
  và cập nhật `docs/operator/`.
- Tài liệu tiếng Việt, thuật ngữ kỹ thuật giữ tiếng Anh.

## Danh sách task

| Task | Lỗi | Mức | Trạng thái xác minh |
|---|---|---|---|
| V11-01 | Commit ngoài aw (IDE hoặc terminal) làm lệch HEAD so với revision aw ghi | Cao | **Đã tái hiện** (script trong repo) |
| V11-02 | Nhà cung cấp báo hết hạn mức phiên bị coi là lỗi thực thi thường, đốt hết lần thử | Cao | **Đã gặp thật** (bench B0/B1) |
| V11-03 | Cạnh hết vòng trỏ vào trong chính vòng làm run treo âm thầm | Cao | Đã tái hiện; nguyên nhân gốc **cần xác minh** |
| V11-04 | CHECKER sau gate không nhận kết quả kiểm tra; agent sửa sau CHECKER cũng không | Trung bình | Đã tái hiện (prompt dump) |
| V11-05 | aw không cô lập cấu hình của CLI nhà cung cấp; hook ghi file gây `SCOPE_VIOLATION` | Trung bình | Đã đo (spike V10-17) |

Mặc định làm tuần tự theo Task ID.

## V11-01 — Commit ngoài aw làm lệch HEAD so với revision aw ghi

- **Mục tiêu:** người dùng tự `git commit` (IDE hoặc terminal) trong worktree do aw quản lý, kể cả lúc run đang chờ duyệt,
  không còn gây hỏng khó hiểu: aw phát hiện, nói rõ nguyên nhân, và có đường xử lý có kiểm soát và có audit.
- **Phụ thuộc:** không.
- **Bằng chứng (đã tái hiện trên agent giả lập, không tốn tiền):**
  - [`repro-approval-waiting.py`](../spikes/v11-01-external-commit/repro-approval-waiting.py): run `wf-task-delivery-plus`
    dừng ở cổng duyệt; người dùng commit tay trong worktree; duyệt cổng. Kết quả: run chạy tiếp và **thành công**, aw không
    phát hiện gì. Trạng thái aw ghi `currentRevision = 1ddc449` trong khi HEAD thật là `2f5f08d`. `commit-task.sh` sau đó báo
    "không worktree nào có thay đổi để commit" vì commit tay đã lấy hết thay đổi, và ReleaseSet của aw không biết gì về commit đó.
  - [`repro-machine-gate.py`](../spikes/v11-01-external-commit/repro-machine-gate.py): workflow có `MACHINE_GATE`.
    Trước commit tay: gate qua. Sau commit tay: WorkItem mới có `secrets` **FAILED, `VALIDATION_FAILED`**, run `FAILED`, blocker
    `RUN_FAILED` mở. Thông báo không nêu nguyên nhân.
- **Nguyên nhân (đã đọc mã):** `internal/app/runtime/gate_node_executor.go` hàm `staleMountRevision` so HEAD thật của worktree
  với revision đã ghim vào snapshot của attempt, và trả `VALIDATION_FAILED` trần khi lệch. Revision aw ghim cho run mới lấy từ
  `currentRevision` của workspace (`run_start_revisions.go`), mà `currentRevision` chỉ được cập nhật bởi local commit của
  ReleaseSet (`AdvanceRepositoryWorkspaceRevision`, `internal/app/releasesetcommit/execute.go`). Không có đường nào để aw biết
  về commit ngoài. Node thường (agent, command) không so HEAD nên không phát hiện; chỉ `MACHINE_GATE` so.
- **Khảo sát cần làm trước khi sửa (đầu task):**
  1. Liệt kê mọi nơi so HEAD với revision đã ghim, ngoài `staleMountRevision` (ví dụ `handleMutatingCancellation`, kiểm quarantine).
  2. Run đang chờ duyệt đã ghim revision cũ trong manifest. Sau khi chấp nhận commit tay, run này còn ghim revision cũ nên cổng
     máy sau đó vẫn thấy lệch. Xem cơ chế `run_manifest_amendments` (đã dùng cho `scope_expansion` và `retry_blocked_activation`)
     có dùng được để cập nhật revision ghim của run đang chờ không; nếu không, cách xử lý là kết thúc run và bắt đầu lại.
  3. Xác nhận cách `aw workspace-set show` và `aw repository-workspace diff` báo lệch hiện nay.
- **Thực hiện (thiết kế đề xuất; chốt sau khảo sát):**
  1. **Nói rõ nguyên nhân.** Khi kiểm tra tươi của gate phát hiện lệch, attempt vẫn thất bại fail-closed, nhưng kèm chẩn đoán có kiểu
     (ví dụ `WORKSPACE_HEAD_DRIFT`): repository, revision đã ghim, HEAD thật, và danh sách commit nằm ngoài aw
     (`git log <pinned>..HEAD`: hash, tác giả, tiêu đề). Hiển thị trong `aw run show/diagnostics` và `show-run.sh`.
  2. **Lệnh nhận commit ngoài có kiểm soát:** `aw repository-workspace adopt-head --expected-version <n> --idempotency-key <k> <id>`.
     Chỉ chạy khi: không có attempt ghi nào đang giữ khóa ghi, worktree không `QUARANTINED`, worktree sạch (không thay đổi chưa commit),
     và HEAD là hậu duệ tuyến tính của `currentRevision` (fast-forward). Khi đó ghi `currentRevision = HEAD`, `version + 1`, phát sự
     kiện audit (danh sách commit, tác giả, người thao tác). Từ chối với thông báo rõ khi HEAD không phải hậu duệ (đã `reset`
     hoặc `rebase`): hướng dẫn dùng `reconcile` hoặc đưa worktree về revision aw ghi.
  3. **Run đang chờ duyệt:** theo kết quả khảo sát (2): hoặc cho phép cập nhật revision ghim của run sau khi nhận commit, hoặc
     báo rõ khi duyệt cổng rằng worktree đã lệch và run phải bắt đầu lại.
  4. **Run mới:** `aw run start` kiểm lệch trước khi bắt đầu; mặc định từ chối với thông báo nêu cách xử lý (fail-closed), không
     tự nhận commit. Có thể thêm cờ `--adopt-head` nhưng **không bật mặc định**: nhận commit ngoài là đưa mã chưa qua gate vào
     lịch sử mà aw coi là đáng tin, nên phải do người vận hành chủ động chọn.
  5. **Kit:** `commit-task.sh`, `run-task.sh`, `watch-run.sh` in cảnh báo rõ khi thấy lệch; thêm `adopt-head.sh` bao lệnh trên.
  6. **Tài liệu:** `docs/operator/06-source-control-and-releases.md` (mục mới "Commit ngoài aw") và `09-troubleshooting.md`; ADR mới.
- **Verify:**
  - Test thất bại trước rồi xanh: hai kịch bản của hai script tái hiện, viết lại thành test tích hợp Go (theo kiểu
    `internal/integration/v5accept`): (a) commit tay lúc chờ duyệt rồi duyệt; (b) WorkItem mới có `MACHINE_GATE` sau commit tay.
    Kết quả mong đợi: thông báo có kiểu và có danh sách commit; sau `adopt-head` hợp lệ thì (b) chạy qua.
  - Test từ chối: HEAD không phải hậu duệ; worktree bẩn; đang có attempt ghi; worktree `QUARANTINED`.
  - Test audit: sự kiện ghi đủ commit, tác giả, người thao tác; `adopt-head` chạy hai lần với cùng idempotency key không nhận hai lần.
  - Hai script tái hiện chạy lại cho kết quả mới theo thiết kế; `go test ./...` xanh.
- **Hoàn thành khi:** thông báo lỗi nêu nguyên nhân, có đường nhận commit ngoài có audit, test hồi quy xanh, tài liệu và ADR có.
- **Rủi ro:** nhận commit ngoài làm mã chưa qua gate được ghi vào lịch sử aw coi là hợp lệ; vì vậy chỉ do hành động tường minh
  của người vận hành, có audit, và gate của các run sau vẫn chạy trên mã đó.

## V11-02 — Hết hạn mức phiên của nhà cung cấp bị coi là lỗi thực thi thường

- **Bằng chứng:** khi đo B0/B1 (2026-10-07), CLI Claude trả "You've hit your session limit · resets 5:30pm (UTC)". aw ghi
  `PROVIDER_REPORTED_FAILURE` rồi `EXECUTION_FAILED`, đốt cả hai lần thử của attempt policy trong vài giây ở mọi lượt chạy song
  song, và run `FAILED` với blocker `RUN_FAILED`. Hạn mức reset sau vài giờ, nên thử lại ngay không bao giờ có ích.
- **Mục tiêu:** nhận diện lỗi hạn mức/rate limit của nhà cung cấp là loại riêng; không đốt lần thử; dừng có thể tiếp tục.
- **Khảo sát:** adapter nào nhận được thông điệp nào (Claude: `rate_limit_event` và kết quả lỗi; Codex có gì tương đương); có
  tín hiệu máy đọc được (thời điểm reset) hay chỉ văn bản.
- **Thực hiện (đề xuất):** mã lỗi `PROVIDER_QUOTA_EXCEEDED` không thuộc `RetryableErrorCodes`; attempt kết thúc với lý do này và mở
  blocker có thể `retry-blocked` sau thời điểm reset (nếu đọc được) thay vì làm run `FAILED`; `show-run.sh` nêu rõ giờ reset.
- **Verify:** adapter giả lập phát thông báo hạn mức: một attempt, không thử lại, run vào trạng thái chờ được; retry sau đó chạy tiếp.

## V11-03 — Cạnh hết vòng trỏ vào trong chính vòng làm run treo âm thầm

- **Bằng chứng:** `wf-task-delivery-plus` với node `simplify` có `cyclePolicy` và cạnh `escalated → quality` (nút `quality` quay
  lại được vòng qua `build`). Khi hết vòng, run đứng ở `RUNNING`, attempt kề trước kết thúc `INDETERMINATE`
  (`OWNERSHIP_LOST_MUTATING`) sau 30 giây, không có thông báo lỗi. Đổi cạnh sang `reject` thì hết treo (kịch bản
  `plus-large-exhaust` của `kit/scripts/walk-workflow.py`).
- **Cần xác minh:** `internal/app/runtime/advance.go` có kiểm `ErrCycleEscalationNotBounded` lúc chạy, và chú thích nói compiler đã
  chặn trước khi publish (`validateBoundedCycles`). Thực tế publish chấp nhận định nghĩa và lỗi chỉ nổ lúc chạy, sau khi node
  trước đã xong. Chưa xác định: vì sao compiler không bắt (định nghĩa "cùng thành phần liên thông" khác nhau giữa hai chỗ?), và
  vì sao lỗi runtime dẫn tới `INDETERMINATE` thay vì lỗi rõ ràng.
- **Mục tiêu:** định nghĩa như vậy bị từ chối lúc publish với thông báo nêu node và cạnh; nếu vẫn nổ lúc chạy thì run thất bại
  rõ ràng, không treo.
- **Verify:** test compiler với định nghĩa của `wf-task-delivery-plus` bản cũ (cạnh `escalated → quality`) bị từ chối; test runtime
  cho cạnh sai không để lại attempt `INDETERMINATE`.

## V11-04 — CHECKER sau gate không nhận kết quả kiểm tra

- **Bằng chứng:** trong `wf-task-delivery-plus`, `gate1 (failed) → debug (CHECKER) → build`: prompt của `debug` không có
  `checkFailures`, và prompt của `build` sau đó chỉ có `reviewerFeedback` (báo cáo của `debug`), không có `checkFailures`
  (`walk-workflow.py`, kịch bản `plus-debug`, dump prompt). Tài liệu `docs/operator/04-authoring-workflows.md` nói `checkFailures`
  chỉ đến với maker đi thẳng từ kiểm tra.
- **Mục tiêu:** quyết định có nên đưa kết quả kiểm tra gần nhất vào prompt của CHECKER nằm giữa kiểm tra và maker không (và
  `checkFailures` có đi tiếp qua CHECKER không). Đây là thay đổi hành vi công khai, nên cần ADR; nếu product owner quyết giữ nguyên,
  task đóng bằng cách ghi rõ giới hạn trong tài liệu và dùng cách đang áp dụng ở kit (agent chẩn đoán tự tái hiện).
- **Verify:** nếu thay đổi: kịch bản `plus-debug` cho thấy `debug` và `build` nhận lỗi của `gate1`.

## V11-05 — Cô lập cấu hình CLI của nhà cung cấp

- **Bằng chứng (spike V10-17, [`../spikes/v10-17-claude-skills/README.md`](../spikes/v10-17-claude-skills/README.md)):**
  cấu hình cấp người dùng (`~/.claude`, qua `HOME`) và `.claude/` trong repository đều được CLI nạp; skill nằm ngoài
  ContextSnapshot; hook ghi file vào worktree gây `SCOPE_VIOLATION` ở MAKER; CHECKER cũng nạp skill của repository.
- **Mục tiêu:** worker có tùy chọn cô lập cấu hình của CLI (thư mục cấu hình riêng, không nạp cấu hình người dùng/dự án) và/hoặc ghi
  băm của cây `.claude/` vào manifest của attempt. Kiểm cờ tương ứng của CLI (ví dụ `CLAUDE_CONFIG_DIR`, chọn nguồn cấu hình) trước.
- **Verify:** lượt dò giống spike cho thấy skill của repository không còn lọt vào attempt khi bật cô lập, hoặc xuất hiện trong manifest.

## Gate của V11

Kế thừa gate chung ở mục 6 của `00-roadmap.md`, thêm:

1. Mỗi task có test hồi quy thất bại trước khi sửa, xanh sau khi sửa; commit tách riêng test và sửa, hoặc ghi rõ trong message.
2. `go test ./...` xanh; `go run ./cmd/docs-coverage-check` nợ 0; CI hiện có (gồm `v8-alpha-gate`) xanh.
3. Mỗi thay đổi hành vi công khai có ADR và cập nhật `docs/operator/`.
4. `kit/tests/check-kit.sh` với `WALK_ALL=1` xanh sau khi core đổi (kịch bản chạy trên engine thật).
5. Hai script tái hiện của V11-01 cho kết quả đúng thiết kế.
