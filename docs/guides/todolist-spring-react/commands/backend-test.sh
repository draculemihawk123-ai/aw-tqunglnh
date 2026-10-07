#!/bin/sh
# COMMAND node "backend-test": chạy trong thư mục gốc của worktree repository todolist.
# Khi test đỏ, agent được gửi lại 4 KiB cuối của STDERR (checkFailures.why). Maven in lỗi ra stdout, nên script
# gom output lại và in phần lỗi cô đọng ra stderr; báo cáo chi tiết nằm ở backend/target/surefire-reports.
set -eu
cd backend
mvn=mvn
[ -x ./mvnw ] && mvn=./mvnw
log=$(mktemp)
if "$mvn" -B -q test > "$log" 2>&1; then
  rm -f "$log"
  echo "backend: test PASS"
  exit 0
fi
grep -E '^\[ERROR\]' "$log" \
  | grep -v -E 'Re-run Maven|For more information|\[Help 1\]|See dump files|To see the full stack trace|^\[ERROR\] *$' \
  | cut -c1-400 | head -n 40 >&2 || true
echo "Chi tiết từng test: backend/target/surefire-reports/*.txt" >&2
rm -f "$log"
exit 1
