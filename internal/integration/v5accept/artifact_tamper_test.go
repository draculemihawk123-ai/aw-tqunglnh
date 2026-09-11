// V5-15B — "Artifact tamper" (docs/design/07-v5-execution-evidence.md
// V5-15's own "inject... artifact tamper" line; user's own binding 5-part
// PR split, full text in baocaov5checklist.md's own "V5-15" section and
// memory agent-kit-v5-15-acceptance-gate-contract.md).
//
// Per the V5-15A research handoff's own already-verified finding
// ("isolation-enforcement-unavailable, scope violation, and artifact
// tamper all have real, already-built detection... V5-15's own job for
// each is a real scenario proving the existing mechanism actually fires
// end-to-end, not new production capability"): ports.ArtifactStore.Verify/
// Open already re-hash stored content against its own recorded
// SHA256/Size before ever trusting it (internal/adapters/artifactstore's
// own package doc comment: "detecting on-disk corruption or tampering
// rather than trusting whatever bytes happen to be on disk") — this file
// proves that guarantee holds for real, against a real Evidence artifact
// a real GateNodeExecutor really produced, by really overwriting its own
// real on-disk bytes (never a DB row — the tamper is a real filesystem
// write at the real content-addressed path, confirmed by reading
// internal/adapters/artifactstore/filesystem.go's own object-path
// scheme).
//
// It also records, honestly, a real boundary this scenario deliberately
// does NOT extend: gatherCompletionCandidateEvidence (completion_policy.go)
// reads only the DB's own Evidence rows (Kind/Verdict) — it never opens or
// re-verifies the Artifact bytes an Evidence row references. A tampered
// artifact is therefore real and detectable by anyone who explicitly
// re-verifies it (this file's own assertion, and
// internal/integration/v5accept's own assertEvidenceArtifact, already
// called by every restart check in happy_path_test.go), but does NOT by
// itself change what EvaluateCompletionCandidate decides — extending
// completion evaluation to also re-verify artifact bytes would be new
// production capability, a decision outside what this scenario was asked
// to build (mirrors this same PR's own AGENT-dispatch-wiring fix, which
// WAS confirmed with the user before being built — this boundary is
// intentionally left as a documented, honest limitation instead).
package v5accept

import (
	"context"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

func TestV5AcceptArtifactTamper_RealVerifyDetectsRealCorruption(t *testing.T) {
	f := newV5AcceptFixture(t)
	ctx := context.Background()

	// Reuses V5-15A's own happy-path document/scripts/marker mechanism
	// directly (happy_path_test.go, same package) — this scenario's own
	// point is what happens to an already-real, already-passing Evidence
	// artifact AFTER the fact, not a new maker/checker shape.
	markerPath := filepath.Join(f.fixtureRoot, "v5b-tamper-marker.txt")
	makerKey, makerScript, gateKey, gateScript := v5AcceptScripts(markerPath)
	skillDoc := v5AcceptTwoResourceSkillDocument(makerKey, makerScript, gateKey, gateScript)
	publishSkillVersion(t, f.uow, "v5a-skill-def", "v5a-skill-v1", skillDoc)
	makerHash := resourceContentHash(t, "v5a-skill-v1", makerKey, skillDoc)
	gateHash := resourceContentHash(t, "v5a-skill-v1", gateKey, skillDoc)

	osCompat := command.Compatibility{OS: []string{stdruntime.GOOS}}
	publishCommandVersion(t, f.uow, "v5a-maker-cmd-def", "v5a-maker-cmd-v1", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "v5a-skill-v1", ResourceKey: makerKey, ContentHash: makerHash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		CwdRepositoryTarget: v5AcceptRepositoryID,
		Compatibility:       osCompat,
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      60,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
	})
	publishCommandVersion(t, f.uow, "v5a-gate-cmd-def", "v5a-gate-cmd-v1", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "v5a-skill-v1", ResourceKey: gateKey, ContentHash: gateHash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		CwdRepositoryTarget: v5AcceptRepositoryID,
		Compatibility:       osCompat,
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      60,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
	})
	publishGateVersion(t, f.uow, "v5a-gate-def", "v5a-gate-v1", gate.GateDocument{
		CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "v5a-gate-cmd-def", VersionID: "v5a-gate-cmd-v1"},
		Criteria:   []gate.Criterion{{Name: "output-verified", EvidenceKey: v5AcceptGateEvidenceKey}},
	})
	publishPolicyVersion(t, f.uow, "v5a-attempt-policy-def", "v5a-attempt-policy-v1", v5AcceptAttemptPolicyDocument(60))
	publishPolicyVersion(t, f.uow, "v5a-permission-policy-def", "v5a-permission-policy-v1", v5AcceptPermissionPolicyDocument())

	// No ReleaseSet in this scenario at all — loadLatestReleaseSetGate's
	// own "zero ReleaseSets -> satisfied=true" default (already exercised
	// for real by V5-15A) keeps this scenario focused purely on Evidence/
	// Artifact integrity, its own actual subject.
	completionFields := publishPolicyVersion(t, f.uow, "v5b-tamper-completion-policy-def", "v5b-tamper-completion-policy-v1", policy.PolicyDocument{
		Category:   policy.CategoryCompletion,
		Completion: &policy.CompletionRules{RequiredEvidenceKinds: []string{runtimedomain.EvidenceKindCommandExecution, v5AcceptGateEvidenceKey}},
	})

	doc := v5AcceptHappyPathDocument()
	doc.CompletionPolicyRef = &definition.DependencyPin{Kind: definition.KindPolicy, DefinitionID: "v5b-tamper-completion-policy-def", VersionID: "v5b-tamper-completion-policy-v1"}
	dependencies := workflow.DependencyManifest{Pins: []workflow.DependencyPin{
		{Kind: string(definition.KindPolicy), Key: "v5b-tamper-completion-policy-def", Version: "v5b-tamper-completion-policy-v1", Hash: completionFields.CompiledHash()},
	}}
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v5b-tamper-workflow-def", "v5b-tamper-workflow-v1", doc, dependencies)

	router := &runtime.NodeExecutorRouter{Command: f.newCommandExecutor(), Gate: f.newGateExecutor()}
	registry := f.registerHandlers(router, "v5bt")
	_, stopPool := f.startPool(t, registry)
	defer stopPool()

	root := f.createRootWorkItem(t, "tamper-root", workdomain.RepositoryWrite)
	child := f.createChildWorkItem(t, root.WorkItemID, "tamper-child", workdomain.RepositoryWrite)

	startCmd := testCmd("v5b-tamper-start", ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	runID := started.RunID
	run := f.waitForRunState(t, runID, runtimedomain.WorkflowRunVerifying)

	// Locate gate_b's own real Evidence row and the real Artifact it
	// references — the exact row+artifact V5-15A's own assertEvidenceArtifact
	// already re-verifies on every restart check, reused here by name.
	var nodeRuns []runtimedomain.NodeRun
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		nodeRuns, err = tx.Runtime().ListNodeRunsForRun(ctx, runID)
		return err
	}); err != nil {
		t.Fatalf("ListNodeRunsForRun: %v", err)
	}
	var gateNodeRunID runtimedomain.NodeRunID
	for _, nr := range nodeRuns {
		if nr.NodeKey == "gate_b" {
			gateNodeRunID = nr.ID
		}
	}
	if gateNodeRunID == "" {
		t.Fatal("no NodeRun found for gate_b")
	}

	var evidenceArtifactID string
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		if err != nil {
			return err
		}
		var attemptID string
		for _, a := range attempts {
			if a.NodeRunID == gateNodeRunID {
				attemptID = string(a.ID)
			}
		}
		evidence, err := tx.Runtime().ListEvidenceForAttempt(ctx, attemptID)
		if err != nil {
			return err
		}
		for _, e := range evidence {
			if e.Kind == v5AcceptGateEvidenceKey && len(e.ArtifactReferences) > 0 {
				evidenceArtifactID = e.ArtifactReferences[0]
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("load gate evidence: %v", err)
	}
	if evidenceArtifactID == "" {
		t.Fatal("no gate evidence artifact reference found")
	}

	var artifactRow artifact.Artifact
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		artifactRow, err = tx.Artifacts().GetArtifact(ctx, evidenceArtifactID)
		return err
	}); err != nil {
		t.Fatalf("GetArtifact(%s): %v", evidenceArtifactID, err)
	}
	ref := ports.ArtifactRef{Locator: artifactRow.Locator, SHA256: artifactRow.ContentHash, Size: artifactRow.Size, ContentType: artifactRow.MediaType}

	// Sanity: the real artifact really verifies clean BEFORE tampering —
	// proves the failure asserted below is really caused by the tamper
	// this test performs, not a pre-existing fixture problem.
	if err := f.artifacts.Verify(ctx, ref); err != nil {
		t.Fatalf("Verify (before tamper) = %v, want nil", err)
	}

	// The real tamper: overwrite the real on-disk bytes at the real
	// content-addressed path a real Put call wrote — never a DB row, a
	// real corrupted file, exactly what internal/adapters/artifactstore's
	// own Verify/Open contract exists to catch.
	objectPath := f.artifactObjectPath(t, artifactRow.Locator)
	if err := os.WriteFile(objectPath, []byte("this is not the real gate result content, corrupted for real"), 0o600); err != nil {
		t.Fatalf("tamper artifact content at %s: %v", objectPath, err)
	}

	if err := f.artifacts.Verify(ctx, ref); err == nil {
		t.Fatal("Verify (after real tamper) = nil, want a real integrity error")
	} else {
		t.Logf("Verify correctly rejected the tampered artifact: %v", err)
	}
	if rc, err := f.artifacts.Open(ctx, ref); err == nil {
		_ = rc.Close()
		t.Fatal("Open (after real tamper) succeeded, want a real integrity error")
	} else {
		t.Logf("Open correctly rejected the tampered artifact: %v", err)
	}

	// Honesty check, not a claim this scenario fixes anything: real
	// completion evaluation reads only the DB's own Evidence row
	// (Kind/Verdict) and never re-verifies the Artifact bytes it
	// references (gatherCompletionCandidateEvidence, completion_policy.go
	// — confirmed by reading the code). A tampered artifact is real and
	// detectable (proven above) but does not, on its own, change what
	// EvaluateCompletionCandidate decides — recorded here so this boundary
	// stays a documented, deliberate fact rather than something a future
	// reader has to rediscover from scratch.
	evalCmd := testCmd("v5b-tamper-eval", ports.ProjectScope(v5AcceptProjectID), "EvaluateCompletionCandidate")
	evalCmd.ExpectedVersion = run.Version
	decision, err := runtime.EvaluateCompletionCandidate(ctx, f.uow, f.ids, clock.System{}, evalCmd, runtime.EvaluateCompletionCandidateRequest{RunID: runID})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}
	if decision.Outcome != runtime.CompletionOutcomePass {
		t.Fatalf("completion decision after artifact tamper = %+v; documented boundary expects PASS here (completion evaluation never re-verifies artifact bytes) — if this now differs, the boundary itself changed and this test's own doc comment needs updating too", decision)
	}
	t.Log("documented boundary confirmed: completion evaluation still PASSed despite the tampered artifact, because it never re-verifies artifact bytes — the tamper is real and detectable (proven above), just not consulted by this decision path")
}
