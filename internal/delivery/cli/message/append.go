package message

import (
	"context"
	"encoding/json"
	"flag"
	"io"

	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	messagedomain "github.com/taQuangLing/agent-workflow/internal/domain/message"
)

// appendMessageWire mirrors internal/delivery/httpapi/message/append.go's
// own appendMessageBody field for field, tag for tag — this package's own
// canonicalizable request shape, marshaled to build cli.BuildEnvelope's own
// NormalizedPayload exactly the way that function's own doc comment
// prescribes ("decodes it into a typed request struct first and re-marshals
// that struct here, exactly like HTTP's own CanonicalizeJSON does"). Keeping
// this byte-for-byte identical to appendMessageBody is what lets an HTTP
// call and a CLI call for the "same" AppendMessage request hash (and
// therefore replay) identically.
type appendMessageWire struct {
	AttemptID   string `json:"attemptId,omitempty"`
	Role        string `json:"role"`
	Content     string `json:"content"`
	ContentType string `json:"contentType"`
	Sensitivity string `json:"sensitivity,omitempty"`
}

// RunMessageAppend implements `aw message append <workItemId> --project-id
// <id> [--attempt-id <id>] [--role <role>] [--content-type <type>]
// [--sensitivity <level>] [--file <path>]` (or pipe the content via stdin)
// — the full CommandEnvelope mutation flow over appmessage.AppendMessage,
// mirroring internal/delivery/httpapi/message/append.go's own
// handleAppendMessage step for step: reload the route's own authoritative
// WorkItem FIRST (reloadWorkItem) → validate --role/--sensitivity → read a
// bounded body (cli.ReadBoundedInput, capped at appmessage.MaxContentSize —
// the "Size" Verify bullet: an oversized --file/stdin is rejected cleanly by
// this bound, never silently truncated) → canonicalize → cli.BuildEnvelope →
// cli.Dispatch (receipt lookup/replay/dispatch) → cli.EncodeCommandResult.
//
// --role defaults to USER when omitted (this task's own command-surface
// line brackets --role as optional, unlike upload-attachment's own required
// --role) — the common case of a human operator appending their own
// message.  --content-type defaults to "text/plain" when omitted — HTTP's
// own appendMessageBody requires a caller-supplied value with no default;
// this leaf adds one as a documented CLI-only convenience, never a silent
// behavior gap (appmessage.AppendMessage itself still requires a non-empty
// ContentType either way).
func RunMessageAppend(ctx context.Context, deps Dependencies, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("message append", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	attemptID := fs.String("attempt-id", "", "execution attempt ID this message is linked to (omit for none)")
	roleRaw := fs.String("role", string(messagedomain.RoleUser), "message role (USER, ASSISTANT, SYSTEM, TOOL)")
	contentType := fs.String("content-type", "text/plain", "content media type")
	sensitivityRaw := fs.String("sensitivity", "", "structural sensitivity (PUBLIC, SENSITIVE, SECRET); empty defaults to PUBLIC")
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	filePath := cli.BindFileFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw message append <workItemId> --project-id <projectId> [--attempt-id <id>] [--role <role>] [--content-type <type>] [--sensitivity <level>] [--file <path>]")
	}
	workItemID := positional[0]

	if _, err := reloadWorkItem(ctx, deps.UnitOfWork, *projectID, workItemID); err != nil {
		return err
	}

	role := messagedomain.Role(*roleRaw)
	if !role.Valid() {
		return usageErrorf("--role must be one of USER, ASSISTANT, SYSTEM, TOOL (got %q)", *roleRaw)
	}
	sensitivity, err := parseSensitivity(*sensitivityRaw)
	if err != nil {
		return usageErrorf("--sensitivity: %v", err)
	}

	raw, err := cli.ReadBoundedInput(stdin, *filePath, appmessage.MaxContentSize)
	if err != nil {
		return cli.UsageError{Err: err}
	}
	if len(raw) == 0 {
		return usageErrorf("message content is required (--file or stdin)")
	}

	wire := appendMessageWire{
		AttemptID: *attemptID, Role: string(role), Content: string(raw),
		ContentType: *contentType, Sensitivity: *sensitivityRaw,
	}
	normalized, err := json.Marshal(wire)
	if err != nil {
		return err
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	// Diagnostic/progress text — always stderr, never mixed into stdout's
	// one JSON document (cli.Diagnosticf's own "diagnostics stderr" rule;
	// this task's own "Stdout/stderr separation" Verify bullet).
	cli.Diagnosticf(stderr, "message append: work-item=%s role=%s bytes=%d", workItemID, role, len(raw))

	clk := resolveClock(deps.Clock)
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeAppendMessage, Scope: ports.ProjectScope(*projectID),
		NormalizedPayload: normalized, IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: clk.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UnitOfWork, envelope.Command, func(ctx context.Context) (any, error) {
		return appmessage.AppendMessage(ctx, deps.UnitOfWork, deps.ArtifactStore, deps.IDs, clk, envelope.Command, appmessage.AppendMessageRequest{
			ProjectID: *projectID, WorkItemID: workItemID, AttemptID: *attemptID, Role: role,
			Content: raw, ContentType: *contentType, Sensitivity: sensitivity, Matcher: deps.Matcher,
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
