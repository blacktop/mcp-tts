package cmd

import (
	"fmt"
	"strconv"
)

// defaultVolume plays speech at half of full volume. Speech voices are
// rendered close to full scale, so at full volume they play louder than most
// other audio at the same system volume.
const defaultVolume = 0.5

// volume scales the level of played speech, above 0 and at most 1. It is set
// by --volume or MCP_TTS_VOLUME and does not change saved audio files.
var volume = defaultVolume

// resolveVolume applies MCP_TTS_VOLUME when --volume was not given, then
// checks that the volume is in range.
func resolveVolume(flagGiven bool, env string) error {
	v := volume
	if !flagGiven && env != "" {
		parsed, err := strconv.ParseFloat(env, 64)
		if err != nil {
			return fmt.Errorf("MCP_TTS_VOLUME=%q is not a number; set it to a value above 0 and at most 1, such as 0.5", env)
		}
		v = parsed
	}
	if err := checkVolume(v); err != nil {
		return err
	}
	volume = v
	return nil
}

func checkVolume(v float64) error {
	if !(v > 0 && v <= 1) {
		return fmt.Errorf("volume %g is out of range; use a value above 0 and at most 1, such as 0.5", v)
	}
	return nil
}

func volumeArg() string {
	return strconv.FormatFloat(volume, 'f', -1, 64)
}

// afplayArgs returns the afplay arguments that play path at the configured
// volume.
func afplayArgs(path string) []string {
	return []string{"-v", volumeArg(), path}
}
