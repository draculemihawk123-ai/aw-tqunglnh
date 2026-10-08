#!/usr/bin/env python3
"""Nhập một skill dạng SKILL.md (hoặc file agent/rule .md) thành BẢN NHÁP resource của aw, để người chắt lọc viết lại.

Cách dùng:
    import-skill.py <thư mục skill | file .md> [--prefix P] [--source NGUỒN] [--owner O] [--priority P]
                    [--ref-priority P] [--default-license L] [--max-bytes N] [--out THƯ_MỤC]

- Nguồn: thư mục có SKILL.md (kèm `references/*.md` nếu có), hoặc một file .md (agent có frontmatter, rule không có).
- Mỗi mục `## …` thành một resource (mục dài được tách theo đoạn); mỗi file trong `references/` thành các resource REFERENCE.
- Bản nháp (`<out>/<tên>.json`) và báo cáo (`<out>/<tên>.report.md`) mặc định nằm ở `kit/drafts/`, thư mục được git bỏ qua:
  bản nháp KHÔNG nằm trong kit.json và không bao giờ được publish. Người chắt lọc viết lại từng resource theo quy ước ở
  kit/README.md ("Chắt lọc từ nguồn bên ngoài"), đặt `priority` và `selector` đúng chỗ, rồi mới chuyển vào kit/skills/
  hoặc kit/layers/ và khai trong kit.json (báo cáo có sẵn đoạn khai mẫu).
- Báo cáo liệt kê những gì `aw-publish.py --check` sẽ bắt: cấu trúc chỉ có ở công cụ gốc, resource quá lớn, luật lặp.

Dùng được cho mọi nguồn theo chuẩn SKILL.md.
"""
import argparse
import datetime
import importlib.util
import json
import os
import re
import subprocess
import sys
import unicodedata

sys.dont_write_bytecode = True
HERE = os.path.dirname(os.path.abspath(__file__))
KIT_ROOT = os.path.dirname(HERE)
_spec = importlib.util.spec_from_file_location("aw_publish", os.path.join(HERE, "aw-publish.py"))
publish = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(publish)

PRIORITIES = ("HARD_CONSTRAINT", "REQUIRED_PROCEDURE", "GUIDANCE", "REFERENCE")
SOURCE_ROOT_DIRS = ("skills", "agents", "rules", "commands")


def slug(text):
    text = unicodedata.normalize("NFKD", text).encode("ascii", "ignore").decode()
    return re.sub(r"[^a-z0-9]+", "-", text.lower()).strip("-") or "muc"


def parse_frontmatter(text):
    """(meta, thân). Chỉ đọc khóa cấp một dạng `key: value` (giá trị trong nháy, hoặc khối `>-`/`|`); đủ cho name, description,
    license, model."""
    if not text.startswith("---\n"):
        return {}, text
    end = text.find("\n---", 4)
    if end < 0:
        return {}, text
    block, body = text[4:end], text[end + 4:].lstrip("\n")
    meta, lines, i = {}, block.split("\n"), 0
    while i < len(lines):
        m = re.match(r"^([A-Za-z0-9_-]+):\s*(.*)$", lines[i])
        i += 1
        if not m:
            continue
        key, value = m.groups()
        if value in (">-", ">", ">+", "|", "|-", "|+"):
            folded = []
            while i < len(lines) and (lines[i].startswith((" ", "\t")) or not lines[i].strip()):
                folded.append(lines[i].strip())
                i += 1
            value = (" " if value.startswith(">") else "\n").join(x for x in folded if x)
        elif len(value) >= 2 and value[0] == value[-1] and value[0] in "'\"":
            value = value[1:-1]
        meta[key] = value
    return meta, body


def split_sections(body):
    """[(tiêu đề, nội dung)] theo mục `## …` (bỏ qua dòng trong khối code); phần trước mục đầu là "Giới thiệu"."""
    sections, title, current, in_code = [], None, [], False

    def flush():
        text = "\n".join(current).strip()
        if title is not None and text:
            sections.append((title, text))
    for line in body.split("\n"):
        if line.startswith("```"):
            in_code = not in_code
        heading = None if in_code else re.match(r"^## (.+)$", line)
        if heading:
            flush()
            title, current = heading.group(1).strip(), []
        elif title is None and not in_code and re.match(r"^# ", line):
            continue  # tiêu đề cấp một của file
        else:
            if title is None and line.strip():
                title = "Giới thiệu"
            current.append(line)
    flush()
    return sections


def pack(text, limit):
    """Tách một đoạn dài theo ranh giới đoạn văn thành các phần không vượt `limit` byte (đoạn đơn lẻ quá dài giữ nguyên)."""
    parts, current = [], ""
    for paragraph in re.split(r"\n\s*\n", text):
        candidate = (current + "\n\n" + paragraph) if current else paragraph
        if current and len(candidate.encode("utf-8")) > limit:
            parts.append(current)
            current = paragraph
        else:
            current = candidate
    if current:
        parts.append(current)
    return parts


def relative_source(path):
    """Đường dẫn trong nguồn, tính từ thư mục skills/ agents/ rules/ gần nhất (ví dụ skills/ck-debug/SKILL.md)."""
    parts = os.path.abspath(path).split(os.sep)
    for index in range(len(parts) - 1, -1, -1):
        if parts[index] in SOURCE_ROOT_DIRS:
            return "/".join(parts[index:])
    return os.path.basename(path)


def default_source(path):
    """<tên repo>@<commit ngắn> nếu nguồn nằm trong một repo git, không thì tên thư mục."""
    folder = path if os.path.isdir(path) else os.path.dirname(path)
    try:
        top = subprocess.run(["git", "-C", folder, "rev-parse", "--show-toplevel"], capture_output=True, text=True, check=True).stdout.strip()
        sha = subprocess.run(["git", "-C", folder, "rev-parse", "--short", "HEAD"], capture_output=True, text=True, check=True).stdout.strip()
        return f"{os.path.basename(top)}@{sha}"
    except (OSError, subprocess.CalledProcessError):
        return os.path.basename(os.path.abspath(folder))


def build_resources(path, prefix, source, owner, priority, ref_priority, limit):
    """(tên, meta, [resource], ghi chú) từ một thư mục skill hoặc một file .md."""
    now = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT00:00:00Z")
    files = []  # (file thật, chuỗi ưu tiên, tiền tố con)
    if os.path.isdir(path):
        main_file = os.path.join(path, "SKILL.md")
        if not os.path.isfile(main_file):
            sys.exit(f"import-skill: không thấy SKILL.md trong {path}")
        files.append((main_file, priority, None))
        refs = os.path.join(path, "references")
        if os.path.isdir(refs):
            for name in sorted(os.listdir(refs)):
                if name.endswith(".md"):
                    files.append((os.path.join(refs, name), ref_priority, slug(name[:-3])))
    else:
        main_file = path
        files.append((main_file, priority, None))
    with open(main_file, encoding="utf-8") as f:
        meta, _ = parse_frontmatter(f.read())
    name = meta.get("name") or os.path.basename(path.rstrip(os.sep)).removesuffix(".md")
    prefix = prefix or slug(name.split(":")[-1])
    resources, notes, used = [], [], set()
    for file, file_priority, sub in files:
        with open(file, encoding="utf-8") as f:
            _, body = parse_frontmatter(f.read())
        rel = relative_source(file)
        for title, text in split_sections(body):
            base = ".".join(x for x in (prefix, sub, slug(title)) if x)
            chunks = pack(f"{title}\n\n{text}", limit)
            for index, chunk in enumerate(chunks, 1):
                key = base if len(chunks) == 1 else f"{base}-{index}"
                n = 2
                while key in used:
                    key, n = f"{base}-{n}", n + 1
                used.add(key)
                resources.append({"key": key, "priority": file_priority, "selector": {},
                                  "provenance": {"owner": owner, "source": f"{source}:{rel}", "revision": "draft", "lastVerified": now},
                                  "instruction": chunk})
            if len(chunks) > 1:
                notes.append(f"mục \"{title}\" ({rel}) quá {limit} byte nên được tách thành {len(chunks)} phần: nên viết lại gọn thành một ý")
    return name, meta, resources, notes


def report_text(name, meta, resources, notes, source, out_json, default_license):
    lines = [f"# Bản nháp nhập từ `{name}`", "",
             f"- Nguồn: `{source}`", f"- Mô tả của nguồn: {meta.get('description', '(không có)')[:300]}",
             f"- `license:` trong nguồn: {meta.get('license') or '(không khai)'}",
             f"- Bản nháp: `{out_json}` ({len(resources)} resource, "
             f"{sum(len(r['instruction'].encode('utf-8')) for r in resources)} byte)", "",
             "## Khai mẫu trong kit.json (sau khi chắt lọc xong và chuyển file vào kit/skills/ hoặc kit/layers/)", "", "```json",
             json.dumps({"id": f"skill-{slug(name)}", "name": f"Skill: {name}",
                         "file": f"skills/skill-{slug(name)}.json", "origin": source,
                         "license": meta.get("license") or default_license}, ensure_ascii=False, indent=1),
             "```", "",
             "Trong bản nháp `provenance.source` của mỗi resource có dạng `<nguồn>:<đường dẫn>` để truy ngược khi chắt lọc; khi chuyển vào kit, đổi nó thành đường dẫn file định nghĩa trong kit nếu không muốn giữ tên nguồn.", "",
             "## Resource", "", "| key | priority | byte | vấn đề |", "|---|---|---|---|"]
    seen = {}
    for resource in resources:
        text = resource["instruction"]
        size = len(text.encode("utf-8"))
        issues = [label for pattern, label in publish.FOREIGN_CONSTRUCTS if re.search(pattern, text)]
        issues = [f"cấu trúc chỉ có ở công cụ gốc: {x}" for x in issues]
        if size > publish.INSTRUCTION_MAX_BYTES:
            issues.append(f"quá {publish.INSTRUCTION_MAX_BYTES} byte")
        elif size > publish.INSTRUCTION_WARN_BYTES:
            issues.append(f"nên gọn dưới {publish.INSTRUCTION_WARN_BYTES} byte")
        for sentence in publish.sentences(text):
            first = seen.setdefault(sentence, resource["key"])
            if first != resource["key"]:
                issues.append(f"lặp luật của {first}")
        lines.append(f"| `{resource['key']}` | {resource['priority']} | {size} | {'; '.join(sorted(set(issues))) or '-'} |")
    lines += ["", "## Việc cần làm"]
    lines += [f"- {x}" for x in notes]
    lines += ["- Viết lại từng resource cho ngữ cảnh aw (agent không chạy được lệnh, không subagent, không hỏi người trực tiếp).",
              "- Đặt `priority` đúng bản chất và `selector` (componentTags, blockKinds, riskClasses) cho từng resource.",
              "- Chạy `aw-publish.py kit/kit.json --check` và `kit/tests/check-kit.sh`."]
    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("source_path")
    parser.add_argument("--prefix", help="tiền tố key resource (mặc định: tên skill)")
    parser.add_argument("--source", help="định danh nguồn trong provenance.source (mặc định: <repo>@<commit>)")
    parser.add_argument("--owner", default="aw-kit")
    parser.add_argument("--priority", default="GUIDANCE", choices=PRIORITIES, help="priority của các mục trong SKILL.md")
    parser.add_argument("--ref-priority", default="REFERENCE", choices=PRIORITIES, help="priority của các file trong references/")
    parser.add_argument("--default-license", default="UNKNOWN", help="giấy phép gợi ý khi nguồn không khai")
    parser.add_argument("--max-bytes", type=int, default=2800, help="tách mục dài hơn ngưỡng này (byte)")
    parser.add_argument("--out", default=os.path.join(KIT_ROOT, "drafts"))
    args = parser.parse_args()
    if not os.path.exists(args.source_path):
        sys.exit(f"import-skill: không thấy {args.source_path}")
    source = args.source or default_source(args.source_path)
    name, meta, resources, notes = build_resources(args.source_path, args.prefix, source, args.owner, args.priority,
                                                   args.ref_priority, args.max_bytes)
    if not resources:
        sys.exit("import-skill: không tìm thấy mục `## …` nào để nhập")
    os.makedirs(args.out, exist_ok=True)
    base = slug(name)
    out_json = os.path.join(args.out, base + ".json")
    with open(out_json, "w", encoding="utf-8") as f:
        json.dump({"resources": resources}, f, ensure_ascii=False, indent=1)
        f.write("\n")
    report = os.path.join(args.out, base + ".report.md")
    with open(report, "w", encoding="utf-8") as f:
        f.write(report_text(name, meta, resources, notes, source, out_json, args.default_license))
    flagged = sum(1 for r in resources if any(re.search(p, r["instruction"]) for p, _ in publish.FOREIGN_CONSTRUCTS))
    print(f"{name}: {len(resources)} resource → {out_json}")
    print(f"báo cáo → {report}  ({flagged} resource còn cấu trúc chỉ có ở công cụ gốc; {len(notes)} mục phải tách/viết gọn)")


if __name__ == "__main__":
    main()
