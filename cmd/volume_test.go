package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setVolume(t *testing.T, v float64) {
	t.Helper()
	prev := volume
	volume = v
	t.Cleanup(func() { volume = prev })
}

func TestResolveVolume(t *testing.T) {
	for _, tc := range []struct {
		name      string
		flag      float64
		flagGiven bool
		env       string
		want      float64
		wantErr   string
	}{
		{name: "default", flag: defaultVolume, want: 0.5},
		{name: "env sets it", flag: defaultVolume, env: "0.25", want: 0.25},
		{name: "flag beats env", flag: 0.8, flagGiven: true, env: "0.25", want: 0.8},
		{name: "full volume", flag: defaultVolume, env: "1", want: 1},
		{name: "env not a number", flag: defaultVolume, env: "loud", wantErr: `MCP_TTS_VOLUME="loud" is not a number`},
		{name: "silent", flag: defaultVolume, env: "0", wantErr: "volume 0 is out of range"},
		{name: "above full", flag: defaultVolume, env: "1.5", wantErr: "volume 1.5 is out of range"},
		{name: "NaN", flag: defaultVolume, env: "NaN", wantErr: "volume NaN is out of range"},
		{name: "negative flag", flag: -0.5, flagGiven: true, wantErr: "volume -0.5 is out of range"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setVolume(t, tc.flag)

			err := resolveVolume(tc.flagGiven, tc.env)

			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				assert.Equal(t, tc.flag, volume, "a rejected value must not be kept")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, volume)
		})
	}
}
