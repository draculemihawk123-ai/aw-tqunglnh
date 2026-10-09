#!/bin/sh
# Lệnh giả lập cho walk-workflow.py: đạt hoặc hỏng theo kịch bản ($HOME/seq/checkdocs, mỗi dòng "pass" hoặc "fail").
n=$(cat "$HOME/seq/checkdocs.n" 2>/dev/null || echo 0); echo $((n + 1)) > "$HOME/seq/checkdocs.n"
r=$(sed -n "$((n + 1))p" "$HOME/seq/checkdocs" 2>/dev/null); [ -n "$r" ] || r=pass
[ "$r" = pass ] && { echo "checkdocs: PASS"; exit 0; }
echo "checkdocs: kiểm tra giả lập đỏ (lần $((n + 1))): expected 2 but was 3 at Foo.java:10" >&2; exit 1
