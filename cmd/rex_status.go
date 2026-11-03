package cmd

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/charmbracelet/log"
)

type rexStatusKey struct{}

type rexStatus struct {
	mu    sync.Mutex
	out   io.Writer
	label *rexLabelClient
}

// withRexStatus uses the process's own terminal. Writing an OSC to stdout would
// corrupt MCP, and sending it through `rex send` would type into the agent.
func withRexStatus(ctx context.Context, openTTY func() (io.WriteCloser, error)) (context.Context, func()) {
	if os.Getenv("REX_SESSION") == "" || os.Getenv("REX_BLOCK") == "" {
		if rexTabLabelEnabled {
			log.Warn("Rex tab label unavailable: REX_SESSION and REX_BLOCK are required")
		}
		return ctx, func() {}
	}
	tty, err := openTTY()
	if err != nil {
		log.Debug("Rex speech status unavailable", "error", err)
		return ctx, func() {}
	}
	status := &rexStatus{out: tty}
	if rexTabLabelEnabled {
		var err error
		status.label, err = newRexLabelClient(os.Getenv("REX_SERVER"), os.Getenv("REX_SESSION"), os.Getenv("REX_BLOCK"))
		if err != nil {
			log.Warn("Rex tab label unavailable", "error", err)
		}
	}
	return context.WithValue(ctx, rexStatusKey{}, status), func() {
		status.mu.Lock()
		defer status.mu.Unlock()
		status.out = io.Discard
		_ = tty.Close()
	}
}

func (s *rexStatus) write(report string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sequence := "\x1b]7501;" + report + "\x1b\\"
	n, err := io.WriteString(s.out, sequence)
	if err == nil && n != len(sequence) {
		err = io.ErrShortWrite
	}
	if err != nil {
		log.Debug("Could not update Rex speech status", "error", err)
	}
}

// beginRexSpeaking returns cleanup for exactly this playback. Separate record
// IDs keep overlapping calls and other agents' status records independent.
func beginRexSpeaking(ctx context.Context) func() {
	status, _ := ctx.Value(rexStatusKey{}).(*rexStatus)
	if status == nil {
		return func() {}
	}
	id := "mcp-tts/" + rand.Text()
	restoreLabel := func() {}
	if status.label != nil {
		restoreLabel = beginRexTabLabel(ctx, status.label, id)
	}
	text := base64.StdEncoding.EncodeToString([]byte("🔊 Speaking"))
	status.write(fmt.Sprintf("state=working:id=%s:app=mcp-tts:title=%s:msg=%s", id, text, text))
	return func() {
		status.write("state=clear:id=" + id)
		restoreLabel()
	}
}
