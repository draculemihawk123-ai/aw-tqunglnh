# Spec: ví dụ
## Mục tiêu
Bình luận trên issue.
## Trong phạm vi
Tạo, liệt kê, xóa bình luận.
## Ngoài phạm vi
Sửa bình luận.
## User story
Là người dùng, tôi muốn bình luận.
## Quy tắc nghiệp vụ
- BR-1: nội dung tối đa 2000 ký tự.
## Acceptance criteria
- AC-1: Given issue tồn tại, When POST bình luận hợp lệ, Then 201.
- AC-2: When POST nội dung 2001 ký tự, Then 400.
- AC-3: When POST JSON hỏng, Then 400.
## Dữ liệu và ràng buộc
Bảng comments, khóa ngoại tới issues.
## Hạn chế đã biết
Chưa phân trang.
## Quyết định đã chốt
- Q-1: giới hạn 2000 (tự chọn).
