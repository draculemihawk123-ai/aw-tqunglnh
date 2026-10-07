#!/bin/sh
# Lệnh giả lập cho walk-workflow.py: đạt hoặc hỏng theo kịch bản ($HOME/seq/expectfail, mỗi dòng "pass" hoặc "fail").
n=$(cat "$HOME/seq/expectfail.n" 2>/dev/null || echo 0); echo $((n + 1)) > "$HOME/seq/expectfail.n"
r=$(sed -n "$((n + 1))p" "$HOME/seq/expectfail" 2>/dev/null); [ -n "$r" ] || r=pass
[ "$r" = pass ] && { echo "expectfail: PASS"; exit 0; }
echo "expectfail: kiểm tra giả lập đỏ (lần $((n + 1))): expected 2 but was 3 at Foo.java:10" >&2; exit 1
