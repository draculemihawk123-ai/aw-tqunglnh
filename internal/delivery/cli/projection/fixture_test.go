package projection_test

// This file mirrors internal/delivery/cli/workitem's and
// internal/delivery/cli/adapterbuild's own fixture_test.go helpers
// (newTestDeps, mustCreateProject, resultField/replayedField/
// idempotencyKeyField, isUsageError) — duplicated rather than imported (Go
// test helpers in a _test.go file are not exported across packages, the
// same reason those sibling packages' own fixture_test.go files give).

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	cliprojection "github.com/taQuangLing/agent-workflow/internal/delivery/cli/projection"
)

var fixedNow = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

// newTestDeps builds a fresh in-memory cliprojection.Dependencies for one
// test — a fake.UnitOfWork, a deterministic idsource.Sequential and a fixed
// Now, mirroring internal/delivery/cli/workitem/fixture_test.go's own
// identical newTestDeps.
func newTestDeps(t *testing.T) cliprojection.Dependencies {
	t.Helper()
	return cliprojection.Dependencies{
		UoW: fake.New(),
		IDs: idsource.NewSequential("id"),
		Now: func() time.Time { return fixedNow },
	}
}

func mustCreateProject(t *testing.T, uow ports.UnitOfWork, id string) {
	t.Helper()
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: id, Name: "project " + id})
		return err
	})
	if err != nil {
		t.Fatalf("seed project %s: %v", id, err)
	}
}

// resultField extracts the top-level "result" object from a
// cli.ResultEnvelope-shaped JSON document.
func resultField(t *testing.T, body string) string {
	t.Helper()
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode ResultEnvelope %s: %v", body, err)
	}
	return string(envelope.Result)
}

func replayedField(t *testing.T, body string) bool {
	t.Helper()
	var envelope struct {
		Replayed bool `json:"replayed"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode ResultEnvelope %s: %v", body, err)
	}
	return envelope.Replayed
}

// isUsageError reports whether err is a cli.UsageError.
func isUsageError(err error) bool {
	return cli.IsUsageError(err)
}
