#!/bin/sh
# COMMAND "npm-test": install dependencies, run the tests and the build of the frontend in the frontend/ directory of the worktree.
# When a step is red, aw_step (lib.sh) prints the end of the output of exactly the failed step to stderr, without colour.
set -eu
. "${AW_KIT:?set AW_KIT=<kit directory> when running by hand}/commands/lib.sh" # @aw-include
AW_STEP_HEAD="frontend: step"
[ -f frontend/package.json ] || aw_fail "npm-test: frontend/package.json does not exist (scaffold the frontend first)"
cd frontend
# Installing dependencies needs the network: a network or certificate error is an error of the machine running the step (aw_env_fail), not sent to the agent as a code error.
AW_ENV_ERRORS='SELF_SIGNED_CERT|UNABLE_TO_VERIFY|CERT_|ENOTFOUND|EAI_AGAIN|ECONNRESET|ECONNREFUSED|ETIMEDOUT|ERR_SOCKET_TIMEOUT|network request|command not found|is not recognized'
if [ -f package-lock.json ]; then
  aw_step "npm ci" npm ci --no-audit --no-fund --fetch-retries=2 --fetch-retry-maxtimeout=20000
else
  aw_step "npm install" npm install --no-audit --no-fund --fetch-retries=2 --fetch-retry-maxtimeout=20000
fi
AW_ENV_ERRORS=
aw_step "npm test" npm test
aw_step "npm run build" npm run build
echo "frontend: tests and build PASS"
