//go:build windows

package cmd

import (
	"golang.org/x/sys/windows"
)

// isProcessAlive checks if a process with the given PID is still running.
// On Windows, we attempt to open the process with limited query access.
// If successful, the process exists.
func isProcessAlive(pid int) bool {
	// PROCESS_QUERY_LIMITED_INFORMATION is the minimum access right needed
	// to query basic process information. Available since Windows Vista.
	const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000

	handle, err := windows.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Process doesn't exist or we don't have access
		return false
	}
	windows.CloseHandle(handle)
	return true
}
