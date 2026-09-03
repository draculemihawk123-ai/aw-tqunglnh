// Package readiness is V3-07's initialization/readiness-evidence domain
// model (docs/design/05-v3-project-workspace.md V3-07: "writer không chạy
// trước clean known base và baseline check"), grounded in
// docs/harness-engineering/06-lec-06-khoi-tao-la-phase-rieng.md's
// HE-06-M01 ("business implementation MUST không bắt đầu trước khi run
// initialization đạt trạng thái ready"), HE-06-M02 ("project/component
// MUST có setup, start/health và verification recipes xác định được"),
// HE-06-M03 ("runner MUST chạy readiness/baseline checks và lưu output
// thật trước khi cho writer hoạt động"), HE-06-M06 ("task family worktree
// MUST bắt đầu từ revision đã biết và trạng thái repository có thể giải
// thích") and HE-06-S04 ("hệ thống SHOULD lưu readiness profile để phân
// biệt lỗi mới với baseline đã biết"), plus
// docs/harness-engineering/12-lec-12-clean-handoff.md's HE-12-M03
// ("build/test/startup outcome MUST được so với baseline hoặc policy;
// regression không được che bởi lỗi có sẵn").
//
// # Profile: why Repository-level, not Component-level
//
// HE-06-M02 names "project/component" ambiguously. This package grounds
// the choice in what is actually checkable given this codebase's real,
// already-merged schema (docs/design/01-system-design.md §6.1,
// internal/domain/project.Component's own doc comment): a Component
// carries no setup/verification recipe fields today (V3-01 deliberately
// kept it minimal — Component.Kind is "a plain string... no citation...
// specifies a fixed set"), a Repository can be onboarded (RepositoryActive)
// with zero discovered Components at all (V3-02's probe finds candidates,
// it never requires at least one), and this task's own trigger point — a
// RepositoryWorkspace reaching READY (V3-06) — is itself a per-Repository
// concept with no Component dimension whatsoever. Tying the profile to
// Repository is therefore the coarser but always-populated anchor; a later
// task that wants per-Component recipes can add that as a real, additive
// refinement once a real caller needs it (00-roadmap.md §3's "không chia
// chỉ để tạo file/field nếu phần đó chưa có contract test hoặc behavior
// quan sát được" — the same discipline Component.Kind's own doc comment
// already models for this exact package).
//
// # CommandSpec: why argv-only, not the full Definition Plane
//
// internal/domain/command already gives this codebase a full, versioned,
// safety-reviewed "how to describe and run a command" model
// (CommandDocument/CommandVersion, resolved through definition.DependencyPin
// and the real Definition Plane publish/pin machinery from V2). This
// package deliberately does NOT route a readiness recipe through that
// machinery: the Definition Plane exists to let a command be authored,
// reviewed, versioned and referenced by many independent callers across a
// project; a readiness recipe here has exactly one caller (this task's own
// baseline-check handler, internal/app/readinesscheck) and no
// publish/review/pin workflow is cited anywhere in V3-07's own "Thực hiện"
// line. Reusing the Definition Plane's full machinery for a single-caller,
// unreviewed recipe would be unfounded scope this task has no citation to
// justify. What this package DOES reuse is the Definition Plane's own
// safety DISCIPLINE — argv-only, never a free-form shell string — the
// same rule internal/archtest.TestProcessSpecHasNoShellStringField already
// enforces on ports.ProcessSpec and docs/design/01-system-design.md states
// outright ("Command/Gate definition không nhận free-form shell"):
// CommandSpec below has Executable + Argv []string, structurally
// identical in shape to ports.ProcessSpec, and nothing in this package (or
// its caller) ever concatenates argv into one string. This is the minimal,
// defensible reading HE-06-M02 itself supports ("setup, start/health và
// verification recipes xác định được" — nothing about definitions,
// versions or publishing).
package readiness

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// CommandSpec is one canonical, argv-only command a readiness Profile runs
// (see this package's own doc comment for why argv-only, and why not the
// full Definition Plane). WorkingDirectory is relative to the
// RepositoryWorkspace's own real worktree root the caller resolves at
// execution time (this domain package has no filesystem access of its
// own, so it can only validate shape, never existence) — empty means the
// worktree root itself. TimeoutSeconds bounds how long the caller's real
// subprocess execution (ports.ProcessSupervisor.Run, entirely outside this
// package's own scope) may run before being treated as an environment
// failure, never a legitimate RED baseline (internal/app/readinesscheck's
// own doc comment draws that exact line).
type CommandSpec struct {
	Executable       string
	Argv             []string
	WorkingDirectory string
	TimeoutSeconds   uint32
}

// NewCommandSpec validates and constructs a CommandSpec.
func NewCommandSpec(executable string, argv []string, workingDirectory string, timeoutSeconds uint32) (CommandSpec, error) {
	executable = strings.TrimSpace(executable)
	if executable == "" {
		return CommandSpec{}, errors.New("readiness: command executable is required")
	}
	if strings.IndexByte(executable, 0) >= 0 {
		return CommandSpec{}, errors.New("readiness: command executable cannot contain NUL")
	}
	if timeoutSeconds == 0 {
		return CommandSpec{}, errors.New("readiness: command timeout must be greater than zero")
	}
	normalizedArgv := append([]string(nil), argv...)
	for _, argument := range normalizedArgv {
		if strings.IndexByte(argument, 0) >= 0 {
			return CommandSpec{}, errors.New("readiness: command argv cannot contain NUL")
		}
	}
	normalizedDirectory, err := normalizeRelativeDirectory(workingDirectory)
	if err != nil {
		return CommandSpec{}, err
	}
	return CommandSpec{
		Executable: executable, Argv: normalizedArgv,
		WorkingDirectory: normalizedDirectory, TimeoutSeconds: timeoutSeconds,
	}, nil
}

// normalizeRelativeDirectory applies this codebase's established
// relative-path validation rules (see internal/domain/project's own
// normalizeComponentPath and internal/domain/work's own
// normalizePathScopes, reproduced here rather than imported — a domain
// package must never import another domain package's unexported helper,
// and each domain package in this codebase already keeps its own small
// copy rather than inventing a shared import cycle to avoid it, per
// project.Component's own doc comment): backslash normalized to forward
// slash, no leading "/" or Windows drive letter, no ".." segment, cleaned
// via path.Clean. An empty input is valid here (unlike a Component path)
// — it means "the workspace root itself", a legitimate, common case for a
// readiness recipe.
func normalizeRelativeDirectory(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	normalized := strings.ReplaceAll(raw, "\\", "/")
	if strings.HasPrefix(normalized, "/") || (len(normalized) >= 2 && normalized[1] == ':') {
		return "", fmt.Errorf("readiness: working directory %q must be relative", raw)
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return "", fmt.Errorf("readiness: working directory %q cannot contain parent traversal", raw)
		}
	}
	normalized = path.Clean(normalized)
	if normalized == "." {
		return "", nil
	}
	return normalized, nil
}

// Profile is this task's own "readiness profile" (HE-06-M02, HE-06-S04):
// the canonical setup/verification recipe for one Repository — see this
// package's own doc comment for why Repository-level. Setup is optional
// (nil means the repository needs no explicit setup step before
// verification — e.g. already vendored dependencies); Verification is
// required — it IS the baseline check HE-06-M03 requires evidence for.
type Profile struct {
	RepositoryID project.RepositoryID
	Setup        *CommandSpec
	Verification CommandSpec
	Version      uint64
}

// NewProfile validates and constructs a Profile at generation 1.
func NewProfile(repositoryID project.RepositoryID, setup *CommandSpec, verification CommandSpec) (Profile, error) {
	if repositoryID == "" {
		return Profile{}, errors.New("readiness: profile repository id is required")
	}
	if strings.TrimSpace(verification.Executable) == "" {
		return Profile{}, errors.New("readiness: profile verification command is required")
	}
	return Profile{RepositoryID: repositoryID, Setup: setup, Verification: verification, Version: 1}, nil
}

// Stage names which one of a Profile's two commands a BaselineAttempt's
// own outcome was determined from.
type Stage string

const (
	StageSetup        Stage = "SETUP"
	StageVerification Stage = "VERIFICATION"
)

// BaselineOutcome is the three-way, mutually exclusive split this task's
// own "Verify: baseline green/red/command-error fixtures" line requires a
// caller reading back evidence to be able to tell apart (see this task's
// own final report for why three, never two): GREEN and RED both mean the
// command genuinely ran to completion under
// ports.ProcessSupervisor.Run — the only difference is its exit code — so
// neither is ever an environment fault; ENVIRONMENT_ERROR means the
// command could not even be observed to run at all (a missing executable,
// an unreadable working directory, a timeout/cancellation — anything
// ports.ProcessSupervisor.Run itself reports as a genuine execution
// failure, never a business/quality signal about the repository's own
// code). This is HE-12-M03's "regression không được che bởi lỗi có sẵn"
// and this task's own "Hoàn thành khi: baseline debt được pin, không giả
// PASS" made concrete: RED is always recorded as RED, never silently
// reported GREEN, and ENVIRONMENT_ERROR is never conflated with either.
type BaselineOutcome string

const (
	// BaselineGreen: the command ran to completion with exit code 0.
	BaselineGreen BaselineOutcome = "GREEN"
	// BaselineRed: the command ran to completion with a non-zero exit
	// code — a real, pinned baseline debt, not a fake PASS and not an
	// environment fault.
	BaselineRed BaselineOutcome = "RED"
	// BaselineEnvironmentError: the command could not even be observed to
	// run — see this type's own doc comment.
	BaselineEnvironmentError BaselineOutcome = "ENVIRONMENT_ERROR"
)

// Blocking reports whether outcome should open a typed environment
// blocker (internal/app/ports.EnvironmentBlocker): only
// BaselineEnvironmentError does — GREEN and RED both mean the check
// genuinely ran (one is good news, one is pinned debt), neither is an
// environment fault a caller needs to intervene on before a baseline
// verdict can even be reached.
func (o BaselineOutcome) Blocking() bool {
	return o == BaselineEnvironmentError
}

// BlockerStatus is a typed environment blocker's own lifecycle — see
// internal/app/ports.EnvironmentBlocker's own doc comment for the full
// shape and why this task builds a narrow, Profile/RepositoryWorkspace-
// scoped blocker rather than the shared cross-cutting `blockers` table
// docs/design/01-system-design.md §6.1 sketches.
type BlockerStatus string

const (
	BlockerOpen     BlockerStatus = "OPEN"
	BlockerResolved BlockerStatus = "RESOLVED"
)

// EnvironmentBlockerType is the one, fixed reason this task's own narrow
// blocker type is ever opened for — never a general, open-ended
// classification (contrast with the shared `blockers` table's own `type`
// column, which the design sketch lists several runtime/V4-era values
// for; this task's own scope only ever produces this one).
const EnvironmentBlockerType = "BASELINE_ENVIRONMENT_ERROR"
