package contextassembler

import (
	"reflect"
	"testing"
)

func msgs(sizes ...uint64) []MessageCandidate {
	out := make([]MessageCandidate, len(sizes))
	for i, size := range sizes {
		out[i] = MessageCandidate{ID: string(rune('a' + i)), Bytes: size}
	}
	return out
}

func TestResolveMessages(t *testing.T) {
	tests := []struct {
		name       string
		candidates []MessageCandidate
		budget     MessageBudget
		included   []string
		omitted    []string
	}{
		{"empty chat", nil, MessageBudget{MaxBytes: 10, KeepLatest: 2}, nil, nil},
		{"everything fits", msgs(3, 3, 3), MessageBudget{MaxBytes: 100, KeepLatest: 1}, []string{"a", "b", "c"}, nil},
		{"keepLatest larger than the chat", msgs(50, 50), MessageBudget{MaxBytes: 1, KeepLatest: 5}, []string{"a", "b"}, nil},
		{"older messages are cut oldest first", msgs(10, 10, 10, 10), MessageBudget{MaxBytes: 25, KeepLatest: 1}, []string{"c", "d"}, []string{"a", "b"}},
		{"the newest message stays even when it alone exceeds the budget", msgs(5, 5, 500), MessageBudget{MaxBytes: 20, KeepLatest: 1}, []string{"c"}, []string{"a", "b"}},
		{"required messages leave less room for the rest", msgs(10, 10, 10), MessageBudget{MaxBytes: 25, KeepLatest: 2}, []string{"b", "c"}, []string{"a"}},
		{"a smaller older message fits after a larger one did not", msgs(4, 30, 10), MessageBudget{MaxBytes: 15, KeepLatest: 1}, []string{"a", "c"}, []string{"b"}},
		{"pinned messages are kept however old and large", []MessageCandidate{{ID: "a", Pinned: true, Bytes: 100}, {ID: "b", Bytes: 5}, {ID: "c", Bytes: 5}, {ID: "d", Bytes: 5}}, MessageBudget{MaxBytes: 10, KeepLatest: 1}, []string{"a", "d"}, []string{"b", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveMessages(tt.candidates, tt.budget)
			if !reflect.DeepEqual(got.Included, tt.included) || !reflect.DeepEqual(got.Omitted, tt.omitted) {
				t.Fatalf("got included=%v omitted=%v, want included=%v omitted=%v", got.Included, got.Omitted, tt.included, tt.omitted)
			}
		})
	}
}
