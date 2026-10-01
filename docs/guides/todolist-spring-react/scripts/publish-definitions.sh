#!/usr/bin/env bash
# Publish toàn bộ definition của hướng dẫn todolist (Layer, Skill, Engineering Pack, Policy,
# Agent Profile, Command, Workflow), đăng ký adapter build cho Claude và gán Engineering Pack
# cho component backend/frontend.
#
# Yêu cầu: bash, jq, python3, binary aw. Chạy SAU khi đã có project + repository ACTIVE.
#
# Biến môi trường:
#   AW_DB, AW_ARTIFACT_ROOT, AW_WORKSPACE_ROOT   (bắt buộc — cùng giá trị với aw serve/aw worker)
#   PROJECT_ID                                   (bắt buộc — id project todolist)
#   CLAUDE_EXECUTABLE                            (bắt buộc — đúng đường dẫn đã truyền cho --claude-executable)
#   AW            (mặc định: aw)            REPO_ID      (mặc định: todolist)
#   CLAUDE_MODEL  (mặc định: sonnet)
#   OUT_ENV       (mặc định: ./aw-ids.env — nơi ghi lại các version id đã publish)
#
# Script an toàn khi chạy lại: idempotency key được suy ra từ nội dung document, nên chạy lại với
# nội dung không đổi chỉ "replay" kết quả cũ; sửa nội dung rồi chạy lại sẽ publish version mới.
set -euo pipefail

: "${AW_DB:?}" "${AW_ARTIFACT_ROOT:?}" "${AW_WORKSPACE_ROOT:?}" "${PROJECT_ID:?}" "${CLAUDE_EXECUTABLE:?}"
AW=${AW:-aw}
REPO_ID=${REPO_ID:-todolist}
CLAUDE_MODEL=${CLAUDE_MODEL:-sonnet}
# OS và Go toolchain của chính binary aw: worker so khớp đúng hai giá trị này (cùng executable/
# protocol/capability) với adapter build đã đăng ký mỗi lần admit một AGENT node; lệch là
# ADAPTER_BUILD_DRIFT.
AW_OS=$("$AW" version --json | jq -er '.os')
AW_TOOLCHAIN=$("$AW" version --json | jq -er '.goVersion')
OUT_ENV=${OUT_ENV:-./aw-ids.env}

HERE=$(cd "$(dirname "$0")" && pwd)
DEFS="$HERE/../definitions"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# shellcheck source=lib.sh
. "$HERE/lib.sh"

echo "== Layer"
create_def LAYER layer-java-spring-boot "Layer: Java 21 + Spring Boot 3.5"
create_def LAYER layer-sqlite "Layer: SQLite + Flyway"
create_def LAYER layer-react-vite "Layer: React + TypeScript + Vite"
LAYER_SPRING=$(publish_def LAYER layer-java-spring-boot "$DEFS/layers/layer-java-spring-boot.json")
LAYER_SQLITE=$(publish_def LAYER layer-sqlite "$DEFS/layers/layer-sqlite.json")
LAYER_REACT=$(publish_def LAYER layer-react-vite "$DEFS/layers/layer-react-vite.json")

echo "== Skill (hướng dẫn cho agent)"
create_def SKILL skill-todolist-dev "Skill: quy trình phát triển todolist"
SKILL_DEV=$(publish_def SKILL skill-todolist-dev "$DEFS/skills/skill-todolist-dev.json")

echo "== Skill (script thực thi cho COMMAND node)"
# Mỗi file script thành một resource; Command tham chiếu resource theo key + content hash.
scripts_skill_doc "$HERE/backend-test.sh" "$HERE/frontend-test.sh" "$HERE/reject.sh" > "$WORK/scripts-todolist.json"
create_def SKILL scripts-todolist "Script build/test của todolist"
SKILL_SCRIPTS=$(publish_def SKILL scripts-todolist "$WORK/scripts-todolist.json")

echo "== Engineering Pack"
jq -n --argjson a "$(pin LAYER layer-java-spring-boot "$LAYER_SPRING")" --argjson b "$(pin LAYER layer-sqlite "$LAYER_SQLITE")" \
  --argjson c "$(pin SKILL skill-todolist-dev "$SKILL_DEV")" '{dependencies: [$a, $b, $c]}' > "$WORK/pack-backend.json"
jq -n --argjson a "$(pin LAYER layer-react-vite "$LAYER_REACT")" \
  --argjson c "$(pin SKILL skill-todolist-dev "$SKILL_DEV")" '{dependencies: [$a, $c]}' > "$WORK/pack-frontend.json"
create_def ENGINEERING_PACK pack-todolist-backend "Pack: todolist backend (Spring Boot + SQLite)"
create_def ENGINEERING_PACK pack-todolist-frontend "Pack: todolist frontend (React + Vite)"
PACK_BACKEND=$(publish_def ENGINEERING_PACK pack-todolist-backend "$WORK/pack-backend.json")
PACK_FRONTEND=$(publish_def ENGINEERING_PACK pack-todolist-frontend "$WORK/pack-frontend.json")

echo "== Context policy (route Layer/Skill vào prompt của agent)"
jq -s '{category: "CONTEXT", context: {selector: ["todolist-backend"], budget: {maxTokens: 65536}, resourceRefs: add}}' \
  <(resource_refs "$LAYER_SPRING" "$DEFS/layers/layer-java-spring-boot.json") \
  <(resource_refs "$LAYER_SQLITE" "$DEFS/layers/layer-sqlite.json") \
  <(resource_refs "$SKILL_DEV" "$DEFS/skills/skill-todolist-dev.json") > "$WORK/ctx-backend.json"
jq -s '{category: "CONTEXT", context: {selector: ["todolist-frontend"], budget: {maxTokens: 65536}, resourceRefs: add}}' \
  <(resource_refs "$LAYER_REACT" "$DEFS/layers/layer-react-vite.json") \
  <(resource_refs "$SKILL_DEV" "$DEFS/skills/skill-todolist-dev.json") > "$WORK/ctx-frontend.json"
jq -s '{category: "CONTEXT", context: {selector: ["todolist-fullstack"], budget: {maxTokens: 65536}, resourceRefs: add}}' \
  <(resource_refs "$LAYER_SPRING" "$DEFS/layers/layer-java-spring-boot.json") \
  <(resource_refs "$LAYER_SQLITE" "$DEFS/layers/layer-sqlite.json") \
  <(resource_refs "$LAYER_REACT" "$DEFS/layers/layer-react-vite.json") \
  <(resource_refs "$SKILL_DEV" "$DEFS/skills/skill-todolist-dev.json") > "$WORK/ctx-fullstack.json"
create_def POLICY ctx-todolist-backend "Context: backend"
create_def POLICY ctx-todolist-frontend "Context: frontend"
create_def POLICY ctx-todolist-fullstack "Context: fullstack"
CTX_BACKEND=$(publish_def POLICY ctx-todolist-backend "$WORK/ctx-backend.json")
CTX_FRONTEND=$(publish_def POLICY ctx-todolist-frontend "$WORK/ctx-frontend.json")
CTX_FULLSTACK=$(publish_def POLICY ctx-todolist-fullstack "$WORK/ctx-fullstack.json")

echo "== Policy attempt / permission / completion"
create_def POLICY policy-attempt "Attempt: 2 lần, timeout 30 phút"
create_def POLICY policy-permission "Permission: OPERATOR_TRUSTED_LOCAL"
create_def POLICY policy-permission-network "Permission: cho phép truy cập mạng (Maven/npm)"
create_def POLICY policy-completion "Completion: cần COMMAND_EXECUTION PASS"
create_def POLICY policy-completion-reviewed "Completion: test PASS + người duyệt"
POL_ATTEMPT=$(publish_def POLICY policy-attempt "$DEFS/policies/policy-attempt.json")
POL_PERMISSION=$(publish_def POLICY policy-permission "$DEFS/policies/policy-permission.json")
POL_NETWORK=$(publish_def POLICY policy-permission-network "$DEFS/policies/policy-permission-network.json")
POL_COMPLETION=$(publish_def POLICY policy-completion "$DEFS/policies/policy-completion.json")
POL_COMPLETION_REVIEWED=$(publish_def POLICY policy-completion-reviewed "$DEFS/policies/policy-completion-reviewed.json")

echo "== Agent profile"
agent_profile ctx-todolist-backend "$CTX_BACKEND" > "$WORK/agent-backend.json"
agent_profile ctx-todolist-frontend "$CTX_FRONTEND" > "$WORK/agent-frontend.json"
agent_profile ctx-todolist-fullstack "$CTX_FULLSTACK" > "$WORK/agent-fullstack.json"
create_def AGENT_PROFILE agent-backend-dev "Agent: backend developer"
create_def AGENT_PROFILE agent-frontend-dev "Agent: frontend developer"
create_def AGENT_PROFILE agent-fullstack-dev "Agent: fullstack developer"
AGENT_BACKEND=$(publish_def AGENT_PROFILE agent-backend-dev "$WORK/agent-backend.json")
AGENT_FRONTEND=$(publish_def AGENT_PROFILE agent-frontend-dev "$WORK/agent-frontend.json")
AGENT_FULLSTACK=$(publish_def AGENT_PROFILE agent-fullstack-dev "$WORK/agent-fullstack.json")

echo "== Command"
command_doc "$SKILL_SCRIPTS" "$WORK/scripts-todolist.json" backend-test.sh ALLOWED > "$WORK/cmd-backend.json"
command_doc "$SKILL_SCRIPTS" "$WORK/scripts-todolist.json" frontend-test.sh ALLOWED > "$WORK/cmd-frontend.json"
command_doc "$SKILL_SCRIPTS" "$WORK/scripts-todolist.json" reject.sh NONE > "$WORK/cmd-reject.json"
create_def COMMAND cmd-backend-test "Command: backend test"
create_def COMMAND cmd-frontend-test "Command: frontend test + build"
create_def COMMAND cmd-reject "Command: kết thúc run khi người duyệt từ chối"
CMD_BACKEND=$(publish_def COMMAND cmd-backend-test "$WORK/cmd-backend.json")
CMD_FRONTEND=$(publish_def COMMAND cmd-frontend-test "$WORK/cmd-frontend.json")
CMD_REJECT=$(publish_def COMMAND cmd-reject "$WORK/cmd-reject.json")

echo "== Adapter build cho Claude CLI"
# Dùng lại build đã đăng ký chỉ khi khớp mọi thành phần mà worker kiểm tra lúc admission,
# kể cả SHA-256 nội dung file executable (sửa wrapper/nâng cấp CLI => phải đăng ký build mới).
EXE_HASH="sha256:$(sha256sum "$CLAUDE_EXECUTABLE" | cut -d' ' -f1)"
ADAPTER_BUILD=$("$AW" adapter list --json | jq -r --arg p "$CLAUDE_EXECUTABLE" --arg t "$AW_TOOLCHAIN" --arg os "$AW_OS" \
  --arg h "$EXE_HASH" '[.builds[]? | select(.providerKey == "claude" and .executablePath == $p and .toolchain == $t
     and .os == $os and .configIdentity == "todolist-claude" and .executableContentHash == $h)][0].id // empty')
if [ -z "$ADAPTER_BUILD" ]; then
  manifest=(--supports-start --supports-resume --supports-cancel
    --canonical-event-kinds EXECUTION_STARTED,STATUS_CHANGED,ASSISTANT_MESSAGE,TOOL_CALL_STARTED,TOOL_CALL_FINISHED,USAGE_REPORTED,CHECKPOINT_PROPOSED,DIAGNOSTIC,EXECUTION_FINISHED)
  "$AW" adapter probe --json --provider-key claude --executable-path "$CLAUDE_EXECUTABLE" \
    --protocol-version claude-stream-json/v1 --os "$AW_OS" --toolchain "$AW_TOOLCHAIN" \
    --config-identity todolist-claude "${manifest[@]}" | jq '.result' > "$WORK/candidate.json"
  ADAPTER_BUILD=$("$AW" adapter register --json --yes --file "$WORK/candidate.json" "${manifest[@]}" | jq -er '.result.build.id')
fi

echo "== Gán Engineering Pack cho component"
assign_pack() {
  local component_id
  component_id=$("$AW" component list "$PROJECT_ID" | jq -er --arg n "$1" --arg r "$REPO_ID" \
    '.components[] | select(.name == $n and .repositoryId == $r) | .id')
  jq -n --arg v "$2" '{packVersionId: $v}' \
    | "$AW" pack-assignment assign --idempotency-key "assign-$1-$2" "$component_id" >/dev/null
}
assign_pack backend "$PACK_BACKEND"
assign_pack frontend "$PACK_FRONTEND"

# Ghi lại mọi version id (giữ ROOT_ID/FAMILY_ID/WF_* đã có từ lần chạy trước).
keep=""
[ -f "$OUT_ENV" ] && keep=$(grep -E '^(ROOT_ID|FAMILY_ID|WF_[A-Z0-9_]+)=' "$OUT_ENV" || true)
{
  for name in PROJECT_ID LAYER_SPRING LAYER_SQLITE LAYER_REACT SKILL_DEV SKILL_SCRIPTS PACK_BACKEND PACK_FRONTEND \
      CTX_BACKEND CTX_FRONTEND CTX_FULLSTACK POL_ATTEMPT POL_PERMISSION POL_NETWORK POL_COMPLETION POL_COMPLETION_REVIEWED \
      AGENT_BACKEND AGENT_FRONTEND AGENT_FULLSTACK CMD_BACKEND CMD_FRONTEND CMD_REJECT ADAPTER_BUILD; do
    echo "$name=${!name}"
  done
  if [ -n "$keep" ]; then echo "$keep"; fi
} > "$OUT_ENV"

echo "== Workflow mặc định (scope project, publish từ template)"
OUT_ENV="$OUT_ENV" AW="$AW" "$HERE/publish-workflow.sh" "$DEFS/workflows/wf-backend-feature.json" wf-backend-feature WF_BACKEND "Workflow: backend feature"
OUT_ENV="$OUT_ENV" AW="$AW" "$HERE/publish-workflow.sh" "$DEFS/workflows/wf-frontend-feature.json" wf-frontend-feature WF_FRONTEND "Workflow: frontend feature"

echo "Xong. Các version id đã được ghi vào $OUT_ENV"
cat "$OUT_ENV"
