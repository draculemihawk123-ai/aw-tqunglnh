#!/bin/sh
# COMMAND "check-lessons" (V10-16, trước cổng duyệt của wf-retro): file docs/lessons/*.md mới hoặc được sửa phải đúng định dạng để
# lessons-to-skill.py biến thành resource: mỗi bài học "### L-n: tiêu đề" có đủ năm dòng (Bằng chứng, Số lần lặp, Luật đề xuất, Gắn vào,
# Mức), luật tối đa 500 ký tự, Mức hợp lệ, tối đa 5 bài học. Chạy ở gốc worktree.
set -eu
files=$(git status --porcelain --untracked-files=all -- docs/lessons | sed -E 's#^.{3}##; s#^"##; s#"$##' | grep -E '^docs/lessons/[^/]+\.md$' | sort -u || true)
if [ -z "$files" ]; then
  echo "THIẾU: không có docs/lessons/<ngày>.md mới hoặc được sửa. FIX: ghi bài học vào docs/lessons/<yyyy-mm-dd>.md theo retro.lessons-format." >&2
  exit 1
fi
fail=0
for f in $files; do
  [ -s "$f" ] || { echo "RỖNG: $f" >&2; fail=1; continue; }
  out=$(awk -v file="$f" '
    function flush() {
      if (id == "") return
      n++
      split("Bằng chứng|Số lần lặp|Luật đề xuất|Gắn vào|Mức", need, "|")
      for (i = 1; i <= 5; i++) if (!(need[i] in seen)) { print "THIẾU \"" need[i] "\" ở " id " trong " file; bad = 1 }
      if (length(rule) > 500) { print id ": luật dài " length(rule) " ký tự (tối đa 500) trong " file; bad = 1 }
      if ("Mức" in seen && level !~ /^(REQUIRED_PROCEDURE|GUIDANCE|HARD_CONSTRAINT)$/) { print id ": Mức \"" level "\" không hợp lệ (REQUIRED_PROCEDURE, GUIDANCE hoặc HARD_CONSTRAINT)"; bad = 1 }
      delete seen; rule = ""; level = ""
    }
    /^### L-[0-9]+: / { flush(); id = $2; sub(/:$/, "", id); next }
    /^## / { flush(); id = ""; next }
    id != "" && /^- (Bằng chứng|Số lần lặp|Luật đề xuất|Gắn vào|Mức|Trạng thái):/ {
      key = $0; sub(/^- /, "", key); sub(/:.*/, "", key); val = $0; sub(/^[^:]*: */, "", val)
      seen[key] = 1
      if (key == "Luật đề xuất") rule = val
      if (key == "Mức") level = val
    }
    END { flush(); if (n == 0) { print "Không có bài học nào (### L-n: …) trong " file; bad = 1 } if (n > 5) { print n " bài học (tối đa 5) trong " file; bad = 1 } exit bad }
  ' "$f") || { printf '%s\n' "$out" >&2; fail=1; continue; }
  echo "OK: $f"
done
[ "$fail" = 0 ] || { echo "FIX: sửa file theo retro.lessons-format (năm dòng cho mỗi bài học, luật tối đa 500 ký tự)." >&2; exit 1; }
