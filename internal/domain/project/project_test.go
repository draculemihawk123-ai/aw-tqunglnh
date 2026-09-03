package project_test

import (
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

func TestNewRepository_StartsRegistering(t *testing.T) {
	repo, err := project.NewRepository("repo-1", "project-1", "svc", "https://example.invalid/repo.git", "main")
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	if repo.Status != project.RepositoryRegistering {
		t.Fatalf("Status = %q, want %q (a freshly registered repository must never start ACTIVE)", repo.Status, project.RepositoryRegistering)
	}
	if repo.Version != 1 {
		t.Fatalf("Version = %d, want 1", repo.Version)
	}
	if repo.LastProbeErrorCode != nil {
		t.Fatalf("LastProbeErrorCode = %v, want nil (nothing has probed yet)", repo.LastProbeErrorCode)
	}
	if repo.VCSKind != project.VCSGit {
		t.Fatalf("VCSKind = %q, want %q", repo.VCSKind, project.VCSGit)
	}
}

func TestNewRepository_RequiresEveryField(t *testing.T) {
	cases := []struct {
		name          string
		id            project.RepositoryID
		projectID     project.ProjectID
		repoName      string
		remoteLocator string
		defaultRef    string
	}{
		{"empty id", "", "project-1", "svc", "https://example.invalid/repo.git", "main"},
		{"empty project id", "repo-1", "", "svc", "https://example.invalid/repo.git", "main"},
		{"empty name", "repo-1", "project-1", "  ", "https://example.invalid/repo.git", "main"},
		{"empty remote locator", "repo-1", "project-1", "svc", "  ", "main"},
		{"empty default ref", "repo-1", "project-1", "svc", "https://example.invalid/repo.git", "  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := project.NewRepository(tc.id, tc.projectID, tc.repoName, tc.remoteLocator, tc.defaultRef); err == nil {
				t.Fatalf("NewRepository(%+v) = nil error, want error", tc)
			}
		})
	}
}

func TestCanTransitionRepositoryStatus_LegalEdges(t *testing.T) {
	legal := []struct {
		from, to project.RepositoryStatus
	}{
		{project.RepositoryRegistering, project.RepositoryProbing},
		{project.RepositoryProbing, project.RepositoryActive},
		{project.RepositoryProbing, project.RepositoryBlocked},
		{project.RepositoryBlocked, project.RepositoryProbing},
		{project.RepositoryActive, project.RepositoryDisabled},
	}
	for _, tc := range legal {
		if err := project.CanTransitionRepositoryStatus(tc.from, tc.to); err != nil {
			t.Errorf("CanTransitionRepositoryStatus(%s, %s) = %v, want nil", tc.from, tc.to, err)
		}
	}
}

func TestCanTransitionRepositoryStatus_IllegalEdges(t *testing.T) {
	illegal := []struct {
		from, to project.RepositoryStatus
	}{
		// REGISTERING may only ever go to PROBING.
		{project.RepositoryRegistering, project.RepositoryActive},
		{project.RepositoryRegistering, project.RepositoryBlocked},
		{project.RepositoryRegistering, project.RepositoryDisabled},
		// BLOCKED may only retry back to PROBING -- never straight to
		// DISABLED (only a repository that reached ACTIVE at least once
		// may ever be disabled).
		{project.RepositoryBlocked, project.RepositoryDisabled},
		{project.RepositoryBlocked, project.RepositoryActive},
		// ACTIVE may only be explicitly disabled by an operator -- this
		// task declares no path back to PROBING/BLOCKED.
		{project.RepositoryActive, project.RepositoryProbing},
		{project.RepositoryActive, project.RepositoryBlocked},
		{project.RepositoryActive, project.RepositoryRegistering},
		// DISABLED is terminal.
		{project.RepositoryDisabled, project.RepositoryActive},
		{project.RepositoryDisabled, project.RepositoryRegistering},
		{project.RepositoryDisabled, project.RepositoryProbing},
		// A same-status "transition" is a no-op, not a real transition.
		{project.RepositoryActive, project.RepositoryActive},
		{project.RepositoryRegistering, project.RepositoryRegistering},
	}
	for _, tc := range illegal {
		err := project.CanTransitionRepositoryStatus(tc.from, tc.to)
		if err == nil {
			t.Errorf("CanTransitionRepositoryStatus(%s, %s) = nil, want ErrIllegalRepositoryTransition", tc.from, tc.to)
			continue
		}
		if !errors.Is(err, project.ErrIllegalRepositoryTransition) {
			t.Errorf("CanTransitionRepositoryStatus(%s, %s) err = %v, want wrapping ErrIllegalRepositoryTransition", tc.from, tc.to, err)
		}
	}
}

func TestCanTransitionRepositoryStatus_UnknownStatus(t *testing.T) {
	if err := project.CanTransitionRepositoryStatus("BOGUS", project.RepositoryActive); err == nil {
		t.Fatal("CanTransitionRepositoryStatus with an unknown from-status = nil, want error")
	}
}
