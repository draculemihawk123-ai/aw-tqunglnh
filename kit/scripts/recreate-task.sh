#!/usr/bin/env bash
# Làm lại một task bằng workflow version MỚI NHẤT của cùng workflow definition (V11-10, ADR-041): hủy WorkItem cũ và
# tạo WorkItem con mới cùng parent với cùng nội dung (tiêu đề thêm "(lần N)", scope, contract), pin version mới nhất,
# READY rồi start run — MỘT lệnh `aw work-item recreate`, một transaction. WorkItem cũ còn lại ở trạng thái CANCELLED
# kèm liên kết "bị thay bởi ...". Dùng sau khi sửa kit/định nghĩa rồi publish lại (chạy lại bằng retry-task.sh vẫn chạy
# bản CŨ vì WorkItem ghim đúng một version).
# Cách dùng: recreate-task.sh <workItemId> "lý do" ["ghi chú cho agent"]
# Biến tùy chọn: CARRY_MESSAGES=0 không mang các ghi chú của người vận hành sang WorkItem mới (mặc định có);
#                OUT_OF_SCOPE=KEEP|PARK xử lý file run trước để lại ngoài scope (xem retry-task.sh);
#                WAIT_SECONDS (mặc định 2700), xem watch-run.sh.
# Không dùng được cho WorkItem gốc (root) và chỉ khi mọi run của WorkItem đã kết thúc.
set -euo pipefail
AW=${AW:-aw}
work_item=$1 reason=$2 note=${3:-}
out=$(mktemp) err=$(mktemp)
trap 'rm -f "$out" "$err"' EXIT
args=(--reason "$reason" --idempotency-key "recreate-$work_item")
[ -z "$note" ] || args+=(--note "$note")
[ "${CARRY_MESSAGES:-1}" != 0 ] || args+=(--carry-messages=false)
[ -z "${OUT_OF_SCOPE:-}" ] || args+=(--out-of-scope "$OUT_OF_SCOPE")
"$AW" work-item recreate "${args[@]}" "$work_item" > "$out" 2> "$err" || true
run_id=$(jq -er '.result.runId' "$out" 2> /dev/null) || { echo "recreate bị từ chối:" >&2; cat "$err" >&2
  if grep -q WORKSPACE_HEAD_DRIFT "$err"; then echo "Giữ các commit ngoài aw: adopt-head.sh [repositoryId], rồi chạy lại script này." >&2; fi
  if grep -q "outside the work item's scope" "$err"; then echo "Chạy lại với OUT_OF_SCOPE=PARK (cất các file đó) hoặc OUT_OF_SCOPE=KEEP." >&2; fi
  exit 1; }
jq -r '.result | "WorkItem mới: \(.newWorkItemId) (\(.title)), workflow \(.previousWorkflowVersionId) -> \(.workflowVersionId); WorkItem cũ \(.oldWorkItemId): \(.oldWorkItemStatus); mang theo \(.carriedMessageCount) ghi chú"' "$out"
"$(dirname "$0")/watch-run.sh" "$run_id"
