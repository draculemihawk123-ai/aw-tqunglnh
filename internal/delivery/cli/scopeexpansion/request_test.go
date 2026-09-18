package scopeexpansion_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/scopeexpansion"
)

func TestRequest_HappyPath_GeneratesIdempotencyKey(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "request-happy.db")
	deps := newTestDeps(uow)
	root := familyFixture(t, uow, deps.IDs, "project-1", "repo-1")
	seedActiveRepository(t, uow, deps.IDs, "project-1", "repo-2")

	var stdout, stderr bytes.Buffer
	body := `{"requestedGrants":[{"repositoryId":"repo-2","access":"WRITE","reason":"need repo-2"}],"reason":"expand scope"}`
	err := scopeexpansion.Request(context.Background(), deps, []string{"--project-id", "project-1", root.FamilyID}, strings.NewReader(body), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Request() error = %v, stderr = %s", err, stderr.String())
	}

	var envelope struct {
		IdempotencyKey string                           `json:"idempotencyKey"`
		Replayed       bool                             `json:"replayed"`
		Result         work.RequestScopeExpansionResult `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode stdout %s: %v", stdout.String(), err)
	}
	if envelope.IdempotencyKey == "" {
		t.Fatal("no --idempotency-key given, so Request must have generated and returned one")
	}
	if envelope.Replayed {
		t.Fatal("first run reported Replayed = true, want false")
	}
	if envelope.Result.FamilyID != root.FamilyID || envelope.Result.Status != "PENDING" {
		t.Fatalf("result = %+v, want FamilyID=%s Status=PENDING", envelope.Result, root.FamilyID)
	}
}

func TestRequest_ReplaySameIdempotencyKey_NeverCreatesTwice(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "request-replay.db")
	deps := newTestDeps(uow)
	root := familyFixture(t, uow, deps.IDs, "project-1", "repo-1")
	seedActiveRepository(t, uow, deps.IDs, "project-1", "repo-2")

	args := []string{"--project-id", "project-1", "--idempotency-key", "key-1", root.FamilyID}
	body := `{"requestedGrants":[{"repositoryId":"repo-2","access":"WRITE","reason":"need repo-2"}],"reason":"expand scope"}`

	var first bytes.Buffer
	if err := scopeexpansion.Request(context.Background(), deps, args, strings.NewReader(body), &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first Request() error = %v", err)
	}
	var firstEnvelope struct {
		Result work.RequestScopeExpansionResult `json:"result"`
	}
	if err := json.Unmarshal(first.Bytes(), &firstEnvelope); err != nil {
		t.Fatalf("decode first stdout: %v", err)
	}

	var second bytes.Buffer
	if err := scopeexpansion.Request(context.Background(), deps, args, strings.NewReader(body), &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second Request() error = %v", err)
	}
	var secondEnvelope struct {
		Replayed bool                             `json:"replayed"`
		Result   work.RequestScopeExpansionResult `json:"result"`
	}
	if err := json.Unmarshal(second.Bytes(), &secondEnvelope); err != nil {
		t.Fatalf("decode second stdout: %v", err)
	}
	if !secondEnvelope.Replayed {
		t.Fatal("second Request() with the identical idempotency key reported Replayed = false, want true")
	}
	if secondEnvelope.Result.RequestID != firstEnvelope.Result.RequestID {
		t.Fatalf("replay RequestID = %s, want unchanged %s", secondEnvelope.Result.RequestID, firstEnvelope.Result.RequestID)
	}
}

func TestRequest_MissingProjectID_IsUsageError(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "request-noproj.db")
	deps := newTestDeps(uow)
	var stdout, stderr bytes.Buffer
	err := scopeexpansion.Request(context.Background(), deps, []string{"fam-1"}, strings.NewReader(`{}`), &stdout, &stderr)
	if err == nil || !cli.IsUsageError(err) {
		t.Fatalf("Request(no --project-id) error = %v, want a cli.UsageError", err)
	}
}

func TestRequest_UnknownFamily_Errors(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "request-unknownfam.db")
	deps := newTestDeps(uow)
	seedProject(t, uow, "project-1")
	var stdout, stderr bytes.Buffer
	err := scopeexpansion.Request(context.Background(), deps, []string{"--project-id", "project-1", "no-such-family"}, strings.NewReader(`{"reason":"x","requestedGrants":[{"repositoryId":"r","access":"WRITE","reason":"y"}]}`), &stdout, &stderr)
	if err == nil {
		t.Fatal("Request(unknown familyId) succeeded, want an error")
	}
}

// TestRequest_CrossProjectFamily_LeakageNormalizedNotFound proves the
// TaskFamily reload this command performs before anything else actually
// enforces scope: a familyId that is real but belongs to a DIFFERENT
// project than --project-id names must fail exactly like an unknown one.
func TestRequest_CrossProjectFamily_LeakageNormalizedNotFound(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "request-crossproj.db")
	deps := newTestDeps(uow)
	root := familyFixture(t, uow, deps.IDs, "project-1", "repo-1")
	seedProject(t, uow, "project-2")

	var stdout, stderr bytes.Buffer
	err := scopeexpansion.Request(context.Background(), deps, []string{"--project-id", "project-2", root.FamilyID}, strings.NewReader(`{"reason":"x","requestedGrants":[{"repositoryId":"r","access":"WRITE","reason":"y"}]}`), &stdout, &stderr)
	if err == nil {
		t.Fatal("Request(cross-project familyId) succeeded, want an error")
	}
}
