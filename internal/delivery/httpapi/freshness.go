package httpapi

// FreshnessStatus is how stale a page of projected data might be relative
// to the authoritative journal, in V6-08/V6-08A's own vocabulary
// (docs/design/08-v6-api-projections.md V6-08's "checkpoint/freshness/poison
// records", V6-08A's "degraded/stale behavior" and "separate tx records
// poison and DEGRADED/STALE at last-good cursor"): a projection consumer
// that is caught up reports LIVE, one that has fallen behind but is still
// making progress reports DEGRADED, and one stuck at a poisoned/blocked
// cursor reports STALE. This package does not decide which status applies
// — that is V6-08A's own runtime job — it only freezes the three-value
// wire vocabulary every projection-backed endpoint reports through.
type FreshnessStatus string

const (
	FreshnessLive     FreshnessStatus = "LIVE"
	FreshnessDegraded FreshnessStatus = "DEGRADED"
	FreshnessStale    FreshnessStatus = "STALE"
)

// Freshness is the shared {Generation, AsOfJournalPosition, Status} triple
// every projection-backed read response embeds next to its data (V6-02A's
// own "Phạm vi: ... Freshness ..." line) — V6-08's own projection row key
// is "(ProjectID, ProjectionName, Generation, EntityKey)" and V6-08A's own
// consumer "verifies active generation/fence/cursor" before applying rows,
// so Generation here is that same active-generation number the rows were
// read under, and AsOfJournalPosition is the greatest global JournalPosition
// (V6-08's own "Cursor is greatest scanned global JournalPosition") already
// reflected in those rows. A client compares AsOfJournalPosition across
// requests to notice progress (or its absence) without needing to
// understand the projection's own internal checkpoint mechanics, and a
// cursor for the same query fingerprint that names a different Generation
// than what this endpoint now reports is exactly the "generation swap"
// case Bind/ResyncError (cursor.go) exists to catch.
type Freshness struct {
	Generation          int             `json:"generation"`
	AsOfJournalPosition int64           `json:"asOfJournalPosition"`
	Status              FreshnessStatus `json:"status"`
}
