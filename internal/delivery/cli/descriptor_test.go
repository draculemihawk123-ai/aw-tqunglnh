package cli_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func TestRegistryRegisterTableDriven(t *testing.T) {
	valid := cli.Descriptor{
		Path: []string{"definition", "create"}, Scope: cli.ScopeInstallation,
		AppOperation: "CreateDefinition", HTTPOperationID: "createDefinition",
	}

	tests := []struct {
		name    string
		seed    []cli.Descriptor
		add     cli.Descriptor
		wantErr string
	}{
		{name: "valid registers cleanly", add: valid},
		{
			name:    "empty path rejected",
			add:     cli.Descriptor{Scope: cli.ScopeInstallation, AppOperation: "X", HTTPOperationID: "x"},
			wantErr: "path must not be empty",
		},
		{
			name:    "blank path segment rejected",
			add:     cli.Descriptor{Path: []string{"definition", " "}, Scope: cli.ScopeInstallation, AppOperation: "X", HTTPOperationID: "x"},
			wantErr: "must not be blank",
		},
		{
			name:    "invalid scope rejected",
			add:     cli.Descriptor{Path: []string{"x"}, Scope: "BOGUS", AppOperation: "X", HTTPOperationID: "x"},
			wantErr: "invalid scope",
		},
		{
			name:    "missing app operation rejected",
			add:     cli.Descriptor{Path: []string{"x"}, Scope: cli.ScopeInstallation, HTTPOperationID: "x"},
			wantErr: "must set AppOperation",
		},
		{
			name:    "missing http operation id rejected",
			add:     cli.Descriptor{Path: []string{"x"}, Scope: cli.ScopeInstallation, AppOperation: "X"},
			wantErr: "must set HTTPOperationID",
		},
		{
			name: "cli-local sentinel accepted as a valid http operation id",
			add:  cli.Descriptor{Path: []string{"events", "watch"}, Scope: cli.ScopeInstallation, AppOperation: "WatchEvents", HTTPOperationID: cli.CLILocalOperation},
		},
		{
			name:    "duplicate path+scope rejected",
			seed:    []cli.Descriptor{valid},
			add:     valid,
			wantErr: "already registered",
		},
		{
			name: "same path different scope allowed (ADR-028 invocation shape = path + scope)",
			seed: []cli.Descriptor{valid},
			add: cli.Descriptor{
				Path: valid.Path, Scope: cli.ScopeProject,
				AppOperation: "CreateDefinition", HTTPOperationID: "createProjectDefinition",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := cli.NewRegistry()
			for _, d := range tc.seed {
				if err := reg.Register(d); err != nil {
					t.Fatalf("seed Register() error = %v", err)
				}
			}
			err := reg.Register(tc.add)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Register() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Register() error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestRegistryAllIsDeterministicallyOrdered(t *testing.T) {
	reg := cli.NewRegistry()
	b := cli.Descriptor{Path: []string{"work-item", "show"}, Scope: cli.ScopeProject, AppOperation: "GetWorkItem", HTTPOperationID: "getWorkItem"}
	a := cli.Descriptor{Path: []string{"definition", "create"}, Scope: cli.ScopeInstallation, AppOperation: "CreateDefinition", HTTPOperationID: "createDefinition"}
	if err := reg.Register(b); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(a); err != nil {
		t.Fatal(err)
	}

	all := reg.All()
	if len(all) != 2 {
		t.Fatalf("All() len = %d, want 2", len(all))
	}
	if all[0].AppOperation != "CreateDefinition" || all[1].AppOperation != "GetWorkItem" {
		t.Fatalf("All() not deterministically ordered: %+v", all)
	}

	// A second call must produce the identical order — never map
	// iteration order leaking through.
	again := reg.All()
	for i := range again {
		if again[i].AppOperation != all[i].AppOperation {
			t.Fatalf("All() order changed between calls: %+v vs %+v", all, again)
		}
	}
}

func TestRegistryMustRegisterPanicsOnDuplicate(t *testing.T) {
	reg := cli.NewRegistry()
	d := cli.Descriptor{Path: []string{"doctor"}, Scope: cli.ScopeInstallation, AppOperation: "Doctor", HTTPOperationID: cli.CLILocalOperation}
	reg.MustRegister(d)

	defer func() {
		if recover() == nil {
			t.Fatal("MustRegister did not panic on duplicate registration")
		}
	}()
	reg.MustRegister(d)
}

func TestRegistryMustRegisterPanicsOnMissingMetadata(t *testing.T) {
	reg := cli.NewRegistry()
	defer func() {
		if recover() == nil {
			t.Fatal("MustRegister did not panic on missing metadata")
		}
	}()
	reg.MustRegister(cli.Descriptor{Path: []string{"broken"}, Scope: cli.ScopeInstallation})
}

func TestPackageLevelRegisterUsesDefaultRegistry(t *testing.T) {
	// cli.Default is shared, global, process-lifetime state (documented on
	// cli.Default itself) — this is the one test in this file allowed to
	// touch it, to prove the package-level Register/All forwarding
	// actually works. The path includes a nanosecond timestamp so repeated
	// executions of this same test function within one process (e.g. `go
	// test -count=N`) never collide with an earlier run's own leftover
	// registration.
	path := fmt.Sprintf("__test-package-level-register-%d__", time.Now().UnixNano())
	d := cli.Descriptor{Path: []string{path}, Scope: cli.ScopeInstallation, AppOperation: "TestOp", HTTPOperationID: cli.CLILocalOperation}
	if err := cli.Register(d); err != nil {
		t.Fatalf("cli.Register() error = %v", err)
	}
	found := false
	for _, got := range cli.All() {
		if len(got.Path) > 0 && got.Path[0] == d.Path[0] {
			found = true
		}
	}
	if !found {
		t.Fatal("cli.All() did not include the descriptor registered via cli.Register")
	}
}
