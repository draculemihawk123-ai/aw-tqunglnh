package clicompose

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// V9-09 / B2: a create that hits ports.ErrPersistenceAlreadyExists
// (definition create, repository register — LIM-06) is the typed CONFLICT
// document over the CLI's --json failure envelope — the same wire code HTTP
// answers with 409 — and exits with ExitFailure, not INTERNAL.
func TestExecuteFailureBodyMapping_AlreadyExistsIsConflict(t *testing.T) {
	err := fmt.Errorf("%w: definition skill-1", ports.ErrPersistenceAlreadyExists)
	if got := string(failureBody(err).Code); got != "CONFLICT" {
		t.Errorf("failureBody code = %q, want CONFLICT", got)
	}
	if got := exitCodeFor(err); got != cli.ExitFailure {
		t.Errorf("exit = %d, want %d", got, cli.ExitFailure)
	}
}

func TestExecuteJSONAlreadyExistsIsExactlyOneTypedConflictDocument(t *testing.T) {
	routes := []Route{{
		Path: []string{"probe", "thing"},
		Run: func(_ context.Context, _ *Deps, _ []string, _ IO) error {
			return fmt.Errorf("%w: repository repo-1", ports.ErrPersistenceAlreadyExists)
		},
	}}
	descriptors := []cli.Descriptor{{Path: []string{"probe", "thing"}, Scope: cli.ScopeInstallation, AppOperation: "P", HTTPOperationID: "p"}}
	var stdout, stderr bytes.Buffer
	code := ExecuteWith(context.Background(), routes, descriptors, (&factoryProbe{}).factory, nil, []string{"probe", "thing", "--json"}, IO{Stdout: &stdout, Stderr: &stderr})
	if code != cli.ExitFailure {
		t.Errorf("exit = %d, want %d", code, cli.ExitFailure)
	}
	var doc struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	dec := json.NewDecoder(&stdout)
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout.String())
	}
	if doc.Error.Code != "CONFLICT" || !strings.Contains(doc.Error.Message, "repository repo-1") {
		t.Errorf("error document = %+v, want code CONFLICT naming repository repo-1", doc.Error)
	}
	var extra map[string]any
	if err := dec.Decode(&extra); err == nil {
		t.Errorf("stdout carries a second document %v", extra)
	}
}
