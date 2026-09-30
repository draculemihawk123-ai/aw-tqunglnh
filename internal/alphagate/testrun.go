package alphagate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Outcome is one test's result in a `go test -json` stream.
type Outcome string

const (
	OutcomePass    Outcome = "pass"
	OutcomeFail    Outcome = "fail"
	OutcomeSkip    Outcome = "skip"
	OutcomeMissing Outcome = ""
)

// TestRun is what a `go test -json` run of the final-gate tests reported.
type TestRun struct {
	// Outcomes is keyed "pkgDir|TestName[/Subtest]".
	Outcomes map[string]Outcome
	// FailedPackages are package directories whose package-level action was
	// `fail` (a build failure or a panic reports no per-test lines at all, and
	// must not read as "test absent").
	FailedPackages map[string]bool
	// Output holds the tail of each test's own output, keyed like Outcomes,
	// so a failing gate can say WHY in its detail.
	Output map[string]string
}

// Outcome returns ref's outcome. A test with no line in a failed package is a
// failure (the package could not even run it); with no line in a package that
// did not fail it is missing.
func (r TestRun) Outcome(ref TestRef) Outcome {
	if o, ok := r.Outcomes[ref.Key()]; ok {
		return o
	}
	if r.FailedPackages[ref.Pkg] {
		return OutcomeFail
	}
	return OutcomeMissing
}

type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

// outputTailBytes bounds how much output is kept per test.
const outputTailBytes = 600

// ParseGoTestJSON reads `go test -json` output. Lines that are not JSON (a
// build error printed by the go tool itself) are ignored; a stream with no
// test event at all is an error so an empty or truncated artifact can never
// be mistaken for "nothing failed".
func ParseGoTestJSON(r io.Reader) (TestRun, error) {
	run := TestRun{Outcomes: map[string]Outcome{}, FailedPackages: map[string]bool{}, Output: map[string]string{}}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	events := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var event testEvent
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		events++
		dir := strings.TrimPrefix(event.Package, ModulePath+"/")
		if event.Action == "output" && event.Test != "" {
			key := dir + "|" + event.Test
			tail := run.Output[key] + event.Output
			if len(tail) > outputTailBytes {
				tail = tail[len(tail)-outputTailBytes:]
			}
			run.Output[key] = tail
			continue
		}
		switch event.Action {
		case "pass", "fail", "skip":
		default:
			continue
		}
		if event.Test == "" {
			if event.Action == "fail" {
				run.FailedPackages[dir] = true
			}
			continue
		}
		key := dir + "|" + event.Test
		outcome := Outcome(event.Action)
		// A test that reports more than once (e.g. -count>1) must not have a
		// later pass hide an earlier failure.
		if existing, ok := run.Outcomes[key]; ok && existing == OutcomeFail {
			continue
		}
		run.Outcomes[key] = outcome
	}
	if err := scanner.Err(); err != nil {
		return TestRun{}, fmt.Errorf("read go test -json: %w", err)
	}
	if events == 0 {
		return TestRun{}, fmt.Errorf("go test -json stream contains no test event")
	}
	return run, nil
}
