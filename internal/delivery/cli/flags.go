package cli

import (
	"flag"
	"time"
)

// BindPrincipalFlag registers the one, only allowed way a CLI invocation
// selects its own acting principal: --principal-config, pointing at the
// exact same trusted JSON config file shape/loader/default
// (config.LoadLocalPrincipalFile / config.DefaultLocalPrincipal) `aw
// serve`'s own --principal-config flag already uses (cmd/aw/serve.go).
// ADR-028's own "Actor và ActorRoles là authentication context, không
// phải input tự khai" is why this is the ONLY principal-shaped flag this
// whole framework ever defines — there is no --actor/--role flag anywhere
// in this package, and none should ever be added to any leaf built on top
// of it either (see flags_test.go's own
// TestBindPrincipalFlagNeverDefinesActorOrRoleFlag, which proves this at
// the flag.FlagSet level for every binder this file defines).
func BindPrincipalFlag(fs *flag.FlagSet) *string {
	return fs.String("principal-config", "", "path to a trusted JSON config file's localPrincipal.actor/localPrincipal.roles (ADR-028); omitted or missing means the local-operator/[operator] default — this is the only allowed way to select a principal, there is no --actor/--role flag")
}

// BindProjectFlag registers --project-id, the flag a project-scoped leaf
// uses to build its own ports.ProjectScope(...); an installation-scoped
// leaf never binds this at all.
func BindProjectFlag(fs *flag.FlagSet) *string {
	return fs.String("project-id", "", "project ID this command is scoped to (omit for an installation-scoped command)")
}

// BindExpectedVersionFlag registers --expected-version, the CLI's own
// equivalent of HTTP's strong If-Match header (RequireIfMatch,
// commandenvelope.go): an update-shaped leaf requires an operator to set
// this to the resource's own current version before it will attempt a
// CAS.
func BindExpectedVersionFlag(fs *flag.FlagSet) *uint64 {
	return fs.Uint64("expected-version", 0, "expected current version for an optimistic-concurrency update (required for update-shaped commands — the CLI equivalent of HTTP's If-Match)")
}

// BindIdempotencyKeyFlag registers --idempotency-key. Leaving it unset is
// the normal case: BuildEnvelope generates one and EncodeCommandResult
// always returns it in the JSON result, so a caller can read it back for
// a deliberate retry rather than being forced to invent one up front.
func BindIdempotencyKeyFlag(fs *flag.FlagSet) *string {
	return fs.String("idempotency-key", "", "idempotency key for this mutation (omit to have one generated and returned in the JSON result)")
}

// BindYesFlag registers --yes, the one way a noninteractive or --json
// invocation may proceed past a high-impact confirmation prompt (see
// Confirm).
func BindYesFlag(fs *flag.FlagSet) *bool {
	return fs.Bool("yes", false, "skip the interactive confirmation prompt for a high-impact command (required when noninteractive or --json)")
}

// BindJSONFlag registers --json, requested machine-readable output — the
// same flag Confirm treats as "noninteractive" for confirmation purposes,
// since a JSON consumer can never answer a TTY prompt.
func BindJSONFlag(fs *flag.FlagSet) *bool {
	return fs.Bool("json", false, "machine-readable JSON output")
}

// BindWaitFlags registers --wait and --wait-timeout: see Wait's own doc
// comment for the observe-only contract these drive.
func BindWaitFlags(fs *flag.FlagSet) (*bool, *time.Duration) {
	wait := fs.Bool("wait", false, "poll and block until the affected job/run reaches a terminal state, or --wait-timeout elapses (read-only: never executes, retries or cancels anything)")
	timeout := fs.Duration("wait-timeout", 0, "maximum time --wait blocks before giving up (0 = no timeout)")
	return wait, timeout
}

// BindFileFlag registers --file: a leaf that accepts a request body reads
// it from this path when set, or from stdin otherwise (see
// ReadBoundedInput).
func BindFileFlag(fs *flag.FlagSet) *string {
	return fs.String("file", "", "read the request body from this file instead of stdin")
}

// BindOutputFlag registers --output, the INPUT-direction counterpart
// BindFileFlag never covers: a leaf that streams real binary/raw content
// back to the operator (an Artifact's own bytes, never a JSON-shaped
// result) writes it to this path, or to the process' own stdout when the
// value is exactly "-" (see WriteBinaryOutput). Deliberately no default:
// an empty value is a usage error for any leaf that binds this — writing
// arbitrary content to a location the operator never named would be a
// silent surprise, never a convenience.
func BindOutputFlag(fs *flag.FlagSet) *string {
	return fs.String("output", "", "write raw content to this file path, or - for stdout (required)")
}
