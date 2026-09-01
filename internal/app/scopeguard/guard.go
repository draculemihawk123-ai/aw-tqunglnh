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
	if len(violations) == 0 {
		return nil
	}
	sort.Slice(violations, func(left, right int) bool {
		if violations[left].RepositoryID != violations[right].RepositoryID {
			return violations[left].RepositoryID < violations[right].RepositoryID
		}
		return violations[left].Path < violations[right].Path
	})
	parts := make([]string, 0, len(violations))
	for _, violation := range violations {
		parts = append(parts, violation.Error())
	}
	return fmt.Errorf("%w: %s", ErrScopeViolation, strings.Join(parts, "; "))
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
