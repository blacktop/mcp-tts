package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MCPMessage represents a JSON-RPC message for MCP
type MCPMessage struct {
	JSONRpc string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// MCPResponse represents a JSON-RPC response from MCP
type MCPResponse struct {
	JSONRpc string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

// InitializeParams for MCP initialization
type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	ClientInfo      map[string]any `json:"clientInfo"`
	Capabilities    map[string]any `json:"capabilities"`
}

// ToolCallParams for calling MCP tools
type ToolCallParams struct {
	Name      string `json:"name"`
	Arguments any    `json:"arguments"`
}

// SayTTSArgs for say_tts tool
type SayTTSArgs struct {
	Text  string  `json:"text"`
	Rate  *int    `json:"rate,omitempty"`
	Voice *string `json:"voice,omitempty"`
}

// ElevenLabsArgs for elevenlabs_tts tool
type ElevenLabsArgs struct {
	Text string `json:"text"`
}

// GoogleTTSArgs for google_tts tool
type GoogleTTSArgs struct {
	Text  string  `json:"text"`
	Voice *string `json:"voice,omitempty"`
	Model *string `json:"model,omitempty"`
}

// OpenAITTSArgs for openai_tts tool
type OpenAITTSArgs struct {
	Text         string   `json:"text"`
	Voice        *string  `json:"voice,omitempty"`
	Model        *string  `json:"model,omitempty"`
	Speed        *float64 `json:"speed,omitempty"`
	Instructions *string  `json:"instructions,omitempty"`
}

// Test runner for MCP integration tests
type MCPTestRunner struct {
	t           *testing.T
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	scanner     *bufio.Scanner
	responses   chan MCPResponse
	ctx         context.Context
	cancel      context.CancelFunc
	initialized bool
}

func NewMCPTestRunner(t *testing.T) *MCPTestRunner {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	// Build the path to the mcp-tts binary
	wd, err := os.Getwd()
	require.NoError(t, err)

	// Start the MCP server
	cmd := exec.CommandContext(ctx, "go", "run", filepath.Join(wd, "main.go"), "--verbose")

	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)

	// Also capture stderr for debugging
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	require.NoError(t, err)

	runner := &MCPTestRunner{
		t:         t,
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		scanner:   bufio.NewScanner(stdout),
		responses: make(chan MCPResponse, 10),
		ctx:       ctx,
		cancel:    cancel,
	}

	// Start reading responses in background
	go runner.readResponses()

	return runner
}

func (r *MCPTestRunner) readResponses() {
	defer close(r.responses)

	for r.scanner.Scan() {
		line := strings.TrimSpace(r.scanner.Text())
		if line == "" {
			continue
		}

		var response MCPResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			r.t.Logf("Failed to parse response: %s - Error: %v", line, err)
			continue
		}

		select {
		case r.responses <- response:
		case <-r.ctx.Done():
			return
		}
	}
}

func (r *MCPTestRunner) sendMessage(msg MCPMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	_, err = r.stdin.Write(append(data, '\n'))
	return err
}

func (r *MCPTestRunner) waitForResponse(expectedID int) (MCPResponse, error) {
	timeout := time.After(10 * time.Second)

	for {
		select {
		case response := <-r.responses:
			if response.ID == expectedID {
				return response, nil
			}
			// Put back responses we don't want
			select {
			case r.responses <- response:
			default:
				r.t.Logf("Dropped unexpected response: %+v", response)
			}
		case <-timeout:
			return MCPResponse{}, fmt.Errorf("timeout waiting for response with ID %d", expectedID)
		case <-r.ctx.Done():
			return MCPResponse{}, r.ctx.Err()
		}
	}
}

func (r *MCPTestRunner) initialize() error {
	if r.initialized {
		return nil
	}

	initMsg := MCPMessage{
		JSONRpc: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: InitializeParams{
			ProtocolVersion: "2024-11-05",
			ClientInfo: map[string]any{
				"name":    "integration-test",
				"version": "1.0.0",
			},
			Capabilities: map[string]any{},
		},
	}

	err := r.sendMessage(initMsg)
	if err != nil {
		return err
	}

	_, err = r.waitForResponse(1)
	if err != nil {
		return err
	}

	r.initialized = true
	return nil
}

func (r *MCPTestRunner) listTools() (MCPResponse, error) {
	err := r.initialize()
	if err != nil {
		return MCPResponse{}, err
	}

	listMsg := MCPMessage{
		JSONRpc: "2.0",
		ID:      2,
		Method:  "tools/list",
		Params:  map[string]any{},
	}

	err = r.sendMessage(listMsg)
	if err != nil {
		return MCPResponse{}, err
	}

	return r.waitForResponse(2)
}

func (r *MCPTestRunner) callTool(id int, name string, args any) (MCPResponse, error) {
	err := r.initialize()
	if err != nil {
		return MCPResponse{}, err
	}

	callMsg := MCPMessage{
		JSONRpc: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params: ToolCallParams{
			Name:      name,
			Arguments: args,
		},
	}

	err = r.sendMessage(callMsg)
	if err != nil {
		return MCPResponse{}, err
	}

	return r.waitForResponse(id)
}

func (r *MCPTestRunner) Close() {
	r.cancel()
	if r.stdin != nil {
		r.stdin.Close()
	}
	if r.cmd != nil && r.cmd.Process != nil {
		r.cmd.Process.Kill()
		r.cmd.Wait()
	}
}

// Helper functions
func stringPtr(s string) *string    { return &s }
func intPtr(i int) *int             { return &i }
func float64Ptr(f float64) *float64 { return &f }

func TestMCPIntegration_Initialize(t *testing.T) {
	runner := NewMCPTestRunner(t)
	defer runner.Close()

	err := runner.initialize()
	assert.NoError(t, err, "MCP initialization should succeed")
}

func TestMCPIntegration_ToolsList(t *testing.T) {
	runner := NewMCPTestRunner(t)
	defer runner.Close()

	response, err := runner.listTools()
	require.NoError(t, err, "tools/list should succeed")
	assert.Nil(t, response.Error, "tools/list should not return error")
	assert.NotNil(t, response.Result, "tools/list should return result")

	// Verify the response contains our expected tools
	result, ok := response.Result.(map[string]any)
	require.True(t, ok, "Result should be a map")

	tools, ok := result["tools"].([]any)
	require.True(t, ok, "Result should contain tools array")

	toolNames := make([]string, 0, len(tools))
	for _, tool := range tools {
		toolMap, ok := tool.(map[string]any)
		require.True(t, ok, "Tool should be a map")

		name, ok := toolMap["name"].(string)
		require.True(t, ok, "Tool should have name")

		toolNames = append(toolNames, name)
	}

	expectedTools := []string{"elevenlabs_tts", "google_tts", "openai_tts"}

	// On macOS, we should also have say_tts
	if os.Getenv("GITHUB_ACTIONS") == "" { // Not in CI
		expectedTools = append(expectedTools, "say_tts")
	}

	for _, expectedTool := range expectedTools {
		assert.Contains(t, toolNames, expectedTool, "Should contain tool: %s", expectedTool)
	}
}

func TestMCPIntegration_SayTTS(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") != "" {
		t.Skip("Skipping say_tts test in CI environment")
	}

	runner := NewMCPTestRunner(t)
	defer runner.Close()

	args := SayTTSArgs{
		Text:  "Hello! This is a test of the macOS say command.",
		Rate:  intPtr(200),
		Voice: stringPtr("Alex"),
	}

	response, err := runner.callTool(3, "say_tts", args)
	require.NoError(t, err, "say_tts call should succeed")

	if response.Error != nil {
		t.Logf("say_tts error: %v", response.Error)
		return // Don't fail the test if say command isn't available
	}

	assert.NotNil(t, response.Result, "say_tts should return result")

	// Verify the result contains expected content
	result, ok := response.Result.(map[string]any)
	require.True(t, ok, "Result should be a map")

	content, ok := result["content"].([]any)
	require.True(t, ok, "Result should contain content array")
	require.Len(t, content, 1, "Should have one content item")

	textContent, ok := content[0].(map[string]any)
	require.True(t, ok, "Content should be a map")

	text, ok := textContent["text"].(string)
	require.True(t, ok, "Content should have text")

	assert.Contains(t, text, "Speaking:", "Response should indicate speaking")
}

func TestMCPIntegration_ElevenLabsTTS(t *testing.T) {
	if os.Getenv("ELEVENLABS_API_KEY") == "" {
		t.Skip("Skipping ElevenLabs test: ELEVENLABS_API_KEY not set")
	}

	runner := NewMCPTestRunner(t)
	defer runner.Close()

	args := ElevenLabsArgs{
		Text: "Hello, world! This is a test of ElevenLabs TTS integration.",
	}

	response, err := runner.callTool(4, "elevenlabs_tts", args)
	require.NoError(t, err, "elevenlabs_tts call should succeed")

	if response.Error != nil {
		t.Logf("elevenlabs_tts error: %v", response.Error)
		// Don't fail if API key is invalid or API is unavailable
		return
	}

	assert.NotNil(t, response.Result, "elevenlabs_tts should return result")

	// Verify the result structure
	result, ok := response.Result.(map[string]any)
	require.True(t, ok, "Result should be a map")

	content, ok := result["content"].([]any)
	require.True(t, ok, "Result should contain content array")
	require.Len(t, content, 1, "Should have one content item")

	textContent, ok := content[0].(map[string]any)
	require.True(t, ok, "Content should be a map")

	text, ok := textContent["text"].(string)
	require.True(t, ok, "Content should have text")

	assert.Contains(t, text, "Speaking:", "Response should indicate speaking")
}

func TestMCPIntegration_GoogleTTS(t *testing.T) {
	if os.Getenv("GOOGLE_AI_API_KEY") == "" && os.Getenv("GEMINI_API_KEY") == "" {
		t.Skip("Skipping Google TTS test: GOOGLE_AI_API_KEY or GEMINI_API_KEY not set")
	}

	runner := NewMCPTestRunner(t)
	defer runner.Close()

	args := GoogleTTSArgs{
		Text:  "Hello! This is a test of Google's TTS API.",
		Voice: stringPtr("Kore"),
		Model: stringPtr("gemini-2.5-flash-preview-tts"),
	}

	response, err := runner.callTool(5, "google_tts", args)
	require.NoError(t, err, "google_tts call should succeed")

	if response.Error != nil {
		t.Logf("google_tts error: %v", response.Error)
		// Don't fail if API key is invalid or API is unavailable
		return
	}

	assert.NotNil(t, response.Result, "google_tts should return result")

	// Verify the result structure
	result, ok := response.Result.(map[string]any)
	require.True(t, ok, "Result should be a map")

	content, ok := result["content"].([]any)
	require.True(t, ok, "Result should contain content array")
	require.Len(t, content, 1, "Should have one content item")

	textContent, ok := content[0].(map[string]any)
	require.True(t, ok, "Content should be a map")

	text, ok := textContent["text"].(string)
	require.True(t, ok, "Content should have text")

	assert.Contains(t, text, "Speaking:", "Response should indicate speaking")
}

func TestMCPIntegration_OpenAITTS(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("Skipping OpenAI TTS test: OPENAI_API_KEY not set")
	}

	runner := NewMCPTestRunner(t)
	defer runner.Close()

	args := OpenAITTSArgs{
		Text:  "Hello! This is a test of OpenAI's text-to-speech API.",
		Voice: stringPtr("coral"),
		Speed: float64Ptr(1.2),
		Model: stringPtr("gpt-4o-mini-tts"),
	}

	response, err := runner.callTool(6, "openai_tts", args)
	require.NoError(t, err, "openai_tts call should succeed")

	if response.Error != nil {
		t.Logf("openai_tts error: %v", response.Error)
		// Don't fail if API key is invalid or API is unavailable
		return
	}

	assert.NotNil(t, response.Result, "openai_tts should return result")

	// Verify the result structure
	result, ok := response.Result.(map[string]any)
	require.True(t, ok, "Result should be a map")

	content, ok := result["content"].([]any)
	require.True(t, ok, "Result should contain content array")
	require.Len(t, content, 1, "Should have one content item")

	textContent, ok := content[0].(map[string]any)
	require.True(t, ok, "Content should be a map")

	text, ok := textContent["text"].(string)
	require.True(t, ok, "Content should have text")

	assert.Contains(t, text, "Speaking:", "Response should indicate speaking")
}

func TestMCPIntegration_ErrorHandling(t *testing.T) {
	runner := NewMCPTestRunner(t)
	defer runner.Close()

	// Test with empty text
	args := SayTTSArgs{
		Text: "",
	}

	response, err := runner.callTool(7, "say_tts", args)
	require.NoError(t, err, "Tool call should complete even with error")

	// Should return an error result
	assert.NotNil(t, response.Result, "Should return a result")

	result, ok := response.Result.(map[string]any)
	require.True(t, ok, "Result should be a map")

	content, ok := result["content"].([]any)
	require.True(t, ok, "Result should contain content array")
	require.Len(t, content, 1, "Should have one content item")

	textContent, ok := content[0].(map[string]any)
	require.True(t, ok, "Content should be a map")

	text, ok := textContent["text"].(string)
	require.True(t, ok, "Content should have text")

	assert.Contains(t, text, "Error:", "Response should indicate error")
}

// TestMCPIntegration_JSONCompatibility tests that our Go integration tests
// produce similar results to the JSON test files
func TestMCPIntegration_JSONCompatibility(t *testing.T) {
	testCases := []struct {
		name     string
		jsonFile string
		toolName string
		skipCI   bool
	}{
		{
			name:     "say_tts",
			jsonFile: "test/json/say.json",
			toolName: "say_tts",
			skipCI:   true, // Skip on CI since macOS say command not available
		},
		{
			name:     "elevenlabs_tts",
			jsonFile: "test/json/elevenlabs.json",
			toolName: "elevenlabs_tts",
		},
		{
			name:     "google_tts",
			jsonFile: "test/json/google_tts.json",
			toolName: "google_tts",
		},
		{
			name:     "openai_tts",
			jsonFile: "test/json/openai_tts.json",
			toolName: "openai_tts",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skipCI && os.Getenv("GITHUB_ACTIONS") != "" {
				t.Skip("Skipping test in CI environment")
			}

			// Read the JSON test file
			jsonData, err := os.ReadFile(tc.jsonFile)
			require.NoError(t, err, "Should be able to read JSON test file")

			// Parse JSON messages
			lines := bytes.Split(jsonData, []byte("\n"))
			var toolCallMsg MCPMessage

			for _, line := range lines {
				line = bytes.TrimSpace(line)
				if len(line) == 0 {
					continue
				}

				var msg MCPMessage
				err := json.Unmarshal(line, &msg)
				if err != nil {
					continue
				}

				if msg.Method == "tools/call" {
					toolCallMsg = msg
					break
				}
			}

			require.NotEmpty(t, toolCallMsg.Method, "Should find tools/call message in JSON file")

			// Run our Go integration test and compare
			runner := NewMCPTestRunner(t)
			defer runner.Close()

			// Extract tool call parameters
			params, ok := toolCallMsg.Params.(map[string]any)
			require.True(t, ok, "Params should be a map")

			name, ok := params["name"].(string)
			require.True(t, ok, "Should have tool name")
			require.Equal(t, tc.toolName, name, "Tool name should match")

			args, ok := params["arguments"]
			require.True(t, ok, "Should have arguments")

			// Call the tool
			response, err := runner.callTool(toolCallMsg.ID, name, args)
			require.NoError(t, err, "Tool call should succeed")

			// Basic validation - the response should have proper structure
			assert.NotNil(t, response.Result, "Should return a result")

			result, ok := response.Result.(map[string]any)
			require.True(t, ok, "Result should be a map")

			content, ok := result["content"].([]any)
			require.True(t, ok, "Result should contain content array")
			require.NotEmpty(t, content, "Should have content")

			t.Logf("Test %s completed successfully", tc.name)
		})
	}
}
