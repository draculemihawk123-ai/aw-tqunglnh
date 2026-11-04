package catalog

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"strings"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// V12-33 (ADR-065): `aw project task-documents <projectId>` and `aw project set-task-documents --location repo|aw <projectId>`.

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"project", "task-documents"}, Scope: cli.ScopeProject,
		AppOperation: "GetProjectTaskDocuments", HTTPOperationID: "getProjectTaskDocuments",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"project", "set-task-documents"}, Scope: cli.ScopeProject,
		AppOperation: "SetProjectTaskDocuments", HTTPOperationID: "setProjectTaskDocuments",
	})
}

// RunProjectTaskDocuments implements `aw project task-documents <projectId>`.
func RunProjectTaskDocuments(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project task-documents", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return usageErrorf("usage: aw project task-documents <projectId>")
	}
	view, err := appcatalog.GetTaskDocuments(ctx, deps.UoW, fs.Args()[0])
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, view)
}

// RunProjectSetTaskDocuments implements `aw project set-task-documents --location repo|aw <projectId>`.
func RunProjectSetTaskDocuments(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project set-task-documents", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	location := fs.String("location", "", "repo (docs/features/<feature>/tasks/<task>/, committed; the default) or aw (.aw/features/…, kept out of git)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	value := strings.ToUpper(strings.TrimSpace(*location))
	if len(fs.Args()) != 1 || (value != appcatalog.TaskDocumentsRepo && value != appcatalog.TaskDocumentsAw) {
		return usageErrorf("usage: aw project set-task-documents --location repo|aw <projectId>")
	}
	projectID := fs.Args()[0]
	payload, err := json.Marshal(struct {
		Location string `json:"location"`
	}{value})
	if err != nil {
		return err
	}
	return projectMutation(ctx, deps, stdout, projectID, *principalConfigPath, *idempotencyKey, "SetProjectTaskDocuments", 0, payload,
		func(ctx context.Context, cmd ports.Command) (any, error) {
			return appcatalog.SetTaskDocuments(ctx, deps.UoW, cmd, projectID, value)
		})
}
