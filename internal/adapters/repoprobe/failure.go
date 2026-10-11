package repoprobe

import (
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// V11-12 (docs/design/14-v11-bugfix.md, ADR-043): a probe failure says what is wrong, where, and what to do. Every failure is still an
// *apperror.Error whose Code is one of the old public codes (INVALID_ARGUMENT, NOT_FOUND, UNAVAILABLE), so callers that branch on the code are
// unaffected; the closed ports.ProbeFailureKind in Details["failureKind"] separates the causes that used to share one sentence, and
// Details carries the path, the (redacted, bounded) Git stderr and a hint with a command that can be pasted.

const maxDetailBytes = 2048

// failure builds the typed error: code and retryability as before, message a plain sentence, Details the four parts.
func failure(code apperror.Code, retryable bool, kind ports.ProbeFailureKind, message, declaredPath, resolvedPath, detail, hint string) *apperror.Error {
	details := map[string]string{ports.ProbeDetailFailureKind: string(kind)}
	if declaredPath != "" {
		details[ports.ProbeDetailPath] = declaredPath
	}
	if resolvedPath != "" && resolvedPath != declaredPath {
		details[ports.ProbeDetailResolvedPath] = resolvedPath
	}
	if detail != "" {
		details[ports.ProbeDetailDetail] = detail
	}
	if hint != "" {
		details[ports.ProbeDetailHint] = hint
	}
	return apperror.New(code, message, retryable).WithDetails(details)
}

// credentialInURL matches the user[:password]@ part of a URL, which a Git error can echo from a remote.
var credentialInURL = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s@]+@`)

// cleanStderr redacts what Git printed and bounds it: secrets the prober was told about, then any credential embedded in a URL.
func (p *Prober) cleanStderr(raw []byte) string {
	redacted, _ := p.matcher.Redact(raw)
	text := credentialInURL.ReplaceAllString(string(redacted), "${1}[REDACTED]@")
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if len(text) > maxDetailBytes {
		text = strings.TrimSpace(text[:maxDetailBytes]) + "\n… (cut)"
	}
	return text
}

// shellPath renders a path the way a copy-pasteable command wants it: forward slashes (Git on Windows accepts them), quoted if it has a space.
func shellPath(path string) string {
	slashed := filepath.ToSlash(path)
	if strings.ContainsAny(slashed, " \t") {
		return `"` + slashed + `"`
	}
	return slashed
}

var gitBashDrivePath = regexp.MustCompile(`^/([a-zA-Z])(/.*)?$`)

// gitBashPathHint recognises a Git Bash style drive path (/d/work/app) that Windows programs, aw included, do not understand. It returns the
// native spelling (D:/work/app) so the hint can name it.
func gitBashPathHint(path string) (string, bool) {
	match := gitBashDrivePath.FindStringSubmatch(strings.TrimSpace(path))
	if match == nil {
		return "", false
	}
	return strings.ToUpper(match[1]) + ":" + match[2], true
}

func (p *Prober) pathEmpty() *apperror.Error {
	return failure(apperror.CodeInvalidArgument, false, ports.ProbeFailurePathEmpty,
		"The repository path is empty or contains an invalid character.", "", "", "",
		"Enter the folder that holds the repository, for example D:/work/app or /home/me/app.")
}

func (p *Prober) pathNotFound(declared, absolute string, cause error) *apperror.Error {
	hint := fmt.Sprintf("No folder exists at %s. Check the spelling and that the drive or share is connected.", absolute)
	if runtime.GOOS == "windows" {
		if native, ok := gitBashPathHint(declared); ok {
			hint = fmt.Sprintf("%s is a Git Bash style path, which Windows programs (aw included) cannot open. Use %s instead.", declared, native)
		}
	}
	detail := ""
	if cause != nil {
		detail = p.cleanStderr([]byte(cause.Error()))
	}
	return failure(apperror.CodeNotFound, false, ports.ProbeFailurePathNotFound,
		fmt.Sprintf("The folder %s does not exist.", declared), declared, absolute, detail, hint)
}

func (p *Prober) pathNotDirectory(declared, absolute string) *apperror.Error {
	return failure(apperror.CodeInvalidArgument, false, ports.ProbeFailurePathNotDirectory,
		fmt.Sprintf("%s is a file, not a folder.", declared), declared, absolute, "",
		"Give the folder that contains the repository (the one with a .git entry), not a file inside it.")
}

func (p *Prober) permissionDenied(declared, absolute string, cause error) *apperror.Error {
	return failure(apperror.CodeUnavailable, false, ports.ProbeFailurePermissionDenied,
		fmt.Sprintf("aw is not allowed to read %s.", declared), declared, absolute, p.cleanStderr([]byte(cause.Error())),
		"Run aw as a user that can read the folder, or fix the folder's permissions, then retry the probe.")
}

func (p *Prober) gitNotFound(cause error) *apperror.Error {
	return failure(apperror.CodeUnavailable, true, ports.ProbeFailureGitNotFound,
		"Git could not be started: it is not installed or not on the PATH of the aw worker.", "", "", p.cleanStderr([]byte(cause.Error())),
		"Install Git (git --version must work in the shell that runs aw), restart aw, then retry the probe.")
}

func (p *Prober) timedOut(declared string) *apperror.Error {
	return failure(apperror.CodeUnavailable, true, ports.ProbeFailureTimeout,
		fmt.Sprintf("Git did not answer in time while inspecting %s.", declared), declared, "", "",
		"The folder may be on a slow or disconnected drive. Check it, then retry the probe.")
}

// notAWorkingTree classifies a failed `git rev-parse --show-toplevel` by what Git said (run with LC_ALL=C so the wording is stable).
func (p *Prober) notAWorkingTree(declared, canonical string, stderr []byte) *apperror.Error {
	detail := p.cleanStderr(stderr)
	lower := strings.ToLower(detail)
	path := shellPath(canonical)
	switch {
	case strings.Contains(lower, "dubious ownership") || strings.Contains(lower, "unsafe repository"):
		return failure(apperror.CodeInvalidArgument, false, ports.ProbeFailureDubiousOwnership,
			fmt.Sprintf("Git refuses to use %s because the folder belongs to another user.", declared), declared, canonical, detail,
			fmt.Sprintf("If you trust the folder, run: git config --global --add safe.directory %s — then retry the probe.", path))
	case strings.Contains(lower, "must be run in a work tree") || strings.Contains(lower, "this operation must be run in a work tree"):
		return failure(apperror.CodeInvalidArgument, false, ports.ProbeFailureBareRepository,
			fmt.Sprintf("%s is a bare repository: it has no working tree for agents to edit.", declared), declared, canonical, detail,
			fmt.Sprintf("Register a normal clone instead: git clone %s <folder>, then register <folder>.", path))
	case strings.Contains(lower, "not a git repository"):
		return failure(apperror.CodeInvalidArgument, false, ports.ProbeFailureNotAGitRepository,
			fmt.Sprintf("The folder %s is not a Git repository yet.", declared), declared, canonical, detail,
			fmt.Sprintf("Run: git -C %s init -b main — then add a first commit (git -C %s add -A and git -C %s commit -m \"initial\") — then retry the probe.", path, path, path))
	case strings.Contains(lower, "permission denied") || strings.Contains(lower, "operation not permitted"):
		return p.permissionDenied(declared, canonical, fmt.Errorf("%s", detail))
	}
	return failure(apperror.CodeInvalidArgument, false, ports.ProbeFailureGitFailed,
		fmt.Sprintf("Git could not inspect %s.", declared), declared, canonical, detail,
		"Read the Git output above. Running git -C "+path+" status in a terminal usually shows the same problem.")
}

func (p *Prober) notTopLevel(declared, canonical, topLevel string) *apperror.Error {
	return failure(apperror.CodeInvalidArgument, false, ports.ProbeFailureNotTopLevel,
		fmt.Sprintf("%s is inside a repository, not the root of one.", declared), declared, canonical, "The repository root is "+topLevel+".",
		fmt.Sprintf("Register the root %s instead (or run git -C %s init -b main to make this folder its own repository).", shellPath(topLevel), shellPath(canonical)))
}

func (p *Prober) noCommits(declared, canonical string) *apperror.Error {
	path := shellPath(canonical)
	return failure(apperror.CodeNotFound, false, ports.ProbeFailureNoCommits,
		fmt.Sprintf("The repository at %s has no commits yet.", declared), declared, canonical, "",
		fmt.Sprintf("Make a first commit: git -C %s add -A and git -C %s commit -m \"initial\" — then retry the probe.", path, path))
}

func (p *Prober) refNotFound(declared, canonical, ref string, existing []string) *apperror.Error {
	hint := fmt.Sprintf("Use a ref that exists, or create it: git -C %s branch %s.", shellPath(canonical), ref)
	detail := ""
	if len(existing) > 0 {
		detail = "Existing refs: " + strings.Join(existing, ", ")
		hint = "Use one of the existing refs as the default ref (edit the repository and retry)."
	}
	return failure(apperror.CodeNotFound, false, ports.ProbeFailureRefNotFound,
		fmt.Sprintf("The default ref %q does not exist in %s.", ref, declared), declared, canonical, detail, hint)
}

func (p *Prober) refInvalid(declared, ref string) *apperror.Error {
	return failure(apperror.CodeInvalidArgument, false, ports.ProbeFailureRefInvalid,
		fmt.Sprintf("The default ref %q contains characters a ref cannot have.", ref), declared, "", "",
		"Use a plain branch name such as main, with no spaces or control characters.")
}

func (p *Prober) gitFailed(declared, canonical, message string, stderr []byte) *apperror.Error {
	return failure(apperror.CodeUnavailable, true, ports.ProbeFailureGitFailed, message, declared, canonical, p.cleanStderr(stderr),
		"Read the Git output above, fix the cause and retry the probe.")
}
