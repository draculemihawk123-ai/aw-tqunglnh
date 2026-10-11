#!/bin/sh
# COMMAND "check-lessons" (V10-16, before the approval gate of wf-retro): a new or modified docs/lessons/*.md file must have the
# format lessons-to-skill.py turns into a resource: every lesson "### L-n: title" has five lines (Evidence, Occurrences,
# Proposed rule, Attach to, Level), the rule is at most 500 characters, the Level is valid, at most 5 lessons. Run at the
# worktree root.
# The labels are English (retro.lessons-format). The Vietnamese labels used before V11-08 are still accepted so an older lessons
# file can be edited; they are written below as octal escapes because this script is ASCII only.
set -eu
vi_evidence=$(printf 'B\341\272\261ng ch\341\273\251ng')
vi_occurrences=$(printf 'S\341\273\221 l\341\272\247n l\341\272\267p')
vi_rule=$(printf 'Lu\341\272\255t \304\221\341\273\201 xu\341\272\245t')
vi_attach=$(printf 'G\341\272\257n v\303\240o')
vi_level=$(printf 'M\341\273\251c')
vi_status=$(printf 'Tr\341\272\241ng th\303\241i')
files=$(git status --porcelain --untracked-files=all -- docs/lessons | sed -E 's#^.{3}##; s#^"##; s#"$##' | grep -E '^docs/lessons/[^/]+\.md$' | sort -u || true)
if [ -z "$files" ]; then
  echo "MISSING: no new or modified docs/lessons/<date>.md. FIX: write the lessons to docs/lessons/<yyyy-mm-dd>.md following retro.lessons-format." >&2
  exit 1
fi
fail=0
for f in $files; do
  [ -s "$f" ] || { echo "EMPTY: $f" >&2; fail=1; continue; }
  out=$(awk -v file="$f" -v vie="$vi_evidence" -v vio="$vi_occurrences" -v vir="$vi_rule" -v via="$vi_attach" -v vil="$vi_level" -v vis="$vi_status" '
    function canon(k) {
      if (k == vie) return "Evidence"
      if (k == vio) return "Occurrences"
      if (k == vir) return "Proposed rule"
      if (k == via) return "Attach to"
      if (k == vil) return "Level"
      if (k == vis) return "Status"
      return k
    }
    function flush() {
      if (id == "") return
      n++
      split("Evidence|Occurrences|Proposed rule|Attach to|Level", need, "|")
      for (i = 1; i <= 5; i++) if (!(need[i] in seen)) { print "MISSING \"" need[i] "\" at " id " in " file; bad = 1 }
      if (length(rule) > 500) { print id ": rule is " length(rule) " characters (max 500) in " file; bad = 1 }
      if ("Level" in seen && level !~ /^(REQUIRED_PROCEDURE|GUIDANCE|HARD_CONSTRAINT)$/) { print id ": Level \"" level "\" is not valid (REQUIRED_PROCEDURE, GUIDANCE or HARD_CONSTRAINT)"; bad = 1 }
      delete seen; rule = ""; level = ""
    }
    /^### L-[0-9]+: / { flush(); id = $2; sub(/:$/, "", id); next }
    /^## / { flush(); id = ""; next }
    id != "" && /^- [^:]+:/ {
      key = $0; sub(/^- /, "", key); sub(/:.*/, "", key); key = canon(key)
      if (key != "Evidence" && key != "Occurrences" && key != "Proposed rule" && key != "Attach to" && key != "Level" && key != "Status") next
      val = $0; sub(/^[^:]*: */, "", val)
      seen[key] = 1
      if (key == "Proposed rule") rule = val
      if (key == "Level") level = val
    }
    END { flush(); if (n == 0) { print "No lessons (### L-n: ...) in " file; bad = 1 } if (n > 5) { print n " lessons (max 5) in " file; bad = 1 } exit bad }
  ' "$f") || { printf '%s\n' "$out" >&2; fail=1; continue; }
  echo "OK: $f"
done
[ "$fail" = 0 ] || { echo "FIX: correct the file following retro.lessons-format (five lines per lesson, rule at most 500 characters)." >&2; exit 1; }
