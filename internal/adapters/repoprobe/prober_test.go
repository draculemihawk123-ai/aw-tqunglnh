package repoprobe

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
)

func newTestProber(t *testing.T) *Prober {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("Git is required for repoprobe contract tests: %v", err)
	}
	prober, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return prober
}

func createGitRepository(t *testing.T, repositoryPath string, content string) (string, string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(repositoryPath), 0o755); err != nil {
		t.Fatalf("create repository parent: %v", err)
	}
	runTestGit(t, "", "init", "--initial-branch=main", repositoryPath)
	runTestGit(t, repositoryPath, "config", "user.name", "Agent Kit Test")
	runTestGit(t, repositoryPath, "config", "user.email", "agent-kit@example.invalid")
	runTestGit(t, repositoryPath, "config", "core.autocrlf", "false")
	writeTestFile(t, filepath.Join(repositoryPath, "service.txt"), content)
	runTestGit(t, repositoryPath, "add", "--", "service.txt")
	runTestGit(t, repositoryPath, "commit", "-m", "initial fixture")
	return repositoryPath, strings.TrimSpace(runTestGit(t, repositoryPath, "rev-parse", "HEAD"))
}

func createBareGitRepository(t *testing.T, repositoryPath string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(repositoryPath), 0o755); err != nil {
		t.Fatalf("create bare repository parent: %v", err)
	}
	runTestGit(t, "", "init", "--bare", "--initial-branch=main", repositoryPath)
	return repositoryPath
}

func runTestGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	commandArguments := arguments
	if directory != "" {
		commandArguments = append([]string{"-C", directory}, arguments...)
	}
	command := exec.Command("git", commandArguments...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", commandArguments, err, output)
	}
	return string(output)
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustAppError(t *testing.T, err error) *apperror.Error {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("err = %v (%T), want *apperror.Error", err, err)
	}
	return appErr
}

// TestProbe_ValidCleanRepository proves the success path: a real, valid,
// clean Git repository resolves its canonical path, its DefaultRef's
// current commit and Dirty=false.
func TestProbe_ValidCleanRepository(t *testing.T) {
	fixtureRoot := t.TempDir()
	repoPath, headCommit := createGitRepository(t, filepath.Join(fixtureRoot, "service"), "v0\n")
	prober := newTestProber(t)

	evidence, err := prober.Probe(context.Background(), repoPath, "main")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if evidence.BaseCommit != headCommit {
		t.Fatalf("BaseCommit = %q, want %q", evidence.BaseCommit, headCommit)
	}
	if evidence.Dirty {
		t.Fatal("Dirty = true, want false for a freshly committed clean repository")
	}
	if evidence.CanonicalPath == "" {
		t.Fatal("CanonicalPath is empty")
	}
}

// TestProbe_DirtyWorkingTree proves the dirty-vs-clean baseline evidence:
// an uncommitted change makes Dirty=true without affecting BaseCommit
// resolution.
func TestProbe_DirtyWorkingTree(t *testing.T) {
	fixtureRoot := t.TempDir()
	repoPath, headCommit := createGitRepository(t, filepath.Join(fixtureRoot, "service"), "v0\n")
	writeTestFile(t, filepath.Join(repoPath, "service.txt"), "v1 (uncommitted)\n")
	prober := newTestProber(t)

	evidence, err := prober.Probe(context.Background(), repoPath, "main")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !evidence.Dirty {
		t.Fatal("Dirty = false, want true after an uncommitted change")
	}
	if evidence.BaseCommit != headCommit {
		t.Fatalf("BaseCommit = %q, want %q (dirty working tree does not change the resolved base commit)", evidence.BaseCommit, headCommit)
	}
}

// TestProbe_InvalidPath_DoesNotExist proves a registered local_path that
// simply does not exist is a validation/business failure (CodeNotFound,
// not retryable) -- not an environment one.
func TestProbe_InvalidPath_DoesNotExist(t *testing.T) {
	prober := newTestProber(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	_, err := prober.Probe(context.Background(), missing, "main")
	if err == nil {
		t.Fatal("Probe succeeded for a non-existent path, want an error")
	}
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeNotFound {
		t.Fatalf("Code = %q, want %q", appErr.Code, apperror.CodeNotFound)
	}
	if appErr.Retryable {
		t.Fatal("Retryable = true, want false for a missing path (a validation/business failure)")
	}
}

// TestProbe_InvalidPath_NotAGitRepository proves an existing directory
// that is not a Git repository at all is a validation/business failure
// (CodeInvalidArgument, not retryable).
func TestProbe_InvalidPath_NotAGitRepository(t *testing.T) {
	prober := newTestProber(t)
	plainDir := filepath.Join(t.TempDir(), "not-a-repo")
	if err := os.MkdirAll(plainDir, 0o755); err != nil {
		t.Fatalf("create plain directory: %v", err)
	}
	writeTestFile(t, filepath.Join(plainDir, "readme.txt"), "hello\n")

	_, err := prober.Probe(context.Background(), plainDir, "main")
	if err == nil {
		t.Fatal("Probe succeeded for a non-Git directory, want an error")
	}
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeInvalidArgument {
		t.Fatalf("Code = %q, want %q", appErr.Code, apperror.CodeInvalidArgument)
	}
	if appErr.Retryable {
		t.Fatal("Retryable = true, want false for a non-Git directory (a validation/business failure)")
	}
}

// TestProbe_BareRepository_Rejected proves a bare repository (no working
// tree) is rejected as a validation/business failure, the same as
// internal/adapters/gitworktree's own validateLocalRepository rejects it
// (git rev-parse --show-toplevel has nothing to report for a bare repo).
func TestProbe_BareRepository_Rejected(t *testing.T) {
	prober := newTestProber(t)
	barePath := createBareGitRepository(t, filepath.Join(t.TempDir(), "bare.git"))

	_, err := prober.Probe(context.Background(), barePath, "main")
	if err == nil {
		t.Fatal("Probe succeeded for a bare repository, want an error")
	}
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeInvalidArgument {
		t.Fatalf("Code = %q, want %q", appErr.Code, apperror.CodeInvalidArgument)
	}
	if appErr.Retryable {
		t.Fatal("Retryable = true, want false for a bare repository")
	}
}

// TestProbe_RefDoesNotResolve proves a DefaultRef that does not name a
// real commit in the repository is a validation/business failure
// (CodeNotFound: the referenced commit does not exist), not an
// environment one.
func TestProbe_RefDoesNotResolve(t *testing.T) {
	fixtureRoot := t.TempDir()
	repoPath, _ := createGitRepository(t, filepath.Join(fixtureRoot, "service"), "v0\n")
	prober := newTestProber(t)

	_, err := prober.Probe(context.Background(), repoPath, "refs/heads/does-not-exist")
	if err == nil {
		t.Fatal("Probe succeeded for a non-existent ref, want an error")
	}
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeNotFound {
		t.Fatalf("Code = %q, want %q", appErr.Code, apperror.CodeNotFound)
	}
	if appErr.Retryable {
		t.Fatal("Retryable = true, want false for a ref that does not resolve")
	}
}

// TestProbe_DiscoversTopLevelDirectoriesAsComponents proves this
// package's own minimal Component discovery heuristic: every depth-1
// subdirectory becomes one candidate, hidden/VCS directories are
// excluded, and a plain top-level file is never mistaken for one.
func TestProbe_DiscoversTopLevelDirectoriesAsComponents(t *testing.T) {
	fixtureRoot := t.TempDir()
	repoPath, _ := createGitRepository(t, filepath.Join(fixtureRoot, "monorepo"), "root\n")
	// Discovery reads the real filesystem tree (os.ReadDir), not Git's
	// index -- these directories deliberately stay untracked/uncommitted
	// (Git does not track empty directories at all) to prove that.
	for _, dir := range []string{"service-a", "service-b", ".hidden-tooling"} {
		if err := os.MkdirAll(filepath.Join(repoPath, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	prober := newTestProber(t)

	evidence, err := prober.Probe(context.Background(), repoPath, "main")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(evidence.Components) != 2 {
		t.Fatalf("Components = %+v, want exactly 2 (service-a, service-b -- .hidden-tooling excluded)", evidence.Components)
	}
	if evidence.Components[0].Name != "service-a" || evidence.Components[1].Name != "service-b" {
		t.Fatalf("Components = %+v, want [service-a, service-b] in sorted order", evidence.Components)
	}
	for _, c := range evidence.Components {
		if c.Path != c.Name || c.Kind != "DIRECTORY" {
			t.Fatalf("component %+v, want Path==Name and Kind=DIRECTORY", c)
		}
	}
}

// TestProbe_FlatRepository_DiscoversZeroComponents proves a flat
// repository with no subdirectories legitimately discovers zero
// candidates -- correct behavior, not a bug.
func TestProbe_FlatRepository_DiscoversZeroComponents(t *testing.T) {
	fixtureRoot := t.TempDir()
	repoPath, _ := createGitRepository(t, filepath.Join(fixtureRoot, "flat"), "v0\n")
	prober := newTestProber(t)

	evidence, err := prober.Probe(context.Background(), repoPath, "main")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(evidence.Components) != 0 {
		t.Fatalf("Components = %+v, want none for a flat repository", evidence.Components)
	}
}

// TestProbe_PathIsSymlinkToRepository_ResolvesToCanonicalTarget proves
// the canonical-path-safety contract: a symlink pointing at a real
// repository is followed to its real target rather than blindly trusted,
// and the probe still succeeds against the real repository's true
// identity. This is the Windows/Linux path-safety case the task's own
// Verify line names -- symlinks are meaningfully supported on both
// platforms this repo's CI runs (windows-latest/ubuntu-latest), though
// creating one on Windows outside Developer Mode/admin can itself fail,
// which this test treats as an environment limitation to skip past
// rather than a repoprobe bug.
func TestProbe_PathIsSymlinkToRepository_ResolvesToCanonicalTarget(t *testing.T) {
	fixtureRoot := t.TempDir()
	realRepoPath, headCommit := createGitRepository(t, filepath.Join(fixtureRoot, "real-service"), "v0\n")
	linkPath := filepath.Join(fixtureRoot, "linked-service")
	if err := os.Symlink(realRepoPath, linkPath); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("os.Symlink unavailable in this Windows test environment (needs Developer Mode or admin): %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}
	prober := newTestProber(t)

	evidence, err := prober.Probe(context.Background(), linkPath, "main")
	if err != nil {
		t.Fatalf("Probe via symlink: %v", err)
	}
	if evidence.BaseCommit != headCommit {
		t.Fatalf("BaseCommit = %q, want %q", evidence.BaseCommit, headCommit)
	}
	canonicalReal, err := canonicalExistingPath(realRepoPath)
	if err != nil {
		t.Fatalf("canonicalExistingPath(real): %v", err)
	}
	if !samePath(evidence.CanonicalPath, filepath.Clean(canonicalReal)) {
		t.Fatalf("CanonicalPath = %q, want the symlink resolved to the real repository's own canonical path %q", evidence.CanonicalPath, canonicalReal)
	}
}

// TestProbe_PathIsSubdirectoryOfRepository_Rejected proves path safety
// rejects a registered local_path that resolves inside a real Git
// repository but is not that repository's own top-level directory (the
// same rule gitworktree.validateLocalRepository already enforces) -- this
// is what "must not escape outside itself" means concretely: the
// canonical path must be exactly what Git itself reports as the
// repository's own root, never a path a caller merely happens to place
// somewhere inside one.
func TestProbe_PathIsSubdirectoryOfRepository_Rejected(t *testing.T) {
	fixtureRoot := t.TempDir()
	repoPath, _ := createGitRepository(t, filepath.Join(fixtureRoot, "service"), "v0\n")
	subDir := filepath.Join(repoPath, "nested")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	prober := newTestProber(t)

	_, err := prober.Probe(context.Background(), subDir, "main")
	if err == nil {
		t.Fatal("Probe succeeded for a subdirectory of a repository, want an error")
	}
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeInvalidArgument {
		t.Fatalf("Code = %q, want %q", appErr.Code, apperror.CodeInvalidArgument)
	}
}

// TestNew_GitExecutableNotFound_ReturnsUnavailable proves construction
// itself fails fast (matching gitworktree.New's precedent) with an
// environment classification, not a validation one, when no usable git
// executable can be resolved.
func TestNew_GitExecutableNotFound_ReturnsUnavailable(t *testing.T) {
	_, err := New(Config{GitExecutable: "agentkit-repoprobe-definitely-not-a-real-executable"})
	if err == nil {
		t.Fatal("New succeeded with a bogus git executable, want an error")
	}
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeUnavailable {
		t.Fatalf("Code = %q, want %q", appErr.Code, apperror.CodeUnavailable)
	}
	if !appErr.Retryable {
		t.Fatal("Retryable = false, want true for a missing git executable (an environment failure)")
	}
}
