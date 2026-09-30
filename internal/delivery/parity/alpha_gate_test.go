package parity

// V8-11 (docs/design/10-v8-alpha-hardening.md): two of the mandatory final
// gates are expressed as tests here so internal/alphagate can read their
// outcome from a targeted `go test -json` run instead of re-deriving them.
//
//   - TestRecoveryCommandsHaveCoreAPIUICLIOwners: "mọi recovery command có đủ
//     owner core/API/UI/CLI". Always runs.
//   - TestParityLedgerIsEmpty: "parity inventory UI↔operationId↔`aw`↔
//     application operation không có debt". OPT-IN (AW_ALPHA_GATE=1): at the
//     time V8-11 was written Ledger() pins known debt that V6-15O's own
//     "no new leaf/route" rule forbade it to close, so this test is red by
//     design until that debt is closed — and a permanently red test in the
//     default suite would make every unrelated PR fail. The alpha gate job
//     sets the variable, so the gate itself reports the debt truthfully
//     (FAIL, never a silent pass) while `go test ./...` stays green.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// alphaGateEnvVar opts a test in to the V8-11 final-gate run.
const alphaGateEnvVar = "AW_ALPHA_GATE"

// recoveryCommands are the three named recovery actions V6-06D exposes for a
// blocked Attempt/WorkItem ("transport cho RetryBlockedActivation,
// CancelWorkItem, ResolveWorkItemBlocker") — the closed set a blocked thing
// can be recovered with through the public surface.
var recoveryCommands = []string{"RetryBlockedActivation", "CancelWorkItem", "ResolveWorkItemBlocker"}

func TestRecoveryCommandsHaveCoreAPIUICLIOwners(t *testing.T) {
	inputs := realInputs(t)

	registry := map[string]PublicOperation{}
	for _, op := range inputs.Registry {
		registry[op.Name] = op
	}
	httpOps := map[string]bool{}
	for _, op := range inputs.HTTP.Operations {
		httpOps[op.OperationID] = true
	}
	webSources := readWebSources(t)

	for _, name := range recoveryCommands {
		op, ok := registry[name]
		if !ok {
			t.Errorf("%s: no entry in the public operation registry (core owner missing)", name)
			continue
		}
		if op.Kind != KindCommand || op.Exposure != ExposurePublic {
			t.Errorf("%s: registered as kind=%s exposure=%s, want a PUBLIC command", name, op.Kind, op.Exposure)
		}
		if strings.TrimSpace(op.Symbol) == "" {
			t.Errorf("%s: registry entry names no application symbol (core owner missing)", name)
		}
		if len(op.HTTP) == 0 {
			t.Errorf("%s: registry entry has no HTTP binding (API owner missing)", name)
			continue
		}
		for _, binding := range op.HTTP {
			if !httpOps[binding.OperationID] {
				t.Errorf("%s: operationId %q is not in the real OpenAPI contract (API owner missing)", name, binding.OperationID)
			}

			cliFound := false
			for _, d := range inputs.CLI {
				if d.AppOperation == name && d.HTTPOperationID == binding.OperationID {
					cliFound = true
				}
			}
			if !cliFound {
				t.Errorf("%s: no `aw` leaf mirrors operationId %q (CLI owner missing)", name, binding.OperationID)
			}

			if !webCallsOperation(webSources, binding.OperationID) {
				t.Errorf("%s: no UI source under web/src calls %s(...) (UI owner missing)", name, binding.OperationID)
			}
		}
	}
}

// readWebSources returns every non-test, non-generated TypeScript source under
// web/src keyed by repo-relative path. Three levels up from this package is
// the repository root.
func readWebSources(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.FromSlash("../../../web/src")
	sources := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if !(strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".tsx")) ||
			strings.Contains(name, ".test.") || name == "generated.ts" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sources[filepath.ToSlash(path)] = string(content)
		return nil
	})
	if err != nil {
		t.Fatalf("read web sources: %v", err)
	}
	if len(sources) == 0 {
		t.Fatal("found no TypeScript source under web/src — the path or the layout changed")
	}
	return sources
}

// webCallsOperation reports whether some UI source invokes the generated API
// client function named operationID (`name(`), as opposed to merely listing
// the id in a string.
func webCallsOperation(sources map[string]string, operationID string) bool {
	call := regexp.MustCompile(`(^|[^A-Za-z0-9_.'"])` + regexp.QuoteMeta(operationID) + `\(`)
	for _, content := range sources {
		if call.MatchString(content) {
			return true
		}
	}
	return false
}

func TestParityLedgerIsEmpty(t *testing.T) {
	if os.Getenv(alphaGateEnvVar) != "1" {
		t.Skipf("V8-11 final-gate check is opt-in: set %s=1 (see this file's doc comment)", alphaGateEnvVar)
	}
	ledger := Ledger()
	for _, entry := range ledger {
		t.Errorf("parity debt: %s (owner %s) — %s", entry.Key(), entry.Owner, entry.Reason)
	}
	if len(ledger) != 0 {
		t.Fatalf("the parity ledger still pins %d debt entr(ies); the Alpha gate requires zero", len(ledger))
	}
}
