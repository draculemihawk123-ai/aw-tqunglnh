// This file is V6-15A's own Verify line (docs/design/08-v6-api-projections.md
// V6-15A: "go build ./cmd/aw; old production path absent; spike builds;
// serve/worker startup/shutdown smoke"): a real go test proving the
// composition-root rename from cmd/agentkit to cmd/aw (ADR-028,
// AK-ARCH-018) stays in place — cmd/aw exists as a real directory holding
// this binary's package main, cmd/agentkit no longer exists at all, not
// even as a stub or alias (ADR-028's own "không duy trì alias production
// `agentkit`", restated in V6-15A's "Không làm: no alias `agentkit`"), and
// the separate V0 regression binary cmd/agentkit-spike is untouched
// (V6-15A's own "Không làm: keep agentkit-spike"). A future PR that
// accidentally recreates cmd/agentkit, or deletes/renames
// cmd/agentkit-spike, fails CI immediately instead of silently drifting
// from the ADR.
package archtest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompositionRootIsCmdAwNotCmdAgentkit(t *testing.T) {
	moduleRoot := findModuleRoot(t)

	awDir := filepath.Join(moduleRoot, "cmd", "aw")
	info, err := os.Stat(awDir)
	if err != nil {
		t.Fatalf("cmd/aw must exist as the canonical composition root (ADR-028, V6-15A): %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("cmd/aw must be a directory, got a file: %s", awDir)
	}
	if _, err := os.Stat(filepath.Join(awDir, "main.go")); err != nil {
		t.Fatalf("cmd/aw/main.go must exist: %v", err)
	}

	oldDir := filepath.Join(moduleRoot, "cmd", "agentkit")
	if _, statErr := os.Stat(oldDir); statErr == nil {
		t.Fatalf("cmd/agentkit must not exist any more — V6-15A moved it to cmd/aw with no alias left behind (ADR-028's own \"không duy trì alias production `agentkit`\")")
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("unexpected error checking cmd/agentkit is absent: %v", statErr)
	}

	spikeDir := filepath.Join(moduleRoot, "cmd", "agentkit-spike")
	spikeInfo, err := os.Stat(spikeDir)
	if err != nil {
		t.Fatalf("cmd/agentkit-spike must still exist unchanged — V6-15A's own \"Không làm: keep agentkit-spike\": %v", err)
	}
	if !spikeInfo.IsDir() {
		t.Fatalf("cmd/agentkit-spike must be a directory, got a file: %s", spikeDir)
	}
}
