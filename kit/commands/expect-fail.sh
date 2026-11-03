#!/bin/sh
# COMMAND "expect-fail": the step between "write a test that reproduces the bug" and "fix the bug" in wf-bugfix. It INVERTS the check: it passes (exit 0) when the
# test suite is RED because of a real test failure, and fails (exit 1) when the tests are green (the test does not reach the bug) or red for another reason
# (does not compile, missing module, network). This way the agent fixing the bug cannot start from a test that does not prove the bug.
# Detects the stack like maven-test.sh and npm-test.sh: backend/pom.xml (Maven) or frontend/package.json (npm), run at the worktree root.
set -eu
. "${AW_KIT:?set AW_KIT=<kit directory> when running by hand}/commands/lib.sh" # @aw-include
ENV_ERRORS='PKIX path|Could not transfer artifact|UnknownHostException|Connection (timed out|refused)|Temporary failure in name resolution|Network is unreachable|SELF_SIGNED_CERT|UNABLE_TO_VERIFY|ENOTFOUND|EAI_AGAIN|ECONNRESET|ECONNREFUSED|ETIMEDOUT'
NOT_A_TEST_FAILURE='COMPILATION ERROR|cannot find symbol|SyntaxError|Cannot find module|error TS[0-9]+|Failed to resolve import|ReferenceError'
# V12-07: the lines that say WHERE a compile error is (javac "File.java:[12,5] ...", tsc "file.ts(12,5): ..."), printed with the
# reason so the agent knows what to fix - "[ERROR] COMPILATION ERROR :" alone said nothing.
COMPILE_LOCATION='\.(java|kt|scala|ts|tsx|js|jsx|mjs)(:\[[0-9]+,[0-9]+\]|\([0-9]+,[0-9]+\)|:[0-9]+:[0-9]+)'
# V12-07: a red test is accepted only when it fails at an ASSERTION. A test that dies while building its data (a constraint
# violation, an NPE in @BeforeEach, a context that does not start) proves nothing about the bug.
SETUP_ERRORS='DataIntegrityViolationException|ConstraintViolationException|SQLIntegrityConstraintViolation|violates (unique|not-null|foreign key|check) constraint|UNIQUE constraint failed|NOT NULL constraint failed|FOREIGN KEY constraint failed|Failed to load ApplicationContext|BeanCreationException|ExceptionInInitializerError|@BeforeEach|@BeforeAll|beforeEach|beforeAll'
ASSERTIONS='AssertionError|AssertionFailedError|ComparisonFailure|expected:? *<|Expected:|expect\(|toBe|toEqual|toHaveBeenCalled|expected .* (to|but)'
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
  grep -E "$NOT_A_TEST_FAILURE|$COMPILE_LOCATION" "$log" | grep -v -E '^\[ERROR\] *(COMPILATION ERROR *:?)? *$' | head -n 12 | cut -c1-300 >&2
  exit 1
fi
# Maven: the last "Tests run: n, Failures: f, Errors: e" line is the summary. Errors without failures are exceptions, not assertions.
failures=0 errors=0
if [ "$stack" = backend ]; then
  counts=$(grep -E 'Tests run: *[0-9]+, *Failures: *[0-9]+, *Errors: *[0-9]+' "$log" | tail -n 1 | sed -E 's/.*Failures: *([0-9]+), *Errors: *([0-9]+).*/\1 \2/')
  [ -z "$counts" ] || { failures=${counts% *} errors=${counts#* }; }
fi
not_assertion=""
if [ "$stack" = backend ] && [ "$failures" = 0 ] && [ "$errors" != 0 ]; then
  not_assertion="$errors test(s) ended with an exception, none failed an assertion"
elif [ "$failures" = 0 ] && grep -q -E "$SETUP_ERRORS" "$log"; then
  not_assertion="the tests died while preparing their data or context"
elif [ "$stack" = frontend ] && ! grep -q -E "$ASSERTIONS" "$log"; then
  not_assertion="no assertion failed (the output names no expected/received value)"
fi
if [ -n "$not_assertion" ]; then
  echo "expect-fail: $stack: the tests are red, but not at an assertion: $not_assertion. A test that crashes while setting up proves nothing about the bug: fix the test data or setup so the test reaches its assertion, and let THAT fail because of the bug. Lines:" >&2
  grep -E "$SETUP_ERRORS|Exception|Error:|Tests run:" "$log" | head -n 10 | cut -c1-300 >&2
  exit 1
fi
echo "expect-fail: $stack: the tests are RED as expected (bug not fixed yet). Failing lines:"
grep -E "$summary" "$log" | head -n 15 | cut -c1-300 || true
exit 0
