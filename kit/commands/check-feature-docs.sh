#!/bin/sh
# COMMAND "check-feature-docs" (trước GATE B): kiểm máy các tài liệu của tính năng đang được định nghĩa trong worktree —
# đủ file, không rỗng, tasks.json đúng schema, 03-design.md đủ khung (skill-doc-templates), AC của tasks.json có trong
# 02-spec.md và mỗi AC của spec có test trong kế hoạch test của thiết kế. Chạy ở gốc worktree.
set -eu
. "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/lib.sh" # @aw-include
. "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/docs-lib.sh" # @aw-include
dirs=$(docs_dirs)
[ -n "$dirs" ] || aw_fail "Không có tài liệu tính năng mới hoặc được sửa trong docs/features/"
for dir in $dirs; do
  for doc in 01-brainstorm.md 02-spec.md 03-design.md tasks.json; do
    [ -s "$dir/$doc" ] || docs_err "THIẾU: $dir/$doc"
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
      docs_err "SAI SCHEMA: $dir/tasks.json (cần mảng {id T-nn duy nhất, title, pathScopes, riskLevel, lane, behavior, acceptanceCriteria; repository tùy chọn})"
    fi
  fi
  design="$dir/03-design.md"
  spec="$dir/02-spec.md"
  if [ -s "$design" ]; then
    docs_head "$design" "thay đổi theo tầng" 'tầng|thay đổi|layer'
    docs_head "$design" "hợp đồng API" 'hợp đồng|api|contract'
    docs_head "$design" "quyết định và đánh đổi" 'quyết định|decision'
    docs_head "$design" "kế hoạch test" 'test|kiểm thử'
    docs_head "$design" "rủi ro và rollback" 'rủi ro|rollback|hoàn tác'
    docs_head "$design" "ngoài phạm vi" 'ngoài phạm vi|để ngoài|out of scope'
    decisions=$(docs_section "$design" 'quyết định|decision')
    printf '%s\n' "$decisions" | grep -q -E '\bD-[0-9]+\b' || docs_err "THIẾU mã D-n trong mục quyết định của $design: mỗi quyết định một mã D-n"
    printf '%s\n' "$decisions" | grep -q -i -E 'loại|không chọn|rejected|alternative' || docs_err "THIẾU phương án bị loại trong mục quyết định của $design: mỗi quyết định nêu phương án đã loại và lý do"
    if [ -s "$spec" ]; then
      covered=$(docs_section "$design" 'test|kiểm thử' | docs_cover AC)
      for ac in $(docs_ids "$spec" AC); do
        printf '%s\n' "$covered" | grep -q -x "$ac" || docs_err "THIẾU test cho $ac (có trong $spec) trong mục kế hoạch test của $design"
      done
      if [ -s "$dir/tasks.json" ] && jq -e 'type == "array"' "$dir/tasks.json" > /dev/null 2>&1; then
        for ac in $(jq -r '.[].acceptanceCriteria[]? | tostring' "$dir/tasks.json" | grep -o -E '\bAC-[0-9]+\b' | sort -u); do
          docs_ids "$spec" AC | grep -q -x "$ac" || docs_err "SAI: $ac trong $dir/tasks.json không có trong $spec"
        done
      fi
    fi
  fi
  [ -n "$docs_errors" ] || echo "OK: $dir — $(jq length "$dir/tasks.json") task, thiết kế đủ khung"
done
docs_report
