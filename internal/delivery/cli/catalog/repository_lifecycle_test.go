package catalog_test

// V11-12 (ADR-043): `aw repository update|disable|delete|check` and `repository list --include-disabled`.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	clicatalog "github.com/taQuangLing/agent-workflow/internal/delivery/cli/catalog"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

func TestRunRepositoryUpdate_CorrectsABlockedRepositoryAndProbesAgain(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-project", "widget")
	mustRegisterRepository(t, deps, projectID, "key-repo", "repo-1")
	version := moveRepositoryToBlocked(t, deps.UoW, "repo-1")

	var stdout, stderr bytes.Buffer
	args := []string{"--expected-version", fmt.Sprintf("%d", version), "--idempotency-key", "key-upd", "--remote-locator", "D:/right", "repo-1"}
	if err := clicatalog.RunRepositoryUpdate(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunRepositoryUpdate: %v (%s)", err, stderr.String())
	}
	repo, err := appcatalog.GetRepository(context.Background(), deps.UoW, "repo-1")
	if err != nil || repo.Status != project.RepositoryProbing || repo.RemoteLocator != "D:/right" || repo.DefaultRef != "main" {
		t.Fatalf("repository = %+v, %v; want PROBING at the corrected path with the old ref kept", repo, err)
	}

	// nothing to change is a usage error
	if err := clicatalog.RunRepositoryUpdate(context.Background(), deps, []string{"--expected-version", "1", "repo-1"}, &stdout, &stderr); err == nil {
		t.Fatal("update with no change succeeded, want a usage error")
	}
}

func TestRunRepositoryDisableAndDelete(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-project", "widget")
	mustRegisterRepository(t, deps, projectID, "key-repo-1", "repo-1")
	mustRegisterRepository(t, deps, projectID, "key-repo-2", "repo-2")
	v1 := moveRepositoryToBlocked(t, deps.UoW, "repo-1")
	v2 := moveRepositoryToBlocked(t, deps.UoW, "repo-2")

	var stdout, stderr bytes.Buffer
	if err := clicatalog.RunRepositoryDisable(context.Background(), deps, []string{"--expected-version", fmt.Sprintf("%d", v1), "--idempotency-key", "key-dis", "--reason", "wrong", "repo-1"}, &stdout, &stderr); err != nil {
		t.Fatalf("RunRepositoryDisable: %v", err)
	}
	if repo, _ := appcatalog.GetRepository(context.Background(), deps.UoW, "repo-1"); repo.Status != project.RepositoryDisabled {
		t.Fatalf("repo-1 = %+v, want DISABLED", repo)
	}

	// delete needs a reason
	err := clicatalog.RunRepositoryDelete(context.Background(), deps, []string{"--expected-version", fmt.Sprintf("%d", v2), "repo-2"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--reason") {
		t.Fatalf("delete without a reason = %v; want a usage error naming --reason", err)
	}
	if err := clicatalog.RunRepositoryDelete(context.Background(), deps, []string{"--expected-version", fmt.Sprintf("%d", v2), "--idempotency-key", "key-del", "--reason", "typo", "repo-2"}, &stdout, &stderr); err != nil {
		t.Fatalf("RunRepositoryDelete: %v", err)
	}
	if _, err := appcatalog.GetRepository(context.Background(), deps.UoW, "repo-2"); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetRepository after delete = %v; want not found", err)
	}

	// list hides the disabled one unless asked
	stdout.Reset()
	if err := clicatalog.RunRepositoryList(context.Background(), deps, []string{projectID}, &stdout, &stderr); err != nil || strings.Contains(stdout.String(), "repo-1") {
		t.Fatalf("default list = %s, %v; want the disabled repository hidden", stdout.String(), err)
	}
	stdout.Reset()
	if err := clicatalog.RunRepositoryList(context.Background(), deps, []string{"--include-disabled", projectID}, &stdout, &stderr); err != nil || !strings.Contains(stdout.String(), "repo-1") {
		t.Fatalf("list --include-disabled = %s, %v; want repo-1", stdout.String(), err)
	}
}

type checkProber struct{ err error }

func (p checkProber) Probe(context.Context, string, string) (ports.RepositoryProbeEvidence, error) {
	return ports.RepositoryProbeEvidence{}, p.err
}

func TestRunRepositoryCheck_PrintsTheProblemAndTheHint(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-project", "widget")
	deps.Prober = checkProber{err: apperror.New(apperror.CodeInvalidArgument, "The folder D:/x is not a Git repository yet.", false).WithDetails(map[string]string{
		ports.ProbeDetailFailureKind: "NOT_A_GIT_REPOSITORY", ports.ProbeDetailHint: "Run: git -C D:/x init -b main",
	})}
	var stdout, stderr bytes.Buffer
	if err := clicatalog.RunRepositoryCheck(context.Background(), deps, []string{"--project-id", projectID, "--path", "D:/x"}, &stdout, &stderr); err != nil {
		t.Fatalf("RunRepositoryCheck: %v", err)
	}
	if !strings.Contains(stdout.String(), "NOT_A_GIT_REPOSITORY") || !strings.Contains(stdout.String(), "init -b main") {
		t.Fatalf("output = %s; want the kind and the hint", stdout.String())
	}
	deps.Prober = nil
	if err := clicatalog.RunRepositoryCheck(context.Background(), deps, []string{"--project-id", projectID, "--path", "D:/x"}, &stdout, &stderr); err == nil {
		t.Fatal("check without a prober succeeded, want an error")
	}
}
