#!/usr/bin/env bash
# Tạo một WorkItem con (dưới root hiện tại) từ file JSON, đánh dấu READY rồi chạy workflow và chờ kết quả.
# Cách dùng: run-task.sh <work-item.json> <workflow id trong aw-project.json, ví dụ wf-backend-feature>
# Biến tùy chọn: MESSAGE="..." gửi thành message USER trước khi chạy (ví dụ log lỗi lần trước);
#                WAIT (mặc định 45m) thời gian chờ run kết thúc.
set -euo pipefail
AW=${AW:-aw}
AW_STATE=${AW_STATE:-./aw-state.json}
WAIT=${WAIT:-45m}
file=$1 workflow=$2
project_id=$(jq -er '.projectId' "$AW_STATE")
root=$(jq -er '.root.workItemId' "$AW_STATE")
wf=$(jq -er --arg w "$workflow" '.definitions.WORKFLOW[$w].versionId // error("aw-state.json không có workflow \($w) — đã chạy aw-publish.py chưa?")' "$AW_STATE")
body=$(jq --arg wf "$wf" '.contract.workflowVersionId = $wf' "$file")
# Idempotency key gắn với nội dung + workflow version: chạy lại y nguyên sẽ replay; sửa contract
# hoặc publish workflow mới sẽ tạo WorkItem mới.
key=$(printf '%s|%s' "$root" "$body" | sha256sum | cut -c1-16)
child=$("$AW" work-item create-child --idempotency-key "child-$key" "$root" <<<"$body" | jq -er '.result.workItemId')
echo "WorkItem: $child"
if [ -n "${MESSAGE:-}" ]; then
  printf '%s\n' "$MESSAGE" | "$AW" message append --project-id "$project_id" --role USER \
    --idempotency-key "message-$key" "$child" > /dev/null 2>&1
fi
"$AW" work-item readiness --project-id "$project_id" "$child" | jq -c '{ready, problems}'
if [ "$("$AW" work-item show --project-id "$project_id" "$child" | jq -r '.status')" = BACKLOG ]; then
  echo '{}' | "$AW" work-item mark-ready --expected-version 1 --idempotency-key "ready-$key" "$child" > /dev/null
fi
out=$(mktemp)
trap 'rm -f "$out"' EXIT
"$AW" run start --workflow-version-id "$wf" --idempotency-key "run-$key" --wait --wait-timeout "$WAIT" "$child" > "$out" 2> /dev/null || true
run_id=$(jq -er '.result.runId' "$out")
echo "Run: $run_id  state: $("$AW" run show "$run_id" | jq -r '.state')"
"$AW" run timeline "$run_id" \
  | jq -r '.entries[] | select(.kind == "NODE_RUN") | "  #\(.activationSequence) \(.nodeKey) (vòng \(.iteration)): \(.nodeState) \(.selectedOutcome // "")"'
"$AW" evidence list --project-id "$project_id" "$child" \
  | jq -r '.. | objects | select(has("kind") and has("verdict")) | "  evidence \(.kind): \(.verdict)"'
# Node APPROVAL đang chờ người duyệt: in lệnh duyệt kèm các outcome hợp lệ của node.
graph=$("$AW" run graph "$run_id")
"$AW" run show "$run_id" | jq -r --arg run "$run_id" --argjson graph "$graph" '.approvalRequests[]? | select(.state == "PENDING")
  | .nodeKey as $k | ([$graph.nodes[] | select(.key == $k)][0].outcomes | join("|")) as $o
  | "  CHỜ DUYỆT node \($k): review-task.sh \($run) <\($o)> [\"phản hồi\"]"'
