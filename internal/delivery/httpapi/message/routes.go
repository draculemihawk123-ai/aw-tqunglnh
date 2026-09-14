// Package message is V6-07's own HTTP slice
// (docs/design/08-v6-api-projections.md V6-07: "append/list canonical text
// messages và bounded context-used metadata"). It owns a dedicated
// subpackage under internal/delivery/httpapi — the same "mỗi
// endpoint/CLI task sở hữu subpackage, descriptor/schema fragment và test
// riêng" discipline internal/delivery/httpapi/workitem (V6-04) already
// establishes — rather than adding flat files directly into
// internal/delivery/httpapi.
//
// Every handler in this package follows the identical flow V6-02's own
// commandenvelope.go/receiptreplay.go doc comments prescribe, and
// internal/delivery/httpapi/workitem already applies for its own routes:
// reload the route's own authoritative target and derive/confirm its real
// project scope (Contract chung §3: "không tin ID shape, payload hoặc
// projection") → for the one mutating route, build the command envelope
// (Idempotency-Key required; no route here ever requires If-Match — a
// Message is immutable/append-only, never updated) → receipt lookup →
// replay/conflict → dispatch the real, already-existing application command
// (internal/app/message.AppendMessage) or query
// (internal/app/message.ListMessages, internal/app/work.GetWorkItem,
// internal/app/ports.ContextSnapshotRepository's own two reads) → encode
// the result. This package never writes or records a command receipt
// itself, and never opens a WithSerializedWrite transaction of its own —
// the same TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt architecture
// test that already walks internal/delivery/httpapi recursively covers
// this subpackage too.
//
// Route inventory (all three project-scoped, WorkItem-scoped):
//
//	POST /projects/{projectId}/work-items/{workItemId}/messages                                    appendMessage
//	GET  /projects/{projectId}/work-items/{workItemId}/messages                                     listMessages
//	GET  /projects/{projectId}/work-items/{workItemId}/messages/{messageId}/context-snapshot         getMessageContextSnapshot
//
// This task's own "Không làm" line (KHÔNG binary upload; KHÔNG approval
// inference from message content; KHÔNG raw provider transcript) is
// satisfied by construction: appendMessageBody (append.go) carries only a
// plain JSON string Content field, never a base64/multipart upload field;
// no handler in this package ever reads a Message's own Content bytes at
// all (ContentArtifactID is returned/listed as a bounded reference only —
// see dto.go's own messageRefDTO doc comment) let alone interprets them as
// a control signal against any Run/WorkItem state; and listMessages/
// getMessageContextSnapshot only ever return this platform's own canonical
// Message/ContextSnapshot rows, never a provider's own session transcript.
package message

import (
	"net/http"

	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// RegisterRoutes registers every route this package owns onto reg — a
// composition root (cmd/aw/serve.go) calls this once, alongside every
// sibling endpoint task's own RegisterRoutes, to compose the final root
// router; this package itself never touches a shared registry beyond this
// one call.
func RegisterRoutes(reg *httpapi.RouteRegistry, deps Dependencies) {
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{projectId}/work-items/{workItemId}/messages", OperationID: "appendMessage",
		ScopeKind: httpapi.ScopeProject, RequestSchema: appendMessageBody{}, ResponseSchema: appmessage.AppendMessageResult{},
		Handler: handleAppendMessage(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/work-items/{workItemId}/messages", OperationID: "listMessages",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: listMessagesResponse{},
		Handler: handleListMessages(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/work-items/{workItemId}/messages/{messageId}/context-snapshot", OperationID: "getMessageContextSnapshot",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: contextSnapshotDetail{},
		Handler: handleGetMessageContextSnapshot(deps),
	})
}
