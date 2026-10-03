// V9-03 — instruction artifact schema v2
// (docs/design/12-v9-harness-alignment.md V9-03; ADR-032 in
// docs/architecture/02-architecture-decisions.md; gap G3 in
// docs/harness-engineering/15-doi-chieu-v9.md).
//
// The instruction a provider receives is one JSON document,
// AssembleAgentExecutionRequest's Prompt and its pinned InstructionArtifact.
// Schema v1 (instructionArtifactContent, assemble_execution_request.go) was
// {taskContract, checkFailures?, messages, resources}: no resource said how
// binding it was, HARD_CONSTRAINT resources sat after every message, the last
// thing in the prompt was REFERENCE material, and nothing told the agent which
// outcomes it may report even though a node with more than one outcome
// REQUIRES a marker (agent_node_executor.go resolveSelectedOutcome).
//
// Schema v2 fixes the order and adds what the agent needs to act on it. The
// keys appear in exactly this order (encoding/json writes struct fields in
// declaration order, so the order is the order of instructionArtifactV2's
// fields and of the types below):
//
//	schemaVersion      2
//	hardConstraints    [] resources whose priority is HARD_CONSTRAINT
//	taskContract       workItemId, title, behavior, acceptanceCriteria,
//	                   verificationSpec, riskLevel, allowedOutcomes,
//	                   outcomeProtocol (only when there is more than one outcome)
//	checkFailures      [] only for a maker sent back by a failing check (V9-02)
//	resources          [] every other resource, each with its priority
//	messages           []
//	omittedMessages    [] only when the context policy's messages budget left
//	                   some out (V9-07): messageId, sequence, actor, role,
//	                   createdAt, reason — a reference, never the content
//	closingChecklist   hardConstraintKeys, allowedOutcomes
//
// The rules that must hold come first, and are repeated by key at the very end
// together with the outcomes, because the start and the end of a long prompt
// are what an agent attends to ("lost in the middle", lecture 04). The
// V9-02 checkFailures section is placed straight after the task contract: it is
// the most specific instruction a maker sent back by a check has, and it must
// not be buried under reference material.
//
// Which schema renders is a fact recorded on the ContextSnapshot
// (contextsnapshot.Snapshot.InstructionSchemaVersion, migration 0044): the same
// snapshot always yields the same bytes and hash (V5-08B0), and a snapshot that
// recorded nothing renders as v1 exactly as it did before this file existed.
package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// instructionOutcomeProtocol is the fixed, engine-owned text of
// taskContract.outcomeProtocol (ADR-032 decision 4): how an agent reports the
// outcome it chose. It describes the marker the provider adapters already
// parse (internal/adapters/providers/claude and codex, extractOutcomeMarker and
// resolveProposedOutcome; ports.AgentProposedOutcome) and nothing more —
// the parser is unchanged:
//
//   - the marker is the last content, outside whitespace, of the FINAL assistant
//     message, so it ends the message;
//   - exactly one marker is accepted across the whole execution — a second one
//     in an earlier message is a duplicate and is rejected, not "last wins";
//   - its body is the JSON object {"schemaVersion":1,"outcome":"<name>"};
//   - a missing marker (when more than one outcome is allowed), a malformed
//     one, or an outcome that is not in the allowed list is a protocol error
//     and fails the attempt.
//
// It is the same bytes for every node, not rendered per node: no per-outcome
// descriptions yet (ADR-032 "Không làm"). It is only included when the node has
// more than one selectable outcome, because with exactly one the engine derives
// the outcome itself (AgentOutcomeDerivedSingleAllowed) and asks for no marker.
// If the marker syntax or its rules ever change, this text changes with them:
// TestInstructionOutcomeProtocol_MatchesTheMarkerTheProvidersParse pins it
// against the fixture that emits real markers.
const instructionOutcomeProtocol = "Finish your final message with exactly one outcome marker, as the very last thing you write: " +
	`<agentkit-outcome>{"schemaVersion":1,"outcome":"NAME"}</agentkit-outcome>` +
	" where NAME is one of taskContract.allowedOutcomes, spelled exactly as listed. " +
	"Write the marker once: do not end any earlier message with one. " +
	"The attempt fails if the marker is missing, repeated, malformed, or names an outcome that is not listed."

// instructionRenderInput is everything an instruction artifact is rendered
// from, already gathered inside the read-only transaction and with the
// artifact-backed text (message content, check failure summaries) already read:
// plain data, so rendering is a pure function of it and the same input is
// always the same bytes.
type instructionRenderInput struct {
	workItemID         string
	title              string
	behavior           string
	acceptanceCriteria []string
	verificationSpec   string
	riskLevel          string
	// allowedOutcomes is the node's agent-selectable outcomes, in the order the
	// pinned workflow document declares them (agentSelectableOutcomes).
	allowedOutcomes []string
	checkFailures   []instructionCheckFailure
	messages        []instructionMessage
	// omittedMessages are the messages the context policy's `messages` budget
	// left out (V9-07), rendered by schema v2 only; schema v1 snapshots are
	// never scheduled with a budget.
	omittedMessages []instructionOmittedMessage
	// resources are the snapshot's pinned resources in the snapshot's own
	// order, which for a snapshot the scheduler built is the context resolver's
	// order: HARD_CONSTRAINT first, then REQUIRED_PROCEDURE, GUIDANCE,
	// REFERENCE, and inside one priority by resourceKey, then ownerVersionId,
	// then contentHash (contextassembler.Resolve).
	resources []instructionResourceInput
}

type instructionResourceInput struct {
	ownerVersionID string
	resourceKey    string
	contentHash    string
	priority       definition.PriorityClass
	content        string
}

// renderInstructionV1 renders the v1 artifact exactly as
// AssembleAgentExecutionRequest always has: task contract, then the V9-02
// checkFailures section when there is one, then messages, then every resource
// in the snapshot's pinned order. riskLevel, allowedOutcomes and priority are
// NOT part of v1 and are not rendered. encoding/json.Marshal with its default
// HTML escaping, as before — the bytes (and so the hash) of a v1 snapshot must
// not change.
func renderInstructionV1(in instructionRenderInput) ([]byte, error) {
	content := instructionArtifactContent{Messages: []instructionMessage{}, Resources: []instructionResource{}}
	content.TaskContract.WorkItemID = in.workItemID
	content.TaskContract.Title = in.title
	content.TaskContract.Behavior = in.behavior
	content.TaskContract.VerificationSpec = in.verificationSpec
	content.TaskContract.AcceptanceCriteria = in.acceptanceCriteria
	if len(in.messages) > 0 {
		content.Messages = append(content.Messages, in.messages...)
	}
	if len(in.checkFailures) > 0 {
		// ADR-032: a snapshot without instructionSchemaVersion assembles as v1
		// exactly as before, so a v1 artifact keeps the original FIX wording
		// (TestRenderInstructionV1_ByteIdenticalToTheArtifactBeforeV903).
		content.CheckFailures = make([]instructionCheckFailure, len(in.checkFailures))
		for i, failure := range in.checkFailures {
			failure.Fix = checkFailureFixV1
			content.CheckFailures[i] = failure
		}
	}
	for _, r := range in.resources {
		content.Resources = append(content.Resources, instructionResource{
			OwnerVersionID: r.ownerVersionID, ResourceKey: r.resourceKey, ContentHash: r.contentHash, Content: r.content,
		})
	}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return nil, fmt.Errorf("runtime: marshal instruction artifact content: %w", err)
	}
	return contentJSON, nil
}

// instructionArtifactV2 is the exact shape of a v2 artifact. Field order IS
// the JSON key order; see this file's doc comment.
type instructionArtifactV2 struct {
	SchemaVersion    int                         `json:"schemaVersion"`
	HardConstraints  []instructionResourceV2     `json:"hardConstraints"`
	TaskContract     instructionTaskContractV2   `json:"taskContract"`
	CheckFailures    []instructionCheckFailure   `json:"checkFailures,omitempty"`
	Resources        []instructionResourceV2     `json:"resources"`
	Messages         []instructionMessage        `json:"messages"`
	OmittedMessages  []instructionOmittedMessage `json:"omittedMessages,omitempty"`
	ClosingChecklist instructionClosingChecklist `json:"closingChecklist"`
}

// instructionTaskContractV2 is v1's task contract plus what the agent needs to
// finish correctly: how risky the work item is and which outcomes it may
// report.
type instructionTaskContractV2 struct {
	WorkItemID         string   `json:"workItemId"`
	Title              string   `json:"title"`
	Behavior           string   `json:"behavior"`
	AcceptanceCriteria []string `json:"acceptanceCriteria,omitempty"`
	VerificationSpec   string   `json:"verificationSpec"`
	// RiskLevel is the WorkItem's own self-declared risk rating
	// (work.WorkItem.RiskLevel — the same value schedule.go resolves context
	// selectors with as RiskClass), rendered as stored.
	RiskLevel string `json:"riskLevel"`
	// AllowedOutcomes is the node's agent-selectable outcomes: the declared
	// ones minus its cycle policy's escalation outcome (agentSelectableOutcomes),
	// the same list ports.AgentExecutionRequest.AllowedOutcomes carries and the
	// provider adapter checks the marker against. Always at least one.
	AllowedOutcomes []string `json:"allowedOutcomes"`
	// OutcomeProtocol is instructionOutcomeProtocol, present only when more than
	// one outcome is allowed.
	OutcomeProtocol string `json:"outcomeProtocol,omitempty"`
}

// instructionResourceV2 is one resource with the priority its authored
// document declared. The priority is read from the pinned resource, whose
// content hash it is already part of; the snapshot's ResourceRef gained no
// field for it (ADR-032 decision 3).
type instructionResourceV2 struct {
	OwnerVersionID string `json:"ownerVersionId"`
	ResourceKey    string `json:"resourceKey"`
	Priority       string `json:"priority"`
	ContentHash    string `json:"contentHash"`
	Content        string `json:"content"`
}

// instructionClosingChecklist repeats, at the very end of the prompt, what must
// not be forgotten: the key of every HARD_CONSTRAINT (in the order hardConstraints
// lists them, one per resource) and the outcomes the agent may report.
type instructionClosingChecklist struct {
	HardConstraintKeys []string `json:"hardConstraintKeys"`
	AllowedOutcomes    []string `json:"allowedOutcomes"`
}

// instructionPriorityRank orders the non-HARD_CONSTRAINT priorities as
// resources[] lists them: REQUIRED_PROCEDURE, then GUIDANCE, then REFERENCE.
func instructionPriorityRank(p definition.PriorityClass) (int, bool) {
	switch p {
	case definition.PriorityRequiredProcedure:
		return 0, true
	case definition.PriorityGuidance:
		return 1, true
	case definition.PriorityReference:
		return 2, true
	default:
		return 0, false
	}
}

// renderInstructionV2 renders the v2 artifact.
//
// Ordering, all deterministic and documented here because the hash depends on
// it:
//   - hardConstraints: the HARD_CONSTRAINT resources in the snapshot's order;
//   - resources: every other resource, stably sorted by priority rank
//     (REQUIRED_PROCEDURE, GUIDANCE, REFERENCE), keeping the snapshot's order
//     inside one priority (for a scheduler-built snapshot: resourceKey, then
//     ownerVersionId, then contentHash);
//   - messages: the snapshot's order, which is message sequence;
//   - omittedMessages (only when the context policy's `messages` budget left
//     some out, V9-07): the snapshot's order, again message sequence, right
//     after messages;
//   - checkFailures: check node key, then attempt id (gatherCheckFailureInputs);
//   - allowedOutcomes: the workflow document's declaration order.
//
// A resource whose priority is not one of the four classes fails the render
// rather than being placed somewhere arbitrary.
//
// The document is encoded with HTML escaping off, unlike v1: the outcome
// protocol text and any markup in a resource are meant to be read by a model,
// and `<agentkit-outcome>` shown as a < escape is something it might
// copy literally into its own marker, which the parser would then reject.
func renderInstructionV2(in instructionRenderInput) ([]byte, error) {
	if len(in.allowedOutcomes) == 0 {
		return nil, fmt.Errorf("runtime: instruction schema v2 requires at least one allowed outcome")
	}
	artifact := instructionArtifactV2{
		SchemaVersion:    2,
		HardConstraints:  []instructionResourceV2{},
		Resources:        []instructionResourceV2{},
		Messages:         []instructionMessage{},
		CheckFailures:    in.checkFailures,
		OmittedMessages:  in.omittedMessages,
		ClosingChecklist: instructionClosingChecklist{HardConstraintKeys: []string{}, AllowedOutcomes: append([]string(nil), in.allowedOutcomes...)},
	}
	artifact.TaskContract = instructionTaskContractV2{
		WorkItemID: in.workItemID, Title: in.title, Behavior: in.behavior, AcceptanceCriteria: in.acceptanceCriteria,
		VerificationSpec: in.verificationSpec, RiskLevel: in.riskLevel, AllowedOutcomes: append([]string(nil), in.allowedOutcomes...),
	}
	if len(in.allowedOutcomes) > 1 {
		artifact.TaskContract.OutcomeProtocol = instructionOutcomeProtocol
	}
	if len(in.messages) > 0 {
		artifact.Messages = append(artifact.Messages, in.messages...)
	}

	type ranked struct {
		rank     int
		resource instructionResourceV2
	}
	var rest []ranked
	for _, r := range in.resources {
		resource := instructionResourceV2{
			OwnerVersionID: r.ownerVersionID, ResourceKey: r.resourceKey, Priority: string(r.priority),
			ContentHash: r.contentHash, Content: r.content,
		}
		if r.priority == definition.PriorityHardConstraint {
			artifact.HardConstraints = append(artifact.HardConstraints, resource)
			artifact.ClosingChecklist.HardConstraintKeys = append(artifact.ClosingChecklist.HardConstraintKeys, r.resourceKey)
			continue
		}
		rank, ok := instructionPriorityRank(r.priority)
		if !ok {
			return nil, fmt.Errorf("runtime: resource %s (%s) has priority %q, not one of the four priority classes", r.resourceKey, r.ownerVersionID, r.priority)
		}
		rest = append(rest, ranked{rank: rank, resource: resource})
	}
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].rank < rest[j].rank })
	for _, r := range rest {
		artifact.Resources = append(artifact.Resources, r.resource)
	}

	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(artifact); err != nil {
		return nil, fmt.Errorf("runtime: marshal instruction artifact v2: %w", err)
	}
	// Encode terminates the value with a newline; the artifact is the bare JSON.
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}
