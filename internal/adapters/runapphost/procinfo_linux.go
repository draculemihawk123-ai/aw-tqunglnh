//go:build linux

package runapphost

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// startTime is field 22 of /proc/<pid>/stat (clock ticks since boot): with the pid it identifies one process, even after pid reuse.
func startTime(pid int) (string, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", false
	}
	s := string(data)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", false
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) < 20 {
		return "", false
	}
	return fields[19], true
}

func processName(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// portHolder finds the process listening on the TCP port: the socket inode in /proc/net/tcp and tcp6, then the process whose fd
// table holds it. Processes of other users are not readable without privileges: then it is unknown.
func portHolder(port int) (int, bool) {
	inodes := map[string]bool{}
	want := fmt.Sprintf("%04X", port)
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(table)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Scan() // header
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 10 || fields[3] != "0A" { // 0A: LISTEN
				continue
			}
			if _, p, ok := strings.Cut(fields[1], ":"); ok && p == want {
				inodes["socket:["+fields[9]+"]"] = true
			}
		}
		f.Close()
	}
	if len(inodes) == 0 {
		return 0, false
	}
	procs, _ := filepath.Glob("/proc/[0-9]*")
	for _, dir := range procs {
		pid, err := strconv.Atoi(filepath.Base(dir))
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			if target, err := os.Readlink(filepath.Join(dir, "fd", fd.Name())); err == nil && inodes[target] {
				return pid, true
			}
		}
	}
	return 0, false
}
