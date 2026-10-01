#!/usr/bin/env bash
# Tạo WorkItem gốc "Todolist MVP" (TaskFamily + WorkspaceSet/worktree riêng) và ghi ROOT_ID,
# FAMILY_ID vào file id (mặc định ./aw-ids.env do publish-definitions.sh tạo ra).
set -euo pipefail
AW=${AW:-aw}
OUT_ENV=${OUT_ENV:-./aw-ids.env}
HERE=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1090
. "$OUT_ENV"
: "${PROJECT_ID:?}"
result=$(jq --arg p "$PROJECT_ID" '.projectId = $p' "$HERE/../work-items/root.json" \
  | "$AW" work-item create --project-id "$PROJECT_ID" --idempotency-key "root-$PROJECT_ID")
ROOT_ID=$(jq -er '.result.workItemId' <<<"$result")
FAMILY_ID=$(jq -er '.result.familyId' <<<"$result")
grep -v '^\(ROOT_ID\|FAMILY_ID\)=' "$OUT_ENV" > "$OUT_ENV.tmp" || true
printf 'ROOT_ID=%s\nFAMILY_ID=%s\n' "$ROOT_ID" "$FAMILY_ID" >> "$OUT_ENV.tmp"
mv "$OUT_ENV.tmp" "$OUT_ENV"
echo "ROOT_ID=$ROOT_ID"
echo "FAMILY_ID=$FAMILY_ID"
