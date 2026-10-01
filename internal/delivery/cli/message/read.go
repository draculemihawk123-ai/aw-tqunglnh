package message

import (
	"context"
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// The two read leaves that close the Message half of the parity ledger
// (docs/design/10-v8-alpha-hardening.md V8-12R-01): `aw message content` and
// `aw message context-snapshot`, the CLI twins of the HTTP routes
// getMessageContent and getMessageContextSnapshot. Both are PROJECT-scoped
// plain reads — no CommandEnvelope, no idempotency key — exactly like `aw
// message list`.
func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"message", "content"}, Scope: cli.ScopeProject,
		AppOperation: "ResolveMessageContent", HTTPOperationID: "getMessageContent",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"message", "context-snapshot"}, Scope: cli.ScopeProject,
		AppOperation: "GetContextSnapshot", HTTPOperationID: "getMessageContextSnapshot",
	})
}

// messageContentSummary is the bounded metadata `aw message content` reports
// about the bytes it wrote: the same fields runtime.ArtifactSummary carries for
// an evidence artifact (content hash, size, media type, sensitivity,
// redaction) and — like it — never a locator or filesystem path.
type messageContentSummary struct {
	MessageID   string `json:"messageId"`
	ContentHash string `json:"contentHash"`
	Size        int64  `json:"size"`
	MediaType   string `json:"mediaType"`
	Sensitivity string `json:"sensitivity"`
	Redacted    bool   `json:"redacted"`
}

// RunMessageContent implements `aw message content <workItemId> <messageId>
// --project-id <id> --output <path|->`: the canonical content bytes of one
// Message, the CLI twin of GET .../messages/{messageId}/content. It mirrors
// that handler's order exactly — (1) the WorkItem is reloaded and
// scope-checked, (2) appmessage.ResolveMessageContent resolves and
// cross-checks the Message and its content Artifact against the project,
// (3) ArtifactStore.Verify re-hashes the stored bytes BEFORE (4)
// ArtifactStore.Open returns a reader — so a tampered artifact is caught with
// no output target ever created. The result is a byte stream, never a JSON
// document; with `--output -` the bytes go to stdout alone and the bounded
// metadata goes to stderr, with a file the metadata is the one JSON document
// on stdout. HTTP's Range support has no CLI counterpart (a file or pipe
// already gives the caller the whole stream).
func RunMessageContent(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("message content", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	output := cli.BindOutputFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 2 {
		return usageErrorf("usage: aw message content <workItemId> <messageId> --project-id <projectId> --output <path|->")
	}
	workItemID, messageID := positional[0], positional[1]
	if strings.TrimSpace(messageID) == "" {
		return usageErrorf("<messageId> argument is required")
	}
	if strings.TrimSpace(*output) == "" {
		return usageErrorf("--output is required (a real file path, or - for stdout)")
	}

	if _, err := reloadWorkItem(ctx, deps.UnitOfWork, *projectID, workItemID); err != nil {
		return err
	}
	ref, err := appmessage.ResolveMessageContent(ctx, deps.UnitOfWork, *projectID, workItemID, messageID)
	if err != nil {
		return err
	}
	if err := deps.ArtifactStore.Verify(ctx, ref); err != nil {
		return errors.New(sanitizeStoreError(err))
	}
	reader, err := deps.ArtifactStore.Open(ctx, ref)
	if err != nil {
		return errors.New(sanitizeStoreError(err))
	}
	defer reader.Close()

	summary := messageContentSummary{
		MessageID: messageID, ContentHash: ref.SHA256, Size: ref.Size, MediaType: ref.ContentType,
		Sensitivity: string(sensitivityWireFor(ref.Sensitivity)), Redacted: ref.Redacted,
	}
	if *output == "-" {
		cli.Diagnosticf(stderr, "messageId=%s contentHash=%s size=%d mediaType=%s sensitivity=%s redacted=%t",
			summary.MessageID, summary.ContentHash, summary.Size, summary.MediaType, summary.Sensitivity, summary.Redacted)
		_, err := cli.WriteBinaryOutput(stdout, *output, reader)
		return err
	}
	if _, err := cli.WriteBinaryOutput(stdout, *output, reader); err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, summary)
}

// RunMessageContextSnapshot implements `aw message context-snapshot
// <workItemId> <messageId> --project-id <id>`: the bounded, read-only view of
// the ContextSnapshot bound to a Message's own execution Attempt, the CLI twin
// of GET .../messages/{messageId}/context-snapshot. Both surfaces call the one
// application query runtime.GetContextSnapshotForMessage; the two "nothing to
// show" outcomes (the Message has no linked Attempt; the Attempt has not
// produced a snapshot yet) are reported as not-found errors with the same
// wording the HTTP 404s use.
func RunMessageContextSnapshot(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("message context-snapshot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 2 {
		return usageErrorf("usage: aw message context-snapshot <workItemId> <messageId> --project-id <projectId>")
	}
	workItemID, messageID := positional[0], positional[1]
	if strings.TrimSpace(messageID) == "" {
		return usageErrorf("<messageId> argument is required")
	}
	if _, err := reloadWorkItem(ctx, deps.UnitOfWork, *projectID, workItemID); err != nil {
		return err
	}

	detail, err := runtimeapp.GetContextSnapshotForMessage(ctx, deps.UnitOfWork, ports.ProjectScope(*projectID), workItemID, messageID)
	switch {
	case errors.Is(err, runtimeapp.ErrMessageHasNoAttempt):
		return apperror.New(apperror.CodeNotFound, "message has no linked execution attempt", false)
	case errors.Is(err, runtimeapp.ErrContextSnapshotNotYetAvailable):
		return apperror.New(apperror.CodeNotFound, "linked execution attempt has not produced a context snapshot yet", false)
	case err != nil:
		return err
	}
	return cli.EncodeQueryResult(stdout, detail)
}

// sanitizeStoreError converts an error from ports.ArtifactStore.Verify/Open
// into a fixed, locator/filesystem-path-free message: the real store embeds
// the Locator and an on-disk path in plain wrapped errors, which must never
// reach stderr. An *apperror.Error already carries a safe Message and is
// surfaced as is. (Same rule as internal/delivery/cli/evidence's own copy.)
func sanitizeStoreError(err error) string {
	if err == nil {
		return ""
	}
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		return appErr.Message
	}
	return "message content verification failed (stored bytes do not match their recorded hash)"
}
