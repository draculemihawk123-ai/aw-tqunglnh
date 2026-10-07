#!/bin/sh
# COMMAND "quality": kiểm tra tĩnh trên thay đổi của task, chạy ở gốc worktree của repository.
# Mã thoát 0 là đạt. Khác 0 là không đạt: phần stderr được lưu trong evidence và gửi lại cho agent BUILD
# (checkFailures) khi node khai failureOutcome.
set -eu
fail=0

# 1. Không có test nào bị tắt (@Disabled của JUnit, it.skip/test.skip/describe.skip của Vitest).
for dir in backend/src frontend/src; do
  [ -d "$dir" ] || continue
  files=$(grep -rIlE '@Disabled|(^|[^A-Za-z_])(it|test|describe)\.skip\(' "$dir" 2>/dev/null || true)
  if [ -n "$files" ]; then
    echo "TEST BỊ TẮT (@Disabled hoặc .skip): bật lại hoặc xóa hẳn kèm lý do, không tắt để cho qua." >&2
    echo "$files" >&2
    fail=1
  fi
done

# 2. Migration Flyway đã commit không bị sửa hay xóa (chỉ được thêm file V<n+1> mới).
changed=$(git --no-optional-locks status --porcelain -- backend/src/main/resources/db/migration \
  | grep -E '^( M|M |MM| D|D |R )' || true)
if [ -n "$changed" ]; then
  echo "MIGRATION ĐÃ COMMIT BỊ SỬA HOẶC XÓA: hoàn tác và thêm một file V<n+1> mới." >&2
  echo "$changed" >&2
  fail=1
fi

if [ "$fail" = 0 ]; then
  echo "QUALITY: PASS"
fi
exit "$fail"
