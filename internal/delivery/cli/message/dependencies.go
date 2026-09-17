package message

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// Dependencies is everything every Run* function in this package needs —
// the CLI-side counterpart of internal/delivery/httpapi/message.Dependencies
// (dependencies.go), mirrored field for field except for the HTTP-only
// Cursor concern (ListMessages is not paginated by this leaf — see list.go's
// own doc comment for why). A future composition root (V6-15O) constructs
// one from its own already-built ports.UnitOfWork/ports.ArtifactStore/
// idsource.Source/clock.Clock/redact.Matcher and passes it into whichever
// Run* function it dispatches to; this package never reaches for a global
// or constructs any of these itself.
type Dependencies struct {
	// UnitOfWork is the one real ports.UnitOfWork every Run* function in
	// this package dispatches through.
	UnitOfWork ports.UnitOfWork
	// ArtifactStore is the real, composition-root-owned content-addressed
	// store both AppendMessage and AppendConversationAttachment durably
	// attach content to — never a second store this package opens on its
	// own.
	ArtifactStore ports.ArtifactStore
	// IDs mints every new Message/Artifact ID (and, when --idempotency-key
	// is omitted, a fresh idempotency key via cli.BuildEnvelope). A
	// composition root supplies idsource.Random{} in production; a test
	// supplies idsource.Sequential for deterministic assertions.
	IDs idsource.Source
	// Clock supplies cmd.RequestedAt for every command envelope this
	// package builds, and the "now" AppendMessage/AppendConversationAttachment
	// themselves use. Defaults to clock.System{} when nil — a caller only
	// needs to supply this for deterministic tests, mirroring
	// internal/delivery/cli/settings's own identical Clock doc comment.
	Clock clock.Clock
	// Matcher optionally redacts an exact-match known secret out of a plain
	// message's own content (appmessage.AppendMessageRequest.Matcher) — the
	// zero value is safe and performs no exact-value matching, leaving only
	// Sensitivity's own structural redaction in effect. Never used by
	// upload-attachment (binary content is never scanned for an exact-match
	// secret, mirroring internal/delivery/httpapi/message/attachment.go's
	// own identical omission) or by `list` (a plain read never redacts —
	// content is never inlined in a listing response either way).
	Matcher redact.Matcher
}

// resolveClock mirrors internal/delivery/cli/settings's own identical
// inline pattern (RunUpdate: "clk := deps.Clock; if clk == nil { clk =
// clock.System{} }"), promoted here to a small shared helper since every
// mutating Run* function in this package needs the identical default.
func resolveClock(c clock.Clock) clock.Clock {
	if c == nil {
		return clock.System{}
	}
	return c
}

// usageErrorf builds a cli.UsageError from a formatted message — mirrors
// internal/delivery/cli/definitions's own usageErrorf exactly.
func usageErrorf(format string, args ...any) error {
	return cli.UsageError{Err: fmt.Errorf(format, args...)}
}

// parseFlags mirrors internal/delivery/cli/definitions's own parseFlags
// exactly: the one place every Run* function's own flag handling goes
// through, wrapping a parse failure as a cli.UsageError and passing
// flag.ErrHelp through unwrapped.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return err
		}
		return cli.UsageError{Err: err}
	}
	return nil
}

// loadPrincipal resolves the acting principal exactly the way
// internal/delivery/cli/definitions/catalog's own loadPrincipal (and `aw
// serve`'s own --principal-config flag) already does: load the trusted JSON
// config file (or the local-operator/[operator] default when path is
// empty/missing/omits localPrincipal), then validate its shape. This is the
// ONLY place any Run* function in this package ever resolves a principal
// from — never a per-invocation --actor/--role flag, per ADR-028 and
// cli.BindPrincipalFlag's own doc comment.
func loadPrincipal(path string) (config.LocalPrincipal, error) {
	principal, err := config.LoadLocalPrincipalFile(path)
	if err != nil {
		return config.LocalPrincipal{}, err
	}
	if err := config.ValidateLocalPrincipal(principal); err != nil {
		return config.LocalPrincipal{}, err
	}
	return principal, nil
}

// --- Sensitivity wire vocabulary ---
//
// Mirrors internal/delivery/httpapi/message/dto.go's own sensitivityWire/
// parseSensitivity exactly (same three constants, same case-sensitive
// comparison, same "empty means PUBLIC" default) so a --sensitivity value an
// operator types matches, byte for byte, what an equivalent HTTP request
// body/header would have produced — required for cli.BuildEnvelope's own
// RequestHash to ever agree with an HTTP-side hash for the "same" request
// (see internal/delivery/cli.BuildEnvelope's own doc comment).

type sensitivityWire string

const (
	sensitivityPublic    sensitivityWire = "PUBLIC"
	sensitivitySensitive sensitivityWire = "SENSITIVE"
	sensitivitySecret    sensitivityWire = "SECRET"
)

func parseSensitivity(raw string) (redact.Sensitivity, error) {
	switch sensitivityWire(raw) {
	case "", sensitivityPublic:
		return redact.Public, nil
	case sensitivitySensitive:
		return redact.Sensitive, nil
	case sensitivitySecret:
		return redact.Secret, nil
	default:
		return 0, fmt.Errorf("unknown sensitivity %q (want PUBLIC, SENSITIVE or SECRET)", raw)
	}
}

// sensitivityWireFor is parseSensitivity's own inverse — used only by
// upload-attachment (mirroring httpapi/message/attachment.go's own
// identically named function) so its own canonical hashing metadata always
// carries an explicit, resolved value rather than an empty string that
// would hash differently than "PUBLIC" for what is semantically the
// identical request (see that function's own doc comment for the full
// reasoning). `aw message append` deliberately does NOT use this: it
// canonicalizes the operator's own raw --sensitivity string unchanged,
// mirroring httpapi/message/append.go's own appendMessageBody.Sensitivity
// passthrough exactly.
func sensitivityWireFor(s redact.Sensitivity) sensitivityWire {
	switch s {
	case redact.Sensitive:
		return sensitivitySensitive
	case redact.Secret:
		return sensitivitySecret
	default:
		return sensitivityPublic
	}
}

// ErrWorkItemNotFound is this package's own leakage-normalized not-found
// sentinel — returned by reloadWorkItem for BOTH "no such WorkItem exists"
// and "it exists, but belongs to a different project than --project-id
// named", so an operator can never use a wrong-project invocation to learn
// whether some WorkItemID exists at all. Mirrors
// internal/delivery/httpapi/message/errors.go's own writeQueryError/
// WriteResourceHidden discipline, translated to a plain Go sentinel error
// since a CLI response has no HTTP status code to normalize — the identical
// translation internal/delivery/cli/definitions's own ErrDefinitionNotFound
// already establishes for its own package.
var ErrWorkItemNotFound = errors.New("cli/message: work item not found")

// reloadWorkItem is this package's own shared preamble for every Run*
// function: require --project-id (a Message/attachment command is always
// project-scoped, never installation-scoped — internal/delivery/httpapi/message's
// own RouteDescriptor.ScopeKind is httpapi.ScopeProject for all three routes
// it registers), then reload workItemID's own real, authoritative detail via
// internal/app/work.GetWorkItem and confirm it actually belongs to that
// project — mirroring internal/delivery/httpapi/message/routes.go's own
// package doc comment ("không tin ID shape, payload hoặc projection") and
// every one of its own handlers' identical first step, applied here once so
// list.go/append.go/upload_attachment.go never each reimplement it.
func reloadWorkItem(ctx context.Context, uow ports.UnitOfWork, projectID, workItemID string) (workapp.WorkItemDetail, error) {
	if strings.TrimSpace(projectID) == "" {
		return workapp.WorkItemDetail{}, usageErrorf("--project-id is required")
	}
	if strings.TrimSpace(workItemID) == "" {
		return workapp.WorkItemDetail{}, usageErrorf("<workItemId> argument is required")
	}
	detail, err := workapp.GetWorkItem(ctx, uow, ports.ProjectScope(projectID), workItemID)
	if err != nil {
		if errors.Is(err, ports.ErrPersistenceNotFound) || errors.Is(err, ports.ErrScopeMismatch) {
			return workapp.WorkItemDetail{}, ErrWorkItemNotFound
		}
		return workapp.WorkItemDetail{}, err
	}
	return detail, nil
}
