#!/bin/sh
# COMMAND "npm-test": cài dependency, chạy test và build của frontend trong thư mục frontend/ của worktree.
# Khi một bước đỏ, aw_step (lib.sh) in phần cuối output của đúng bước hỏng ra stderr, không màu.
set -eu
. "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/lib.sh" # @aw-include
AW_STEP_HEAD="frontend: bước"
[ -f frontend/package.json ] || aw_fail "npm-test: không có frontend/package.json (scaffold frontend trước)"
cd frontend
# Cài dependency cần mạng: lỗi mạng hoặc chứng chỉ là lỗi của máy chạy (aw_env_fail), không gửi cho agent như lỗi code.
AW_ENV_ERRORS='SELF_SIGNED_CERT|UNABLE_TO_VERIFY|CERT_|ENOTFOUND|EAI_AGAIN|ECONNRESET|ECONNREFUSED|ETIMEDOUT|ERR_SOCKET_TIMEOUT|network request'
if [ -f package-lock.json ]; then
  aw_step "npm ci" npm ci --no-audit --no-fund --fetch-retries=2 --fetch-retry-maxtimeout=20000
else
  aw_step "npm install" npm install --no-audit --no-fund --fetch-retries=2 --fetch-retry-maxtimeout=20000
fi
AW_ENV_ERRORS=
aw_step "npm test" npm test
aw_step "npm run build" npm run build
echo "frontend: test và build PASS"
