package run_test

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	clirun "github.com/taQuangLing/agent-workflow/internal/delivery/cli/run"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

func TestRunDiagnostics_ReturnsBlockerForThisRun(t *testing.T) {
	deps, ids := newRunDeps(t)
	root := readyWorkItemFixture(t, deps.UOW, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, deps.UOW, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())

	var startOut, startErr bytes.Buffer
	if err := clirun.Start(context.Background(), deps, []string{
		"--workflow-version-id", string(version.ID()), "--idempotency-key", "idem-1", root.WorkItemID,
	}, &startOut, &startErr); err != nil {
		t.Fatalf("Start: %v, stderr=%s", err, startErr.String())
	}
	runID := decodeStartRunID(t, startOut.Bytes())

	// Hand-seed one OPEN blocker sourced from this Run — a narrow,
	// targeted repository seed (mirrors seedBlockedNodeRun's own
	// convention) rather than driving a real admission-blocked scenario
	// end to end (that full scenario is exercised by
	// internal/delivery/cli/noderun's own retry-blocked test instead).
	err := deps.UOW.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		blocker, err := workdomain.NewWorkItemBlocker(
			workdomain.BlockerID("blocker-1"), project.ProjectID("project-1"), workdomain.WorkItemID(root.WorkItemID),
			workdomain.BlockerIsolationEnforcementUnavailable, runID, "", "", "isolation unavailable in test", time.Now().UTC(),
		)
		if err != nil {
			return err
		}
		_, err = tx.Work().CreateWorkItemBlocker(context.Background(), blocker)
		return err
	})
	if err != nil {
		t.Fatalf("seed blocker: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := clirun.Diagnostics(context.Background(), deps, []string{"--project-id", "project-1", runID}, &stdout, &stderr); err != nil {
		t.Fatalf("Diagnostics: %v, stderr=%s", err, stderr.String())
	}
	var result clirun.DiagnosticsResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode DiagnosticsResult: %v\nstdout=%s", err, stdout.String())
	}
	if result.RunID != runID {
		t.Fatalf("RunID = %q, want %q", result.RunID, runID)
	}
	if len(result.Blockers) != 1 {
		t.Fatalf("len(Blockers) = %d, want 1", len(result.Blockers))
	}
	if result.Blockers[0].Type != string(workdomain.BlockerIsolationEnforcementUnavailable) {
		t.Fatalf("Blockers[0].Type = %q, want %q", result.Blockers[0].Type, workdomain.BlockerIsolationEnforcementUnavailable)
	}
	if !result.Blockers[0].AdmissionReason {
		t.Fatal("Blockers[0].AdmissionReason = false, want true (isolation-unavailable is one of the four admission reasons)")
	}
}

func TestRunDiagnostics_RequiresProjectID(t *testing.T) {
	deps, ids := newRunDeps(t)
	root := readyWorkItemFixture(t, deps.UOW, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, deps.UOW, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())

	var startOut, startErr bytes.Buffer
	if err := clirun.Start(context.Background(), deps, []string{
		"--workflow-version-id", string(version.ID()), "--idempotency-key", "idem-1", root.WorkItemID,
	}, &startOut, &startErr); err != nil {
		t.Fatalf("Start: %v, stderr=%s", err, startErr.String())
	}
	runID := decodeStartRunID(t, startOut.Bytes())

	var stdout, stderr bytes.Buffer
	if err := clirun.Diagnostics(context.Background(), deps, []string{runID}, &stdout, &stderr); err == nil {
		t.Fatal("Diagnostics with no --project-id succeeded, want a usage error")
	}
}

// TestRunDiagnostics_NeverExposesProcessOrSecretShapedFields is this
// task's own "diagnostics redaction" Verify bullet, proved the identical
// way internal/app/runtime's own diagnostics.go package doc comment says
// its HTTP sibling proves it
// (TestGetRunDiagnostics_NeverExposesProcessOrSecretShapedFields): a
// reflect-based field-name scan across every exported type this package's
// own diagnostics.go declares. GetRunDiagnostics' own RunDiagnostics has
// no PID/argv/cwd/secret-shaped field to begin with (that query's own
// package doc comment: every type there is a hand-written DTO with an
// explicit field allowlist) — this test proves this package's OWN
// second, hand-mapped DTO layer (toDiagnosticsResult and friends) never
// silently reintroduces one, e.g. by a future field addition here that
// forgets to keep mapping field-by-field rather than embedding a richer
// upstream type verbatim. A field name containing any of these fragments
// WOULD leak PID/argv/cwd/secret-shaped data if it existed — this test is
// the fixture that "would leak if redaction were missing", made
// mechanical.
func TestRunDiagnostics_NeverExposesProcessOrSecretShapedFields(t *testing.T) {
	forbidden := []string{"pid", "argv", "cwd", "secret", "workingdirectory", "executablepath", "command", "env"}

	types := []reflect.Type{
		reflect.TypeOf(clirun.DiagnosticsResult{}),
		reflect.TypeOf(clirun.DiagnosticsBlockerView{}),
		reflect.TypeOf(clirun.DiagnosticsOrphanedAttemptView{}),
		reflect.TypeOf(clirun.DiagnosticsProviderView{}),
		reflect.TypeOf(clirun.DiagnosticsIsolationView{}),
		reflect.TypeOf(clirun.DiagnosticsRepositoryWorkspaceView{}),
	}
	for _, typ := range types {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			lower := strings.ToLower(field.Name)
			for _, bad := range forbidden {
				if strings.Contains(lower, bad) {
					t.Errorf("%s.%s: field name contains forbidden fragment %q — a PID/argv/cwd/secret-shaped field must never appear in diagnostics output", typ.Name(), field.Name, bad)
				}
			}
		}
	}
}
