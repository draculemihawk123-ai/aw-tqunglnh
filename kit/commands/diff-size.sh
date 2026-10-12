#!/bin/sh
# COMMAND "diff-size" (V10-15; replaces the kind of hook that warns about a big diff and lets it through on error - this one CANNOT be skipped):
# measures the uncommitted diff of the worktree against HEAD. Over the threshold it exits 1 so the workflow takes the `large` branch to a
# node that simplifies the code; under the threshold it exits 0.
# Default thresholds: 400 changed lines in total, 8 files, 200 lines in one file. Set AW_DIFF_MAX_LOC, AW_DIFF_MAX_FILES,
# AW_DIFF_MAX_FILE_LOC to change them when running by hand. Ignored: documents (docs/, *.md), lockfiles, generated/minified files;
# whitespace-only changes are ignored.
set -eu
. "${AW_KIT:?set AW_KIT=<kit directory> when running by hand}/commands/lib.sh" # @aw-include
max_loc=${AW_DIFF_MAX_LOC:-400} max_files=${AW_DIFF_MAX_FILES:-8} max_file_loc=${AW_DIFF_MAX_FILE_LOC:-200}
git rev-parse --verify -q HEAD > /dev/null || aw_fail "diff-size: the repository has no commit yet"
counted() { grep -v -E '(^|/)(docs/|node_modules/|target/|dist/|build/)|\.md$|(package-lock\.json|pnpm-lock\.yaml|yarn\.lock)$|\.min\.(js|css)$|\.(lock|map|svg|png|jpg)$'; }
rows=$(mktemp)
trap 'rm -f "$rows"' EXIT
git diff --numstat --ignore-all-space HEAD | awk -F'\t' '$1 != "-" { print $1 + $2 "\t" $3 }' | while IFS="$(printf '\t')" read -r n f; do
  printf '%s\n' "$f" | counted > /dev/null && printf '%s\t%s\n' "$n" "$f"
done > "$rows" || true
git ls-files --others --exclude-standard | counted | while IFS= read -r f; do
  [ -f "$f" ] && printf '%s\t%s\n' "$(wc -l < "$f" | tr -d ' ')" "$f"
done >> "$rows" || true
total=$(awk -F'\t' '{ s += $1 } END { print s + 0 }' "$rows")
files=$(wc -l < "$rows" | tr -d ' ')
biggest=$(sort -rn "$rows" | head -n 1)
big_n=${biggest%%	*} big_f=${biggest#*	}
breach=""
[ "$total" -le "$max_loc" ] || breach="$breach $total changed lines in total (threshold $max_loc);"
[ "$files" -le "$max_files" ] || breach="$breach $files changed files (threshold $max_files);"
[ -z "$biggest" ] || [ "$big_n" -le "$max_file_loc" ] || breach="$breach $big_f has $big_n changed lines (single-file threshold $max_file_loc);"
if [ -n "$breach" ]; then
  echo "diff-size: large diff:$breach" >&2
  echo "WHY: a large diff is hard to review and tends to contain leftovers; simplify it before the review step." >&2
  echo "FIX: simplify the code (remove duplication, remove leftovers, make it simpler) WITHOUT changing behaviour, then run the checks again; do not delete tests." >&2
  exit 1
fi
echo "diff-size: $total changed lines, $files files: within the threshold"
