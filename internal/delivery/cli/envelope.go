package cli

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// EnvelopeRequest is everything BuildEnvelope needs to construct one
// ports.Command. Principal is the ONLY source of Actor/ActorRoles — there
// is deliberately no field here through which a caller could supply a
// different actor/roles per invocation (ADR-028's own "spoof actor
// absent" rule; see envelope_dispatch_test.go's own
// TestBuildEnvelopeNeverPopulatesActorFromAnythingButPrincipal). A leaf
// resolves Principal exactly once, at startup, via
// config.LoadLocalPrincipalFile(*principalConfigPathFlag) +
// config.ValidateLocalPrincipal — the same mechanism `aw serve` already
// uses (cmd/aw/serve.go) — and passes the same value into every
// EnvelopeRequest it ever builds for that process invocation.
type EnvelopeRequest struct {
	Principal config.LocalPrincipal
	// CommandType is the application command name (e.g.
	// "CreateDefinition") — SemanticHash's own commandType input.
	CommandType string
	Scope       ports.CommandScope
	// NormalizedPayload is the command's own canonicalized business
	// payload — the same "canonical, key-order-independent,
	// whitespace-independent" encoding CanonicalizeJSON produces on the
	// HTTP side (commandenvelope.go); a leaf that reads its request body
	// via ReadBoundedInput decodes it into a typed request struct first
	// and re-marshals that struct here, exactly like HTTP's own
	// CanonicalizeJSON does, rather than hashing the raw bytes a caller
	// happened to send.
	NormalizedPayload []byte
	// ExtraContentDigest is SemanticHash's own optional exact-content
	// digest for a command carrying raw/binary content no JSON
	// canonicalization applies to — "" when there is none (SemanticHash's
	// own doc comment).
	ExtraContentDigest string
	ExpectedVersion    uint64
	// IdempotencyKey is the operator-supplied --idempotency-key value, or
	// "" to have BuildEnvelope generate one (see
	// Envelope.IdempotencyKeyGenerated and EncodeCommandResult's own
	// "generated key returned" contract).
	IdempotencyKey string
	// IDSource generates a fresh idempotency key when IdempotencyKey is
	// "". Defaults to idsource.Random{} when nil — a leaf only needs to
	// supply this for deterministic tests.
	IDSource idsource.Source
	// Now defaults to time.Now().UTC when nil — a leaf only needs to
	// supply this for deterministic tests.
	Now func() time.Time
}

// Envelope is BuildEnvelope's own result: the fully-built ports.Command
// ready for Dispatch, plus whether IdempotencyKey was generated rather
// than operator-supplied (the "generated key returned" verify bullet: a
// caller always knows which case it is in, and EncodeCommandResult always
// echoes the key back either way).
type Envelope struct {
	Command                 ports.Command
	IdempotencyKeyGenerated bool
}

// BuildEnvelope constructs the one ports.Command every mutating leaf
// dispatches (V1-06's "mọi mutation dùng cùng command boundary", the same
// boundary every HTTP handler already builds via newWorkspaceCommand-style
// helpers). RequestHash is computed by httpapi.SemanticHash — the exact
// same function, not a parallel CLI-only hashing scheme (V6-15B's own
// task brief: "Your framework's own idempotency-key/request-hash handling
// should call this SAME function"), so a receipt written by an HTTP call
// and a receipt written by a CLI call for equivalent requests always hash
// identically.
//
// Command.ID and Command.CorrelationID are derived deterministically as
// "<CommandType>-<IdempotencyKey>" — mirroring
// internal/delivery/httpapi/workspaceroutes.go's own newWorkspaceCommand
// and cmd/aw/definition.go's own newDefinitionCommand exactly: "stable
// and reproducible across a retry, never a fresh random value that would
// defeat log correlation across retried attempts" (that file's own doc
// comment). This is also why IDSource is only ever needed to generate a
// fresh IdempotencyKey when the caller omits one — ID/CorrelationID never
// need their own separate random draw.
func BuildEnvelope(req EnvelopeRequest) Envelope {
	idSource := req.IDSource
	if idSource == nil {
		idSource = idsource.Random{}
	}
	now := req.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	idempotencyKey := req.IdempotencyKey
	generated := false
	if idempotencyKey == "" {
		idempotencyKey = idSource.NewID()
		generated = true
	}

	hash := httpapi.SemanticHash(req.CommandType, req.Scope, req.NormalizedPayload, req.ExtraContentDigest, req.ExpectedVersion)
	id := req.CommandType + "-" + idempotencyKey

	return Envelope{
		Command: ports.Command{
			ID:              id,
			IdempotencyKey:  idempotencyKey,
			Actor:           req.Principal.Actor,
			ActorRoles:      append([]string(nil), req.Principal.Roles...),
			CorrelationID:   id,
			Scope:           req.Scope,
			ExpectedVersion: req.ExpectedVersion,
			RequestedAt:     now(),
			Type:            req.CommandType,
			RequestHash:     hash,
		},
		IdempotencyKeyGenerated: generated,
	}
}
