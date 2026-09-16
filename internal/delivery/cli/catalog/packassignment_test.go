package catalog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	clicatalog "github.com/taQuangLing/agent-workflow/internal/delivery/cli/catalog"
)

func TestRunPackAssignmentAssign_PinsExactVersionAndReturnsActor(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-project", "widget")
	mustRegisterRepository(t, deps, projectID, "key-repo", "repo-1")
	completeRepositoryProbeActive(t, deps.UoW, projectID, "repo-1", "probe-job-1")
	componentID := mustDiscoverComponent(t, deps, projectID, "repo-1", "api", "services/api")

	var stdout, stderr bytes.Buffer
	body := `{"packVersionId":"pack-v1"}`
	// Flags must precede the positional componentId — see the identical
	// note in repository_test.go's own retry-probe tests.
	args := []string{"--idempotency-key", "key-assign", componentID}
	if err := clicatalog.RunPackAssignmentAssign(context.Background(), deps, args, strings.NewReader(body), &stdout, &stderr); err != nil {
		t.Fatalf("RunPackAssignmentAssign() error = %v, stderr = %s", err, stderr.String())
	}
	var result appcatalog.AssignComponentPackResult
	if err := json.Unmarshal([]byte(resultField(t, stdout.String())), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.PackVersionID != "pack-v1" {
		t.Fatalf("PackVersionID = %q, want pack-v1 (exact pin, never resolved against \"latest\")", result.PackVersionID)
	}
	// principal defaults to config.DefaultLocalPrincipal() (no
	// --principal-config given) — Actor must come from the resolved
	// principal, never a request body field (none exists on
	// assignComponentPackRequestBody).
	if result.Actor != "local-operator" {
		t.Fatalf("Actor = %q, want local-operator (the default principal, read from context, never the body)", result.Actor)
	}
}

// TestRunPackAssignmentList_EffectiveReflectsPointInTimeNotJustLatest is
// this task's own "exact pack pin" Verify bullet made concrete: two
// assignments are made, the SECOND one effective in the FUTURE relative to
// deps.Now — `pack-assignment list`'s own Effective field must still
// resolve to the FIRST (currently-in-effect) assignment, never simply "the
// most recently assigned row", proving this leaf's own composed view
// really calls appcatalog.GetEffectiveComponentPackAssignment(ctx, uow,
// componentID, at) with `at` = the current moment rather than defaulting
// to "latest by EffectiveAt".
func TestRunPackAssignmentList_EffectiveReflectsPointInTimeNotJustLatest(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-project", "widget")
	mustRegisterRepository(t, deps, projectID, "key-repo", "repo-1")
	completeRepositoryProbeActive(t, deps.UoW, projectID, "repo-1", "probe-job-1")
	componentID := mustDiscoverComponent(t, deps, projectID, "repo-1", "api", "services/api")

	// First assignment: no explicit effectiveAt, so it takes cmd.RequestedAt
	// (deps.Now(), the fixed 2026-09-17T12:00:00Z) — already in effect.
	firstBody := `{"packVersionId":"pack-v1"}`
	var firstOut bytes.Buffer
	firstArgs := []string{"--idempotency-key", "key-assign-1", componentID}
	if err := clicatalog.RunPackAssignmentAssign(context.Background(), deps, firstArgs, strings.NewReader(firstBody), &firstOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunPackAssignmentAssign() error = %v", err)
	}

	// Second assignment: explicitly effective one full year in the future
	// — a real row, but not YET the effective one as of deps.Now().
	future := time.Date(2027, 9, 17, 12, 0, 0, 0, time.UTC)
	secondBody := fmt.Sprintf(`{"packVersionId":"pack-v2","effectiveAt":%q}`, future.Format(time.RFC3339))
	var secondOut bytes.Buffer
	secondArgs := []string{"--idempotency-key", "key-assign-2", componentID}
	if err := clicatalog.RunPackAssignmentAssign(context.Background(), deps, secondArgs, strings.NewReader(secondBody), &secondOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("second RunPackAssignmentAssign() error = %v", err)
	}

	var stdout bytes.Buffer
	if err := clicatalog.RunPackAssignmentList(context.Background(), deps, []string{componentID}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunPackAssignmentList() error = %v", err)
	}
	var view struct {
		Assignments []struct {
			PackVersionID string `json:"packVersionId"`
		} `json:"assignments"`
		Effective struct {
			PackVersionID string `json:"packVersionId"`
		} `json:"effective"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &view); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if len(view.Assignments) != 2 {
		t.Fatalf("assignments = %+v, want exactly 2 (full append-only history)", view.Assignments)
	}
	if view.Effective.PackVersionID != "pack-v1" {
		t.Fatalf("effective.packVersionId = %q, want pack-v1 (the one actually effective as of now — pack-v2 is only future-scheduled, not \"latest by assignment order\")", view.Effective.PackVersionID)
	}
}

// TestRunPackAssignmentList_NeverAssigned_EffectiveIsNilNotOmitted proves
// the "no assignment yet" branch composes correctly: a Component with no
// ComponentPackAssignment rows at all must report an explicit JSON null
// for "effective", not simply omit the field.
func TestRunPackAssignmentList_NeverAssigned_EffectiveIsNilNotOmitted(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-project", "widget")
	mustRegisterRepository(t, deps, projectID, "key-repo", "repo-1")
	completeRepositoryProbeActive(t, deps.UoW, projectID, "repo-1", "probe-job-1")
	componentID := mustDiscoverComponent(t, deps, projectID, "repo-1", "api", "services/api")

	var stdout bytes.Buffer
	if err := clicatalog.RunPackAssignmentList(context.Background(), deps, []string{componentID}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunPackAssignmentList() error = %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	effective, ok := raw["effective"]
	if !ok {
		t.Fatal(`response has no "effective" key at all, want an explicit null`)
	}
	if string(effective) != "null" {
		t.Fatalf(`effective = %s, want JSON null`, effective)
	}
}

// TestRunPackAssignmentAssign_ReplaySameKey_NeverCreatesASecondRow is this
// task's own "Replay" Verify bullet applied to pack-assignment assign.
func TestRunPackAssignmentAssign_ReplaySameKey_NeverCreatesASecondRow(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-project", "widget")
	mustRegisterRepository(t, deps, projectID, "key-repo", "repo-1")
	completeRepositoryProbeActive(t, deps.UoW, projectID, "repo-1", "probe-job-1")
	componentID := mustDiscoverComponent(t, deps, projectID, "repo-1", "api", "services/api")

	body := `{"packVersionId":"pack-v1"}`
	args := []string{"--idempotency-key", "key-assign", componentID}

	var first bytes.Buffer
	if err := clicatalog.RunPackAssignmentAssign(context.Background(), deps, args, strings.NewReader(body), &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunPackAssignmentAssign() error = %v", err)
	}
	var second bytes.Buffer
	if err := clicatalog.RunPackAssignmentAssign(context.Background(), deps, args, strings.NewReader(body), &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second RunPackAssignmentAssign() error = %v", err)
	}
	var secondEnvelope struct {
		Replayed bool `json:"replayed"`
	}
	if err := json.Unmarshal(second.Bytes(), &secondEnvelope); err != nil {
		t.Fatalf("decode second stdout %s: %v", second.String(), err)
	}
	if !secondEnvelope.Replayed {
		t.Fatal("second call reported Replayed = false, want true")
	}

	assignments, err := appcatalog.ListComponentPackAssignments(context.Background(), deps.UoW, componentID)
	if err != nil {
		t.Fatalf("ListComponentPackAssignments: %v", err)
	}
	if len(assignments) != 1 {
		t.Fatalf("assignments = %+v, want exactly 1 (replay must never create a second row)", assignments)
	}
}

func TestRunPackAssignmentList_UnknownComponent_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := clicatalog.RunPackAssignmentList(context.Background(), deps, []string{"does-not-exist"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunPackAssignmentList() for an unknown component returned nil error")
	}
}
