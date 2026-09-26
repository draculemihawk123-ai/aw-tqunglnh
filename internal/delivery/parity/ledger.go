package parity

// Ledger is the reviewed, pinned list of parity debt V6-15O found in the
// already-merged tree and is NOT allowed to close: the task's own "Không làm:
// no new leaf/route" forbids adding the CLI leaves and the one HTTP route that
// would satisfy these rows, and the design's checker rules forbid papering
// over them (a CLI_LOCAL leaf outside the closed set, or an HTTP operation
// with no `aw` mirror, is exactly what the gate exists to reject).
//
// It works like V6-12's knownUnimplementedGaps, in both directions:
//   - a NEW finding (anything Check reports that is not listed here) fails
//     the gate, so debt can never grow silently;
//   - a listed entry no finding matches any more (Report.Stale) also fails,
//     so closing a debt forces its entry to be deleted and the ledger can
//     only shrink.
//
// `Report.Debt` — the number V6-15P's terminal gate requires to be ZERO — is
// len(Ledger()) today. V6-15P must not pass until this function returns nil;
// each entry names the task that owns closing it.
func Ledger() []LedgerEntry {
	const (
		noLeaf  = "V6-15O forbids adding a CLI leaf (design §V6-15O \"Không làm: no new leaf/route\"); the owning leaf task must add the command"
		noRoute = "V6-15O forbids adding an HTTP route (design §V6-15O \"Không làm: no new leaf/route\"); the owning endpoint task must add the route"
	)
	return []LedgerEntry{
		// ---- HTTP operations with no `aw` mirror ---------------------------
		{ClassMissingCLI, "http:getEvidence", "V6-15K", "UX Screen 11 row 2 reserves `aw evidence show`; V6-15K chose not to add it; " + noLeaf},
		{ClassMissingCLI, "http:listArtifacts", "V6-15K", "UX Screen 11 row 3 reserves `aw artifact list`; " + noLeaf},
		{ClassMissingCLI, "http:getMessageContextSnapshot", "V6-15J", "route GET .../messages/{messageId}/context-snapshot has no `aw` command (UX Screen 12 row 4 bundles it with `aw message list`); " + noLeaf},
		{ClassMissingCLI, "http:getReleaseSetLocalCommitStatus", "V6-15M", "UX Screen 10 row 7 reserves `aw release-set local-commit status`; `--wait` observes it internally but no command exposes it; " + noLeaf},
		{ClassMissingCLI, "http:getRepositoryWorkspaceState", "V6-15L", "UX Screen 9 row 1 names repository-workspace state next to workspace-set state; V6-15L wrapped only `workspace-set show`; " + noLeaf},
		{ClassMissingCLI, "http:getScopeExpansionRequest", "V6-15I", "route GET .../scope-expansions/{requestId} has no `aw scope-expansion show`; " + noLeaf},
		{ClassMissingCLI, "http:getTaskFamily", "V6-15G", "route GET .../task-families/{familyId} has no `aw` command; " + noLeaf},
		{ClassMissingCLI, "http:listChildWorkItems", "V6-15G", "UX Screen 7 row 2 says `aw work-item show` (embed) but `work-item show` does not embed children; " + noLeaf},
		{ClassMissingCLI, "http:listWorkItemKanban", "V6-10 + V6-15G", "UX Screen 5 row 1 (`aw work-item list`) is the projected board query; the CLI's `work-item list` mirrors the authoritative listWorkItems instead; " + noLeaf},
		{ClassMissingCLI, "http:getWorkItemProjectedDetail", "V6-10 + V6-15G", "the projected WorkItem detail route has no `aw` command; " + noLeaf},
		{ClassMissingCLI, "http:repositoriesGet", "V6-15D", "UX Screen 2 row 6 reserves `aw repository show`; V6-15D shipped only `repository onboarding`; " + noLeaf},

		// ---- HTTP operations with no application operation ----------------
		{ClassMissingApp, "http:listWorkItemKanban", "V6-10", "the route reads tx.Projections() directly in the delivery layer; ADR-028 wants every public query on a public APPLICATION operation"},
		{ClassMissingApp, "http:getWorkItemProjectedDetail", "V6-10", "the route reads tx.Projections() directly in the delivery layer; ADR-028 wants every public query on a public APPLICATION operation"},
	}
}
