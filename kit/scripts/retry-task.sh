#!/usr/bin/env bash
# Chạy lại một WorkItem có run vừa FAILED hoặc bị hủy, trên CHÍNH WorkItem đó (lịch sử, message và evidence ở
# một chỗ): in lỗi của các bước kiểm tra, gỡ blocker đang mở, gửi ghi chú cho agent (tùy chọn) rồi start một run
# mới với đúng workflow version đã pin. Worktree KHÔNG được reset: run mới bắt đầu từ những gì run trước để lại —
# hoàn tác những gì run mới không nên thấy TRƯỚC khi gọi script này.
# Cách dùng: retry-task.sh <workItemId> ["ghi chú cho agent"]
# Biến tùy chọn: WAIT_SECONDS (mặc định 2700) thời gian chờ tối đa, xem watch-run.sh.
# Run mới chạy lại workflow TỪ ĐẦU (aw không chạy tiếp từ node bị hỏng); các agent thấy phần việc đã có trong
# worktree và làm tiếp từ đó.
# Ghi chú Windows: jq.exe in CRLF. `$(…)` của Git Bash tự bỏ CR cuối, còn `read` thì không, nên các vòng
# `while read` lọc CR bằng tr.
set -euo pipefail
AW=${AW:-aw}
AW_STATE=${AW_STATE:-./aw-state.json}
work_item=$1 note=${2:-}
project_id=$(jq -er '.projectId' "$AW_STATE")
detail=$("$AW" work-item detail --project-id "$project_id" "$work_item")
runs=$(jq '.runs | length' <<<"$detail")
[ "$runs" -gt 0 ] || { echo "WorkItem $work_item chưa có run nào — dùng run-task.sh" >&2; exit 1; }
last_run=$(jq -er '.runs[-1].runId' <<<"$detail")
wf=$("$AW" run show "$last_run" | jq -er '.workflowVersionId')

# 1. Lỗi của các bước kiểm tra trong run trước. Một COMMAND thoát mã khác 0 để lại evidence FAILED kèm output;
#    các vòng lặp cùng một lỗi được gộp lại thành một dòng có số lần.
"$AW" evidence list --project-id "$project_id" --kind COMMAND_EXECUTION --run-id "$last_run" "$work_item" \
  | jq -c '.items[] | select(.verdict == "FAILED")' | tr -d '\r' | while read -r evidence; do
    "$AW" artifact get --project-id "$project_id" --output - "$work_item" \
        "$(jq -r '.evidenceId' <<<"$evidence")" "$(jq -r '.artifactReferences[0]' <<<"$evidence")" 2> /dev/null \
      | jq -r '"lệnh thoát mã \(.exitCode): \((.stderr // .stdout // "(không có output)") | gsub("\\s+"; " ") | .[0:600])"'
  done | uniq -c

# 2. Gỡ mọi blocker đang mở của run đó. RUN_FAILED chỉ RESOLVED được (không WAIVED được); lệnh bị từ chối khi
#    family còn worktree QUARANTINED (operations.md mục 5.4).
"$AW" run diagnostics --project-id "$project_id" "$last_run" \
  | jq -r '.blockers[]? | select(.state == "OPEN") | .blockerId' | tr -d '\r' | while read -r blocker; do
    "$AW" blocker resolve --mode RESOLVED --reason "${note:-đã xem lỗi, chạy lại}" "$blocker" > /dev/null
    echo "Đã gỡ blocker $blocker"
  done

# 3. Ghi chú cho agent: thành message của WorkItem, có trong prompt của run mới.
if [ -n "$note" ]; then
  printf '%s\n' "$note" | "$AW" message append --project-id "$project_id" --role USER \
    --idempotency-key "retry-note-$work_item-$runs" "$work_item" > /dev/null 2>&1
fi

# 4. Run mới trên cùng WorkItem. Khóa idempotency gắn với số thứ tự run, nên gọi lại script sau một lần FAILED
#    nữa sẽ tạo run tiếp theo chứ không phát lại run cũ.
out=$(mktemp) err=$(mktemp)
trap 'rm -f "$out" "$err"' EXIT
"$AW" run start --workflow-version-id "$wf" --idempotency-key "run-$work_item-$((runs + 1))" "$work_item" \
  > "$out" 2> "$err" || true
run_id=$(jq -er '.result.runId' "$out" 2> /dev/null) || { echo "run start bị từ chối:" >&2; cat "$err" >&2; exit 1; }
"$(dirname "$0")/watch-run.sh" "$run_id"
