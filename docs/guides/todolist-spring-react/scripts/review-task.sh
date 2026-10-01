#!/usr/bin/env bash
# Quyết định node APPROVAL đang chờ của một run (cổng duyệt, NEEDS_INFO…), rồi chờ run đi tiếp.
# Cách dùng: review-task.sh <runId> <outcome> ["phản hồi cho agent"]
#   outcome là một outcome khai báo của node đó (run-task.sh in sẵn danh sách).
#   Phản hồi được append thành message USER của WorkItem; mọi lần chạy sau của các node AGENT
#   nhận toàn bộ message trong prompt (taskContract + messages + resources).
set -euo pipefail
AW=${AW:-aw}
AW_STATE=${AW_STATE:-./aw-state.json}
WAIT_SECONDS=${WAIT_SECONDS:-2700}
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
pending=0 state=RUNNING
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
  graph=$("$AW" run graph "$run_id")
  jq -r --arg run "$run_id" --argjson graph "$graph" '.approvalRequests[]? | select(.state == "PENDING")
    | .nodeKey as $k | ([$graph.nodes[] | select(.key == $k)][0].outcomes | join("|")) as $o
    | "  CHỜ DUYỆT node \($k): review-task.sh \($run) <\($o)> [\"phản hồi\"]"' <<<"$detail"
fi
