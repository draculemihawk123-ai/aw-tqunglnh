package catalog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	clicatalog "github.com/taQuangLing/agent-workflow/internal/delivery/cli/catalog"
)

func TestRunRepositoryShow_ReportsTheRepositoryExactlyAsListDoes(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-project", "widget")
	mustRegisterRepository(t, deps, projectID, "key-repo", "repo-1")

	var shown bytes.Buffer
	if err := clicatalog.RunRepositoryShow(context.Background(), deps, []string{"repo-1"}, &shown, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunRepositoryShow: %v", err)
	}
	var one map[string]any
	if err := json.Unmarshal(shown.Bytes(), &one); err != nil {
		t.Fatalf("decode %s: %v", shown.String(), err)
	}
	if one["id"] != "repo-1" || one["projectId"] != projectID || one["status"] != "REGISTERING" || one["version"] == nil {
		t.Fatalf("show = %v, want repo-1 of the project, REGISTERING, with a version", one)
	}

	var listed bytes.Buffer
	if err := clicatalog.RunRepositoryList(context.Background(), deps, []string{projectID}, &listed, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunRepositoryList: %v", err)
	}
	var list struct {
		Repositories []map[string]any `json:"repositories"`
	}
	if err := json.Unmarshal(listed.Bytes(), &list); err != nil || len(list.Repositories) != 1 {
		t.Fatalf("list %s (err %v)", listed.String(), err)
	}
	for field, want := range list.Repositories[0] {
		if one[field] != want {
			t.Errorf("%s = %v, but `repository list` reports %v for the same repository", field, one[field], want)
		}
	}
	if len(one) != len(list.Repositories[0]) {
		t.Errorf("show carries %d fields, list carries %d per element", len(one), len(list.Repositories[0]))
	}
}

func TestRunRepositoryShow_UnknownIDIsNotFoundAndWritesNothing(t *testing.T) {
	deps := newTestDeps(t)
	var stdout bytes.Buffer
	err := clicatalog.RunRepositoryShow(context.Background(), deps, []string{"no-such-repository"}, &stdout, &bytes.Buffer{})
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("a failed read wrote %q to stdout", stdout.String())
	}
}

func TestRunRepositoryShow_UsageErrors(t *testing.T) {
	deps := newTestDeps(t)
	for name, args := range map[string][]string{
		"no argument":   nil,
		"blank id":      {" "},
		"two arguments": {"a", "b"},
	} {
		err := clicatalog.RunRepositoryShow(context.Background(), deps, args, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || !cli.IsUsageError(err) {
			t.Errorf("%s: err = %v, want a usage error", name, err)
		}
	}
}
