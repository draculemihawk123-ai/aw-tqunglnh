#!/usr/bin/env python3
"""Kiểm hành vi thật của bộ bọc .cmd của Windows (V11-09): mọi script kit/commands chạy qua `cmd.exe /c <file>.cmd` phải cho đúng mã
thoát và đúng thông báo như `sh <file>.sh`, kể cả khi nội dung có tiếng Việt, có dấu cách trong đường dẫn và có đối số.

Chỉ chạy trên Windows có Git for Windows; nơi khác in "skip" và thoát mã 0 (kit/tests/check-kit.sh gọi file này).
Cách dùng: python kit/tests/test-windows-launcher.py
"""
import importlib.util
import os
import re
import shutil
import subprocess
import sys
import tempfile

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
KIT = os.path.dirname(HERE)
failed = 0


def check(name, ok, detail=""):
    global failed
    print(("  ok    " if ok else "  HỎNG  ") + name + ("" if ok else f": {detail}"))
    if not ok:
        failed += 1


def norm(data):
    return data.decode("utf-8", "replace").replace("\r\n", "\n")


def main():
    if os.name != "nt":
        print("  skip  bộ bọc .cmd chỉ kiểm trên Windows")
        return 0
    git = shutil.which("git")
    if not git:
        print("  skip  không có Git for Windows")
        return 0
    spec = importlib.util.spec_from_file_location("aw_publish", os.path.join(KIT, "scripts", "aw-publish.py"))
    publish = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(publish)
    sh = os.path.normpath(os.path.join(os.path.dirname(os.path.realpath(git)), "..", "..", "bin", "sh.exe"))
    exec_path = subprocess.run([git, "--exec-path"], capture_output=True, text=True).stdout.strip()
    sh = os.path.normpath(os.path.join(exec_path, "..", "..", "..", "bin", "sh.exe"))
    if not os.path.isfile(sh):
        print(f"  skip  không tìm thấy sh.exe của Git for Windows ({sh})")
        return 0

    manifest = publish.load_manifest(os.path.join(KIT, "kit.json"))
    unix = {r["key"]: r["instruction"] for r in publish.script_skill_doc(manifest, manifest["scriptSkills"][0], False)["resources"]}
    win = {r["key"]: r["instruction"] for r in publish.script_skill_doc(manifest, manifest["scriptSkills"][0], True)["resources"]}

    tmp = tempfile.mkdtemp(prefix="aw launcher ")  # có dấu cách: đường dẫn thật của người dùng hay có
    try:
        work = os.path.join(tmp, "work dir")
        os.makedirs(work)
        subprocess.run([git, "init", "-q", "-b", "main", work], check=True)
        subprocess.run([git, "-C", work, "config", "user.email", "t@example.invalid"], check=True)
        subprocess.run([git, "-C", work, "config", "user.name", "t"], check=True)
        open(os.path.join(work, "x.txt"), "w").write("x\n")
        subprocess.run([git, "-C", work, "add", "-A"], check=True)
        subprocess.run([git, "-C", work, "commit", "-q", "-m", "init"], check=True)

        def run_pair(name, unix_body, win_body, args=()):
            """Chạy bản .sh bằng sh.exe và bản .cmd bằng cmd.exe /c; trả về (mã, stdout, stderr) của mỗi bản."""
            sh_path = os.path.join(tmp, name + ".sh")
            cmd_path = os.path.join(tmp, name + ".cmd")
            open(sh_path, "wb").write(unix_body.encode("utf-8"))
            open(cmd_path, "wb").write(win_body.encode("utf-8"))
            env = {k: v for k, v in os.environ.items() if k != "AW_KIT"}
            a = subprocess.run([sh, sh_path, *args], capture_output=True, cwd=work, env=env)
            b = subprocess.run(["cmd.exe", "/c", cmd_path, *args], capture_output=True, cwd=work, env=env)

            def view(done, path):
                # the script's own path appears in some shell messages ($0); the two files differ only by extension
                # and the launcher header shifts shell line numbers by its own length
                scrub = lambda data: re.sub(r"line \d+", "line N", norm(data).replace(path, "<script>"))
                return (done.returncode, scrub(done.stdout), scrub(done.stderr))
            return view(a, sh_path), view(b, cmd_path)

        # 1. script tổng hợp: mã thoát, stdout/stderr, tiếng Việt, đối số
        body_vi = "#!/bin/sh\n# ghi chú tiếng Việt: kiểm tra, lỗi, không đạt\necho \"Chạy thử: xin chào $1\"\necho \"lỗi giả\" >&2\nexit %d\n"
        for code in (0, 1, 3):
            text = body_vi % code
            ub, wb = text, publish.WINDOWS_LAUNCHER.replace("\n", "\r\n") + text
            expected, got = run_pair(f"synthetic-{code}", ub, wb, ["đối-số"])
            check(f"script tổng hợp thoát mã {code}: .cmd cho cùng mã và cùng thông báo như .sh", expected == got and got[0] == code, f"sh={expected} cmd={got}")
        ascii_text = "#!/bin/sh\n# only ascii\necho ascii-ok\nexit 5\n"
        expected, got = run_pair("synthetic-ascii", ascii_text, publish.WINDOWS_LAUNCHER.replace("\n", "\r\n") + ascii_text)
        check("script ASCII thoát mã 5 qua .cmd", expected == got and got[0] == 5, f"sh={expected} cmd={got}")

        # 2. mọi script của kit, chạy trong một repository git trống (hầu hết thoát sớm với thông báo xác định): cùng mã, cùng thông báo
        scripts = sorted(k for k in unix if k.endswith(".sh"))
        check("kit có script để kiểm", len(scripts) >= 10, scripts)
        for key in scripts:
            expected, got = run_pair("kit-" + key[:-3], unix[key], win[key[:-3] + ".cmd"])
            check(f"{key}: .cmd cho cùng mã thoát ({expected[0]}) và cùng thông báo như .sh", expected == got, f"sh={expected!r} cmd={got!r}")
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    print(f"\n{'Tất cả đạt' if not failed else str(failed) + ' kiểm tra hỏng'}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
