package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// fakeGh writes a stand-in gh that records its arguments and answers like the real one.
func fakeGh(t *testing.T, view string) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in gh is a shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := `#!/bin/sh
echo "$@" >> "` + log + `"
case "$1 $2" in
  "pr create") echo "Creating pull request"; echo "https://github.com/o/r/pull/7" ;;
  "pr view") cat <<'EOF'
` + view + `
EOF
  ;;
  "pr merge") exit 0 ;;
  "auth status") echo "You are not logged into any GitHub hosts. Run gh auth login to authenticate." >&2; exit 1 ;;
esac
`
	path := filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, log
}

const view = `{"number":7,"url":"https://github.com/o/r/pull/7","state":"OPEN","isDraft":false,"headRefOid":"abc","baseRefName":"main",
"headRefName":"agentkit/w-1","title":"feat: x","mergeable":"MERGEABLE","mergedAt":null,"statusCheckRollup":[
{"__typename":"CheckRun","name":"build","status":"COMPLETED","conclusion":"SUCCESS"},
{"__typename":"StatusContext","context":"ci/legacy","state":"PENDING"}]}`

func TestClient_CreatesReadsAndMergesThroughGh(t *testing.T) {
	gh, log := fakeGh(t, view)
	c := Client{Executable: gh}
	ctx := context.Background()
	pr, err := c.CreatePullRequest(ctx, ports.CreatePullRequestRequest{Directory: t.TempDir(), Head: "agentkit/w-1", Base: "main", Title: "feat: x", Body: "b", Draft: true})
	if err != nil {
		t.Fatalf("CreatePullRequest: %v", err)
	}
	if pr.Number != 7 || pr.HeadSHA != "abc" || pr.State != "OPEN" || len(pr.Checks) != 2 {
		t.Fatalf("pr = %+v", pr)
	}
	if pr.Checks[0] != (ports.ForgeCheck{Name: "build", Status: "COMPLETED", Conclusion: "SUCCESS"}) || pr.Checks[1].Status != "PENDING" {
		t.Fatalf("checks = %+v", pr.Checks)
	}
	if err := c.MergePullRequest(ctx, t.TempDir(), 7, "merge", "abc"); err != nil {
		t.Fatalf("MergePullRequest: %v", err)
	}
	calls, _ := os.ReadFile(log)
	got := string(calls)
	for _, want := range []string{"pr create --head agentkit/w-1 --base main --title feat: x --body b --draft", "pr view https://github.com/o/r/pull/7 --json",
		"pr merge 7 --merge --match-head-commit abc"} {
		if !strings.Contains(got, want) {
			t.Fatalf("gh calls = %q, want %q", got, want)
		}
	}
	if err := c.MergePullRequest(ctx, t.TempDir(), 7, "force", "abc"); err == nil {
		t.Fatal("an unknown merge method must be refused")
	}
}

func TestClient_ReportsAMissingOrSignedOutGh(t *testing.T) {
	_, err := Client{Executable: filepath.Join(t.TempDir(), "no-gh")}.GetPullRequest(context.Background(), t.TempDir(), 1)
	if !errors.Is(err, ports.ErrForgeUnavailable) {
		t.Fatalf("err = %v, want ErrForgeUnavailable", err)
	}
	gh, _ := fakeGh(t, view)
	_, err = Client{Executable: gh}.run(context.Background(), t.TempDir(), "auth", "status")
	if !errors.Is(err, ports.ErrForgeUnavailable) || !strings.Contains(err.Error(), "gh auth login") {
		t.Fatalf("err = %v, want the sign-in command named", err)
	}
}
