// Command spike-helper plays a generic "agent/provider process" for
// acceptance scenarios that must prove something about a genuinely separate
// OS process, not about the scenario's own goroutine — e.g. SPK-07's scope
// enforcement scenario, which needs a real child process to attempt writes
// both inside and outside its granted scope, so post-execution diff
// enforcement is proven against real process/filesystem boundaries rather
// than an in-process shortcut.
//
// Contract: each argument is "<path>=<content>"; the helper writes each
// content to its path verbatim (splitting only on the first '=', so content
// may itself contain '=') and exits 0. It decides nothing about scope —
// that is exactly the enforcement point each scenario is testing.
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	for _, spec := range os.Args[1:] {
		path, content, ok := strings.Cut(spec, "=")
		if !ok {
			fmt.Fprintf(os.Stderr, "spike-helper: invalid write spec %q, want path=content\n", spec)
			os.Exit(2)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "spike-helper: write %s: %v\n", path, err)
			os.Exit(1)
		}
	}
}
