#!/bin/sh
# COMMAND "gate1" (GATE 1: build + test): chạy test cho phần code mà task đã đổi (so với HEAD của
# worktree); nếu task không đổi code thì chạy test của mọi phần đang có. Chạy ở gốc worktree.
# Khi một bước đỏ, agent được gửi lại 4 KiB cuối của STDERR (checkFailures.why), nên phần lỗi được in ra stderr.
set -eu
# Không màu: mã màu ANSI của Vitest chiếm chỗ trong 4 KiB mà agent nhận.
export NO_COLOR=1 CI=true
changed() { [ -n "$(git status --porcelain --untracked-files=all -- "$1")" ]; }
run_backend() {
  log=$(mktemp)
  mvn=mvn
  [ -x backend/mvnw ] && mvn=./mvnw
  if (cd backend && "$mvn" -B -q test) > "$log" 2>&1; then rm -f "$log"; echo "GATE 1: backend PASS"; return 0; fi
  echo "GATE 1: backend không đạt" >&2
  grep -E '^\[ERROR\]' "$log" \
    | grep -v -E 'Re-run Maven|For more information|\[Help 1\]|See dump files|To see the full stack trace|^\[ERROR\] *$' \
    | cut -c1-400 | head -n 40 >&2 || true
  echo "Chi tiết từng test: backend/target/surefire-reports/*.txt" >&2
  rm -f "$log"
  exit 1
}
frontend_step() {
  name=$1; shift
  log=$(mktemp)
  if (cd frontend && "$@") > "$log" 2>&1; then rm -f "$log"; return 0; fi
  echo "GATE 1: frontend, bước '$name' không đạt" >&2
  tail -n 60 "$log" | cut -c1-300 >&2
  rm -f "$log"
  exit 1
}
run_frontend() {
  if [ -f frontend/package-lock.json ]; then
    frontend_step "npm ci" npm ci --no-audit --no-fund
  else
    frontend_step "npm install" npm install --no-audit --no-fund
  fi
  frontend_step "npm test" npm test
  frontend_step "npm run build" npm run build
  echo "GATE 1: frontend PASS"
}
ran=0
if [ -f backend/pom.xml ] && changed backend; then run_backend; ran=1; fi
if [ -f frontend/package.json ] && changed frontend; then run_frontend; ran=1; fi
if [ "$ran" = 0 ]; then
  [ -f backend/pom.xml ] && run_backend
  [ -f frontend/package.json ] && run_frontend
fi
echo "GATE 1: PASS"
