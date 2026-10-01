#!/bin/sh
# COMMAND node "backend-test": chạy trong thư mục gốc của worktree repository todolist.
set -eu
cd backend
if [ -x ./mvnw ]; then
  exec ./mvnw -B -q test
fi
exec mvn -B -q test
