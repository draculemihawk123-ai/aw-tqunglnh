package decision

import (
	"flag"
	"testing"
)

// TestDecisionCommandsNeverDefineActorOrRoleFlag is this task's own
// "Spoof" Verify bullet made mechanical: an explicit test scanning every
// flag this package's own two commands actually bind (via their own
// bind*Flags helpers — approval.go/wait.go), mirroring
// internal/delivery/cli's own TestBindPrincipalFlagNeverDefinesActorOrRoleFlag
// (flags_test.go there). ADR-028 permits exactly one mechanism to select
// the acting principal (--principal-config) — there must be no
// --actor/--role flag anywhere on any command in this package.
func TestDecisionCommandsNeverDefineActorOrRoleFlag(t *testing.T) {
	forbidden := map[string]bool{"actor": true, "role": true, "roles": true, "actor-roles": true}

	binders := map[string]func(*flag.FlagSet){
		"approval resolve": func(fs *flag.FlagSet) { bindResolveApprovalFlags(fs) },
		"wait signal":      func(fs *flag.FlagSet) { bindSignalWaitFlags(fs) },
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

// TestExpectedVersionFlag_OnlyOnApprovalResolve proves this package's own
// explicit asymmetry (approval.go's/wait.go's own doc comments, mirroring
// internal/delivery/httpapi/decision's own package doc comment): `approval
// resolve` binds --expected-version, `wait signal` deliberately does not.
func TestExpectedVersionFlag_OnlyOnApprovalResolve(t *testing.T) {
	hasExpectedVersion := func(bind func(*flag.FlagSet)) bool {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		bind(fs)
		found := false
		fs.VisitAll(func(f *flag.Flag) {
			if f.Name == "expected-version" {
				found = true
			}
		})
		return found
	}

	if !hasExpectedVersion(func(fs *flag.FlagSet) { bindResolveApprovalFlags(fs) }) {
		t.Fatal("approval resolve does not bind --expected-version, want it required (CLI equivalent of HTTP's If-Match)")
	}
	if hasExpectedVersion(func(fs *flag.FlagSet) { bindSignalWaitFlags(fs) }) {
		t.Fatal("wait signal unexpectedly binds --expected-version — see this package's own doc comment for why it must not")
	}
}
