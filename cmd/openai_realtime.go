package cmd

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/realtime"
)

const (
	// openAITTSSampleRate is the only PCM rate the Realtime API produces.
	openAITTSSampleRate = 24000
	// openAISynthesisTimeout bounds one whole Realtime exchange, so a stalled
	// socket cannot hold a tool call open indefinitely.
	openAISynthesisTimeout = 3 * time.Minute
	// A Realtime model answers its input by default; speaking the text as
	// written has to be asked for.
	openAIReadAloudInstructions = "You are a text-to-speech engine. " +
		"Read the user's message aloud exactly as written, word for word. " +
		"Do not answer it, comment on it, translate it, add to it or leave anything out."
)

type openAISpeechRequest struct {
	Model        string
	Voice        string
	Speed        float64
	Text         string
	Instructions string
}

func (r openAISpeechRequest) session() realtime.RealtimeSessionCreateRequestParam {
	instructions := openAIReadAloudInstructions
	if r.Instructions != "" {
		instructions += "\n\nDelivery style: " + r.Instructions
	}
	return realtime.RealtimeSessionCreateRequestParam{
		OutputModalities: []string{"audio"},
		Instructions:     openai.String(instructions),
		Audio: realtime.RealtimeAudioConfigParam{
			Output: realtime.RealtimeAudioConfigOutputParam{
				Format: realtime.RealtimeAudioFormatsUnionParam{
					OfAudioPCM: &realtime.RealtimeAudioFormatsAudioPCMParam{
						Type: "audio/pcm",
						Rate: openAITTSSampleRate,
					},
				},
				Voice: realtime.RealtimeAudioConfigOutputVoiceUnionParam{
					OfString: openai.String(r.Voice),
				},
				Speed: openai.Float(r.Speed),
			},
		},
	}
}

// synthesizeOpenAISpeech speaks req.Text through the Realtime API and returns
// the audio as 16-bit little-endian mono PCM at openAITTSSampleRate.
func synthesizeOpenAISpeech(
	ctx context.Context, client openai.Client, req openAISpeechRequest,
) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, openAISynthesisTimeout)
	defer cancel()

	conn, err := client.Realtime.Connect(
		ctx, realtime.RealtimeConnectionOptions{}, option.WithQuery("model", req.Model),
	)
	if err != nil {
		return nil, fmt.Errorf("connecting to the Realtime API: %w", err)
	}
	// The exchange is over either way, so there is nothing to finish politely.
	defer conn.Abort()

	events := []realtime.RealtimeClientEventUnionParam{
		realtime.RealtimeClientEventParamOfSessionUpdate(req.session()),
		realtime.RealtimeClientEventParamOfConversationItemCreate(
			realtime.RealtimeConversationItemUserMessageParam{
				Content: []realtime.RealtimeConversationItemUserMessageContentParam{
					{Type: "input_text", Text: openai.String(req.Text)},
				},
			},
		),
		{OfResponseCreate: &realtime.ResponseCreateEventParam{}},
	}
	for _, event := range events {
		if err := conn.Send(ctx, event); err != nil {
			return nil, fmt.Errorf("sending Realtime request: %w", err)
		}
	}
	return collectOpenAISpeech(ctx, conn)
}

func collectOpenAISpeech(ctx context.Context, conn *realtime.RealtimeConnection) ([]byte, error) {
	var audio []byte
	for {
		event, err := conn.Recv(ctx)
		if err != nil {
			return nil, fmt.Errorf("waiting for Realtime audio: %w", err)
		}
		switch event := event.AsAny().(type) {
		case realtime.RealtimeErrorEvent:
			return nil, fmt.Errorf("Realtime API error: %s", event.Error.Message)
		case realtime.ResponseAudioDeltaEvent:
			audio, err = base64.StdEncoding.AppendDecode(audio, []byte(event.Delta))
			if err != nil {
				return nil, fmt.Errorf("decoding Realtime audio chunk: %w", err)
			}
		case realtime.ResponseDoneEvent:
			status := event.Response.Status
			if status != realtime.RealtimeResponseStatusCompleted {
				return nil, fmt.Errorf("Realtime response ended with status %q", status)
			}
			if len(audio) == 0 {
				return nil, errors.New("Realtime response contained no audio")
			}
			return audio, nil
		}
	}
}
