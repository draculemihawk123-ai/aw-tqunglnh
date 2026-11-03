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
