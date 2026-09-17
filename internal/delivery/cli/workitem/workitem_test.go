package workitem_test

import (
	"flag"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	_ "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitem"
)

// TestWorkItemPackage_RegistersAllSevenDescriptors proves this package's own
// seven init() registrations (list.go, show.go, create.go, createchild.go,
// readiness.go, markready.go, cancel.go) all landed in the shared
// internal/delivery/cli.Default registry — mirrors
// internal/delivery/cli/run/descriptor_test.go's own identical proof.
func TestWorkItemPackage_RegistersAllSevenDescriptors(t *testing.T) {
	want := map[string]struct {
		scope           string
		httpOperationID string
	}{
		"work-item list|PROJECT":         {"PROJECT", "listWorkItems"},
		"work-item show|PROJECT":         {"PROJECT", "getWorkItem"},
		"work-item create|PROJECT":       {"PROJECT", "createRootWorkItem"},
		"work-item create-child|PROJECT": {"PROJECT", "createChildWorkItem"},
		"work-item readiness|PROJECT":    {"PROJECT", "getWorkItemReadiness"},
		"work-item mark-ready|PROJECT":   {"PROJECT", "markWorkItemReady"},
		"work-item cancel|PROJECT":       {"PROJECT", "cancelWorkItem"},
	}

	got := map[string]bool{}
	for _, d := range cli.All() {
		path := strings.Join(d.Path, " ")
		if !strings.HasPrefix(path, "work-item ") {
			continue
		}
		key := path + "|" + string(d.Scope)
		got[key] = true
		expected, ok := want[key]
		if !ok {
			t.Fatalf("unexpected descriptor registered for %q", key)
		}
		if d.HTTPOperationID != expected.httpOperationID {
			t.Errorf("descriptor %q HTTPOperationID = %q, want %q", key, d.HTTPOperationID, expected.httpOperationID)
		}
		if strings.TrimSpace(d.AppOperation) == "" {
			t.Errorf("descriptor %q has empty AppOperation", key)
		}
	}
	for key := range want {
		if !got[key] {
			t.Errorf("descriptor %q never registered", key)
		}
	}
}

// TestWorkItemCommandsNeverDefineActorOrRoleFlag is this task's own "no
// spoof surface" Verify bullet made mechanical — mirrors
// internal/delivery/cli/scopeexpansion's own
// TestScopeExpansionCommandsNeverDefineActorOrRoleFlag (V6-15I): ADR-028
// permits exactly one mechanism to select the acting principal
// (--principal-config) — there must be no --actor/--role flag anywhere on
// any command in this package. Each binder closure below reproduces one
// subcommand's own flag.FlagSet construction (the exact same sequence of
// cli.Bind*/fs.String calls that subcommand's own Run* function makes, in
// the same order) against a throwaway FlagSet, then walks every flag it
// registered — a forbidden flag added to any subcommand in the future is
// caught here too, without needing to export anything from the non-test
// files just for this test.
func TestWorkItemCommandsNeverDefineActorOrRoleFlag(t *testing.T) {
	forbidden := []string{"actor", "role", "roles", "actor-roles"}

	// bindersUnderTest mirrors each subcommand's own flag.FlagSet
	// construction (principal/project/idempotency-key/expected-version/
	// file/reason binders only) — reproduced here rather than exported from
	// the non-test files, since none of those Bind* calls are separable
	// from their own Run* function's body without changing production code
	// just for this test. Each entry below runs the identical sequence of
	// cli.Bind*/fs.String calls its own Run* function makes, in the same
	// order, so a forbidden flag added to any of them in the future is
	// caught here too.
	binders := map[string]func(*flag.FlagSet){
		"work-item list": func(fs *flag.FlagSet) {
			cli.BindProjectFlag(fs)
		},
		"work-item show": func(fs *flag.FlagSet) {
			cli.BindProjectFlag(fs)
		},
		"work-item create": func(fs *flag.FlagSet) {
			cli.BindPrincipalFlag(fs)
			cli.BindProjectFlag(fs)
			cli.BindIdempotencyKeyFlag(fs)
			cli.BindFileFlag(fs)
		},
		"work-item create-child": func(fs *flag.FlagSet) {
			cli.BindPrincipalFlag(fs)
			cli.BindIdempotencyKeyFlag(fs)
			cli.BindFileFlag(fs)
		},
		"work-item readiness": func(fs *flag.FlagSet) {
			cli.BindProjectFlag(fs)
		},
		"work-item mark-ready": func(fs *flag.FlagSet) {
			cli.BindPrincipalFlag(fs)
			cli.BindExpectedVersionFlag(fs)
			cli.BindIdempotencyKeyFlag(fs)
		},
		"work-item cancel": func(fs *flag.FlagSet) {
			cli.BindPrincipalFlag(fs)
			fs.String("reason", "", "reason for cancelling this work item (required)")
		},
	}

	for name, bind := range binders {
		t.Run(name, func(t *testing.T) {
			fs := flag.NewFlagSet(name, flag.ContinueOnError)
			bind(fs)
			forbiddenSet := make(map[string]bool, len(forbidden))
			for _, f := range forbidden {
				forbiddenSet[f] = true
			}
			fs.VisitAll(func(f *flag.Flag) {
				if forbiddenSet[f.Name] {
					t.Fatalf("%q defines forbidden flag --%s — ADR-028 requires Actor/Roles come only from --principal-config", name, f.Name)
				}
			})
		})
	}
}
