#!/usr/bin/env bash
# Quyết định node APPROVAL "review" của một run đang chờ duyệt.
# Cách dùng: review-task.sh <runId> <approved|rework|rejected> ["phản hồi cho agent"]
#   rework  : phản hồi được append thành message USER của WorkItem; lần chạy lại của node
#             implement nhận toàn bộ message trong prompt (taskContract + messages + resources).
#   rejected: run đi tới node reject (COMMAND thoát mã 1) và kết thúc FAILED.
set -euo pipefail
AW=${AW:-aw}
OUT_ENV=${OUT_ENV:-./aw-ids.env}
WAIT_SECONDS=${WAIT_SECONDS:-2700}
# shellcheck disable=SC1090
. "$OUT_ENV"
: "${PROJECT_ID:?}"
run_id=$1 outcome=$2 feedback=${3:-}

detail=$("$AW" run show "$run_id")
work_item=$(jq -er '.workItemId' <<<"$detail")
request=$(jq -ec '[.approvalRequests[]? | select(.state == "PENDING")][0] // error("run không có approval nào đang chờ")' <<<"$detail")
request_id=$(jq -r '.approvalRequestId' <<<"$request")
if [ -n "$feedback" ]; then
  printf '%s\n' "$feedback" | "$AW" message append --project-id "$PROJECT_ID" --role USER \
    --idempotency-key "feedback-$request_id" "$work_item" >/dev/null
fi
"$AW" approval resolve --outcome "$outcome" --reason "${feedback:-$outcome}" \
  --expected-version "$(jq -r '.version' <<<"$request")" --idempotency-key "resolve-$request_id" \
  "$run_id" "$request_id" >/dev/null
echo "Đã chọn '$outcome' cho node $(jq -r '.nodeKey' <<<"$request"); chờ run chạy tiếp..."

# Chờ tới khi run kết thúc hoặc lại dừng ở một approval mới (vòng rework).
for _ in $(seq 1 $((WAIT_SECONDS / 5))); do
  sleep 5
  detail=$("$AW" run show "$run_id")
  state=$(jq -r '.state' <<<"$detail")
  pending=$(jq -r --arg old "$request_id" '[.approvalRequests[]? | select(.state == "PENDING" and .approvalRequestId != $old)] | length' <<<"$detail")
  if [ "$pending" != 0 ] || { [ "$state" != RUNNING ] && [ "$state" != COMPLETING ] && [ "$state" != VERIFYING ]; }; then
    break
  fi
done
echo "Run: $run_id  state: $state"
"$AW" run timeline "$run_id" \
  | jq -r '.entries[] | select(.kind == "NODE_RUN") | "  #\(.activationSequence) \(.nodeKey) (vòng \(.iteration)): \(.nodeState) \(.selectedOutcome // "")"'
if [ "$pending" != 0 ]; then
  echo "  CHỜ DUYỆT lại: review-task.sh $run_id <approved|rework|rejected> [\"phản hồi\"]"
fi
