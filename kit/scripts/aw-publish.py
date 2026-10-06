#!/usr/bin/env python3
"""Publish mọi definition mà một project khai báo trong aw-project.json lên một bản cài aw.

Cách dùng:
    aw-publish.py aw-project.json --check            # chỉ kiểm tra khai báo + template (không gọi aw)
    aw-publish.py aw-project.json --slots            # chỗ trống mà workflow của kit đòi project điền
    aw-publish.py aw-project.json                    # publish, ghi trạng thái vào aw-state.json
    aw-publish.py <kit>/kit.json --check             # kiểm tra chính kho (kit)

Kho dùng chung (kit): file khai báo project có khóa "kit": "<đường dẫn tới kit.json>" (hoặc {"path", "version"}).
Id mà kit khai báo (Layer, Skill, script skill, Policy) tham chiếu được từ project mà không phải khai lại; chúng được
publish MỘT lần cho cả bản cài, dưới prefix của kit (kit-), nên mọi project dùng chung đúng một version. Command, Gate
và Workflow của kit là bản mẫu: project lấy bằng {"id": "...", "from": "kit"} và chúng được publish dưới prefix của
project (Command ghim repository của project, Workflow có scope project). Script có dòng
`. "${AW_KIT:?…}/commands/lib.sh" # @aw-include` được nhúng nguyên nội dung thư viện khi publish.

Biến môi trường: AW (mặc định "aw"), AW_DB, AW_ARTIFACT_ROOT, AW_WORKSPACE_ROOT (giống aw serve/worker),
AW_STATE (mặc định ./aw-state.json, phải có projectId — tạo bằng init-project.sh), AW_CLAUDE_EXECUTABLE
(đúng đường dẫn đã truyền cho --claude-executable của aw serve/worker; bắt buộc nếu khai báo agent — cũng là
biến mà mọi lệnh `aw` đọc thay cho tùy chọn --claude-executable).

Thứ tự publish (đúng thứ tự phụ thuộc): LAYER, SKILL, script skill, ENGINEERING_PACK, POLICY, mỗi agent một
CONTEXT policy + AGENT_PROFILE, COMMAND, GATE, adapter build, WORKFLOW (từ template, giải quyết {"$ref": ...}),
gán Engineering Pack cho component. Idempotency key suy ra từ nội dung: chạy lại khi không đổi gì trả về
đúng các version cũ; sửa một file chỉ tạo version mới cho nó và những gì phụ thuộc vào nó. Cảnh báo mà aw trả
về khi publish (resource quá hạn kiểm tra, quá nhiều HARD_CONSTRAINT) được in ngay dưới dòng của definition đó.
"""
import argparse
import hashlib
import importlib.util
import json
import os
import re
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
# Windows: chương trình nào cũng cần thêm các biến chỉ vị trí hệ thống và thư mục người dùng. aw-publish.py tự cộng
# danh sách này vào envAllowlist của Command và của agent khi `aw version --json` báo os là windows.
WINDOWS_ENV = ["USERPROFILE", "APPDATA", "LOCALAPPDATA", "SystemRoot", "SystemDrive", "ComSpec", "PATHEXT",
               "TEMP", "TMP", "ProgramData", "ProgramFiles"]
# Worker trên Windows không spawn được file .sh. Mỗi script .sh được bọc thành một file .cmd tự chứa: cmd.exe chạy
# phần đầu (tìm sh.exe của Git for Windows rồi gọi nó trên chính file này); sh thì bỏ qua phần đầu nhờ here-document
# và chạy nguyên văn script gốc ở phía dưới. Nội dung script không đổi giữa các hệ điều hành.
WINDOWS_LAUNCHER = """: << 'AW_CMD_HEADER'
@echo off
setlocal
set "AW_SH="
for /f "delims=" %%i in ('git --exec-path') do set "AW_SH=%%i\\..\\..\\..\\bin\\sh.exe"
if not exist "%AW_SH%" (
  echo aw-publish launcher: khong tim thay sh.exe cua Git for Windows qua "git --exec-path" 1>&2
  exit /b 127
)
"%AW_SH%" "%~f0" %*
exit /b %errorlevel%
AW_CMD_HEADER
"""
CLAUDE_EVENT_KINDS = ["EXECUTION_STARTED", "STATUS_CHANGED", "ASSISTANT_MESSAGE", "TOOL_CALL_STARTED",
                      "TOOL_CALL_FINISHED", "USAGE_REPORTED", "CHECKPOINT_PROPOSED", "DIAGNOSTIC", "EXECUTION_FINISHED"]
REF_KINDS = {"agent": "AGENT_PROFILE", "command": "COMMAND", "gate": "GATE", "policy": "POLICY", "workflow": "WORKFLOW",
             "layer": "LAYER", "skill": "SKILL", "pack": "ENGINEERING_PACK"}


class ManifestError(Exception):
    pass


# ---------------------------------------------------------------- khai báo project

SECTIONS = (("layers", "LAYER"), ("skills", "SKILL"), ("scriptSkills", "SKILL"), ("packs", "ENGINEERING_PACK"),
            ("policies", "POLICY"), ("agents", "AGENT_PROFILE"), ("commands", "COMMAND"), ("gates", "GATE"),
            ("workflows", "WORKFLOW"))
SHARED = ("layers", "skills", "scriptSkills", "policies")  # phần của kit được publish dùng chung
INSTANTIATED = ("commands", "gates", "workflows")          # phần của kit là bản mẫu, publish theo project
INCLUDE_RE = re.compile(r'^\s*\.\s+"\$\{AW_KIT:\?[^}]*\}/(?P<rel>[^"]+)"\s*#\s*@aw-include\s*$')


def load_manifest(path, as_kit=False):
    base = os.path.dirname(os.path.abspath(path))
    with open(path, encoding="utf-8") as f:
        manifest = json.load(f)
    manifest["_base"] = base
    for key in ("layers", "skills", "scriptSkills", "packs", "policies", "agents", "commands", "gates", "workflows"):
        manifest.setdefault(key, [])
    manifest.setdefault("prefix", "")
    manifest.setdefault("contextBudgetBytes", 65536)
    manifest.setdefault("provider", {})
    manifest["provider"].setdefault("key", "claude")
    manifest["provider"].setdefault("model", "sonnet")
    manifest["provider"].setdefault("configIdentity", manifest["prefix"] + "claude")
    manifest["provider"].setdefault("envAllowlist", [])
    manifest.setdefault("messageBudget", None)
    manifest.setdefault("role", "kit" if as_kit else "project")
    manifest["_kit"] = None
    spec = manifest.pop("kit", None)
    if spec is not None and not as_kit:
        spec = {"path": spec} if isinstance(spec, str) else dict(spec)
        kit = load_manifest(os.path.join(base, spec["path"]), as_kit=True)
        if kit["role"] != "kit":
            raise ManifestError(f"{spec['path']} không phải kho (kit): thiếu \"role\": \"kit\"")
        wanted = str(spec.get("version", "")).split(".")[0]
        if wanted and wanted != str(kit.get("version", "")).split(".")[0]:
            raise ManifestError(f"project đòi kit phiên bản {spec['version']} nhưng kho ở {kit.get('version')}")
        manifest["_kit"] = kit
        expand_from_kit(manifest, kit)
    return manifest


def expand_from_kit(manifest, kit):
    """{"id": "x", "from": "kit"} trong commands/gates/workflows: lấy bản mẫu cùng id của kit; field khác trong entry
    của project ghi đè. Đường dẫn file của bản mẫu tính từ thư mục của kit."""
    for section in INSTANTIATED:
        expanded = []
        for entry in manifest[section]:
            if entry.get("from") == "kit":
                template = next((e for e in kit[section] if e["id"] == entry["id"]), None)
                if template is None:
                    raise ManifestError(f"{section}/{entry['id']}: kit không có bản mẫu này")
                entry = {**template, **{k: v for k, v in entry.items() if k != "from"}, "_owner": kit}
            expanded.append(entry)
        manifest[section] = expanded


def owners(manifest):
    """Project rồi tới kit: thứ tự phân giải id."""
    return [manifest] + ([manifest["_kit"]] if manifest.get("_kit") else [])


def find_entry(manifest, sections, item_id):
    """(owner, entry) của id trong project hoặc kit; (None, None) nếu không có."""
    for owner in owners(manifest):
        for section in sections:
            for entry in owner[section]:
                if entry["id"] == item_id:
                    return owner, entry
    return None, None


def owner_of(manifest, entry):
    return entry.get("_owner") or manifest


def kit_base(manifest):
    kit = manifest if manifest.get("role") == "kit" else manifest.get("_kit")
    return kit["_base"] if kit else None


def read_json(manifest, rel):
    with open(os.path.join(manifest["_base"], rel), encoding="utf-8") as f:
        return json.load(f)


def resources_of(manifest, item_id):
    """Danh sách resource (dict) của một Layer/Skill/script skill đã khai báo (ở project hoặc kit)."""
    owner, entry = find_entry(manifest, ("layers", "skills"), item_id)
    if entry:
        return read_json(owner, entry["file"])["resources"]
    owner, entry = find_entry(manifest, ("scriptSkills",), item_id)
    if entry:
        return script_skill_doc(owner, entry)["resources"]
    raise ManifestError(f"không có Layer/Skill nào tên {item_id!r}")


def script_key(name, windows):
    """Key của resource script: tên file; trên Windows file .sh được publish dưới dạng launcher .cmd."""
    name = os.path.basename(name)
    return name[:-3] + ".cmd" if windows and name.endswith(".sh") else name


def inline_includes(manifest, rel, body):
    """Thay mỗi dòng `. "${AW_KIT:?…}/<đường dẫn>" # @aw-include` bằng nội dung file đó của kit (bỏ dòng shebang)."""
    lines = []
    for line in body.splitlines():
        match = INCLUDE_RE.match(line)
        if not match:
            lines.append(line)
            continue
        base = kit_base(manifest)
        if base is None:
            raise ManifestError(f"{rel}: dùng @aw-include nhưng project không khai báo kit")
        target = os.path.join(base, match.group("rel"))
        if not os.path.isfile(target):
            raise ManifestError(f"{rel}: @aw-include trỏ tới file không có trong kit: {match.group('rel')}")
        with open(target, encoding="utf-8") as f:
            included = [l for l in f.read().splitlines() if not l.startswith("#!")]
        lines += [f"# --- @aw-include {match.group('rel')} (aw-publish nhúng nguyên văn) ---", *included,
                  "# --- hết @aw-include ---"]
    return "\n".join(lines) + ("\n" if body.endswith("\n") else "")


def script_skill_doc(manifest, entry, windows=False):
    resources = []
    for rel in entry["files"]:
        with open(os.path.join(manifest["_base"], rel), encoding="utf-8") as f:
            body = inline_includes(manifest, rel, f.read())
        if windows and rel.endswith(".sh"):
            body = WINDOWS_LAUNCHER + body
        resources.append({"key": script_key(rel, windows), "instruction": body, "priority": "REQUIRED_PROCEDURE",
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
    is_kit = manifest["role"] == "kit"
    kit = manifest["_kit"]
    if is_kit:
        for section in ("agents", "packs"):
            if manifest[section]:
                problems.append(f"kit không được khai báo {section}: {section} phụ thuộc vào stack của từng project")
    if kit and kit["prefix"] == manifest["prefix"]:
        problems.append(f"prefix của project ({manifest['prefix']!r}) trùng prefix của kit")
    seen = {}
    for owner in owners(manifest):
        for section, _ in SECTIONS:
            if owner is kit and section in INSTANTIATED:
                continue  # bản mẫu của kit chỉ có nghĩa khi project lấy về bằng "from": "kit"
            for entry in owner[section]:
                item_id = entry.get("id", "")
                if not item_id:
                    problems.append(f"{section}: thiếu id")
                    continue
                where = f"{section} của {'kit' if owner is kit or is_kit else 'project'}"
                if item_id in seen:
                    problems.append(f"id {item_id!r} bị trùng ({seen[item_id]} và {where}) — definitionId là duy nhất trên toàn bản cài")
                seen[item_id] = where
    for owner in owners(manifest):
        for section, _ in SECTIONS:
            for entry in owner[section]:
                entry_owner = owner_of(owner, entry)
                for field in ("file", "template"):
                    if field in entry and not os.path.isfile(os.path.join(entry_owner["_base"], entry[field])):
                        problems.append(f"{section}/{entry.get('id')}: không thấy file {entry[field]}")
                for rel in entry.get("files", []):
                    if not os.path.isfile(os.path.join(entry_owner["_base"], rel)):
                        problems.append(f"{section}/{entry.get('id')}: không thấy file {rel}")
    if problems:
        return problems
    for owner in owners(manifest):
        for entry in owner["scriptSkills"]:
            try:
                script_skill_doc(owner, entry)  # nhúng thử: bắt @aw-include trỏ file không có
            except ManifestError as exc:
                problems.append(f"scriptSkills/{entry['id']}: {exc}")
    if problems:
        return problems
    passive = {e["id"] for o in owners(manifest) for e in o["layers"] + o["skills"]}
    packs = {p["id"] for p in manifest["packs"]}
    keys_by_item = {item_id: [r["key"] for r in resources_of(manifest, item_id)] for item_id in passive}
    if is_kit:
        problems += lint_provenance(manifest)
    for pack in manifest["packs"]:
        for dep in pack.get("include", []):
            if dep not in passive and dep not in packs:
                problems.append(f"packs/{pack['id']}: include {dep!r} không phải Layer/Skill/Pack đã khai báo")
    for agent in manifest["agents"]:
        keys = []
        for selector in agent.get("resources", []):
            item_id, key = split_selector(selector)
            if item_id not in passive:
                problems.append(f"agents/{agent['id']}: {selector!r} không trỏ tới Layer/Skill đã khai báo (ở project hoặc kit)")
            elif key and key not in keys_by_item[item_id]:
                problems.append(f"agents/{agent['id']}: {item_id} không có resource {key!r}")
            else:
                keys += [key] if key else keys_by_item[item_id]
        duplicated = sorted({k for k in keys if keys.count(k) > 1})
        if duplicated:
            problems.append(f"agents/{agent['id']}: resource key trùng {duplicated} — resolver sẽ báo conflict")
    script_keys = {}
    for owner in owners(manifest):
        for s_ in owner["scriptSkills"]:
            script_keys[s_["id"]] = [os.path.basename(f) for f in s_["files"]]
    policy_docs = {}
    for owner in owners(manifest):
        for p_ in owner["policies"]:
            policy_docs[p_["id"]] = read_json(owner, p_["file"])
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
    command_ids = {c["id"] for c in manifest["commands"]}
    for gate in manifest["gates"]:
        if gate.get("command") not in command_ids:
            problems.append(f"gates/{gate['id']}: command {gate.get('command')!r} chưa khai báo")
        keys = [c.get("evidenceKey", "") for c in gate.get("criteria", [])]
        if not keys or any(not k for k in keys) or len(set(keys)) != len(keys):
            problems.append(f"gates/{gate['id']}: cần ít nhất một tiêu chí, mỗi tiêu chí một evidenceKey khác nhau")
    for agent in manifest["agents"]:
        for name in agent.get("envAllowlist", manifest["provider"]["envAllowlist"]):
            if not name or "=" in name or any(ch.isspace() for ch in name):
                problems.append(f"agents/{agent['id']}: envAllowlist chỉ chứa TÊN biến, không có giá trị: {name!r}")
    # Layer/Skill/Policy dùng chung được tham chiếu từ cả project lẫn kit; Command/Gate/Agent/Pack/Workflow chỉ của
    # project (chúng ghim repository hoặc phụ thuộc stack), nên bản mẫu của kit phải được lấy về bằng "from": "kit".
    declared = {"AGENT_PROFILE": {a["id"] for a in manifest["agents"]}, "COMMAND": command_ids,
                "GATE": {g["id"] for g in manifest["gates"]},
                "POLICY": set(policy_docs), "WORKFLOW": {w["id"] for w in manifest["workflows"]},
                "LAYER": {l["id"] for o in owners(manifest) for l in o["layers"]},
                "SKILL": {s_["id"] for o in owners(manifest) for s_ in o["skills"] + o["scriptSkills"]},
                "ENGINEERING_PACK": packs}
    for workflow in manifest["workflows"]:
        template = read_json(owner_of(manifest, workflow), workflow["template"])
        for ref in collect_refs(template):
            if ref == "adapter":
                if not manifest["agents"] and not is_kit:
                    problems.append(f"workflows/{workflow['id']}: dùng $ref adapter nhưng không khai báo agent nào")
                continue
            kind_word, _, ref_id = ref.partition(":")
            kind = REF_KINDS.get(kind_word)
            if is_kit and kind in SLOT_KINDS:
                continue  # chỗ trống: project điền (xem --slots)
            if not kind or ref_id not in declared.get(kind, set()):
                problems.append(f"workflows/{workflow['id']}: $ref {ref!r} không giải quyết được" + kit_hint(manifest, kind, ref_id))
        problems += lint_graph(workflow["id"], template)
    return problems


SLOT_KINDS = {"AGENT_PROFILE", "COMMAND", "GATE", "ENGINEERING_PACK", "WORKFLOW"}


def kit_hint(manifest, kind, ref_id):
    """Gợi ý khi một $ref trỏ tới Command/Gate/Workflow mà kit có bản mẫu nhưng project chưa lấy về."""
    kit = manifest.get("_kit")
    section = {"COMMAND": "commands", "GATE": "gates", "WORKFLOW": "workflows"}.get(kind)
    if kit and section and any(e["id"] == ref_id for e in kit[section]):
        return f' — kit có bản mẫu: thêm {{"id": "{ref_id}", "from": "kit"}} vào {section}'
    return ""


def lint_provenance(manifest):
    """Nội dung nhận vào kho phải truy được nguồn: source không rỗng; nguồn là URL thì phải ghi license."""
    problems = []
    for section in ("layers", "skills"):
        for entry in manifest[section]:
            for resource in read_json(manifest, entry["file"])["resources"]:
                prov = resource.get("provenance") or {}
                where = f"{section}/{entry['id']}#{resource.get('key')}"
                if not prov.get("source"):
                    problems.append(f"{where}: thiếu provenance.source")
                elif str(prov["source"]).startswith(("http://", "https://")) and not prov.get("license"):
                    problems.append(f"{where}: nguồn bên ngoài ({prov['source']}) phải có provenance.license")
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
        if node.get("type") in ("COMMAND", "MACHINE_GATE"):
            # Mặc định một outcome. Với failureOutcome (check fail quay lại maker) phải có đúng hai outcome mà
            # chính bước kiểm tra chọn được: failureOutcome và một outcome thành công. escalationOutcome của
            # cyclePolicy trên chính node đó không tính, vì engine tự gán nó khi hết số vòng.
            failure = config.get("failureOutcome")
            escalation = (node.get("cyclePolicy") or {}).get("escalationOutcome")
            selectable = [o for o in node.get("outcomes") or [] if o != escalation]
            if failure:
                if failure not in selectable or len(selectable) != 2:
                    problems.append(f"workflows/{workflow_id}: {node['type']} {node['key']!r} khai failureOutcome thì phải có "
                                    f"đúng hai outcome: {failure!r} và một outcome thành công")
            elif len(selectable) > 1:
                problems.append(f"workflows/{workflow_id}: {node['type']} {node['key']!r} có nhiều outcome nhưng "
                                f"không khai failureOutcome")
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
        kit = manifest["_kit"]
        if kit:
            self.state["kit"] = {"name": kit.get("name", "kit"), "version": kit.get("version"), "prefix": kit["prefix"]}
        version = aw.json(["version", "--json"])
        self.os, self.toolchain = version["os"], version["goVersion"]
        self.windows = self.os == "windows"

    def env(self, names):
        """Danh sách tên biến môi trường cho một Command hoặc agent, cộng phần riêng của Windows."""
        return sorted(set(names) | set(WINDOWS_ENV if self.windows and names else []))

    def def_id(self, item_id, prefix=None):
        return (self.m["prefix"] if prefix is None else prefix) + item_id

    def record(self, kind, item_id, version_id, prefix=None):
        self.state["definitions"].setdefault(kind, {})[item_id] = {"definitionId": self.def_id(item_id, prefix),
                                                                   "versionId": version_id}

    def version(self, kind, item_id):
        return self.state["definitions"][kind][item_id]["versionId"]

    def pin(self, kind, item_id):
        """Ghim đúng definitionId đã publish (prefix của project hoặc của kit) và version của nó."""
        entry = self.state["definitions"][kind][item_id]
        return {"kind": kind, "definitionId": entry["definitionId"], "versionId": entry["versionId"]}

    def publish(self, kind, item_id, name, document, project=False, prefix=None):
        """Tạo vỏ definition nếu chưa có (tạo trùng id bị từ chối với CONFLICT), rồi publish một version."""
        def_id = self.def_id(item_id, prefix)
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
        self.record(kind, item_id, result["result"]["id"], prefix)
        print(f"  {kind:<16} {def_id:<36} {result['result']['id']}")
        for warning in result["result"].get("warnings") or []:
            print(f"    CẢNH BÁO: {warning}")

    def resource_refs(self, selector):
        item_id, key = split_selector(selector)
        kind = "LAYER" if find_entry(self.m, ("layers",), item_id)[1] else "SKILL"
        owner = self.version(kind, item_id)
        refs = []
        for resource in resources_of(self.m, item_id):
            if key is None or resource["key"] == key:
                refs.append({"ownerVersionId": owner, "resourceKey": resource["key"],
                             "contentHash": _hashes.content_hash(resource)})
        return refs

    def publish_passive(self, owner, prefix):
        """Layer, Skill và script skill của một khai báo (project hoặc kit), dưới prefix tương ứng."""
        for entry in owner["layers"]:
            self.publish("LAYER", entry["id"], entry.get("name"), read_json(owner, entry["file"]), prefix=prefix)
        for entry in owner["skills"]:
            self.publish("SKILL", entry["id"], entry.get("name"), read_json(owner, entry["file"]), prefix=prefix)
        for entry in owner["scriptSkills"]:
            self.publish("SKILL", entry["id"], entry.get("name"), script_skill_doc(owner, entry, self.windows),
                         prefix=prefix)

    def publish_policies(self, owner, prefix):
        for entry in owner["policies"]:
            self.publish("POLICY", entry["id"], entry.get("name"), read_json(owner, entry["file"]), prefix=prefix)

    def run_all(self):
        m = self.m
        kit = m["_kit"]
        if kit:
            print(f"== Kit {kit.get('name', 'kit')} {kit.get('version', '')} (dùng chung toàn bản cài, prefix {kit['prefix']})")
            self.publish_passive(kit, kit["prefix"])
            self.publish_policies(kit, kit["prefix"])
        print("== Layer / Skill")
        self.publish_passive(m, m["prefix"])

        print("== Engineering Pack")
        for pack in m["packs"]:
            deps = []
            for dep in pack.get("include", []):
                kind = ("LAYER" if find_entry(m, ("layers",), dep)[1] else
                        "ENGINEERING_PACK" if any(p["id"] == dep for p in m["packs"]) else "SKILL")
                deps.append(self.pin(kind, dep))
            self.publish("ENGINEERING_PACK", pack["id"], pack.get("name"), {"dependencies": deps})

        print("== Policy")
        self.publish_policies(m, m["prefix"])

        print("== Agent (mỗi agent một CONTEXT policy = bảng định tuyến của bước đó)")
        for agent in m["agents"]:
            refs = [r for selector in agent.get("resources", []) for r in self.resource_refs(selector)]
            context = {"category": "CONTEXT",
                       "context": {"selector": [self.def_id(agent["id"])],
                                   "budget": {"maxTokens": agent.get("contextBudgetBytes", m["contextBudgetBytes"])},
                                   "resourceRefs": refs}}
            messages = agent.get("messageBudget", m["messageBudget"])
            if messages:
                context["context"]["messages"] = messages
            ctx_id = "ctx-" + agent["id"]
            self.publish("POLICY", ctx_id, f"Context: {agent.get('name', agent['id'])}", context)
            profile = {"providerKey": m["provider"]["key"], "model": agent.get("model", m["provider"]["model"]),
                       "toolRefs": agent.get("toolRefs", ["Read", "Edit", "Write", "Bash"]),
                       "contextPolicyRef": self.pin("POLICY", ctx_id),
                       "compatibility": {"os": [self.os]}, "budget": {"maxTokens": agent.get("maxTokens", 200000)}}
            env_allowlist = agent.get("envAllowlist", m["provider"]["envAllowlist"])
            if env_allowlist:
                profile["envAllowlist"] = self.env(env_allowlist)
            self.publish("AGENT_PROFILE", agent["id"], agent.get("name"), profile)

        print("== Command")
        for command in m["commands"]:
            skill_id, key = split_selector(command["script"])
            key = script_key(key, self.windows)
            skill_owner, skill = find_entry(m, ("scriptSkills",), skill_id)
            resource = next(r for r in script_skill_doc(skill_owner, skill, self.windows)["resources"] if r["key"] == key)
            repository = command.get("repository", m["repository"])
            doc = {"executable": {"ownerVersionId": self.version("SKILL", skill_id), "resourceKey": key,
                                  "contentHash": _hashes.content_hash(resource)},
                   "argv": [{"kind": "LITERAL", "value": "run"}],
                   "cwdRepositoryTarget": repository,
                   "compatibility": {"os": [self.os]},
                   "envAllowlist": self.env(command.get("envAllowlist", DEFAULT_ENV_ALLOWLIST)),
                   "networkAccess": command.get("network", "NONE"),
                   "policyRefs": [self.pin("POLICY", p) for p in command.get("policies", [])],
                   "timeoutSeconds": command.get("timeoutSeconds", 1800),
                   "output": {"captureStdout": True, "captureStderr": True,
                              "maxOutputBytes": command.get("maxOutputBytes", 4194304)}}
            if command.get("repositoryArg"):
                # Lệnh của MACHINE_GATE chạy trong một thư mục scratch: nó nhận đường dẫn worktree của repository
                # (chỉ đọc) làm tham số thứ hai, qua một placeholder mà engine thay lúc chạy.
                doc["argv"].append({"kind": "PLACEHOLDER", "value": repository})
                doc["placeholderAllowlist"] = [repository]
            self.publish("COMMAND", command["id"], command.get("name"), doc)

        if m["gates"]:
            print("== Gate")
        for gate in m["gates"]:
            self.publish("GATE", gate["id"], gate.get("name"),
                         {"commandRef": self.pin("COMMAND", gate["command"]), "criteria": gate["criteria"]})

        if m["agents"]:
            print("== Adapter build")
            self.state["adapterBuild"] = self.adapter_build()
            print(f"  ADAPTER          {self.state['adapterBuild']}")

        print("== Workflow (scope project)")
        for workflow in m["workflows"]:
            document = self.resolve_refs(read_json(owner_of(m, workflow), workflow["template"]))
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
        executable = os.environ.get("AW_CLAUDE_EXECUTABLE")
        if not executable:
            raise SystemExit("cần AW_CLAUDE_EXECUTABLE (đúng đường dẫn đã truyền cho --claude-executable)")
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


def print_slots(manifest):
    """Với mỗi workflow của kit: những thứ project phải có (agent, command, gate…) để dùng nó, và đã có chưa."""
    kit = manifest if manifest["role"] == "kit" else manifest["_kit"]
    if kit is None:
        sys.exit("khai báo này không dùng kit (thiếu khóa \"kit\")")
    project = None if manifest["role"] == "kit" else manifest
    have = {} if project is None else {
        "agent": {a["id"] for a in project["agents"]}, "command": {c["id"] for c in project["commands"]},
        "gate": {g["id"] for g in project["gates"]}, "pack": {p["id"] for p in project["packs"]},
        "workflow": {w["id"] for w in project["workflows"]}}
    for workflow in kit["workflows"]:
        print(f"{workflow['id']}  — {workflow.get('name', '')}")
        refs = sorted({r for r in collect_refs(read_json(kit, workflow["template"])) if r != "adapter"
                       and REF_KINDS.get(r.partition(":")[0]) in SLOT_KINDS})
        for ref in refs:
            kind_word, _, ref_id = ref.partition(":")
            from_kit = {"command": "commands", "gate": "gates"}.get(kind_word)
            offered = from_kit and any(e["id"] == ref_id for e in kit[from_kit])
            note = "kit có bản mẫu, lấy bằng \"from\": \"kit\"" if offered else "project tự định nghĩa"
            status = "" if project is None else ("[có]   " if ref_id in have.get(kind_word, set()) else "[THIẾU]")
            print(f"  {status} {ref:<34} {note}")
    if project is None:
        print("(chạy với aw-project.json để thấy project đã đủ chưa)")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("manifest")
    parser.add_argument("--check", action="store_true", help="chỉ kiểm tra khai báo, không gọi aw")
    parser.add_argument("--slots", action="store_true", help="in chỗ trống mà workflow của kit đòi project điền")
    args = parser.parse_args()
    try:
        manifest = load_manifest(args.manifest)
    except (OSError, json.JSONDecodeError, ManifestError) as exc:
        sys.exit(f"Không đọc được {args.manifest}: {exc}")
    try:
        problems = lint(manifest)
    except (ManifestError, OSError, json.JSONDecodeError) as exc:
        problems = [str(exc)]
    if args.slots:
        print_slots(manifest)
        if problems:
            print(f"(khai báo còn {len(problems)} lỗi, chạy --check để xem)", file=sys.stderr)
        return
    if problems:
        print("Khai báo chưa hợp lệ:", file=sys.stderr)
        for problem in problems:
            print("  - " + problem, file=sys.stderr)
        sys.exit(1)
    if args.check:
        if manifest["role"] == "kit":
            shared = sum(len(manifest[k]) for k in SHARED)
            print(f"OK: kit {manifest.get('name', '')} {manifest.get('version', '')} hợp lệ ({shared} mục dùng chung, "
                  f"{len(manifest['commands'])} command / {len(manifest['gates'])} gate / "
                  f"{len(manifest['workflows'])} workflow mẫu)")
        else:
            kit = manifest["_kit"]
            print(f"OK: {args.manifest} hợp lệ ({len(manifest['agents'])} agent, {len(manifest['commands'])} command, "
                  f"{len(manifest['workflows'])} workflow" + (f"; kit {kit.get('version')}" if kit else "") + ")")
        return
    if manifest["role"] == "kit":
        sys.exit("kit không publish riêng: nó được publish cùng project, chạy aw-publish.py aw-project.json")
    started = time.time()
    Publisher(manifest, Aw(os.environ.get("AW", "aw")), os.environ.get("AW_STATE", "./aw-state.json")).run_all()
    print(f"({time.time() - started:.1f}s)")


if __name__ == "__main__":
    main()
