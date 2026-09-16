package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// DefaultMaxInputBytes bounds a leaf's own --file/stdin request-body
// read: 1 MiB, the same order-of-magnitude default `aw serve`'s own
// --max-body-bytes flag already uses for the identical concern on the
// HTTP side (cmd/aw/serve.go: `flags.Int64("max-body-bytes", 1<<20, ...)`).
const DefaultMaxInputBytes int64 = 1 << 20

// ErrInputTooLarge is returned by ReadBoundedInput when the input exceeds
// maxBytes — a typed, caller-actionable usage error, never a silent
// truncation and never an unbounded read that could OOM this process on a
// huge accidental input.
var ErrInputTooLarge = errors.New("cli: input exceeds the maximum accepted size")

// ReadBoundedInput reads a leaf's own request body from filePath (when
// non-empty) or stdin otherwise, capped at maxBytes (pass
// DefaultMaxInputBytes, or <= 0, absent a leaf-specific reason to pick a
// different bound — <=0 defaults to DefaultMaxInputBytes). It reads at
// most maxBytes+1 bytes via io.LimitReader — enough to detect an
// oversized input without ever buffering an unbounded amount when the
// caller hands it something huge.
func ReadBoundedInput(stdin io.Reader, filePath string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxInputBytes
	}
	reader := stdin
	if filePath != "" {
		f, err := os.Open(filePath)
		if err != nil {
			return nil, fmt.Errorf("cli: open %s: %w", filePath, err)
		}
		defer f.Close()
		reader = f
	}
	if reader == nil {
		return nil, errors.New("cli: no input source (neither --file nor stdin provided)")
	}

	limited := io.LimitReader(reader, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("cli: read input: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: limit is %d bytes", ErrInputTooLarge, maxBytes)
	}
	return data, nil
}
