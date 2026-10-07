#!/usr/bin/env python3
"""Biến các bài học đã duyệt (docs/lessons/<ngày>.md, đầu ra của wf-retro) thành resource của một skill riêng của project
(mặc định skill-project-lessons), tăng revision khi nội dung đổi, và gắn vào agent. V10-16.

Cách dùng:
    lessons-to-skill.py <file bài học>... --skill <skill-project-lessons.json> [--manifest aw-project.json] [--owner TÊN] [--dry-run]

Mỗi bài học có dạng (xem skill retro.lessons-format; wf-retro có bước check-lessons kiểm định dạng này):

    ### L-1: <tiêu đề ngắn>
    - Bằng chứng: …
    - Số lần lặp: …
    - Luật đề xuất: <luật, tối đa 500 ký tự>
    - Gắn vào: agent-flow-build, agent-reviewer
    - Mức: REQUIRED_PROCEDURE | GUIDANCE | HARD_CONSTRAINT
    - Trạng thái: bỏ        (tùy chọn; người duyệt đánh dấu bài học không lấy)

Kết quả:
  - <skill>: mỗi bài học thành một resource `lesson.<slug-tiêu-đề>`. Chạy lại với cùng tiêu đề thì cập nhật tại chỗ: nội dung
    đổi thì provenance.revision tăng (v1 → v2); không đổi thì giữ nguyên. Bài học không còn trong file đầu vào vẫn giữ.
  - --manifest: thêm skill vào `skills` của aw-project.json nếu chưa có, và thêm `skill-project-lessons#<key>` vào `addResources`
    (agent `from kit`) hoặc `resources` của từng agent trong "Gắn vào". Agent không có trong manifest là lỗi.
Sau đó publish như thường (`aw-publish.py aw-project.json`): aw tạo version mới của skill khi nội dung đổi.
Bài học đã gắn vào agent nằm trong ngân sách context của agent đó và tính vào giới hạn 15 HARD_CONSTRAINT.
"""
import argparse
import json
import os
import re
import sys
import unicodedata

sys.dont_write_bytecode = True
LEVELS = ("REQUIRED_PROCEDURE", "GUIDANCE", "HARD_CONSTRAINT")
FIELDS = ("Bằng chứng", "Số lần lặp", "Luật đề xuất", "Gắn vào", "Mức")
MAX_RULE = 500
SKILL_ID = "skill-project-lessons"
HEADING = re.compile(r"^### (L-\d+): (.+?)\s*$")
FIELD = re.compile(r"^- (Bằng chứng|Số lần lặp|Luật đề xuất|Gắn vào|Mức|Trạng thái):\s*(.*?)\s*$")


def fail(message):
    sys.exit(f"lessons-to-skill: {message}")


def slug(title):
    text = unicodedata.normalize("NFD", title.lower().replace("đ", "d"))
    text = "".join(c for c in text if unicodedata.category(c) != "Mn")
    return re.sub(r"[^a-z0-9]+", "-", text).strip("-")[:48]


def parse(path):
    """Danh sách bài học (dict) của một file; báo lỗi rõ khi thiếu trường."""
    lessons, current = [], None
    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.rstrip("\n")
            heading = HEADING.match(line)
            if heading:
                current = {"id": heading.group(1), "title": heading.group(2), "fields": {}, "source": f"{path}#{heading.group(1)}"}
                lessons.append(current)
            elif line.startswith("## "):
                current = None
            elif current is not None:
                field = FIELD.match(line)
                if field:
                    current["fields"][field.group(1)] = field.group(2)
    for lesson in lessons:
        name = f"{path}: {lesson['id']}"
        for field in FIELDS:
            if not lesson["fields"].get(field):
                fail(f"{name} thiếu \"{field}\"")
        rule = lesson["fields"]["Luật đề xuất"]
        if len(rule) > MAX_RULE:
            fail(f"{name}: luật dài {len(rule)} ký tự (tối đa {MAX_RULE})")
        if lesson["fields"]["Mức"] not in LEVELS:
            fail(f"{name}: Mức {lesson['fields']['Mức']!r} không hợp lệ ({', '.join(LEVELS)})")
        lesson["agents"] = [a.strip() for a in lesson["fields"]["Gắn vào"].split(",") if a.strip()]
        lesson["skipped"] = lesson["fields"].get("Trạng thái", "").lower().startswith("bỏ")
    return lessons


def merge(skill_path, lessons, owner):
    """Gộp bài học vào file skill; trả về (đã thêm, đã cập nhật, không đổi) và nội dung mới."""
    doc = json.load(open(skill_path, encoding="utf-8")) if os.path.isfile(skill_path) else {"resources": []}
    by_key = {r["key"]: r for r in doc["resources"]}
    added, updated, same = [], [], []
    for lesson in lessons:
        key = "lesson." + slug(lesson["title"])
        text = lesson["fields"]["Luật đề xuất"]
        priority = lesson["fields"]["Mức"]
        existing = by_key.get(key)
        if existing is None:
            resource = {"key": key, "priority": priority, "global": True, "selector": {},
                        "provenance": {"owner": owner, "source": lesson["source"], "revision": "v1", "lastVerified": "2026-10-07T00:00:00Z"},
                        "instruction": text}
            doc["resources"].append(resource)
            by_key[key] = resource
            added.append(key)
        elif existing["instruction"] != text or existing["priority"] != priority:
            revision = existing["provenance"].get("revision", "v1")
            number = int(revision[1:]) if revision[1:].isdigit() else 1
            existing["instruction"], existing["priority"] = text, priority
            existing["provenance"]["revision"] = f"v{number + 1}"
            existing["provenance"]["source"] = lesson["source"]
            updated.append(key)
        else:
            same.append(key)
        lesson["key"] = key
    return added, updated, same, doc


def edit_manifest(manifest_path, skill_rel, lessons):
    """Sửa aw-project.json bằng chỉnh văn bản tại chỗ để giữ nguyên định dạng tay của file."""
    text = open(manifest_path, encoding="utf-8").read()
    manifest = json.loads(text)
    agents = {a["id"]: a for a in manifest.get("agents", [])}
    for lesson in lessons:
        for agent in lesson["agents"]:
            if agent not in agents:
                fail(f"{lesson['source']}: agent {agent!r} không có trong {manifest_path}")
    if not any(s["id"] == SKILL_ID for s in manifest.get("skills", [])):
        entry = f'    {{"id": "{SKILL_ID}", "name": "Skill: bài học của project (sinh bởi lessons-to-skill.py)", "file": "{skill_rel}"}}'
        match = re.search(r'"skills":\s*\[', text)
        if match is None:
            fail(f"{manifest_path} chưa có mục \"skills\"; thêm \"skills\": [] rồi chạy lại")
        has_items = manifest.get("skills")
        text = text[:match.end()] + "\n" + entry + ("," if has_items else "") + text[match.end():]
    decoder = json.JSONDecoder()
    for agent_id in sorted({a for lesson in lessons for a in lesson["agents"]}):
        refs = [f"{SKILL_ID}#{lesson['key']}" for lesson in lessons if agent_id in lesson["agents"]]
        found = re.search(r'^\s*\{"id": "%s"' % re.escape(agent_id), text, re.M)
        if found is None:
            fail(f"không tìm thấy khai báo của agent {agent_id!r} trong {manifest_path} (cần dạng {{\"id\": \"{agent_id}\", …}})")
        start = text.index("{", found.start())
        obj, end = decoder.raw_decode(text, start)
        field = "resources" if "from" not in obj else "addResources"
        current = obj.get(field, [])
        obj[field] = current + [r for r in refs if r not in current]
        text = text[:start] + json.dumps(obj, ensure_ascii=False) + text[end:]
    json.loads(text)
    with open(manifest_path, "w", encoding="utf-8") as f:
        f.write(text)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("lessons", nargs="+")
    parser.add_argument("--skill", required=True, help="file JSON của skill bài học (tạo nếu chưa có)")
    parser.add_argument("--manifest", help="aw-project.json để thêm skill và gắn bài học vào agent")
    parser.add_argument("--owner", default="project", help="provenance.owner của resource (mặc định: project)")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    lessons = [l for path in args.lessons for l in parse(path)]
    kept = [l for l in lessons if not l["skipped"]]
    if not kept:
        fail("không có bài học nào còn hiệu lực trong các file đầu vào")
    added, updated, same, doc = merge(args.skill, kept, args.owner)
    for lesson in lessons:
        if lesson["skipped"]:
            print(f"bỏ qua {lesson['id']} (Trạng thái: bỏ): {lesson['title']}")
    print(f"{args.skill}: thêm {len(added)}, cập nhật {len(updated)}, không đổi {len(same)}")
    for key in added + updated:
        resource = next(r for r in doc["resources"] if r["key"] == key)
        print(f"  {key} [{resource['priority']}] {resource['provenance']['revision']}")
    if args.dry_run:
        return
    os.makedirs(os.path.dirname(os.path.abspath(args.skill)), exist_ok=True)
    with open(args.skill, "w", encoding="utf-8") as f:
        json.dump(doc, f, ensure_ascii=False, indent=2)
        f.write("\n")
    if args.manifest:
        rel = os.path.relpath(os.path.abspath(args.skill), os.path.dirname(os.path.abspath(args.manifest)))
        edit_manifest(args.manifest, rel.replace(os.sep, "/"), kept)
        print(f"{args.manifest}: skill đã khai báo, bài học đã gắn vào " + ", ".join(sorted({a for l in kept for a in l["agents"]})))


if __name__ == "__main__":
    main()
