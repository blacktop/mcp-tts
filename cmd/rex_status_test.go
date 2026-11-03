package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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

func TestRexSpeakingFollowsDetachedPlayback(t *testing.T) {
	var out bytes.Buffer
	ctx, cancel := context.WithCancel(rexTestContext(&out))
	previousCtx, previousSequential := serverCtx, sequentialTTS
	serverCtx, sequentialTTS = ctx, false
	t.Cleanup(func() {
		cancel()
		playbackWG.Wait()
		serverCtx, sequentialTTS = previousCtx, previousSequential
	})

	started := make(chan struct{})
	require.NoError(t, playInBackground("test", func(ctx context.Context) {
		close(started)
		<-ctx.Done()
	}))
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("playback did not start")
	}
	require.Len(t, rexTestReports(t, out.String()), 1, "returning to the MCP caller must not clear active playback")
	cancel()
	playbackWG.Wait()
	reports := rexTestReports(t, out.String())
	require.Len(t, reports, 2)
	require.Equal(t, map[string]string{"state": "clear", "id": reports[0]["id"]}, reports[1])
}

func TestRexVoiceCleanup(t *testing.T) {
	previousRunner, previousLock, previousNoPlay, previousOutputDir := voiceSayRunner, voiceTTSLock, noPlay, outputDir
	t.Cleanup(func() {
		voiceSayRunner, voiceTTSLock, noPlay = previousRunner, previousLock, previousNoPlay
		outputDir = previousOutputDir
	})
	outputDir = t.TempDir()
	voiceTTSLock = func(context.Context) (func(), error) { return func() {}, nil }
	for _, outcome := range []string{"success", "failure", "cancel", "save-only"} {
		t.Run(outcome, func(t *testing.T) {
			var out bytes.Buffer
			ctx, cancel := context.WithCancel(rexTestContext(&out))
			defer cancel()
			noPlay = outcome == "save-only"
			voiceSayRunner = func(context.Context, string, []string, string) error {
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
