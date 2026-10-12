#!/usr/bin/env bash
# Ghi nhận các commit được tạo NGOÀI aw (IDE, terminal) trong worktree của TaskFamily hiện tại thành revision hiện
# tại của repository đó, để run bắt đầu SAU lệnh này ghim đúng HEAD. Chỉ chạy khi worktree sạch, HEAD là hậu duệ
# tuyến tính của revision aw đã ghi và không attempt nào đang ghi; nếu không `aw` từ chối và nói rõ lý do.
# Đây là quyết định của người vận hành: mã trong các commit đó CHƯA qua cổng kiểm tra nào của aw, nhưng từ đây aw
# coi nó là một phần lịch sử của family. Gate của các run sau vẫn chạy trên mã đó.
# Run đã bắt đầu trước lệnh này vẫn giữ revision cũ đã ghim (manifest bất biến): hủy và start lại bằng retry-task.sh.
# Cách dùng: adopt-head.sh [repositoryId]   (mặc định: repository của project trong aw-state.json)
set -euo pipefail
AW=${AW:-aw}
AW_STATE=${AW_STATE:-./aw-state.json}
project_id=$(jq -er '.projectId' "$AW_STATE")
family=$(jq -er '.root.familyId' "$AW_STATE")
repo=${1:-$(jq -er '.repository' "$AW_STATE")}
ws=$("$AW" workspace-set show --project-id "$project_id" "$family" \
  | jq -ec --arg r "$repo" '[.repositoryWorkspaces[] | select(.repositoryId == $r)] | max_by(.generation) // empty')
[ -n "$ws" ] || { echo "không tìm thấy workspace của repository $repo trong family $family" >&2; exit 1; }
ws_id=$(jq -r '.repositoryWorkspaceId' <<<"$ws") version=$(jq -r '.version' <<<"$ws")
worktree=$(AW_STATE="$AW_STATE" "$(dirname "$0")/worktree-path.sh" "$repo")
recorded=$(jq -r '.currentRevision' <<<"$ws") head=$(git -C "$worktree" rev-parse HEAD)
if [ "$head" = "$recorded" ]; then
  echo "HEAD của worktree đã là revision aw ghi ($head): không có gì để nhận." >&2
  exit 1
fi
echo "Các commit ngoài aw sẽ được nhận (cũ → mới):"
git -C "$worktree" log --reverse --format='  %h %an: %s' "$recorded..$head" 2> /dev/null || true
# Khóa idempotency gắn với workspace và version: gọi lại y nguyên sau lần thành công sẽ phát lại kết quả đó;
# commit ngoài mới (version đã đổi) là một lần nhận mới.
key=$(printf '%s|%s' "$ws_id" "$version" | sha256sum | cut -c1-16)
"$AW" repository-workspace adopt-head --project-id "$project_id" --expected-version "$version" \
  --idempotency-key "adopt-$key" "$ws_id" \
  | jq -c '.result | {previousRevision, adoptedRevision, adoptedCommitCount, version}'
echo "Đã nhận. Run bắt đầu từ bây giờ ghim $head; run cũ còn ghim revision cũ nên hủy và chạy lại (retry-task.sh)."
