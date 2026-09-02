package doctor_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/doctor"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
)

// openTestQueryStore opens a real, migrated database and wraps it as a
// ports.QueryStore — these golden tests exercise Run end to end, so they
// use the real adapter (legitimate here: this is a _test.go file, which
// internal/archtest's domain/app-never-imports-adapters rule does not
// check — only the importable, non-test doctor package itself must stay
// adapter-free, which is exactly why CheckDatabase takes a
// ports.QueryStore instead of opening one).
func openTestQueryStore(t *testing.T) (store *sqlite.Store, queryStore *sqlite.QueryStoreAdapter) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "agentkit.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store, sqlite.NewQueryStore(store)
}

// normalize replaces every occurrence of each key in replacements with
// its mapped value, applied to each check's raw Detail/Remediation field
// BEFORE JSON-encoding the result — not after, on the encoded text,
// where a Windows path's backslashes would already be JSON-escaped
// (`\\`) and so would never match the raw (single-backslash) key a
// caller naturally passes in. This exists because a real Report
// necessarily embeds environment-specific text a golden file can never
// hold verbatim: a fresh t.TempDir() path every run, and this machine's
// installed git --version string. Everything else in the JSON — check
// names, categories, statuses, and the fixed parts of every detail/
// remediation message — stays byte-for-byte real, so the golden file
// still catches any actual behavior change.
func normalize(t *testing.T, report doctor.Report, replacements map[string]string) []byte {
	t.Helper()
	normalized := doctor.Report{Status: report.Status}
	for _, c := range report.Checks {
		c.Detail = applyReplacements(c.Detail, replacements)
		c.Remediation = applyReplacements(c.Remediation, replacements)
		normalized.Checks = append(normalized.Checks, c)
	}
	encoded, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	return append(encoded, '\n')
}

func applyReplacements(s string, replacements map[string]string) string {
	for from, to := range replacements {
		if from == "" {
			continue
		}
		s = strings.ReplaceAll(s, from, to)
	}
	return s
}

func compareToGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	goldenPath := filepath.Join("testdata", "golden", name)
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture %s: %v", goldenPath, err)
	}
	if string(got) != string(want) {
		t.Fatalf("normalized report for %s does not match golden fixture.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func gitVersion(t *testing.T) string {
	t.Helper()
	result := doctor.CheckGit(context.Background())
	if result.Status != doctor.StatusHealthy {
		t.Skip("git is not available on this machine; skipping golden report test")
	}
	return result.Detail
}

// TestRun_Golden_Healthy is V1-11's own "doctor JSON golden cho healthy"
// Verify requirement.
func TestRun_Golden_Healthy(t *testing.T) {
	dbDir := t.TempDir()
	artifactDir := t.TempDir()
	providerDir := t.TempDir()
	providerPath := filepath.Join(providerDir, "fake-provider")
	if err := os.WriteFile(providerPath, []byte("fake-provider-binary-v1\n"), 0o755); err != nil {
		t.Fatalf("write fixture executable: %v", err)
	}
	dbPath := filepath.Join(dbDir, "agentkit.db")
	_, queryStore := openTestQueryStore(t)

	opts := doctor.Options{
		Config: config.Config{
			DatabasePath: dbPath, ArtifactRoot: artifactDir, WorkerID: "worker-1",
			WorkerConcurrency: 4, LeaseTTL: 30 * time.Second, LeaseHeartbeat: 10 * time.Second,
			ProcessOutputLimit:  1 << 20,
			ProviderExecutables: map[string]string{"claude": providerPath},
		},
		Store:        queryStore,
		WorkerConfig: workerpool.Config{Owner: "worker-1", LeaseTTL: 30 * time.Second, HeartbeatEvery: 10 * time.Second},
		CheckWorker:  true,
	}
	report := doctor.Run(context.Background(), opts)
	if report.Status != doctor.StatusHealthy {
		t.Fatalf("report.Status = %v, want HEALTHY: %+v", report.Status, report.Checks)
	}

	got := normalize(t, report, map[string]string{
		dbPath:        "{{DB_PATH}}",
		artifactDir:   "{{ARTIFACT_ROOT}}",
		providerPath:  "{{PROVIDER_PATH}}",
		gitVersion(t): "{{GIT_VERSION}}",
	})
	compareToGolden(t, "healthy.json", got)
}

// TestRun_Golden_Degraded is V1-11's own "doctor JSON golden cho
// degraded" Verify requirement: nothing is BLOCKED, but the artifact
// root doesn't exist yet and the configured provider executable is
// missing.
func TestRun_Golden_Degraded(t *testing.T) {
	dbDir := t.TempDir()
	missingArtifactRoot := filepath.Join(t.TempDir(), "not-created-yet")
	missingProviderPath := filepath.Join(t.TempDir(), "missing-provider-binary")
	dbPath := filepath.Join(dbDir, "agentkit.db")
	_, queryStore := openTestQueryStore(t)

	opts := doctor.Options{
		Config: config.Config{
			DatabasePath: dbPath, ArtifactRoot: missingArtifactRoot, WorkerID: "worker-1",
			WorkerConcurrency: 4, LeaseTTL: 30 * time.Second, LeaseHeartbeat: 10 * time.Second,
			ProcessOutputLimit:  1 << 20,
			ProviderExecutables: map[string]string{"claude": missingProviderPath},
		},
		Store:        queryStore,
		WorkerConfig: workerpool.Config{Owner: "worker-1", LeaseTTL: 30 * time.Second, HeartbeatEvery: 10 * time.Second},
		CheckWorker:  true,
	}
	report := doctor.Run(context.Background(), opts)
	if report.Status != doctor.StatusDegraded {
		t.Fatalf("report.Status = %v, want DEGRADED: %+v", report.Status, report.Checks)
	}

	got := normalize(t, report, map[string]string{
		dbPath:              "{{DB_PATH}}",
		missingArtifactRoot: "{{ARTIFACT_ROOT}}",
		missingProviderPath: "{{PROVIDER_PATH}}",
		gitVersion(t):       "{{GIT_VERSION}}",
	})
	compareToGolden(t, "degraded.json", got)
}

// TestRun_Golden_Blocked is V1-11's own "doctor JSON golden cho blocked"
// Verify requirement: invalid startup configuration (empty WorkerID)
// blocks the whole report even though every other check would pass.
func TestRun_Golden_Blocked(t *testing.T) {
	dbDir := t.TempDir()
	artifactDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "agentkit.db")
	_, queryStore := openTestQueryStore(t)

	opts := doctor.Options{
		Config: config.Config{
			DatabasePath: dbPath, ArtifactRoot: artifactDir, WorkerID: "", // invalid: triggers BLOCKED
			WorkerConcurrency: 4, LeaseTTL: 30 * time.Second, LeaseHeartbeat: 10 * time.Second,
			ProcessOutputLimit:  1 << 20,
			ProviderExecutables: map[string]string{},
		},
		Store:        queryStore,
		WorkerConfig: workerpool.Config{Owner: "worker-1", LeaseTTL: 30 * time.Second, HeartbeatEvery: 10 * time.Second},
		CheckWorker:  true,
	}
	report := doctor.Run(context.Background(), opts)
	if report.Status != doctor.StatusBlocked {
		t.Fatalf("report.Status = %v, want BLOCKED: %+v", report.Status, report.Checks)
	}

	got := normalize(t, report, map[string]string{
		dbPath:        "{{DB_PATH}}",
		artifactDir:   "{{ARTIFACT_ROOT}}",
		gitVersion(t): "{{GIT_VERSION}}",
	})
	compareToGolden(t, "blocked.json", got)
}
