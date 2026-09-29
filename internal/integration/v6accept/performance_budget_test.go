// V8-07 — Performance budgets and large-state checks
// (docs/design/10-v8-alpha-hardening.md V8-07): "đo ... startup ... ghi
// hardware/profile rồi freeze numeric threshold". This is the one V8-07
// measurement that genuinely needs this package's own real-process stack
// (an operator's actual "how long until I can use this" experience) —
// every other V8-07 measurement in this session lives closer to its own
// subsystem (internal/delivery/httpapi/kanban, internal/app/workerpool)
// where a lighter, non-OS-process fixture already existed.
//
// Scope note: this file measures ONLY the cold-start latency named
// explicitly in V8-07's own Thực hiện line. "Task detail", "Kanban" and
// "scheduler latency" are measured in their own owning packages; "UI large
// graph/diff" and "projection rebuild" are explicitly out of scope for
// THIS session — see baocaov8checklist.md's own V8-07 section for why
// (in short: projection rebuild has no real worker built yet — V6-09A —
// so there is nothing to benchmark, and a real interactive-browser
// graph/diff render is deferred to a spawned follow-up task rather than a
// new, heavier Playwright perf suite under this task's own time budget).
package v6accept

import (
	"testing"
	"time"
)

// TestV8PerformanceBudget_ColdStartLatency measures real wall-clock time
// from spawning `aw serve` through it announcing readiness (waitReady)
// PLUS `aw worker` announcing its own workerId — the real, observable
// "installation is usable" moment an operator would experience, using
// already-compiled binaries (builtBinaries' own sync.Once is warmed up
// first, deliberately outside the timed section, so this measurement never
// includes one-time `go build` cost).
//
// Frozen threshold: < 20s. Owner: V8-07 task (this session, 2026-09-29).
// Hardware/profile: measured locally at ~2-3s on a contended Windows 10
// dev machine (this session's own environment); this run's CI has
// separately proven capable of unusually slow/contended windows-latest
// runners this same session (see agent-kit-ci-known-flakes memory's own
// "PR #131" entry — real deadline-based waits elsewhere in this repo
// timed out under that load). 20s is a large, deliberate multiple over the
// local baseline specifically to survive that same class of contention
// without becoming a new flake source, not a tuned performance target —
// V8-07's own design doc explicitly warns against treating an arbitrary
// number as an SLO. Revisit before the final Alpha release candidate is
// measured, per this task's own "không đặt ngưỡng sau khi xem RC" line.
func TestV8PerformanceBudget_ColdStartLatency(t *testing.T) {
	requireAcceptance(t)
	builtBinaries(t) // warm the sync.Once build cache OUTSIDE the timed section.

	s := newStack(t)
	start := time.Now()
	s.start(t)
	elapsed := time.Since(start)

	t.Logf("V8-07 benchmark report: cold-start latency (aw serve ready + aw worker announced) = %v", elapsed)

	const maxColdStart = 20 * time.Second
	if elapsed > maxColdStart {
		t.Errorf("cold-start latency = %v, want < %v (frozen V8-07 threshold)", elapsed, maxColdStart)
	}
}
