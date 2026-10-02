package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/agentprofile"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// V9-05 (gap G5): the environment an AGENT process inherits is the
// intersection of the AgentProfile's envAllowlist and the operator's
// --env-allowlist, pinned per NodeRun in the execution profile (names only),
// handed to the provider adapter by the AGENT executor — and never wider than
// the allowlist of the worker that executes the attempt.

// workerConfig returns the runtime execution config provider of a worker whose
// --env-allowlist is names.
func workerConfig(names ...string) *fake.RuntimeExecutionConfigProvider {
	provider := fake.NewRuntimeExecutionConfigProvider()
	provider.Snapshot.EnvAllowlist = append([]string{}, names...)
	return provider
}

func profileWithEnvAllowlist(names ...string) *agentprofile.AgentProfileDocument {
	doc := validAgentProfileDocument()
	doc.EnvAllowlist = names
	return &doc
}

// pinnedExecutionProfile reads the "<nodeRunId>-execution-profile-v1"
// DecisionArtifact exactly as it was written (raw JSON), decoded into the
// domain type, and the ExecutionProfileHash the attempt pinned.
func pinnedExecutionProfile(t *testing.T, uow *fake.UnitOfWork, nodeRunID, attemptID string) (raw string, profile runtimedomain.ResolvedExecutionProfileV1, attemptHash string) {
	t.Helper()
	ctx := context.Background()
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		attempt, err := tx.Runtime().GetExecutionAttempt(ctx, attemptID)
		if err != nil {
			return err
		}
		attemptHash = attempt.ExecutionProfileHash
		decision, err := tx.Runtime().GetDecisionArtifact(ctx, nodeRunID+"-execution-profile-v1")
		if err != nil {
			return err
		}
		raw = string(decision.Result)
		return json.Unmarshal(decision.Result, &profile)
	}); err != nil {
		t.Fatalf("read the pinned execution profile: %v", err)
	}
	return raw, profile, attemptHash
}

// TestScheduleExecutableNodeRun_PinsTheIntersectionOfProfileAndWorkerAllowlists
// is the regression for G5's declaration half: whatever the profile asks for is
// cut down to what the operator allows, matched exactly, and the resulting
// NAMES are in the pinned profile, its decision artifact and its hash. Before
// V9-05 the profile had no such field and the pinned profile no such set.
func TestScheduleExecutableNodeRun_PinsTheIntersectionOfProfileAndWorkerAllowlists(t *testing.T) {
	tests := []struct {
		name    string
		profile []string
		worker  []string
		want    []string
	}{
		{"profile is a subset of the worker list", []string{"HOME", "PATH"}, []string{"TMP", "PATH", "HOME"}, []string{"HOME", "PATH"}},
		{"worker list is a subset of the profile", []string{"HOME", "PATH", "TMP"}, []string{"PATH"}, []string{"PATH"}},
		{"partial overlap", []string{"HOME", "AW_ONLY_IN_PROFILE"}, []string{"HOME", "AW_ONLY_IN_WORKER"}, []string{"HOME"}},
		{"disjoint", []string{"HOME"}, []string{"PATH"}, nil},
		{"the profile asks for nothing", nil, []string{"HOME", "PATH"}, nil},
		{"the worker allows nothing", []string{"HOME", "PATH"}, nil, nil},
		{"matching is case-sensitive", []string{"Path"}, []string{"PATH"}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			uow, _, _, runID, nodeRunID, attemptID := assembleRequestFixtureWith(
				t, workflow.AgentRoleMaker, *profileWithEnvAllowlist(test.profile...), workerConfig(test.worker...), attemptPolicyDocument(600))
			_ = runID

			raw, profile, attemptHash := pinnedExecutionProfile(t, uow, nodeRunID, attemptID)
			if !reflect.DeepEqual(profile.AgentInheritedEnvironment, test.want) {
				t.Fatalf("pinned AgentInheritedEnvironment = %v, want %v\n%s", profile.AgentInheritedEnvironment, test.want, raw)
			}

			// The set is part of what the hash covers: re-deriving the hash from
			// the stored profile reproduces the one the attempt pinned...
			_, rehashed, err := runtimedomain.NewResolvedExecutionProfileV1(profile)
			if err != nil {
				t.Fatalf("re-normalize the stored profile: %v", err)
			}
			if rehashed != attemptHash {
				t.Fatalf("the attempt pinned %s but the stored profile hashes to %s", attemptHash, rehashed)
			}
			// ...and dropping the set changes it, unless there was none.
			without := profile
			without.AgentInheritedEnvironment = nil
			_, hashWithout, err := runtimedomain.NewResolvedExecutionProfileV1(without)
			if err != nil {
				t.Fatalf("hash without the set: %v", err)
			}
			if (hashWithout == attemptHash) != (len(test.want) == 0) {
				t.Fatalf("the inherited set %v and the profile hash are not related as expected (hash without the set equals the pinned one: %v)", test.want, hashWithout == attemptHash)
			}

			if len(test.want) == 0 {
				// An empty set is not even encoded: this is what keeps the hash
				// of every profile scheduled before V9-05 what it was.
				if strings.Contains(raw, "agentInheritedEnvironment") {
					t.Fatalf("an empty set must be absent from the decision artifact: %s", raw)
				}
				return
			}
			encoded, err := json.Marshal(test.want)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(raw, `"agentInheritedEnvironment":`+string(encoded)) {
				t.Fatalf("the decision artifact does not carry %s:\n%s", encoded, raw)
			}
		})
	}
}

// TestScheduleExecutableNodeRun_InheritedEnvironmentRecordsNamesNeverValues:
// the variable's value exists in the scheduling process' environment and is
// nowhere in what scheduling writes.
func TestScheduleExecutableNodeRun_InheritedEnvironmentRecordsNamesNeverValues(t *testing.T) {
	const (
		name  = "AW_V905_RUNTIME_SENTINEL"
		value = "sentinel-value-that-must-never-be-persisted-6b1d0e"
	)
	t.Setenv(name, value)
	uow, _, _, _, nodeRunID, attemptID := assembleRequestFixtureWith(
		t, workflow.AgentRoleMaker, *profileWithEnvAllowlist(name), workerConfig(name), attemptPolicyDocument(600))

	raw, profile, hash := pinnedExecutionProfile(t, uow, nodeRunID, attemptID)
	if !reflect.DeepEqual(profile.AgentInheritedEnvironment, []string{name}) {
		t.Fatalf("pinned AgentInheritedEnvironment = %v, want [%s]", profile.AgentInheritedEnvironment, name)
	}
	if strings.Contains(raw, value) || strings.Contains(hash, value) {
		t.Fatalf("the pinned execution profile carries the variable's value:\n%s", raw)
	}
}

// TestScheduleExecutableNodeRun_ProfileWithoutEnvAllowlistPinsNoEnvironment is
// the backward-compatibility regression at the scheduling level: an AgentProfile
// that does not use the field pins an execution profile with no inherited
// environment at all, whatever the worker allows, and its stored profile still
// hashes to what the attempt pinned.
func TestScheduleExecutableNodeRun_ProfileWithoutEnvAllowlistPinsNoEnvironment(t *testing.T) {
	uow, _, _, _, nodeRunID, attemptID := assembleRequestFixtureWith(
		t, workflow.AgentRoleMaker, validAgentProfileDocument(), workerConfig("HOME", "PATH", "TMP"), attemptPolicyDocument(600))
	raw, profile, hash := pinnedExecutionProfile(t, uow, nodeRunID, attemptID)
	if len(profile.AgentInheritedEnvironment) != 0 || strings.Contains(raw, "agentInheritedEnvironment") {
		t.Fatalf("a profile without envAllowlist pinned an inherited environment: %s", raw)
	}
	if _, rehashed, err := runtimedomain.NewResolvedExecutionProfileV1(profile); err != nil || rehashed != hash {
		t.Fatalf("stored profile hashes to %s (%v), the attempt pinned %s", rehashed, err, hash)
	}
}

// TestAssembleAgentExecutionRequest_CarriesThePinnedInheritedEnvironment: the
// request handed towards the provider carries exactly the pinned names, read
// from the pinned decision.
func TestAssembleAgentExecutionRequest_CarriesThePinnedInheritedEnvironment(t *testing.T) {
	for name, test := range map[string]struct {
		profile, worker, want []string
	}{
		"pinned names":     {[]string{"PATH", "HOME"}, []string{"HOME", "PATH", "TMP"}, []string{"HOME", "PATH"}},
		"nothing pinned":   {[]string{"PATH"}, []string{"HOME"}, nil},
		"profile has none": {nil, []string{"HOME", "PATH"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			uow, _, store, runID, nodeRunID, attemptID := assembleRequestFixtureWith(
				t, workflow.AgentRoleMaker, *profileWithEnvAllowlist(test.profile...), workerConfig(test.worker...), attemptPolicyDocument(600))
			request, err := runtime.AssembleAgentExecutionRequest(context.Background(), uow, store, runtime.AssembleAgentExecutionRequestRequest{
				RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID,
			})
			if err != nil {
				t.Fatalf("AssembleAgentExecutionRequest: %v", err)
			}
			if len(request.InheritedEnvironment) != len(test.want) || (len(test.want) > 0 && !reflect.DeepEqual(request.InheritedEnvironment, test.want)) {
				t.Fatalf("request.InheritedEnvironment = %v, want %v", request.InheritedEnvironment, test.want)
			}
		})
	}
}

// TestAgentNodeExecutor_SpawnsWithThePinnedNamesCutByTheExecutingWorkersAllowlist
// is the regression for G5's runtime half. The agent used to be spawned with
// InheritedEnvironment never set. Now the provider adapter is handed the
// pinned names intersected with the allowlist of the worker executing the
// attempt. Nothing at admission or execution compares that worker's runtime
// config hash with the pinned one, so the executor must not trust the pin
// alone: a worker restarted with a narrower list passes less, and no worker
// ever passes a name its own operator did not allow or the profile did not
// pin.
func TestAgentNodeExecutor_SpawnsWithThePinnedNamesCutByTheExecutingWorkersAllowlist(t *testing.T) {
	profile := []string{"HOME", "PATH"}
	scheduledBy := []string{"HOME", "PATH", "TMP"} // pinned: HOME, PATH
	tests := []struct {
		name      string
		executing *fake.RuntimeExecutionConfigProvider // nil = the executor is built without the option
		want      []string
	}{
		{"the executing worker runs the config the NodeRun was scheduled with", workerConfig("HOME", "PATH", "TMP"), []string{"HOME", "PATH"}},
		{"the executing worker has a narrower list", workerConfig("PATH"), []string{"PATH"}},
		{"the executing worker has a wider list: nothing is added", workerConfig("HOME", "PATH", "TMP", "AWS_SECRET_ACCESS_KEY"), []string{"HOME", "PATH"}},
		{"the executing worker allows nothing", workerConfig(), nil},
		{"the executing worker allows only other names", workerConfig("TMP"), nil},
		{"the executor has no ceiling at all", nil, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var seen []ports.AgentExecutionRequest
			options := bridgeFixtureOptions{
				diff:            defaultInScopeDiff(),
				agentResult:     ports.AgentExecutionResult{Status: ports.AgentExecutionSucceeded, TreeQuiesced: true},
				agentEvents:     []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
				profileDocument: profileWithEnvAllowlist(profile...),
				scheduleConfig:  workerConfig(scheduledBy...),
				onAgentRequest:  func(request ports.AgentExecutionRequest) { seen = append(seen, request) },
			}
			if test.executing != nil {
				options.executorOptions = []runtime.AgentNodeExecutorOption{runtime.WithAgentEnvironmentCeiling(test.executing)}
			}
			executor, req, _, _, _, _, _ := bridgeFixture(t, options)

			result, err := executor.Execute(context.Background(), req)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result.State != runtimedomain.ExecutionAttemptSucceeded {
				t.Fatalf("result = %+v, want SUCCEEDED", result)
			}
			if len(seen) != 1 {
				t.Fatalf("the provider adapter was started %d times, want once", len(seen))
			}
			if got := seen[0].InheritedEnvironment; len(got) != len(test.want) || (len(test.want) > 0 && !reflect.DeepEqual(got, test.want)) {
				t.Fatalf("InheritedEnvironment handed to the provider adapter = %v, want %v", got, test.want)
			}
		})
	}
}

// TestAgentNodeExecutor_ProfileWithoutEnvAllowlistInheritsNothingWhateverTheWorkerAllows:
// the default is what it always was — an empty environment — however generous
// the worker's list. The ceiling configuration is not even consulted.
func TestAgentNodeExecutor_ProfileWithoutEnvAllowlistInheritsNothingWhateverTheWorkerAllows(t *testing.T) {
	var seen []ports.AgentExecutionRequest
	failing := &fake.RuntimeExecutionConfigProvider{Err: fake.ErrRuntimeExecutionConfigUnavailable}
	executor, req, _, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:            defaultInScopeDiff(),
		agentResult:     ports.AgentExecutionResult{Status: ports.AgentExecutionSucceeded, TreeQuiesced: true},
		agentEvents:     []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
		profileDocument: profileWithEnvAllowlist(),
		scheduleConfig:  workerConfig("HOME", "PATH"),
		onAgentRequest:  func(request ports.AgentExecutionRequest) { seen = append(seen, request) },
		// A ceiling that would fail if it were consulted: nothing is pinned, so
		// there is nothing to cut and no reason to ask.
		executorOptions: []runtime.AgentNodeExecutorOption{runtime.WithAgentEnvironmentCeiling(failing)},
	})
	if _, err := executor.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(seen) != 1 || len(seen[0].InheritedEnvironment) != 0 {
		t.Fatalf("requests = %+v, want one request with an empty InheritedEnvironment", seen)
	}
}

// TestAgentNodeExecutor_FailsClosedWhenTheWorkersConfigIsUnavailable: with names
// pinned but the ceiling unknowable, no process is spawned — the executor does
// not guess a wider environment, and does not quietly spawn with a narrower
// one either.
func TestAgentNodeExecutor_FailsClosedWhenTheWorkersConfigIsUnavailable(t *testing.T) {
	starts := 0
	executor, req, _, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:            defaultInScopeDiff(),
		agentResult:     ports.AgentExecutionResult{Status: ports.AgentExecutionSucceeded, TreeQuiesced: true},
		agentEvents:     []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
		agentStarts:     &starts,
		profileDocument: profileWithEnvAllowlist("HOME", "PATH"),
		scheduleConfig:  workerConfig("HOME", "PATH"),
		executorOptions: []runtime.AgentNodeExecutorOption{
			runtime.WithAgentEnvironmentCeiling(&fake.RuntimeExecutionConfigProvider{Err: fake.ErrRuntimeExecutionConfigUnavailable}),
		},
	})
	_, err := executor.Execute(context.Background(), req)
	if !errors.Is(err, runtime.ErrRuntimeExecutionConfigUnavailable) {
		t.Fatalf("Execute error = %v, want it to wrap ErrRuntimeExecutionConfigUnavailable", err)
	}
	if starts != 0 {
		t.Fatalf("a process was spawned %d time(s) although the worker's allowlist could not be determined", starts)
	}
}

// TestAgentNodeExecutor_RetryAttemptOfTheSameNodeRunGetsTheSameEnvironment: a
// technical retry creates a new attempt of the SAME NodeRun with the same
// pinned execution profile, so it is handed the same names — decided by the
// same rule — as the attempt it replaces.
func TestAgentNodeExecutor_RetryAttemptOfTheSameNodeRunGetsTheSameEnvironment(t *testing.T) {
	retryable := retryableAttemptPolicyDocument(3, 1, 600, errorcode.CodeProviderUnavailable)
	var seen []ports.AgentExecutionRequest
	executor, req, uow, ids, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff: defaultInScopeDiff(),
		// The provider fails to start: a retryable, definite FAILED.
		agentResult:     ports.AgentExecutionResult{Status: ports.AgentExecutionFailed, TreeQuiesced: true},
		agentErr:        errors.New("provider could not start"),
		agentEvents:     []ports.AgentEventKind{ports.AgentEventExecutionStarted},
		profileDocument: profileWithEnvAllowlist("HOME", "PATH"),
		scheduleConfig:  workerConfig("HOME", "PATH", "TMP"),
		attemptPolicy:   &retryable,
		onAgentRequest:  func(request ports.AgentExecutionRequest) { seen = append(seen, request) },
		executorOptions: []runtime.AgentNodeExecutorOption{runtime.WithAgentEnvironmentCeiling(workerConfig("HOME", "PATH", "TMP"))},
	})
	ctx := context.Background()

	first, err := executor.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute (attempt 1): %v", err)
	}
	if first.State != runtimedomain.ExecutionAttemptFailed || first.ErrorCode != errorcode.CodeProviderUnavailable {
		t.Fatalf("attempt 1 result = %+v, want a retryable FAILED/PROVIDER_UNAVAILABLE", first)
	}
	if _, err := runtime.FinalizeExecutionAttempt(ctx, uow, ids, clock.NewFixed(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: req.RunID, NodeRunID: req.NodeRunID, AttemptID: req.AttemptID, ExpectedVersion: loadAttemptVersion(t, uow, req.AttemptID),
		NextState: first.State, TerminationReason: first.TerminationReason, FailureCode: first.ErrorCode,
		JobLease: req.JobLease, CorrelationID: "corr-1",
	}); err != nil {
		t.Fatalf("FinalizeExecutionAttempt: %v", err)
	}

	// Attempt 2: created by the retry decision, with its own job.
	var retryAttemptID string
	for id, attempt := range uow.Snapshot.Runtime().(*fake.RuntimeRepository).Attempts() {
		if id != req.AttemptID && attempt.NodeRunID == runtimedomain.NodeRunID(req.NodeRunID) {
			retryAttemptID = id
		}
	}
	if retryAttemptID == "" {
		t.Fatal("no retry attempt was created")
	}
	var retryJob ports.EnqueueJobRequest
	for _, job := range uow.Snapshot.Jobs().(*fake.JobsRepository).Items() {
		if job.Kind == runtime.ExecuteNodeJobKind && job.AggregateID == retryAttemptID {
			retryJob = job
		}
	}
	if retryJob.ID == "" {
		t.Fatalf("no %s job for the retry attempt %s", runtime.ExecuteNodeJobKind, retryAttemptID)
	}
	lease := ports.JobLease{JobID: retryJob.ID, Owner: "worker-1", Token: 1, LeaseUntil: time.Now().Add(time.Hour)}
	uow.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(retryJob.ID), lease)
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		attempt, err := tx.Runtime().GetExecutionAttempt(ctx, retryAttemptID)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
			AttemptID: retryAttemptID, ExpectedState: runtimedomain.ExecutionAttemptQueued, ExpectedVersion: attempt.Version,
			NextState: runtimedomain.ExecutionAttemptRunning,
		})
		return err
	}); err != nil {
		t.Fatalf("mark the retry attempt RUNNING: %v", err)
	}

	if _, err := executor.Execute(ctx, ports.NodeExecutionRequest{AttemptID: retryAttemptID, NodeRunID: req.NodeRunID, RunID: req.RunID, JobLease: lease}); err != nil {
		t.Fatalf("Execute (attempt 2): %v", err)
	}

	if len(seen) != 2 {
		t.Fatalf("the provider adapter was started %d times, want once per attempt (2)", len(seen))
	}
	want := []string{"HOME", "PATH"}
	for i, request := range seen {
		if !reflect.DeepEqual(request.InheritedEnvironment, want) {
			t.Fatalf("attempt %d: InheritedEnvironment = %v, want %v", i+1, request.InheritedEnvironment, want)
		}
	}
	if seen[0].ExecutionProfileHash != seen[1].ExecutionProfileHash {
		t.Fatalf("the retry attempt pinned a different execution profile: %s vs %s", seen[0].ExecutionProfileHash, seen[1].ExecutionProfileHash)
	}
}
