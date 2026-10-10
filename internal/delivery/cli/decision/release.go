package decision

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/approvalrelease"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// V11-07 (ADR-040): the CLI mirror of the approve-and-commit read side and restore command — the same
// internal/app/approvalrelease operations internal/delivery/httpapi/decision serves.

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"approval", "release"}, Scope: cli.ScopeProject,
		AppOperation: "GetApprovalRelease", HTTPOperationID: "getApprovalRelease",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"workspace-set", "release-context"}, Scope: cli.ScopeProject,
		AppOperation: "GetFamilyReleaseContext", HTTPOperationID: "getFamilyReleaseContext",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"repository-workspace", "restore-parked"}, Scope: cli.ScopeProject,
		AppOperation: "RestoreParkedChanges", HTTPOperationID: "restoreParkedChanges",
	})
}

const commandTypeRestoreParkedChanges = "RestoreParkedChanges"

func (d Dependencies) queries() approvalrelease.Queries {
	return approvalrelease.Queries{UOW: d.UOW, Identities: d.Release.Identities}
}

// ApprovalRelease implements `aw approval release <runId> <approvalRequestId>`: the commits a decision
// requested, how each went, and the patch the approver reviewed.
func ApprovalRelease(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("approval release", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 2 {
		return usageErrorf("usage: aw approval release <runId> <approvalRequestId>")
	}
	result, err := deps.queries().ApprovalRelease(ctx, positional[0], positional[1])
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, result)
}

// WorkspaceSetReleaseContext implements `aw workspace-set release-context <familyId> --project-id <projectId>`:
// per repository, the git author a commit would carry (or why there is none), plus the parked sets.
func WorkspaceSetReleaseContext(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("workspace-set release-context", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" {
		return usageErrorf("usage: aw workspace-set release-context <familyId> --project-id <projectId>")
	}
	if strings.TrimSpace(*projectID) == "" {
		return usageErrorf("--project-id is required")
	}
	result, err := deps.queries().FamilyReleaseContext(ctx, *projectID, positional[0])
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, result)
}

// RepositoryWorkspaceRestoreParked implements `aw repository-workspace restore-parked <repositoryWorkspaceId>
// --project-id <projectId> --release-set-local-commit-id <id> [--idempotency-key <key>]`: put the files a PARK
// commit moved out of the worktree back.
func RepositoryWorkspaceRestoreParked(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository-workspace restore-parked", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	projectID := cli.BindProjectFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	operationID := fs.String("release-set-local-commit-id", "", "the commit operation whose parked files come back (required; listed by `aw workspace-set release-context`)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" {
		return usageErrorf("usage: aw repository-workspace restore-parked <repositoryWorkspaceId> --project-id <projectId> --release-set-local-commit-id <id>")
	}
	if strings.TrimSpace(*projectID) == "" {
		return usageErrorf("--project-id is required")
	}
	if strings.TrimSpace(*operationID) == "" {
		return usageErrorf("--release-set-local-commit-id is required")
	}
	if deps.Release.Parker == nil {
		return errors.New("decision: this process has no Git workspace provider, so it cannot restore parked changes")
	}
	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}
	repositoryWorkspaceID := positional[0]
	body, err := json.Marshal(struct {
		ReleaseSetLocalCommitID string `json:"releaseSetLocalCommitId"`
	}{*operationID})
	if err != nil {
		return err
	}
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeRestoreParkedChanges, Scope: ports.ProjectScope(*projectID),
		NormalizedPayload: append([]byte(repositoryWorkspaceID+"\x00"), body...),
		IdempotencyKey:    *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})
	dispatched, err := cli.Dispatch(ctx, deps.UOW, envelope.Command, func(ctx context.Context) (any, error) {
		return approvalrelease.RestoreParked(ctx, approvalrelease.RestoreDeps{UOW: deps.UOW, Parker: deps.Release.Parker}, envelope.Command,
			approvalrelease.RestoreRequest{ProjectID: *projectID, RepositoryWorkspaceID: repositoryWorkspaceID, ReleaseSetLocalCommitID: *operationID})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}

// commitSpec is the --commit-spec JSON: byte-for-byte the shape of the HTTP body's "commit" object.
type commitSpec struct {
	Message      string                 `json:"message"`
	Repositories []commitSpecRepository `json:"repositories"`
}

type commitSpecRepository struct {
	RepositoryWorkspaceID    string           `json:"repositoryWorkspaceId"`
	ExpectedWorkspaceVersion uint64           `json:"expectedWorkspaceVersion"`
	Files                    []commitSpecFile `json:"files"`
	Unselected               string           `json:"unselected,omitempty"`
}

type commitSpecFile struct {
	Path        string `json:"path"`
	Status      string `json:"status,omitempty"`
	ContentHash string `json:"contentHash,omitempty"`
}

// parseCommitSpec reads --commit-spec: JSON text, or @path to read it from a file. An empty value means no commit.
func parseCommitSpec(value string) (*commitSpec, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	raw := []byte(value)
	if strings.HasPrefix(value, "@") {
		data, err := os.ReadFile(strings.TrimPrefix(value, "@"))
		if err != nil {
			return nil, usageErrorf("--commit-spec: %v", err)
		}
		raw = data
	}
	if len(raw) > 1<<20 {
		return nil, usageErrorf("--commit-spec is larger than 1 MiB")
	}
	var spec commitSpec
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return nil, usageErrorf("--commit-spec is not a valid commit specification: %v", err)
	}
	return &spec, nil
}

func (s *commitSpec) input() approvalrelease.CommitInput {
	input := approvalrelease.CommitInput{Message: s.Message}
	for _, repository := range s.Repositories {
		files := make([]approvalrelease.FileInput, 0, len(repository.Files))
		for _, file := range repository.Files {
			files = append(files, approvalrelease.FileInput{Path: file.Path, Status: file.Status, ContentHash: file.ContentHash})
		}
		input.Repositories = append(input.Repositories, approvalrelease.RepositoryInput{
			RepositoryWorkspaceID: repository.RepositoryWorkspaceID, ExpectedWorkspaceVersion: repository.ExpectedWorkspaceVersion,
			Files: files, Unselected: repository.Unselected,
		})
	}
	return input
}

