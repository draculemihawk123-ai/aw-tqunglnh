# Tham chiếu CLI

## Vì sao trang này không liệt kê từng flag bằng tay

`aw help` và `aw <resource> <action> -h` tự chúng là nguồn có thẩm quyền, luôn đồng bộ, cho biết chính xác một
lệnh nhận những flag nào — chúng được sinh từ chính các định nghĩa flag mà lệnh thực sự parse, nên không bao
giờ lệch khỏi hành vi thật như một bảng tĩnh chép tay có thể bị lệch. Trang này mô tả CẤU TRÚC và các QUY ƯỚC
mà mọi lệnh dùng chung (đã kiểm chứng trên binary thật ở bên dưới), rồi chỉ cho bạn nơi lấy chi tiết chính xác
cho từng lệnh. Đây là lựa chọn có chủ đích, không phải lỗ hổng: một bản sao hàng trăm flag chép tay vào file
này sẽ lỗi thời ngay lần tới flag của một lệnh thay đổi, còn `-h` thì không bao giờ.

## Toàn bộ bề mặt lệnh thật

```
$ aw help
Usage: aw <command> [flags]

Process commands:
  serve       start the local API/control-plane server
  worker      start an embedded worker process
  help        show this help
  version     print the aw build version

Resource commands (aw <resource> <action> [flags]):
  adapter                list|probe|register|show
  approval               resolve
  artifact               get
  blocker                resolve
  component              list
  context-snapshot       show
  definition             create|list|publish|show|validate|versions
  doctor
  events                 watch
  evidence               list|verify
  health                 live|ready
  message                append|list|upload-attachment
  node-run               retry-blocked
  pack-assignment        assign|list
  project                create|list|show
  projection             rebuild|rebuild-status|status
  release-set            abandon|create|list|local-commit|seal|show
  repository             list|onboarding|register|retry-probe
  repository-workspace   diff|log|reconcile|source
  run                    cancel|diagnostics|graph|show|start|timeline
  scope-expansion        approve|reject|request|withdraw
  settings               show|update
  version                diff|show
  wait                   signal
  work-item              cancel|create|create-child|list|mark-ready|readiness|show
  workspace-set          release|show

Global options (any resource command; env AW_DB, AW_ARTIFACT_ROOT, ...):
  --db <path>                 sqlite database of the installation
  --artifact-root <dir>       artifact storage root
  --workspace-root <dir>      Git worktree storage root (workspace/source commands)
  --claude-executable <path>  register the Claude CLI as a live provider
  --codex-executable <path>   register the Codex CLI as a live provider

Machine-readable output: add --json (one JSON document on stdout; a failure is
one typed error document). High-impact commands require --yes when stdin is not
a terminal or --json is set. Actor and roles are never flags: --principal-config
selects the trusted principal file.

Run 'aw <command> -h' for command-specific flags.
```

Đây CHÍNH LÀ tập process-command đóng của `CLI_LOCAL` (`serve`/`worker`/`help`/`version` — mọi từ cấp cao
nhất khác là một "resource" được định tuyến qua một lớp resource-command dùng chung, V6-15O). `evidence verify`
cũng chạy bộ verify bundle offline có từ trước V6 khi được gọi với flag `--evidence-dir`/`--suite` thay vì
`--project-id` — xem `isLegacyBundleVerify` trong `cmd/aw/cli.go`.

## Các quy ước dùng chung cho mọi lệnh resource

- **Body đầu vào**: mọi mutation đọc một request body JSON từ stdin (pipe vào) hoặc `--file <path>` — không bao
  giờ dưới dạng flag inline. Một lệnh query (`list`/`show`/v.v.) không nhận body.
- **`--idempotency-key`**: mọi mutation đều nhận flag này; bỏ trống thì lệnh tự sinh một key và trả về trong
  trường `idempotencyKey` của response. Phát lại CÙNG key với CÙNG request sẽ trả về `"replayed": true` và
  kết quả GỐC — không bao giờ thực thi lại.
- **`--expected-version`**: mọi lệnh dạng update (không bao giờ là create) đều bắt buộc flag này — tương
  đương `If-Match` của HTTP trên CLI. Một version cũ (stale) là một `CONFLICT` thật, có kiểu, không bao giờ bị
  ghi đè âm thầm.
- **`--project-id`**: bắt buộc cho mọi thứ có scope project; bỏ qua cho các lệnh thực sự có scope installation
  (`project create`, `definition create --kind POLICY` không có project, v.v.) — xem
  [04-authoring-workflows.md](04-authoring-workflows.md) để biết loại definition nào có scope project, loại
  nào có scope installation.
- **`--principal-config`**: chọn file actor/roles tin cậy; không bao giờ là flag `--actor`/`--role` (ADR-028).
- **`--yes`**: bắt buộc cho một mutation "high-impact" bất cứ khi nào stdin không phải terminal tương tác HOẶC
  có `--json` (tức là mọi lần gọi bằng script/pipe) — một session terminal tương tác sẽ nhận một lời nhắc xác
  nhận thật thay vào đó. `--yes` là flag mà `clicompose` gỡ ra trước khi phần parse flag riêng của lệnh chạy,
  nên nó có thể xuất hiện ở bất kỳ vị trí nào trong danh sách tham số; một lệnh KHÔNG high-impact (ví dụ
  `definition create`) hoàn toàn không nhận `--yes` và sẽ từ chối nó như một flag không xác định nếu bạn vẫn
  truyền vào — hãy thử bỏ nó đi trước nếu bạn gặp `flag provided but not defined: -yes`.
- **`--wait`/`--wait-timeout`**: trên các lệnh khởi động công việc bất đồng bộ (`run start`, `release-set
  local-commit`), block và poll cho tới khi job/run bị ảnh hưởng đạt trạng thái terminal. Thuần túy quan sát —
  nó không bao giờ tự thực thi, retry hay hủy bất cứ thứ gì; bỏ nó đi thì lệnh trả về ngay với trạng thái ban
  đầu (chưa terminal).
- **`--json`**: output một document duy nhất, máy đọc được, trên mọi lệnh, dù thành công hay thất bại — một
  lỗi là một error document có kiểu (`{"error": {"code": ..., "message": ...}}`), không bao giờ là stack trace
  trần hay một thông điệp định dạng cho người đọc trộn vào stdout.
- **Exit code**: `0` thành công, `1` thất bại (một lỗi ứng dụng thật, có kiểu — kiểm tra `code` trong JSON error
  body), `2` lỗi cách dùng (flag sai, thiếu tham số bắt buộc — lệnh thậm chí chưa thử thực hiện thao tác).

## Tham số vị trí đứng sau flag

Mọi lệnh resource nhận tham số vị trí (một ID) đều yêu cầu flag ĐỨNG TRƯỚC, tham số vị trí đứng cuối — đúng
hành vi chuẩn của package `flag` trong Go (nó ngừng nhận diện flag ở tham số không-phải-flag đầu tiên).
`aw definition publish --kind POLICY --yes my-policy-id` chạy được; `aw definition publish my-policy-id --kind
POLICY --yes` thì không (các flag bị đảo vị trí sẽ bị đọc như các tham số vị trí thừa và bị từ chối).

## Hai ví dụ thực tế (thật, đã kiểm chứng)

```bash
# Query: không có body, bắt buộc --project-id cho resource có scope project
aw work-item show --project-id <projectId> <workItemId>

# Mutation: body qua stdin, bắt buộc --expected-version (đây là update)
echo '{}' | aw work-item mark-ready --expected-version 1 --idempotency-key my-key <workItemId>
```

> **Ghi chú khi dịch (kiểm chứng lại trên binary build từ commit `7d0fb4c`, 2026-10-01):** bản gốc tiếng Anh có thêm
> `--yes` trong ví dụ `mark-ready` ở trên, nhưng `work-item mark-ready` không phải lệnh high-impact và từ chối `--yes`
> (`flag provided but not defined: -yes`), nên ví dụ đã được sửa. Các lệnh đã xác nhận **cần** `--yes` khi chạy không
> tương tác: `definition publish`, `adapter register`, `release-set seal`, `release-set local-commit`, `run cancel`,
> `work-item cancel`. Các lệnh **không** nhận `--yes`: `definition create`, `work-item mark-ready`,
> `pack-assignment assign`, `release-set create`, `message append`.

Xem [01-quickstart.md](01-quickstart.md) để có một walkthrough đầy đủ, thật, nhiều lệnh từ một bản cài mới cho
tới một run hoàn tất, và [08-backup-and-restore.md](08-backup-and-restore.md) cho binary `aw-maintenance` riêng
biệt (backup/restore KHÔNG phải là một lệnh resource — xem trang đó để biết lý do).
