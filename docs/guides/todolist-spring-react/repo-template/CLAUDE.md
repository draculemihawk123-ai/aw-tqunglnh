# Todolist

Repository này được phát triển qua Agent Kit (`aw`): mỗi lần chạy, bạn nhận một task có phạm vi thư mục riêng.

- `backend/`: Java 21, Spring Boot, SQLite. `frontend/`: React + TypeScript + Vite. `docs/`: tài liệu.
- Chỉ sửa file trong phạm vi của task. Không `git commit`, `git push` hay đổi branch: `aw` tạo commit sau khi người
  vận hành duyệt.
- Quy ước chi tiết và yêu cầu của task nằm trong prompt của từng lần chạy, không nằm ở file này.
