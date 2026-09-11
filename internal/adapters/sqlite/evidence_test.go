package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func mustEvidence(t *testing.T, id, attemptID, kind, verdict string, artifactReferences []string, createdAt time.Time) runtime.Evidence {
	t.Helper()
	revisions, err := workspace.NewRevisionSet([]workspace.Revision{
		{RepositoryID: "repo-user", VCSObjectID: "user-base", WorkspaceGeneration: 1},
	})
	if err != nil {
		t.Fatalf("NewRevisionSet: %v", err)
	}
	evidence, err := runtime.NewEvidence(
		runtime.EvidenceID(id), "project-1", "work-root", "run-1", "node-run-1", runtime.ExecutionAttemptID(attemptID),
		kind, verdict, artifactReferences, revisions, "policy-version-1", createdAt,
	)
	if err != nil {
		t.Fatalf("NewEvidence: %v", err)
	}
	return evidence
}

func TestEvidenceRepository_CreateAndGet_RoundTrips(t *testing.T) {
	store := openCatalogTestStore(t, "evidence-create-get.db")
	seedSchedulingFixture(t, store)
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	evidence := mustEvidence(t, "evidence-1", "attempt-1", "lint-clean", "PASS", []string{"artifact-1", "artifact-2"}, createdAt)

	var stored runtime.Evidence
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		stored, err = (runtimeRepository{tx: tx}).CreateEvidence(context.Background(), evidence)
		return err
	})
	if stored.Kind != "lint-clean" || stored.Verdict != "PASS" {
		t.Fatalf("stored = %+v, want Kind=lint-clean Verdict=PASS", stored)
	}

	var loaded runtime.Evidence
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		loaded, err = (runtimeRepository{tx: tx}).GetEvidence(context.Background(), "evidence-1")
		return err
	})
	if len(loaded.ArtifactReferences) != 2 || loaded.ArtifactReferences[0] != "artifact-1" || loaded.ArtifactReferences[1] != "artifact-2" {
		t.Fatalf("loaded.ArtifactReferences = %v, want [artifact-1 artifact-2]", loaded.ArtifactReferences)
	}
	if loaded.PolicyVersion != "policy-version-1" || !loaded.CreatedAt.Equal(createdAt) {
		t.Fatalf("loaded = %+v, want PolicyVersion=policy-version-1 CreatedAt=%v", loaded, createdAt)
	}
	if entries := loaded.Revisions.Entries(); len(entries) != 1 || entries[0].RepositoryID != "repo-user" {
		t.Fatalf("loaded.Revisions = %+v, want one entry for repo-user", entries)
	}
}

func TestEvidenceRepository_CreateEvidence_IsIdempotentByID(t *testing.T) {
	store := openCatalogTestStore(t, "evidence-idempotent.db")
	seedSchedulingFixture(t, store)
	evidence := mustEvidence(t, "evidence-1", "attempt-1", "lint-clean", "PASS", []string{"artifact-1"}, time.Now())

	var first, second runtime.Evidence
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		first, err = (runtimeRepository{tx: tx}).CreateEvidence(context.Background(), evidence)
		return err
	})
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		second, err = (runtimeRepository{tx: tx}).CreateEvidence(context.Background(), evidence)
		return err
	})
	if first.ID != second.ID || !first.CreatedAt.Equal(second.CreatedAt) {
		t.Fatalf("duplicate create returned a different row: first=%+v second=%+v", first, second)
	}

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM evidence WHERE id = 'evidence-1'`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Fatalf("evidence rows with id=evidence-1 = %d, want exactly 1", count)
		}
		return nil
	})
}

func TestEvidenceRepository_GetEvidence_NotFound(t *testing.T) {
	store := openCatalogTestStore(t, "evidence-get-notfound.db")
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := (runtimeRepository{tx: tx}).GetEvidence(context.Background(), "missing")
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

// TestEvidenceRepository_ListEvidenceForAttempt_OrderedByKind proves a
// MACHINE_GATE attempt's own multiple per-criterion Evidence rows (one per
// EvidenceKey) all come back for the same AttemptID, ordered deterministically,
// and never leak across attempts.
func TestEvidenceRepository_ListEvidenceForAttempt_OrderedByKind(t *testing.T) {
	store := openCatalogTestStore(t, "evidence-list.db")
	seedSchedulingFixture(t, store)
	testsPass := mustEvidence(t, "evidence-tests", "attempt-1", "tests-pass", "PASS", []string{"artifact-1"}, time.Now())
	lintClean := mustEvidence(t, "evidence-lint", "attempt-1", "lint-clean", "PASS", []string{"artifact-2"}, time.Now())
	otherAttempt := mustEvidence(t, "evidence-other", "attempt-2", "lint-clean", "FAIL", []string{"artifact-3"}, time.Now())

	for _, evidence := range []runtime.Evidence{testsPass, lintClean, otherAttempt} {
		evidence := evidence
		withCatalogTx(t, store, func(tx *sql.Tx) error {
			_, err := (runtimeRepository{tx: tx}).CreateEvidence(context.Background(), evidence)
			return err
		})
	}

	var listed []runtime.Evidence
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		listed, err = (runtimeRepository{tx: tx}).ListEvidenceForAttempt(context.Background(), "attempt-1")
		return err
	})
	if len(listed) != 2 {
		t.Fatalf("listed = %d evidence rows, want 2 (attempt-2's own row must not leak in)", len(listed))
	}
	if listed[0].Kind != "lint-clean" || listed[1].Kind != "tests-pass" {
		t.Fatalf("listed order = [%s, %s], want [lint-clean, tests-pass] (ordered by Kind)", listed[0].Kind, listed[1].Kind)
	}
}
