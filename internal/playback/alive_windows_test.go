//go:build windows

package playback

import "golang.org/x/sys/windows"

// stillActive is the exit code Windows reports for a running process.
const stillActive = 259

// processAlive reports whether pid is a running process.
func processAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == stillActive
}
