# Thư viện kiểm cấu trúc tài liệu tính năng (không chạy trực tiếp). Nạp bằng:
#   . "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/docs-lib.sh" # @aw-include
# Cần lib.sh nạp trước (aw_fail). Khung tiêu đề do skill-doc-templates quy định; ở đây chỉ kiểm máy: tiêu đề (## … ####) có từ khóa
# bắt buộc, mã mục (A-n, BR-n, AC-n, D-n) có mặt, AC có When/Then, AC của tasks.json có trong spec, mỗi AC của spec có test trong thiết kế.
# Lỗi in theo dạng THIẾU/SAI + WHY + FIX vào biến docs_errors; docs_report in tất cả rồi thoát mã 1 nếu có.
docs_errors=""
docs_err() { docs_errors="${docs_errors}$*
"; }
docs_report() {
  [ -z "$docs_errors" ] && return 0
  printf '%s' "$docs_errors" >&2
  echo "WHY: các tài liệu sau được người duyệt và các giai đoạn sau đọc theo khung cố định; thiếu mục thì họ phải đoán. FIX: bổ sung đúng các mục trên theo khung (skill-doc-templates), mục không áp dụng thì giữ tiêu đề và ghi \"Không áp dụng: <lý do>\"." >&2
  exit 1
}
# docs_dirs: thư mục tính năng (docs/features/<tên>) có file mới hoặc được sửa trong worktree.
docs_dirs() {
  git status --porcelain --untracked-files=all -- docs/features \
    | sed -E 's#^.{3}##; s#^"##' | awk -F/ 'NF >= 3 { print $1 "/" $2 "/" $3 }' | sort -u
}
# docs_head <file> <tên mục> <regex viết thường>: tiêu đề của file phải khớp regex.
docs_head() {
  grep -E '^#{2,4} ' "$1" | tr 'A-Z' 'a-z' | sed 's/Đ/đ/g' | grep -q -E "$3" || docs_err "THIẾU mục \"$2\" trong $1 (tiêu đề ## chứa: $3)"
}
# docs_ids <file> <tiền tố>: các mã tiền tố-số (ví dụ AC) có trong file, mỗi mã một dòng, không trùng.
docs_ids() { grep -o -E "\b$2-[0-9]+\b" "$1" | sort -u || true; }
# docs_need_ids <file> <tiền tố> <tối thiểu> <tên>
docs_need_ids() {
  n=$(docs_ids "$1" "$2" | wc -l | tr -d ' ')
  [ "$n" -ge "$3" ] || docs_err "THIẾU mã $2-n trong $1: có $n, cần ít nhất $3 ($4)"
}
# docs_ac_gwt <spec>: mỗi AC-n phải có When và Then (Given khi có tiền điều kiện) trong khối của nó (từ dòng nhắc AC-n đầu tiên tới AC kế tiếp hoặc tiêu đề).
docs_ac_gwt() {
  awk '
    function flush() { if (id != "" && !(w && t)) bad = bad " " id; }
    /^#+ / && id != "" { flush(); id = "" }
    {
      line = tolower($0)
      if (match($0, /^[ \t]*([-*]|[0-9]+[.)]|\|)?[ \t]*(\*\*|#+ )?AC-[0-9]+/)) {
        flush(); id = substr($0, RSTART, RLENGTH); sub(/^[^A]*/, "", id); g = w = t = 0
      }
      if (id != "") { if (line ~ /given/) g = 1; if (line ~ /when/) w = 1; if (line ~ /then/) t = 1 }
    }
    END { flush(); if (bad != "") print bad }' "$1"
}
# docs_section <file> <regex viết thường trên tiêu đề>: nội dung các mục có tiêu đề khớp (tới tiêu đề cùng cấp hoặc cao hơn kế tiếp).
docs_section() {
  awk -v re="$2" '
    /^##+ / { h = tolower($0); gsub(/Đ/, "đ", h); level = match($0, /[^#]/) - 1
      if (on && level <= on_level) on = 0
      if (h ~ re) { on = 1; on_level = level; next } }
    on { print }' "$1"
}
# docs_cover <tiền tố>: đọc văn bản từ stdin, in mọi mã tiền tố-số được nhắc, kể cả khoảng "AC-2..5", "AC-2–5", "AC-2 đến AC-5", "AC-2-AC-5".
docs_cover() {
  awk -v p="$1" '{
    s = $0
    while (match(s, p "-[0-9]+")) {
      a = substr(s, RSTART + length(p) + 1, RLENGTH - length(p) - 1) + 0
      rest = substr(s, RSTART + RLENGTH); b = a
      if (match(rest, /^[ ]*(\.\.|…|–|—|-|~|đến|to)[ ]*/)) {
        r2 = substr(rest, RSTART + RLENGTH)
        if (substr(r2, 1, length(p) + 1) == p "-") r2 = substr(r2, length(p) + 2)
        if (match(r2, /^[0-9]+/)) { b = substr(r2, RSTART, RLENGTH) + 0 }
      }
      if (b < a || b - a > 200) b = a
      for (i = a; i <= b; i++) print p "-" i
      s = rest
    }
  }' | sort -u
}
