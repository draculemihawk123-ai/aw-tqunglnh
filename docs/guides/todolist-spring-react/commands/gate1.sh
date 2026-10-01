#!/bin/sh
# COMMAND "gate1" (GATE 1: build + test): chạy test cho phần code mà task đã đổi (so với HEAD của
# worktree); nếu task không đổi code thì chạy test của mọi phần đang có. Chạy ở gốc worktree.
set -eu
changed() { [ -n "$(git status --porcelain --untracked-files=all -- "$1")" ]; }
run_backend() {
  echo "== GATE 1: backend"
  (cd backend && if [ -x ./mvnw ]; then ./mvnw -B -q test; else mvn -B -q test; fi)
}
run_frontend() {
  echo "== GATE 1: frontend"
  (cd frontend
   if [ -f package-lock.json ]; then npm ci --no-audit --no-fund; else npm install --no-audit --no-fund; fi
   npm test
   npm run build)
}
ran=0
if [ -f backend/pom.xml ] && changed backend; then run_backend; ran=1; fi
if [ -f frontend/package.json ] && changed frontend; then run_frontend; ran=1; fi
if [ "$ran" = 0 ]; then
  [ -f backend/pom.xml ] && run_backend
  [ -f frontend/package.json ] && run_frontend
fi
echo "GATE 1: PASS"
