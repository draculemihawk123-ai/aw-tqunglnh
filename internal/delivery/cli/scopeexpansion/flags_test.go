package scopeexpansion

import (
	"flag"
	"testing"
)

// TestScopeExpansionCommandsNeverDefineActorOrRoleFlag is this task's own
// "Spoof" Verify bullet made mechanical: an explicit test scanning every
// flag this package's own four commands actually bind (via their own
// bind*Flags helpers — request.go/approve.go/reject.go/withdraw.go),
// mirroring internal/delivery/cli's own
// TestBindPrincipalFlagNeverDefinesActorOrRoleFlag (flags_test.go there).
// ADR-028 permits exactly one mechanism to select the acting principal
// (--principal-config) — there must be no --actor/--role flag anywhere on
// any command in this package.
func TestScopeExpansionCommandsNeverDefineActorOrRoleFlag(t *testing.T) {
	forbidden := map[string]bool{"actor": true, "role": true, "roles": true, "actor-roles": true}

	binders := map[string]func(*flag.FlagSet){
		"scope-expansion request":  func(fs *flag.FlagSet) { bindRequestFlags(fs) },
		"scope-expansion approve":  func(fs *flag.FlagSet) { bindApproveFlags(fs) },
		"scope-expansion reject":   func(fs *flag.FlagSet) { bindRejectFlags(fs) },
		"scope-expansion withdraw": func(fs *flag.FlagSet) { bindWithdrawFlags(fs) },
	}

	for name, bind := range binders {
		t.Run(name, func(t *testing.T) {
			fs := flag.NewFlagSet(name, flag.ContinueOnError)
			bind(fs)
			fs.VisitAll(func(f *flag.Flag) {
				if forbidden[f.Name] {
					t.Fatalf("%q defines forbidden flag --%s — ADR-028 requires Actor/Roles come only from --principal-config", name, f.Name)
				}
			})
		})
	}
}
