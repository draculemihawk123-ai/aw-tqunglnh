// Package spikeacceptance runs the reproducible, offline baseline of the Go
// spike and writes a sealed evidence bundle. It is deliberately outside the
// domain/application packages: this package coordinates OS commands and the
// local evidence adapter, but owns no workflow semantics.
package spikeacceptance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/evidence"
)

const AlphaEvidenceRetention = 7 * 24 * time.Hour

type CommandResult struct {
	Command  string
	ExitCode int
	Output   []byte
}

type CommandRunner interface {
	Run(context.Context, string, []string, string) (CommandResult, error)
}

type OSCommandRunner struct{}

func (OSCommandRunner) Run(ctx context.Context, executable string, arguments []string, directory string) (CommandResult, error) {
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Dir = directory
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	result := CommandResult{Command: strings.TrimSpace(executable + " " + strings.Join(arguments, " ")), Output: output.Bytes()}
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

type RunRequest struct {
	EvidenceRoot string
	WorkingDir   string
	GoExecutable string
	Now          time.Time
	SuiteID      string
}

// RunResult is the outcome of the offline go-test baseline only; it is not
// an SPK-01..SPK-14 gate verdict. See SPKManifest for the typed, validated
// per-SPK result schema.
type RunResult struct {
	SuiteID         string
	EvidenceDir     string
	Passed          bool
	PrunedBundleIDs []string
}

// RunOfflineBaseline is not a declaration that SPK-01..SPK-14 passed. It
// produces a sealed, verifiable record of the offline contract suite on one
// platform. The full gate is reached only after each SPK is represented here
// and Windows/Linux/race evidence is reviewed.
func RunOfflineBaseline(ctx context.Context, runner CommandRunner, request RunRequest) (RunResult, error) {
	if runner == nil {
		return RunResult{}, errors.New("acceptance command runner is required")
	}
	if strings.TrimSpace(request.EvidenceRoot) == "" || strings.TrimSpace(request.WorkingDir) == "" {
		return RunResult{}, errors.New("acceptance evidence root and working directory are required")
	}
	if request.Now.IsZero() {
		request.Now = time.Now().UTC()
	}
	if request.SuiteID == "" {
		request.SuiteID = "offline-" + request.Now.UTC().Format("20060102t150405.000000000z")
	}
	if request.GoExecutable == "" {
		request.GoExecutable = "go"
	}
	workingDir, err := filepath.Abs(request.WorkingDir)
	if err != nil {
		return RunResult{}, fmt.Errorf("resolve acceptance working directory: %w", err)
	}
	bundle, err := evidence.CreateAt(request.EvidenceRoot, request.SuiteID, request.Now)
	if err != nil {
		return RunResult{}, err
	}
	result := RunResult{SuiteID: request.SuiteID, EvidenceDir: bundle.Directory()}
	if _, err := bundle.PutJSON("environment.json", map[string]string{
		"goos": runtime.GOOS, "goarch": runtime.GOARCH, "suiteKind": "offline-baseline",
	}); err != nil {
		return result, err
	}
	commandResult, commandErr := runner.Run(ctx, request.GoExecutable, []string{"test", "-count=1", "./..."}, workingDir)
	if _, err := bundle.PutRedacted("assertions/offline-go-test.log", commandResult.Output, sensitiveValues()...); err != nil {
		return result, err
	}
	if _, err := bundle.PutJSON("assertions/report.json", map[string]any{
		"command": commandResult.Command, "exitCode": commandResult.ExitCode,
		"passed": commandErr == nil, "scope": "baseline only; not SPK-01..SPK-14 verdict",
	}); err != nil {
		return result, err
	}
	if _, err := bundle.Finalize(map[string]string{
		"suiteId": request.SuiteID, "gateVerdict": "IN_PROGRESS", "offline": "true",
	}); err != nil {
		return result, err
	}
	if _, err := evidence.Verify(bundle.Directory()); err != nil {
		return result, fmt.Errorf("verify newly-created acceptance evidence: %w", err)
	}
	pruned, err := evidence.PruneExpired(request.EvidenceRoot, request.Now, AlphaEvidenceRetention)
	if err != nil {
		return result, err
	}
	result.Passed = commandErr == nil
	result.PrunedBundleIDs = pruned
	if commandErr != nil {
		return result, fmt.Errorf("offline Go contract suite failed: %w", commandErr)
	}
	return result, nil
}

func VerifySuite(evidenceRoot, suiteID string) error {
	if strings.TrimSpace(evidenceRoot) == "" || strings.TrimSpace(suiteID) == "" {
		return errors.New("evidence root and suite id are required")
	}
	directory := filepath.Join(evidenceRoot, suiteID)
	if _, err := evidence.Verify(directory); err != nil {
		return fmt.Errorf("verify suite %s: %w", suiteID, err)
	}
	return nil
}

func sensitiveValues() []string {
	values := make([]string, 0, 2)
	for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY"} {
		if value := os.Getenv(key); value != "" {
			values = append(values, value)
		}
	}
	return values
}
