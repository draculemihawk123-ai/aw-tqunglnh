package message

import (
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// Dependencies is everything this package's own handlers need — mirrors
// internal/delivery/httpapi/workitem.Dependencies' own shape (a UnitOfWork,
// an ID source, a Clock — no HTTP server/listener concern belongs here),
// extended with the two concerns unique to a conversation-message endpoint:
// a real ports.ArtifactStore and a process-lifetime redact.Matcher/
// CursorCodec, neither of which any route registered by cmd/aw/serve.go has
// needed until now.
type Dependencies struct {
	// UnitOfWork is the one real ports.UnitOfWork every handler in this
	// package dispatches through — this package never opens a second,
	// competing persistence path.
	UnitOfWork ports.UnitOfWork
	// ArtifactStore is the real, composition-root-owned content-addressed
	// store internal/app/message.AppendMessage durably attaches a
	// message's own content to (via internal/app/artifact.PrepareAttachment)
	// — never a second store this package opens on its own. cmd/aw/serve.go
	// constructs exactly one, from its own already-required --artifact-root
	// flag (internal/adapters/artifactstore.New), the same root every other
	// artifact-producing command in this process will ever use.
	ArtifactStore ports.ArtifactStore
	// IDs mints every new Message ID (internal/app/message.AppendMessage
	// takes an idsource.Source itself). A composition root supplies
	// idsource.Random{} in production; a test supplies idsource.Sequential
	// for deterministic assertions.
	IDs idsource.Source
	// Clock supplies cmd.RequestedAt for every command envelope this
	// package builds.
	Clock clock.Clock
	// Matcher is the ONE process-lifetime known-secrets matcher every
	// AppendMessage call in this package uses — cmd/aw/serve.go's own
	// redact.NewMatcher(sessionToken), the identical value already wired
	// into that composition root's own Logger. It is deliberately NOT a
	// caller-supplied HTTP field: redact.Matcher's own bounded contract is
	// exact-value equality against a fixed, already-known set of secrets,
	// never a pattern/regex scan of caller-supplied free text (see
	// internal/app/redact's own package doc comment) — "let an HTTP caller
	// declare its own secret values to redact" is a meaningless request
	// shape under that contract, since the platform's own known secrets
	// are exactly (and only) the ones this process itself minted or
	// resolved, never anything an untrusted request body could name. A
	// message's own Sensitivity (PUBLIC/SENSITIVE/SECRET — dto.go's
	// sensitivityWire) is the one redaction axis this package's own wire
	// contract actually exposes to a caller.
	Matcher redact.Matcher
	// Cursor signs/opens this package's own opaque ListMessages pagination
	// cursor (httpapi.CursorCodec, cursor.go) — a fresh per-process secret
	// the composition root mints exactly like its own per-start session
	// token (idsource.Random{}.NewID()), never a fixed compiled-in value.
	Cursor *httpapi.CursorCodec
}
