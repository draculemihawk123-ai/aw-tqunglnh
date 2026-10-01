# Xử lý sự cố

> Các thông báo lỗi trong tiêu đề và khối code được giữ nguyên văn tiếng Anh (đúng như binary in ra) để bạn có
> thể tìm kiếm trực tiếp.

## Bắt đầu từ đây: `aw doctor`

```bash
aw doctor          # dạng cho người đọc
aw doctor --json   # dạng máy đọc được
```

Output thật từ một bản cài khỏe mạnh (đã kiểm chứng trong [01-quickstart.md](01-quickstart.md)):

```
status: HEALTHY
restartRequired: false
checks:
  - process_liveness [LIVENESS] HEALTHY: process is running and able to respond
  - app_config [READINESS] HEALTHY: startup configuration is valid
  - database [READINESS] HEALTHY: database is reachable
  - artifact_root [READINESS] HEALTHY: <path> exists and is writable
  - git [READINESS] HEALTHY: git version <version>
  - safe_settings [READINESS] HEALTHY: persisted safe settings desired document decodes cleanly
  - isolation_enforcement [CAPABILITY] HEALTHY: OPERATOR_TRUSTED_LOCAL isolation is enforceable; ...
```

`status` chỉ là `HEALTHY` khi mọi check `LIVENESS`/`READINESS` đều khỏe (một check `CAPABILITY` như
`isolation_enforcement` mô tả cái gì KHẢ DỤNG, không phải một yêu cầu cứng để được coi là khỏe). `restartRequired:
true` nghĩa là một lệnh `aw settings update` đã thay đổi cấu hình desired kể từ khi tiến trình này khởi động —
restart để áp dụng (xem [02-configuration.md](02-configuration.md)).

## `database schema version N is newer than this binary supports`

```
aw: open database: database schema version 42 is newer than this binary supports (highest migration it knows: 41; ...) — the database was migrated by a newer release and this binary has NOT modified it; ...
```

Bạn đã khởi động một `aw` (hoặc `aw-maintenance`) cũ hơn trên một database mà một bản phát hành mới hơn đã migrate.
Không có gì bị thay đổi. Hãy chạy binary mới hơn, hoặc khôi phục bản backup được tạo trước lần nâng cấp và dùng
binary cũ trên bản đó — xem [10-upgrade-and-rollback.md](10-upgrade-and-rollback.md). `aw version --json` cho biết
một binary yêu cầu schema nào (`schemaVersion`).

## Repository kẹt ở `BLOCKED`

```bash
aw repository list <projectId>
# "status": "BLOCKED", "lastProbeErrorCode": "NOT_FOUND"
```

Đường dẫn `remoteLocator` bạn đã đăng ký không resolve được từ góc nhìn của tiến trình `aw worker` — thường là một
đường dẫn tương đối, hoặc một đường dẫn đặc thù của shell (một đường dẫn kiểu MSYS/Git-Bash `/tmp/...` trên
Windows KHÔNG phải là cùng đường dẫn mà tiến trình Go native nhìn thấy). Đăng ký một repository ID MỚI với đường
dẫn TUYỆT ĐỐI, theo đúng định dạng của OS — không có lệnh "edit repository", nên sửa lỗi này luôn có nghĩa là đăng
ký lại với một ID mới.

## Gate/command "could not be spawned" / "not a valid Win32 application" / permission denied

Script mà Command document của bạn tham chiếu không khớp với OS mà tiến trình worker thực sự chạy trên đó — một
script `.sh` có dòng shebang không có interpreter trên Windows native; một script thiếu bit thực thi sẽ fail trên
Linux/macOS. Sửa bằng cách publish một version skill MỚI với script đúng OS, rồi một version command mới trỏ tới
nó, rồi version gate/workflow mới trỏ tiếp qua chuỗi đó — Definition là bất biến theo từng version, nên không bao
giờ có chuyện "sửa rồi thử lại" với một document đã publish; luôn publish tiến lên. Xem walkthrough thật trong
[01-quickstart.md](01-quickstart.md) về việc gặp và sửa đúng lỗi này.

## Work item không đạt READY

```bash
aw work-item readiness --project-id <id> <workItemId>
# "ready": false, "problems": ["behavior is required", "verification spec is required", ...]
```

WorkItem không có readiness contract thật. Hãy cung cấp nó lúc tạo (`"contract": {"schemaVersion": 1,
"behavior": "...", "verificationSpec": "...", "riskLevel": "LOW"|"MEDIUM"|"HIGH",
"acceptanceCriteria": [{"description": "...", "verificationRef": "..."}]}`) — không có lệnh "set contract"
riêng; contract của một WorkItem được cung cấp một lần, lúc tạo, trong CÙNG request body với
`title`/`effectiveScope`.

## `run start` fail với "work item is not READY"

Gọi `aw work-item mark-ready --expected-version <n> <workItemId>` trước — `readiness: true` từ `aw work-item
readiness` chỉ có nghĩa là contract của chính WorkItem đã đủ đầy để có thể chuyển sang ready; nó không tự chuyển
trạng thái. `--expected-version` phải khớp với version hiện tại thật của WorkItem (lấy từ `work-item show`/response
của mutation trước đó) — một version cũ là một `CONFLICT` thật.

## `RESYNC_REQUIRED` trên một list có phân trang

Một cursor của trang trước đã bị phát lại với một filter/sort KHÁC với filter/sort mà nó được tạo ra (hoặc
generation dữ liệu bên dưới đã thay đổi). Đây là một tín hiệu có chủ đích, có kiểu — không bao giờ là một trang sai
âm thầm — hãy bắt đầu lại từ request không filter/trang đầu tiên.

## "high-impact command requires confirmation" / "flag provided but not defined: -yes"

- Nếu bạn nhận thông báo THỨ NHẤT: thêm `--yes` (bạn đang chạy không tương tác — stdin là pipe hoặc có `--json`).
- Nếu bạn nhận thông báo THỨ HAI: bạn đã thêm `--yes` vào một lệnh hoàn toàn không cần xác nhận — hãy bỏ nó đi.
  Xem mục quy ước `--yes` trong [03-cli-reference.md](03-cli-reference.md).

## `flag provided but not defined` cho một flag mà bạn biết là có tồn tại

Flag phải đứng TRƯỚC tham số vị trí, không bao giờ sau (`aw definition publish --kind POLICY --yes my-id`, không
phải `aw definition publish my-id --kind POLICY --yes`) — xem mục "tham số vị trí đứng sau flag" trong
[03-cli-reference.md](03-cli-reference.md).

## Không có gì xảy ra sau `repository register` / sau một mutation lẽ ra phải kích hoạt công việc bất đồng bộ

Xác nhận `aw worker` thực sự đang chạy với CÙNG `--db`/`--artifact-root`/`--workspace-root` như `aw serve` —
riêng `aw serve` không bao giờ probe repository, thực thi node workflow hay cập nhật projection; nó chỉ phục vụ bề
mặt HTTP/CLI và tiếp nhận lệnh. Xem bước 2 của [01-quickstart.md](01-quickstart.md).

## Xem tiếp ở đâu

- Flag/dạng body chính xác của một lệnh cụ thể: `aw <resource> <action> -h`, hoặc
  [03-cli-reference.md](03-cli-reference.md).
- Vì sao một run đạt tới một trạng thái terminal nhất định: `aw run diagnostics --project-id <id> <runId>` (blocker,
  attempt mồ côi, trạng thái repository workspace) và `aw evidence list --project-id <id> <workItemId>` (verdict +
  artifact thật cho mọi node đã chạy).
- Mọi thứ mà database/artifact của bản cài này thực sự chứa, độc lập với bất kỳ run nào đang chạy: `aw work-item
  show`, `aw run show`, `aw project list`, `aw repository list` — tất cả đều là query thật, trực tiếp, không bao
  giờ là bản tóm tắt cache hay lỗi thời.
