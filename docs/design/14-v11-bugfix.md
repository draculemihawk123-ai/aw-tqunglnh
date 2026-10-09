# V11 — Sửa lỗi (core engine được phép sửa)

> Trạng thái: **ĐANG SOẠN — chờ product owner duyệt, chưa triển khai.** File này chia task; chưa task nào được thực hiện.
> Khi triển khai: mỗi task một commit, merge vào `master` khi product owner duyệt.
>
> Nhánh thiết kế: `v11-design` (tách từ `master` sau PR #174; mọi thay đổi thiết kế V11 đi vào nhánh này). V10 đã vào `master`; các file
> V10-19 (`check-feature-docs.sh` bản không kiểm máy, `code-accept.py`, `skill-doc-templates`) và bản vá kit cho Windows nằm ở nhánh
> `v10-followup`, vào `master` khi PR của nhánh đó được merge. Nhánh triển khai V11 bắt đầu từ `master` sau khi hai PR này đã merge.
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
| V11-08 | Ngôn ngữ trong kit không thống nhất: tri thức và thông báo của agent là tiếng Việt, tài liệu cho người cũng tiếng Việt | Trung bình | Chính sách đã nêu; hiệu ứng lên chi phí và chất lượng **chưa đo** |
| V11-09 | Bộ bọc `.cmd` trên Windows làm `cmd.exe` chạy phần bash như lệnh Windows, agent chỉ thấy rác | Cao | **Đã tái hiện và vá tạm** trên Windows 11 (Git 2.55); chạy tay file `.cmd` đã vá cho kết quả đúng; chưa thử trên máy khác |
| V11-10 | Chạy lại và làm lại một task phải ghép nhiều bước web và terminal (hủy trên web, `run-task.sh` trên terminal) | Trung bình | Cơ chế **đã đọc trong mã**; kiểm chứng bằng chính các lần chạy lại của người vận hành |
| V11-11 | Bấm vào một node trên web không cho biết node đang làm gì và kết quả là gì; Chat không cho biết agent đang làm việc | Cao | Cơ chế **đã đọc trong mã**; dữ liệu phần lớn đã có trong DB nhưng không có API/UI đọc |
| V11-12 | Đăng ký repository sai không sửa được, không xóa được; lỗi chỉ hiện mã `INVALID_ARGUMENT`, không nói sai chỗ nào | Cao | **Đã gặp thật** (đăng ký thư mục chưa `git init` trên Windows, 2026-10-09); cơ chế đã đọc trong mã |
| V11-13 | Project tạo ra không đổi tên được, không xóa hay lưu trữ được | Trung bình | Cơ chế **đã đọc trong mã**: chỉ có `create`, `list`, `show` |

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
2b. **Danh tính tác giả từ `git config`.** Thêm một cổng đọc `ports.GitIdentityReader`: `git -C <worktree> config --get user.name` và
   `user.email` (cấu hình của repository đè cấu hình toàn cục; báo nguồn `local` hay `global`). Chỉ đọc, chạy ở tiến trình `aw`, nên
   tiến trình đó phải có `HOME` (Windows: `USERPROFILE`) trong môi trường, nếu không sẽ không thấy cấu hình toàn cục và báo thiếu giả.
   Mỗi repository có danh tính riêng (repository có `user.name` cục bộ khác được giữ). Cách dùng:
   - **Trước khi hiện mục Commit**, UI gọi truy vấn mới (`GET` danh tính theo repository) và hiển thị "Tác giả: Tên <email> (git config,
     nguồn)" chỉ đọc cho từng repository.
   - **Thiếu `user.name` hoặc `user.email`** của repository nào: hiện cảnh báo rõ repository đó, kèm lệnh
     `git config --global user.name "Tên"` và `git config --global user.email "email"`, nút "Kiểm tra lại", và **vô hiệu hóa tích Commit**
     cho tới khi có. Duyệt không kèm commit vẫn dùng được.
   - **Chặn cả ở máy chủ:** `ResolveApproval` có `commit` mà thiếu danh tính thì **từ chối toàn bộ lệnh với mã `COMMIT_IDENTITY_MISSING`,
     trước khi ghi bất cứ thứ gì** (cổng duyệt vẫn `PENDING`, run không đi tiếp). Việc đọc `git config` làm **ngoài transaction**, rồi truyền
     giá trị đã đọc vào lệnh; danh tính được lưu cùng thao tác commit và job dùng đúng giá trị đó, không đọc lại, để không lệch giữa lúc
     duyệt và lúc commit. Danh tính truyền cho `git` bằng `-c user.name=… -c user.email=…` như hiện nay, không ghi vào cấu hình của worktree.
   - Đề xuất nhỏ kèm theo: thêm kiểm tra `aw doctor` mức cảnh báo cho danh tính git toàn cục, để người vận hành biết sớm.
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
   (điền sẵn từ tiêu đề WorkItem) và dòng tác giả chỉ đọc lấy từ `git config` (bước 2b). Sau khi bấm: trạng thái từng repository (đang commit, đã commit kèm id, lỗi) và
   liên kết tới Diff của commit. Có các nút "Khôi phục file đã cất" và "Thử commit lại". Tab Workspace giữ `Seal`/`Local Commit`
   thủ công và dùng chung bộ chọn file (cùng backend).
8. **API, CLI, parity.** Thêm vào hợp đồng OpenAPI và regenerate `web/src/api/generated.ts`; `aw approval resolve` nhận
   `--commit-spec <json|->`; `aw release-set local-commit` nhận `--path` (lặp được) và `--unselected`; đăng ký trong
   `internal/delivery/parity/registry.go`, `go run ./cmd/docs-coverage-check` nợ 0.
9. **Kit và tài liệu.** `review-task.sh` thêm `--commit "<message>"` (dùng cùng API, mặc định chọn tất cả); các workflow mẫu có
   bước người duyệt ghi file (`wf-contract-change`, `wf-task-delivery*`) bật `OFFERED`; `commit-task.sh` giữ làm đường thủ công và ghi
   chú. Cập nhật `docs/operator/` (04, 06), hướng dẫn issue-tracker (bỏ bước `commit-task.sh` khỏi luồng chính), thêm ADR mới
   ("approval-bound release", bổ sung ADR-014).

### Quyết định đã chốt (product owner, 2026-10-09)

1. **File không chọn: mặc định `KEEP`.** `PARK` vẫn là lựa chọn không mặc định.
2. **Không xóa hẳn file** từ UI. Muốn bỏ file thì `PARK`.
3. **Tác giả commit lấy từ `git config` của máy chạy `aw`**, không có ô nhập tay và không có safe setting riêng. Khi người duyệt tích Commit,
   engine đọc `user.name` và `user.email` trước; **thiếu thì hiện cảnh báo hướng dẫn cấu hình git** và không cho commit (xem bước 2b).
4. **Quyền commit = quyền duyệt** (cùng `authorizedRoles` của node). Không có role riêng cho release.
5. **Dùng cả `OFFERED` và `REQUIRED`; kit chỉ bật `OFFERED`** (khuyến nghị được nhận). Lý do và cách xử lý ở "Về `REQUIRED`" bên dưới.
6. **Chọn theo file, không theo hunk/dòng.**

#### Về `REQUIRED` (khuyến nghị)

- `REQUIRED` hợp với quy trình "không có duyệt nào mà chưa commit", đúng lỗi mà người vận hành đã gặp ở C-01. Nhưng nếu áp cho node mà
  lần chạy đó không đổi file nào (ví dụ cổng duyệt tài liệu đã commit từ trước) thì buộc commit sẽ thành lỗi vô lý.
- Vì vậy: schema hỗ trợ cả ba giá trị; **kit chỉ dùng `OFFERED`**; với `REQUIRED` mà tập thay đổi rỗng thì commit **được bỏ qua và ghi
  nhận `NOTHING_TO_COMMIT`** (không coi là lỗi, không tạo blocker), còn người duyệt vẫn bắt buộc đi qua hộp thoại commit khi có file.
  Với `OFFERED` rỗng thì mục Commit không hiện.
- Ngoài ra `NO_CHANGES` của lệnh commit thủ công (`aw release-set local-commit`) giữ nguyên như hiện nay.

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
- **Danh tính:** repository có `user.name` cục bộ dùng giá trị cục bộ, thiếu cục bộ thì dùng toàn cục; thiếu cả hai thì UI hiện cảnh báo với lệnh
  cấu hình và vô hiệu hóa tích Commit; gọi thẳng `ResolveApproval` có `commit` khi thiếu danh tính trả `COMMIT_IDENTITY_MISSING` và
  cổng duyệt vẫn `PENDING`, run không đổi; danh tính đổi giữa lúc duyệt và lúc commit không ảnh hưởng commit (dùng giá trị đã lưu);
  tiến trình thiếu `HOME`/`USERPROFILE` báo thiếu thay vì đoán; chạy trên Windows (Git Bash).
- **`REQUIRED`:** tập thay đổi rỗng thì ghi `NOTHING_TO_COMMIT`, không blocker, run vẫn đi tiếp; có file thì buộc đi qua hộp thoại commit.
- **Không hồi quy:** workflow không có `release` giữ nguyên hash và hành vi; `commit-task.sh` và `aw release-set local-commit`
  không có `--path` vẫn commit tất cả; V11-01 (HEAD lệch) vẫn bị chặn trước khi commit.
- **UI:** Vitest cho hộp thoại (chọn một phần, ba trạng thái thư mục, cảnh báo `KEEP`, trạng thái lỗi) và e2e Playwright chạy trên
  bản cài thật: duyệt kèm commit từ trình duyệt, không dùng terminal ở bước nào, rồi kiểm tab Diff của commit mới.
- **Kit:** kịch bản `walk-workflow.py` cho duyệt kèm commit; `kit/tests/check-kit.sh` với `WALK_ALL=1` xanh.

## V11-08 — Thống nhất ngôn ngữ của kit

- **Hiện trạng:** skill, layer, thông báo lỗi của script, tên và mô tả định nghĩa đều bằng tiếng Việt (83 tài nguyên tri thức, khoảng
  67 KB, 9.341 ký tự có dấu; 11 script trong `kit/commands/`, mỗi script 4 đến 21 dòng có ký tự không ASCII). Tài liệu cho người cũng
  tiếng Việt. Chưa có quy ước nào nói phần nào dùng ngôn ngữ nào.
- **Quyết định của product owner (2026-10-09):** phần agent và engine đọc dùng **tiếng Anh**; phần dành cho người đọc giữ tiếng Việt.
  Chính sách:

  | Phần | Ngôn ngữ |
  |---|---|
  | Skill, layer, tài nguyên của agent (`kit/skills`, `kit/layers`) | Tiếng Anh |
  | Script chạy trong worker (`kit/commands/*.sh`): thông báo, chú thích | Tiếng Anh, **chỉ ASCII** |
  | Tên và mô tả định nghĩa trong `kit.json`, workflow, policy | Tiếng Anh |
  | README, hướng dẫn, `docs/design`, báo cáo | Tiếng Việt |
  | Script vận hành cho người (`kit/scripts/*.sh`) | Tiếng Việt |
  | **Tài liệu agent sinh ra** (brainstorm, spec, design, plan) | Theo ngôn ngữ của `taskContract.behavior` |

- **Thực hiện (đề xuất, theo thứ tự, mỗi bước một commit):**
  1. **Script worker sang tiếng Anh ASCII** (không còn vì lỗi Windows: V11-09 cho thấy tiếng Việt không phải nguyên nhân; làm vì thống nhất ngôn ngữ) (`kit/commands/*.sh`, khoảng 120 dòng). Thêm kiểm tra trong `kit/tests/check-kit.sh`: script
     publish lên worker có ký tự không ASCII thì hỏng. Sửa các test đang dò chuỗi tiếng Việt (`THIẾU mục`, `WHAT:`/`FIX:`...).
  2. **Luật ngôn ngữ cho tài liệu sinh ra:** thêm vào `flow.working-rules` (một chỗ duy nhất): "viết tài liệu bằng ngôn ngữ của
     `taskContract.behavior`; tên mục theo khung của skill". Giữ hợp đồng skill ↔ kiểm tra bằng máy: `plan.checklist` ↔ `check-plan.sh`
     (từ khóa tiêu đề song ngữ), và các từ khóa trong `check-feature-docs.sh`.
  3. **Dịch skill và layer** (83 tài nguyên) và tên/mô tả định nghĩa. Giữ nguyên ý nghĩa; không chỉnh nội dung cùng lúc để việc đo
     không bị lẫn. Cập nhật `revision` của từng tài nguyên.
  4. **Đo A/B** (bench B-series, hai lượt mỗi nhóm, chấm mù tài liệu và mã, kiểm chấp nhận độc lập `code-accept.py`) giữa bản tiếng
     Việt và bản tiếng Anh: chi phí, vòng sửa, điểm tài liệu, điểm mã, **ngôn ngữ của tài liệu sinh ra**. Quyết định giữ hay quay lại
     dựa trên số đo; bước 1 và 2 giữ riêng vì có lý do độc lập (Windows, rõ ràng).
- **Rủi ro cần biết:** (a) chất lượng và chi phí có thể đổi theo cả hai hướng, hiện chưa có số; (b) agent có thể viết tài liệu tiếng
  Anh khi người dùng cần tiếng Việt, nên luật ở bước 2 phải được đo; (c) mọi số B0–B4 đo với skill tiếng Việt, nên sau khi dịch phải
  lập mốc mới, không so thẳng; (d) người vận hành đọc log agent bằng tiếng Anh.
- **Verify:** `check-kit.sh` với `WALK_ALL=1` xanh; `grep -P '[^\x00-\x7F]' kit/commands/*.sh` rỗng; bench A/B có báo cáo; một lượt chạy
  với work item tiếng Việt cho tài liệu tiếng Việt.

## V11-09 — Bộ bọc `.cmd` trên Windows chạy phần bash như lệnh Windows

- **Bằng chứng (người vận hành, Windows 11 build 26100, Git 2.55.0.windows.3, 2026-10-09):** task W-01 của issue-tracker; bước
  `test` luôn trả `failed` nhưng nội dung lỗi mà agent nhận là chuỗi `'<từ>' is not recognized as an internal or external command`
  với các từ lấy từ chú thích và thông báo của `lib.sh`. Agent sửa mò ba vòng (2,2 USD), rồi dừng bằng `needs_info` và vi phạm scope.
  Chạy tay `sh kit/commands/npm-test.sh` trong Git Bash thì in đúng lỗi thật (`Cannot find module '@testing-library/dom'`); chạy tay
  file `.cmd` do `aw-publish.py` tạo bằng `cmd //c` thì tái hiện lỗi: `cmd.exe` thực thi từ **giữa dòng** (có khi giữa một ký tự UTF-8)
  của phần bash, tức đọc file sai vị trí sau khi chạy lệnh ngoài.
- **Nguyên nhân (đã xác nhận một phần, 2026-10-09):** lỗi nằm ở **phần đầu của file `.cmd`** (nhiều dòng, chỉ `\n`), không phải ở nội
  dung bash hay tiếng Việt. Bằng chứng: sau khi đổi phần đầu thành một khối lệnh duy nhất kèm đuôi CRLF (bản vá tạm bên dưới),
  chạy tay file `npm-test.cmd` do `aw-publish.py` tạo (**vẫn chứa tiếng Việt**) bằng `cmd //c` cho đúng thông báo của `npm-test.sh`,
  không còn dòng `is not recognized`. Chưa phân biệt được giữa "một khối" và "CRLF" cái nào là cái có tác dụng (bản vá làm cả hai);
  chưa kiểm trên máy Windows khác hay phiên bản Git khác. Bài thử 8 tổ hợp chưa cần chạy nữa trừ khi muốn tách hai yếu tố này.
- **Bản vá tạm trong kit:** phần đầu thành một khối lệnh duy nhất có `enabledelayedexpansion`, đuôi dòng CRLF riêng cho phần đầu,
  phần bash giữ LF; có kiểm tra tự động về hình dạng nội dung (không kiểm hành vi `cmd.exe`).
- **Mục tiêu:** bước kiểm tra chạy trên Windows cho cùng kết quả và cùng thông báo như trên Linux, với mọi script trong `kit/commands/`.
- **Thực hiện (đề xuất):** (a) xác định nguyên nhân bằng bài thử đã soạn và ghi vào đây; (b) tách hai yếu tố "một khối" và "CRLF" để giữ bản vá nhỏ nhất; (c) cân nhắc sửa ở engine để không cần bộ bọc: Command khai thông dịch viên (`interpreter:
  sh`) và worker trên Windows tự gọi `sh.exe` của Git, thay vì kit bọc file; (d) `aw doctor` thêm kiểm tra `sh.exe` của Git for Windows.
- **Verify:** trên máy Windows có Git for Windows: tất cả script `kit/commands` chạy qua `cmd //c` cho đúng mã thoát và đúng thông
  báo, với nội dung có tiếng Việt và không có; một lượt chạy thật của W-01 cho agent nhận đúng lỗi test thay vì rác; kịch bản kiểm
  tra trong CI nếu có runner Windows.

## V11-10 — Chạy lại và làm lại một task bằng một thao tác trên web

> Cũng là **thiếu khả năng** (như V11-06, V11-07). Mục tiêu: người vận hành không phải rời web để xử lý một task hỏng.

### Bằng chứng (đã đọc trong mã)

- Hôm nay "chạy lại" gồm nhiều bước ở nhiều nơi. Trên web có từng mảnh: tab Chat (gửi ghi chú cho agent), gỡ blocker `RUN_FAILED`
  (`resolveWorkItemBlocker`, chỉ chọn `RESOLVED`), nút `Start Run` (chỉ hiện khi WorkItem `READY`), `Cancel Run`, `Cancel WorkItem`.
  `kit/scripts/retry-task.sh` làm cả chuỗi đó một lượt (in lỗi, gỡ blocker, gửi ghi chú, start run) nhưng phải mở terminal.
- Nút `Retry` có sẵn trong `TaskDetail.tsx` gọi `retryBlockedActivation` là **việc khác**: thử lại một activation bị chặn (ví dụ
  `ADAPTER_BUILD_DRIFT`); nó không chạy lại một run đã `FAILED`.
- Run mới trên cùng WorkItem **luôn dùng workflow version đã ghim**: `StartWorkflowRun` (`internal/app/runtime/commands.go`) từ chối với
  `ErrWorkflowVersionMismatch` nếu version yêu cầu khác version đã ghim trong contract. Vì vậy sau khi sửa kit hoặc định nghĩa rồi publish
  lại, "chạy lại" trên WorkItem cũ vẫn chạy bản **cũ** (đã gặp: sửa bộ bọc `.cmd` rồi vẫn phải tạo WorkItem mới).
- Tạo WorkItem **con** có API (`createChildWorkItem` trong `web/src/api/generated.ts`, `aw work-item create-child`) nhưng **không có
  giao diện**: `CreateWorkItemDialog.tsx` ghi rõ việc này "deferred to V7-11" và chưa ai làm. Nên "hủy rồi tạo mới" hiện phải qua `run-task.sh`.
- Hậu quả thực tế: một lần sửa kit rồi chạy lại cần bốn bước ở hai nơi (hủy trên web, xóa file thừa trong worktree, publish, `run-task.sh`),
  và `commit-task.sh` từ chối chạy khi family còn WorkItem `ACTIVE` hoặc `BLOCKED`, nên quên hủy bản cũ là chặn luôn việc commit.

### Hai thao tác cần có

**A. Chạy lại (cùng WorkItem).** Nút `Chạy lại` ở trang Task khi run gần nhất `FAILED` hoặc bị hủy. Hộp thoại: ô ghi chú cho agent (tùy
chọn), dòng thông tin "sẽ chạy bằng workflow version đã ghim: <version>", và xem trước các blocker sẽ được gỡ. Một lần bấm: gửi ghi chú,
gỡ blocker `RUN_FAILED` đang mở, start run mới. Dùng khi nguyên nhân là môi trường hoặc ghi chú đủ để agent làm đúng.

**B. Làm lại với định nghĩa mới (hủy và tạo mới).** Nút `Làm lại bằng phiên bản mới` ở trang Task. Hộp thoại hiển thị: version workflow
đang ghim và version mới nhất của **cùng** workflow definition, tùy chọn mang theo các ghi chú của người vận hành sang WorkItem mới, ô lý
do. Một lần bấm: hủy WorkItem cũ, tạo WorkItem con mới cùng parent với **cùng nội dung** (tiêu đề thêm hậu tố "lần 2", `effectiveScope`,
contract) nhưng `workflowVersionId` là version mới nhất, đánh dấu READY, start run. WorkItem mới ghi `supersedes` trỏ về cái cũ, UI
Board hiện "thay thế cho ..." và WorkItem cũ hiện "bị thay bởi ...". Dùng khi vừa sửa kit hoặc định nghĩa và publish lại.

### Thực hiện (đề xuất)

1. **Lệnh gộp trong core, không ghép nhiều gọi từ trình duyệt.** Nếu UI gọi tuần tự nhiều API thì một lỗi giữa chừng để lại trạng thái dở
   (đã gỡ blocker mà chưa start run; đã hủy cái cũ mà chưa tạo cái mới). Thêm hai lệnh có kiểu, idempotent, mỗi lệnh một idempotency key:
   `RetryWorkItem` (ghi chú + gỡ blocker + start run, trong một transaction cho phần ghi DB) và `RecreateWorkItem` (kiểm điều kiện, tạo
   WorkItem mới và run, yêu cầu hủy cái cũ). CLI: `aw work-item retry <id> [--note ...]` và `aw work-item recreate <id> [--note ...]
   [--carry-messages]`; đăng ký trong `internal/delivery/parity/registry.go`; sinh lại `web/src/api/generated.ts`.
2. **Thứ tự và an toàn của `RecreateWorkItem`.** Kiểm tất cả điều kiện trước khi đổi gì: WorkItem cũ không có run đang chạy hoặc đang ở bước
   ghi; family không có worktree `QUARANTINED`; tồn tại version workflow mới hơn hoặc ít nhất cùng version; scope của bản mới là tập con của
   scope được phê duyệt của family. Thao tác ghi: tạo WorkItem mới và run trong một transaction; yêu cầu hủy WorkItem cũ (theo cơ chế hủy hiện
   có, bất đồng bộ) trong cùng transaction. Cần khảo sát: hai WorkItem cùng một worktree của family trong lúc bản cũ đang được hủy.
3. **Lỗi giữa chừng.** Cả hai lệnh trả kết quả rõ (đã làm tới đâu); lỗi điều kiện trả mã có kiểu và UI giải thích bằng lời, không để trạng
   thái dở. Replay cùng idempotency key trả đúng kết quả cũ, kể cả id WorkItem mới.
4. **File thừa của lần chạy trước.** Lần chạy hỏng có thể để lại file ngoài scope (ví dụ `docs/needs-info.md`) làm lần sau vi phạm `SCOPE_VIOLATION`. Hộp
   thoại liệt kê các đường dẫn đang thay đổi trong worktree (dùng danh sách thay đổi của V11-06) và đánh dấu cái nào **ngoài scope** của
   WorkItem; lệnh từ chối chạy khi còn file ngoài scope mà người dùng chưa chọn cách xử lý: `Giữ` (mặc định, kèm cảnh báo sẽ vi phạm lại) hoặc
   `Cất` (cùng cơ chế `PARK` của V11-07). Không xóa hẳn file. Nếu V11-06 và V11-07 chưa xong thì bước này tạm bỏ và UI chỉ hiện cảnh báo.
5. **Giao diện.** Hai nút ở thanh đầu trang Task, đặt cạnh `Cancel Run` và `Cancel WorkItem`; chỉ hiện khi đủ điều kiện (`validActions` từ
   `getRunDiagnostics`, như các nút khác). Sau khi bấm, trang chuyển tới WorkItem mới (nếu là B) và theo dõi run. Hộp thoại ghi rõ chi phí
   đã tiêu của các run trước (để người vận hành thấy việc lặp lại tốn bao nhiêu).
6. **Kit và tài liệu.** `retry-task.sh` gọi `aw work-item retry` (giữ phần in lỗi của bước kiểm tra); thêm `recreate-task.sh` mỏng; cập nhật
   `docs/operator/`, hướng dẫn issue-tracker (bỏ bước "hủy trên web rồi `run-task.sh`" khỏi luồng sửa lỗi), thêm ADR cho hai lệnh mới.

### Quyết định đã chốt (product owner)

1. **Làm lại = hủy WorkItem cũ rồi tạo WorkItem con mới (B)**, không đổi version đã ghim trên chính WorkItem. Giữ bất biến "WorkItem ghim một
   version"; mỗi lần làm lại để lại một WorkItem `CANCELLED` kèm liên kết `supersedes`. Phương án "đổi version đã ghim" không làm trong V11.
2. **Mang theo ghi chú của người vận hành sang WorkItem mới: mặc định có**, hộp thoại có ô bỏ chọn.
3. **Không giới hạn cứng số lần làm lại.** Hộp thoại hiện tổng chi phí các lần trước và cảnh báo từ lần thứ ba.

### Ngoài phạm vi

Tự động quyết định chạy lại; chạy lại một phần (từ node hỏng thay vì từ đầu: `aw` không chạy tiếp từ node bị hỏng, chạy lại là chạy workflow từ đầu);
sửa nội dung contract khi làm lại (làm lại giữ nguyên contract); thay đổi quyền.

### Verify

- **Engine (test tích hợp):** `RetryWorkItem`: run `FAILED` với blocker mở thành run mới, ghi chú có trong prompt, blocker `RESOLVED`; lỗi giữa
  chừng không để blocker đã gỡ mà chưa có run; replay cùng key cho cùng kết quả; không chạy khi WorkItem không `READY` sau khi gỡ blocker.
  `RecreateWorkItem`: WorkItem mới có đúng contract, scope và version mới nhất, `supersedes` đúng, cái cũ vào trạng thái hủy; từ chối khi có run
  đang ghi, khi worktree `QUARANTINED`, khi scope vượt phê duyệt; khi version mới nhất bằng version đã ghim vẫn tạo được (làm lại sạch).
- **Không hồi quy:** `StartWorkflowRun` vẫn từ chối version khác version đã ghim; `commit-task.sh` không còn bị WorkItem cũ chặn sau khi làm lại.
- **UI:** Vitest cho hai hộp thoại và trạng thái nút theo `validActions`; e2e Playwright trên bản cài thật: một run hỏng, bấm `Chạy lại`, rồi bấm
  `Làm lại bằng phiên bản mới` sau khi publish workflow mới, không mở terminal.
- **Kit:** kịch bản `walk-workflow.py` cho retry và recreate; `kit/tests/check-kit.sh` với `WALK_ALL=1` xanh.

## V11-11 — Xem tiến trình và kết quả của từng node trên web; Chat thành nguồn tin hoạt động của agent

> Cũng là **thiếu khả năng**. Mục tiêu: người vận hành mở web là biết mỗi node đang làm gì, đã làm gì, kết quả là gì và vì sao
> hỏng, không phải mở terminal hay đọc DB.

### Bằng chứng (đã đọc trong mã)

- **Bấm vào node chỉ lọc timeline.** Trong `GraphTimelineTab` (`web/src/screens/TaskDetail.tsx`), `setSelectedNodeKey` chỉ lọc danh sách
  timeline bên phải. Mỗi dòng timeline chỉ có: loại bản ghi, trạng thái, `selectedOutcome`, mã lỗi, token/chi phí, thời điểm. **Không có nội
  dung**: không có agent đã nói gì, đã gọi công cụ gì, đã sửa file nào, lệnh in ra gì.
- **Dữ liệu có sẵn nhưng không có đường đọc theo node:**
  - `agent_events` (EXECUTION_STARTED, ASSISTANT_MESSAGE, TOOL_CALL_STARTED/FINISHED, DIAGNOSTIC, USAGE_REPORTED, CHECKPOINT_PROPOSED,
    EXECUTION_FINISHED), đã redact, ghi liên tục lúc chạy. **Không API, CLI hay UI nào đọc** (ghi nhận trong `internal/domain/runtime/evidence.go`
    và `agent_output_evidence.go`). Chỉ hai trường hợp được rút ra: danh sách vi phạm scope và lý do nhà cung cấp báo lỗi.
  - Evidence `AGENT_OUTPUT` (V9-17) giữ **tin nhắn cuối** của agent, nhưng **chỉ khi attempt SUCCEEDED**. Attempt FAILED không để lại lời agent.
  - Evidence `COMMAND_EXECUTION` có artifact `application/vnd.agentkit.command-output+json` (exit code, argv, thời lượng, stdout, stderr đã
    redact). Evidence của MACHINE_GATE có từng tiêu chí. Evidence `AGENT_EXECUTION` có manifest diff.
  - Tab **Evidence** liệt kê các dòng của cả WorkItem, phẳng, chỉ ghi `kind` và verdict; **không nói thuộc node nào** dù `nodeRunId` có trong
    dữ liệu, và artifact kiểu `...command-output+json` không nằm trong danh sách media type xem trước được (`isInlineSafeMediaType`), nên
    chỉ tải về được.
  - Lời nhắc (prompt) mà node nhận: `InstructionArtifact` được tạo và lưu cho mỗi attempt (`assemble_execution_request.go`), không có đường đọc.
- **Không có hiển thị khi đang chạy.** Không màn hình nào dùng `watchProjectEvents` (SSE), Chat thăm dò mỗi 4 giây nhưng chỉ đọc tin nhắn. Một
  node agent chạy mười phút trông giống hệt một node bị treo.
- **Chat không phải nơi agent nói.** `message.Role` đã có `ASSISTANT`, `SYSTEM`, `TOOL`, `Message.AttemptID` đã có, nhưng nơi duy nhất gọi
  `AppendMessage` là API do người dùng gõ; engine không bao giờ ghi vào thread. Tab Chat vì vậy chỉ chứa tin của chính người vận hành.
- **Rủi ro cần tránh:** thread tin nhắn là đầu vào của khối `messages` trong CONTEXT policy (V9-07). Nếu ghi tin hoạt động vào thread mà không
  chặn, tin đó sẽ chui vào prompt của agent sau, tốn token và gây vòng phản hồi.

### Mục tiêu hiển thị

**A. Bấm vào node mở ngăn chi tiết** (thay vì chỉ lọc timeline), gồm bốn phần, mỗi phần đọc từ dữ liệu có thật:

1. **Đầu vào ("node này được yêu cầu làm gì").** Tên agent/command, vai trò, resource đính kèm (skill, command), scope được ghi, lời nhắc rút gọn
   (phần `InstructionArtifact` đã redact, mở rộng được), kết quả của các node trước mà nó nhận (evidence reviewer feedback, kết quả gate).
2. **Tiến trình ("đang làm gì").** Luồng từ `agent_events` theo thứ tự: tin nhắn của agent, từng công cụ gọi (tên, đối số rút gọn, thành công
   hay lỗi, thời lượng), file thay đổi theo checkpoint, cảnh báo. Khi attempt chưa kết thúc, ngăn tự cập nhật (SSE nếu dùng được, nếu không thì
   thăm dò) và đầu ngăn hiện dòng trạng thái: "đang chạy, lượt 2, 14 lần gọi công cụ, lần cuối 8 giây trước: Edit src/App.tsx".
3. **Kết quả ("xong thì ra gì").** Theo loại node:
   - Agent: outcome đã chọn, tin nhắn cuối của agent (kể cả khi FAILED), danh sách file đổi (+/-), chi phí, thời lượng.
   - Command: exit code, thời lượng, stdout/stderr đã redact, cờ bị cắt.
   - Gate: từng tiêu chí, PASS/FAIL, bằng chứng đi kèm.
   - Duyệt hoặc chờ người: ai quyết định, khi nào, ghi chú.
   - Node cấu trúc (start, fork, join, end): một dòng "đã đi qua, vì sao đi nhánh này".
4. **Lỗi ("vì sao hỏng, làm gì tiếp").** Mã lỗi kèm câu giải thích bằng ngôn ngữ thường và hành động đề nghị (ví dụ `SCOPE_VIOLATION` kèm đường
   dẫn vi phạm; lỗi môi trường kèm lệnh cần chạy; liên kết tới `Chạy lại` của V11-10). Dùng chung với phần giải thích lỗi đã nêu ở các task khác.

Trên **chính node trong đồ thị**: nhãn một dòng (ví dụ "✓ approved · 3 file · 41s", "✗ SCOPE_VIOLATION", "● đang chạy 02:10") để biết trạng thái mà
không phải bấm.

**B. Tab Evidence gắn với node.** Mỗi dòng ghi tên node, lượt (iteration), attempt; lọc theo node; artifact `command-output+json` xem trước được
(hiển thị exit code, stdout, stderr dạng văn bản); từ ngăn chi tiết của node có liên kết sang evidence tương ứng.

**C. Chat là nguồn tin hoạt động của agent.** Thread của WorkItem ngoài tin của người còn nhận tin do engine ghi, mỗi tin gắn `attemptId`
và `nodeRunId`:
- bắt đầu node: "brainstorm bắt đầu (lượt 1)";
- câu hỏi hoặc nhận định của agent khi nó dừng để hỏi người (needs-info, câu hỏi làm rõ) và tin nhắn cuối khi xong;
- kết thúc: outcome, số file đổi, chi phí; hoặc lỗi với câu giải thích như mục A4;
- cổng duyệt đang chờ: "đang chờ bạn duyệt" kèm liên kết tới nơi duyệt.
Luồng chi tiết từng công cụ **không** đổ vào Chat (quá dài); Chat có liên kết "xem tiến trình" mở ngăn chi tiết của node. Tin hoạt động hiển thị
khác tin của người (nhãn node, màu), có bộ lọc "chỉ tin của tôi / tất cả".

### Thực hiện (đề xuất)

1. **Đọc theo node, không thêm nguồn sự thật.** Thêm truy vấn `getNodeRunDetail` (`GET /node-runs/{nodeRunId}`): trả đầu vào, danh sách attempt
   với trạng thái, kết quả theo loại node, evidence và artifact tham chiếu, lỗi đã giải thích. Thêm `listAttemptEvents`
   (`GET /execution-attempts/{attemptId}/events?cursor=`): phân trang theo `Sequence`, chỉ trả các bản ghi **đã redact** trong `agent_events`,
   giới hạn kích thước mỗi bản ghi; cursor cho phép đọc tiếp khi đang chạy. Cả hai chỉ đọc; CLI: `aw node-run show <id>` và
   `aw node-run events <attempt-id> [--follow]`; đăng ký parity, sinh lại `generated.ts`.
2. **Giữ lời agent cả khi FAILED.** Mở rộng V9-17: attempt FAILED hoặc TIMED_OUT cũng để lại evidence `AGENT_OUTPUT` (tin nhắn cuối đã có, kèm
   cờ `partial`). Không đổi verdict (vẫn `RECORDED`, không thỏa CompletionPolicy).
3. **Đọc lời nhắc.** Cho đọc `InstructionArtifact` đã redact qua cùng truy vấn node, giới hạn kích thước và mặc định thu gọn. Cần khảo sát: phần
   nội dung nào của instruction file (`instruction_files.go`) nằm ngoài artifact và có hiển thị lại được không.
4. **Hiển thị khi đang chạy.** Ưu tiên nối `watchProjectEvents` (đã có, chưa ai gọi) để làm mới ngăn chi tiết khi có sự kiện; nếu journal chưa phát
   sự kiện cho `agent_events`, dùng thăm dò theo cursor `Sequence` mỗi 2–4 giây và ghi vào ADR là bước tạm. Không đẩy payload qua SSE, chỉ
   báo "có dữ liệu mới".
5. **Tin hoạt động trong Chat (C).** Engine ghi tin qua đúng lệnh `AppendMessage` (redact trước khi lưu, idempotent theo khóa
   `nodeRunId:attempt:sự kiện`), role `SYSTEM` cho thông báo của engine và `ASSISTANT` cho lời agent, `Actor` là tên agent hoặc `aw`. Ghi trong
   cùng bước chuyển trạng thái đã có (không giao dịch mới ngoài ý muốn; không bao giờ ghi bằng Git hay I/O ngoài trong transaction). **Tin hoạt động
   phải bị loại khỏi khối `messages` của CONTEXT policy theo mặc định** (thêm cờ `display-only` bất biến trên Message, hoặc loại theo role/actor trong
   bộ lắp ngữ cảnh; chọn một cách trong ADR), kèm test chứng minh prompt của agent kế tiếp không đổi so với khi chưa bật tính năng.
6. **Giao diện.** Ngăn chi tiết bên phải đồ thị (thay cho bộ lọc đơn thuần; vẫn giữ lọc timeline), nhãn một dòng trên node, Evidence có cột node,
   Chat có kiểu hiển thị cho tin hoạt động và bộ lọc. Danh sách truy cập (accessible list) có cùng nội dung và điều hướng bằng bàn phím.
   Văn bản người dùng và của agent luôn hiển thị dạng văn bản đã escape, không HTML. Nội dung dài thu gọn, tải thêm theo trang.
7. **Tài liệu.** Cập nhật `docs/operator/` (cách đọc ngăn chi tiết), hướng dẫn issue-tracker (bỏ chỉ dẫn xem thủ công trong DB/artifact), ADR cho
   `getNodeRunDetail`, `listAttemptEvents`, tin hoạt động và cờ loại khỏi ngữ cảnh.

### Quyết định đã chốt (product owner, theo đề xuất)

1. **Chat chỉ nhận mốc và tin cuối của agent** (bắt đầu node, câu hỏi, kết thúc, lỗi, chờ duyệt). Luồng từng công cụ và tin trung gian nằm ở ngăn
   chi tiết của node; Chat có liên kết "xem tiến trình". Không đổ mọi tin trung gian vào Chat.
2. **Giữ nguyên chính sách lưu `agent_events`**, chỉ thêm đường đọc. Chưa đặt thời hạn giữ; xem lại nếu đo được dung lượng đáng ngại.
3. **Xem prompt đầy đủ cùng quyền với xem evidence của WorkItem** (không thêm quyền riêng).

### Ngoài phạm vi

Sửa nội dung node trong lúc chạy; điều khiển agent từ ngăn chi tiết (tạm dừng, can thiệp giữa chừng); tìm kiếm toàn văn trong luồng sự kiện; xuất
luồng sự kiện ra file; thay đổi cách agent tạo `agent_events` (adapter giữ nguyên).

### Verify

- **Engine (test tích hợp):** `getNodeRunDetail` cho từng loại node (agent, command, gate, duyệt, cấu trúc) trả đúng đầu vào, kết quả và lỗi;
  `listAttemptEvents` phân trang đúng theo `Sequence`, không trả bản ghi chưa redact (secret giả trong event không bao giờ ra), tôn trọng giới hạn
  kích thước; attempt FAILED cũng có `AGENT_OUTPUT`.
- **Chat:** tin hoạt động được ghi đúng một lần cho mỗi sự kiện (replay không nhân đôi); **prompt của node kế tiếp giống hệt** khi bật và tắt
  tính năng; tin bị redact trước khi lưu.
- **UI:** Vitest cho ngăn chi tiết (bốn phần, từng loại node, trạng thái đang chạy/xong/lỗi), nhãn trên node, Evidence có cột node và xem trước
  `command-output+json`, Chat với tin hoạt động và bộ lọc; kiểm tra trình đọc màn hình ở danh sách truy cập. e2e Playwright trên bản cài thật:
  chạy một workflow nhiều node, bấm từng node trong lúc chạy và sau khi xong, thấy đúng tiến trình và kết quả, Chat có tin hoạt động.
- **Không hồi quy:** `docs-coverage`, parity API/CLI, `v8-alpha-gate` xanh; không thêm truy vấn nào ghi dữ liệu.

## V11-12 — Sửa và ngừng dùng repository; thông báo lỗi chi tiết khi đăng ký và thăm dò

> Hai thiếu sót đi cùng nhau: đăng ký sai là lỗi rất thường gặp, mà người dùng vừa **không biết sai gì** vừa **không có cách sửa**.

### Bằng chứng (đã gặp thật và đã đọc trong mã)

- **Ca thật:** đăng ký `D:\project\bpm-gl` (thư mục chưa `git init`). Repository vào `BLOCKED` với mã `INVALID_ARGUMENT`. Người dùng phải đoán qua
  ba lệnh (`list`, `show`, `onboarding`) mới thấy câu `repository local path is not a Git working tree`, rồi tự chạy `git` để biết thật sự thiếu
  gì.
- **Không có lệnh sửa.** Chỉ có `register`, `list`, `show`, `onboarding`, `retry-probe` và nhóm `readiness`. `retry-probe` thăm dò lại **đúng đường dẫn đã
  lưu** (`repositories.local_path`), nên đổi đường dẫn hay `defaultRef` là không thể. Mã định danh và tên (duy nhất trong mỗi project, kiểm ở
  `registerRepositoryTx`) bị giữ mãi bởi bản đăng ký sai.
- **Không có lệnh ngừng dùng.** Máy trạng thái (`legalRepositoryTransitions`, `internal/domain/project/project.go`): `BLOCKED -> PROBING` là cạnh ra duy nhất của
  `BLOCKED`; `DISABLED` chỉ đến được từ `ACTIVE` (ghi chú trong mã: "BLOCKED never DISABLED"), và hiện **không có lệnh nào** thực hiện cạnh
  `ACTIVE -> DISABLED` (`RepositoryDisabled` chỉ được nhắc ở bộ xử lý thăm dò). Hơn mười bảng tham chiếu `repositories(id)` (scope của work item,
  workspace, release set, readiness, evidence), nên xóa cứng không an toàn.
- **Lỗi chỉ hiện mã.** `repository_probe_attempts` đã lưu `errorMessage`, nhưng `repository list` và `repository show` chỉ trả `lastProbeErrorCode`; thẻ
  repository trên web (`Projects.tsx`, khối `lastProbeErrorCode`) cũng chỉ hiện mã. Chỉ `repository onboarding` có câu lý do, và không lệnh nào gợi ý
  dùng nó. `kit/scripts/init-project.sh` và `add-repository.sh` chỉ in `repository <id>: BLOCKED`.
- **Câu lý do quá chung.** `runGit` (`internal/adapters/repoprobe/prober.go`) bỏ stderr của Git; mọi lần `git rev-parse --show-toplevel` thoát khác 0 đều thành
  một câu `is not a Git working tree`, trộn bốn nguyên nhân khác hẳn nhau: chưa `git init`, "dubious ownership" (thư mục thuộc người dùng khác),
  repo bare, Git hỏng. Mỗi nguyên nhân có cách sửa khác nhau. Câu lý do cũng không nêu đường dẫn đã dùng hay lệnh sửa.
- Form `Register Repository` (`Projects.tsx`) chỉ kiểm "bắt buộc"; sai đường dẫn chỉ lộ ra sau khi đã đăng ký và đã tốn một lượt thăm dò.

### A. Thông báo lỗi chi tiết

1. **Phân loại lỗi thăm dò thành tập đóng `failureKind`** thay vì một mã chung. Mỗi giá trị có câu giải thích và gợi ý riêng:
   `PATH_EMPTY`, `PATH_NOT_FOUND`, `PATH_NOT_DIRECTORY`, `NOT_A_GIT_REPOSITORY`, `DUBIOUS_OWNERSHIP`, `BARE_REPOSITORY`, `NOT_TOP_LEVEL`
   (kèm đường dẫn thư mục gốc thật của repo cha), `NO_COMMITS`, `REF_NOT_FOUND` (kèm danh sách nhánh hiện có), `REF_INVALID`, `GIT_NOT_FOUND`,
   `PERMISSION_DENIED`, `GIT_FAILED` (còn lại), `TIMEOUT`. Mã lỗi công khai cũ (`INVALID_ARGUMENT`, `NOT_FOUND`, `UNAVAILABLE`) giữ nguyên để tương thích.
2. **Bắt stderr của Git, đã redact và có giới hạn** (ví dụ 2 KB), và chạy Git với `LC_ALL=C` (hoặc `LANGUAGE=C`) để việc phân loại theo câu chữ ổn định,
   không phụ thuộc ngôn ngữ của Windows. Stderr có thể chứa URL remote kèm thông tin xác thực, nên đi qua cùng bộ redact (`redact.Matcher`) như
   `agent_events`. Không phân loại bằng stderr thì rơi về `GIT_FAILED` kèm nguyên văn stderr đã redact, không đoán.
3. **Mỗi lỗi trả bốn phần:** `message` (một câu bằng lời thường), `path` (chuỗi đã khai và đường dẫn đã chuẩn hóa), `detail` (stderr đã redact),
   `hint` (việc cần làm, có lệnh chép dán được). Ví dụ cho ca thật: "Thư mục `D:\project\bpm-gl` chưa phải repository Git. Chạy
   `git -C D:/project/bpm-gl init -b main` rồi commit đầu tiên, sau đó bấm Thăm dò lại." Gợi ý riêng cho Windows: đường dẫn kiểu Git Bash (`/d/...`) báo
   `PATH_NOT_FOUND` kèm "thử `D:/...`"; "dubious ownership" kèm đúng lệnh `git config --global --add safe.directory <path>`.
4. **Hiển thị ở mọi nơi người dùng nhìn thấy repository:** thêm `lastProbeError {failureKind, message, hint}` vào dữ liệu của `list`, `show` và
   `onboarding` (chi tiết đầy đủ và `detail` ở `onboarding`); thẻ repository trên web hiện `message` và `hint` ngay trên thẻ, mở rộng được để xem `detail` và
   sao chép lệnh gợi ý; `kit/scripts/init-project.sh` và `add-repository.sh` in `message` và `hint` khi trạng thái là `BLOCKED`.
5. **Kiểm tra trước khi đăng ký.** Thêm truy vấn chỉ đọc `aw repository check --path <p> --ref <r>` (và `POST /projects/{id}/repositories/check`) chạy đúng bộ
   thăm dò nhưng **không ghi gì**; trả `failureKind`/`message`/`hint` hoặc kết quả đạt (base commit, có thay đổi chưa commit, thành phần phát hiện được).
   Form `Register Repository` trên web gọi nó khi rời ô đường dẫn và hiển thị kết quả ngay trong form, để lỗi dạng ca thật bị chặn trước khi đăng ký.

### B. Sửa và ngừng dùng repository

1. **Sửa (`UpdateRepository`).** `aw repository update <id> --expected-version <n>` và `PATCH /repositories/{id}` (If-Match theo `version`), cho `remoteLocator`
   và `defaultRef` (và `name` nếu rảnh; `id` bất biến). Chỉ cho khi repository ở `BLOCKED`: sửa một lần đăng ký sai. Một transaction: ghi trường mới,
   chuyển `BLOCKED -> PROBING` và xếp job thăm dò (giống `RegisterRepository`), ghi sự kiện `RepositoryUpdated` và biên nhận idempotent. Repository
   `ACTIVE` không cho sửa đường dẫn (workspace, revision và evidence đã gắn với đường dẫn cũ); muốn đổi thì ngừng dùng rồi đăng ký mới.
2. **Ngừng dùng (`DisableRepository`).** `aw repository disable <id> --expected-version <n> [--reason ...]`. Sửa máy trạng thái để cho phép cả
   `BLOCKED -> DISABLED` (sửa quyết định V3-01 "chỉ từ ACTIVE" bằng ADR mới) và thêm lệnh thực hiện `ACTIVE -> DISABLED`. Từ chối có kiểu lỗi, liệt kê rõ các thứ
   đang giữ nó: work item chưa kết thúc có repository trong scope, workspace hoặc release set còn mở.
3. **Giải phóng tên.** Ràng buộc "tên duy nhất trong project" chỉ áp dụng cho repository chưa `DISABLED` (chỉ mục duy nhất một phần), để đăng ký lại cùng tên sau khi
   ngừng dùng. `id` vẫn duy nhất mãi mãi.
4. **Không xóa cứng.** Repository `DISABLED` bị ẩn khỏi danh sách mặc định (`--include-disabled` để xem) nhưng vẫn còn cho lịch sử, evidence và kiểm toán.
5. **Giao diện.** Thẻ repository: `Sửa` (chỉ khi `BLOCKED`, dùng chung form với kiểm tra trước), `Thăm dò lại` (đã có), `Ngừng dùng` (hộp thoại liệt kê thứ đang giữ). Các nút theo
   `validActions` do core trả, như các màn hình khác.
6. **Tài liệu.** Cập nhật `docs/operator/`, hướng dẫn issue-tracker (thêm mục "đăng ký sai thì làm gì"), parity registry, sinh lại `generated.ts`, ADR cho hai lệnh mới,
   `failureKind` và việc sửa máy trạng thái.

### Quyết định cần chốt với product owner

1. **Sửa chỉ khi `BLOCKED`, hay cả `ACTIVE` chưa có tham chiếu?** Đề xuất: chỉ `BLOCKED`.
2. **Cho `BLOCKED -> DISABLED` (sửa quyết định V3-01)?** Đề xuất: có; không thì lần đăng ký sai không thoát được.
3. **Xóa cứng repository chưa từng `ACTIVE` và không được tham chiếu?** Đề xuất: không; ngừng dùng và giải phóng tên là đủ.
4. **Stderr của Git (đã redact) hiển thị cho mọi người xem project?** Đề xuất: có; đường dẫn máy là thông tin vận hành, không phải bí mật.

### Ngoài phạm vi

Tự động chạy `git init` hay `git config` thay người dùng; đổi `id`; di chuyển workspace và evidence sang đường dẫn mới; clone repository từ URL.

### Verify

- **Phân loại (test với Git thật trong thư mục tạm):** thư mục không phải repo; thư mục con của repo; repo bare; repo chưa có commit; ref không tồn tại; đường dẫn không tồn tại; đường dẫn là file. "Dubious
  ownership" và Git không có: dùng trình chạy Git giả trả stderr tương ứng. Mỗi trường hợp có đúng `failureKind`, `message`, `hint`. Stderr chứa URL có token thì `detail` đã bị redact.
  Chạy với `LC_ALL=C`: kết quả không đổi khi ngôn ngữ hệ thống khác.
- **Windows:** đường dẫn `D:\x`, `D:/x`, `/d/x`; `failureKind` và `hint` đúng cho từng kiểu. Chạy trên runner Windows nếu CI có.
- **Sửa và ngừng dùng:** `UpdateRepository` chỉ chạy khi `BLOCKED`, đổi đường dẫn rồi thăm dò lại và thành `ACTIVE`; replay cùng khóa cho cùng kết quả; sai `version` bị từ chối.
  `DisableRepository` bị từ chối khi còn work item hay workspace dùng và liệt kê đúng; thành công thì tên dùng lại được; `ACTIVE -> DISABLED` có test hồi quy.
- **Hiển thị:** `list`, `show`, `onboarding` có `lastProbeError`; Vitest cho thẻ repository (message, hint, mở rộng detail), form đăng ký có kiểm tra trước, hộp thoại `Sửa` và `Ngừng dùng`.
  e2e Playwright tái hiện ca thật: đăng ký thư mục chưa `git init`, thấy câu "chưa phải repository Git" và lệnh gợi ý, `git init` và commit, bấm `Thăm dò lại`, thành `ACTIVE`, không mở terminal để tìm lý do.
- **Kit:** `init-project.sh` và `add-repository.sh` in lý do khi `BLOCKED`; `kit/tests/check-kit.sh` xanh.
- **Không hồi quy:** mã lỗi công khai cũ (`INVALID_ARGUMENT`, `NOT_FOUND`) vẫn giữ nguyên; `go test ./...` và `docs-coverage-check` xanh.

## V11-13 — Đổi tên và lưu trữ Project

> Cùng loại thiếu sót với V11-12 nhưng ở mức Project: tạo nhầm hoặc đặt tên sai là kẹt vĩnh viễn.

### Bằng chứng (đã đọc trong mã)

- **Chỉ có ba thao tác.** CLI `aw project create|list|show` (`internal/delivery/cli/catalog/project.go`) và HTTP `projectsCreate|projectsList|projectsGet`
  (`internal/delivery/httpapi/catalog/catalog.go`). Không có đổi tên, xóa hay lưu trữ; trên web cũng không có.
- **Trạng thái đã có nhưng không ai đặt.** `ProjectArchived` có trong domain (`ProjectActive|ProjectArchived`) và trong ràng buộc CHECK của bảng `projects`, nhưng không lệnh
  nào chuyển sang `ARCHIVED`, và `ListProjects` không lọc theo trạng thái.
- **Xóa cứng không an toàn.** 25 migration có khóa ngoại tới `projects(id)` (repository, work item, family, run, evidence, release set, definition gán, nhật ký sự kiện, biên nhận...).
  Xóa dòng `projects` hoặc làm hỏng lịch sử kiểm toán, hoặc bị khóa ngoại từ chối.
- **Tên không duy nhất** (cột `projects.name` không có UNIQUE), nên hai project cùng tên là hợp lệ và chỉ phân biệt bằng id; đổi tên không đụng ràng buộc nào.
- **Cạm bẫy của kit:** `kit/scripts/init-project.sh` tạo project với khóa idempotency là `sha256(tên)`. Chạy lại cùng tên **trả đúng project cũ** (biên nhận), kể cả khi người dùng muốn một project mới
  sau khi lưu trữ. Cần xử lý khi thêm lưu trữ.

### Thiết kế

1. **Đổi tên (`RenameProject`).** `aw project rename <id> --expected-version <n> --name <tên>` và `PATCH /projects/{id}` (If-Match theo `version`). Một transaction: ghi tên mới, tăng `version`,
   ghi sự kiện `ProjectRenamed` (tên cũ, tên mới, người thực hiện) và biên nhận idempotent. Kiểm tên: khác rỗng, tối đa 200 ký tự, cắt khoảng trắng hai đầu; id không đổi.
   Cho phép khi Project `ACTIVE`; Project `ARCHIVED` phải khôi phục trước khi đổi tên.
2. **Lưu trữ (`ArchiveProject`) thay cho xóa.** `aw project archive <id> --expected-version <n> [--reason ...]` và `restore`. Chuyển `ACTIVE <-> ARCHIVED`. Từ chối có kiểu lỗi và liệt kê thứ
   đang chạy: WorkflowRun chưa kết thúc, work item `ACTIVE`/`BLOCKED`, job nền đang giữ lease, release set còn mở. Người dùng xử lý (hủy, hoàn tất) rồi lưu trữ lại. Project `ARCHIVED` bị ẩn khỏi danh sách
   mặc định (`--include-archived` để xem), **chỉ đọc**: mọi lệnh ghi vào Project hoặc repository, work item của nó bị từ chối với lỗi nói rõ "project đã lưu trữ" cùng cách khôi phục;
   worker không nhận job mới của nó; evidence và lịch sử vẫn xem được.
3. **Không có xóa cứng** trong V11 (xem quyết định 2). Hộp thoại ghi rõ "lưu trữ, không xóa dữ liệu".
4. **Kit.** `init-project.sh` khi khóa idempotency trỏ về một project `ARCHIVED` thì in lý do và gợi ý `restore` hoặc dùng tên khác (hoặc thêm hậu tố vào khóa), thay vì im lặng dùng lại.
5. **Giao diện.** Trang Projects: nút `Đổi tên` (hộp thoại một ô, kiểm tra tại chỗ) và `Lưu trữ` (hộp thoại liệt kê thứ đang chặn, ghi lý do), bộ lọc "hiện cả project đã lưu trữ" với nút `Khôi phục`.
   Các nút theo `validActions` do core trả. Thanh đầu trang của project đã lưu trữ ghi rõ trạng thái chỉ đọc.
6. **Tài liệu.** Cập nhật `docs/operator/`, parity registry, sinh lại `generated.ts`, ADR cho ba lệnh mới và quy tắc "project lưu trữ là chỉ đọc".

### Quyết định cần chốt với product owner

1. **Lưu trữ chặn khi còn thứ đang chạy (đề xuất), hay tự hủy chúng?** Đề xuất: chặn và liệt kê; không tự hủy.
2. **Có xóa cứng Project rỗng** (không repository, không work item) không? Đề xuất: không trong V11; lưu trữ là đủ, xóa cứng để sau nếu cần.
3. **Project lưu trữ chỉ đọc hoàn toàn**, hay cho tiếp tục xem evidence và xuất dữ liệu? Đề xuất: xem được hết, ghi thì chặn.

### Ngoài phạm vi

Gộp hai project; di chuyển repository sang project khác; xuất hoặc nhập project; quyền riêng theo project.

### Verify

- **Engine:** đổi tên cập nhật tên, `version`, sự kiện và biên nhận; replay cùng khóa cho cùng kết quả; sai `version` bị từ chối; tên rỗng hoặc quá dài bị từ chối. Lưu trữ bị từ chối khi còn run, work item hay job mở và liệt kê đúng;
  khi thành công, mọi lệnh ghi vào project đó bị từ chối với lỗi đúng, worker không claim job của nó, các truy vấn đọc vẫn chạy; `restore` đưa mọi thứ về như cũ.
- **Không hồi quy:** `project create`, `list`, `show` giữ hành vi cũ với project `ACTIVE`; `go test ./...`, `docs-coverage-check` xanh.
- **UI:** Vitest cho hai hộp thoại, bộ lọc, nút theo `validActions`; e2e Playwright: tạo project, đổi tên, lưu trữ (bị chặn khi còn work item, rồi được khi đã dọn), khôi phục, không mở terminal.
- **Kit:** `init-project.sh` với tên của project đã lưu trữ in đúng gợi ý; `kit/tests/check-kit.sh` xanh.

## Gate của V11

Kế thừa gate chung ở mục 6 của `00-roadmap.md`, thêm:

1. Mỗi task có test hồi quy thất bại trước khi sửa, xanh sau khi sửa; commit tách riêng test và sửa, hoặc ghi rõ trong message.
2. `go test ./...` xanh; `go run ./cmd/docs-coverage-check` nợ 0; CI hiện có (gồm `v8-alpha-gate`) xanh.
3. Mỗi thay đổi hành vi công khai có ADR và cập nhật `docs/operator/`.
4. `kit/tests/check-kit.sh` với `WALK_ALL=1` xanh sau khi core đổi (kịch bản chạy trên engine thật).
5. Hai script tái hiện của V11-01 cho kết quả đúng thiết kế.
6. V11-06: chụp màn hình tab Diff của một cổng duyệt thật hiện nội dung chưa commit; test chứng minh index và HEAD không đổi sau khi đọc.
7. V11-07: một lần duyệt trên web, không mở terminal, tạo commit chỉ chứa đúng các file đã chọn; file còn lại đúng theo xử lý đã chọn; `changeSetDigest` lệch thì không commit.
8. V11-08: `grep -P '[^\x00-\x7F]' kit/commands/*.sh` rỗng; bench A/B có báo cáo.
9. V11-09: script kiểm tra chạy đúng trên Windows, agent nhận đúng lỗi thật.
10. V11-10: từ một run hỏng, `Chạy lại` và `Làm lại bằng phiên bản mới` hoàn tất trên web, không để trạng thái dở khi lỗi giữa chừng.
11. V11-11: bấm một node (đang chạy, đã xong, đã hỏng) hiện đủ đầu vào, tiến trình, kết quả, lỗi từ dữ liệu thật; Chat có tin hoạt động của agent; prompt của agent kế tiếp không đổi khi bật tính năng.
12. V11-12: đăng ký thư mục chưa phải repository Git cho thông báo nói đúng nguyên nhân kèm lệnh sửa trên web, CLI và script kit; repository `BLOCKED` sửa được và ngừng dùng được mà không xóa cứng.
13. V11-13: đổi tên project, lưu trữ và khôi phục project hoàn tất trên web; lưu trữ bị chặn có lý do khi còn việc đang chạy; project lưu trữ chỉ đọc.
