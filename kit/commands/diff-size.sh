#!/bin/sh
# COMMAND "diff-size" (V10-15; replaces the kind of hook that warns about a big diff and lets it through on error - this one CANNOT be skipped):
# measures the uncommitted diff of the worktree against HEAD. Over the threshold it exits 1 so the workflow takes the `large` branch to a
# node that simplifies the code; under the threshold it exits 0.
# Default thresholds: 400 changed lines in total, 8 files, 200 lines in one file. Set AW_DIFF_MAX_LOC, AW_DIFF_MAX_FILES,
# AW_DIFF_MAX_FILE_LOC to change them when running by hand. Ignored: documents (docs/, *.md), lockfiles, generated/minified files;
# whitespace-only changes are ignored.
# V12-06: thresholds per component. An optional file .aw-diff-size at the repository root holds one line per component:
#   <path prefix> <max lines> <max files> <max lines in one file>     e.g.  backend/ 800 15 300
# (# starts a comment). Each changed file counts against the component with the longest matching prefix; a file no prefix
# matches counts against the default thresholds above. Every component is checked on its own, and the output always lists the
# figures per component (the size-review gate shows them).
set -eu
. "${AW_KIT:?set AW_KIT=<kit directory> when running by hand}/commands/lib.sh" # @aw-include
max_loc=${AW_DIFF_MAX_LOC:-400} max_files=${AW_DIFF_MAX_FILES:-8} max_file_loc=${AW_DIFF_MAX_FILE_LOC:-200}
git rev-parse --verify -q HEAD > /dev/null || aw_fail "diff-size: the repository has no commit yet"
counted() { grep -v -E '^\.aw-diff-size$|(^|/)(docs/|node_modules/|target/|dist/|build/)|\.md$|(package-lock\.json|pnpm-lock\.yaml|yarn\.lock)$|\.min\.(js|css)$|\.(lock|map|svg|png|jpg)$'; }
rows=$(mktemp) limits=$(mktemp)
trap 'rm -f "$rows" "$limits"' EXIT
git diff --numstat --ignore-all-space HEAD | awk -F'\t' '$1 != "-" { print $1 + $2 "\t" $3 }' | while IFS="$(printf '\t')" read -r n f; do
  printf '%s\n' "$f" | counted > /dev/null && printf '%s\t%s\n' "$n" "$f"
done > "$rows" || true
git ls-files --others --exclude-standard | counted | while IFS= read -r f; do
  [ -f "$f" ] && printf '%s\t%s\n' "$(wc -l < "$f" | tr -d ' ')" "$f"
done >> "$rows" || true
[ ! -f .aw-diff-size ] || sed -e 's/#.*//' .aw-diff-size | awk 'NF >= 4 { print $1 "\t" $2 "\t" $3 "\t" $4 }' > "$limits"
report=$(awk -F'\t' -v dl="$max_loc" -v df="$max_files" -v dfl="$max_file_loc" -v lf="$limits" '
  BEGIN { while ((getline line < lf) > 0) { split(line, p, "\t"); pre[++np] = p[1]; L[p[1]] = p[2]; F[p[1]] = p[3]; FL[p[1]] = p[4] } }
  {
    best = ""
    for (i = 1; i <= np; i++) if (index($2, pre[i]) == 1 && length(pre[i]) > length(best)) best = pre[i]
    c = (best == "" ? "(default)" : best)
    if (!(c in seen)) { seen[c] = 1; order[++nc] = c }
    loc[c] += $1; files[c]++
    if ($1 > big[c]) { big[c] = $1; bigf[c] = $2 }
  }
  END {
    for (k = 1; k <= nc; k++) {
      c = order[k]
      ml = (c == "(default)" ? dl : L[c]); mf = (c == "(default)" ? df : F[c]); mfl = (c == "(default)" ? dfl : FL[c])
      b = ""
      if (loc[c] > ml) b = b " " loc[c] " changed lines (threshold " ml ");"
      if (files[c] > mf) b = b " " files[c] " changed files (threshold " mf ");"
      if (big[c] > mfl) b = b " " bigf[c] " has " big[c] " changed lines (single-file threshold " mfl ");"
      printf "%s\t%s\t%d\t%d\t%s\n", (b == "" ? "ok" : "large"), c, loc[c], files[c], b
    }
  }' "$rows")
total=$(awk -F'\t' '{ s += $1 } END { print s + 0 }' "$rows")
files=$(wc -l < "$rows" | tr -d ' ')
printf '%s\n' "$report" | awk -F'\t' 'NF >= 4 { printf "diff-size: component %s: %d lines, %d files (%s)\n", $2, $3, $4, $1 }'
breach=$(printf '%s\n' "$report" | awk -F'\t' '$1 == "large" { printf " %s:%s", $2, $5 }')
if [ -n "$breach" ]; then
  echo "diff-size: large diff:$breach" >&2
  echo "WHY: a large diff is hard to review and tends to contain leftovers; simplify it before the review step." >&2
  echo "FIX: simplify the code (remove duplication, remove leftovers, make it simpler) WITHOUT changing behaviour, then run the checks again; do not delete tests." >&2
  exit 1
fi
echo "diff-size: $total changed lines, $files files: within the threshold"
