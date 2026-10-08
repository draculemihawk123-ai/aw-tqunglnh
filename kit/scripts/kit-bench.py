#!/usr/bin/env python3
"""Chạy golden workload "bình luận" của kit/bench với Claude CLI THẬT và thu số đo (kit-metrics.py).

Cách dùng (qua kit-bench.sh):
    kit-bench.sh --label A0 --runs 2 [--tree REV|THƯ_MỤC] [--out THƯ_MỤC] [--parallel]
                 [--stop-after setup|F-00|T-01|T-02|T-03|review] [--no-readiness] [--setup-only] [--force]

Mỗi lượt dựng một bản cài aw MỚI (database, worker, worktree riêng), dựng ba repository từ fixture
kit/bench/issue-tracker-mvp (trạng thái sau C-01, A-01, W-01 của hướng dẫn issue tracker), publish project của hướng dẫn với
kho của `--tree`, rồi chạy: F-00 (wf-feature-definition) → chia task → T-01 (contracts), T-02 (api), T-03 (web) → review độc
lập (wf-code-review). Các cổng người duyệt do script tự duyệt `approved` (CHỈ trong bench); agent chọn needs_info thì lượt đó
dừng và được ghi lại; run hỏng được chạy lại đúng một lần trên chính WorkItem (như người vận hành sẽ làm).

`--tree` cho biết lấy kho (kit/) và project (docs/guides/issue-tracker) từ đâu: một git rev (script `git archive` ra thư mục) hoặc
một thư mục có kit/ và docs/guides/issue-tracker/. Mặc định: thư mục làm việc hiện tại của repo aw. Fixture, work item của review
và script harness luôn lấy từ chính kit chứa script này, nên hai nhãn đo cùng một bài toán.

Kết quả: <out>/<label>/run-N/{steps.json, metrics.json, logs/, docs/, install/, aw-state.json}. Tốn tiền thật (khoảng 3 USD mỗi lượt).
Dùng kit-metrics.py compare <out>/A0 <out>/A1 để so hai nhãn.
"""
import argparse
import concurrent.futures
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
KIT_ROOT = os.path.dirname(HERE)
REPO_ROOT = os.path.dirname(KIT_ROOT)
BENCH = os.path.join(KIT_ROOT, "bench")
STEPS = ["setup", "F-00", "T-01", "T-02", "T-03", "review"]
WAITING = re.compile(r"CHỜ DUYỆT node (\S+): review-task\.sh (\S+)")
RUN_LINE = re.compile(r"^Run: (\S+)\s+state: (\S+)", re.M)


def fail(message):
    sys.exit(f"kit-bench: {message}")


def find_tool(env_name, command, go_package, workdir):
    path = os.environ.get(env_name) or shutil.which(command)
    if path:
        return os.path.abspath(path)
    if go_package and shutil.which("go") and os.path.isfile(os.path.join(REPO_ROOT, "go.mod")):
        out = os.path.join(workdir, command)
        built = subprocess.run(["go", "build", "-o", out, go_package], cwd=REPO_ROOT, capture_output=True, text=True)
        if built.returncode == 0:
            return out
        fail(f"go build {go_package} hỏng: {built.stderr.strip()[-300:]}")
    fail(f"không thấy `{command}`: đặt {env_name} hoặc thêm vào PATH")


def export_tree(tree, dest):
    """Thư mục gốc có kit/ và docs/guides/issue-tracker/: từ thư mục có sẵn hoặc `git archive <rev>`."""
    if os.path.isdir(tree):
        root = os.path.abspath(tree)
    else:
        os.makedirs(dest, exist_ok=True)
        archive = subprocess.run(["git", "-C", REPO_ROOT, "archive", tree, "kit", "docs/guides/issue-tracker"], capture_output=True)
        if archive.returncode != 0:
            fail(f"git archive {tree} hỏng: {archive.stderr.decode()[-300:]}")
        subprocess.run(["tar", "-x", "-C", dest], input=archive.stdout, check=True)
        root = dest
    for needed in ("kit/kit.json", "docs/guides/issue-tracker/aw-project.json"):
        if not os.path.isfile(os.path.join(root, needed)):
            fail(f"--tree {tree}: thiếu {needed}")
    return root


class Lane:
    def __init__(self, args, tools, tree_root, label_dir, index):
        self.args, self.tools, self.tree = args, tools, tree_root
        self.dir = os.path.join(label_dir, f"run-{index}")
        self.index = index
        self.steps = []
        self.worker = None
        install = os.path.join(self.dir, "install")
        self.env = dict(os.environ)
        self.env.update({
            "AW": tools["aw"], "AW_DB": os.path.join(install, "aw.db"), "AW_ARTIFACT_ROOT": os.path.join(install, "artifacts"),
            "AW_WORKSPACE_ROOT": os.path.join(install, "workspaces"), "AW_CLAUDE_EXECUTABLE": tools["claude"],
            "AW_STATE": os.path.join(self.dir, "aw-state.json"), "AW_KIT": os.path.join(tree_root, "kit"),
            "PATH": os.path.join(tree_root, "kit", "scripts") + os.pathsep + os.environ.get("PATH", ""),
            "AUTHOR_NAME": "kit-bench", "AUTHOR_EMAIL": "kit-bench@example.invalid",
            # cấu hình Claude riêng cho từng lượt: không nạp skill, hook hay cài đặt cấp người dùng của máy chạy (đo ở spike V10-17)
            "CLAUDE_CONFIG_DIR": os.path.join(self.dir, "claude-config"),
            "WAIT_SECONDS": os.environ.get("WAIT_SECONDS", "2700")})
        self.scripts = os.path.join(tree_root, "kit", "scripts")
        self.logs = os.path.join(self.dir, "logs")
        self.log_no = 0

    # ---- tiện ích
    def sh(self, name, cmd, stdin=None, check=True, timeout=None):
        self.log_no += 1
        path = os.path.join(self.logs, f"{self.log_no:02d}-{name}.txt")
        done = subprocess.run(cmd, env=self.env, cwd=self.dir, input=stdin, capture_output=True, text=True, timeout=timeout)
        text = done.stdout + (("\n[stderr]\n" + done.stderr) if done.stderr.strip() else "")
        with open(path, "w", encoding="utf-8") as f:
            f.write("$ " + " ".join(cmd) + "\n" + text)
        if check and done.returncode != 0:
            raise StepError(f"{name} thoát mã {done.returncode}: {text.strip()[-400:]}")
        return done.stdout

    def script(self, name, *args, **kw):
        return self.sh(name.replace(".sh", ""), [os.path.join(self.scripts, name), *map(str, args)], **kw)

    def record(self, name, state, started, **extra):
        self.steps.append({"name": name, "state": state, "seconds": round(time.time() - started, 1), **extra})
        with open(os.path.join(self.dir, "steps.json"), "w", encoding="utf-8") as f:
            json.dump(self.steps, f, ensure_ascii=False, indent=1)

    # ---- dựng môi trường
    def setup(self):
        started = time.time()
        for sub in ("logs", "repos", "install/artifacts", "install/workspaces"):
            os.makedirs(os.path.join(self.dir, sub), exist_ok=True)
        log = open(os.path.join(self.logs, "worker.log"), "w")
        a = self.args
        cmd = [self.tools["aw"], "worker", "--db", self.env["AW_DB"], "--artifact-root", self.env["AW_ARTIFACT_ROOT"],
               "--workspace-root", self.env["AW_WORKSPACE_ROOT"], "--claude-executable", self.tools["claude"],
               "--env-allowlist", "PATH,HOME,CLAUDE_CONFIG_DIR", "--claude-permission-mode", "acceptEdits", "--claude-effort", a.effort,
               "--claude-max-budget-usd", str(a.max_budget)]
        self.worker = subprocess.Popen(cmd, env=self.env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        time.sleep(1.0)
        repos = {}
        for repo in ("contracts", "api", "web"):
            repos[repo] = os.path.join(self.dir, "repos", repo)
            self.sh(f"clone-{repo}", ["git", "clone", "-q", "-b", "main", os.path.join(BENCH, "issue-tracker-mvp", repo + ".bundle"), repos[repo]])
        self.script("init-project.sh", "bench", "contracts", repos["contracts"])
        for repo in ("api", "web"):
            self.script("add-repository.sh", repo, repos[repo])
        if not a.no_readiness:
            profiles = {"contracts": ("python3", ["-c", "import json; json.load(open(\"spec/openapi.json\"))"], 60),
                        "api": ("sh", ["-c", "cd backend && mvn -q -B test"], 900)}
            for repo, (exe, argv, seconds) in profiles.items():
                body = json.dumps({"verification": {"executable": exe, "argv": argv, "timeoutSeconds": seconds}})
                self.sh(f"readiness-{repo}", [self.tools["aw"], "repository", "readiness", "set", "--idempotency-key", f"readiness-{repo}", repo], stdin=body)
        self.sh("publish", [sys.executable, os.path.join(self.scripts, "aw-publish.py"),
                            os.path.join(self.tree, "docs", "guides", "issue-tracker", "aw-project.json"),
                            "--kit", os.path.join(self.tree, "kit")], timeout=600)
        self.script("create-root.sh", f"Bench {os.path.basename(os.path.dirname(self.dir))} {self.index}: bình luận", "api=WRITE", "web=WRITE", timeout=3600)
        self.record("setup", "SUCCEEDED", started)

    # ---- chạy một WorkItem tới khi xong, tự duyệt cổng người
    def drive(self, name, work_item_file, workflow):
        started = time.time()
        out = self.script("run-task.sh", work_item_file, workflow, check=False)
        item = (re.search(r"WorkItem: (\S+)", out) or [None, ""])[1]
        retries, runs, stopped = 0, [], None
        for _ in range(40):
            for run_id, _state in RUN_LINE.findall(out):
                if run_id not in runs:
                    runs.append(run_id)
            states = RUN_LINE.findall(out)
            waiting = WAITING.findall(out)
            if waiting and states and states[-1][1] in ("RUNNING", "WAITING"):
                node, run_id = waiting[-1]
                if node.startswith("needs-info"):
                    stopped = f"needs_info ({node})"
                    break
                out = self.script("review-task.sh", run_id, "approved", "kit-bench tự duyệt", check=False)
                continue
            if states and states[-1][1] == "SUCCEEDED":
                self.record(name, "SUCCEEDED", started, retries=retries, runIds=runs, workItemId=item)
                return True
            if states and states[-1][1] == "FAILED" and retries < 1 and item:
                retries += 1
                out = self.script("retry-task.sh", item, "kit-bench: chạy lại một lần", check=False)
                continue
            break
        state = "STOPPED" if stopped else "FAILED"
        self.record(name, state, started, retries=retries, runIds=runs, workItemId=item, stopped=stopped,
                    tail=out[-500:] if not stopped else None)
        return False

    def commit(self, message):
        self.script("commit-task.sh", message)

    def collect_docs(self):
        docs = os.path.join(self.dir, "docs", "features", "comments")
        for repo in ("contracts", "api", "web"):
            done = subprocess.run([os.path.join(self.scripts, "worktree-path.sh"), repo], env=self.env, capture_output=True, text=True)
            source = os.path.join(done.stdout.strip(), "docs", "features", "comments") if done.returncode == 0 else ""
            if source and os.path.isdir(source):
                shutil.copytree(source, docs, dirs_exist_ok=True)

    # ---- một lượt
    def run(self):
        a = self.args
        try:
            self.setup()
            if a.setup_only or a.stop_after == "setup":
                return
            wi = os.path.join(self.tree, "docs", "guides", "issue-tracker", "work-items")
            if not self.drive("F-00", os.path.join(wi, "feature-comments.json"), "wf-feature-definition"):
                return
            self.commit("F-00: tài liệu tính năng bình luận")
            if a.stop_after == "F-00":
                return
            tasks = os.path.join(self.dir, "tasks")
            self.script("split-tasks.sh", "comments", tasks)
            for task, repo in (("T-01", "contracts"), ("T-02", "api"), ("T-03", "web")):
                workflow = a.delivery or f"wf-task-delivery-{repo}"
                if not self.drive(task, os.path.join(tasks, task + ".json"), workflow):
                    return
                self.commit(f"{task}: bench")
                if a.stop_after == task:
                    return
            self.drive("review", os.path.join(BENCH, "work-items", "review-comments.json"), "wf-code-review")
        except StepError as exc:
            self.record("lỗi harness", "FAILED", time.time(), error=str(exc))
        except subprocess.TimeoutExpired as exc:
            self.record("timeout harness", "FAILED", time.time(), error=str(exc)[:300])
        finally:
            try:
                self.collect_docs()
            except Exception as exc:  # noqa: BLE001 — thu docs không được làm hỏng kết quả
                print(f"run-{self.index}: không thu được docs: {exc}", file=sys.stderr)
            if self.worker is not None:
                try:
                    os.killpg(self.worker.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
            if os.path.isfile(self.env["AW_DB"]) and not a.setup_only:
                subprocess.run([sys.executable, os.path.join(HERE, "kit-metrics.py"), "collect", self.dir], check=False)


class StepError(Exception):
    pass


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--label", required=True, help="tên lần đo, ví dụ A0")
    parser.add_argument("--runs", type=int, default=2)
    parser.add_argument("--tree", default=REPO_ROOT, help="git rev hoặc thư mục có kit/ và docs/guides/issue-tracker/ (mặc định: repo hiện tại)")
    parser.add_argument("--out", default=os.path.join(BENCH, "runs"))
    parser.add_argument("--parallel", action="store_true", help="chạy các lượt song song (nhanh hơn, cùng quota provider)")
    parser.add_argument("--stop-after", choices=STEPS, help="dừng sau bước này (thử từng phần)")
    parser.add_argument("--no-readiness", action="store_true", help="bỏ readiness profile (không chạy mvn lúc dựng worktree)")
    parser.add_argument("--setup-only", action="store_true", help="chỉ dựng môi trường (không chạy agent, không tốn tiền)")
    parser.add_argument("--delivery", help="workflow giao task dùng cho cả T-01…T-03 (mặc định wf-task-delivery-<repo>)")
    parser.add_argument("--effort", default="medium")
    parser.add_argument("--max-budget", type=float, default=3.0, help="trần USD mỗi attempt (--claude-max-budget-usd)")
    parser.add_argument("--force", action="store_true", help="xóa <out>/<label> nếu đã có")
    args = parser.parse_args()

    label_dir = os.path.join(os.path.abspath(args.out), args.label)
    if os.path.exists(label_dir):
        if not args.force:
            fail(f"{label_dir} đã có; đặt nhãn khác hoặc --force")
        shutil.rmtree(label_dir)
    os.makedirs(label_dir)
    for fixture in ("contracts", "api", "web"):
        if not os.path.isfile(os.path.join(BENCH, "issue-tracker-mvp", fixture + ".bundle")):
            fail(f"thiếu fixture kit/bench/issue-tracker-mvp/{fixture}.bundle")
    tools_dir = os.path.join(label_dir, "bin")
    os.makedirs(tools_dir)
    tools = {"aw": find_tool("AW", "aw", "./cmd/aw", tools_dir), "claude": find_tool("AW_CLAUDE_EXECUTABLE", "claude", "", tools_dir)}
    tree_root = export_tree(args.tree, os.path.join(label_dir, "tree"))
    print(f"kit-bench {args.label}: {args.runs} lượt, tree={args.tree}, kho={os.path.join(tree_root, 'kit')}")
    lanes = [Lane(args, tools, tree_root, label_dir, i) for i in range(1, args.runs + 1)]
    if args.parallel and args.runs > 1:
        with concurrent.futures.ThreadPoolExecutor(max_workers=args.runs) as pool:
            list(pool.map(lambda lane: lane.run(), lanes))
    else:
        for lane in lanes:
            lane.run()
    for lane in lanes:
        states = ", ".join(f"{s['name']}={s['state']}" for s in lane.steps)
        print(f"run-{lane.index}: {states or '(chưa có bước nào)'}")


if __name__ == "__main__":
    main()
