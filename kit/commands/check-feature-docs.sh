#!/bin/sh
# COMMAND "check-feature-docs" (trước GATE B): kiểm tra máy các tài liệu của tính năng đang được
# định nghĩa trong worktree — đủ file, không rỗng, tasks.json đúng schema. Chạy ở gốc worktree.
set -eu
dirs=$(git status --porcelain --untracked-files=all -- docs/features \
  | sed -E 's#^.{3}##; s#^"##' | awk -F/ 'NF >= 3 { print $1 "/" $2 "/" $3 }' | sort -u)
if [ -z "$dirs" ]; then
  echo "Không có tài liệu tính năng mới hoặc được sửa trong docs/features/" >&2
  exit 1
fi
fail=0
for dir in $dirs; do
  for doc in 01-brainstorm.md 02-spec.md 03-design.md tasks.json; do
    if [ ! -s "$dir/$doc" ]; then
      echo "THIẾU: $dir/$doc" >&2
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
          and (.acceptanceCriteria | type == "array" and length > 0))
        and ([.[].id] | length == (unique | length))' "$dir/tasks.json" > /dev/null; then
      echo "SAI SCHEMA: $dir/tasks.json (cần mảng {id T-nn duy nhất, title, pathScopes, riskLevel, lane, behavior, acceptanceCriteria})" >&2
      fail=1
    else
      echo "OK: $dir — $(jq length "$dir/tasks.json") task"
    fi
  fi
done
exit "$fail"
