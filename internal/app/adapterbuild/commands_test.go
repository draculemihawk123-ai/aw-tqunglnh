package adapterbuild_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

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

func TestProbeThenRegister_Succeeds(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	path := writeExecutable(t, "binary-content-v1")

	token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeRequest(path))
	if err != nil {
		t.Fatalf("ProbeAdapterBuild: %v", err)
	}

	result, err := adapterbuild.RegisterAdapterBuild(ctx, uow, adapterbuild.RegisterRequest{
		Token: token, CapabilityManifest: validManifest(), RegisteredBy: "operator-1",
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
}

// TestRegister_DuplicateIsIdempotent is V2-07A's own "duplicate" contract
// test: registering the exact same measured tuple twice must return the
// same row, never error, never create a second row.
func TestRegister_DuplicateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	path := writeExecutable(t, "binary-content-v1")

	token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeRequest(path))
	if err != nil {
		t.Fatalf("ProbeAdapterBuild: %v", err)
	}
	first, err := adapterbuild.RegisterAdapterBuild(ctx, uow, adapterbuild.RegisterRequest{
		Token: token, CapabilityManifest: validManifest(), RegisteredBy: "operator-1",
	})
	if err != nil {
		t.Fatalf("first register: %v", err)
	}

	// A second, independent probe+register of the identical unchanged
	// executable — not merely reusing the same token — must still be
	// idempotent, since idempotency is keyed on the measured content, not
	// on token identity.
	token2, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeRequest(path))
	if err != nil {
		t.Fatalf("second probe: %v", err)
	}
	second, err := adapterbuild.RegisterAdapterBuild(ctx, uow, adapterbuild.RegisterRequest{
		Token: token2, CapabilityManifest: validManifest(), RegisteredBy: "operator-2",
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
	// overwrites the existing immutable row.
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

	tokenV1, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeRequest(path))
	if err != nil {
		t.Fatalf("probe v1: %v", err)
	}
	buildV1, err := adapterbuild.RegisterAdapterBuild(ctx, uow, adapterbuild.RegisterRequest{
		Token: tokenV1, CapabilityManifest: validManifest(), RegisteredBy: "operator-1",
	})
	if err != nil {
		t.Fatalf("register v1: %v", err)
	}

	if err := os.WriteFile(path, []byte("binary-content-v2-genuinely-different"), 0o755); err != nil {
		t.Fatalf("overwrite executable: %v", err)
	}
	tokenV2, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeRequest(path))
	if err != nil {
		t.Fatalf("probe v2: %v", err)
	}
	buildV2, err := adapterbuild.RegisterAdapterBuild(ctx, uow, adapterbuild.RegisterRequest{
		Token: tokenV2, CapabilityManifest: validManifest(), RegisteredBy: "operator-2",
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

	staleToken, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeRequest(path))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	// Someone swaps the binary after the operator saw the candidate but
	// before they actually registered it.
	if err := os.WriteFile(path, []byte("binary-content-SWAPPED"), 0o755); err != nil {
		t.Fatalf("swap executable: %v", err)
	}

	_, err = adapterbuild.RegisterAdapterBuild(ctx, uow, adapterbuild.RegisterRequest{
		Token: staleToken, CapabilityManifest: validManifest(), RegisteredBy: "operator-1",
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

	token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeRequest(path))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	tamperedManifest := validManifest()
	tamperedManifest.SupportsCancel = false

	_, err = adapterbuild.RegisterAdapterBuild(ctx, uow, adapterbuild.RegisterRequest{
		Token: token, CapabilityManifest: tamperedManifest, RegisteredBy: "operator-1",
	})
	if !errors.Is(err, adapterbuild.ErrCapabilityManifestDrift) {
		t.Fatalf("RegisterAdapterBuild error = %v, want ErrCapabilityManifestDrift", err)
	}
}

func TestRegister_RejectsExpiredToken(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	path := writeExecutable(t, "binary-content-v1")

	token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeRequest(path))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	_, err = adapterbuild.RegisterAdapterBuild(ctx, uow, adapterbuild.RegisterRequest{
		Token: expireToken(token), CapabilityManifest: validManifest(), RegisteredBy: "operator-1",
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
	if _, err := adapterbuild.ProbeAdapterBuild(ctx, uow, req); err == nil {
		t.Fatal("ProbeAdapterBuild should reject an invalid capability manifest")
	}
}

func TestProbe_RejectsMissingExecutable(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	req := probeRequest(filepath.Join(t.TempDir(), "does-not-exist"))
	if _, err := adapterbuild.ProbeAdapterBuild(ctx, uow, req); err == nil {
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

	token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeRequest(path))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.AdapterBuilds().RotateSigningKey(ctx)
		return err
	}); err != nil {
		t.Fatalf("rotate signing key: %v", err)
	}

	_, err = adapterbuild.RegisterAdapterBuild(ctx, uow, adapterbuild.RegisterRequest{
		Token: token, CapabilityManifest: validManifest(), RegisteredBy: "operator-1",
	})
	if err == nil {
		t.Fatal("RegisterAdapterBuild should reject a token signed under a since-rotated signing key")
	}
}

// TestRegister_NoUpdateMethodExists proves V2-07A's own "version đã
// publish không sửa được" bar at the port level: ports.AdapterBuildRepository
// has no Update/Delete method for anyone to call in the first place.
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
