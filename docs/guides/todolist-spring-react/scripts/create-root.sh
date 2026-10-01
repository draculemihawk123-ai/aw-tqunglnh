#!/usr/bin/env bash
# Tạo WorkItem gốc: một TaskFamily với worktree + branch agentkit/w-… riêng, tách từ commit hiện tại của
# defaultRef. Mỗi đợt việc / mỗi tính năng nên có một gốc riêng. Ghi root vào aw-state.json.
# Cách dùng: create-root.sh "<tiêu đề>" [repositoryId READ bổ sung…]
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
