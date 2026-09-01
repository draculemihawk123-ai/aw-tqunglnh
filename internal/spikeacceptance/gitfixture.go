package spikeacceptance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// fixtureCommitTimestamp is a fixed author/committer date for every fixture
// repository this package creates. Without pinning it, `git commit` stamps
// the real wall-clock time, so the same fixture content would hash to a
// different commit on every run — and SemanticDiff's "keep ID/hash" rule
// (docs/design/02-v0-spike-verdict.md V0-10) would then report a spurious
// revision difference between any two runs, including two runs on the same
// platform, not just genuine Windows/Linux differences.
const fixtureCommitTimestamp = "2026-08-28T00:00:00Z"

// createFixtureGitRepository creates a small real local Git repository with
// one commit at repositoryPath (parent directories are created as needed)
// and returns its HEAD revision. It mirrors
// internal/adapters/gitworktree's own test helper of the same shape, since
// gitworktree scenarios need a genuine repository, not a mock — but pins
// the commit's author/committer date (see fixtureCommitTimestamp) so the
// same content deterministically produces the same commit hash on every
// run and every platform, which a real test helper (verified once per `go
// test` invocation, never compared across separate runs) does not need.
func createFixtureGitRepository(repositoryPath string, content string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git is required for workspace acceptance scenarios: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(repositoryPath), 0o755); err != nil {
		return "", fmt.Errorf("create repository parent: %w", err)
	}
	run := func(directory string, extraEnv []string, arguments ...string) (string, error) {
		commandArguments := arguments
		if directory != "" {
			commandArguments = append([]string{"-C", directory}, arguments...)
		}
		command := exec.Command("git", commandArguments...)
		command.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), extraEnv...)
		output, err := command.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git %v: %w: %s", commandArguments, err, output)
		}
		return string(output), nil
	}
	if _, err := run("", nil, "init", "--initial-branch=main", repositoryPath); err != nil {
		return "", err
	}
	if _, err := run(repositoryPath, nil, "config", "user.name", "Agent Kit Spike"); err != nil {
		return "", err
	}
	if _, err := run(repositoryPath, nil, "config", "user.email", "agent-kit@example.invalid"); err != nil {
		return "", err
	}
	if _, err := run(repositoryPath, nil, "config", "core.autocrlf", "false"); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(repositoryPath, "service.txt"), []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("write fixture file: %w", err)
	}
	if _, err := run(repositoryPath, nil, "add", "--", "service.txt"); err != nil {
		return "", err
	}
	commitEnv := []string{
		"GIT_AUTHOR_DATE=" + fixtureCommitTimestamp, "GIT_COMMITTER_DATE=" + fixtureCommitTimestamp,
	}
	if _, err := run(repositoryPath, commitEnv, "commit", "-m", "initial fixture"); err != nil {
		return "", err
	}
	revision, err := run(repositoryPath, nil, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(revision), nil
}
