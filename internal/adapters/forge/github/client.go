// Package github is the GitHub ports.ForgeClient (V12-28, ADR-060): it runs the machine's own GitHub CLI (`gh`), signed in with
// `gh auth login`, in the repository's checkout. aw passes no token and keeps none; `gh` reads its own credentials. Another forge
// (GitLab) is another adapter of the same port.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// Client runs `gh`.
type Client struct {
	// Executable is the gh binary ("gh" from PATH by default).
	Executable string
	// Timeout bounds one call (1 minute by default).
	Timeout time.Duration
}

var _ ports.ForgeClient = Client{}

const viewFields = "number,url,state,isDraft,headRefOid,baseRefName,headRefName,title,mergeable,statusCheckRollup,mergedAt"

func (c Client) run(ctx context.Context, directory string, args ...string) ([]byte, error) {
	exe := c.Executable
	if exe == "" {
		exe = "gh"
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, exe, args...)
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}
	var notFound *exec.Error
	if errors.As(err, &notFound) || errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: the GitHub CLI (gh) is not installed; install it and run `gh auth login`", ports.ErrForgeUnavailable)
	}
	msg := strings.TrimSpace(stderr.String())
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "gh auth login") || strings.Contains(lower, "not logged") || strings.Contains(lower, "authentication") {
		return nil, fmt.Errorf("%w: gh is not signed in for this repository; run `gh auth login`: %s", ports.ErrForgeUnavailable, msg)
	}
	return nil, fmt.Errorf("github: gh %s: %v: %s", args[0], err, msg)
}

type rollupItem struct {
	Typename   string `json:"__typename"`
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

type prView struct {
	Number            int          `json:"number"`
	URL               string       `json:"url"`
	State             string       `json:"state"`
	IsDraft           bool         `json:"isDraft"`
	HeadRefOid        string       `json:"headRefOid"`
	BaseRefName       string       `json:"baseRefName"`
	HeadRefName       string       `json:"headRefName"`
	Title             string       `json:"title"`
	Mergeable         string       `json:"mergeable"`
	StatusCheckRollup []rollupItem `json:"statusCheckRollup"`
	MergedAt          *time.Time   `json:"mergedAt"`
}

func toPullRequest(v prView) ports.PullRequest {
	pr := ports.PullRequest{
		Number: v.Number, URL: v.URL, State: strings.ToUpper(v.State), Draft: v.IsDraft, HeadSHA: v.HeadRefOid, Base: v.BaseRefName,
		Head: v.HeadRefName, Title: v.Title, Mergeable: strings.ToUpper(v.Mergeable), Checks: []ports.ForgeCheck{}, MergedAt: v.MergedAt,
	}
	if pr.MergedAt != nil && pr.MergedAt.IsZero() {
		pr.MergedAt = nil
	}
	for _, item := range v.StatusCheckRollup {
		if item.Typename == "StatusContext" {
			// A commit status: state SUCCESS, FAILURE, ERROR, PENDING, EXPECTED.
			check := ports.ForgeCheck{Name: item.Context, Status: "COMPLETED", Conclusion: strings.ToUpper(item.State)}
			if check.Conclusion == "PENDING" || check.Conclusion == "EXPECTED" {
				check.Status, check.Conclusion = "PENDING", ""
			}
			if check.Conclusion == "ERROR" {
				check.Conclusion = "FAILURE"
			}
			pr.Checks = append(pr.Checks, check)
			continue
		}
		pr.Checks = append(pr.Checks, ports.ForgeCheck{Name: item.Name, Status: strings.ToUpper(item.Status), Conclusion: strings.ToUpper(item.Conclusion)})
	}
	return pr
}

func (c Client) view(ctx context.Context, directory, selector string) (ports.PullRequest, error) {
	out, err := c.run(ctx, directory, "pr", "view", selector, "--json", viewFields)
	if err != nil {
		return ports.PullRequest{}, err
	}
	var v prView
	if err := json.Unmarshal(out, &v); err != nil {
		return ports.PullRequest{}, fmt.Errorf("github: read gh pr view: %w", err)
	}
	return toPullRequest(v), nil
}

// CreatePullRequest implements ports.ForgeClient.
func (c Client) CreatePullRequest(ctx context.Context, req ports.CreatePullRequestRequest) (ports.PullRequest, error) {
	args := []string{"pr", "create", "--head", req.Head, "--base", req.Base, "--title", req.Title, "--body", req.Body}
	if req.Draft {
		args = append(args, "--draft")
	}
	out, err := c.run(ctx, req.Directory, args...)
	if err != nil {
		return ports.PullRequest{}, err
	}
	url := ""
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "https://") {
			url = strings.TrimSpace(line)
		}
	}
	if url == "" {
		return ports.PullRequest{}, fmt.Errorf("github: gh pr create printed no pull request URL: %q", out)
	}
	return c.view(ctx, req.Directory, url)
}

// GetPullRequest implements ports.ForgeClient.
func (c Client) GetPullRequest(ctx context.Context, directory string, number int) (ports.PullRequest, error) {
	return c.view(ctx, directory, strconv.Itoa(number))
}

// MergePullRequest implements ports.ForgeClient.
func (c Client) MergePullRequest(ctx context.Context, directory string, number int, method, expectedHeadSHA string) error {
	flag := map[string]string{"merge": "--merge", "squash": "--squash", "rebase": "--rebase"}[method]
	if flag == "" {
		return fmt.Errorf("github: merge method must be merge, squash or rebase, got %q", method)
	}
	_, err := c.run(ctx, directory, "pr", "merge", strconv.Itoa(number), flag, "--match-head-commit", expectedHeadSHA)
	return err
}
