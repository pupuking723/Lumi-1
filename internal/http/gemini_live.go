package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/nextlevelbuilder/goclaw/internal/closy"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/providers/googleauth"
	"github.com/nextlevelbuilder/goclaw/internal/sessions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

const (
	defaultGeminiLiveModel          = "gemini-live-2.5-flash-preview-native-audio-09-2025"
	defaultGeminiLiveLocation       = "us-central1"
	defaultGeminiLiveAPIVersion     = "v1beta1"
	defaultGeminiLiveInputAudioMIME = "audio/pcm;rate=16000"
	defaultGeminiLiveTimeout        = 10 * time.Minute
	defaultGeminiLiveVADPrefix      = 150 * time.Millisecond
	defaultGeminiLiveVADSilence     = 500 * time.Millisecond
)

const geminiLiveAuthHelp = "set GOCLAW_VERTEX_ACCESS_TOKEN, VERTEX_ACCESS_TOKEN, GOOGLE_OAUTH_ACCESS_TOKEN, GOOGLE_ACCESS_TOKEN, provide service account credentials via GOOGLE_APPLICATION_CREDENTIALS / GOOGLE_APPLICATION_CREDENTIALS_JSON / GOCLAW_VERTEX_SERVICE_ACCOUNT_FILE / GOCLAW_VERTEX_SERVICE_ACCOUNT_JSON, or run gcloud auth print-access-token locally"

// GeminiLiveHandler is an independent Gemini Live WebSocket bridge.
// It intentionally does not share routes or implementation state with
// VertexLiveHandler, so the existing live API remains unchanged.
type GeminiLiveHandler struct {
	upgrader websocket.Upgrader
	agents   store.AgentStore
	sessions geminiLiveSessionStore
	assets   store.MediaAssetStore
	memory   store.ClosyMemoryStore
	dialer   *websocket.Dialer
}

type geminiLiveSessionStore interface {
	AddMessage(ctx context.Context, key string, msg providers.Message)
	GetHistory(ctx context.Context, key string) []providers.Message
}

func NewGeminiLiveHandler(agents store.AgentStore, sessions geminiLiveSessionStore) *GeminiLiveHandler {
	return &GeminiLiveHandler{
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     func(*http.Request) bool { return true },
		},
		agents:   agents,
		sessions: sessions,
		dialer:   websocket.DefaultDialer,
	}
}

// SetMediaAssetStore enables Live media events that reference uploaded media_id
// records from POST /v1/chat/attachments/upload.
func (h *GeminiLiveHandler) SetMediaAssetStore(assets store.MediaAssetStore) {
	h.assets = assets
}

// SetClosyMemoryStore enables Mochi domain memory prompt injection and post-turn extraction.
func (h *GeminiLiveHandler) SetClosyMemoryStore(mem store.ClosyMemoryStore) {
	h.memory = mem
}

func (h *GeminiLiveHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/gemini/live/ws", h.handleWebSocket)
	mux.HandleFunc("GET /v1/closy/live/gemini/ws", h.handleWebSocket)
}

func (h *GeminiLiveHandler) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	r, bearer := liveRequestWithBrowserAuth(r)
	req, ok := requireAuthBearer(permissions.RoleOperator, bearer, w, r)
	if !ok {
		return
	}

	cfg := geminiLiveConfigFromRequest(req)
	if err := cfg.validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	rt := h.runtimeContext(req, cfg)

	clientConn, err := h.upgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	defer clientConn.Close()

	ctx, cancel := context.WithTimeout(req.Context(), cfg.SessionTimeout)
	defer cancel()

	upstream, err := h.connect(ctx, cfg, rt)
	if err != nil {
		_ = clientConn.WriteJSON(geminiLiveEvent{Type: "error", SessionID: rt.SessionID, Error: err.Error()})
		return
	}
	defer upstream.Close()

	_ = clientConn.WriteJSON(geminiLiveEvent{
		Type:      "live_ready",
		SessionID: rt.SessionID,
		Data: geminiLiveJSON(map[string]any{
			"agent":                 rt.AgentKey,
			"model":                 cfg.Model,
			"input_audio_mime_type": cfg.InputAudioMIMEType,
			"session_id":            rt.SessionID,
		}),
	})

	errCh := make(chan error, 2)
	var clientWriteMu sync.Mutex
	go func() {
		errCh <- h.geminiLiveClientToUpstream(ctx, rt, clientConn, upstream, cfg, &clientWriteMu)
	}()
	go func() {
		errCh <- h.geminiLiveUpstreamToClient(ctx, upstream, clientConn, cfg, rt, &clientWriteMu)
	}()

	err = <-errCh
	cancel()
	if err != nil && !geminiLiveExpectedClose(err) {
		_ = geminiLiveWriteClient(clientConn, &clientWriteMu, geminiLiveEvent{Type: "error", SessionID: rt.SessionID, Error: err.Error()})
	}
	_ = geminiLiveWriteClient(clientConn, &clientWriteMu, geminiLiveEvent{Type: "done", SessionID: rt.SessionID})
}

type geminiLiveRuntimeContext struct {
	AgentKey  string
	UserID    string
	SessionID string
}

func (h *GeminiLiveHandler) runtimeContext(r *http.Request, cfg geminiLiveConfig) geminiLiveRuntimeContext {
	userID := store.UserIDFromContext(r.Context())
	if userID == "" {
		userID = strings.TrimSpace(r.URL.Query().Get("user_id"))
	}
	if userID == "" {
		userID = "anonymous"
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID != "" {
		sessionID = chatCompletionSessionKey(cfg.AgentKey, userID, sessionID, "live0000")
	} else {
		sessionID = userID
		sessionID = sessions.BuildSessionKey(cfg.AgentKey, "live", sessions.PeerDirect, sessionID)
	}
	return geminiLiveRuntimeContext{AgentKey: cfg.AgentKey, UserID: userID, SessionID: sessionID}
}

type geminiLiveConfig struct {
	AgentKey                   string
	Model                      string
	ProjectID                  string
	Location                   string
	BaseURL                    string
	APIVersion                 string
	InputAudioMIMEType         string
	OutputAudioMIMEType        string
	InputTranscriptionEnabled  bool
	OutputTranscriptionEnabled bool
	VADStartSensitivity        string
	VADEndSensitivity          string
	VADPrefixPadding           time.Duration
	VADSilenceDuration         time.Duration
	SessionTimeout             time.Duration
}

func geminiLiveConfigFromRequest(r *http.Request) geminiLiveConfig {
	q := r.URL.Query()
	return geminiLiveConfig{
		AgentKey:                   geminiLiveFirst(q.Get("agent"), q.Get("agent_key"), os.Getenv("GOCLAW_GEMINI_LIVE_AGENT"), "closy"),
		Model:                      geminiLiveFirst(q.Get("model"), os.Getenv("GOCLAW_GEMINI_LIVE_MODEL"), defaultGeminiLiveModel),
		ProjectID:                  geminiLiveFirst(q.Get("project_id"), os.Getenv("GOCLAW_GEMINI_LIVE_PROJECT_ID"), os.Getenv("GOCLAW_VERTEX_PROJECT_ID"), os.Getenv("VERTEX_PROJECT_ID"), os.Getenv("GOOGLE_CLOUD_PROJECT"), os.Getenv("GCLOUD_PROJECT")),
		Location:                   geminiLiveFirst(q.Get("location"), os.Getenv("GOCLAW_GEMINI_LIVE_LOCATION"), defaultGeminiLiveLocation),
		BaseURL:                    geminiLiveFirst(q.Get("base_url"), os.Getenv("GOCLAW_GEMINI_LIVE_BASE_URL")),
		APIVersion:                 geminiLiveFirst(q.Get("api_version"), os.Getenv("GOCLAW_GEMINI_LIVE_API_VERSION"), defaultGeminiLiveAPIVersion),
		InputAudioMIMEType:         geminiLiveFirst(q.Get("input_mime"), os.Getenv("GOCLAW_GEMINI_LIVE_INPUT_MIME"), defaultGeminiLiveInputAudioMIME),
		OutputAudioMIMEType:        geminiLiveFirst(q.Get("output_mime"), os.Getenv("GOCLAW_GEMINI_LIVE_OUTPUT_MIME")),
		InputTranscriptionEnabled:  geminiLiveBool(geminiLiveFirst(q.Get("input_transcription"), os.Getenv("GOCLAW_GEMINI_LIVE_INPUT_TRANSCRIPTION")), true),
		OutputTranscriptionEnabled: geminiLiveBool(geminiLiveFirst(q.Get("output_transcription"), os.Getenv("GOCLAW_GEMINI_LIVE_OUTPUT_TRANSCRIPTION")), true),
		VADStartSensitivity:        geminiLiveEnum(geminiLiveFirst(q.Get("vad_start_sensitivity"), os.Getenv("GOCLAW_GEMINI_LIVE_VAD_START_SENSITIVITY")), "START_SENSITIVITY_HIGH"),
		VADEndSensitivity:          geminiLiveEnum(geminiLiveFirst(q.Get("vad_end_sensitivity"), os.Getenv("GOCLAW_GEMINI_LIVE_VAD_END_SENSITIVITY")), "END_SENSITIVITY_HIGH"),
		VADPrefixPadding:           geminiLiveDuration(geminiLiveFirst(q.Get("vad_prefix_padding"), os.Getenv("GOCLAW_GEMINI_LIVE_VAD_PREFIX_PADDING")), defaultGeminiLiveVADPrefix),
		VADSilenceDuration:         geminiLiveDuration(geminiLiveFirst(q.Get("vad_silence_duration"), os.Getenv("GOCLAW_GEMINI_LIVE_VAD_SILENCE_DURATION")), defaultGeminiLiveVADSilence),
		SessionTimeout:             geminiLiveDuration(geminiLiveFirst(q.Get("timeout"), os.Getenv("GOCLAW_GEMINI_LIVE_TIMEOUT")), defaultGeminiLiveTimeout),
	}
}

func (c geminiLiveConfig) validate() error {
	if strings.TrimSpace(c.AgentKey) == "" {
		return fmt.Errorf("gemini live agent key is required")
	}
	if strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("gemini live model is required; pass ?model=%s or set GOCLAW_GEMINI_LIVE_MODEL", defaultGeminiLiveModel)
	}
	if !strings.Contains(c.Model, "/") && strings.TrimSpace(c.ProjectID) == "" {
		return fmt.Errorf("gemini live project ID is required for model %q; set ?project_id=, GOCLAW_GEMINI_LIVE_PROJECT_ID, GOCLAW_VERTEX_PROJECT_ID, VERTEX_PROJECT_ID, GOOGLE_CLOUD_PROJECT, or pass a full model resource", c.Model)
	}
	return nil
}

func (h *GeminiLiveHandler) connect(ctx context.Context, cfg geminiLiveConfig, rt geminiLiveRuntimeContext) (*websocket.Conn, error) {
	token := geminiLiveFirst(os.Getenv("GOCLAW_VERTEX_ACCESS_TOKEN"), os.Getenv("VERTEX_ACCESS_TOKEN"), os.Getenv("GOOGLE_OAUTH_ACCESS_TOKEN"), os.Getenv("GOOGLE_ACCESS_TOKEN"))
	if token == "" {
		var err error
		token, err = googleauth.AccessTokenFromEnvOrGcloud(ctx, &http.Client{Timeout: 30 * time.Second})
		if err != nil {
			return nil, fmt.Errorf("gemini live access token is required; %s: %w", geminiLiveAuthHelp, err)
		}
	}
	wsURL, err := geminiLiveWebSocketURL(cfg)
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	dialer := h.dialer
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	conn, _, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return nil, fmt.Errorf("connect gemini live websocket %s: %w", wsURL, err)
	}
	if err := conn.WriteJSON(h.setupMessage(ctx, cfg, rt)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("write gemini live setup: %w", err)
	}
	return conn, nil
}

func (h *GeminiLiveHandler) setupMessage(ctx context.Context, cfg geminiLiveConfig, rt geminiLiveRuntimeContext) map[string]any {
	generationConfig := map[string]any{"responseModalities": []string{"AUDIO"}}
	if thinking := geminiLiveThinkingConfig(cfg.Model); len(thinking) > 0 {
		generationConfig["thinkingConfig"] = thinking
	}
	setup := map[string]any{
		"model":            geminiLiveModelResource(cfg),
		"generationConfig": generationConfig,
		"realtimeInputConfig": map[string]any{
			"automaticActivityDetection": map[string]any{
				"startOfSpeechSensitivity": cfg.VADStartSensitivity,
				"endOfSpeechSensitivity":   cfg.VADEndSensitivity,
				"prefixPaddingMs":          int(cfg.VADPrefixPadding / time.Millisecond),
				"silenceDurationMs":        int(cfg.VADSilenceDuration / time.Millisecond),
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
	if instruction := strings.TrimSpace(h.systemInstruction(ctx, rt)); instruction != "" {
		setup["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": instruction}},
		}
	}
	return map[string]any{"setup": setup}
}

func (h *GeminiLiveHandler) systemInstruction(ctx context.Context, rt geminiLiveRuntimeContext) string {
	if h == nil || h.agents == nil {
		return ""
	}
	agentData, err := h.agents.GetByKey(ctx, rt.AgentKey)
	if err != nil || agentData == nil {
		return ""
	}
	var parts []string
	parts = append(parts, "You are in realtime voice conversation mode. Keep responses natural, concise, and suitable for spoken audio.")
	if agentData.DisplayName != "" || agentData.AgentDescription != "" || agentData.Frontmatter != "" {
		parts = append(parts, strings.TrimSpace(strings.Join([]string{
			"Agent: " + agentData.DisplayName,
			agentData.AgentDescription,
			agentData.Frontmatter,
		}, "\n")))
	}
	if files, err := h.agents.GetAgentContextFiles(ctx, agentData.ID); err == nil {
		for _, f := range files {
			content := strings.TrimSpace(f.Content)
			if content == "" {
				continue
			}
			parts = append(parts, fmt.Sprintf("<%s>\n%s\n</%s>", f.FileName, content, f.FileName))
		}
	}
	if h.sessions != nil {
		history := h.sessions.GetHistory(ctx, rt.SessionID)
		if len(history) > 20 {
			history = history[len(history)-20:]
		}
		var transcript strings.Builder
		for _, msg := range history {
			if msg.Role != "user" && msg.Role != "assistant" {
				continue
			}
			content := strings.TrimSpace(msg.Content)
			if content == "" && len(msg.MediaRefs) == 0 {
				continue
			}
			if transcript.Len() > 0 {
				transcript.WriteString("\n")
			}
			transcript.WriteString(msg.Role)
			transcript.WriteString(": ")
			transcript.WriteString(content)
			if len(msg.MediaRefs) > 0 {
				transcript.WriteString("\nmedia_refs:")
				for _, ref := range msg.MediaRefs {
					transcript.WriteString(" ")
					transcript.WriteString(ref.Kind)
					transcript.WriteString("(")
					transcript.WriteString(geminiLiveFirst(ref.ID, ref.Path))
					if ref.MimeType != "" {
						transcript.WriteString(", ")
						transcript.WriteString(ref.MimeType)
					}
					transcript.WriteString(")")
				}
			}
		}
		if transcript.Len() > 0 {
			parts = append(parts, "Recent conversation context:\n"+transcript.String())
		}
	}
	if rt.AgentKey == closy.AgentKey && h.memory != nil {
		if prompt := closy.BuildMemoryPromptForUser(ctx, h.memory, agentData.ID, rt.UserID); strings.TrimSpace(prompt) != "" {
			parts = append(parts, prompt)
		}
	}
	return strings.Join(parts, "\n\n")
}

func geminiLiveThinkingConfig(model string) map[string]any {
	normalized := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(normalized, "2.5"):
		return map[string]any{"thinkingBudget": 0}
	case strings.Contains(normalized, "3.1"):
		return map[string]any{"thinkingLevel": "MINIMAL"}
	default:
		return nil
	}
}

func (h *GeminiLiveHandler) geminiLiveClientToUpstream(ctx context.Context, rt geminiLiveRuntimeContext, clientConn, upstream *websocket.Conn, cfg geminiLiveConfig, clientWriteMu *sync.Mutex) error {
	for {
		var event geminiLiveClientEvent
		if err := clientConn.ReadJSON(&event); err != nil {
			return err
		}
		payload, ack, err := h.geminiLiveClientEventToPayload(ctx, rt, event, cfg.InputAudioMIMEType)
		if err != nil {
			_ = geminiLiveWriteClient(clientConn, clientWriteMu, geminiLiveEvent{Type: "error", SessionID: rt.SessionID, Error: err.Error()})
			continue
		}
		if payload == nil {
			if strings.EqualFold(strings.TrimSpace(event.Type), "close") {
				return nil
			}
			continue
		}
		if err := upstream.WriteJSON(payload); err != nil {
			return fmt.Errorf("send gemini live input: %w", err)
		}
		if ack != nil {
			ack.SessionID = rt.SessionID
			_ = geminiLiveWriteClient(clientConn, clientWriteMu, *ack)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func (h *GeminiLiveHandler) geminiLiveUpstreamToClient(ctx context.Context, upstream, clientConn *websocket.Conn, cfg geminiLiveConfig, rt geminiLiveRuntimeContext, clientWriteMu *sync.Mutex) error {
	var turn geminiLiveAccumulator
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
			event.SessionID = rt.SessionID
			if err := geminiLiveWriteClient(clientConn, clientWriteMu, event); err != nil {
				return err
			}
		}
		if complete {
			userText, assistantText := turn.flush()
			h.recordTurn(ctx, rt, userText, assistantText)
			if userText != "" {
				_ = geminiLiveWriteClient(clientConn, clientWriteMu, geminiLiveEvent{Type: "message", SessionID: rt.SessionID, Role: "user", Content: userText})
			}
			if assistantText != "" {
				_ = geminiLiveWriteClient(clientConn, clientWriteMu, geminiLiveEvent{Type: "message", SessionID: rt.SessionID, Role: "assistant", Content: assistantText})
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func (h *GeminiLiveHandler) recordTurn(ctx context.Context, rt geminiLiveRuntimeContext, userText, assistantText string) {
	if h == nil || h.sessions == nil {
		return
	}
	if strings.TrimSpace(userText) != "" {
		h.sessions.AddMessage(ctx, rt.SessionID, providers.Message{Role: "user", Content: strings.TrimSpace(userText)})
	}
	if strings.TrimSpace(assistantText) != "" {
		h.sessions.AddMessage(ctx, rt.SessionID, providers.Message{Role: "assistant", Content: strings.TrimSpace(assistantText)})
	}
	if rt.AgentKey == closy.AgentKey && strings.TrimSpace(userText) != "" && h.agents != nil && h.memory != nil {
		if ag, err := h.agents.GetByKey(ctx, rt.AgentKey); err == nil && ag != nil {
			closy.PersistExtractedMemories(ctx, h.memory, closy.ExtractMemoryParams{
				UserID:           rt.UserID,
				AgentID:          ag.ID,
				UserMessage:      userText,
				AssistantMessage: assistantText,
				SessionKey:       rt.SessionID,
			})
		}
	}
}

func (h *GeminiLiveHandler) recordMedia(ctx context.Context, rt geminiLiveRuntimeContext, asset *store.MediaAssetData, event geminiLiveClientEvent) {
	if h == nil || h.sessions == nil || asset == nil {
		return
	}
	content := geminiLiveMediaHistoryContent(asset, event)
	ref := providers.MediaRef{
		ID:       asset.ID.String(),
		Kind:     geminiLiveMediaKind(asset.MimeType),
		MimeType: asset.MimeType,
		Path:     asset.StorageKey,
	}
	h.sessions.AddMessage(ctx, rt.SessionID, providers.Message{Role: "user", Content: content, MediaRefs: []providers.MediaRef{ref}})
}

func geminiLiveWriteClient(conn *websocket.Conn, mu *sync.Mutex, event geminiLiveEvent) error {
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	return conn.WriteJSON(event)
}

type geminiLiveClientEvent struct {
	Type         string `json:"type"`
	MIMEType     string `json:"mime_type,omitempty"`
	Data         string `json:"data,omitempty"`
	Content      string `json:"content,omitempty"`
	MediaID      string `json:"media_id,omitempty"`
	Caption      string `json:"caption,omitempty"`
	Source       string `json:"source,omitempty"`
	Role         string `json:"role,omitempty"`
	TurnComplete bool   `json:"turn_complete,omitempty"`
}

type geminiLiveEvent struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id,omitempty"`
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	Error     string          `json:"error,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

func geminiLiveClientEventToPayload(event geminiLiveClientEvent, defaultMIME string) (map[string]any, error) {
	payload, _, err := (*GeminiLiveHandler)(nil).geminiLiveClientEventToPayload(context.Background(), geminiLiveRuntimeContext{}, event, defaultMIME)
	return payload, err
}

func (h *GeminiLiveHandler) geminiLiveClientEventToPayload(ctx context.Context, rt geminiLiveRuntimeContext, event geminiLiveClientEvent, defaultMIME string) (map[string]any, *geminiLiveEvent, error) {
	switch strings.ToLower(strings.TrimSpace(event.Type)) {
	case "audio":
		data := strings.TrimSpace(event.Data)
		if data == "" {
			return nil, nil, fmt.Errorf("live audio event requires base64 data")
		}
		mimeType := strings.TrimSpace(event.MIMEType)
		if mimeType == "" {
			mimeType = defaultMIME
		}
		return map[string]any{"realtimeInput": map[string]any{"audio": map[string]any{"mimeType": mimeType, "data": data}}}, nil, nil
	case "audio_end":
		return map[string]any{"realtimeInput": map[string]any{"audioStreamEnd": true}}, nil, nil
	case "activity_start":
		return map[string]any{"realtimeInput": map[string]any{"activityStart": map[string]any{}}}, nil, nil
	case "activity_end":
		return map[string]any{"realtimeInput": map[string]any{"activityEnd": map[string]any{}}}, nil, nil
	case "text":
		text := strings.TrimSpace(event.Content)
		if text == "" {
			return nil, nil, fmt.Errorf("live text event requires content")
		}
		return map[string]any{"realtimeInput": map[string]any{"text": text}}, nil, nil
	case "start", "client_trace", "ready":
		return nil, nil, nil
	case "media":
		payload, asset, err := h.geminiLiveMediaPayload(ctx, event)
		if err != nil {
			return nil, nil, err
		}
		h.recordMedia(ctx, rt, asset, event)
		ack := &geminiLiveEvent{
			Type:    "media_received",
			Role:    "user",
			Content: strings.TrimSpace(event.Caption),
			Data: geminiLiveJSON(map[string]any{
				"media_id":      asset.ID.String(),
				"mime_type":     asset.MimeType,
				"filename":      asset.OriginalFilename,
				"turn_complete": event.TurnComplete,
			}),
		}
		return payload, ack, nil
	case "audio_end_and_close", "done":
		return map[string]any{"realtimeInput": map[string]any{"audioStreamEnd": true}}, nil, nil
	case "close":
		return nil, nil, nil
	default:
		return nil, nil, fmt.Errorf("unknown live client event type %q", event.Type)
	}
}

func (h *GeminiLiveHandler) geminiLiveMediaPayload(ctx context.Context, event geminiLiveClientEvent) (map[string]any, *store.MediaAssetData, error) {
	if h == nil || h.assets == nil {
		return nil, nil, fmt.Errorf("live media events are not configured")
	}
	mediaID := strings.TrimSpace(event.MediaID)
	if mediaID == "" {
		return nil, nil, fmt.Errorf("live media event requires media_id")
	}
	id, err := uuid.Parse(mediaID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid live media_id: %s", mediaID)
	}
	asset, err := h.assets.GetMediaAsset(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("load live media %s: %w", mediaID, err)
	}
	if asset == nil {
		return nil, nil, fmt.Errorf("live media not found: %s", mediaID)
	}
	if asset.Status != store.MediaStatusReady {
		return nil, nil, fmt.Errorf("live media is not ready: %s", mediaID)
	}
	if asset.StorageBackend != "" && asset.StorageBackend != store.MediaStorageLocal {
		return nil, nil, fmt.Errorf("live media storage backend %q is not supported by this runtime yet", asset.StorageBackend)
	}
	if !strings.HasPrefix(strings.ToLower(asset.MimeType), "image/") {
		return nil, nil, fmt.Errorf("live media_id %s is %q; only image/* is supported for live media events", mediaID, asset.MimeType)
	}
	if asset.StorageKey == "" {
		return nil, nil, fmt.Errorf("live media has no storage key: %s", mediaID)
	}
	data, err := os.ReadFile(asset.StorageKey)
	if err != nil {
		return nil, nil, fmt.Errorf("read live media %s: %w", mediaID, err)
	}
	text := strings.TrimSpace(event.Caption)
	if text == "" {
		text = "Use this newly uploaded image as the current visual context for the live conversation. If the user speaks about \"this\" or \"the photo\", they are referring to this image."
	}
	parts := []map[string]any{
		{"text": geminiLiveMediaContextText(asset, event, text)},
		{"inlineData": map[string]any{
			"mimeType": asset.MimeType,
			"data":     base64.StdEncoding.EncodeToString(data),
		}},
	}
	return map[string]any{"clientContent": map[string]any{
		"turns": []map[string]any{{
			"role":  "user",
			"parts": parts,
		}},
		"turnComplete": event.TurnComplete,
	}}, asset, nil
}

func geminiLiveMediaContextText(asset *store.MediaAssetData, event geminiLiveClientEvent, caption string) string {
	var lines []string
	lines = append(lines, caption)
	lines = append(lines, "<mochi_live_media_context>")
	lines = append(lines, "- media_id: "+asset.ID.String())
	if asset.OriginalFilename != "" {
		lines = append(lines, "- filename: "+asset.OriginalFilename)
	}
	if asset.MimeType != "" {
		lines = append(lines, "- mime_type: "+asset.MimeType)
	}
	if strings.TrimSpace(event.Source) != "" {
		lines = append(lines, "- source: "+strings.TrimSpace(event.Source))
	}
	if strings.TrimSpace(event.Role) != "" {
		lines = append(lines, "- role: "+strings.TrimSpace(event.Role))
	}
	lines = append(lines, "</mochi_live_media_context>")
	return strings.Join(lines, "\n")
}

func geminiLiveMediaHistoryContent(asset *store.MediaAssetData, event geminiLiveClientEvent) string {
	caption := strings.TrimSpace(event.Caption)
	if caption == "" {
		caption = "Uploaded a new image during the live conversation."
	}
	return geminiLiveMediaContextText(asset, event, caption)
}

func geminiLiveMediaKind(mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	default:
		return "document"
	}
}

type geminiLiveAccumulator struct {
	input            strings.Builder
	output           strings.Builder
	outputSuppressed bool
}

func (a *geminiLiveAccumulator) consume(message map[string]any, outputMIME string) ([]geminiLiveEvent, bool, error) {
	if errValue := message["error"]; errValue != nil {
		data, _ := json.Marshal(errValue)
		return nil, false, fmt.Errorf("gemini live server error: %s", data)
	}
	if _, ok := message["setupComplete"]; ok {
		return []geminiLiveEvent{{Type: "live_setup_complete"}}, false, nil
	}
	content, _ := message["serverContent"].(map[string]any)
	if len(content) == 0 {
		return nil, false, nil
	}
	var events []geminiLiveEvent
	if interrupted, _ := content["interrupted"].(bool); interrupted {
		a.output.Reset()
		a.outputSuppressed = true
		events = append(events, geminiLiveEvent{Type: "live_interrupted"})
	}
	if input := geminiLiveTranscriptionText(content, "inputTranscription"); input != "" && !geminiLiveIsNoisyInputTranscript(input) {
		a.input.WriteString(input)
		events = append(events, geminiLiveEvent{Type: "live_transcript", Role: "user", Content: input, Data: geminiLiveJSON(map[string]any{"source": "input", "final": false})})
	}
	if !a.outputSuppressed {
		if output := geminiLiveTranscriptionText(content, "outputTranscription"); output != "" {
			a.output.WriteString(output)
			events = append(events, geminiLiveEvent{Type: "live_transcript", Role: "assistant", Content: output, Data: geminiLiveJSON(map[string]any{"source": "output", "final": false})})
		}
		for _, audio := range geminiLiveOutputAudioParts(content, outputMIME) {
			events = append(events, geminiLiveEvent{Type: "live_audio", Role: "assistant", Data: geminiLiveJSON(audio)})
		}
	}
	complete, _ := content["turnComplete"].(bool)
	return events, complete, nil
}

func (a *geminiLiveAccumulator) flush() (string, string) {
	userText := strings.TrimSpace(a.input.String())
	assistantText := strings.TrimSpace(a.output.String())
	a.input.Reset()
	a.output.Reset()
	a.outputSuppressed = false
	return userText, assistantText
}

func geminiLiveTranscriptionText(content map[string]any, key string) string {
	transcription, _ := content[key].(map[string]any)
	text, _ := transcription["text"].(string)
	return text
}

func geminiLiveIsNoisyInputTranscript(text string) bool {
	compact := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), ""))
	if compact == "" || len([]rune(compact)) <= 1 {
		return true
	}
	switch compact {
	case "嗯", "嗯嗯", "啊", "啊啊", "呃", "额", "喂", "alo", "hello":
		return true
	}
	runes := []rune(compact)
	if len(runes) >= 4 {
		same := true
		for _, r := range runes[1:] {
			if r != runes[0] {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return len(runes) <= 8 && (strings.Contains(compact, "调调调") || strings.Contains(compact, "孤独"))
}

func geminiLiveOutputAudioParts(content map[string]any, fallbackMIME string) []map[string]any {
	modelTurn, _ := content["modelTurn"].(map[string]any)
	parts, _ := modelTurn["parts"].([]any)
	out := make([]map[string]any, 0, len(parts))
	for _, item := range parts {
		part, _ := item.(map[string]any)
		inlineData, _ := geminiLiveFirstMap(part["inlineData"], part["inline_data"])
		if len(inlineData) == 0 {
			continue
		}
		mimeType := geminiLiveFirstString(inlineData["mimeType"], inlineData["mime_type"])
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

func geminiLiveWebSocketURL(cfg geminiLiveConfig) (string, error) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = geminiLiveBaseURL(cfg.Location)
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
			apiVersion = defaultGeminiLiveAPIVersion
		}
		path += "/ws/google.cloud.aiplatform." + apiVersion + ".LlmBidiService/BidiGenerateContent"
	}
	return (&url.URL{Scheme: scheme, Host: parsed.Host, Path: path}).String(), nil
}

func geminiLiveBaseURL(location string) string {
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

func geminiLiveModelResource(cfg geminiLiveConfig) string {
	model := strings.TrimSpace(cfg.Model)
	if strings.Contains(model, "/") {
		return strings.TrimLeft(model, "/")
	}
	return fmt.Sprintf("projects/%s/locations/%s/publishers/google/models/%s", cfg.ProjectID, cfg.Location, model)
}

func geminiLiveFirst(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func geminiLiveBool(raw string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func geminiLiveEnum(raw, fallback string) string {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	if raw == "" {
		return fallback
	}
	return raw
}

func geminiLiveDuration(raw string, fallback time.Duration) time.Duration {
	if parsed, err := time.ParseDuration(strings.TrimSpace(raw)); err == nil && parsed > 0 {
		return parsed
	}
	return fallback
}

func geminiLiveFirstMap(values ...any) (map[string]any, bool) {
	for _, value := range values {
		if mapped, ok := value.(map[string]any); ok && len(mapped) > 0 {
			return mapped, true
		}
	}
	return nil, false
}

func geminiLiveFirstString(values ...any) string {
	for _, value := range values {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func geminiLiveExpectedClose(err error) bool {
	if err == nil {
		return true
	}
	return websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) ||
		strings.Contains(err.Error(), "use of closed network connection") ||
		strings.Contains(err.Error(), context.Canceled.Error())
}

func geminiLiveJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}
