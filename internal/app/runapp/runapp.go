// Package runapp is V12-30 (docs/design/15-v12-ui-operations.md, UX-32, ADR-062): running a repository workspace's application so
// a person can try it by hand. A repository has one profile — its processes (argv, never a shell string; a sub-directory; a port;
// plain environment variables; names of repository secrets, ADR-061), a preview URL and an automatic stop. A session runs that
// profile in one worktree; it is run by `aw worker` (Runner), which kills every process tree on Stop, at the deadline, when the
// worktree stops being READY, when an attempt takes its write lease, and when the worker stops.
//
// One session per repository at a time (the profile's ports are fixed); Start is refused while the worktree is not READY, while an
// attempt holds its write lease, and while another session of the repository is active.
package runapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/secretstore"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// Bounds of a profile.
const (
	DefaultStopAfterMinutes = 120
	MaxStopAfterMinutes     = 24 * 60
	MaxProcesses            = 8
	MaxArgs                 = 64
	MaxEnv                  = 64
	LogTailBytes            = 16 << 10
)

// Errors.
var (
	ErrInvalidProfile = errors.New("runapp: invalid profile")
	ErrNoProfile      = errors.New("runapp: this repository has no Run app profile")
	ErrNotReady       = errors.New("runapp: the repository workspace is not READY")
	ErrWriterActive   = errors.New("runapp: an attempt is writing in the worktree; wait for it to finish")
	ErrAlreadyActive  = errors.New("runapp: an application of this repository is already running (one at a time: its ports are fixed)")
	ErrNotActive      = errors.New("runapp: no application is running for this repository workspace")
)

// Process is one process of a profile.
type Process struct {
	// Name identifies the process (its log); letters, digits, - and _.
	Name string `json:"name"`
	// Argv is the command, argv[0] the executable (looked up in the worker's PATH). Never a shell string.
	Argv []string `json:"argv"`
	// Dir is the sub-directory of the worktree it runs in ("" or "." is the worktree root).
	Dir string `json:"dir,omitempty"`
	// Port is the port it listens on (checked free before Start); 0 when none.
	Port int `json:"port,omitempty"`
	// Env are plain environment variables.
	Env map[string]string `json:"env,omitempty"`
	// SecretNames are repository secrets (ADR-061) given as environment variables of the same name.
	SecretNames []string `json:"secretNames,omitempty"`
}

// Profile is a repository's Run app profile.
type Profile struct {
	Processes []Process `json:"processes"`
	// PreviewURL is the link the UI opens (http or https).
	PreviewURL string `json:"previewUrl,omitempty"`
	// StopAfterMinutes stops the session automatically (default 120, at most 1440).
	StopAfterMinutes int `json:"stopAfterMinutes,omitempty"`
}

var processName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidProfile, fmt.Sprintf(format, args...))
}

// Normalize validates p and fills its defaults.
func Normalize(p Profile) (Profile, error) {
	if len(p.Processes) == 0 || len(p.Processes) > MaxProcesses {
		return Profile{}, invalid("a profile has 1 to %d processes", MaxProcesses)
	}
	if p.StopAfterMinutes == 0 {
		p.StopAfterMinutes = DefaultStopAfterMinutes
	}
	if p.StopAfterMinutes < 1 || p.StopAfterMinutes > MaxStopAfterMinutes {
		return Profile{}, invalid("stopAfterMinutes is 1 to %d", MaxStopAfterMinutes)
	}
	if p.PreviewURL != "" {
		u, err := url.Parse(p.PreviewURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Profile{}, invalid("previewUrl must be an http or https URL")
		}
	}
	names, ports_ := map[string]bool{}, map[int]bool{}
	for i := range p.Processes {
		proc := &p.Processes[i]
		if !processName.MatchString(proc.Name) {
			return Profile{}, invalid("process %d: name is 1 to 32 letters, digits, - or _", i+1)
		}
		if names[proc.Name] {
			return Profile{}, invalid("process name %q is used twice", proc.Name)
		}
		names[proc.Name] = true
		if len(proc.Argv) == 0 || len(proc.Argv) > MaxArgs || strings.TrimSpace(proc.Argv[0]) == "" {
			return Profile{}, invalid("process %s: argv has 1 to %d items, the first one the executable", proc.Name, MaxArgs)
		}
		for _, a := range proc.Argv {
			if strings.ContainsRune(a, 0) {
				return Profile{}, invalid("process %s: argv contains NUL", proc.Name)
			}
		}
		dir := strings.ReplaceAll(strings.TrimSpace(proc.Dir), `\`, "/")
		if dir == "" {
			dir = "."
		}
		dir = path.Clean(dir)
		if path.IsAbs(dir) || dir == ".." || strings.HasPrefix(dir, "../") || strings.Contains(dir, ":") {
			return Profile{}, invalid("process %s: dir must be a sub-directory of the worktree", proc.Name)
		}
		proc.Dir = dir
		if proc.Port < 0 || proc.Port > 65535 {
			return Profile{}, invalid("process %s: port is 1 to 65535", proc.Name)
		}
		if proc.Port != 0 {
			if ports_[proc.Port] {
				return Profile{}, invalid("port %d is used twice", proc.Port)
			}
			ports_[proc.Port] = true
		}
		if len(proc.Env)+len(proc.SecretNames) > MaxEnv {
			return Profile{}, invalid("process %s: at most %d environment variables", proc.Name, MaxEnv)
		}
		for k, v := range proc.Env {
			if secretstore.ValidateName(k) != nil || strings.ContainsRune(v, 0) {
				return Profile{}, invalid("process %s: env %q is not NAME=value", proc.Name, k)
			}
		}
		seen := map[string]bool{}
		for _, s := range proc.SecretNames {
			if secretstore.ValidateName(s) != nil {
				return Profile{}, invalid("process %s: secret name %q is not a variable name", proc.Name, s)
			}
			if _, clash := proc.Env[s]; clash || seen[s] {
				return Profile{}, invalid("process %s: %s is given twice", proc.Name, s)
			}
			seen[s] = true
		}
		sort.Strings(proc.SecretNames)
	}
	return p, nil
}

// Deps is what the commands need.
type Deps struct {
	UnitOfWork ports.UnitOfWork
	// Host reads the log tails (nil: no log is shown).
	Host ports.RunAppHost
	// Now is the clock (nil: time.Now).
	Now func() time.Time
	// NewID names a session (nil: random).
	NewID func() string
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

func checkRepository(ctx context.Context, tx ports.Tx, projectID, repositoryID string) error {
	repo, err := tx.Catalog().GetRepository(ctx, repositoryID)
	if err != nil {
		return err
	}
	if string(repo.ProjectID) != projectID {
		return fmt.Errorf("%w: repository %s", ports.ErrPersistenceNotFound, repositoryID)
	}
	return nil
}

// ProfileView is a repository's profile (Version 0 and no processes: none yet).
type ProfileView struct {
	RepositoryID string    `json:"repositoryId"`
	Profile      *Profile  `json:"profile,omitempty"`
	Version      uint64    `json:"version"`
	UpdatedBy    string    `json:"updatedBy,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt,omitempty"`
}

func profileView(rec ports.RunAppProfileRecord) (ProfileView, error) {
	var p Profile
	if err := json.Unmarshal([]byte(rec.Document), &p); err != nil {
		return ProfileView{}, err
	}
	return ProfileView{RepositoryID: rec.RepositoryID, Profile: &p, Version: rec.Version, UpdatedBy: rec.UpdatedBy, UpdatedAt: rec.UpdatedAt}, nil
}

func getProfile(ctx context.Context, tx ports.Tx, repositoryID string) (ProfileView, error) {
	rec, err := tx.RunApp().GetRunAppProfile(ctx, repositoryID)
	if errors.Is(err, ports.ErrPersistenceNotFound) {
		return ProfileView{RepositoryID: repositoryID}, nil
	}
	if err != nil {
		return ProfileView{}, err
	}
	return profileView(rec)
}

// GetProfile reads a repository's profile.
func GetProfile(ctx context.Context, d Deps, projectID, repositoryID string) (ProfileView, error) {
	var out ProfileView
	err := d.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		if err := checkRepository(ctx, tx, projectID, repositoryID); err != nil {
			return err
		}
		var err error
		out, err = getProfile(ctx, tx, repositoryID)
		return err
	})
	return out, err
}

func replay(ctx context.Context, tx ports.Tx, cmd ports.Command, out any) (bool, error) {
	receipt, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
	if err != nil || !found {
		return false, err
	}
	if receipt.RequestHash != cmd.RequestHash {
		return false, ports.ErrReceiptConflict
	}
	return true, json.Unmarshal([]byte(receipt.ResultJSON), out)
}

func record(ctx context.Context, tx ports.Tx, cmd ports.Command, result any) error {
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return tx.Receipts().Record(ctx, ports.Receipt{
		Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey, CommandType: cmd.Type,
		RequestHash: cmd.RequestHash, ResultJSON: string(body), CreatedAt: cmd.RequestedAt,
	})
}

// SetProfile stores a repository's profile; expectedVersion is the version read (0 for the first one). A running session keeps the
// profile it started with.
func SetProfile(ctx context.Context, d Deps, cmd ports.Command, projectID, repositoryID string, p Profile, expectedVersion uint64) (ProfileView, error) {
	p, err := Normalize(p)
	if err != nil {
		return ProfileView{}, err
	}
	doc, err := json.Marshal(p)
	if err != nil {
		return ProfileView{}, err
	}
	var out ProfileView
	err = d.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if done, err := replay(ctx, tx, cmd, &out); err != nil || done {
			return err
		}
		if err := checkRepository(ctx, tx, projectID, repositoryID); err != nil {
			return err
		}
		rec, err := tx.RunApp().PutRunAppProfile(ctx, ports.RunAppProfileRecord{
			RepositoryID: repositoryID, ProjectID: projectID, Document: string(doc), UpdatedBy: cmd.Actor, UpdatedAt: cmd.RequestedAt,
		}, expectedVersion)
		if err != nil {
			return err
		}
		if out, err = profileView(rec); err != nil {
			return err
		}
		return record(ctx, tx, cmd, out)
	})
	return out, err
}

// SessionView is a session as the UI shows it.
type SessionView struct {
	ID             string     `json:"id"`
	State          string     `json:"state"`
	Active         bool       `json:"active"`
	ProfileVersion uint64     `json:"profileVersion"`
	RequestedBy    string     `json:"requestedBy"`
	WorkerID       string     `json:"workerId,omitempty"`
	DeadlineAt     time.Time  `json:"deadlineAt"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	EndedAt        *time.Time `json:"endedAt,omitempty"`
	Problem        string     `json:"problem,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	RepositoryWsID string     `json:"repositoryWorkspaceId"`
	RepositoryID   string     `json:"repositoryId"`
	Version        uint64     `json:"version"`
}

func sessionView(s ports.RunAppSession) SessionView {
	return SessionView{
		ID: s.ID, State: string(s.State), Active: s.State.Active(), ProfileVersion: s.ProfileVersion, RequestedBy: s.RequestedBy,
		WorkerID: s.WorkerID, DeadlineAt: s.DeadlineAt, StartedAt: s.StartedAt, EndedAt: s.EndedAt, Problem: s.Problem,
		CreatedAt: s.CreatedAt, RepositoryWsID: s.RepositoryWorkspaceID, RepositoryID: s.RepositoryID, Version: s.Version,
	}
}

// ProcessLog is the tail of one process's log (secrets redacted when it was written).
type ProcessLog struct {
	Name string `json:"name"`
	Port int    `json:"port,omitempty"`
	Tail string `json:"tail"`
}

// Status is what the Workspace tab shows.
type Status struct {
	RepositoryWorkspaceID string       `json:"repositoryWorkspaceId"`
	RepositoryID          string       `json:"repositoryId"`
	WorkspaceState        string       `json:"workspaceState"`
	Profile               ProfileView  `json:"profile"`
	Session               *SessionView `json:"session,omitempty"`
	Logs                  []ProcessLog `json:"logs"`
	// StartBlockers say why Start is refused now (empty: it can start).
	StartBlockers []string `json:"startBlockers"`
}

type loaded struct {
	rw      workspace.RepositoryWorkspace
	profile ProfileView
	latest  *ports.RunAppSession
	writer  bool
	// otherActive is an active session of the same repository in another worktree.
	otherActive *ports.RunAppSession
}

func load(ctx context.Context, tx ports.Tx, projectID, repositoryWorkspaceID string) (loaded, error) {
	var l loaded
	record, err := tx.Work().GetRepositoryWorkspaceByID(ctx, repositoryWorkspaceID)
	if err != nil {
		return l, err
	}
	l.rw = record.Workspace
	if err := checkRepository(ctx, tx, projectID, string(l.rw.RepositoryID)); err != nil {
		return l, fmt.Errorf("%w: repository workspace %s", ports.ErrPersistenceNotFound, repositoryWorkspaceID)
	}
	if l.profile, err = getProfile(ctx, tx, string(l.rw.RepositoryID)); err != nil {
		return l, err
	}
	if s, err := tx.RunApp().LatestRunAppSession(ctx, repositoryWorkspaceID); err == nil {
		l.latest = &s
	} else if !errors.Is(err, ports.ErrPersistenceNotFound) {
		return l, err
	}
	if l.writer, err = tx.Work().HasActiveWriteLease(ctx, []string{repositoryWorkspaceID}); err != nil {
		return l, err
	}
	active, err := tx.RunApp().ListRunAppSessionsInStates(ctx, []ports.RunAppSessionState{ports.RunAppRequested, ports.RunAppRunning, ports.RunAppStopRequested})
	if err != nil {
		return l, err
	}
	for i := range active {
		if active[i].RepositoryID == string(l.rw.RepositoryID) {
			l.otherActive = &active[i]
			break
		}
	}
	return l, nil
}

func (l loaded) blockers() []string {
	out := []string{}
	if l.profile.Profile == nil {
		out = append(out, ErrNoProfile.Error())
	}
	if l.rw.State != workspace.RepositoryWorkspaceReady {
		out = append(out, fmt.Sprintf("%s (it is %s)", ErrNotReady.Error(), l.rw.State))
	}
	if l.writer {
		out = append(out, ErrWriterActive.Error())
	}
	if l.otherActive != nil {
		msg := ErrAlreadyActive.Error()
		if l.otherActive.RepositoryWorkspaceID != string(l.rw.ID) {
			msg += " — in repository workspace " + l.otherActive.RepositoryWorkspaceID
		}
		out = append(out, msg)
	}
	return out
}

// GetStatus reads the profile, the latest session and its log tails.
func GetStatus(ctx context.Context, d Deps, projectID, repositoryWorkspaceID string) (Status, error) {
	var l loaded
	if err := d.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		l, err = load(ctx, tx, projectID, repositoryWorkspaceID)
		return err
	}); err != nil {
		return Status{}, err
	}
	st := Status{
		RepositoryWorkspaceID: repositoryWorkspaceID, RepositoryID: string(l.rw.RepositoryID), WorkspaceState: string(l.rw.State),
		Profile: l.profile, Logs: []ProcessLog{}, StartBlockers: l.blockers(),
	}
	if l.latest != nil {
		v := sessionView(*l.latest)
		st.Session = &v
		if d.Host != nil && l.profile.Profile != nil {
			for _, p := range l.profile.Profile.Processes {
				tail, _ := d.Host.ReadLogTail(l.latest.ID, p.Name, LogTailBytes)
				st.Logs = append(st.Logs, ProcessLog{Name: p.Name, Port: p.Port, Tail: tail})
			}
		}
	}
	return st, nil
}

// Start asks a worker to run the profile in the repository workspace. The worker checks the ports and resolves the secrets; a problem
// there ends the session FAILED with the reason.
func Start(ctx context.Context, d Deps, cmd ports.Command, projectID, repositoryWorkspaceID string) (SessionView, error) {
	var out SessionView
	err := d.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if done, err := replay(ctx, tx, cmd, &out); err != nil || done {
			return err
		}
		l, err := load(ctx, tx, projectID, repositoryWorkspaceID)
		if err != nil {
			return err
		}
		switch {
		case l.profile.Profile == nil:
			return ErrNoProfile
		case l.rw.State != workspace.RepositoryWorkspaceReady:
			return fmt.Errorf("%w (it is %s)", ErrNotReady, l.rw.State)
		case l.writer:
			return ErrWriterActive
		case l.otherActive != nil:
			return ErrAlreadyActive
		}
		id := randomID()
		if d.NewID != nil {
			id = d.NewID()
		}
		now := d.now()
		session, err := tx.RunApp().CreateRunAppSession(ctx, ports.RunAppSession{
			ID: id, ProjectID: projectID, RepositoryID: string(l.rw.RepositoryID), RepositoryWorkspaceID: repositoryWorkspaceID,
			State: ports.RunAppRequested, ProfileVersion: l.profile.Version, RequestedBy: cmd.Actor,
			DeadlineAt: now.Add(time.Duration(l.profile.Profile.StopAfterMinutes) * time.Minute), CreatedAt: now,
		})
		if errors.Is(err, ports.ErrOptimisticConflict) {
			return ErrAlreadyActive
		}
		if err != nil {
			return err
		}
		out = sessionView(session)
		return record(ctx, tx, cmd, out)
	})
	return out, err
}

// Stop asks the worker to stop the active session of the repository workspace (a session no worker took yet ends at once).
func Stop(ctx context.Context, d Deps, cmd ports.Command, projectID, repositoryWorkspaceID string) (SessionView, error) {
	var out SessionView
	err := d.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if done, err := replay(ctx, tx, cmd, &out); err != nil || done {
			return err
		}
		l, err := load(ctx, tx, projectID, repositoryWorkspaceID)
		if err != nil {
			return err
		}
		if l.latest == nil || !l.latest.State.Active() {
			return ErrNotActive
		}
		s := *l.latest
		switch s.State {
		case ports.RunAppRequested:
			now := d.now()
			s.State, s.EndedAt, s.Problem = ports.RunAppStopped, &now, "stopped by "+cmd.Actor+" before a worker started it"
		case ports.RunAppRunning:
			s.State = ports.RunAppStopRequested
		}
		if s.State != l.latest.State {
			if s, err = tx.RunApp().UpdateRunAppSession(ctx, s, l.latest.Version); err != nil {
				return err
			}
		}
		out = sessionView(s)
		return record(ctx, tx, cmd, out)
	})
	return out, err
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "runapp-" + hex.EncodeToString(b)
}
