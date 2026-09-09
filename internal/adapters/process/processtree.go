package process

import (
	"errors"
	"os/exec"
)

// errNoProcess guards against signaling/binding a Cmd that never started —
// should be unreachable from Run's own call sequence (bind/signal/kill are
// only ever invoked after a successful Start), kept only as a defensive
// fail-closed return rather than a nil-pointer panic.
var errNoProcess = errors.New("process: cmd has no running process")

// processTree owns whatever OS-level grouping a spawned child needs so its
// full descendant tree can be terminated together — a process group on
// Unix, a Job Object on Windows (V5-05, ADR-023's own "cancel descendants"
// verify bar). Neither mechanism blocks filesystem or network access by
// itself; IsolationChecker never treats either as ENFORCED_ISOLATED.
type processTree interface {
	// configure mutates cmd before Start so the OS groups this child (and
	// anything it spawns) for later termination. Never fails: it only sets
	// fields on cmd: no syscall happens yet.
	configure(cmd *exec.Cmd)
	// bind captures whatever a platform needs once the process is actually
	// running (Windows: assigning it to a Job Object; a no-op on Unix,
	// where configure's Setpgid already did all the work at fork time). A
	// failure here degrades this one run to direct single-process
	// signaling rather than failing the run outright — process-tree kill
	// is a best-effort robustness property, not the isolation guarantee
	// (that is IsolationChecker's job, checked before Run is ever called).
	bind(cmd *exec.Cmd) error
	// signalGraceful asks the whole tree to exit without forcing it
	// (SIGTERM on Unix, CTRL_BREAK on Windows). Errors are non-fatal: the
	// grace timer still runs, and kill still fires if the tree ignores it.
	signalGraceful(cmd *exec.Cmd) error
	// kill forcibly terminates every process bind could reach.
	kill(cmd *exec.Cmd) error
	// quiesced reports whether every process this tree ever grouped
	// together (the direct child and any descendants it spawned) has
	// genuinely exited — never "probably," and never inferred from the
	// direct child's own Wait() alone (V5-08B's own normal-exit
	// quiescence postcondition: a parent that exits while a descendant
	// keeps running/writing must never be reported as a quiesced tree, on
	// EVERY exit path, not only cancellation/timeout). A non-nil error, or
	// (false, nil), both mean "not confirmed empty" — Run's own caller
	// polls this rather than trusting a single call. Returns (false,
	// non-nil) when bind never succeeded (degraded single-process mode):
	// there is no OS handle left to ask, so quiescence beyond the direct
	// child is unconfirmable, fail-closed.
	quiesced(cmd *exec.Cmd) (bool, error)
	// close releases any OS handle this tree holds. Safe to call exactly
	// once, always, even if bind was never called or failed.
	close()
}
