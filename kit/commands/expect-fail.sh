#!/bin/sh
# COMMAND "expect-fail": the step between "write a test that reproduces the bug" and "fix the bug" in wf-bugfix. It INVERTS the check: it passes (exit 0) when the
# test suite is RED because of a real test failure, and fails (exit 1) when the tests are green (the test does not reach the bug) or red for another reason
# (does not compile, missing module, network). This way the agent fixing the bug cannot start from a test that does not prove the bug.
# Detects the stack like maven-test.sh and npm-test.sh: backend/pom.xml (Maven) or frontend/package.json (npm), run at the worktree root.
set -eu
. "${AW_KIT:?set AW_KIT=<kit directory> when running by hand}/commands/lib.sh" # @aw-include
ENV_ERRORS='PKIX path|Could not transfer artifact|UnknownHostException|Connection (timed out|refused)|Temporary failure in name resolution|Network is unreachable|SELF_SIGNED_CERT|UNABLE_TO_VERIFY|ENOTFOUND|EAI_AGAIN|ECONNRESET|ECONNREFUSED|ETIMEDOUT'
NOT_A_TEST_FAILURE='COMPILATION ERROR|cannot find symbol|SyntaxError|Cannot find module|error TS[0-9]+|Failed to resolve import|ReferenceError'
log=$(mktemp)
trap 'rm -f "$log"' EXIT
# Failure marks printed by the JavaScript test runners (a cross and a multiplication sign), written as octal escapes: this script is ASCII only.
cross=$(printf '\342\234\227')
times=$(printf '\303\227')

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
  summary="(FAIL|$cross|$times|AssertionError|expected)"
  run() {
    if [ -f package-lock.json ]; then npm ci --no-audit --no-fund --fetch-retries=2 --fetch-retry-maxtimeout=20000; else npm install --no-audit --no-fund --fetch-retries=2 --fetch-retry-maxtimeout=20000; fi
    npm test
  }
else
  aw_fail "expect-fail: neither backend/pom.xml nor frontend/package.json exists to run tests"
fi

if run > "$log" 2>&1; then
  echo "expect-fail: $stack: ALL tests are GREEN. The test that reproduces the bug does not reach the bug: fix the test itself (input, condition or assertion) until it is red exactly because of the described bug; do not conclude the bug does not exist." >&2
  exit 1
fi
if grep -q -E "$ENV_ERRORS" "$log"; then
  grep -E "$ENV_ERRORS" "$log" | head -n 4 | cut -c1-300 >&2
  aw_env_fail "expect-fail: $stack cannot run the tests because of the network or a certificate on the machine running it"
fi
if grep -q -E "$NOT_A_TEST_FAILURE" "$log"; then
  echo "expect-fail: $stack: the tests are red because the code does not compile or load, not because of the bug to reproduce. Make the test run, then let it fail for the right reason:" >&2
  grep -E "$NOT_A_TEST_FAILURE" "$log" | head -n 8 | cut -c1-300 >&2
  exit 1
fi
echo "expect-fail: $stack: the tests are RED as expected (bug not fixed yet). Failing lines:"
grep -E "$summary" "$log" | head -n 15 | cut -c1-300 || true
exit 0
