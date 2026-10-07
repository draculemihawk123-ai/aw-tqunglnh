#!/usr/bin/env python3
"""Số đo của một lần chạy golden workload (kit-bench.sh): thu thập, so sánh hai nhãn, chấm tài liệu không biết nhãn.

Cách dùng:
    kit-metrics.py collect <thư mục lượt chạy>            # đọc install/aw.db + steps.json (+ docs/), ghi metrics.json
    kit-metrics.py compare <thư mục nhãn A> <thư mục nhãn B> # mỗi thư mục nhãn chứa run-1/, run-2/… đã có metrics.json
    kit-metrics.py blind <thư mục nhãn>... --out THƯ_MỤC     # xuất tài liệu với mã ngẫu nhiên để chấm, ghi key.json riêng
    kit-metrics.py unblind <key.json> <điểm.csv>             # ghép điểm đã chấm với nhãn, in trung bình theo nhãn và loại tài liệu
    kit-metrics.py lessons-input <thư mục lượt chạy>...      # tóm tắt dữ liệu thật để gửi cho wf-retro (V10-16): in Markdown ra stdout

Chỉ số theo node (cộng trên mọi WorkItem của lượt chạy):
    activations   số lần node được kích hoạt;   loops   số lần kích hoạt lặp lại (vòng sửa: iteration > 0)
    firstPass     với node kiểm tra (gate1, quality…): bao nhiêu WorkItem qua ngay lần đầu
    attemptsFailed theo mã lỗi (SCOPE_VIOLATION, VALIDATION_FAILED…);   needsInfo   số lần agent chọn needs_info
    inputTokens, outputTokens, costUsd   cộng trên các attempt của agent (provider tự báo)
Đọc thẳng database của bản cài tạm (chỉ đọc) như `agent-log.py`: đây là chi tiết cài đặt của Alpha, không phải API công khai.
"""
import argparse
import csv
import glob
import json
import os
import random
import re
import sqlite3
import sys

sys.dont_write_bytecode = True
GATE_KEYS = {"gate1", "quality", "test", "conformance", "lint", "compat", "check-docs"}
SEVERITY = re.compile(r"\[(Critical|Important|Minor|High|Medium|Low)\]")
ERROR_PATH = re.compile(r"(?i)\b(4\d\d|5\d\d)\b|lỗi|không hợp lệ|rỗng|thiếu|không tồn tại|invalid|error|not found")
DOC_TYPES = {"01-brainstorm.md": "brainstorm", "02-spec.md": "spec", "03-design.md": "design"}


def rows(db, sql, *params):
    cursor = db.execute(sql, params)
    names = [c[0] for c in cursor.description]
    return [dict(zip(names, r)) for r in cursor.fetchall()]


def doc_metrics(docs_dir):
    out = {}
    spec = os.path.join(docs_dir, "02-spec.md")
    if os.path.isfile(spec):
        criteria = [l for l in open(spec, encoding="utf-8").read().splitlines() if re.match(r"^\s*[-*] \*{0,2}AC-\d+", l)]
        out["acCount"] = len(criteria)
        out["acWithErrorPath"] = sum(1 for l in criteria if ERROR_PATH.search(l))
    tasks = os.path.join(docs_dir, "tasks.json")
    if os.path.isfile(tasks):
        try:
            out["taskCount"] = len(json.load(open(tasks, encoding="utf-8")))
        except (OSError, json.JSONDecodeError):
            out["taskCount"] = -1
    for name in DOC_TYPES:
        path = os.path.join(docs_dir, name)
        if os.path.isfile(path):
            out[DOC_TYPES[name] + "Bytes"] = os.path.getsize(path)
    return out


def collect(run_dir):
    db_path = os.path.join(run_dir, "install", "aw.db")
    if not os.path.isfile(db_path):
        sys.exit(f"kit-metrics: không thấy {db_path}")
    db = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    steps_file = os.path.join(run_dir, "steps.json")
    steps = json.load(open(steps_file, encoding="utf-8")) if os.path.isfile(steps_file) else []
    nodes = {}

    def node(key):
        return nodes.setdefault(key, {"activations": 0, "loops": 0, "outcomes": {}, "firstPass": {"passed": 0, "total": 0},
                                      "attempts": 0, "attemptsFailed": {}, "needsInfo": 0,
                                      "inputTokens": 0, "outputTokens": 0, "costUsd": 0.0})
    first_seen = set()
    for r in rows(db, """select n.node_key, n.iteration, n.selected_outcome, n.activation_sequence, w.work_item_id
                         from node_runs n join workflow_runs w on w.id = n.run_id order by w.work_item_id, n.activation_sequence"""):
        item = node(r["node_key"])
        item["activations"] += 1
        if r["iteration"] > 0:
            item["loops"] += 1
        outcome = r["selected_outcome"] or "-"
        item["outcomes"][outcome] = item["outcomes"].get(outcome, 0) + 1
        if outcome == "needs_info":
            item["needsInfo"] += 1
        key = (r["work_item_id"], r["node_key"])
        if key not in first_seen:
            first_seen.add(key)
            if r["node_key"] in GATE_KEYS:
                item["firstPass"]["total"] += 1
                item["firstPass"]["passed"] += 1 if outcome == "passed" else 0
    findings, review_outcomes, review_text = {}, {}, ""
    for a in rows(db, """select a.id, a.state, a.failure_code, a.provider_key, n.node_key, n.selected_outcome
                         from execution_attempts a join node_runs n on n.id = a.node_run_id"""):
        item = node(a["node_key"])
        item["attempts"] += 1
        if a["state"] != "SUCCEEDED":
            code = a["failure_code"] or a["state"]
            item["attemptsFailed"][code] = item["attemptsFailed"].get(code, 0) + 1
        if not a["provider_key"]:
            continue
        message = ""
        for e in rows(db, "select kind, payload_json from agent_events where attempt_id = ? order by sequence", a["id"]):
            payload = json.loads(e["payload_json"])
            if e["kind"] == "USAGE_REPORTED":
                usage = payload.get("usage") or {}
                item["inputTokens"] += usage.get("InputTokens", 0) + usage.get("CachedInputTokens", 0)
                item["outputTokens"] += usage.get("OutputTokens", 0)
                item["costUsd"] += usage.get("CostUSD", 0.0)
            elif e["kind"] == "ASSISTANT_MESSAGE" and payload.get("message", "").strip():
                message = payload["message"]
        if a["node_key"] in ("ai-review", "review") and a["state"] == "SUCCEEDED":
            for label in SEVERITY.findall(message):
                findings[label] = findings.get(label, 0) + 1
            review_outcomes[a["selected_outcome"] or "-"] = review_outcomes.get(a["selected_outcome"] or "-", 0) + 1
            review_text = message
    for item in nodes.values():
        item["costUsd"] = round(item["costUsd"], 4)
    docs = doc_metrics(os.path.join(run_dir, "docs", "features", "comments")) if os.path.isdir(os.path.join(run_dir, "docs")) else {}
    totals = {k: round(sum(n[k] for n in nodes.values()), 4) for k in ("activations", "loops", "attempts", "needsInfo",
                                                                          "inputTokens", "outputTokens", "costUsd")}
    totals["attemptsFailed"] = sum(sum(n["attemptsFailed"].values()) for n in nodes.values())
    totals["seconds"] = round(sum(s.get("seconds", 0) for s in steps), 1)
    completed = bool(steps) and all(s.get("state") == "SUCCEEDED" for s in steps)
    metrics = {"run": os.path.basename(run_dir.rstrip("/")), "completed": completed,
               "steps": [{k: s.get(k) for k in ("name", "state", "retries", "seconds", "stopped")} for s in steps],
               "nodes": dict(sorted(nodes.items())), "totals": totals,
               "review": {"findings": findings, "outcomes": review_outcomes, "chars": len(review_text)}, "docs": docs}
    with open(os.path.join(run_dir, "metrics.json"), "w", encoding="utf-8") as f:
        json.dump(metrics, f, ensure_ascii=False, indent=1)
        f.write("\n")
    return metrics


def lessons_input(run_dirs):
    """Markdown cho wf-retro: mỗi lượt chạy một mục gồm vòng sửa theo node, kiểm tra không qua ngay lần đầu, mã lỗi attempt
    và nguyên văn nhận xét của reviewer. Dữ liệu đọc thẳng từ database của lượt chạy (chỉ đọc); không suy diễn thêm."""
    out = ["# Dữ liệu cho rút bài học", "", "Mỗi mục là một lượt chạy. Đây là toàn bộ dữ liệu; thiếu gì thì ghi \"không có dữ liệu\".", ""]
    for run_dir in run_dirs:
        db_path = os.path.join(run_dir, "install", "aw.db")
        if not os.path.isfile(db_path):
            sys.exit(f"kit-metrics: không thấy {db_path}")
        db = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
        name = os.path.join(os.path.basename(os.path.dirname(run_dir.rstrip("/"))), os.path.basename(run_dir.rstrip("/")))
        out += [f"## Lượt chạy {name}", ""]
        loops, first, failed_attempts = {}, {}, {}
        seen = set()
        for r in rows(db, """select n.node_key, n.iteration, n.selected_outcome, w.work_item_id from node_runs n
                             join workflow_runs w on w.id = n.run_id order by w.work_item_id, n.activation_sequence"""):
            if r["iteration"] > 0:
                loops[r["node_key"]] = loops.get(r["node_key"], 0) + 1
            if r["node_key"] in GATE_KEYS and (r["work_item_id"], r["node_key"]) not in seen:
                seen.add((r["work_item_id"], r["node_key"]))
                if r["selected_outcome"] != "passed":
                    first[r["node_key"]] = first.get(r["node_key"], 0) + 1
        for a in rows(db, """select a.state, a.failure_code, n.node_key from execution_attempts a
                             join node_runs n on n.id = a.node_run_id where a.state != 'SUCCEEDED'"""):
            key = f"{a['node_key']}: {a['failure_code'] or a['state']}"
            failed_attempts[key] = failed_attempts.get(key, 0) + 1
        out.append("- Vòng sửa (kích hoạt lặp) theo node: " + (", ".join(f"{k} {v}" for k, v in sorted(loops.items())) or "không có"))
        out.append("- Kiểm tra không qua ngay lần đầu: " + (", ".join(f"{k} {v}" for k, v in sorted(first.items())) or "không có"))
        out.append("- Attempt hỏng: " + (", ".join(f"{k} ×{v}" for k, v in sorted(failed_attempts.items())) or "không có"))
        message = ""
        for a in rows(db, """select a.id from execution_attempts a join node_runs n on n.id = a.node_run_id
                             where n.node_key in ('ai-review', 'review') and a.state = 'SUCCEEDED' order by a.rowid desc limit 1"""):
            for e in rows(db, "select kind, payload_json from agent_events where attempt_id = ? order by sequence", a["id"]):
                payload = json.loads(e["payload_json"])
                if e["kind"] == "ASSISTANT_MESSAGE" and payload.get("message", "").strip():
                    message = payload["message"]
        out += ["", "Nhận xét của reviewer độc lập (nguyên văn):" if message else "Không có nhận xét của reviewer.", ""]
        if message:
            out += ["~~~", message.strip(), "~~~", ""]
    print("\n".join(out))


def load_label(label_dir):
    runs = []
    for path in sorted(glob.glob(os.path.join(label_dir, "run-*", "metrics.json"))):
        runs.append(json.load(open(path, encoding="utf-8")))
    if not runs:
        sys.exit(f"kit-metrics: {label_dir} không có run-*/metrics.json")
    return runs


def mean(values):
    return sum(values) / len(values) if values else 0.0


def compare(dir_a, dir_b):
    a, b = load_label(dir_a), load_label(dir_b)
    names = (os.path.basename(dir_a.rstrip("/")), os.path.basename(dir_b.rstrip("/")))
    print(f"# So sánh {names[0]} (n={len(a)}) và {names[1]} (n={len(b)})\n")
    print("Mỗi ô là trung bình trên các lượt; n nhỏ nên chỉ tin khác biệt lớn.\n")
    print(f"Lượt hoàn tất: {names[0]} {sum(r['completed'] for r in a)}/{len(a)}, {names[1]} {sum(r['completed'] for r in b)}/{len(b)}\n")

    def avg(runs, path):
        out = []
        for r in runs:
            v = r
            for part in path:
                v = v.get(part, 0) if isinstance(v, dict) else 0
            out.append(v if isinstance(v, (int, float)) else 0)
        return mean(out)
    print("## Tổng\n\n| chỉ số | " + names[0] + " | " + names[1] + " |\n|---|---|---|")
    for label, key in (("kích hoạt node", "activations"), ("vòng sửa (kích hoạt lặp)", "loops"), ("attempt", "attempts"),
                       ("attempt hỏng", "attemptsFailed"), ("needs_info", "needsInfo"), ("token vào", "inputTokens"),
                       ("token ra", "outputTokens"), ("chi phí USD", "costUsd"), ("giây", "seconds")):
        print(f"| {label} | {avg(a, ['totals', key]):.2f} | {avg(b, ['totals', key]):.2f} |")
    print("\n## Theo node\n\n| node | loops A | loops B | qua ngay lần đầu A | B | attempt hỏng A | B | USD A | B |\n|---|---|---|---|---|---|---|---|---|")
    keys = sorted({k for r in a + b for k in r["nodes"]})
    for key in keys:
        def fp(runs):
            passed = sum(r["nodes"].get(key, {}).get("firstPass", {}).get("passed", 0) for r in runs)
            total = sum(r["nodes"].get(key, {}).get("firstPass", {}).get("total", 0) for r in runs)
            return f"{passed}/{total}" if total else "-"

        def failed(runs):
            return mean([sum(r["nodes"].get(key, {}).get("attemptsFailed", {}).values()) for r in runs])
        print(f"| {key} | {avg(a, ['nodes', key, 'loops']):.2f} | {avg(b, ['nodes', key, 'loops']):.2f} | {fp(a)} | {fp(b)} | "
              f"{failed(a):.2f} | {failed(b):.2f} | {avg(a, ['nodes', key, 'costUsd']):.3f} | {avg(b, ['nodes', key, 'costUsd']):.3f} |")
    print("\n## Review độc lập và tài liệu\n\n| chỉ số | " + names[0] + " | " + names[1] + " |\n|---|---|---|")
    for label in sorted({k for r in a + b for k in r["review"]["findings"]}):
        print(f"| finding {label} | {mean([r['review']['findings'].get(label, 0) for r in a]):.2f} | {mean([r['review']['findings'].get(label, 0) for r in b]):.2f} |")
    for label, key in (("AC trong spec", "acCount"), ("AC có đường lỗi", "acWithErrorPath"), ("số task trong tasks.json", "taskCount")):
        print(f"| {label} | {avg(a, ['docs', key]):.2f} | {avg(b, ['docs', key]):.2f} |")


def blind(label_dirs, out_dir, seed):
    """Xuất mọi tài liệu BRAINSTORM, SPEC, DESIGN, PLAN và SYNC với mã ngẫu nhiên; key.json giữ ánh xạ, đừng đưa cho người chấm."""
    rng = random.Random(seed)
    items = []
    for label_dir in label_dirs:
        label = os.path.basename(label_dir.rstrip("/"))
        for run in sorted(glob.glob(os.path.join(label_dir, "run-*"))):
            base = os.path.join(run, "docs", "features", "comments")
            patterns = {"brainstorm": "01-brainstorm.md", "spec": "02-spec.md", "design": "03-design.md"}
            for kind, name in patterns.items():
                if os.path.isfile(os.path.join(base, name)):
                    items.append((label, os.path.basename(run), kind, os.path.join(base, name)))
            for path in sorted(glob.glob(os.path.join(base, "tasks", "*", "plan.md"))):
                items.append((label, os.path.basename(run), "plan", path))
    if not items:
        sys.exit("kit-metrics: không có tài liệu nào để chấm (chạy kit-bench.sh trước)")
    rng.shuffle(items)
    os.makedirs(out_dir, exist_ok=True)
    key, sheet = {}, []
    for index, (label, run, kind, path) in enumerate(items, 1):
        code = f"{kind}-{index:03d}"
        with open(path, encoding="utf-8") as src, open(os.path.join(out_dir, code + ".md"), "w", encoding="utf-8") as dst:
            dst.write(src.read())
        key[code] = {"label": label, "run": run, "kind": kind}
        sheet.append([code, kind, ""])
    with open(os.path.join(out_dir, "key.json"), "w", encoding="utf-8") as f:
        json.dump(key, f, ensure_ascii=False, indent=1)
    with open(os.path.join(out_dir, "diem.csv"), "w", encoding="utf-8", newline="") as f:
        writer = csv.writer(f)
        writer.writerow(["ma", "loai", "diem"])
        writer.writerows(sheet)
    print(f"{len(items)} tài liệu → {out_dir} (chấm cột `diem` trong diem.csv theo kit/bench/rubric.md, thang 1 đến 5; key.json để sau)")


def unblind(key_file, scores_file):
    key = json.load(open(key_file, encoding="utf-8"))
    table = {}
    with open(scores_file, encoding="utf-8", newline="") as f:
        for row in csv.DictReader(f):
            if not row["diem"].strip():
                continue
            info = key[row["ma"]]
            table.setdefault((info["label"], info["kind"]), []).append(float(row["diem"]))
    print("| nhãn | loại | số tài liệu | điểm trung bình |\n|---|---|---|---|")
    for (label, kind), values in sorted(table.items()):
        print(f"| {label} | {kind} | {len(values)} | {mean(values):.2f} |")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("collect").add_argument("run_dir")
    c = sub.add_parser("compare")
    c.add_argument("label_a")
    c.add_argument("label_b")
    b = sub.add_parser("blind")
    b.add_argument("label_dirs", nargs="+")
    b.add_argument("--out", required=True)
    b.add_argument("--seed", type=int, default=20261007)
    u = sub.add_parser("unblind")
    u.add_argument("key_file")
    u.add_argument("scores_file")
    li = sub.add_parser("lessons-input")
    li.add_argument("run_dirs", nargs="+")
    args = parser.parse_args()
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8")
    if args.command == "collect":
        metrics = collect(args.run_dir)
        print(f"{metrics['run']}: {metrics['totals']['activations']} kích hoạt node, {metrics['totals']['loops']} vòng sửa, "
              f"{metrics['totals']['attemptsFailed']} attempt hỏng, {metrics['totals']['costUsd']} USD → {args.run_dir}/metrics.json")
    elif args.command == "compare":
        compare(args.label_a, args.label_b)
    elif args.command == "blind":
        blind(args.label_dirs, args.out, args.seed)
    elif args.command == "lessons-input":
        lessons_input(args.run_dirs)
    else:
        unblind(args.key_file, args.scores_file)


if __name__ == "__main__":
    main()
