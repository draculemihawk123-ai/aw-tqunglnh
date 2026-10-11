#!/usr/bin/env bash
# Chạy lại một WorkItem có run vừa FAILED hoặc bị hủy, trên CHÍNH WorkItem đó (lịch sử, message và evidence ở
# một chỗ): in lỗi của các bước kiểm tra rồi gọi `aw work-item retry` — MỘT lệnh gửi ghi chú cho agent (tùy chọn),
# gỡ blocker của run hỏng và start run mới với đúng workflow version đã pin, trong một transaction (V11-10, ADR-041).
# Worktree KHÔNG được reset: run mới bắt đầu từ những gì run trước để lại.
# Cách dùng: retry-task.sh <workItemId> ["ghi chú cho agent"]
# Biến tùy chọn: OUT_OF_SCOPE=KEEP|PARK xử lý file run trước để lại NGOÀI scope của task (bắt buộc khi có; PARK cất vào
#                một ref Git ẩn, không xóa); WAIT_SECONDS (mặc định 2700) thời gian chờ tối đa, xem watch-run.sh.
# Muốn chạy bằng workflow version MỚI (vừa sửa kit rồi publish lại): dùng recreate-task.sh.
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

# 2. Một lệnh: ghi chú + gỡ blocker + run mới. Khóa idempotency gắn với số thứ tự run, nên gọi lại script sau một
#    lần FAILED nữa sẽ tạo run tiếp theo chứ không phát lại run cũ; gọi lại ngay sau khi lệnh đã thành công thì phát lại
#    đúng kết quả cũ.
out=$(mktemp) err=$(mktemp)
trap 'rm -f "$out" "$err"' EXIT
args=(--idempotency-key "retry-$work_item-$((runs + 1))")
[ -z "$note" ] || args+=(--note "$note")
[ -z "${OUT_OF_SCOPE:-}" ] || args+=(--out-of-scope "$OUT_OF_SCOPE")
"$AW" work-item retry "${args[@]}" "$work_item" > "$out" 2> "$err" || true
run_id=$(jq -er '.result.runId' "$out" 2> /dev/null) || { echo "retry bị từ chối:" >&2; cat "$err" >&2
  if grep -q WORKSPACE_HEAD_DRIFT "$err"; then echo "Giữ các commit ngoài aw: adopt-head.sh [repositoryId], rồi chạy lại script này." >&2; fi
  if grep -q "outside the work item's scope" "$err"; then echo "Chạy lại với OUT_OF_SCOPE=PARK (cất các file đó) hoặc OUT_OF_SCOPE=KEEP (giữ, run sau có thể vi phạm scope lần nữa)." >&2; fi
  exit 1; }
jq -r '.result | "Đã gỡ \(.resolvedBlockerIds | length) blocker, run mới \(.runId)" + (if .parked then "; đã cất file ngoài scope: \([.parked[].paths[]] | join(", "))" else "" end)' "$out"
"$(dirname "$0")/watch-run.sh" "$run_id"
