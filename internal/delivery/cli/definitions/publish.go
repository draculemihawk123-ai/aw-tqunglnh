package definitions

import (
	"context"
	"encoding/json"
	"flag"
	"io"

	appdefinitions "github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// publishDefinitionBody is the struct RunDefinitionPublish canonicalizes
// and marshals for its own RequestHash — mirrors
// internal/delivery/httpapi/definitions/document.go's own authorDocumentBody
// wire shape (minus Dependencies — see dispatch.go's own documentInput
// doc comment for why this package's flag surface omits it), following
// cli.EnvelopeRequest.NormalizedPayload's own doc comment: "decodes it
// into a typed request struct first and re-marshals that struct here,
// exactly like HTTP's own CanonicalizeJSON does, rather than hashing the
// raw bytes a caller happened to send". --file/stdin itself still carries
// only the bare authored document (never this wrapper) — the natural CLI
// ergonomic of pointing --file at your actual BLOCK/SKILL/WORKFLOW
// document, mirroring cmd/aw/definition.go's own --file convention; this
// wrapper exists only to give RequestHash a stable, canonical shape.
type publishDefinitionBody struct {
	Content       string `json:"content"`
	Format        string `json:"format,omitempty"`
	SchemaVersion int    `json:"schemaVersion,omitempty"`
}

// RunDefinitionPublish implements `aw definition publish <id> --kind
// <KIND> [--project-id <id>] [--file <path>] [--format json|yaml]
// [--schema-version N]` (document read from --file or stdin) — a mutation
// over appdefinitions.PublishDefinitionVersion. This is a CREATE-shaped
// mutation (Idempotency-Key optional, generated when omitted; no
// --expected-version) rather than an UPDATE-shaped one, mirroring
// internal/delivery/httpapi/definitions/publish.go's own doc comment
// exactly: publishing genuinely new content always appends a new
// immutable Version, never overwrites an existing one, so there is no
// "current resource state" for an optimistic-concurrency check to
// protect.
//
// The Definition is authoritatively reloaded and scope-checked FIRST
// (loadDefinitionInScope), before --idempotency-key/body are even read —
// mirrors publish.go's own doc comment: the Definition must already exist
// (via `definition create`) and must actually belong to THIS invocation's
// own derived scope, never trusted from any request body.
func RunDefinitionPublish(ctx context.Context, deps Dependencies, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("definition publish", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kindRaw := bindKindFlag(fs)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	projectID := bindProjectFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	filePath := cli.BindFileFlag(fs)
	formatRaw := fs.String("format", "json", `document format: "json" or "yaml" (WORKFLOW documents are always json)`)
	schemaVersion := fs.Int("schema-version", 1, "document schema version (ignored for WORKFLOW, whose schemaVersion is a field of the document itself)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw definition publish <id> --kind <KIND> [--project-id <projectId>] [--file <path>] [--format json|yaml] [--schema-version N] (or pipe the document via stdin)")
	}
	definitionID := positional[0]
	kind, err := parseKind(*kindRaw)
	if err != nil {
		return err
	}
	format, err := parseDocumentFormat(*formatRaw)
	if err != nil {
		return err
	}

	routeScope := definitionScopeFromProjectID(*projectID)
	fields, err := loadDefinitionInScope(ctx, deps.UoW, kind, definitionID, routeScope)
	if err != nil {
		return err
	}

	raw, err := cli.ReadBoundedInput(stdin, *filePath, cli.DefaultMaxInputBytes)
	if err != nil {
		return cli.UsageError{Err: err}
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	normalized, err := json.Marshal(publishDefinitionBody{Content: string(raw), Format: *formatRaw, SchemaVersion: *schemaVersion})
	if err != nil {
		return err
	}

	cmdScope := commandScopeFromProjectID(*projectID)
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypePublishDefinition, Scope: cmdScope,
		NormalizedPayload: normalized, IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		// VersionNumber: a discarded placeholder for the eight shared kinds
		// (internal/adapters/sqlite always reallocates the real next number
		// from the database) — but Workflow genuinely compares
		// candidate.VersionNumber() against the real next version, so only
		// for Workflow this must ask the registry what that number actually
		// is first, exactly like publish.go's own doc comment explains.
		versionNumber := uint64(1)
		if kind == definition.KindWorkflow {
			existing, err := appdefinitions.ListVersions(ctx, deps.UoW, definition.KindWorkflow, definitionID)
			if err != nil {
				return nil, err
			}
			versionNumber = uint64(len(existing)) + 1
		}
		versionID := deps.IDs.NewID()

		compile, wfDefinition, wfRequest, err := buildCandidate(kind, definitionID, fields, documentInput{
			Content: string(raw), Format: format, SchemaVersion: *schemaVersion,
		}, versionID, versionNumber, envelope.Command.Actor, envelope.Command.RequestedAt)
		if err != nil {
			return nil, err
		}

		published, err := appdefinitions.PublishDefinitionVersion(ctx, deps.UoW, envelope.Command, appdefinitions.PublishDefinitionVersionRequest{
			DefinitionID: definitionID, Kind: kind, Compile: compile, WorkflowDefinition: wfDefinition, WorkflowRequest: wfRequest,
		})
		if err != nil {
			return nil, err
		}
		// definition.VersionFields carries no exported fields of its own
		// (V2-01's own immutability discipline) — a fresh (non-replayed)
		// result must be converted to this package's own JSON-tagged
		// versionFieldsView here, exactly like
		// internal/delivery/httpapi/definitions/publish.go's own
		// handlePublishDefinitionVersion does, or json.MarshalIndent below
		// would silently encode it as "{}". A REPLAYED result never reaches
		// this closure at all (cli.Dispatch's own replay short-circuit) —
		// it instead returns the receipt's own stored ResultJSON as
		// json.RawMessage, already in this exact shape (PublishDefinitionVersion
		// writes its receipt via the byte-for-byte identical
		// toVersionFieldsDTO field/tag set), so fresh and replayed
		// responses stay byte-for-byte the same shape either way.
		return newVersionFieldsView(published), nil
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
