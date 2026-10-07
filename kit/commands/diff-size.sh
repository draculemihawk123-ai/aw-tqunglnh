#!/bin/sh
# COMMAND "diff-size" (V10-15; tương ứng hook simplify-gate của ClaudeKit, nhưng KHÔNG thể bỏ qua): đo diff chưa commit của worktree so với
# HEAD. Vượt ngưỡng thì thoát mã 1 để workflow đi nhánh `large` sang node làm gọn code; dưới ngưỡng thì thoát mã 0.
# Ngưỡng mặc định lấy từ ClaudeKit: tổng dòng đổi 400, số file 8, một file 200 dòng. Đặt AW_DIFF_MAX_LOC, AW_DIFF_MAX_FILES,
# AW_DIFF_MAX_FILE_LOC để đổi khi chạy tay. Bỏ qua: tài liệu (docs/, *.md), lockfile, file sinh ra/đã nén; bỏ qua thay đổi khoảng trắng.
set -eu
. "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/lib.sh" # @aw-include
max_loc=${AW_DIFF_MAX_LOC:-400} max_files=${AW_DIFF_MAX_FILES:-8} max_file_loc=${AW_DIFF_MAX_FILE_LOC:-200}
git rev-parse --verify -q HEAD > /dev/null || aw_fail "diff-size: repository chưa có commit nào"
counted() { grep -v -E '(^|/)(docs/|node_modules/|target/|dist/|build/)|\.md$|(package-lock\.json|pnpm-lock\.yaml|yarn\.lock)$|\.min\.(js|css)$|\.(lock|map|svg|png|jpg)$'; }
rows=$(mktemp)
trap 'rm -f "$rows"' EXIT
git diff --numstat --ignore-all-space HEAD | awk -F'\t' '$1 != "-" { print $1 + $2 "\t" $3 }' | while IFS="$(printf '\t')" read -r n f; do
  printf '%s\n' "$f" | counted > /dev/null && printf '%s\t%s\n' "$n" "$f"
done > "$rows" || true
git ls-files --others --exclude-standard | counted | while IFS= read -r f; do
  [ -f "$f" ] && printf '%s\t%s\n' "$(wc -l < "$f" | tr -d ' ')" "$f"
done >> "$rows" || true
total=$(awk -F'\t' '{ s += $1 } END { print s + 0 }' "$rows")
files=$(wc -l < "$rows" | tr -d ' ')
biggest=$(sort -rn "$rows" | head -n 1)
big_n=${biggest%%	*} big_f=${biggest#*	}
breach=""
[ "$total" -le "$max_loc" ] || breach="$breach tổng $total dòng đổi (ngưỡng $max_loc);"
[ "$files" -le "$max_files" ] || breach="$breach $files file đổi (ngưỡng $max_files);"
[ -z "$biggest" ] || [ "$big_n" -le "$max_file_loc" ] || breach="$breach $big_f đổi $big_n dòng (ngưỡng một file $max_file_loc);"
if [ -n "$breach" ]; then
  echo "diff-size: diff lớn:$breach" >&2
  echo "WHY: diff lớn khó review và hay chứa phần thừa; làm gọn trước khi qua bước review." >&2
  echo "FIX: làm gọn code (bỏ lặp, bỏ phần thừa, đơn giản hóa) mà KHÔNG đổi hành vi, rồi chạy lại các kiểm tra; không xóa test." >&2
  exit 1
fi
echo "diff-size: $total dòng đổi, $files file: trong ngưỡng"
