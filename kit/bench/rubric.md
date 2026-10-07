# Rubric chấm tài liệu của golden workload

Chấm từng tài liệu do agent viết (BRAINSTORM, SPEC, DESIGN, PLAN) trên thang 1 đến 5, **không biết** tài liệu thuộc lần đo nào:
`kit-metrics.py blind <out>/A0 <out>/A1 --out <thư mục chấm>` xuất tài liệu với mã ngẫu nhiên và `diem.csv`; điền cột `diem`; sau đó
`kit-metrics.py unblind key.json diem.csv`. Người chấm (hoặc một agent CHECKER chấm theo rubric, người xác nhận vài mẫu) chỉ đọc file
`<mã>.md` và rubric này. Không chấm theo độ dài hay văn phong; chấm theo việc người duyệt có đủ thông tin để quyết định.

Điểm chung: **5** = người duyệt quyết định được ngay, không thiếu gì quan trọng, không thừa; **4** = đủ, thiếu một chi tiết nhỏ;
**3** = dùng được nhưng phải hỏi thêm một điều quan trọng; **2** = thiếu nhiều, hoặc có khẳng định không kiểm chứng được; **1** = không
dùng được (sai trọng tâm, bịa, hoặc rỗng).

| Loại | Điểm 5 cần có | Hạ điểm khi |
|---|---|---|
| **brainstorm** | Nêu vấn đề gốc (không chỉ chép lại yêu cầu); 2 đến 3 hướng thay thế có ưu và nhược; giả định nào chưa kiểm chứng và cách kiểm; phạm vi tối thiểu | Chỉ có một hướng; không nêu giả định; nhảy thẳng vào giải pháp |
| **spec** | Quy tắc nghiệp vụ rõ; acceptance criteria Given/When/Then đo được; phủ đường lỗi và giá trị biên; nêu cái nằm ngoài phạm vi và hạn chế đã biết | AC mơ hồ ("hoạt động tốt"); chỉ có đường thành công; mâu thuẫn giữa quy tắc và AC |
| **design** | Luồng dữ liệu; quyết định kèm lý do và phương án đã loại; rủi ro, tương thích ngược, rollback; khẳng định về code có trích file; `tasks.json` chia việc không chồng file | Không nêu rủi ro; khẳng định về code sai hoặc không trích; hai task cùng sửa một file |
| **plan** | File sẽ sửa theo thứ tự; phụ thuộc; cách kiểm chứng từng bước; tiêu chí xong đo được; khớp với thiết kế đã duyệt | Chung chung ("sửa backend"); thiếu test; lệch thiết kế |
| **sync** | Cập nhật tài liệu đúng chỗ khi hành vi đổi; không viết thừa; liên kết đúng | Bỏ sót thay đổi hành vi; viết lại cả tài liệu không cần |

Ghi chú khi chấm: ghi vào cột ghi chú của `diem.csv` (tùy chọn) một câu lý do cho điểm 1, 2 và 5.
