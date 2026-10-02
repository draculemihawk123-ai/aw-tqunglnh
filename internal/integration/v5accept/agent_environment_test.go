// V9-05 — "Môi trường của agent được khai báo và ghi vào manifest"
// (docs/design/12-v9-harness-alignment.md V9-05; gap G5 in
// docs/harness-engineering/15-doi-chieu-v9.md).
//
// Before V9-05 the AGENT process was spawned with an EMPTY environment:
// `aw worker --env-allowlist` reached the config snapshot and COMMAND/GATE, but
// the AGENT executor never set InheritedEnvironment, so the Claude CLI needed a
// wrapper script that hard-coded HOME/PATH — environment no aw version or hash
// recorded. The scenarios here drive the real thing end to end — the fake
// provider CLI as a real process, a real sqlite store, a real worker pool, a
// real git worktree — and prove:
//
//   - the process inherits exactly the INTERSECTION of the AgentProfile's
//     envAllowlist and the worker's --env-allowlist: a variable both lists name
//     is there (with the right value), one only the worker allows is not, one
//     only the profile asks for is not, one nobody lists is not;
//   - the pinned execution profile (and its decision artifact) records the
//     NAMES, and the sentinel VALUES appear nowhere — not in the database file,
//     not in an evidence or agent-event row, not in any artifact object, not in
//     the capture the fake CLI wrote (design Verify: "Quét log/evidence/DB
//     không thấy giá trị biến");
//   - a profile that declares nothing still gets an empty environment, however
//     much the worker allows;
//   - the pinned list is only a maximum: when the worker that EXECUTES the
//     attempt allows less than the one that scheduled it, the process gets less.
//
// The fake CLI reports what it inherited as SHA-256 digests of the values it
// saw (providers/fixtures.go AGENTKIT_HELPER_REPORT_ENV), so the evidence that
// "the process really received the value" never puts the value in a file.
package v5accept

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	stdruntime "runtime"
	"sort"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/providers"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/agentprofile"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// Four variable names that exist identically on every operating system (no
// HOME/USERPROFILE: Windows and Linux disagree), each with a value distinctive
// enough that finding it in a file can only mean aw wrote it there.
const (
	v905Visible     = "AW_V905_E2E_VISIBLE"      // named by the profile AND the worker
	v905WorkerOnly  = "AW_V905_E2E_WORKER_ONLY"  // named by the worker only
	v905ProfileOnly = "AW_V905_E2E_PROFILE_ONLY" // named by the profile only
	v905Unlisted    = "AW_V905_E2E_UNLISTED"     // named by nobody
)

var v905Values = map[string]string{
	v905Visible:     "v905-sentinel-value-visible-3a9f6c1e84d2",
	v905WorkerOnly:  "v905-sentinel-value-worker-only-b27d05e9c613",
	v905ProfileOnly: "v905-sentinel-value-profile-only-5e1c8a7f40b9",
	v905Unlisted:    "v905-sentinel-value-unlisted-90d4e2b6a1c7",
}

func v905Names() []string {
	names := make([]string, 0, len(v905Values))
	for name := range v905Values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// v905Document is start -> implement (AGENT, MAKER) -> end.
func v905Document(agentBuildID string) workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			v9AgentNode("implement", agentBuildID, workflow.AgentRoleMaker, "done"),
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-implement", From: "start", Outcome: "next", To: "implement"},
			{Key: "implement-end", From: "implement", Outcome: "done", To: "end"},
		},
	}
}

// newV905AgentSetup is newV9AgentSetup with the AgentProfile document and the
// executor's options chosen by the scenario. It publishes under the same
// "v9-..." ids newV9AgentSetup uses, so v9AgentNode's pins resolve.
func newV905AgentSetup(t *testing.T, f *v5AcceptFixture, profile agentprofile.AgentProfileDocument, options ...runtime.AgentNodeExecutorOption) v9AgentSetup {
	t.Helper()
	adapter := f.newClaudeAdapter(t)
	agentBuildID := f.registerAgentBuild(t, adapter)
	agentRegistry := f.newAgentRegistry(t, adapter)

	publishPolicyVersion(t, f.uow, "v9-context-policy-def", "v9-context-policy-v1", v5AcceptContextPolicyDocument())
	publishAgentProfileDocument(t, f.uow, "v9-agent-profile-def", "v9-agent-profile-v1", profile)
	publishPolicyVersion(t, f.uow, "v9-attempt-policy-def", "v9-attempt-policy-v1", v5AcceptAttemptPolicyDocument(120))
	publishPolicyVersion(t, f.uow, "v9-permission-policy-def", "v9-permission-policy-v1", v5AcceptDriftPermissionPolicyDocument())
	return v9AgentSetup{buildID: agentBuildID, registry: agentRegistry, executor: f.newAgentExecutor(agentRegistry, options...)}
}

func v905Profile(envAllowlist ...string) agentprofile.AgentProfileDocument {
	return agentprofile.AgentProfileDocument{
		ProviderKey: string(ports.ProviderClaude), Model: "fake-model", ToolRefs: []string{"read_file"},
		ContextPolicyRef: definition.DependencyPin{Kind: definition.KindPolicy, DefinitionID: "v9-context-policy-def", VersionID: "v9-context-policy-v1"},
		Compatibility:    agentprofile.Compatibility{OS: []string{stdruntime.GOOS}},
		Budget:           agentprofile.Budget{MaxTokens: 4096},
		EnvAllowlist:     envAllowlist,
	}
}

func v905WorkerConfig(allowlist ...string) *fake.RuntimeExecutionConfigProvider {
	provider := fake.NewRuntimeExecutionConfigProvider()
	provider.Snapshot.EnvAllowlist = append([]string{}, allowlist...)
	return provider
}

// v905Outcome is what a scenario observed.
type v905Outcome struct {
	f       *v5AcceptFixture
	run     v9Run
	capture providers.FakeCLIInvocation
	// pinned is the execution profile of the implement NodeRun, read back
	// from its decision artifact; pinnedRaw is that artifact's JSON text.
	pinned    runtimedomain.ResolvedExecutionProfileV1
	pinnedRaw string
}

// runV905Scenario runs the one-agent workflow. profile is the AgentProfile
// envAllowlist, scheduling the allowlist of the worker that SCHEDULES the node
// (pinned into the execution profile), executing the allowlist of the worker
// that EXECUTES it (the AGENT executor's ceiling). Every variable of
// v905Values is set in this process — "the worker's environment".
func runV905Scenario(t *testing.T, profile, scheduling, executing []string) v905Outcome {
	t.Helper()
	f := newV5AcceptFixture(t)
	capturePath := filepath.Join(f.fixtureRoot, "fake-cli-capture.json")
	t.Setenv("AGENTKIT_CAPTURE_PATH", capturePath)
	t.Setenv("AGENTKIT_HELPER_REPORT_ENV", strings.Join(v905Names(), ","))
	for name, value := range v905Values {
		t.Setenv(name, value)
	}

	f.runtimeConfig = v905WorkerConfig(scheduling...)
	agents := newV905AgentSetup(t, f, v905Profile(profile...), runtime.WithAgentEnvironmentCeiling(v905WorkerConfig(executing...)))
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v905-workflow-def", "v905-workflow-v1", v905Document(agents.buildID), workflow.DependencyManifest{})
	router := &runtime.NodeExecutorRouter{Agent: agents.executor}
	run := startV9Run(t, f, router, agents.registry, version, "v905", runtimedomain.WorkflowRunVerifying, nil)

	if state := run.nodeRuns["implement"].State; state != runtimedomain.NodeRunSucceeded {
		t.Fatalf("implement node run state = %s, want SUCCEEDED", state)
	}
	requireAttemptState(t, run, "implement", runtimedomain.ExecutionAttemptSucceeded)

	raw, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read the fake CLI capture: %v", err)
	}
	var capture providers.FakeCLIInvocation
	if err := json.Unmarshal(raw, &capture); err != nil {
		t.Fatalf("decode the fake CLI capture: %v", err)
	}

	nodeRunID := string(run.nodeRuns["implement"].ID)
	ctx := context.Background()
	outcome := v905Outcome{f: f, run: run, capture: capture}
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		decision, err := tx.Runtime().GetDecisionArtifact(ctx, nodeRunID+"-execution-profile-v1")
		if err != nil {
			return err
		}
		outcome.pinnedRaw = string(decision.Result)
		return json.Unmarshal(decision.Result, &outcome.pinned)
	}); err != nil {
		t.Fatalf("read the pinned execution profile: %v", err)
	}
	return outcome
}

// seen returns the names of the sentinel variables the fake CLI process held,
// checking each against the digest of the value this test set.
func (o v905Outcome) seen(t *testing.T) []string {
	t.Helper()
	var names []string
	for name, digest := range o.capture.EnvironmentSeen {
		if want := providers.EnvironmentDigest(v905Values[name]); digest != want {
			t.Fatalf("the process saw %s with digest %s, want the digest %s of the value the worker holds", name, digest, want)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TestV9AcceptAgentEnvironment_ProcessInheritsTheIntersection is the G5
// regression and the design's "Quét log/evidence/DB không thấy giá trị biến".
// On a build without V9-05 the process sees none of the variables (the AGENT
// executor never set InheritedEnvironment), so the first assertion fails.
func TestV9AcceptAgentEnvironment_ProcessInheritsTheIntersection(t *testing.T) {
	workerList := []string{v905Visible, v905WorkerOnly}
	outcome := runV905Scenario(t,
		[]string{v905Visible, v905ProfileOnly}, // the profile asks for these
		workerList, workerList,                 // the worker allows these
	)

	// The process: exactly the intersection, with the real value.
	if got, want := outcome.seen(t), []string{v905Visible}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the fake CLI process inherited %v of the sentinel variables, want exactly %v (profile ∩ worker); a variable only the worker allows, only the profile asks for, or nobody lists must be absent", got, want)
	}

	// The record: the pinned execution profile names the variable — only the
	// name — and that is what ExecutionProfileHash covers.
	if want := []string{v905Visible}; !reflect.DeepEqual(outcome.pinned.AgentInheritedEnvironment, want) {
		t.Fatalf("pinned AgentInheritedEnvironment = %v, want %v\n%s", outcome.pinned.AgentInheritedEnvironment, want, outcome.pinnedRaw)
	}
	if !strings.Contains(outcome.pinnedRaw, `"agentInheritedEnvironment":["`+v905Visible+`"]`) {
		t.Fatalf("the execution-profile decision artifact does not name the inherited variable:\n%s", outcome.pinnedRaw)
	}
	attempt := outcome.run.attempts["implement"]
	if _, rehashed, err := runtimedomain.NewResolvedExecutionProfileV1(outcome.pinned); err != nil || rehashed != attempt.ExecutionProfileHash {
		t.Fatalf("the stored profile hashes to %s (%v), the attempt pinned %s", rehashed, err, attempt.ExecutionProfileHash)
	}

	// Positive controls for the scan below: values really reached a real
	// process (above), the scanner really finds a value, and the corpus is
	// not empty — agent events and evidence were really recorded.
	requireScannerFindsValues(t)
	ctx := context.Background()
	var events int
	if err := outcome.f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		records, err := tx.AgentEvents().ListByAttempt(ctx, string(attempt.ID))
		if err != nil {
			return err
		}
		events = len(records)
		evidence, err := tx.Runtime().ListEvidenceForAttempt(ctx, string(attempt.ID))
		if err != nil {
			return err
		}
		if len(evidence) == 0 {
			t.Errorf("no evidence was recorded for the attempt; the scan would prove nothing about evidence rows")
		}
		return nil
	}); err != nil {
		t.Fatalf("read the recorded events and evidence: %v", err)
	}
	if events == 0 {
		t.Fatal("no agent events were recorded for the attempt; the scan would prove nothing about them")
	}

	// The scan: no value of any sentinel variable — the one the process
	// received or any other — is anywhere in what aw retained: the database
	// file (every table, including the event journal, agent events, evidence
	// and decision artifacts), every artifact object, the worktrees, and the
	// fake CLI's own capture.
	if err := outcome.f.store.Close(); err != nil {
		t.Fatalf("close the store before the raw scan: %v", err)
	}
	scanned := scanTreeForValues(t, outcome.f.fixtureRoot, v905Values)
	if len(scanned.findings) != 0 {
		t.Fatalf("variable VALUES were found in retained data: %v", scanned.findings)
	}
	if scanned.files == 0 || scanned.bytes == 0 {
		t.Fatalf("the scan covered %d files / %d bytes, want a non-empty corpus", scanned.files, scanned.bytes)
	}
	if !scanned.sawDatabase {
		t.Fatal("the scan never read the sqlite database file")
	}
}

// TestV9AcceptAgentEnvironment_ProfileWithoutEnvAllowlistGetsAnEmptyEnvironment:
// the default is what it always was. A profile that declares nothing inherits
// nothing, whatever the worker allows.
func TestV9AcceptAgentEnvironment_ProfileWithoutEnvAllowlistGetsAnEmptyEnvironment(t *testing.T) {
	workerList := v905Names()
	outcome := runV905Scenario(t, nil, workerList, workerList)

	if got := outcome.seen(t); len(got) != 0 {
		t.Fatalf("the process inherited %v although its profile declares no envAllowlist", got)
	}
	if len(outcome.pinned.AgentInheritedEnvironment) != 0 || strings.Contains(outcome.pinnedRaw, "agentInheritedEnvironment") {
		t.Fatalf("a profile without envAllowlist pinned an inherited environment:\n%s", outcome.pinnedRaw)
	}
}

// TestV9AcceptAgentEnvironment_WorkerAllowingNothingIsACeiling: the profile can
// ask for everything; a worker started without --env-allowlist hands over
// nothing.
func TestV9AcceptAgentEnvironment_WorkerAllowingNothingIsACeiling(t *testing.T) {
	outcome := runV905Scenario(t, v905Names(), nil, nil)

	if got := outcome.seen(t); len(got) != 0 {
		t.Fatalf("the process inherited %v although the worker allows nothing", got)
	}
	if len(outcome.pinned.AgentInheritedEnvironment) != 0 {
		t.Fatalf("pinned AgentInheritedEnvironment = %v, want none", outcome.pinned.AgentInheritedEnvironment)
	}
}

// TestV9AcceptAgentEnvironment_ExecutingWorkerNarrowerThanSchedulingWorker: the
// NodeRun was scheduled by a worker that allowed the variable (so it is
// pinned), but the worker that executes the attempt does not allow it. Nothing
// at admission or execution compares the two workers' config hashes, so the
// executor must apply its own worker's allowlist: the process gets nothing,
// while the record still shows what was pinned at scheduling.
func TestV9AcceptAgentEnvironment_ExecutingWorkerNarrowerThanSchedulingWorker(t *testing.T) {
	outcome := runV905Scenario(t,
		[]string{v905Visible},
		[]string{v905Visible},    // the scheduling worker allowed it: pinned
		[]string{v905WorkerOnly}, // the executing worker does not
	)

	if want := []string{v905Visible}; !reflect.DeepEqual(outcome.pinned.AgentInheritedEnvironment, want) {
		t.Fatalf("pinned AgentInheritedEnvironment = %v, want %v (decided at scheduling)", outcome.pinned.AgentInheritedEnvironment, want)
	}
	if got := outcome.seen(t); len(got) != 0 {
		t.Fatalf("the process inherited %v although the worker that executed it does not allow that", got)
	}
}

// --- the value scan ------------------------------------------------------

type scanResult struct {
	files, bytes int
	sawDatabase  bool
	findings     []string
}

// scanTreeForValues reads every regular file under root and reports each file
// that contains any of values. It is a plain byte search on the raw files, so
// it covers what no query could miss: free pages and the write-ahead log of
// the sqlite file included.
func scanTreeForValues(t *testing.T, root string, values map[string]string) scanResult {
	t.Helper()
	var result scanResult
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result.files++
		result.bytes += len(body)
		if strings.HasSuffix(path, ".db") || strings.HasSuffix(path, ".db-wal") {
			result.sawDatabase = true
		}
		for name, value := range values {
			if bytes.Contains(body, []byte(value)) {
				result.findings = append(result.findings, name+" value found in "+path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(result.findings)
	return result
}

// requireScannerFindsValues is the scan's own positive control: planted in a
// file, a value is found; a file without it is clean.
func requireScannerFindsValues(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "planted.bin"), []byte("noise "+v905Values[v905Unlisted]+" noise"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "clean.bin"), []byte("noise only"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := scanTreeForValues(t, dir, v905Values)
	if len(result.findings) != 1 || !strings.Contains(result.findings[0], v905Unlisted) || !strings.Contains(result.findings[0], "planted.bin") {
		t.Fatalf("the scanner's positive control found %v, want exactly the planted %s value", result.findings, v905Unlisted)
	}
}
