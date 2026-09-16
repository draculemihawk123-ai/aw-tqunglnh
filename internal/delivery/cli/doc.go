// Package cli is the shared CLI framework every `aw <resource> <action>`
// leaf task (V6-15C onward) builds on top of (ADR-028, V6-15B,
// docs/design/08-v6-api-projections.md line 653: "freeze grammar/envelope/
// output/wait/confirmation and leaf descriptor registration"): grammar,
// command envelope construction, output encoding, `--wait` polling and
// confirmation prompting are frozen here, once, so no individual leaf
// reinvents any of them or drifts from another leaf's own version.
//
// It deliberately contains no domain leaf of its own (no `definition`/
// `doctor`/`work-item`/... subcommand), and never imports
// internal/adapters/sqlite, any Git-touching adapter package, any provider
// adapter package, or an internal worker package (V6-15B's own "Không
// làm" line; enforced for real by
// internal/archtest.TestDeliveryCLINeverImportsSQLiteGitProviderOrWorker) —
// the same "shared framework, not a leaf" split HTTP delivery already has
// between commandenvelope.go/receiptreplay.go (V6-02's own shared
// primitives) and each individual endpoint package (V6-03A onward, one
// leaf at a time).
//
// A leaf package (once one exists) registers itself into this framework's
// own Registry (Register/MustRegister, descriptor.go) from its own
// init(), builds a ports.Command via BuildEnvelope (envelope.go),
// dispatches it through Dispatch (dispatch.go) — which reuses
// internal/delivery/httpapi's own SemanticHash/LookupReceipt/
// ReconcileReceipt directly, so a receipt written by an HTTP call and a
// receipt written by a CLI call are checked against the exact same replay
// authority, never a second parallel hashing/replay scheme — and writes
// its own result with EncodeCommandResult/EncodeQueryResult (output.go).
// Wait/Confirm/ReadBoundedInput (wait.go, confirm.go, input.go) are the
// remaining shared primitives V6-15B's own "Thực hiện" line names.
// ADR-028's own "Actor và ActorRoles là authentication context, không
// phải input tự khai" is why nothing in this package ever exposes a way
// to set them from a per-invocation flag — see BindPrincipalFlag's own
// doc comment, and flags_test.go's own
// TestBindPrincipalFlagNeverDefinesActorOrRoleFlag.
package cli
