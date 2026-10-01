#!/usr/bin/env bash
# Hàm dùng chung cho publish-definitions.sh và publish-feature-flow.sh (source, không chạy trực tiếp).
# Cần: AW, OUT_ENV, REPO_ID, AW_OS, HERE (thư mục scripts), WORK (thư mục tạm) đã được đặt.

sha() { sha256sum | cut -c1-16; }

# create_def KIND ID NAME [project]  — tạo vỏ definition; bỏ qua nếu đã tồn tại.
# (Alpha: tạo trùng definitionId trả về lỗi chung "sqlite: unexpected error" thay vì CONFLICT,
#  nên kiểm tra tồn tại trước bằng `definition show`.)
create_def() {
  local kind=$1 id=$2 name=$3 project=${4:-}
  local scope=()
  [ -n "$project" ] && scope=(--project-id "$project")
  if "$AW" definition show --kind "$kind" "${scope[@]}" "$id" >/dev/null 2>&1; then return 0; fi
  jq -n --arg id "$id" --arg name "$name" '{definitionId: $id, name: $name}' \
    | "$AW" definition create --kind "$kind" "${scope[@]}" --idempotency-key "create-$id" >/dev/null
}

# publish_def KIND ID FILE [project] — publish một version, in ra version id. Idempotency key suy ra
# từ nội dung file: chạy lại với nội dung không đổi trả về đúng version cũ.
publish_def() {
  local kind=$1 id=$2 file=$3 project=${4:-}
  local scope=()
  [ -n "$project" ] && scope=(--project-id "$project")
  local key="pub-$id-$(sha < "$file")"
  "$AW" definition publish --kind "$kind" "${scope[@]}" --idempotency-key "$key" --yes --file "$file" "$id" \
    | jq -er '.result.id'
}

# resource_hash FILE KEY — content hash (ADR-012) của một resource trong document SKILL/LAYER.
resource_hash() {
  python3 "$HERE/aw-resource-hashes.py" "$1" | awk -v k="$2" '$1 == k { print $2 }'
}

# resource_refs VERSION_ID FILE [KEY...] — mảng resourceRefs cho các resource của một document
# (mọi resource nếu không liệt kê KEY).
resource_refs() {
  local version=$1 file=$2
  shift 2
  python3 "$HERE/aw-resource-hashes.py" "$file" \
    | jq -R --arg v "$version" 'split(" ") | {ownerVersionId: $v, resourceKey: .[0], contentHash: .[1]}' \
    | jq -s --args '. as $all | if ($ARGS.positional | length) == 0 then $all
        else [$all[] | select(.resourceKey as $k | $ARGS.positional | index($k))] end' "$@"
}

pin() { jq -n --arg k "$1" --arg d "$2" --arg v "$3" '{kind: $k, definitionId: $d, versionId: $v}'; }

# scripts_skill_doc FILE... — Skill document với mỗi file script là một resource (key = tên file).
scripts_skill_doc() {
  local file
  for file in "$@"; do
    jq -n --arg key "$(basename "$file")" --rawfile body "$file" '{key: $key, instruction: $body,
      priority: "REQUIRED_PROCEDURE", global: true, selector: {},
      provenance: {owner: "team-platform", source: "docs/guides/todolist-spring-react/scripts", revision: "v1"}}'
  done | jq -s '{resources: .}'
}

# command_doc SKILL_VERSION SKILL_DOC_FILE SCRIPT_KEY NETWORK(ALLOWED|NONE)
# networkAccess ALLOWED chỉ chạy được khi chính Command pin một PERMISSION policy cấp NETWORK_ACCESS
# (POL_NETWORK).
command_doc() {
  local owner=$1 doc=$2 key=$3 network=$4 policies='[]'
  [ "$network" = ALLOWED ] && policies="[$(pin POLICY policy-permission-network "$POL_NETWORK")]"
  jq -n --arg owner "$owner" --arg key "$key" --arg hash "$(resource_hash "$doc" "$key")" \
    --arg repo "$REPO_ID" --arg os "$AW_OS" --arg network "$network" --argjson policies "$policies" '{
    executable: {ownerVersionId: $owner, resourceKey: $key, contentHash: $hash},
    argv: [{kind: "LITERAL", value: "run"}], cwdRepositoryTarget: $repo, compatibility: {os: [$os]},
    envAllowlist: ["PATH", "HOME", "JAVA_HOME", "MAVEN_OPTS", "JAVA_TOOL_OPTIONS", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"],
    networkAccess: $network, policyRefs: $policies, timeoutSeconds: 1800,
    output: {captureStdout: true, captureStderr: true, maxOutputBytes: 4194304}}'
}

# agent_profile CONTEXT_POLICY_ID CONTEXT_VERSION — Agent profile Claude trỏ tới một context policy.
agent_profile() {
  jq -n --arg model "$CLAUDE_MODEL" --arg os "$AW_OS" --argjson ctx "$(pin POLICY "$1" "$2")" '{
    providerKey: "claude", model: $model, toolRefs: ["Read", "Edit", "Write", "Bash"],
    contextPolicyRef: $ctx, compatibility: {os: [$os]}, budget: {maxTokens: 200000}}'
}

# set_env NAME VALUE — ghi/ghi đè một dòng NAME=VALUE trong OUT_ENV.
set_env() {
  touch "$OUT_ENV"
  grep -v "^$1=" "$OUT_ENV" > "$OUT_ENV.tmp" || true
  echo "$1=$2" >> "$OUT_ENV.tmp"
  mv "$OUT_ENV.tmp" "$OUT_ENV"
}
