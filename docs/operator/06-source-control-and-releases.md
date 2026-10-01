# Quản lý mã nguồn và release

## Git chỉ-cục-bộ — ràng buộc cốt lõi

Mọi repository mà `aw` chạm vào là một Git repository cục bộ thật, nằm ở đường dẫn filesystem tuyệt đối mà bạn
đã đăng ký (`remoteLocator` — xem [01-quickstart.md](01-quickstart.md)). `aw` không bao giờ push, fetch hay
clone từ remote — `internal/adapters/gitworktree.Provider` (Git adapter thật DUY NHẤT trong codebase này) không
hề gọi bất kỳ thao tác nào trong số đó ở đâu trong phần cài đặt của nó. Một "release" trong Agent Kit là một
commit cục bộ thật nằm trong chính lịch sử repository của bạn — không hơn, không có gì đi qua mạng.

## Vòng đời ReleaseSet

```bash
echo '{"repositories":[{"repositoryId":"<repoId>","baseVcsObjectId":"<commit>","resultVcsObjectId":"<commit>","verdict":"PASS"}]}' \
  | aw release-set create --project-id <id> --family-id <taskFamilyId> --idempotency-key rs-1
aw release-set seal --expected-version 1 --idempotency-key seal-1 --yes <releaseSetId>
```

Một ReleaseSet gom một hoặc nhiều commit cục bộ thật mà một task family tạo ra trước khi chúng được coi là
"xong". Sau khi đã tạo (và seal):

```bash
aw release-set local-commit --project-id <id> --release-set-id <releaseSetId> \
  --repository-workspace-id <repositoryWorkspaceId> --expected-release-set-version 2 --expected-workspace-version 1 \
  --author-name you --author-email you@example.com --message "my commit" \
  --idempotency-key commit-1 --yes --wait --wait-timeout 30s
```

> **Ghi chú khi dịch (kiểm chứng lại trên binary build từ commit `7d0fb4c`, 2026-10-01):** hai ví dụ ở trên đã được
> sửa so với bản gốc tiếng Anh cho khớp binary hiện tại:
> - `release-set create` nhận family qua flag `--family-id` và body bắt buộc có `repositories` (mỗi mục:
>   `repositoryId`, `baseVcsObjectId`, `resultVcsObjectId`, `verdict`); body `{"familyId": ...}` bị từ chối với
>   `repositories: at least one entry is required`.
> - `release-set local-commit` nhận mọi tham số qua **flag** (không đọc body JSON) và cần `--yes`. Luồng đã chạy thật
>   là `create` → `seal` → `local-commit` (giống acceptance test `internal/integration/v6accept/stage_release_test.go`);
>   `repositoryWorkspaceId`, version và `currentRevision` lấy từ `aw workspace-set show --project-id <id> <familyId>`.
> - `release-set abandon` chỉ dùng được khi ReleaseSet còn mở; ReleaseSet đã seal báo
>   `release set is not open (already sealed or abandoned)`.
> - Local commit trên worktree không có thay đổi không kết thúc bằng lỗi có kiểu: job retry tới `DEAD` và local commit
>   kẹt ở `REQUESTED`, nên `--wait` hết hạn. Hãy kiểm tra `git status` của worktree trước khi commit.
>
> Ví dụ đầy đủ, có script: [hướng dẫn todolist](../guides/todolist-spring-react/README.md#55-commit--sau-mỗi-task-trước-task-tiếp-theo).

Đây là một lệnh `git commit` THẬT trên repository workspace thật, được rào bởi CẢ version optimistic-concurrency
của ReleaseSet LẪN version của RepositoryWorkspace — hai worker tranh nhau commit vào cùng một workspace thì bên
thua nhận một `CONFLICT` thật, có kiểu, không bao giờ là một lịch sử bị hỏng âm thầm. `--wait` block cho tới
khi job commit đạt trạng thái terminal.

```bash
aw release-set seal --expected-version <n> --idempotency-key seal-1 --yes <releaseSetId>   # chốt danh sách repository của ReleaseSet
aw release-set abandon --expected-version <n> --idempotency-key abandon-1 --yes <releaseSetId>  # bỏ đi, không bao giờ được áp dụng (chỉ khi còn mở)
aw release-set show <releaseSetId>
aw release-set list --project-id <id>
```

## Vòng đời repository workspace

Một `RepositoryWorkspace` là Git worktree thật đã được cấp phát mà `WorkspaceSet` của một WorkItem giữ cho một
repository — được tạo tự động khi `initialScope` của một WorkItem gốc nêu tên repository đó (xem trường
`provisionedRepositories` trong response ở [01-quickstart.md](01-quickstart.md)). Hai lệnh quản lý trực tiếp
vòng đời của nó:

```bash
aw repository-workspace reconcile --project-id <id> --expected-version <n> --idempotency-key r-1 <id>
aw workspace-set release --project-id <id> --expected-version <n> --idempotency-key rel-1 <workspaceSetId>
```

## Source/diff/log chỉ-đọc — không bao giờ là terminal tương tác

Ba lệnh cung cấp các view chỉ-đọc, có phân trang, trên Git object store của một repository workspace thật — đây
là TOÀN BỘ bề mặt "duyệt code"; cố ý không có shell/terminal nào vào một workspace đang chạy:

```bash
# Đọc nội dung thật của một file tại một revision chính xác
aw repository-workspace source --project-id <id> --repository-id <repoId> --workspace-set-id <wsId> \
  --revision <commitId> --revision-generation <gen> --path path/to/file.go --output -

# Một unified diff thật giữa hai revision
aw repository-workspace diff --project-id <id> --repository-id <repoId> --workspace-set-id <wsId> \
  --base-revision <commitA> --base-revision-generation <genA> \
  --result-revision <commitB> --result-revision-generation <genB>

# Lịch sử commit có phân trang, neo tại một commit thật
aw repository-workspace log --project-id <id> --repository-id <repoId> --workspace-set-id <wsId> \
  --anchor <commitId> --anchor-generation <gen> [--cursor <commitId>] [--limit <n>]
```

Cả ba lệnh đều nhận `--byte-limit`/`--file-limit`/`--line-limit` (dùng mặc định của server nếu bỏ trống) — một
diff hoặc file thực sự rất lớn sẽ bị cắt ngắn, không bao giờ âm thầm nạp toàn bộ vào bộ nhớ hay vào terminal
của bạn. `--workspace-set-id`/`--repository-id`/một revision + số generation của nó đều bắt buộc — một revision
luôn được gọi tên tương đối với generation workspace cụ thể mà nó thuộc về, vì trong suốt vòng đời của mình một
workspace có thể được cấp phát, giải phóng, rồi cấp phát lại (một generation mới).
