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

# agent mẫu của kit + addResources: tri thức của kit cộng tri thức của project, không trùng
cat > "$tmp/p/agent.json" <<JSON
{"prefix": "p-", "repository": "r", $kitref, "layers": [{"id": "layer-x", "file": "layer-x.json", "global": true}],
 "agents": [{"id": "agent-flow-sync", "from": "kit", "addResources": ["layer-x", "skill-docs#docs.when"], "model": "opus"}]}
JSON
echo '{"resources": [{"key": "x.k", "convention": "Quy ước x.", "global": true, "provenance": {"owner": "test", "source": "test"}}]}' > "$tmp/p/layer-x.json"
$pub "$tmp/p/agent.json" --check > "$tmp/o" 2>&1 && ok "agent from kit + addResources hợp lệ" || bad "agent from kit: $(cat "$tmp/o")"
python3 - "$kit/scripts/aw-publish.py" "$tmp/p/agent.json" <<'PY' && ok "addResources được gộp vào resources của mẫu, không trùng" || bad "addResources không gộp đúng"
import importlib.util, sys
spec = importlib.util.spec_from_file_location("ap", sys.argv[1]); ap = importlib.util.module_from_spec(spec); spec.loader.exec_module(ap)
agent = ap.load_manifest(sys.argv[2])["agents"][0]
res = agent["resources"]
assert res[-1] == "layer-x" and res.count("skill-docs#docs.when") == 1 and "skill-feature-flow#flow.sync" in res, res
assert agent["model"] == "opus" and "addResources" not in agent
PY
sed 's/"layer-x", "skill-docs#docs.when"/"layer-khong-co"/' "$tmp/p/agent.json" > "$tmp/p/agent-bad.json"
expect_err "addResources trỏ tới resource không có" "layer-khong-co" "$tmp/p/agent-bad.json"
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
expect_err "cấu trúc chỉ có ở công cụ gốc bị chặn (/ck:)" "cấu trúc chỉ có ở công cụ gốc" "$tmp/k1/kit.json"
cp -R "$kit" "$tmp/k2"; mutate "$tmp/k2" 'd["resources"][0]["instruction"] += " Dùng AskUserQuestion để hỏi."'
expect_err "AskUserQuestion bị chặn" "AskUserQuestion" "$tmp/k2/kit.json"
cp -R "$kit" "$tmp/k3"; mutate "$tmp/k3" 'd["resources"][0]["instruction"] += " Tiếp theo." * 400'
expect_err "resource quá lớn bị chặn" "quá 3072" "$tmp/k3/kit.json"
cp -R "$kit" "$tmp/k11"; mutate "$tmp/k11" 'entry["license"] = "UNKNOWN"'
$pub "$tmp/k11/kit.json" --check 2>&1 | grep -q "giấy phép chưa xác định" && ok "license UNKNOWN chỉ cảnh báo khi --check" || bad "không cảnh báo license UNKNOWN"
# nội dung đã chắt lọc của kit không mang tên nguồn (kit tự đứng được, dùng cho project khác)
leak=$(grep -rIl -i "claudekit" "$kit" --exclude-dir=bench --exclude-dir=tests --exclude-dir=drafts 2>/dev/null | head -n 3)
[ -z "$leak" ] && ok "kit không chứa tên công cụ nguồn (kit.json, skills, layers, scripts, README)" || bad "còn tên công cụ nguồn trong: $leak"
cp -R "$kit" "$tmp/k10"; mutate "$tmp/k10" 'd["resources"][0]["selector"] = {}; d["resources"][0].pop("global", None)'
expect_err "resource không selector mà thiếu global:true" 'phải khai "global": true' "$tmp/k10/kit.json"
# Layer dùng trường "convention", Skill dùng "instruction": aw từ chối trường lạ lúc publish nên --check phải bắt sớm
cp -R "$kit" "$tmp/k12"
python3 - "$tmp/k12/layers/layer-react-vite.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1], encoding="utf-8"))
d["resources"][0]["instruction"] = d["resources"][0].pop("convention")
json.dump(d, open(sys.argv[1], "w", encoding="utf-8"), ensure_ascii=False)
PY
expect_err "Layer dùng trường instruction thay vì convention" 'nằm ở trường "convention"' "$tmp/k12/kit.json"
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
assert '"origin": "mau@1"' in report and "redistributable" not in report
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

echo "== workflow mẫu và expect-fail (V10-14)"
mkdir -p "$tmp/ef/bin" "$tmp/ef/wt/backend"
touch "$tmp/ef/wt/backend/pom.xml"
cat > "$tmp/ef/bin/mvn" <<'MVN'
#!/bin/sh
case "$FAKE_MVN" in
  green) exit 0 ;;
  red) echo "[ERROR] Tests run: 3, Failures: 1, Errors: 0, Skipped: 0"; echo "[ERROR]   IssueServiceTest.reopen:42 expected: <OPEN> but was: <CLOSED>"; exit 1 ;;
  context) echo "[ERROR] Tests run: 1, Failures: 0, Errors: 1, Skipped: 0 <<< FAILURE! -- in AppTests"; echo "java.lang.IllegalStateException: Failed to load ApplicationContext"; echo "Caused by: org.flywaydb.core.api.FlywayException: Found non-empty schema without schema history table"; echo "Caused by: org.flywaydb.core.api.FlywayException: Found non-empty schema without schema history table"; exit 1 ;;
  compile) echo "[ERROR] COMPILATION ERROR :"; echo "[ERROR] cannot find symbol"; exit 1 ;;
  net) echo "PKIX path building failed"; exit 1 ;;
esac
MVN
chmod +x "$tmp/ef/bin/mvn"
ef() { (cd "$tmp/ef/wt" && AW_KIT="$kit" FAKE_MVN="$1" PATH="$tmp/ef/bin:$PATH" sh "$kit/commands/expect-fail.sh" 2>&1); }
mt() { (cd "$tmp/ef/wt" && AW_KIT="$kit" FAKE_MVN="$1" PATH="$tmp/ef/bin:$PATH" sh "$kit/commands/maven-test.sh" 2>&1); }
out=$(mt context) && bad "maven-test: context lỗi phải không đạt" || { printf '%s' "$out" | grep -q "Caused by: org.flywaydb" && [ "$(printf '%s' "$out" | grep -c 'Caused by')" = 1 ] && ok "maven-test: in nguyên nhân gốc (Caused by), bỏ trùng, không chỉ danh sách test đỏ" || bad "maven-test nguyên nhân: $out"; }
out=$(ef red) && printf '%s' "$out" | grep -q "ĐỎ như mong đợi" && ok "expect-fail: test đỏ thì đạt, in các dòng thất bại" || bad "expect-fail test đỏ: $out"
out=$(ef green) && bad "expect-fail: test xanh phải không đạt" || { printf '%s' "$out" | grep -q "chưa chạm tới lỗi" && ok "expect-fail: test xanh thì không đạt, dặn sửa test" || bad "expect-fail test xanh: $out"; }
out=$(ef compile) && bad "expect-fail: lỗi biên dịch phải không đạt" || { printf '%s' "$out" | grep -q "không biên dịch" && ok "expect-fail: lỗi biên dịch không được tính là test đỏ" || bad "expect-fail biên dịch: $out"; }
out=$(ef net) && bad "expect-fail: lỗi mạng phải không đạt" || { printf '%s' "$out" | grep -q "^MÔI TRƯỜNG:" && ok "expect-fail: lỗi mạng báo là MÔI TRƯỜNG" || bad "expect-fail mạng: $out"; }
out=$(cd "$tmp/ef" && AW_KIT="$kit" sh "$kit/commands/expect-fail.sh" 2>&1) && bad "expect-fail: thiếu backend/pom.xml phải không đạt" || ok "expect-fail: không nhận ra stack thì không đạt"
# diff-size, temp-artifacts, check-plan (V10-15) trên một repository Git tạm
g="$tmp/hyg"; mkdir -p "$g/src" && git -C "$g" init -q -b main && git -C "$g" config user.email t@example.invalid && git -C "$g" config user.name t
echo "class A {}" > "$g/src/A.java"; git -C "$g" add -A && git -C "$g" commit -q -m init
cmdrun() { (cd "$g" && AW_KIT="$kit" sh "$kit/commands/$1" 2>&1); }
out=$(cmdrun diff-size.sh) && ok "diff-size: không đổi gì thì trong ngưỡng" || bad "diff-size sạch: $out"
out=$(cmdrun temp-artifacts.sh) && ok "temp-artifacts: không có dòng thêm thì đạt" || bad "temp-artifacts sạch: $out"
seq 1 30 | sed 's/^/int x/' > "$g/src/Big.java"
out=$(cd "$g" && AW_DIFF_MAX_LOC=20 AW_KIT="$kit" sh "$kit/commands/diff-size.sh" 2>&1) && bad "diff-size: vượt ngưỡng phải không đạt" || { printf '%s' "$out" | grep -q "FIX: làm gọn" && ok "diff-size: vượt ngưỡng thì không đạt, dặn làm gọn" || bad "diff-size vượt: $out"; }
mkdir -p "$g/docs"; seq 1 500 > "$g/docs/long.md"; rm "$g/src/Big.java"
out=$(cd "$g" && AW_DIFF_MAX_LOC=20 AW_KIT="$kit" sh "$kit/commands/diff-size.sh" 2>&1) && ok "diff-size: tài liệu không tính vào ngưỡng" || bad "diff-size tài liệu: $out"
printf 'class B {\n  void f() { System.out.println("x"); }\n  // TODO later\n  @Disabled void t() {}\n}\n' > "$g/src/B.java"; echo 'it.only("a", () => {});' > "$g/src/a.test.js"
out=$(cmdrun temp-artifacts.sh) && bad "temp-artifacts: rác phải không đạt" || { n=$(printf '%s' "$out" | grep -c '^WHAT:'); [ "$n" = 4 ] && printf '%s' "$out" | grep -q "^FIX:" && ok "temp-artifacts: bắt đủ 4 loại rác, có WHAT/WHY/FIX" || bad "temp-artifacts rác ($n loại): $out"; }
git -C "$g" add -A && git -C "$g" commit -q -m "ghi nhận rác cũ"
out=$(cmdrun temp-artifacts.sh) && ok "temp-artifacts: code cũ đã commit không bị tính" || bad "temp-artifacts code cũ: $out"
d="$g/docs/features/f/tasks/T-01"; mkdir -p "$d"
out=$(cmdrun check-plan.sh) && bad "check-plan: không có plan.md phải không đạt" || ok "check-plan: thiếu plan.md thì không đạt"
printf '## Tệp sẽ sửa\nx\n## Các bước\nx\n' > "$d/plan.md"
out=$(cmdrun check-plan.sh) && bad "check-plan: plan thiếu mục phải không đạt" || { printf '%s' "$out" | grep -q 'THIẾU mục "rủi ro và hoàn tác"' && ok "check-plan: plan thiếu mục thì không đạt, nêu mục thiếu" || bad "check-plan thiếu mục: $out"; }
printf '## Tệp sẽ sửa\nx\n## Các bước\nx\n## Test cho từng AC\nx\n## Lệnh kiểm tra cuối\nx\n## Rủi ro và hoàn tác\nx\n## Tiêu chí xong\nx\n' > "$d/plan.md"
out=$(cmdrun check-plan.sh) && ok "check-plan: plan đủ sáu mục thì đạt" || bad "check-plan đủ: $out"
# khung tài liệu tính năng (skill-doc-templates): check-brainstorm, check-spec, check-feature-docs
fd="$tmp/fdocs"; mkdir -p "$fd" && git -C "$fd" init -q -b main && git -C "$fd" config user.email t@example.invalid && git -C "$fd" config user.name t
touch "$fd/x" && git -C "$fd" add -A && git -C "$fd" commit -q -m init
mkdir -p "$fd/docs/features/f" && cp "$kit"/tests/fixtures/docs-good/* "$fd/docs/features/f/"
drun() { (cd "$fd" && AW_KIT="$kit" sh "$kit/commands/$1" 2>&1); }
out=$(drun check-brainstorm.sh) && ok "check-brainstorm: tài liệu đủ khung thì đạt" || bad "check-brainstorm đủ: $out"
out=$(drun check-spec.sh) && ok "check-spec: tài liệu đủ khung thì đạt" || bad "check-spec đủ: $out"
out=$(drun check-feature-docs.sh) && ok "check-feature-docs: thiết kế đủ khung, AC khớp thì đạt" || bad "check-feature-docs đủ: $out"
cp "$fd/docs/features/f/01-brainstorm.md" "$tmp/b.bak"; sed -i '/^### P-2/,/^Ưu: không migration/d' "$fd/docs/features/f/01-brainstorm.md"
out=$(drun check-brainstorm.sh) && bad "check-brainstorm: chỉ một phương án phải không đạt" || { printf '%s' "$out" | grep -q 'ít nhất 2' && printf '%s' "$out" | grep -q '^WHY:' && ok "check-brainstorm: thiếu phương án thì không đạt, có WHY/FIX" || bad "check-brainstorm thiếu P: $out"; }
cp "$tmp/b.bak" "$fd/docs/features/f/01-brainstorm.md"
cp "$fd/docs/features/f/02-spec.md" "$tmp/s.bak"; sed -i 's/^- AC-2: When POST nội dung 2001 ký tự, Then 400./- AC-2: POST nội dung 2001 ký tự bị từ chối./; /^## Hạn chế đã biết/,/^Chưa phân trang./d' "$fd/docs/features/f/02-spec.md"
out=$(drun check-spec.sh) && bad "check-spec: AC thiếu When/Then và thiếu mục hạn chế phải không đạt" || { printf '%s' "$out" | grep -q 'AC thiếu When hoặc Then.*AC-2' && printf '%s' "$out" | grep -q 'THIẾU mục "hạn chế đã biết"' && ok "check-spec: nêu AC thiếu When/Then và mục thiếu" || bad "check-spec hỏng: $out"; }
cp "$tmp/s.bak" "$fd/docs/features/f/02-spec.md"
cp "$fd/docs/features/f/03-design.md" "$tmp/d.bak"; sed -i 's/AC-2\.\.3/AC-2/; s/ Phương án đã loại: dựa vào ON DELETE CASCADE vì SQLite mặc định tắt khóa ngoại\.//' "$fd/docs/features/f/03-design.md"
out=$(drun check-feature-docs.sh) && bad "check-feature-docs: AC-3 không có test và quyết định thiếu phương án loại phải không đạt" || { printf '%s' "$out" | grep -q 'THIẾU test cho AC-3' && printf '%s' "$out" | grep -q 'THIẾU phương án bị loại' && ok "check-feature-docs: nêu AC chưa có test và quyết định thiếu phương án loại" || bad "check-feature-docs hỏng: $out"; }
cp "$tmp/d.bak" "$fd/docs/features/f/03-design.md"
sed -i 's/AC-2: quá dài/AC-9: không có trong spec/' "$fd/docs/features/f/tasks.json"
out=$(drun check-feature-docs.sh) && bad "check-feature-docs: AC của task không có trong spec phải không đạt" || { printf '%s' "$out" | grep -q 'SAI: AC-9 trong' && ok "check-feature-docs: AC của tasks.json phải có trong spec" || bad "check-feature-docs AC lạ: $out"; }
# check-lessons (V10-16)
l="$tmp/les"; mkdir -p "$l/docs/lessons" && git -C "$l" init -q -b main && git -C "$l" config user.email t@example.invalid && git -C "$l" config user.name t
touch "$l/x" && git -C "$l" add -A && git -C "$l" commit -q -m init
lrun() { (cd "$l" && AW_KIT="$kit" sh "$kit/commands/check-lessons.sh" 2>&1); }
out=$(lrun) && bad "check-lessons: không có file bài học phải không đạt" || ok "check-lessons: thiếu file thì không đạt"
cp "$kit/bench/lessons/2026-10-07-A0-A1.md" "$l/docs/lessons/2026-10-07.md"
out=$(lrun) && ok "check-lessons: ví dụ thật từ A0/A1 đạt" || bad "check-lessons ví dụ: $out"
sed -i '/^- Mức: GUIDANCE/d' "$l/docs/lessons/2026-10-07.md"
out=$(lrun) && bad "check-lessons: thiếu Mức phải không đạt" || { printf '%s' "$out" | grep -q 'THIẾU "Mức"' && ok "check-lessons: thiếu trường thì không đạt, nêu bài học và trường" || bad "check-lessons thiếu Mức: $out"; }
# lessons-input trên database giả lập của một lượt chạy
KITDIR="$kit" python3 - "$tmp/run-fake" <<'PY' && ok "kit-metrics lessons-input: tóm tắt vòng sửa, gate đỏ, nhận xét reviewer" || bad "kit-metrics lessons-input"
import json, os, sqlite3, subprocess, sys
run = sys.argv[1]; os.makedirs(os.path.join(run, "install"))
db = sqlite3.connect(os.path.join(run, "install", "aw.db"))
db.executescript("""create table workflow_runs(id text, work_item_id text);
create table node_runs(id text, run_id text, node_key text, iteration int, selected_outcome text, activation_sequence int);
create table execution_attempts(id text, node_run_id text, state text, failure_code text, provider_key text);
create table agent_events(attempt_id text, sequence int, kind text, payload_json text);
insert into workflow_runs values ('r1','w1');
insert into node_runs values ('n1','r1','gate1',0,'failed',1),('n2','r1','build',1,'done',2),('n3','r1','ai-review',0,'rework',3);
insert into execution_attempts values ('a1','n2','FAILED','SCOPE_VIOLATION','claude'),('a2','n3','SUCCEEDED',null,'claude');
insert into agent_events values ('a2',1,'ASSISTANT_MESSAGE','{"message":"[Important] Foo.java:1 thiếu test"}');""")
db.commit(); db.close()
out = subprocess.run([sys.executable, os.path.join(sys.argv[0] if False else os.environ["KITDIR"], "scripts", "kit-metrics.py"), "lessons-input", run],
                     capture_output=True, text=True).stdout
for needle in ("build 1", "gate1 1", "build: SCOPE_VIOLATION ×1", "[Important] Foo.java:1 thiếu test"):
    assert needle in out, (needle, out)
PY
out=$(python3 "$kit/tests/test-lessons.py" 2>&1) && ok "lessons-to-skill: định dạng, gộp, revision, gắn vào agent, publish version mới ($(printf '%s' "$out" | grep -c '^  ok') kiểm tra)" || bad "test-lessons: $out"
if [ -n "${AW:-}" ] || command -v aw > /dev/null 2>&1 || command -v go > /dev/null 2>&1; then
  for s in plus-debug bugfix-not-red fd-spec-exhaust; do
    out=$(python3 "$kit/scripts/walk-workflow.py" "$kit/tests/walk/scenarios/$s.json" 2>&1) && ok "walk-workflow: $s đi đúng thứ tự node" || bad "walk-workflow $s: $out"
  done
  if [ -n "${WALK_ALL:-}" ]; then
    for s in "$kit"/tests/walk/scenarios/*.json; do
      out=$(python3 "$kit/scripts/walk-workflow.py" "$s" 2>&1) && ok "walk-workflow: $(basename "$s" .json)" || bad "walk-workflow $(basename "$s"): $out"
    done
  fi
else
  echo "  bỏ qua walk-workflow (không có aw hay go); WALK_ALL=1 chạy hết kịch bản"
fi

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
