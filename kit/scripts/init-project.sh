#!/usr/bin/env bash
# Bước 1 của quy trình: tạo project, đăng ký repository chính, chờ probe xong và ghi aw-state.json.
# Cách dùng: init-project.sh <tên project> <repositoryId> <đường dẫn TUYỆT ĐỐI tới Git repo cục bộ> [defaultRef]
# Trên Windows (Git Bash) truyền đường dẫn kiểu Windows, ví dụ "$(cygpath -m ~/work/todolist)" → C:/Users/…:
# `aw` là chương trình Windows, không hiểu đường dẫn /c/Users/… của Git Bash.
# Chạy lại an toàn (idempotency key suy ra từ tham số).
set -euo pipefail
AW=${AW:-aw}
AW_STATE=${AW_STATE:-./aw-state.json}
name=$1 repo_id=$2 repo_path=$3 ref=${4:-main}
case "$(uname -s)" in
  MINGW* | MSYS* | CYGWIN*)
    case "$repo_path" in [A-Za-z]:[\\/]*) ;; *) echo "trên Windows cần đường dẫn dạng C:/…: dùng \"\$(cygpath -m <đường dẫn>)\"" >&2; exit 2 ;; esac ;;
  *)
    case "$repo_path" in /*) ;; *) echo "đường dẫn repository phải là tuyệt đối" >&2; exit 2 ;; esac ;;
esac
key=$(printf '%s' "$name" | sha256sum | cut -c1-16)
project_id=$(jq -n --arg n "$name" '{name: $n}' | "$AW" project create --idempotency-key "project-$key" | jq -er '.result.projectId')
jq -n --arg id "$repo_id" --arg path "$repo_path" --arg ref "$ref" \
    '{repositoryId: $id, name: $id, remoteLocator: $path, defaultRef: $ref}' \
  | "$AW" repository register --project-id "$project_id" --idempotency-key "repo-$key-$repo_id" > /dev/null
for _ in $(seq 1 60); do
  status=$("$AW" repository list "$project_id" | jq -r --arg id "$repo_id" '.repositories[] | select(.id == $id) | .status')
  [ "$status" = ACTIVE ] || [ "$status" = BLOCKED ] && break
  sleep 1
done
echo "repository $repo_id: $status"
[ "$status" = ACTIVE ] || { echo "probe chưa ACTIVE — kiểm tra đường dẫn và aw worker" >&2; exit 1; }
"$AW" component list "$project_id" | jq -r --arg id "$repo_id" '.components[] | select(.repositoryId == $id) | "  component \(.name) (\(.path))"'
state='{}'
[ -f "$AW_STATE" ] && state=$(cat "$AW_STATE")
jq --arg p "$project_id" --arg r "$repo_id" '.projectId = $p | .repository = $r' <<<"$state" > "$AW_STATE"
echo "projectId=$project_id  → $AW_STATE"
