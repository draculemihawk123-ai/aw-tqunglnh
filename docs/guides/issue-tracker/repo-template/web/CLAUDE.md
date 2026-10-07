# tracker-web

Repository này được phát triển qua Agent Kit (`aw`): mỗi lần chạy, bạn nhận một task có phạm vi thư mục riêng.

- `frontend/`: React + TypeScript + Vite. `docs/`: tài liệu. Hợp đồng API nằm ở repository `contracts` (`spec/openapi.json`); repository này chỉ gọi những endpoint có trong đó.
- Chỉ sửa file trong phạm vi của task. Không `git commit`, `git push` hay đổi branch: `aw` tạo commit sau khi người vận hành duyệt.
- Quy ước chi tiết và yêu cầu của task nằm trong prompt của từng lần chạy, không nằm ở file này.
