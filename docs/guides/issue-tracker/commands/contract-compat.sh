#!/bin/sh
# COMMAND "contract-compat" (repository contracts): so spec/openapi.json trong worktree với bản đã commit (HEAD) và từ chối
# thay đổi phá vỡ: xóa path, method, response, thuộc tính, giá trị enum; đổi kiểu; thêm trường bắt buộc vào request.
set -eu
. "${AW_KIT:?đặt AW_KIT=<thư mục kit> khi chạy tay}/commands/lib.sh" # @aw-include
[ -f spec/openapi.json ] || aw_fail "contract-compat: thiếu spec/openapi.json"
PY=$(command -v python3 || command -v python) || aw_env_fail "không có python3"
"$PY" - <<'PY'
import json, subprocess, sys

cur = json.load(open("spec/openapi.json", encoding="utf-8"))
shown = subprocess.run(["git", "--no-optional-locks", "show", "HEAD:spec/openapi.json"], capture_output=True, text=True)
if shown.returncode != 0:
    print("contract-compat: PASS (chưa có bản đã commit để so)")
    sys.exit(0)
old = json.loads(shown.stdout)
problems = []


def resolve(spec, node):
    for _ in range(20):
        if not (isinstance(node, dict) and "$ref" in node):
            break
        target = spec
        for part in node["$ref"][2:].split("/"):
            target = target.get(part, {}) if isinstance(target, dict) else {}
        node = target
    return node


def schema_of(holder):
    return (((holder or {}).get("content") or {}).get("application/json") or {}).get("schema")


def shape(spec, schema):
    """({thuộc tính: (kiểu, giá trị enum)}, tập required) của một schema object hoặc mảng object."""
    s = resolve(spec, schema or {})
    if s.get("type") == "array":
        s = resolve(spec, s.get("items") or {})
    props = {}
    for name, value in (s.get("properties") or {}).items():
        v = resolve(spec, value)
        props[name] = (v.get("type"), tuple(sorted(map(str, v.get("enum", [])))))
    return props, set(s.get("required", []))


for p, item in (old.get("paths") or {}).items():
    now = (cur.get("paths") or {}).get(p)
    if now is None:
        problems.append(f"đã xóa path {p}")
        continue
    for method in ("get", "put", "post", "delete", "patch"):
        if method not in item:
            continue
        tag = f"{method.upper()} {p}"
        if method not in now:
            problems.append(f"đã xóa {tag}")
            continue
        for code, response in (item[method].get("responses") or {}).items():
            new_response = (now[method].get("responses") or {}).get(code)
            if new_response is None:
                problems.append(f"{tag}: đã xóa response {code}")
                continue
            old_props, _ = shape(old, schema_of(response))
            new_props, _ = shape(cur, schema_of(new_response))
            for name, (kind, enum) in old_props.items():
                if name not in new_props:
                    problems.append(f"{tag} {code}: đã xóa thuộc tính {name}")
                    continue
                if new_props[name][0] != kind:
                    problems.append(f"{tag} {code}: thuộc tính {name} đổi kiểu {kind} -> {new_props[name][0]}")
                if enum and set(enum) - set(new_props[name][1]):
                    problems.append(f"{tag} {code}: thuộc tính {name} mất giá trị enum {sorted(set(enum) - set(new_props[name][1]))}")
        old_body = schema_of(item[method].get("requestBody"))
        if old_body is not None:
            _, old_required = shape(old, old_body)
            _, new_required = shape(cur, schema_of(now[method].get("requestBody")))
            if new_required - old_required:
                problems.append(f"{tag}: thêm trường bắt buộc vào request: {sorted(new_required - old_required)}")
if problems:
    print(f"contract-compat: {len(problems)} thay đổi phá vỡ so với bản đã commit", file=sys.stderr)
    for line in problems[:30]:
        print("  - " + line, file=sys.stderr)
    sys.exit(1)
print("contract-compat: PASS")
PY
