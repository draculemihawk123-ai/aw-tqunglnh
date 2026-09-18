package workitemblocker_test

import (
	"flag"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	_ "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitemblocker"
)

// TestWorkItemBlockerPackage_RegistersOneDescriptor proves this package's
// own one init() registration (workitemblocker.go) landed in the shared
// internal/delivery/cli.Default registry.
func TestWorkItemBlockerPackage_RegistersOneDescriptor(t *testing.T) {
	var found *cli.Descriptor
	for _, d := range cli.All() {
		if len(d.Path) == 2 && d.Path[0] == "blocker" && d.Path[1] == "resolve" {
			d := d
			found = &d
		}
	}
	if found == nil {
		t.Fatal("descriptor \"blocker resolve\" not found in cli.Default")
	}
	if found.Scope != cli.ScopeProject {
		t.Errorf("descriptor Scope = %q, want PROJECT", found.Scope)
	}
	if found.HTTPOperationID != "resolveWorkItemBlocker" {
		t.Errorf("descriptor HTTPOperationID = %q, want resolveWorkItemBlocker", found.HTTPOperationID)
	}
	if found.AppOperation != "ResolveWorkItemBlocker" {
		t.Errorf("descriptor AppOperation = %q, want ResolveWorkItemBlocker", found.AppOperation)
	}
}

// TestBlockerCommandsNeverDefineActorOrRoleFlag mirrors
// internal/delivery/cli/workitem's own
// TestWorkItemCommandsNeverDefineActorOrRoleFlag — ADR-028 permits exactly
// one mechanism to select the acting principal (--principal-config).
func TestBlockerCommandsNeverDefineActorOrRoleFlag(t *testing.T) {
	forbidden := map[string]bool{"actor": true, "role": true, "roles": true, "actor-roles": true}

	fs := flag.NewFlagSet("blocker resolve", flag.ContinueOnError)
	cli.BindPrincipalFlag(fs)
	fs.String("mode", "", "resolution mode: RESOLVED or WAIVED (required, no default)")
	fs.String("reason", "", "reason for this resolution (required)")
	fs.String("policy-grant-ref", "", "policy grant reference authorizing a WAIVED resolution (required when --mode=WAIVED)")

	fs.VisitAll(func(f *flag.Flag) {
		if forbidden[f.Name] {
			t.Fatalf("\"blocker resolve\" defines forbidden flag --%s — ADR-028 requires Actor/Roles come only from --principal-config", f.Name)
		}
	})
}
