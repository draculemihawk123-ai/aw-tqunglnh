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
| V11-06 | Cổng duyệt của người không cho xem thay đổi chưa commit: tab Diff trống cho tới khi commit | Cao | **Đã gặp thật** (chạy issue-tracker C-01 trên Windows, 2026-10-09); cơ chế đã đọc trong mã |
| V11-07 | Duyệt xong vẫn phải chạy lệnh riêng để commit; commit luôn lấy toàn bộ worktree, không chọn được file | Cao | Cơ chế **đã đọc trong mã**; thiết kế cần product owner chốt 6 điểm (mục "Quyết định cần chốt" của V11-07) |

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

## V11-06 — Duyệt cổng người mà không xem được nội dung thay đổi

> Đây là **thiếu khả năng**, không phải lỗi đúng nghĩa. Vẫn đưa vào V11 vì nó làm cổng duyệt (`APPROVAL`) mất tác dụng trên web:
> người duyệt phải quyết định mà không thấy agent đã sửa gì, trong khi đó là lý do tồn tại của cổng. Product owner có thể chuyển
> sang một version khác nếu muốn giữ V11 chỉ cho lỗi.

- **Bằng chứng:** chạy C-01 của `docs/guides/issue-tracker` (người vận hành, Windows, UI qua `aw serve --ui-dist`). Run tới node
  `review` (APPROVAL, `WAITING`). Trang Task, tab Workspace, mục Diff hiện `e738631385 → e738631385 · 0 files changed` và "No
  changes between the base and current revision", dù agent đã ghi `spec/openapi.json` trong worktree.
- **Nguyên nhân (đã đọc trong mã, chưa viết test):** `web/src/screens/TaskDetail.tsx` (`WorkspaceTab`) truy vấn diff với
  `base = baseRevision` và `result = currentRevision` của workspace; `internal/adapters/gitworktree/inspection.go` (`ReadDiff`)
  chạy `git diff <base> <result>` giữa **hai commit**, sau `authorizeRevision` cho từng revision. `currentRevision` chỉ tiến lên
  qua ReleaseSet Local Commit (xem `run_start_revisions.go`). Thay đổi chưa commit trong worktree không có revision, nên không có
  chỗ nào trong API hay UI để xem; tab Source cũng chỉ đọc file tại một revision. Muốn xem, người vận hành phải mở worktree bằng
  terminal hoặc IDE (`worktree-path.sh`), mà thao tác Git ngoài aw lại là chính lỗi của V11-01.
- **Mục tiêu:** tại cổng duyệt, người duyệt xem được trên web (và CLI) **những gì đã thay đổi so với `currentRevision`**, kiểu một pull
  request: danh sách file (thêm/sửa/xóa, số dòng), diff từng file, file mới (untracked) có nội dung đầy đủ, file nhị phân chỉ hiện
  tên và kích thước. Không cần commit trước khi duyệt.
- **Thực hiện (đề xuất):**
  1. Mở rộng `ports.WorkspaceInspectionReader` với đọc diff **working tree so với một revision đã được ủy quyền**: tracked bằng
     `git diff <currentRevision> --`, untracked bằng `git ls-files --others --exclude-standard` rồi `git diff --no-index` từng
     file với `/dev/null`. Chỉ đọc: chạy với `GIT_OPTIONAL_LOCKS=0`, không `git add`, không ghi index, không đổi HEAD.
  2. Giữ nguyên giới hạn của `ReadDiff` (`byteLimit`, `fileLimit`, `lineLimit`, `maxDiffSummaryBytes`), kiểm đường dẫn nằm trong
     worktree (symlink không được thoát ra ngoài), bỏ qua `.git`.
  3. Chỉ cho phép khi workspace **không có node ghi đang chạy** (đang ở APPROVAL hoặc run đã dừng), để tránh đọc nửa chừng; nếu
     đang có attempt ghi thì trả mã lỗi rõ ràng thay vì kết quả sai.
  3b. Mỗi file trong kết quả có `contentHash` và kết quả có `changeSetDigest` (V11-07 dùng chúng để bảo đảm commit đúng cái đã duyệt).
  4. API: tham số `result=WORKING_TREE` (hoặc route riêng) cho `GET` diff của workspace; CLI `aw repository-workspace diff
     --working-tree`; kết quả có cờ `uncommitted: true` để UI không nhầm với commit.
  5. UI: trong tab Workspace thêm chế độ **"Thay đổi chưa commit"**, mặc định bật khi `currentRevision` không đổi so với `base`
     nhưng worktree có thay đổi; ở thẻ yêu cầu duyệt (Graph & Timeline) thêm dòng tóm tắt "N file đổi, +x −y" kèm liên kết. Văn bản
     tab Diff hiện tại khi trống phải nói rõ "có thay đổi chưa commit" thay vì "No changes".
  6. Cập nhật `docs/operator/06-source-control-and-releases.md`, hướng dẫn issue-tracker (bước duyệt C-01) và thêm ADR.
- **Ngoài phạm vi:** sửa hay stage từ UI; diff giữa hai run; bình luận trên dòng.
- **Verify:** test tích hợp với worktree thật: file sửa, file mới, file xóa, file nhị phân, file lớn quá giới hạn, symlink ra ngoài
  worktree; sau mỗi lệnh đọc thì `git status --porcelain`, `git rev-parse HEAD` và hash của `.git/index` không đổi; gọi khi attempt
  ghi đang chạy trả lỗi đúng mã. Test UI (Vitest) cho chế độ mới và cho thông điệp khi trống. Chạy lại C-01 trên bản cài thật và
  chụp màn hình tab Diff trước khi duyệt.
- **Cách làm tạm trong lúc chưa có:** `cd "$(worktree-path.sh contracts)" && git status --short && git diff`; chỉ xem, không thao
  tác Git ghi. Đã ghi vào hướng dẫn issue-tracker (bước duyệt C-01); chưa ghi vào `docs/operator/`.

## V11-07 — Duyệt và commit trong một thao tác, chọn được file, làm hoàn toàn trên web

> Cũng là **thiếu khả năng** như V11-06, đưa vào V11 vì cùng gốc: cổng duyệt không đi tới được điều mà nó phải quyết định.
> Task lớn (engine, API, CLI, UI, kit). Làm sau V11-06, vì cần chung danh sách thay đổi và băm của V11-06.

### Bằng chứng (đã đọc trong mã)

- `internal/adapters/gitworktree/localcommit.go` (`CreateLocalCommit`): chạy `git add -A` rồi `git commit`. `ports.CreateLocalCommitRequest`
  chỉ có `Handle, Message, AuthorName, AuthorEmail`, **không có danh sách đường dẫn**. Mọi thay đổi trong worktree, kể cả file
  mới chưa theo dõi, đều vào commit; không có cách commit một phần.
- Worktree thuộc về **TaskFamily và repository**, không thuộc về một task. `kit/scripts/commit-task.sh` phải tự cảnh báo "family còn
  task dở" vì commit sẽ đóng dấu luôn phần việc chưa duyệt của task khác.
- Duyệt (`internal/app/runtime/approval.go`, `ResolveApproval`) chỉ ghi quyết định và đẩy run đi tiếp trong **một transaction**.
  Commit là một thao tác khác (`release-set create → seal → local-commit`, job nền `ExecuteReleaseSetLocalCommit`), nên người dùng
  phải chạy `commit-task.sh` hoặc thao tác tab Workspace sau khi duyệt. Quên bước này thì task sau bị `SCOPE_VIOLATION` mà thông
  báo không nói nguyên nhân (đã gặp khi chạy issue-tracker).
- `docs/architecture/04-go-core-spec.md` mục 11.1: không gọi Git trong transaction. Vì vậy "duyệt kèm commit" không thể gọi
  `git commit` ngay trong `ResolveApproval`; phải tạo ý định commit bền trong cùng transaction rồi để job nền thực hiện.
- ADR-014 cho phép local commit "khi workflow policy cho phép hoặc operator phát typed command". Thiết kế dưới đây thỏa cả hai
  vế: workflow khai policy cho phép, và quyết định của người là lệnh có kiểu.

### Mục tiêu

Ở cổng duyệt trên web, người duyệt: (1) xem được **đúng những file sẽ bị ảnh hưởng** (V11-06); (2) chọn outcome; (3) tích "Commit"
và chọn file nào được commit, file còn lại xử lý ra sao; (4) bấm một nút. Engine ghi quyết định, đẩy run đi tiếp và tạo local
commit cho đúng tập file đã chọn. Mọi bước, kể cả xử lý khi commit lỗi, làm được trên web; CLI có lệnh tương đương (parity).

### Đối tượng của commit là gì

| Khái niệm | Định nghĩa |
|---|---|
| **Tập thay đổi** (change set) | Với mỗi repository của family: các đường dẫn mà worktree khác `currentRevision` của workspace, tính như `git status --porcelain=v1 -z --untracked-files=all`, không gồm file bị `.gitignore`. Đây là cùng nguồn với tab diff của V11-06 và với `scopeguard`. |
| Đơn vị chọn | **File** (đường dẫn). Xóa là một mục; đổi tên là **một cặp** (đường dẫn cũ và mới được chọn hoặc bỏ cùng nhau). Không chọn theo dòng hay hunk (ngoài phạm vi, xem dưới). |
| Phạm vi chọn | Từng repository riêng. Repository không tích chọn thì không tạo commit. Mỗi repository có thay đổi nhận một commit, như hiện nay (ADR-004: không atomic xuyên repository). |
| Mặc định | Chọn **tất cả** file của các repository có thay đổi (giữ hành vi hiện tại), người duyệt bỏ chọn nếu muốn. |
| Nguồn gốc từng file | Nếu khảo sát thấy engine đã lưu danh sách đường dẫn của từng attempt (nó đã tính `WorkspaceDiff.Files` để kiểm scope), UI gắn nhãn "do node X, vòng N". Nếu chưa lưu thì thêm vào evidence của attempt. Mục đích: khi family có nhiều task, người duyệt biết file nào của task nào. |

### Commit một phần: điều gì xảy ra với file không được chọn

Sau commit một phần, worktree vẫn còn các file đó. Mà node chỉ-đọc (CHECKER, MACHINE_GATE) coi worktree còn thay đổi là
`SCOPE_VIOLATION` (`validateStrictlyReadOnlyDiffs`), nên người duyệt **phải chọn** cách xử lý, không để mặc định âm thầm:

| Xử lý | Ý nghĩa | Ghi chú |
|---|---|---|
| `KEEP` (mặc định) | Để nguyên trong worktree, chưa commit | Giống hôm nay. UI cảnh báo: node chỉ-đọc kế tiếp của family sẽ bị chặn cho tới khi file được commit hoặc cất đi |
| `PARK` | Cất vào một ref ẩn `refs/agentkit/parked/<releaseSetId>` rồi dọn khỏi worktree | Khôi phục được bằng một nút ("Khôi phục file đã cất"); worktree sạch nên các node sau chạy bình thường |
| Xóa hẳn | **Không có trong V11-07** | Xóa không hoàn tác được; muốn bỏ file thì `PARK` rồi để cơ chế retention dọn. Nếu product owner muốn thì làm thành task riêng có xác nhận gõ lại tên file |

### Thực hiện (đề xuất, theo thứ tự)

1. **Engine, commit theo đường dẫn.** `ports.CreateLocalCommitRequest` thêm `Paths []string` (rỗng = tất cả, giữ hành vi cũ) và
   `Unselected` (`KEEP` hoặc `PARK`). `CreateLocalCommit`: kiểm mọi đường dẫn thuộc tập thay đổi; `git reset -q` (chỉ index) rồi
   `git add -A -- :(literal)<path>…` qua `--pathspec-from-file=- --pathspec-file-nul` (chống glob, dấu cách, tiền tố `-`, unicode);
   `git commit` không `-a`; xác minh sau commit rằng cây commit chỉ chứa đúng đường dẫn đã chọn. `PARK`: `git stash create`
   cho phần còn lại, ghi vào ref ẩn rồi `git restore`/`git clean` đúng các đường dẫn đó. Mọi bước dưới `WriteLease` hiện có;
   dấu `marker` phục hồi sau crash giữ nguyên và test lại.
2. **Chống lệch giữa lúc xem và lúc commit ("duyệt đúng cái đã thấy").** Quyết định mang theo `changeSet`: danh sách
   `{repositoryId, path, status, contentHash}` của các file được chọn, và `changeSetDigest`. Job commit tính lại trong lúc
   giữ `WriteLease`; khác thì **không commit**, kết thúc `FAILED/CHANGESET_DRIFT`. Cùng cơ chế với `FAILED/NO_CHANGES` hiện có.
3. **Chính sách của workflow.** `ApprovalNodeConfig` thêm `release`: `{"commit": "DISABLED"|"OFFERED"|"REQUIRED", "outcomes": ["approved"]}`.
   Mặc định `DISABLED` nên mọi định nghĩa cũ không đổi hash, không đổi hành vi. Compiler từ chối khi một outcome có `release` dẫn
   tới node có thể ghi (commit nền sẽ đua với agent): chỉ cho outcome dẫn tới `END` hoặc tới các node không ghi.
4. **`ResolveApproval` nhận `commit`.** Trong **cùng một transaction** với quyết định: kiểm policy và quyền, kiểm `outcome` nằm
   trong `release.outcomes`, tạo ReleaseSet cho family, seal, và tạo `ReleaseSetLocalCommit` ở `REQUESTED` cho từng repository kèm
   job nền. Không gọi Git trong transaction (mục 11.1). Request hash gồm cả `commit`, nên replay idempotent trả lại cùng id các
   thao tác commit. Kết quả trả về danh sách thao tác commit để UI theo dõi.
5. **Lưu bằng chứng của cái đã duyệt.** Tại thời điểm quyết định, lưu patch của các file được chọn (có giới hạn byte) thành
   artifact gắn với `ApprovalRequest`, kèm `changeSetDigest`. Sau này xem lại được người duyệt đã thấy gì, kể cả khi worktree
   đã đổi.
6. **Commit lỗi sau khi duyệt.** Run đã đi tiếp và không rollback. Commit `FAILED` (`NO_CHANGES`, `CHANGESET_DRIFT`, workspace
   `QUARANTINED`, `CONFLICT`) mở blocker mới `RELEASE_FAILED` trên WorkItem (hiện ở Board) và dòng trạng thái từng repository.
   Có thao tác "Thử commit lại" mở lại hộp thoại với danh sách file **mới**, không tái dùng lựa chọn cũ. Cần khảo sát: quan hệ với
   `CompletionPolicy` (WorkItem có được `DONE` khi commit chưa xong, theo ADR-014 "parent chỉ DONE khi mọi repository bắt buộc đạt
   release policy hoặc policy cho phép uncommitted").
7. **Giao diện (web).** Trong trang Task, thẻ yêu cầu duyệt có tóm tắt "N file đổi, +x −y" và liên kết tới tab Workspace.
   Hộp thoại duyệt: các nút outcome như hôm nay; nếu `release` cho phép, hiện mục **Commit**: tích chọn, cây thư mục có hộp
   kiểm ba trạng thái theo file, diff của file đang chọn bên cạnh, cách xử lý file không chọn (`KEEP`/`PARK`), ô commit message
   (điền sẵn từ tiêu đề WorkItem) và tác giả. Sau khi bấm: trạng thái từng repository (đang commit, đã commit kèm id, lỗi) và
   liên kết tới Diff của commit. Có các nút "Khôi phục file đã cất" và "Thử commit lại". Tab Workspace giữ `Seal`/`Local Commit`
   thủ công và dùng chung bộ chọn file (cùng backend).
8. **API, CLI, parity.** Thêm vào hợp đồng OpenAPI và regenerate `web/src/api/generated.ts`; `aw approval resolve` nhận
   `--commit-spec <json|->`; `aw release-set local-commit` nhận `--path` (lặp được) và `--unselected`; đăng ký trong
   `internal/delivery/parity/registry.go`, `go run ./cmd/docs-coverage-check` nợ 0.
9. **Kit và tài liệu.** `review-task.sh` thêm `--commit "<message>"` (dùng cùng API, mặc định chọn tất cả); các workflow mẫu có
   bước người duyệt ghi file (`wf-contract-change`, `wf-task-delivery*`) bật `OFFERED`; `commit-task.sh` giữ làm đường thủ công và ghi
   chú. Cập nhật `docs/operator/` (04, 06), hướng dẫn issue-tracker (bỏ bước `commit-task.sh` khỏi luồng chính), thêm ADR mới
   ("approval-bound release", bổ sung ADR-014).

### Quyết định cần chốt với product owner (chưa tự quyết)

1. **Mặc định `KEEP` hay `PARK`** cho file không chọn? (Đề xuất `KEEP` để không đổi hành vi, kèm cảnh báo rõ.)
2. **Có cần xóa hẳn file** từ UI không, hay `PARK` là đủ? (Đề xuất: không, làm sau nếu cần.)
3. **Tác giả commit:** lấy từ đâu? Hiện `commit-task.sh` lấy `git config` của máy chạy script; trên web không có nguồn đó. Đề
   xuất: safe setting `commitAuthor` của bản cài, người duyệt sửa được trong hộp thoại, bắt buộc có giá trị.
4. **Vai trò:** quyền duyệt và quyền commit cùng một role, hay tách (`release` có `authorizedRoles` riêng)? (Đề xuất: mặc định
   bằng quyền duyệt, cho khai tách khi cần.)
5. **`REQUIRED`** có cần không, hay chỉ `OFFERED`? (`REQUIRED` buộc phải commit khi duyệt; hữu ích cho quy trình không cho phép
   duyệt mà chưa commit.)
6. **Mức chọn:** file, không phải hunk. Chọn theo dòng hay hunk cần dựng patch và áp bằng `git apply --cached`, có rủi ro lệch
   nội dung; đề xuất ngoài phạm vi, làm thành task sau.

### Ngoài phạm vi

Push, tạo pull request, merge (ADR-014 vẫn cấm); chọn theo hunk/dòng; sửa nội dung file từ UI; commit cho repository ngoài family.

### Verify

- **Engine (test tích hợp, Git thật):** tập file gồm sửa, mới, xóa, đổi tên, tên có dấu cách/unicode/ký tự glob/bắt đầu bằng `-`,
  file nhị phân, symlink; chọn một phần thì `git show --name-status HEAD` chỉ chứa đúng file đã chọn, file còn lại đúng theo
  `KEEP` (còn trong worktree) hoặc `PARK` (worktree sạch, ref ẩn chứa đúng nội dung, khôi phục lại giống hệt); đường dẫn `..`,
  tuyệt đối, `.git/...`, hoặc không thuộc tập thay đổi bị từ chối; file đổi giữa lúc xem và lúc commit cho `CHANGESET_DRIFT` và
  không có commit; crash giữa chừng và `marker` vẫn phục hồi đúng.
- **Duyệt kèm commit:** workflow thử có node duyệt `OFFERED`; duyệt với chọn một phần cho run `SUCCEEDED` và đúng một commit
  mỗi repository đã chọn; outcome không nằm trong `release.outcomes` bị từ chối; compiler từ chối `release` dẫn tới node ghi;
  replay cùng idempotency key trả cùng kết quả; khác `commit` mà cùng key bị `ErrReceiptConflict`; role không đủ quyền bị
  `POLICY_DENIED`; nhiều repository mà một repository lỗi cho blocker `RELEASE_FAILED`, repository kia vẫn commit, không
  rollback.
- **Không hồi quy:** workflow không có `release` giữ nguyên hash và hành vi; `commit-task.sh` và `aw release-set local-commit`
  không có `--path` vẫn commit tất cả; V11-01 (HEAD lệch) vẫn bị chặn trước khi commit.
- **UI:** Vitest cho hộp thoại (chọn một phần, ba trạng thái thư mục, cảnh báo `KEEP`, trạng thái lỗi) và e2e Playwright chạy trên
  bản cài thật: duyệt kèm commit từ trình duyệt, không dùng terminal ở bước nào, rồi kiểm tab Diff của commit mới.
- **Kit:** kịch bản `walk-workflow.py` cho duyệt kèm commit; `kit/tests/check-kit.sh` với `WALK_ALL=1` xanh.

## Gate của V11

Kế thừa gate chung ở mục 6 của `00-roadmap.md`, thêm:

1. Mỗi task có test hồi quy thất bại trước khi sửa, xanh sau khi sửa; commit tách riêng test và sửa, hoặc ghi rõ trong message.
2. `go test ./...` xanh; `go run ./cmd/docs-coverage-check` nợ 0; CI hiện có (gồm `v8-alpha-gate`) xanh.
3. Mỗi thay đổi hành vi công khai có ADR và cập nhật `docs/operator/`.
4. `kit/tests/check-kit.sh` với `WALK_ALL=1` xanh sau khi core đổi (kịch bản chạy trên engine thật).
5. Hai script tái hiện của V11-01 cho kết quả đúng thiết kế.
6. V11-06: chụp màn hình tab Diff của một cổng duyệt thật hiện nội dung chưa commit; test chứng minh index và HEAD không đổi sau khi đọc.
7. V11-07: một lần duyệt trên web, không mở terminal, tạo commit chỉ chứa đúng các file đã chọn; file còn lại đúng theo xử lý đã chọn; `changeSetDigest` lệch thì không commit.
