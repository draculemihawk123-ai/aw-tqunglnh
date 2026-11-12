package releasesetcommit

import "testing"

// V12-25 (ADR-058): the marker is a trailer at the end of the body, never on the subject line.
func TestCommitMessageWithMarker_PutsTheMarkerInTheTrailers(t *testing.T) {
	cases := []struct{ message, want string }{
		{"feat: keep the status", "feat: keep the status\n\nRelease-Set-Local-Commit-Marker: sha256:m"},
		{"feat: keep the status\n\nWhy it changed.", "feat: keep the status\n\nWhy it changed.\n\nRelease-Set-Local-Commit-Marker: sha256:m"},
		{"feat: x\n\nBody.\n\nRefs: #12\nCo-Authored-By: A <a@example.invalid>\n",
			"feat: x\n\nBody.\n\nRefs: #12\nCo-Authored-By: A <a@example.invalid>\nRelease-Set-Local-Commit-Marker: sha256:m"},
		// A one-paragraph message that happens to look like a trailer is a subject, not a trailer block.
		{"Fix: the thing", "Fix: the thing\n\nRelease-Set-Local-Commit-Marker: sha256:m"},
	}
	for _, c := range cases {
		if got := commitMessageWithMarker(c.message, "sha256:m"); got != c.want {
			t.Errorf("commitMessageWithMarker(%q) = %q, want %q", c.message, got, c.want)
		}
	}
}
