#!/bin/sh
# COMMAND "check-feature-docs" (before GATE B): machine check of the documents of the feature being defined in the worktree -
# all files present, not empty, tasks.json matches its schema. Run at the worktree root.
set -eu
dirs=$(git status --porcelain --untracked-files=all -- docs/features \
  | sed -E 's#^.{3}##; s#^"##' | awk -F/ 'NF >= 3 { print $1 "/" $2 "/" $3 }' | sort -u)
if [ -z "$dirs" ]; then
  echo "No new or modified feature documents under docs/features/" >&2
  exit 1
fi
fail=0
for dir in $dirs; do
  for doc in 01-brainstorm.md 02-spec.md 03-design.md tasks.json; do
    if [ ! -s "$dir/$doc" ]; then
      echo "MISSING: $dir/$doc" >&2
      fail=1
    fi
  done
  if [ -s "$dir/tasks.json" ]; then
    if ! jq -e '
        type == "array" and length > 0
        and all(.[]; (.id | type == "string" and test("^T-[0-9]+$"))
          and (.title | type == "string" and length > 0)
          and (.pathScopes | type == "array" and length > 0)
          and (.riskLevel | IN("LOW", "MEDIUM", "HIGH"))
          and (.lane | IN("fast", "full"))
          and (.behavior | type == "string" and length > 0)
          and ((has("repository") | not) or (.repository | type == "string" and length > 0))
          and (.acceptanceCriteria | type == "array" and length > 0))
        and ([.[].id] | length == (unique | length))' "$dir/tasks.json" > /dev/null; then
      echo "BAD SCHEMA: $dir/tasks.json (needs an array of {id unique T-nn, title, pathScopes, riskLevel, lane, behavior, acceptanceCriteria; repository optional})" >&2
      fail=1
    else
      echo "OK: $dir - $(jq length "$dir/tasks.json") task(s)"
    fi
  fi
done
exit "$fail"
