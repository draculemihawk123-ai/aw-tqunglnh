#!/usr/bin/env bash
# Đăng ký thêm một repository vào project hiện tại (project nhiều repository), chờ probe xong.
# Cách dùng: add-repository.sh <repositoryId> <đường dẫn TUYỆT ĐỐI tới Git repo cục bộ> [defaultRef]
# Repository đầu tiên đăng ký bằng init-project.sh. Id repository là duy nhất trên toàn bản cài. Chạy lại an toàn.
# Trên Windows (Git Bash) truyền đường dẫn dạng C:/… (cygpath -m), như init-project.sh.
set -euo pipefail
AW=${AW:-aw}
AW_STATE=${AW_STATE:-./aw-state.json}
repo_id=$1 repo_path=$2 ref=${3:-main}
case "$(uname -s)" in
  MINGW* | MSYS* | CYGWIN*)
    case "$repo_path" in [A-Za-z]:[\\/]*) ;; *) echo "trên Windows cần đường dẫn dạng C:/…: dùng \"\$(cygpath -m <đường dẫn>)\"" >&2; exit 2 ;; esac ;;
  *)
    case "$repo_path" in /*) ;; *) echo "đường dẫn repository phải là tuyệt đối" >&2; exit 2 ;; esac ;;
esac
project_id=$(jq -er '.projectId' "$AW_STATE")
key=$(printf '%s|%s' "$project_id" "$repo_id" | sha256sum | cut -c1-16)
# V11-12: kiểm tra đường dẫn TRƯỚC khi đăng ký — cùng bộ thăm dò, không ghi gì. Lỗi dạng "chưa git init" dừng ở đây với lệnh sửa chép dán được.
# (aw cũ chưa có lệnh `repository check` thì bỏ qua bước này.)
if check=$("$AW" repository check --project-id "$project_id" --path "$repo_path" --ref "$ref" 2>/dev/null | tr -d '\r'); then
  if [ "$(jq -r '.ok' <<<"$check")" != true ]; then
    echo "repository $repo_id: không đăng ký được — $(jq -r '.failureKind + ": " + .message' <<<"$check")" >&2
    jq -r 'select(.hint) | "  → " + .hint' <<<"$check" >&2
    exit 1
  fi
fi
jq -n --arg id "$repo_id" --arg path "$repo_path" --arg ref "$ref" \
    '{repositoryId: $id, name: $id, remoteLocator: $path, defaultRef: $ref}' \
  | "$AW" repository register --project-id "$project_id" --idempotency-key "repo-$key" > /dev/null
status=
for _ in $(seq 1 60); do
  status=$("$AW" repository list "$project_id" | jq -r --arg id "$repo_id" '.repositories[] | select(.id == $id) | .status' | tr -d '\r')
  [ "$status" = ACTIVE ] || [ "$status" = BLOCKED ] && break
  sleep 1
done
echo "repository $repo_id: $status"
if [ "$status" != ACTIVE ]; then
  # V11-12: lý do và cách sửa nằm ngay trong `repository list` (lastProbeError); chi tiết đầy đủ ở `aw repository onboarding`.
  "$AW" repository list "$project_id" | tr -d '\r' | jq -r --arg id "$repo_id" '.repositories[] | select(.id == $id) | .lastProbeError // empty | "  " + (.failureKind // "BLOCKED") + ": " + .message + (if .hint then "\n  → " + .hint else "" end)' >&2
  echo "  sửa đăng ký: aw repository update --expected-version <version> --remote-locator <đường dẫn đúng> $repo_id; bỏ: aw repository disable|delete (--expected-version <version> --reason <lý do> $repo_id)" >&2
fi
[ "$status" = ACTIVE ] || { echo "probe chưa ACTIVE — kiểm tra đường dẫn và aw worker" >&2; exit 1; }
"$AW" component list "$project_id" | jq -r --arg id "$repo_id" '.components[] | select(.repositoryId == $id) | "  component \(.name) (\(.path))"' | tr -d '\r'
