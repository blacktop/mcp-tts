//go:build darwin

package playback

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	incidentPrefix    = "incident-"
	incidentSizeLimit = 512 << 10
	incidentKeep      = 10
	// sampleSeconds is passed explicitly: sample's own default is ten seconds.
	sampleSeconds = "1"
)

// sampleCommand builds the stack sampler invocation. A variable so tests can
// substitute a stand-in.
var sampleCommand = func(ctx context.Context, pid int, outPath string) *exec.Cmd {
	return exec.CommandContext(ctx, "/usr/bin/sample", strconv.Itoa(pid), sampleSeconds, "-file", outPath)
}

// CaptureIncident writes a private report about an overrunning player to dir:
// what overran, the macOS build, and a stack sample of the player. It returns
// the report path.
//
// It is diagnostic only. The caller's stop sequence does not wait for it, and
// ctx ending simply loses the sample. The sampler reads the player's stacks
// first and writes its report afterwards, so a player that is killed while the
// report is being written is still captured. The report never contains speech
// text, is capped in size, and only the newest few are kept.
func CaptureIncident(ctx context.Context, dir string, incident Incident) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating incident directory %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("restricting incident directory %s: %w", dir, err)
	}
	name := fmt.Sprintf("%s%s-%d.txt", incidentPrefix, time.Now().UTC().Format("20060102T150405.000Z"), incident.PID)
	path := filepath.Join(dir, name)
	report, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("creating incident report: %w", err)
	}
	defer report.Close()

	header := incidentHeader(incident)
	if _, err := report.WriteString(header); err != nil {
		return path, fmt.Errorf("writing incident report %s: %w", path, err)
	}
	sampleErr := appendSample(ctx, report, path+".sample", incident.PID, incidentSizeLimit-len(header))
	pruneIncidents(dir)
	if sampleErr != nil {
		return path, fmt.Errorf("sampling pid %d: %w", incident.PID, sampleErr)
	}
	return path, nil
}

func incidentHeader(incident Incident) string {
	version, _ := unix.Sysctl("kern.osproductversion")
	build, _ := unix.Sysctl("kern.osversion")
	return fmt.Sprintf(
		"mcp-tts playback incident\nlabel: %s\nplayer: %s\npid: %d\nelapsed: %s\nbudget: %s\nmacOS: %s (%s)\n\n",
		incident.Label, incident.Player, incident.PID,
		incident.Elapsed.Round(time.Millisecond), incident.Budget, version, build,
	)
}

// appendSample runs the sampler into a scratch file and copies at most limit
// bytes of it into the report.
func appendSample(ctx context.Context, report *os.File, scratch string, pid, limit int) error {
	defer os.Remove(scratch)
	cmd := sampleCommand(ctx, pid, scratch)
	cmd.WaitDelay = 500 * time.Millisecond
	runErr := cmd.Run()

	sample, err := os.Open(scratch)
	if err != nil {
		if runErr != nil {
			return runErr
		}
		return err
	}
	defer sample.Close()
	if _, err := io.Copy(report, io.LimitReader(sample, int64(limit))); err != nil {
		return err
	}
	return runErr
}

// pruneIncidents keeps the newest reports. Names sort by their timestamp.
func pruneIncidents(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var reports []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), incidentPrefix) && strings.HasSuffix(entry.Name(), ".txt") {
			reports = append(reports, entry.Name())
		}
	}
	slices.Sort(reports)
	for len(reports) > incidentKeep {
		os.Remove(filepath.Join(dir, reports[0]))
		reports = reports[1:]
	}
}
