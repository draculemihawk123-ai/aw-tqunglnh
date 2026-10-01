#!/usr/bin/env bash
# Sau khi đã review diff trong worktree: tạo ReleaseSet, seal và tạo local commit thật
# trên branch agentkit/... của TaskFamily. Cách dùng: commit-task.sh "<commit message>"
set -euo pipefail
AW=${AW:-aw}
OUT_ENV=${OUT_ENV:-./aw-ids.env}
REPO_ID=${REPO_ID:-todolist}
AUTHOR_NAME=${AUTHOR_NAME:-$(git config user.name)}
AUTHOR_EMAIL=${AUTHOR_EMAIL:-$(git config user.email)}
# shellcheck disable=SC1090
. "$OUT_ENV"
: "${PROJECT_ID:?}" "${FAMILY_ID:?chạy create-root.sh trước}"
message=$1
# Alpha: local commit trên worktree không có thay đổi không trả lỗi có kiểu — job retry tới DEAD và
# local commit kẹt ở REQUESTED. Vì vậy kiểm tra trước.
worktree=$(OUT_ENV="$OUT_ENV" REPO_ID="$REPO_ID" "$(dirname "$0")/worktree-path.sh")
if [ -z "$(git -C "$worktree" status --porcelain)" ]; then
  echo "Worktree $worktree không có thay đổi nào để commit." >&2
  exit 1
fi
key=$(printf '%s' "$message" | sha256sum | cut -c1-16)
ws=$("$AW" workspace-set show --project-id "$PROJECT_ID" "$FAMILY_ID" \
  | jq -ec --arg r "$REPO_ID" '.repositoryWorkspaces[] | select(.repositoryId == $r)')
rw_id=$(jq -r '.repositoryWorkspaceId' <<<"$ws")
rw_version=$(jq -r '.version' <<<"$ws")
revision=$(jq -r '.currentRevision' <<<"$ws")
rs=$(jq -n --arg r "$REPO_ID" --arg v "$revision" \
  '{repositories: [{repositoryId: $r, baseVcsObjectId: $v, resultVcsObjectId: $v, verdict: "PASS"}]}' \
  | "$AW" release-set create --project-id "$PROJECT_ID" --family-id "$FAMILY_ID" --idempotency-key "rs-$key")
rs_id=$(jq -er '.result.releaseSetId' <<<"$rs")
"$AW" release-set seal --expected-version 1 --idempotency-key "seal-$key" --yes "$rs_id" >/dev/null
"$AW" release-set local-commit --project-id "$PROJECT_ID" --release-set-id "$rs_id" \
  --repository-workspace-id "$rw_id" --expected-release-set-version 2 --expected-workspace-version "$rw_version" \
  --author-name "$AUTHOR_NAME" --author-email "$AUTHOR_EMAIL" --message "$message" \
  --idempotency-key "commit-$key" --yes --wait --wait-timeout 5m \
  | jq -c '.result.wait | {state, parentVcsObjectId, resultVcsObjectId}'
