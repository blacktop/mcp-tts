//go:build windows

package playback

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// procThreadAttributeJobList is PROC_THREAD_ATTRIBUTE_JOB_LIST, which
// golang.org/x/sys/windows does not define. It places a new process in the
// listed jobs as part of its creation.
const procThreadAttributeJobList = 0x0002000D

const outputDrainDelay = time.Second

// exitError reports a player that exited with a nonzero status.
type exitError struct {
	code uint32
}

func (e *exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

type windowsProc struct {
	processID int
	output    <-chan struct{}

	mu      sync.Mutex
	process windows.Handle
	job     windows.Handle
	closed  bool
}

// startOS creates the player inside a kill-on-close Job Object.
//
// The job is assigned at creation, not afterwards: a player that existed for
// even an instant outside the job would survive this process dying in that
// instant. The job handle is not inheritable and the player inherits only its
// three standard handles, so this process holds the only reference to the job
// and the kernel ends the player when this process goes away.
func startOS(spec Spec, output io.Writer, wantStdout bool) (osProc, io.ReadCloser, error) {
	if spec.Stdin != nil {
		return nil, nil, errors.New("standard input is not supported for playback processes on Windows")
	}
	job, err := newKillOnCloseJob()
	if err != nil {
		return nil, nil, err
	}
	stdio, err := newChildStdio(wantStdout)
	if err != nil {
		windows.CloseHandle(job)
		return nil, nil, err
	}

	process, processID, err := createInJob(spec, job, stdio)
	stdio.closeChildEnds()
	if err != nil {
		stdio.closeParentEnds()
		windows.CloseHandle(job)
		return nil, nil, err
	}

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(output, stdio.errRead)
		stdio.errRead.Close()
	}()
	proc := &windowsProc{processID: processID, output: drained, process: process, job: job}
	if stdio.outRead == nil {
		return proc, nil, nil
	}
	return proc, stdio.outRead, nil
}

func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("creating job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("configuring job object: %w", err)
	}
	return job, nil
}

// childStdio holds the player's standard handles. The player's stderr, and its
// stdout unless the caller reads it, feed one pipe drained into the bounded
// output buffer.
type childStdio struct {
	null     *os.File
	errRead  *os.File
	errWrite *os.File
	outRead  *os.File
	outWrite *os.File
}

func newChildStdio(wantStdout bool) (*childStdio, error) {
	stdio := &childStdio{}
	var err error
	if stdio.null, err = os.OpenFile(os.DevNull, os.O_RDWR, 0); err != nil {
		return nil, err
	}
	if stdio.errRead, stdio.errWrite, err = os.Pipe(); err != nil {
		stdio.closeChildEnds()
		return nil, err
	}
	if wantStdout {
		if stdio.outRead, stdio.outWrite, err = os.Pipe(); err != nil {
			stdio.closeChildEnds()
			stdio.closeParentEnds()
			return nil, err
		}
	}
	for _, file := range stdio.inherited() {
		handle := windows.Handle(file.Fd())
		if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			stdio.closeChildEnds()
			stdio.closeParentEnds()
			return nil, fmt.Errorf("marking a standard handle inheritable: %w", err)
		}
	}
	return stdio, nil
}

// inherited lists each handle the player receives exactly once; a handle list
// with duplicates is rejected by CreateProcess.
func (s *childStdio) inherited() []*os.File {
	files := []*os.File{s.null, s.errWrite}
	if s.outWrite != nil {
		files = append(files, s.outWrite)
	}
	return files
}

func (s *childStdio) stdout() *os.File {
	if s.outWrite != nil {
		return s.outWrite
	}
	return s.errWrite
}

func (s *childStdio) closeChildEnds() {
	for _, file := range []*os.File{s.null, s.errWrite, s.outWrite} {
		if file != nil {
			file.Close()
		}
	}
}

func (s *childStdio) closeParentEnds() {
	for _, file := range []*os.File{s.errRead, s.outRead} {
		if file != nil {
			file.Close()
		}
	}
}

func createInJob(spec Spec, job windows.Handle, stdio *childStdio) (windows.Handle, int, error) {
	jobs := []windows.Handle{job}
	inherit := make([]windows.Handle, 0, 3)
	for _, file := range stdio.inherited() {
		inherit = append(inherit, windows.Handle(file.Fd()))
	}

	attrs, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return 0, 0, fmt.Errorf("allocating process attributes: %w", err)
	}
	defer attrs.Delete()
	if err := attrs.Update(procThreadAttributeJobList,
		unsafe.Pointer(&jobs[0]), uintptr(len(jobs))*unsafe.Sizeof(jobs[0])); err != nil {
		return 0, 0, fmt.Errorf("setting the job list attribute: %w", err)
	}
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
		unsafe.Pointer(&inherit[0]), uintptr(len(inherit))*unsafe.Sizeof(inherit[0])); err != nil {
		return 0, 0, fmt.Errorf("setting the handle list attribute: %w", err)
	}

	startup := new(windows.StartupInfoEx)
	startup.Cb = uint32(unsafe.Sizeof(*startup))
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.StdInput = windows.Handle(stdio.null.Fd())
	startup.StdOutput = windows.Handle(stdio.stdout().Fd())
	startup.StdErr = windows.Handle(stdio.errWrite.Fd())
	startup.ProcThreadAttributeList = attrs.List()

	appName, err := windows.UTF16PtrFromString(spec.Path)
	if err != nil {
		return 0, 0, err
	}
	commandLine, err := windows.UTF16PtrFromString(
		windows.ComposeCommandLine(append([]string{spec.Path}, spec.Args...)))
	if err != nil {
		return 0, 0, err
	}

	var info windows.ProcessInformation
	const flags = windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_NO_WINDOW | windows.CREATE_UNICODE_ENVIRONMENT
	err = windows.CreateProcess(appName, commandLine, nil, nil, true, flags, nil, nil, &startup.StartupInfo, &info)
	runtime.KeepAlive(jobs)
	runtime.KeepAlive(inherit)
	runtime.KeepAlive(stdio)
	if err != nil {
		return 0, 0, err
	}
	windows.CloseHandle(info.Thread)
	return info.Process, int(info.ProcessId), nil
}

func (w *windowsProc) pid() int { return w.processID }

// terminate has no gentler form for a windowless process, so it ends the job.
func (w *windowsProc) terminate() error { return w.kill() }

func (w *windowsProc) kill() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	return windows.TerminateJobObject(w.job, 1)
}

func (w *windowsProc) wait() error {
	if _, err := windows.WaitForSingleObject(w.process, windows.INFINITE); err != nil {
		return fmt.Errorf("waiting for the player: %w", err)
	}
	var code uint32
	codeErr := windows.GetExitCodeProcess(w.process, &code)

	select {
	case <-w.output:
	case <-time.After(outputDrainDelay):
	}

	w.mu.Lock()
	windows.CloseHandle(w.process)
	windows.CloseHandle(w.job)
	w.closed = true
	w.mu.Unlock()

	if codeErr != nil {
		return fmt.Errorf("reading the player's exit status: %w", codeErr)
	}
	if code != 0 {
		return &exitError{code: code}
	}
	return nil
}
