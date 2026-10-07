#!/usr/bin/env python3
"""Đi một workflow theo kịch bản, không tốn tiền: dựng bản cài aw tạm, publish project, chạy MỘT WorkItem qua workflow với agent
giả lập (chọn outcome theo kịch bản) và lệnh giả lập (đạt/hỏng theo kịch bản), tự duyệt các cổng người theo kịch bản, rồi so
thứ tự node đã chạy với kết quả mong đợi. Dùng để chứng minh mọi cạnh của workflow mẫu chạy đúng (V10-14).

Cách dùng (qua walk-workflow.sh):
    walk-workflow.sh <kịch-bản.json> [--keep] [--json]

Kịch bản (xem kit/tests/walk/scenarios/*.json):
    manifest    aw-project.json của project thử (đường dẫn tính từ thư mục kịch bản)
    workflow    id workflow trong manifest
    agents      {node: [outcome, ...]}: outcome lần thứ n node AGENT đó chạy (hết danh sách thì lặp lại cái cuối)
    commands    {gate1|quality|expectfail|hygiene|size|checkplan|checklessons: ["pass"|"fail", ...]}: kết quả lần thứ n (hết thì "pass")
    approvals   {node: [outcome, ...]}: quyết định cho node APPROVAL
    expect      {"state": "SUCCEEDED|FAILED", "sequence": ["node:outcome", ...], "prompts": [{"node", "nth", "has", "lacks"}]}

Node AGENT được nhận ra bằng resource có trong prompt (frame ← flow.frame, repro ← bugfix.repro…). Cần: binary `aw` (biến AW hoặc
PATH) và `fake-claude` (biến AW_FAKE_CLAUDE hoặc PATH); trong repo aw, thiếu thì tự `go build`. Cần thêm git, jq, python3.
"""
import argparse
import importlib.util
import json
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import time

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
KIT_ROOT = os.path.dirname(HERE)
_spec = importlib.util.spec_from_file_location("context_check", os.path.join(HERE, "context-check.py"))
cc = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(cc)
publish = cc.publish

# Thứ tự quan trọng: resource đặc trưng hơn đứng trước (agent chẩn đoán cũng nạp review.read-only).
NODE_MARKERS = [("bugfix.repro", "repro"), ("bugfix.fix", "fix"), ("debug.diagnosis-report", "debug"), ("review.checklist", "review"),
                ("simplify.refine", "simplify"), ("retro.evidence", "retro"), ("flow.frame", "frame"), ("flow.plan", "plan"), ("flow.build", "build"), ("flow.sync", "sync")]

CLAUDE_WRAPPER = '''#!@PYTHON@
import json, os, subprocess, sys
home = os.environ.get("HOME", "")
fake = @FAKE@
data = sys.stdin.buffer.read()
env = dict(os.environ)
if sys.argv[1:] != ["--version"] and data.strip():
    doc = json.loads(data)
    keys = {r["resourceKey"] for r in doc.get("hardConstraints", []) + doc.get("resources", [])}
    markers = @MARKERS@
    node = next((n for k, n in markers if k in keys), "unknown")
    allowed = doc["taskContract"]["allowedOutcomes"]
    seq = json.load(open(os.path.join(home, "agents.json"))).get(node, [])
    counter = os.path.join(home, "prompts", node + ".n")
    n = int(open(counter).read()) if os.path.exists(counter) else 0
    open(counter, "w").write(str(n + 1))
    want = seq[min(n, len(seq) - 1)] if seq else allowed[0]
    env["AGENTKIT_HELPER_MODE"] = "outcome-from-prompt"
    env["AGENTKIT_HELPER_OUTCOME_PICK"] = str(allowed.index(want)) if want in allowed else "not-listed"
    with open(os.path.join(home, "prompts", "%s-%d.json" % (node, n + 1)), "wb") as f:
        f.write(data)
sys.exit(subprocess.run([fake] + sys.argv[1:], input=data, env=env).returncode)
'''


def fail(message):
    sys.exit(f"walk-workflow: {message}")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("scenario")
    parser.add_argument("--keep", action="store_true", help="giữ thư mục tạm (in đường dẫn)")
    parser.add_argument("--json", action="store_true", help="in kết quả dạng JSON")
    args = parser.parse_args()

    scenario_path = os.path.abspath(args.scenario)
    with open(scenario_path, encoding="utf-8") as f:
        scenario = json.load(f)
    manifest_path = os.path.normpath(os.path.join(os.path.dirname(scenario_path), scenario["manifest"]))

    work = tempfile.mkdtemp(prefix="walk-workflow-")
    worker = None
    exit_code = 0
    try:
        aw = cc.find_tool("AW", "aw", "./cmd/aw", work)
        fake = cc.find_tool("AW_FAKE_CLAUDE", "fake-claude", "./cmd/fake-claude", work)
        home = os.path.join(work, "home")
        os.makedirs(os.path.join(home, "seq"))
        os.makedirs(os.path.join(home, "prompts"))
        with open(os.path.join(home, "agents.json"), "w") as f:
            json.dump(scenario.get("agents", {}), f)
        for name, results in scenario.get("commands", {}).items():
            with open(os.path.join(home, "seq", name), "w") as f:
                f.write("\n".join(results) + "\n")
        claude = os.path.join(work, "claude")
        with open(claude, "w") as f:
            f.write(CLAUDE_WRAPPER.replace("@PYTHON@", sys.executable).replace("@FAKE@", json.dumps(fake)).replace("@MARKERS@", json.dumps(NODE_MARKERS)))
        os.chmod(claude, 0o755)

        repo = os.path.join(work, "repos", "repo")
        cc.make_repository(repo, set())
        manifest = publish.load_manifest(manifest_path)
        install = os.path.join(work, "install")
        env = dict(os.environ)
        env.update({"AW": aw, "AW_DB": os.path.join(install, "aw.db"), "AW_ARTIFACT_ROOT": os.path.join(install, "artifacts"),
                    "AW_WORKSPACE_ROOT": os.path.join(install, "workspaces"), "AW_CLAUDE_EXECUTABLE": claude,
                    "AW_STATE": os.path.join(work, "aw-state.json"), "AW_KIT": KIT_ROOT, "WAIT_SECONDS": "120",
                    "PATH": HERE + os.pathsep + os.environ.get("PATH", "")})
        os.makedirs(os.path.dirname(env["AW_DB"]))
        os.makedirs(env["AW_ARTIFACT_ROOT"])
        os.makedirs(env["AW_WORKSPACE_ROOT"])
        worker_env = dict(env, HOME=home)
        log = open(os.path.join(work, "worker.log"), "w")
        worker = subprocess.Popen([aw, "worker", "--db", env["AW_DB"], "--artifact-root", env["AW_ARTIFACT_ROOT"],
                                   "--workspace-root", env["AW_WORKSPACE_ROOT"], "--claude-executable", claude,
                                   "--env-allowlist", "PATH,HOME", "--claude-permission-mode", "acceptEdits"],
                                  env=worker_env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        time.sleep(1.0)

        def run(cmd, check=True):
            done = subprocess.run(cmd, env=env, capture_output=True, text=True, cwd=work)
            if check and done.returncode != 0:
                fail(f"{' '.join(cmd[:3])} hỏng:\n{(done.stdout + done.stderr).strip()[-600:]}")
            return done.stdout

        run([os.path.join(HERE, "init-project.sh"), "walk", "repo", repo])
        run([sys.executable, os.path.join(HERE, "aw-publish.py"), manifest_path])
        run([os.path.join(HERE, "create-root.sh"), "walk"])
        item = {"title": "walk: " + scenario["workflow"], "parentJoinPolicy": "ALL_CHILDREN_DONE",
                "effectiveScope": [{"repositoryId": "repo", "access": "WRITE", "reason": "walk-workflow"}],
                "contract": {"schemaVersion": 1, "behavior": "Đi workflow theo kịch bản (không có việc thật).",
                             "verificationSpec": "Không có.", "riskLevel": "MEDIUM",
                             "acceptanceCriteria": [{"description": "Không có", "verificationRef": "COMMAND_EXECUTION"}],
                             "workflowVersionId": "WORKFLOW_VERSION_ID"}}
        item_file = os.path.join(work, "work-item.json")
        with open(item_file, "w", encoding="utf-8") as f:
            json.dump(item, f, ensure_ascii=False)
        out = run([os.path.join(HERE, "run-task.sh"), item_file, scenario["workflow"]])
        run_id = next((line.split()[1] for line in out.splitlines() if line.startswith("Run: ")), "")
        if not run_id:
            fail("không thấy runId:\n" + out[-600:])

        approvals = {k: list(v) for k, v in scenario.get("approvals", {}).items()}
        state = ""
        for _ in range(60):
            if "state: SUCCEEDED" in out or "state: FAILED" in out or "state: CANCELLED" in out:
                break
            waiting = [line for line in out.splitlines() if "CHỜ DUYỆT node" in line]
            if not waiting:
                out = run([os.path.join(HERE, "watch-run.sh"), run_id], check=False)
                continue
            node = waiting[0].split("CHỜ DUYỆT node ")[1].split(":")[0]
            if not approvals.get(node):
                fail(f"kịch bản chưa có quyết định cho node duyệt {node!r}:\n{out[-600:]}")
            outcome = approvals[node].pop(0)
            out = run([os.path.join(HERE, "review-task.sh"), run_id, outcome, f"walk-workflow: {outcome}"], check=False)
        state = next((line.split("state: ")[1].split()[0] for line in out.splitlines() if "state: " in line), "?")

        timeline = json.loads(run([aw, "run", "timeline", run_id]))
        entries = sorted((e for e in timeline["entries"] if e.get("kind") == "NODE_RUN"), key=lambda e: e["activationSequence"])
        sequence = [f"{e['nodeKey']}:{e.get('selectedOutcome') or '-'}" for e in entries]
        report = {"state": state, "sequence": sequence}

        problems = []
        expect = scenario.get("expect", {})
        if "state" in expect and expect["state"] != state:
            problems.append(f"state: mong đợi {expect['state']}, thực tế {state}")
        if "sequence" in expect and expect["sequence"] != sequence:
            problems.append("thứ tự node khác mong đợi:\n    mong đợi: " + " ".join(expect["sequence"]) + "\n    thực tế:  " + " ".join(sequence))
        for check in expect.get("prompts", []):
            path = os.path.join(home, "prompts", f"{check['node']}-{check.get('nth', 1)}.json")
            if not os.path.exists(path):
                problems.append(f"không có prompt {check['node']} lần {check.get('nth', 1)}")
                continue
            text = open(path, encoding="utf-8").read()
            for needle in check.get("has", []):
                if needle not in text:
                    problems.append(f"prompt {check['node']}#{check.get('nth', 1)} thiếu {needle!r}")
            for needle in check.get("lacks", []):
                if needle in text:
                    problems.append(f"prompt {check['node']}#{check.get('nth', 1)} không được có {needle!r}")
        report["problems"] = problems
        if args.json:
            print(json.dumps(report, ensure_ascii=False, indent=1))
        else:
            print(f"{scenario.get('name', os.path.basename(scenario_path))}: {state}")
            print("  " + " ".join(sequence))
            for problem in problems:
                print("  LỖI " + problem)
        if problems:
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


if __name__ == "__main__":
    main()
