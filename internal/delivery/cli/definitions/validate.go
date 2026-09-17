package definitions

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	appdefinitions "github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/workflowcompiler"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// RunDefinitionValidate implements `aw definition validate <id> --kind
// <KIND> [--project-id <id>] [--file <path>] [--format json|yaml]
// [--schema-version N]` (document read from --file or stdin) —
// appdefinitions.ValidateDraft's own doc comment: "a read-only dry run: it
// compiles ... a candidate Version without publishing it — no Command
// envelope, because a dry run has no side effect to make idempotent".
// This is why, unlike create/publish, RunDefinitionValidate never calls
// cli.BuildEnvelope/cli.Dispatch at all: there is no mutation, no
// idempotency key, no receipt — ValidateDraft's own signature settles that
// question (it takes no ports.Command), mirroring
// internal/delivery/httpapi/definitions/validate.go's own doc comment
// ("this route requires no Idempotency-Key and never touches the receipt
// store") exactly.
//
// V6-15E's own "Diagnostics" Verify bullet: a compile failure is never
// surfaced as a bare error string. ValidateDraft's own error can be
// authoring.Diagnostics (the eight shared kinds' own decode/validation
// problems, each with a real source Line/Column/Path) or one of
// Workflow's own *workflow.ValidationError/*workflowcompiler.
// ResolutionError/*workflowcompiler.AgentRoleValidationError (free-text
// Problems, no source position — workflow documents are decoded as plain
// JSON, never through authoring.DecodeStrict) — this function detects
// each of those four shapes (mirrors
// internal/delivery/httpapi/definitions/errors.go's own writeCommandError
// switch, enumerated by reading every one of those four types' own
// source) and writes a structured validateResultView{Valid:false,
// Diagnostics:[...]} as the one JSON document on stdout, faithfully
// carrying every diagnostic ValidateDraft's own result already provides —
// never silently dropping detail to a one-line message. The function
// still returns a non-nil error either way (so a future composition
// root's own cli.ExitCodeFor reports failure), but the structured detail
// is already on stdout by the time it does. Any OTHER error (a
// dependency pin that does not exist, a genuine persistence failure) is
// not a "diagnostic" in this sense — it propagates as a plain error, no
// JSON written, exactly like every other query in this package.
func RunDefinitionValidate(ctx context.Context, deps Dependencies, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("definition validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kindRaw := bindKindFlag(fs)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	projectID := bindProjectFlag(fs)
	filePath := cli.BindFileFlag(fs)
	formatRaw := fs.String("format", "json", `document format: "json" or "yaml" (WORKFLOW documents are always json)`)
	schemaVersion := fs.Int("schema-version", 1, "document schema version (ignored for WORKFLOW, whose schemaVersion is a field of the document itself)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw definition validate <id> --kind <KIND> [--project-id <projectId>] [--file <path>] [--format json|yaml] [--schema-version N] (or pipe the document via stdin)")
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

	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	// A dry run mints its own throwaway candidate VersionID/VersionNumber
	// (never persisted) purely so the nine kinds' own Compile/
	// CompileAndResolve functions — which all take a real VersionID/
	// VersionNumber as part of computing CompiledHash — have something to
	// compute against, mirroring
	// internal/delivery/httpapi/definitions/validate.go's own identical
	// versionID/versionNumber=1 minting for the same reason.
	versionID := deps.IDs.NewID()
	compile, wfDefinition, wfRequest, err := buildCandidate(kind, definitionID, fields, documentInput{
		Content: string(raw), Format: format, SchemaVersion: *schemaVersion,
	}, versionID, 1, principal.Actor, now())
	if err != nil {
		return err
	}

	result, err := appdefinitions.ValidateDraft(ctx, deps.UoW, appdefinitions.ValidateDraftRequest{
		Kind: kind, Compile: compile, WorkflowDefinition: wfDefinition, WorkflowRequest: wfRequest,
	})
	if err != nil {
		if diags, ok := diagnosticsFromError(err); ok {
			if encodeErr := cli.EncodeQueryResult(stdout, validateResultView{Valid: false, Diagnostics: diags}); encodeErr != nil {
				return encodeErr
			}
			return err
		}
		return err
	}

	view := newVersionFieldsView(result)
	return cli.EncodeQueryResult(stdout, validateResultView{Valid: true, Version: &view})
}

// diagnosticsFromError detects whether err is one of the four
// diagnostic-bearing shapes ValidateDraft (and PublishDefinitionVersion)
// can return and, if so, translates it into this package's own
// diagnosticView list — mirrors
// internal/delivery/httpapi/definitions/errors.go's own writeDiagnostics/
// writeProblems logic exactly (message formatting included), just
// returning a value instead of writing an HTTP response.
func diagnosticsFromError(err error) ([]diagnosticView, bool) {
	var diags authoring.Diagnostics
	if errors.As(err, &diags) {
		views := make([]diagnosticView, 0, len(diags))
		for _, d := range diags {
			message := d.Error()
			if d.Line > 0 {
				message = fmt.Sprintf("line %d, column %d: %s", d.Line, d.Column, message)
			}
			views = append(views, diagnosticView{Field: d.Path, Message: message})
		}
		return views, true
	}
	var workflowErr *workflow.ValidationError
	if errors.As(err, &workflowErr) {
		return problemsToDiagnostics(workflowErr.Problems), true
	}
	var resolutionErr *workflowcompiler.ResolutionError
	if errors.As(err, &resolutionErr) {
		return problemsToDiagnostics(resolutionErr.Problems), true
	}
	var agentRoleErr *workflowcompiler.AgentRoleValidationError
	if errors.As(err, &agentRoleErr) {
		return problemsToDiagnostics(agentRoleErr.Problems), true
	}
	return nil, false
}

func problemsToDiagnostics(problems []string) []diagnosticView {
	views := make([]diagnosticView, 0, len(problems))
	for _, p := range problems {
		views = append(views, diagnosticView{Message: p})
	}
	return views
}
