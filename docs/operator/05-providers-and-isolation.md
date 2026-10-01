# Provider và isolation

## Đăng ký một provider

`aw serve` và `aw worker` đều nhận độc lập `--claude-executable <path>` / `--codex-executable <path>` — hai loại
provider duy nhất mà bản build này biết (trường `supportedProviders` trong `aw version --json`, V8-08). Bỏ
một flag nghĩa là: mọi node `AGENT` được pin vào provider đó sẽ fail closed (503 ở bước admission re-check của
`RetryBlockedActivation` đối với `aw serve`; worker đơn giản là không thể thực thi một node được pin vào một
provider chưa đăng ký). Không có lệnh runtime "đăng ký provider" nào — đó là một flag lúc khởi động tiến trình
trên CẢ `serve` lẫn `worker` (hai bên phải khớp nhau, vì tiến trình nào cũng có thể cần suy luận về một provider
cụ thể).

```bash
aw serve  ... --claude-executable /path/to/claude
aw worker ... --claude-executable /path/to/claude
```

`aw adapter register`/`aw adapter probe`/`aw adapter list`/`aw adapter show` quản lý kho `AdapterBuild` riêng,
theo từng bản cài — một capability manifest thật cho một bộ (provider, executable, version) cụ thể, được kiểm
tra lúc bắt đầu run so với những gì một `AGENT_PROFILE`/`WORKFLOW` đã pin. Xem `aw adapter -h` và `-h` của từng
subcommand để có flag chính xác; đây là một khái niệm thực sự khác với các flag tiến trình
`--claude-executable`/`--codex-executable` ở trên (các flag đó đăng ký provider NÓI CHUNG; adapter build là
snapshot capability cụ thể, có version, mà một workflow pin vào).

> **Ghi chú khi dịch (kiểm chứng lại 2026-10-01 với provider giả lập `cmd/fake-claude`):**
> - Mỗi lần admit một node AGENT, worker đo lại adapter build và so với build đã pin; lệch là blocker
>   `ADAPTER_BUILD_DRIFT` (run đứng ở `RUNNING`, WorkItem `BLOCKED`). Worker dùng **`os` và `goVersion` của chính
>   binary `aw`** (`aw version --json`), SHA-256 của file executable, `protocolVersion` (`claude-stream-json/v1` cho
>   Claude) và capability manifest. Vì vậy `aw adapter probe --toolchain` phải là `goVersion` của `aw`, không phải
>   phiên bản của Claude CLI; và `aw adapter register` cần `--yes`.
> - Tiến trình agent được spawn với **environment rỗng**; `aw worker --env-allowlist` chỉ áp dụng cho command, không
>   áp dụng cho agent. Claude CLI thật cần `HOME`/`PATH`, nên hãy trỏ `--claude-executable` tới một wrapper tự đặt các
>   biến này. Ví dụ đầy đủ: [hướng dẫn todolist, mục 1.4 và 3.3](../guides/todolist-spring-react/README.md#14-wrapper-cho-claude-cli-bắt-buộc).

## Các isolation tier

Hai giá trị (`internal/domain/policy/policy.go`):

- **`OPERATOR_TRUSTED_LOCAL`** — tier duy nhất thực sự dùng được trong Alpha. Một node chạy như một tiến trình OS
  cục bộ thật, không có sandbox bổ sung nào ngoài cơ chế kiểm soát ở mức tiến trình của chính repo này
  (allowlist argv/env, giới hạn output, khai báo quyền truy cập mạng trên Command document). Đây là tier mà ví dụ
  permission policy thật trong [01-quickstart.md](01-quickstart.md) sử dụng.
- **`ENFORCED_ISOLATED`** — một sandbox cấp OS thật (container, VM, hoặc tương tự). **Không có trong Alpha** —
  check `isolation_enforcement` của `aw doctor` báo điều này một cách tường minh: *"ENFORCED_ISOLATED is not
  available in this environment (no real OS-level sandbox yet) and any node pinned to it fails closed rather
  than silently downgrading."* Một permission policy pin `ENFORCED_ISOLATED` vẫn publish thành công (nó là một
  document hợp lệ) nhưng bất kỳ run nào chạm tới một node yêu cầu tier này sẽ fail closed ở bước admission —
  đây là một lựa chọn thiết kế có chủ đích (không bao giờ âm thầm chạy một node cần isolation mà không có
  sandbox), không phải một tính năng còn thiếu mà bạn cần tìm cách lách.

Kiểm tra tier nào thực sự khả dụng trên máy của bạn bằng `aw doctor` — output thật mà tài liệu này đã được kiểm
chứng:

```
- isolation_enforcement [CAPABILITY] HEALTHY: OPERATOR_TRUSTED_LOCAL isolation is enforceable;
  ENFORCED_ISOLATED is not available in this environment (no real OS-level sandbox yet) and any
  node pinned to it fails closed rather than silently downgrading
```

## Granted capabilities

Danh sách `grantedCapabilities` của một policy `PERMISSION` đặt tên các chuỗi capability cụ thể mà một node có
thể cần ngoài isolation cơ bản — ví dụ `INTEGRATION_MULTI_REPOSITORY_WRITE` (một node chạm vào write scope của
nhiều hơn một repository trong cùng một attempt). Bỏ hẳn trường này với một node không cần capability nào ngoài
repository scope đã khai báo của nó (đã kiểm chứng: permission-policy document trong
[01-quickstart.md](01-quickstart.md) bỏ trường này và run vẫn thành công, vì ví dụ MACHINE_GATE không hề chạm
vào repository nào).

## Env allowlist

Mặc định, một tiến trình provider hoặc command được spawn KHÔNG kế thừa gì từ environment của tiến trình worker.
`aw worker --env-allowlist NAME1,NAME2` là cách duy nhất để cho phép những biến có tên cụ thể đi qua — xem
[02-configuration.md](02-configuration.md).
