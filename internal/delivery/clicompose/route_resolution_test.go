package clicompose

import (
	"strings"
	"testing"
)

// TestResolveRoute_ThreeWordPathBeatsItsTwoWordPrefix pins the one shape
// resolveRoute has beyond `aw <resource> <action>`: `aw release-set
// local-commit status` is a route of its own, while `aw release-set
// local-commit` (the request leaf, whose later tokens are positional) keeps
// resolving to the two-word route. Neither may shadow the other.
func TestResolveRoute_ThreeWordPathBeatsItsTwoWordPrefix(t *testing.T) {
	routes := Routes()

	status, rest, err := resolveRoute(routes, []string{"release-set", "local-commit", "status", "--project-id", "p1", "rs-1", "lc-1"})
	if err != nil {
		t.Fatalf("three-word resolve: %v", err)
	}
	if status.Name() != "release-set local-commit status" {
		t.Errorf("route = %q, want the three-word status route", status.Name())
	}
	if strings.Join(rest, " ") != "--project-id p1 rs-1 lc-1" {
		t.Errorf("rest = %q, want everything after the three-word path", rest)
	}

	request, rest, err := resolveRoute(routes, []string{"release-set", "local-commit", "--project-id", "p1", "rs-1"})
	if err != nil {
		t.Fatalf("two-word resolve: %v", err)
	}
	if request.Name() != "release-set local-commit" {
		t.Errorf("route = %q, want the two-word request route", request.Name())
	}
	if strings.Join(rest, " ") != "--project-id p1 rs-1" {
		t.Errorf("rest = %q, want everything after the two-word path", rest)
	}

	// A positional id in the third slot is NOT a path word: it must fall back
	// to the two-word route, never be rejected as an unknown three-word path.
	positional, rest, err := resolveRoute(routes, []string{"release-set", "local-commit", "rs-1", "--project-id", "p1"})
	if err != nil {
		t.Fatalf("positional-third resolve: %v", err)
	}
	if positional.Name() != "release-set local-commit" || strings.Join(rest, " ") != "rs-1 --project-id p1" {
		t.Errorf("route = %q rest = %q, want the two-word route with its positionals intact", positional.Name(), rest)
	}
}

// TestResolveRoute_TwoWordRoutesAreUnaffectedByAThirdPositionalToken guards
// the most common call shape — `aw work-item show <id> --project-id <p>` has a
// non-flag third token — against the three-word lookup swallowing it.
func TestResolveRoute_TwoWordRoutesAreUnaffectedByAThirdPositionalToken(t *testing.T) {
	routes := Routes()
	for _, args := range [][]string{
		{"work-item", "show", "wi-1", "--project-id", "p1"},
		{"scope-expansion", "list", "fam-1", "--project-id", "p1"},
		{"evidence", "show", "wi-1", "ev-1", "--project-id", "p1"},
	} {
		route, rest, err := resolveRoute(routes, args)
		if err != nil {
			t.Errorf("%v: %v", args, err)
			continue
		}
		if want := args[0] + " " + args[1]; route.Name() != want {
			t.Errorf("%v: route = %q, want %q", args, route.Name(), want)
		}
		if strings.Join(rest, " ") != strings.Join(args[2:], " ") {
			t.Errorf("%v: rest = %q, want %q", args, rest, args[2:])
		}
	}
}

// TestUsage_ListsEveryRouteIncludingThreeWordOnes proves `aw help` can never
// hide a dispatchable command: every route's own words (after the resource)
// appear in that resource's help line, the three-word `release-set
// local-commit status` among them.
func TestUsage_ListsEveryRouteIncludingThreeWordOnes(t *testing.T) {
	lines := map[string]string{}
	for _, line := range strings.Split(Usage(), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			lines[fields[0]] = line
		}
	}
	for _, r := range Routes() {
		line, ok := lines[r.Path[0]]
		if !ok {
			t.Errorf("route %q: its resource has no help line", r.Name())
			continue
		}
		if len(r.Path) < 2 {
			continue
		}
		action := strings.Join(r.Path[1:], " ")
		found := false
		for _, listed := range strings.Split(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), r.Path[0])), "|") {
			if strings.TrimSpace(listed) == action {
				found = true
			}
		}
		if !found {
			t.Errorf("route %q is dispatchable but not in help line %q", r.Name(), line)
		}
	}
	if !strings.Contains(lines["release-set"], "local-commit status") {
		t.Errorf("help does not list `release-set local-commit status`: %q", lines["release-set"])
	}
}
