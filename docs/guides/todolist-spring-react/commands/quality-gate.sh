#!/bin/sh
# MACHINE_GATE "quality": kiểm tra tĩnh trên nhánh chính sau khi merge. Gate chạy trong một thư mục scratch;
# $2 là đường dẫn worktree của repository (chỉ đọc). Kết quả là MỘT dòng JSON trên stdout, mỗi tiêu chí
# {"verdict": "PASS" | "FAIL" | "ERROR", "detail": "…"}. Mã thoát luôn là 0: verdict nằm trong JSON.
set -eu
repo=$2

# Tiêu chí 1: không có test nào bị tắt (@Disabled của JUnit, it.skip/test.skip/describe.skip của Vitest).
disabled=""
for dir in backend/src frontend/src; do
  [ -d "$repo/$dir" ] || continue
  found=$(cd "$repo" && grep -rIlE '@Disabled|(^|[^A-Za-z_])(it|test|describe)\.skip\(' "$dir" 2> /dev/null || true)
  disabled="$disabled$found"
done

# Tiêu chí 2: không có hai migration Flyway cùng số version. Hai nhánh tính năng cùng thêm V2__… là lỗi chỉ
# lộ ra sau khi merge: từng nhánh riêng đều xanh, gộp lại thì Flyway từ chối khởi động.
duplicated=""
dir="$repo/backend/src/main/resources/db/migration"
if [ -d "$dir" ]; then
  duplicated=$(ls "$dir" | sed -n 's/^V\([0-9][0-9_.]*\)__.*\.sql$/\1/p' | sort | uniq -d | tr '\n' ' ')
fi

jq -cn --arg disabled "$disabled" --arg duplicated "$duplicated" '
  def result(bad; text): if bad == "" then {verdict: "PASS"} else {verdict: "FAIL", detail: (text + bad)} end;
  {NO_DISABLED_TESTS: result($disabled; "test bị tắt trong: "),
   MIGRATION_VERSIONS_UNIQUE: result($duplicated; "số version bị trùng: V")}'
