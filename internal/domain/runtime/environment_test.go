package runtime

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// V9-05 (gap G5): the AGENT process's inherited environment, as a pinned part
// of ResolvedExecutionProfileV1, and the one rule that forms it.

// The three hashes below were computed on origin/master (b01ca55), BEFORE
// ResolvedExecutionProfileV1 had AgentInheritedEnvironment, for the
// fixtures of executionprofile_test.go. A profile whose set is empty must
// keep producing exactly these — that is what keeps the ExecutionProfileHash
// of every NodeRun scheduled before V9-05 (and of every COMMAND and
// MACHINE_GATE node) unchanged.
const (
	preV905AgentProfileHash   = "sha256:779e408d1285b760dd38c37c2be7406b37ba5663479e89a4ece7200872157d56"
	preV905CommandProfileHash = "sha256:295b71ce6e3d32037eed3edd706c3092ca83bd9ce53680515ff918d77f67a1f3"
	preV905GateProfileHash    = "sha256:9a06ffb6cb0d333c3dbdb825a6189e6a1a48547d3ce364f1c0ecb8780a02ed5e"
)

func TestNewResolvedExecutionProfileV1_EmptyAgentInheritedEnvironment_HashesExactlyAsBeforeV905(t *testing.T) {
	for name, test := range map[string]struct {
		profile ResolvedExecutionProfileV1
		want    string
	}{
		"agent":        {validAgentProfile(), preV905AgentProfileHash},
		"command":      {validCommandProfile(), preV905CommandProfileHash},
		"machine_gate": {validMachineGateProfile(), preV905GateProfileHash},
	} {
		for form, list := range map[string][]string{"nil": nil, "empty": {}} {
			t.Run(name+"/"+form, func(t *testing.T) {
				profile := test.profile
				profile.AgentInheritedEnvironment = list
				normalized, hash, err := NewResolvedExecutionProfileV1(profile)
				if err != nil {
					t.Fatalf("NewResolvedExecutionProfileV1: %v", err)
				}
				if hash != test.want {
					t.Fatalf("hash = %s, want the pre-V9-05 %s", hash, test.want)
				}
				raw, err := json.Marshal(normalized)
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				if strings.Contains(string(raw), "agentInheritedEnvironment") {
					t.Fatalf("an empty set must not be encoded at all: %s", raw)
				}
			})
		}
	}
}

func TestNewResolvedExecutionProfileV1_AgentInheritedEnvironment_IsNormalizedAndHashed(t *testing.T) {
	_, hashWithout, err := NewResolvedExecutionProfileV1(validAgentProfile())
	if err != nil {
		t.Fatalf("without: %v", err)
	}

	profile := validAgentProfile()
	profile.AgentInheritedEnvironment = []string{"PATH", "HOME", "PATH"}
	normalized, hashWith, err := NewResolvedExecutionProfileV1(profile)
	if err != nil {
		t.Fatalf("with: %v", err)
	}
	if want := []string{"HOME", "PATH"}; !reflect.DeepEqual(normalized.AgentInheritedEnvironment, want) {
		t.Fatalf("normalized.AgentInheritedEnvironment = %v, want the sorted, deduplicated %v", normalized.AgentInheritedEnvironment, want)
	}
	if hashWith == hashWithout {
		t.Fatal("pinning inherited names did not change ExecutionProfileHash")
	}

	reordered := validAgentProfile()
	reordered.AgentInheritedEnvironment = []string{"HOME", "PATH"}
	_, hashReordered, err := NewResolvedExecutionProfileV1(reordered)
	if err != nil {
		t.Fatalf("reordered: %v", err)
	}
	if hashReordered != hashWith {
		t.Fatalf("hash depends on the order/repetition of the set: %s vs %s", hashReordered, hashWith)
	}

	other := validAgentProfile()
	other.AgentInheritedEnvironment = []string{"HOME"}
	_, hashOther, err := NewResolvedExecutionProfileV1(other)
	if err != nil {
		t.Fatalf("other: %v", err)
	}
	if hashOther == hashWith {
		t.Fatal("two different inherited sets hash the same")
	}

	raw, err := json.Marshal(normalized)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"agentInheritedEnvironment":["HOME","PATH"]`) {
		t.Fatalf("canonical JSON does not carry the sorted names: %s", raw)
	}
}

func TestNewResolvedExecutionProfileV1_RejectsAgentInheritedEnvironmentOnCommandAndGate(t *testing.T) {
	for name, base := range map[string]func() ResolvedExecutionProfileV1{
		"COMMAND": validCommandProfile, "MACHINE_GATE": validMachineGateProfile,
	} {
		t.Run(name, func(t *testing.T) {
			profile := base()
			profile.AgentInheritedEnvironment = []string{"PATH"}
			_, _, err := NewResolvedExecutionProfileV1(profile)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), name+" executor must not populate AgentInheritedEnvironment") {
				t.Fatalf("err = %v, want it to say a %s executor must not populate AgentInheritedEnvironment", err, name)
			}
		})
	}
}

func TestNewResolvedExecutionProfileV1_RejectsInvalidAgentInheritedEnvironmentName(t *testing.T) {
	for name, entry := range map[string]string{
		"empty": "", "equals": "A=b", "NUL": "A\x00B", "space": "A B", "padded": " PATH",
	} {
		t.Run(name, func(t *testing.T) {
			profile := validAgentProfile()
			profile.AgentInheritedEnvironment = []string{"HOME", entry}
			_, _, err := NewResolvedExecutionProfileV1(profile)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), "AgentInheritedEnvironment[1] is not a valid environment variable name") {
				t.Fatalf("err = %v, want it to name AgentInheritedEnvironment[1]", err)
			}
		})
	}
}

func TestIntersectEnvironmentNames(t *testing.T) {
	tests := []struct {
		name      string
		requested []string
		ceiling   []string
		want      []string
	}{
		{"profile is a subset of the worker list", []string{"HOME", "PATH"}, []string{"PATH", "HOME", "TMP"}, []string{"HOME", "PATH"}},
		{"worker list is a subset of the profile", []string{"HOME", "PATH", "TMP"}, []string{"PATH"}, []string{"PATH"}},
		{"partial overlap", []string{"HOME", "AW_A"}, []string{"AW_A", "AW_B"}, []string{"AW_A"}},
		{"disjoint", []string{"HOME"}, []string{"PATH"}, nil},
		{"identical", []string{"PATH", "HOME"}, []string{"HOME", "PATH"}, []string{"HOME", "PATH"}},
		{"the profile asks for nothing", nil, []string{"PATH"}, nil},
		{"the profile asks for nothing (empty list)", []string{}, []string{"PATH"}, nil},
		{"the worker allows nothing", []string{"PATH"}, nil, nil},
		{"the worker allows nothing (empty list)", []string{"PATH"}, []string{}, nil},
		{"neither side lists anything", nil, nil, nil},
		{"matching is case-sensitive", []string{"Path"}, []string{"PATH"}, nil},
		{"case variants are different names", []string{"PATH", "Path"}, []string{"Path"}, []string{"Path"}},
		{"repeats collapse and the result is sorted", []string{"B", "A", "B"}, []string{"A", "B", "B"}, []string{"A", "B"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requested := append([]string(nil), test.requested...)
			ceiling := append([]string(nil), test.ceiling...)
			got := IntersectEnvironmentNames(test.requested, test.ceiling)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("IntersectEnvironmentNames(%v, %v) = %v, want %v", test.requested, test.ceiling, got, test.want)
			}
			if !reflect.DeepEqual(append([]string(nil), test.requested...), requested) || !reflect.DeepEqual(append([]string(nil), test.ceiling...), ceiling) {
				t.Fatal("IntersectEnvironmentNames modified one of its inputs")
			}
		})
	}
}

// TestIntersectEnvironmentNames_NeverWidensTheCeiling: whatever the profile
// asks for, no name outside the ceiling can come out.
func TestIntersectEnvironmentNames_NeverWidensTheCeiling(t *testing.T) {
	ceiling := []string{"PATH", "HOME"}
	for _, requested := range [][]string{
		{"PATH", "HOME", "SECRET_TOKEN"}, {"SECRET_TOKEN"}, {"path"}, {"PATH", "PATH", "HOME", "HOME", "AWS_SECRET_ACCESS_KEY"},
	} {
		for _, name := range IntersectEnvironmentNames(requested, ceiling) {
			if name != "PATH" && name != "HOME" {
				t.Fatalf("IntersectEnvironmentNames(%v, %v) returned %q, which the ceiling does not allow", requested, ceiling, name)
			}
		}
	}
}
