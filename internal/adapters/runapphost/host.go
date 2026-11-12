// Package runapphost is the machine side of V12-30 (ADR-062): whether a port is free, and a log file per process of a Run app session
// under <artifact-root>/run-app/<session>/<process>.log. The worker writes the logs; `aw serve` (same artifact root) reads their tails.
package runapphost

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Host implements ports.RunAppHost.
type Host struct {
	root string
}

// New roots the logs in artifactRoot.
func New(artifactRoot string) Host { return Host{root: filepath.Join(artifactRoot, "run-app")} }

var safe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func (h Host) path(sessionID, process string) (string, error) {
	if !safe.MatchString(sessionID) || !safe.MatchString(process) {
		return "", fmt.Errorf("runapphost: invalid session %q or process %q", sessionID, process)
	}
	return filepath.Join(h.root, sessionID, process+".log"), nil
}

// PortInUse reports whether the port cannot be bound (on every address or on loopback): something listens there.
func (Host) PortInUse(port int) bool {
	for _, addr := range []string{":" + strconv.Itoa(port), "127.0.0.1:" + strconv.Itoa(port)} {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return true
		}
		_ = l.Close()
	}
	return false
}

// OpenLog creates (truncating) the log of one process, readable only by this user.
func (h Host) OpenLog(sessionID, process string) (io.WriteCloser, error) {
	p, err := h.path(sessionID, process)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
}

// ReadLogTail returns the last maxBytes of a process's log ("" when there is none).
func (h Host) ReadLogTail(sessionID, process string, maxBytes int) (string, error) {
	p, err := h.path(sessionID, process)
	if err != nil {
		return "", err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	offset := info.Size() - int64(maxBytes)
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, info.Size()-offset)
	n, err := f.ReadAt(buf, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return string(buf[:n]), nil
}

// workerRecord names the record of the worker running a session; a process name never has a dot, so they cannot collide.
const workerRecord = "aw.worker"

// V12-30: which process holds a port, and the processes of a session, so a worker that starts again can end what a dead worker
// left running (an orphan backend still holding 8080).

// PortHolder implements ports.RunAppHost.
func (Host) PortHolder(port int) string {
	pid, ok := portHolder(port)
	if !ok || pid <= 0 {
		return ""
	}
	if name := processName(pid); name != "" {
		return fmt.Sprintf("%s (pid %d)", name, pid)
	}
	return fmt.Sprintf("pid %d", pid)
}

func (h Host) recordPath(sessionID, name string) (string, error) {
	if !safe.MatchString(sessionID) || !safe.MatchString(name) {
		return "", fmt.Errorf("runapphost: invalid session %q or process %q", sessionID, name)
	}
	return filepath.Join(h.root, sessionID, name+".pid"), nil
}

func (h Host) record(sessionID, name string, pid int) error {
	p, err := h.recordPath(sessionID, name)
	if err != nil {
		return err
	}
	start, ok := startTime(pid)
	if !ok {
		return fmt.Errorf("runapphost: cannot read the start time of pid %d", pid)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strconv.Itoa(pid)+"\n"+start+"\n"), 0o600)
}

// running reads a record and reports whether that very process still runs.
func running(path string) (int, bool, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false, false
	}
	pidText, start, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(pidText))
	if err != nil || pid <= 0 {
		return 0, false, false
	}
	now, ok := startTime(pid)
	return pid, true, ok && now == strings.TrimSpace(start)
}

// RecordWorker implements ports.RunAppHost.
func (h Host) RecordWorker(sessionID string) error {
	return h.record(sessionID, workerRecord, os.Getpid())
}

// RecordProcess implements ports.RunAppHost.
func (h Host) RecordProcess(sessionID, process string, pid int) error {
	return h.record(sessionID, process, pid)
}

// WorkerGone implements ports.RunAppHost.
func (h Host) WorkerGone(sessionID string) bool {
	p, err := h.recordPath(sessionID, workerRecord)
	if err != nil {
		return false
	}
	_, recorded, alive := running(p)
	return recorded && !alive
}

// ReapSession implements ports.RunAppHost.
func (h Host) ReapSession(sessionID string) []string {
	if !safe.MatchString(sessionID) {
		return nil
	}
	records, _ := filepath.Glob(filepath.Join(h.root, sessionID, "*.pid"))
	var out []string
	for _, rec := range records {
		name := strings.TrimSuffix(filepath.Base(rec), ".pid")
		if name == workerRecord {
			continue
		}
		pid, recorded, alive := running(rec)
		if !recorded || (!alive && !orphanedGroup(pid)) {
			continue
		}
		killTree(pid)
		out = append(out, fmt.Sprintf("%s (pid %d)", name, pid))
	}
	return out
}
