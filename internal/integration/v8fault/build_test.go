// Package v8fault is V8-02's own fault-injection matrix
// (docs/design/10-v8-alpha-hardening.md V8-02, GC-ACC-16): SPK-04's six
// standard crash-transaction boundaries (docs/spikes/01-go-core-spike-plan.md
// §9), already proven at the spike level against `cmd/spike-worker` in
// internal/spikeacceptance's own SPK-04 scenario, re-run here against the
// REAL `aw worker` binary — proving the PRODUCTION reaper/scheduler loop
// itself genuinely reclaims each crashed job, not just that the underlying
// SQLite transactions are individually safe to replay (which SPK-04 already
// established by driving recovery through direct Store calls). Every
// arrange/seed step reuses internal/adapters/sqlite's already-exported
// crash-fixture helpers (SeedCrashResumeOwners, CrashResumeWorkflowDefinition,
// etc. — crashworker.go's own doc comment: shared, non-test code, precisely
// so a caller outside that package's own tests can seed the identical
// fixtures) and the crash injection itself reuses `cmd/spike-worker` exactly
// as SPK-04 does — this package invents no new crash mechanism, it changes
// only what recovers from the crash: a real `aw worker` process instead of
// a manual RecoverExpiredJobs/ClaimJob call.
package v8fault

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// acceptanceEnvVar mirrors internal/integration/v6accept's own opt-in: this
// suite builds two real binaries and spawns real child processes, the wrong
// cost profile for the default `go test ./...` gate.
const acceptanceEnvVar = "AW_HTTP_ACCEPTANCE"

func requireAcceptance(t *testing.T) {
	t.Helper()
	if os.Getenv(acceptanceEnvVar) != "1" {
		t.Skipf("fault-injection matrix is opt-in: set %s=1 (it builds and spawns real aw/spike-worker processes)", acceptanceEnvVar)
	}
}

type binaries struct {
	aw          string
	spikeWorker string
}

var (
	buildOnce sync.Once
	built     binaries
	buildErr  error
	buildDir  string
)

func builtBinaries(t *testing.T) binaries {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "aw-v8fault-bin-*")
		if err != nil {
			buildErr = err
			return
		}
		buildDir = dir
		root, err := moduleRoot()
		if err != nil {
			buildErr = err
			return
		}
		suffix := ""
		if runtime.GOOS == "windows" {
			suffix = ".exe"
		}
		for _, target := range []struct {
			name string
			pkg  string
			out  *string
		}{
			{"aw", "./cmd/aw", &built.aw},
			{"spike-worker", "./cmd/spike-worker", &built.spikeWorker},
		} {
			out := filepath.Join(dir, target.name+suffix)
			command := exec.Command("go", "build", "-o", out, target.pkg)
			command.Dir = root
			if combined, err := command.CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("go build %s: %w\n%s", target.pkg, err, combined)
				return
			}
			*target.out = out
		}
	})
	if buildErr != nil {
		t.Fatalf("build fault-matrix binaries: %v", buildErr)
	}
	return built
}

func moduleRoot() (string, error) {
	output, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		return "", fmt.Errorf("go list -m: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func TestMain(m *testing.M) {
	code := m.Run()
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
	os.Exit(code)
}
