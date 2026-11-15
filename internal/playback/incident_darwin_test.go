//go:build darwin

package playback

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func useSampler(t *testing.T, build func(ctx context.Context, pid int, outPath string) *exec.Cmd) {
	t.Helper()
	original := sampleCommand
	sampleCommand = build
	t.Cleanup(func() { sampleCommand = original })
}

func samplerWriting(bytes int) func(context.Context, int, string) *exec.Cmd {
	return func(ctx context.Context, _ int, outPath string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], helperArg, "write", outPath, strconv.Itoa(bytes))
	}
}

func testIncident() Incident {
	return Incident{Label: "openai_tts", Player: "/usr/bin/afplay", PID: 4242, Elapsed: 20 * time.Second, Budget: 18 * time.Second}
}

func TestCaptureIncidentWritesPrivateBoundedReport(t *testing.T) {
	useSampler(t, samplerWriting(2*incidentSizeLimit))
	dir := filepath.Join(t.TempDir(), "incidents")

	path, err := CaptureIncident(context.Background(), dir, testIncident())
	if err != nil {
		t.Fatalf("CaptureIncident: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("report missing: %v", err)
	}
	if info.Size() > incidentSizeLimit {
		t.Fatalf("report is %d bytes, over the %d limit", info.Size(), incidentSizeLimit)
	}
	if info.Size() < incidentSizeLimit/2 {
		t.Fatalf("report is %d bytes: the sample was not appended", info.Size())
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("report mode = %o, want 600", perm)
	}
	if dirInfo, _ := os.Stat(dir); dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("incident dir mode = %o, want 700", dirInfo.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	header := string(data[:min(len(data), 400)])
	for _, want := range []string{"label: openai_tts", "player: /usr/bin/afplay", "pid: 4242", "budget: 18s", "macOS: "} {
		if !strings.Contains(header, want) {
			t.Fatalf("report header missing %q:\n%s", want, header)
		}
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.sample")); len(leftovers) != 0 {
		t.Fatalf("scratch sample files left behind: %v", leftovers)
	}
}

func TestCaptureIncidentKeepsHeaderWhenSamplerHangs(t *testing.T) {
	useSampler(t, func(ctx context.Context, _ int, _ string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], helperArg, "hang")
	})
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	began := time.Now()
	path, err := CaptureIncident(ctx, dir, testIncident())
	if err == nil {
		t.Fatal("CaptureIncident = nil error for a sampler that hung")
	}
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("CaptureIncident took %s for a hung sampler", took)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || !strings.Contains(string(data), "label: openai_tts") {
		t.Fatalf("report after a hung sampler = %q, %v; want the header", data, readErr)
	}
}

func TestCaptureIncidentKeepsOnlyNewestReports(t *testing.T) {
	useSampler(t, samplerWriting(16))
	dir := t.TempDir()
	for i := range incidentKeep + 3 {
		incident := testIncident()
		incident.PID = 1000 + i
		if _, err := CaptureIncident(context.Background(), dir, incident); err != nil {
			t.Fatalf("CaptureIncident #%d: %v", i, err)
		}
		time.Sleep(2 * time.Millisecond) // distinct, ordered timestamps
	}
	reports, _ := filepath.Glob(filepath.Join(dir, incidentPrefix+"*.txt"))
	if len(reports) != incidentKeep {
		t.Fatalf("%d reports kept, want %d", len(reports), incidentKeep)
	}
	for _, report := range reports {
		if strings.HasSuffix(report, "-1000.txt") || strings.HasSuffix(report, "-1002.txt") {
			t.Fatalf("an oldest report survived pruning: %s", report)
		}
	}
}
