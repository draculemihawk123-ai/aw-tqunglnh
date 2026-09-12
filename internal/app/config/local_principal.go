package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
)

// LocalPrincipal is ADR-028's own `LocalPrincipalSnapshot {Actor, Roles[]}`
// source: the authentication context a composition root (`aw serve`'s HTTP
// session or a one-shot `aw` invocation) resolves exactly once from trusted
// startup config, docs/architecture/02-architecture-decisions.md ADR-028's
// "`Actor` và `ActorRoles` là authentication context, không phải input tự
// khai". It is deliberately its own small type rather than a field folded
// into the shared Config/Overrides file<env<flags precedence pipeline:
// ADR-028 permits exactly one mechanism to select it — "global composition
// option chọn một trusted config file" — and explicitly forbids every other
// layer that pipeline exists to support ("không có per-command
// `--actor`/`--role`"), so giving it env/flag overrides here would be
// building the very escape hatch the ADR closes.
type LocalPrincipal struct {
	Actor string
	Roles []string
}

// DefaultLocalPrincipal is ADR-028's safe single-user Alpha default, used
// whenever a startup config omits `localPrincipal` entirely ("nếu thiếu
// toàn bộ dùng `actor=local-operator`, `roles=[operator]`").
func DefaultLocalPrincipal() LocalPrincipal {
	return LocalPrincipal{Actor: "local-operator", Roles: []string{"operator"}}
}

// rawLocalPrincipalFile mirrors the canonical config keys ADR-028 names:
// `localPrincipal.actor`/`localPrincipal.roles`, nested under one
// `localPrincipal` object rather than flat snake_case like Config's other
// keys — a deliberate visual distinction, since this key is read by a
// different, narrower loader (LoadLocalPrincipalFile) than the general
// Config file layer.
type rawLocalPrincipalFile struct {
	LocalPrincipal *struct {
		Actor *string  `json:"actor"`
		Roles []string `json:"roles"`
	} `json:"localPrincipal"`
}

// LoadLocalPrincipalFile reads a trusted JSON config file's own
// `localPrincipal` key. An empty path, a missing file, or a file present
// but missing the `localPrincipal` key entirely all resolve to
// DefaultLocalPrincipal — "thiếu toàn bộ" in ADR-028's own text. A file
// that DOES declare `localPrincipal` but only partially (e.g. an actor with
// no roles) is returned as-is, not silently merged with the default:
// Validate/ValidateLocalPrincipal is what turns that into a typed startup
// failure, since a partial declaration is far more likely an operator
// mistake than an intentional half-default.
func LoadLocalPrincipalFile(path string) (LocalPrincipal, error) {
	if strings.TrimSpace(path) == "" {
		return DefaultLocalPrincipal(), nil
	}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return DefaultLocalPrincipal(), nil
	}
	if err != nil {
		return LocalPrincipal{}, fmt.Errorf("read local principal config %s: %w", path, err)
	}
	var raw rawLocalPrincipalFile
	if err := json.Unmarshal(content, &raw); err != nil {
		return LocalPrincipal{}, fmt.Errorf("parse local principal config %s: %w", path, err)
	}
	if raw.LocalPrincipal == nil {
		return DefaultLocalPrincipal(), nil
	}
	lp := LocalPrincipal{Roles: append([]string(nil), raw.LocalPrincipal.Roles...)}
	if raw.LocalPrincipal.Actor != nil {
		lp.Actor = *raw.LocalPrincipal.Actor
	}
	return lp, nil
}

// ValidateLocalPrincipal enforces ADR-028's own shape constraint: "Actor/
// role phải non-empty, roles unique và so khớp case-sensitive." It never
// defaults a missing/blank field on the caller's behalf — LoadLocalPrincipalFile
// already applied the one legitimate default (a fully absent
// `localPrincipal` key), so anything reaching here that is still
// empty/duplicate is a real startup misconfiguration to fail fast on, not
// paper over.
func ValidateLocalPrincipal(lp LocalPrincipal) error {
	var problems []string

	if strings.TrimSpace(lp.Actor) == "" {
		problems = append(problems, "local_principal.actor: WHAT is empty; WHY HTTP/CLI command dispatch has no safe anonymous identity; FIX set localPrincipal.actor in the trusted startup config file (or omit localPrincipal entirely for the local-operator default)")
	}
	if len(lp.Roles) == 0 {
		problems = append(problems, "local_principal.roles: WHAT is empty; WHY AuthorizedRoles has nothing to check against; FIX set localPrincipal.roles to a non-empty list (or omit localPrincipal entirely for the [operator] default)")
	}
	seen := make(map[string]struct{}, len(lp.Roles))
	for i, role := range lp.Roles {
		if strings.TrimSpace(role) == "" {
			problems = append(problems, fmt.Sprintf("local_principal.roles[%d]: WHAT entry is blank; WHY a blank role cannot match any real authorization check; FIX remove the blank entry", i))
			continue
		}
		if _, dup := seen[role]; dup {
			problems = append(problems, fmt.Sprintf("local_principal.roles[%d]: WHAT role %q is a case-sensitive duplicate; WHY a role list is a set, not a multiset; FIX remove the duplicate", i, role))
			continue
		}
		seen[role] = struct{}{}
	}

	if len(problems) == 0 {
		return nil
	}
	return apperror.New(apperror.CodeInvalidArgument, fmt.Sprintf("%d local principal problem(s) found", len(problems)), false).
		WithDetails(map[string]string{
			"correlationId": idsource.Random{}.NewID(),
			"problems":      strings.Join(problems, " | "),
		})
}
