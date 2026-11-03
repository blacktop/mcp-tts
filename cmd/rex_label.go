package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const rexLabelPrefix = "🔊 "
const rexLabelTimeout = 4 * time.Second

type rexLabelTarget struct {
	host, session, window, label string
}

type rexLabelClient struct {
	session, block, stateDir string
	request                  func(context.Context, string, any, any) error
}

func newRexLabelClient(server, session, block string) (*rexLabelClient, error) {
	endpoint, err := url.Parse(server)
	if runtime.GOOS == "windows" || err != nil || endpoint.Scheme != "unix" || endpoint.Host != "" ||
		!filepath.IsAbs(endpoint.Path) || endpoint.RawQuery != "" || endpoint.Fragment != "" ||
		!validRexID("session", session) || !validRexID("block", block) {
		return nil, errors.New("Rex tab labels require a local Unix Rex server and full session/block IDs")
	}
	return &rexLabelClient{session: session, block: block, request: func(ctx context.Context, method string, args, result any) error {
		return callRexLabelAPI(ctx, server, method, args, result)
	}}, nil
}

func validRexID(kind, id string) bool {
	value, ok := strings.CutPrefix(id, kind+":")
	if !ok || len(value) != 36 {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// locate validates the exact block for acquisition, or the captured window for
// cleanup after a pane has moved. It never falls back to Rex's current focus.
func (r *rexLabelClient) locate(ctx context.Context, window string) (rexLabelTarget, error) {
	var server struct {
		HostID string `json:"host_id"`
	}
	if err := r.request(ctx, "server.status", struct{}{}, &server); err != nil {
		return rexLabelTarget{}, err
	}
	var view struct {
		SessionID string `json:"session_id"`
		Windows   []struct {
			ID     string `json:"window_id"`
			Label  string `json:"label"`
			Layers []struct {
				Blocks []struct {
					ID string `json:"block_id"`
				} `json:"blocks"`
			} `json:"layers"`
		} `json:"windows"`
	}
	if err := r.request(ctx, "session.view", map[string]string{"session_id": r.session}, &view); err != nil {
		return rexLabelTarget{}, err
	}
	if server.HostID == "" || view.SessionID != r.session {
		return rexLabelTarget{}, errors.New("Rex returned a different session or no host identity")
	}
	var found []rexLabelTarget
	for _, w := range view.Windows {
		matches := window != "" && w.ID == window
		if window == "" {
			for _, layer := range w.Layers {
				for _, block := range layer.Blocks {
					matches = matches || block.ID == r.block
				}
			}
		}
		if matches && validRexID("window", w.ID) && validRexLabel(w.Label) {
			found = append(found, rexLabelTarget{server.HostID, r.session, w.ID, w.Label})
		}
	}
	if len(found) != 1 {
		return rexLabelTarget{}, errors.New("Rex target does not identify exactly one live window")
	}
	return found[0], nil
}

func (r *rexLabelClient) rename(ctx context.Context, target rexLabelTarget, label string) error {
	return r.request(ctx, "session.set_window_label", map[string]string{
		"session_id": target.session, "window_id": target.window, "label": label,
	}, &struct{}{})
}

func validRexLabel(label string) bool {
	return utf8.ValidString(label) && utf8.RuneCountInString(label) <= 128 && strings.IndexFunc(label, unicode.IsControl) < 0
}

func markedRexLabel(label string) string {
	runes := []rune(label)
	if len(runes) > 126 {
		label = string(runes[:125]) + "…"
	}
	return rexLabelPrefix + label
}

type rexOutput struct {
	data     []byte
	overflow bool
}

func (b *rexOutput) Write(p []byte) (int, error) {
	const limit = 4 << 20
	n := min(len(p), limit-len(b.data))
	b.data = append(b.data, p[:n]...)
	b.overflow = b.overflow || n != len(p)
	return len(p), nil
}

func callRexLabelAPI(ctx context.Context, server, method string, args, result any) error {
	data, err := json.Marshal(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "rex", "--autostart=false", "--timeout=1s", "--color=never", "-S", server, "api", "call", method, string(data))
	var output rexOutput
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = &output, io.Discard, 100*time.Millisecond
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Rex %s: %w", method, err)
	}
	if output.overflow {
		return errors.New("Rex response exceeded 4 MiB")
	}
	return json.Unmarshal(output.data, result)
}
