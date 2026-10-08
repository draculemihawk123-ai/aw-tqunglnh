# Design: ví dụ
## Thay đổi theo tầng
Migration V3, entity Comment, CommentService, controller.
## Hợp đồng API
POST /api/issues/{id}/comments → 201 `{"id":1}`.
## Quyết định và đánh đổi
- D-1: xóa tường minh trong service. Phương án đã loại: dựa vào ON DELETE CASCADE vì SQLite mặc định tắt khóa ngoại.
## Kế hoạch test
| Test | AC |
|---|---|
| createValid | AC-1 |
| tooLong, badJson | AC-2..3 |
## Rủi ro và rollback
- R-1: migration lỗi. Giảm: chạy trên bản sao. Rollback: bỏ bảng.
## Ngoài phạm vi
Phân trang.
