#!/bin/sh
# COMMAND "check-plan" (V10-15, sau node PLAN của wf-task-delivery-plus): plan.md của task phải có đủ sáu mục để BUILD không phải đoán.
# Kiểm các file docs/features/*/tasks/*/plan.md mới hoặc được sửa trong worktree. Mục được nhận bằng tiêu đề (##) chứa một trong các từ khóa.
set -eu
files=$(git status --porcelain --untracked-files=all -- docs/features | sed -E 's#^.{3}##; s#^"##; s#"$##' | grep -E '/tasks/[^/]+/plan\.md$' | sort -u || true)
if [ -z "$files" ]; then
  echo "THIẾU: không có docs/features/<tính năng>/tasks/<task>/plan.md mới hoặc được sửa. WHY: BUILD đọc plan này. FIX: tạo plan.md theo plan.checklist." >&2
  exit 1
fi
fail=0
for plan in $files; do
  [ -s "$plan" ] || { echo "RỖNG: $plan" >&2; fail=1; continue; }
  heads=$(grep -E '^#{2,4} ' "$plan" | tr 'A-Z' 'a-z' || true)
  need() { # <tên mục> <regex trên tiêu đề viết thường>
    printf '%s\n' "$heads" | grep -q -E "$2" || { echo "THIẾU mục \"$1\" trong $plan (tiêu đề ## chứa: $3)" >&2; fail=1; }
  }
  need "file sẽ sửa" 'file|tệp|tập tin' "file hoặc tệp"
  need "các bước" 'bước|step|thứ tự' "bước hoặc thứ tự"
  need "test cho từng AC" 'test|kiểm thử' "test"
  need "lệnh kiểm tra cuối" 'lệnh|kiểm tra|verify|verification' "lệnh hoặc kiểm tra"
  need "rủi ro và hoàn tác" 'rủi ro|risk|hoàn tác|rollback' "rủi ro hoặc hoàn tác"
  need "tiêu chí xong" 'tiêu chí|done|hoàn thành|xong' "tiêu chí hoặc xong"
  [ "$fail" = 0 ] && echo "OK: $plan đủ sáu mục"
done
[ "$fail" = 0 ] || { echo "WHY: plan thiếu mục thì BUILD phải đoán. FIX: bổ sung các mục trên theo plan.checklist rồi kết thúc lại." >&2; exit 1; }
