// Package message is V6-15J's own CLI leaf
// (docs/design/08-v6-api-projections.md V6-15J: "append/inspect canonical
// conversation and upload verified attachments from the terminal") — the
// terminal-side mirror of internal/delivery/httpapi/message (V6-07, V6-07A),
// built directly on top of internal/app/message's own AppendMessage/
// AppendConversationAttachment/ListMessages (never a second, independently
// invented conversation concept) and internal/delivery/cli's own shared
// framework (V6-15B: init()-time cli.MustRegister, cli.BuildEnvelope/
// cli.Dispatch/cli.EncodeCommandResult/cli.EncodeQueryResult,
// cli.ReadBoundedInput for a bounded --file/stdin body). Named "message"
// (singular), matching internal/delivery/httpapi/message's own package name
// exactly — this package imports internal/domain/message as messagedomain
// and internal/app/message as appmessage throughout, exactly like that HTTP
// package already does, so neither import ever collides with this
// package's own name.
//
// Every route this package mirrors is PROJECT-scoped only
// (internal/delivery/httpapi/message/routes.go's own RouteDescriptor.ScopeKind
// is httpapi.ScopeProject for all three routes it registers, never
// installation) — unlike internal/delivery/cli/definitions' own dual-scoped
// commands, every command here requires --project-id, and reloads the
// route's own authoritative WorkItem via internal/app/work.GetWorkItem
// FIRST, unconditionally, to confirm it actually belongs to that project
// before ever dispatching — the identical "không tin ID shape, payload hoặc
// projection" discipline internal/delivery/httpapi/message/routes.go's own
// package doc comment states and every one of its own handlers already
// enforces (append.go's own handleAppendMessage, list.go's own
// handleListMessages, attachment.go's own handleAppendConversationAttachment
// all reload workapp.GetWorkItem before doing anything else) — reloadWorkItem
// (dependencies.go) is this package's own single, shared implementation of
// that same discipline, folding both "no such WorkItem" and "it exists, but
// in a different project" into one leakage-normalized ErrWorkItemNotFound,
// mirroring httpapi's own WriteResourceHidden.
//
// This package never touches cmd/aw/main.go or cmd/aw/cli.go: routing real
// os.Args to this leaf's own Run* functions is V6-15O's own future
// composition-root job (docs/design/08-v6-api-projections.md §1 rule 8:
// "chỉ V6-15O compose CLI/parity registry"), not this task's — this
// package's own tests call those Run* functions directly instead, exactly
// like every sibling CLI leaf (clicatalog, clidefinitions, clisettings)
// already does.
//
// # Command surface
//
//   - `aw message list <workItemId> --project-id <id>` — a plain read over
//     appmessage.ListMessages, no idempotency key or CommandEnvelope at all
//     (mirrors GET .../messages).
//   - `aw message append <workItemId> --project-id <id> [--attempt-id <id>]
//     [--role <role>] [--content-type <type>] [--sensitivity <level>]
//     [--file <path>]` — the full CommandEnvelope mutation flow over
//     appmessage.AppendMessage, content bounded at appmessage.MaxContentSize
//     (mirrors POST .../messages).
//   - `aw message upload-attachment <workItemId> --project-id <id> --role
//     <role> [--attempt-id <id>] [--content-type <type>]
//     [--sensitivity <level>] [--sha256 <digest>] [--file <path>]` — the
//     same CommandEnvelope flow over appmessage.AppendConversationAttachment,
//     content bounded at appmessage.MaxAttachmentSize (mirrors POST
//     .../attachments, which travels as raw headers-plus-body over HTTP;
//     this leaf's own mirror is flags-plus-bounded-body, per this task's own
//     brief).
//
// # Digest handling
//
// upload-attachment computes the SHA-256 hex digest of the bounded-read
// content itself (crypto/sha256) and passes it as
// AppendConversationAttachmentRequest.DeclaredSHA256 by default — an
// operator never has to pre-compute a digest by hand for the common case.
// An explicit --sha256 flag OVERRIDES that local computation: this is the
// one deliberate way an operator can declare a digest computed elsewhere
// (e.g. on a different machine before piping a file over) or, just as
// usefully, deliberately exercise appmessage.ErrAttachmentDigestMismatch's
// own tamper-detection path against a digest that does NOT match the actual
// bytes — appmessage.AppendConversationAttachment itself only ever VERIFIES
// a declared digest, it never computes one, so this leaf must supply one
// either way.
//
// # No locator output
//
// This task's own "Không làm" line ("không xuất raw internal artifact
// storage locators") is satisfied by construction: every view type this
// package defines (views.go) carries only appmessage.AppendMessageResult's
// own ContentArtifactID (a bounded, platform-minted reference — the exact
// same field HTTP already returns) or messagedomain.Message's own
// ContentArtifactID for `list` — never anything read from
// ports.ArtifactStore or ports.ArtifactRef.Locator, which this package never
// touches directly at all.
package message
