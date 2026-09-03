package project_test

import (
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

func TestNewComponent_NormalizesPath(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"services/api", "services/api"},
		{"services\\api", "services/api"},
		{"./services/api", "services/api"},
		{"services/api/", "services/api"},
		{"services//api", "services/api"},
	}
	for _, tc := range cases {
		component, err := project.NewComponent("comp-1", "project-1", "repo-1", "api", tc.raw, "SERVICE")
		if err != nil {
			t.Fatalf("NewComponent(path=%q): %v", tc.raw, err)
		}
		if component.Path != tc.want {
			t.Fatalf("NewComponent(path=%q).Path = %q, want %q", tc.raw, component.Path, tc.want)
		}
		if component.Version != 1 {
			t.Fatalf("Version = %d, want 1", component.Version)
		}
	}
}

func TestNewComponent_RejectsInvalidPath(t *testing.T) {
	cases := []string{
		"",
		"/absolute/path",
		"C:/windows/path",
		"../escape",
		"services/../../escape",
		".",
	}
	for _, raw := range cases {
		if _, err := project.NewComponent("comp-1", "project-1", "repo-1", "api", raw, "SERVICE"); err == nil {
			t.Errorf("NewComponent(path=%q) = nil error, want error", raw)
		}
	}
}

func TestNewComponent_RequiresIdentityFields(t *testing.T) {
	cases := []struct {
		name         string
		id           project.ComponentID
		projectID    project.ProjectID
		repositoryID project.RepositoryID
		compName     string
		path         string
		kind         string
	}{
		{"empty id", "", "project-1", "repo-1", "api", "services/api", "SERVICE"},
		{"empty project id", "comp-1", "", "repo-1", "api", "services/api", "SERVICE"},
		{"empty repository id", "comp-1", "project-1", "", "api", "services/api", "SERVICE"},
		{"empty name", "comp-1", "project-1", "repo-1", "  ", "services/api", "SERVICE"},
		{"empty kind", "comp-1", "project-1", "repo-1", "api", "services/api", "  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := project.NewComponent(tc.id, tc.projectID, tc.repositoryID, tc.compName, tc.path, tc.kind); err == nil {
				t.Fatalf("NewComponent(%+v) = nil error, want error", tc)
			}
		})
	}
}

func TestNewComponentPackAssignment_Valid(t *testing.T) {
	effectiveAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	assignment, err := project.NewComponentPackAssignment("assign-1", "project-1", "comp-1", "pack-version-1", effectiveAt, "operator-1")
	if err != nil {
		t.Fatalf("NewComponentPackAssignment: %v", err)
	}
	if assignment.PackVersionID != "pack-version-1" {
		t.Fatalf("PackVersionID = %q, want %q", assignment.PackVersionID, "pack-version-1")
	}
	if !assignment.EffectiveAt.Equal(effectiveAt) {
		t.Fatalf("EffectiveAt = %v, want %v", assignment.EffectiveAt, effectiveAt)
	}
}

func TestNewComponentPackAssignment_RequiresEveryField(t *testing.T) {
	effectiveAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name          string
		id            project.ComponentPackAssignmentID
		projectID     project.ProjectID
		componentID   project.ComponentID
		packVersionID project.PackVersionID
		effectiveAt   time.Time
		actor         string
	}{
		{"empty id", "", "project-1", "comp-1", "pack-version-1", effectiveAt, "operator-1"},
		{"empty project id", "assign-1", "", "comp-1", "pack-version-1", effectiveAt, "operator-1"},
		{"empty component id", "assign-1", "project-1", "", "pack-version-1", effectiveAt, "operator-1"},
		{"empty pack version id", "assign-1", "project-1", "comp-1", "  ", effectiveAt, "operator-1"},
		{"zero effective time", "assign-1", "project-1", "comp-1", "pack-version-1", time.Time{}, "operator-1"},
		{"empty actor", "assign-1", "project-1", "comp-1", "pack-version-1", effectiveAt, "  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := project.NewComponentPackAssignment(tc.id, tc.projectID, tc.componentID, tc.packVersionID, tc.effectiveAt, tc.actor); err == nil {
				t.Fatalf("NewComponentPackAssignment(%+v) = nil error, want error", tc)
			}
		})
	}
}
