//go:build darwin

package cmd

import (
	"bufio"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
)

const (
	launchctlPath = "/bin/launchctl"
	sayBinPath    = "/usr/bin/say"
	afplayBinPath = "/usr/bin/afplay"

	guiLabelPrefix = "com.blacktop.mcp-tts"

	guiPollInterval = 50 * time.Millisecond
	guiJobTimeout   = 120 * time.Second
)

// sessionMode classifies the launchd audio context of this process.
type sessionMode int

const (
	sessionAqua     sessionMode = iota // GUI login session; play in-process / direct exec.
	sessionRoutable                    // Background session with a reachable gui/<uid> domain.
	sessionHeadless                    // No reachable GUI session; local audio impossible.
)

var (
	guiLabelCounter   atomic.Uint64
	sessionModeOnce   sync.Once
	cachedSessionMode sessionMode
	cachedManagerName string
)

// sessionModeFn is the seam used by the say handler, routeCloudPlayback, and the
// stale-job sweep so tests can pin the session classification without touching
// launchctl. Production code resolves it via currentSessionMode.
var sessionModeFn = currentSessionMode

const headlessTemplate = `macOS audio is unavailable: this process is running in the launchd "%s" session, which has no route to the GUI audio system, and no Aqua login session (gui/%d) is reachable.
Local speech and playback only work from a GUI login session. To fix this, either:
  - run mcp-tts from a GUI terminal (Terminal.app or iTerm launched from the Dock/Finder), or restart your tmux server from such a terminal so its children inherit the GUI session;
  - log in at the console (or via Screen Sharing) so a GUI session exists; or
  - set MCP_TTS_OUTPUT_DIR (and MCP_TTS_NO_PLAY=1) to save audio files instead of playing them, or use a cloud TTS provider with MCP_TTS_OUTPUT_DIR for file output.`

// currentSessionMode returns the cached launchd audio session classification.
// The launchd session does not change for the process lifetime, so detection
// runs once.
func currentSessionMode() (sessionMode, string) {
	sessionModeOnce.Do(func() {
		cachedSessionMode, cachedManagerName = detectSessionMode()
	})
	return cachedSessionMode, cachedManagerName
}

// detectSessionMode classifies this process's launchd audio context by reading
// `launchctl managername` and, when not Aqua, probing the reachability of the
// user's gui/<uid> domain.
func detectSessionMode() (sessionMode, string) {
	name := managerName()
	if name == "Aqua" {
		return sessionAqua, name
	}
	if guiDomainReachable() {
		return sessionRoutable, name
	}
	return sessionHeadless, name
}

// managerName returns the launchd job-manager name for this process
// ("Aqua", "Background", "LoginWindow", "System", ...). On any error it returns
// "Background" so callers fall through to the routing/headless branches rather
// than wrongly assuming in-process audio works.
func managerName() string {
	out, err := exec.Command(launchctlPath, "managername").CombinedOutput()
	if err != nil {
		log.Debug("launchctl managername failed; assuming non-Aqua session",
			"error", err, "output", strings.TrimSpace(string(out)))
		return "Background"
	}
	return parseManagerName(string(out))
}

// parseManagerName trims the raw `launchctl managername` output to the bare name.
func parseManagerName(out string) string {
	return strings.TrimSpace(out)
}

// guiDomainReachable reports whether `launchctl print gui/<uid>` succeeds,
// which is true iff an Aqua login session exists for this uid.
func guiDomainReachable() bool {
	out, err := exec.Command(launchctlPath, "print", guiDomainTarget()).CombinedOutput()
	if err != nil {
		log.Debug("gui domain unreachable", "target", guiDomainTarget(),
			"error", err, "output", strings.TrimSpace(string(out)))
		return false
	}
	return true
}

func guiDomainTarget() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

func guiServiceTarget(label string) string {
	return fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
}

// nextGUILabel returns a process-unique launchd label of the form
// com.blacktop.mcp-tts.<pid>.<n>. Uniqueness guarantees the exit-code poll can
// never read a previous run's result, and the <pid> segment lets the startup
// sweep identify leftovers from dead mcp-tts processes.
func nextGUILabel() string {
	return fmt.Sprintf("%s.%d.%d", guiLabelPrefix, os.Getpid(), guiLabelCounter.Add(1))
}

// guiJobSpec is the controlled input for a transient launchd one-shot plist.
type guiJobSpec struct {
	Label             string
	ProgramArguments  []string
	StandardErrorPath string
}

// writePlist renders a minimal launchd job plist for a transient one-shot:
// Label, ProgramArguments, RunAtLoad=true, AbandonProcessGroup=true,
// ProcessType=Interactive, and StandardErrorPath. Every string is escaped via
// xml.EscapeText so controlled values can never break the document.
func writePlist(w io.Writer, spec guiJobSpec) error {
	var firstErr error
	write := func(s string) {
		if firstErr != nil {
			return
		}
		if _, err := io.WriteString(w, s); err != nil {
			firstErr = err
		}
	}
	escAttr := func(s string) {
		if firstErr != nil {
			return
		}
		if err := xml.EscapeText(w, []byte(s)); err != nil {
			firstErr = err
		}
	}
	writeKVString := func(key, val string) {
		write("<key>" + key + "</key><string>")
		escAttr(val)
		write("</string>\n")
	}

	write(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	write(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	write(`<plist version="1.0">` + "\n<dict>\n")
	writeKVString("Label", spec.Label)
	write("<key>ProgramArguments</key>\n<array>\n")
	for _, arg := range spec.ProgramArguments {
		write("<string>")
		escAttr(arg)
		write("</string>\n")
	}
	write("</array>\n")
	write("<key>RunAtLoad</key><true/>\n")
	write("<key>AbandonProcessGroup</key><true/>\n")
	writeKVString("ProcessType", "Interactive")
	writeKVString("StandardErrorPath", spec.StandardErrorPath)
	write("</dict>\n</plist>\n")
	return firstErr
}

// guiJobHandle identifies a bootstrapped (running) GUI job so a caller can wait
// for it after the synchronous start succeeded.
type guiJobHandle struct {
	label   string
	program string
	dir     string
	errPath string
}

// startGUIJobFn and waitGUIJobFn are the launchctl-touching seams used by the
// say handler and cloud playback so tests can fake GUI execution (bootstrap and
// wait) without a real GUI session.
var (
	startGUIJobFn = startGUIJob
	waitGUIJobFn  = waitGUIJob
)

// runInGUISession runs a one-shot command (program + args, e.g. /usr/bin/say or
// /usr/bin/afplay) inside the user's gui/<uid> Aqua domain via a transient
// launchd job, blocks until it exits, and returns the job's error. It always
// boots the job out and removes its temp plist + stderr file. ctx cancellation
// and an internal timeout both stop the job and clean up.
func runInGUISession(ctx context.Context, program string, args []string) error {
	handle, err := startGUIJobFn(ctx, program, args)
	if err != nil {
		return err
	}
	return waitGUIJobFn(ctx, handle)
}

// startGUIJob renders a transient launchd plist and bootstraps it into the
// gui/<uid> domain synchronously, returning a handle to wait on. A bootstrap
// failure is returned to the caller (the GUI-session analog of cmd.Start), so a
// routable play path can surface it instead of silently reporting success. On
// any error it removes the scratch dir it created.
func startGUIJob(ctx context.Context, program string, args []string) (guiJobHandle, error) {
	label := nextGUILabel()
	dir, err := os.MkdirTemp("", "mcp-tts-gui-")
	if err != nil {
		return guiJobHandle{}, fmt.Errorf("creating scratch dir for GUI audio routing: %w", err)
	}

	plistPath := filepath.Join(dir, label+".plist")
	errPath := filepath.Join(dir, label+".err")
	argv := append([]string{program}, args...)
	if err := renderPlistFile(plistPath, guiJobSpec{
		Label: label, ProgramArguments: argv, StandardErrorPath: errPath,
	}); err != nil {
		os.RemoveAll(dir)
		return guiJobHandle{}, err
	}

	out, err := exec.CommandContext(ctx, launchctlPath, "bootstrap",
		guiDomainTarget(), plistPath).CombinedOutput()
	if err != nil {
		os.RemoveAll(dir)
		return guiJobHandle{}, fmt.Errorf("launchctl bootstrap into %s failed: %w (output: %s)",
			guiDomainTarget(), err, strings.TrimSpace(string(out)))
	}
	return guiJobHandle{label: label, program: program, dir: dir, errPath: errPath}, nil
}

// waitGUIJob blocks until the bootstrapped job exits, returning its error. It
// always boots the job out and removes the scratch dir. ctx cancellation and an
// internal timeout both stop the job and clean up.
func waitGUIJob(ctx context.Context, handle guiJobHandle) error {
	defer os.RemoveAll(handle.dir)
	defer bootoutGUIJob(handle.label)

	exitCode, err := pollGUIExit(ctx, handle.label)
	if err != nil {
		killGUIJob(handle.label)
		return err
	}
	if exitCode == 0 {
		return nil
	}
	return fmt.Errorf("GUI-routed %s exited with status %d: %s",
		filepath.Base(handle.program), exitCode, readStderrSnippet(handle.errPath))
}

// renderPlistFile writes spec to plistPath with restrictive permissions.
func renderPlistFile(plistPath string, spec guiJobSpec) error {
	f, err := os.OpenFile(plistPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("creating launchd plist for GUI audio routing: %w", err)
	}
	if err := writePlist(f, spec); err != nil {
		f.Close()
		return fmt.Errorf("writing launchd plist for GUI audio routing: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing launchd plist for GUI audio routing: %w", err)
	}
	return nil
}

// readStderrSnippet returns the trimmed first ~500 bytes of the job's stderr
// file, or a placeholder when nothing was captured.
func readStderrSnippet(errPath string) string {
	data, err := os.ReadFile(errPath)
	if err != nil || len(data) == 0 {
		return "(no stderr captured)"
	}
	if len(data) > 500 {
		data = data[:500]
	}
	return strings.TrimSpace(string(data))
}

// pollGUIExit polls `launchctl print gui/<uid>/<label>` until the one-shot job
// has exited, returning its exit status. It is bounded by ctx and an internal
// timeout so a wedged job cannot hang the handler.
func pollGUIExit(ctx context.Context, label string) (int, error) {
	deadlineCtx, cancel := context.WithTimeout(ctx, guiJobTimeout)
	defer cancel()
	ticker := time.NewTicker(guiPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-deadlineCtx.Done():
			return 0, fmt.Errorf("timed out (or cancelled) waiting for GUI audio job %s: %w",
				label, deadlineCtx.Err())
		case <-ticker.C:
			out, _ := exec.CommandContext(deadlineCtx, launchctlPath, "print",
				guiServiceTarget(label)).CombinedOutput()
			if code, done := parseGUIExitCode(string(out)); done {
				return code, nil
			}
		}
	}
}

// parseGUIExitCode scans `launchctl print` output for a terminal one-shot job.
// It returns (code, true) once the job has exited, else (0, false). It anchors
// on the literal substrings launchd emits; the print format is unstructured, so
// these anchors are the contract.
func parseGUIExitCode(printOutput string) (code int, done bool) {
	var notRunning, exitFound bool
	var exitCode int
	scanner := bufio.NewScanner(strings.NewReader(printOutput))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "state = not running" {
			notRunning = true
			continue
		}
		if rest, ok := strings.CutPrefix(line, "last exit code = "); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil {
				exitCode = n
				exitFound = true
			}
		}
	}
	return exitCode, exitFound && notRunning
}

// bootoutGUIJob removes a (typically already-exited) transient job from the GUI
// domain. Best-effort: a one-shot lingers showing its last exit code until
// booted out, and a leftover label blocks re-bootstrap of the same label.
func bootoutGUIJob(label string) {
	if out, err := exec.Command(launchctlPath, "bootout",
		guiServiceTarget(label)).CombinedOutput(); err != nil {
		log.Debug("launchctl bootout failed", "label", label,
			"error", err, "output", strings.TrimSpace(string(out)))
	}
}

// killGUIJob sends SIGTERM to a still-running GUI job (used on ctx-cancel /
// timeout before bootout). Best-effort.
func killGUIJob(label string) {
	if out, err := exec.Command(launchctlPath, "kill", "SIGTERM",
		guiServiceTarget(label)).CombinedOutput(); err != nil {
		log.Debug("launchctl kill failed", "label", label,
			"error", err, "output", strings.TrimSpace(string(out)))
	}
}

// startGUISay bootstraps a say synthesis (and prepares the optional afplay of a
// saved AIFF) into the GUI session synchronously, returning a wait func and a
// cleanup func. text is written to a temp file consumed via `say -f`, keeping
// user text out of the plist. baseArgs is the validated [--rate N (--voice V)?]
// slice from sayCommandArgs. When savedPath != "", say writes the AIFF there;
// when play is true the AIFF (or, with no savedPath, the synthesis itself) is
// played in the GUI session. A bootstrap failure is returned here so the say
// handler can report an error instead of a misleading "Speaking:" result.
func startGUISay(
	ctx context.Context, baseArgs []string, text, savedPath string, play bool,
) (wait func(context.Context) error, cleanup func(), err error) {
	txtPath, removeTxt, err := writeTempFile("mcp-tts-say-*.txt", []byte(text))
	if err != nil {
		return nil, nil, fmt.Errorf("preparing say text for GUI routing: %w", err)
	}

	sayArgs := append([]string{}, baseArgs...)
	if savedPath != "" {
		sayArgs = append(sayArgs, "-o", savedPath)
	}
	sayArgs = append(sayArgs, "-f", txtPath)

	stopSpeaking := func() {}
	if savedPath == "" && play {
		stopSpeaking = beginRexSpeaking(ctx)
	}
	handle, err := startGUIJobFn(ctx, sayBinPath, sayArgs)
	if err != nil {
		stopSpeaking()
		removeTxt()
		return nil, nil, err
	}

	wait = func(waitCtx context.Context) error {
		defer stopSpeaking()
		if err := waitGUIJobFn(waitCtx, handle); err != nil {
			return err
		}
		if savedPath != "" && play {
			stopSavedSpeech := beginRexSpeaking(waitCtx)
			defer stopSavedSpeech()
			return runInGUISession(waitCtx, afplayBinPath, []string{savedPath})
		}
		return nil
	}
	return wait, removeTxt, nil
}

// guiSayPlay routes a say synthesis (and optional afplay of a saved AIFF) through
// the GUI session, blocking until it completes. It surfaces bootstrap and exit
// errors to the caller; the save-only handler path relies on this synchronous
// contract.
func guiSayPlay(ctx context.Context, baseArgs []string, text, savedPath string, play bool) error {
	wait, cleanup, err := startGUISay(ctx, baseArgs, text, savedPath, play)
	if err != nil {
		return err
	}
	defer cleanup()
	return wait(ctx)
}

// startRoutableSayPlayback acquires the TTS lock and bootstraps a GUI-routed say
// job synchronously — surfacing lock and launchctl bootstrap failures to the
// caller, mirroring the in-process startSayPlayback contract — then detaches the
// wait so a cancelled request cannot truncate speech.
func startRoutableSayPlayback(baseArgs []string, text, savedPath string) error {
	release, err := acquireTTSLock(serverCtx)
	if err != nil {
		return fmt.Errorf("failed to acquire TTS lock: %w", err)
	}
	wait, cleanup, err := startGUISay(serverCtx, baseArgs, text, savedPath, true)
	if err != nil {
		release()
		return err
	}
	playbackWG.Go(func() {
		defer release()
		defer cleanup()
		if e := wait(serverCtx); e != nil && serverCtx.Err() == nil {
			log.Error("GUI-routed say failed", "error", e)
		}
	})
	return nil
}

// writeTempFile creates a 0600 temp file from pattern, writes data, and returns
// the path with a cleanup func. On any failure it removes the partial file.
func writeTempFile(pattern string, data []byte) (string, func(), error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", func() {}, fmt.Errorf("creating temp file %q: %w", pattern, err)
	}
	path := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return "", func() {}, fmt.Errorf("writing temp file %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", func() {}, fmt.Errorf("closing temp file %q: %w", path, err)
	}
	return path, func() { os.Remove(path) }, nil
}

// playViaGUI saves a fully-buffered cloud audio payload to a temp file and plays
// it through the GUI session via afplay, holding the TTS lock and tracking the
// work in playbackWG exactly like the in-process path. ext is "mp3" or "wav".
// The afplay job is bootstrapped synchronously so a launchd bootstrap failure
// surfaces to deliverAudio instead of the caller being told audio is playing;
// only the wait is detached so a cancelled request cannot truncate it.
func playViaGUI(label, ext string, audioData []byte) error {
	path, cleanup, err := writeTempFile("mcp-tts-audio-*."+ext, audioData)
	if err != nil {
		return fmt.Errorf("preparing GUI audio for %s: %w", label, err)
	}
	release, err := acquireTTSLock(serverCtx)
	if err != nil {
		cleanup()
		return fmt.Errorf("failed to acquire TTS lock: %w", err)
	}
	stopSpeaking := beginRexSpeaking(serverCtx)
	handle, err := startGUIJobFn(serverCtx, afplayBinPath, []string{path})
	if err != nil {
		stopSpeaking()
		release()
		cleanup()
		return fmt.Errorf("starting GUI playback for %s: %w", label, err)
	}
	playbackWG.Go(func() {
		defer release()
		defer cleanup()
		defer stopSpeaking()
		if e := waitGUIJobFn(serverCtx, handle); e != nil && serverCtx.Err() == nil {
			log.Error("GUI-routed playback failed", "label", label, "error", e)
		}
	})
	return nil
}

// routeCloudPlayback decides how a cloud provider's buffered audio is played
// based on the session mode. Returns (handled, error): handled=false means the
// caller should fall back to in-process playback (Aqua / non-darwin). build is
// invoked only when routing is required, so callers never pay to materialize the
// afplay-ready bytes (e.g. wrap PCM in a WAV) on the in-process Aqua path.
func routeCloudPlayback(label, ext string, build func() ([]byte, error)) (handled bool, err error) {
	mode, name := sessionModeFn()
	switch mode {
	case sessionAqua:
		return false, nil
	case sessionRoutable:
		audioData, err := build()
		if err != nil {
			return true, err
		}
		return true, playViaGUI(label, ext, audioData)
	case sessionHeadless:
		return true, errors.New(headlessAudioError(name))
	default:
		return false, nil
	}
}

// headlessAudioError returns the user-facing message returned by every handler
// when local audio cannot be produced because no GUI session is reachable.
func headlessAudioError(managerName string) string {
	return fmt.Sprintf(headlessTemplate, managerName, os.Getuid())
}

// cleanupStaleGUIJobs boots out leftover mcp-tts GUI jobs whose owning process
// is dead (crash leftovers). Best-effort: it logs at debug and never fails
// startup. No-op when not routable.
func cleanupStaleGUIJobs() {
	mode, _ := sessionModeFn()
	if mode != sessionRoutable {
		return
	}
	out, err := exec.Command(launchctlPath, "print", guiDomainTarget()).CombinedOutput()
	if err != nil {
		log.Debug("cleanupStaleGUIJobs: print failed", "target", guiDomainTarget(),
			"error", err, "output", strings.TrimSpace(string(out)))
		return
	}
	for label := range staleGUILabels(string(out)) {
		log.Debug("Booting out stale GUI job from dead process", "label", label)
		bootoutGUIJob(label)
	}
}

// staleGUILabels scans `launchctl print gui/<uid>` output for mcp-tts job labels
// whose owning pid is no longer alive, returning them as a set.
func staleGUILabels(printOutput string) map[string]struct{} {
	stale := make(map[string]struct{})
	scanner := bufio.NewScanner(strings.NewReader(printOutput))
	for scanner.Scan() {
		for tok := range strings.FieldsSeq(scanner.Text()) {
			label, ok := guiLabelFromToken(tok)
			if !ok {
				continue
			}
			if pid, ok := guiLabelPID(label); ok && !isProcessAlive(pid) {
				stale[label] = struct{}{}
			}
		}
	}
	return stale
}

// guiLabelFromToken returns the mcp-tts label embedded in a token, if present.
func guiLabelFromToken(tok string) (string, bool) {
	if !strings.HasPrefix(tok, guiLabelPrefix+".") {
		return "", false
	}
	return strings.Trim(tok, "\""), true
}

// guiLabelPID parses the <pid> segment from a com.blacktop.mcp-tts.<pid>.<n>
// label. It returns false for malformed labels rather than panicking.
func guiLabelPID(label string) (int, bool) {
	parts := strings.Split(label, ".")
	if len(parts) < 5 {
		return 0, false
	}
	pid, err := strconv.Atoi(parts[3])
	if err != nil {
		return 0, false
	}
	return pid, true
}
