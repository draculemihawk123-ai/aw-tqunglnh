#!/bin/sh
# COMMAND node "frontend-test": chạy trong thư mục gốc của worktree repository todolist.
# Khi một bước đỏ, agent được gửi lại 4 KiB cuối của STDERR (checkFailures.why): aw_step (kit/commands/lib.sh) in phần
# cuối output của đúng bước hỏng ra stderr, không màu.
set -eu
. "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/lib.sh" # @aw-include
AW_STEP_HEAD="frontend: bước"
cd frontend
if [ -f package-lock.json ]; then
  aw_step "npm ci" npm ci --no-audit --no-fund
else
  aw_step "npm install" npm install --no-audit --no-fund
fi
aw_step "npm test" npm test
aw_step "npm run build" npm run build
echo "frontend: test và build PASS"
