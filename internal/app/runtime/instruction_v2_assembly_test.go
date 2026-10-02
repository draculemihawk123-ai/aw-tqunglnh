package runtime_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceprovision"
	contextsnapshotpkg "github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	domainmessage "github.com/taQuangLing/agent-workflow/internal/domain/message"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// V9-03 (ADR-032) — the instruction artifact as AssembleAgentExecutionRequest
// really assembles it from a real scheduled Attempt: which schema a snapshot
// selects, that a snapshot written before V9-03 still renders as v1 byte for
// byte, and that a retry or recovery clone keeps the schema of the snapshot it
// clones. The renderers' own goldens are in
// instruction_artifact_internal_test.go.

// promptKeys returns the keys of the JSON object raw, in document order.
func promptKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		t.Fatalf("not a JSON object: %v %v", token, err)
	}
	var keys []string
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			t.Fatalf("read key: %v", err)
		}
		keys = append(keys, token.(string))
		var skipped json.RawMessage
		if err := decoder.Decode(&skipped); err != nil {
			t.Fatalf("skip value: %v", err)
		}
	}
	return keys
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assembleFor(t *testing.T, uow ports.UnitOfWork, store ports.ArtifactStore, runID, nodeRunID, attemptID string) ports.AgentExecutionRequest {
	t.Helper()
	req, err := runtime.AssembleAgentExecutionRequest(context.Background(), uow, store, runtime.AssembleAgentExecutionRequestRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID,
	})
	if err != nil {
		t.Fatalf("AssembleAgentExecutionRequest: %v", err)
	}
	return req
}

// requireArtifactIsPrompt checks the pinned InstructionArtifact holds exactly
// the bytes of Prompt and that its recorded hash is theirs.
func requireArtifactIsPrompt(t *testing.T, store ports.ArtifactStore, req ports.AgentExecutionRequest) {
	t.Helper()
	body, err := store.Open(context.Background(), req.InstructionArtifact)
	if err != nil {
		t.Fatalf("open the instruction artifact: %v", err)
	}
	defer body.Close()
	stored, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read the instruction artifact: %v", err)
	}
	if string(stored) != req.Prompt {
		t.Fatalf("the stored instruction artifact is not the prompt\nstored: %s\nprompt: %s", stored, req.Prompt)
	}
	sum := sha256.Sum256(stored)
	if want := "sha256:" + hex.EncodeToString(sum[:]); req.InstructionArtifact.SHA256 != want {
		t.Fatalf("InstructionArtifact.SHA256 = %s, want %s", req.InstructionArtifact.SHA256, want)
	}
}

func snapshotOfAttempt(t *testing.T, uow ports.UnitOfWork, attemptID string) contextsnapshotpkg.Snapshot {
	t.Helper()
	var snapshot contextsnapshotpkg.Snapshot
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		snapshot, err = tx.ContextSnapshots().GetSnapshotByAttemptID(context.Background(), attemptID)
		return err
	}); err != nil {
		t.Fatalf("GetSnapshotByAttemptID(%s): %v", attemptID, err)
	}
	return snapshot
}

// asPreV903 returns snapshot as a build from before V9-03 would have stored
// it: same identity and refs, no recorded instruction schema version.
func asPreV903(t *testing.T, snapshot contextsnapshotpkg.Snapshot) contextsnapshotpkg.Snapshot {
	t.Helper()
	legacy, err := contextsnapshotpkg.NewSnapshot(
		snapshot.ID, snapshot.ProjectID, snapshot.WorkItemID, snapshot.AttemptID,
		snapshot.MessageRefs, snapshot.ResourceRefs, snapshot.EvidenceRefs, snapshot.Revisions, snapshot.CreatedAt,
	)
	if err != nil {
		t.Fatalf("rebuild the snapshot without a schema version: %v", err)
	}
	return legacy
}

// --- scheduling and the default schema ---

func TestScheduleExecutableNodeRun_RecordsInstructionSchemaV2(t *testing.T) {
	uow, _, _, _, _, attemptID := assembleRequestFixture(t)
	snapshot := snapshotOfAttempt(t, uow, attemptID)
	if snapshot.InstructionSchemaVersion != contextsnapshotpkg.InstructionSchemaV2 {
		t.Fatalf("a snapshot created by the scheduler records instruction schema %d, want 2", snapshot.InstructionSchemaVersion)
	}
	if v1 := asPreV903(t, snapshot); v1.ManifestHash == snapshot.ManifestHash {
		t.Fatal("the v2 snapshot's manifest hash equals that of the same refs without a version")
	}
}

func TestAssembleAgentExecutionRequest_NewSnapshot_RendersSchemaV2(t *testing.T) {
	uow, _, store, runID, nodeRunID, attemptID := assembleRequestFixture(t)
	req := assembleFor(t, uow, store, runID, nodeRunID, attemptID)
	requireArtifactIsPrompt(t, store, req)

	if got, want := promptKeys(t, []byte(req.Prompt)), []string{"schemaVersion", "hardConstraints", "taskContract", "resources", "messages", "closingChecklist"}; !sameStrings(got, want) {
		t.Fatalf("prompt keys = %v, want %v", got, want)
	}
	var prompt struct {
		SchemaVersion   int                 `json:"schemaVersion"`
		HardConstraints []json.RawMessage   `json:"hardConstraints"`
		TaskContract    map[string]any      `json:"taskContract"`
		Resources       []map[string]any    `json:"resources"`
		Messages        []map[string]any    `json:"messages"`
		Closing         map[string][]string `json:"closingChecklist"`
	}
	if err := json.Unmarshal([]byte(req.Prompt), &prompt); err != nil {
		t.Fatalf("decode the prompt: %v", err)
	}
	if prompt.SchemaVersion != 2 || len(prompt.HardConstraints) != 0 {
		t.Fatalf("schemaVersion %d with %d hard constraints, want 2 with none (the fixture has one GUIDANCE resource)", prompt.SchemaVersion, len(prompt.HardConstraints))
	}
	if len(prompt.Resources) != 1 || prompt.Resources[0]["resourceKey"] != "golden-rule" || prompt.Resources[0]["priority"] != "GUIDANCE" {
		t.Fatalf("resources = %v, want the GUIDANCE golden-rule", prompt.Resources)
	}
	// The fixture's node declares exactly one outcome: it is listed, and no
	// marker protocol is described (the engine derives the outcome).
	if outcomes, _ := json.Marshal(prompt.TaskContract["allowedOutcomes"]); string(outcomes) != `["done"]` {
		t.Fatalf("taskContract.allowedOutcomes = %s, want [\"done\"]", outcomes)
	}
	if _, present := prompt.TaskContract["outcomeProtocol"]; present {
		t.Fatalf("a single-outcome node has an outcomeProtocol: %v", prompt.TaskContract["outcomeProtocol"])
	}
	if _, present := prompt.TaskContract["riskLevel"]; !present {
		t.Fatal("taskContract has no riskLevel key")
	}
	if len(prompt.Messages) != 1 || prompt.Messages[0]["content"] != "please implement the feature" {
		t.Fatalf("messages = %v, want the one appended message", prompt.Messages)
	}
	if !sameStrings(prompt.Closing["allowedOutcomes"], []string{"done"}) || len(prompt.Closing["hardConstraintKeys"]) != 0 {
		t.Fatalf("closingChecklist = %v, want allowedOutcomes [done] and no hard constraint keys", prompt.Closing)
	}
	if req.AllowedOutcomes[0] != "done" || len(req.AllowedOutcomes) != 1 {
		t.Fatalf("req.AllowedOutcomes = %v, the adapter's list must be the one the prompt shows", req.AllowedOutcomes)
	}
}

// Two assemblies of the same snapshot are byte-identical and share one hash,
// for a snapshot rendered as v2 and for one rendered as v1 (V5-08B0's lock).
func TestAssembleAgentExecutionRequest_SameSnapshotSameInstructionAndHash(t *testing.T) {
	for _, schema := range []string{"v2", "v1"} {
		t.Run(schema, func(t *testing.T) {
			uow, _, store, runID, nodeRunID, attemptID := assembleRequestFixture(t)
			if schema == "v1" {
				uow.Snapshot.ContextSnapshots().(*fake.ContextSnapshotRepository).Overwrite(asPreV903(t, snapshotOfAttempt(t, uow, attemptID)))
			}
			first := assembleFor(t, uow, store, runID, nodeRunID, attemptID)
			second := assembleFor(t, uow, store, runID, nodeRunID, attemptID)
			if first.Prompt != second.Prompt || first.InstructionArtifact.SHA256 != second.InstructionArtifact.SHA256 {
				t.Fatalf("two assemblies of one snapshot differ:\n%s\n%s", first.Prompt, second.Prompt)
			}
			requireArtifactIsPrompt(t, store, first)
			if gotV2 := strings.HasPrefix(first.Prompt, `{"schemaVersion":2,`); gotV2 != (schema == "v2") {
				t.Fatalf("%s snapshot rendered %q", schema, first.Prompt)
			}
		})
	}
}

// ADR-032 decision 2, the backward-compatibility proof: a snapshot that
// records no instruction schema version assembles as v1 exactly as before.
// testdata/golden/instruction_v1_snapshot_fixture.json is the Prompt this very
// fixture produced BEFORE V9-03 (with the two ids this fixture mints replaced
// by placeholders so the file does not depend on the id sequence).
func TestAssembleAgentExecutionRequest_SnapshotWithoutVersion_AssemblesAsV1ByteIdentically(t *testing.T) {
	uow, _, store, runID, nodeRunID, attemptID := assembleRequestFixture(t)
	original := snapshotOfAttempt(t, uow, attemptID)
	uow.Snapshot.ContextSnapshots().(*fake.ContextSnapshotRepository).Overwrite(asPreV903(t, original))

	run, err := uow.Snapshot.Runtime().GetWorkflowRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if len(original.MessageRefs) != 1 {
		t.Fatalf("fixture snapshot has %d message refs, want 1", len(original.MessageRefs))
	}

	raw, err := os.ReadFile(filepath.Join("testdata", "golden", "instruction_v1_snapshot_fixture.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	want := strings.TrimSpace(strings.ReplaceAll(string(raw), "\r\n", "\n"))
	want = strings.ReplaceAll(want, "{{WORK_ITEM_ID}}", string(run.WorkItemID))
	want = strings.ReplaceAll(want, "{{MESSAGE_ID}}", original.MessageRefs[0].MessageID)

	req := assembleFor(t, uow, store, runID, nodeRunID, attemptID)
	if req.Prompt != want {
		t.Fatalf("a snapshot without a schema version no longer assembles as before\n got: %s\nwant: %s", req.Prompt, want)
	}
	requireArtifactIsPrompt(t, store, req)
	sum := sha256.Sum256([]byte(want))
	if got := req.InstructionArtifact.SHA256; got != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("v1 instruction hash = %s, want the hash of the pre-V9-03 bytes", got)
	}
}

// --- a realistic v2 instruction ---

const richRiskLevel = "HIGH"

// richAssembleFixture is assembleRequestFixture's richer sibling: a work item
// with a full contract (behavior, acceptance criteria, verification spec,
// risk level), two messages, one skill with five resources of every priority,
// and an AGENT node with two selectable outcomes plus a cycle escalation
// outcome (which an agent never selects).
func richAssembleFixture(t *testing.T) (uow *fake.UnitOfWork, store ports.ArtifactStore, runID, nodeRunID, attemptID string) {
	t.Helper()
	ctx := context.Background()
	build := assembleFixtureBuild(t)
	buildID := build.ID()
	store = artifactstoreForTest(t)

	u := fake.New()
	seq := idsource.NewSequential("id")
	mustCreateProject(t, u, "project-1")
	mustCreateActiveRepository(t, u, seq, "project-1", "repo-1")
	rootCmd := testCommand("idem-root-repo-1", "hash-root-repo-1", ports.ProjectScope("project-1"), "CreateRootWorkItem")
	root, err := work.CreateRootWorkItem(ctx, u, seq, rootCmd, work.CreateRootWorkItemRequest{
		ProjectID: "project-1", Title: "Add rate limiting to the public API",
		InitialScope: []work.ScopeGrantRequest{{
			RepositoryID: "repo-1", Access: string(workdomain.RepositoryWrite), PathScopes: []string{"**"}, Reason: "root task",
		}},
		Contract: &work.WorkItemContractRequest{
			SchemaVersion: 1,
			Behavior:      "Requests above 100 per minute per API key get HTTP 429 with a Retry-After header.",
			AcceptanceCriteria: []work.AcceptanceCriterionRequest{
				{Description: "Requests above the limit receive 429", VerificationRef: "go test ./ratelimit/..."},
			},
			VerificationSpec: "go test ./... passes",
			RiskLevel:        richRiskLevel,
		},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	provider := &stubProvider{
		handle:   mustHandle(t, "handle-repo-1"),
		revision: workspace.Revision{RepositoryID: project.RepositoryID("repo-1"), VCSObjectID: "cafebabecafebabecafebabecafebabecafebabe", WorkspaceGeneration: 1},
	}
	provisionRichRoot(t, u, seq, provider, root)

	seedEffectiveScope(t, u, root.WorkItemID, "repo-1")
	document := workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "implement", Type: workflow.NodeAgent, Outcomes: []string{"approved", "rework", "escalated"},
				CyclePolicy: &workflow.CyclePolicy{MaxIterations: 2, EscalationOutcome: "escalated"},
				Agent: &workflow.AgentNodeConfig{
					ProfileRef:     definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "agent-profile-def", VersionID: "agent-profile-v1"},
					PolicyRefs:     fullyResolvablePolicyRefs(),
					AdapterBuildID: &buildID,
					Role:           workflow.AgentRoleMaker,
				}},
			{Key: "end", Type: workflow.NodeEnd},
			{Key: "escalated_end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-implement", From: "start", Outcome: "next", To: "implement"},
			{Key: "implement-approved", From: "implement", Outcome: "approved", To: "end"},
			{Key: "implement-rework", From: "implement", Outcome: "rework", To: "implement"},
			{Key: "implement-escalated", From: "implement", Outcome: "escalated", To: "escalated_end"},
		},
	}
	version := publishWorkflowVersionDocument(t, u, "project-1", "wf-def-1", "wf-v-1", document)
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, u, seq, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop, err := runtime.AdvanceRun(ctx, u, seq, runtime.AdvanceRunRequest{RunID: started.RunID, NodeRunID: started.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun: %v", err)
	}
	if err := u.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, _, err := tx.AdapterBuilds().InsertIfAbsent(ctx, build)
		return err
	}); err != nil {
		t.Fatalf("register pinned adapter build: %v", err)
	}

	for i, content := range []string{"Please keep the public handler signatures unchanged.", "The limit is per API key, not per IP."} {
		msgCmd := testCommand("idem-msg-"+string(rune('1'+i)), "hash-msg-"+string(rune('1'+i)), ports.ProjectScope("project-1"), "AppendMessage")
		if _, err := message.AppendMessage(ctx, u, store, seq, clock.System{}, msgCmd, message.AppendMessageRequest{
			ProjectID: "project-1", WorkItemID: root.WorkItemID, Role: domainmessage.RoleUser,
			Content: []byte(content), ContentType: "text/plain", Sensitivity: redact.Public,
		}); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	provenance := skill.Provenance{Owner: "team-x", Source: "doc-1", Revision: "v1"}
	resource := func(key, instruction string, priority definition.PriorityClass) skill.Resource {
		return skill.Resource{Key: key, Instruction: instruction, Priority: priority, Global: true, Provenance: provenance}
	}
	// Deliberately NOT in priority order: the resolver, not the author, orders them.
	skillDoc := skill.SkillDocument{Resources: []skill.Resource{
		resource("architecture-notes", "The ratelimit package owns all quota state.", definition.PriorityReference),
		resource("style-guide", "Prefer small functions; wrap errors with %w.", definition.PriorityGuidance),
		resource("no-force-push", "Never force-push or rewrite published history.", definition.PriorityHardConstraint),
		resource("pr-checklist", "Run go vet and go test before reporting done.", definition.PriorityRequiredProcedure),
		resource("api-compat", "Never change the signature of an exported function.", definition.PriorityHardConstraint),
	}}
	publishSkillVersion(t, u, "skill-def-1", "skill-v1", skillDoc)
	var refs []policy.ResourceRef
	for _, r := range skillDoc.Resources {
		refs = append(refs, policy.ResourceRef{OwnerVersionID: "skill-v1", ResourceKey: r.Key, ContentHash: resourceContentHash(t, "skill-v1", r.Key, skillDoc)})
	}
	publishPolicyVersion(t, u, "context-policy-def", "context-policy-v1", policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context:  &policy.ContextRules{Selector: []string{"*"}, Budget: policy.ContextBudget{MaxTokens: 65536}, ResourceRefs: refs},
	})
	publishAgentProfileVersionOnly(t, u, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, u, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, u, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	result, err := runtime.ScheduleExecutableNodeRun(ctx, u, seq, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: started.RunID, NodeRunID: hop.NextNodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}
	return u, store, started.RunID, hop.NextNodeRunID, result.AttemptID
}

// provisionRichRoot brings the root work item's workspace up and forces it
// READY, the way readyFixture does for a root created without a contract.
func provisionRichRoot(t *testing.T, u *fake.UnitOfWork, seq idsource.Source, provider *stubProvider, root work.CreateRootWorkItemResult) {
	t.Helper()
	ctx := context.Background()
	handler := workspaceprovision.New(u, seq, provider)
	if err := handler.Handle(ctx, provisionJob(
		root.ProvisionedRepositories[0].ProvisionJobID, root.WorkItemID, "project-1", root.FamilyID, root.WorkspaceSetID, "repo-1",
	)); err != nil {
		t.Fatalf("workspaceprovision.Handle: %v", err)
	}
	if err := u.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Work().TransitionWorkItemStatus(ctx, ports.TransitionWorkItemStatusRequest{
			WorkItemID: root.WorkItemID, ExpectedStatus: workdomain.WorkItemBacklog, ExpectedVersion: 1,
			NextStatus: workdomain.WorkItemReady,
		})
		return err
	}); err != nil {
		t.Fatalf("force work item READY: %v", err)
	}
}

func TestAssembleAgentExecutionRequest_RealWorkItemAndResources_RenderV2InPriorityOrder(t *testing.T) {
	uow, store, runID, nodeRunID, attemptID := richAssembleFixture(t)
	req := assembleFor(t, uow, store, runID, nodeRunID, attemptID)
	requireArtifactIsPrompt(t, store, req)

	var prompt struct {
		SchemaVersion   int `json:"schemaVersion"`
		HardConstraints []struct {
			ResourceKey string `json:"resourceKey"`
			Priority    string `json:"priority"`
			Content     string `json:"content"`
		} `json:"hardConstraints"`
		TaskContract struct {
			Title              string   `json:"title"`
			Behavior           string   `json:"behavior"`
			AcceptanceCriteria []string `json:"acceptanceCriteria"`
			VerificationSpec   string   `json:"verificationSpec"`
			RiskLevel          string   `json:"riskLevel"`
			AllowedOutcomes    []string `json:"allowedOutcomes"`
			OutcomeProtocol    string   `json:"outcomeProtocol"`
		} `json:"taskContract"`
		Resources []struct {
			ResourceKey string `json:"resourceKey"`
			Priority    string `json:"priority"`
		} `json:"resources"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
		Closing struct {
			HardConstraintKeys []string `json:"hardConstraintKeys"`
			AllowedOutcomes    []string `json:"allowedOutcomes"`
		} `json:"closingChecklist"`
	}
	if err := json.Unmarshal([]byte(req.Prompt), &prompt); err != nil {
		t.Fatalf("decode the prompt: %v", err)
	}
	if prompt.SchemaVersion != 2 {
		t.Fatalf("schemaVersion = %d, want 2", prompt.SchemaVersion)
	}

	// HARD_CONSTRAINT first, in resolver order (resourceKey), repeated by key
	// in the closing checklist.
	var hardKeys []string
	for _, r := range prompt.HardConstraints {
		hardKeys = append(hardKeys, r.ResourceKey)
		if r.Priority != "HARD_CONSTRAINT" || r.Content == "" {
			t.Fatalf("hardConstraints entry %+v", r)
		}
	}
	if want := []string{"api-compat", "no-force-push"}; !sameStrings(hardKeys, want) || !sameStrings(prompt.Closing.HardConstraintKeys, want) {
		t.Fatalf("hardConstraints %v / closing keys %v, want %v in both", hardKeys, prompt.Closing.HardConstraintKeys, want)
	}
	// The rest by priority, whatever order the skill document listed them in.
	var rest []string
	for _, r := range prompt.Resources {
		rest = append(rest, r.ResourceKey+":"+r.Priority)
	}
	if want := []string{"pr-checklist:REQUIRED_PROCEDURE", "style-guide:GUIDANCE", "architecture-notes:REFERENCE"}; !sameStrings(rest, want) {
		t.Fatalf("resources = %v, want %v", rest, want)
	}

	// Task contract: the work item's contract, its risk level, and exactly the
	// outcomes an agent may select (the cycle's escalation outcome is the
	// runtime's, never the agent's), with the protocol since there is a choice.
	if prompt.TaskContract.RiskLevel != richRiskLevel || prompt.TaskContract.Behavior == "" || prompt.TaskContract.VerificationSpec != "go test ./... passes" {
		t.Fatalf("taskContract = %+v", prompt.TaskContract)
	}
	if len(prompt.TaskContract.AcceptanceCriteria) != 1 || !strings.Contains(prompt.TaskContract.AcceptanceCriteria[0], "Requests above the limit receive 429") {
		t.Fatalf("acceptanceCriteria = %v", prompt.TaskContract.AcceptanceCriteria)
	}
	wantOutcomes := []string{"approved", "rework"}
	if !sameStrings(prompt.TaskContract.AllowedOutcomes, wantOutcomes) || !sameStrings(prompt.Closing.AllowedOutcomes, wantOutcomes) || !sameStrings(req.AllowedOutcomes, wantOutcomes) {
		t.Fatalf("allowed outcomes: prompt %v, checklist %v, request %v, want %v everywhere", prompt.TaskContract.AllowedOutcomes, prompt.Closing.AllowedOutcomes, req.AllowedOutcomes, wantOutcomes)
	}
	if !strings.Contains(prompt.TaskContract.OutcomeProtocol, "<agentkit-outcome>") {
		t.Fatalf("outcomeProtocol = %q, want the marker description", prompt.TaskContract.OutcomeProtocol)
	}
	if len(prompt.Messages) != 2 || prompt.Messages[0].Content != "Please keep the public handler signatures unchanged." {
		t.Fatalf("messages = %+v, want the two appended messages in order", prompt.Messages)
	}

	// The rule that matters most is early in the document, the checklist last.
	if hard, tail, closing := strings.Index(req.Prompt, `"hardConstraints"`), strings.Index(req.Prompt, `"messages"`), strings.Index(req.Prompt, `"closingChecklist"`); !(hard < tail && tail < closing) {
		t.Fatalf("section order hardConstraints@%d messages@%d closingChecklist@%d", hard, tail, closing)
	}
}

// --- clones keep the schema of the snapshot they clone ---

// A technical retry's snapshot is rendered with the schema of the snapshot it
// clones: an attempt scheduled by this build (v2) retries as v2, and an
// attempt whose snapshot predates V9-03 retries as v1 — never silently
// upgraded in the middle of its node run.
func TestExecuteNodeHandler_RetryableFailure_RetrySnapshotKeepsTheInstructionSchema(t *testing.T) {
	for _, tc := range []struct {
		name    string
		preV903 bool
		want    int
	}{
		{"scheduled by this build", false, contextsnapshotpkg.InstructionSchemaV2},
		{"snapshot predates V9-03", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attemptDoc := retryableAttemptPolicyDocument(3, 30, 600, errorcode.CodeProviderUnavailable)
			uow, ids, _, _, attemptID := scheduledExecutionFixtureWithAttemptPolicy(t, attemptDoc)
			original := snapshotOfAttempt(t, uow, attemptID)
			if tc.preV903 {
				original = asPreV903(t, original)
				uow.Snapshot.ContextSnapshots().(*fake.ContextSnapshotRepository).Overwrite(original)
			}

			job := claimableExecuteNodeJob(t, uow, attemptID)
			executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptFailed, ErrorCode: errorcode.CodeProviderUnavailable}}
			handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.NewFixed(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t), nil)
			if err := handler.Handle(context.Background(), job); err != nil {
				t.Fatalf("Handle: %v", err)
			}

			retry := successorOf(t, uow.Snapshot.Runtime().(*fake.RuntimeRepository).Attempts(), attemptID)
			cloned := snapshotOfAttempt(t, uow, string(retry.ID))
			if cloned.ID == original.ID {
				t.Fatal("the retry shares its predecessor's snapshot row")
			}
			if cloned.InstructionSchemaVersion != tc.want {
				t.Fatalf("retry snapshot records instruction schema %d, want %d (the version of the snapshot it clones)", cloned.InstructionSchemaVersion, tc.want)
			}
			if cloned.ManifestHash != original.ManifestHash {
				t.Fatalf("retry snapshot hash = %s, want the original's %s", cloned.ManifestHash, original.ManifestHash)
			}
		})
	}
}

// The same for crash recovery on a real database: the recovery attempt's
// snapshot is persisted with the version of the one it replaces, and an old
// (NULL) row stays NULL.
func TestRecoveryReaperHandler_OrphanedAttempt_RecoverySnapshotKeepsTheInstructionSchema(t *testing.T) {
	for _, tc := range []struct {
		name    string
		preV903 bool
		want    int
	}{
		{"scheduled by this build", false, contextsnapshotpkg.InstructionSchemaV2},
		{"snapshot predates V9-03", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			uow, store, ids, runID, _, attemptID := sqliteExecutionFixture(t)
			original := snapshotOfAttempt(t, uow, attemptID)
			if original.InstructionSchemaVersion != contextsnapshotpkg.InstructionSchemaV2 {
				t.Fatalf("the scheduled snapshot records %d, want 2", original.InstructionSchemaVersion)
			}
			if tc.preV903 {
				if err := sqlite.RewriteSnapshotAsPreV903ForTest(ctx, store, string(original.ID)); err != nil {
					t.Fatalf("RewriteSnapshotAsPreV903ForTest: %v", err)
				}
				original = snapshotOfAttempt(t, uow, attemptID)
				if original.InstructionSchemaVersion != 0 {
					t.Fatalf("the rewritten snapshot records %d, want none", original.InstructionSchemaVersion)
				}
			}

			// The lease is expired explicitly: nothing here is about lease timing.
			claimedJob, _ := claimExecuteNodeJob(t, ctx, store, 30*time.Second)
			if err := sqlite.ExpireJobLeaseForTest(ctx, store, string(claimedJob.ID)); err != nil {
				t.Fatalf("expire the driving job lease: %v", err)
			}
			handler := runtime.NewRecoveryReaperHandler(uow, ids, clock.System{}, store, store, store)
			if err := runtime.StartupRecoveryScan(ctx, uow, ids); err != nil {
				t.Fatalf("StartupRecoveryScan: %v", err)
			}
			if err := handler.Handle(ctx, firstSQLiteJobOfKind(t, ctx, store, runtime.RecoveryReaperJobKind)); err != nil {
				t.Fatalf("Handle: %v", err)
			}

			var attempts []runtimedomain.ExecutionAttempt
			if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
				var err error
				attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
				return err
			}); err != nil {
				t.Fatalf("ListExecutionAttemptsForRun: %v", err)
			}
			var recovery *runtimedomain.ExecutionAttempt
			for i := range attempts {
				if attempts[i].AttemptNumber == 2 {
					recovery = &attempts[i]
				}
			}
			if recovery == nil {
				t.Fatalf("no recovery attempt among %+v", attempts)
			}
			cloned := snapshotOfAttempt(t, uow, string(recovery.ID))
			if cloned.InstructionSchemaVersion != tc.want {
				t.Fatalf("recovery snapshot records instruction schema %d, want %d", cloned.InstructionSchemaVersion, tc.want)
			}
			if cloned.ManifestHash != original.ManifestHash {
				t.Fatalf("recovery snapshot hash = %s, want the original's %s", cloned.ManifestHash, original.ManifestHash)
			}
		})
	}
}
