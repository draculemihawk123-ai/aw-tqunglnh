package gitworktree

import (
	"bytes"
	"fmt"
	"path"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func parsePorcelainV1Z(raw []byte) ([]ports.FileStatus, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	records := bytes.Split(raw, []byte{0})
	if len(records) == 0 || len(records[len(records)-1]) != 0 {
		return nil, fmt.Errorf("%w: unterminated Git status output", ErrGit)
	}
	records = records[:len(records)-1]

	statuses := make([]ports.FileStatus, 0, len(records))
	for index := 0; index < len(records); index++ {
		record := records[index]
		if len(record) < 4 || record[2] != ' ' {
			return nil, fmt.Errorf("%w: malformed Git status record", ErrGit)
		}
		code := string(record[:2])
		filePath, err := normalizeReportedPath(string(record[3:]))
		if err != nil {
			return nil, err
		}
		status := ports.FileStatus{Code: code, Path: filePath}
		if strings.ContainsAny(code, "RC") {
			index++
			if index >= len(records) {
				return nil, fmt.Errorf("%w: missing original path for rename/copy", ErrGit)
			}
			originalPath, err := normalizeReportedPath(string(records[index]))
			if err != nil {
				return nil, err
			}
			status.OriginalPath = originalPath
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func normalizeReportedPath(raw string) (string, error) {
	if raw == "" || strings.IndexByte(raw, 0) >= 0 {
		return "", fmt.Errorf("%w: empty or invalid Git status path", ErrGit)
	}
	normalized := strings.ReplaceAll(raw, "\\", "/")
	if strings.HasPrefix(normalized, "/") || (len(normalized) >= 2 && normalized[1] == ':') {
		return "", fmt.Errorf("%w: Git reported an absolute path", ErrUnsafePath)
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return "", fmt.Errorf("%w: Git reported parent traversal", ErrUnsafePath)
		}
	}
	normalized = path.Clean(normalized)
	if normalized == "." || normalized == "" {
		return "", fmt.Errorf("%w: Git reported an invalid path", ErrUnsafePath)
	}
	return normalized, nil
}
