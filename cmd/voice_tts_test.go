package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterVoiceTTSToolPATHGate(t *testing.T) {
	originalLookPath := voiceSayLookPath
	t.Cleanup(func() {
		voiceSayLookPath = originalLookPath
	})

	baselineJSON, _ := listedTools(t, false)

	voiceSayLookPath = func(string) (string, error) {
		return "", errors.New("voice-say not found")
	}
	absentJSON, absentTools := listedTools(t, true)
	assert.Equal(t, baselineJSON, absentJSON, "missing voice-say must not change tools/list")
	assert.Equal(t, []string{"existing_tool"}, toolNames(absentTools))

	voiceSayLookPath = func(name string) (string, error) {
		assert.Equal(t, voiceSayBinary, name)
		return "/test/bin/voice-say", nil
	}
	_, presentTools := listedTools(t, true)
	assert.Equal(t, []string{"existing_tool", "voice_tts"}, toolNames(presentTools))

	var voiceTool *mcp.Tool
	for _, tool := range presentTools {
		if tool.Name == "voice_tts" {
			voiceTool = tool
			break
		}
	}
	require.NotNil(t, voiceTool)
	assert.Contains(t, voiceTool.Description, "local Qwen3-TTS")
	assert.Contains(t, voiceTool.Description, "requires no API key")
	assert.Contains(t, voiceTool.Description, "several seconds")

	schemaData, err := json.Marshal(voiceTool.InputSchema)
	require.NoError(t, err)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(schemaData, &schema))
	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	assert.ElementsMatch(
		t,
		[]string{"text", "voice", "tier", "style", "describe"},
		slices.Collect(maps.Keys(properties)),
	)
	voiceSchema, ok := properties["voice"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"Ryan", "Aiden"}, voiceSchema["enum"])
	tierSchema, ok := properties["tier"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"small", "large"}, tierSchema["enum"])
	assert.Equal(t, []any{"text"}, schema["required"])
}

func TestVoiceTTSToolInvocation(t *testing.T) {
	originalLookPath := voiceSayLookPath
	originalRunner := voiceSayRunner
	originalLock := voiceTTSLock
	originalSuppress := suppressSpeakingOutput
	t.Cleanup(func() {
		voiceSayLookPath = originalLookPath
		voiceSayRunner = originalRunner
		voiceTTSLock = originalLock
		suppressSpeakingOutput = originalSuppress
	})

	const binaryPath = "/test/bin/voice-say"
	voiceSayLookPath = func(string) (string, error) {
		return binaryPath, nil
	}

	var (
		lockAcquired bool
		lockReleased bool
		gotPath      string
		gotArgs      []string
		gotText      string
	)
	voiceTTSLock = func(context.Context) (func(), error) {
		lockAcquired = true
		return func() {
			lockReleased = true
		}, nil
	}
	voiceSayRunner = func(_ context.Context, path string, args []string, text string) error {
		gotPath = path
		gotArgs = slices.Clone(args)
		gotText = text
		return nil
	}
	suppressSpeakingOutput = false

	server := mcp.NewServer(
		&mcp.Implementation{Name: "voice-tts-test-server", Version: "1.0.0"},
		nil,
	)
	registerVoiceTTSTool(server)
	clientSession := connectTestClient(t, server)

	result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "voice_tts",
		Arguments: map[string]any{
			"text":  "Local speech",
			"voice": "Ryan",
			"tier":  "small",
			"style": "calm and unhurried",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Equal(t, "Speaking: Local speech", extractTextFromMCPResult(result))

	assert.True(t, lockAcquired)
	assert.True(t, lockReleased)
	assert.Equal(t, binaryPath, gotPath)
	assert.Equal(t, []string{
		"--quiet",
		"--wait",
		"--voice", "Ryan",
		"--tier", "small",
		"--style", "calm and unhurried",
	}, gotArgs)
	assert.Equal(t, "Local speech", gotText)
}

func TestVoiceSayArgs(t *testing.T) {
	stringPointer := func(value string) *string {
		return &value
	}

	tests := []struct {
		name    string
		input   VoiceTTSParams
		want    []string
		wantErr string
	}{
		{
			name:  "defaults still wait quietly",
			input: VoiceTTSParams{Text: "hello"},
			want:  []string{"--quiet", "--wait"},
		},
		{
			name: "preset voice tier and style",
			input: VoiceTTSParams{
				Text:  "hello",
				Voice: stringPointer("Aiden"),
				Tier:  stringPointer("large"),
				Style: stringPointer("bright and animated"),
			},
			want: []string{
				"--quiet",
				"--wait",
				"--voice", "Aiden",
				"--tier", "large",
				"--style", "bright and animated",
			},
		},
		{
			name: "described voice",
			input: VoiceTTSParams{
				Text:     "hello",
				Describe: stringPointer("a warm documentary narrator"),
			},
			want: []string{
				"--quiet",
				"--wait",
				"--describe", "a warm documentary narrator",
			},
		},
		{
			name: "voice conflicts with describe",
			input: VoiceTTSParams{
				Text:     "hello",
				Voice:    stringPointer("Ryan"),
				Describe: stringPointer("a warm narrator"),
			},
			wantErr: "voice and describe cannot be used together",
		},
		{
			name: "invalid voice",
			input: VoiceTTSParams{
				Text:  "hello",
				Voice: stringPointer("Unknown"),
			},
			wantErr: `unsupported voice "Unknown"; expected Ryan or Aiden`,
		},
		{
			name: "invalid tier",
			input: VoiceTTSParams{
				Text: "hello",
				Tier: stringPointer("medium"),
			},
			wantErr: `unsupported tier "medium"; expected small or large`,
		},
		{
			name:    "empty text",
			input:   VoiceTTSParams{},
			wantErr: "empty text provided",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := voiceSayArgs(test.input)
			if test.wantErr != "" {
				require.EqualError(t, err, test.wantErr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestCallVoiceTTSCommandFailure(t *testing.T) {
	originalRunner := voiceSayRunner
	originalLock := voiceTTSLock
	t.Cleanup(func() {
		voiceSayRunner = originalRunner
		voiceTTSLock = originalLock
	})

	released := false
	voiceTTSLock = func(context.Context) (func(), error) {
		return func() {
			released = true
		}, nil
	}
	voiceSayRunner = func(context.Context, string, []string, string) error {
		return errors.New("model failed to load")
	}

	result := callVoiceTTS(context.Background(), "/test/bin/voice-say", VoiceTTSParams{Text: "hello"})
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, extractTextFromMCPResult(result), "voice-say failed: model failed to load")
	assert.True(t, released)
}

func TestCallVoiceTTSRejectsNoPlay(t *testing.T) {
	originalRunner := voiceSayRunner
	originalLock := voiceTTSLock
	originalOutputDir := outputDir
	originalNoPlay := noPlay
	t.Cleanup(func() {
		voiceSayRunner = originalRunner
		voiceTTSLock = originalLock
		outputDir = originalOutputDir
		noPlay = originalNoPlay
	})

	lockCalled := false
	runnerCalled := false
	voiceTTSLock = func(context.Context) (func(), error) {
		lockCalled = true
		return func() {}, nil
	}
	voiceSayRunner = func(context.Context, string, []string, string) error {
		runnerCalled = true
		return nil
	}
	outputDir = t.TempDir()
	noPlay = true

	result := callVoiceTTS(context.Background(), "/test/bin/voice-say", VoiceTTSParams{Text: "hello"})
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, extractTextFromMCPResult(result), "voice_tts cannot run with --no-play")
	assert.False(t, lockCalled)
	assert.False(t, runnerCalled)
}

func listedTools(t *testing.T, registerVoice bool) ([]byte, []*mcp.Tool) {
	t.Helper()

	server := mcp.NewServer(
		&mcp.Implementation{Name: "voice-tts-list-test-server", Version: "1.0.0"},
		nil,
	)
	mcp.AddTool(server, &mcp.Tool{Name: "existing_tool"}, func(
		context.Context,
		*mcp.CallToolRequest,
		struct{},
	) (*mcp.CallToolResult, any, error) {
		return textResult("existing"), nil, nil
	})
	if registerVoice {
		registerVoiceTTSTool(server)
	}

	clientSession := connectTestClient(t, server)
	result, err := clientSession.ListTools(context.Background(), nil)
	require.NoError(t, err)

	data, err := json.Marshal(result)
	require.NoError(t, err)
	return data, result.Tools
}

func connectTestClient(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = serverSession.Close()
	})

	client := mcp.NewClient(
		&mcp.Implementation{Name: "voice-tts-test-client", Version: "1.0.0"},
		nil,
	)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = clientSession.Close()
	})
	return clientSession
}

func toolNames(tools []*mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}
