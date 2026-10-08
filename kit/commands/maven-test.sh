#!/bin/sh
# COMMAND "maven-test": chạy test của backend Maven trong thư mục backend/ của worktree (gốc worktree là thư mục làm việc).
# Khi test đỏ, agent được gửi lại 4 KiB cuối của STDERR (checkFailures.why). Maven in lỗi ra stdout, nên script gom output
# lại và in phần lỗi cô đọng ra stderr; báo cáo chi tiết nằm ở backend/target/surefire-reports.
set -eu
. "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/lib.sh" # @aw-include
[ -f backend/pom.xml ] || aw_fail "maven-test: không có backend/pom.xml"
cd backend
mvn=mvn
[ -x ./mvnw ] && mvn=./mvnw
log=$(mktemp)
if "$mvn" -B -q test > "$log" 2>&1; then
  rm -f "$log"
  echo "backend: test PASS"
  exit 0
fi
# Maven không tải được dependency (mạng, chứng chỉ) là lỗi của máy chạy, không phải lỗi code.
if grep -q -E 'PKIX path|Could not transfer artifact|UnknownHostException|Connection (timed out|refused)|Temporary failure in name resolution|Network is unreachable' "$log"; then
  grep -E 'PKIX path|Could not transfer artifact|UnknownHostException|Connection (timed out|refused)|Temporary failure|Network is unreachable' "$log" | head -n 4 | cut -c1-300 >&2
  rm -f "$log"
  aw_env_fail "Maven không tải được dependency (mạng hoặc chứng chỉ của máy chạy)"
fi
# Nguyên nhân gốc trước: Surefire in stack trace KHÔNG có tiền tố [ERROR], nên lọc chỉ theo [ERROR] làm mất dòng "Caused by"
# và agent chỉ thấy danh sách test đỏ mà không biết vì sao (ví dụ ApplicationContext không nạp được). Lấy các dòng nguyên nhân,
# bỏ trùng, rồi mới tới danh sách test.
causes=$(grep -E '^(Caused by:|[A-Za-z0-9_.$]+(Exception|Error)\b|APPLICATION FAILED TO START|Description:|Action:|Message: )' "$log" \
  | cut -c1-400 | awk '!seen[$0]++' | head -n 14 || true)
if [ -n "$causes" ]; then
  echo "Nguyên nhân (các dòng khác nhau đầu tiên trong stack trace):" >&2
  printf '%s\n' "$causes" >&2
fi
grep -E '^\[ERROR\]' "$log" \
  | grep -v -E 'Re-run Maven|For more information|\[Help 1\]|See dump files|To see the full stack trace|^\[ERROR\] *$' \
  | cut -c1-400 | head -n 25 >&2 || true
echo "Chi tiết từng test: backend/target/surefire-reports/*.txt" >&2
rm -f "$log"
exit 1
