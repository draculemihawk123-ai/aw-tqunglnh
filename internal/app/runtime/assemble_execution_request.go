// V5-08B0 — Canonical AgentExecutionRequest assembly
// (docs/design/07-v5-execution-evidence.md, baocaov5checklist.md's own
// "Quyết định sau review source of truth — 2026-09-08"): turns a durable
// V5-04 contextsnapshot.Snapshot plus this Attempt's own admitted pins into
// exactly the ports.AgentExecutionRequest go-core-spec.md §14 requires,
// materializing a deterministic InstructionArtifact along the way.
//
// Deliberately NOT wired into ExecuteNodeHandler.Handle's own dispatch call
// (still ports.NodeExecutor, unchanged) — that bridge, and the fenced
// finalize it composes with, is V5-08B's own scope. AssembleAgentExecutionRequest
// is a standalone, independently callable/testable function a future V5-08B
// caller invokes once right before spawning a real ports.AgentExecutor, and
// invokes AGAIN as its own "revalidate everything fail-closed" step: every
// call re-loads and re-verifies from scratch, so there is no separate
// "revalidate" entry point — calling this function again fresh IS the
// revalidation, by construction.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextassembler"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// AssembleAgentExecutionRequestRequest identifies which Attempt to assemble
// a request for — the same three IDs ExecuteNodeJobPayload already carries
// together (RunID/NodeRunID/AttemptID), since NodeRun/Attempt alone cannot
// be cross-checked against their own owning Run without it.
type AssembleAgentExecutionRequestRequest struct {
	RunID     string
	NodeRunID string
	AttemptID string
}

// instructionArtifactContent is the exact deterministic shape
// AssembleAgentExecutionRequest materializes into the InstructionArtifact for
// a snapshot that records no instruction schema version (schema v1) — task
// contract, then messages, then resources, each in the snapshot's own pinned
// order. Schema v2 (V9-03, ADR-032) is instructionArtifactV2 in
// instruction_artifact.go; THIS shape and its encoding must never change, so a
// v1 snapshot keeps rendering the same bytes. encoding/json.Marshal of a fixed Go value is itself
// deterministic (stable field order from the struct definition), so the
// same snapshot always produces byte-identical content and therefore the
// identical ArtifactRef.SHA256/hash — this is what "cùng snapshot tạo cùng
// instruction hash/request" (V5-08B0's own Verify line) actually rests on.
type instructionArtifactContent struct {
	TaskContract struct {
		WorkItemID         string   `json:"workItemId"`
		Title              string   `json:"title"`
		Behavior           string   `json:"behavior"`
		AcceptanceCriteria []string `json:"acceptanceCriteria,omitempty"`
		VerificationSpec   string   `json:"verificationSpec"`
	} `json:"taskContract"`
	// CheckFailures (V9-02, ADR-031 decision 6) is present only for a MAKER
	// activated through a check's failureOutcome edge: the failing check's
	// WHAT/WHY/FIX (check_failure_context.go). omitempty keeps every other
	// prompt byte-for-byte what it was.
	CheckFailures []instructionCheckFailure `json:"checkFailures,omitempty"`
	Messages      []instructionMessage      `json:"messages"`
	Resources     []instructionResource     `json:"resources"`
}

type instructionMessage struct {
	MessageID string `json:"messageId"`
	Role      string `json:"role"`
	Content   string `json:"content"`
}

// instructionOmittedMessage is how a message the context policy's `messages`
// budget left out of the prompt (V9-07) still shows up in a v2 artifact: who
// wrote it, when, and where it sits in the chat, never its content. The agent
// can ask the operator for it, and the snapshot's OmittedMessageRefs say why
// it is not here.
type instructionOmittedMessage struct {
	MessageID string `json:"messageId"`
	Sequence  uint64 `json:"sequence"`
	Actor     string `json:"actor"`
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt"`
	Reason    string `json:"reason"`
}

type instructionResource struct {
	OwnerVersionID string `json:"ownerVersionId"`
	ResourceKey    string `json:"resourceKey"`
	ContentHash    string `json:"contentHash"`
	Content        string `json:"content"`
}

// AssembleAgentExecutionRequest resolves req's own Attempt into a real
// ports.AgentExecutionRequest. store is a separate parameter from uow
// deliberately: every ArtifactStore call here (message content Open, the
// InstructionArtifact's own Put/Verify) runs OUTSIDE any transaction
// (docs/architecture/04-go-core-spec.md §11.1: "Không gọi ... filesystem
// artifact store ... trong transaction") — Phase 1 below gathers every
// DB-row fact it needs inside one read-only transaction, then Phase 2 does
// all real I/O after that transaction has already closed.
func AssembleAgentExecutionRequest(
	ctx context.Context, uow ports.UnitOfWork, store ports.ArtifactStore, req AssembleAgentExecutionRequestRequest,
) (ports.AgentExecutionRequest, error) {
	if req.RunID == "" || req.NodeRunID == "" || req.AttemptID == "" {
		return ports.AgentExecutionRequest{}, fmt.Errorf("runtime: AssembleAgentExecutionRequest requires RunID, NodeRunID and AttemptID")
	}

	var gathered assembledRequestInputs
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		gathered, err = gatherAssembledRequestInputs(ctx, tx, req)
		return err
	})
	if err != nil {
		return ports.AgentExecutionRequest{}, err
	}

	// Phase 2: real I/O, entirely outside the transaction above.
	input := instructionRenderInput{
		workItemID: gathered.workItemID, title: gathered.workItemTitle, behavior: gathered.workItemBehavior,
		acceptanceCriteria: gathered.workItemAcceptanceCriteria, verificationSpec: gathered.workItemVerificationSpec,
		riskLevel: gathered.workItemRiskLevel, allowedOutcomes: gathered.allowedOutcomes,
		omittedMessages: gathered.omittedMessages,
	}
	for _, m := range gathered.messages {
		body, err := readArtifact(ctx, store, m.ref)
		if err != nil {
			return ports.AgentExecutionRequest{}, fmt.Errorf("runtime: open message %s content artifact: %w", m.messageID, err)
		}
		input.messages = append(input.messages, instructionMessage{MessageID: m.messageID, Role: m.role, Content: body})
	}
	if len(gathered.checkFailures) > 0 {
		input.checkFailures, err = renderCheckFailures(ctx, store, gathered.checkFailures)
		if err != nil {
			return ports.AgentExecutionRequest{}, err
		}
	}
	for _, r := range gathered.resources {
		input.resources = append(input.resources, instructionResourceInput{
			ownerVersionID: r.Identity.OwnerVersionID, resourceKey: r.Identity.ResourceKey, contentHash: r.Identity.ContentHash,
			priority: r.Priority, content: string(r.Payload),
		})
	}

	// V9-03 (ADR-032 decision 2): the snapshot, not this build, says which
	// schema renders it. A snapshot that recorded none is v1 and renders
	// exactly as it always has.
	var contentJSON []byte
	switch gathered.instructionSchema {
	case contextsnapshot.InstructionSchemaV1:
		contentJSON, err = renderInstructionV1(input)
	case contextsnapshot.InstructionSchemaV2:
		contentJSON, err = renderInstructionV2(input)
	default:
		err = fmt.Errorf("runtime: snapshot %s records instruction schema version %d, which this build cannot render", gathered.snapshotID, gathered.instructionSchema)
	}
	if err != nil {
		return ports.AgentExecutionRequest{}, err
	}
	instructionRef, err := store.Put(ctx, ports.ArtifactMetadata{ContentType: "application/json", Sensitivity: redact.Sensitive}, strings.NewReader(string(contentJSON)))
	if err != nil {
		return ports.AgentExecutionRequest{}, fmt.Errorf("runtime: put instruction artifact: %w", err)
	}
	if err := store.Verify(ctx, instructionRef); err != nil {
		return ports.AgentExecutionRequest{}, fmt.Errorf("runtime: verify instruction artifact: %w", err)
	}

	var recoveryCheckpoint *string
	if gathered.recoveryCheckpointID != "" {
		recoveryCheckpoint = &gathered.recoveryCheckpointID
	}

	return ports.AgentExecutionRequest{
		AttemptID: ports.ExecutionAttemptID(req.AttemptID),
		// ContextSnapshotID is the legacy, spike-era field
		// ports.ContextSnapshotPin's own doc comment documents as
		// coexisting with (never bridged to) the real V5-08B0 pin below —
		// set here to the SAME real snapshot's own ID (never a distinct
		// value) purely so a real ports.AgentExecutor's own validateRequest
		// (every real adapter's own non-empty-ContextSnapshotID check
		// predates V5-08B0 and was never updated to also accept the new
		// pin) has something real to check; this is not a second, separate
		// context system in use, just the one real snapshot's ID read
		// through both of this request's own historical fields at once
		// (2026-09-11: confirmed with the user — the mapping is "ID of the
		// same resolved context snapshot carried by the structured field").
		ContextSnapshotID:    domainruntime.ContextSnapshotID(gathered.snapshotID),
		ProviderKey:          ports.ProviderKey(gathered.providerKey),
		AdapterBuildID:       gathered.adapterBuildID,
		InstructionArtifact:  instructionRef,
		ContextSnapshot:      &ports.ContextSnapshotPin{ID: gathered.snapshotID, ManifestHash: gathered.snapshotManifestHash},
		EffectiveScope:       gathered.effectiveScope,
		ExecutionProfileHash: gathered.executionProfileHash,
		IsolationProfile:     gathered.isolationTier,
		AllowedCapabilities:  gathered.allowedCapabilities,
		WorkspaceMounts:      gathered.workspaceMounts,
		IdempotencyKey:       req.AttemptID,
		AllowedOutcomes:      gathered.allowedOutcomes,
		RecoveryCheckpoint:   recoveryCheckpoint,
		// Prompt is the exact bytes just Put as the InstructionArtifact
		// (contentJSON, above) — "canonical rendered content of the pinned
		// InstructionArtifact" (2026-09-11, confirmed with the user):
		// using the in-memory bytes directly, never a redundant
		// store.Open/readArtifact round trip of what this same call just
		// wrote.
		Prompt: string(contentJSON),
		// Timeout/Model come from the SAME pinned ResolvedExecutionProfileV1
		// decision every other admission-time value here already reads
		// (gathered.timeoutSeconds/model, both threaded through
		// gatherAssembledRequestInputs above) — never a fresh, live re-read
		// of current profile configuration (2026-09-11, confirmed with the
		// user: "Do not... read current profile values at dispatch time").
		Timeout: time.Duration(gathered.timeoutSeconds) * time.Second,
		Model:   gathered.model,
		// V9-05 (gap G5): the environment NAMES pinned for this NodeRun — the
		// intersection of the AgentProfileVersion's envAllowlist and the
		// operator's --env-allowlist at scheduling time — read from the same
		// pinned decision, never from the profile's or the worker's current
		// configuration. This is the pinned maximum: AgentNodeExecutor.Execute
		// narrows it by the executing worker's own allowlist before spawning,
		// because nothing at admission or execution compares that worker's
		// runtime config hash with the pinned one. Every attempt of the
		// NodeRun, retries and recovery replacements included, reads the same
		// decision and so starts from the same list. Values are not here: the
		// process supervisor reads them from the worker's environment at spawn
		// time.
		InheritedEnvironment: gathered.inheritedEnvironment,
	}, nil
}

// agentSelectableOutcomes returns node's own declared Outcomes minus its
// CyclePolicy's own EscalationOutcome (if any) — V5-08B (confirmed with
// the user 2026-09-09): the runtime ALWAYS assigns EscalationOutcome
// itself, on a SKIPPED NodeRun, the moment a cycle exhausts its own
// MaxIterations budget (advance.go's own exhausted branch) — the agent is
// never even invoked for that round, so it is never a real candidate
// AgentExecutionRequest.AllowedOutcomes ever needs to cover. Mirrors
// internal/domain/workflow's own identical validateNormalizedDocument
// check exactly (that package cannot export this as a shared helper this
// one calls — domain packages never depend on app packages — so the
// simple loop is duplicated, not re-derived differently).
func agentSelectableOutcomes(node workflow.Node) []string {
	if node.CyclePolicy == nil {
		return append([]string(nil), node.Outcomes...)
	}
	selectable := make([]string, 0, len(node.Outcomes))
	for _, outcome := range node.Outcomes {
		if outcome == node.CyclePolicy.EscalationOutcome {
			continue
		}
		selectable = append(selectable, outcome)
	}
	return selectable
}

func readArtifact(ctx context.Context, store ports.ArtifactStore, ref ports.ArtifactRef) (string, error) {
	rc, err := store.Open(ctx, ref)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// assembledRequestInputs is everything Phase 1 (the read-only transaction)
// gathers — plain data only, no I/O left to do except what Phase 2 performs
// against store.
type assembledRequestInputs struct {
	providerKey          string
	adapterBuildID       string
	snapshotID           contextsnapshot.ID
	snapshotManifestHash string
	// instructionSchema is the instruction-artifact schema the snapshot
	// renders with (contextsnapshot.Snapshot.InstructionSchema): 1 for a
	// snapshot that recorded none.
	instructionSchema    int
	effectiveScope       []workdomain.RepositoryScope
	executionProfileHash string
	// timeoutSeconds/model are the pinned AGENT execution profile's own
	// values (resolveExecutionProfile, schedule.go) — threaded through to
	// populate ports.AgentExecutionRequest.Timeout/Model for real
	// (2026-09-11: neither was previously read here, since nothing
	// consumed them before AssembleAgentExecutionRequest itself started
	// populating those two request fields). timeoutSeconds is guaranteed
	// non-zero by resolveExecutionProfile's own admission-time fail-closed
	// check (ErrAttemptPolicyRequired) — never re-validated here, the same
	// "trust what admission already enforced" discipline this function's
	// own AdapterBuild check just above does NOT extend to (that one has
	// no admission-time equivalent, see its own comment).
	timeoutSeconds      uint32
	model               string
	isolationTier       policy.IsolationTier
	allowedCapabilities []string
	workspaceMounts     []ports.AgentWorkspaceMount
	messages            []assembledMessageInput
	omittedMessages     []instructionOmittedMessage
	checkFailures       []assembledCheckFailureInput
	resources           []contextassembler.Candidate
	allowedOutcomes     []string
	// recoveryCheckpointID is V5-13's own recovery marker (2026-09-11):
	// empty for an ordinary Attempt, populated with the real Checkpoint's
	// own ID when this Attempt is a FRESH_START replacement
	// (consumeFreshStart, recovery_reaper.go, pins ExecutionAttempt.
	// LastCheckpointID for exactly this reason). Threaded into
	// ports.AgentExecutionRequest.RecoveryCheckpoint below — an opaque
	// reference only, never rendered content: the actual context this
	// Attempt runs with is ALREADY fully delivered through
	// InstructionArtifact/messages/resources (the cloned Snapshot
	// consumeFreshStart itself built), so RecoveryCheckpoint's own job is
	// narrower — telling the provider adapter "this is a recovery, here is
	// which checkpoint it recovers from," not re-delivering context a
	// second time through a different channel.
	recoveryCheckpointID string

	// inheritedEnvironment is the pinned execution profile's
	// AgentInheritedEnvironment (V9-05): names only, a copy.
	inheritedEnvironment []string

	workItemID                 string
	workItemTitle              string
	workItemBehavior           string
	workItemVerificationSpec   string
	workItemRiskLevel          string
	workItemAcceptanceCriteria []string
}

func gatherAssembledRequestInputs(ctx context.Context, tx ports.Tx, req AssembleAgentExecutionRequestRequest) (assembledRequestInputs, error) {
	attempt, err := tx.Runtime().GetExecutionAttempt(ctx, req.AttemptID)
	if err != nil {
		return assembledRequestInputs{}, fmt.Errorf("runtime: load attempt %s: %w", req.AttemptID, err)
	}
	if string(attempt.NodeRunID) != req.NodeRunID {
		return assembledRequestInputs{}, fmt.Errorf("%w: attempt %s belongs to node run %s, not %s", ErrContextSnapshotUnverified, req.AttemptID, attempt.NodeRunID, req.NodeRunID)
	}
	if attempt.ContextSnapshotID == nil {
		return assembledRequestInputs{}, fmt.Errorf("%w: attempt %s has no bound context snapshot", ErrContextSnapshotUnverified, req.AttemptID)
	}

	nodeRun, err := tx.Runtime().GetNodeRun(ctx, req.NodeRunID)
	if err != nil {
		return assembledRequestInputs{}, err
	}
	if string(nodeRun.RunID) != req.RunID {
		return assembledRequestInputs{}, fmt.Errorf("%w: node run %s belongs to run %s, not %s", ErrNodeRunMismatch, req.NodeRunID, nodeRun.RunID, req.RunID)
	}

	run, err := tx.Runtime().GetWorkflowRun(ctx, req.RunID)
	if err != nil {
		return assembledRequestInputs{}, err
	}

	snapshot, err := tx.ContextSnapshots().GetSnapshot(ctx, string(*attempt.ContextSnapshotID))
	if err != nil {
		return assembledRequestInputs{}, fmt.Errorf("%w: load snapshot %s: %v", ErrContextSnapshotUnverified, *attempt.ContextSnapshotID, err)
	}
	if string(snapshot.AttemptID) != req.AttemptID {
		return assembledRequestInputs{}, fmt.Errorf("%w: snapshot %s is bound to attempt %s, not %s", ErrContextSnapshotUnverified, snapshot.ID, snapshot.AttemptID, req.AttemptID)
	}
	if snapshot.ProjectID != run.ProjectID || snapshot.WorkItemID != run.WorkItemID {
		return assembledRequestInputs{}, fmt.Errorf("%w: snapshot %s project/work item does not match run %s", ErrContextSnapshotUnverified, snapshot.ID, req.RunID)
	}

	workItem, err := tx.Work().GetWorkItem(ctx, string(run.WorkItemID))
	if err != nil {
		return assembledRequestInputs{}, err
	}

	// V5-08B: resolve this NodeRun's own agent-selectable outcomes from
	// the pinned WorkflowVersion's own Document — re-checked EVERY time
	// this function runs (its own "revalidate everything fail-closed" doc
	// comment), so a WorkflowVersion published BEFORE internal/domain/workflow's
	// own AGENT-outcome validation rule existed can never reach a real
	// provider spawn with zero agent-selectable outcomes: this is the
	// runtime-side defense the compile-time rule alone cannot provide for
	// already-published versions.
	version, err := tx.Definitions().GetWorkflowVersion(ctx, string(run.WorkflowVersionID))
	if err != nil {
		return assembledRequestInputs{}, err
	}
	document := version.Document()
	node, ok := findNode(document, nodeRun.NodeKey)
	if !ok {
		return assembledRequestInputs{}, fmt.Errorf("runtime: node %s not found in workflow version %s", nodeRun.NodeKey, run.WorkflowVersionID)
	}
	allowedOutcomes := agentSelectableOutcomes(node)
	if len(allowedOutcomes) == 0 {
		return assembledRequestInputs{}, fmt.Errorf("runtime: node %s has no agent-selectable outcome (every declared outcome is its own CyclePolicy escalation outcome) — cannot assemble a request for it", nodeRun.NodeKey)
	}

	decision, err := tx.Runtime().GetDecisionArtifact(ctx, req.NodeRunID+"-execution-profile-v1")
	if err != nil {
		return assembledRequestInputs{}, fmt.Errorf("runtime: load execution profile decision for node run %s: %w", req.NodeRunID, err)
	}
	var profile resolvedExecutionProfileView
	if err := json.Unmarshal(decision.Result, &profile); err != nil {
		return assembledRequestInputs{}, fmt.Errorf("runtime: decode execution profile decision for node run %s: %w", req.NodeRunID, err)
	}
	// go-core-spec.md §14's own AgentExecutionRequest requires
	// AdapterBuildVersion unconditionally — unlike V5-08's own admission
	// (which still treats a nil AdapterBuild as a "legitimate deferred
	// Alpha state" for the drift check specifically), request ASSEMBLY has
	// no legitimate way to populate a required field with nothing: an
	// AGENT node with no adapter build pinned cannot produce a valid
	// request and must fail closed here.
	if profile.AdapterBuild == nil {
		return assembledRequestInputs{}, fmt.Errorf("runtime: node run %s has no pinned AdapterBuildID; AgentExecutionRequest requires one", req.NodeRunID)
	}

	messages := make([]assembledMessageInput, 0, len(snapshot.MessageRefs))
	for _, ref := range snapshot.MessageRefs {
		msg, err := tx.Messages().GetMessage(ctx, ref.MessageID)
		if err != nil {
			return assembledRequestInputs{}, fmt.Errorf("runtime: load message %s: %w", ref.MessageID, err)
		}
		if msg.WorkItemID != run.WorkItemID {
			return assembledRequestInputs{}, fmt.Errorf("runtime: message %s belongs to work item %s, not %s", ref.MessageID, msg.WorkItemID, run.WorkItemID)
		}
		art, err := tx.Artifacts().GetArtifact(ctx, string(msg.ContentArtifactID))
		if err != nil {
			return assembledRequestInputs{}, fmt.Errorf("runtime: load message %s content artifact: %w", ref.MessageID, err)
		}
		messages = append(messages, assembledMessageInput{
			messageID: ref.MessageID, role: string(msg.Role),
			ref: ports.ArtifactRef{Locator: art.Locator, SHA256: art.ContentHash, Size: art.Size, ContentType: art.MediaType, Sensitivity: art.Sensitivity, Redacted: art.Redacted},
		})
	}

	// V9-07: messages the snapshot's budget left out become references (who,
	// when), in the order the snapshot lists them. The row is read through the
	// same work-item check as an included message.
	var omittedMessages []instructionOmittedMessage
	for _, ref := range snapshot.OmittedMessageRefs {
		msg, err := tx.Messages().GetMessage(ctx, ref.MessageID)
		if err != nil {
			return assembledRequestInputs{}, fmt.Errorf("runtime: load omitted message %s: %w", ref.MessageID, err)
		}
		if msg.WorkItemID != run.WorkItemID {
			return assembledRequestInputs{}, fmt.Errorf("runtime: message %s belongs to work item %s, not %s", ref.MessageID, msg.WorkItemID, run.WorkItemID)
		}
		omittedMessages = append(omittedMessages, instructionOmittedMessage{
			MessageID: ref.MessageID, Sequence: msg.Sequence, Actor: msg.Actor, Role: string(msg.Role),
			CreatedAt: msg.CreatedAt.UTC().Format(time.RFC3339Nano), Reason: string(ref.Reason),
		})
	}

	resources := make([]contextassembler.Candidate, 0, len(snapshot.ResourceRefs))
	for _, ref := range snapshot.ResourceRefs {
		// A pre-V5-08B0 snapshot's own ResourceRef has no OwnerVersionID
		// (see contextsnapshot.ResourceRef's own doc comment) — such a
		// snapshot must never dispatch silently; fail closed rather than
		// skip it or guess an owner.
		if ref.OwnerVersionID == "" {
			return assembledRequestInputs{}, fmt.Errorf("runtime: snapshot %s resource %s has no OwnerVersionID (pre-V5-08B0 snapshot) — cannot assemble a request from it", snapshot.ID, ref.ResourceKey)
		}
		candidate, err := loadResourceCandidate(ctx, tx, policy.ResourceRef{OwnerVersionID: ref.OwnerVersionID, ResourceKey: ref.ResourceKey, ContentHash: ref.ContentHash})
		if err != nil {
			return assembledRequestInputs{}, err
		}
		resources = append(resources, candidate)
	}

	// V9-02 (ADR-031 decision 6): the Evidence a MAKER's snapshot pins is the
	// failing check that sent it back here (schedule.go,
	// gatherCheckFailureEvidenceRefs) — resolved now, rendered in Phase 2. A
	// CHECKER's snapshot also carries EvidenceRefs (its predecessors'), but
	// those are V5-12's input allowlist for a different reader and are left
	// exactly as they were.
	var checkFailures []assembledCheckFailureInput
	if profile.Role != workflow.AgentRoleChecker && len(snapshot.EvidenceRefs) > 0 {
		checkFailures, err = gatherCheckFailureInputs(ctx, tx, snapshot.EvidenceRefs)
		if err != nil {
			return assembledRequestInputs{}, err
		}
	}

	mounts := assembleWorkspaceMounts(nodeRun.EffectiveScope, snapshot.Revisions.Entries())
	// V5-12 contract 3 (2026-09-10): a CHECKER-role AGENT node's own
	// mounts are ALWAYS forced read-only, regardless of what
	// EffectiveScope itself grants — reuses gate_node_executor.go's own
	// forceReadOnlyMounts exactly (the identical "downgrade every mount's
	// Access" mechanism Gate already relies on for its own "read-only by
	// design" invariant). This is also what makes "checker never acquires
	// a WriteLease" true for free: resolveExecutionResources
	// (agent_node_executor_resources.go) only calls AcquireWriteLeases
	// for mounts whose Access is WRITE, so forcing every mount READ_ONLY
	// here leaves nothing for it to ever acquire a lease for.
	if profile.Role == workflow.AgentRoleChecker {
		mounts = forceReadOnlyMounts(mounts)
	}

	acceptance := make([]string, 0, len(workItem.AcceptanceCriteria))
	for _, c := range workItem.AcceptanceCriteria {
		acceptance = append(acceptance, fmt.Sprintf("%+v", c))
	}

	var recoveryCheckpointID string
	if attempt.LastCheckpointID != nil {
		recoveryCheckpointID = string(*attempt.LastCheckpointID)
	}

	return assembledRequestInputs{
		providerKey: attempt.ProviderKey, adapterBuildID: profile.AdapterBuild.BuildID,
		snapshotID: snapshot.ID, snapshotManifestHash: snapshot.ManifestHash, instructionSchema: snapshot.InstructionSchema(),
		effectiveScope: nodeRun.EffectiveScope, executionProfileHash: attempt.ExecutionProfileHash,
		timeoutSeconds: profile.TimeoutSeconds, model: profile.Model,
		isolationTier: profile.IsolationTier, allowedCapabilities: profile.AllowedCapabilities,
		workspaceMounts: mounts, messages: messages, omittedMessages: omittedMessages, checkFailures: checkFailures, resources: resources, allowedOutcomes: allowedOutcomes,
		workItemID: string(workItem.ID), workItemTitle: workItem.Title, workItemBehavior: workItem.Behavior,
		workItemVerificationSpec: workItem.VerificationSpec, workItemRiskLevel: string(workItem.RiskLevel), workItemAcceptanceCriteria: acceptance,
		recoveryCheckpointID: recoveryCheckpointID,
		inheritedEnvironment: append([]string(nil), profile.AgentInheritedEnvironment...),
	}, nil
}

type assembledMessageInput struct {
	messageID string
	role      string
	ref       ports.ArtifactRef
}

// assembleWorkspaceMounts pairs each distinct repository in scope with its
// exact pinned revision/generation from the snapshot's own RevisionSet —
// Handle/WorkingDirectory are left at zero value (resolving a real
// ports.WorkspaceHandle is V3's own workspace-lifecycle machinery; wiring
// that in is V5-08B's own real execution-bridge work, the identical
// deferral admission.go's buildExecutionEnvelope already documents for the
// same reason). Sorted by RepositoryID for a deterministic mount order,
// independent of EffectiveScope's own append order.
func assembleWorkspaceMounts(scope []workdomain.RepositoryScope, revisions []workspace.Revision) []ports.AgentWorkspaceMount {
	revisionByRepo := make(map[string]workspace.Revision, len(revisions))
	for _, r := range revisions {
		revisionByRepo[string(r.RepositoryID)] = r
	}
	seen := make(map[string]struct{}, len(scope))
	mounts := make([]ports.AgentWorkspaceMount, 0, len(scope))
	for _, s := range scope {
		id := string(s.RepositoryID())
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		access := ports.WorkspaceReadOnly
		if s.Access() == workdomain.RepositoryWrite {
			access = ports.WorkspaceReadWrite
		}
		mount := ports.AgentWorkspaceMount{RepositoryID: project.RepositoryID(id), Access: access}
		if rev, ok := revisionByRepo[id]; ok {
			mount.VCSObjectID = rev.VCSObjectID
			mount.WorkspaceGeneration = rev.WorkspaceGeneration
		}
		mounts = append(mounts, mount)
	}
	sort.Slice(mounts, func(i, j int) bool { return mounts[i].RepositoryID < mounts[j].RepositoryID })
	return mounts
}
