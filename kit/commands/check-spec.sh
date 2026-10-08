#!/bin/sh
# COMMAND "check-spec" (sau node SPEC của wf-feature-definition): 02-spec.md đủ khung của skill-doc-templates.
# Kiểm: đủ mục (kể cả "Ngoài phạm vi" và "Hạn chế đã biết"), BR-n, AC-n, mỗi AC có When và Then (Given khi có tiền điều kiện). Lỗi quay về SPEC.
set -eu
. "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/lib.sh" # @aw-include
. "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/docs-lib.sh" # @aw-include
dirs=$(docs_dirs)
[ -n "$dirs" ] || aw_fail "Không có tài liệu tính năng mới hoặc được sửa trong docs/features/"
for dir in $dirs; do
  f="$dir/02-spec.md"
  if [ ! -s "$f" ]; then docs_err "THIẾU: $f"; continue; fi
  docs_head "$f" "mục tiêu" 'mục tiêu|goal'
  docs_head "$f" "trong phạm vi" 'trong phạm vi|in scope|phạm vi'
  docs_head "$f" "ngoài phạm vi" 'ngoài phạm vi|out of scope'
  docs_head "$f" "user story" 'user story|câu chuyện'
  docs_head "$f" "quy tắc nghiệp vụ" 'quy tắc|business rule'
  docs_head "$f" "acceptance criteria" 'acceptance|tiêu chí chấp nhận'
  docs_head "$f" "dữ liệu và ràng buộc" 'dữ liệu|ràng buộc|data'
  docs_head "$f" "hạn chế đã biết" 'hạn chế|limitation'
  docs_head "$f" "quyết định đã chốt" 'quyết định|decision'
  docs_need_ids "$f" BR 1 "quy tắc nghiệp vụ"
  docs_need_ids "$f" AC 1 "acceptance criteria"
  bad=$(docs_ac_gwt "$f")
  [ -z "$bad" ] || docs_err "SAI: AC thiếu When hoặc Then trong $f:$bad"
done
docs_report
echo "OK: 02-spec.md đủ khung"
