package idsource

import "testing"

func TestRandomNeverReturnsZeroID(t *testing.T) {
	source := Random{}
	for i := 0; i < 100; i++ {
		if id := source.NewID(); id == "" {
			t.Fatal("Random.NewID() returned the zero (empty) ID")
		}
	}
}

func TestRandomReturnsDistinctIDs(t *testing.T) {
	source := Random{}
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := source.NewID()
		if seen[id] {
			t.Fatalf("Random.NewID() returned a duplicate: %s", id)
		}
		seen[id] = true
	}
}

func TestSequentialIsDeterministicAndOrdered(t *testing.T) {
	source := NewSequential("test")
	first := source.NewID()
	second := source.NewID()
	third := source.NewID()
	if first != "test-1" || second != "test-2" || third != "test-3" {
		t.Fatalf("Sequential IDs = %q, %q, %q; want test-1, test-2, test-3", first, second, third)
	}
}

func TestSequentialDefaultsPrefix(t *testing.T) {
	source := NewSequential("")
	if id := source.NewID(); id != "seq-1" {
		t.Fatalf("Sequential with empty prefix = %q, want seq-1", id)
	}
}

func TestSequentialNeverReturnsZeroID(t *testing.T) {
	source := NewSequential("x")
	for i := 0; i < 10; i++ {
		if source.NewID() == "" {
			t.Fatal("Sequential.NewID() returned the zero (empty) ID")
		}
	}
}

func TestSequentialInstancesAreIndependent(t *testing.T) {
	a := NewSequential("a")
	b := NewSequential("b")
	a.NewID()
	a.NewID()
	got := b.NewID()
	if got != "b-1" {
		t.Fatalf("a new Sequential source should start its own counter at 1, got %q", got)
	}
}
