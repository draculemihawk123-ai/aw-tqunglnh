#!/bin/sh
# COMMAND "api-conformance" (repository api, chạy ở gốc worktree): so route trong code Spring với hợp đồng. $2 là đường dẫn
# worktree của repository contracts (chỉ đọc; Command khai argRepositories). Báo route thiếu và route thừa.
set -eu
. "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/lib.sh" # @aw-include
contracts=${2:?thiếu tham số: đường dẫn worktree của repository contracts}
[ -f "$contracts/spec/openapi.json" ] || aw_fail "api-conformance: thiếu $contracts/spec/openapi.json"
PY=$(command -v python3 || command -v python) || aw_env_fail "không có python3"
"$PY" - "$contracts/spec/openapi.json" <<'PY'
import json, pathlib, re, sys


def norm(path):
    return re.sub(r"\{[^}]*\}", "{}", path).rstrip("/") or "/"


spec = json.load(open(sys.argv[1], encoding="utf-8"))
wanted = {(m.upper(), norm(p)) for p, item in (spec.get("paths") or {}).items() for m in item
          if m in ("get", "put", "post", "delete", "patch")}
found = set()
for java in pathlib.Path("backend/src/main/java").rglob("*.java"):
    source = java.read_text(encoding="utf-8")
    if "@RestController" not in source and "@Controller" not in source:
        continue
    base = re.search(r'@RequestMapping\(\s*(?:(?:value|path)\s*=\s*)?\{?\s*"([^"]*)"', source)
    prefix = base.group(1) if base else ""
    for match in re.finditer(r'@(Get|Post|Put|Patch|Delete)Mapping(\(([^)]*)\))?', source):
        sub = re.search(r'"([^"]*)"', match.group(3) or "")
        found.add((match.group(1).upper(), norm(prefix + (sub.group(1) if sub else ""))))
missing, extra = sorted(wanted - found), sorted(found - wanted)
if missing or extra:
    print(f"api-conformance: code không khớp hợp đồng ({len(missing)} thiếu, {len(extra)} thừa)", file=sys.stderr)
    for method, path in missing:
        print(f"  - CHƯA HIỆN THỰC: {method} {path} (có trong spec, không có trong controller)", file=sys.stderr)
    for method, path in extra:
        print(f"  - NGOÀI HỢP ĐỒNG: {method} {path} (có trong controller, không có trong spec)", file=sys.stderr)
    sys.exit(1)
print(f"api-conformance: PASS ({len(wanted)} route khớp hợp đồng)")
PY
