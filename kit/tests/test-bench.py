#!/usr/bin/env python3
"""Kiểm tra logic của kit-bench.py (tự duyệt cổng, chạy lại, needs_info) và kit-metrics.py (collect, compare, blind) bằng dữ liệu
tổng hợp: không cần aw, không tốn tiền. Mẫu output của run-task.sh/review-task.sh lấy từ lần chạy thật."""
import importlib.util
import json
import os
import sqlite3
import subprocess
import sys
import tempfile

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPTS = os.path.join(os.path.dirname(HERE), "scripts")


def load(name, file):
    spec = importlib.util.spec_from_file_location(name, os.path.join(SCRIPTS, file))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


bench = load("kit_bench", "kit-bench.py")
RUN = "4dd24e8c-53cf-40a3-8cbe-548f5d978fc5"
START = f"WorkItem: wi-1\n{{\"ready\":true}}\nRun: {RUN}  state: RUNNING\n  #1 start (vòng 0): SUCCEEDED next\n"
WAIT = lambda node: f"  CHỜ DUYỆT node {node}: review-task.sh {RUN} <approved|rejected|revise> [\"phản hồi\"]\n"
DONE = f"Đã chọn 'approved' cho node x; chờ run chạy tiếp...\nRun: {RUN}  state: SUCCEEDED\n"
FAILED = f"WorkItem: wi-1\nRun: {RUN}  state: FAILED\n  BLOCKER RUN_FAILED đang mở\n  Run FAILED. Xem lỗi rồi chạy lại trên chính WorkItem này:\n"


def lane(outputs):
    """Lane giả: script() trả lần lượt các output mẫu và ghi lại tên script đã gọi."""
    obj = object.__new__(bench.Lane)
    obj.dir = tempfile.mkdtemp()
    obj.steps, calls, queue = [], [], list(outputs)
    obj.script = lambda name, *args, **kw: (calls.append(name), queue.pop(0))[1]
    return obj, calls


def check(condition, message):
    if not condition:
        print("HỎNG:", message)
        sys.exit(1)


# 1) hai cổng người rồi xong
obj, calls = lane([START + WAIT("gate-a"), START + WAIT("gate-b"), DONE])
check(obj.drive("F-00", "f.json", "wf") is True, "đường đi thẳng phải thành công")
check(calls == ["run-task.sh", "review-task.sh", "review-task.sh"], calls)
check(obj.steps[0]["state"] == "SUCCEEDED" and obj.steps[0]["retries"] == 0 and obj.steps[0]["runIds"] == [RUN], obj.steps)
# 2) run hỏng, chạy lại một lần rồi xong
obj, calls = lane([FAILED, START + WAIT("gate2"), DONE])
check(obj.drive("T-02", "t.json", "wf") is True, "hỏng một lần rồi chạy lại phải thành công")
check(calls == ["run-task.sh", "retry-task.sh", "review-task.sh"] and obj.steps[0]["retries"] == 1, (calls, obj.steps))
# 3) hỏng hai lần thì dừng, ghi FAILED
obj, calls = lane([FAILED, FAILED])
check(obj.drive("T-03", "t.json", "wf") is False and obj.steps[0]["state"] == "FAILED", obj.steps)
check(calls == ["run-task.sh", "retry-task.sh"], calls)
# 4) agent chọn needs_info: dừng lượt, không tự duyệt
obj, calls = lane([START + WAIT("needs-info")])
check(obj.drive("F-00", "f.json", "wf") is False and obj.steps[0]["state"] == "STOPPED", obj.steps)
check("needs-info" in obj.steps[0]["stopped"] and calls == ["run-task.sh"], (calls, obj.steps))
print("ok  kit-bench: tự duyệt cổng, chạy lại một lần, dừng ở needs_info")

# 5) kit-metrics collect trên database tổng hợp
metrics = load("kit_metrics", "kit-metrics.py")
root = tempfile.mkdtemp()
run = os.path.join(root, "A", "run-1")
os.makedirs(os.path.join(run, "install"))
db = sqlite3.connect(os.path.join(run, "install", "aw.db"))
db.executescript("""
create table workflow_runs(id text, work_item_id text);
create table node_runs(id text, run_id text, node_key text, activation_sequence int, iteration int, selected_outcome text);
create table execution_attempts(id text, node_run_id text, state text, failure_code text, provider_key text);
create table agent_events(attempt_id text, sequence int, kind text, payload_json text);
insert into workflow_runs values ('r1','w1');
insert into node_runs values ('n1','r1','build',1,0,'done'),('n2','r1','gate1',2,0,'failed'),('n3','r1','build',3,1,'done'),
  ('n4','r1','gate1',4,1,'passed'),('n5','r1','ai-review',5,0,'approved');
insert into execution_attempts values ('a1','n1','SUCCEEDED',null,'claude'),('a2','n2','SUCCEEDED',null,''),
  ('a3','n3','FAILED','SCOPE_VIOLATION','claude'),('a4','n5','SUCCEEDED',null,'claude');
""")
usage = lambda i, o, c: json.dumps({"usage": {"InputTokens": i, "CachedInputTokens": 0, "OutputTokens": o, "CostUSD": c}})
db.executemany("insert into agent_events values (?,?,?,?)", [
    ("a1", 1, "USAGE_REPORTED", usage(100, 10, 0.5)), ("a3", 1, "USAGE_REPORTED", usage(50, 5, 0.25)),
    ("a4", 1, "ASSISTANT_MESSAGE", json.dumps({"message": "- [Minor] a.java:1 — x\n- [Minor] b.java:2 — y\n- [Important] c.java:3 — z"})),
    ("a4", 2, "USAGE_REPORTED", usage(10, 1, 0.1))])
db.commit()
db.close()
json.dump([{"name": "T-02", "state": "SUCCEEDED", "seconds": 60, "retries": 0}], open(os.path.join(run, "steps.json"), "w"))
docs = os.path.join(run, "docs", "features", "comments")
os.makedirs(docs)
open(os.path.join(docs, "02-spec.md"), "w", encoding="utf-8").write(
    "- AC-1: Given a, When b, Then 201\n- AC-2: Given x, When y, Then 404 lỗi Problem\n- **AC-3:** Given p, When q, Then 400\n")
open(os.path.join(docs, "tasks.json"), "w").write('[{"id":"T-01"},{"id":"T-02"}]')
m = metrics.collect(run)
check(m["completed"] is True, m)
check(m["nodes"]["build"]["loops"] == 1 and m["nodes"]["build"]["attemptsFailed"] == {"SCOPE_VIOLATION": 1}, m["nodes"]["build"])
check(m["nodes"]["gate1"]["firstPass"] == {"passed": 0, "total": 1}, m["nodes"]["gate1"])
check(abs(m["totals"]["costUsd"] - 0.85) < 1e-9 and m["totals"]["inputTokens"] == 160, m["totals"])
check(m["review"]["findings"] == {"Minor": 2, "Important": 1} and m["review"]["outcomes"] == {"approved": 1}, m["review"])
check(m["docs"]["acCount"] == 3 and m["docs"]["acWithErrorPath"] == 2 and m["docs"]["taskCount"] == 2, m["docs"])
shutil_run = os.path.join(root, "B")
subprocess.run(["cp", "-R", os.path.join(root, "A"), shutil_run], check=True)
out = subprocess.run([sys.executable, os.path.join(SCRIPTS, "kit-metrics.py"), "compare", os.path.join(root, "A"), shutil_run],
                     capture_output=True, text=True)
check(out.returncode == 0 and "| build |" in out.stdout and "finding Minor" in out.stdout, out.stdout + out.stderr)
# 6) blind/unblind
blind_dir = os.path.join(root, "blind")
out = subprocess.run([sys.executable, os.path.join(SCRIPTS, "kit-metrics.py"), "blind", os.path.join(root, "A"), shutil_run, "--out", blind_dir],
                     capture_output=True, text=True)
check(out.returncode == 0 and os.path.isfile(os.path.join(blind_dir, "key.json")), out.stdout + out.stderr)
key = json.load(open(os.path.join(blind_dir, "key.json")))
check(sorted(v["label"] for v in key.values()) == ["A", "B"], key)
check(all(os.path.basename(k) == k and not any(v["label"] in k for v in key.values()) for k in key), "tên file không được chứa nhãn")
print("ok  kit-metrics: collect, compare, blind")
