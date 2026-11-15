package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blacktop/mcp-tts/internal/playback"
	"github.com/blacktop/mcp-tts/internal/speechlock"
)

func rexTestContext(out io.Writer) context.Context {
	return context.WithValue(context.Background(), rexStatusKey{}, &rexStatus{out: out})
}

func rexTestReports(t *testing.T, output string) []map[string]string {
	t.Helper()
	var reports []map[string]string
	for output != "" {
		require.True(t, strings.HasPrefix(output, "\x1b]7501;"), "non-OSC output: %q", output)
		body, rest, ok := strings.Cut(strings.TrimPrefix(output, "\x1b]7501;"), "\x1b\\")
		require.True(t, ok, "unterminated report")
		report := make(map[string]string)
		for pair := range strings.SplitSeq(body, ":") {
			key, value, found := strings.Cut(pair, "=")
			require.True(t, found)
			report[key] = value
		}
		reports = append(reports, report)
		output = rest
	}
	return reports
}

func TestRexSpeakingRecordsAreIndependent(t *testing.T) {
	var out bytes.Buffer
	ctx := rexTestContext(&out)
	first := beginRexSpeaking(ctx)
	second := beginRexSpeaking(ctx)
	first()
	second()

	reports := rexTestReports(t, out.String())
	require.Len(t, reports, 4)
	for _, report := range reports[:2] {
		require.Equal(t, "working", report["state"])
		require.Equal(t, "mcp-tts", report["app"])
		require.Regexp(t, `^mcp-tts/[A-Z2-7]{26}$`, report["id"])
		title, err := base64.StdEncoding.DecodeString(report["title"])
		require.NoError(t, err)
		require.Equal(t, "🔊 Speaking", string(title))
	}
	require.NotEqual(t, reports[0]["id"], reports[1]["id"])
	require.Equal(t, map[string]string{"state": "clear", "id": reports[0]["id"]}, reports[2])
	require.Equal(t, map[string]string{"state": "clear", "id": reports[1]["id"]}, reports[3])
}

func TestRexTabLabelRequiresControllingTerminal(t *testing.T) {
	t.Setenv("REX_SERVER", "unix:///tmp/rex-test.sock")
	t.Setenv("REX_SESSION", "session:11111111-1111-1111-1111-111111111111")
	t.Setenv("REX_BLOCK", "block:33333333-3333-3333-3333-333333333333")
	previousEnabled, previousCtx, previousSequential := rexTabLabelEnabled, serverCtx, sequentialTTS
	rexTabLabelEnabled, sequentialTTS = true, false
	t.Cleanup(func() {
		playbackWG.Wait()
		rexTabLabelEnabled, serverCtx, sequentialTTS = previousEnabled, previousCtx, previousSequential
	})
	ctx, closeStatus := withRexStatus(context.Background(), func() (io.WriteCloser, error) {
		return nil, errors.New("no controlling terminal")
	})
	defer closeStatus()
	require.Nil(t, ctx.Value(rexStatusKey{}), "inherited IDs alone must not enable status or tab relabeling")
	serverCtx = ctx
	useStandInPlayers(t, []string{"ok"})
	proc, err := startPlayback(playback.Spec{Label: "no-tty-test", Budget: 10 * time.Second})
	require.NoError(t, err)
	played := make(chan struct{}, 1)
	detachPlayback("no-tty-test", proc, func() { played <- struct{}{} })
	playbackWG.Wait()
	select {
	case <-played:
	default:
		t.Fatal("missing controlling terminal prevented playback")
	}
}

// lockedBuffer is a terminal stand-in that a test can read while detached
// playback is still writing to it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRexSpeakingFollowsDetachedPlayback(t *testing.T) {
	var out lockedBuffer
	ctx, cancel := context.WithCancel(rexTestContext(&out))
	previousCtx, previousSequential := serverCtx, sequentialTTS
	serverCtx, sequentialTTS = ctx, false
	t.Cleanup(func() {
		cancel()
		playbackWG.Wait()
		serverCtx, sequentialTTS = previousCtx, previousSequential
	})

	useStandInPlayers(t, []string{"hang"})
	proc, err := startPlayback(playback.Spec{Label: "test", Budget: time.Minute})
	require.NoError(t, err)
	detachPlayback("test", proc, func() {})
	require.Eventually(t, func() bool { return out.String() != "" }, 5*time.Second, 10*time.Millisecond,
		"playback did not report that it is speaking")
	require.Len(t, rexTestReports(t, out.String()), 1, "returning to the MCP caller must not clear active playback")
	cancel()
	playbackWG.Wait()
	reports := rexTestReports(t, out.String())
	require.Len(t, reports, 2)
	require.Equal(t, map[string]string{"state": "clear", "id": reports[0]["id"]}, reports[1])
}

func TestRexVoiceCleanup(t *testing.T) {
	previousRunner, previousNoPlay, previousOutputDir := voiceSayRunner, noPlay, outputDir
	t.Cleanup(func() {
		voiceSayRunner, noPlay = previousRunner, previousNoPlay
		outputDir = previousOutputDir
	})
	outputDir = t.TempDir()
	useTestSpeechLock(t)
	for _, outcome := range []string{"success", "failure", "cancel", "save-only"} {
		t.Run(outcome, func(t *testing.T) {
			var out bytes.Buffer
			ctx, cancel := context.WithCancel(rexTestContext(&out))
			defer cancel()
			noPlay = outcome == "save-only"
			voiceSayRunner = func(context.Context, string, []string, string, *speechlock.Lock) error {
				require.Len(t, rexTestReports(t, out.String()), 1)
				switch outcome {
				case "failure":
					return errors.New("voice failed")
				case "cancel":
					cancel()
					return ctx.Err()
				}
				return nil
			}
			_ = callVoiceTTS(ctx, "voice-say", VoiceTTSParams{Text: "private spoken content"})
			reports := rexTestReports(t, out.String())
			if noPlay {
				require.Empty(t, reports)
				return
			}
			require.Len(t, reports, 2)
			require.Equal(t, map[string]string{"state": "clear", "id": reports[0]["id"]}, reports[1])
		})
	}
}
