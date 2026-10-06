#!/usr/bin/env bash
# Chờ một run tới lúc cần người: run kết thúc, có node APPROVAL chờ duyệt, có node WAIT chờ tín hiệu, hoặc có
# blocker mở; rồi in trạng thái bằng show-run.sh. (`aw run start --wait` chỉ trả về khi run KẾT THÚC: với
# workflow có cổng duyệt nó sẽ đứng tới hết --wait-timeout, nên các script ở đây tự chờ.)
# Cách dùng: watch-run.sh <runId> [approvalRequestId vừa quyết định — bỏ qua khi tìm cổng đang chờ]
# Biến tùy chọn: WAIT_SECONDS (mặc định 2700) thời gian chờ tối đa. Hết giờ thì chỉ thôi chờ; run vẫn chạy tiếp,
# gọi lại watch-run.sh hoặc show-run.sh để xem.
set -euo pipefail
AW=${AW:-aw}
AW_STATE=${AW_STATE:-./aw-state.json}
WAIT_SECONDS=${WAIT_SECONDS:-2700}
run_id=$1 decided=${2:-}
project_id=$(jq -er '.projectId' "$AW_STATE")
for _ in $(seq 1 $((WAIT_SECONDS / 2))); do
  detail=$("$AW" run show "$run_id")
  case "$(jq -r '.state' <<<"$detail")" in SUCCEEDED | FAILED | CANCELLED) break ;; esac
  waiting=$(jq -r --arg old "$decided" '[(.approvalRequests[]? | select(.state == "PENDING" and .approvalRequestId != $old)),
    (.waitRegistrations[]? | select(.state == "ACTIVE"))] | length' <<<"$detail")
  [ "$waiting" = 0 ] || break
  blocked=$("$AW" run diagnostics --project-id "$project_id" "$run_id" | jq -r '[.blockers[]? | select(.state == "OPEN")] | length')
  [ "$blocked" = 0 ] || break
  sleep 2
done
exec "$(dirname "$0")/show-run.sh" "$run_id"
