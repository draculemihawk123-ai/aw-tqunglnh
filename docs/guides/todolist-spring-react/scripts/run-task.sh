#!/usr/bin/env bash
# Tạo một WorkItem con từ file JSON, đánh dấu READY rồi chạy workflow tương ứng và chờ kết quả.
# Cách dùng: run-task.sh <work-items/xx.json> <backend|frontend|TEN_BIEN_WORKFLOW>
#   backend  -> WF_BACKEND, frontend -> WF_FRONTEND; tham số khác là tên biến trong aw-ids.env
#   (ví dụ WF_FULLSTACK_REVIEW do publish-workflow.sh ghi ra).
# Biến tùy chọn: MESSAGE="..." — gửi thành message USER của WorkItem trước khi chạy (ví dụ log lỗi của
#   lần chạy trước); WAIT (mặc định 45m) — thời gian chờ run kết thúc.
set -euo pipefail
AW=${AW:-aw}
OUT_ENV=${OUT_ENV:-./aw-ids.env}
WAIT=${WAIT:-45m}
# shellcheck disable=SC1090
. "$OUT_ENV"
: "${PROJECT_ID:?}" "${ROOT_ID:?chạy create-root.sh trước}"
file=$1
case "${2:-}" in
  backend) wf_var=WF_BACKEND ;;
  frontend) wf_var=WF_FRONTEND ;;
  WF_*) wf_var=$2 ;;
  *) echo "tham số thứ hai phải là backend, frontend hoặc tên biến WF_..." >&2; exit 2 ;;
esac
wf=${!wf_var:?"aw-ids.env chưa có $wf_var"}
# Idempotency key gắn với nội dung file + workflow version: chạy lại y nguyên sẽ replay,
# sửa contract hoặc publish workflow mới sẽ tạo WorkItem mới.
key=$(jq -c --arg wf "$wf" '.contract.workflowVersionId = $wf' "$file" | sha256sum | cut -c1-16)
child=$(jq --arg wf "$wf" '.contract.workflowVersionId = $wf' "$file" \
  | "$AW" work-item create-child --idempotency-key "child-$key" "$ROOT_ID" | jq -er '.result.workItemId')
echo "WorkItem: $child"
if [ -n "${MESSAGE:-}" ]; then
  printf '%s\n' "$MESSAGE" | "$AW" message append --project-id "$PROJECT_ID" --role USER \
    --idempotency-key "message-$key" "$child" >/dev/null 2>&1
fi
"$AW" work-item readiness --project-id "$PROJECT_ID" "$child" | jq -c '{ready, problems}'
if [ "$("$AW" work-item show --project-id "$PROJECT_ID" "$child" | jq -r '.status')" = "BACKLOG" ]; then
  echo '{}' | "$AW" work-item mark-ready --expected-version 1 --idempotency-key "ready-$key" "$child" >/dev/null
fi
out=$(mktemp)
trap 'rm -f "$out"' EXIT
"$AW" run start --workflow-version-id "$wf" --idempotency-key "run-$key" --wait --wait-timeout "$WAIT" "$child" > "$out" || true
run_id=$(jq -er '.result.runId' "$out")
echo "Run: $run_id  state: $(jq -r '.result.wait.state // .result.state' "$out")"
"$AW" run timeline "$run_id" \
  | jq -r '.entries[] | select(.kind == "EXECUTION_ATTEMPT") | "  \(.nodeKey) #\(.attemptNumber): \(.attemptState) \(.terminationReason // "") \(.failureCode // "")"'
"$AW" evidence list --project-id "$PROJECT_ID" "$child" \
  | jq -r '.. | objects | select(has("kind") and has("verdict")) | "  evidence \(.kind): \(.verdict)"'
# Workflow có node APPROVAL dừng ở trạng thái chờ người duyệt: in yêu cầu đang chờ + outcome hợp lệ.
pending_hint() {
  local graph
  graph=$("$AW" run graph "$1")
  "$AW" run show "$1" | jq -r --arg run "$1" --argjson graph "$graph" '.approvalRequests[]? | select(.state == "PENDING")
    | .nodeKey as $k | ([$graph.nodes[] | select(.key == $k)][0].outcomes | join("|")) as $o
    | "  CHỜ DUYỆT node \($k): review-task.sh \($run) <\($o)> [\"phản hồi\"]"'
}
pending_hint "$run_id"
