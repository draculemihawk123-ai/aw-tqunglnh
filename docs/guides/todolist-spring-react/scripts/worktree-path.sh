#!/usr/bin/env bash
# In đường dẫn Git worktree của TaskFamily hiện tại (root trong aw-state.json).
# Dựa trên bố cục file của gitworktree adapter trong Alpha: $AW_WORKSPACE_ROOT/metadata/<handle>.json
# + $AW_WORKSPACE_ROOT/worktrees/<handle>. Đây là chi tiết cài đặt, không phải API công khai.
set -euo pipefail
AW_STATE=${AW_STATE:-./aw-state.json}
: "${AW_WORKSPACE_ROOT:?}"
family=$(jq -er '.root.familyId' "$AW_STATE")
repo=${1:-$(jq -er '.repository' "$AW_STATE")}
handle=$(jq -sr --arg f "$family" --arg r "$repo" \
  'map(select(.familyId == $f and .repositoryId == $r)) | max_by(.generation) | .handle // empty' \
  "$AW_WORKSPACE_ROOT"/metadata/*.json)
[ -n "$handle" ] || { echo "không tìm thấy worktree của family $family" >&2; exit 1; }
echo "$AW_WORKSPACE_ROOT/worktrees/$handle"
