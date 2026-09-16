package eventschema

import (
	"context"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// EnforcingEventsRepository wraps a ports.EventsRepository and rejects
// Append for any (EventType, SchemaVersion) Registry has no Decoder for —
// V1-07A's own "từ chối emit event chưa đăng ký ngay tại boundary
// append." A caller composes this around whichever real
// ports.EventsRepository it already has (sqlite's or the fake's); the
// rejection happens before Inner.Append is ever called, so an
// unregistered event never reaches storage.
type EnforcingEventsRepository struct {
	Inner    ports.EventsRepository
	Registry *Registry
}

var _ ports.EventsRepository = EnforcingEventsRepository{}

func (r EnforcingEventsRepository) Append(ctx context.Context, event ports.DomainEvent) error {
	if !r.Registry.IsRegistered(event.EventType, event.SchemaVersion) {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			fmt.Sprintf("event type %q schema version %d is not registered", event.EventType, event.SchemaVersion),
			false, ErrNotRegistered)
	}
	return r.Inner.Append(ctx, event)
}

// ScanJournal forwards to Inner unchanged — a read path has nothing to
// enforce a registered-decoder precondition against (V6-08A's own live
// consumer, the actual reader, already fails closed via this Catalog's own
// exhaustiveness test plus a real poison record for anything it cannot
// classify at read time).
func (r EnforcingEventsRepository) ScanJournal(ctx context.Context, afterPosition uint64, limit int) ([]ports.JournalEvent, error) {
	return r.Inner.ScanJournal(ctx, afterPosition, limit)
}
