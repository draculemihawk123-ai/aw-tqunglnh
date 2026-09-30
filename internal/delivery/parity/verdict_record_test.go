package parity

// V8-12: docs/release/alpha-verdict.json is the recorded Alpha verdict. The
// parity ledger is the one thing it currently blames, so this test ties the two
// together: the record may list the parity final gate as a blocker exactly as
// long as Ledger() still pins debt, and ALPHA_READY can never be recorded while
// it does. Closing the last ledger entry therefore fails this test until the
// verdict record (and the report and start-here that restate it) is updated —
// the record cannot silently go stale.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const verdictRecordPath = "../../../docs/release/alpha-verdict.json"

func TestAlphaVerdictRecordMatchesTheParityLedger(t *testing.T) {
	content, err := os.ReadFile(filepath.FromSlash(verdictRecordPath))
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Verdict  string `json:"verdict"`
		Blockers []struct {
			ID string `json:"id"`
		} `json:"blockers"`
	}
	if err := json.Unmarshal(content, &record); err != nil {
		t.Fatalf("%s: %v", verdictRecordPath, err)
	}

	const parityGate = "parity-inventory-has-zero-debt"
	recordBlamesParity := false
	for _, b := range record.Blockers {
		if b.ID == parityGate {
			recordBlamesParity = true
		}
	}
	ledgerHasDebt := len(Ledger()) > 0

	if ledgerHasDebt && !recordBlamesParity {
		t.Errorf("the ledger still pins %d debt entries but the verdict record does not list %s as a blocker", len(Ledger()), parityGate)
	}
	if !ledgerHasDebt && recordBlamesParity {
		t.Errorf("the ledger is empty but the verdict record still lists %s as a blocker — record a new verdict for the commit that closed it", parityGate)
	}
	if ledgerHasDebt && record.Verdict == "ALPHA_READY" {
		t.Errorf("the verdict record says ALPHA_READY while the parity ledger still pins %d debt entries", len(Ledger()))
	}
}
