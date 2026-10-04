#!/usr/bin/env bash
# Bước "chia thành N task": đọc docs/features/<slug>/tasks.json (do agent DESIGN viết, đã duyệt và commit)
# trong worktree của family hiện tại, sinh một file WorkItem cho mỗi task.
# Cách dùng: split-tasks.sh <slug> [thư mục ra, mặc định ./tasks/<slug>]
# Sau đó chạy lần lượt: run-task.sh ./tasks/<slug>/T-01.json wf-task-delivery   (rồi duyệt, commit, task tiếp)
set -euo pipefail
AW_STATE=${AW_STATE:-./aw-state.json}
slug=$1
out=${2:-./tasks/$slug}
repo=$(jq -er '.repository' "$AW_STATE")
worktree=$(AW_STATE="$AW_STATE" "$(dirname "$0")/worktree-path.sh")
tasks="$worktree/docs/features/$slug/tasks.json"
[ -s "$tasks" ] || { echo "không thấy $tasks" >&2; exit 1; }
mkdir -p "$out"
jq -c '.[]' "$tasks" | tr -d '\r' | while read -r task; do   # jq.exe trên Windows in CRLF
  id=$(jq -r '.id' <<<"$task")
  jq -n --argjson t "$task" --arg slug "$slug" --arg repo "$repo" '{
    title: "\($t.id): \($t.title)",
    parentJoinPolicy: "ALL_CHILDREN_DONE",
    effectiveScope: [{repositoryId: $repo, access: "WRITE", reason: "Task \($t.id) của tính năng \($slug)",
                      pathScopes: (($t.pathScopes + ["docs"]) | unique)}],
    contract: {
      schemaVersion: 1,
      behavior: "Thư mục tài liệu: docs/features/\($slug) (02-spec.md, 03-design.md, tasks.json). Task \($t.id), lane đề xuất: \($t.lane). \($t.behavior)",
      verificationSpec: "GATE 1 (build + test của phần code bị đổi) và bước kiểm tra tĩnh đều thoát mã 0 với evidence COMMAND_EXECUTION; GATE 2: người vận hành duyệt diff.",
      riskLevel: $t.riskLevel,
      acceptanceCriteria: [$t.acceptanceCriteria[] | {description: ., verificationRef: "COMMAND_EXECUTION"}],
      workflowVersionId: "WORKFLOW_VERSION_ID"}}' > "$out/$id.json"
  echo "$out/$id.json"
done
