#!/bin/sh
# Lệnh giả lập cho walk-workflow.py: đạt hoặc hỏng theo kịch bản ($HOME/seq/gate1, mỗi dòng "pass" hoặc "fail").
n=$(cat "$HOME/seq/gate1.n" 2>/dev/null || echo 0); echo $((n + 1)) > "$HOME/seq/gate1.n"
r=$(sed -n "$((n + 1))p" "$HOME/seq/gate1" 2>/dev/null); [ -n "$r" ] || r=pass
[ "$r" = pass ] && { echo "gate1: PASS"; exit 0; }
echo "gate1: kiểm tra giả lập đỏ (lần $((n + 1))): expected 2 but was 3 at Foo.java:10" >&2; exit 1
