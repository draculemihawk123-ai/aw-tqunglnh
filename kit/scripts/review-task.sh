#!/usr/bin/env bash
# Quyết định node APPROVAL đang chờ của một run (cổng duyệt, NEEDS_INFO…), rồi chờ run đi tiếp.
# Cách dùng: review-task.sh <runId> <outcome> ["phản hồi cho agent"]
# Biến tùy chọn: WAIT_SECONDS (mặc định 2700) thời gian chờ tối đa, xem watch-run.sh.
#   outcome là một outcome khai báo của node đó (show-run.sh in sẵn danh sách).
#   Phản hồi được append thành message USER của WorkItem; các node AGENT chạy sau đó nhận nó trong phần
#   messages của prompt (trong giới hạn messageBudget của agent; message mới nhất luôn được giữ).
set -euo pipefail
AW=${AW:-aw}
AW_STATE=${AW_STATE:-./aw-state.json}
project_id=$(jq -er '.projectId' "$AW_STATE")
run_id=$1 outcome=$2 feedback=${3:-}
detail=$("$AW" run show "$run_id")
work_item=$(jq -er '.workItemId' <<<"$detail")
request=$(jq -ec '[.approvalRequests[]? | select(.state == "PENDING")][0] // error("run không có approval nào đang chờ")' <<<"$detail")
request_id=$(jq -r '.approvalRequestId' <<<"$request")
if [ -n "$feedback" ]; then
  printf '%s\n' "$feedback" | "$AW" message append --project-id "$project_id" --role USER \
    --idempotency-key "feedback-$request_id" "$work_item" > /dev/null 2>&1
fi
"$AW" approval resolve --outcome "$outcome" --reason "${feedback:-$outcome}" \
  --expected-version "$(jq -r '.version' <<<"$request")" --idempotency-key "resolve-$request_id" \
  "$run_id" "$request_id" > /dev/null
echo "Đã chọn '$outcome' cho node $(jq -r '.nodeKey' <<<"$request"); chờ run chạy tiếp..."
"$(dirname "$0")/watch-run.sh" "$run_id" "$request_id"
