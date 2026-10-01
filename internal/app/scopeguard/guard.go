package scopeguard

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

var ErrScopeViolation = errors.New("workspace diff exceeds effective write scope")

type Violation struct {
	RepositoryID project.RepositoryID
	Path         string
	Reason       string
}

func (v Violation) Error() string {
	return fmt.Sprintf("%s:%s: %s", v.RepositoryID, v.Path, v.Reason)
}

// ViolationsError is the typed form of ErrScopeViolation (V9-09): it carries
// the individual Violations behind the "workspace diff exceeds effective
// write scope" failure, so a caller can report WHICH paths breached the
// scope (bounded, on an operator-visible surface) without parsing the
// message text. errors.Is(err, ErrScopeViolation) holds for it, and Error()
// is byte-for-byte the message ValidateDiffs has always produced, so every
// existing errors.Is branch and message check is unaffected. Every producer
// of a scope violation — ValidateDiffs here, a strict read-only check in the
// runtime — should build one with NewViolationsError so the list reaches the
// operator no matter which check fired.
type ViolationsError struct {
	// Violations is sorted by (RepositoryID, Path) and never empty.
	Violations []Violation
	// Summary, when non-empty, is what Error() reports after the
	// ErrScopeViolation prefix instead of the per-violation list — for a
	// producer (the strict read-only check) that already renders its own
	// bounded message but still wants to hand the full, typed path list on.
	Summary string
}

// NewViolationsError sorts violations by (RepositoryID, Path) and wraps them
// in a ViolationsError. It returns nil for an empty list.
func NewViolationsError(violations []Violation) error {
	if len(violations) == 0 {
		return nil
	}
	sorted := append([]Violation(nil), violations...)
	sort.Slice(sorted, func(left, right int) bool {
		if sorted[left].RepositoryID != sorted[right].RepositoryID {
			return sorted[left].RepositoryID < sorted[right].RepositoryID
		}
		return sorted[left].Path < sorted[right].Path
	})
	return &ViolationsError{Violations: sorted}
}

// NewViolationsErrorWithSummary is NewViolationsError for a producer that
// supplies its own already-bounded message (see ViolationsError.Summary).
func NewViolationsErrorWithSummary(violations []Violation, summary string) error {
	err := NewViolationsError(violations)
	if typed, ok := err.(*ViolationsError); ok {
		typed.Summary = summary
	}
	return err
}

func (e *ViolationsError) Error() string {
	if e.Summary != "" {
		return fmt.Sprintf("%s: %s", ErrScopeViolation, e.Summary)
	}
	parts := make([]string, 0, len(e.Violations))
	for _, violation := range e.Violations {
		parts = append(parts, violation.Error())
	}
	return fmt.Sprintf("%s: %s", ErrScopeViolation, strings.Join(parts, "; "))
}

// Unwrap makes errors.Is(err, ErrScopeViolation) true.
func (e *ViolationsError) Unwrap() error { return ErrScopeViolation }

// ValidateDiffs is a post-execution guard. OS mounts may enforce access where
// available, but a diff must still be checked before a worker result can be
// accepted: a read-only or out-of-path change is never a successful outcome.
func ValidateDiffs(scopes []work.RepositoryScope, diffs []ports.WorkspaceDiff) error {
	writeScopes := make(map[project.RepositoryID][][]string)
	for _, scope := range scopes {
		if scope.Access() != work.RepositoryWrite {
			continue
		}
		writeScopes[scope.RepositoryID()] = append(writeScopes[scope.RepositoryID()], scope.PathScopes())
	}

	violations := make([]Violation, 0)
	for _, diff := range diffs {
		for _, file := range diff.Files {
			for _, candidate := range changedPaths(file) {
				if !isAllowed(candidate, writeScopes[diff.RepositoryID]) {
					violations = append(violations, Violation{
						RepositoryID: diff.RepositoryID,
						Path:         candidate,
						Reason:       "path is not covered by a WRITE repository scope",
					})
				}
			}
		}
	}
	return NewViolationsError(violations)
}

func changedPaths(file ports.FileStatus) []string {
	paths := []string{file.Path}
	if file.OriginalPath != "" && file.OriginalPath != file.Path {
		paths = append(paths, file.OriginalPath)
	}
	return paths
}

func isAllowed(candidate string, grants [][]string) bool {
	normalized, ok := normalizePath(candidate)
	if !ok {
		return false
	}
	for _, paths := range grants {
		if len(paths) == 0 {
			return true
		}
		for _, grant := range paths {
			if normalized == grant || strings.HasPrefix(normalized, grant+"/") {
				return true
			}
		}
	}
	return false
}

func normalizePath(value string) (string, bool) {
	value = strings.ReplaceAll(value, "\\", "/")
	if value == "" || strings.HasPrefix(value, "/") || (len(value) >= 2 && value[1] == ':') {
		return "", false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return "", false
		}
	}
	value = path.Clean(value)
	return value, value != "." && value != ""
}
