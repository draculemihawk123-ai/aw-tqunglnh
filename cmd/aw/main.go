// Command aw is the Alpha composition root
// (docs/design/03-v1-alpha-foundation.md V1-01): it wires the `serve`,
// `worker`, `doctor`, `definition`, `evidence` and `adapter` subcommands.
// Only `evidence` may depend on internal/spikeacceptance-adjacent packages;
// every other subcommand's implementation must come from internal/app and
// internal/adapters as those are built out in later V1..V8 tasks — this
// binary never imports cmd/agentkit-spike's own fixtures, and
// cmd/agentkit-spike keeps building/testing unchanged as the live
// regression gate (00-roadmap.md §5B). `adapter` (V2-07B) is the first
// subcommand to open a real SQLite connection, since serve/worker/doctor/
// definition are all still stubs.
package main

import "os"

func main() {
	os.Exit(int(run(os.Args[1:], os.Stdout, os.Stderr)))
}
