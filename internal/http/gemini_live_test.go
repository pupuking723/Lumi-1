package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	sessionspkg "github.com/nextlevelbuilder/goclaw/internal/sessions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func TestGeminiLiveWebSocketURL(t *testing.T) {
	u, err := geminiLiveWebSocketURL(geminiLiveConfig{Location: "us-central1", APIVersion: "v1beta1"})
	if err != nil {
		t.Fatalf("url: %v", err)
	}
	want := "wss://us-central1-aiplatform.googleapis.com/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent"
	if u != want {
		t.Fatalf("url = %q, want %q", u, want)
	}
}

func TestGeminiLiveSetupMessage(t *testing.T) {
	h := NewGeminiLiveHandler(nil, nil)
	msg := h.setupMessage(context.Background(), geminiLiveConfig{
		Model:                      "gemini-live-2.5-flash-preview-native-audio-09-2025",
		ProjectID:                  "project-1",
		Location:                   "us-central1",
		InputTranscriptionEnabled:  true,
		OutputTranscriptionEnabled: true,
		VADStartSensitivity:        "START_SENSITIVITY_HIGH",
		VADEndSensitivity:          "END_SENSITIVITY_HIGH",
		VADPrefixPadding:           150 * time.Millisecond,
		VADSilenceDuration:         500 * time.Millisecond,
	}, geminiLiveRuntimeContext{AgentKey: "closy", UserID: "u", SessionID: "s"})

	data, _ := json.Marshal(msg)
	got := string(data)
	for _, want := range []string{
		`"model":"projects/project-1/locations/us-central1/publishers/google/models/gemini-live-2.5-flash-preview-native-audio-09-2025"`,
		`"inputAudioTranscription":{}`,
		`"outputAudioTranscription":{}`,
		`"prefixPaddingMs":150`,
		`"silenceDurationMs":500`,
		`"thinkingBudget":0`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("setup missing %s: %s", want, got)
		}
	}
}

func TestGeminiLiveClientEventToPayload(t *testing.T) {
	payload, err := geminiLiveClientEventToPayload(geminiLiveClientEvent{Type: "audio", Data: "AAEC"}, "audio/pcm;rate=16000")
	if err != nil {
		t.Fatalf("audio payload: %v", err)
	}
	data, _ := json.Marshal(payload)
	got := string(data)
	if !strings.Contains(got, `"mimeType":"audio/pcm;rate=16000"`) || !strings.Contains(got, `"data":"AAEC"`) {
		t.Fatalf("payload = %s", got)
	}

	payload, err = geminiLiveClientEventToPayload(geminiLiveClientEvent{Type: "text", Content: "hello"}, "")
	if err != nil {
		t.Fatalf("text payload: %v", err)
	}
	data, _ = json.Marshal(payload)
	if !strings.Contains(string(data), `"text":"hello"`) {
		t.Fatalf("text payload = %s", string(data))
	}
}

func TestGeminiLiveMediaEventToPayloadAndSessionHistory(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), "live-*.png")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	if _, err := tmp.Write([]byte("png-bytes")); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatalf("close temp: %v", err)
	}

	id := uuid.New()
	assets := &fakeMediaAssetStore{byID: map[uuid.UUID]*store.MediaAssetData{
		id: {
			ID:               id,
			TenantID:         store.MasterTenantID,
			UserID:           "user-a",
			OriginalFilename: "ootd.png",
			MimeType:         "image/png",
			Size:             int64(len("png-bytes")),
			StorageBackend:   store.MediaStorageLocal,
			StorageKey:       tmp.Name(),
			Status:           store.MediaStatusReady,
			Visibility:       "private",
		},
	}}
	mgr := sessionspkg.NewManager("")
	h := NewGeminiLiveHandler(nil, mgr)
	h.SetMediaAssetStore(assets)
	rt := geminiLiveRuntimeContext{AgentKey: "closy", UserID: "user-a", SessionID: "agent:closy:cchat:direct:user-a-fit"}

	payload, ack, err := h.geminiLiveClientEventToPayload(context.Background(), rt, geminiLiveClientEvent{
		Type:         "media",
		MediaID:      id.String(),
		Caption:      "看这张新图",
		Source:       "camera",
		Role:         "current_outfit",
		TurnComplete: true,
	}, "audio/pcm;rate=16000")
	if err != nil {
		t.Fatalf("media payload: %v", err)
	}
	if ack == nil || ack.Type != "media_received" {
		t.Fatalf("ack = %#v", ack)
	}
	data, _ := json.Marshal(payload)
	got := string(data)
	for _, want := range []string{
		`"clientContent"`,
		`"turnComplete":true`,
		`"mimeType":"image/png"`,
		`"data":"` + base64.StdEncoding.EncodeToString([]byte("png-bytes")) + `"`,
		`"看这张新图`,
		`source: camera`,
		`role: current_outfit`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("payload missing %s: %s", want, got)
		}
	}

	history := mgr.GetHistory(context.Background(), rt.SessionID)
	if len(history) != 1 {
		t.Fatalf("history len = %d", len(history))
	}
	if history[0].Role != "user" || !strings.Contains(history[0].Content, "看这张新图") || len(history[0].MediaRefs) != 1 {
		t.Fatalf("history message = %#v", history[0])
	}
	if history[0].MediaRefs[0].ID != id.String() || history[0].MediaRefs[0].Kind != "image" {
		t.Fatalf("history media ref = %#v", history[0].MediaRefs[0])
	}
}

func TestGeminiLiveAccumulator(t *testing.T) {
	var acc geminiLiveAccumulator
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

func TestGeminiLiveConfigDefaultsAndRuntimeContext(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/closy/live/gemini/ws?timeout=2s&session_id=s1", nil)
	cfg := geminiLiveConfigFromRequest(req)
	if cfg.Model != defaultGeminiLiveModel {
		t.Fatalf("model = %q", cfg.Model)
	}
	if cfg.APIVersion != defaultGeminiLiveAPIVersion {
		t.Fatalf("api version = %q", cfg.APIVersion)
	}
	if cfg.SessionTimeout != 2*time.Second {
		t.Fatalf("timeout = %s", cfg.SessionTimeout)
	}
	rt := NewGeminiLiveHandler(nil, nil).runtimeContext(req, cfg)
	if rt.AgentKey != "closy" || rt.SessionID != "agent:closy:cchat:direct:anonymous-s1" {
		t.Fatalf("runtime context = %#v", rt)
	}
}

func TestGeminiLiveConfigUsesGoClawGeminiEnv(t *testing.T) {
	t.Setenv("GOCLAW_GEMINI_LIVE_MODEL", "alias-live-model")
	t.Setenv("GOCLAW_GEMINI_LIVE_PROJECT_ID", "alias-project")
	t.Setenv("GOCLAW_GEMINI_LIVE_LOCATION", "europe-west4")
	t.Setenv("GOCLAW_GEMINI_LIVE_API_VERSION", "v1beta1")
	t.Setenv("GOCLAW_GEMINI_LIVE_INPUT_MIME", "audio/pcm;rate=24000")
	t.Setenv("GOCLAW_GEMINI_LIVE_OUTPUT_MIME", "audio/pcm")
	t.Setenv("GOCLAW_GEMINI_LIVE_INPUT_TRANSCRIPTION", "false")
	t.Setenv("GOCLAW_GEMINI_LIVE_OUTPUT_TRANSCRIPTION", "true")
	t.Setenv("GOCLAW_GEMINI_LIVE_VAD_START_SENSITIVITY", "START_SENSITIVITY_LOW")
	t.Setenv("GOCLAW_GEMINI_LIVE_VAD_END_SENSITIVITY", "END_SENSITIVITY_LOW")
	t.Setenv("GOCLAW_GEMINI_LIVE_VAD_PREFIX_PADDING", "250ms")
	t.Setenv("GOCLAW_GEMINI_LIVE_VAD_SILENCE_DURATION", "750ms")
	t.Setenv("GOCLAW_GEMINI_LIVE_TIMEOUT", "3m")

	cfg := geminiLiveConfigFromRequest(httptest.NewRequest("GET", "/v1/gemini/live/ws", nil))
	if cfg.Model != "alias-live-model" || cfg.ProjectID != "alias-project" || cfg.Location != "europe-west4" {
		t.Fatalf("alias config mismatch: %#v", cfg)
	}
	if cfg.InputAudioMIMEType != "audio/pcm;rate=24000" || cfg.OutputAudioMIMEType != "audio/pcm" {
		t.Fatalf("alias mime mismatch: %#v", cfg)
	}
	if cfg.InputTranscriptionEnabled || !cfg.OutputTranscriptionEnabled {
		t.Fatalf("alias transcription mismatch: %#v", cfg)
	}
	if cfg.VADStartSensitivity != "START_SENSITIVITY_LOW" || cfg.VADEndSensitivity != "END_SENSITIVITY_LOW" {
		t.Fatalf("alias vad sensitivity mismatch: %#v", cfg)
	}
	if cfg.VADPrefixPadding != 250*time.Millisecond || cfg.VADSilenceDuration != 750*time.Millisecond || cfg.SessionTimeout != 3*time.Minute {
		t.Fatalf("alias duration mismatch: %#v", cfg)
	}
}

func TestGeminiLiveRecordTurnStoresSessionMessages(t *testing.T) {
	mgr := sessionspkg.NewManager("")
	h := NewGeminiLiveHandler(nil, mgr)
	rt := geminiLiveRuntimeContext{AgentKey: "closy", UserID: "u", SessionID: "agent:closy:live:direct:u"}

	h.recordTurn(context.Background(), rt, "hello", "hi there")

	history := mgr.GetHistory(context.Background(), rt.SessionID)
	if len(history) != 2 {
		t.Fatalf("history len = %d", len(history))
	}
	if history[0].Role != "user" || history[0].Content != "hello" {
		t.Fatalf("user message = %#v", history[0])
	}
	if history[1].Role != "assistant" || history[1].Content != "hi there" {
		t.Fatalf("assistant message = %#v", history[1])
	}
}
