#!/bin/sh
# COMMAND "temp-artifacts" (V10-15): chặn rác để quên trong diff chưa commit: lệnh gỡ lỗi, test bị tắt hoặc chỉ chạy một phần, TODO mới.
# Chỉ xét các dòng THÊM VÀO (diff so với HEAD và file chưa theo dõi), không xét code cũ. Mỗi lỗi in theo dạng WHAT / WHY / FIX
# vì agent chỉ nhận 4 KiB cuối của stderr. Thoát mã 1 nếu có.
set -eu
. "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/lib.sh" # @aw-include
git rev-parse --verify -q HEAD > /dev/null || aw_fail "temp-artifacts: repository chưa có commit nào"
added=$(mktemp)
trap 'rm -f "$added"' EXIT
skip='(^|/)(docs/|node_modules/|target/|dist/|build/)|\.md$|(package-lock\.json|pnpm-lock\.yaml|yarn\.lock)$|^kit/|(^|/)commands/[^/]*\.sh$'
{
  git diff -U0 --no-color HEAD | awk '
    /^\+\+\+ b\// { file = substr($0, 7); next }
    /^\+\+\+ / { file = ""; next }
    /^@@/ { match($0, /\+[0-9]+/); line = substr($0, RSTART + 1, RLENGTH - 1) + 0; next }
    /^\+/ { if (file != "") printf "%s:%d:%s\n", file, line, substr($0, 2); line++ }'
  git ls-files --others --exclude-standard | while IFS= read -r f; do
    [ -f "$f" ] && awk -v f="$f" '{ printf "%s:%d:%s\n", f, NR, $0 }' "$f"
  done
} | grep -v -E "^($skip)" > "$added" || true
found=0
report() { # <mã> <regex> <WHAT> <WHY> <FIX>
  hits=$(grep -E -e "^[^:]+:[0-9]+:.*($2)" "$added" | head -n 5 | cut -c1-200 || true)
  [ -n "$hits" ] || return 0
  found=1
  echo "WHAT: $3" >&2
  printf '%s\n' "$hits" | sed 's/^/  /' >&2
  echo "WHY: $4" >&2
  echo "FIX: $5" >&2
}
report debugger '(^|[^A-Za-z_])debugger;|System\.out\.print|printStackTrace\(\)|console\.(log|debug)\(' \
  "lệnh gỡ lỗi để quên (debugger, console.log, System.out, printStackTrace)" \
  "log gỡ lỗi làm bẩn output và có thể lộ dữ liệu" "xóa các dòng trên, hoặc đổi sang logger của dự án nếu cần giữ"
report only '(describe|it|test|context)\.only\(|(^|[^A-Za-z_])f(describe|it)\(' \
  "test chỉ chạy một phần (.only, fit, fdescribe)" \
  "các test còn lại bị bỏ qua nên bước kiểm tra xanh giả" "bỏ .only và chạy lại toàn bộ test"
report disabled '@Disabled|@Ignore\b|(describe|it|test)\.skip\(|(^|[^A-Za-z_])x(describe|it)\(' \
  "test bị tắt (@Disabled, @Ignore, .skip, xit)" \
  "test tắt che lỗi thật, và tắt test để được xanh là lối tắt bị cấm" "bật lại test và sửa code cho test đạt; nếu test thật sự sai thì sửa test và nêu lý do trong báo cáo"
report todo '(TODO|FIXME|XXX)([^A-Za-z]|$)' \
  "TODO/FIXME mới thêm vào code" \
  "việc dở dang bị chôn trong code, không ai theo dõi" "làm xong ngay, hoặc ghi vào báo cáo cuối của bạn thay vì để trong code"
[ "$found" = 0 ] || exit 1
echo "temp-artifacts: không có rác trong các dòng thêm vào"
