# Tham chiếu cấu hình

> Các khối `aw serve -h`/`aw worker -h`/global options bên dưới là output thật của binary nên được giữ nguyên
> văn tiếng Anh; phần giải thích xung quanh đã được dịch.

## Thứ tự ưu tiên cấu hình (theo `docs/design/01-system-design.md` §12, nguồn có thẩm quyền)

- Khi khởi động, `DatabasePath` được resolve theo thứ tự `safe defaults < config file < environment < CLI
  flags`, sau đó mới mở/migrate SQLite.
- Với mọi trường nằm trong **allowlist safe-settings** (bên dưới), lúc khởi động sẽ merge theo thứ tự `safe
  defaults < config file < SQLite desired settings < environment < CLI flags`. Một giá trị override từ
  environment/flag che mất một desired setting đã lưu trong SQLite sẽ được báo qua `maskedByStartupSource`
  trong `GET /settings/safe` / `aw settings show`.
- Cấu hình hiệu lực là bất biến trong suốt vòng đời của một tiến trình. `aw settings update` (`PUT
  /settings/safe`) chỉ thay đổi version *desired* và báo `restartRequired: true` — phải restart thì thay đổi
  mới thực sự được áp dụng.
- **Không bao giờ thay đổi được qua safe-settings**: `LocalPrincipal`, session/signing key, `DatabasePath`,
  `WorkerID`, và mọi giá trị secret thô. Những thứ này chỉ được đặt lúc khởi động tiến trình (CLI flag /
  config file / environment), theo thiết kế — xem [05-providers-and-isolation.md](05-providers-and-isolation.md)
  để biết vì sao riêng principal không bao giờ là một giá trị có thể đổi lúc runtime.
- Bề mặt safe-settings bao gồm: đường dẫn SQLite, thư mục gốc artifact/workspace, concurrency của worker,
  lease TTL/heartbeat, executable/argv/model của provider, giới hạn tiến trình/output, retention, và log level.

```bash
aw settings show                                    # cấu hình hiệu lực + desired hiện tại, và mọi chỗ bị che (masking)
echo '{...}' | aw settings update --expected-version 1  # đổi cấu hình desired; restart để áp dụng
```

## `aw serve` — mọi flag thật

```
$ aw serve -h
Usage of serve:
  -artifact-root string
        artifact storage root directory
  -claude-executable string
        path to the Claude CLI executable to register as a live agent provider for RetryBlockedActivation's own admission re-checks (omitted = provider not registered, RetryBlockedActivation fails closed with 503 for a build pinned to it)
  -codex-executable string
        path to the Codex CLI executable to register as a live agent provider for RetryBlockedActivation's own admission re-checks (omitted = provider not registered, RetryBlockedActivation fails closed with 503 for a build pinned to it)
  -db string
        sqlite database path
  -host string
        loopback bind host (default "127.0.0.1")
  -max-body-bytes int
        maximum accepted request body size in bytes (default 1048576)
  -port int
        bind port (0 = OS-assigned ephemeral port)
  -principal-config string
        path to a trusted JSON config file's localPrincipal.actor/localPrincipal.roles (ADR-028); omitted or missing means the local-operator/[operator] default — this is the only allowed way to select a principal, there is no --actor/--role flag
  -ui-dist pnpm build
        directory containing a built V7 UI (pnpm build output of web/, i.e. web/dist) to serve at / and /assets/; omitted = no UI, aw serve still works exactly as before V7
  -worker-id aw worker
        identity string recorded in this process' own config.Config for GET /doctor's config-validity check; this process does not itself run the lease/reaper worker pool (run aw worker for that; it has its own --worker-id) (default "aw-serve")
  -workspace-root string
        root directory for real Git worktree-backed workspace storage (internal/adapters/gitworktree.Provider) that the source/diff/repository-log inspection routes read through
```

`--db`/`--artifact-root`/`--workspace-root` là bắt buộc (thiếu = lỗi khởi động thật, không có giá trị mặc
định âm thầm). `--ui-dist` thực sự là tùy chọn — từ V8-08, một binary phát hành được build bằng
`cmd/aw-release-build` tự phục vụ UI nhúng sẵn của nó ngay cả khi bỏ `--ui-dist`; một lệnh `go build ./cmd/aw`
thông thường (không nhúng UI) sẽ quay về trạng thái "không có UI" giống hệt trước V8-08.

`--host`/`--port` chỉ bind vào loopback — bind ra ngoài bị từ chối ngay lúc khởi động, chứ không chỉ là một
khuyến nghị cấu hình (đúng như câu "external bind bị từ chối" trong `docs/design/01-system-design.md`).

## `aw worker` — mọi flag thật

```
$ aw worker -h
Usage of worker:
  -artifact-root aw serve
        artifact storage root directory (must already exist; the same root aw serve uses)
  -claude-executable string
        path to the Claude CLI executable to register as an agent provider (omitted = not registered; AGENT nodes pinned to it cannot run)
  -codex-executable string
        path to the Codex CLI executable to register as an agent provider (omitted = not registered; AGENT nodes pinned to it cannot run)
  -completion-interval duration
        how often the completion orchestrator looks for runs waiting in VERIFYING (default 1s)
  -db aw serve
        sqlite database path (the same file aw serve uses)
  -env-allowlist string
        comma-separated names of parent environment variables a spawned provider/command process may inherit (default: none)
  -lease-heartbeat duration
        how often an in-flight job's lease is renewed; must be shorter than --lease-ttl (default 10s)
  -lease-ttl duration
        how long a claimed job's lease stays valid without a heartbeat (default 30s)
  -local-commit-write-lease-ttl duration
        how long a release-set local-commit write lease is held without renewal (default 2m0s)
  -poll-interval duration
        how long an idle worker waits before polling for a job again (default 200ms)
  -projection-interval duration
        how often the live projection consumer scans the event journal (default 500ms)
  -projection-rebuild-batch-size int
        how many journal rows one projection rebuild BUILDING/CUTTING_OVER round scans (0 keeps the package default, 500)
  -reaper-interval duration
        pause between two recovery-reaper passes (orphaned attempts, stranded cancellation intents) (default 5s)
  -shutdown-grace duration
        how long in-flight jobs may finish after a shutdown signal before their context is cancelled (default 30s)
  -sweep-interval duration
        pause between two artifact retention sweeps (default 1h0m0s)
  -worker-concurrency int
        maximum jobs run at once (default 4)
  -worker-id string
        lease-owner identity for this process; must be unique among running workers (default "aw-worker-<pid>")
  -workspace-root aw serve
        root directory for Git worktree-backed workspaces (the same root aw serve uses)
```

`--db`/`--artifact-root`/`--workspace-root` PHẢI trỏ tới đúng ba vị trí mà tiến trình `aw serve` của bản cài
này đang dùng — `aw worker` không bao giờ tạo database riêng. Chạy nhiều hơn một tiến trình `aw worker` trên
cùng một `--db` được hỗ trợ (mỗi tiến trình cần `--worker-id` riêng) — cơ chế fencing bằng lease thật
(`--lease-ttl`/`--lease-heartbeat`) là thứ giữ cho hai worker không xử lý trùng cùng một job.

Env allowlist (`--env-allowlist`) mặc định rỗng — một tiến trình provider/command được spawn KHÔNG kế thừa gì
từ environment của worker, trừ khi tên biến được liệt kê tường minh ở đây.

## Global options (mọi lệnh `aw <resource> <action>`)

```
Global options (any resource command; env AW_DB, AW_ARTIFACT_ROOT, ...):
  --db <path>                 sqlite database of the installation
  --artifact-root <dir>       artifact storage root
  --workspace-root <dir>      Git worktree storage root (workspace/source commands)
  --claude-executable <path>  register the Claude CLI as a live provider
  --codex-executable <path>   register the Codex CLI as a live provider
```

Mỗi option có một biến môi trường dự phòng (`AW_DB`, `AW_ARTIFACT_ROOT`, `AW_WORKSPACE_ROOT`,
`AW_CLAUDE_EXECUTABLE`, `AW_CODEX_EXECUTABLE`) để một session shell chạy lâu không cần lặp lại chúng ở mỗi lần
gọi:

```bash
export AW_DB=./aw-install/aw.db AW_ARTIFACT_ROOT=./aw-install/artifacts AW_WORKSPACE_ROOT=./aw-install/workspaces
aw project list   # giờ không cần --db/--artifact-root/--workspace-root nữa
```

CLI flag luôn thắng biến môi trường cùng tên.

## `--principal-config`

Đây là cách DUY NHẤT để chọn local principal (actor + roles) mà một tiến trình chạy dưới danh nghĩa — cố ý
không có flag `--actor`/`--role` ở bất kỳ đâu (ADR-028: "không có per-command impersonation flag"). Bỏ trống
hoặc file không tồn tại nghĩa là dùng mặc định `local-operator`/`[operator]`. Flag này trỏ tới một file JSON
tin cậy có `localPrincipal.actor`/`localPrincipal.roles`.
