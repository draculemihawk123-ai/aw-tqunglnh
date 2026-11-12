//go:build darwin

package runapphost

import (
	"os/exec"
	"strconv"
	"strings"
)

func psField(pid int, field string) (string, bool) {
	out, err := exec.Command("ps", "-o", field+"=", "-p", strconv.Itoa(pid)).Output()
	v := strings.TrimSpace(string(out))
	return v, err == nil && v != ""
}

// startTime is ps's lstart: with the pid it identifies one process.
func startTime(pid int) (string, bool) { return psField(pid, "lstart") }

func processName(pid int) string {
	name, _ := psField(pid, "comm")
	return name
}

// portHolder asks lsof for the process listening on the TCP port.
func portHolder(port int) (int, bool) {
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t").Output()
	if err != nil {
		return 0, false
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(first))
	return pid, err == nil && pid > 0
}
