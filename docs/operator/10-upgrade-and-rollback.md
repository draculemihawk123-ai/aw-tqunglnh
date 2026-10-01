# Nâng cấp và rollback

Mọi khẳng định ở đây đều được bảo chứng bởi một test thật (V8-10): ma trận nâng cấp và buổi diễn tập
backup-restore trong `internal/adapters/sqlite/upgrade_rollback_test.go`, và test từ chối trên binary thật trong
`internal/integration/v6accept/upgrade_rollback_test.go`.

## "Nâng cấp" có nghĩa là gì với bản cài này

Trạng thái bền vững của bản cài là database SQLite cộng với thư mục `--artifact-root` của bạn. Một build `aw` mới
mang theo một danh sách migration schema được đánh số; **mở database bằng một build mới sẽ tự động áp dụng những
migration mà database đó chưa từng thấy, mỗi migration đúng một lần.** Không có lệnh "migrate" riêng.

- Migration là bất biến sau khi đã phát hành. Checksum của từng migration được ghi trong database
  (`schema_migrations`), và mở một database có checksum đã ghi không còn khớp với bản sao trong binary sẽ thất bại
  — một migration đã phát hành không bao giờ bị sửa, chỉ được nối tiếp bằng một migration mới.
- Mỗi migration commit trong transaction riêng của nó. Những migration phải dựng lại một bảng mà các bảng khác trỏ
  tới còn chạy thêm một bước kiểm tra foreign-key trước khi được ghi nhận, và sẽ rollback nếu dù chỉ một tham chiếu
  bị hỏng.
- File artifact của bạn không bị chạm vào khi nâng cấp schema.

Tìm hiểu một binary yêu cầu schema nào, mà không cần mở bất kỳ database nào:

```bash
aw version --json    # "schemaVersion" là migration cao nhất mà binary này mang theo
```

Cấu hình: các safe setting bạn đã thay đổi bằng `aw settings update` được lưu trong database và đi cùng nó. File
cấu hình JSON tùy chọn là của bạn và không được quản lý version; các key mà một binary không biết sẽ bị bỏ qua mà
không có cảnh báo, nên sau một lần rollback hãy kiểm tra các giá trị hiệu lực bằng `aw settings show`.

## Trước khi nâng cấp: tạo backup bằng bản phát hành HIỆN TẠI

Nâng cấp là một chiều (xem bên dưới), nên bản backup là con đường duy nhất để quay lại. Hãy tạo nó **trước khi**
binary mới chạm vào database:

```bash
aw-maintenance backup --db ./aw-install/aw.db --out ./pre-upgrade-backup    # chạy công cụ của bản phát hành CŨ
```

Chạy `aw-maintenance` của bản phát hành **cũ**, không phải bản mới. `aw-maintenance` mở database qua cùng đường
khởi động như chính `aw`, nên `aw-maintenance backup` của một build mới sẽ migrate database đang chạy trước rồi
mới backup bản sao đã được nâng cấp — không phải một bản backup trước nâng cấp. Nếu bạn không còn công cụ cũ, hãy
dừng `aw serve`/`aw worker` và copy `aw.db` cùng với các file `aw.db-wal` và `aw.db-shm` nằm bên cạnh nó (nếu có).

Sao lưu cả thư mục `--artifact-root` của bạn (xem [08-backup-and-restore.md](08-backup-and-restore.md)).

## Nâng cấp

1. Dừng `aw serve` và `aw worker`.
2. Tạo bản backup như ở trên.
3. Thay các binary.
4. Khởi động `aw serve` / `aw worker` (hoặc chạy `aw doctor`). Lần mở đầu tiên sẽ áp dụng các migration đang chờ.
5. Chạy `aw doctor` và xác nhận `status: HEALTHY`; project, run và evidence của bạn được đọc lại từ chính database
   như trước.

Khởi động lại sau khi nâng cấp là một no-op: lần mở thứ hai không áp dụng gì và không thay đổi gì.

## Rollback

**Một binary cũ hơn database của bạn sẽ từ chối mở nó.** Nếu một bản phát hành mới hơn đã migrate database, binary
cũ sẽ dừng ngay lúc khởi động và để nguyên database đúng như nó đã tìm thấy:

```
aw: open database: database schema version 42 is newer than this binary supports (highest migration it knows: 41; unknown applied migration(s): [42]) — the database was migrated by a newer release and this binary has NOT modified it; run that release's (or a newer) binary, or restore a backup taken before the upgrade (docs/operator/10-upgrade-and-rollback.md)
```

Migration không đảo ngược được và không bản phát hành nào tự đánh dấu là tương thích với một binary cũ hơn, nên
quy tắc rất chặt: một binary chỉ chạy trên một database mà nó biết đầy đủ mọi migration đã áp dụng.

| Tình huống | Cách xử lý |
|---|---|
| Đã rollback binary, nhưng chưa có migration mới nào được áp dụng (`schemaVersion` của hai build bằng nhau) | Khởi động binary cũ — nó mở database bình thường. |
| Rollback sau khi build mới đã migrate database | Binary cũ từ chối. Khôi phục bản backup trước nâng cấp (bên dưới), rồi khởi động binary cũ. |
| Muốn dùng lại bản phát hành mới sau khi đã rollback | Mở database đã khôi phục bằng binary mới; việc nâng cấp đơn giản được thử lại từ bản sao đã khôi phục. |

### Khôi phục bản backup trước nâng cấp

```bash
aw-maintenance restore --backup ./pre-upgrade-backup --into ./restored --artifact-root ./restored-artifacts
```

Sau đó trỏ `--db` của binary cũ tới `./restored/restored.db`. **Mọi thứ bản phát hành mới đã ghi sau thời điểm tạo
backup đều không có trong backup** — rollback là quay về đúng thời điểm của bản backup, không phải hạ cấp database
mới hơn.

### Một giới hạn bạn không thể sửa từ đây

Cơ chế từ chối nằm trong chính binary thực hiện nó. Một binary được build **trước** V8-10 không có kiểm tra này
và sẽ mở một database mới hơn mà không hề phàn nàn, rồi chạy SQL cũ trên một schema mà nó không hiểu. Cơ chế từ
chối bảo vệ mọi binary được build từ V8-10 trở đi; với bất cứ binary nào cũ hơn, bản backup ở mục trước là sự bảo
vệ duy nhất. Đây là lý do việc tạo backup trước không phải là tùy chọn.
