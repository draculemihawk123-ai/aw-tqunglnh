#!/usr/bin/env python3
"""Kiểm lessons-to-skill.py (V10-16): định dạng, gộp và tăng revision, sửa aw-project.json, và — nếu có `aw` — publish ra version mới
khi bài học đổi, giữ nguyên version khi không đổi. In từng dòng "ok"/"HỎNG", thoát mã 1 nếu có HỎNG. Gọi từ check-kit.sh."""
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
KIT = os.path.dirname(HERE)
SCRIPTS = os.path.join(KIT, "scripts")
spec = importlib.util.spec_from_file_location("context_check", os.path.join(SCRIPTS, "context-check.py"))
cc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cc)
LESSONS = os.path.join(KIT, "bench", "lessons", "2026-10-07-A0-A1.md")
failed = 0


def check(name, condition, detail=""):
    global failed
    print(("  ok    " if condition else "  HỎNG  ") + name + ("" if condition else f": {detail}"))
    failed += 0 if condition else 1


def run(*args, cwd=None, env=None):
    done = subprocess.run([sys.executable, *args], capture_output=True, text=True, cwd=cwd, env=env)
    return done.returncode, done.stdout + done.stderr


def shell(*args, cwd=None, env=None):
    done = subprocess.run(list(args), capture_output=True, text=True, cwd=cwd, env=env)
    return done.returncode, done.stdout + done.stderr


def tool(*args, **kw):
    return run(os.path.join(SCRIPTS, "lessons-to-skill.py"), *args, **kw)


def lesson(title="Một bài học", rule="Luật ngắn gọn.", level="GUIDANCE", agents="agent-flow-build", extra=""):
    return (f"# Bài học\n\n### L-1: {title}\n- Bằng chứng: x\n- Số lần lặp: 2\n- Luật đề xuất: {rule}\n- Gắn vào: {agents}\n- Mức: {level}\n{extra}")


def main():
    tmp = tempfile.mkdtemp(prefix="test-lessons-")
    worker = None
    try:
        skill = os.path.join(tmp, "skill.json")
        # 1. định dạng
        for name, text, needle in [
            ("thiếu trường Mức", lesson().replace("- Mức: GUIDANCE\n", ""), "thiếu \"Mức\""),
            ("Mức không hợp lệ", lesson(level="QUAN_TRONG"), "không hợp lệ"),
            ("luật quá dài", lesson(rule="x" * 501), "tối đa 500"),
        ]:
            path = os.path.join(tmp, "bad.md")
            open(path, "w", encoding="utf-8").write(text)
            code, out = tool(path, "--skill", skill)
            check(f"lessons-to-skill: {name} bị chặn", code != 0 and needle in out, out)
        # 2. gộp, revision
        path = os.path.join(tmp, "l.md")
        open(path, "w", encoding="utf-8").write(lesson())
        code, out = tool(path, "--skill", skill)
        doc = json.load(open(skill, encoding="utf-8"))
        res = doc["resources"][0]
        check("lessons-to-skill: tạo resource lesson.<tiêu đề> ở v1", code == 0 and res["key"] == "lesson.mot-bai-hoc" and res["provenance"]["revision"] == "v1"
              and res["global"] is True and res["priority"] == "GUIDANCE", out)
        code, out = tool(path, "--skill", skill)
        check("lessons-to-skill: chạy lại không đổi gì thì giữ revision", "không đổi 1" in out and json.load(open(skill, encoding="utf-8"))["resources"][0]["provenance"]["revision"] == "v1", out)
        open(path, "w", encoding="utf-8").write(lesson(rule="Luật đã đổi."))
        code, out = tool(path, "--skill", skill)
        res = json.load(open(skill, encoding="utf-8"))["resources"][0]
        check("lessons-to-skill: nội dung đổi thì revision tăng v2", "cập nhật 1" in out and res["provenance"]["revision"] == "v2" and res["instruction"] == "Luật đã đổi.", out)
        open(path, "w", encoding="utf-8").write(lesson(extra="- Trạng thái: bỏ\n"))
        code, out = tool(path, "--skill", skill)
        check("lessons-to-skill: bài học đánh dấu bỏ không được lấy", code != 0 and "không có bài học nào còn hiệu lực" in out, out)

        # 3. aw-project.json của hướng dẫn issue tracker (bản sao)
        mirror = os.path.join(tmp, "mirror")
        os.makedirs(os.path.join(mirror, "docs", "guides"))
        os.symlink(KIT, os.path.join(mirror, "kit"))
        guide = os.path.join(mirror, "docs", "guides", "issue-tracker")
        shutil.copytree(os.path.join(os.path.dirname(KIT), "docs", "guides", "issue-tracker"), guide)
        manifest = os.path.join(guide, "aw-project.json")
        out_skill = os.path.join(guide, "definitions", "skills", "skill-project-lessons.json")
        code, out = tool(LESSONS, "--skill", out_skill, "--manifest", manifest, "--owner", "tracker")
        check("lessons-to-skill --manifest: ghi skill và sửa aw-project.json", code == 0 and os.path.isfile(out_skill), out)
        code, out = run(os.path.join(SCRIPTS, "aw-publish.py"), manifest, "--check")
        check("aw-publish --check hợp lệ sau khi gắn bài học vào agent", code == 0 and "hợp lệ" in out, out)
        text = open(manifest, encoding="utf-8").read()
        check("aw-project.json: skill đã khai báo, bài học gắn đúng agent, không trùng", text.count('"skill-project-lessons"') == 1
              and text.count("skill-project-lessons#lesson.") == 2, text.count("skill-project-lessons"))
        before = text
        tool(LESSONS, "--skill", out_skill, "--manifest", manifest, "--owner", "tracker")
        check("lessons-to-skill --manifest: chạy lại không đổi aw-project.json", open(manifest, encoding="utf-8").read() == before)
        bad = lesson(agents="agent-khong-co")
        open(path, "w", encoding="utf-8").write(bad)
        code, out = tool(path, "--skill", out_skill, "--manifest", manifest)
        check("lessons-to-skill --manifest: agent không có trong project bị báo", code != 0 and "agent-khong-co" in out, out)
        check("aw-project.json không bị sửa khi có lỗi", open(manifest, encoding="utf-8").read() == before)

        # 4. publish ra version mới (cần aw)
        try:
            aw = cc.find_tool("AW", "aw", "./cmd/aw", tmp)
            fake = cc.find_tool("AW_FAKE_CLAUDE", "fake-claude", "./cmd/fake-claude", tmp)
        except SystemExit:
            print("  bỏ qua publish thật (không có aw)")
            return
        kitcopy = os.path.join(tmp, "k")
        os.makedirs(os.path.join(kitcopy, "kit", "tests"))
        for entry in os.listdir(KIT):
            if entry not in ("tests", "bench"):
                os.symlink(os.path.join(KIT, entry), os.path.join(kitcopy, "kit", entry))
        shutil.copytree(os.path.join(HERE, "walk"), os.path.join(kitcopy, "kit", "tests", "walk"))
        wm = os.path.join(kitcopy, "kit", "tests", "walk", "aw-project.json")
        wskill = os.path.join(kitcopy, "kit", "tests", "walk", "definitions", "skills", "skill-project-lessons.json")
        repo = os.path.join(tmp, "repos", "repo")
        cc.make_repository(repo, set())
        install = os.path.join(tmp, "install")
        env = dict(os.environ, AW=aw, AW_DB=os.path.join(install, "aw.db"), AW_ARTIFACT_ROOT=os.path.join(install, "artifacts"),
                   AW_WORKSPACE_ROOT=os.path.join(install, "workspaces"), AW_CLAUDE_EXECUTABLE=fake, AW_STATE=os.path.join(tmp, "aw-state.json"),
                   AW_KIT=KIT, PATH=SCRIPTS + os.pathsep + os.environ.get("PATH", ""))
        os.makedirs(install)
        os.makedirs(env["AW_ARTIFACT_ROOT"])
        os.makedirs(env["AW_WORKSPACE_ROOT"])
        log = open(os.path.join(tmp, "worker.log"), "w")
        worker = subprocess.Popen([aw, "worker", "--db", env["AW_DB"], "--artifact-root", env["AW_ARTIFACT_ROOT"], "--workspace-root", env["AW_WORKSPACE_ROOT"],
                                   "--claude-executable", fake, "--env-allowlist", "PATH,HOME", "--claude-permission-mode", "acceptEdits"],
                                  env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        time.sleep(1.0)
        code, out = shell(os.path.join(SCRIPTS, "init-project.sh"), "lessons", "repo", repo, cwd=tmp, env=env)
        if code != 0:
            check("dựng project thử để publish", False, out)
            return

        def publish():
            c, o = run(os.path.join(SCRIPTS, "aw-publish.py"), wm, cwd=tmp, env=env)
            state = json.load(open(env["AW_STATE"], encoding="utf-8"))
            return c, o, state["definitions"]["SKILL"].get("skill-project-lessons", {}).get("versionId")

        one = os.path.join(tmp, "one.md")
        open(one, "w", encoding="utf-8").write(lesson(agents="agent-flow-build"))
        tool(one, "--skill", wskill, "--manifest", wm)
        c1, o1, v1 = publish()
        check("publish lần 1: skill-project-lessons có version", c1 == 0 and v1, o1)
        c2, o2, v2 = publish()
        check("publish lần 2 không đổi bài học: giữ nguyên version", c2 == 0 and v2 == v1, f"{v1} → {v2} {o2}")
        open(one, "w", encoding="utf-8").write(lesson(rule="Luật đã đổi sau khi duyệt.", agents="agent-flow-build"))
        tool(one, "--skill", wskill, "--manifest", wm)
        c3, o3, v3 = publish()
        check("publish sau khi bài học đổi: ra version mới", c3 == 0 and v3 and v3 != v1, f"{v1} → {v3} {o3}")
    finally:
        if worker is not None:
            try:
                os.killpg(worker.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
        shutil.rmtree(tmp, ignore_errors=True)
    return


if __name__ == "__main__":
    main()
    sys.exit(1 if failed else 0)
