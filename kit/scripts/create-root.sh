#!/usr/bin/env bash
# Tạo WorkItem gốc: một TaskFamily với worktree + branch agentkit/w-… riêng, tách từ commit hiện tại của
# defaultRef. Mỗi đợt việc / mỗi tính năng nên có một gốc riêng. Ghi root vào aw-state.json, rồi chờ worker
# tạo xong worktree và chạy xong baseline của repository: `aw run start` bị từ chối chừng nào workspace set
# chưa READY hoặc baseline còn PENDING.
# Cách dùng: create-root.sh "<tiêu đề>" [repositoryId READ bổ sung…]
# Biến tùy chọn: BASELINE_WAIT_SECONDS (mặc định 1800) thời gian chờ baseline tối đa.
set -euo pipefail
AW=${AW:-aw}
AW_STATE=${AW_STATE:-./aw-state.json}
title=$1
shift
project_id=$(jq -er '.projectId' "$AW_STATE")
repo=$(jq -er '.repository' "$AW_STATE")
key=$(printf '%s|%s' "$project_id" "$title" | sha256sum | cut -c1-16)
result=$(jq -n --arg p "$project_id" --arg t "$title" --arg r "$repo" --args '{projectId: $p, title: $t,
    initialScope: ([{repositoryId: $r, access: "WRITE", reason: $t}]
                   + [$ARGS.positional[] | {repositoryId: ., access: "READ", reason: $t}])}' "$@" \
  | "$AW" work-item create --project-id "$project_id" --idempotency-key "root-$key")
jq --argjson r "$(jq '.result | {workItemId, familyId, title: $t}' --arg t "$title" <<<"$result")" '.root = $r' \
  "$AW_STATE" > "$AW_STATE.tmp" && mv "$AW_STATE.tmp" "$AW_STATE"
jq -r '.root | "root \(.workItemId)\nfamily \(.familyId)"' "$AW_STATE"
family=$(jq -er '.root.familyId' "$AW_STATE")

workspace='{}' state=
for _ in $(seq 1 120); do
  workspace=$("$AW" workspace-set show --project-id "$project_id" "$family")
  state=$(jq -r '.state' <<<"$workspace")
  [ "$state" = READY ] || [ "$state" = BLOCKED ] && break
  sleep 1
done
echo "workspace: $state"
[ "$state" = READY ] || { echo "worktree chưa sẵn sàng — kiểm tra aw worker và \`aw workspace-set show\`" >&2; exit 1; }

# Repository có readiness profile (README mục 2.7) thì worktree mới còn phải qua baseline trước khi nhận task.
workspace_set=$(jq -r '.workspaceSetId' <<<"$workspace")
baseline=
for _ in $(seq 1 "${BASELINE_WAIT_SECONDS:-1800}"); do
  baseline=$("$AW" repository readiness show "$repo" \
    | jq -r --arg ws "$workspace_set" '[.workspaces[] | select(.workspaceSetId == $ws)][0].baselineState // "NOT_REQUIRED"')
  [ "$baseline" = PENDING ] || break
  sleep 1
done
echo "baseline: $baseline"
case "$baseline" in
  PASS | NOT_REQUIRED | EXCEPTION_ACCEPTED) ;;
  *) echo "baseline chưa đạt — xem \`aw repository readiness show $repo\` (README mục 2.7)" >&2; exit 1 ;;
esac
