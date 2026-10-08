#!/bin/sh
# Kiểm tra tri thức nào thực sự đến một node AGENT, không tốn tiền: chạy node đó bằng agent giả lập trên một bản cài aw tạm
# rồi in danh sách resource mà attempt nhận (và resource bị loại).
# Cách dùng: context-check.sh <aw-project.json> [<workflow>:]<node> [--repository ID] [--component TÊN] [--risk LOW|MEDIUM|HIGH]
#                              [--expect [<skill>#]<key>]... [--absent [<skill>#]<key>]... [--keep] [--json]
# Chi tiết và điều kiện chạy: context-check.py --help
exec python3 "$(dirname "$0")/context-check.py" "$@"
