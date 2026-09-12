package doctor_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/doctor"
)

// TestCheckSafeSettings_HealthyOnFreshDatabase proves a freshly migrated
// database (the safe_settings singleton row seeded by migration 0036) reads
// back cleanly through the real sqlite.UnitOfWork.
func TestCheckSafeSettings_HealthyOnFreshDatabase(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "doctor-safe-settings-healthy.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	result := doctor.CheckSafeSettings(context.Background(), sqlite.NewUnitOfWork(store))
	if result.Status != doctor.StatusHealthy {
		t.Fatalf("CheckSafeSettings (fresh db) = %+v, want HEALTHY", result)
	}
}

// TestCheckSafeSettings_BlockedOnCorruptRow is this task's own "corrupt
// persisted settings fail ... Doctor typed" Verify scenario. There is no
// real production code path that ever writes malformed JSON into
// desired_json — this test corrupts the row directly via raw SQL to
// simulate the out-of-band corruption (disk bit rot, a manual DB edit) this
// check exists to catch, never to fabricate a verified command outcome.
func TestCheckSafeSettings_BlockedOnCorruptRow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "doctor-safe-settings-corrupt.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if err := sqlite.CorruptSafeSettingsDesiredJSONForTest(context.Background(), store); err != nil {
		t.Fatalf("corrupt fixture row: %v", err)
	}

	result := doctor.CheckSafeSettings(context.Background(), sqlite.NewUnitOfWork(store))
	if result.Status != doctor.StatusBlocked {
		t.Fatalf("CheckSafeSettings (corrupt row) = %+v, want BLOCKED", result)
	}
	if result.Remediation == "" {
		t.Fatal("CheckSafeSettings (corrupt row) has no Remediation — Doctor exists to say what to do next")
	}
}
