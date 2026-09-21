// Package v6accept is V6-14's black-box acceptance suite
// (docs/design/08-v6-api-projections.md V6-14): one journey, from a clean
// database, driven ONLY through the public HTTP surface of two real
// operating-system processes — the final `aw serve` and `aw worker`
// binaries — never an in-process handler call, never SQLite or Git state
// seeded after bootstrap.
//
// The suite is opt-in (AW_HTTP_ACCEPTANCE=1): it builds three binaries and
// spawns real child processes, which is the wrong cost profile for the
// offline unit suite that the V0-12 gate re-runs eleven times inside a hard
// job budget. Its own CI job sets the variable; a default `go test ./...`
// compiles and vets it (so it cannot bit-rot) but skips it, saying why.
package v6accept

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

// acceptanceEnvVar opts a run in to the black-box suite.
const acceptanceEnvVar = "AW_HTTP_ACCEPTANCE"

// requireAcceptance skips the calling test unless the suite was opted in to.
func requireAcceptance(t *testing.T) {
	t.Helper()
	if os.Getenv(acceptanceEnvVar) != "1" {
		t.Skipf("black-box HTTP acceptance is opt-in: set %s=1 (it builds and spawns real aw processes)", acceptanceEnvVar)
	}
}

// binaries are the three executables the journey needs, built once per test
// binary run from the current working tree.
type binaries struct {
	aw         string // the final production composition root
	fakeClaude string // the repository's own Claude wire-protocol stand-in
}

var (
	buildOnce sync.Once
	built     binaries
	buildErr  error
	buildDir  string
)

// builtBinaries compiles cmd/aw and cmd/fake-claude exactly once. It builds
// from the repository the test runs in (not a checked-in artifact), so the
// journey always exercises the code under review.
func builtBinaries(t *testing.T) binaries {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "aw-v6accept-bin-*")
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
			{"fake-claude", "./cmd/fake-claude", &built.fakeClaude},
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
		t.Fatalf("build acceptance binaries: %v", buildErr)
	}
	return built
}

// moduleRoot returns the directory holding go.mod.
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
