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

# alias bản mẫu + bind: một workflow mẫu dùng cho nhiều repository
cat > "$tmp/p/bind.json" <<JSON
{"prefix": "p-", "repository": "r", $kitref,
 "agents": [{"id": "a", "resources": ["skill-feature-flow#flow.working-rules"]}],
 "commands": [{"id": "cmd-reject", "from": "kit"}, {"id": "cmd-api-test", "from": "kit:cmd-reject"}, {"id": "cmd-api-q", "from": "kit:cmd-reject"}],
 "workflows": [{"id": "wf-task-delivery-api", "from": "kit:wf-task-delivery", "bind": {"command:cmd-gate1": "cmd-api-test",
   "command:cmd-quality-check": "cmd-api-q", "agent:agent-flow-frame": "a", "agent:agent-flow-plan": "a",
   "agent:agent-flow-build": "a", "agent:agent-flow-sync": "a"}}]}
JSON
$pub "$tmp/p/bind.json" --check > "$tmp/o" 2>&1 && ok "from kit:<id> + bind giải quyết được chỗ trống" || bad "alias + bind: $(cat "$tmp/o")"
sed 's/"command:cmd-gate1"/"command:cmd-gate9"/' "$tmp/p/bind.json" > "$tmp/p/bind-typo.json"
expect_err "bind gõ sai khóa bị bắt" "không khớp \$ref nào" "$tmp/p/bind-typo.json"
sed 's/"from": "kit:cmd-reject"}, {"id": "cmd-api-q"/"from": "kit:cmd-khong-co"}, {"id": "cmd-api-q"/' "$tmp/p/bind.json" > "$tmp/p/alias-bad.json"
expect_err "alias tới bản mẫu không có" "kit không có bản mẫu" "$tmp/p/alias-bad.json"
# argRepositories: lệnh nhận đường dẫn worktree của repository khác (kiểm tra bằng publish thật ở README của ví dụ)
python3 - "$kit/scripts/aw-publish.py" <<'PY' && ok "argRepositories có trong aw-publish" || bad "argRepositories thiếu"
import sys
sys.exit(0 if "argRepositories" in open(sys.argv[1], encoding="utf-8").read() else 1)
PY

echo "== tri thức chắt lọc từ nguồn ngoài (V10-00)"
# mutate <thư mục kho copy> <file python sửa d (skill-review.json) và entry (kit.json)>: dựng một bản kho đã sửa
mutate() {
  python3 - "$1" "$2" <<'PY'
import json, sys
root, code = sys.argv[1], sys.argv[2]
f = root + "/skills/skill-review.json"
d = json.load(open(f, encoding="utf-8"))
k = json.load(open(root + "/kit.json", encoding="utf-8"))
entry = next(e for e in k["skills"] if e["id"] == "skill-review")
exec(code)
json.dump(d, open(f, "w", encoding="utf-8"), ensure_ascii=False)
json.dump(k, open(root + "/kit.json", "w", encoding="utf-8"), ensure_ascii=False)
PY
}
cp -R "$kit" "$tmp/k1"; mutate "$tmp/k1" 'd["resources"][0]["instruction"] += " Hãy dùng /ck:plan trước."'
expect_err "cấu trúc chỉ có ở ClaudeKit bị chặn (/ck:)" "cấu trúc chỉ có ở công cụ gốc" "$tmp/k1/kit.json"
cp -R "$kit" "$tmp/k2"; mutate "$tmp/k2" 'd["resources"][0]["instruction"] += " Dùng AskUserQuestion để hỏi."'
expect_err "AskUserQuestion bị chặn" "AskUserQuestion" "$tmp/k2/kit.json"
cp -R "$kit" "$tmp/k3"; mutate "$tmp/k3" 'd["resources"][0]["instruction"] += " Tiếp theo." * 400'
expect_err "resource quá lớn bị chặn" "quá 3072" "$tmp/k3/kit.json"
cp -R "$kit" "$tmp/k4"; mutate "$tmp/k4" 'entry["origin"] = "claudekit-engineer@ed8a1fa"; entry["license"] = "MIT"'
expect_err "mục từ ClaudeKit thiếu redistributable" "redistributable" "$tmp/k4/kit.json"
cp -R "$kit" "$tmp/k5"; mutate "$tmp/k5" 'entry["origin"] = "claudekit-engineer@ed8a1fa"; entry["license"] = "MIT"; entry["redistributable"] = True'
expect_err "mục từ ClaudeKit: provenance.source sai dạng" "provenance.source phải có dạng claudekit-engineer@ed8a1fa:" "$tmp/k5/kit.json"
SRC='entry["origin"] = "claudekit-engineer@ed8a1fa (chắt lọc)"; entry["license"] = "ClaudeKit-Proprietary (licensed)"; entry["redistributable"] = RED
for r in d["resources"]: r["provenance"]["source"] = "claudekit-engineer@ed8a1fa:skills/ck-code-review/SKILL.md"'
cp -R "$kit" "$tmp/k6"; mutate "$tmp/k6" "$(printf '%s' "$SRC" | sed 's/RED/False/')"
$pub "$tmp/k6/kit.json" --check > "$tmp/o" 2>&1 && ok "mục từ ClaudeKit khai đủ origin, license, redistributable, source" || bad "mục ClaudeKit hợp lệ bị từ chối: $(cat "$tmp/o")"
$pub "$tmp/k6/kit.json" --share > "$tmp/o" 2>&1 && bad "--share đáng lẽ chặn mục redistributable=false" \
  || { grep -q "skills/skill-review: không được phân phối lại" "$tmp/o" && ok "--share liệt kê mục redistributable=false và thoát mã 1" || bad "--share thiếu thông báo: $(cat "$tmp/o")"; }
cp -R "$kit" "$tmp/k7"; mutate "$tmp/k7" "$(printf '%s' "$SRC" | sed 's/RED/True/')"
python3 - "$tmp/k7/kit.json" <<'PY'
import json, sys  # trước khi chia sẻ phải xử lý mục UNKNOWN và mục không được phân phối lại: đặt giấy phép rõ ràng, cho phép phân phối
k = json.load(open(sys.argv[1], encoding="utf-8"))
for e in k["skills"]:
    if str(e.get("license", "")).upper().startswith("UNKNOWN"):
        e["license"] = "MIT"
    if e.get("redistributable") is False:
        e["redistributable"] = True
json.dump(k, open(sys.argv[1], "w", encoding="utf-8"), ensure_ascii=False)
PY
$pub "$tmp/k7/kit.json" --share > "$tmp/o" 2>&1 && ok "--share cho qua khi mọi mục được phép chia sẻ" || bad "--share chặn nhầm: $(cat "$tmp/o")"
cp -R "$kit" "$tmp/k11"; mutate "$tmp/k11" 'entry["license"] = "UNKNOWN"'
$pub "$tmp/k11/kit.json" --check 2>&1 | grep -q "giấy phép chưa xác định" && ok "license UNKNOWN chỉ cảnh báo khi --check" || bad "không cảnh báo license UNKNOWN"
$pub "$tmp/k11/kit.json" --share > "$tmp/o" 2>&1 && bad "--share đáng lẽ chặn mục giấy phép UNKNOWN" \
  || { grep -q "giấy phép chưa xác định" "$tmp/o" && ok "--share chặn mục giấy phép UNKNOWN" || bad "--share thiếu thông báo UNKNOWN: $(cat "$tmp/o")"; }
cp -R "$kit" "$tmp/k10"; mutate "$tmp/k10" 'd["resources"][0]["selector"] = {}; d["resources"][0].pop("global", None)'
expect_err "resource không selector mà thiếu global:true" 'phải khai "global": true' "$tmp/k10/kit.json"
# quá 15 HARD_CONSTRAINT cho một agent
python3 - "$tmp/p" <<'PY'
import json, sys
root = sys.argv[1]
res = [{"key": "h%d" % i, "priority": "HARD_CONSTRAINT", "selector": {}, "instruction": "Luật số %d." % i,
        "provenance": {"owner": "t", "source": "t", "revision": "v1", "lastVerified": "2026-10-07T00:00:00Z"}} for i in range(16)]
json.dump({"resources": res}, open(root + "/hard16.json", "w", encoding="utf-8"), ensure_ascii=False)
PY
cat > "$tmp/p/hard.json" <<JSON
{"prefix": "p-", "repository": "r", $kitref, "skills": [{"id": "skill-hard", "file": "hard16.json"}],
 "agents": [{"id": "a", "resources": ["skill-hard"]}]}
JSON
expect_err "agent có quá 15 HARD_CONSTRAINT" "quá 15" "$tmp/p/hard.json"
# luật lặp giữa hai resource: chỉ cảnh báo
cp -R "$kit" "$tmp/k8"; mutate "$tmp/k8" 'text = "Mọi khẳng định về code trong báo cáo phải kèm đường dẫn file và số dòng cụ thể để người đọc kiểm chứng được."
d["resources"][0]["instruction"] += " " + text
d["resources"][1]["instruction"] += " " + text'
$pub "$tmp/k8/kit.json" --check 2>&1 | grep -q "lặp luật đã có" && ok "luật lặp ở hai resource chỉ bị cảnh báo" || bad "không cảnh báo luật lặp"

echo "== công cụ nhập skill (V10-01)"
fixture="$kit/tests/fixtures/skills/sample-skill"
python3 "$kit/scripts/import-skill.py" "$fixture" --source "mau@1" --out "$tmp/draft" > "$tmp/o" 2>&1 && ok "import-skill chạy trên skill mẫu" || bad "import-skill: $(cat "$tmp/o")"
python3 - "$tmp/draft" <<'PY' && ok "bản nháp: key, priority, provenance, tách mục dài, bỏ qua dòng ## trong khối code" || bad "bản nháp sai (xem chi tiết ở trên)"
import json, sys
d = json.load(open(sys.argv[1] + "/sample-skill.json", encoding="utf-8"))
keys = [r["key"] for r in d["resources"]]
by = {r["key"]: r for r in d["resources"]}
assert "skill.gioi-thieu" in keys and "skill.nguyen-tac-chung" in keys, keys
assert any(k.startswith("skill.kiem-thu-") for k in keys), keys  # mục dài bị tách thành nhiều phần
assert all(len(r["instruction"].encode()) <= 3072 for r in d["resources"]), "phần tách còn quá 3072 byte"
assert by["skill.extra.danh-muc-kiem-tra"]["priority"] == "REFERENCE", by["skill.extra.danh-muc-kiem-tra"]["priority"]
assert by["skill.nguyen-tac-chung"]["priority"] == "GUIDANCE"
assert by["skill.nguyen-tac-chung"]["provenance"]["source"] == "mau@1:skills/sample-skill/SKILL.md", by["skill.nguyen-tac-chung"]["provenance"]
assert set(by["skill.nguyen-tac-chung"]["provenance"]) == {"owner", "source", "revision", "lastVerified"}
assert "dòng này nằm trong khối code" in by["skill.muc-co-khoi-code"]["instruction"]
assert not any("dòng này nằm trong khối code" in r["key"] for r in d["resources"])
report = open(sys.argv[1] + "/sample-skill.report.md", encoding="utf-8").read()
assert "MIT" in report and "/ck:" in report and "cấu trúc chỉ có ở công cụ gốc" in report, "báo cáo thiếu license hoặc cấu trúc cấm"
assert '"redistributable"' in report and '"origin": "mau@1"' in report
PY
# bản nháp không nằm trong kit.json nên không bị --check; chuyển vào kho thì lint bắt cấu trúc cấm
cp -R "$kit" "$tmp/k9"
python3 - "$tmp/draft/sample-skill.json" "$tmp/k9" <<'PY'
import json, shutil, sys
shutil.copy(sys.argv[1], sys.argv[2] + "/skills/skill-mau.json")
k = json.load(open(sys.argv[2] + "/kit.json", encoding="utf-8"))
k["skills"].append({"id": "skill-mau", "name": "Skill: mẫu", "file": "skills/skill-mau.json", "origin": "mau@1", "license": "MIT"})
json.dump(k, open(sys.argv[2] + "/kit.json", "w", encoding="utf-8"), ensure_ascii=False)
PY
expect_err "bản nháp chưa chắt lọc bị lint chặn khi vào kho" "cấu trúc chỉ có ở công cụ gốc" "$tmp/k9/kit.json"
git -C "$repo" check-ignore -q "$kit/drafts/x.json" 2> /dev/null && ok "kit/drafts/ được git bỏ qua" || bad "kit/drafts/ chưa được git bỏ qua"
echo "== context-check (V10-01)"
if command -v go > /dev/null 2>&1 && [ -f "$repo/go.mod" ]; then
  if [ -f "$example" ] && sh "$kit/scripts/context-check.sh" "$example" build --component backend --expect flow.build --absent react.api-client > "$tmp/o" 2>&1; then
    grep -q "NOT_APPLICABLE" "$tmp/o" && ok "context-check: build/backend nạp flow.build, loại react.api-client (agent giả lập)" || bad "context-check không in resource bị loại"
  else
    bad "context-check: $(tail -5 "$tmp/o")"
  fi
  sh "$kit/scripts/context-check.sh" "$example" build --component backend --expect react.api-client > "$tmp/o" 2>&1 \
    && bad "context-check đáng lẽ báo thiếu khi --expect resource không được nạp" || { grep -q "thiếu: react.api-client" "$tmp/o" && ok "context-check --expect báo thiếu và thoát mã 1" || bad "thiếu thông báo --expect: $(tail -3 "$tmp/o")"; }
else
  echo "  bỏ qua (cần Go và repo aw để build aw và fake-claude, hoặc đặt AW và AW_FAKE_CLAUDE)"
fi

echo "== bộ đo (V10-02)"
python3 "$kit/tests/test-bench.py" > "$tmp/o" 2>&1 && ok "kit-bench (tự duyệt, chạy lại, needs_info) và kit-metrics (collect, compare, blind)" || bad "bộ đo: $(cat "$tmp/o")"
for fixture in contracts api web; do
  git clone -q -b main "$kit/bench/issue-tracker-mvp/$fixture.bundle" "$tmp/fx-$fixture" 2> /dev/null && ok "fixture $fixture.bundle clone được" || bad "fixture $fixture.bundle hỏng"
done
if command -v go > /dev/null 2>&1 && [ -f "$repo/go.mod" ]; then
  # chỉ dựng môi trường (không chạy agent, không tốn tiền): dùng agent giả lập làm Claude CLI
  fake=$(sh -c 'cd "$1" && go build -o "$2/fake-claude" ./cmd/fake-claude && echo "$2/fake-claude"' _ "$repo" "$tmp") \
    && AW_CLAUDE_EXECUTABLE="$fake" python3 "$kit/scripts/kit-bench.py" --label thu --runs 1 --out "$tmp/bench" --setup-only --no-readiness > "$tmp/o" 2>&1 \
    && grep -q "setup=SUCCEEDED" "$tmp/o" && ok "kit-bench --setup-only dựng được bản cài, ba repository, publish, gốc (agent giả lập)" \
    || bad "kit-bench --setup-only: $(tail -5 "$tmp/o")"
fi

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

# aw_step nhận diện lỗi môi trường (AW_ENV_ERRORS) và không coi đó là lỗi code
cat > "$tmp/p/env.sh" <<'SH'
#!/bin/sh
set -eu
. "${AW_KIT:?x}/commands/lib.sh" # @aw-include
AW_ENV_ERRORS='SELF_SIGNED_CERT|ENOTFOUND'
aw_step "cài dependency" sh -c 'echo "npm error code SELF_SIGNED_CERT_IN_CHAIN"; exit 1'
SH
out=$(AW_KIT="$kit" sh "$tmp/p/env.sh" 2>&1 >/dev/null); code=$?
[ "$code" = 1 ] && printf '%s' "$out" | grep -q "^MÔI TRƯỜNG:" && ok "lỗi mạng/chứng chỉ báo là MÔI TRƯỜNG" || bad "lỗi môi trường bị coi là lỗi code: $out"
sed -i 's/SELF_SIGNED_CERT_IN_CHAIN/Cannot find module x/' "$tmp/p/env.sh"
out=$(AW_KIT="$kit" sh "$tmp/p/env.sh" 2>&1 >/dev/null)
printf '%s' "$out" | grep -q "^MÔI TRƯỜNG:" && bad "lỗi code bị báo nhầm là môi trường" || ok "lỗi code vẫn là lỗi code"

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
