package message

import (
	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// commandTypeAppendMessage is byte-for-byte identical to the commandType
// string literal internal/delivery/httpapi/message/append.go's own
// handleAppendMessage already passes to prepareCreateCommand ("AppendMessage")
// — a hard requirement (see internal/delivery/cli/catalog/helpers.go's own
// commandType* doc comment, and internal/delivery/cli/definitions/descriptor.go's
// own identical precedent): cli.Dispatch/cli.BuildEnvelope reuse the exact
// same httpapi.SemanticHash/LookupReceipt/ReconcileReceipt replay authority
// internal/delivery/httpapi/message itself uses, keyed in part on this
// command_type string, so an HTTP call and a CLI call for "the same"
// AppendMessage command must hash and replay identically. There is no
// exported constant for this in internal/app/message (unlike
// AppendConversationAttachment's own exported appmessage.AttachmentCommandType
// below) — internal/app/message.AppendMessage never validates cmd.Type
// itself, so this literal is this package's own considered copy of the one
// every other caller in this codebase already uses.
const commandTypeAppendMessage = "AppendMessage"

// appOpListMessages is this package's own AppOperation label for `aw
// message list` — a plain read, never itself an application "command" in
// the receipt-replay sense, but still named for cli.Descriptor's own
// four-column parity inventory (ADR-028, V6-15O).
const appOpListMessages = "ListMessages"

// This block registers V6-15J's own three Descriptors into cli.Default from
// this package's own init() — the "own package, own init()" discipline
// every sibling CLI leaf (settings, definitions, catalog) already follows.
// Every command here is PROJECT-scoped only (see doc.go's own top comment
// for why) — unlike internal/delivery/cli/definitions' own dual-scoped
// commands, there is exactly one Descriptor per Path, never two.
func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"message", "list"}, Scope: cli.ScopeProject,
		AppOperation: appOpListMessages, HTTPOperationID: "listMessages",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"message", "append"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeAppendMessage, HTTPOperationID: "appendMessage",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"message", "upload-attachment"}, Scope: cli.ScopeProject,
		AppOperation: appmessage.AttachmentCommandType, HTTPOperationID: "appendConversationAttachment",
	})
}
