//go:build !windows

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const labelSession = "session:11111111-1111-1111-1111-111111111111"
const labelWindow = "window:22222222-2222-2222-2222-222222222222"
const labelBlockA = "block:33333333-3333-3333-3333-333333333333"
const labelBlockB = "block:44444444-4444-4444-4444-444444444444"

type labelTestAPI struct {
	label      string
	renames    []string
	failNext   bool
	titles     map[string]string
	titleReads []string
}

func (a *labelTestAPI) request(ctx context.Context, method string, args, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var response any
	switch method {
	case "server.status":
		response = map[string]any{"host_id": "test-host"}
	case "session.view":
		response = map[string]any{"session_id": labelSession, "windows": []any{
			map[string]any{"window_id": labelWindow, "label": a.label, "layers": []any{
				map[string]any{"blocks": []any{map[string]any{"block_id": labelBlockA}, map[string]any{"block_id": labelBlockB}}},
			}},
		}}
	case "session.set_window_label":
		p, ok := args.(map[string]string)
		if !ok || p["session_id"] != labelSession || p["window_id"] != labelWindow {
			return errors.New("wrong rename target")
		}
		a.label = p["label"]
		a.renames = append(a.renames, a.label)
		if a.label == "" {
			a.label = "Window 22222222"
		}
		if a.failNext {
			a.failNext = false
			return context.DeadlineExceeded // The server applied it before the reply was lost.
		}
		response = map[string]any{}
	case "com.superlogical.terminal.title":
		p, ok := args.(map[string]any)
		if !ok || p["session_id"] != labelSession {
			return errors.New("wrong terminal title target")
		}
		block, ok := p["block_id"].(string)
		if !ok {
			return errors.New("missing terminal title block")
		}
		a.titleReads = append(a.titleReads, block)
		response = map[string]any{"title": a.titles[block]}
	default:
		return fmt.Errorf("unexpected method %s", method)
	}
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, result)
}

func labelClient(t *testing.T, api *labelTestAPI, dir, block string) *rexLabelClient {
	t.Helper()
	require.NoError(t, os.Chmod(dir, 0700))
	client, err := newRexLabelClient("unix:///tmp/rex-test.sock", labelSession, block)
	require.NoError(t, err)
	client.stateDir, client.request = dir, api.request
	return client
}

func labelID(c string) string { return "mcp-tts/" + strings.Repeat(c, 26) }

func TestRexTabLabelOptInAndTargetValidation(t *testing.T) {
	require.Equal(t, "false", rootCmd.PersistentFlags().Lookup("rex-tab-label").DefValue)
	for _, args := range [][3]string{
		{"", labelSession, labelBlockA},
		{"https://example.com", labelSession, labelBlockA},
		{"unix:///tmp/rex.sock", "work", labelBlockA},
		{"unix:///tmp/rex.sock", labelSession, "3333"},
	} {
		_, err := newRexLabelClient(args[0], args[1], args[2])
		require.Error(t, err)
	}
	api := &labelTestAPI{label: "original"}
	client := labelClient(t, api, t.TempDir(), "block:55555555-5555-5555-5555-555555555555")
	beginRexTabLabel(context.Background(), client, labelID("A"))()
	require.Empty(t, api.renames)
}

func TestRexTabLabelSharedWindowAndManualRename(t *testing.T) {
	api := &labelTestAPI{label: "original"}
	dir := t.TempDir()
	a := labelClient(t, api, dir, labelBlockA)
	b := labelClient(t, api, dir, labelBlockB)
	first := beginRexTabLabel(context.Background(), a, labelID("A"))
	second := beginRexTabLabel(context.Background(), b, labelID("B"))
	require.Equal(t, "🔊 original", api.label)
	first()
	require.Equal(t, "🔊 original", api.label)
	second()
	require.Equal(t, []string{"🔊 original", "original"}, api.renames)

	first = beginRexTabLabel(context.Background(), a, labelID("C"))
	api.label = "manually renamed"
	second = beginRexTabLabel(context.Background(), b, labelID("D"))
	first()
	second()
	require.Equal(t, "manually renamed", api.label)
	require.Equal(t, []string{"🔊 original", "original", "🔊 original"}, api.renames)
	third := beginRexTabLabel(context.Background(), a, labelID("E"))
	require.Equal(t, "🔊 manually renamed", api.label)
	third()
	require.Equal(t, "manually renamed", api.label)
}

func TestRexTabLabelAutomaticAndExplicitNames(t *testing.T) {
	for _, tc := range []struct {
		name, original, marked, restored string
		wantTitle                        bool
	}{
		{name: "automatic", original: "Window 22222222", marked: "🔊 dotfiles | master", restored: "", wantTitle: true},
		{name: "explicit", original: "Review", marked: "🔊 Review", restored: "Review"},
		{name: "default-looking explicit", original: "Window deadbeef", marked: "🔊 Window deadbeef", restored: "Window deadbeef"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &labelTestAPI{label: tc.original, titles: map[string]string{labelBlockA: "other pane", labelBlockB: "dotfiles | master"}}
			dir := t.TempDir()
			first := beginRexTabLabel(context.Background(), labelClient(t, api, dir, labelBlockB), labelID("N"))
			second := beginRexTabLabel(context.Background(), labelClient(t, api, dir, labelBlockA), labelID("O"))
			require.Equal(t, tc.marked, api.label)
			first()
			require.Equal(t, tc.marked, api.label)
			second()
			require.Equal(t, tc.original, api.label)
			require.Equal(t, []string{tc.marked, tc.restored}, api.renames)
			if tc.wantTitle {
				require.Equal(t, []string{labelBlockB}, api.titleReads)
			} else {
				require.Empty(t, api.titleReads)
			}
		})
	}
}

func TestRexAutomaticLabelPreservesManualRename(t *testing.T) {
	api := &labelTestAPI{label: "Window 22222222", titles: map[string]string{labelBlockA: "dotfiles"}}
	client := labelClient(t, api, t.TempDir(), labelBlockA)
	finish := beginRexTabLabel(context.Background(), client, labelID("P"))
	api.label = "manual title"
	finish()
	require.Equal(t, "manual title", api.label)
	require.Equal(t, []string{"🔊 dotfiles"}, api.renames)
}

func TestRexAutomaticLabelWithoutTitleStaysAutomatic(t *testing.T) {
	api := &labelTestAPI{label: "Window 22222222"}
	client := labelClient(t, api, t.TempDir(), labelBlockA)
	beginRexTabLabel(context.Background(), client, labelID("Q"))()
	require.Equal(t, "Window 22222222", api.label)
	require.Empty(t, api.renames)
}

func TestRexAutomaticLabelRecoversLegacyMarker(t *testing.T) {
	for _, title := range []string{"dotfiles | master", ""} {
		t.Run(title, func(t *testing.T) {
			api := &labelTestAPI{label: "🔊 Window 22222222", titles: map[string]string{labelBlockA: title}}
			dir := t.TempDir()
			client := labelClient(t, api, dir, labelBlockA)
			target := rexLabelTarget{"test-host", labelSession, labelWindow, api.label}
			require.NoError(t, writeRexLabelState(rexLabelStatePath(dir, target), &rexLabelState{
				Original: "Window 22222222", Owned: api.label,
			}))
			finish := beginRexTabLabel(context.Background(), client, labelID("R"))
			if title != "" {
				require.Equal(t, "🔊 "+title, api.label)
			} else {
				require.Equal(t, "Window 22222222", api.label)
			}
			finish()
			require.Equal(t, "Window 22222222", api.label)
			_, err := os.Stat(rexLabelStatePath(dir, target))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestRexTabLabelCleanupAfterUncertainRPCAndCancellation(t *testing.T) {
	original := strings.Repeat("界", 128)
	api := &labelTestAPI{label: original, failNext: true}
	client := labelClient(t, api, t.TempDir(), labelBlockA)
	ctx, cancel := context.WithCancel(context.Background())
	finish := beginRexTabLabel(ctx, client, labelID("F"))
	require.True(t, strings.HasPrefix(api.label, rexLabelPrefix))
	require.Equal(t, 128, utf8.RuneCountInString(api.label))
	cancel()
	finish()
	require.Equal(t, original, api.label)
}

func TestRexTabLabelRecoversLostRestoreReply(t *testing.T) {
	api := &labelTestAPI{label: "original"}
	client := labelClient(t, api, t.TempDir(), labelBlockA)
	finish := beginRexTabLabel(context.Background(), client, labelID("J"))
	api.failNext = true
	finish()
	require.Equal(t, "original", api.label)
	finish = beginRexTabLabel(context.Background(), client, labelID("K"))
	require.Equal(t, "🔊 original", api.label, "a completed ownership period must not suppress new speech")
	finish()
	require.Equal(t, "original", api.label)

	// With no remaining speaker, a later manual title is the next period's
	// original label. It must be restored exactly after that new speech.
	finish = beginRexTabLabel(context.Background(), client, labelID("L"))
	api.failNext = true
	finish()
	api.label = "later manual title"
	finish = beginRexTabLabel(context.Background(), client, labelID("M"))
	require.Equal(t, "🔊 later manual title", api.label)
	finish()
	require.Equal(t, "later manual title", api.label)
}

// The helper holds a real kernel lock until its process exits. It does not
// start a Rex server or generate fake playback.
func TestRexLabelLeaseHelper(t *testing.T) {
	if os.Getenv("MCP_TTS_TEST_LEASE_HELPER") != "1" {
		return
	}
	path := os.Getenv("MCP_TTS_TEST_LEASE_PATH")
	f, err := openRexLabelFile(path, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	fmt.Println("ready")
	_, _ = bufio.NewReader(os.Stdin).ReadByte()
}

func TestRexTabLabelRecoversExitedProcess(t *testing.T) {
	dir := t.TempDir()
	name := "lease-" + strings.Repeat("G", 26)
	child := exec.Command(os.Args[0], "-test.run=^TestRexLabelLeaseHelper$")
	child.Env = append(os.Environ(), "MCP_TTS_TEST_LEASE_HELPER=1", "MCP_TTS_TEST_LEASE_PATH="+filepath.Join(dir, name))
	stdin, err := child.StdinPipe()
	require.NoError(t, err)
	stdout, err := child.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, child.Start())
	t.Cleanup(func() { _ = stdin.Close(); _ = child.Process.Kill(); _ = child.Wait() })
	ready := make(chan bool, 1)
	go func() { ready <- bufio.NewScanner(stdout).Scan() }()
	select {
	case ok := <-ready:
		require.True(t, ok)
	case <-time.After(5 * time.Second):
		t.Fatal("lease holder did not start")
	}
	api := &labelTestAPI{label: "🔊 original"}
	client := labelClient(t, api, dir, labelBlockA)
	target := rexLabelTarget{"test-host", labelSession, labelWindow, api.label}
	require.NoError(t, writeRexLabelState(rexLabelStatePath(dir, target), &rexLabelState{
		Original: "original", Owned: api.label, Leases: []string{name},
	}))
	finish := beginRexTabLabel(context.Background(), client, labelID("H"))
	finish()
	require.Equal(t, "🔊 original", api.label, "another process still owns the marker")
	require.NoError(t, child.Process.Kill())
	require.Error(t, child.Wait())
	finish = beginRexTabLabel(context.Background(), client, labelID("I"))
	finish()
	require.Equal(t, "original", api.label)
	_, err = os.Stat(rexLabelStatePath(dir, target))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRexTabLabelCoordinatorTimeout(t *testing.T) {
	dir := t.TempDir()
	lock, err := lockRexLabels(context.Background(), dir)
	require.NoError(t, err)
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = lockRexLabels(ctx, dir)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRexLabelStateRejectsSpecialFiles(t *testing.T) {
	if path := os.Getenv("MCP_TTS_TEST_SPECIAL_FILE"); path != "" {
		_, err := readRexLabelState(path, labelWindow)
		require.Error(t, err)
		return
	}
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, unix.Mkfifo(path, 0600))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRexLabelStateRejectsSpecialFiles$")
	child.Env = append(os.Environ(), "MCP_TTS_TEST_SPECIAL_FILE="+path)
	output, err := child.CombinedOutput()
	require.NoError(t, err, "special state file blocked or was accepted: %s", output)
}
