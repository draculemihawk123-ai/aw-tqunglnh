#!/usr/bin/env bash
# In đường dẫn Git worktree mà aw cấp cho TaskFamily hiện tại (FAMILY_ID trong aw-ids.env).
# Dựa trên bố cục file của gitworktree adapter trong Alpha: $AW_WORKSPACE_ROOT/metadata/<handle>.json
# + $AW_WORKSPACE_ROOT/worktrees/<handle>. Đây là chi tiết cài đặt, không phải API công khai.
# Dùng để review: git -C "$(worktree-path.sh)" status / diff
set -euo pipefail
OUT_ENV=${OUT_ENV:-./aw-ids.env}
REPO_ID=${REPO_ID:-todolist}
: "${AW_WORKSPACE_ROOT:?}"
# shellcheck disable=SC1090
. "$OUT_ENV"
: "${FAMILY_ID:?chạy create-root.sh trước}"
handle=$(jq -sr --arg f "$FAMILY_ID" --arg r "$REPO_ID" \
  'map(select(.familyId == $f and .repositoryId == $r)) | max_by(.generation) | .handle // empty' \
  "$AW_WORKSPACE_ROOT"/metadata/*.json)
[ -n "$handle" ] || { echo "không tìm thấy worktree của family $FAMILY_ID" >&2; exit 1; }
echo "$AW_WORKSPACE_ROOT/worktrees/$handle"
