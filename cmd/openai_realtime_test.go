package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coder/websocket"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realtimeExchange records what a fake Realtime server saw from the client.
type realtimeExchange struct {
	model  string
	events []map[string]any
}

// fakeRealtimeServer accepts one Realtime session, reads the client's events up
// to response.create, then sends replies.
func fakeRealtimeServer(t *testing.T, replies ...map[string]any) (openai.Client, <-chan realtimeExchange) {
	t.Helper()
	seen := make(chan realtimeExchange, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accepting websocket: %v", err)
			return
		}
		defer conn.CloseNow()

		exchange := realtimeExchange{model: r.URL.Query().Get("model")}
		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				t.Errorf("reading client event: %v", err)
				return
			}
			var event map[string]any
			if err := json.Unmarshal(data, &event); err != nil {
				t.Errorf("decoding client event: %v", err)
				return
			}
			exchange.events = append(exchange.events, event)
			if event["type"] == "response.create" {
				break
			}
		}
		seen <- exchange

		for _, reply := range replies {
			data, err := json.Marshal(reply)
			if err != nil {
				t.Errorf("encoding reply: %v", err)
				return
			}
			if err := conn.Write(r.Context(), websocket.MessageText, data); err != nil {
				return
			}
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	client := openai.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
	return client, seen
}

func audioDelta(pcm []byte) map[string]any {
	return map[string]any{
		"type":  "response.output_audio.delta",
		"delta": base64.StdEncoding.EncodeToString(pcm),
	}
}

func responseDone(status string) map[string]any {
	return map[string]any{
		"type":     "response.done",
		"response": map[string]any{"status": status},
	}
}

func testSpeechRequest() openAISpeechRequest {
	return openAISpeechRequest{
		Model: DefaultOpenAIModel,
		Voice: "cedar",
		Speed: 1.25,
		Text:  "What time is it?",
	}
}

func TestSynthesizeOpenAISpeechSendsSettingsAndJoinsAudio(t *testing.T) {
	client, seen := fakeRealtimeServer(t,
		map[string]any{"type": "session.updated"},
		audioDelta([]byte{1, 2, 3, 4}),
		audioDelta([]byte{5, 6}),
		responseDone("completed"),
	)
	req := testSpeechRequest()
	req.Instructions = "Whisper."

	audio, err := synthesizeOpenAISpeech(context.Background(), client, req)

	require.NoError(t, err)
	assert.Equal(t, []byte{1, 2, 3, 4, 5, 6}, audio)

	exchange := <-seen
	assert.Equal(t, DefaultOpenAIModel, exchange.model)

	sent, err := json.Marshal(exchange.events)
	require.NoError(t, err)
	assert.JSONEq(t, `[
		{
			"type": "session.update",
			"session": {
				"type": "realtime",
				"output_modalities": ["audio"],
				"instructions": `+mustJSON(t, openAIReadAloudInstructions+"\n\nDelivery style: Whisper.")+`,
				"audio": {"output": {
					"format": {"type": "audio/pcm", "rate": 24000},
					"voice": "cedar",
					"speed": 1.25
				}}
			}
		},
		{
			"type": "conversation.item.create",
			"item": {
				"type": "message",
				"role": "user",
				"content": [{"type": "input_text", "text": "What time is it?"}]
			}
		},
		{"type": "response.create"}
	]`, string(sent))
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

func TestSynthesizeOpenAISpeechFailures(t *testing.T) {
	tests := []struct {
		name    string
		replies []map[string]any
		wantErr string
	}{
		{
			name: "API error event",
			replies: []map[string]any{{
				"type":  "error",
				"error": map[string]any{"message": "model not found", "type": "invalid_request_error"},
			}},
			wantErr: "Realtime API error: model not found",
		},
		{
			name:    "truncated response",
			replies: []map[string]any{audioDelta([]byte{1, 2}), responseDone("incomplete")},
			wantErr: `ended with status "incomplete"`,
		},
		{
			name:    "response without audio",
			replies: []map[string]any{responseDone("completed")},
			wantErr: "contained no audio",
		},
		{
			name: "corrupt audio chunk",
			replies: []map[string]any{
				{"type": "response.output_audio.delta", "delta": "not base64!"},
			},
			wantErr: "decoding Realtime audio chunk",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := fakeRealtimeServer(t, tt.replies...)

			audio, err := synthesizeOpenAISpeech(context.Background(), client, testSpeechRequest())

			require.ErrorContains(t, err, tt.wantErr)
			assert.Nil(t, audio)
		})
	}
}

func TestSynthesizeOpenAISpeechStopsWhenCancelled(t *testing.T) {
	client, seen := fakeRealtimeServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-seen
		cancel()
	}()

	_, err := synthesizeOpenAISpeech(ctx, client, testSpeechRequest())

	require.ErrorIs(t, err, context.Canceled)
}

func TestSynthesizeOpenAISpeechReportsConnectionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	client := openai.NewClient(
		option.WithAPIKey("test-key"), option.WithBaseURL(server.URL), option.WithMaxRetries(0),
	)

	_, err := synthesizeOpenAISpeech(context.Background(), client, testSpeechRequest())

	require.ErrorContains(t, err, "connecting to the Realtime API")
}
