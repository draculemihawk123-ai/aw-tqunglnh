#!/usr/bin/env python3
"""Kiểm chấp nhận ĐỘC LẬP cho mã cuối của một lượt bench (bình luận trên issue): dựng backend từ nhánh T-02, khởi động thật, gọi HTTP
theo các hành vi mà ý tưởng gốc buộc phải có (không dùng test của chính lượt đó), rồi chạy test và build của frontend từ nhánh T-03.

Cách dùng:  code-accept.py <thư mục lượt chạy, ví dụ bench2/B3/run-1> [--skip-web] [--skip-api] [--json]
Cần: git, java, mvn, node, npm. Mỗi kiểm tra trả về đạt / hỏng; lỗi 5xx ở bất kỳ đâu là lỗi cứng. Giới hạn độ dài lấy từ hợp đồng
(spec/openapi.json trên nhánh T-01) của chính lượt đó, nên không đoán trước con số; thiếu thì dùng 100 và 2000.
Thư mục `backend/data` được tạo sẵn (bench B3 phát hiện worktree mới không có thư mục này nên mọi test nạp context đỏ).
"""
import argparse
import concurrent.futures
import json
import os
import re
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

sys.dont_write_bytecode = True


def sh(cmd, cwd=None, timeout=600, env=None):
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, timeout=timeout, env=env)


def export_branch(repo, workdir, name):
    refs = sh(["git", "-C", repo, "for-each-ref", "--format=%(refname:short)", "refs/heads/agentkit/"]).stdout.split()
    if not refs:
        return None
    dest = os.path.join(workdir, name)
    os.makedirs(dest)
    arc = subprocess.Popen(["git", "-C", repo, "archive", refs[0]], stdout=subprocess.PIPE)
    subprocess.run(["tar", "-x", "-C", dest], stdin=arc.stdout, check=True)
    arc.wait()
    return dest


def limits(contracts_dir):
    out = {"author": 100, "body": 2000}
    try:
        spec = json.load(open(os.path.join(contracts_dir, "spec", "openapi.json"), encoding="utf-8"))
        for name, schema in spec["components"]["schemas"].items():
            if "comment" in name.lower() and "new" in name.lower():
                for field in out:
                    out[field] = schema["properties"][field].get("maxLength", out[field])
    except Exception:
        pass
    return out


class Api:
    def __init__(self, base):
        self.base = base

    def call(self, method, path, body=None, raw=None):
        data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
        req = urllib.request.Request(self.base + path, data=data, method=method, headers={"Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=20) as resp:
                text = resp.read().decode("utf-8", "replace")
                return resp.status, (json.loads(text) if text.strip().startswith(("{", "[")) else text)
        except urllib.error.HTTPError as exc:
            text = exc.read().decode("utf-8", "replace")
            try:
                return exc.code, json.loads(text)
            except ValueError:
                return exc.code, text
        except Exception as exc:  # kết nối đứt
            return 0, str(exc)


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def comment_list(api, issue):
    status, data = api.call("GET", f"/api/issues/{issue}/comments")
    items = data if isinstance(data, list) else (data.get("items") or data.get("content") or data.get("comments") or []) if isinstance(data, dict) else []
    return status, items


def api_checks(api, lim):
    results, five = [], []

    def check(name, ok, detail=""):
        results.append({"name": name, "ok": bool(ok), "detail": "" if ok else str(detail)[:200]})

    def watch(status, label):
        if status >= 500 or status == 0:
            five.append(f"{label}: {status}")
        return status

    def mk_issue(title="accept"):
        status, data = api.call("POST", "/api/issues", {"title": title})
        watch(status, "tạo issue")
        return data.get("id") if isinstance(data, dict) else None

    issue = mk_issue()
    other = mk_issue("other")
    check("tạo được issue nền", issue and other, "không tạo được issue")
    if not (issue and other):
        return results, five
    base = f"/api/issues/{issue}/comments"
    s, c1 = api.call("POST", base, {"author": "an", "body": "một"}); watch(s, "POST hợp lệ")
    check("POST hợp lệ trả 201", s == 201, s)
    check("phản hồi POST có id, author, body", isinstance(c1, dict) and all(k in c1 for k in ("id", "author", "body")), c1)
    time.sleep(1.1)
    s, c2 = api.call("POST", base, {"author": "bình", "body": "hai"}); watch(s, "POST thứ hai")
    s, items = comment_list(api, issue); watch(s, "GET danh sách")
    check("GET trả 200 và có đủ hai bình luận", s == 200 and len(items) == 2, (s, items))
    ids = [i.get("id") for i in items if isinstance(i, dict)]
    check("danh sách theo thời gian tạo tăng dần", ids == sorted(ids) and len(ids) == 2 and c1 and c2 and ids[0] == c1.get("id"), ids)
    s, none = comment_list(api, other); watch(s, "GET issue khác")
    check("bình luận không lẫn sang issue khác", s == 200 and none == [], (s, none))
    s, _ = api.call("POST", "/api/issues/99999999/comments", {"author": "a", "body": "b"}); watch(s, "POST issue không có")
    check("POST vào issue không tồn tại trả 404", s == 404, s)
    s, _ = api.call("GET", "/api/issues/99999999/comments"); watch(s, "GET issue không có")
    check("GET issue không tồn tại trả 404", s == 404, s)
    bodies = [("thiếu body", {"author": "a"}), ("thiếu author", {"body": "b"}), ("body rỗng", {"author": "a", "body": ""}),
              ("body chỉ khoảng trắng", {"author": "a", "body": "   "}), ("body dài quá giới hạn", {"author": "a", "body": "x" * (lim["body"] + 1)}),
              ("author dài quá giới hạn", {"author": "a" * (lim["author"] + 1), "body": "b"}),
              ("body sai kiểu", {"author": "a", "body": 123}), ("author sai kiểu", {"author": 5, "body": "b"})]
    for label, payload in bodies:
        s, _ = api.call("POST", base, payload); watch(s, label)
        check(f"POST {label} bị từ chối 4xx", 400 <= s < 500, s)
    s, _ = api.call("POST", base, raw=b"{khong phai json"); watch(s, "JSON hỏng")
    check("POST JSON hỏng trả 4xx, không 5xx", 400 <= s < 500, s)
    s, _ = api.call("POST", base, {"author": "a" * lim["author"], "body": "x" * lim["body"]}); watch(s, "POST đúng biên")
    check("POST đúng giới hạn độ dài được chấp nhận", s == 201, s)
    s, _ = api.call("POST", base, {"author": "an", "body": "xin chào 👍 " + "é" * 5}); watch(s, "POST unicode")
    check("POST có emoji và dấu được chấp nhận", s == 201, s)
    s, _ = api.call("DELETE", f"/api/issues/{other}/comments/{c1.get('id') if isinstance(c1, dict) else 0}"); watch(s, "DELETE chéo issue")
    check("xóa bình luận qua issue khác trả 404", s == 404, s)
    cid = c1.get("id") if isinstance(c1, dict) else 0
    s, _ = api.call("DELETE", f"{base}/{cid}"); watch(s, "DELETE")
    check("DELETE bình luận trả 204", s == 204, s)
    s, items = comment_list(api, issue)
    check("bình luận đã xóa không còn trong danh sách", all(i.get("id") != cid for i in items if isinstance(i, dict)), items)
    s, _ = api.call("DELETE", f"{base}/{cid}"); watch(s, "DELETE lần hai")
    check("xóa lần hai trả 404", s == 404, s)
    s, _ = api.call("DELETE", f"{base}/abc"); watch(s, "DELETE id sai")
    check("DELETE id không phải số trả 4xx", 400 <= s < 500, s)

    def post(i):
        return api.call("POST", base, {"author": f"u{i}", "body": f"đồng thời {i}"})[0]
    with concurrent.futures.ThreadPoolExecutor(max_workers=10) as pool:
        codes = list(pool.map(post, range(20)))
    for code in codes:
        watch(code, "POST đồng thời")
    s, items = comment_list(api, issue)
    check("20 POST đồng thời đều thành công và đủ trong danh sách", all(c == 201 for c in codes) and len(items) >= 21, (codes, len(items)))
    s, _ = api.call("DELETE", f"/api/issues/{issue}"); watch(s, "xóa issue có bình luận")
    check("xóa issue đang có bình luận thành công (2xx)", 200 <= s < 300, s)
    s, _ = api.call("GET", base)
    check("sau khi xóa issue, GET bình luận trả 404", s == 404, s)
    after = mk_issue("sau xóa")
    check("vẫn tạo được issue mới sau khi xóa (không mồ côi/khóa)", bool(after), "không tạo được")
    return results, five


def run_api(backend, report):
    data = os.path.join(backend, "data")
    os.makedirs(data, exist_ok=True)
    built = sh(["mvn", "-q", "-B", "-DskipTests", "package"], cwd=backend, timeout=900)
    jars = [f for f in os.listdir(os.path.join(backend, "target")) if f.endswith(".jar") and not f.endswith(".original")] if os.path.isdir(os.path.join(backend, "target")) else []
    if built.returncode != 0 or not jars:
        report["api"] = {"build": False, "error": (built.stdout + built.stderr)[-600:]}
        return
    port = free_port()
    log = open(os.path.join(backend, "accept.log"), "w")
    proc = subprocess.Popen(["java", "-jar", os.path.join("target", jars[0]), f"--server.port={port}"], cwd=backend, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
    try:
        api = Api(f"http://127.0.0.1:{port}")
        deadline = time.time() + 120
        while time.time() < deadline and api.call("GET", "/api/issues")[0] == 0 and proc.poll() is None:
            time.sleep(1)
        if proc.poll() is not None or api.call("GET", "/api/issues")[0] == 0:
            report["api"] = {"build": True, "start": False, "error": open(os.path.join(backend, "accept.log"), errors="replace").read()[-600:]}
            return
        results, five = api_checks(api, report["limits"])
        report["api"] = {"build": True, "start": True, "passed": sum(r["ok"] for r in results), "total": len(results),
                         "fiveXX": five, "failed": [f"{r['name']} ({r['detail']})" for r in results if not r["ok"]]}
    finally:
        try:
            os.killpg(proc.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass


def run_web(frontend, report):
    steps = {}
    inst = sh(["npm", "ci", "--no-audit", "--no-fund", "--loglevel=error"], cwd=frontend, timeout=900)
    steps["install"] = inst.returncode == 0
    if inst.returncode == 0:
        env = dict(os.environ, CI="true", NO_COLOR="1")
        test = sh(["npm", "test", "--silent"], cwd=frontend, timeout=900, env=env)
        out = test.stdout + test.stderr
        m = re.search(r"Tests\s+(?:(\d+) failed \| )?(\d+) passed", out)
        steps["test"] = test.returncode == 0
        steps["tests"] = int(m.group(2)) if m else None
        build = sh(["npm", "run", "build", "--silent"], cwd=frontend, timeout=900, env=env)
        steps["build"] = build.returncode == 0
        if test.returncode or build.returncode:
            steps["error"] = (out + build.stdout + build.stderr)[-500:]
    report["web"] = steps


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("run")
    parser.add_argument("--skip-web", action="store_true")
    parser.add_argument("--skip-api", action="store_true")
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args()
    repos = os.path.join(os.path.abspath(args.run), "repos")
    work = tempfile.mkdtemp(prefix="code-accept-")
    report = {"run": args.run}
    try:
        contracts = export_branch(os.path.join(repos, "contracts"), work, "contracts")
        report["limits"] = limits(contracts) if contracts else {"author": 100, "body": 2000}
        if not args.skip_api:
            api_dir = export_branch(os.path.join(repos, "api"), work, "api")
            run_api(os.path.join(api_dir, "backend"), report) if api_dir else report.update(api={"build": False, "error": "không có nhánh T-02"})
        if not args.skip_web:
            web_dir = export_branch(os.path.join(repos, "web"), work, "web")
            run_web(os.path.join(web_dir, "frontend"), report) if web_dir else report.update(web={"error": "không có nhánh T-03"})
    finally:
        shutil.rmtree(work, ignore_errors=True)
    print(json.dumps(report, ensure_ascii=False, indent=1))


if __name__ == "__main__":
    main()
