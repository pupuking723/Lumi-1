package http

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVertexLiveWebSocketURL(t *testing.T) {
	u, err := vertexLiveWebSocketURL(vertexLiveConfig{Location: "us-central1", APIVersion: "v1"})
	if err != nil {
		t.Fatalf("url: %v", err)
	}
	want := "wss://us-central1-aiplatform.googleapis.com/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent"
	if u != want {
		t.Fatalf("url = %q, want %q", u, want)
	}
}

func TestVertexLiveSetupMessage(t *testing.T) {
	msg := vertexLiveSetupMessage(vertexLiveConfig{
		Model:                      "gemini-live-2.5-flash-native-audio",
		ProjectID:                  "project-1",
		Location:                   "us-central1",
		InputTranscriptionEnabled:  true,
		OutputTranscriptionEnabled: true,
	})
	setup, _ := msg["setup"].(map[string]any)
	if setup["model"] != "projects/project-1/locations/us-central1/publishers/google/models/gemini-live-2.5-flash-native-audio" {
		t.Fatalf("model resource = %#v", setup["model"])
	}
	if _, ok := setup["inputAudioTranscription"]; !ok {
		t.Fatal("inputAudioTranscription missing")
	}
	if _, ok := setup["outputAudioTranscription"]; !ok {
		t.Fatal("outputAudioTranscription missing")
	}
}

func TestVertexLiveClientEventToPayload(t *testing.T) {
	payload, err := vertexLiveClientEventToPayload(vertexLiveClientEvent{Type: "audio", Data: "AAEC"}, "audio/pcm;rate=16000")
	if err != nil {
		t.Fatalf("audio payload: %v", err)
	}
	data, _ := json.Marshal(payload)
	got := string(data)
	if !strings.Contains(got, `"mimeType":"audio/pcm;rate=16000"`) || !strings.Contains(got, `"data":"AAEC"`) {
		t.Fatalf("payload = %s", got)
	}

	payload, err = vertexLiveClientEventToPayload(vertexLiveClientEvent{Type: "text", Content: "hello"}, "")
	if err != nil {
		t.Fatalf("text payload: %v", err)
	}
	data, _ = json.Marshal(payload)
	if !strings.Contains(string(data), `"text":"hello"`) {
		t.Fatalf("text payload = %s", string(data))
	}
}

func TestVertexLiveAccumulator(t *testing.T) {
	var acc vertexLiveAccumulator
	events, complete, err := acc.consume(map[string]any{
		"serverContent": map[string]any{
			"inputTranscription":  map[string]any{"text": "hi"},
			"outputTranscription": map[string]any{"text": "hello"},
			"modelTurn": map[string]any{"parts": []any{
				map[string]any{"inlineData": map[string]any{"mimeType": "audio/pcm", "data": "AA=="}},
			}},
			"turnComplete": true,
		},
	}, "")
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if !complete {
		t.Fatal("complete = false")
	}
	if len(events) != 3 {
		t.Fatalf("events = %#v", events)
	}
	user, assistant := acc.flush()
	if user != "hi" || assistant != "hello" {
		t.Fatalf("flush = %q/%q", user, assistant)
	}
}

func TestParseLiveBoolAndTimeoutDefaults(t *testing.T) {
	if !parseLiveBool("", true) || parseLiveBool("false", true) {
		t.Fatal("parseLiveBool mismatch")
	}
	req := httptest.NewRequest("GET", "/v1/vertex/live/ws?timeout=2s", nil)
	cfg := vertexLiveConfigFromRequest(req)
	if cfg.SessionTimeout != 2*time.Second {
		t.Fatalf("timeout = %s", cfg.SessionTimeout)
	}
}

func TestVertexLiveValidationErrorsAreActionable(t *testing.T) {
	err := (vertexLiveConfig{Model: "gemini-live-2.5-flash-native-audio"}).validate()
	if err == nil {
		t.Fatal("expected project ID validation error")
	}
	for _, want := range []string{"project_id query parameter", "GOCLAW_VERTEX_PROJECT_ID", "projects/<project>/locations/<location>"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not include %q", err.Error(), want)
		}
	}

	err = (vertexLiveConfig{}).validate()
	if err == nil || !strings.Contains(err.Error(), "GOCLAW_VERTEX_LIVE_MODEL") {
		t.Fatalf("model error = %v", err)
	}
}
