package apicontract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// generatedTSClientPath is the one committed fixture this test protects —
// V7-02's own "API client từ contract" requirement, generated from the
// SAME real production Contract every other test in this package already
// builds via buildRealContract(t) (golden_test.go's own helper), so "the
// generated client matches what the server actually serves" is true by
// construction, exactly like TestContract_MatchesGoldenFixture above it.
//
// Regeneration recipe when a route/schema change makes this test fail
// (mirrors TestContract_MatchesGoldenFixture's own doc comment): review the
// diff shown in the failure message, then temporarily add a throwaway test
// in this package that calls
//
//	os.WriteFile(generatedTSClientPath, GenerateTypeScriptClient(buildRealContract(t)), 0o644)
//
// run it once, delete it, and commit the resulting web/src/api/generated.ts
// alongside whatever route/schema change caused the diff.
const generatedTSClientPath = "../../../../web/src/api/generated.ts"

func TestGeneratedTypeScriptClient_MatchesGoldenFixture(t *testing.T) {
	contract := buildRealContract(t)
	got := GenerateTypeScriptClient(contract)

	want, err := os.ReadFile(filepath.Clean(generatedTSClientPath))
	if err != nil {
		t.Fatalf("read committed client %s: %v", generatedTSClientPath, err)
	}

	if string(got) != string(want) {
		t.Errorf("generated TypeScript client does not match %s.\n"+
			"If this is an intentional API change, regenerate the fixture "+
			"(see this test's own doc comment) and review the diff before committing.\n"+
			"--- got ---\n%s\n--- want ---\n%s", generatedTSClientPath, got, want)
	}
}

// TestGeneratedTypeScriptClient_SkipsBrowserOnlyOperations proves the
// generator never emits a callable function for "bootstrap"/"staticAsset"
// — a UI action must never be tempted to `fetch()` either through the
// generated client (see tsclient.go's own doc comment for why).
func TestGeneratedTypeScriptClient_SkipsBrowserOnlyOperations(t *testing.T) {
	contract := buildRealContract(t)
	got := string(GenerateTypeScriptClient(contract))

	for op := range browserOnlyOperations {
		if !hasOperationID(contract, op) {
			t.Fatalf("test fixture assumption broken: contract no longer has operationId %q", op)
		}
		if strings.Contains(got, "export function "+op+"(") {
			t.Fatalf("generated client emitted a callable function for browser-only operation %q", op)
		}
	}
}

// TestGeneratedTypeScriptClient_SkipsRawContentOperations mirrors
// TestGeneratedTypeScriptClient_SkipsBrowserOnlyOperations exactly, for the
// other reason a generated function would be actively misleading: a route
// whose real response is raw bytes, never JSON (tsclient.go's own
// rawContentOperations doc comment).
func TestGeneratedTypeScriptClient_SkipsRawContentOperations(t *testing.T) {
	contract := buildRealContract(t)
	got := string(GenerateTypeScriptClient(contract))

	for op := range rawContentOperations {
		if !hasOperationID(contract, op) {
			t.Fatalf("test fixture assumption broken: contract no longer has operationId %q", op)
		}
		if strings.Contains(got, "export function "+op+"(") {
			t.Fatalf("generated client emitted a callable function for raw-content operation %q", op)
		}
	}
}

// TestGeneratedTypeScriptClient_SkipsRawUploadOperations mirrors
// TestGeneratedTypeScriptClient_SkipsRawContentOperations exactly, for the
// write-side twin: a route whose real REQUEST body is raw bytes, never
// JSON (tsclient.go's own rawUploadOperations doc comment) — a generated
// function would JSON-marshal the wrong body and the real handler would
// reject every call.
func TestGeneratedTypeScriptClient_SkipsRawUploadOperations(t *testing.T) {
	contract := buildRealContract(t)
	got := string(GenerateTypeScriptClient(contract))

	for op := range rawUploadOperations {
		if !hasOperationID(contract, op) {
			t.Fatalf("test fixture assumption broken: contract no longer has operationId %q", op)
		}
		if strings.Contains(got, "export function "+op+"(") {
			t.Fatalf("generated client emitted a callable function for raw-upload operation %q", op)
		}
	}
}

func hasOperationID(c Contract, id string) bool {
	for _, op := range c.Operations {
		if op.OperationID == id {
			return true
		}
	}
	return false
}
