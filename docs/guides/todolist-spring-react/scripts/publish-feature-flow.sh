#!/usr/bin/env bash
# Publish quy trình hai tầng TÍNH NĂNG → TASK (feature-task-flow.md):
#   Skill skill-feature-flow (hướng dẫn từng giai đoạn) + scripts-feature-flow (script COMMAND),
#   7 context policy + 7 agent profile (mỗi giai đoạn một "tuyến" context riêng),
#   Command cmd-check-feature-docs, cmd-gate1, Policy policy-completion-feature,
#   Workflow wf-feature-definition (WF_FEATURE) và wf-task-delivery (WF_TASK).
#
# Chạy SAU publish-definitions.sh (dùng lại Layer, skill-todolist-dev, policy, cmd-reject, adapter build
# trong aw-ids.env). Cùng biến môi trường với publish-definitions.sh. Chạy lại an toàn (idempotent).
set -euo pipefail

: "${AW_DB:?}" "${AW_ARTIFACT_ROOT:?}" "${AW_WORKSPACE_ROOT:?}"
AW=${AW:-aw}
REPO_ID=${REPO_ID:-todolist}
CLAUDE_MODEL=${CLAUDE_MODEL:-sonnet}
OUT_ENV=${OUT_ENV:-./aw-ids.env}
AW_OS=$("$AW" version --json | jq -er '.os')

HERE=$(cd "$(dirname "$0")" && pwd)
DEFS="$HERE/../definitions"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
# shellcheck source=lib.sh
. "$HERE/lib.sh"
# shellcheck disable=SC1090
. "$OUT_ENV"
: "${PROJECT_ID:?}" "${LAYER_SPRING:?chạy publish-definitions.sh trước}" "${SKILL_DEV:?}" "${POL_NETWORK:?}" \
  "${CMD_REJECT:?}" "${ADAPTER_BUILD:?}" "${POL_ATTEMPT:?}" "${POL_PERMISSION:?}" "${POL_COMPLETION_REVIEWED:?}"

FLOW_DOC="$DEFS/skills/skill-feature-flow.json"

echo "== Skill quy trình tính năng/task"
create_def SKILL skill-feature-flow "Skill: quy trình tính năng → task"
SKILL_FLOW=$(publish_def SKILL skill-feature-flow "$FLOW_DOC")

echo "== Skill script cho COMMAND"
scripts_skill_doc "$HERE/check-feature-docs.sh" "$HERE/gate1.sh" > "$WORK/scripts-feature-flow.json"
create_def SKILL scripts-feature-flow "Script kiểm tra của quy trình tính năng/task"
SKILL_FLOW_SCRIPTS=$(publish_def SKILL scripts-feature-flow "$WORK/scripts-feature-flow.json")

echo "== Context policy + agent profile cho từng giai đoạn"
layers() {
  resource_refs "$LAYER_SPRING" "$DEFS/layers/layer-java-spring-boot.json"
  resource_refs "$LAYER_SQLITE" "$DEFS/layers/layer-sqlite.json"
  resource_refs "$LAYER_REACT" "$DEFS/layers/layer-react-vite.json"
}
# stage STAGE KEY... — CONTEXT policy chỉ gồm các resource flow.* đã liệt kê (+ Layer/skill dev nếu cần),
# rồi agent profile agent-flow-STAGE trỏ tới nó. Đây là "bảng định tuyến" theo node của workflow.
stage() {
  local name=$1 with_layers=$2 with_dev=$3
  shift 3
  {
    resource_refs "$SKILL_FLOW" "$FLOW_DOC" "$@"
    if [ "$with_layers" = yes ]; then layers; fi
    if [ "$with_dev" = yes ]; then
      resource_refs "$SKILL_DEV" "$DEFS/skills/skill-todolist-dev.json" dev.definition-of-done dev.high-risk-extra
    fi
  } | jq -s --arg sel "flow-$name" '{category: "CONTEXT",
        context: {selector: [$sel], budget: {maxTokens: 65536}, resourceRefs: add}}' > "$WORK/ctx-$name.json"
  create_def POLICY "ctx-flow-$name" "Context: giai đoạn $name"
  local ctx
  ctx=$(publish_def POLICY "ctx-flow-$name" "$WORK/ctx-$name.json")
  agent_profile "ctx-flow-$name" "$ctx" > "$WORK/agent-$name.json"
  create_def AGENT_PROFILE "agent-flow-$name" "Agent: giai đoạn $name"
  local upper agent
  upper=$(echo "$name" | tr '[:lower:]' '[:upper:]')
  agent=$(publish_def AGENT_PROFILE "agent-flow-$name" "$WORK/agent-$name.json")
  set_env "CTX_FLOW_$upper" "$ctx"
  set_env "AGENT_FLOW_$upper" "$agent"
}
#     giai đoạn   Layer dev  resource của skill-feature-flow
stage brainstorm  no    no   flow.outcome-protocol flow.brainstorm
stage spec        no    no   flow.outcome-protocol flow.needs-info flow.spec
stage design      yes   no   flow.outcome-protocol flow.design
stage frame       no    no   flow.outcome-protocol flow.needs-info flow.frame
stage plan        yes   no   flow.outcome-protocol flow.routing-table flow.plan
stage build       yes   yes  flow.outcome-protocol flow.needs-info flow.build
stage sync        no    no   flow.outcome-protocol flow.sync

echo "== Command và completion policy"
command_doc "$SKILL_FLOW_SCRIPTS" "$WORK/scripts-feature-flow.json" check-feature-docs.sh NONE > "$WORK/cmd-check.json"
command_doc "$SKILL_FLOW_SCRIPTS" "$WORK/scripts-feature-flow.json" gate1.sh ALLOWED > "$WORK/cmd-gate1.json"
create_def COMMAND cmd-check-feature-docs "Command: kiểm tra tài liệu tính năng"
create_def COMMAND cmd-gate1 "Command: GATE 1 build + test"
create_def POLICY policy-completion-feature "Completion: kiểm tra tài liệu + người duyệt"
CMD_CHECK_FEATURE_DOCS=$(publish_def COMMAND cmd-check-feature-docs "$WORK/cmd-check.json")
CMD_GATE1=$(publish_def COMMAND cmd-gate1 "$WORK/cmd-gate1.json")
POL_COMPLETION_FEATURE=$(publish_def POLICY policy-completion-feature "$DEFS/policies/policy-completion-feature.json")
for name in SKILL_FLOW SKILL_FLOW_SCRIPTS CMD_CHECK_FEATURE_DOCS CMD_GATE1 POL_COMPLETION_FEATURE; do
  set_env "$name" "${!name}"
done

echo "== Workflow"
OUT_ENV="$OUT_ENV" AW="$AW" "$HERE/publish-workflow.sh" "$DEFS/workflows/wf-feature-definition.json" \
  wf-feature-definition WF_FEATURE "Workflow: định nghĩa tính năng"
OUT_ENV="$OUT_ENV" AW="$AW" "$HERE/publish-workflow.sh" "$DEFS/workflows/wf-task-delivery.json" \
  wf-task-delivery WF_TASK "Workflow: giao một task"
echo "Xong. Version id đã ghi vào $OUT_ENV"
