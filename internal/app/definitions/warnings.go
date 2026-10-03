package definitions

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/layer"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
)

// V9-10 (gap G10, docs/design/12-v9-harness-alignment.md): knowledge hygiene
// warnings at publish. Both are advisory — they never block a publish, and the
// published version is immutable whatever they say — because "knowledge decay"
// (lecture 03) is something to tell the author about, not something a machine
// can decide:
//
//   - a resource whose provenance `lastVerified` is older than the configured
//     age ("lastVerified bắt buộc khai nhưng chỉ được chép vào bản ghi audit",
//     G10: nothing used to say a resource had gone stale);
//   - too many HARD_CONSTRAINT resources for one agent to hold at once: in one
//     Skill/Layer version, and across the resources one CONTEXT policy (the
//     route an agent profile pins) delivers to an agent. Lecture 04's default
//     is a teaching number, not a verified limit, so the ceiling is a setting.

const (
	// DefaultMaxResourceAge is how old a resource's `lastVerified` may be before
	// publishing a version that carries it draws a warning.
	DefaultMaxResourceAge = 180 * 24 * time.Hour
	// DefaultMaxHardConstraints is how many HARD_CONSTRAINT resources one
	// Skill/Layer version, or one CONTEXT policy's route, may hold before
	// publishing it draws a warning.
	DefaultMaxHardConstraints = 15
)

// WarnPolicy configures PublishWarnings. A zero field takes its default; a
// negative MaxResourceAge or MaxHardConstraints turns that warning off.
type WarnPolicy struct {
	// Now is the clock the age is measured against; zero means time.Now().
	Now                time.Time
	MaxResourceAge     time.Duration
	MaxHardConstraints int
}

func (p WarnPolicy) resolved() WarnPolicy {
	if p.Now.IsZero() {
		p.Now = time.Now().UTC()
	}
	if p.MaxResourceAge == 0 {
		p.MaxResourceAge = DefaultMaxResourceAge
	}
	if p.MaxHardConstraints == 0 {
		p.MaxHardConstraints = DefaultMaxHardConstraints
	}
	return p
}

// PublishWarnings returns the knowledge-hygiene warnings for a version that was
// just published, in a stable order (sorted text). It reads only: the version's
// own compiled document and, for a CONTEXT policy, the owner versions of the
// resources it pins. An empty result means nothing to say.
func PublishWarnings(ctx context.Context, uow ports.UnitOfWork, published definition.VersionFields, warn WarnPolicy) ([]string, error) {
	warn = warn.resolved()
	var warnings []string
	switch published.Kind() {
	case definition.KindSkill:
		var wrapper struct {
			Document skill.SkillDocument `json:"document"`
		}
		if err := json.Unmarshal([]byte(published.CompiledSnapshot()), &wrapper); err != nil {
			return nil, fmt.Errorf("definitions: decode compiled skill %s: %w", published.ID(), err)
		}
		views := make([]resourceView, len(wrapper.Document.Resources))
		for i, r := range wrapper.Document.Resources {
			views[i] = resourceView{key: r.Key, priority: r.Priority, lastVerified: r.Provenance.LastVerified}
		}
		warnings = documentWarnings("skill", published.ID(), views, warn)
	case definition.KindLayer:
		var wrapper struct {
			Document layer.LayerDocument `json:"document"`
		}
		if err := json.Unmarshal([]byte(published.CompiledSnapshot()), &wrapper); err != nil {
			return nil, fmt.Errorf("definitions: decode compiled layer %s: %w", published.ID(), err)
		}
		views := make([]resourceView, len(wrapper.Document.Resources))
		for i, r := range wrapper.Document.Resources {
			views[i] = resourceView{key: r.Key, priority: r.Priority, lastVerified: r.Provenance.LastVerified}
		}
		warnings = documentWarnings("layer", published.ID(), views, warn)
	case definition.KindPolicy:
		var wrapper struct {
			Document policy.PolicyDocument `json:"document"`
		}
		if err := json.Unmarshal([]byte(published.CompiledSnapshot()), &wrapper); err != nil {
			return nil, fmt.Errorf("definitions: decode compiled policy %s: %w", published.ID(), err)
		}
		if wrapper.Document.Category != policy.CategoryContext || wrapper.Document.Context == nil || warn.MaxHardConstraints < 0 {
			return nil, nil
		}
		count, err := countPinnedHardConstraints(ctx, uow, wrapper.Document.Context.ResourceRefs)
		if err != nil {
			return nil, err
		}
		if count > warn.MaxHardConstraints {
			warnings = append(warnings, fmt.Sprintf(
				"context policy version %s delivers %d HARD_CONSTRAINT resources to an agent, more than the %d the warning allows: an agent holds few hard rules reliably, so keep the ones that must never be broken as HARD_CONSTRAINT and make the rest REQUIRED_PROCEDURE or GUIDANCE",
				published.ID(), count, warn.MaxHardConstraints))
		}
	}
	sort.Strings(warnings)
	return warnings, nil
}

type resourceView struct {
	key          string
	priority     definition.PriorityClass
	lastVerified *time.Time
}

func documentWarnings(kind, versionID string, resources []resourceView, warn WarnPolicy) []string {
	var warnings []string
	hard := 0
	for _, r := range resources {
		if r.priority == definition.PriorityHardConstraint {
			hard++
		}
		if warn.MaxResourceAge > 0 && r.lastVerified != nil && warn.Now.Sub(*r.lastVerified) > warn.MaxResourceAge {
			days := int(warn.Now.Sub(*r.lastVerified).Hours() / 24)
			warnings = append(warnings, fmt.Sprintf(
				"%s version %s: resource %q was last verified %d days ago (%s), older than the %d days the warning allows: confirm it is still accurate and publish it again with a new lastVerified",
				kind, versionID, r.key, days, r.lastVerified.UTC().Format("2006-01-02"), int(warn.MaxResourceAge.Hours()/24)))
		}
	}
	if warn.MaxHardConstraints > 0 && hard > warn.MaxHardConstraints {
		warnings = append(warnings, fmt.Sprintf(
			"%s version %s holds %d HARD_CONSTRAINT resources, more than the %d the warning allows: an agent holds few hard rules reliably, so keep the ones that must never be broken as HARD_CONSTRAINT and make the rest REQUIRED_PROCEDURE or GUIDANCE",
			kind, versionID, hard, warn.MaxHardConstraints))
	}
	return warnings
}

// countPinnedHardConstraints counts the pinned resources whose priority, read
// from the owner version, is HARD_CONSTRAINT. A pin whose owner version is
// missing is not this function's business (the publish validated it); it simply
// does not count.
func countPinnedHardConstraints(ctx context.Context, uow ports.UnitOfWork, refs []policy.ResourceRef) (int, error) {
	count := 0
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		for _, ref := range refs {
			owner, err := tx.Definitions().LoadVersion(ctx, ref.OwnerVersionID)
			if err != nil {
				continue
			}
			var priority definition.PriorityClass
			switch owner.Kind() {
			case definition.KindSkill:
				var wrapper struct {
					Document skill.SkillDocument `json:"document"`
				}
				if err := json.Unmarshal([]byte(owner.CompiledSnapshot()), &wrapper); err != nil {
					return fmt.Errorf("definitions: decode compiled skill %s: %w", ref.OwnerVersionID, err)
				}
				for _, r := range wrapper.Document.Resources {
					if r.Key == ref.ResourceKey {
						priority = r.Priority
					}
				}
			case definition.KindLayer:
				var wrapper struct {
					Document layer.LayerDocument `json:"document"`
				}
				if err := json.Unmarshal([]byte(owner.CompiledSnapshot()), &wrapper); err != nil {
					return fmt.Errorf("definitions: decode compiled layer %s: %w", ref.OwnerVersionID, err)
				}
				for _, r := range wrapper.Document.Resources {
					if r.Key == ref.ResourceKey {
						priority = r.Priority
					}
				}
			}
			if priority == definition.PriorityHardConstraint {
				count++
			}
		}
		return nil
	})
	return count, err
}
