#!/bin/sh
# Claude Code Stop hook — vòng lặp trong của GATE 1 ("fail → BUILD") và kiểm tra tasks.json.
# Khi agent định kết thúc: nếu backend/ hoặc frontend/ có thay đổi thì chạy test phần đó; nếu một
# docs/features/*/tasks.json thay đổi thì kiểm tra schema. Fail => exit 2: Claude Code chặn việc kết
# thúc và đưa stderr cho agent sửa tiếp. Tối đa MAX_BLOCKS lần liên tiếp, sau đó để agent kết thúc và
# COMMAND gate1/check-docs của aw phán quyết (fail-closed ở phía aw).
set -u
MAX_BLOCKS=${STOP_GATE_MAX_BLOCKS:-3}
cat > /dev/null            # input JSON của hook (không cần dùng)
cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
counter="$(git rev-parse --git-dir 2>/dev/null)/aw-stop-gate.count"
changed() { [ -n "$(git status --porcelain --untracked-files=all -- "$1" 2>/dev/null)" ]; }
log=$(mktemp)
fail=0
if [ -f backend/pom.xml ] && changed backend; then
  (cd backend && if [ -x ./mvnw ]; then ./mvnw -B -q test; else mvn -B -q test; fi) >> "$log" 2>&1 || fail=1
fi
if [ -f frontend/package.json ] && changed frontend; then
  (cd frontend && npm install --no-audit --no-fund && npm test && npm run build) >> "$log" 2>&1 || fail=1
fi
for tasks in $(git status --porcelain --untracked-files=all -- docs/features | sed -E 's#^.{3}##' | grep '/tasks.json$'); do
  jq -e 'type == "array" and length > 0 and all(.[]; (.id | test("^T-[0-9]+$")) and (.title | length > 0)
      and (.pathScopes | length > 0) and (.riskLevel | IN("LOW","MEDIUM","HIGH")) and (.lane | IN("fast","full"))
      and (.behavior | length > 0) and (.acceptanceCriteria | length > 0))
      and ([.[].id] | length == (unique | length))' "$tasks" > /dev/null 2>> "$log" \
    || { echo "tasks.json sai schema: $tasks" >> "$log"; fail=1; }
done
if [ "$fail" = 0 ]; then
  rm -f "$counter" "$log"
  exit 0
fi
count=$(( $(cat "$counter" 2>/dev/null || echo 0) + 1 ))
echo "$count" > "$counter"
if [ "$count" -gt "$MAX_BLOCKS" ]; then
  rm -f "$counter" "$log"
  exit 0
fi
{
  echo "GATE 1 (Stop hook) chưa đạt — lần $count/$MAX_BLOCKS. Sửa lỗi dưới đây rồi mới kết thúc:"
  tail -n 60 "$log"
} >&2
rm -f "$log"
exit 2
