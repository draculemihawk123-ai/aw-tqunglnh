package versionprobe

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/process"
)

func TestProbe_ReturnsTrimmedStdout(t *testing.T) {
	t.Parallel()

	version, err := Probe(context.Background(), process.NewSupervisor(), "probe-success",
		os.Args[0], []string{"-test.run=TestProbeHelper", "--", "print", "1.2.3\n"}, nil, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if version != "1.2.3" {
		t.Fatalf("version = %q, want 1.2.3", version)
	}
}

func TestProbe_NonZeroExitFailsClosed(t *testing.T) {
	t.Parallel()

	_, err := Probe(context.Background(), process.NewSupervisor(), "probe-exit-nonzero",
		os.Args[0], []string{"-test.run=TestProbeHelper", "--", "fail"}, nil, nil, 5*time.Second)
	if err == nil {
		t.Fatal("expected an error for a non-zero exit")
	}
}

func TestProbe_EmptyOutputFailsClosed(t *testing.T) {
	t.Parallel()

	_, err := Probe(context.Background(), process.NewSupervisor(), "probe-empty",
		os.Args[0], []string{"-test.run=TestProbeHelper", "--", "print", ""}, nil, nil, 5*time.Second)
	if !errors.Is(err, ErrEmptyOutput) {
		t.Fatalf("err = %v, want ErrEmptyOutput", err)
	}
}

func TestProbe_TimeoutFailsClosed(t *testing.T) {
	t.Parallel()

	_, err := Probe(context.Background(), process.NewSupervisor(), "probe-timeout",
		os.Args[0], []string{"-test.run=TestProbeHelper", "--", "hang"}, nil, nil, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error for a timed-out probe")
	}
}

func TestProbe_MissingExecutableFailsClosed(t *testing.T) {
	t.Parallel()

	_, err := Probe(context.Background(), process.NewSupervisor(), "probe-missing",
		"definitely-not-a-real-executable-xyz", nil, nil, nil, 5*time.Second)
	if err == nil {
		t.Fatal("expected an error for a missing executable")
	}
}

// TestProbeHelper is only ever a real helper when re-invoked with
// "-test.run=TestProbeHelper -- ..." (every Probe call above does exactly
// that) — a normal `go test` run never puts a literal "--" in os.Args, so
// argumentsAfterSeparator returning nil is this test's own no-op case.
func TestProbeHelper(t *testing.T) {
	arguments := argumentsAfterSeparator(os.Args)
	if arguments == nil {
		return
	}
	if len(arguments) == 0 {
		os.Exit(2)
	}
	switch arguments[0] {
	case "print":
		if len(arguments) > 1 {
			os.Stdout.WriteString(arguments[1])
		}
		os.Exit(0)
	case "fail":
		os.Exit(1)
	case "require-env":
		// V9-05: exits 7 unless every variable named by the remaining
		// arguments is present in this process' own environment, else prints
		// a version — a probe target whose success genuinely depends on what
		// it inherited.
		for _, name := range arguments[1:] {
			if _, ok := os.LookupEnv(name); !ok {
				os.Exit(7)
			}
		}
		os.Stdout.WriteString("1.2.3\n")
		os.Exit(0)
	case "hang":
		time.Sleep(5 * time.Second)
		os.Exit(0)
	default:
		os.Exit(3)
	}
}

func argumentsAfterSeparator(arguments []string) []string {
	for index, argument := range arguments {
		if argument == "--" {
			return arguments[index+1:]
		}
	}
	return nil
}
