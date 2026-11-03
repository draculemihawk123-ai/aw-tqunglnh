package doctor_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/doctor"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// V12-04 (UX-07): `aw doctor` said HEALTHY while the Claude session had expired. It now reports DEGRADED while an attempt waits
// for a provider sign-in.
func TestCheckProviderLogin_DegradedWhileAnAttemptWaitsForSignIn(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "doctor-login.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := sqlite.SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-root"); err != nil {
		t.Fatal(err)
	}
	uow := sqlite.NewUnitOfWork(store)
	if got := doctor.CheckProviderLogin(ctx, uow); got.Status != doctor.StatusHealthy {
		t.Fatalf("no blocker: %+v, want HEALTHY", got)
	}

	blocker, err := work.NewWorkItemBlocker("b-1", "project-1", "work-root", work.BlockerProviderLoginRequired, "", "", "",
		"OAuth token has expired · Please run /login", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Work().CreateWorkItemBlocker(ctx, blocker)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	got := doctor.CheckProviderLogin(ctx, uow)
	if got.Status != doctor.StatusDegraded || !strings.Contains(got.Detail, "work-root") || !strings.Contains(got.Detail, "OAuth token has expired") ||
		!strings.Contains(got.Remediation, "/login") {
		t.Fatalf("with an open blocker: %+v, want DEGRADED naming the work item and the provider's words", got)
	}
}
