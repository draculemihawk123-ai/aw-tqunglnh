#!/bin/sh
# COMMAND node "frontend-test": chạy trong thư mục gốc của worktree repository todolist.
# Khi một bước đỏ, agent được gửi lại 4 KiB cuối của STDERR (checkFailures.why), nên script in phần cuối output
# của đúng bước hỏng ra stderr.
set -eu
# Không màu: mã màu ANSI của Vitest chiếm chỗ trong 4 KiB mà agent nhận.
export NO_COLOR=1 CI=true
cd frontend
step() {  # step <tên> <lệnh…>: chạy lệnh; khi hỏng in 60 dòng cuối ra stderr rồi dừng
  name=$1; shift
  log=$(mktemp)
  if "$@" > "$log" 2>&1; then rm -f "$log"; return 0; fi
  echo "frontend: bước '$name' không đạt" >&2
  tail -n 60 "$log" | cut -c1-300 >&2
  rm -f "$log"
  exit 1
}
if [ -f package-lock.json ]; then
  step "npm ci" npm ci --no-audit --no-fund
else
  step "npm install" npm install --no-audit --no-fund
fi
step "npm test" npm test
step "npm run build" npm run build
echo "frontend: test và build PASS"
