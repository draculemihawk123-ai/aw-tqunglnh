package ports

import (
	"context"
	"io"
	"time"
)

// V12-30 (ADR-062): running a worktree's application for manual QA.

// RunAppSessionState is the state of a run-app session.
type RunAppSessionState string

const (
	RunAppRequested     RunAppSessionState = "REQUESTED"
	RunAppRunning       RunAppSessionState = "RUNNING"
	RunAppStopRequested RunAppSessionState = "STOP_REQUESTED"
	RunAppStopped       RunAppSessionState = "STOPPED"
	RunAppFailed        RunAppSessionState = "FAILED"
)

// Active reports whether the session still holds its repository's ports.
func (s RunAppSessionState) Active() bool {
	return s == RunAppRequested || s == RunAppRunning || s == RunAppStopRequested
}

// RunAppProfileRecord is a repository's stored profile (Document is the validated JSON).
type RunAppProfileRecord struct {
	RepositoryID string
	ProjectID    string
	Document     string
	Version      uint64
	UpdatedBy    string
	UpdatedAt    time.Time
}

// RunAppSession is one run of a profile.
type RunAppSession struct {
	ID                    string
	ProjectID             string
	RepositoryID          string
	RepositoryWorkspaceID string
	State                 RunAppSessionState
	ProfileVersion        uint64
	RequestedBy           string
	WorkerID              string
	DeadlineAt            time.Time
	StartedAt             *time.Time
	EndedAt               *time.Time
	Problem               string
	Version               uint64
	CreatedAt             time.Time
}

// RunAppRepository is the Tx accessor of run-app profiles and sessions.
type RunAppRepository interface {
	GetRunAppProfile(ctx context.Context, repositoryID string) (RunAppProfileRecord, error)
	PutRunAppProfile(ctx context.Context, record RunAppProfileRecord, expectedVersion uint64) (RunAppProfileRecord, error)
	// CreateRunAppSession inserts a session; ErrOptimisticConflict when the repository already has an active one.
	CreateRunAppSession(ctx context.Context, session RunAppSession) (RunAppSession, error)
	GetRunAppSession(ctx context.Context, id string) (RunAppSession, error)
	// LatestRunAppSession returns the newest session of a repository workspace, or ErrPersistenceNotFound.
	LatestRunAppSession(ctx context.Context, repositoryWorkspaceID string) (RunAppSession, error)
	// ListRunAppSessionsInStates returns the sessions in any of states, oldest first.
	ListRunAppSessionsInStates(ctx context.Context, states []RunAppSessionState) ([]RunAppSession, error)
	// UpdateRunAppSession replaces the session when its stored version is expectedVersion (ErrOptimisticConflict otherwise).
	UpdateRunAppSession(ctx context.Context, session RunAppSession, expectedVersion uint64) (RunAppSession, error)
}

// RunAppHost is the machine side the worker needs: free ports, and a log per process.
type RunAppHost interface {
	// PortInUse reports whether something listens on 127.0.0.1:port (or any address).
	PortInUse(port int) bool
	// OpenLog opens the bounded log of one process of a session for writing (truncating it).
	OpenLog(sessionID, process string) (io.WriteCloser, error)
	// ReadLogTail returns the last bytes of a process's log (empty when there is none).
	ReadLogTail(sessionID, process string, maxBytes int) (string, error)
	// PortHolder names the process listening on the port ("java (pid 4120)"), or "" when it cannot tell. It only reports.
	PortHolder(port int) string
	// RecordWorker records this process (pid and start time) as the worker running the session.
	RecordWorker(sessionID string) error
	// RecordProcess records a started process of the session (pid and start time).
	RecordProcess(sessionID, process string, pid int) error
	// WorkerGone reports whether the session's recorded worker process no longer runs (false when none was recorded).
	WorkerGone(sessionID string) bool
	// ReapSession ends every recorded process tree of the session that still runs (same pid and start time) and names them.
	ReapSession(sessionID string) []string
}
