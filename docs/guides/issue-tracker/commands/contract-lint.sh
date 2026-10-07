#!/bin/sh
# COMMAND "contract-lint" (repository contracts, chạy ở gốc worktree): kiểm tra spec/openapi.json hợp lệ theo quy ước của
# project. Lỗi in ra STDERR (agent nhận 4 KiB cuối). Chỉ cần python3 (thư viện chuẩn).
set -eu
. "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/lib.sh" # @aw-include
[ -f spec/openapi.json ] || aw_fail "contract-lint: thiếu spec/openapi.json"
PY=$(command -v python3 || command -v python) || aw_env_fail "không có python3"
"$PY" - spec/openapi.json <<'PY'
import json, re, sys

path = sys.argv[1]
problems = []
err = problems.append
try:
    spec = json.load(open(path, encoding="utf-8"))
except Exception as exc:
    print(f"contract-lint: {path} không phải JSON hợp lệ: {exc}", file=sys.stderr)
    sys.exit(1)
if not str(spec.get("openapi", "")).startswith("3.0"):
    err("openapi phải là 3.0.x")
for key in ("title", "version"):
    if not (spec.get("info") or {}).get(key):
        err(f"info.{key} thiếu")
paths = spec.get("paths") or {}
refs = []


def walk(node, where):
    if isinstance(node, dict):
        if "$ref" in node:
            refs.append((node["$ref"], where))
        for value in node.values():
            walk(value, where)
    elif isinstance(node, list):
        for value in node:
            walk(value, where)


ops, operation_ids = 0, {}
for p, item in paths.items():
    if not p.startswith("/api/"):
        err(f"path {p}: phải bắt đầu bằng /api/")
    shared = {x.get("name") for x in item.get("parameters", []) if isinstance(x, dict) and x.get("in") == "path"}
    for method in ("get", "put", "post", "delete", "patch"):
        op = item.get(method)
        if op is None:
            continue
        ops += 1
        tag = f"{method.upper()} {p}"
        oid = op.get("operationId")
        if not oid:
            err(f"{tag}: thiếu operationId")
        elif oid in operation_ids:
            err(f"{tag}: operationId {oid!r} trùng với {operation_ids[oid]}")
        else:
            operation_ids[oid] = tag
        if not op.get("tags"):
            err(f"{tag}: thiếu tags")
        responses = op.get("responses") or {}
        if not any(str(code).startswith("2") for code in responses):
            err(f"{tag}: thiếu response 2xx")
        declared = shared | {x.get("name") for x in op.get("parameters", []) if isinstance(x, dict) and x.get("in") == "path"}
        for name in re.findall(r"\{([^}]+)\}", p):
            if name not in declared:
                err(f"{tag}: path parameter {name!r} chưa khai báo")
        for code in ("400", "404"):
            if code in responses:
                schema = ((((responses[code] or {}).get("content") or {}).get("application/json") or {}).get("schema") or {})
                if schema.get("$ref") != "#/components/schemas/Problem":
                    err(f"{tag}: response {code} phải dùng schema Problem")
    walk(item, p)
walk(spec.get("components", {}), "components")
for ref, where in refs:
    if not ref.startswith("#/"):
        err(f"{where}: $ref ngoài tài liệu không được hỗ trợ: {ref}")
        continue
    node = spec
    for part in ref[2:].split("/"):
        if not isinstance(node, dict) or part not in node:
            err(f"{where}: $ref treo {ref}")
            break
        node = node[part]
if problems:
    print(f"contract-lint: {len(problems)} lỗi", file=sys.stderr)
    for line in problems[:30]:
        print("  - " + line, file=sys.stderr)
    sys.exit(1)
print(f"contract-lint: PASS ({len(paths)} path, {ops} operation)")
PY
