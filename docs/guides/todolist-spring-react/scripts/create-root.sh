#!/usr/bin/env bash
# Tạo WorkItem gốc (TaskFamily + WorkspaceSet/worktree riêng, branch agentkit/w-…) và ghi ROOT_ID,
# FAMILY_ID vào file id (mặc định ./aw-ids.env do publish-definitions.sh tạo ra).
# Cách dùng: create-root.sh ["Tiêu đề"]   (mặc định "Todolist MVP"; mỗi tính năng nên có gốc riêng)
set -euo pipefail
AW=${AW:-aw}
OUT_ENV=${OUT_ENV:-./aw-ids.env}
HERE=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1090
. "$OUT_ENV"
: "${PROJECT_ID:?}"
title=${1:-}
key="root-$PROJECT_ID"
[ -n "$title" ] && key="root-$(printf '%s|%s' "$PROJECT_ID" "$title" | sha256sum | cut -c1-16)"
result=$(jq --arg p "$PROJECT_ID" --arg t "$title" '.projectId = $p | if $t != "" then .title = $t else . end' \
    "$HERE/../work-items/root.json" \
  | "$AW" work-item create --project-id "$PROJECT_ID" --idempotency-key "$key")
ROOT_ID=$(jq -er '.result.workItemId' <<<"$result")
FAMILY_ID=$(jq -er '.result.familyId' <<<"$result")
grep -v '^\(ROOT_ID\|FAMILY_ID\)=' "$OUT_ENV" > "$OUT_ENV.tmp" || true
printf 'ROOT_ID=%s\nFAMILY_ID=%s\n' "$ROOT_ID" "$FAMILY_ID" >> "$OUT_ENV.tmp"
mv "$OUT_ENV.tmp" "$OUT_ENV"
echo "ROOT_ID=$ROOT_ID"
echo "FAMILY_ID=$FAMILY_ID"
