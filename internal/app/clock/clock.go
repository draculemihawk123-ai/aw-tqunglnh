// Package clock abstracts the current time (docs/design/03-v1-alpha-foundation.md
// V1-02) so application/domain code never calls time.Now() directly: every
// caller depends on Clock, and a test injects Fixed instead of racing real
// wall-clock time.
package clock

import "time"

// Clock returns the current instant, always normalized to UTC.
type Clock interface {
	Now() time.Time
}

// System is the production Clock.
type System struct{}

func (System) Now() time.Time { return time.Now().UTC() }

// Fixed is a deterministic test double that returns the same instant until
// explicitly advanced.
type Fixed struct {
	now time.Time
}

// NewFixed returns a Fixed clock at t, normalized to UTC the same way
// System is — a test can swap Fixed in for System without changing what
// "the current time" means to the code under test.
func NewFixed(t time.Time) *Fixed {
	return &Fixed{now: t.UTC()}
}

func (f *Fixed) Now() time.Time { return f.now }

// Advance moves the fixed instant forward by d and returns the new value.
// A negative d panics: a Clock must never appear to go backward, which is
// exactly the guarantee real wall-clock time gives callers.
func (f *Fixed) Advance(d time.Duration) time.Time {
	if d < 0 {
		panic("clock: Advance requires a non-negative duration")
	}
	f.now = f.now.Add(d)
	return f.now
}

var (
	_ Clock = System{}
	_ Clock = (*Fixed)(nil)
)
