#!/usr/bin/env bash
# Publish một WORKFLOW (scope project) từ file template JSON.
# Trong template, mọi chuỗi có dạng "{{TEN_BIEN}}" được thay bằng giá trị cùng tên trong
# aw-ids.env (do publish-definitions.sh ghi ra), ví dụ "{{AGENT_BACKEND}}" -> version id của
# agent-backend-dev. Version id mới của workflow được ghi lại vào aw-ids.env dưới tên <BIEN_KET_QUA>.
#
# Cách dùng: publish-workflow.sh <template.json> <workflow-id> <BIEN_KET_QUA> ["Tên hiển thị"]
set -euo pipefail
AW=${AW:-aw}
OUT_ENV=${OUT_ENV:-./aw-ids.env}
template=$1 wf_id=$2 var=$3 name=${4:-$2}
set -a
# shellcheck disable=SC1090
. "$OUT_ENV"
set +a
: "${PROJECT_ID:?}"

doc=$(mktemp)
trap 'rm -f "$doc"' EXIT
jq 'walk(if type == "string" and test("^\\{\\{[A-Z0-9_]+\\}\\}$")
         then .[2:-2] as $k | (env[$k] // error("aw-ids.env chưa có biến \($k)"))
         else . end)' "$template" > "$doc"

if ! "$AW" definition show --kind WORKFLOW --project-id "$PROJECT_ID" "$wf_id" >/dev/null 2>&1; then
  jq -n --arg id "$wf_id" --arg name "$name" '{definitionId: $id, name: $name}' \
    | "$AW" definition create --kind WORKFLOW --project-id "$PROJECT_ID" --idempotency-key "create-$wf_id" >/dev/null
fi
# validate chạy đúng các kiểm tra của publish (graph, outcome/edge, cycle, pin) mà không tạo version.
if ! "$AW" definition validate --kind WORKFLOW --project-id "$PROJECT_ID" --file "$doc" "$wf_id" > "$doc.validate" 2>&1; then
  cat "$doc.validate" >&2; rm -f "$doc.validate"; exit 1
fi
rm -f "$doc.validate"
version=$("$AW" definition publish --kind WORKFLOW --project-id "$PROJECT_ID" --yes --file "$doc" \
  --idempotency-key "pub-$wf_id-$(sha256sum < "$doc" | cut -c1-16)" "$wf_id" | jq -er '.result.id')

grep -v "^$var=" "$OUT_ENV" > "$OUT_ENV.tmp" || true
echo "$var=$version" >> "$OUT_ENV.tmp"
mv "$OUT_ENV.tmp" "$OUT_ENV"
echo "$var=$version"
