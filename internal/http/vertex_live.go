package http

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/providers/googleauth"
)

const (
	defaultVertexLiveModel          = "gemini-live-2.5-flash-native-audio"
	defaultVertexLiveLocation       = "us-central1"
	defaultVertexLiveAPIVersion     = "v1"
	defaultVertexLiveInputAudioMIME = "audio/pcm;rate=16000"
	defaultVertexLiveTimeout        = 10 * time.Minute
)

const vertexLiveProjectIDHelp = "set project_id query parameter, GOCLAW_VERTEX_PROJECT_ID, VERTEX_PROJECT_ID, GOOGLE_CLOUD_PROJECT, or pass a full model resource like projects/<project>/locations/<location>/publishers/google/models/<model>"
const vertexLiveAuthHelp = "set GOCLAW_VERTEX_ACCESS_TOKEN, VERTEX_ACCESS_TOKEN, GOOGLE_OAUTH_ACCESS_TOKEN, GOOGLE_ACCESS_TOKEN, provide service account credentials via GOOGLE_APPLICATION_CREDENTIALS / GOOGLE_APPLICATION_CREDENTIALS_JSON / GOCLAW_VERTEX_SERVICE_ACCOUNT_FILE / GOCLAW_VERTEX_SERVICE_ACCOUNT_JSON, or run gcloud auth print-access-token locally"

type VertexLiveHandler struct {
	upgrader websocket.Upgrader
}

func NewVertexLiveHandler() *VertexLiveHandler {
	return &VertexLiveHandler{
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     func(*http.Request) bool { return true },
		},
	}
}

func (h *VertexLiveHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/vertex/live/ws", h.handleLiveWebSocket)
	mux.HandleFunc("GET /v1/closy/live/ws", h.handleLiveWebSocket)
}

func (h *VertexLiveHandler) handleLiveWebSocket(w http.ResponseWriter, r *http.Request) {
	bearer := extractBearerToken(r)
	if bearer == "" {
		bearer = r.URL.Query().Get("token")
	}
	req, ok := requireAuthBearer(permissions.RoleOperator, bearer, w, r)
	if !ok {
		return
	}

	cfg := vertexLiveConfigFromRequest(req)
	if err := cfg.validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	clientConn, err := h.upgrader.Upgrade(w, req, nil)
	if err != nil {
		slog.Warn("vertex_live: client websocket upgrade failed", "error", err)
		return
	}
	defer clientConn.Close()

	ctx, cancel := context.WithTimeout(req.Context(), cfg.SessionTimeout)
	defer cancel()

	upstream, err := connectVertexLive(ctx, cfg)
	if err != nil {
		_ = clientConn.WriteJSON(vertexLiveEvent{Type: "error", Error: err.Error()})
		return
	}
	defer upstream.Close()

	_ = clientConn.WriteJSON(vertexLiveEvent{
		Type: "live_ready",
		Data: liveJSON(map[string]any{
			"model":                 cfg.Model,
			"input_audio_mime_type": cfg.InputAudioMIMEType,
		}),
	})

	errCh := make(chan error, 2)
	var clientWriteMu sync.Mutex
	go func() {
		errCh <- vertexLiveClientToUpstream(ctx, clientConn, upstream, cfg, &clientWriteMu)
	}()
	go func() {
		errCh <- vertexLiveUpstreamToClient(ctx, upstream, clientConn, cfg, &clientWriteMu)
	}()
	if err := <-errCh; err != nil && !isExpectedVertexLiveClose(err) {
		writeVertexLiveClient(clientConn, &clientWriteMu, vertexLiveEvent{Type: "error", Error: err.Error()})
	}
	cancel()
	writeVertexLiveClient(clientConn, &clientWriteMu, vertexLiveEvent{Type: "done"})
}

type vertexLiveConfig struct {
	Model                      string
	ProjectID                  string
	Location                   string
	BaseURL                    string
	APIVersion                 string
	InputAudioMIMEType         string
	OutputAudioMIMEType        string
	InputTranscriptionEnabled  bool
	OutputTranscriptionEnabled bool
	SessionTimeout             time.Duration
}

func vertexLiveConfigFromRequest(r *http.Request) vertexLiveConfig {
	q := r.URL.Query()
	timeout := defaultVertexLiveTimeout
	if raw := firstNonEmptyLive(q.Get("timeout"), os.Getenv("GOCLAW_VERTEX_LIVE_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			timeout = parsed
		}
	}
	return vertexLiveConfig{
		Model:                      firstNonEmptyLive(q.Get("model"), os.Getenv("GOCLAW_VERTEX_LIVE_MODEL"), os.Getenv("VERTEX_LIVE_MODEL"), defaultVertexLiveModel),
		ProjectID:                  firstNonEmptyLive(q.Get("project_id"), os.Getenv("GOCLAW_VERTEX_PROJECT_ID"), os.Getenv("VERTEX_PROJECT_ID"), os.Getenv("GOOGLE_CLOUD_PROJECT"), os.Getenv("GCLOUD_PROJECT")),
		Location:                   firstNonEmptyLive(q.Get("location"), os.Getenv("GOCLAW_VERTEX_LOCATION"), os.Getenv("VERTEX_LOCATION"), defaultVertexLiveLocation),
		BaseURL:                    firstNonEmptyLive(q.Get("base_url"), os.Getenv("GOCLAW_VERTEX_LIVE_BASE_URL")),
		APIVersion:                 firstNonEmptyLive(q.Get("api_version"), os.Getenv("GOCLAW_VERTEX_LIVE_API_VERSION"), defaultVertexLiveAPIVersion),
		InputAudioMIMEType:         firstNonEmptyLive(q.Get("input_mime"), os.Getenv("GOCLAW_VERTEX_LIVE_INPUT_MIME"), defaultVertexLiveInputAudioMIME),
		OutputAudioMIMEType:        firstNonEmptyLive(q.Get("output_mime"), os.Getenv("GOCLAW_VERTEX_LIVE_OUTPUT_MIME")),
		InputTranscriptionEnabled:  parseLiveBool(firstNonEmptyLive(q.Get("input_transcription"), os.Getenv("GOCLAW_VERTEX_LIVE_INPUT_TRANSCRIPTION")), true),
		OutputTranscriptionEnabled: parseLiveBool(firstNonEmptyLive(q.Get("output_transcription"), os.Getenv("GOCLAW_VERTEX_LIVE_OUTPUT_TRANSCRIPTION")), true),
		SessionTimeout:             timeout,
	}
}

func (c vertexLiveConfig) validate() error {
	if strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("vertex live model is required; pass ?model=gemini-live-2.5-flash-native-audio or set GOCLAW_VERTEX_LIVE_MODEL")
	}
	if !strings.Contains(c.Model, "/") && strings.TrimSpace(c.ProjectID) == "" {
		return fmt.Errorf("vertex live project ID is required for model %q; %s", c.Model, vertexLiveProjectIDHelp)
	}
	return nil
}

func connectVertexLive(ctx context.Context, cfg vertexLiveConfig) (*websocket.Conn, error) {
	token := firstNonEmptyLive(os.Getenv("GOCLAW_VERTEX_ACCESS_TOKEN"), os.Getenv("VERTEX_ACCESS_TOKEN"), os.Getenv("GOOGLE_OAUTH_ACCESS_TOKEN"), os.Getenv("GOOGLE_ACCESS_TOKEN"))
	if token == "" {
		var err error
		token, err = googleauth.AccessTokenFromEnvOrGcloud(ctx, &http.Client{Timeout: 30 * time.Second})
		if err != nil {
			return nil, fmt.Errorf("vertex live access token is required; %s: %w", vertexLiveAuthHelp, err)
		}
	}
	wsURL, err := vertexLiveWebSocketURL(cfg)
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return nil, fmt.Errorf("connect vertex live websocket %s: %w", wsURL, err)
	}
	if err := conn.WriteJSON(vertexLiveSetupMessage(cfg)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("write vertex live setup: %w", err)
	}
	return conn, nil
}

func vertexLiveSetupMessage(cfg vertexLiveConfig) map[string]any {
	setup := map[string]any{
		"model": vertexLiveModelResource(cfg),
		"generationConfig": map[string]any{
			"responseModalities": []string{"AUDIO"},
		},
		"realtimeInputConfig": map[string]any{
			"automaticActivityDetection": map[string]any{
				"startOfSpeechSensitivity": "START_SENSITIVITY_HIGH",
				"endOfSpeechSensitivity":   "END_SENSITIVITY_HIGH",
				"prefixPaddingMs":          40,
				"silenceDurationMs":        180,
			},
			"turnCoverage": "TURN_INCLUDES_ONLY_ACTIVITY",
		},
	}
	if cfg.InputTranscriptionEnabled {
		setup["inputAudioTranscription"] = map[string]any{}
	}
	if cfg.OutputTranscriptionEnabled {
		setup["outputAudioTranscription"] = map[string]any{}
	}
	return map[string]any{"setup": setup}
}

func vertexLiveClientToUpstream(ctx context.Context, clientConn, upstream *websocket.Conn, cfg vertexLiveConfig, clientWriteMu *sync.Mutex) error {
	for {
		var event vertexLiveClientEvent
		if err := clientConn.ReadJSON(&event); err != nil {
			return err
		}
		payload, err := vertexLiveClientEventToPayload(event, cfg.InputAudioMIMEType)
		if err != nil {
			writeVertexLiveClient(clientConn, clientWriteMu, vertexLiveEvent{Type: "error", Error: err.Error()})
			continue
		}
		if payload == nil {
			return nil
		}
		if err := upstream.WriteJSON(payload); err != nil {
			return fmt.Errorf("send vertex live input: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func vertexLiveUpstreamToClient(ctx context.Context, upstream, clientConn *websocket.Conn, cfg vertexLiveConfig, clientWriteMu *sync.Mutex) error {
	var turn vertexLiveAccumulator
	for {
		var message map[string]any
		if err := upstream.ReadJSON(&message); err != nil {
			return err
		}
		events, complete, err := turn.consume(message, cfg.OutputAudioMIMEType)
		if err != nil {
			return err
		}
		for _, event := range events {
			if err := writeVertexLiveClient(clientConn, clientWriteMu, event); err != nil {
				return err
			}
		}
		if complete {
			userText, assistantText := turn.flush()
			if userText != "" {
				_ = writeVertexLiveClient(clientConn, clientWriteMu, vertexLiveEvent{Type: "message", Role: "user", Content: userText})
			}
			if assistantText != "" {
				_ = writeVertexLiveClient(clientConn, clientWriteMu, vertexLiveEvent{Type: "message", Role: "assistant", Content: assistantText})
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func writeVertexLiveClient(conn *websocket.Conn, mu *sync.Mutex, event vertexLiveEvent) error {
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	return conn.WriteJSON(event)
}

type vertexLiveClientEvent struct {
	Type     string `json:"type"`
	MIMEType string `json:"mime_type,omitempty"`
	Data     string `json:"data,omitempty"`
	Content  string `json:"content,omitempty"`
}

type vertexLiveEvent struct {
	Type    string          `json:"type"`
	Role    string          `json:"role,omitempty"`
	Content string          `json:"content,omitempty"`
	Error   string          `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func vertexLiveClientEventToPayload(event vertexLiveClientEvent, defaultMIME string) (map[string]any, error) {
	switch strings.ToLower(strings.TrimSpace(event.Type)) {
	case "audio":
		data := strings.TrimSpace(event.Data)
		if data == "" {
			return nil, fmt.Errorf("live audio event requires base64 data")
		}
		mimeType := strings.TrimSpace(event.MIMEType)
		if mimeType == "" {
			mimeType = defaultMIME
		}
		return map[string]any{
			"realtimeInput": map[string]any{
				"audio": map[string]any{"mimeType": mimeType, "data": data},
			},
		}, nil
	case "audio_end":
		return map[string]any{"realtimeInput": map[string]any{"audioStreamEnd": true}}, nil
	case "activity_start":
		return map[string]any{"realtimeInput": map[string]any{"activityStart": map[string]any{}}}, nil
	case "activity_end":
		return map[string]any{"realtimeInput": map[string]any{"activityEnd": map[string]any{}}}, nil
	case "text":
		text := strings.TrimSpace(event.Content)
		if text == "" {
			return nil, fmt.Errorf("live text event requires content")
		}
		return map[string]any{"realtimeInput": map[string]any{"text": text}}, nil
	case "close":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown live client event type %q", event.Type)
	}
}

type vertexLiveAccumulator struct {
	input  strings.Builder
	output strings.Builder
}

func (a *vertexLiveAccumulator) consume(message map[string]any, outputMIME string) ([]vertexLiveEvent, bool, error) {
	if errValue := message["error"]; errValue != nil {
		data, _ := json.Marshal(errValue)
		return nil, false, fmt.Errorf("vertex live server error: %s", data)
	}
	if _, ok := message["setupComplete"]; ok {
		return []vertexLiveEvent{{Type: "live_setup_complete"}}, false, nil
	}
	content, _ := message["serverContent"].(map[string]any)
	if len(content) == 0 {
		return nil, false, nil
	}
	var events []vertexLiveEvent
	if interrupted, _ := content["interrupted"].(bool); interrupted {
		a.output.Reset()
		events = append(events, vertexLiveEvent{Type: "live_interrupted"})
	}
	if input := liveTranscriptionText(content, "inputTranscription"); input != "" {
		a.input.WriteString(input)
		events = append(events, vertexLiveEvent{Type: "live_transcript", Role: "user", Content: input, Data: liveJSON(map[string]any{"source": "input"})})
	}
	if output := liveTranscriptionText(content, "outputTranscription"); output != "" {
		a.output.WriteString(output)
		events = append(events, vertexLiveEvent{Type: "live_transcript", Role: "assistant", Content: output, Data: liveJSON(map[string]any{"source": "output"})})
	}
	for _, audio := range liveOutputAudioParts(content, outputMIME) {
		events = append(events, vertexLiveEvent{Type: "live_audio", Role: "assistant", Data: liveJSON(audio)})
	}
	complete, _ := content["turnComplete"].(bool)
	return events, complete, nil
}

func (a *vertexLiveAccumulator) flush() (string, string) {
	userText := strings.TrimSpace(a.input.String())
	assistantText := strings.TrimSpace(a.output.String())
	a.input.Reset()
	a.output.Reset()
	return userText, assistantText
}

func liveTranscriptionText(content map[string]any, key string) string {
	transcription, _ := content[key].(map[string]any)
	text, _ := transcription["text"].(string)
	return text
}

func liveOutputAudioParts(content map[string]any, fallbackMIME string) []map[string]any {
	modelTurn, _ := content["modelTurn"].(map[string]any)
	parts, _ := modelTurn["parts"].([]any)
	out := make([]map[string]any, 0, len(parts))
	for _, item := range parts {
		part, _ := item.(map[string]any)
		inlineData, _ := firstLiveMap(part["inlineData"], part["inline_data"])
		if len(inlineData) == 0 {
			continue
		}
		mimeType := firstLiveString(inlineData["mimeType"], inlineData["mime_type"])
		data, _ := inlineData["data"].(string)
		if data == "" || (!strings.HasPrefix(mimeType, "audio/") && fallbackMIME == "") {
			continue
		}
		if mimeType == "" {
			mimeType = fallbackMIME
		}
		out = append(out, map[string]any{"mime_type": mimeType, "data": data})
	}
	return out
}

func vertexLiveWebSocketURL(cfg vertexLiveConfig) (string, error) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = vertexLiveBaseURL(cfg.Location)
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	scheme := parsed.Scheme
	if scheme != "ws" && scheme != "wss" {
		scheme = "wss"
	}
	path := strings.TrimRight(parsed.Path, "/")
	if !strings.Contains(path, "/ws/") {
		apiVersion := strings.TrimSpace(cfg.APIVersion)
		if apiVersion == "" {
			apiVersion = defaultVertexLiveAPIVersion
		}
		path += "/ws/google.cloud.aiplatform." + apiVersion + ".LlmBidiService/BidiGenerateContent"
	}
	return (&url.URL{Scheme: scheme, Host: parsed.Host, Path: path}).String(), nil
}

func vertexLiveBaseURL(location string) string {
	location = strings.ToLower(strings.TrimSpace(location))
	switch location {
	case "", "us-central1":
		return "https://us-central1-aiplatform.googleapis.com"
	case "global":
		return "https://aiplatform.googleapis.com"
	case "us":
		return "https://aiplatform.us.rep.googleapis.com"
	case "eu":
		return "https://aiplatform.eu.rep.googleapis.com"
	default:
		return fmt.Sprintf("https://%s-aiplatform.googleapis.com", location)
	}
}

func vertexLiveModelResource(cfg vertexLiveConfig) string {
	model := strings.TrimSpace(cfg.Model)
	if strings.Contains(model, "/") {
		return strings.TrimLeft(model, "/")
	}
	return fmt.Sprintf("projects/%s/locations/%s/publishers/google/models/%s", cfg.ProjectID, cfg.Location, model)
}

func firstLiveMap(values ...any) (map[string]any, bool) {
	for _, value := range values {
		if mapped, ok := value.(map[string]any); ok && len(mapped) > 0 {
			return mapped, true
		}
	}
	return nil, false
}

func firstLiveString(values ...any) string {
	for _, value := range values {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func firstNonEmptyLive(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseLiveBool(raw string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func isExpectedVertexLiveClose(err error) bool {
	if err == nil {
		return true
	}
	return websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) ||
		strings.Contains(err.Error(), "use of closed network connection") ||
		strings.Contains(err.Error(), context.Canceled.Error())
}

func liveJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}
