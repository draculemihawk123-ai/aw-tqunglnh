# Thư viện dùng chung cho script của node COMMAND (không chạy trực tiếp). Nạp bằng đúng một dòng ở đầu script:
#
#   . "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/lib.sh" # @aw-include
#
# Khi publish, aw-publish.py thay dòng đó bằng chính nội dung file này, nên script đã publish tự chứa đủ và hash của
# script đổi theo thư viện. Khi chạy tay (AW_KIT trỏ tới thư mục kit) dòng đó nạp file thật.
#
# Hợp đồng của script COMMAND (README mục 3.4): mã thoát 0 là đạt; lỗi in ra STDERR vì agent chỉ nhận 4 KiB cuối của
# stderr; không màu (NO_COLOR=1) để mã ANSI không chiếm chỗ trong 4 KiB đó. Các hàm dưới đây thực hiện hợp đồng ấy.
export NO_COLOR=1 CI=true

# aw_fail <thông báo…>: in lỗi của code ra stderr rồi thoát mã 1 (agent sẽ sửa code).
aw_fail() { echo "$@" >&2; exit 1; }

# aw_env_fail <thông báo…>: lỗi của MÁY chạy, không sửa được bằng code. Mã thoát không phân biệt được hai loại lỗi,
# nên thông báo bắt đầu bằng "MÔI TRƯỜNG:" và dặn agent dừng bằng outcome needs_info thay vì sửa code vô ích.
aw_env_fail() {
  echo "MÔI TRƯỜNG: $* — không phải lỗi code; đừng sửa code, hãy kết thúc bằng outcome needs_info và nêu rõ." >&2
  exit 1
}

# aw_step <tên bước> <lệnh…>: chạy lệnh; đạt thì im lặng. Hỏng thì in "<AW_STEP_HEAD> '<tên>' không đạt" cùng
# ${AW_TAIL:-60} dòng cuối của output (mỗi dòng cắt 300 ký tự) ra stderr rồi thoát mã 1.
# AW_STEP_HEAD (mặc định "bước") là phần đầu thông báo, ví dụ "frontend: bước".
aw_step() {
  _aw_name=$1; shift
  _aw_log=$(mktemp)
  if "$@" > "$_aw_log" 2>&1; then rm -f "$_aw_log"; return 0; fi
  echo "${AW_STEP_HEAD:-bước} '$_aw_name' không đạt" >&2
  tail -n "${AW_TAIL:-60}" "$_aw_log" | cut -c1-300 >&2
  rm -f "$_aw_log"
  exit 1
}

# aw_wait_http <url> <tên> <file log> [số giây, mặc định 120]: chờ tới khi url trả lời HTTP (cần curl).
# Quá hạn thì in lỗi kèm 40 dòng cuối của file log rồi thoát mã 1.
aw_wait_http() {
  _aw_i=0
  until curl -s -o /dev/null --max-time 2 "$1"; do
    _aw_i=$((_aw_i + 1))
    if [ "$_aw_i" -gt "${4:-120}" ]; then
      echo "$2 không lên sau ${4:-120} giây" >&2
      tail -n 40 "$3" | cut -c1-300 >&2
      exit 1
    fi
    sleep 1
  done
}
