// V8-05 — Retention-class, cleanup and disk-pressure behavior
// (docs/design/10-v8-alpha-hardening.md V8-05, AK-ARCH-025B): "TTL 7 ngày
// của raw output/evidence tạm không lan sang canonical conversation/context,
// không phá recovery evidence" — a real end-to-end proof that the retention
// sweeper's own real fake-clock aging, hold, and reference-sharing rules
// (internal/app/artifactsweep, already exhaustively unit-tested at the
// decision-table level in sweep_test.go/sweep_sqlite_test.go) hold when a
// REAL canonical conversation Message sits in the SAME real database and
// real filesystem ArtifactStore as the raw-temp artifacts being aged and
// swept — not just synthetic fixtures on both sides.
//
// A real finding, worth recording for whoever next touches artifact
// retention in this repo: NO real production code path in this codebase
// ever constructs an `artifact.RetentionRawOutputTemp` artifact today.
// internal/app/message (AppendMessage/AppendConversationAttachment) and
// every one of internal/app/runtime's three node executors
// (command_node_executor.go, gate_node_executor.go,
// agent_node_executor_resources.go) all hardcode
// artifact.RetentionCanonicalContext — confirmed by grepping every real
// (non-test) `artifact.NewArtifact`/`artifact.Retention*` call site in the
// app layer. The RetentionRawOutputTemp domain type, its 7-day
// ComputeExpiresAt math, and the sweeper's own aging/hold/reference
// decision table are all real and already well-tested — there is simply no
// real PRODUCER wired to that class yet. This test therefore seeds its own
// raw-temp artifacts directly (the same technique
// internal/app/artifactsweep/sweep_sqlite_test.go's own putAndInsertOrphan
// already uses), the only way to exercise this path against the real
// retained stores until a real producer exists.
package v5accept

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/artifactsweep"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	messagedomain "github.com/taQuangLing/agent-workflow/internal/domain/message"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// seedRawTempArtifact writes body through f's own real ArtifactStore and
// inserts a durable RetentionRawOutputTemp Artifact row for it, backdated to
// createdAt — mirrors internal/app/artifactsweep/sweep_sqlite_test.go's own
// putAndInsertOrphan, generalized to a caller-chosen AttachState/Hold since
// this test needs three different combinations (eligible orphan, held
// orphan, referenced/blocked orphan), not just the one bare-orphan shape
// that file's own helper covers.
func (f *v5AcceptFixture) seedRawTempArtifact(t *testing.T, id string, body []byte, state artifact.AttachState, hold bool, createdAt time.Time) artifact.Artifact {
	t.Helper()
	ctx := context.Background()
	ref, err := f.artifacts.Put(ctx, ports.ArtifactMetadata{ContentType: "text/plain"}, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Put raw-temp artifact %s: %v", id, err)
	}
	a, err := artifact.NewArtifact(
		artifact.ID(id), project.ProjectID(v5AcceptProjectID), ref.Locator, ref.SHA256, ref.Size, "text/plain",
		ref.Sensitivity, ref.Redacted, artifact.RetentionRawOutputTemp, state, hold,
		artifact.ComputeExpiresAt(artifact.RetentionRawOutputTemp, createdAt), createdAt, 1,
	)
	if err != nil {
		t.Fatalf("construct raw-temp artifact %s: %v", id, err)
	}
	var inserted artifact.Artifact
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		var err error
		inserted, err = tx.Artifacts().InsertArtifact(ctx, a)
		return err
	}); err != nil {
		t.Fatalf("InsertArtifact raw-temp %s: %v", id, err)
	}
	return inserted
}

func (f *v5AcceptFixture) reloadArtifactByID(t *testing.T, id string) artifact.Artifact {
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

func TestV5AcceptRetentionSweep_CanonicalConversationHeldAndReferencedSurviveRealEligibleOrphanPurge(t *testing.T) {
	f := newV5AcceptFixture(t)
	ctx := context.Background()

	// A real worker pool is needed here purely so the real WORKSPACE_PROVISION
	// job createRootWorkItem waits on actually gets claimed and processed —
	// this test never starts a workflow run, so the router's own executors
	// are left nil.
	registry := f.registerHandlers(&runtime.NodeExecutorRouter{}, "v805")
	_, stopPool := f.startPool(t, registry)
	defer stopPool()

	root := f.createRootWorkItem(t, "v8-05-retention-root", workdomain.RepositoryRead)

	// -- 1. A REAL canonical conversation Message, through the real
	// application command, in the SAME real database/artifact store the
	// sweep below runs against. --
	conversationBody := []byte("this is a real canonical conversation message that must survive every retention sweep, forever")
	digest := sha256.Sum256(conversationBody)
	appended, err := message.AppendConversationAttachment(
		ctx, f.uow, f.artifacts, f.ids, clock.System{},
		testCmd("v8-05-conversation", ports.ProjectScope(v5AcceptProjectID), "AppendConversationAttachment"),
		message.AppendConversationAttachmentRequest{
			ProjectID: v5AcceptProjectID, WorkItemID: root.WorkItemID, Role: messagedomain.RoleUser,
			Body: bytes.NewReader(conversationBody), ContentType: "text/plain", Sensitivity: 0,
			DeclaredSHA256: hex.EncodeToString(digest[:]),
		},
	)
	if err != nil {
		t.Fatalf("AppendConversationAttachment: %v", err)
	}
	canonicalArtifactID := appended.ContentArtifactID
	if canonicalArtifactID == "" {
		t.Fatal("AppendConversationAttachment returned no ContentArtifactID")
	}

	// -- 2. Three real raw-temp artifacts, all backdated 8 days before the
	// fixed "now" the sweep below runs with (past the real 7-day grace),
	// covering the three ways V8-05's own completion bar says a sweep must
	// NOT purge something: held, referenced (shares a locator with a live
	// ATTACHED sibling), and genuinely eligible (neither). --
	sweepClock := clock.NewFixed(time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC))
	agedPastGrace := sweepClock.Now().Add(-8 * 24 * time.Hour)

	eligible := f.seedRawTempArtifact(t, "v8-05-eligible-orphan", []byte("real eligible raw output, past grace, no hold, no reference"), artifact.Orphan, false, agedPastGrace)

	held := f.seedRawTempArtifact(t, "v8-05-held-orphan", []byte("real held raw output, past grace but on legal hold"), artifact.Orphan, true, agedPastGrace)

	referencedBody := []byte("real raw output whose exact bytes are still referenced by a live attached sibling")
	referencedOrphan := f.seedRawTempArtifact(t, "v8-05-referenced-orphan", referencedBody, artifact.Orphan, false, agedPastGrace)
	// The live sibling: same content (so it shares referencedOrphan's own
	// real, content-addressed Locator), inserted directly as ATTACHED —
	// mirrors internal/app/artifactsweep/sweep_sqlite_test.go's own
	// TestExecuteArtifactSweep_AttachedSiblingSharesLocator_BlocksWholeGroup
	// fixture technique exactly.
	sameLocatorRef, err := f.artifacts.Put(ctx, ports.ArtifactMetadata{ContentType: "text/plain"}, bytes.NewReader(referencedBody))
	if err != nil {
		t.Fatalf("Put duplicate content for the referenced sibling: %v", err)
	}
	if sameLocatorRef.Locator != referencedOrphan.Locator {
		t.Fatalf("duplicate Put produced a different Locator (%s vs %s) — content-addressing assumption broken", sameLocatorRef.Locator, referencedOrphan.Locator)
	}
	referencedSibling, err := artifact.NewArtifact(
		"v8-05-referenced-sibling-attached", project.ProjectID(v5AcceptProjectID), sameLocatorRef.Locator, sameLocatorRef.SHA256, sameLocatorRef.Size,
		"text/plain", sameLocatorRef.Sensitivity, sameLocatorRef.Redacted, artifact.RetentionCanonicalContext, artifact.Attached, false, nil, agedPastGrace, 1,
	)
	if err != nil {
		t.Fatalf("construct referenced sibling: %v", err)
	}
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Artifacts().InsertArtifact(ctx, referencedSibling)
		return err
	}); err != nil {
		t.Fatalf("InsertArtifact referenced sibling: %v", err)
	}

	// -- 3. "Before" manifest: every artifact's own real content, verified
	// readable, BEFORE the sweep runs. --
	for _, id := range []string{canonicalArtifactID, string(eligible.ID), string(held.ID), string(referencedOrphan.ID)} {
		a := f.reloadArtifactByID(t, id)
		if err := f.artifacts.Verify(ctx, ports.ArtifactRef{Locator: a.Locator, SHA256: a.ContentHash, Size: a.Size}); err != nil {
			t.Fatalf("before-sweep: artifact %s does not verify: %v", id, err)
		}
	}

	// -- 4. The real sweep, real run (not dry-run), with the fixed clock
	// above as its own "now". --
	var sweepState ports.ArtifactSweepState
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		sweepState, err = tx.Artifacts().GetArtifactSweepState(ctx)
		return err
	}); err != nil {
		t.Fatalf("GetArtifactSweepState: %v", err)
	}
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Artifacts().SetArtifactSweepDryRun(ctx, ports.SetArtifactSweepDryRunRequest{DryRun: false, ExpectedVersion: sweepState.Version})
		return err
	}); err != nil {
		t.Fatalf("SetArtifactSweepDryRun(false): %v", err)
	}

	manifest, err := artifactsweep.ExecuteArtifactSweep(ctx, artifactsweep.ExecuteArtifactSweepDeps{
		UnitOfWork: f.uow, IDs: f.ids, Clock: sweepClock, Store: f.artifacts,
	}, 0, "v8-05-corr-1")
	if err != nil {
		t.Fatalf("ExecuteArtifactSweep: %v", err)
	}
	if manifest.DryRun {
		t.Fatal("manifest.DryRun = true after explicitly disabling dry-run")
	}

	// -- 5. "After" manifest: real per-artifact assertions. --

	// The real canonical conversation Message must be completely untouched
	// — structurally guaranteed (RetentionCanonicalContext never carries an
	// ExpiresAt, so the sweep's own aging check can never select it), but
	// asserted here for real: same state, same real bytes, still verifies.
	canonicalAfter := f.reloadArtifactByID(t, canonicalArtifactID)
	if canonicalAfter.AttachState != artifact.Attached {
		t.Fatalf("canonical conversation artifact state after sweep = %s, want Attached (untouched)", canonicalAfter.AttachState)
	}
	if canonicalAfter.RetentionClass != artifact.RetentionCanonicalContext {
		t.Fatalf("canonical conversation artifact RetentionClass changed to %s", canonicalAfter.RetentionClass)
	}
	requireArtifactBytesUnchanged(t, ctx, f.artifacts, canonicalAfter, conversationBody)

	// The held orphan must survive, Hold still true, content intact.
	heldAfter := f.reloadArtifactByID(t, string(held.ID))
	if heldAfter.AttachState != artifact.Orphan || !heldAfter.Hold {
		t.Fatalf("held orphan after sweep = state=%s hold=%v, want Orphan+Hold (untouched)", heldAfter.AttachState, heldAfter.Hold)
	}
	requireArtifactBytesUnchanged(t, ctx, f.artifacts, heldAfter, []byte("real held raw output, past grace but on legal hold"))

	// The referenced orphan must survive (BLOCKED by its own live ATTACHED
	// sibling sharing the same real Locator), content intact.
	referencedAfter := f.reloadArtifactByID(t, string(referencedOrphan.ID))
	if referencedAfter.AttachState != artifact.Orphan {
		t.Fatalf("referenced orphan after sweep = state=%s, want Orphan (untouched, blocked by its live sibling)", referencedAfter.AttachState)
	}
	requireArtifactBytesUnchanged(t, ctx, f.artifacts, referencedAfter, referencedBody)
	siblingAfter := f.reloadArtifactByID(t, "v8-05-referenced-sibling-attached")
	if siblingAfter.AttachState != artifact.Attached {
		t.Fatalf("referenced sibling after sweep = state=%s, want Attached (untouched)", siblingAfter.AttachState)
	}

	// The genuinely eligible orphan (no hold, no reference, past grace)
	// must actually be purged — the positive control proving this sweep
	// really did something, not merely that nothing got destroyed.
	eligibleAfter := f.reloadArtifactByID(t, string(eligible.ID))
	if eligibleAfter.AttachState != artifact.Purged {
		t.Fatalf("eligible orphan after sweep = state=%s, want Purged", eligibleAfter.AttachState)
	}
	if verifyErr := f.artifacts.Verify(ctx, ports.ArtifactRef{Locator: eligibleAfter.Locator, SHA256: eligibleAfter.ContentHash, Size: eligibleAfter.Size}); verifyErr == nil {
		t.Fatal("eligible orphan's real content should be gone after a real purge, but Verify succeeded")
	}

	// The manifest itself (V8-05's own "before/after manifest" Verify bar,
	// made concrete) must report exactly one PURGED group (the eligible
	// orphan) and the rest BLOCKED (held, referenced) — never silently
	// omitting either outcome.
	var purgedGroups, blockedGroups int
	for _, group := range manifest.Groups {
		switch group.Decision {
		case "PURGED":
			purgedGroups++
		case "BLOCKED":
			blockedGroups++
		}
	}
	if purgedGroups != 1 {
		t.Fatalf("manifest reports %d PURGED groups, want exactly 1; manifest=%+v", purgedGroups, manifest)
	}
	if blockedGroups != 2 {
		t.Fatalf("manifest reports %d BLOCKED groups, want exactly 2 (held, referenced); manifest=%+v", blockedGroups, manifest)
	}
}

func requireArtifactBytesUnchanged(t *testing.T, ctx context.Context, store ports.ArtifactStore, a artifact.Artifact, want []byte) {
	t.Helper()
	rc, err := store.Open(ctx, ports.ArtifactRef{Locator: a.Locator, SHA256: a.ContentHash, Size: a.Size})
	if err != nil {
		t.Fatalf("Open artifact %s after sweep: %v", a.ID, err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read artifact %s after sweep: %v", a.ID, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("artifact %s content changed by sweep: got %q, want %q", a.ID, got, want)
	}
}
