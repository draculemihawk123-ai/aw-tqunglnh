package catalog

// V12-25 (docs/design/15-v12-ui-operations.md, UX-13, ADR-058): the project's commit message template — the default message of an
// approval-bound commit, written in the repository's convention ("feat: {title}", "{taskId}: {title}"). A commit made by aw had the
// work item title with a " (lần N)" suffix as its whole message; the template drops the suffix and lets the subject follow the
// repository. The release marker is a trailer at the end of the body (releasesetcommit.commitMessageWithMarker), not in the template.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// MaxCommitTemplateBytes bounds a template.
const MaxCommitTemplateBytes = 2048

// DefaultCommitTemplate is used when the project has none.
const DefaultCommitTemplate = "{title}"

// ErrInvalidCommitTemplate: the template is too long, has no subject line, or names an unknown placeholder.
var ErrInvalidCommitTemplate = errors.New("catalog: the commit message template must have a subject line, at most 2048 bytes, and only the placeholders {title}, {taskId} and {workItemId}")

var placeholder = regexp.MustCompile(`\{[A-Za-z]+\}`)

var knownPlaceholders = map[string]bool{"{title}": true, "{taskId}": true, "{workItemId}": true}

// redoSuffix is the " (lần N)" a redo appends to a work item title (runtime.RecreateWorkItem).
var redoSuffix = regexp.MustCompile(`\s*\(lần \d+\)\s*$`)

// taskIDPrefix is a "T-01: " style task id at the start of a title (tasks.json, split-tasks.sh).
var taskIDPrefix = regexp.MustCompile(`^([A-Z][A-Z0-9]*-\d+[a-z]?):\s+`)

// ValidateCommitTemplate checks a template's shape.
func ValidateCommitTemplate(template string) error {
	t := strings.TrimSpace(template)
	if t == "" || len(t) > MaxCommitTemplateBytes || !utf8.ValidString(t) || strings.ContainsAny(t, "\x00\r") {
		return ErrInvalidCommitTemplate
	}
	if subject, _, _ := strings.Cut(t, "\n"); strings.TrimSpace(subject) == "" {
		return ErrInvalidCommitTemplate
	}
	for _, p := range placeholder.FindAllString(t, -1) {
		if !knownPlaceholders[p] {
			return ErrInvalidCommitTemplate
		}
	}
	return nil
}

// RenderCommitTemplate fills a template for a work item: {title} is the title without the redo suffix and without a leading task id
// when the template places {taskId} itself; {taskId} is that id ("T-01") or empty; {workItemId} the id.
func RenderCommitTemplate(template, title, workItemID string) string {
	if strings.TrimSpace(template) == "" {
		template = DefaultCommitTemplate
	}
	title = strings.TrimSpace(redoSuffix.ReplaceAllString(title, ""))
	taskID := ""
	if m := taskIDPrefix.FindStringSubmatch(title); m != nil {
		taskID = m[1]
		if strings.Contains(template, "{taskId}") {
			title = strings.TrimSpace(title[len(m[0]):])
		}
	}
	out := strings.NewReplacer("{title}", title, "{taskId}", taskID, "{workItemId}", workItemID).Replace(template)
	return strings.TrimSpace(out)
}

// CommitTemplateView is the project's template (Template empty and Version 0: none set, the default applies).
type CommitTemplateView struct {
	ProjectID string `json:"projectId"`
	Template  string `json:"template"`
	Default   string `json:"default"`
	Version   uint64 `json:"version,omitempty"`
}

// GetCommitTemplate reads the project's template.
func GetCommitTemplate(ctx context.Context, uow ports.UnitOfWork, projectID string) (CommitTemplateView, error) {
	view := CommitTemplateView{ProjectID: projectID, Default: DefaultCommitTemplate}
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().GetProject(ctx, projectID); err != nil {
			return err
		}
		s, err := tx.ProjectSettings().GetProjectSetting(ctx, projectID, ports.ProjectSettingCommitMessageTemplate)
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			return nil
		}
		view.Template, view.Version = s.Value, s.Version
		return err
	})
	return view, err
}

// SetCommitTemplate sets (non-empty) or removes (empty) the project's template, idempotently by cmd's receipt.
func SetCommitTemplate(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, projectID, template string) (CommitTemplateView, error) {
	template = strings.TrimSpace(strings.ReplaceAll(template, "\r\n", "\n"))
	if template != "" {
		if err := ValidateCommitTemplate(template); err != nil {
			return CommitTemplateView{}, err
		}
	}
	var result CommitTemplateView
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if replayed, err := replay(ctx, tx, cmd, &result); err != nil || replayed {
			return err
		}
		current, err := loadProject(ctx, tx, cmd.Scope, projectID)
		if err != nil {
			return err
		}
		if current.Status == project.ProjectArchived {
			return apperror.ProjectArchived(projectID)
		}
		existing, err := tx.ProjectSettings().GetProjectSetting(ctx, projectID, ports.ProjectSettingCommitMessageTemplate)
		var expected uint64
		switch {
		case err == nil:
			expected = existing.Version
		case !errors.Is(err, ports.ErrPersistenceNotFound):
			return err
		}
		result = CommitTemplateView{ProjectID: projectID, Default: DefaultCommitTemplate}
		if template == "" {
			if err := tx.ProjectSettings().DeleteProjectSetting(ctx, projectID, ports.ProjectSettingCommitMessageTemplate); err != nil {
				return err
			}
		} else {
			stored, err := tx.ProjectSettings().PutProjectSetting(ctx, ports.ProjectSetting{
				ProjectID: projectID, Key: ports.ProjectSettingCommitMessageTemplate, Value: template, UpdatedBy: cmd.Actor, UpdatedAt: cmd.RequestedAt,
			}, expected)
			if err != nil {
				return fmt.Errorf("store the commit message template: %w", err)
			}
			result.Template, result.Version = stored.Value, stored.Version
		}
		return recordReceipt(ctx, tx, cmd, result)
	})
	return result, err
}
