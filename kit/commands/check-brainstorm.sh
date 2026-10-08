#!/bin/sh
# COMMAND "check-brainstorm" (sau node BRAINSTORM của wf-feature-definition): 01-brainstorm.md đủ khung của skill-doc-templates.
# Kiểm: đủ mục, ít nhất một giả định A-n, ít nhất hai phương án P-n. Lỗi quay về BRAINSTORM; lỗi in dạng THIẾU + WHY + FIX.
set -eu
. "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/lib.sh" # @aw-include
. "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/docs-lib.sh" # @aw-include
dirs=$(docs_dirs)
[ -n "$dirs" ] || aw_fail "Không có tài liệu tính năng mới hoặc được sửa trong docs/features/"
for dir in $dirs; do
  f="$dir/01-brainstorm.md"
  if [ ! -s "$f" ]; then docs_err "THIẾU: $f"; continue; fi
  docs_head "$f" "hiện trạng" 'hiện trạng|current state'
  docs_head "$f" "vấn đề gốc" 'vấn đề|problem'
  docs_head "$f" "giả định" 'giả định|assumption'
  docs_head "$f" "phương án" 'phương án|option'
  docs_head "$f" "đề xuất" 'đề xuất|khuyến nghị|recommend'
  docs_head "$f" "yêu cầu cụ thể" 'yêu cầu|requirement'
  docs_head "$f" "câu hỏi mở" 'câu hỏi|open question'
  docs_need_ids "$f" A 1 "giả định, mỗi giả định kèm cách kiểm"
  docs_need_ids "$f" P 2 "ít nhất hai phương án"
done
docs_report
echo "OK: 01-brainstorm.md đủ khung"
