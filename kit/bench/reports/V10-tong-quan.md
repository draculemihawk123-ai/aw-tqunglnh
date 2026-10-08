# V10 — báo cáo tổng quan (đo lại từ đầu, bộ đo đã sửa)

## Tóm tắt một đoạn

Kit sau V10 làm **tài liệu (brainstorm, spec, thiết kế, plan) tốt hơn rõ rệt** so với kit trước V10: điểm chấm mù tăng từ 3,4 lên 4,9 trên thang 5, và spec có nhiều tiêu chí nghiệm thu hơn khoảng 44%. Cái giá là **chi phí tăng khoảng 40%** và **chưa thấy lợi ích ở bước viết code**; ở bước code còn có tín hiệu xấu nhẹ (2 trong 6 task phải sửa lại sau lần kiểm tra đầu, so với 0 trong 6 ở kit cũ), nhưng chỉ có 2 lượt đo mỗi mốc nên chưa đủ chắc chắn.

## Cách đo

- Hai mốc, mỗi mốc 2 lượt chạy thật bằng Claude (cùng model `sonnet`, cùng effort, cùng bài toán "thêm tính năng bình luận" cho issue tracker ba repository, cùng commit `aw`):
  - **B0** = kit trước V10 (commit `fd892d0`).
  - **B1** = kit hiện tại (commit `9271de5`).
- **Bộ đo đã sửa so với lần A0/A1:** tiêu chí của bước review không còn ghi cứng ba giá trị mâu thuẫn với tài liệu agent viết (xem [A1.md](A1.md)); mỗi lượt có thư mục cấu hình Claude riêng (không nạp skill của máy chạy). Vì vậy A0/A1 không còn dùng để so sánh; B0/B1 thay thế chúng.
- Hai lần chạy đầu của B0/B1 và một lần của B1 bị cắt giữa chừng vì tài khoản hết hạn mức phiên của Claude ("You've hit your session limit"), không phải lỗi của kit; chúng bị loại và chạy lại. Kết quả dưới đây chỉ dùng các lượt chạy hết (cả 4 lượt đều xong, 0 attempt hỏng).
- Tài liệu được **chấm mù** bởi một subagent (`sonnet`, effort `high`) không biết tài liệu thuộc mốc nào, theo [rubric](../rubric.md).

## Kết quả

| | B0 (kit cũ) | B1 (kit hiện tại) | thay đổi |
|---|---:|---:|---:|
| **Điểm tài liệu (chấm mù, thang 5)** | **3,42** | **4,92** | **+1,5** |
| – brainstorm | 3,0 | 4,5 | |
| – spec | 3,5 | 5,0 | |
| – thiết kế | 2,5 | 5,0 | |
| – plan | 3,83 | 5,0 | |
| Tiêu chí nghiệm thu trong spec | 17 | 24,5 | +44% |
| – trong đó có đường lỗi | 10 | 15 | +50% |
| Kích thước tài liệu thiết kế | 4,9 đến 5,9 KB | 15 đến 20 KB | gấp 3 đến 4 |
| **Chi phí mỗi lượt (USD)** | **3,78** | **5,31** | **+40%** |
| Thời gian | 17,7 phút | 20,8 phút | +17% |
| Token đầu vào | 4,09 M | 5,86 M | +43% |
| Kiểm tra gate1 qua ngay lần đầu (6 task) | 6/6 | 4/6 | xấu hơn, nhiễu lớn |
| Vòng sửa | 0 | 2 | |
| Reviewer độc lập | duyệt (approved) cả 2 lượt, chỉ có lỗi Minor | duyệt (approved) cả 2 lượt, chỉ có lỗi Minor | không phân biệt được |

Chi phí theo bước (USD, trung bình): brainstorm 0,12 → 0,27; spec 0,14 → 0,20; **thiết kế 0,21 → 0,57 (gấp 2,7)**; frame 0,46 → 0,66; plan 0,60 → 0,79; **code (BUILD) 1,19 → 1,59 (+34%)**; sync 0,53 → 0,59; review 0,53 → 0,65.

## Điều rút ra

1. **Tri thức mới có tác dụng lớn ở bước tài liệu.** Cả bốn loại tài liệu đều tăng điểm, và người chấm nêu lý do giống nhau: tài liệu điểm cao trích được `file:dòng` của code, nêu phương án đã loại, rủi ro và cách hoàn tác, bảng giả định kèm cách kiểm. Đây là phần nên giữ.
2. **Bước viết code: chưa có bằng chứng lợi ích, chi phí +34%.** Có một dấu hiệu tích cực mang tính ví dụ (ở một lượt B0 reviewer nhắc code đếm độ dài theo UTF-16, còn code ở B1 có validator đếm theo code point), nhưng đó là một quan sát, không phải số đo. Còn tín hiệu ngược: gate1 hỏng lần đầu ở 2/6 task của B1 so với 0/6 của B0. Nguyên nhân hỏng chưa được điều tra. Với n=2 không thể kết luận cả hai chiều.
3. **Reviewer đã hết bị bộ đo cũ làm sai lệch:** cả bốn lượt đều duyệt, chỉ có lỗi Minor. Nhưng reviewer vẫn không chạy được test hay `git diff` (bị chặn), nên nhận xét của nó dựa trên đọc code, chưa phải bằng chứng test xanh. Số lỗi Minor ít và cách đếm không đều, không dùng được để so hai mốc.
4. **Tốn kém không đều:** thiết kế tốn gấp 2,7 lần nhưng đạt điểm cao nhất (từ 2,5 lên 5,0), đáng giá. Code tốn thêm 0,40 USD mỗi lượt mà chưa thấy lợi ích.

## Những gì chưa đo hoặc chưa chắc

- **Chỉ 2 lượt mỗi mốc.** Chênh lệch lớn và cùng chiều ở cả hai lượt (điểm tài liệu, chi phí) đáng tin; chênh lệch nhỏ (gate1, lỗi Minor) thì không.
- **Chấm mù bằng một agent**, chưa có người xác nhận. Tài liệu B1 dài hơn gấp 3 đến 4 lần ở bước thiết kế; rubric yêu cầu không chấm theo độ dài, nhưng không loại trừ hoàn toàn khả năng độ dài ảnh hưởng.
- **Không tách được đóng góp từng skill hay layer.** B1 gộp toàn bộ V10.
- **Các workflow mới của V10** (`wf-task-delivery-plus`, `wf-bugfix`, `wf-retro`, các bước `check-plan`, `hygiene`, `size`/`simplify`) **chưa chạy với Claude thật**; bộ đo dùng `wf-task-delivery-<repo>` cũ. Chúng chỉ được kiểm bằng agent giả lập (đồ thị đúng).
- Chất lượng code chỉ được đánh giá qua gate (test) và đọc của reviewer; chưa có kiểm tra độc lập khác.

## Chi phí của lần đo lại này

Khoảng **23 USD**: bốn lượt hợp lệ 18,2 USD (B0 7,55; B1 10,63) và khoảng 5 USD cho các lượt bị loại vì hết hạn mức. Cộng với A0/A1 (khoảng 21 USD), tổng V10 khoảng 44 USD.

## Đề xuất sửa (chưa làm, cần anh quyết)

1. **Giữ** tri thức cho brainstorm, spec, thiết kế, plan.
2. **Thu gọn tri thức BUILD** (giảm số resource nạp cho agent code; ví dụ chỉ nạp `build.*` và `test.*` cho task rủi ro cao) rồi đo lại một mốc B2 (khoảng 10 USD) để xem chi phí và gate1 có cải thiện không.
3. **Điều tra vì sao gate1 hỏng lần đầu ở B1** trước khi kết luận về BUILD.
4. Chạy thật `wf-task-delivery-plus` một lần (khoảng 2 đến 3 USD) để kiểm các bước mới.

## Bổ sung: B2 — sau khi thu gọn tri thức của agent code

B2 = cây `83976ab` (agent code nhận 28 resource, 19,9 KB, thay vì 39 resource, 27,3 KB), cùng bộ đo, 2 lượt (một lượt đầu hỏng ngay lúc khởi tạo vì một lỗi của aw: hai tiến trình cùng migrate database mới tạo, `UNIQUE constraint failed: schema_migrations.version`; đã chạy bù một lượt).

| | B0 (kit cũ) | B1 | B2 (thu gọn) |
|---|---:|---:|---:|
| Chi phí mỗi lượt (USD) | 3,78 | 5,31 | 6,12 |
| Chi phí node code (USD) | 1,19 | 1,59 | 1,70 |
| gate1 hỏng ở lần đầu (trên 6 task) | 0 | 2 | 4 |
| Vòng sửa | 0 | 2 | 4 |

**Thu gọn không giảm chi phí và không giảm việc phải sửa lại.** Chi phí node code không giảm (1,59 → 1,70), vòng sửa còn tăng; chi phí tổng B2 cao hơn một phần vì một lượt có một attempt review hỏng (chưa điều tra, 1,63 USD ở node review). Với n=2, khác biệt nhỏ chỉ là nhiễu, nhưng ít nhất không có dấu hiệu thu gọn giúp ích. Chưa rút ra kết luận giữ hay bỏ phần đã bỏ.

**Phát hiện khi tìm nguyên nhân (đã sửa trong kit, chưa đo lại):** ở cả 6 lần task backend hỏng kiểm tra lần đầu (B1, B2), danh sách lỗi agent nhận là hàng chục dòng `[ERROR] … <<< ERROR!` của từng test, và `contextLoads` đỏ cùng mọi test khác, tức Spring không nạp được `ApplicationContext`. `maven-test.sh` chỉ in dòng bắt đầu bằng `[ERROR]`, mà stack trace của Surefire (có `Caused by:`) không có tiền tố đó, nên **agent không bao giờ thấy nguyên nhân gốc**. Đã sửa: in các dòng nguyên nhân (không trùng) trước danh sách test. Chưa biết sửa này giảm được bao nhiêu vòng sửa; cần một mốc đo mới (B3, khoảng 10 USD).

## Bổ sung: chất lượng đầu ra B2 so với B0 (chấm mù)

Cùng 2 lượt mỗi bên, task api + web (bình luận cho issue). Nhãn bị ẩn khi chấm; giải mã sau (D, B = B0; A, C = B2).

**Tài liệu (brainstorm, thiết kế, spec, plan; 24 file, một agent chấm theo rubric 1–5):**

| Loại | B0 | B2 |
|---|---:|---:|
| brainstorm | 3,00 | 4,50 |
| design | 2,50 | 4,50 |
| plan | 3,83 | 4,67 |
| spec | 4,00 | 5,00 |

**Mã (một agent review mỗi lượt, chỉ đọc diff, không chạy):**

| Lượt | đúng spec | đường lỗi | test | vững | quy ước |
|---|---:|---:|---:|---:|---:|
| B0 run-1 | 4 | 4 | 4 | 4 | 5 |
| B0 run-2 | 4 | 4 | 4 | 4 | 4 |
| B2 run-1 | 4 | 4 | 4 | 4 | 3 |
| B2 run-2 | 4 | 4 | 4 | 3 | 4 |

Kết luận: tài liệu của B2 rõ ràng tốt hơn B0 (chênh khoảng 1–2 điểm, ổn định ở cả bốn loại). Mã thì ngang nhau, không phân biệt được trong khoảng nhiễu. Lỗi hay gặp ở cả hai bên là frontend: thiếu chống gửi trùng, trạng thái lỗi lẫn trạng thái rỗng, race khi đổi issue.

Giới hạn: n=2 mỗi bên; chấm bằng LLM, mỗi lượt một người chấm; review mã chỉ đọc diff; số liệu test đếm bằng regex. Phần tăng chi phí và số vòng sửa của B2 mua được chất lượng tài liệu, không mua được chất lượng mã.

## Quyết định sau khi so sánh B2 và B0

Giữ tri thức đầy đủ cho brainstorm, design, spec, plan (nơi B2 tốt hơn rõ). **Đưa agent code về đúng như B0**: `agent-flow-build` chỉ nạp `flow.working-rules`, `flow.needs-info`, `flow.build` (cộng phần riêng của project: layer của project và `dev.definition-of-done`), không nạp skill hay layer chắt lọc nào của V10. Lý do: B1 (39 resource) và B2 (28 resource) đều đắt hơn B0 mà mã không khác. Các file layer và skill vẫn nằm trong kit và vẫn dùng cho agent khác (design, reviewer…); chỉ danh sách resource của agent code thay đổi, nội dung layer/skill không sửa.

Ghi chú sửa lỗi: lần đầu tôi hoàn nhầm về bản B1 (39 resource); đã sửa lại thành B0. Sửa `maven-test.sh` (in `Caused by:`) vẫn giữ; tác dụng chưa đo.
