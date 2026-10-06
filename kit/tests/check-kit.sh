#!/bin/sh
# Kiểm tra tự động của kho kit (không cần bản cài aw). Cách dùng: kit/tests/check-kit.sh
# Chạy: --check của kho và của project ví dụ; các nhánh báo lỗi; script đã nhúng thư viện chạy đúng khi KHÔNG có AW_KIT;
# validate theo schema nếu có Python jsonschema. Thoát mã 1 nếu có bất kỳ kiểm tra nào hỏng.
set -u
here=$(cd "$(dirname "$0")" && pwd)
kit=$(dirname "$here")
repo=$(dirname "$kit")
pub="python3 $kit/scripts/aw-publish.py"
example="$repo/docs/guides/todolist-spring-react/aw-project.json"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
pass=0
failed=0
ok() { echo "  ok    $1"; pass=$((pass + 1)); }
bad() { echo "  HỎNG  $1"; failed=$((failed + 1)); }
# expect_err <tên> <chuỗi phải có trong thông báo lỗi> <file khai báo>: --check phải thất bại với thông báo đó
expect_err() {
  out=$($pub "$3" --check 2>&1) && { bad "$1: đáng lẽ phải báo lỗi"; return; }
  if printf '%s' "$out" | grep -q -F -- "$2"; then ok "$1"; else bad "$1: thiếu thông báo '$2'; nhận được: $out"; fi
}

echo "== kho và ví dụ"
$pub "$kit/kit.json" --check > "$tmp/o" 2>&1 && ok "kit.json hợp lệ" || { bad "kit.json: $(cat "$tmp/o")"; }
if [ -f "$example" ]; then
  $pub "$example" --check > "$tmp/o" 2>&1 && ok "project ví dụ hợp lệ" || bad "project ví dụ: $(cat "$tmp/o")"
  $pub "$example" --slots 2>&1 | grep -q 'THIẾU' && bad "project ví dụ còn THIẾU chỗ trống" || ok "project ví dụ đủ chỗ trống của workflow mẫu"
fi

echo "== các nhánh báo lỗi"
kitref="\"kit\": {\"path\": \"$kit/kit.json\", \"version\": \"1\"}"
mkdir -p "$tmp/p"
cat > "$tmp/p/prefix.json" <<JSON
{"prefix": "kit-", "repository": "r", $kitref}
JSON
expect_err "prefix trùng prefix của kit" "trùng prefix của kit" "$tmp/p/prefix.json"
cat > "$tmp/p/major.json" <<JSON
{"prefix": "p-", "repository": "r", "kit": {"path": "$kit/kit.json", "version": "99"}}
JSON
expect_err "kit khác major" "đòi kit phiên bản 99" "$tmp/p/major.json"
echo '{"resources": []}' > "$tmp/p/empty.json"
cat > "$tmp/p/dup.json" <<JSON
{"prefix": "p-", "repository": "r", $kitref, "skills": [{"id": "skill-review", "file": "empty.json"}]}
JSON
expect_err "id trùng id của kit" "skills của project và skills của kit" "$tmp/p/dup.json"
cat > "$tmp/p/nofrom.json" <<JSON
{"prefix": "p-", "repository": "r", $kitref, "commands": [{"id": "cmd-khong-co", "from": "kit"}]}
JSON
expect_err "from kit nhưng kit không có bản mẫu" "kit không có bản mẫu" "$tmp/p/nofrom.json"
cat > "$tmp/p/slot.json" <<JSON
{"prefix": "p-", "repository": "r", $kitref, "workflows": [{"id": "wf-feature-definition", "from": "kit"}]}
JSON
expect_err "thiếu chỗ trống, gợi ý lấy bản mẫu của kit" '"from": "kit"' "$tmp/p/slot.json"
cat > "$tmp/p/include.sh" <<'SH'
#!/bin/sh
. "${AW_KIT:?x}/commands/khong-co.sh" # @aw-include
SH
cat > "$tmp/p/include.json" <<JSON
{"prefix": "p-", "repository": "r", $kitref, "scriptSkills": [{"id": "s", "files": ["include.sh"]}],
 "commands": [{"id": "c", "script": "s#include.sh"}]}
JSON
expect_err "@aw-include trỏ file không có" "@aw-include trỏ tới file không có" "$tmp/p/include.json"
# kho copy: nguồn URL thiếu license (khai ở entry trong kit.json, không phải trong provenance)
cp -R "$kit" "$tmp/kitcopy"
python3 - "$tmp/kitcopy" <<'PY'
import json, sys
f = sys.argv[1] + "/skills/skill-review.json"
d = json.load(open(f, encoding="utf-8"))
d["resources"][0]["provenance"]["source"] = "https://example.com/review"
json.dump(d, open(f, "w", encoding="utf-8"), ensure_ascii=False)
PY
expect_err "nguồn URL thiếu license ở kit.json" '"license"' "$tmp/kitcopy/kit.json"
# provenance.license bị aw từ chối lúc publish (decode nghiêm ngặt) nên --check phải chặn sớm
cp -R "$kit" "$tmp/kitcopy2"
python3 - "$tmp/kitcopy2" <<'PY'
import json, sys
f = sys.argv[1] + "/skills/skill-review.json"
d = json.load(open(f, encoding="utf-8"))
d["resources"][0]["provenance"]["license"] = "MIT"
json.dump(d, open(f, "w", encoding="utf-8"), ensure_ascii=False)
PY
expect_err "provenance.license bị chặn (aw không nhận)" "provenance có trường aw không nhận" "$tmp/kitcopy2/kit.json"
# license UNKNOWN: không chặn, chỉ cảnh báo
$pub "$kit/kit.json" --check 2>&1 | grep -q "giấy phép chưa xác định" && ok "license UNKNOWN chỉ cảnh báo" \
  || echo "  (không có mục UNKNOWN trong kho: bỏ qua kiểm tra cảnh báo)"

echo "== script đã nhúng thư viện chạy khi KHÔNG có AW_KIT"
cat > "$tmp/p/demo.sh" <<'SH'
#!/bin/sh
set -eu
. "${AW_KIT:?đặt AW_KIT}/commands/lib.sh" # @aw-include
AW_STEP_HEAD="demo: bước"
AW_TAIL=3
aw_step "bước đạt" true
aw_step "bước hỏng" sh -c 'i=0; while [ $i -lt 10 ]; do echo "dòng $i"; i=$((i+1)); done; exit 1'
echo "không tới đây"
SH
cat > "$tmp/p/demo.json" <<JSON
{"prefix": "p-", "repository": "r", $kitref, "scriptSkills": [{"id": "s", "files": ["demo.sh"]}],
 "commands": [{"id": "c", "script": "s#demo.sh"}]}
JSON
python3 - "$kit/scripts/aw-publish.py" "$tmp/p/demo.json" "$tmp/p/demo.inlined.sh" <<'PY'
import importlib.util, re, sys
sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("p", sys.argv[1]); p = importlib.util.module_from_spec(spec); spec.loader.exec_module(p)
m = p.load_manifest(sys.argv[2])
body = p.script_skill_doc(m, m["scriptSkills"][0])["resources"][0]["instruction"]
assert not any(p.INCLUDE_RE.match(l) for l in body.splitlines()), "còn dòng @aw-include chưa được thay"
open(sys.argv[3], "w", encoding="utf-8").write(body)
PY
if [ -f "$tmp/p/demo.inlined.sh" ]; then
  ok "aw-publish nhúng thư viện, không còn dòng @aw-include"
  out=$(env -u AW_KIT sh "$tmp/p/demo.inlined.sh" 2>&1 >/dev/null); code=$?
  [ "$code" = 1 ] && ok "bước hỏng thoát mã 1" || bad "bước hỏng phải thoát mã 1, nhận $code"
  printf '%s' "$out" | grep -q "demo: bước 'bước hỏng' không đạt" && ok "thông báo lỗi ra stderr đúng định dạng" || bad "thông báo lỗi sai: $out"
  [ "$(printf '%s\n' "$out" | wc -l)" = 4 ] && ok "chỉ in AW_TAIL=3 dòng cuối của output" || bad "số dòng stderr sai: $out"
  printf '%s' "$out" | grep -q "không tới đây" && bad "script chạy tiếp sau bước hỏng" || ok "script dừng ngay ở bước hỏng"
else
  bad "không tạo được bản đã nhúng"
fi
# chạy tay: dòng nạp file thật từ AW_KIT
out=$(AW_KIT="$kit" sh "$tmp/p/demo.sh" 2>&1 >/dev/null); [ $? = 1 ] && printf '%s' "$out" | grep -q "demo: bước 'bước hỏng'" \
  && ok "chạy tay với AW_KIT nạp thư viện thật" || bad "chạy tay với AW_KIT hỏng: $out"
out=$(env -u AW_KIT sh "$tmp/p/demo.sh" 2>&1 >/dev/null); printf '%s' "$out" | grep -q "đặt AW_KIT" \
  && ok "chạy tay thiếu AW_KIT báo rõ cần đặt biến" || bad "thiếu AW_KIT mà không báo: $out"

echo "== schema"
if python3 -c "import jsonschema" 2> /dev/null; then
  python3 - "$kit/schema/aw-project.schema.json" "$kit/kit.json" "$example" <<'PY' && ok "kit.json và ví dụ khớp schema" || bad "khai báo không khớp schema"
import json, sys, jsonschema
schema = json.load(open(sys.argv[1], encoding="utf-8"))
jsonschema.Draft7Validator.check_schema(schema)
for path in sys.argv[2:]:
    try:
        doc = json.load(open(path, encoding="utf-8"))
    except OSError:
        continue
    errors = [e.message for e in jsonschema.Draft7Validator(schema).iter_errors(doc)]
    if errors:
        print(path, errors[:3]); sys.exit(1)
bad = {"prefix": "x-", "commands": [{"id": "c"}], "workflows": [{"id": "w", "from": "zzz"}], "extra": 1}
assert list(jsonschema.Draft7Validator(schema).iter_errors(bad)), "schema không bắt được mẫu sai"
PY
else
  echo "  bỏ qua (chưa cài Python jsonschema: pip install jsonschema)"
fi

echo
echo "Kết quả: $pass đạt, $failed hỏng"
[ "$failed" = 0 ]
