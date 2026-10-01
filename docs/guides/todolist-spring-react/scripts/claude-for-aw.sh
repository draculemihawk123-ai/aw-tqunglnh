#!/bin/sh
# Wrapper giữa `aw worker` và Claude CLI thật. Truyền ĐƯỜNG DẪN FILE NÀY cho
# `aw serve --claude-executable` và `aw worker --claude-executable`.
#
# Vì sao cần: aw spawn tiến trình agent với environment RỖNG (không PATH, không HOME —
# đã kiểm chứng trên Alpha; `aw worker --env-allowlist` không áp dụng cho agent). Claude CLI
# cần HOME để đọc thông tin đăng nhập (~/.claude) và PATH để chạy git/java/mvn/node/npm.
#
# Lưu ý: mỗi lần sửa file này (hoặc thay CLAUDE_BIN) phải đăng ký lại adapter build
# (chạy lại scripts/publish-definitions.sh), nếu không AGENT node sẽ bị ADAPTER_BUILD_DRIFT.
# aw chỉ băm file wrapper này, nên nâng cấp Claude CLI phía sau KHÔNG bị phát hiện là drift.

# --- SỬA cho máy của bạn ---------------------------------------------------------
export HOME="/home/your-user"
export PATH="/usr/local/bin:/usr/bin:/bin"          # phải chứa claude, git, java, mvn, node, npm
# export JAVA_HOME="/usr/lib/jvm/java-21-openjdk"
CLAUDE_BIN="/usr/local/bin/claude"
# ----------------------------------------------------------------------------------

# aw probe phiên bản bằng `--version`: giữ nguyên lời gọi này.
if [ "$1" = "--version" ]; then
  exec "$CLAUDE_BIN" --version
fi

# aw gọi: -p --input-format text --output-format stream-json --verbose --model <model>
# (prompt JSON đi qua stdin). Thêm permission mode cho phép agent sửa file mà không hỏi;
# quyền chạy lệnh Bash lấy từ .claude/settings.json trong repository.
exec "$CLAUDE_BIN" "$@" --permission-mode acceptEdits
