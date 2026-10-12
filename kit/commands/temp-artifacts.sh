#!/bin/sh
# COMMAND "temp-artifacts" (V10-15): blocks leftovers in the uncommitted diff: debug statements, tests that are disabled or run only in part, new TODOs.
# Only ADDED lines are examined (the diff against HEAD and untracked files), not old code. Every error is printed as WHAT / WHY / FIX
# because the agent only receives the last 4 KiB of stderr. Exits 1 if there is any.
set -eu
. "${AW_KIT:?set AW_KIT=<kit directory> when running by hand}/commands/lib.sh" # @aw-include
git rev-parse --verify -q HEAD > /dev/null || aw_fail "temp-artifacts: the repository has no commit yet"
added=$(mktemp)
trap 'rm -f "$added"' EXIT
skip='(^|/)(docs/|node_modules/|target/|dist/|build/)|\.md$|(package-lock\.json|pnpm-lock\.yaml|yarn\.lock)$|^kit/|(^|/)commands/[^/]*\.sh$'
{
  git diff -U0 --no-color HEAD | awk '
    /^\+\+\+ b\// { file = substr($0, 7); next }
    /^\+\+\+ / { file = ""; next }
    /^@@/ { match($0, /\+[0-9]+/); line = substr($0, RSTART + 1, RLENGTH - 1) + 0; next }
    /^\+/ { if (file != "") printf "%s:%d:%s\n", file, line, substr($0, 2); line++ }'
  git ls-files --others --exclude-standard | while IFS= read -r f; do
    [ -f "$f" ] && awk -v f="$f" '{ printf "%s:%d:%s\n", f, NR, $0 }' "$f"
  done
} | grep -v -E "^($skip)" > "$added" || true
found=0
report() { # <code> <regex> <WHAT> <WHY> <FIX>
  hits=$(grep -E -e "^[^:]+:[0-9]+:.*($2)" "$added" | head -n 5 | cut -c1-200 || true)
  [ -n "$hits" ] || return 0
  found=1
  echo "WHAT: $3" >&2
  printf '%s\n' "$hits" | sed 's/^/  /' >&2
  echo "WHY: $4" >&2
  echo "FIX: $5" >&2
}
report debugger '(^|[^A-Za-z_])debugger;|System\.out\.print|printStackTrace\(\)|console\.(log|debug)\(' \
  "forgotten debug statements (debugger, console.log, System.out, printStackTrace)" \
  "debug logging pollutes the output and can leak data" "remove the lines above, or switch to the project's logger if the logging must stay"
report only '(describe|it|test|context)\.only\(|(^|[^A-Za-z_])f(describe|it)\(' \
  "tests that run only in part (.only, fit, fdescribe)" \
  "the other tests are skipped so the check is falsely green" "remove .only and run the whole test suite again"
report disabled '@Disabled|@Ignore\b|(describe|it|test)\.skip\(|(^|[^A-Za-z_])x(describe|it)\(' \
  "disabled tests (@Disabled, @Ignore, .skip, xit)" \
  "a disabled test hides a real bug, and disabling a test to get green is a forbidden shortcut" "enable the test again and fix the code so it passes; if the test itself is wrong, fix the test and say why in your report"
report todo '(TODO|FIXME|XXX)([^A-Za-z]|$)' \
  "a newly added TODO/FIXME in code" \
  "unfinished work buried in code that nobody tracks" "finish it now, or put it in your final report instead of leaving it in the code"
[ "$found" = 0 ] || exit 1
echo "temp-artifacts: no leftovers in the added lines"
