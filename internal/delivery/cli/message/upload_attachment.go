package message

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"strings"

	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	messagedomain "github.com/taQuangLing/agent-workflow/internal/domain/message"
)

// attachmentWireMetadata mirrors internal/delivery/httpapi/message/attachment.go's
// own attachmentMetadata field for field, tag for tag — this package's own
// canonicalizable metadata shape, marshaled to build cli.BuildEnvelope's own
// NormalizedPayload (with the declared digest as its own separate
// ExtraContentDigest, exactly the way httpapi's own prepareAttachmentCommand
// calls httpapi.SemanticHash: metadata canonicalized, digest passed
// alongside as a distinct argument, never folded into the same JSON blob).
// Sensitivity here is always the RESOLVED wire value (sensitivityWireFor),
// never the operator's own raw --sensitivity string — see that function's
// own doc comment for why, mirrored here identically to httpapi's own
// attachment route.
type attachmentWireMetadata struct {
	WorkItemID  string `json:"workItemId"`
	AttemptID   string `json:"attemptId,omitempty"`
	Role        string `json:"role"`
	ContentType string `json:"contentType"`
	Sensitivity string `json:"sensitivity"`
}

// RunMessageUploadAttachment implements `aw message upload-attachment
// <workItemId> --project-id <id> --role <role> [--attempt-id <id>]
// [--content-type <type>] [--sensitivity <level>] [--sha256 <digest>]
// [--file <path>]` (or pipe the content via stdin) — the CommandEnvelope
// mutation flow over appmessage.AppendConversationAttachment, mirroring
// internal/delivery/httpapi/message/attachment.go's own
// handleAppendConversationAttachment step for step: reload the route's own
// authoritative WorkItem FIRST (reloadWorkItem) → validate --role (required,
// unlike append's own optional --role) → read a bounded body
// (cli.ReadBoundedInput, capped at appmessage.MaxAttachmentSize — the "Size"
// Verify bullet) → resolve the declared digest → canonicalize metadata →
// cli.BuildEnvelope → cli.Dispatch → cli.EncodeCommandResult.
//
// --content-type defaults to "application/octet-stream" when omitted — HTTP's
// own Content-Type header is required with no default; this leaf adds one as
// a documented CLI-only convenience, never a silent behavior gap
// (appmessage.AppendConversationAttachment itself still requires a
// non-empty ContentType either way).
//
// # Digest handling (this task's own "digest computed locally by default,
// or an explicit --sha256 override" design)
//
// appmessage.AppendConversationAttachment only ever VERIFIES a declared
// digest against the actual durably-stored bytes — it never computes one.
// When --sha256 is omitted, this function computes the SHA-256 hex digest
// of the bounded-read content ITSELF (crypto/sha256) and passes that as
// DeclaredSHA256 — the common case, needing no operator effort. An explicit
// --sha256 OVERRIDES that local computation: the one deliberate escape
// hatch for a digest computed elsewhere (a different machine, before piping
// the file over) or, just as usefully, for deliberately exercising
// appmessage.ErrAttachmentDigestMismatch's own tamper-detection path with a
// digest that does NOT match the actual bytes (the "Tamper" Verify
// bullet) — appmessage never silently accepts a mismatch either way.
func RunMessageUploadAttachment(ctx context.Context, deps Dependencies, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("message upload-attachment", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	attemptID := fs.String("attempt-id", "", "execution attempt ID this attachment is linked to (omit for none)")
	roleRaw := fs.String("role", "", "attachment role (USER, ASSISTANT, SYSTEM, TOOL) — required")
	contentType := fs.String("content-type", "application/octet-stream", "attachment media type")
	sensitivityRaw := fs.String("sensitivity", "", "structural sensitivity (PUBLIC, SENSITIVE, SECRET); empty defaults to PUBLIC")
	sha256Override := fs.String("sha256", "", "declared SHA-256 hex digest of the content (default: computed locally from the bounded input)")
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	filePath := cli.BindFileFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw message upload-attachment <workItemId> --project-id <projectId> --role <role> [--attempt-id <id>] [--content-type <type>] [--sensitivity <level>] [--sha256 <digest>] [--file <path>]")
	}
	workItemID := positional[0]
	if strings.TrimSpace(*roleRaw) == "" {
		return usageErrorf("--role is required")
	}

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

	raw, err := cli.ReadBoundedInput(stdin, *filePath, appmessage.MaxAttachmentSize)
	if err != nil {
		return cli.UsageError{Err: err}
	}
	if len(raw) == 0 {
		return usageErrorf("attachment content is required (--file or stdin)")
	}

	declaredSHA256 := strings.ToLower(strings.TrimSpace(*sha256Override))
	if declaredSHA256 == "" {
		sum := sha256.Sum256(raw)
		declaredSHA256 = hex.EncodeToString(sum[:])
		cli.Diagnosticf(stderr, "message upload-attachment: computed local sha256 digest %s (%d bytes)", declaredSHA256, len(raw))
	} else {
		cli.Diagnosticf(stderr, "message upload-attachment: using operator-declared sha256 digest %s (%d bytes)", declaredSHA256, len(raw))
	}

	metadata := attachmentWireMetadata{
		WorkItemID: workItemID, AttemptID: *attemptID, Role: string(role),
		ContentType: *contentType, Sensitivity: string(sensitivityWireFor(sensitivity)),
	}
	canonical, err := json.Marshal(metadata)
	if err != nil {
		return err
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	clk := resolveClock(deps.Clock)
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: appmessage.AttachmentCommandType, Scope: ports.ProjectScope(*projectID),
		NormalizedPayload: canonical, ExtraContentDigest: declaredSHA256,
		IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: clk.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UnitOfWork, envelope.Command, func(ctx context.Context) (any, error) {
		return appmessage.AppendConversationAttachment(ctx, deps.UnitOfWork, deps.ArtifactStore, deps.IDs, clk, envelope.Command, appmessage.AppendConversationAttachmentRequest{
			ProjectID: *projectID, WorkItemID: workItemID, AttemptID: *attemptID, Role: role,
			Body: bytes.NewReader(raw), ContentType: *contentType, Sensitivity: sensitivity, DeclaredSHA256: declaredSHA256,
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
