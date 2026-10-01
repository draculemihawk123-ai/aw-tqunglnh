package parity

// Ledger is the reviewed, pinned list of parity debt the checker is allowed to
// find in the tree. It is EMPTY: V8-12R-01 closed the last of the fifteen
// entries V6-15O originally pinned (fourteen HTTP operations with no `aw`
// mirror, two of them also with no application operation) by adding the CLI
// leaves and the one application package (internal/app/kanban) that satisfy
// them, and deleting each entry as its finding disappeared.
//
// It still works like V6-12's knownUnimplementedGaps, in both directions, so
// the next piece of debt cannot hide:
//   - a NEW finding (anything Check reports that is not listed here) fails
//     the gate, so debt can never grow silently;
//   - a listed entry no finding matches any more (Report.Stale) also fails,
//     so closing a debt forces its entry to be deleted and the ledger can
//     only shrink.
//
// `Report.Debt` — the number V6-15P's terminal gate and the V8 Alpha gate
// require to be ZERO — is len(Ledger()), which is why returning nil here is
// the statement "no parity debt", not an omission. An entry added back must
// name the task that owns closing it; docs/release/alpha-verdict.json's
// parity blocker is tied to this function's length by
// TestAlphaVerdictRecordMatchesTheParityLedger, so re-opening debt also forces
// the recorded verdict to stop claiming a clean ledger.
func Ledger() []LedgerEntry {
	return nil
}
