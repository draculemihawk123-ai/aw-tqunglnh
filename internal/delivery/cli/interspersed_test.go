package cli

import (
	"flag"
	"io"
	"reflect"
	"testing"
)

func TestInterspersed_ReadsFlagsWhereverTheyAre(t *testing.T) {
	newFS := func() (*flag.FlagSet, *string, *bool) {
		fs := flag.NewFlagSet("evidence list", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		return fs, fs.String("project-id", "", ""), fs.Bool("follow", false, "")
	}
	cases := []struct {
		args       []string
		project    string
		follow     bool
		positional []string
	}{
		{[]string{"wi-1", "--project-id", "p"}, "p", false, []string{"wi-1"}},
		{[]string{"--project-id", "p", "wi-1"}, "p", false, []string{"wi-1"}},
		{[]string{"wi-1", "--follow", "--project-id=p", "x"}, "p", true, []string{"wi-1", "x"}},
		{[]string{"--project-id", "p", "--", "-not-a-flag"}, "p", false, []string{"-not-a-flag"}},
	}
	for _, c := range cases {
		fs, project, follow := newFS()
		if err := fs.Parse(Interspersed(fs, c.args)); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if *project != c.project || *follow != c.follow || !reflect.DeepEqual(fs.Args(), c.positional) {
			t.Fatalf("%v: project %q follow %v positional %v; want %q %v %v", c.args, *project, *follow, fs.Args(), c.project, c.follow, c.positional)
		}
	}
	fs, _, _ := newFS()
	if err := fs.Parse(Interspersed(fs, []string{"wi-1", "--unknown"})); err == nil {
		t.Fatal("an unknown flag after a positional argument must still be an error")
	}
	fs, _, _ = newFS()
	if err := fs.Parse(Interspersed(fs, []string{"wi-1", "--project-id"})); err == nil {
		t.Fatal("a value flag without its value must be an error, not take the separator as its value")
	}
}
