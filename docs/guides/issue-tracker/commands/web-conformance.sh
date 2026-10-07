#!/bin/sh
# COMMAND "web-conformance" (repository web, chạy ở gốc worktree): mọi đường dẫn /api/... xuất hiện trong frontend/src phải có
# trong hợp đồng. $2 là đường dẫn worktree của repository contracts (chỉ đọc; Command khai argRepositories).
set -eu
. "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/lib.sh" # @aw-include
contracts=${2:?thiếu tham số: đường dẫn worktree của repository contracts}
[ -f "$contracts/spec/openapi.json" ] || aw_fail "web-conformance: thiếu $contracts/spec/openapi.json"
PY=$(command -v python3 || command -v python) || aw_env_fail "không có python3"
"$PY" - "$contracts/spec/openapi.json" <<'PY'
import json, pathlib, re, sys


def norm(path):
    path = re.sub(r"\$\{[^}]*\}", "{}", path.split("?")[0])
    path = re.sub(r"([A-Za-z])\{\}$", r"\1", path)          # /api/issues${query}: hậu tố query, không phải tham số path
    path = re.sub(r"/\d+(?=/|$)", "/{}", path)                 # id cụ thể trong test: /api/issues/1
    return re.sub(r"\{[^}]*\}", "{}", path).rstrip("/") or "/"


spec = json.load(open(sys.argv[1], encoding="utf-8"))
allowed = {norm(p) for p in (spec.get("paths") or {})}
unknown = {}
root = pathlib.Path("frontend/src")
for source in sorted(root.rglob("*")) if root.is_dir() else []:
    if source.suffix not in (".ts", ".tsx", ".js", ".jsx"):
        continue
    for line_no, line in enumerate(source.read_text(encoding="utf-8").splitlines(), 1):
        for match in re.finditer(r'["\'`](/api/[^"\'`\s]*)', line):
            if norm(match.group(1)) not in allowed:
                unknown.setdefault(norm(match.group(1)), f"{source}:{line_no}")
if unknown:
    print(f"web-conformance: {len(unknown)} đường dẫn không có trong hợp đồng", file=sys.stderr)
    for path, where in sorted(unknown.items()):
        print(f"  - NGOÀI HỢP ĐỒNG: {path} ({where})", file=sys.stderr)
    sys.exit(1)
print("web-conformance: PASS")
PY
