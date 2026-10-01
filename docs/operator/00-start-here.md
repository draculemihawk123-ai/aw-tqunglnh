# Tài liệu vận hành Agent Kit — bắt đầu từ đây

> V8-09 (`docs/design/10-v8-alpha-hardening.md` V8-09): tài liệu first-run/vận hành cho bản phát hành
> Alpha. Thư mục này chỉ mô tả **năng lực đã được kiểm chứng trên binary thật** — mọi lệnh xuất hiện ở đây
> đều đã thực sự được chạy trên một bản cài `aw serve`/`aw worker` thật trong lúc viết tài liệu (xem mục
> V8-09 trong `baocaov8checklist.md` để có toàn bộ transcript thật mà quickstart này được trích ra). Tài liệu
> này không bao giờ mô tả hành vi mong muốn hay mới chỉ nằm trong kế hoạch.

## Đây là gì

Agent Kit (`aw`) là một control plane cục bộ, dành cho một người vận hành (operator), dùng để chạy các
workflow AI-agent trên chính các Git repository của bạn: bạn đăng ký repository, soạn một workflow graph
(agent, command, gate, approval), và `aw` điều khiển các lần chạy thật trên worktree thật của repository,
đồng thời ghi lại mọi quyết định dưới dạng evidence bền vững, truy vấn được. Đây không phải sản phẩm SaaS
multi-tenant — một bản cài `aw` chỉ phục vụ máy của chính một operator (ADR-025/ADR-028).

## Mọi thứ nằm ở đâu

| Bạn muốn... | Đọc |
|---|---|
| Cài đặt và chạy workflow đầu tiên | [01-quickstart.md](01-quickstart.md) |
| Hiểu mọi flag của `aw serve`/`aw worker` và thứ tự ưu tiên cấu hình | [02-configuration.md](02-configuration.md) |
| Tra cứu một lệnh `aw <resource> <action>`, flag, dạng JSON hoặc exit code của nó | [03-cli-reference.md](03-cli-reference.md) |
| Tự viết các document Policy/Skill/Command/Gate/Workflow | [04-authoring-workflows.md](04-authoring-workflows.md) |
| Đăng ký provider Claude/Codex, hoặc hiểu các isolation tier | [05-providers-and-isolation.md](05-providers-and-isolation.md) |
| Hiểu ReleaseSet, Git commit chỉ-cục-bộ, và cách xem source/diff/log | [06-source-control-and-releases.md](06-source-control-and-releases.md) |
| Hiểu evidence, retention của artifact, và cái gì được dọn dẹp khi nào | [07-evidence-and-retention.md](07-evidence-and-retention.md) |
| Sao lưu hoặc khôi phục một bản cài | [08-backup-and-restore.md](08-backup-and-restore.md) |
| Nâng cấp lên bản phát hành mới, hoặc rollback | [10-upgrade-and-rollback.md](10-upgrade-and-rollback.md) |
| Chẩn đoán một lỗi | [09-troubleshooting.md](09-troubleshooting.md) |

## Ngoài phạm vi (nói rõ ra, để bạn khỏi phải tìm ở đây)

- **Không có terminal tương tác trên trình duyệt.** `repository-workspace source`/`diff`/`log` cho bạn các
  view chỉ-đọc, có phân trang, về file/diff/lịch sử trên một Git object store thật — không bao giờ là shell
  vào một container đang chạy hay một trình giả lập terminal trong trình duyệt. Xem
  [06-source-control-and-releases.md](06-source-control-and-releases.md).
- **Không đồng bộ nhiều bản cài.** Backup/restore chỉ bao phủ MỘT bản cài cục bộ; nó không bao giờ hứa hợp
  nhất hay đồng bộ hai bản cài riêng biệt. Xem [08-backup-and-restore.md](08-backup-and-restore.md).
- **Không có Git push/fetch/clone tới remote.** Mọi repository mà bản cài này chạm tới đều được đọc trực tiếp
  từ đường dẫn trên filesystem cục bộ mà bạn đăng ký (`remoteLocator`); commit của ReleaseSet nằm ngay trong
  repository cục bộ đó dưới dạng commit cục bộ thông thường — không có gì được push lên remote
  (`06-source-control-and-releases.md`).
- **Không có sandbox cấp hệ điều hành thật trong Alpha.** Chỉ có isolation `OPERATOR_TRUSTED_LOCAL`; một node
  của workflow được pin vào `ENFORCED_ISOLATED` sẽ fail closed thay vì âm thầm chạy không sandbox — xem
  [05-providers-and-isolation.md](05-providers-and-isolation.md).
- **Không có PostgreSQL, không có persistence adapter thứ hai.** SQLite là engine lưu trữ duy nhất của Alpha.
- **Không triển khai multi-tenant/cloud.** Một bản cài `aw`, một operator cục bộ, một máy.

## Tự kiểm tra khi chạy mới (fresh-run self-test)

Một session sạch — không có ngữ cảnh nào trước đó ngoài tài liệu này — phải trả lời được, kèm trích dẫn tới
một trong các file ở trên, cho từng luồng trong năm luồng thuộc phạm vi của V8-09 (cài đặt & chạy lần đầu;
đăng ký repository; soạn & publish workflow; chạy một task; khôi phục sau lỗi):

- **WHAT** — luồng này đạt được điều gì, và vì sao operator cần làm nó?
- **WHERE** — luồng này được mô tả ở đâu trong tài liệu (file nào, mục nào)?
- **HOW** — chính xác là như thế nào: lệnh `aw` thật nào, với flag thật nào?
- **DONE** — tín hiệu quan sát được nào (một trường JSON, một exit code, một check của `doctor`) chứng minh
  luồng đó thực sự thành công?
- **Out of scope** — luồng này cố ý KHÔNG bao phủ điều gì (liên kết tới danh sách ở trên khi phù hợp)?

Nếu bất kỳ câu trả lời nào trong năm câu không thể lấy nguồn từ một file trong thư mục này, đó là một lỗ hổng
thật của tài liệu, không phải thứ để giải thích bằng lời — hãy ghi nhận nó theo đúng cách mọi lỗ hổng thật
khác trong repo này được theo dõi.
