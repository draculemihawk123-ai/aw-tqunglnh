# Shared part of the COMMAND scripts ui-expect-fail.sh and ui-verify.sh (wf-bugfix-ui, V12-31, ADR-063); not run directly. Load it with
#   . "${AW_KIT:?set AW_KIT=<kit directory> when running by hand}/commands/ui-check.sh" # @aw-include
# then call: ui_check expect-fail | expect-pass
#
# The reproduction stage writes browser scripts for a UI that has no test suite yet: .aw-qa/*.mjs at the worktree root, run with
# Node and the Playwright library. Contract of a script: it opens $AW_QA_BASE_URL, checks the behavior the task describes, saves
# screenshots into $AW_QA_OUT, prints the line QA-ASSERT-FAILED: <what is wrong> and exits 1 when the behavior is wrong, exits 0 when
# it is right. Any other failure (a crash, a selector that does not exist) is a script error, not a reproduction.
#   expect-fail (before the fix): passes only when a script reports QA-ASSERT-FAILED.
#   expect-pass (after the fix):  passes only when every script exits 0.
# The scripts stay out of the repository (listed in .git/info/exclude) and are kept by aw as this step's output; a project that wants
# them in the repository sets AW_QA_SCRIPTS_IN_REPO=1 (only .aw-qa/out stays excluded). Playwright is never added to package.json:
# it must be installed where node finds it (NODE_PATH), with a browser (npx playwright install chromium).
# The application must be running at AW_QA_BASE_URL (default http://localhost:3000): start it with Run app in the Workspace tab.
ui_check() {
  mode=$1
  dir=.aw-qa
  ls "$dir"/*.mjs > /dev/null 2>&1 || aw_fail "ui-check: no $dir/*.mjs script: the reproduction stage writes the Playwright script there (see the skill bugfix.repro-ui)"
  exclude=$(git rev-parse --git-path info/exclude 2>/dev/null) || aw_fail "ui-check: not a git worktree"
  mkdir -p "$(dirname "$exclude")"
  if [ "${AW_QA_SCRIPTS_IN_REPO:-}" = 1 ]; then lines="/.aw-qa/out/ /.aw-qa/node_modules/"; else lines=/.aw-qa/; fi
  for line in $lines; do
    grep -q -x -F "$line" "$exclude" 2> /dev/null || printf '%s\n' "$line" >> "$exclude"
  done
  base=${AW_QA_BASE_URL:-http://localhost:3000}
  command -v node > /dev/null 2>&1 || aw_env_fail "ui-check: node is not on PATH"
  node -e "require.resolve('playwright')" > /dev/null 2>&1 \
    || aw_env_fail "ui-check: node cannot find the Playwright library: install it outside the project (npm i -g playwright && npx playwright install chromium) and give the worker NODE_PATH=\$(npm root -g) in its --env-allowlist"
  # An ES module "import 'playwright'" ignores NODE_PATH: give the scripts a small local package that loads the one node found.
  mkdir -p "$dir/node_modules/playwright"
  node -e "const p=require.resolve('playwright');require('fs').writeFileSync(process.argv[1],'import { createRequire } from \'module\';\nconst pw = createRequire(import.meta.url)('+JSON.stringify(p)+');\nexport const { chromium, firefox, webkit, devices, errors, selectors, request } = pw;\nexport default pw;\n')" "$dir/node_modules/playwright/index.mjs"
  printf '{"name":"playwright","type":"module","main":"index.mjs","exports":"./index.mjs"}\n' > "$dir/node_modules/playwright/package.json"
  curl -s -o /dev/null --max-time 5 "$base" \
    || aw_env_fail "ui-check: the application does not answer at $base: start it with Run app in the task's Workspace tab (or set AW_QA_BASE_URL)"
  out=$dir/out/$mode
  rm -rf "$out"
  mkdir -p "$out"
  log=$(mktemp)
  asserted=0 broken=0
  for script in "$dir"/*.mjs; do
    if AW_QA_BASE_URL=$base AW_QA_OUT=$out node "$script" > "$log" 2>&1; then
      echo "ui-check: $script: behavior is right"
    elif grep -q '^QA-ASSERT-FAILED' "$log"; then
      asserted=$((asserted + 1))
      grep '^QA-ASSERT-FAILED' "$log" | head -n 3 | cut -c1-300 | sed "s|^|ui-check: $script: |"
    else
      broken=$((broken + 1))
      echo "ui-check: $script did not run to its check (a script error, not the bug):" >&2
      tail -n 15 "$log" | cut -c1-300 >&2
    fi
  done
  rm -f "$log"
  for shot in "$out"/*; do
    [ -f "$shot" ] || continue
    sum=$( (sha256sum "$shot" 2> /dev/null || shasum -a 256 "$shot") | cut -d' ' -f1)
    echo "ui-check: screenshot $shot sha256 $sum"
  done
  [ "$broken" -eq 0 ] || exit 1
  if [ "$mode" = expect-fail ]; then
    [ "$asserted" -gt 0 ] || aw_fail "ui-check: every script says the behavior is right: the script does not reach the bug; change the script (steps, selector, assertion) until it reports QA-ASSERT-FAILED exactly because of the described bug"
    echo "ui-check: the bug is reproduced ($asserted script(s) report it)"
  else
    [ "$asserted" -eq 0 ] || aw_fail "ui-check: the behavior is still wrong ($asserted script(s) report QA-ASSERT-FAILED)"
    echo "ui-check: every script passes"
  fi
}
