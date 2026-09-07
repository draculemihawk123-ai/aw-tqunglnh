package process

import "io"

// defaultOutputLimitBytes backstops any caller that leaves
// ports.ProcessSpec.OutputLimitBytes at zero — a runaway child must never
// be able to exhaust worker memory by default (V5-05).
const defaultOutputLimitBytes = 10 << 20 // 10 MiB

// boundedWriter forwards up to limit bytes to the underlying writer, then
// silently discards the rest while still reporting truncated. It never
// returns a short count without an error (io.Writer's own contract): bytes
// past the limit are accepted-and-dropped, not rejected, so a spawned
// process is never surprised by a partial write or a broken pipe purely
// because it produced more output than the cap.
type boundedWriter struct {
	w         io.Writer
	remaining int
	truncated bool
}

func newBoundedWriter(w io.Writer, limit int) *boundedWriter {
	if w == nil {
		w = io.Discard
	}
	return &boundedWriter{w: w, remaining: limit}
}

func (b *boundedWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) <= b.remaining {
		n, err := b.w.Write(p)
		b.remaining -= n
		return n, err
	}
	if _, err := b.w.Write(p[:b.remaining]); err != nil {
		return 0, err
	}
	b.truncated = true
	b.remaining = 0
	return len(p), nil
}
