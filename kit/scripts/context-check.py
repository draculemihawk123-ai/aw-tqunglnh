#!/usr/bin/env python3
"""Kiểm tra tri thức nào thực sự đến một node AGENT, không tốn tiền: dựng một bản cài aw tạm, publish project, chạy MỘT node
bằng agent giả lập (fake-claude) rồi in ContextSnapshot của attempt đó.

Cách dùng (qua context-check.sh):
    context-check.sh <aw-project.json> [<workflow>:]<node> [--repository ID] [--component TÊN] [--risk LOW|MEDIUM|HIGH]
                     [--keep] [--json]

- `<node>` là key của node AGENT (ví dụ build); thêm `<workflow>:` khi nhiều workflow cùng có node đó.
- `--repository` (mặc định: repository chính của project) và `--component` (thư mục cấp một, mặc định cả repository) là phạm vi
  của WorkItem: nó quyết định componentTags mà selector của resource được so với. `--risk` là mức rủi ro của WorkItem.
- Kết quả: mỗi resource agent nhận (key, độ ưu tiên, byte), resource bị bỏ và lý do, ngân sách; không có gì được gửi tới
  model thật. Resource nào đã khai trong `resources` của agent mà không xuất hiện ở đây là bị selector hoặc ngân sách loại.

Cần: binary `aw` (biến AW hoặc PATH) và `fake-claude` (biến AW_FAKE_CLAUDE hoặc PATH). Trong repo aw, thiếu thì script tự
`go build` vào thư mục tạm. Cần thêm git, jq, python3.
"""
import argparse
import importlib.util
import json
import os
import shutil
import signal
import sqlite3
import subprocess
import sys
import tempfile
import time

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
KIT_ROOT = os.path.dirname(HERE)
REPO_ROOT = os.path.dirname(KIT_ROOT)
_spec = importlib.util.spec_from_file_location("aw_publish", os.path.join(HERE, "aw-publish.py"))
publish = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(publish)

HELPER_ENV = ["AGENTKIT_HELPER_MODE", "AGENTKIT_HELPER_OUTCOME_PICK"]


def fail(message):
    sys.exit(f"context-check: {message}")


def find_tool(env_name, command, go_package, workdir):
    """Đường dẫn tới binary: biến môi trường, PATH, rồi go build trong repo aw."""
    path = os.environ.get(env_name) or shutil.which(command)
    if path:
        return path
    if shutil.which("go") and os.path.isfile(os.path.join(REPO_ROOT, "go.mod")):
        out = os.path.join(workdir, command)
        built = subprocess.run(["go", "build", "-o", out, go_package], cwd=REPO_ROOT, capture_output=True, text=True)
        if built.returncode == 0:
            return out
        fail(f"go build {go_package} hỏng: {built.stderr.strip()[-300:]}")
    fail(f"không thấy `{command}`: đặt {env_name} hoặc thêm vào PATH")


def locate_node(manifest, selector):
    """(workflow entry, template, node) của node AGENT được chọn; báo lỗi rõ khi mơ hồ hoặc không có."""
    wanted_workflow, _, node_key = selector.rpartition(":")
    found = []
    for workflow in manifest["workflows"]:
        if wanted_workflow and workflow["id"] != wanted_workflow:
            continue
        template = publish.workflow_template(manifest, workflow)
        for node in template.get("nodes", []):
            if node.get("key") == node_key:
                found.append((workflow, template, node))
    if not found:
        fail(f"không có node {selector!r} trong workflow nào của project")
    agents = [f for f in found if f[2].get("type") == "AGENT"]
    if not agents:
        fail(f"node {node_key!r} không phải node AGENT (loại {found[0][2].get('type')}): chỉ node AGENT có context")
    if len(agents) > 1:
        names = ", ".join(w["id"] for w, _, _ in agents)
        print(f"context-check: node {node_key!r} có ở nhiều workflow ({names}); dùng {agents[0][0]['id']}. "
              f"Đặt <workflow>:{node_key} để chọn.", file=sys.stderr)
    return agents[0]


def probe_template(node):
    """Workflow chỉ gồm START, đúng node AGENT đó (giữ role, profile, policy) và END."""
    node = json.loads(json.dumps(node))
    node.pop("cyclePolicy", None)
    outcomes = node.get("outcomes") or ["done"]
    return {
        "schemaVersion": "1",
        "nodes": [{"key": "start", "type": "START", "outcomes": ["next"]}, node, {"key": "end", "type": "END"}],
        "edges": [{"key": "start-node", "from": "start", "outcome": "next", "to": node["key"]}]
                 + [{"key": f"node-end-{o}", "from": node["key"], "outcome": o, "to": "end"} for o in outcomes],
    }


def absolutize(raw, base):
    """Đường dẫn file trong khai báo gốc tính từ thư mục của nó; bản tạm nằm chỗ khác nên đổi sang tuyệt đối."""
    for section in ("layers", "skills", "policies"):
        for entry in raw.get(section, []):
            if "file" in entry:
                entry["file"] = os.path.normpath(os.path.join(base, entry["file"]))
    kit = raw.get("kit")
    if kit is not None:
        spec = {"path": kit} if isinstance(kit, str) else dict(kit)
        spec["path"] = os.path.normpath(os.path.join(base, spec["path"]))
        raw["kit"] = spec


def git(path, *args):
    subprocess.run(["git", "-C", path, *args], check=True, capture_output=True)


def make_repository(path, components):
    os.makedirs(path, exist_ok=True)
    git(path, "init", "-q", "-b", "main")
    for name in sorted(components | {"docs"}):
        os.makedirs(os.path.join(path, name), exist_ok=True)
        with open(os.path.join(path, name, ".gitkeep"), "w") as f:
            f.write("")
    with open(os.path.join(path, "README.md"), "w") as f:
        f.write("# context-check\n")
    git(path, "add", "-A")
    git(path, "-c", "user.name=context-check", "-c", "user.email=context-check@example.invalid", "commit", "-q", "-m", "init")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("manifest")
    parser.add_argument("node")
    parser.add_argument("--repository")
    parser.add_argument("--component")
    parser.add_argument("--risk", default="MEDIUM", choices=["LOW", "MEDIUM", "HIGH", "CRITICAL"])
    parser.add_argument("--expect", action="append", default=[], metavar="KEY",
                        help="resource PHẢI được nạp: <key> hoặc <skill|layer>#<key> (lặp được); thiếu thì thoát mã 1")
    parser.add_argument("--absent", action="append", default=[], metavar="KEY",
                        help="resource KHÔNG được nạp: <key> hoặc <skill|layer>#<key> (lặp được); có thì thoát mã 1")
    parser.add_argument("--keep", action="store_true", help="giữ thư mục tạm (in đường dẫn) để xem lại")
    parser.add_argument("--json", action="store_true", help="in kết quả dạng JSON")
    args = parser.parse_args()

    manifest = publish.load_manifest(args.manifest)
    problems = publish.lint(manifest)
    if problems:
        fail("khai báo chưa hợp lệ (chạy aw-publish.py --check): " + problems[0])
    workflow, template, node = locate_node(manifest, args.node)
    agent_ref = (node.get("agent") or {}).get("profileRef", {}).get("$ref", "")
    agent_id = agent_ref.split(":", 1)[-1]
    agent = next((a for a in manifest["agents"] if a["id"] == agent_id), None)
    if agent is None:
        fail(f"node {args.node!r} dùng agent {agent_ref!r} mà project chưa khai báo (chỗ trống của workflow mẫu?)")

    work = tempfile.mkdtemp(prefix="context-check-")
    worker = None
    exit_code = 0
    try:
        aw = find_tool("AW", "aw", "./cmd/aw", work)
        fake = find_tool("AW_FAKE_CLAUDE", "fake-claude", "./cmd/fake-claude", work)

        # repository và component cần có
        main_repo = args.repository or manifest.get("repository") or "repo"
        components = {}
        for pack in manifest["packs"]:
            components.setdefault(pack.get("repository") or manifest.get("repository") or main_repo, set()).update(pack.get("assignTo", []))
        components.setdefault(main_repo, set())
        if args.component:
            components[main_repo].add(args.component)
        repos = {}
        for repo_id, comps in components.items():
            repos[repo_id] = os.path.join(work, "repos", repo_id)
            make_repository(repos[repo_id], comps)

        # khai báo tạm: chỉ phần tri thức + agent + một workflow dò
        with open(args.manifest, encoding="utf-8") as f:
            raw = json.load(f)
        absolutize(raw, manifest["_base"])
        raw["repository"] = main_repo
        for section in ("commands", "gates", "scriptSkills"):
            raw[section] = []
        probe_file = os.path.join(work, "probe-workflow.json")
        with open(probe_file, "w", encoding="utf-8") as f:
            json.dump(probe_template(node), f, ensure_ascii=False)
        raw["workflows"] = [{"id": "wf-context-probe", "name": "Workflow dò context", "template": probe_file}]
        raw.setdefault("provider", {})
        raw["provider"]["envAllowlist"] = sorted(set(raw["provider"].get("envAllowlist", ["HOME", "PATH"])) | set(HELPER_ENV))
        for item in raw.get("agents", []):
            if "envAllowlist" in item:
                item["envAllowlist"] = sorted(set(item["envAllowlist"]) | set(HELPER_ENV))
        tmp_manifest = os.path.join(work, "aw-project.json")
        with open(tmp_manifest, "w", encoding="utf-8") as f:
            json.dump(raw, f, ensure_ascii=False)

        install = os.path.join(work, "install")
        env = dict(os.environ)
        env.update({"AW": aw, "AW_DB": os.path.join(install, "aw.db"), "AW_ARTIFACT_ROOT": os.path.join(install, "artifacts"),
                    "AW_WORKSPACE_ROOT": os.path.join(install, "workspaces"), "AW_CLAUDE_EXECUTABLE": fake,
                    "AW_STATE": os.path.join(work, "aw-state.json"), "AW_KIT": KIT_ROOT,
                    "AGENTKIT_HELPER_MODE": "outcome-from-prompt", "AGENTKIT_HELPER_OUTCOME_PICK": "first",
                    "PATH": os.path.join(HERE) + os.pathsep + os.environ.get("PATH", ""), "WAIT_SECONDS": "120"})
        for key in ("AW_DB", "AW_ARTIFACT_ROOT", "AW_WORKSPACE_ROOT"):
            os.makedirs(os.path.dirname(env[key]) if key == "AW_DB" else env[key], exist_ok=True)
        log = open(os.path.join(work, "worker.log"), "w")
        worker = subprocess.Popen([aw, "worker", "--db", env["AW_DB"], "--artifact-root", env["AW_ARTIFACT_ROOT"],
                                   "--workspace-root", env["AW_WORKSPACE_ROOT"], "--claude-executable", fake,
                                   "--env-allowlist", ",".join(["PATH", "HOME"] + HELPER_ENV),
                                   "--claude-permission-mode", "acceptEdits"],
                                  env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        time.sleep(1.0)

        def run(cmd, **kw):
            done = subprocess.run(cmd, env=env, capture_output=True, text=True, cwd=work, **kw)
            if done.returncode != 0:
                fail(f"{' '.join(cmd[:3])} hỏng:\n{(done.stdout + done.stderr).strip()[-600:]}")
            return done.stdout

        run([os.path.join(HERE, "init-project.sh"), "context-check", main_repo, repos[main_repo]])
        for repo_id, path in repos.items():
            if repo_id != main_repo:
                run([os.path.join(HERE, "add-repository.sh"), repo_id, path])
        run([sys.executable, os.path.join(HERE, "aw-publish.py"), tmp_manifest])
        run([os.path.join(HERE, "create-root.sh"), "context-check"])

        scope = {"repositoryId": main_repo, "access": "WRITE", "reason": "context-check"}
        if args.component:
            scope["pathScopes"] = [args.component]
        item = {"title": f"context-check: {args.node}", "parentJoinPolicy": "ALL_CHILDREN_DONE", "effectiveScope": [scope],
                "contract": {"schemaVersion": 1, "behavior": "Kiểm tra context (không có việc thật).",
                             "verificationSpec": "Không có.", "riskLevel": args.risk,
                             "acceptanceCriteria": [{"description": "Không có", "verificationRef": "AGENT_EXECUTION"}],
                             "workflowVersionId": "WORKFLOW_VERSION_ID"}}
        item_file = os.path.join(work, "work-item.json")
        with open(item_file, "w", encoding="utf-8") as f:
            json.dump(item, f, ensure_ascii=False)
        out = run([os.path.join(HERE, "run-task.sh"), item_file, "wf-context-probe"])
        node_key = args.node.rpartition(":")[2]
        # Run có thể FAILED vì completion policy (workflow dò không có bằng chứng kiểm tra); điều cần là attempt của node đã chạy.
        if f"{node_key} (vòng 0): SUCCEEDED" not in out:
            fail("node dò không chạy xong:\n" + out[-800:])
        run_id = next((line.split()[1] for line in out.splitlines() if line.startswith("Run: ")), "")

        report = snapshot_report(env, manifest, agent)
        report["node"] = args.node
        report["agent"] = agent_id
        report["scope"] = {"repository": main_repo, "component": args.component, "risk": args.risk}
        violations = check_expectations(report, args.expect, args.absent)
        report["violations"] = violations
        if args.json:
            print(json.dumps(report, ensure_ascii=False, indent=1))
        else:
            print_report(report)
        if violations:
            exit_code = 1
        if args.keep:
            print(f"(giữ thư mục tạm: {work})", file=sys.stderr)
    finally:
        if worker is not None:
            try:
                os.killpg(worker.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
        if not args.keep:
            shutil.rmtree(work, ignore_errors=True)
    sys.exit(exit_code)


def declared_resources(manifest, agent):
    """(<id Layer/Skill>, key) mà `resources` của agent khai báo."""
    out = []
    for selector in agent.get("resources", []):
        item_id, key = publish.split_selector(selector)
        for resource in publish.resources_of(manifest, item_id):
            if key is None or resource["key"] == key:
                out.append((item_id, resource["key"]))
    return out


def snapshot_report(env, manifest, agent):
    """Kết quả chọn context của attempt agent duy nhất trong bản cài tạm: đọc quyết định CONTEXT_RESOLUTION_V1 (chi tiết cài
    đặt của Alpha, cùng cách `agent-log.py` đọc agent_events), chỉ đọc trên database tạm của chính script này."""
    state = json.load(open(env["AW_STATE"], encoding="utf-8"))
    owner_ids = {}
    for kind in ("LAYER", "SKILL"):
        for item_id, info in state["definitions"].get(kind, {}).items():
            owner_ids[info["versionId"]] = item_id
    db = sqlite3.connect(f"file:{env['AW_DB']}?mode=ro", uri=True)
    try:
        row = db.execute("select input_json, result_json from decision_artifacts where kind = 'CONTEXT_RESOLUTION_V1' "
                         "order by created_at desc limit 1").fetchone()
    finally:
        db.close()
    if row is None:
        fail("không thấy quyết định CONTEXT_RESOLUTION_V1: attempt chưa tạo context")
    resolution_input, result = json.loads(row[0]), json.loads(row[1])

    def entry(item):
        identity = item["Identity"]
        return {"owner": owner_ids.get(identity["OwnerVersionID"], identity["OwnerVersionID"]), "key": identity["ResourceKey"],
                "priority": item["Priority"], "bytes": item.get("Cost", 0), "reason": item.get("Reason", "")}
    selected = [entry(i) for i in result.get("Selected") or []]
    excluded = [entry(i) for i in result.get("Excluded") or []]
    loaded = {(e["owner"], e["key"]) for e in selected}
    return {"input": resolution_input, "selected": selected, "excluded": excluded,
            "declaredNotLoaded": [{"owner": o, "key": k} for o, k in declared_resources(manifest, agent) if (o, k) not in loaded],
            "spentBytes": result.get("SpentBytes", 0), "maxBytes": result.get("MaxBytes", 0)}


def matches(entry, wanted):
    owner, _, key = wanted.rpartition("#")
    return entry["key"] == key and (not owner or entry["owner"] == owner)


def check_expectations(report, expect, absent):
    problems = []
    for wanted in expect:
        if not any(matches(e, wanted) for e in report["selected"]):
            problems.append(f"thiếu: {wanted} không được nạp")
    for wanted in absent:
        if any(matches(e, wanted) for e in report["selected"]):
            problems.append(f"thừa: {wanted} được nạp")
    return problems


def print_report(report):
    scope, resolution = report["scope"], report["input"]
    print(f"Node {report['node']} (agent {report['agent']}): repository {scope['repository']}, "
          f"component {scope['component'] or 'cả repository'}, rủi ro {scope['risk']}")
    print(f"Context: componentTags={resolution['componentTags']} pathTags={resolution['pathTags']} "
          f"blockKind={resolution['blockKind']} taskKind={resolution['taskKind']} riskClass={resolution['riskClass']}")
    print(f"Nạp {len(report['selected'])} resource ({report['spentBytes']} / {report['maxBytes']} byte):")
    for e in report["selected"]:
        print(f"  {e['priority']:<18} {e['owner']}#{e['key']}  {e['bytes']} B")
    if report["excluded"]:
        print(f"Không nạp {len(report['excluded'])} resource (có trong CONTEXT policy của agent):")
        for e in report["excluded"]:
            print(f"  {e['reason']:<18} {e['owner']}#{e['key']}  ({e['priority']})")
    if report["declaredNotLoaded"]:
        print("Khai trong `resources` của agent nhưng không được nạp: "
              + ", ".join(f"{d['owner']}#{d['key']}" for d in report["declaredNotLoaded"]))
    for problem in report["violations"]:
        print(f"LỖI: {problem}", file=sys.stderr)


if __name__ == "__main__":
    main()
