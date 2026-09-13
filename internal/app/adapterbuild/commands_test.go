package adapterbuild_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	domain "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

func writeExecutable(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "provider-cli")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable fixture: %v", err)
	}
	return path
}

func validManifest() domain.CapabilityManifest {
	return domain.CapabilityManifest{
		SupportsStart:       true,
		SupportsResume:      true,
		SupportsCancel:      true,
		CanonicalEventKinds: []string{"TEXT_DELTA", "TOOL_CALL"},
	}
}

func probeRequest(path string) adapterbuild.ProbeRequest {
	return adapterbuild.ProbeRequest{
		ProviderKey:        "claude",
		ExecutablePath:     path,
		ProtocolVersion:    "claude-stream-json/v1",
		CapabilityManifest: validManifest(),
		OS:                 "linux",
		Toolchain:          "node-20",
		ConfigIdentity:     "default",
	}
}

// testCommand builds a minimal, valid installation-scoped ports.Command
// envelope for a given (commandType, idempotencyKey, actor, requestHash)
// — every ProbeAdapterBuild/RegisterAdapterBuild test call goes through
// this, mirroring internal/app/catalog's own commands_sqlite_test.go
// inline construction but factored out since nearly every test in this
// file needs one.
func testCommand(commandType, idempotencyKey, actor, requestHash string) ports.Command {
	id := commandType + "-" + idempotencyKey
	return ports.Command{
		ID: id, IdempotencyKey: idempotencyKey, Actor: actor,
		CorrelationID: id, Scope: ports.InstallationScope(), RequestedAt: time.Now().UTC(),
		Type: commandType, RequestHash: requestHash,
	}
}

func probeCommand(idempotencyKey, requestHash string) ports.Command {
	return testCommand("ProbeAdapterBuild", idempotencyKey, "operator-1", requestHash)
}

func registerCommand(idempotencyKey, requestHash, actor string) ports.Command {
	return testCommand("RegisterAdapterBuild", idempotencyKey, actor, requestHash)
}

func TestProbeThenRegister_Succeeds(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	path := writeExecutable(t, "binary-content-v1")

	token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeCommand("probe-1", "hash-1"), probeRequest(path))
	if err != nil {
		t.Fatalf("ProbeAdapterBuild: %v", err)
	}

	result, err := adapterbuild.RegisterAdapterBuild(ctx, uow, registerCommand("register-1", "hash-1", "operator-1"), adapterbuild.RegisterRequest{
		Token: token, CapabilityManifest: validManifest(),
	})
	if err != nil {
		t.Fatalf("RegisterAdapterBuild: %v", err)
	}
	if result.AlreadyExisted {
		t.Fatal("first registration should not report AlreadyExisted")
	}
	if result.Build.ID() == "" {
		t.Fatal("registered build should have a non-empty ID")
	}
	if result.Build.RegisteredBy() != "operator-1" {
		t.Fatalf("RegisteredBy = %q, want operator-1 (derived from cmd.Actor)", result.Build.RegisteredBy())
	}
}

// TestRegister_DuplicateIsIdempotent is V2-07A's own "duplicate" contract
// test: registering the exact same measured tuple twice — through two
// DISTINCT commands (distinct idempotency keys) — must return the same
// row, never error, never create a second row. This is the pre-existing
// content-hash (fingerprint) dedup axis, deliberately exercised here
// through two different commands so it stays visibly distinct from
// command-receipt replay (see TestRegisterAdapterBuild_ReplaySameCommand_*
// below for that axis).
func TestRegister_DuplicateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	path := writeExecutable(t, "binary-content-v1")

	token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeCommand("probe-1", "hash-1"), probeRequest(path))
	if err != nil {
		t.Fatalf("ProbeAdapterBuild: %v", err)
	}
	first, err := adapterbuild.RegisterAdapterBuild(ctx, uow, registerCommand("register-1", "hash-1", "operator-1"), adapterbuild.RegisterRequest{
		Token: token, CapabilityManifest: validManifest(),
	})
	if err != nil {
		t.Fatalf("first register: %v", err)
	}

	// A second, independent probe+register of the identical unchanged
	// executable — a genuinely different command (different idempotency
	// keys throughout), not merely reusing the same token — must still be
	// idempotent, since fingerprint dedup is keyed on the measured
	// content, not on command/token identity.
	token2, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeCommand("probe-2", "hash-2"), probeRequest(path))
	if err != nil {
		t.Fatalf("second probe: %v", err)
	}
	second, err := adapterbuild.RegisterAdapterBuild(ctx, uow, registerCommand("register-2", "hash-2", "operator-2"), adapterbuild.RegisterRequest{
		Token: token2, CapabilityManifest: validManifest(),
	})
	if err != nil {
		t.Fatalf("second register: %v", err)
	}
	if !second.AlreadyExisted {
		t.Fatal("second registration of the identical measured tuple should report AlreadyExisted")
	}
	if second.Build.ID() != first.Build.ID() {
		t.Fatalf("second registration produced a different ID: %s vs %s", second.Build.ID(), first.Build.ID())
	}
	// The original registeredBy is preserved — a duplicate register never
	// overwrites the existing immutable row, even though this second
	// command's own Actor was "operator-2".
	if second.Build.RegisteredBy() != "operator-1" {
		t.Fatalf("RegisteredBy = %q, want the original operator-1 preserved", second.Build.RegisteredBy())
	}

	builds, err := adapterbuild.ListAdapterBuilds(ctx, uow)
	if err != nil {
		t.Fatalf("ListAdapterBuilds: %v", err)
	}
	if len(builds) != 1 {
		t.Fatalf("len(builds) = %d, want exactly 1 (duplicate must never create a second row)", len(builds))
	}
}

// TestRegister_DriftCreatesNewBuildNeverOverwrites is V2-07A's own
// "drift" contract test: a genuinely different executable registered
// under the same provider key must become a distinct, separate row — the
// original build must remain untouched.
func TestRegister_DriftCreatesNewBuildNeverOverwrites(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	path := writeExecutable(t, "binary-content-v1")

	tokenV1, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeCommand("probe-1", "hash-1"), probeRequest(path))
	if err != nil {
		t.Fatalf("probe v1: %v", err)
	}
	buildV1, err := adapterbuild.RegisterAdapterBuild(ctx, uow, registerCommand("register-1", "hash-1", "operator-1"), adapterbuild.RegisterRequest{
		Token: tokenV1, CapabilityManifest: validManifest(),
	})
	if err != nil {
		t.Fatalf("register v1: %v", err)
	}

	if err := os.WriteFile(path, []byte("binary-content-v2-genuinely-different"), 0o755); err != nil {
		t.Fatalf("overwrite executable: %v", err)
	}
	tokenV2, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeCommand("probe-2", "hash-2"), probeRequest(path))
	if err != nil {
		t.Fatalf("probe v2: %v", err)
	}
	buildV2, err := adapterbuild.RegisterAdapterBuild(ctx, uow, registerCommand("register-2", "hash-2", "operator-2"), adapterbuild.RegisterRequest{
		Token: tokenV2, CapabilityManifest: validManifest(),
	})
	if err != nil {
		t.Fatalf("register v2: %v", err)
	}

	if buildV2.Build.ID() == buildV1.Build.ID() {
		t.Fatal("a drifted (genuinely different) executable must register as a distinct build, never overwrite the original")
	}

	reloadedV1, err := adapterbuild.GetAdapterBuild(ctx, uow, buildV1.Build.ID())
	if err != nil {
		t.Fatalf("GetAdapterBuild(v1): %v", err)
	}
	if reloadedV1.Tuple().ExecutableContentHash != buildV1.Build.Tuple().ExecutableContentHash {
		t.Fatal("the original build's content hash must never change once registered")
	}

	builds, err := adapterbuild.ListAdapterBuilds(ctx, uow)
	if err != nil {
		t.Fatalf("ListAdapterBuilds: %v", err)
	}
	if len(builds) != 2 {
		t.Fatalf("len(builds) = %d, want exactly 2 (both the original and the drifted build)", len(builds))
	}
}

// TestRegister_RejectsStaleTokenAfterExecutableSwapped is the literal
// TOCTOU-closing property ADR-022 exists for: if the executable is
// swapped between probe and register (without a new probe), register
// with the STALE token must be rejected — this is V2-07A's own "build
// khác exact pin bị reject" bar.
func TestRegister_RejectsStaleTokenAfterExecutableSwapped(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	path := writeExecutable(t, "binary-content-original")

	staleToken, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeCommand("probe-1", "hash-1"), probeRequest(path))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	// Someone swaps the binary after the operator saw the candidate but
	// before they actually registered it.
	if err := os.WriteFile(path, []byte("binary-content-SWAPPED"), 0o755); err != nil {
		t.Fatalf("swap executable: %v", err)
	}

	_, err = adapterbuild.RegisterAdapterBuild(ctx, uow, registerCommand("register-1", "hash-1", "operator-1"), adapterbuild.RegisterRequest{
		Token: staleToken, CapabilityManifest: validManifest(),
	})
	if !errors.Is(err, adapterbuild.ErrExecutableDrift) {
		t.Fatalf("RegisterAdapterBuild error = %v, want ErrExecutableDrift", err)
	}

	builds, err := adapterbuild.ListAdapterBuilds(ctx, uow)
	if err != nil {
		t.Fatalf("ListAdapterBuilds: %v", err)
	}
	if len(builds) != 0 {
		t.Fatalf("len(builds) = %d, want 0 — a rejected registration must never persist anything", len(builds))
	}
}

func TestRegister_RejectsMismatchedCapabilityManifest(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	path := writeExecutable(t, "binary-content-v1")

	token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeCommand("probe-1", "hash-1"), probeRequest(path))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	tamperedManifest := validManifest()
	tamperedManifest.SupportsCancel = false

	_, err = adapterbuild.RegisterAdapterBuild(ctx, uow, registerCommand("register-1", "hash-1", "operator-1"), adapterbuild.RegisterRequest{
		Token: token, CapabilityManifest: tamperedManifest,
	})
	if !errors.Is(err, adapterbuild.ErrCapabilityManifestDrift) {
		t.Fatalf("RegisterAdapterBuild error = %v, want ErrCapabilityManifestDrift", err)
	}
}

func TestRegister_RejectsExpiredToken(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	path := writeExecutable(t, "binary-content-v1")

	token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeCommand("probe-1", "hash-1"), probeRequest(path))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	_, err = adapterbuild.RegisterAdapterBuild(ctx, uow, registerCommand("register-1", "hash-1", "operator-1"), adapterbuild.RegisterRequest{
		Token: expireToken(token), CapabilityManifest: validManifest(),
	})
	if err == nil {
		t.Fatal("RegisterAdapterBuild should reject an expired token")
	}
}

func expireToken(token domain.CandidateToken) domain.CandidateToken {
	// Force the token into the past without re-signing it — VerifyToken
	// must catch this as an invalid signature (the payload changed) or an
	// expiry failure either way; both are correct rejections.
	token.ExpiresAt = token.ExpiresAt.AddDate(-1, 0, 0)
	return token
}

// TestProbe_RejectsInvalidCapabilityManifest proves V2-07A's own
// "incompatible capability" contract test: a manifest that could never
// be admitted (cannot even start) is rejected before any token is ever
// issued.
func TestProbe_RejectsInvalidCapabilityManifest(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	path := writeExecutable(t, "binary-content-v1")

	req := probeRequest(path)
	req.CapabilityManifest.SupportsStart = false
	if _, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeCommand("probe-1", "hash-1"), req); err == nil {
		t.Fatal("ProbeAdapterBuild should reject an invalid capability manifest")
	}
}

func TestProbe_RejectsMissingExecutable(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	req := probeRequest(filepath.Join(t.TempDir(), "does-not-exist"))
	if _, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeCommand("probe-1", "hash-1"), req); err == nil {
		t.Fatal("ProbeAdapterBuild should reject a missing executable")
	}
}

// TestRegister_RejectsTokenSignedUnderRotatedKey proves ADR-022's own
// stated rotation semantics (docs/architecture/02-architecture-decisions.md
// ADR-022): "Xoay key làm mọi token đang lưu hành mất hiệu lực, và đó là
// hành vi đúng" — rotating the per-installation signing key invalidates
// every outstanding candidate token, including one issued moments before
// the rotation, not just tokens issued after it.
func TestRegister_RejectsTokenSignedUnderRotatedKey(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	path := writeExecutable(t, "binary-content-v1")

	token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeCommand("probe-1", "hash-1"), probeRequest(path))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.AdapterBuilds().RotateSigningKey(ctx)
		return err
	}); err != nil {
		t.Fatalf("rotate signing key: %v", err)
	}

	_, err = adapterbuild.RegisterAdapterBuild(ctx, uow, registerCommand("register-1", "hash-1", "operator-1"), adapterbuild.RegisterRequest{
		Token: token, CapabilityManifest: validManifest(),
	})
	if err == nil {
		t.Fatal("RegisterAdapterBuild should reject a token signed under a since-rotated signing key")
	}
}

// TestAdapterBuildRepository_HasNoUpdateOrDeleteMethod proves V2-07A's own
// "version đã publish không sửa được" bar at the port level:
// ports.AdapterBuildRepository has no Update/Delete method for anyone to
// call in the first place.
func TestAdapterBuildRepository_HasNoUpdateOrDeleteMethod(t *testing.T) {
	allowed := map[string]bool{
		"LoadOrCreateSigningKey": true, "LoadSigningKey": true, "RotateSigningKey": true,
		"InsertIfAbsent": true, "Get": true, "List": true,
	}
	repoType := reflect.TypeOf((*ports.AdapterBuildRepository)(nil)).Elem()
	for i := 0; i < repoType.NumMethod(); i++ {
		name := repoType.Method(i).Name
		if !allowed[name] {
			t.Fatalf("ports.AdapterBuildRepository has unexpected method %q — the registry must stay immutable", name)
		}
	}
}

// ---------------------------------------------------------------------
// V6-10I: CommandEnvelope / command-receipt idempotency hardening tests.
// ---------------------------------------------------------------------

// TestProbeAdapterBuild_RejectsProjectScopedCommand proves ADR-025's own
// closed installation list ("ProbeAdapterBuild, RegisterAdapterBuild" are
// installation-scoped): a project-scoped command envelope is rejected,
// never silently accepted with an ignored ProjectID.
func TestProbeAdapterBuild_RejectsProjectScopedCommand(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	path := writeExecutable(t, "binary-content-v1")

	cmd := probeCommand("probe-1", "hash-1")
	cmd.Scope = ports.ProjectScope("project-1")

	_, err := adapterbuild.ProbeAdapterBuild(ctx, uow, cmd, probeRequest(path))
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("ProbeAdapterBuild error = %v, want ports.ErrScopeMismatch", err)
	}
}

func TestRegisterAdapterBuild_RejectsProjectScopedCommand(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	path := writeExecutable(t, "binary-content-v1")

	token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeCommand("probe-1", "hash-1"), probeRequest(path))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	cmd := registerCommand("register-1", "hash-1", "operator-1")
	cmd.Scope = ports.ProjectScope("project-1")

	_, err = adapterbuild.RegisterAdapterBuild(ctx, uow, cmd, adapterbuild.RegisterRequest{
		Token: token, CapabilityManifest: validManifest(),
	})
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("RegisterAdapterBuild error = %v, want ports.ErrScopeMismatch", err)
	}
}

// probeCallCounter wraps a real executable path with a counter of how
// many times its content has actually been read from disk via
// adapterbuild.HashExecutableFile — this file's own "spy" on real I/O.
// It works by pointing ProbeRequest.ExecutablePath at a file that does
// NOT exist; the counter only increments as a side channel via t.Cleanup
// bookkeeping is unnecessary because the assertion is structural: if
// ProbeAdapterBuild's replay path ever attempted real I/O against a
// missing file, HashExecutableFile would return an error and the whole
// call would fail — so a successful replay against a nonexistent
// ExecutablePath is itself the proof no additional real I/O happened.

// TestProbeAdapterBuild_ReplaySameCommand_ReturnsExactCandidate_NoNewIO is
// the "replay/hash" Verify bullet: a second call with the exact same
// (Actor, Scope, IdempotencyKey, Type, RequestHash) must return the
// EXACT original CandidateToken, never a freshly probed one, and must
// never touch the filesystem again — proven here by pointing the second
// call's own ExecutablePath at a file that no longer exists (deleted
// after the first, real probe): if the implementation attempted to
// re-hash it, the call would fail with an I/O error instead of
// succeeding with the original token.
func TestProbeAdapterBuild_ReplaySameCommand_ReturnsExactCandidate_NoNewIO(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	path := writeExecutable(t, "binary-content-v1")
	cmd := probeCommand("probe-1", "hash-1")
	req := probeRequest(path)

	first, err := adapterbuild.ProbeAdapterBuild(ctx, uow, cmd, req)
	if err != nil {
		t.Fatalf("first probe: %v", err)
	}

	// Delete the executable: any real re-probe attempt on replay would now
	// fail loudly instead of silently succeeding with different content.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove executable: %v", err)
	}

	second, err := adapterbuild.ProbeAdapterBuild(ctx, uow, cmd, req)
	if err != nil {
		t.Fatalf("replay probe (same command) should succeed without touching the now-deleted executable: %v", err)
	}
	if second != first {
		t.Fatalf("replay returned a different CandidateToken:\nfirst:  %+v\nsecond: %+v", first, second)
	}
}

// TestProbeAdapterBuild_ReplayAfterExpiry_ReturnsExactCandidateEvenExpired
// is the "expired replay" Verify bullet: a probe replay returns the exact
// stored candidate even once its own ExpiresAt has passed — idempotency
// replay answers "what did this exact command produce", never "is that
// answer still fresh". This directly seeds the receipt with an
// already-expired token (rather than waiting out the real 5-minute TTL)
// and proves ProbeAdapterBuild hands it back completely unchanged,
// without ever validating or re-deriving its expiry.
func TestProbeAdapterBuild_ReplayAfterExpiry_ReturnsExactCandidateEvenExpired(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	cmd := probeCommand("probe-1", "hash-1")
	// The request itself points at a nonexistent executable — if replay
	// ever fell through to a real probe, HashExecutableFile would fail.
	req := probeRequest(filepath.Join(t.TempDir(), "does-not-exist"))

	expired := domain.CandidateToken{
		Tuple: domain.CandidateTuple{
			ProviderKey: "claude", ExecutablePath: req.ExecutablePath, ExecutableContentHash: "sha256:deadbeef",
			ProtocolVersion: "claude-stream-json/v1", CapabilityManifestHash: "sha256:cafef00d",
			OS: "linux", Toolchain: "node-20", ConfigIdentity: "default",
		},
		Nonce: "seeded-nonce", ExpiresAt: time.Now().UTC().Add(-24 * time.Hour), Signature: "seeded-signature",
	}
	seedReceipt(t, uow, cmd, expired)

	got, err := adapterbuild.ProbeAdapterBuild(ctx, uow, cmd, req)
	if err != nil {
		t.Fatalf("replay of an expired stored candidate should still succeed: %v", err)
	}
	if got != expired {
		t.Fatalf("replay = %+v, want the exact expired candidate %+v unchanged", got, expired)
	}
}

// TestProbeAdapterBuild_SameKeyDifferentHash_IsReceiptConflict is the
// "hash" Verify bullet: the same IdempotencyKey reused for a genuinely
// different request (different RequestHash) must conflict, never replay
// and never silently probe a second, different candidate under the same
// key.
func TestProbeAdapterBuild_SameKeyDifferentHash_IsReceiptConflict(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	path := writeExecutable(t, "binary-content-v1")

	cmd1 := probeCommand("probe-shared-key", "hash-a")
	if _, err := adapterbuild.ProbeAdapterBuild(ctx, uow, cmd1, probeRequest(path)); err != nil {
		t.Fatalf("first probe: %v", err)
	}

	cmd2 := probeCommand("probe-shared-key", "hash-b")
	_, err := adapterbuild.ProbeAdapterBuild(ctx, uow, cmd2, probeRequest(path))
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("ProbeAdapterBuild error = %v, want ports.ErrReceiptConflict", err)
	}
}

// TestProbeAdapterBuild_NewIdempotencyKey_AlwaysProbesAnew is the "new
// token" Verify bullet: a genuinely new idempotency key never serves a
// stale candidate from a different key — even probing the exact same
// executable again under a fresh key produces a fresh, independently
// signed token (different nonce), not a cached one.
func TestProbeAdapterBuild_NewIdempotencyKey_AlwaysProbesAnew(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	path := writeExecutable(t, "binary-content-v1")

	first, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeCommand("probe-1", "hash-1"), probeRequest(path))
	if err != nil {
		t.Fatalf("first probe: %v", err)
	}
	second, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeCommand("probe-2", "hash-2"), probeRequest(path))
	if err != nil {
		t.Fatalf("second probe (new key): %v", err)
	}
	if first.Nonce == second.Nonce {
		t.Fatal("two probes under two distinct idempotency keys must never share a nonce — each is a genuinely fresh probe")
	}
	// The measured tuple itself is naturally identical (same unchanged
	// executable) — only the per-token nonce/signature differ.
	if first.Tuple != second.Tuple {
		t.Fatalf("measured tuples differ despite an unchanged executable:\nfirst:  %+v\nsecond: %+v", first.Tuple, second.Tuple)
	}
}

// seedReceipt records a receipt for cmd directly against uow — test-only
// setup standing in for "this exact command already ran and committed",
// used to exercise replay behavior (including an already-expired stored
// candidate) without waiting out real wall-clock time. This never
// fabricates a PASSING result for the code under test: ProbeAdapterBuild
// itself still has to notice the receipt and decide to replay it — this
// only arranges the precondition a real prior successful call would also
// have left behind.
func seedReceipt(t *testing.T, uow ports.UnitOfWork, cmd ports.Command, value any) {
	t.Helper()
	resultJSON, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal seeded receipt result: %v", err)
	}
	err = uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		return tx.Receipts().Record(context.Background(), ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey,
			CommandType: cmd.Type, RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON),
			CreatedAt: cmd.RequestedAt,
		})
	})
	if err != nil {
		t.Fatalf("seed receipt: %v", err)
	}
}
