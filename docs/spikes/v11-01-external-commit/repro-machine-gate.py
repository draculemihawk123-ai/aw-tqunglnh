import importlib.util, json, os, signal, subprocess, sys, tempfile, time
sys.dont_write_bytecode = True
R = os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))), "kit"); SC = R + "/scripts"
spec = importlib.util.spec_from_file_location("ww", SC + "/walk-workflow.py"); ww = importlib.util.module_from_spec(spec); spec.loader.exec_module(ww)
cc = ww.cc
work = tempfile.mkdtemp(prefix="gprobe-")
home = work + "/home"; os.makedirs(home)
aw = cc.find_tool("AW", "aw", "./cmd/aw", work); fake = cc.find_tool("AW_FAKE_CLAUDE", "fake-claude", "./cmd/fake-claude", work)
repo = work + "/repos/repo"; cc.make_repository(repo, set())
wf = {"schemaVersion": "1", "nodes": [{"key": "start", "type": "START", "outcomes": ["next"]},
      {"key": "secrets", "type": "MACHINE_GATE", "outcomes": ["passed"], "machineGate": {"gateRef": {"$ref": "gate:gate-secrets"}, "policyRefs": [{"$ref": "policy:policy-attempt-once"}, {"$ref": "policy:policy-permission"}]}},
      {"key": "end", "type": "END"}], "edges": [{"key": "a", "from": "start", "outcome": "next", "to": "secrets"}, {"key": "b", "from": "secrets", "outcome": "passed", "to": "end"}],
      "completionPolicyRef": {"$ref": "policy:policy-completion"}}
json.dump(wf, open(work + "/wf.json", "w"))
json.dump({"prefix": "gp-", "repository": "repo", "kit": {"path": R + "/kit.json", "version": "1"},
           "provider": {"key": "claude", "model": "sonnet", "configIdentity": "gp", "envAllowlist": ["HOME", "PATH"]},
           "commands": [{"id": "cmd-secrets-gate", "from": "kit", "repository": "repo"}], "gates": [{"id": "gate-secrets", "from": "kit"}],
           "workflows": [{"id": "wf-gate", "name": "gate", "template": work + "/wf.json"}]}, open(work + "/p.json", "w"))
install = work + "/install"
env = dict(os.environ, AW=aw, AW_DB=install + "/aw.db", AW_ARTIFACT_ROOT=install + "/artifacts", AW_WORKSPACE_ROOT=install + "/workspaces", AW_CLAUDE_EXECUTABLE=fake, AW_STATE=work + "/aw-state.json", AW_KIT=R, WAIT_SECONDS="60", PATH=SC + os.pathsep + os.environ["PATH"])
for k in ("AW_ARTIFACT_ROOT", "AW_WORKSPACE_ROOT"): os.makedirs(env[k])
worker = subprocess.Popen([aw, "worker", "--db", env["AW_DB"], "--artifact-root", env["AW_ARTIFACT_ROOT"], "--workspace-root", env["AW_WORKSPACE_ROOT"], "--claude-executable", fake, "--env-allowlist", "PATH,HOME", "--claude-permission-mode", "acceptEdits"], env=dict(env, HOME=home), stdout=open(work + "/w.log", "w"), stderr=subprocess.STDOUT, start_new_session=True)
def sh(*c, check=True):
    d = subprocess.run(list(c), env=env, capture_output=True, text=True, cwd=work)
    if check and d.returncode: print("LỖI", c[:2], (d.stdout + d.stderr)[-500:]); sys.exit(1)
    return d.stdout + d.stderr
def item(t):
    p = work + "/%s.json" % t
    json.dump({"title": t, "parentJoinPolicy": "ALL_CHILDREN_DONE", "effectiveScope": [{"repositoryId": "repo", "access": "WRITE", "reason": "p"}], "contract": {"schemaVersion": 1, "behavior": "p", "verificationSpec": "-", "riskLevel": "MEDIUM", "acceptanceCriteria": [{"description": "x", "verificationRef": "COMMAND_EXECUTION"}], "workflowVersionId": "X"}}, open(p, "w")); return p
def show(label, out): print("==", label); print("\n".join(l for l in out.splitlines() if l.strip().startswith(("#", "attempt", "Run:", "BLOCK", "BỊ", "WORKTREE")) or "aw:" in l or "từ chối" in l)[:900])
try:
    time.sleep(1)
    sh(SC + "/init-project.sh", "gp", "repo", repo)
    sh(sys.executable, SC + "/aw-publish.py", work + "/p.json")
    sh(SC + "/create-root.sh", "gp")
    show("1. trước khi commit tay (HEAD == revision aw ghim)", sh(SC + "/run-task.sh", item("one"), "wf-gate", check=False))
    wt = sh(SC + "/worktree-path.sh", "repo").strip().splitlines()[-1]
    open(wt + "/manual.txt", "w").write("tay\n")
    subprocess.run(["git", "-C", wt, "add", "-A"], check=True); subprocess.run(["git", "-C", wt, "-c", "user.name=u", "-c", "user.email=u@x.invalid", "commit", "-q", "-m", "commit tay"], check=True)
    print("HEAD sau commit tay:", subprocess.run(["git", "-C", wt, "rev-parse", "--short", "HEAD"], capture_output=True, text=True).stdout.strip())
    show("2. WorkItem mới sau commit tay", sh(SC + "/run-task.sh", item("two"), "wf-gate", check=False))
finally:
    try: os.killpg(worker.pid, signal.SIGTERM)
    except ProcessLookupError: pass
    print("work dir:", work)
