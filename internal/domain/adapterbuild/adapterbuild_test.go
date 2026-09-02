package adapterbuild_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

func validTuple() adapterbuild.CandidateTuple {
	return adapterbuild.CandidateTuple{
		ProviderKey:            "claude",
		ExecutablePath:         "/usr/local/bin/claude",
		ExecutableContentHash:  "sha256:abc",
		ProtocolVersion:        "claude-stream-json/v1",
		CapabilityManifestHash: "sha256:def",
		OS:                     "linux",
		Toolchain:              "node-20",
		ConfigIdentity:         "default",
	}
}

func validManifest() adapterbuild.CapabilityManifest {
	return adapterbuild.CapabilityManifest{
		SupportsStart:       true,
		SupportsResume:      true,
		SupportsCancel:      true,
		CanonicalEventKinds: []string{"TEXT_DELTA", "TOOL_CALL"},
	}
}

func TestCandidateTuple_ID_DeterministicAndContentAddressed(t *testing.T) {
	id1, err := validTuple().ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	id2, err := validTuple().ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("ID() not deterministic: %s vs %s", id1, id2)
	}

	changed := validTuple()
	changed.ExecutableContentHash = "sha256:different"
	id3, err := changed.ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	if id3 == id1 {
		t.Fatal("a different executable content hash should change the tuple's ID")
	}
}

func TestCandidateTuple_ID_RejectsEmptyField(t *testing.T) {
	tuple := validTuple()
	tuple.OS = ""
	if _, err := tuple.ID(); err == nil {
		t.Fatal("ID() should reject an empty field")
	}
}

func TestValidateCapabilityManifest_RejectsCannotStart(t *testing.T) {
	manifest := validManifest()
	manifest.SupportsStart = false
	if err := adapterbuild.ValidateCapabilityManifest(manifest); err == nil {
		t.Fatal("ValidateCapabilityManifest should reject SupportsStart=false")
	}
}

func TestValidateCapabilityManifest_RejectsDuplicateEventKind(t *testing.T) {
	manifest := validManifest()
	manifest.CanonicalEventKinds = []string{"TEXT_DELTA", "TEXT_DELTA"}
	if err := adapterbuild.ValidateCapabilityManifest(manifest); err == nil {
		t.Fatal("ValidateCapabilityManifest should reject a duplicate event kind")
	}
}

func TestValidateCapabilityManifest_RejectsEmptyEventKind(t *testing.T) {
	manifest := validManifest()
	manifest.CanonicalEventKinds = []string{""}
	if err := adapterbuild.ValidateCapabilityManifest(manifest); err == nil {
		t.Fatal("ValidateCapabilityManifest should reject an empty event kind")
	}
}

func TestHashCapabilityManifest_SetOrderIndependent(t *testing.T) {
	first := validManifest()
	first.CanonicalEventKinds = []string{"A", "B"}
	second := validManifest()
	second.CanonicalEventKinds = []string{"B", "A"}

	_, hash1, err := adapterbuild.HashCapabilityManifest(first)
	if err != nil {
		t.Fatalf("HashCapabilityManifest(first): %v", err)
	}
	_, hash2, err := adapterbuild.HashCapabilityManifest(second)
	if err != nil {
		t.Fatalf("HashCapabilityManifest(second): %v", err)
	}
	if hash1 != hash2 {
		t.Fatalf("hash1 = %s, hash2 = %s, want identical regardless of event kind order", hash1, hash2)
	}
}

func TestNewBuild_ValidRequestSucceeds(t *testing.T) {
	build, err := adapterbuild.NewBuild(adapterbuild.NewBuildRequest{
		Tuple:              validTuple(),
		CapabilityManifest: validManifest(),
		RegisteredBy:       "operator-1",
		RegisteredAt:       time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NewBuild: %v", err)
	}
	if build.ID() == "" {
		t.Fatal("NewBuild should produce a non-empty ID")
	}
}

func TestNewBuild_RejectsInvalidCapabilityManifest(t *testing.T) {
	manifest := validManifest()
	manifest.SupportsStart = false
	_, err := adapterbuild.NewBuild(adapterbuild.NewBuildRequest{
		Tuple:              validTuple(),
		CapabilityManifest: manifest,
		RegisteredBy:       "operator-1",
		RegisteredAt:       time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("NewBuild should reject an invalid capability manifest")
	}
}

func TestNewBuild_RejectsMissingRegisteredByOrAt(t *testing.T) {
	if _, err := adapterbuild.NewBuild(adapterbuild.NewBuildRequest{
		Tuple: validTuple(), CapabilityManifest: validManifest(),
		RegisteredBy: "", RegisteredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}); err == nil {
		t.Fatal("NewBuild should reject an empty RegisteredBy")
	}
	if _, err := adapterbuild.NewBuild(adapterbuild.NewBuildRequest{
		Tuple: validTuple(), CapabilityManifest: validManifest(),
		RegisteredBy: "operator-1", RegisteredAt: time.Time{},
	}); err == nil {
		t.Fatal("NewBuild should reject a zero RegisteredAt")
	}
}

// TestBuild_NoExportedMutationMethod is V2-07A's own "version đã publish
// không sửa được" bar, verified the same way V2-01 verified
// definition.VersionFields's own immutability: reflection over Build's
// method set finds nothing beyond accessors.
func TestBuild_NoExportedMutationMethod(t *testing.T) {
	build, err := adapterbuild.NewBuild(adapterbuild.NewBuildRequest{
		Tuple: validTuple(), CapabilityManifest: validManifest(),
		RegisteredBy: "operator-1", RegisteredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NewBuild: %v", err)
	}
	allowed := map[string]bool{
		"ID": true, "Tuple": true, "CapabilityManifest": true, "RegisteredBy": true, "RegisteredAt": true,
	}
	buildType := reflect.TypeOf(build)
	for i := 0; i < buildType.NumMethod(); i++ {
		name := buildType.Method(i).Name
		if !allowed[name] {
			t.Fatalf("Build has unexpected exported method %q — Build must stay immutable", name)
		}
	}
}

// TestAdapterBuildVersion_NeverLeaksAsDefinitionKind is V2-07A's own
// Verify-line requirement: "test khẳng định registry không lộ ra như một
// DefinitionKind" (ADR-022). AdapterBuildVersion must never be
// expressible as a definition.Kind — neither directly (the closed enum
// simply has no such value) nor indirectly through
// definition.DependencyPin (whose Kind field is typed as the same closed
// enum, so it can never carry one either).
func TestAdapterBuildVersion_NeverLeaksAsDefinitionKind(t *testing.T) {
	for _, candidate := range []definition.Kind{
		"ADAPTER_BUILD_VERSION", "ADAPTER_BUILD", "AdapterBuildVersion",
	} {
		if candidate.Valid() {
			t.Fatalf("definition.Kind(%q).Valid() = true, want false — AdapterBuildVersion must never be a DefinitionKind", candidate)
		}
	}
	// A DependencyPin's Kind is the same closed definition.Kind type, so
	// the only way to construct one that claims to reference an adapter
	// build is to defeat the type system with a raw string conversion —
	// which is exactly what this asserts stays rejected downstream by
	// every Kind.Valid() check this repo already has (Block/Command/
	// Gate/etc.'s own ValidateDocument functions), not silently accepted.
	pin := definition.DependencyPin{Kind: definition.Kind("ADAPTER_BUILD_VERSION"), DefinitionID: "x", VersionID: "y"}
	if pin.Kind.Valid() {
		t.Fatal("a DependencyPin claiming an ADAPTER_BUILD_VERSION kind must never validate as a real Kind")
	}
}
