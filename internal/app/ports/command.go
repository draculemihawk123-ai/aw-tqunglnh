package ports

import (
	"errors"
	"time"
)

// CommandScope is the boundary a command's idempotency and aggregate load
// key on (docs/design/03-v1-alpha-foundation.md V1-06, ADR-025): every
// mutation declares exactly one scope, either installation-wide or one
// project.
type CommandScope struct {
	kind      commandScopeKind
	projectID string
}

type commandScopeKind int

const (
	commandScopeInstallation commandScopeKind = iota
	commandScopeProject
)

// InstallationScope is the one CommandScope value for a command that
// applies installation-wide, not to a specific project.
func InstallationScope() CommandScope {
	return CommandScope{kind: commandScopeInstallation}
}

// ProjectScope is the CommandScope for a command scoped to one project.
// An empty projectID panics — a project scope with no project id is a
// construction bug at the call site, not a runtime condition a caller
// should have to handle.
func ProjectScope(projectID string) CommandScope {
	if projectID == "" {
		panic("ports: ProjectScope requires a non-empty projectID")
	}
	return CommandScope{kind: commandScopeProject, projectID: projectID}
}

// IsInstallation reports whether this is the installation scope.
func (s CommandScope) IsInstallation() bool { return s.kind == commandScopeInstallation }

// ProjectID returns the scoped project ID and true, or ("", false) for
// the installation scope.
func (s CommandScope) ProjectID() (string, bool) {
	if s.kind != commandScopeProject {
		return "", false
	}
	return s.projectID, true
}

// Key returns the scope_key command_receipts stores (GC-INV-35): one
// non-null column encoding either scope, so the receipt's unique tuple
// never relies on a nullable column — SQLite treats every NULL in a
// unique index as distinct from every other NULL, which would let
// duplicate installation-scoped commands slip through if a nullable
// project_id stood in for "no project" instead.
func (s CommandScope) Key() string {
	if s.kind == commandScopeInstallation {
		return "installation"
	}
	return "project:" + s.projectID
}

// Command is the one envelope every mutation goes through
// (docs/design/03-v1-alpha-foundation.md V1-06's own Mục tiêu: "mọi
// mutation dùng cùng command boundary").
type Command struct {
	ID              string
	IdempotencyKey  string
	Actor           string
	CorrelationID   string
	Scope           CommandScope
	ExpectedVersion uint64
	RequestedAt     time.Time
	Type            string
	RequestHash     string
}

// ErrReceiptConflict is returned by CommandReceipts.Record when a receipt
// already exists for the same (actor, scope, idempotency key, command
// type) but with a different RequestHash: the caller sent the same
// idempotency key for a genuinely different payload, which is a conflict,
// never a silent overwrite or a "duplicate" replay.
var ErrReceiptConflict = errors.New("ports: command receipt exists with a different request hash")

// Receipt is one durable idempotency record: a command handler writes
// exactly one of these, atomically with whatever state/event change the
// command caused, the same UnitOfWork call that request_hash's
// uniqueness protects the exactly-once contract for.
type Receipt struct {
	Actor          string
	Scope          CommandScope
	IdempotencyKey string
	CommandType    string
	RequestHash    string
	ResultJSON     string
	ErrorCode      string
	CreatedAt      time.Time
}
