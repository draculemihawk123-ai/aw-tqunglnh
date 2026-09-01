// Package idsource generates new identifiers (docs/design/03-v1-alpha-foundation.md
// V1-02, docs/design/00-roadmap.md §7's "ID do application tạo"): Alpha
// never lets a persistence adapter mint identity (no auto-increment, no
// database-generated UUID) — application code always creates the ID before
// the first write.
package idsource

import (
	"fmt"

	"github.com/google/uuid"
)

// Source generates a new unique identifier's raw string form. A caller
// converts the result to its own aggregate's named ID type (e.g.
// work.WorkItemID(source.NewID())); Source itself stays untyped so it
// never needs to import every domain package that defines an ID type.
type Source interface {
	NewID() string
}

// Random is the production Source: a version 4 (random) UUID per call.
type Random struct{}

func (Random) NewID() string { return uuid.NewString() }

// Sequential is a deterministic test double: NewID returns "<prefix>-1",
// "<prefix>-2", ... in call order, so a test can assert on exact IDs
// instead of only on "is non-empty". Not safe for concurrent use — it is a
// single-threaded test helper, not a production allocator.
type Sequential struct {
	prefix string
	next   int
}

// NewSequential returns a Sequential source. An empty prefix defaults to
// "seq".
func NewSequential(prefix string) *Sequential {
	if prefix == "" {
		prefix = "seq"
	}
	return &Sequential{prefix: prefix}
}

func (s *Sequential) NewID() string {
	s.next++
	return fmt.Sprintf("%s-%d", s.prefix, s.next)
}

var (
	_ Source = Random{}
	_ Source = (*Sequential)(nil)
)
