#!/bin/sh
# COMMAND node "frontend-test": chạy trong thư mục gốc của worktree repository todolist.
set -eu
cd frontend
if [ -f package-lock.json ]; then
  npm ci --no-audit --no-fund
else
  npm install --no-audit --no-fund
fi
npm test
npm run build
