#!/bin/sh
# MACHINE_GATE "secrets": không có file bí mật hay file database nào nằm trong repository (đã track hoặc
# chưa bị .gitignore bỏ qua). Gate chạy trong thư mục scratch; $2 là đường dẫn worktree (chỉ đọc).
# Kết quả là MỘT dòng JSON trên stdout; mã thoát luôn là 0.
set -eu
repo=$2
found=$(git --no-optional-locks -C "$repo" ls-files --cached --others --exclude-standard \
  | grep -E '(^|/)\.env(\..*)?$|\.pem$|\.p12$|\.db$' | tr '\n' ' ' || true)
jq -cn --arg found "$found" '
  {NO_SECRET_FILES: (if $found == "" then {verdict: "PASS"} else {verdict: "FAIL", detail: ("file không được có: " + $found)} end)}'
