package catalog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	clicatalog "github.com/taQuangLing/agent-workflow/internal/delivery/cli/catalog"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// newTestDeps builds a fresh in-memory clicatalog.Dependencies for one
// test — a fake.UnitOfWork (internal/app/ports/fake), a deterministic
// idsource.Sequential and a fixed Now, mirroring
// internal/delivery/cli/sample_test.go's own TestSampleNoOpLeafEndToEnd
// setup. Every test in this package gets its own isolated UnitOfWork —
// never shared state between tests.
func newTestDeps(t *testing.T) clicatalog.Dependencies {
	t.Helper()
	fixedNow := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	return clicatalog.Dependencies{
		UoW: fake.New(),
		IDs: idsource.NewSequential("id"),
		Now: func() time.Time { return fixedNow },
	}
}

// mustCreateProject runs `aw project create` for real (never a direct
// Catalog() insert) and returns the generated ProjectID — shared setup for
// every repository/component/pack-assignment test in this package.
func mustCreateProject(t *testing.T, deps clicatalog.Dependencies, idempotencyKey, name string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader(`{"name":"` + name + `"}`)
	args := []string{"--idempotency-key", idempotencyKey}
	if err := clicatalog.RunProjectCreate(context.Background(), deps, args, stdin, &stdout, &stderr); err != nil {
		t.Fatalf("RunProjectCreate() error = %v, stderr = %s", err, stderr.String())
	}
	return jsonField(t, resultField(t, stdout.String()), "projectId")
}

// mustRegisterRepository runs `aw repository register` for real and
// returns the caller-chosen RepositoryID.
func mustRegisterRepository(t *testing.T, deps clicatalog.Dependencies, projectID, idempotencyKey, repositoryID string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	body := `{"repositoryId":"` + repositoryID + `","name":"` + repositoryID + `","remoteLocator":"https://example.invalid/repo.git","defaultRef":"main"}`
	stdin := strings.NewReader(body)
	args := []string{"--project-id", projectID, "--idempotency-key", idempotencyKey}
	if err := clicatalog.RunRepositoryRegister(context.Background(), deps, args, stdin, &stdout, &stderr); err != nil {
		t.Fatalf("RunRepositoryRegister() error = %v, stderr = %s", err, stderr.String())
	}
}

// moveRepositoryToBlocked drives a freshly REGISTERING repository straight
// through REGISTERING->PROBING->BLOCKED via direct Catalog() calls against
// the fake UnitOfWork — standing in for what V3-02's own REPOSITORY_PROBE
// job handler would otherwise do (no real Git-backed prober is wired into
// this leaf's own tests), mirroring
// internal/delivery/httpapi/catalog/catalog_test.go's own
// moveRepositoryToBlocked exactly. Returns the Version the repository ends
// up at (3) so a test can build the correct --expected-version.
func moveRepositoryToBlocked(t *testing.T, uow ports.UnitOfWork, repositoryID string) uint64 {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
		}); err != nil {
			return err
		}
		code := "INVALID_ARGUMENT"
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2,
			NextStatus: project.RepositoryBlocked, LastProbeErrorCode: &code,
		})
		return err
	})
	if err != nil {
		t.Fatalf("moveRepositoryToBlocked(%s): %v", repositoryID, err)
	}
	return 3
}

// completeRepositoryProbeActive drives a freshly REGISTERING repository
// through REGISTERING->PROBING->ACTIVE, recording one SUCCEEDED
// RepositoryProbeAttempt along the way — standing in for a real
// REPOSITORY_PROBE job that discovered the repository is usable, so a test
// can exercise `repository onboarding`'s own "completed" branch. Returns
// the Version the repository ends up at (3).
func completeRepositoryProbeActive(t *testing.T, uow ports.UnitOfWork, projectID, repositoryID, jobID string) uint64 {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
		}); err != nil {
			return err
		}
		if _, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2,
			NextStatus: project.RepositoryActive, LastProbeErrorCode: nil,
		}); err != nil {
			return err
		}
		result := project.RepositoryActive
		baseCommit := "deadbeef"
		dirty := false
		_, err := tx.Catalog().RecordRepositoryProbeAttempt(ctx, ports.RecordRepositoryProbeAttemptRequest{
			ID: jobID + "-attempt", ProjectID: projectID, RepositoryID: repositoryID, JobID: jobID,
			State: ports.RepositoryProbeAttemptSucceeded, Result: &result, BaseCommit: &baseCommit, Dirty: &dirty,
		})
		return err
	})
	if err != nil {
		t.Fatalf("completeRepositoryProbeActive(%s): %v", repositoryID, err)
	}
	return 3
}

// resultField extracts the top-level "result" object from a
// cli.ResultEnvelope-shaped JSON document and returns it re-encoded as its
// own JSON string — so a test can decode a mutation's own inner result
// (e.g. CreateProjectResult) the same way it would decode any query
// output.
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

// jsonField extracts one top-level string field from a JSON object body —
// a tiny, dependency-free helper mirroring
// internal/delivery/httpapi/catalog/catalog_test.go's own jsonField.
func jsonField(t *testing.T, body, field string) string {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("decode JSON body %s: %v", body, err)
	}
	value, _ := raw[field].(string)
	return value
}

// isUsageError reports whether err is a cli.UsageError — every Run*
// function in this package must return one of these (never a bare error)
// for a bad invocation (wrong argument count, a required flag left
// empty), so a future composition root's own cli.ExitCodeFor maps it to
// cli.ExitUsage rather than cli.ExitFailure.
func isUsageError(err error) bool {
	return cli.IsUsageError(err)
}

// TestDescriptorsRegisterAllTenCommandsWithConsistentMetadata is this
// task's own descriptor coverage proof: every command this task's brief
// lists under "Command surface to build" must be registered into
// cli.Default (via this package's own init()), each with a non-CLI_LOCAL
// HTTPOperationID matching internal/delivery/httpapi/catalog's own
// RegisterRoutes OperationID for the same operation (this leaf mirrors an
// existing HTTP surface 1:1, so none of its ten leaves is a CLI-only
// exception) and the Scope this task's own text implies for each (project/
// repository/component/pack-assignment resources — including project
// itself once it names a specific one — are always project-scoped except
// the two ADR-025 installation-scoped exceptions, project list/create).
func TestDescriptorsRegisterAllTenCommandsWithConsistentMetadata(t *testing.T) {
	want := map[string]struct {
		scope           string
		httpOperationID string
	}{
		"project list":           {"INSTALLATION", "projectsList"},
		"project create":         {"INSTALLATION", "projectsCreate"},
		"project show":           {"PROJECT", "projectsGet"},
		"repository list":        {"PROJECT", "projectRepositoriesList"},
		"repository register":    {"PROJECT", "projectRepositoriesRegister"},
		"repository onboarding":  {"PROJECT", "repositoriesOnboarding"},
		"repository retry-probe": {"PROJECT", "repositoriesRetryProbe"},
		"component list":         {"PROJECT", "projectComponentsList"},
		"pack-assignment list":   {"PROJECT", "componentPackAssignmentsList"},
		"pack-assignment assign": {"PROJECT", "componentPackAssignmentsAssign"},
	}

	all := cli.All()
	if len(all) != len(want) {
		t.Fatalf("got %d catalog descriptors, want exactly %d: %+v", len(all), len(want), all)
	}
	for _, d := range all {
		path := strings.Join(d.Path, " ")
		expected, ok := want[path]
		if !ok {
			t.Fatalf("unexpected descriptor registered for path %q", path)
		}
		if string(d.Scope) != expected.scope {
			t.Errorf("descriptor %q Scope = %q, want %q", path, d.Scope, expected.scope)
		}
		if d.HTTPOperationID != expected.httpOperationID {
			t.Errorf("descriptor %q HTTPOperationID = %q, want %q", path, d.HTTPOperationID, expected.httpOperationID)
		}
		if strings.TrimSpace(d.AppOperation) == "" {
			t.Errorf("descriptor %q has empty AppOperation", path)
		}
		delete(want, path)
	}
	if len(want) != 0 {
		t.Fatalf("descriptors never registered: %+v", want)
	}
}
