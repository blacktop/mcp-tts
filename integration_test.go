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
	ID      any    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// MCPResponse represents a JSON-RPC response from MCP
type MCPResponse struct {
	JSONRpc string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

// MCPRequest represents a JSON-RPC request emitted by the server.
type MCPRequest struct {
	JSONRpc string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
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
	requests    chan MCPRequest
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
		requests:  make(chan MCPRequest, 10),
		ctx:       ctx,
		cancel:    cancel,
	}

	// Start reading responses in background
	go runner.readResponses()

	return runner
}

func (r *MCPTestRunner) readResponses() {
	defer close(r.responses)
	defer close(r.requests)

	for r.scanner.Scan() {
		line := strings.TrimSpace(r.scanner.Text())
		if line == "" {
			continue
		}

		var message struct {
			JSONRpc string `json:"jsonrpc"`
			ID      any    `json:"id,omitempty"`
			Method  string `json:"method,omitempty"`
			Params  any    `json:"params,omitempty"`
			Result  any    `json:"result,omitempty"`
			Error   any    `json:"error,omitempty"`
		}
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		if err := decoder.Decode(&message); err != nil {
			r.t.Logf("Failed to parse response: %s - Error: %v", line, err)
			continue
		}

		if message.Method != "" {
			if message.ID == nil {
				continue
			}
			select {
			case r.requests <- MCPRequest{
				JSONRpc: message.JSONRpc,
				ID:      message.ID,
				Method:  message.Method,
				Params:  message.Params,
			}:
			case <-r.ctx.Done():
				return
			}
			continue
		}

		if message.ID == nil {
			r.t.Logf("Dropped unexpected JSON-RPC message without id: %s", line)
			continue
		}

		select {
		case r.responses <- MCPResponse{
			JSONRpc: message.JSONRpc,
			ID:      message.ID,
			Result:  message.Result,
			Error:   message.Error,
		}:
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

func (r *MCPTestRunner) sendResult(id any, result any) error {
	data, err := json.Marshal(MCPResponse{
		JSONRpc: "2.0",
		ID:      id,
		Result:  result,
	})
	if err != nil {
		return err
	}

	_, err = r.stdin.Write(append(data, '\n'))
	return err
}

func messageIDMatches(actual any, expected int) bool {
	switch value := actual.(type) {
	case int:
		return value == expected
	case int64:
		return value == int64(expected)
	case float64:
		return value == float64(expected)
	case json.Number:
		id, err := value.Int64()
		return err == nil && id == int64(expected)
	default:
		return false
	}
}

func requireMessageIDInt(t *testing.T, id any) int {
	t.Helper()

	switch value := id.(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		id, err := value.Int64()
		require.NoError(t, err)
		return int(id)
	default:
		t.Fatalf("unexpected JSON-RPC id type %T", id)
		return 0
	}
}

func (r *MCPTestRunner) waitForResponse(expectedID int) (MCPResponse, error) {
	timeout := time.After(10 * time.Second)

	for {
		select {
		case response, ok := <-r.responses:
			if !ok {
				return MCPResponse{}, fmt.Errorf("response stream closed while waiting for id %d", expectedID)
			}
			if messageIDMatches(response.ID, expectedID) {
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
	return r.initializeWithParams(1, InitializeParams{
		ProtocolVersion: "2024-11-05",
		ClientInfo: map[string]any{
			"name":    "integration-test",
			"version": "1.0.0",
		},
		Capabilities: map[string]any{},
	})
}

func (r *MCPTestRunner) initializeWithParams(id int, params InitializeParams) error {
	if r.initialized {
		return nil
	}

	initMsg := MCPMessage{
		JSONRpc: "2.0",
		ID:      id,
		Method:  "initialize",
		Params:  params,
	}

	err := r.sendMessage(initMsg)
	if err != nil {
		return err
	}

	_, err = r.waitForResponse(id)
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

type elicitationReply struct {
	Action   string
	Content  map[string]any
	Validate func(*testing.T, MCPRequest)
}

func (r *MCPTestRunner) callToolWithElicitations(id int, name string, args any, replies []elicitationReply) (MCPResponse, error) {
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

	if err := r.sendMessage(callMsg); err != nil {
		return MCPResponse{}, err
	}

	timeout := time.After(10 * time.Second)
	replyIndex := 0

	for {
		select {
		case response, ok := <-r.responses:
			if !ok {
				return MCPResponse{}, fmt.Errorf("response stream closed while waiting for tool result")
			}
			if !messageIDMatches(response.ID, id) {
				r.t.Logf("Ignoring unexpected response while waiting for tool result: %+v", response)
				continue
			}
			if replyIndex != len(replies) {
				return MCPResponse{}, fmt.Errorf("tool returned before all elicitation replies were used (%d of %d)", replyIndex, len(replies))
			}
			return response, nil
		case request, ok := <-r.requests:
			if !ok {
				return MCPResponse{}, fmt.Errorf("request stream closed while waiting for elicitation")
			}
			if request.Method != "elicitation/create" {
				return MCPResponse{}, fmt.Errorf("unexpected server request %q", request.Method)
			}
			if replyIndex >= len(replies) {
				return MCPResponse{}, fmt.Errorf("received unexpected elicitation request: %+v", request)
			}
			reply := replies[replyIndex]
			replyIndex++
			if reply.Validate != nil {
				reply.Validate(r.t, request)
			}

			result := map[string]any{"action": reply.Action}
			if reply.Content != nil {
				result["content"] = reply.Content
			}
			if err := r.sendResult(request.ID, result); err != nil {
				return MCPResponse{}, err
			}
		case <-timeout:
			return MCPResponse{}, fmt.Errorf("timeout waiting for tool result with elicitation")
		case <-r.ctx.Done():
			return MCPResponse{}, r.ctx.Err()
		}
	}
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

func parseJSONLMessages(t *testing.T, path string) []MCPMessage {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err, "Should be able to read JSONL fixture")

	lines := bytes.Split(data, []byte("\n"))
	messages := make([]MCPMessage, 0, len(lines))

	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var msg MCPMessage
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		err := decoder.Decode(&msg)
		require.NoError(t, err, "Should parse JSONL message")
		messages = append(messages, msg)
	}

	return messages
}

func interactiveTTSToolCallFromFixture(t *testing.T) (int, InitializeParams, int, string, any) {
	t.Helper()

	messages := parseJSONLMessages(t, "test/json/tts_elicitation.jsonl")

	var initMsg *MCPMessage
	var toolCallMsg *MCPMessage
	for i := range messages {
		switch messages[i].Method {
		case "initialize":
			initMsg = &messages[i]
		case "tools/call":
			toolCallMsg = &messages[i]
		}
	}

	require.NotNil(t, initMsg, "fixture should contain initialize message")
	require.NotNil(t, toolCallMsg, "fixture should contain tools/call message")

	initParamsBytes, err := json.Marshal(initMsg.Params)
	require.NoError(t, err)

	var initParams InitializeParams
	err = json.Unmarshal(initParamsBytes, &initParams)
	require.NoError(t, err)

	toolParams, ok := toolCallMsg.Params.(map[string]any)
	require.True(t, ok, "tool call params should be a map")

	name, ok := toolParams["name"].(string)
	require.True(t, ok, "tool call should specify a tool name")
	require.Equal(t, "tts", name)

	args, ok := toolParams["arguments"]
	require.True(t, ok, "tool call should include arguments")

	return requireMessageIDInt(t, initMsg.ID), initParams, requireMessageIDInt(t, toolCallMsg.ID), name, args
}

func providerSelectionValidator(t *testing.T, request MCPRequest) {
	t.Helper()

	params, ok := request.Params.(map[string]any)
	require.True(t, ok, "elicitation params should be a map")
	assert.Equal(t, "Which TTS provider would you like to use?", params["message"])

	schema, ok := params["requestedSchema"].(map[string]any)
	require.True(t, ok, "provider selection should include a schema")
	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	providerField, ok := properties["provider"].(map[string]any)
	require.True(t, ok)
	enumValues, ok := providerField["enum"].([]any)
	require.True(t, ok)
	assert.Contains(t, enumValues, "Google Gemini")
	assert.Contains(t, enumValues, "OpenAI")
}

func openAISettingsValidator(t *testing.T, request MCPRequest) {
	t.Helper()

	params, ok := request.Params.(map[string]any)
	require.True(t, ok, "settings elicitation params should be a map")
	assert.Equal(t, "Configure voice settings (or accept defaults):", params["message"])

	schema, ok := params["requestedSchema"].(map[string]any)
	require.True(t, ok, "settings elicitation should include a schema")
	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, properties, "voice")
	assert.Contains(t, properties, "model")
	assert.Contains(t, properties, "speed")
}

func interactiveTTSTextResult(t *testing.T, replies []elicitationReply) string {
	t.Helper()

	t.Setenv("GOOGLE_AI_API_KEY", "test-google-api-key")
	t.Setenv("OPENAI_API_KEY", "test-openai-api-key")

	initID, initParams, toolCallID, name, args := interactiveTTSToolCallFromFixture(t)

	runner := NewMCPTestRunner(t)
	defer runner.Close()

	err := runner.initializeWithParams(initID, initParams)
	require.NoError(t, err, "initialize should succeed with elicitation capability")

	response, err := runner.callToolWithElicitations(toolCallID, name, args, replies)
	require.NoError(t, err, "interactive tts call should succeed")
	assert.Nil(t, response.Error, "interactive tts should not return JSON-RPC error")
	assert.NotNil(t, response.Result, "interactive tts should return a result")

	result, ok := response.Result.(map[string]any)
	require.True(t, ok, "result should be a map")

	content, ok := result["content"].([]any)
	require.True(t, ok, "result should contain content")
	require.NotEmpty(t, content, "result content should not be empty")

	textContent, ok := content[0].(map[string]any)
	require.True(t, ok, "text content should be a map")

	text, ok := textContent["text"].(string)
	require.True(t, ok, "result content should include text")
	return text
}

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

	expectedTools := []string{"elevenlabs_tts", "google_tts", "openai_tts", "tts"}

	// On macOS, we should also have say_tts
	if os.Getenv("GITHUB_ACTIONS") == "" { // Not in CI
		expectedTools = append(expectedTools, "say_tts")
	}

	for _, expectedTool := range expectedTools {
		assert.Contains(t, toolNames, expectedTool, "Should contain tool: %s", expectedTool)
	}
}

func TestMCPIntegration_TTSInteractiveJSONL(t *testing.T) {
	text := interactiveTTSTextResult(
		t,
		[]elicitationReply{
			{
				Action:   "accept",
				Content:  map[string]any{"provider": "OpenAI"},
				Validate: providerSelectionValidator,
			},
			{
				Action:   "accept",
				Content:  map[string]any{},
				Validate: openAISettingsValidator,
			},
		},
	)
	assert.Contains(t, text, "User selected OpenAI. Please call openai_tts with arguments:")
	assert.Contains(t, text, `"text":"Hello from the interactive tts JSONL test."`)
	assert.Contains(t, text, `"voice":"alloy"`)
	assert.Contains(t, text, `"model":"gpt-4o-mini-tts-2025-12-15"`)
	assert.Contains(t, text, `"speed":1`)
}

func TestMCPIntegration_TTSInteractiveCancellationJSONL(t *testing.T) {
	t.Run("provider selection cancel returns cancellation text", func(t *testing.T) {
		text := interactiveTTSTextResult(
			t,
			[]elicitationReply{
				{
					Action:   "cancel",
					Validate: providerSelectionValidator,
				},
			},
		)

		assert.Equal(t, "Request cancelled", text)
	})

	t.Run("settings decline returns cancellation text", func(t *testing.T) {
		text := interactiveTTSTextResult(
			t,
			[]elicitationReply{
				{
					Action:   "accept",
					Content:  map[string]any{"provider": "OpenAI"},
					Validate: providerSelectionValidator,
				},
				{
					Action:   "decline",
					Validate: openAISettingsValidator,
				},
			},
		)

		assert.Equal(t, "Request cancelled", text)
	})
}

func TestMCPIntegration_SayTTS(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") != "" {
		t.Skip("Skipping say_tts test in CI environment")
	}

	runner := NewMCPTestRunner(t)
	defer runner.Close()

	args := SayTTSArgs{
		Text: "Hello! This is a test of the macOS say command.",
		Rate: intPtr(200),
		// Don't specify voice - use system default to avoid "voice not installed" errors
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
			response, err := runner.callTool(requireMessageIDInt(t, toolCallMsg.ID), name, args)
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
