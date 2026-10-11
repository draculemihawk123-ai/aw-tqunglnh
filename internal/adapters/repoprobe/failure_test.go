package repoprobe

// V11-12 (ADR-043): a probe failure names its cause (failureKind), the path, the redacted Git stderr and a hint. Real Git for the causes Git
// itself reports; a fake Git (this test binary re-run with FAKE_GIT_STDERR) for the ones a test machine cannot produce on demand.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
)

// TestMain turns the test binary into a Git that fails with a canned stderr, so the dubious-ownership and secret-redaction paths can be driven
// without a machine that actually produces them.
func TestMain(m *testing.M) {
	if text := os.Getenv("FAKE_GIT_STDERR"); text != "" {
		os.Stderr.WriteString(text)
		os.Exit(128)
	}
	os.Exit(m.Run())
}

func probeFailure(t *testing.T, prober *Prober, path, ref string) *apperror.Error {
	t.Helper()
	_, err := prober.Probe(context.Background(), path, ref)
	if err == nil {
		t.Fatalf("Probe(%q, %q) succeeded, want a failure", path, ref)
	}
	return mustAppError(t, err)
}

func kindOf(e *apperror.Error) string { return e.Details[ports.ProbeDetailFailureKind] }

func requireKind(t *testing.T, e *apperror.Error, kind ports.ProbeFailureKind, code apperror.Code) {
	t.Helper()
	if kindOf(e) != string(kind) || e.Code != code {
		t.Fatalf("failure = %s / %s (%q); want %s / %s", kindOf(e), e.Code, e.Message, kind, code)
	}
	if e.Message == "" || e.Details[ports.ProbeDetailHint] == "" {
		t.Fatalf("failure %s must carry a message and a hint, got %+v", kind, e)
	}
}

func TestProbeFailure_PathProblems(t *testing.T) {
	prober := newTestProber(t)
	dir := t.TempDir()

	e := probeFailure(t, prober, "   ", "main")
	requireKind(t, e, ports.ProbeFailurePathEmpty, apperror.CodeInvalidArgument)

	missing := filepath.Join(dir, "gone")
	e = probeFailure(t, prober, missing, "main")
	requireKind(t, e, ports.ProbeFailurePathNotFound, apperror.CodeNotFound)
	if e.Details[ports.ProbeDetailPath] != missing || !strings.Contains(e.Message, missing) {
		t.Fatalf("path not carried: %+v", e)
	}

	file := filepath.Join(dir, "a.txt")
	writeTestFile(t, file, "x")
	e = probeFailure(t, prober, file, "main")
	requireKind(t, e, ports.ProbeFailurePathNotDirectory, apperror.CodeInvalidArgument)
}

func TestProbeFailure_GitBashPathOnWindowsNamesTheNativeSpelling(t *testing.T) {
	if native, ok := gitBashPathHint("/d/work/app"); !ok || native != "D:/work/app" {
		t.Fatalf("gitBashPathHint = %q, %v", native, ok)
	}
	if _, ok := gitBashPathHint("D:/work/app"); ok {
		t.Fatal("a native path is not a Git Bash path")
	}
	if runtime.GOOS != "windows" {
		t.Skip("the Git Bash spelling is only suggested on Windows")
	}
	e := probeFailure(t, newTestProber(t), "/d/definitely/not/here", "main")
	requireKind(t, e, ports.ProbeFailurePathNotFound, apperror.CodeNotFound)
	if !strings.Contains(e.Details[ports.ProbeDetailHint], "D:/definitely/not/here") {
		t.Fatalf("hint = %q; want the D:/ spelling", e.Details[ports.ProbeDetailHint])
	}
}

func TestProbeFailure_NotYetAGitRepositoryGivesTheInitCommand(t *testing.T) {
	prober := newTestProber(t)
	plain := filepath.Join(t.TempDir(), "bpm-gl")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	e := probeFailure(t, prober, plain, "main")
	requireKind(t, e, ports.ProbeFailureNotAGitRepository, apperror.CodeInvalidArgument)
	hint := e.Details[ports.ProbeDetailHint]
	if !strings.Contains(hint, "git -C ") || !strings.Contains(hint, "init -b main") || !strings.Contains(hint, "retry the probe") {
		t.Fatalf("hint = %q; want a pasteable git init command", hint)
	}
	if !strings.Contains(strings.ToLower(e.Details[ports.ProbeDetailDetail]), "not a git repository") {
		t.Fatalf("detail = %q; want Git's own words", e.Details[ports.ProbeDetailDetail])
	}
}

func TestProbeFailure_SubdirectoryNamesTheRealRoot(t *testing.T) {
	prober := newTestProber(t)
	root, _ := createGitRepository(t, filepath.Join(t.TempDir(), "repo"), "x\n")
	sub := filepath.Join(root, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	e := probeFailure(t, prober, sub, "main")
	requireKind(t, e, ports.ProbeFailureNotTopLevel, apperror.CodeInvalidArgument)
	canonicalRoot, err := canonicalExistingPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(e.Details[ports.ProbeDetailDetail]), strings.ToLower(filepath.ToSlash(canonicalRoot))) {
		t.Fatalf("detail = %q; want the real root %s", e.Details[ports.ProbeDetailDetail], canonicalRoot)
	}
}

func TestProbeFailure_BareRepository(t *testing.T) {
	bare := createBareGitRepository(t, filepath.Join(t.TempDir(), "bare.git"))
	e := probeFailure(t, newTestProber(t), bare, "main")
	requireKind(t, e, ports.ProbeFailureBareRepository, apperror.CodeInvalidArgument)
}

func TestProbeFailure_NoCommitsAndRefProblems(t *testing.T) {
	prober := newTestProber(t)

	empty := filepath.Join(t.TempDir(), "empty")
	runTestGit(t, "", "init", "--initial-branch=main", empty)
	e := probeFailure(t, prober, empty, "main")
	requireKind(t, e, ports.ProbeFailureNoCommits, apperror.CodeNotFound)

	repo, _ := createGitRepository(t, filepath.Join(t.TempDir(), "repo"), "x\n")
	e = probeFailure(t, prober, repo, "develop")
	requireKind(t, e, ports.ProbeFailureRefNotFound, apperror.CodeNotFound)
	if !strings.Contains(e.Details[ports.ProbeDetailDetail], "main") {
		t.Fatalf("detail = %q; want the existing branches listed", e.Details[ports.ProbeDetailDetail])
	}

	e = probeFailure(t, prober, repo, "bad ref\nname")
	requireKind(t, e, ports.ProbeFailureRefInvalid, apperror.CodeInvalidArgument)
}

func fakeGit(t *testing.T, stderr string) *Prober {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_GIT_STDERR", stderr)
	prober, err := New(Config{GitExecutable: exe, Matcher: redact.NewMatcher("s3cr3t-token")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return prober
}

func TestProbeFailure_DubiousOwnershipNamesTheSafeDirectoryCommand(t *testing.T) {
	folder := t.TempDir()
	prober := fakeGit(t, "fatal: detected dubious ownership in repository at '"+filepath.ToSlash(folder)+"'\nTo add an exception for this directory, call:\n\n\tgit config --global --add safe.directory "+filepath.ToSlash(folder)+"\n")
	e := probeFailure(t, prober, folder, "main")
	requireKind(t, e, ports.ProbeFailureDubiousOwnership, apperror.CodeInvalidArgument)
	if !strings.Contains(e.Details[ports.ProbeDetailHint], "git config --global --add safe.directory ") {
		t.Fatalf("hint = %q", e.Details[ports.ProbeDetailHint])
	}
}

func TestProbeFailure_UnknownGitFailureKeepsGitsWordsRedacted(t *testing.T) {
	folder := t.TempDir()
	prober := fakeGit(t, "fatal: unable to access 'https://user:hunter2@example.com/x.git/': boom s3cr3t-token")
	e := probeFailure(t, prober, folder, "main")
	requireKind(t, e, ports.ProbeFailureGitFailed, apperror.CodeInvalidArgument)
	detail := e.Details[ports.ProbeDetailDetail]
	if strings.Contains(detail, "hunter2") || strings.Contains(detail, "s3cr3t-token") {
		t.Fatalf("detail leaks a secret: %q", detail)
	}
	if !strings.Contains(detail, "boom") {
		t.Fatalf("detail lost Git's words: %q", detail)
	}
}

func TestProbeFailure_DetailIsBounded(t *testing.T) {
	folder := t.TempDir()
	prober := fakeGit(t, "fatal: "+strings.Repeat("x", 10000))
	e := probeFailure(t, prober, folder, "main")
	if n := len(e.Details[ports.ProbeDetailDetail]); n > maxDetailBytes+16 {
		t.Fatalf("detail is %d bytes, want at most about %d", n, maxDetailBytes)
	}
}

func TestProbeFailure_GitMissingIsGitNotFound(t *testing.T) {
	prober := newTestProber(t)
	prober.gitExecutable = filepath.Join(t.TempDir(), "no-such-git")
	e := probeFailure(t, prober, t.TempDir(), "main")
	requireKind(t, e, ports.ProbeFailureGitNotFound, apperror.CodeUnavailable)
	if !e.Retryable {
		t.Fatal("a missing Git is an environment failure: retryable")
	}
}

func TestProbeFailure_StableUnderAnotherLocale(t *testing.T) {
	t.Setenv("LC_ALL", "vi_VN.UTF-8")
	t.Setenv("LANGUAGE", "vi")
	plain := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	e := probeFailure(t, newTestProber(t), plain, "main")
	requireKind(t, e, ports.ProbeFailureNotAGitRepository, apperror.CodeInvalidArgument)
}
