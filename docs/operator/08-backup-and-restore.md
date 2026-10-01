# Sao lưu và khôi phục

## Vì sao đây là một binary riêng, không phải `aw backup`

Tập lệnh cấp tiến trình của `aw` (`serve`/`worker`/`help`/`version`) là một danh sách đóng đã được ghi nhận
(`docs/design/08-v6-api-projections.md:799`: *"no new leaf/route"*) — một công cụ backup/restore làm việc trên
các file của bản cài, không phải trên bề mặt HTTP của một bản cài đang chạy, nên nó cũng không có dạng
`aw <resource> <action>` tự nhiên nào. `cmd/aw-maintenance` là một binary độc lập riêng (V8-06), được build theo
cùng cách với `cmd/v6-gate`/`cmd/v8-security-gate`/`cmd/aw-release-build`.

## Những gì nó KHÔNG hứa

**Chỉ một bản cài cục bộ — không bao giờ đồng bộ hay hợp nhất hai bản cài riêng biệt.** Nếu bạn cần chuyển một
bản cài sang máy khác, `backup` + `restore` ở máy đó là con đường được hỗ trợ; không có khái niệm hai bản cài
được giữ đồng bộ với nhau.

> Sao lưu **trước khi nâng cấp**? Hãy dùng `aw-maintenance` của bản phát hành đang chạy hiện tại, không phải của
> bản mới — xem [10-upgrade-and-rollback.md](10-upgrade-and-rollback.md).

## Backup

```
$ aw-maintenance backup -h
Usage of backup:
  -db string
        path to the live installation's sqlite database
  -out string
        destination directory for the backup (created if absent; must not already contain a snapshot.db or manifest.json)
```

```bash
aw-maintenance backup --db ./aw-install/aw.db --out ./my-backup
```

An toàn khi chạy trong lúc một tiến trình `aw serve`/`aw worker` thật đang tích cực dùng cùng database — nó dùng
`VACUUM INTO` của SQLite, vốn chỉ lấy shared read lock (không bao giờ chặn một writer đồng thời ở chế độ WAL).
Tạo ra hai file: `snapshot.db` (một bản sao thật, nhất quán tại một thời điểm của toàn bộ database) và
`manifest.json` (mọi artifact mà bản cài này biết — ID, locator, content hash, kích thước, retention class, trạng
thái attach — nhưng không bao giờ chứa BYTE của artifact; hãy tự sao lưu thư mục `--artifact-root` của bạn riêng,
bằng bất kỳ phương tiện filesystem nào bạn vẫn đang dùng cho nó).

## Restore

```
$ aw-maintenance restore -h
Usage of restore:
  -artifact-root string
        the artifact-store root to verify the manifest against (the operator's own separately-restored artifact content)
  -backup string
        the backup directory a prior 'aw-maintenance backup' produced
  -into string
        a fresh, empty temp root to materialize the restored database into (created if absent)
```

```bash
aw-maintenance restore --backup ./my-backup --into ./restored --artifact-root ./restored-artifacts
```

Ba việc xảy ra, tất cả đều thật: `snapshot.db` của bản backup được copy vào `--into` (từ chối nếu `--into` đã có
một `restored.db` — không bao giờ âm thầm ghi đè một lần restore trước đó); database đã khôi phục được mở qua
đường `sqlite.Open` THẬT của production (bao gồm migration — một lỗi mở thật ở đây có nghĩa là bản backup tự nó
không dùng được, không phải bug riêng của công cụ restore); và mọi mục trong `manifest.json` được đối chiếu với
`--artifact-root` (nội dung artifact bạn đã khôi phục riêng), báo từng mục là `OK` / `MISSING` / `CORRUPT` — một
mục manifest ở trạng thái `Purged` đúng là không bao giờ bị đánh dấu thiếu (nội dung thật của nó đã bị xóa một
cách hợp lệ, bền vững bởi một retention sweep thật trước cả khi bản backup được tạo — xem
[07-evidence-and-retention.md](07-evidence-and-retention.md)).

Một lần restore sạch in ra `restore verified clean — every manifest entry is present and intact` và thoát với mã
`0`; bất kỳ mục thiếu/hỏng nào sẽ được in ra chính xác là mục nào và thoát với mã khác 0 — hãy trỏ `--artifact-root`
tới đúng thư mục artifact bạn đã khôi phục và chạy lại nếu gặp trường hợp này.
