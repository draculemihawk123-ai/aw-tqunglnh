package readiness

import (
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

func TestNewCommandSpec(t *testing.T) {
	t.Parallel()

	spec, err := NewCommandSpec(" /usr/bin/npm ", []string{"test"}, "sub/dir", 30)
	if err != nil {
		t.Fatalf("NewCommandSpec: %v", err)
	}
	if spec.Executable != "/usr/bin/npm" {
		t.Fatalf("executable = %q, want trimmed", spec.Executable)
	}
	if spec.WorkingDirectory != "sub/dir" {
		t.Fatalf("working directory = %q, want %q", spec.WorkingDirectory, "sub/dir")
	}
	if spec.TimeoutSeconds != 30 {
		t.Fatalf("timeout = %d, want 30", spec.TimeoutSeconds)
	}

	cases := []struct {
		name       string
		executable string
		argv       []string
		dir        string
		timeout    uint32
	}{
		{"empty executable", "", nil, "", 5},
		{"zero timeout", "npm", nil, "", 0},
		{"NUL in executable", "np\x00m", nil, "", 5},
		{"NUL in argv", "npm", []string{"te\x00st"}, "", 5},
		{"absolute working directory", "npm", nil, "/etc", 5},
		{"windows drive working directory", "npm", nil, `C:\repo`, 5},
		{"parent traversal", "npm", nil, "../escape", 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewCommandSpec(tc.executable, tc.argv, tc.dir, tc.timeout); err == nil {
				t.Fatalf("NewCommandSpec(%q, %v, %q, %d) succeeded, want error", tc.executable, tc.argv, tc.dir, tc.timeout)
			}
		})
	}
}

func TestNewCommandSpecEmptyWorkingDirectoryMeansRoot(t *testing.T) {
	t.Parallel()
	spec, err := NewCommandSpec("npm", []string{"ci"}, "", 5)
	if err != nil {
		t.Fatalf("NewCommandSpec: %v", err)
	}
	if spec.WorkingDirectory != "" {
		t.Fatalf("working directory = %q, want empty (workspace root)", spec.WorkingDirectory)
	}

	spec, err = NewCommandSpec("npm", []string{"ci"}, "./", 5)
	if err != nil {
		t.Fatalf("NewCommandSpec: %v", err)
	}
	if spec.WorkingDirectory != "" {
		t.Fatalf("working directory = %q, want empty (\"./\" cleans to root)", spec.WorkingDirectory)
	}
}

func TestNewCommandSpecArgvIsCopied(t *testing.T) {
	t.Parallel()
	argv := []string{"test"}
	spec, err := NewCommandSpec("npm", argv, "", 5)
	if err != nil {
		t.Fatalf("NewCommandSpec: %v", err)
	}
	argv[0] = "mutated"
	if spec.Argv[0] != "test" {
		t.Fatalf("CommandSpec.Argv aliases the caller's own slice: got %q", spec.Argv[0])
	}
}

func mustVerification(t *testing.T) CommandSpec {
	t.Helper()
	spec, err := NewCommandSpec("npm", []string{"test"}, "", 30)
	if err != nil {
		t.Fatalf("NewCommandSpec: %v", err)
	}
	return spec
}

func TestNewProfile(t *testing.T) {
	t.Parallel()

	verification := mustVerification(t)
	profile, err := NewProfile(project.RepositoryID("repo-1"), nil, verification)
	if err != nil {
		t.Fatalf("NewProfile: %v", err)
	}
	if profile.RepositoryID != "repo-1" || profile.Version != 1 || profile.Setup != nil {
		t.Fatalf("unexpected profile: %+v", profile)
	}

	if _, err := NewProfile("", nil, verification); err == nil {
		t.Fatal("NewProfile with empty repository id succeeded, want error")
	}
	if _, err := NewProfile("repo-1", nil, CommandSpec{}); err == nil {
		t.Fatal("NewProfile with empty verification command succeeded, want error")
	}

	setup, err := NewCommandSpec("npm", []string{"ci"}, "", 60)
	if err != nil {
		t.Fatalf("NewCommandSpec: %v", err)
	}
	withSetup, err := NewProfile("repo-1", &setup, verification)
	if err != nil {
		t.Fatalf("NewProfile with setup: %v", err)
	}
	if withSetup.Setup == nil || withSetup.Setup.Executable != "npm" {
		t.Fatalf("unexpected setup command: %+v", withSetup.Setup)
	}
}

func TestBaselineOutcomeBlocking(t *testing.T) {
	t.Parallel()
	cases := []struct {
		outcome BaselineOutcome
		want    bool
	}{
		{BaselineGreen, false},
		{BaselineRed, false},
		{BaselineEnvironmentError, true},
	}
	for _, tc := range cases {
		if got := tc.outcome.Blocking(); got != tc.want {
			t.Errorf("%s.Blocking() = %v, want %v", tc.outcome, got, tc.want)
		}
	}
}

func TestNormalizeRelativeDirectoryBackslash(t *testing.T) {
	t.Parallel()
	spec, err := NewCommandSpec("npm", nil, `sub\dir`, 5)
	if err != nil {
		t.Fatalf("NewCommandSpec: %v", err)
	}
	if spec.WorkingDirectory != "sub/dir" {
		t.Fatalf("working directory = %q, want forward-slash normalized", spec.WorkingDirectory)
	}
}

func TestBlockerStatusConstants(t *testing.T) {
	t.Parallel()
	if BlockerOpen == BlockerResolved {
		t.Fatal("BlockerOpen and BlockerResolved must be distinct")
	}
	if !strings.HasPrefix(EnvironmentBlockerType, "BASELINE") {
		t.Fatalf("EnvironmentBlockerType = %q, want a BASELINE_-prefixed reason", EnvironmentBlockerType)
	}
}
