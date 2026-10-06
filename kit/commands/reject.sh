#!/bin/sh
# COMMAND node "reject": điểm dừng của một run không được hoàn thành — người duyệt chọn "rejected"/"abandon",
# hoặc một vòng sửa đã hết ngân sách (cyclePolicy). Thoát mã 1 để run kết thúc FAILED thay vì đi tới END
# (END được tính là hoàn thành). WorkItem khi đó BLOCKED với blocker RUN_FAILED: xem README mục 4.7.
echo "Run dừng: thay đổi bị từ chối hoặc đã hết số vòng sửa cho phép." >&2
exit 1
