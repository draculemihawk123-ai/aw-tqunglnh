package cli_test

import (
	"flag"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

type boundFlags struct {
	principal, project, idKey *string
	version                   *uint64
	yes, jsonOut, wait        *bool
	waitTimeout               *time.Duration
}

func bindAll(fs *flag.FlagSet) boundFlags {
	var f boundFlags
	f.principal = cli.BindPrincipalFlag(fs)
	f.project = cli.BindProjectFlag(fs)
	f.idKey = cli.BindIdempotencyKeyFlag(fs)
	f.version = cli.BindExpectedVersionFlag(fs)
	f.yes = cli.BindYesFlag(fs)
	f.jsonOut = cli.BindJSONFlag(fs)
	f.wait, f.waitTimeout = cli.BindWaitFlags(fs)
	return f
}

func TestBindSharedFlagsParsingTableDriven(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		checkFn func(t *testing.T, f boundFlags)
	}{
		{
			name: "defaults when nothing passed",
			args: nil,
			checkFn: func(t *testing.T, f boundFlags) {
				if *f.principal != "" || *f.project != "" || *f.idKey != "" {
					t.Fatalf("expected empty string defaults, got principal=%q project=%q idKey=%q", *f.principal, *f.project, *f.idKey)
				}
				if *f.version != 0 || *f.yes || *f.jsonOut || *f.wait || *f.waitTimeout != 0 {
					t.Fatalf("expected zero-value defaults, got version=%d yes=%v json=%v wait=%v waitTimeout=%v",
						*f.version, *f.yes, *f.jsonOut, *f.wait, *f.waitTimeout)
				}
			},
		},
		{
			name: "every flag parses to its given value",
			args: []string{
				"--principal-config=/tmp/p.json", "--project-id=proj-1", "--idempotency-key=key-1",
				"--expected-version=7", "--yes", "--json", "--wait", "--wait-timeout=5s",
			},
			checkFn: func(t *testing.T, f boundFlags) {
				if *f.principal != "/tmp/p.json" || *f.project != "proj-1" || *f.idKey != "key-1" {
					t.Fatalf("string flags = %q/%q/%q", *f.principal, *f.project, *f.idKey)
				}
				if *f.version != 7 || !*f.yes || !*f.jsonOut || !*f.wait || *f.waitTimeout != 5*time.Second {
					t.Fatalf("version=%d yes=%v json=%v wait=%v waitTimeout=%v", *f.version, *f.yes, *f.jsonOut, *f.wait, *f.waitTimeout)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			f := bindAll(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			tc.checkFn(t, f)
		})
	}
}

func TestBindFileFlagParses(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	file := cli.BindFileFlag(fs)
	if err := fs.Parse([]string{"--file=/tmp/body.json"}); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if *file != "/tmp/body.json" {
		t.Fatalf("file = %q, want /tmp/body.json", *file)
	}
}

func TestBindOutputFlagParses(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "default empty", args: nil, want: ""},
		{name: "dash for stdout", args: []string{"--output=-"}, want: "-"},
		{name: "real file path", args: []string{"--output=/tmp/artifact.bin"}, want: "/tmp/artifact.bin"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			output := cli.BindOutputFlag(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if *output != tc.want {
				t.Fatalf("output = %q, want %q", *output, tc.want)
			}
		})
	}
}

// TestBindPrincipalFlagNeverDefinesActorOrRoleFlag is the "spoof actor
// absent" rule at the flag.FlagSet level: ADR-028's own hard rule (no
// per-command --actor/--role flag, ever) proven for every binder this
// package defines, not just by code review.
func TestBindPrincipalFlagNeverDefinesActorOrRoleFlag(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	bindAll(fs)
	cli.BindFileFlag(fs)
	cli.BindOutputFlag(fs)

	forbidden := map[string]bool{"actor": true, "role": true, "roles": true, "actor-roles": true}
	fs.VisitAll(func(f *flag.Flag) {
		if forbidden[f.Name] {
			t.Fatalf("shared flag framework defines forbidden flag --%s — ADR-028 requires Actor/Roles come only from --principal-config", f.Name)
		}
	})
}
