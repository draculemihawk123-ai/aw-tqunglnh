# tracker-contracts

Repository này giữ hợp đồng API duy nhất của hệ thống issue tracker: `spec/openapi.json` (OpenAPI 3.0.3). Repository `api` hiện thực nó và repository `web` gọi nó.

- Được phát triển qua Agent Kit (`aw`): mỗi lần chạy, bạn nhận một task có phạm vi thư mục riêng.
- Chỉ sửa file trong phạm vi của task. Không `git commit`, `git push` hay đổi branch: `aw` tạo commit sau khi người vận hành duyệt.
- Quy ước chi tiết và yêu cầu của task nằm trong prompt của từng lần chạy, không nằm ở file này.
