package message

import (
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func TestNewMessage_Valid_WithAttempt_Succeeds(t *testing.T) {
	createdAt := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	attemptID := runtime.ExecutionAttemptID("attempt-1")
	m, err := NewMessage("msg-1", "project-1", "work-item-1", &attemptID, 1, "user-1", RoleUser, "artifact-1", "corr-1", createdAt)
	if err != nil {
		t.Fatalf("NewMessage returned error: %v", err)
	}
	if m.AttemptID == nil || *m.AttemptID != attemptID {
		t.Fatalf("AttemptID = %v, want %v", m.AttemptID, attemptID)
	}
	if m.Sequence != 1 || m.Role != RoleUser {
		t.Fatalf("unexpected message: %+v", m)
	}
	if m.CreatedAt.Location() != time.UTC {
		t.Fatalf("CreatedAt not normalized to UTC: %v", m.CreatedAt)
	}
}

func TestNewMessage_Valid_NoAttempt_Succeeds(t *testing.T) {
	m, err := NewMessage("msg-1", "project-1", "work-item-1", nil, 1, "user-1", RoleUser, "artifact-1", "corr-1", time.Now())
	if err != nil {
		t.Fatalf("NewMessage returned error: %v", err)
	}
	if m.AttemptID != nil {
		t.Fatalf("AttemptID = %v, want nil", m.AttemptID)
	}
}

func TestNewMessage_RejectsMissingRequiredFields(t *testing.T) {
	createdAt := time.Now()
	blankAttempt := runtime.ExecutionAttemptID("")

	cases := []struct {
		name string
		fn   func() (Message, error)
	}{
		{"empty ID", func() (Message, error) {
			return NewMessage("", "project-1", "work-item-1", nil, 1, "user-1", RoleUser, "artifact-1", "corr-1", createdAt)
		}},
		{"empty ProjectID", func() (Message, error) {
			return NewMessage("msg-1", "", "work-item-1", nil, 1, "user-1", RoleUser, "artifact-1", "corr-1", createdAt)
		}},
		{"empty WorkItemID", func() (Message, error) {
			return NewMessage("msg-1", "project-1", "", nil, 1, "user-1", RoleUser, "artifact-1", "corr-1", createdAt)
		}},
		{"blank AttemptID pointer", func() (Message, error) {
			return NewMessage("msg-1", "project-1", "work-item-1", &blankAttempt, 1, "user-1", RoleUser, "artifact-1", "corr-1", createdAt)
		}},
		{"zero Sequence", func() (Message, error) {
			return NewMessage("msg-1", "project-1", "work-item-1", nil, 0, "user-1", RoleUser, "artifact-1", "corr-1", createdAt)
		}},
		{"empty Actor", func() (Message, error) {
			return NewMessage("msg-1", "project-1", "work-item-1", nil, 1, "", RoleUser, "artifact-1", "corr-1", createdAt)
		}},
		{"unknown Role", func() (Message, error) {
			return NewMessage("msg-1", "project-1", "work-item-1", nil, 1, "user-1", Role("BOGUS"), "artifact-1", "corr-1", createdAt)
		}},
		{"empty ContentArtifactID", func() (Message, error) {
			return NewMessage("msg-1", "project-1", "work-item-1", nil, 1, "user-1", RoleUser, "", "corr-1", createdAt)
		}},
		{"zero CreatedAt", func() (Message, error) {
			return NewMessage("msg-1", "project-1", "work-item-1", nil, 1, "user-1", RoleUser, "artifact-1", "corr-1", time.Time{})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.fn(); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestRoleValid(t *testing.T) {
	for _, r := range []Role{RoleUser, RoleAssistant, RoleSystem, RoleTool} {
		if !r.Valid() {
			t.Fatalf("Role %q should be valid", r)
		}
	}
	if Role("BOGUS").Valid() {
		t.Fatal("unknown role must not be valid")
	}
}
