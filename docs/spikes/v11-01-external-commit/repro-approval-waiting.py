import importlib.util, json, os, shutil, signal, subprocess, sys, tempfile, time
sys.dont_write_bytecode = True
R = os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))), "kit")
spec = importlib.util.spec_from_file_location("ww", R + "/scripts/walk-workflow.py"); ww = importlib.util.module_from_spec(spec); spec.loader.exec_module(ww)
cc, publish = ww.cc, ww.publish
SC = R + "/scripts"
work = tempfile.mkdtemp(prefix="probe-")
home = os.path.join(work, "home"); os.makedirs(os.path.join(home, "seq")); os.makedirs(os.path.join(home, "prompts"))
json.dump({"frame": ["full"], "plan": ["done"], "build": ["done"], "review": ["approved"], "sync": ["done"]}, open(home + "/agents.json", "w"))
aw = cc.find_tool("AW", "aw", "./cmd/aw", work); fake = cc.find_tool("AW_FAKE_CLAUDE", "fake-claude", "./cmd/fake-claude", work)
claude = work + "/claude"
open(claude, "w").write(ww.CLAUDE_WRAPPER.replace("@PYTHON@", sys.executable).replace("@FAKE@", json.dumps(fake)).replace("@MARKERS@", json.dumps(ww.NODE_MARKERS))); os.chmod(claude, 0o755)
repo = work + "/repos/repo"; cc.make_repository(repo, set())
install = work + "/install"
env = dict(os.environ, AW=aw, AW_DB=install + "/aw.db", AW_ARTIFACT_ROOT=install + "/artifacts", AW_WORKSPACE_ROOT=install + "/workspaces",
           AW_CLAUDE_EXECUTABLE=claude, AW_STATE=work + "/aw-state.json", AW_KIT=R, WAIT_SECONDS="120", PATH=SC + os.pathsep + os.environ["PATH"])
for k in ("AW_ARTIFACT_ROOT", "AW_WORKSPACE_ROOT"): os.makedirs(env[k])
log = open(work + "/worker.log", "w")
worker = subprocess.Popen([aw, "worker", "--db", env["AW_DB"], "--artifact-root", env["AW_ARTIFACT_ROOT"], "--workspace-root", env["AW_WORKSPACE_ROOT"], "--claude-executable", claude, "--env-allowlist", "PATH,HOME", "--claude-permission-mode", "acceptEdits"], env=dict(env, HOME=home), stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
def sh(*c, check=True):
    d = subprocess.run(list(c), env=env, capture_output=True, text=True, cwd=work)
    if check and d.returncode: print("LỖI", c[:2], (d.stdout + d.stderr)[-400:]); sys.exit(1)
    return d.stdout + d.stderr
def item(title):
    p = work + "/wi-%s.json" % title
    json.dump({"title": title, "parentJoinPolicy": "ALL_CHILDREN_DONE", "effectiveScope": [{"repositoryId": "repo", "access": "WRITE", "reason": "probe"}],
               "contract": {"schemaVersion": 1, "behavior": "probe", "verificationSpec": "-", "riskLevel": "MEDIUM", "acceptanceCriteria": [{"description": "x", "verificationRef": "COMMAND_EXECUTION"}], "workflowVersionId": "X"}}, open(p, "w"))
    return p
try:
    time.sleep(1)
    sh(SC + "/init-project.sh", "probe", "repo", repo)
    sh(sys.executable, SC + "/aw-publish.py", R + "/tests/walk/aw-project.json")
    sh(SC + "/create-root.sh", "probe")
    out = sh(SC + "/run-task.sh", item("A"), "wf-task-delivery-plus")
    print("=== A: run dừng ở cổng duyệt\n", "\n".join(l for l in out.splitlines() if "CHỜ" in l or "state" in l))
    run = next(l.split()[1] for l in out.splitlines() if l.startswith("Run: "))
    wt = sh(SC + "/worktree-path.sh", "repo").strip().splitlines()[-1]
    print("worktree:", wt, "| HEAD trước:", subprocess.run(["git", "-C", wt, "rev-parse", "--short", "HEAD"], capture_output=True, text=True).stdout.strip())
    print("status worktree:", subprocess.run(["git", "-C", wt, "status", "--porcelain"], capture_output=True, text=True).stdout.strip() or "(sạch)")
    # người dùng tự commit bằng git/IDE trong lúc chờ duyệt
    open(wt + "/manual.txt", "w").write("người dùng sửa tay\n")
    subprocess.run(["git", "-C", wt, "add", "-A"], check=True)
    subprocess.run(["git", "-C", wt, "-c", "user.name=u", "-c", "user.email=u@x.invalid", "commit", "-q", "-m", "commit tay"], check=True)
    print("HEAD sau commit tay:", subprocess.run(["git", "-C", wt, "rev-parse", "--short", "HEAD"], capture_output=True, text=True).stdout.strip())
    out = sh(SC + "/review-task.sh", run, "approved", "duyệt sau commit tay", check=False)
    print("=== A: sau khi duyệt\n", "\n".join(l for l in out.splitlines() if l.strip().startswith(("#", "attempt", "Run:", "BLOCK", "BỊ", "WORKTREE", "evidence")) or "Run" in l))
    print(sh(aw, "repository-workspace", "--help", check=False)[:0])
    ws = sh(SC + "/worktree-path.sh", "repo", check=False)
    # B: chạy WorkItem thứ hai trên cùng family sau commit tay
    out = sh(SC + "/run-task.sh", item("B"), "wf-task-delivery-plus", check=False)
    print("=== B: WorkItem thứ hai sau commit tay\n", "\n".join(l for l in out.splitlines() if l.strip().startswith(("#", "attempt", "Run:", "BLOCK", "BỊ", "WORKTREE", "CHỜ")) or "ready" in l or "từ chối" in l or "aw:" in l))
finally:
    try: os.killpg(worker.pid, signal.SIGTERM)
    except ProcessLookupError: pass
    print("work dir:", work)
