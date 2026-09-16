// Package health is V6-15C's own CLI leaf over V6-01's own health
// endpoints (docs/design/08-v6-api-projections.md V6-15C: "operate health
// ... from the terminal with the same installation authority as HTTP"):
// `aw health live` and `aw health ready` — the terminal-side mirror of GET
// /health/live and GET /health/ready
// (internal/delivery/httpapi/health.go), built on the exact same
// httpapi.ReadinessChecker aggregation that package already ships, never a
// second, independently-invented health concept.
//
// This package never wires itself into cmd/aw (V6-15O's own job — "chỉ
// V6-15O compose CLI/parity registry", docs/design/08-v6-api-projections.md
// §1 rule 8): it only registers its own cli.Descriptor(s) into cli.Default
// from its own init(), and exposes RunLive/RunReady for a future
// composition root to call once that wiring exists.
package health

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"health", "live"}, Scope: cli.ScopeInstallation,
		AppOperation: "HealthLive", HTTPOperationID: "healthLive",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"health", "ready"}, Scope: cli.ScopeInstallation,
		AppOperation: "HealthReady", HTTPOperationID: "healthReady",
	})
}

// Dependencies is everything `aw health ready` needs from a composition
// root — deliberately narrow, mirroring
// internal/delivery/httpapi/doctor.Dependencies' own "everything a handler
// needs travels through one explicit struct" shape. `aw health live` needs
// nothing at all (see Live below), so it takes no Dependencies.
type Dependencies struct {
	// UnitOfWork backs the "database" and "safe_settings" readiness checks
	// — the SAME ports.UnitOfWork every other command in this process
	// dispatches through, never a second, competing connection.
	UnitOfWork ports.UnitOfWork
	// ArtifactRoot backs the "artifact_root" readiness check — this
	// process' own configured artifact root path (config.Config.ArtifactRoot,
	// or cmd/aw/serve.go's own --artifact-root flag value).
	ArtifactRoot string
}

// NewReadinessChecker builds this invocation's own httpapi.ReadinessChecker,
// registering the same "database"/"artifact_root"/"safe_settings" checks
// cmd/aw/serve.go's own composition root registers for GET /health/ready
// (see that file's own checker.Register calls) — deliberately omitting the
// HTTP-only "routes" check: there is no HTTP route composition step for a
// one-shot CLI invocation to ever finalize.
func NewReadinessChecker(deps Dependencies) *httpapi.ReadinessChecker {
	checker := httpapi.NewReadinessChecker()
	checker.Register("database", func(ctx context.Context) error {
		return deps.UnitOfWork.WithReadOnly(ctx, func(ports.Tx) error { return nil })
	})
	checker.Register("artifact_root", func(ctx context.Context) error {
		info, err := os.Stat(deps.ArtifactRoot)
		if err != nil {
			return fmt.Errorf("artifact root: %w", err)
		}
		if !info.IsDir() {
			return errors.New("artifact root is not a directory")
		}
		return nil
	})
	checker.Register("safe_settings", func(ctx context.Context) error {
		return deps.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
			_, err := tx.SafeSettings().Get(ctx)
			return err
		})
	})
	return checker
}

// LiveResult mirrors httpapi.LiveHandler's own wire body ({"status":"live"})
// exactly — `aw health live` never inspects a dependency (V6-01's own
// "live chỉ chứng minh process/event loop còn phục vụ"), so it always
// succeeds the moment this process can run any code at all.
type LiveResult struct {
	Status string `json:"status"`
}

// Live returns V6-15C's own CLI-side equivalent of a successful GET
// /health/live response: always {"status":"live"}, no dependency check.
func Live() LiveResult { return LiveResult{Status: "live"} }

// CheckFailure mirrors internal/delivery/httpapi's own unexported
// readinessFailure wire shape ({"check":...,"reason":...}) — restated here
// so `aw health ready`'s JSON output matches GET /health/ready's own 503
// body byte-for-byte in shape, without reaching into that package's
// unexported type.
type CheckFailure struct {
	Check  string `json:"check"`
	Reason string `json:"reason"`
}

// ReadyResult mirrors GET /health/ready's own response body for both
// outcomes: {"status":"ready"} on success, or
// {"status":"not_ready","checks":[...]} naming exactly which checks still
// fail — httpapi.ReadyHandler's own two-branch contract, restated as a
// plain value this leaf can encode instead of writing an HTTP response.
type ReadyResult struct {
	Status string         `json:"status"`
	Checks []CheckFailure `json:"checks,omitempty"`
}

// Ready runs every check checker knows about and reports a ReadyResult —
// the CLI-side equivalent of httpapi.ReadyHandler's own body, computed the
// identical way (checker.Evaluate), just never written as an HTTP response.
func Ready(ctx context.Context, checker *httpapi.ReadinessChecker) ReadyResult {
	failures := checker.Evaluate(ctx)
	if len(failures) == 0 {
		return ReadyResult{Status: "ready"}
	}
	checks := make([]CheckFailure, len(failures))
	for i, f := range failures {
		checks[i] = CheckFailure{Check: f.Check, Reason: f.Reason}
	}
	return ReadyResult{Status: "not_ready", Checks: checks}
}

// RunLive implements `aw health live`: parses its own (empty besides
// --json) flag set, runs Live, and writes it to stdout as JSON (--json) or
// a one-line human summary otherwise — V6-15B's own JSON/human dual-output
// contract every leaf command follows (flags.go's own BindJSONFlag doc
// comment). Always returns nil: Live never fails.
func RunLive(arguments []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("health live", flag.ContinueOnError)
	jsonOutput := cli.BindJSONFlag(fs)
	if err := fs.Parse(arguments); err != nil {
		return cli.UsageError{Err: err}
	}
	result := Live()
	if *jsonOutput {
		return cli.EncodeQueryResult(stdout, result)
	}
	fmt.Fprintf(stdout, "status: %s\n", result.Status)
	return nil
}

// RunReady implements `aw health ready`: parses its own (empty besides
// --json) flag set, builds this invocation's own checker via
// NewReadinessChecker, runs Ready, and writes it to stdout as JSON
// (--json) or a human summary otherwise. Unlike GET /health/ready's own
// 503 status code, a CLI invocation has no HTTP status line to carry that
// signal in — so this returns a non-nil (non-UsageError) error whenever
// the report is not ready, after writing the report, so a future
// composition root's own cli.ExitCodeFor maps a genuinely failing
// readiness check to ExitFailure, mirroring GET /health/ready's own
// "binary load-balancer-style gate" semantics (internal/delivery/httpapi/
// doctor/queries.go's own doc comment draws the identical live/ready-vs-
// doctor distinction this leaf's own doctor sibling deliberately does NOT
// follow — see that package's own RunDoctor doc comment).
func RunReady(ctx context.Context, deps Dependencies, arguments []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("health ready", flag.ContinueOnError)
	jsonOutput := cli.BindJSONFlag(fs)
	if err := fs.Parse(arguments); err != nil {
		return cli.UsageError{Err: err}
	}
	checker := NewReadinessChecker(deps)
	result := Ready(ctx, checker)
	if *jsonOutput {
		if err := cli.EncodeQueryResult(stdout, result); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(stdout, "status: %s\n", result.Status)
		for _, c := range result.Checks {
			fmt.Fprintf(stdout, "  - %s: %s\n", c.Check, c.Reason)
		}
	}
	if result.Status != "ready" {
		return fmt.Errorf("health ready: not ready (%d check(s) failing)", len(result.Checks))
	}
	return nil
}
