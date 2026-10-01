#!/usr/bin/env python3
"""In content hash (ADR-012) của từng resource trong một document SKILL hoặc LAYER.

Cách dùng: python3 aw-resource-hashes.py <document.json>
Tái tạo đúng internal/domain/{skill,layer}/resource_identity.go:
SHA-256 của JSON canonical gồm {instruction|convention, priority, global (nếu true), selector}.
"""
import hashlib, json, sys

SELECTOR_SETS = ("componentTags", "pathTags", "taskKinds", "blockKinds", "riskClasses")

def go_json(value):
    # encoding/json của Go: key đã sort, không khoảng trắng, UTF-8 thô, escape <, >, &, U+2028, U+2029.
    text = json.dumps(value, ensure_ascii=False, separators=(",", ":"), sort_keys=True)
    for raw, esc in (("<", "\\u003c"), (">", "\\u003e"), ("&", "\\u0026"),
                     (" ", "\\u2028"), (" ", "\\u2029")):
        text = text.replace(raw, esc)
    return text

def content_hash(resource):
    body_key = "instruction" if "instruction" in resource else "convention"
    selector = {}
    for name in SELECTOR_SETS:
        values = (resource.get("selector") or {}).get(name) or []
        if values:
            selector[name] = sorted(values, key=lambda v: go_json(v).encode())
    content = {body_key: resource[body_key].replace("\r\n", "\n"),
               "priority": resource["priority"], "selector": selector}
    if resource.get("global"):
        content["global"] = True
    return "sha256:" + hashlib.sha256(go_json(content).encode("utf-8")).hexdigest()

doc = json.load(open(sys.argv[1], encoding="utf-8"))
for resource in doc["resources"]:
    print(resource["key"], content_hash(resource))
