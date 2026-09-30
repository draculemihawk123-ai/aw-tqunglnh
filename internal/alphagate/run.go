package alphagate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/taQuangLing/agent-workflow/internal/docscoverage"
)

// Options names the artifacts Load reads. Every path is optional: an absent
// or unreadable artifact is missing EVIDENCE (reported as such by Assess),
// never a tool error that aborts before the assessment exists — V8-11's own
// requirement is that the matrix is always produced, even when the suite has
// failures or CI could not supply an input.
type Options struct {
	RepoRoot string
	Commit   string
	// NeedsJSON is a file holding GitHub's `needs` context as JSON:
	// {"contract": {"result": "success"}, ...}.
	NeedsJSON string
	// FinalGateTests is a file holding the `go test -json` output of the
	// final-gate run.
	FinalGateTests string
	// GitDiffCheck is "success" or "failure" (the `git diff --check` step).
	GitDiffCheck string
}

// Load reads the repository and the artifacts into an Input.
func Load(opts Options) (Input, error) {
	inventory, err := docscoverage.LoadInventory(opts.RepoRoot)
	if err != nil {
		return Input{}, fmt.Errorf("load coverage inventory: %w", err)
	}
	scan, err := ScanRepository(opts.RepoRoot)
	if err != nil {
		return Input{}, err
	}
	in := Input{
		Commit: opts.Commit, Inventory: inventory, Scan: scan,
		Suites: map[string]string{}, GitDiffCheck: opts.GitDiffCheck,
		TestRun: TestRun{Outcomes: map[string]Outcome{}, FailedPackages: map[string]bool{}},
	}

	if opts.NeedsJSON != "" {
		content, err := os.ReadFile(opts.NeedsJSON)
		switch {
		case errors.Is(err, os.ErrNotExist):
			// missing evidence — every suite stays absent
		case err != nil:
			return Input{}, fmt.Errorf("read %s: %w", opts.NeedsJSON, err)
		default:
			var needs map[string]struct {
				Result string `json:"result"`
			}
			if err := json.Unmarshal(content, &needs); err != nil {
				return Input{}, fmt.Errorf("parse %s: %w", opts.NeedsJSON, err)
			}
			for id, n := range needs {
				in.Suites[id] = n.Result
			}
		}
	}

	if opts.FinalGateTests != "" {
		file, err := os.Open(opts.FinalGateTests)
		switch {
		case errors.Is(err, os.ErrNotExist):
			// missing evidence
		case err != nil:
			return Input{}, fmt.Errorf("open %s: %w", opts.FinalGateTests, err)
		default:
			run, parseErr := ParseGoTestJSON(file)
			file.Close()
			if parseErr == nil {
				in.TestRun = run
			}
			// An unparseable stream is also missing evidence.
		}
	}
	return in, nil
}
