package v6accept

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// createGitRepository builds the one thing a real operator brings to the
// product before any API call: an existing Git repository. It is fixture
// INPUT (the thing being registered), created before the journey starts and
// never edited afterwards by the test — every later change to it is made by
// the product's own worker.
func createGitRepository(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, "src"), 0o755); err != nil {
		t.Fatalf("mkdir repository: %v", err)
	}
	files := map[string]string{
		"README.md":   "# acceptance fixture\n",
		"src/app.txt": "hello\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(path, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	runGit(t, path, "init", "-q", "-b", "main")
	runGit(t, path, "add", "-A")
	runGit(t, path, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "-q", "-m", "initial")
	return path
}

// runGit runs a real git command and returns its trimmed stdout.
func runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
