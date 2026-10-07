#!/usr/bin/env bash
# In trạng thái của một run: timeline, mức dùng mà provider báo cáo, evidence, và việc đang chờ người làm
# (cổng duyệt, tín hiệu, blocker, worktree bị cách ly). watch-run.sh gọi script này khi run dừng lại.
# Cách dùng: show-run.sh <runId>
set -euo pipefail
AW=${AW:-aw}
AW_STATE=${AW_STATE:-./aw-state.json}
run_id=$1
project_id=$(jq -er '.projectId' "$AW_STATE")
detail=$("$AW" run show "$run_id")
state=$(jq -r '.state' <<<"$detail")
work_item=$(jq -r '.workItemId' <<<"$detail")
echo "Run: $run_id  state: $state"
timeline=$("$AW" run timeline "$run_id")
jq -r '.entries[] | select(.kind == "NODE_RUN") | "  #\(.activationSequence) \(.nodeKey) (vòng \(.iteration)): \(.nodeState) \(.selectedOutcome // "")"' <<<"$timeline"
# Attempt hỏng vì lỗi kỹ thuật (không phải vì bước kiểm tra không đạt): mã lỗi và chi tiết nằm ở attempt.
jq -r '.entries[] | select(.kind == "EXECUTION_ATTEMPT" and (.attemptState == "FAILED" or .attemptState == "INDETERMINATE"))
  | "  attempt \(.nodeKey) #\(.attemptNumber): \(.attemptState) \(.failureCode // .terminationReason // "") \(.failureDetail // "")"' <<<"$timeline"
# Mức dùng là con số của provider CLI, cộng trên mọi attempt của run; không có khi chưa attempt nào báo cáo.
jq -r '.usage // empty | "  provider báo cáo: \(.inputTokens) token vào, \(.outputTokens) token ra, \(.costUsd * 10000 | round / 10000) USD"' <<<"$timeline"
"$AW" evidence list --project-id "$project_id" --run-id "$run_id" "$work_item" \
  | jq -r '.items[] | "  evidence \(.kind): \(.verdict)"'
# Node APPROVAL đang chờ người duyệt: in lệnh duyệt kèm các outcome hợp lệ của node.
graph=$("$AW" run graph "$run_id")
jq -r --arg run "$run_id" --argjson graph "$graph" '.approvalRequests[]? | select(.state == "PENDING")
  | .nodeKey as $k | ([$graph.nodes[] | select(.key == $k)][0].outcomes | join("|")) as $o
  | "  CHỜ DUYỆT node \($k): review-task.sh \($run) <\($o)> [\"phản hồi\"]"' <<<"$detail"
# Node WAIT đang chờ tín hiệu bên ngoài.
jq -r --arg run "$run_id" '.waitRegistrations[]? | select(.state == "ACTIVE")
  | "  CHỜ TÍN HIỆU node \(.nodeKey): aw wait signal --signal-key \(.signalName) --idempotency-key <khóa> \($run) \(.waitRegistrationId)"' <<<"$detail"
# Blocker đang mở. Blocker admission (CLI của provider đổi, thiếu quyền…) chặn một node trong khi run vẫn
# RUNNING: sửa nguyên nhân rồi cho node đó chạy lại. Các blocker còn lại gỡ bằng retry-task.sh.
diagnostics=$("$AW" run diagnostics --project-id "$project_id" "$run_id")
jq -r '.blockers[]? | select(.state == "OPEN") | if .admissionReason
  then "  BỊ CHẶN \(.type): \(.reason)\n    sửa nguyên nhân rồi chạy: aw node-run retry-blocked --reason \"<lý do>\" \(.sourceNodeRunId)"
  else "  BLOCKER \(.type) đang mở" end' <<<"$diagnostics"
jq -r '.repositoryWorkspaces[]? | select(.state == "QUARANTINED")
  | "  WORKTREE của \(.repositoryId) bị QUARANTINED: đọc operations.md mục 5.4 trước khi làm gì tiếp"' <<<"$diagnostics"
case "$state" in
  FAILED | CANCELLED)
    echo "  Run $state. Xem lỗi rồi chạy lại trên chính WorkItem này:"
    echo "    retry-task.sh $work_item [\"ghi chú cho agent\"]" ;;
esac
