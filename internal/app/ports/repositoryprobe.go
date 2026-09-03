package ports

import "context"

// RepositoryProbeEvidence is the read-only evidence V3-02's onboarding
// probe gathers about a Repository's own registered locator
// (docs/design/05-v3-project-workspace.md V3-02, HE-06-M03's "runner MUST
// chạy readiness/baseline checks và lưu output thật trước khi cho writer
// hoạt động"): a resolved, symlink-safe canonical path, the commit
// DefaultRef currently resolves to, whether the working tree is clean or
// dirty, and a set of Component discovery candidates
// (HE-06-M05's "Repository -> Component -> EngineeringPack" topology).
type RepositoryProbeEvidence struct {
	CanonicalPath string
	BaseCommit    string
	Dirty         bool
	Components    []ProbedComponent
}

// ProbedComponent is one Component candidate RepositoryProber's discovery
// heuristic found under a Repository's canonical root.
type ProbedComponent struct {
	Name string
	Path string
	Kind string
}

// RepositoryProber is implemented by internal/adapters/repoprobe: a
// read-only, durable Git inspection of one Repository's own registered
// local_path — never a worktree, branch or workspace of any kind (that is
// V3-06's own, separate concern). Probe never mutates the target
// repository in any way (no fetch, no checkout, no write) — every
// operation it performs is read-only, matching HE-06-M03's "runner ...
// readiness/baseline checks" being observation, not setup.
//
// On success it returns a populated RepositoryProbeEvidence and a nil
// error. On failure it returns a zero RepositoryProbeEvidence and an
// *internal/app/apperror.Error whose Code distinguishes an environment
// failure — apperror.CodeUnavailable, Retryable true: the local_path is
// temporarily unreachable, the git executable itself could not be run, a
// filesystem/symlink-resolution I/O error — from a validation/business
// failure — apperror.CodeInvalidArgument or apperror.CodeNotFound,
// Retryable false: local_path exists but is not a Git working tree (or is
// not its own Git top-level directory), or DefaultRef does not resolve to
// a commit in this repository. This is V3-02's own "Hoàn thành khi:
// environment failure khác validation/business failure" made concrete —
// a caller branches on apperror.CodeOf(err)/apperror.IsRetryable(err),
// never on a parsed error string.
type RepositoryProber interface {
	Probe(ctx context.Context, localPath string, defaultRef string) (RepositoryProbeEvidence, error)
}
