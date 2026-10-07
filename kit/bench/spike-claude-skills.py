#!/usr/bin/env python3
"""Spike V10-17: để nguyên skill, agent và hook của ClaudeKit trong `.claude/` của repository đích, aw có chạy được không và tốn gì?

Chạy 3 lượt THẬT (Claude CLI thật, khoảng 0,5 USD tổng): một node AGENT nhỏ của kit qua aw, trên ba bản của cùng một repository:
  v0-maker    repository sạch (không có .claude/)                              MAKER
  v1-maker    repository có đủ `.claude/` của ClaudeKit (skills, agents, hooks, settings.json)   MAKER
  v1-checker  như v1 nhưng node là CHECKER (chỉ đọc)
Ghi lại: attempt hỏng hay không (mã lỗi), token và chi phí, công cụ agent gọi, skill agent kể ra, ContextSnapshot có chứa nội dung CK không,
file hook để lại trong worktree. Kết quả đổ ra --out (mặc định /tmp/spike-claude-skills) dạng JSON + text; báo cáo kết luận ở
kit/bench/reports/spike-claude-skills.md.

Cách dùng: spike-claude-skills.py --ck <thư mục claude/ của claudekit-engineer> [--out DIR]
Cần: `aw` (biến AW hoặc PATH), `claude` đã đăng nhập, git, jq, node (hook của ClaudeKit chạy bằng node).
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
import time

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
KIT = os.path.dirname(HERE)
SCRIPTS = os.path.join(KIT, "scripts")
spec = importlib.util.spec_from_file_location("context_check", os.path.join(SCRIPTS, "context-check.py"))
cc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cc)

TASK = ("Ý tưởng thô: thêm nút \"xóa tất cả\" vào danh sách. Chỉ việc sau: viết một brainstorm RẤT NGẮN (tối đa 10 dòng) vào "
        "docs/spike/01-brainstorm.md. Ở cuối báo cáo cuối của bạn, liệt kê tên các skill bạn thấy có sẵn (Skill tool) và nói bạn có dùng skill nào không.")

CONDITIONS = [("v0-maker", False, "MAKER"), ("v1-maker", True, "MAKER"), ("v1-checker", True, "CHECKER")]


def sh(cmd, env=None, cwd=None, check=True):
    done = subprocess.run(cmd, env=env, cwd=cwd, capture_output=True, text=True)
    if check and done.returncode != 0:
        sys.exit(f"{' '.join(cmd[:3])} hỏng:\n{(done.stdout + done.stderr)[-600:]}")
    return done.stdout


def make_repo(path, ck_dir):
    cc.make_repository(path, {"docs"})
    if ck_dir:
        shutil.copytree(ck_dir, os.path.join(path, ".claude"), ignore=shutil.ignore_patterns(".git"))
        cc.git(path, "add", "-A", "-f", ".")
        cc.git(path, "-c", "user.name=spike", "-c", "user.email=spike@example.invalid", "commit", "-q", "-m", "ck")


def run_condition(name, with_ck, role, args, aw, claude, out):
    work = os.path.join(out, name)
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(work)
    repo = os.path.join(work, "repos", "repo")
    make_repo(repo, args.ck if with_ck else None)
    install = os.path.join(work, "install")
    env = dict(os.environ)
    env.update({"AW": aw, "AW_DB": os.path.join(install, "aw.db"), "AW_ARTIFACT_ROOT": os.path.join(install, "artifacts"),
                "AW_WORKSPACE_ROOT": os.path.join(install, "workspaces"), "AW_CLAUDE_EXECUTABLE": claude,
                "AW_STATE": os.path.join(work, "aw-state.json"), "AW_KIT": KIT, "WAIT_SECONDS": "300",
                "PATH": SCRIPTS + os.pathsep + os.environ.get("PATH", "")})
    for key in ("AW_ARTIFACT_ROOT", "AW_WORKSPACE_ROOT"):
        os.makedirs(env[key])
    os.makedirs(os.path.dirname(env["AW_DB"]), exist_ok=True)
    log = open(os.path.join(work, "worker.log"), "w")
    worker = subprocess.Popen([aw, "worker", "--db", env["AW_DB"], "--artifact-root", env["AW_ARTIFACT_ROOT"], "--workspace-root", env["AW_WORKSPACE_ROOT"],
                               "--claude-executable", claude, "--env-allowlist", "PATH,HOME", "--claude-permission-mode", "acceptEdits",
                               "--claude-effort", "medium", "--claude-max-budget-usd", "1"],
                              env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
    try:
        time.sleep(1.0)
        # project tối thiểu: agent-flow-brainstorm của kit, một workflow START → node → END
        agent_id = "agent-flow-brainstorm" if role == "MAKER" else "agent-reviewer"
        manifest = {"prefix": "spk-", "repository": "repo", "kit": {"path": os.path.join(KIT, "kit.json"), "version": "1"},
                    "provider": {"key": "claude", "model": "sonnet", "configIdentity": "spk-claude", "envAllowlist": ["HOME", "PATH"]},
                    "contextBudgetBytes": 65536, "agents": [{"id": agent_id, "from": "kit"}]}
        node = {"key": "probe", "type": "AGENT", "outcomes": ["done"] if role == "MAKER" else ["approved", "rework"],
                "agent": {"profileRef": {"$ref": f"agent:{agent_id}"}, "policyRefs": [{"$ref": "policy:policy-attempt-once"}, {"$ref": "policy:policy-permission"}],
                          "adapterBuildId": {"$ref": "adapter"}, "role": role}}
        wf_file = os.path.join(work, "wf.json")
        json.dump(cc.probe_template(node), open(wf_file, "w"))
        manifest["workflows"] = [{"id": "wf-spike", "name": "Workflow dò", "template": wf_file}]
        mfile = os.path.join(work, "aw-project.json")
        json.dump(manifest, open(mfile, "w"))
        sh([os.path.join(SCRIPTS, "init-project.sh"), "spike", "repo", repo], env=env, cwd=work)
        sh([sys.executable, os.path.join(SCRIPTS, "aw-publish.py"), mfile], env=env, cwd=work)
        sh([os.path.join(SCRIPTS, "create-root.sh"), "spike"], env=env, cwd=work)
        behavior = TASK if role == "MAKER" else ("Không có thay đổi cần review. Chỉ việc sau: liệt kê tên các skill bạn thấy có sẵn (Skill tool) và nói bạn có dùng skill nào không; "
                                                 "rồi kết thúc bằng outcome approved.")
        item = {"title": "spike " + name, "parentJoinPolicy": "ALL_CHILDREN_DONE",
                "effectiveScope": [{"repositoryId": "repo", "access": "WRITE", "reason": "spike", "pathScopes": ["docs"]}],
                "contract": {"schemaVersion": 1, "behavior": behavior, "verificationSpec": "Không có.", "riskLevel": "LOW",
                             "acceptanceCriteria": [{"description": "Báo cáo cuối có danh sách skill", "verificationRef": "AGENT_EXECUTION"}],
                             "workflowVersionId": "WORKFLOW_VERSION_ID"}}
        item_file = os.path.join(work, "work-item.json")
        json.dump(item, open(item_file, "w"), ensure_ascii=False)
        # run-task.sh trả về khi run kết thúc hoặc cần người; một node nên tới END
        out_text = subprocess.run([os.path.join(SCRIPTS, "run-task.sh"), item_file, "wf-spike"], env=env, cwd=work, capture_output=True, text=True).stdout
        open(os.path.join(work, "run-task.txt"), "w").write(out_text)
    finally:
        os.killpg(worker.pid, signal.SIGTERM)
    return collect(name, with_ck, role, work, out_text)


def collect(name, with_ck, role, work, run_text):
    db = sqlite3.connect(f"file:{os.path.join(work, 'install', 'aw.db')}?mode=ro", uri=True)
    db.row_factory = sqlite3.Row
    result = {"name": name, "role": role, "ck": with_ck, "runText": [l for l in run_text.splitlines() if "probe" in l or "state:" in l]}
    attempt = db.execute("select a.id, a.state, a.failure_code, a.termination_reason, a.context_snapshot_id from execution_attempts a "
                         "join node_runs n on n.id = a.node_run_id where n.node_key = 'probe' order by a.rowid desc limit 1").fetchone()
    if attempt is None:
        result["attempt"] = None
        return result
    result["attempt"] = {k: attempt[k] for k in ("state", "failure_code", "termination_reason")}
    tools, message, usage = [], "", {"in": 0, "out": 0, "usd": 0.0}
    for e in db.execute("select kind, payload_json from agent_events where attempt_id = ? order by sequence", (attempt["id"],)):
        payload = json.loads(e["payload_json"])
        if e["kind"] == "TOOL_CALL_STARTED":
            tools.append(payload["tool"].get("Name") or payload["tool"].get("Kind") or "?")
        elif e["kind"] == "ASSISTANT_MESSAGE" and payload.get("message", "").strip():
            message = payload["message"]
        elif e["kind"] == "USAGE_REPORTED":
            u = payload.get("usage") or {}
            usage["in"] += u.get("InputTokens", 0) + u.get("CachedInputTokens", 0)
            usage["out"] += u.get("OutputTokens", 0)
            usage["usd"] += u.get("CostUSD", 0.0)
        elif e["kind"] == "STATUS_CHANGED" and "hook" in payload.get("message", "").lower():
            result.setdefault("hookStatus", []).append(payload["message"])
    result["tools"] = tools
    result["usage"] = {k: round(v, 4) for k, v in usage.items()}
    result["finalMessage"] = message
    result["ckInMessage"] = any(s in message for s in ("ck-debug", "ck-plan", "cook", "brainstorm"))
    row = db.execute("select canonical_content from context_snapshots where id = ?", (attempt["context_snapshot_id"],)).fetchone()
    blob = row["canonical_content"] if row else ""
    result["snapshotBytes"] = len(blob)
    result["snapshotMentionsCk"] = any(s in blob for s in (".claude/skills", "ck:debug", "ClaudeKit", "claudekit", "ck-debug"))
    # file còn lại trong worktree
    left = []
    for root, _, files in os.walk(os.path.join(work, "install", "workspaces")):
        if ".git" in root.split(os.sep):
            continue
        for f in files:
            full = os.path.join(root, f)
            if ".claude/hooks/.logs" in full or "session-state" in full:
                left.append(os.path.relpath(full, os.path.join(work, "install", "workspaces")))
    result["hookFilesInWorktree"] = left[:10]
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--ck", required=True, help="thư mục claude/ của claudekit-engineer (chép thành .claude/ của repository)")
    parser.add_argument("--out", default="/tmp/spike-claude-skills")
    parser.add_argument("--only", action="append", help="chỉ chạy các điều kiện này")
    args = parser.parse_args()
    work = os.path.join(args.out, ".tools")
    os.makedirs(work, exist_ok=True)
    aw = cc.find_tool("AW", "aw", "./cmd/aw", work)
    claude = shutil.which(os.environ.get("AW_CLAUDE_EXECUTABLE", "claude")) or sys.exit("không thấy claude")
    results = []
    for name, with_ck, role in CONDITIONS:
        if args.only and name not in args.only:
            continue
        print(f"== {name}", flush=True)
        results.append(run_condition(name, with_ck, role, args, aw, claude, args.out))
        print(json.dumps(results[-1], ensure_ascii=False, indent=1), flush=True)
    json.dump(results, open(os.path.join(args.out, "results.json"), "w"), ensure_ascii=False, indent=1)


if __name__ == "__main__":
    main()
