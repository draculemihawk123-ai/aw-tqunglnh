#!/bin/sh
# COMMAND "expect-fail": bước giữa "viết test tái hiện lỗi" và "sửa lỗi" của wf-bugfix. ĐẢO nghĩa kiểm tra: đạt (mã 0) khi bộ test
# đang ĐỎ vì một lỗi test thật, không đạt (mã 1) khi test xanh (test chưa chạm tới lỗi) hoặc đỏ vì lý do khác (không biên dịch
# được, thiếu module, mạng). Nhờ vậy agent sửa lỗi không thể bắt đầu từ một test không chứng minh được lỗi.
# Nhận diện stack như maven-test.sh và npm-test.sh: backend/pom.xml (Maven) hoặc frontend/package.json (npm), chạy ở gốc worktree.
set -eu
. "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/lib.sh" # @aw-include
ENV_ERRORS='PKIX path|Could not transfer artifact|UnknownHostException|Connection (timed out|refused)|Temporary failure in name resolution|Network is unreachable|SELF_SIGNED_CERT|UNABLE_TO_VERIFY|ENOTFOUND|EAI_AGAIN|ECONNRESET|ECONNREFUSED|ETIMEDOUT'
NOT_A_TEST_FAILURE='COMPILATION ERROR|cannot find symbol|SyntaxError|Cannot find module|error TS[0-9]+|Failed to resolve import|ReferenceError'
log=$(mktemp)
trap 'rm -f "$log"' EXIT

if [ -f backend/pom.xml ]; then
  cd backend
  mvn=mvn
  [ -x ./mvnw ] && mvn=./mvnw
  stack=backend
  summary='^\[ERROR\] +(Tests run|[A-Za-z0-9_.$]+(Test|Tests|IT)\b|.*(FAIL|expected))'
  run() { "$mvn" -B -q test; }
elif [ -f frontend/package.json ]; then
  cd frontend
  stack=frontend
  summary='(FAIL|✗|×|AssertionError|expected)'
  run() {
    if [ -f package-lock.json ]; then npm ci --no-audit --no-fund --fetch-retries=2 --fetch-retry-maxtimeout=20000; else npm install --no-audit --no-fund --fetch-retries=2 --fetch-retry-maxtimeout=20000; fi
    npm test
  }
else
  aw_fail "expect-fail: không có backend/pom.xml hay frontend/package.json để chạy test"
fi

if run > "$log" 2>&1; then
  echo "expect-fail: $stack: toàn bộ test đang XANH. Test tái hiện lỗi chưa chạm tới lỗi: sửa chính test (đầu vào, điều kiện hoặc khẳng định) cho tới khi nó đỏ đúng vì lỗi đã mô tả; đừng kết luận lỗi không tồn tại." >&2
  exit 1
fi
if grep -q -E "$ENV_ERRORS" "$log"; then
  grep -E "$ENV_ERRORS" "$log" | head -n 4 | cut -c1-300 >&2
  aw_env_fail "expect-fail: $stack không chạy được test vì mạng hoặc chứng chỉ của máy chạy"
fi
if grep -q -E "$NOT_A_TEST_FAILURE" "$log"; then
  echo "expect-fail: $stack: test đỏ vì code không biên dịch hoặc không nạp được, không phải vì lỗi cần tái hiện. Sửa test cho chạy được rồi để nó đỏ vì đúng lỗi:" >&2
  grep -E "$NOT_A_TEST_FAILURE" "$log" | head -n 8 | cut -c1-300 >&2
  exit 1
fi
echo "expect-fail: $stack: test đang ĐỎ như mong đợi (chưa sửa lỗi). Các dòng thất bại:"
grep -E "$summary" "$log" | head -n 15 | cut -c1-300 || true
exit 0
