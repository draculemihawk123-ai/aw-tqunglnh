package catalog

import (
	"context"
	"errors"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// V11-12: what a client shows about a repository's last failed probe, and which lifecycle actions the repository allows, derived from the repository
// and its probe history. Shared by the HTTP and CLI faces so both say the same thing.

// ProbeErrorView is a BLOCKED repository's reason, in words: what is wrong, what kind of problem it is, what to do. Detail (Git's redacted
// stderr) is filled only when the caller asks for it.
type ProbeErrorView struct {
	FailureKind string `json:"failureKind,omitempty"`
	Message     string `json:"message"`
	Hint        string `json:"hint,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

// LastProbeError returns the reason of the latest probe that ended BLOCKED, only while the repository is BLOCKED. Nil otherwise.
func LastProbeError(repo project.Repository, attempts []ports.RepositoryProbeAttempt, withDetail bool) *ProbeErrorView {
	if repo.Status != project.RepositoryBlocked {
		return nil
	}
	for i := len(attempts) - 1; i >= 0; i-- {
		a := attempts[i]
		if a.Result == nil || *a.Result != project.RepositoryBlocked {
			continue
		}
		view := &ProbeErrorView{Message: "The probe failed."}
		if a.ErrorMessage != nil && *a.ErrorMessage != "" {
			view.Message = *a.ErrorMessage
		} else if a.ErrorCode != nil {
			view.Message = "The probe failed with code " + *a.ErrorCode + "."
		}
		if a.FailureKind != nil {
			view.FailureKind = *a.FailureKind
		}
		if a.ErrorHint != nil {
			view.Hint = *a.ErrorHint
		}
		if withDetail && a.ErrorDetail != nil {
			view.Detail = *a.ErrorDetail
		}
		return view
	}
	return nil
}

// RepositoryActions says which lifecycle commands the repository currently allows. Advisory: each command re-checks against the live row.
type RepositoryActions struct {
	RetryProbe bool
	Update     bool
	Disable    bool
	Delete     bool
}

// ActionsFor derives RepositoryActions. Delete needs the history because a repository that was ever ACTIVE can only be disabled.
func ActionsFor(repo project.Repository, attempts []ports.RepositoryProbeAttempt) RepositoryActions {
	var actions RepositoryActions
	switch repo.Status {
	case project.RepositoryBlocked:
		actions.RetryProbe, actions.Update, actions.Disable = true, true, true
	case project.RepositoryActive:
		actions.Disable = true
	}
	switch repo.Status {
	case project.RepositoryRegistering, project.RepositoryProbing, project.RepositoryBlocked:
		actions.Delete = true
		for _, a := range attempts {
			if a.Result != nil && *a.Result == project.RepositoryActive {
				actions.Delete = false
			}
		}
	}
	return actions
}

// CheckedComponent is a component the probe would discover.
type CheckedComponent struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// CheckRepositoryResult is what a pre-registration check returns: either OK with what the probe found, or the failure in the same four parts a
// registered repository's last probe error has (FailureKind, Message, Detail, Hint) plus the path as given and as resolved.
type CheckRepositoryResult struct {
	OK            bool               `json:"ok"`
	FailureKind   string             `json:"failureKind,omitempty"`
	Message       string             `json:"message,omitempty"`
	Path          string             `json:"path,omitempty"`
	ResolvedPath  string             `json:"resolvedPath,omitempty"`
	Detail        string             `json:"detail,omitempty"`
	Hint          string             `json:"hint,omitempty"`
	CanonicalPath string             `json:"canonicalPath,omitempty"`
	BaseCommit    string             `json:"baseCommit,omitempty"`
	Dirty         bool               `json:"dirty,omitempty"`
	Components    []CheckedComponent `json:"components,omitempty"`
}

// CheckRepository runs the same probe a registration would, writing nothing. A problem with the repository is a result (OK false); only a failure of
// the check itself (no prober, an unclassified error) is an error. An empty ref checks HEAD.
func CheckRepository(ctx context.Context, prober ports.RepositoryProber, path, ref string) (CheckRepositoryResult, error) {
	if prober == nil {
		return CheckRepositoryResult{}, errors.New("catalog: no repository prober is configured")
	}
	if strings.TrimSpace(ref) == "" {
		ref = "HEAD"
	}
	evidence, err := prober.Probe(ctx, path, ref)
	if err == nil {
		result := CheckRepositoryResult{OK: true, CanonicalPath: evidence.CanonicalPath, BaseCommit: evidence.BaseCommit, Dirty: evidence.Dirty}
		for _, c := range evidence.Components {
			result.Components = append(result.Components, CheckedComponent{Name: c.Name, Path: c.Path, Kind: c.Kind})
		}
		return result, nil
	}
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Details[ports.ProbeDetailFailureKind] == "" {
		return CheckRepositoryResult{}, err
	}
	return CheckRepositoryResult{
		FailureKind: appErr.Details[ports.ProbeDetailFailureKind], Message: appErr.Message, Path: appErr.Details[ports.ProbeDetailPath],
		ResolvedPath: appErr.Details[ports.ProbeDetailResolvedPath], Detail: appErr.Details[ports.ProbeDetailDetail], Hint: appErr.Details[ports.ProbeDetailHint],
	}, nil
}
