#!/bin/sh
# MACHINE_GATE "contract-valid": spec/openapi.json của repository contracts là JSON OpenAPI hợp lệ. Gate chạy trong một thư mục
# scratch; $2 là đường dẫn worktree (chỉ đọc). Kết quả là MỘT dòng JSON trên stdout; mã thoát luôn là 0.
set -eu
repo=$2
PY=$(command -v python3 || command -v python)
"$PY" - "$repo/spec/openapi.json" <<'PY'
import json, sys
try:
    spec = json.load(open(sys.argv[1], encoding="utf-8"))
    ok = str(spec.get("openapi", "")).startswith("3.0") and isinstance(spec.get("paths"), dict)
    result = {"verdict": "PASS"} if ok else {"verdict": "FAIL", "detail": "thiếu openapi 3.0.x hoặc paths"}
except Exception as exc:
    result = {"verdict": "FAIL", "detail": f"không đọc được spec/openapi.json: {exc}"}
print(json.dumps({"SPEC_VALID": result}))
PY
