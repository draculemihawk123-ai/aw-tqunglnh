#!/bin/sh
# COMMAND "maven-test": run the tests of the Maven backend in the backend/ directory of the worktree (the worktree root is the working directory).
# When the tests are red the agent is sent back the last 4 KiB of STDERR (checkFailures.why). Maven prints errors to stdout, so the script collects the
# output and prints a condensed error part to stderr; the detailed report is in backend/target/surefire-reports.
set -eu
. "${AW_KIT:?set AW_KIT=<kit directory> when running by hand}/commands/lib.sh" # @aw-include
[ -f backend/pom.xml ] || aw_fail "maven-test: backend/pom.xml does not exist"
cd backend
mvn=mvn
[ -x ./mvnw ] && mvn=./mvnw
log=$(mktemp)
code=0
"$mvn" -B -q test > "$log" 2>&1 || code=$?
if [ "$code" = 0 ]; then
  rm -f "$log"
  echo "backend: tests PASS"
  exit 0
fi
# mvn not runnable (not in PATH, wrong JAVA_HOME) is an error of the machine running the step, not of the code: report it as ENVIRONMENT so the
# agent stops with needs_info instead of changing code in vain (a real run once cost 5.87 USD because Maven was missing).
if [ "$code" = 127 ] || grep -q -i -E 'mvn: command not found|command not found|is not recognized|JAVA_HOME (environment variable )?(is|not)|JAVA_HOME.*(invalid|not defined|not found)' "$log"; then
  head -n 4 "$log" | cut -c1-300 >&2
  rm -f "$log"
  aw_env_fail "mvn cannot run in the worker environment (mvn is not in PATH or JAVA_HOME is wrong)"
fi
# Maven cannot download dependencies (network, certificate): an error of the machine running the step, not of the code.
if grep -q -E 'PKIX path|Could not transfer artifact|UnknownHostException|Connection (timed out|refused)|Temporary failure in name resolution|Network is unreachable' "$log"; then
  grep -E 'PKIX path|Could not transfer artifact|UnknownHostException|Connection (timed out|refused)|Temporary failure|Network is unreachable' "$log" | head -n 4 | cut -c1-300 >&2
  rm -f "$log"
  aw_env_fail "Maven cannot download dependencies (network or certificate of the machine running it)"
fi
# Root cause first: Surefire prints stack traces WITHOUT the [ERROR] prefix, so filtering on [ERROR] alone loses the "Caused by" lines and
# the agent sees only the list of red tests without knowing why (for example an ApplicationContext that cannot load). Take the cause lines,
# remove duplicates, then the list of tests.
causes=$(grep -E '^(Caused by:|[A-Za-z0-9_.$]+(Exception|Error)\b|APPLICATION FAILED TO START|Description:|Action:|Message: )' "$log" \
  | cut -c1-400 | awk '!seen[$0]++' | head -n 14 || true)
if [ -n "$causes" ]; then
  echo "Cause (the first distinct lines of the stack trace):" >&2
  printf '%s\n' "$causes" >&2
fi
errors=$(grep -E '^\[ERROR\]' "$log" \
  | grep -v -E 'Re-run Maven|For more information|\[Help 1\]|See dump files|To see the full stack trace|^\[ERROR\] *$' \
  | cut -c1-400 | head -n 25 || true)
[ -z "$errors" ] || printf '%s\n' "$errors" >&2
# No error shape recognised (for example a wrong JAVA_HOME, mvn not runnable, a Maven version printing errors in another shape): do NOT swallow the error,
# print the exit code and the end of the output so the agent and the operator still see the reason.
if [ -z "$causes" ] && [ -z "$errors" ]; then
  echo "mvn exited with code $code without printing any error line the script recognises. Last 30 lines of the mvn output:" >&2
  if [ -s "$log" ]; then tail -n 30 "$log" | cut -c1-300 >&2; else echo "(empty output; code 127 means the mvn command was not found in PATH)" >&2; fi
fi
echo "Per-test details: backend/target/surefire-reports/*.txt" >&2
rm -f "$log"
exit 1
