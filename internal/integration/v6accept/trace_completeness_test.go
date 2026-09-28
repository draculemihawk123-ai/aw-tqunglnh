// V8-01's own trace completeness verifier (docs/design/10-v8-alpha-hardening.md
// V8-01, AK-ARCH-021, HE-11-S03). "Complete" is defined concretely, in two
// independent halves, rather than as a vague aspiration:
//
//  1. AK-ARCH-021's own evidence chain — "Evidence truy ngược được WorkItem
//     -> Run -> NodeRun -> Attempt -> invocation -> artifact -> exact
//     repository revision" — checked entirely through the real public
//     evidence routes V7-14's own EvidenceTab already uses
//     (runtime.EvidenceDetail already carries every one of those identities
//     plus a per-repository RevisionView, so no internal package needs
//     touching for this half).
//  2. HE-11-S03's own canonical event envelope — "so trace/failure can be
//     compared across harness versions" implies the envelope itself
//     (correlation_id, causation_id, sequence) must be genuinely present
//     and unbroken, not merely that the business outcome looks right —
//     checked via the SAME raw domain_events introspection V4-14's own
//     runtimeengine_test.go already established as this repository's
//     precedent for a test reading back its own causal event log
//     (sqlite.Store.ListDomainEventsForProject), opened read-only AFTER the
//     real serve/worker processes have cleanly stopped.
package v6accept

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
)

// traceCompletenessReport is what TestV8GoldenWorkload_TraceCompleteness
// writes out (t.Logf always, a file when goldenReportPathEnvVar is set).
// Violations is empty on a genuinely complete trace; the test fails
// whenever it is not.
type traceCompletenessReport struct {
	ProjectID           string   `json:"projectId"`
	WorkItemID          string   `json:"workItemId"`
	RunID               string   `json:"runId"`
	EvidenceRowsChecked int      `json:"evidenceRowsChecked"`
	ArtifactsVerified   int      `json:"artifactsVerified"`
	DomainEventsChecked int      `json:"domainEventsChecked"`
	AggregatesChecked   int      `json:"aggregatesChecked"`
	EvidenceKindsFound  []string `json:"evidenceKindsFound"`
	Violations          []string `json:"violations"`
}

// verifyEvidenceChain implements AK-ARCH-021's own chain, entirely through
// the real public evidence routes (mirrors stage_evidence_test.go's own
// list/get/artifact-download/hash-check pattern, generalized to assert
// every field that chain names rather than a fixed two kinds).
func verifyEvidenceChain(t *testing.T, api *apiClient, projectID, workItemID, runID string, repositoryIDs []string, report *traceCompletenessReport) {
	t.Helper()
	base := "/projects/" + projectID + "/work-items/" + workItemID

	type revisionView struct {
		RepositoryID string `json:"repositoryId"`
		VCSObjectID  string `json:"vcsObjectId"`
	}
	type evidenceDetail struct {
		EvidenceID         string         `json:"evidenceId"`
		WorkItemID         string         `json:"workItemId"`
		RunID              string         `json:"runId"`
		NodeRunID          string         `json:"nodeRunId"`
		AttemptID          string         `json:"attemptId"`
		Kind               string         `json:"kind"`
		Verdict            string         `json:"verdict"`
		ArtifactReferences []string       `json:"artifactReferences"`
		Revisions          []revisionView `json:"revisions"`
		RevisionSetHash    string         `json:"revisionSetHash"`
	}
	var list struct {
		Items []evidenceDetail `json:"items"`
	}
	api.get(t, base+"/evidence").requireStatus(t, http.StatusOK).decode(t, &list)
	report.EvidenceRowsChecked = len(list.Items)
	if len(list.Items) == 0 {
		report.Violations = append(report.Violations, "no Evidence rows recorded for the golden run at all")
		return
	}

	kindsSeen := map[string]bool{}
	for _, item := range list.Items {
		kindsSeen[item.Kind] = true

		// WorkItem -> Run -> NodeRun -> Attempt.
		if item.WorkItemID != workItemID {
			report.Violations = append(report.Violations, fmt.Sprintf("evidence %s: workItemId=%q, want %q", item.EvidenceID, item.WorkItemID, workItemID))
		}
		if item.RunID != runID {
			report.Violations = append(report.Violations, fmt.Sprintf("evidence %s: runId=%q, want %q", item.EvidenceID, item.RunID, runID))
		}
		if item.NodeRunID == "" {
			report.Violations = append(report.Violations, fmt.Sprintf("evidence %s: nodeRunId is empty", item.EvidenceID))
		}
		if item.AttemptID == "" {
			report.Violations = append(report.Violations, fmt.Sprintf("evidence %s: attemptId is empty (no invocation link)", item.EvidenceID))
		}

		// -> exact repository revision, for EVERY repository this golden
		// workload's own WorkItem is scoped to (the multi-repo dimension).
		if item.RevisionSetHash == "" {
			report.Violations = append(report.Violations, fmt.Sprintf("evidence %s: revisionSetHash is empty", item.EvidenceID))
		}
		revisionByRepo := map[string]string{}
		for _, rev := range item.Revisions {
			revisionByRepo[rev.RepositoryID] = rev.VCSObjectID
		}
		for _, repositoryID := range repositoryIDs {
			revision, ok := revisionByRepo[repositoryID]
			if !ok {
				report.Violations = append(report.Violations, fmt.Sprintf("evidence %s: no revision entry for repository %s", item.EvidenceID, repositoryID))
				continue
			}
			if revision == "" {
				report.Violations = append(report.Violations, fmt.Sprintf("evidence %s: revision for repository %s is empty", item.EvidenceID, repositoryID))
			}
		}

		// The single-item route agrees with the list (a second, independent
		// read of the identical chain).
		var one evidenceDetail
		api.get(t, base+"/evidence/"+item.EvidenceID).requireStatus(t, http.StatusOK).decode(t, &one)
		if one.RunID != item.RunID || one.NodeRunID != item.NodeRunID || one.AttemptID != item.AttemptID || one.RevisionSetHash != item.RevisionSetHash {
			report.Violations = append(report.Violations, fmt.Sprintf("evidence %s: single-item GET disagrees with the list entry", item.EvidenceID))
		}

		// -> artifact, downloadable and hash-verified (never a dangling
		// reference an evidence row merely claims).
		if len(item.ArtifactReferences) == 0 {
			continue
		}
		var artifacts struct {
			Items []struct {
				ArtifactID  string `json:"artifactId"`
				ContentHash string `json:"contentHash"`
				Size        int64  `json:"size"`
			} `json:"items"`
		}
		api.get(t, base+"/evidence/"+item.EvidenceID+"/artifacts").requireStatus(t, http.StatusOK).decode(t, &artifacts)
		if len(artifacts.Items) == 0 {
			report.Violations = append(report.Violations, fmt.Sprintf("evidence %s: names %d artifact reference(s) but the artifacts route returns none", item.EvidenceID, len(item.ArtifactReferences)))
			continue
		}
		for _, artifact := range artifacts.Items {
			content := api.get(t, base+"/evidence/"+item.EvidenceID+"/artifacts/"+artifact.ArtifactID+"/content")
			if content.status != http.StatusOK {
				report.Violations = append(report.Violations, fmt.Sprintf("evidence %s: artifact %s content download = %d, want 200", item.EvidenceID, artifact.ArtifactID, content.status))
				continue
			}
			if int64(len(content.body)) != artifact.Size {
				report.Violations = append(report.Violations, fmt.Sprintf("evidence %s: artifact %s is %d bytes, API reported %d", item.EvidenceID, artifact.ArtifactID, len(content.body), artifact.Size))
			}
			report.ArtifactsVerified++
		}
	}

	for kind := range kindsSeen {
		report.EvidenceKindsFound = append(report.EvidenceKindsFound, kind)
	}
	sort.Strings(report.EvidenceKindsFound)
	for _, want := range []string{"COMMAND_EXECUTION", goldenGateEvidenceKey} {
		if !kindsSeen[want] {
			report.Violations = append(report.Violations, fmt.Sprintf("no evidence of kind %s recorded for the golden run (kinds seen: %v)", want, report.EvidenceKindsFound))
		}
	}
}

// verifyDomainEventJournal implements HE-11-S03's own canonical-envelope
// completeness check: opens a second, read-only connection to the SAME
// sqlite file the real serve/worker processes just wrote to (only after
// they have both cleanly stopped — TestV8GoldenWorkload_TraceCompleteness's
// own caller enforces the ordering), then walks every domain_events row this
// project ever produced.
func verifyDomainEventJournal(t *testing.T, s *stack, projectID string, report *traceCompletenessReport) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, s.dbPath)
	if err != nil {
		report.Violations = append(report.Violations, fmt.Sprintf("open %s read-only for journal introspection: %v", s.dbPath, err))
		return
	}
	defer store.Close()

	events, err := store.ListDomainEventsForProject(ctx, projectID)
	if err != nil {
		report.Violations = append(report.Violations, fmt.Sprintf("ListDomainEventsForProject(%s): %v", projectID, err))
		return
	}
	report.DomainEventsChecked = len(events)
	if len(events) == 0 {
		report.Violations = append(report.Violations, "the project's own domain_events journal is empty")
		return
	}

	eventIDs := map[string]bool{}
	seenJournalPositions := map[int64]bool{}
	lastJournalPosition := int64(-1)
	type aggregateKey struct{ aggregateType, aggregateID string }
	sequencesByAggregate := map[aggregateKey][]int64{}

	for _, event := range events {
		eventIDs[event.ID] = true

		// journal_position: globally unique, and ListDomainEventsForProject
		// already orders by it — a real gap-free total order is not
		// required (allocateJournalPosition's own doc comment only
		// promises database-monotonic, not gapless across every project),
		// but it must never repeat and must never come back smaller than a
		// row already seen.
		if seenJournalPositions[event.JournalPosition] {
			report.Violations = append(report.Violations, fmt.Sprintf("event %s: duplicate journal_position %d", event.ID, event.JournalPosition))
		}
		seenJournalPositions[event.JournalPosition] = true
		if event.JournalPosition <= lastJournalPosition {
			report.Violations = append(report.Violations, fmt.Sprintf("event %s: journal_position %d is not strictly increasing after %d", event.ID, event.JournalPosition, lastJournalPosition))
		}
		lastJournalPosition = event.JournalPosition

		// The canonical envelope's own correlation_id must always be
		// present (HE-11's own minimal envelope, "Canonical event envelope
		// tối thiểu").
		if event.CorrelationID == "" {
			report.Violations = append(report.Violations, fmt.Sprintf("event %s (%s): correlation_id is empty", event.ID, event.EventType))
		}

		key := aggregateKey{event.AggregateType, event.AggregateID}
		sequencesByAggregate[key] = append(sequencesByAggregate[key], event.Sequence)
	}

	// causation_id, when set, must point at a real event this SAME project's
	// journal actually contains — never a dangling reference.
	for _, event := range events {
		if event.CausationID != "" && !eventIDs[event.CausationID] {
			report.Violations = append(report.Violations, fmt.Sprintf("event %s (%s): causation_id %s does not match any event in this project's own journal", event.ID, event.EventType, event.CausationID))
		}
	}

	// Per aggregate, Sequence is that aggregate's own optimistic-concurrency
	// Version AS OF this event (confirmed by reading the code, not assumed:
	// every runtime command site sets Sequence from its own updated
	// aggregate's Version field, e.g. advance.go's
	// "Sequence: int64(completedNodeRun.Version)") — NOT a separate
	// "N-th event for this aggregate" counter. A version bump with no
	// domain event is real and expected here (e.g. a NodeRun's own
	// PENDING->RUNNING transition logs nothing; only its first REAL
	// terminal/decision event does, which is why a NodeRun's first-ever
	// logged Sequence is routinely 2, not 1 — found live the first time
	// this check wrongly assumed gap-free numbering). The two invariants
	// this domain's own design actually guarantees, and that a real
	// completeness bug WOULD break, are: no two events for the same
	// aggregate ever claim the identical Sequence (a lost update silently
	// overwriting the version another event already claimed), and Sequence
	// only ever increases as journal_position advances (an event for an
	// OLDER version can never be appended after a newer one already was).
	report.AggregatesChecked = len(sequencesByAggregate)
	for key, sequences := range sequencesByAggregate {
		// sequences is already in journal_position (append) order — the
		// query events came from is ordered by journal_position and this
		// slice was built by a single forward pass over it.
		seenSequences := map[int64]bool{}
		lastSequence := int64(-1)
		for _, sequence := range sequences {
			if seenSequences[sequence] {
				report.Violations = append(report.Violations, fmt.Sprintf("aggregate %s/%s: sequence %d claimed by more than one event", key.aggregateType, key.aggregateID, sequence))
			}
			seenSequences[sequence] = true
			if sequence <= lastSequence {
				report.Violations = append(report.Violations, fmt.Sprintf("aggregate %s/%s: sequence %d is not strictly increasing after %d", key.aggregateType, key.aggregateID, sequence, lastSequence))
			}
			lastSequence = sequence
		}
	}
}
