#!/bin/sh
# COMMAND "check-plan" (V10-15, after the PLAN node of wf-task-delivery-plus): the task's plan.md must have all six sections so BUILD does
# not have to guess. Checks the docs/features/*/tasks/*/plan.md files that are new or modified in the worktree. A section is recognised
# by a heading (##) that contains one of the keywords.
# The keywords are English plus the Vietnamese ones used before V11-08 (a plan may be written in the language of the task contract). The
# Vietnamese ones are written as octal escapes because this script is ASCII only: tep (file), tap tin, buoc (step), thu tu (order),
# kiem thu (testing), lenh (command), kiem tra (check), rui ro (risk), hoan tac (rollback), tieu chi (criteria), hoan thanh (completed).
set -eu
v() { printf "$1"; }
vi_file="$(v 't\341\273\207p')|$(v 't\341\272\255p tin')"
vi_steps="$(v 'b\306\260\341\273\233c')|$(v 'th\341\273\251 t\341\273\261')"
vi_tests="$(v 'ki\341\273\203m th\341\273\255')"
vi_verify="$(v 'l\341\273\207nh')|$(v 'ki\341\273\203m tra')"
vi_risk="$(v 'r\341\273\247i ro')|$(v 'ho\303\240n t\303\241c')"
vi_done="$(v 'ti\303\252u ch\303\255')|$(v 'ho\303\240n th\303\240nh')"
files=$(git status --porcelain --untracked-files=all -- docs/features | sed -E 's#^.{3}##; s#^"##; s#"$##' | grep -E '/tasks/[^/]+/plan\.md$' | sort -u || true)
if [ -z "$files" ]; then
  echo "MISSING: no new or modified docs/features/<feature>/tasks/<task>/plan.md. WHY: BUILD reads this plan. FIX: create plan.md following plan.checklist." >&2
  exit 1
fi
fail=0
for plan in $files; do
  [ -s "$plan" ] || { echo "EMPTY: $plan" >&2; fail=1; continue; }
  heads=$(grep -E '^#{2,4} ' "$plan" | tr 'A-Z' 'a-z' || true)
  need() { # <section name> <regex on the lowercased heading> <keywords to show>
    printf '%s\n' "$heads" | grep -q -E "$2" || { echo "MISSING section \"$1\" in $plan (a ## heading containing: $3)" >&2; fail=1; }
  }
  need "files to change" "file|$vi_file" "file"
  need "steps" "step|order|$vi_steps" "step or order"
  need "tests for each AC" "test|$vi_tests" "test"
  need "final verification command" "command|check|verify|verification|$vi_verify" "command or verification"
  need "risks and rollback" "risk|rollback|$vi_risk" "risk or rollback"
  need "done criteria" "criteria|done|completed|$vi_done" "criteria or done"
  [ "$fail" = 0 ] && echo "OK: $plan has all six sections"
done
[ "$fail" = 0 ] || { echo "WHY: a plan with missing sections makes BUILD guess. FIX: add the sections above following plan.checklist, then finish again." >&2; exit 1; }
