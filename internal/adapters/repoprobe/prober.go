// Package repoprobe implements ports.RepositoryProber (V3-02,
// docs/design/05-v3-project-workspace.md): a read-only, durable
// inspection of a Repository's own registered local_path — canonical/safe
// path resolution (never trusting the stored path blindly, rejecting a
// symlink/traversal escape), base-commit resolution against DefaultRef,
// dirty-vs-clean working-tree evidence, and a lightweight Component
// discovery heuristic (HE-06-M05's "Repository -> Component ->
// EngineeringPack" topology).
//
// This package deliberately never provisions a worktree, branch or
// workspace of any kind — that is internal/adapters/gitworktree's own,
// separate concern (V3-06). It shares that package's proven canonical-
// path-resolution technique (an OS-specific symlink-free real-path
// primitive, gated by the same //go:build windows / !windows split) but
// is otherwise its own, much smaller adapter: gitworktree's own git-
// inspection helpers are unexported and gitworktree exists to serve a
// different contract (provisioning) with its own already-merged test
// suite this task must not modify, so this package intentionally
// duplicates only that one small, subtle primitive rather than either
// reaching into gitworktree's internals or refactoring it to export them.
package repoprobe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// Config controls a Prober's git invocation.
type Config struct {
	// GitExecutable names (or paths to) the git binary to invoke. Empty
	// defaults to "git", resolved via exec.LookPath at New time — the same
	// fail-fast-at-construction discipline gitworktree.New already
	// establishes.
	GitExecutable string
}

// Prober implements ports.RepositoryProber.
type Prober struct {
	gitExecutable string
}

var _ ports.RepositoryProber = (*Prober)(nil)

// New resolves the configured git executable (defaulting to "git" on
// PATH) and returns a ready-to-use Prober, or an *apperror.Error
// (CodeUnavailable, retryable) if no usable git executable can be found —
// an environment failure, not a validation one: the caller's own request
// was fine, the host is simply missing a required tool.
func New(config Config) (*Prober, error) {
	gitExecutable := strings.TrimSpace(config.GitExecutable)
	if gitExecutable == "" {
		gitExecutable = "git"
	}
	resolved, err := exec.LookPath(gitExecutable)
	if err != nil {
		return nil, apperror.Wrap(apperror.CodeUnavailable, "find git executable", true, err)
	}
	return &Prober{gitExecutable: resolved}, nil
}

// Probe implements ports.RepositoryProber. See that interface method's
// own doc comment for the full success/failure contract.
func (p *Prober) Probe(ctx context.Context, localPath string, defaultRef string) (ports.RepositoryProbeEvidence, error) {
	canonical, err := p.resolveCanonicalRepository(ctx, localPath)
	if err != nil {
		return ports.RepositoryProbeEvidence{}, err
	}
	baseCommit, err := p.resolveBaseCommit(ctx, canonical, defaultRef)
	if err != nil {
		return ports.RepositoryProbeEvidence{}, err
	}
	dirty, err := p.workingTreeDirty(ctx, canonical)
	if err != nil {
		return ports.RepositoryProbeEvidence{}, err
	}
	components, err := discoverComponents(canonical)
	if err != nil {
		return ports.RepositoryProbeEvidence{}, err
	}
	return ports.RepositoryProbeEvidence{
		CanonicalPath: canonical, BaseCommit: baseCommit, Dirty: dirty, Components: components,
	}, nil
}

// resolveCanonicalRepository resolves rawPath to its real, symlink-free,
// existing-directory form and proves Git itself recognizes that exact
// canonical path as its own top-level working tree — the same "resolve,
// then ask Git to confirm" technique gitworktree's own
// validateLocalRepository uses, adapted to a read-only probe with no
// workspace-root containment check (there is no workspace root here: the
// registered local_path IS the thing being validated, not a path relative
// to some other managed root). This is what rejects a symlink/traversal
// escape: a rawPath that resolves somewhere Git does not recognize as its
// own checkout (a subdirectory of a repository, a symlink chain landing
// outside any repository, a bare repository with no working tree at all)
// fails here rather than silently probing the wrong thing.
func (p *Prober) resolveCanonicalRepository(ctx context.Context, rawPath string) (string, error) {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" || strings.IndexByte(trimmed, 0) >= 0 {
		return "", apperror.New(apperror.CodeInvalidArgument, "repository local path is empty or contains an invalid character", false)
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", apperror.Wrap(apperror.CodeUnavailable, "resolve repository local path", true, err)
	}
	info, err := os.Stat(absolute)
	if errors.Is(err, os.ErrNotExist) {
		return "", apperror.Wrap(apperror.CodeNotFound, "repository local path does not exist", false, err)
	}
	if err != nil {
		return "", apperror.Wrap(apperror.CodeUnavailable, "inspect repository local path", true, err)
	}
	if !info.IsDir() {
		return "", apperror.New(apperror.CodeInvalidArgument, "repository local path is not a directory", false)
	}

	canonical, err := canonicalExistingPath(absolute)
	if err != nil {
		return "", apperror.Wrap(apperror.CodeUnavailable, "resolve repository local path symlinks", true, err)
	}
	canonical = filepath.Clean(canonical)

	topLevelOutput, exitCode, err := p.runGit(ctx, canonical, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", apperror.Wrap(apperror.CodeUnavailable, "run git rev-parse --show-toplevel", true, err)
	}
	if exitCode != 0 {
		return "", apperror.New(apperror.CodeInvalidArgument, "repository local path is not a Git working tree", false)
	}
	topLevel, err := canonicalExistingPath(strings.TrimSpace(string(topLevelOutput)))
	if err != nil {
		return "", apperror.Wrap(apperror.CodeUnavailable, "resolve Git top-level path", true, err)
	}
	topLevel = filepath.Clean(topLevel)
	if !samePath(canonical, topLevel) {
		return "", apperror.New(apperror.CodeInvalidArgument, "repository local path is not its own Git top-level directory", false)
	}
	return canonical, nil
}

// resolveBaseCommit resolves ref to the exact commit object id it names
// in the repository rooted at canonical — a ref that does not resolve is
// a validation/business failure (CodeNotFound: the referenced commit does
// not exist), never an environment one.
func (p *Prober) resolveBaseCommit(ctx context.Context, canonical string, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !validRevisionInput(ref) {
		return "", apperror.New(apperror.CodeInvalidArgument, "repository default ref contains unsupported characters", false)
	}
	output, exitCode, err := p.runGit(ctx, canonical, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", apperror.Wrap(apperror.CodeUnavailable, "run git rev-parse to resolve base ref", true, err)
	}
	if exitCode != 0 {
		return "", apperror.New(apperror.CodeNotFound, fmt.Sprintf("repository default ref %q does not resolve to a commit", ref), false)
	}
	commit := strings.TrimSpace(string(output))
	if !validObjectID(commit) {
		return "", apperror.New(apperror.CodeUnavailable, "git returned an unexpected object id", true)
	}
	return commit, nil
}

// workingTreeDirty reports whether the working tree at canonical has any
// uncommitted change (staged, unstaged or untracked) — a plain read of
// `git status`, never a mutation.
func (p *Prober) workingTreeDirty(ctx context.Context, canonical string) (bool, error) {
	output, exitCode, err := p.runGit(ctx, canonical, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return false, apperror.Wrap(apperror.CodeUnavailable, "run git status", true, err)
	}
	if exitCode != 0 {
		return false, apperror.New(apperror.CodeUnavailable, "git status exited with a non-zero code", true)
	}
	return len(output) != 0, nil
}

// discoverComponents is V3-02's own minimal, documented Component
// discovery heuristic: every direct (depth-1) subdirectory of the
// repository's canonical root, excluding hidden/VCS-internal directories
// (a leading "."), is proposed as one Component candidate — Name and Path
// both the directory's own base name, Kind "DIRECTORY" (project.Component
// deliberately keeps Kind an open string; no citation in this task's
// scope names a fixed vocabulary — see project.Component's own doc
// comment). This is a deliberately minimal reading of HE-06-M05's
// "onboarding MUST tạo hoặc xác nhận topology Repository -> Component":
// good enough to prove the topology exists and give an operator something
// real to look at, without building a full monorepo-tooling detector
// (recognizing go.mod/package.json/pyproject.toml/etc. as component
// markers) the task's own text explicitly warns against over-engineering.
// A flat repository with no subdirectories legitimately discovers zero
// candidates — that is correct behavior, not a bug.
func discoverComponents(canonical string) ([]ports.ProbedComponent, error) {
	entries, err := os.ReadDir(canonical)
	if err != nil {
		return nil, apperror.Wrap(apperror.CodeUnavailable, "scan repository top-level directories", true, err)
	}
	var components []ports.ProbedComponent
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		components = append(components, ports.ProbedComponent{Name: name, Path: name, Kind: "DIRECTORY"})
	}
	sort.Slice(components, func(i, j int) bool { return components[i].Name < components[j].Name })
	return components, nil
}

// runGit runs git with args inside directory and reports (stdout, exit
// code, err) — err is non-nil only when the process itself could not be
// run at all (missing executable, permission denied, context
// cancellation); a clean non-zero exit (git ran and said "no") is
// reported as (output, exitCode, nil), leaving every call site free to
// classify that specific non-zero outcome as either an environment or a
// validation/business failure on its own terms. This mirrors
// gitworktree's own runGitWithExitCode contract exactly (see that
// method's doc comment), duplicated rather than shared for the same
// reason canonical_windows.go/canonical_other.go are.
func (p *Prober) runGit(ctx context.Context, directory string, arguments ...string) ([]byte, int, error) {
	commandArguments := append([]string{"-C", directory}, arguments...)
	command := exec.CommandContext(ctx, p.gitExecutable, commandArguments...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return stdout.Bytes(), 0, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return stdout.Bytes(), exitError.ExitCode(), nil
	}
	return nil, -1, err
}

func samePath(left string, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func validRevisionInput(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 1024 || !utf8.ValidString(value) {
		return false
	}
	return !strings.ContainsAny(value, "\x00\r\n")
}

func validObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
