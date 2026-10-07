#!/bin/sh
# Chạy golden workload "bình luận" (kit/bench) với Claude CLI THẬT và thu số đo; tốn tiền thật (~3 USD mỗi lượt).
# Cách dùng: kit-bench.sh --label A0 --runs 2 [--tree REV|THƯ_MỤC] [--out THƯ_MỤC] [--parallel] [--setup-only] [--stop-after BƯỚC]
# So hai nhãn: kit-metrics.py compare <out>/A0 <out>/A1. Chi tiết: kit-bench.py --help và kit/bench/README.md
exec python3 "$(dirname "$0")/kit-bench.py" "$@"
