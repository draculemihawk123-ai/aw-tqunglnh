#!/usr/bin/env python3
"""Publish mọi definition mà một project khai báo trong aw-project.json lên một bản cài aw.

Cách dùng:
    aw-publish.py aw-project.json --check            # chỉ kiểm tra khai báo + template (không gọi aw)
    aw-publish.py aw-project.json                    # publish, ghi trạng thái vào aw-state.json

Biến môi trường: AW (mặc định "aw"), AW_DB, AW_ARTIFACT_ROOT, AW_WORKSPACE_ROOT (giống aw serve/worker),
AW_STATE (mặc định ./aw-state.json, phải có projectId — tạo bằng init-project.sh), CLAUDE_EXECUTABLE
(đúng đường dẫn đã truyền cho --claude-executable của aw serve/worker; bắt buộc nếu khai báo agent).

Thứ tự publish (đúng thứ tự phụ thuộc): LAYER, SKILL, script skill, ENGINEERING_PACK, POLICY, mỗi agent một
CONTEXT policy + AGENT_PROFILE, COMMAND, adapter build, WORKFLOW (từ template, giải quyết {"$ref": ...}),
gán Engineering Pack cho component. Idempotency key suy ra từ nội dung: chạy lại khi không đổi gì trả về
đúng các version cũ; sửa một file chỉ tạo version mới cho nó và những gì phụ thuộc vào nó.
"""
import argparse
import hashlib
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import time

sys.dont_write_bytecode = True  # không để lại __pycache__ cạnh script khi nạp aw-resource-hashes.py
HERE = os.path.dirname(os.path.abspath(__file__))
_spec = importlib.util.spec_from_file_location("aw_resource_hashes", os.path.join(HERE, "aw-resource-hashes.py"))
_hashes = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_hashes)

DEFAULT_ENV_ALLOWLIST = ["PATH", "HOME", "JAVA_HOME", "MAVEN_OPTS", "JAVA_TOOL_OPTIONS",
                         "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"]
CLAUDE_EVENT_KINDS = ["EXECUTION_STARTED", "STATUS_CHANGED", "ASSISTANT_MESSAGE", "TOOL_CALL_STARTED",
                      "TOOL_CALL_FINISHED", "USAGE_REPORTED", "CHECKPOINT_PROPOSED", "DIAGNOSTIC", "EXECUTION_FINISHED"]
REF_KINDS = {"agent": "AGENT_PROFILE", "command": "COMMAND", "policy": "POLICY", "workflow": "WORKFLOW",
             "layer": "LAYER", "skill": "SKILL", "pack": "ENGINEERING_PACK"}


class ManifestError(Exception):
    pass


# ---------------------------------------------------------------- khai báo project

def load_manifest(path):
    base = os.path.dirname(os.path.abspath(path))
    with open(path, encoding="utf-8") as f:
        manifest = json.load(f)
    manifest["_base"] = base
    for key in ("layers", "skills", "scriptSkills", "packs", "policies", "agents", "commands", "workflows"):
        manifest.setdefault(key, [])
    manifest.setdefault("prefix", "")
    manifest.setdefault("contextBudgetBytes", 65536)
    manifest.setdefault("provider", {})
    manifest["provider"].setdefault("key", "claude")
    manifest["provider"].setdefault("model", "sonnet")
    manifest["provider"].setdefault("configIdentity", manifest["prefix"] + "claude")
    return manifest


def read_json(manifest, rel):
    with open(os.path.join(manifest["_base"], rel), encoding="utf-8") as f:
        return json.load(f)


def resources_of(manifest, item_id):
    """Danh sách resource (dict) của một Layer/Skill/script skill đã khai báo."""
    for entry in manifest["layers"] + manifest["skills"]:
        if entry["id"] == item_id:
            return read_json(manifest, entry["file"])["resources"]
    for entry in manifest["scriptSkills"]:
        if entry["id"] == item_id:
            return script_skill_doc(manifest, entry)["resources"]
    raise ManifestError(f"không có Layer/Skill nào tên {item_id!r}")


def script_skill_doc(manifest, entry):
    resources = []
    for rel in entry["files"]:
        with open(os.path.join(manifest["_base"], rel), encoding="utf-8") as f:
            body = f.read()
        resources.append({"key": os.path.basename(rel), "instruction": body, "priority": "REQUIRED_PROCEDURE",
                          "global": True, "selector": {},
                          "provenance": {"owner": entry.get("owner", "platform"),
                                         "source": os.path.dirname(rel) or ".", "revision": entry.get("revision", "v1")}})
    return {"resources": resources}


def split_selector(selector):
    """"skill-x#key" -> ("skill-x", "key"); "layer-y" -> ("layer-y", None)."""
    if "#" in selector:
        item_id, key = selector.split("#", 1)
        return item_id, key
    return selector, None


def lint(manifest):
    """Kiểm tra khai báo trước khi gọi aw: file tồn tại, id duy nhất, tham chiếu giải quyết được."""
    problems = []
    seen = {}
    for section, kind in (("layers", "LAYER"), ("skills", "SKILL"), ("scriptSkills", "SKILL"),
                          ("packs", "ENGINEERING_PACK"), ("policies", "POLICY"), ("agents", "AGENT_PROFILE"),
                          ("commands", "COMMAND"), ("workflows", "WORKFLOW")):
        for entry in manifest[section]:
            item_id = entry.get("id", "")
            if not item_id:
                problems.append(f"{section}: thiếu id")
                continue
            if item_id in seen:
                problems.append(f"id {item_id!r} bị trùng ({seen[item_id]} và {section}) — definitionId là duy nhất trên toàn bản cài")
            seen[item_id] = section
            for field in ("file", "template"):
                if field in entry and not os.path.isfile(os.path.join(manifest["_base"], entry[field])):
                    problems.append(f"{section}/{item_id}: không thấy file {entry[field]}")
            for rel in entry.get("files", []):
                if not os.path.isfile(os.path.join(manifest["_base"], rel)):
                    problems.append(f"{section}/{item_id}: không thấy file {rel}")
    if problems:
        return problems
    passive = {e["id"] for e in manifest["layers"] + manifest["skills"]}
    keys_by_item = {}
    for item_id in passive:
        keys_by_item[item_id] = [r["key"] for r in resources_of(manifest, item_id)]
    for pack in manifest["packs"]:
        for dep in pack.get("include", []):
            if dep not in passive and dep not in {p["id"] for p in manifest["packs"]}:
                problems.append(f"packs/{pack['id']}: include {dep!r} không phải Layer/Skill/Pack đã khai báo")
    for agent in manifest["agents"]:
        keys = []
        for selector in agent.get("resources", []):
            item_id, key = split_selector(selector)
            if item_id not in passive:
                problems.append(f"agents/{agent['id']}: {selector!r} không trỏ tới Layer/Skill đã khai báo")
            elif key and key not in keys_by_item[item_id]:
                problems.append(f"agents/{agent['id']}: {item_id} không có resource {key!r}")
            else:
                keys += [key] if key else keys_by_item[item_id]
        duplicated = sorted({k for k in keys if keys.count(k) > 1})
        if duplicated:
            problems.append(f"agents/{agent['id']}: resource key trùng {duplicated} — resolver sẽ báo conflict")
    script_keys = {s["id"]: [os.path.basename(f) for f in s["files"]] for s in manifest["scriptSkills"]}
    policy_docs = {p["id"]: read_json(manifest, p["file"]) for p in manifest["policies"]}
    for command in manifest["commands"]:
        item_id, key = split_selector(command.get("script", ""))
        if item_id not in script_keys or key not in script_keys[item_id]:
            problems.append(f"commands/{command['id']}: script {command.get('script')!r} phải có dạng <scriptSkill>#<tên file>")
        for policy_id in command.get("policies", []):
            if policy_id not in policy_docs:
                problems.append(f"commands/{command['id']}: policy {policy_id!r} chưa khai báo")
        if command.get("network", "NONE") == "ALLOWED":
            granted = any("NETWORK_ACCESS" in ((policy_docs.get(p) or {}).get("permission") or {}).get("grantedCapabilities", [])
                          for p in command.get("policies", []))
            if not granted:
                problems.append(f"commands/{command['id']}: network ALLOWED cần một PERMISSION policy cấp NETWORK_ACCESS trong 'policies'")
    declared = {"AGENT_PROFILE": {a["id"] for a in manifest["agents"]}, "COMMAND": {c["id"] for c in manifest["commands"]},
                "POLICY": set(policy_docs), "WORKFLOW": {w["id"] for w in manifest["workflows"]},
                "LAYER": {l["id"] for l in manifest["layers"]},
                "SKILL": {s["id"] for s in manifest["skills"] + manifest["scriptSkills"]},
                "ENGINEERING_PACK": {p["id"] for p in manifest["packs"]}}
    for workflow in manifest["workflows"]:
        template = read_json(manifest, workflow["template"])
        for ref in collect_refs(template):
            if ref == "adapter":
                if not manifest["agents"]:
                    problems.append(f"workflows/{workflow['id']}: dùng $ref adapter nhưng không khai báo agent nào")
                continue
            kind_word, _, ref_id = ref.partition(":")
            kind = REF_KINDS.get(kind_word)
            if not kind or ref_id not in declared.get(kind, set()):
                problems.append(f"workflows/{workflow['id']}: $ref {ref!r} không giải quyết được")
        problems += lint_graph(workflow["id"], template)
    return problems


def collect_refs(node):
    if isinstance(node, dict):
        if set(node) == {"$ref"}:
            return [node["$ref"]]
        return [r for v in node.values() for r in collect_refs(v)]
    if isinstance(node, list):
        return [r for v in node for r in collect_refs(v)]
    return []


def lint_graph(workflow_id, doc):
    """Kiểm tra nhanh những lỗi graph hay gặp (aw definition validate vẫn là kiểm tra cuối cùng)."""
    problems = []
    nodes = {n.get("key"): n for n in doc.get("nodes", [])}
    edges = doc.get("edges", [])
    for node in nodes.values():
        for outcome in node.get("outcomes") or []:
            if not any(e["from"] == node["key"] and e["outcome"] == outcome for e in edges):
                problems.append(f"workflows/{workflow_id}: node {node['key']!r} có outcome {outcome!r} nhưng không có edge")
        if node.get("type") in ("AGENT", "COMMAND", "MACHINE_GATE"):
            config = node.get({"AGENT": "agent", "COMMAND": "command", "MACHINE_GATE": "machineGate"}[node["type"]]) or {}
            if len(config.get("policyRefs", [])) < 2:
                problems.append(f"workflows/{workflow_id}: node {node['key']!r} cần pin một policy ATTEMPT và một PERMISSION")
        if node.get("type") == "COMMAND" and len(node.get("outcomes") or []) > 1:
            problems.append(f"workflows/{workflow_id}: COMMAND {node['key']!r} chỉ được một outcome")
        if node.get("type") == "ROUTER" and len(node.get("outcomes") or []) > 1:
            problems.append(f"workflows/{workflow_id}: ROUTER {node['key']!r} chỉ được một outcome trong Alpha")
    for edge in edges:
        if edge.get("from") not in nodes or edge.get("to") not in nodes:
            problems.append(f"workflows/{workflow_id}: edge {edge.get('key')!r} trỏ tới node không tồn tại")
    return problems


# ---------------------------------------------------------------- gọi aw

class Aw:
    def __init__(self, binary):
        self.binary = binary

    def run(self, args, body=None, check=True):
        proc = subprocess.run([self.binary, *args], input=body, capture_output=True, text=True)
        if check and proc.returncode != 0:
            raise SystemExit(f"aw {' '.join(args)} thất bại:\n{proc.stderr or proc.stdout}")
        return proc

    def json(self, args, body=None):
        return json.loads(self.run(args, body).stdout)


class Publisher:
    def __init__(self, manifest, aw, state_path):
        self.m = manifest
        self.aw = aw
        self.state_path = state_path
        with open(state_path, encoding="utf-8") as f:
            self.state = json.load(f)
        if not self.state.get("projectId"):
            raise SystemExit(f"{state_path} chưa có projectId — chạy init-project.sh trước")
        self.state["prefix"] = manifest["prefix"]
        self.state.setdefault("definitions", {})
        version = aw.json(["version", "--json"])
        self.os, self.toolchain = version["os"], version["goVersion"]

    def def_id(self, item_id):
        return self.m["prefix"] + item_id

    def record(self, kind, item_id, version_id):
        self.state["definitions"].setdefault(kind, {})[item_id] = {"definitionId": self.def_id(item_id),
                                                                   "versionId": version_id}

    def version(self, kind, item_id):
        return self.state["definitions"][kind][item_id]["versionId"]

    def pin(self, kind, item_id):
        return {"kind": kind, "definitionId": self.def_id(item_id), "versionId": self.version(kind, item_id)}

    def publish(self, kind, item_id, name, document, project=False):
        """Tạo vỏ definition nếu chưa có (Alpha báo lỗi chung khi tạo trùng), rồi publish một version."""
        def_id = self.def_id(item_id)
        scope = ["--project-id", self.state["projectId"]] if project else []
        if self.aw.run(["definition", "show", "--kind", kind, *scope, def_id], check=False).returncode != 0:
            self.aw.run(["definition", "create", "--kind", kind, *scope, "--idempotency-key", f"create-{def_id}"],
                        json.dumps({"definitionId": def_id, "name": name or item_id}))
        content = json.dumps(document, ensure_ascii=False, indent=2) + "\n"
        key = f"pub-{def_id}-{hashlib.sha256(content.encode()).hexdigest()[:16]}"
        with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False, encoding="utf-8") as f:
            f.write(content)
        try:
            result = self.aw.json(["definition", "publish", "--kind", kind, *scope, "--idempotency-key", key,
                                   "--yes", "--file", f.name, def_id])
        finally:
            os.unlink(f.name)
        self.record(kind, item_id, result["result"]["id"])
        print(f"  {kind:<16} {def_id:<36} {result['result']['id']}")

    def resource_refs(self, selector):
        item_id, key = split_selector(selector)
        kind = "LAYER" if any(l["id"] == item_id for l in self.m["layers"]) else "SKILL"
        owner = self.version(kind, item_id)
        refs = []
        for resource in resources_of(self.m, item_id):
            if key is None or resource["key"] == key:
                refs.append({"ownerVersionId": owner, "resourceKey": resource["key"],
                             "contentHash": _hashes.content_hash(resource)})
        return refs

    def run_all(self):
        m = self.m
        print("== Layer / Skill")
        for entry in m["layers"]:
            self.publish("LAYER", entry["id"], entry.get("name"), read_json(m, entry["file"]))
        for entry in m["skills"]:
            self.publish("SKILL", entry["id"], entry.get("name"), read_json(m, entry["file"]))
        for entry in m["scriptSkills"]:
            self.publish("SKILL", entry["id"], entry.get("name"), script_skill_doc(m, entry))

        print("== Engineering Pack")
        for pack in m["packs"]:
            deps = []
            for dep in pack.get("include", []):
                kind = ("LAYER" if any(l["id"] == dep for l in m["layers"]) else
                        "ENGINEERING_PACK" if any(p["id"] == dep for p in m["packs"]) else "SKILL")
                deps.append(self.pin(kind, dep))
            self.publish("ENGINEERING_PACK", pack["id"], pack.get("name"), {"dependencies": deps})

        print("== Policy")
        for entry in m["policies"]:
            self.publish("POLICY", entry["id"], entry.get("name"), read_json(m, entry["file"]))

        print("== Agent (mỗi agent một CONTEXT policy = bảng định tuyến của bước đó)")
        for agent in m["agents"]:
            refs = [r for selector in agent.get("resources", []) for r in self.resource_refs(selector)]
            context = {"category": "CONTEXT",
                       "context": {"selector": [self.def_id(agent["id"])],
                                   "budget": {"maxTokens": agent.get("contextBudgetBytes", m["contextBudgetBytes"])},
                                   "resourceRefs": refs}}
            ctx_id = "ctx-" + agent["id"]
            self.publish("POLICY", ctx_id, f"Context: {agent.get('name', agent['id'])}", context)
            profile = {"providerKey": m["provider"]["key"], "model": agent.get("model", m["provider"]["model"]),
                       "toolRefs": agent.get("toolRefs", ["Read", "Edit", "Write", "Bash"]),
                       "contextPolicyRef": self.pin("POLICY", ctx_id),
                       "compatibility": {"os": [self.os]}, "budget": {"maxTokens": agent.get("maxTokens", 200000)}}
            self.publish("AGENT_PROFILE", agent["id"], agent.get("name"), profile)

        print("== Command")
        for command in m["commands"]:
            skill_id, key = split_selector(command["script"])
            skill = next(s for s in m["scriptSkills"] if s["id"] == skill_id)
            resource = next(r for r in script_skill_doc(m, skill)["resources"] if r["key"] == key)
            doc = {"executable": {"ownerVersionId": self.version("SKILL", skill_id), "resourceKey": key,
                                  "contentHash": _hashes.content_hash(resource)},
                   "argv": [{"kind": "LITERAL", "value": "run"}],
                   "cwdRepositoryTarget": command.get("repository", m["repository"]),
                   "compatibility": {"os": [self.os]},
                   "envAllowlist": command.get("envAllowlist", DEFAULT_ENV_ALLOWLIST),
                   "networkAccess": command.get("network", "NONE"),
                   "policyRefs": [self.pin("POLICY", p) for p in command.get("policies", [])],
                   "timeoutSeconds": command.get("timeoutSeconds", 1800),
                   "output": {"captureStdout": True, "captureStderr": True,
                              "maxOutputBytes": command.get("maxOutputBytes", 4194304)}}
            self.publish("COMMAND", command["id"], command.get("name"), doc)

        if m["agents"]:
            print("== Adapter build")
            self.state["adapterBuild"] = self.adapter_build()
            print(f"  ADAPTER          {self.state['adapterBuild']}")

        print("== Workflow (scope project)")
        for workflow in m["workflows"]:
            document = self.resolve_refs(read_json(m, workflow["template"]))
            self.validate_workflow(workflow["id"], workflow.get("name"), document)
            self.publish("WORKFLOW", workflow["id"], workflow.get("name"), document, project=True)

        print("== Gán Engineering Pack cho component")
        components = self.aw.json(["component", "list", self.state["projectId"]])["components"]
        for pack in m["packs"]:
            for component_name in pack.get("assignTo", []):
                matches = [c for c in components if c["name"] == component_name
                           and c["repositoryId"] == pack.get("repository", m["repository"])]
                if not matches:
                    raise SystemExit(f"không thấy component {component_name!r} — repository đã ACTIVE chưa?")
                version = self.version("ENGINEERING_PACK", pack["id"])
                self.aw.run(["pack-assignment", "assign", "--idempotency-key", f"assign-{matches[0]['id']}-{version}",
                             matches[0]["id"]], json.dumps({"packVersionId": version}))
                print(f"  {pack['id']} -> component {component_name}")

        with open(self.state_path, "w", encoding="utf-8") as f:
            json.dump(self.state, f, ensure_ascii=False, indent=2)
            f.write("\n")
        print(f"Xong. Trạng thái đã ghi vào {self.state_path}")

    def resolve_refs(self, node):
        if isinstance(node, dict):
            if set(node) == {"$ref"}:
                ref = node["$ref"]
                if ref == "adapter":
                    return self.state["adapterBuild"]
                kind_word, _, ref_id = ref.partition(":")
                return self.pin(REF_KINDS[kind_word], ref_id)
            return {k: self.resolve_refs(v) for k, v in node.items()}
        if isinstance(node, list):
            return [self.resolve_refs(v) for v in node]
        return node

    def validate_workflow(self, item_id, name, document):
        def_id = self.def_id(item_id)
        scope = ["--project-id", self.state["projectId"]]
        if self.aw.run(["definition", "show", "--kind", "WORKFLOW", *scope, def_id], check=False).returncode != 0:
            self.aw.run(["definition", "create", "--kind", "WORKFLOW", *scope, "--idempotency-key", f"create-{def_id}"],
                        json.dumps({"definitionId": def_id, "name": name or item_id}))
        with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False, encoding="utf-8") as f:
            json.dump(document, f, ensure_ascii=False)
        try:
            proc = self.aw.run(["definition", "validate", "--kind", "WORKFLOW", *scope, "--file", f.name, def_id],
                               check=False)
        finally:
            os.unlink(f.name)
        if proc.returncode != 0 or not json.loads(proc.stdout or "{}").get("valid", False):
            raise SystemExit(f"workflow {def_id} không hợp lệ:\n{proc.stdout}{proc.stderr}")

    def adapter_build(self):
        """Adapter build phải khớp đúng những gì worker đo lại khi admit node AGENT: đường dẫn, SHA-256 của
        file executable, protocol, capability, os và goVersion của chính binary aw."""
        executable = os.environ.get("CLAUDE_EXECUTABLE")
        if not executable:
            raise SystemExit("cần CLAUDE_EXECUTABLE (đúng đường dẫn đã truyền cho --claude-executable)")
        with open(executable, "rb") as f:
            exe_hash = "sha256:" + hashlib.sha256(f.read()).hexdigest()
        identity = self.m["provider"]["configIdentity"]
        for build in self.aw.json(["adapter", "list", "--json"]).get("builds", []):
            if (build["providerKey"] == self.m["provider"]["key"] and build["executablePath"] == executable
                    and build["executableContentHash"] == exe_hash and build["os"] == self.os
                    and build["toolchain"] == self.toolchain and build["configIdentity"] == identity):
                return build["id"]
        manifest_flags = ["--supports-start", "--supports-resume", "--supports-cancel",
                          "--canonical-event-kinds", ",".join(CLAUDE_EVENT_KINDS)]
        candidate = self.aw.json(["adapter", "probe", "--json", "--provider-key", self.m["provider"]["key"],
                                  "--executable-path", executable, "--protocol-version", "claude-stream-json/v1",
                                  "--os", self.os, "--toolchain", self.toolchain, "--config-identity", identity,
                                  *manifest_flags])["result"]
        registered = self.aw.json(["adapter", "register", "--json", "--yes", *manifest_flags],
                                  json.dumps(candidate))
        return registered["result"]["build"]["id"]


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("manifest")
    parser.add_argument("--check", action="store_true", help="chỉ kiểm tra khai báo, không gọi aw")
    args = parser.parse_args()
    try:
        manifest = load_manifest(args.manifest)
    except (OSError, json.JSONDecodeError) as exc:
        sys.exit(f"Không đọc được {args.manifest}: {exc}")
    try:
        problems = lint(manifest)
    except (ManifestError, OSError, json.JSONDecodeError) as exc:
        problems = [str(exc)]
    if problems:
        print("Khai báo chưa hợp lệ:", file=sys.stderr)
        for problem in problems:
            print("  - " + problem, file=sys.stderr)
        sys.exit(1)
    if args.check:
        print(f"OK: {args.manifest} hợp lệ ({len(manifest['agents'])} agent, {len(manifest['commands'])} command, "
              f"{len(manifest['workflows'])} workflow)")
        return
    started = time.time()
    Publisher(manifest, Aw(os.environ.get("AW", "aw")), os.environ.get("AW_STATE", "./aw-state.json")).run_all()
    print(f"({time.time() - started:.1f}s)")


if __name__ == "__main__":
    main()
