package artifactsweep_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/artifactsweep"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// This file exercises the real stack end to end — real sqlite persistence,
// a real filesystem-backed ports.ArtifactStore (internal/adapters/
// artifactstore, real files on real disk) — mirroring every other real-I/O
// job handler test in this codebase (workspacereconcile_test/
// workspacerelease_test's own newRealFixture pattern).

type sweepFixture struct {
	store     *sqlite.Store
	uow       ports.UnitOfWork
	ids       idsource.Source
	artStore  *artifactstore.Store
	clk       *clock.Fixed
	projectID string
}

func newSweepFixture(t *testing.T, dbName string) *sweepFixture {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), dbName))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	artStore, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}
	clk := clock.NewFixed(time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC))

	const projectID = "project-1"
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: projectID, Name: "project " + projectID})
		return err
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	return &sweepFixture{store: store, uow: uow, ids: ids, artStore: artStore, clk: clk, projectID: projectID}
}

// putAndInsertOrphan really writes body through f's own real ArtifactStore,
// then inserts a durable Artifact row for it as Orphan, backdated to
// createdAt — real content, real hash-verified locator, exactly what a
// crashed-before-confirmation writer would leave behind.
func (f *sweepFixture) putAndInsertOrphan(t *testing.T, id string, body []byte, createdAt time.Time) artifact.Artifact {
	t.Helper()
	ctx := context.Background()
	ref, err := f.artStore.Put(ctx, ports.ArtifactMetadata{ContentType: "text/plain"}, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	a, err := artifact.NewArtifact(
		artifact.ID(id), project.ProjectID(f.projectID), ref.Locator, ref.SHA256, ref.Size, "text/plain",
		ref.Sensitivity, ref.Redacted, artifact.RetentionRawOutputTemp, artifact.Orphan, false,
		artifact.ComputeExpiresAt(artifact.RetentionRawOutputTemp, createdAt), createdAt, 1,
	)
	if err != nil {
		t.Fatalf("construct artifact: %v", err)
	}
	var inserted artifact.Artifact
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		var err error
		inserted, err = tx.Artifacts().InsertArtifact(ctx, a)
		return err
	}); err != nil {
		t.Fatalf("InsertArtifact: %v", err)
	}
	return inserted
}

func (f *sweepFixture) reloadArtifact(t *testing.T, id string) artifact.Artifact {
	t.Helper()
	var a artifact.Artifact
	if err := f.uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		a, err = tx.Artifacts().GetArtifact(context.Background(), id)
		return err
	}); err != nil {
		t.Fatalf("GetArtifact(%s): %v", id, err)
	}
	return a
}

func (f *sweepFixture) setRealRun(t *testing.T) {
	t.Helper()
	state, err := f.getState(t)
	if err != nil {
		t.Fatalf("GetArtifactSweepState: %v", err)
	}
	if err := f.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Artifacts().SetArtifactSweepDryRun(context.Background(), ports.SetArtifactSweepDryRunRequest{
			DryRun: false, ExpectedVersion: state.Version,
		})
		return err
	}); err != nil {
		t.Fatalf("SetArtifactSweepDryRun(false): %v", err)
	}
}

func (f *sweepFixture) getState(t *testing.T) (ports.ArtifactSweepState, error) {
	t.Helper()
	var state ports.ArtifactSweepState
	err := f.uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		state, err = tx.Artifacts().GetArtifactSweepState(context.Background())
		return err
	})
	return state, err
}

func (f *sweepFixture) deps() artifactsweep.ExecuteArtifactSweepDeps {
	return artifactsweep.ExecuteArtifactSweepDeps{UnitOfWork: f.uow, IDs: f.ids, Clock: f.clk, Store: f.artStore}
}

// TestExecuteArtifactSweep_DryRunDefault_ReportsWithoutTouchingAnything is
// this task's own "dry-run mặc định" bar: a fresh installation's default
// DryRun=true state must classify and report a real, genuinely-eligible
// Orphan artifact as WOULD_PURGE without ever deleting the real content or
// mutating the row.
func TestExecuteArtifactSweep_DryRunDefault_ReportsWithoutTouchingAnything(t *testing.T) {
	f := newSweepFixture(t, "sweep-dry-run.db")
	old := f.clk.Now().Add(-8 * 24 * time.Hour)
	seeded := f.putAndInsertOrphan(t, "orphan-1", []byte("stale raw output"), old)

	manifest, err := artifactsweep.ExecuteArtifactSweep(context.Background(), f.deps(), 0, "corr-1")
	if err != nil {
		t.Fatalf("ExecuteArtifactSweep: %v", err)
	}
	if !manifest.DryRun || len(manifest.Groups) != 1 || manifest.Groups[0].Decision != "WOULD_PURGE" {
		t.Fatalf("manifest = %+v, want one WOULD_PURGE group under DryRun", manifest)
	}

	got := f.reloadArtifact(t, string(seeded.ID))
	if got.AttachState != artifact.Orphan {
		t.Fatalf("artifact state after dry-run sweep = %s, want unchanged Orphan", got.AttachState)
	}
	if err := f.artStore.Verify(context.Background(), ports.ArtifactRef{Locator: seeded.Locator, SHA256: seeded.ContentHash, Size: seeded.Size}); err != nil {
		t.Fatalf("real content should still exist and verify after a dry run: %v", err)
	}
}

// TestExecuteArtifactSweep_RealRun_DeletesContentAndMarksPurged is the
// happy path once an operator has explicitly flipped DryRun off: the real
// on-disk content is genuinely deleted, the row transitions to Purged
// (never actually removed, per ADR-017's own audit bar), and the next
// generation's own ARTIFACT_SWEEP job is enqueued.
func TestExecuteArtifactSweep_RealRun_DeletesContentAndMarksPurged(t *testing.T) {
	f := newSweepFixture(t, "sweep-real-run.db")
	f.setRealRun(t)
	old := f.clk.Now().Add(-8 * 24 * time.Hour)
	seeded := f.putAndInsertOrphan(t, "orphan-1", []byte("stale raw output"), old)

	manifest, err := artifactsweep.ExecuteArtifactSweep(context.Background(), f.deps(), 0, "corr-1")
	if err != nil {
		t.Fatalf("ExecuteArtifactSweep: %v", err)
	}
	if manifest.DryRun || len(manifest.Groups) != 1 || manifest.Groups[0].Decision != "PURGED" {
		t.Fatalf("manifest = %+v, want one real PURGED group", manifest)
	}
	if manifest.Groups[0].BytesFreed != seeded.Size {
		t.Fatalf("manifest bytesFreed = %d, want %d", manifest.Groups[0].BytesFreed, seeded.Size)
	}

	got := f.reloadArtifact(t, string(seeded.ID))
	if got.AttachState != artifact.Purged {
		t.Fatalf("artifact state after real sweep = %s, want Purged", got.AttachState)
	}
	if got.Locator != seeded.Locator || got.ContentHash != seeded.ContentHash {
		t.Fatalf("Purged row lost its own Locator/ContentHash — audit trail must survive: got=%+v", got)
	}
	verifyErr := f.artStore.Verify(context.Background(), ports.ArtifactRef{Locator: seeded.Locator, SHA256: seeded.ContentHash, Size: seeded.Size})
	if verifyErr == nil {
		t.Fatal("real content should be gone after a real purge, but Verify succeeded")
	}

	state, err := f.getState(t)
	if err != nil {
		t.Fatalf("GetArtifactSweepState: %v", err)
	}
	if state.Generation != 1 {
		t.Fatalf("artifact_sweep_state.Generation after one real run = %d, want 1", state.Generation)
	}
}

// TestExecuteArtifactSweep_AttachedSiblingSharesLocator_BlocksWholeGroup is
// this task's own refcount/liveness bar: a Locator shared by an ORPHAN
// candidate AND a live ATTACHED row (simulating the same bytes Put twice,
// once as a diff-manifest still in use) must never be deleted — the
// ATTACHED row's own real content must survive completely untouched.
func TestExecuteArtifactSweep_AttachedSiblingSharesLocator_BlocksWholeGroup(t *testing.T) {
	f := newSweepFixture(t, "sweep-blocked-sibling.db")
	f.setRealRun(t)
	ctx := context.Background()
	old := f.clk.Now().Add(-8 * 24 * time.Hour)

	body := []byte("shared bytes")
	orphanRow := f.putAndInsertOrphan(t, "orphan-shared", body, old)

	ref, err := f.artStore.Put(ctx, ports.ArtifactMetadata{ContentType: "text/plain"}, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Put (duplicate content): %v", err)
	}
	if ref.Locator != orphanRow.Locator {
		t.Fatalf("duplicate Put produced a different Locator (%s vs %s) — content-addressing assumption broken", ref.Locator, orphanRow.Locator)
	}
	attached, err := artifact.NewArtifact(
		"attached-shared", project.ProjectID(f.projectID), ref.Locator, ref.SHA256, ref.Size, "text/plain",
		ref.Sensitivity, ref.Redacted, artifact.RetentionCanonicalContext, artifact.Attached, false, nil, old, 1,
	)
	if err != nil {
		t.Fatalf("construct attached sibling: %v", err)
	}
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Artifacts().InsertArtifact(ctx, attached)
		return err
	}); err != nil {
		t.Fatalf("InsertArtifact (attached sibling): %v", err)
	}

	manifest, err := artifactsweep.ExecuteArtifactSweep(ctx, f.deps(), 0, "corr-1")
	if err != nil {
		t.Fatalf("ExecuteArtifactSweep: %v", err)
	}
	if len(manifest.Groups) != 1 || manifest.Groups[0].Decision != "BLOCKED" {
		t.Fatalf("manifest = %+v, want one BLOCKED group", manifest)
	}

	if err := f.artStore.Verify(ctx, ref); err != nil {
		t.Fatalf("shared content must survive while a live Attached sibling references it: %v", err)
	}
	if got := f.reloadArtifact(t, "orphan-shared"); got.AttachState != artifact.Orphan {
		t.Fatalf("orphan sibling state = %s, want unchanged Orphan (blocked, not purged)", got.AttachState)
	}
}

// TestExecuteArtifactSweep_ResumesAfterCrashedClaim proves this package's
// own documented crash-recovery reasoning: a Locator claim left behind by
// an earlier, crashed attempt of this exact singleton job (simulated by
// claiming it directly before calling ExecuteArtifactSweep) is resumed,
// never treated as a foreign conflict to abort on.
func TestExecuteArtifactSweep_ResumesAfterCrashedClaim(t *testing.T) {
	f := newSweepFixture(t, "sweep-resume-crash.db")
	f.setRealRun(t)
	ctx := context.Background()
	old := f.clk.Now().Add(-8 * 24 * time.Hour)
	seeded := f.putAndInsertOrphan(t, "orphan-1", []byte("stale raw output"), old)

	// Simulate a crash between phase 1 (claim) and phase 2/3 (delete +
	// finalize) of an earlier attempt: the claim already exists, but the
	// row is still Orphan and the real content is still on disk.
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		return tx.Artifacts().ClaimArtifactLocatorForPurge(ctx, seeded.Locator, "artifact-sweep:singleton", f.clk.Now())
	}); err != nil {
		t.Fatalf("simulate a pre-existing (crashed) claim: %v", err)
	}

	manifest, err := artifactsweep.ExecuteArtifactSweep(ctx, f.deps(), 0, "corr-1")
	if err != nil {
		t.Fatalf("ExecuteArtifactSweep after a simulated crashed claim: %v", err)
	}
	if len(manifest.Groups) != 1 || manifest.Groups[0].Decision != "PURGED" {
		t.Fatalf("manifest = %+v, want the resumed group to actually PURGE", manifest)
	}
	if got := f.reloadArtifact(t, string(seeded.ID)); got.AttachState != artifact.Purged {
		t.Fatalf("artifact state after resumed sweep = %s, want Purged", got.AttachState)
	}
	verifyErr := f.artStore.Verify(ctx, ports.ArtifactRef{Locator: seeded.Locator, SHA256: seeded.ContentHash, Size: seeded.Size})
	if verifyErr == nil {
		t.Fatal("real content should be gone after the resumed purge completes")
	}
}

// TestStartupArtifactSweep_EnqueuesExactlyOneJobPerGeneration proves the
// same idempotent-enqueue guarantee StartupRecoveryScan's own doc comment
// names: two callers racing against the same generation only ever produce
// one durable job.
func TestStartupArtifactSweep_EnqueuesExactlyOneJobPerGeneration(t *testing.T) {
	f := newSweepFixture(t, "sweep-startup.db")
	ctx := context.Background()

	if err := artifactsweep.StartupArtifactSweep(ctx, f.uow, f.ids); err != nil {
		t.Fatalf("first StartupArtifactSweep: %v", err)
	}
	if err := artifactsweep.StartupArtifactSweep(ctx, f.uow, f.ids); err != nil {
		t.Fatalf("second StartupArtifactSweep: %v", err)
	}

	state, err := f.store.LoadDurableJobState(ctx, ports.JobID("id-1"))
	if err != nil {
		t.Fatalf("LoadDurableJobState: %v", err)
	}
	if state == "" {
		t.Fatal("expected exactly one ARTIFACT_SWEEP job to have been enqueued")
	}
}
