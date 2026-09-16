package projection

import (
	"sort"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/artifactsweep"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/releasesetcommit"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/safesettings"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacereconcile"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacerelease"
)

// buildRealRegisteredKeys calls every real production RegisterEventSchemas
// into one combined eventschema.Registry — the exact same 11 call sites
// internal/archtest/event_catalog_test.go's own
// TestEmittedDomainEventInventoryMatchesRegisteredInventory uses (a real
// functional call, never a re-parse of those files, so this can never
// drift from what registration actually does). agentevents' own separate
// agent_events journal keys are excluded for the identical reason that
// test's own doc comment gives: a different journal, never written to
// domain_events, never in scope for this projection either.
func buildRealRegisteredKeys(t *testing.T) map[eventschema.EventKey]bool {
	t.Helper()
	registry := eventschema.NewRegistry()
	sqlite.RegisterEventSchemas(registry)
	adapterbuild.RegisterEventSchemas(registry)
	catalog.RegisterEventSchemas(registry)
	definitions.RegisterEventSchemas(registry)
	message.RegisterEventSchemas(registry)
	work.RegisterEventSchemas(registry)
	releasesetcommit.RegisterEventSchemas(registry)
	workspacerelease.RegisterEventSchemas(registry)
	workspacereconcile.RegisterEventSchemas(registry)
	runtime.RegisterEventSchemas(registry)
	artifactsweep.RegisterEventSchemas(registry)
	safesettings.RegisterEventSchemas(registry)

	registered := map[eventschema.EventKey]bool{}
	for _, key := range registry.Keys() {
		registered[key] = true
	}
	if len(registered) == 0 {
		t.Fatal("buildRealRegisteredKeys found zero registered keys — this test needs updating alongside the implementation")
	}
	return registered
}

// TestCatalog_ClassifiesEveryRegisteredEventKey is V6-08's own CI inventory
// guard — the reducer-classification counterpart to V6-00A's own
// TestEmittedDomainEventInventoryMatchesRegisteredInventory
// (internal/archtest/event_catalog_test.go). Unlike that test's own
// deliberately asymmetric registered-vs-emitted comparison (a registered-
// but-unemitted key is expected, for a retired historical emission site),
// this Catalog is built FROM the currently registered set, so both
// directions are real bugs: a registered key with no Classification here
// is the "no implicit default" gap V6-08's own Thực hiện bullet forbids; a
// Classification for a key that is NOT currently registered is a stale or
// mistyped entry that can never actually be reached.
func TestCatalog_ClassifiesEveryRegisteredEventKey(t *testing.T) {
	registered := buildRealRegisteredKeys(t)
	c := NewCatalog()
	classified := map[eventschema.EventKey]bool{}
	for _, key := range c.Keys() {
		classified[key] = true
	}

	var missing []string
	for key := range registered {
		if !classified[key] {
			missing = append(missing, key.EventType)
		}
	}
	sort.Strings(missing)
	for _, eventType := range missing {
		t.Errorf("%s: registered but has no Classification in this Catalog", eventType)
	}

	var stale []string
	for key := range classified {
		if !registered[key] {
			stale = append(stale, key.EventType)
		}
	}
	sort.Strings(stale)
	for _, eventType := range stale {
		t.Errorf("%s: has a Classification but is not currently registered (stale or mistyped entry)", eventType)
	}

	t.Logf("projection catalog: %d registered key(s), %d classified key(s)", len(registered), len(classified))
}

// TestCatalog_EveryApplyEntryHasAReducerAndEveryIgnoreHasAReason guards
// against a Classification built with the wrong Outcome-dependent fields
// populated — e.g. an Apply entry with a nil Reducer (would panic at
// apply-time, never at registration time, the worst possible place to
// discover it) or an Ignore entry with an empty Reason (the "always
// explain WHY" discipline catalog_registrations.go's own doc comment
// requires).
func TestCatalog_EveryApplyEntryHasAReducerAndEveryIgnoreHasAReason(t *testing.T) {
	c := NewCatalog()
	for _, key := range c.Keys() {
		classification, ok := c.Classify(key)
		if !ok {
			t.Fatalf("%s v%d: Classify returned ok=false for a key Keys() itself returned", key.EventType, key.SchemaVersion)
		}
		switch classification.Outcome {
		case Apply:
			if classification.Reducer == nil {
				t.Errorf("%s v%d: Outcome=Apply but Reducer is nil", key.EventType, key.SchemaVersion)
			}
			if classification.Reason != "" {
				t.Errorf("%s v%d: Outcome=Apply but Reason is set (%q) — Reason is Ignore-only", key.EventType, key.SchemaVersion, classification.Reason)
			}
		case Ignore:
			if classification.Reason == "" {
				t.Errorf("%s v%d: Outcome=Ignore but Reason is empty", key.EventType, key.SchemaVersion)
			}
			if classification.Reducer != nil {
				t.Errorf("%s v%d: Outcome=Ignore but Reducer is non-nil", key.EventType, key.SchemaVersion)
			}
		default:
			t.Errorf("%s v%d: unknown Outcome %d", key.EventType, key.SchemaVersion, classification.Outcome)
		}
	}
}

// TestCatalog_ApplyEntriesWithNilEntityKeyOfDocumentAResolutionNote guards
// the EntityKeyOf-can't-resolve-directly case (WORKFLOW_RUN_FINALIZED):
// every such entry must carry a non-empty EntityKeyNote explaining the
// caller's own required resolution strategy, never a silent nil with no
// explanation.
func TestCatalog_ApplyEntriesWithNilEntityKeyOfDocumentAResolutionNote(t *testing.T) {
	c := NewCatalog()
	for _, key := range c.Keys() {
		classification, _ := c.Classify(key)
		if classification.Outcome != Apply {
			continue
		}
		if classification.EntityKeyOf == nil && classification.EntityKeyNote == "" {
			t.Errorf("%s v%d: EntityKeyOf is nil but EntityKeyNote is empty", key.EventType, key.SchemaVersion)
		}
		if classification.EntityKeyNote != "" && classification.FallbackMatch == nil {
			t.Errorf("%s v%d: EntityKeyNote documents a fallback strategy but FallbackMatch is nil — a live consumer would have no way to actually resolve this event's own EntityKey when EntityKeyOf returns ok=false", key.EventType, key.SchemaVersion)
		}
	}
}
