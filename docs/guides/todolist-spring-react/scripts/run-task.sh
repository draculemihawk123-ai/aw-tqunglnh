#!/usr/bin/env bash
# Tạo một WorkItem con (dưới root hiện tại) từ file JSON, đánh dấu READY, chạy workflow rồi chờ tới khi run
# kết thúc hoặc cần người (cổng duyệt, tín hiệu).
# Cách dùng: run-task.sh <work-item.json> <workflow id trong aw-project.json, ví dụ wf-backend-feature>
# Biến tùy chọn: MESSAGE="..." gửi thành message USER trước khi chạy (ví dụ một ràng buộc bổ sung);
#                WAIT_SECONDS (mặc định 2700) thời gian chờ tối đa, xem watch-run.sh.
# Run FAILED thì chạy lại trên chính WorkItem đó bằng retry-task.sh, không tạo WorkItem mới.
set -euo pipefail
AW=${AW:-aw}
AW_STATE=${AW_STATE:-./aw-state.json}
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
# `problems` liệt kê cả contract thiếu trường lẫn baseline của repository chưa cho phép ghi (README mục 2.7).
"$AW" work-item readiness --project-id "$project_id" "$child" | jq -c '{ready, problems}'
item=$("$AW" work-item show --project-id "$project_id" "$child")
if [ "$(jq -r '.status' <<<"$item")" = BACKLOG ]; then
  echo '{}' | "$AW" work-item mark-ready --expected-version "$(jq -r '.version' <<<"$item")" \
    --idempotency-key "ready-$key" "$child" > /dev/null
fi
out=$(mktemp) err=$(mktemp)
trap 'rm -f "$out" "$err"' EXIT
"$AW" run start --workflow-version-id "$wf" --idempotency-key "run-$key" "$child" > "$out" 2> "$err" || true
run_id=$(jq -er '.result.runId' "$out" 2> /dev/null) || { echo "run start bị từ chối:" >&2; cat "$err" >&2; exit 1; }
"$(dirname "$0")/watch-run.sh" "$run_id"
