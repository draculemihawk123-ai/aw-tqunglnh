# Brainstorm: ví dụ
## Hiện trạng
`IssueService.java:40` xóa issue bằng `repository.delete`.
## Vấn đề gốc và người bị ảnh hưởng
Người dùng không ghi chú được trên issue.
## Giả định
| Mã | Giả định | Bằng chứng | Cách kiểm |
|---|---|---|---|
| A-1 | SQLite không bật khóa ngoại | chưa kiểm | đọc `application.properties` |
## Phương án
### P-1: bảng comments mới
Ưu: tách bạch. Nhược: thêm migration. Công sức M.
### P-2: dùng cái đã có (cột notes)
Ưu: không migration. Nhược: một ghi chú duy nhất. Công sức S.
## Đề xuất
P-1, vì P-2 không cho nhiều bình luận.
## Yêu cầu cụ thể
Ba endpoint, giới hạn 2000 ký tự.
## Câu hỏi mở
Có cần sửa bình luận không? Người trả lời: chủ sản phẩm.
