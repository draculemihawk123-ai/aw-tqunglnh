package definitions_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
)

// V9-10 (gap G10) — the knowledge-hygiene warnings a publish reports: a stale
// resource, and too many HARD_CONSTRAINTs for one agent. Advisory only.

var warnNow = time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)

func skillResource(key string, priority definition.PriorityClass, lastVerified *time.Time) skill.Resource {
	return skill.Resource{
		Key: key, Instruction: "follow " + key, Priority: priority, Global: true,
		Provenance: skill.Provenance{Owner: "team-x", Source: "doc-1", LastVerified: lastVerified, Revision: "r1"},
	}
}

// publishSkill publishes doc as skill version versionID and returns the fields.
func publishSkill(t *testing.T, uow *fake.UnitOfWork, definitionID, versionID string, doc skill.SkillDocument) definition.VersionFields {
	t.Helper()
	ctx := context.Background()
	createTestDefinition(t, ctx, uow, definitionID, definition.KindSkill, definition.GlobalScope())
	published, err := definitions.PublishDefinitionVersion(ctx, uow, testCommand("pub-"+versionID, "hash-"+versionID, ports.InstallationScope(), "PublishDefinitionVersion"),
		definitions.PublishDefinitionVersionRequest{
			DefinitionID: definitionID, Kind: definition.KindSkill,
			Compile: func() (definition.VersionFields, error) {
				return skill.Compile(
					skill.SkillDefinition{ID: skill.SkillDefinitionID(definitionID), Fields: definition.Fields{
						Kind: definition.KindSkill, Scope: definition.GlobalScope(), Name: "skill", Status: definition.StatusDraft, Version: 1,
					}},
					skill.PublishRequest{VersionID: skill.SkillVersionID(versionID), VersionNumber: 1, SchemaVersion: 1, Document: doc, PublishedBy: "operator-1", PublishedAt: warnNow},
				)
			},
		})
	if err != nil {
		t.Fatalf("publish skill %s: %v", versionID, err)
	}
	return published
}

func warningsOf(t *testing.T, uow *fake.UnitOfWork, published definition.VersionFields, warn definitions.WarnPolicy) []string {
	t.Helper()
	warn.Now = warnNow
	warnings, err := definitions.PublishWarnings(context.Background(), uow, published, warn)
	if err != nil {
		t.Fatalf("PublishWarnings: %v", err)
	}
	return warnings
}

func TestPublishWarnings_StaleResource(t *testing.T) {
	uow := fake.New()
	old := warnNow.AddDate(0, 0, -400)
	recent := warnNow.AddDate(0, 0, -10)
	published := publishSkill(t, uow, "skill-def", "skill-v1", skill.SkillDocument{Resources: []skill.Resource{
		skillResource("stale-rule", definition.PriorityGuidance, &old),
		skillResource("fresh-rule", definition.PriorityGuidance, &recent),
		{Key: "revision-only", Instruction: "x", Priority: definition.PriorityGuidance, Global: true, Provenance: skill.Provenance{Owner: "t", Source: "s", Revision: "r1"}},
	}})

	warnings := warningsOf(t, uow, published, definitions.WarnPolicy{})
	if len(warnings) != 1 || !strings.Contains(warnings[0], `resource "stale-rule" was last verified 400 days ago`) ||
		!strings.Contains(warnings[0], "older than the 180 days") {
		t.Fatalf("warnings = %q, want exactly the stale resource, measured against the default of 180 days", warnings)
	}
	// The ceiling is configurable, and a negative value turns the warning off.
	if got := warningsOf(t, uow, published, definitions.WarnPolicy{MaxResourceAge: 500 * 24 * time.Hour}); len(got) != 0 {
		t.Fatalf("with a 500 day ceiling: %q, want none", got)
	}
	if got := warningsOf(t, uow, published, definitions.WarnPolicy{MaxResourceAge: 5 * 24 * time.Hour}); len(got) != 2 {
		t.Fatalf("with a 5 day ceiling: %q, want both dated resources", got)
	}
	if got := warningsOf(t, uow, published, definitions.WarnPolicy{MaxResourceAge: -1}); len(got) != 0 {
		t.Fatalf("with the age warning off: %q, want none", got)
	}
}

func TestPublishWarnings_TooManyHardConstraintsInOneSkill(t *testing.T) {
	uow := fake.New()
	var resources []skill.Resource
	for _, key := range []string{"a", "b", "c", "d"} {
		resources = append(resources, skillResource("hard-"+key, definition.PriorityHardConstraint, nil))
	}
	resources = append(resources, skillResource("guidance", definition.PriorityGuidance, nil))
	published := publishSkill(t, uow, "skill-def", "skill-v1", skill.SkillDocument{Resources: resources})

	if got := warningsOf(t, uow, published, definitions.WarnPolicy{}); len(got) != 0 {
		t.Fatalf("4 hard constraints against the default ceiling of 15: %q, want none", got)
	}
	got := warningsOf(t, uow, published, definitions.WarnPolicy{MaxHardConstraints: 3})
	if len(got) != 1 || !strings.Contains(got[0], "holds 4 HARD_CONSTRAINT resources, more than the 3") {
		t.Fatalf("4 hard constraints against a ceiling of 3: %q, want one warning", got)
	}
}

func TestPublishWarnings_ContextPolicyCountsTheHardConstraintsItDeliversToAnAgent(t *testing.T) {
	uow := fake.New()
	ctx := context.Background()
	// Two skills, each within the ceiling on its own; together the route delivers 4.
	first := skill.SkillDocument{Resources: []skill.Resource{
		skillResource("h1", definition.PriorityHardConstraint, nil), skillResource("h2", definition.PriorityHardConstraint, nil),
	}}
	second := skill.SkillDocument{Resources: []skill.Resource{
		skillResource("h3", definition.PriorityHardConstraint, nil), skillResource("h4", definition.PriorityHardConstraint, nil),
		skillResource("g1", definition.PriorityGuidance, nil),
	}}
	publishSkill(t, uow, "skill-def-1", "skill-v1", first)
	publishSkill(t, uow, "skill-def-2", "skill-v2", second)

	refs := []policy.ResourceRef{
		{OwnerVersionID: "skill-v1", ResourceKey: "h1", ContentHash: "sha256:a"},
		{OwnerVersionID: "skill-v1", ResourceKey: "h2", ContentHash: "sha256:b"},
		{OwnerVersionID: "skill-v2", ResourceKey: "h3", ContentHash: "sha256:c"},
		{OwnerVersionID: "skill-v2", ResourceKey: "h4", ContentHash: "sha256:d"},
		{OwnerVersionID: "skill-v2", ResourceKey: "g1", ContentHash: "sha256:e"},
	}
	createTestDefinition(t, ctx, uow, "policy-def", definition.KindPolicy, definition.GlobalScope())
	published, err := definitions.PublishDefinitionVersion(ctx, uow, testCommand("pub-policy", "hash-policy", ports.InstallationScope(), "PublishDefinitionVersion"),
		definitions.PublishDefinitionVersionRequest{
			DefinitionID: "policy-def", Kind: definition.KindPolicy,
			Compile: func() (definition.VersionFields, error) {
				return policy.Compile(
					policy.PolicyDefinition{ID: "policy-def", Fields: definition.Fields{Kind: definition.KindPolicy, Scope: definition.GlobalScope(), Name: "route", Status: definition.StatusDraft, Version: 1}},
					policy.PublishRequest{
						VersionID: "policy-v1", VersionNumber: 1, SchemaVersion: 1, PublishedBy: "operator-1", PublishedAt: warnNow,
						Document: policy.PolicyDocument{Category: policy.CategoryContext, Context: &policy.ContextRules{
							Selector: []string{"*"}, Budget: policy.ContextBudget{MaxTokens: 4096}, ResourceRefs: refs,
						}},
					},
				)
			},
		})
	if err != nil {
		t.Fatalf("publish context policy: %v", err)
	}

	if got := warningsOf(t, uow, published, definitions.WarnPolicy{}); len(got) != 0 {
		t.Fatalf("against the default ceiling: %q, want none", got)
	}
	got := warningsOf(t, uow, published, definitions.WarnPolicy{MaxHardConstraints: 3})
	if len(got) != 1 || !strings.Contains(got[0], "delivers 4 HARD_CONSTRAINT resources to an agent, more than the 3") {
		t.Fatalf("a route of 4 hard constraints against a ceiling of 3: %q, want one warning", got)
	}
}

func TestPublishWarnings_OtherKindsHaveNothingToSay(t *testing.T) {
	uow := fake.New()
	ctx := context.Background()
	createTestDefinition(t, ctx, uow, "policy-def", definition.KindPolicy, definition.GlobalScope())
	published, err := definitions.PublishDefinitionVersion(ctx, uow, testCommand("pub", "hash", ports.InstallationScope(), "PublishDefinitionVersion"),
		definitions.PublishDefinitionVersionRequest{DefinitionID: "policy-def", Kind: definition.KindPolicy, Compile: policyPublishCompile("policy-def", "policy-v1", nil)})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got := warningsOf(t, uow, published, definitions.WarnPolicy{MaxHardConstraints: 1, MaxResourceAge: time.Hour}); len(got) != 0 {
		t.Fatalf("a permission policy: %q, want none", got)
	}
}
