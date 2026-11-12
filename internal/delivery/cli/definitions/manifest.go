package definitions

import (
	"context"
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/manifestpublish"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"definition", "manifest-status"}, Scope: cli.ScopeProject,
		AppOperation: "GetDefinitionManifest", HTTPOperationID: "getDefinitionManifest",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"definition", "manifest-run"}, Scope: cli.ScopeProject,
		AppOperation: "RunDefinitionManifest", HTTPOperationID: "runDefinitionManifest",
	})
}

// ManifestDependencies is what the manifest leaves need: a builder of the kit publisher runner for the manifest named on the
// command line (the CLI twin of `aw serve --project-manifest`).
type ManifestDependencies struct {
	NewPublisher func(manifests map[string]string) (ports.ManifestPublisher, error)
}

func manifestPublisher(deps ManifestDependencies, projectID, manifest string) (ports.ManifestPublisher, error) {
	if deps.NewPublisher == nil {
		return nil, errors.New("definitions: this process cannot run the kit publisher")
	}
	return deps.NewPublisher(map[string]string{projectID: manifest})
}

// RunDefinitionManifestStatus implements `aw definition manifest-status --project-id <p> --manifest <aw-project.json>` (V12-21): the
// manifest and the kit publisher (aw-publish.py) it resolves to.
func RunDefinitionManifestStatus(ctx context.Context, deps ManifestDependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("definition manifest-status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	manifest := fs.String("manifest", "", "path to the project's aw-project.json (required)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*projectID) == "" || strings.TrimSpace(*manifest) == "" || fs.NArg() != 0 {
		return usageErrorf("usage: aw definition manifest-status --project-id <projectId> --manifest <aw-project.json>")
	}
	publisher, err := manifestPublisher(deps, *projectID, *manifest)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, manifestpublish.Status(publisher, *projectID))
}

// RunDefinitionManifestRun implements `aw definition manifest-run --project-id <p> --manifest <aw-project.json> --mode CHECK|PUBLISH`
// (V12-21): run the kit's aw-publish.py over the manifest, as the UI's Check and Publish buttons do.
func RunDefinitionManifestRun(ctx context.Context, deps ManifestDependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("definition manifest-run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	manifest := fs.String("manifest", "", "path to the project's aw-project.json (required)")
	mode := fs.String("mode", ports.ManifestCheck, "CHECK (the kit's checks only) or PUBLISH")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*projectID) == "" || strings.TrimSpace(*manifest) == "" || fs.NArg() != 0 {
		return usageErrorf("usage: aw definition manifest-run --project-id <projectId> --manifest <aw-project.json> [--mode CHECK|PUBLISH]")
	}
	publisher, err := manifestPublisher(deps, *projectID, *manifest)
	if err != nil {
		return err
	}
	run, err := manifestpublish.Run(ctx, publisher, *projectID, strings.ToUpper(*mode))
	if err != nil {
		return err
	}
	if err := cli.EncodeQueryResult(stdout, run); err != nil {
		return err
	}
	if !run.Succeeded {
		return errors.New("definitions: the kit publisher failed (see output)")
	}
	return nil
}
