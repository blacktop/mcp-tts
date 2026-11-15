//go:build !darwin

package cmd

import (
	"context"
	"errors"
)

var errGUIUnsupported = errors.New("GUI session routing is only supported on macOS")

// sessionMode classifies the launchd audio context of this process. On
// non-darwin platforms there is no launchd session, so the type exists only so
// shared code in root.go compiles; detection always reports sessionAqua.
type sessionMode int

const (
	sessionAqua sessionMode = iota
	sessionRoutable
	sessionHeadless
)

func currentSessionMode() (sessionMode, string) { return sessionAqua, "" }

// sessionModeFn mirrors the darwin seam so shared code in root.go compiles. On
// non-darwin platforms it always reports sessionAqua (no GUI routing).
var sessionModeFn = currentSessionMode

func headlessAudioError(string) string { return "" }

func guiSayPlay(context.Context, []string, string, string, bool) error {
	return errGUIUnsupported
}

func startRoutableSayPlayback(context.Context, []string, string, string) error {
	return errGUIUnsupported
}

func routeCloudPlayback(context.Context, string, string, func() ([]byte, error)) (bool, error) {
	return false, nil
}

func cleanupStaleGUIJobs() {}
