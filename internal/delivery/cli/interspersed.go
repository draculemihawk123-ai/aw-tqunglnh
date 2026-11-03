package cli

import (
	"flag"
	"strings"
)

// Interspersed (V12-27, docs/design/15-v12-ui-operations.md UX-41) returns args with every flag of fs moved before the positional
// arguments, which follow a "--". Go's flag package stops at the first positional argument, so `aw evidence list <workItemId>
// --project-id p` used to ignore --project-id and print the usage; with Interspersed a flag is read wherever it is written. A flag's
// value is taken from "--name=value" or from the next argument (not for a bool flag); an unknown flag is kept for fs.Parse to report.
// "--" still ends the flags: what follows it is positional, even when it starts with "-".
func Interspersed(fs *flag.FlagSet, args []string) []string {
	flags := make([]string, 0, len(args))
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			continue
		}
		if i+1 >= len(args) {
			// A value flag with no value: leave it last so fs.Parse reports "flag needs an argument" (it must not take "--" as its value).
			return flags
		}
		flags = append(flags, args[i+1])
		i++
	}
	return append(append(flags, "--"), positional...)
}
