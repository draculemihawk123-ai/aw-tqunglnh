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

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"project", "commit-template"}, Scope: cli.ScopeProject,
		AppOperation: "GetProjectCommitTemplate", HTTPOperationID: "getProjectCommitTemplate",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"project", "set-commit-template"}, Scope: cli.ScopeProject,
		AppOperation: "SetProjectCommitTemplate", HTTPOperationID: "setProjectCommitTemplate",
	})
}

// RunProjectCommitTemplate implements `aw project commit-template <projectId>` (V12-25): the project's commit message template.
func RunProjectCommitTemplate(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project commit-template", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return usageErrorf("usage: aw project commit-template <projectId>")
	}
	view, err := appcatalog.GetCommitTemplate(ctx, deps.UoW, fs.Args()[0])
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, view)
}

// RunProjectSetCommitTemplate implements `aw project set-commit-template --template "<template>" <projectId>` (or --clear): the default
// commit message of an approval-bound commit, with {title}, {taskId} and {workItemId}.
func RunProjectSetCommitTemplate(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project set-commit-template", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	template := fs.String("template", "", `the template, e.g. "feat: {title}" (placeholders {title}, {taskId}, {workItemId})`)
	clear := fs.Bool("clear", false, "remove the template ({title} is used)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 || (strings.TrimSpace(*template) == "") == !*clear {
		return usageErrorf(`usage: aw project set-commit-template (--template "<template>" | --clear) <projectId>`)
	}
	projectID := fs.Args()[0]
	value := *template
	if *clear {
		value = ""
	}
	payload, err := json.Marshal(struct {
		Template string `json:"template"`
	}{value})
	if err != nil {
		return err
	}
	return projectMutation(ctx, deps, stdout, projectID, *principalConfigPath, *idempotencyKey, "SetProjectCommitTemplate", 0, payload,
		func(ctx context.Context, cmd ports.Command) (any, error) {
			return appcatalog.SetCommitTemplate(ctx, deps.UoW, cmd, projectID, value)
		})
}
