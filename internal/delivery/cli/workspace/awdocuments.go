package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/awdocuments"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// V12-33 (ADR-065): `aw repository-workspace aw-documents list|export`, the CLI faces of listAwDocuments and exportAwDocuments.

func init() {
	cli.MustRegister(cli.Descriptor{Path: []string{"repository-workspace", "aw-documents", "list"}, Scope: cli.ScopeProject,
		AppOperation: "ListAwDocuments", HTTPOperationID: "listAwDocuments"})
	cli.MustRegister(cli.Descriptor{Path: []string{"repository-workspace", "aw-documents", "export"}, Scope: cli.ScopeProject,
		AppOperation: "ExportAwDocuments", HTTPOperationID: "exportAwDocuments"})
}

func awDocumentsDeps(deps Dependencies) (awdocuments.Deps, error) {
	store, _ := deps.HeadAuditor.(ports.AwDocumentStore)
	if store == nil {
		return awdocuments.Deps{}, errors.New("workspace: this process has no Git workspace provider (pass --workspace-root)")
	}
	return awdocuments.Deps{UnitOfWork: deps.UOW, Store: store}, nil
}

// RunRepositoryWorkspaceAwDocumentsList implements `aw repository-workspace aw-documents list --project-id <p> <repositoryWorkspaceId>`.
func RunRepositoryWorkspaceAwDocumentsList(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository-workspace aw-documents list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return usageErrorf("usage: aw repository-workspace aw-documents list --project-id <projectId> <repositoryWorkspaceId>")
	}
	if err := requireProjectID(*projectID); err != nil {
		return err
	}
	d, err := awDocumentsDeps(deps)
	if err != nil {
		return err
	}
	list, err := awdocuments.ListDocuments(ctx, d, *projectID, fs.Args()[0])
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, list)
}

// RunRepositoryWorkspaceAwDocumentsExport implements `aw repository-workspace aw-documents export --project-id <p> [--overwrite]
// <repositoryWorkspaceId>`: copies .aw/features/ into docs/features/, uncommitted.
func RunRepositoryWorkspaceAwDocumentsExport(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository-workspace aw-documents export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	projectID := cli.BindProjectFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	overwrite := fs.Bool("overwrite", false, "replace documents that already exist in docs/features/ with other content")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return usageErrorf("usage: aw repository-workspace aw-documents export --project-id <projectId> [--overwrite] <repositoryWorkspaceId>")
	}
	if err := requireProjectID(*projectID); err != nil {
		return err
	}
	d, err := awDocumentsDeps(deps)
	if err != nil {
		return err
	}
	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]bool{"overwrite": *overwrite})
	if err != nil {
		return err
	}
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: "ExportAwDocuments", Scope: ports.ProjectScope(*projectID), NormalizedPayload: body,
		IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})
	dispatched, err := cli.Dispatch(ctx, deps.UOW, envelope.Command, func(ctx context.Context) (any, error) {
		return awdocuments.Export(ctx, d, envelope.Command, *projectID, fs.Args()[0], *overwrite)
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result})
}
