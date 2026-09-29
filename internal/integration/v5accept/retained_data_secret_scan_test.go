// V8-04D — Content, redaction and secret-scan suite
// (docs/design/10-v8-alpha-hardening.md V8-04D, ADR-017, AK-ARCH-024): the
// "retained-data secret scan trên DB/event/log/artifact" half of that
// task's own Thực hiện line, proven the way V8-04A/B's own real-topology
// additions were — not a new redaction MECHANISM (internal/app/redact
// already has extensive unit coverage, and
// internal/app/runtime/truncation_redaction_test.go already proves a
// resolved secret is redacted out of one persisted command-output artifact
// in isolation, via a fake, in-memory UnitOfWork), but a real, whole-corpus
// proof: after a real Attempt genuinely resolves and echoes a real secret
// value through a real spawned process, the raw bytes of BOTH real retained
// stores this codebase actually has — the real on-disk sqlite database file
// (which is also where every domain_event row lives, so scanning it covers
// "DB" and "event" in the same sweep) and every real object under the real
// filesystem ArtifactStore's own root — are searched for that exact secret
// value, with a real positive control proving the one artifact that
// legitimately carries the command's own (redacted) output actually exists
// and was actually scrubbed, not merely absent because nothing ran.
//
// The "log" half of V8-04D's own DB/event/log/artifact list is deliberately
// NOT re-proven here: internal/app/logging/logger_test.go already has its
// own real, dedicated "zero occurrences" suite
// (TestLogger_JSON_SecretField_ZeroOccurrences,
// TestLogger_Text_SecretField_ZeroOccurrences,
// TestLogger_SecretNestedInFieldValue_ZeroOccurrences) against the actual
// production internal/app/logging.Logger — this package's own real
// handlers/executors log through that same Logger, so duplicating it here
// would prove nothing new.
package v5accept

import (
	"context"
	"io"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// v8d04dSecretEnvVarName is the name this scenario's own real
// secretenv.Resolver looks up (t.Setenv sets it on the TEST process, which
// is the "worker host" secretenv.Resolver's own doc comment describes) —
// and, per command_node_executor.go's own resolveArgvAndSecrets, the exact
// same name the resolved value is re-injected into the SPAWNED process's
// environment under, so the maker script below can echo it back out by
// this same name.
const v8d04dSecretEnvVarName = "V8D04D_SECRET_FIXTURE"

// v8d04dSecretValue is deliberately long and distinctive — long enough that
// a false "not found" from truncation or partial-match elsewhere would be
// implausible, distinctive enough that grepping real production source or
// fixture files for it would only ever find this file.
const v8d04dSecretValue = "sk-v8-04d-retained-data-secret-scan-fixture-4c9f1e7a2b"

// v8d04dEchoSecretScript is a real, OS-appropriate script that echoes
// v8d04dSecretEnvVarName's own resolved value to stdout — the one thing a
// real attacker-adjacent script (or an honestly buggy one) might do with a
// secret it was handed, which is exactly the case CommandNodeExecutor's own
// persistCommandOutputArtifact (command_node_executor.go) exists to defend
// against.
func v8d04dEchoSecretScript() (key, script string) {
	if stdruntime.GOOS == "windows" {
		return "echo-secret.bat", "@echo off\r\necho %" + v8d04dSecretEnvVarName + "%\r\nexit /b 0\r\n"
	}
	return "echo-secret.sh", "#!/bin/sh\necho \"$" + v8d04dSecretEnvVarName + "\"\nexit 0\n"
}

// v8d04dDocument is a minimal real graph: a single real COMMAND node whose
// own successful execution produces runtimedomain.EvidenceKindCommandExecution
// evidence (command_node_executor.go), which is all a CompletionPolicy
// naming only that one RequiredEvidenceKind ever needs — no MACHINE_GATE,
// unlike happy_path_test.go's own richer scenario, since this test's only
// concern is the command output's own redaction, not gate evidence.
func v8d04dDocument() workflow.WorkflowDocument {
	attemptPermissionRefs := []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "v8d04d-attempt-policy-def", VersionID: "v8d04d-attempt-policy-v1"},
		{Kind: definition.KindPolicy, DefinitionID: "v8d04d-permission-policy-def", VersionID: "v8d04d-permission-policy-v1"},
	}
	completionRef := definition.DependencyPin{Kind: definition.KindPolicy, DefinitionID: "v8d04d-completion-policy-def", VersionID: "v8d04d-completion-policy-v1"}
	return workflow.WorkflowDocument{
		SchemaVersion:       "1",
		CompletionPolicyRef: &completionRef,
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{
				Key: "echo_secret", Type: workflow.NodeCommand, Outcomes: []string{"done"},
				Command: &workflow.CommandNodeConfig{
					CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "v8d04d-maker-cmd-def", VersionID: "v8d04d-maker-cmd-v1"},
					PolicyRefs: attemptPermissionRefs,
				},
			},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-echo_secret", From: "start", Outcome: "next", To: "echo_secret"},
			{Key: "echo_secret-end", From: "echo_secret", Outcome: "done", To: "end"},
		},
	}
}

func TestV5AcceptRetainedDataSecretScan_RealSecretNeverPersistedUnredacted(t *testing.T) {
	f := newV5AcceptFixture(t)
	ctx := context.Background()

	// The real secret lives in THIS process's own environment — exactly
	// what secretenv.Resolver's own doc comment names as Alpha's "trust the
	// local machine" secret store; f.supervisor (a real
	// processadapter.Supervisor) inherits it into the spawned script via
	// resolveArgvAndSecrets' own env[ref]=value re-injection.
	t.Setenv(v8d04dSecretEnvVarName, v8d04dSecretValue)

	makerKey, makerScript := v8d04dEchoSecretScript()
	skillDoc := v5AcceptTwoResourceSkillDocument(makerKey, makerScript, "unused.txt", "unused")
	publishSkillVersion(t, f.uow, "v8d04d-skill-def", "v8d04d-skill-v1", skillDoc)
	makerHash := resourceContentHash(t, "v8d04d-skill-v1", makerKey, skillDoc)

	publishCommandVersion(t, f.uow, "v8d04d-maker-cmd-def", "v8d04d-maker-cmd-v1", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "v8d04d-skill-v1", ResourceKey: makerKey, ContentHash: makerHash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		CwdRepositoryTarget: v5AcceptRepositoryID,
		Compatibility:       command.Compatibility{OS: []string{stdruntime.GOOS}},
		NetworkAccess:       command.NetworkAccessNone,
		SecretRefs:          []string{v8d04dSecretEnvVarName},
		TimeoutSeconds:      60,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
	})

	publishPolicyVersion(t, f.uow, "v8d04d-attempt-policy-def", "v8d04d-attempt-policy-v1", v5AcceptAttemptPolicyDocument(60))
	// v5AcceptDriftPermissionPolicyDocument (adapter_drift_test.go) pins
	// OperatorTrustedLocal with no granted capabilities — this scenario
	// needs neither isolation enforcement nor any special capability, only
	// a permission policy that satisfies the real, permissive fake
	// isolation checker registerHandlers wires by default.
	publishPolicyVersion(t, f.uow, "v8d04d-permission-policy-def", "v8d04d-permission-policy-v1", v5AcceptDriftPermissionPolicyDocument())
	publishPolicyVersion(t, f.uow, "v8d04d-completion-policy-def", "v8d04d-completion-policy-v1", policy.PolicyDocument{
		Category:   policy.CategoryCompletion,
		Completion: &policy.CompletionRules{RequiredEvidenceKinds: []string{runtimedomain.EvidenceKindCommandExecution}},
	})

	doc := v8d04dDocument()
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v8d04d-workflow-def", "v8d04d-workflow-v1", doc, workflow.DependencyManifest{})

	router := &runtime.NodeExecutorRouter{Command: f.newCommandExecutor()}
	registry := f.registerHandlers(router, "v8d04d")
	_, stopPool := f.startPool(t, registry)
	defer stopPool()

	root := f.createRootWorkItem(t, "v8d04d-root", workdomain.RepositoryRead)
	child := f.createChildWorkItem(t, root.WorkItemID, "v8d04d-child", workdomain.RepositoryRead)

	startCmd := testCmd("v8d04d-start", ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	runID := started.RunID

	nodeRun := f.waitForNodeRunState(t, runID, "echo_secret", runtimedomain.NodeRunSucceeded)

	// Read the real persisted artifact content back through the real store
	// BEFORE tearing anything down — this is the positive control: the
	// artifact must exist, and must NOT contain the raw secret (it must
	// contain the redactor's own marker instead), proving redaction
	// genuinely ran rather than the command simply never having produced
	// output.
	var attempts []runtimedomain.ExecutionAttempt
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		return err
	}); err != nil {
		t.Fatalf("list execution attempts: %v", err)
	}
	var succeededAttemptID string
	for i := range attempts {
		if attempts[i].NodeRunID == nodeRun.ID && attempts[i].State == runtimedomain.ExecutionAttemptSucceeded {
			succeededAttemptID = string(attempts[i].ID)
		}
	}
	if succeededAttemptID == "" {
		t.Fatalf("no SUCCEEDED ExecutionAttempt found for node run %s (attempts=%+v)", nodeRun.ID, attempts)
	}
	var evidenceArtifactID string
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		list, err := tx.Runtime().ListEvidenceForAttempt(ctx, succeededAttemptID)
		if err != nil {
			return err
		}
		for _, e := range list {
			if e.Kind == runtimedomain.EvidenceKindCommandExecution && len(e.ArtifactReferences) > 0 {
				evidenceArtifactID = e.ArtifactReferences[0]
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("list evidence for attempt %s: %v", succeededAttemptID, err)
	}
	if evidenceArtifactID == "" {
		t.Fatalf("no COMMAND_EXECUTION evidence with an artifact reference was found for attempt %s", succeededAttemptID)
	}

	// truncation_redaction_test.go's own mustReadArtifactContent is typed
	// for *fake.UnitOfWork (an in-memory harness); this package's own real
	// *sqlite.UnitOfWork needs its own tiny inline read instead.
	var artifactContent string
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		record, err := tx.Artifacts().GetArtifact(ctx, evidenceArtifactID)
		if err != nil {
			return err
		}
		ref := ports.ArtifactRef{Locator: record.Locator, SHA256: record.ContentHash, Size: record.Size, ContentType: record.MediaType, Sensitivity: record.Sensitivity, Redacted: record.Redacted}
		rc, err := f.artifacts.Open(ctx, ref)
		if err != nil {
			return err
		}
		defer rc.Close()
		body, err := io.ReadAll(rc)
		if err != nil {
			return err
		}
		artifactContent = string(body)
		return nil
	}); err != nil {
		t.Fatalf("read command output artifact %s: %v", evidenceArtifactID, err)
	}
	if strings.Contains(artifactContent, v8d04dSecretValue) {
		t.Fatalf("command output artifact %s contains the raw secret value unredacted:\n%s", evidenceArtifactID, artifactContent)
	}
	if !strings.Contains(artifactContent, "REDACTED") {
		t.Fatalf("command output artifact %s does not carry the redactor's own marker — positive control failed (is the maker script actually echoing the secret?): %s", evidenceArtifactID, artifactContent)
	}

	// Real, whole-corpus scan #1: every real object the filesystem
	// ArtifactStore ever wrote, walked directly off disk — not just the one
	// artifact this test already knows about, so a FUTURE artifact this
	// scenario doesn't explicitly reference would be caught too.
	artifactRoot := filepath.Join(f.fixtureRoot, "artifacts")
	if err := filepath.WalkDir(artifactRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(body), v8d04dSecretValue) {
			t.Errorf("real artifact-store object %s contains the raw secret value", path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk artifact store root %s: %v", artifactRoot, err)
	}

	// Real, whole-corpus scan #2: the real on-disk sqlite database file
	// itself, closed first so nothing still has it memory-mapped or
	// mid-write — the same "close before direct introspection" discipline
	// golden_workload_test.go's own trace-completeness check already
	// established. This covers BOTH "DB" and "event" from V8-04D's own
	// scope in one sweep: every domain_event row this run ever logged lives
	// in this same file.
	if err := f.store.Close(); err != nil {
		t.Fatalf("close store before raw DB scan: %v", err)
	}
	dbBytes, err := os.ReadFile(f.dbPath)
	if err != nil {
		t.Fatalf("read raw sqlite database file %s: %v", f.dbPath, err)
	}
	if strings.Contains(string(dbBytes), v8d04dSecretValue) {
		t.Fatalf("the real sqlite database file %s contains the raw secret value somewhere in its retained rows (a table this test's own targeted queries above never looked at)", f.dbPath)
	}
}
