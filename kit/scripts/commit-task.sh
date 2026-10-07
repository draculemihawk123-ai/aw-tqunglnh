#!/usr/bin/env bash
# Sau khi đã review thay đổi trong worktree: tạo ReleaseSet, seal và tạo local commit thật trên branch
# agentkit/w-… của TaskFamily hiện tại. Cách dùng: commit-task.sh "<commit message>"
# Biến tùy chọn: AUTHOR_NAME, AUTHOR_EMAIL (mặc định lấy từ git config); FORCE=1 để commit dù family còn task dở.
# Family có nhiều repository: commit từng repository có thay đổi (mỗi repo một commit), bỏ qua repo không đổi.
set -euo pipefail
AW=${AW:-aw}
AW_STATE=${AW_STATE:-./aw-state.json}
AUTHOR_NAME=${AUTHOR_NAME:-$(git config user.name)}
AUTHOR_EMAIL=${AUTHOR_EMAIL:-$(git config user.email)}
project_id=$(jq -er '.projectId' "$AW_STATE")
family=$(jq -er '.root.familyId' "$AW_STATE")
repo=$(jq -er '.repository' "$AW_STATE")
message=$1
# Local commit gom MỌI thay đổi đang có trong worktree của family. Nếu một task của family còn dở (run đang chạy,
# đang chờ duyệt, hoặc FAILED), commit lúc này đóng dấu cả phần việc chưa được duyệt của nó.
root=$(jq -er '.root.workItemId' "$AW_STATE")
unfinished=$("$AW" work-item children --project-id "$project_id" "$root" \
  | jq -r '.items[] | select(.status == "ACTIVE" or .status == "BLOCKED") | "  \(.title): \(.status)"')
if [ -n "$unfinished" ] && [ -z "${FORCE:-}" ]; then
  printf 'Family còn task chưa xong:\n%s\n' "$unfinished" >&2
  echo "Xử lý chúng trước (show-run.sh, review-task.sh, retry-task.sh), hoặc đặt FORCE=1 để vẫn commit." >&2
  exit 1
fi
# Family nhiều repository: mỗi repository có thay đổi nhận một local commit riêng (cùng message), repository không đổi bị
# bỏ qua. Family một repository thì giống hệt trước đây.
repos=$("$AW" workspace-set show --project-id "$project_id" "$family" | jq -r '.repositoryWorkspaces[].repositoryId' | tr -d '\r')
committed=0
for r in $repos; do
  worktree=$(AW_STATE="$AW_STATE" "$(dirname "$0")/worktree-path.sh" "$r")
  # Worktree không có gì để commit thì `aw` kết thúc local commit ở FAILED với lý do NO_CHANGES (không tạo
  # commit, không thử lại). Kiểm tra trước để báo sớm, khỏi tạo một ReleaseSet thừa.
  [ -n "$(git -C "$worktree" status --porcelain)" ] || continue
  if [ "$r" = "$repo" ]; then key=$(printf '%s|%s' "$family" "$message" | sha256sum | cut -c1-16)
  else key=$(printf '%s|%s|%s' "$family" "$r" "$message" | sha256sum | cut -c1-16); fi
  ws=$("$AW" workspace-set show --project-id "$project_id" "$family" \
    | jq -ec --arg r "$r" '.repositoryWorkspaces[] | select(.repositoryId == $r)')
  revision=$(jq -r '.currentRevision' <<<"$ws")
  rs=$(jq -n --arg r "$r" --arg v "$revision" \
    '{repositories: [{repositoryId: $r, baseVcsObjectId: $v, resultVcsObjectId: $v, verdict: "PASS"}]}' \
    | "$AW" release-set create --project-id "$project_id" --family-id "$family" --idempotency-key "rs-$key")
  rs_id=$(jq -er '.result.releaseSetId' <<<"$rs")
  "$AW" release-set seal --expected-version 1 --idempotency-key "seal-$key" --yes "$rs_id" > /dev/null
  result=$("$AW" release-set local-commit --project-id "$project_id" --release-set-id "$rs_id" \
    --repository-workspace-id "$(jq -r '.repositoryWorkspaceId' <<<"$ws")" --expected-release-set-version 2 \
    --expected-workspace-version "$(jq -r '.version' <<<"$ws")" \
    --author-name "$AUTHOR_NAME" --author-email "$AUTHOR_EMAIL" --message "$message" \
    --idempotency-key "commit-$key" --yes --wait --wait-timeout 5m \
    | jq -c '.result.wait | {state, parentVcsObjectId, resultVcsObjectId}')
  if [ "$(printf '%s\n' "$repos" | wc -l)" -gt 1 ]; then echo "repository $r: $result"; else echo "$result"; fi
  committed=$((committed + 1))
done
if [ "$committed" = 0 ]; then
  echo "Không worktree nào của family có thay đổi để commit." >&2
  exit 1
fi
