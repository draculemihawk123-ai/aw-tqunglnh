//go:build windows

package runapphost

import (
	"encoding/binary"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetExtendedTcpTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

const tcpTableOwnerPIDListener = 3

// startTime is the process creation time (GetProcessTimes).
func startTime(pid int) (string, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(h)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return "", false
	}
	return strconv.FormatInt(creation.Nanoseconds(), 10), true
}

func processName(pid int) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	path := windows.UTF16ToString(buf[:size])
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '\\' || path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

// tcpTable returns GetExtendedTcpTable(TCP_TABLE_OWNER_PID_LISTENER) for one address family.
func tcpTable(family uint32) []byte {
	var size uint32
	_, _, _ = procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(family), tcpTableOwnerPIDListener, 0)
	if size == 0 {
		return nil
	}
	buf := make([]byte, size)
	r, _, _ := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, uintptr(family), tcpTableOwnerPIDListener, 0)
	if r != 0 {
		return nil
	}
	return buf[:size]
}

// portHolder reads the listening TCP tables (IPv4 MIB_TCPROW_OWNER_PID, IPv6 MIB_TCP6ROW_OWNER_PID) for the port's owning pid.
func portHolder(port int) (int, bool) {
	for _, t := range []struct {
		family                       uint32
		rowSize, portOffset, pidOffs int
	}{{windows.AF_INET, 24, 8, 20}, {windows.AF_INET6, 56, 20, 52}} {
		buf := tcpTable(t.family)
		if len(buf) < 4 {
			continue
		}
		n := int(binary.LittleEndian.Uint32(buf[:4]))
		for i := 0; i < n; i++ {
			row := buf[4+i*t.rowSize:]
			if len(row) < t.rowSize {
				break
			}
			// The port is in network byte order in the low 16 bits.
			p := int(row[t.portOffset])<<8 | int(row[t.portOffset+1])
			if p == port {
				return int(binary.LittleEndian.Uint32(row[t.pidOffs:])), true
			}
		}
	}
	return 0, false
}

// killTree terminates the recorded process. Its descendants normally died with the worker already (the supervisor's Job Object kills
// the tree when the worker exits).
func killTree(pid int) {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	_ = windows.TerminateProcess(h, 1)
}

// orphanedGroup: Windows has no process groups to find again; the Job Object already ended the tree with the worker.
func orphanedGroup(int) bool { return false }
