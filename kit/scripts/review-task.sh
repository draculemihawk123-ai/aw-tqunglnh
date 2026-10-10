#!/usr/bin/env bash
# Quyết định node APPROVAL đang chờ của một run (cổng duyệt, NEEDS_INFO…), rồi chờ run đi tiếp.
# Cách dùng: review-task.sh [--commit "<commit message>"] <runId> <outcome> ["phản hồi cho agent"]
# Biến tùy chọn: WAIT_SECONDS (mặc định 2700) thời gian chờ tối đa, xem watch-run.sh.
#   COMMIT_MESSAGE (hoặc --commit): duyệt VÀ commit trong một thao tác (V11-07). Chỉ dùng được khi node duyệt bật
#     release cho outcome đó (`show-run.sh` / `run show` in `release`). Commit MỌI file đang thay đổi của mọi repository
#     của family, tác giả lấy từ `git config` của máy chạy aw; muốn chọn từng file hoặc cất phần còn lại (UNSELECTED=PARK)
#     thì dùng giao diện web. Thiếu user.name/user.email thì aw từ chối cả lệnh và cổng vẫn chờ.
#   UNSELECTED=KEEP|PARK: file không được commit (mặc định KEEP; với lệnh này mọi file đều được chọn nên chỉ có ý nghĩa
#     khi bạn tự sửa --commit-spec).
#   outcome là một outcome khai báo của node đó (show-run.sh in sẵn danh sách).
#   Phản hồi được append thành message USER của WorkItem; các node AGENT chạy sau đó nhận nó trong phần
#   messages của prompt (trong giới hạn messageBudget của agent; message mới nhất luôn được giữ).
set -euo pipefail
AW=${AW:-aw}
AW_STATE=${AW_STATE:-./aw-state.json}
if [ "${1:-}" = "--commit" ]; then COMMIT_MESSAGE=${2:?--commit cần một commit message}; shift 2; fi
project_id=$(jq -er '.projectId' "$AW_STATE")
run_id=$1 outcome=$2 feedback=${3:-}
detail=$("$AW" run show "$run_id")
work_item=$(jq -er '.workItemId' <<<"$detail")
request=$(jq -ec '[.approvalRequests[]? | select(.state == "PENDING")][0] // error("run không có approval nào đang chờ")' <<<"$detail")
request_id=$(jq -r '.approvalRequestId' <<<"$request")
if [ -n "$feedback" ]; then
  printf '%s\n' "$feedback" | "$AW" message append --project-id "$project_id" --role USER \
    --idempotency-key "feedback-$request_id" "$work_item" > /dev/null 2>&1
fi
commit_args=()
if [ -n "${COMMIT_MESSAGE:-}" ]; then
  # Node không bật release cho outcome này thì aw từ chối; báo sớm và nói rõ thay vì để lỗi 422 khó hiểu.
  jq -e --arg o "$outcome" '(.release // null) != null and (.release.outcomes | index($o)) != null' <<<"$request" > /dev/null \
    || { echo "node $(jq -r '.nodeKey' <<<"$request") không bật commit cho outcome '$outcome' (xem 'release' trong: aw run show $run_id)" >&2; exit 1; }
  family=$(jq -er '.familyId' <<<"$detail")
  set=$("$AW" workspace-set show --project-id "$project_id" "$family")
  set_id=$(jq -er '.workspaceSetId' <<<"$set")
  repos='[]'
  while IFS= read -r ws; do
    ws=$(printf '%s' "$ws" | tr -d '\r')
    [ "$(jq -r '.state' <<<"$ws")" = READY ] || continue
    diff=$("$AW" repository-workspace diff --project-id "$project_id" --repository-id "$(jq -r '.repositoryId' <<<"$ws")" \
      --workspace-set-id "$set_id" --base-revision "$(jq -r '.currentRevision' <<<"$ws")" \
      --base-revision-generation "$(jq -r '.generation' <<<"$ws")" --working-tree)
    files=$(jq -c '[.files[] | {path, status, contentHash}]' <<<"$diff")
    [ "$files" != "[]" ] || continue
    repos=$(jq -c --argjson f "$files" --argjson w "$ws" --arg u "${UNSELECTED:-KEEP}" \
      '. + [{repositoryWorkspaceId: $w.repositoryWorkspaceId, expectedWorkspaceVersion: $w.version, files: $f, unselected: $u}]' <<<"$repos")
  done < <(jq -c '.repositoryWorkspaces[]' <<<"$set")
  # Không có file nào thay đổi: commit rỗng (aw ghi NOTHING_TO_COMMIT, không phải lỗi).
  commit_args=(--commit-spec "$(jq -cn --arg m "$COMMIT_MESSAGE" --argjson r "$repos" '{message: $m, repositories: $r}')")
fi
"$AW" approval resolve --outcome "$outcome" --reason "${feedback:-$outcome}" \
  --expected-version "$(jq -r '.version' <<<"$request")" --idempotency-key "resolve-$request_id" \
  ${commit_args[@]+"${commit_args[@]}"} "$run_id" "$request_id" > /dev/null
echo "Đã chọn '$outcome' cho node $(jq -r '.nodeKey' <<<"$request"); chờ run chạy tiếp..."
"$(dirname "$0")/watch-run.sh" "$run_id" "$request_id"
