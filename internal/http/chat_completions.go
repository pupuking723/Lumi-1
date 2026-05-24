package http

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/agent"
	"github.com/nextlevelbuilder/goclaw/internal/bus"
	mediapkg "github.com/nextlevelbuilder/goclaw/internal/channels/media"
	"github.com/nextlevelbuilder/goclaw/internal/closy"
	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/sessions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/tools"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// ChatCompletionsHandler handles POST /v1/chat/completions (OpenAI-compatible).
type ChatCompletionsHandler struct {
	agents      *agent.Router
	sessions    store.SessionStore
	isManaged   bool
	rateLimiter func(string) bool // rate limit check: key → allowed (nil = no limit)
	postTurn    tools.PostTurnProcessor
	mediaAssets store.MediaAssetStore
	agentStore  store.AgentStore
	closyMemory store.ClosyMemoryStore
}

// SetPostTurnProcessor sets the post-turn processor for team task dispatch.
func (h *ChatCompletionsHandler) SetPostTurnProcessor(pt tools.PostTurnProcessor) {
	h.postTurn = pt
}

// SetMediaAssetStore enables media_id attachments on /v1/chat/completions.
func (h *ChatCompletionsHandler) SetMediaAssetStore(st store.MediaAssetStore) {
	h.mediaAssets = st
}

// SetClosyMemoryStore enables Mochi domain memory prompt injection and post-turn extraction.
func (h *ChatCompletionsHandler) SetClosyMemoryStore(agentStore store.AgentStore, mem store.ClosyMemoryStore) {
	h.agentStore = agentStore
	h.closyMemory = mem
}

// NewChatCompletionsHandler creates a handler for the chat completions endpoint.
func NewChatCompletionsHandler(agents *agent.Router, sess store.SessionStore, isManaged bool) *ChatCompletionsHandler {
	return &ChatCompletionsHandler{
		agents:    agents,
		sessions:  sess,
		isManaged: isManaged,
	}
}

// SetRateLimiter sets the rate limiter function for HTTP requests.
func (h *ChatCompletionsHandler) SetRateLimiter(fn func(string) bool) {
	h.rateLimiter = fn
}

type chatCompletionsRequest struct {
	Model        string           `json:"model"`
	Messages     []chatMessage    `json:"messages"`
	Stream       bool             `json:"stream"`
	User         string           `json:"user,omitempty"`
	SessionID    string           `json:"session_id,omitempty"`
	Scenario     string           `json:"scenario,omitempty"`
	InputContext chatInputContext `json:"input_context,omitempty"`
	Attachments  []chatAttachment `json:"attachments,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

type chatAttachment struct {
	MediaID string `json:"media_id,omitempty"`
	Caption string `json:"caption,omitempty"`
	Source  string `json:"source,omitempty"`
	Role    string `json:"role,omitempty"`
}

type chatInputContext struct {
	Source           string   `json:"source,omitempty"`
	Mode             string   `json:"mode,omitempty"`
	VoiceTranscript  string   `json:"voice_transcript,omitempty"`
	Note             string   `json:"note,omitempty"`
	RefersToMediaID  string   `json:"refers_to_media_id,omitempty"`
	RefersToMediaIDs []string `json:"refers_to_media_ids,omitempty"`
}

type resolvedChatAttachment struct {
	MediaID  string
	Kind     string
	Filename string
	MIMEType string
	Caption  string
	Source   string
	Role     string
}

type chatCompletionsResponse struct {
	ID        string       `json:"id"`
	Object    string       `json:"object"`
	Created   int64        `json:"created"`
	Model     string       `json:"model"`
	SessionID string       `json:"session_id,omitempty"`
	Choices   []chatChoice `json:"choices"`
	Usage     *chatUsage   `json:"usage,omitempty"`
}

type chatChoice struct {
	Index        int          `json:"index"`
	Message      *chatMessage `json:"message,omitempty"`
	Delta        *chatMessage `json:"delta,omitempty"`
	FinishReason string       `json:"finish_reason,omitempty"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatStreamRunResult struct {
	result *agent.RunResult
	err    error
}

func chatCompletionSessionKey(agentID, userID, requestedSessionID, runID string) string {
	requestedSessionID = strings.TrimSpace(requestedSessionID)
	if requestedSessionID != "" {
		if keyAgent, _ := sessions.ParseSessionKey(requestedSessionID); keyAgent == agentID {
			return requestedSessionID
		}
		owner := sanitizeChatSessionPart(userID)
		if owner == "" {
			owner = "anonymous"
		}
		session := sanitizeChatSessionPart(requestedSessionID)
		if session == "" {
			session = "default"
		}
		return sessions.BuildSessionKey(agentID, "cchat", sessions.PeerDirect, owner+"-"+session)
	}

	sessionSuffix := "http-" + runID[:8]
	if userID != "" {
		sessionSuffix = "http-" + sanitizeChatSessionPart(userID) + "-" + runID[:8]
	}
	return sessions.SessionKey(agentID, sessionSuffix)
}

func sanitizeChatSessionPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '-' || r == '.' || r == '@'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func buildChatCompletionUserMessage(userMessage string, req chatCompletionsRequest, mediaInfos []mediapkg.MediaInfo, attachments []resolvedChatAttachment, sessionKey string) string {
	var parts []string
	if tags := mediapkg.BuildMediaTags(mediaInfos); tags != "" {
		parts = append(parts, tags)
	}
	if block := buildChatMultimodalContextBlock(req, attachments, sessionKey); block != "" {
		parts = append(parts, block)
	}
	userMessage = strings.TrimSpace(userMessage)
	if userMessage != "" {
		parts = append(parts, userMessage)
	}
	return strings.Join(parts, "\n\n")
}

func buildChatMultimodalContextBlock(req chatCompletionsRequest, attachments []resolvedChatAttachment, sessionKey string) string {
	if strings.TrimSpace(req.Scenario) == "" &&
		strings.TrimSpace(req.InputContext.Source) == "" &&
		strings.TrimSpace(req.InputContext.Mode) == "" &&
		strings.TrimSpace(req.InputContext.VoiceTranscript) == "" &&
		strings.TrimSpace(req.InputContext.Note) == "" &&
		strings.TrimSpace(req.InputContext.RefersToMediaID) == "" &&
		len(req.InputContext.RefersToMediaIDs) == 0 &&
		len(attachments) == 0 {
		return ""
	}
	var lines []string
	add := func(label, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			lines = append(lines, "- "+label+": "+value)
		}
	}
	add("session_key", sessionKey)
	add("scenario", req.Scenario)
	add("source", req.InputContext.Source)
	add("mode", req.InputContext.Mode)
	add("voice_transcript", req.InputContext.VoiceTranscript)
	add("note", req.InputContext.Note)
	add("refers_to_media_id", req.InputContext.RefersToMediaID)
	if len(req.InputContext.RefersToMediaIDs) > 0 {
		var ids []string
		for _, id := range req.InputContext.RefersToMediaIDs {
			if strings.TrimSpace(id) != "" {
				ids = append(ids, strings.TrimSpace(id))
			}
		}
		if len(ids) > 0 {
			add("refers_to_media_ids", strings.Join(ids, ", "))
		}
	}
	for i, att := range attachments {
		var meta []string
		meta = append(meta, "media_id="+att.MediaID)
		if att.Kind != "" {
			meta = append(meta, "kind="+att.Kind)
		}
		if att.Filename != "" {
			meta = append(meta, "filename="+att.Filename)
		}
		if att.MIMEType != "" {
			meta = append(meta, "mime="+att.MIMEType)
		}
		if att.Source != "" {
			meta = append(meta, "source="+att.Source)
		}
		if att.Role != "" {
			meta = append(meta, "role="+att.Role)
		}
		if att.Caption != "" {
			meta = append(meta, "caption="+att.Caption)
		}
		lines = append(lines, fmt.Sprintf("- attachment_%d: %s", i+1, strings.Join(meta, "; ")))
	}
	if len(lines) == 0 {
		return ""
	}
	return "<mochi_multimodal_context>\n" + strings.Join(lines, "\n") + "\n</mochi_multimodal_context>"
}

func (h *ChatCompletionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	locale := extractLocale(r)

	if r.Method != http.MethodPost {
		http.Error(w, i18n.T(locale, i18n.MsgMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	// Auth + RBAC check (gateway token or API key, operator required for POST)
	auth := resolveAuth(r)
	if !auth.Authenticated {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s","type":"invalid_request_error"}}`, i18n.T(locale, i18n.MsgInvalidAuth)), http.StatusUnauthorized)
		return
	}
	if !permissions.HasMinRole(auth.Role, permissions.RoleOperator) {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s","type":"invalid_request_error"}}`, i18n.T(locale, i18n.MsgPermissionDenied, "/v1/chat/completions")), http.StatusForbidden)
		return
	}

	// Inject tenant, role, user, and locale into context for downstream stores/tools.
	r = r.WithContext(enrichContext(r.Context(), r, auth))

	// Rate limit check (per IP or bearer token)
	if h.rateLimiter != nil {
		key := r.RemoteAddr
		if token := extractBearerToken(r); token != "" {
			key = "token:" + token
		}
		if !h.rateLimiter(key) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, fmt.Sprintf(`{"error":{"message":"%s","type":"rate_limit_error"}}`, i18n.T(locale, i18n.MsgRateLimitExceeded)), http.StatusTooManyRequests)
			return
		}
	}

	// Limit request body size to prevent DoS
	const maxRequestBodySize = 1 << 20 // 1MB
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

	var req chatCompletionsRequest
	if !bindJSON(w, r, locale, &req) {
		return
	}

	if len(req.Messages) == 0 {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s"}}`, i18n.T(locale, i18n.MsgMsgsRequired)), http.StatusBadRequest)
		return
	}

	agentID := extractAgentID(r, req.Model)
	userID := store.UserIDFromContext(r.Context()) // resolved by enrichContext (respects API key owner binding)
	if h.isManaged && userID == "" {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s"}}`, i18n.T(locale, i18n.MsgUserIDHeader)), http.StatusBadRequest)
		return
	}

	loop, err := h.agents.Get(r.Context(), agentID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s"}}`, i18n.T(locale, i18n.MsgNotFound, "agent", agentID)), http.StatusNotFound)
		return
	}

	// Extract the last user message
	var lastMessage string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			lastMessage = req.Messages[i].Content
			break
		}
	}
	if lastMessage == "" {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s"}}`, i18n.T(locale, i18n.MsgNoUserMessage)), http.StatusBadRequest)
		return
	}

	mediaFiles, mediaInfos, resolvedAttachments, err := h.resolveAttachments(r.Context(), req.Attachments)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s"}}`, err.Error()), http.StatusBadRequest)
		return
	}

	runID := uuid.NewString()
	sessionKey := chatCompletionSessionKey(agentID, userID, req.SessionID, runID)
	lastMessage = buildChatCompletionUserMessage(lastMessage, req, mediaInfos, resolvedAttachments, sessionKey)
	memoryPrompt, memoryAgentID := h.closyMemoryPrompt(r.Context(), agentID, userID)

	w.Header().Set("X-GoClaw-Session-Id", sessionKey)
	slog.Info("chat completions request", "agent", agentID, "stream", req.Stream, "user", userID, "session", sessionKey, "attachments", len(resolvedAttachments))

	if req.Stream {
		h.handleStream(w, r, loop, runID, sessionKey, lastMessage, req.Model, userID, mediaFiles, memoryPrompt, memoryAgentID)
	} else {
		h.handleNonStream(w, r, loop, runID, sessionKey, lastMessage, req.Model, userID, mediaFiles, memoryPrompt, memoryAgentID)
	}
}

func (h *ChatCompletionsHandler) closyMemoryPrompt(ctx context.Context, agentID, userID string) (string, uuid.UUID) {
	if h == nil || h.closyMemory == nil || h.agentStore == nil || agentID != closy.AgentKey || strings.TrimSpace(userID) == "" {
		return "", uuid.Nil
	}
	ag, err := h.agentStore.GetByKey(ctx, agentID)
	if err != nil || ag == nil {
		return "", uuid.Nil
	}
	return closy.BuildMemoryPromptForUser(ctx, h.closyMemory, ag.ID, userID), ag.ID
}

func (h *ChatCompletionsHandler) resolveAttachments(ctx context.Context, attachments []chatAttachment) ([]bus.MediaFile, []mediapkg.MediaInfo, []resolvedChatAttachment, error) {
	if len(attachments) == 0 {
		return nil, nil, nil, nil
	}
	if h.mediaAssets == nil {
		return nil, nil, nil, fmt.Errorf("media attachments are not configured")
	}

	files := make([]bus.MediaFile, 0, len(attachments))
	infos := make([]mediapkg.MediaInfo, 0, len(attachments))
	resolved := make([]resolvedChatAttachment, 0, len(attachments))
	for _, att := range attachments {
		if att.MediaID == "" {
			return nil, nil, nil, fmt.Errorf("attachment media_id is required")
		}
		id, err := uuid.Parse(att.MediaID)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("invalid attachment media_id: %s", att.MediaID)
		}
		asset, err := h.mediaAssets.GetMediaAsset(ctx, id)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("load attachment %s: %w", att.MediaID, err)
		}
		if asset == nil {
			return nil, nil, nil, fmt.Errorf("attachment not found: %s", att.MediaID)
		}
		if asset.Status != store.MediaStatusReady {
			return nil, nil, nil, fmt.Errorf("attachment is not ready: %s", att.MediaID)
		}
		if asset.StorageBackend != "" && asset.StorageBackend != store.MediaStorageLocal {
			return nil, nil, nil, fmt.Errorf("attachment storage backend %q is not supported by this runtime yet", asset.StorageBackend)
		}
		if asset.StorageKey == "" {
			return nil, nil, nil, fmt.Errorf("attachment has no storage key: %s", att.MediaID)
		}
		if _, err := os.Stat(asset.StorageKey); err != nil {
			return nil, nil, nil, fmt.Errorf("attachment file unavailable: %s", att.MediaID)
		}
		mimeType := asset.MimeType
		if mimeType == "" {
			mimeType = mediapkg.DetectMIMEType(asset.OriginalFilename)
		}
		kind := mediapkg.MediaKindFromMime(mimeType)
		files = append(files, bus.MediaFile{
			Path:     asset.StorageKey,
			MimeType: mimeType,
			Filename: asset.OriginalFilename,
		})
		infos = append(infos, mediapkg.MediaInfo{
			Type:        kind,
			FilePath:    asset.StorageKey,
			FileID:      att.MediaID,
			ContentType: mimeType,
			FileName:    asset.OriginalFilename,
			FileSize:    asset.Size,
		})
		resolved = append(resolved, resolvedChatAttachment{
			MediaID:  att.MediaID,
			Kind:     kind,
			Filename: asset.OriginalFilename,
			MIMEType: mimeType,
			Caption:  strings.TrimSpace(att.Caption),
			Source:   strings.TrimSpace(att.Source),
			Role:     strings.TrimSpace(att.Role),
		})
	}
	return files, infos, resolved, nil
}

func (h *ChatCompletionsHandler) handleNonStream(w http.ResponseWriter, r *http.Request, loop agent.Agent, runID, sessionKey, message, model, userID string, mediaFiles []bus.MediaFile, extraSystemPrompt string, memoryAgentID uuid.UUID) {
	ctx, drainTeamDispatch := tools.InjectTeamDispatch(r.Context(), h.postTurn)
	defer drainTeamDispatch()

	result, err := loop.Run(ctx, agent.RunRequest{
		SessionKey: sessionKey,
		Message:    message,
		Media:      mediaFiles,
		// C-side chat attachments should be visible to the selected agent model
		// directly. Keep the global read_image tool routing untouched for
		// console/channel flows.
		ForceInlineImages: len(mediaFiles) > 0,
		Channel:           "http",
		ChatID:            "api",
		RunID:             runID,
		UserID:            userID,
		Stream:            false,
		ExtraSystemPrompt: extraSystemPrompt,
	})

	if err != nil {
		locale := store.LocaleFromContext(r.Context())
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s"}}`, i18n.T(locale, i18n.MsgInternalError, err.Error())), http.StatusInternalServerError)
		return
	}
	if result != nil {
		h.captureClosyMemory(r.Context(), memoryAgentID, userID, sessionKey, message, result.Content)
	}

	resp := chatCompletionsResponse{
		ID:        "chatcmpl-" + runID[:8],
		Object:    "chat.completion",
		Created:   time.Now().Unix(),
		Model:     model,
		SessionID: sessionKey,
		Choices: []chatChoice{{
			Index:        0,
			Message:      &chatMessage{Role: "assistant", Content: SignFileURLs(result.Content, FileSigningKey())},
			FinishReason: "stop",
		}},
	}

	if result.Usage != nil {
		resp.Usage = &chatUsage{
			PromptTokens:     result.Usage.PromptTokens,
			CompletionTokens: result.Usage.CompletionTokens,
			TotalTokens:      result.Usage.TotalTokens,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *ChatCompletionsHandler) handleStream(w http.ResponseWriter, r *http.Request, loop agent.Agent, runID, sessionKey, message, model, userID string, mediaFiles []bus.MediaFile, extraSystemPrompt string, memoryAgentID uuid.UUID) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		locale := store.LocaleFromContext(r.Context())
		http.Error(w, i18n.T(locale, i18n.MsgStreamingNotSupported), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	completionID := "chatcmpl-" + runID[:8]

	// Send initial role chunk
	if err := writeSSEChunk(w, flusher, completionID, model, &chatMessage{Role: "assistant"}, ""); err != nil {
		return
	}

	baseCtx, drainTeamDispatch := tools.InjectTeamDispatch(r.Context(), h.postTurn)
	defer drainTeamDispatch()
	ctx, cancel := context.WithCancel(baseCtx)
	defer cancel()

	eventCh := make(chan agent.AgentEvent, 64)
	resultCh := make(chan chatStreamRunResult, 1)

	go func() {
		defer close(eventCh)

		result, err := loop.Run(ctx, agent.RunRequest{
			SessionKey: sessionKey,
			Message:    message,
			Media:      mediaFiles,
			// C-side chat attachments should be visible to the selected agent model
			// directly. Keep the global read_image tool routing untouched for
			// console/channel flows.
			ForceInlineImages: len(mediaFiles) > 0,
			Channel:           "http",
			ChatID:            "api",
			RunID:             runID,
			UserID:            userID,
			Stream:            true,
			ExtraSystemPrompt: extraSystemPrompt,
			OnEvent: func(event agent.AgentEvent) {
				if event.RunID != runID {
					return
				}
				switch event.Type {
				case protocol.ChatEventChunk, protocol.ChatEventThinking:
					select {
					case eventCh <- event:
					case <-ctx.Done():
					}
				}
			},
		})

		resultCh <- chatStreamRunResult{result: result, err: err}
	}()

	streamedContent := false
	writeEvent := func(event agent.AgentEvent) bool {
		if event.Type != protocol.ChatEventChunk {
			return true
		}
		content := agentEventPayloadString(event.Payload, "content")
		if content == "" {
			return true
		}
		streamedContent = true
		if err := writeSSEChunk(w, flusher, completionID, model, &chatMessage{Content: content}, ""); err != nil {
			cancel()
			return false
		}
		return true
	}

	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				eventCh = nil
				continue
			}
			if !writeEvent(event) {
				return
			}
		case run := <-resultCh:
			for event := range eventCh {
				if !writeEvent(event) {
					return
				}
			}

			if run.err != nil {
				_ = writeSSEChunk(w, flusher, completionID, model, &chatMessage{Content: "Error: " + run.err.Error()}, "stop")
			} else if !streamedContent && run.result != nil && run.result.Content != "" {
				// Fallback for providers that expose ChatStream but only return final content.
				_ = writeSSEChunk(w, flusher, completionID, model, &chatMessage{Content: SignFileURLs(run.result.Content, FileSigningKey())}, "stop")
			} else {
				_ = writeSSEChunk(w, flusher, completionID, model, &chatMessage{}, "stop")
			}
			if run.err == nil && run.result != nil {
				h.captureClosyMemory(r.Context(), memoryAgentID, userID, sessionKey, message, run.result.Content)
			}
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		case <-r.Context().Done():
			cancel()
			return
		}
	}
}

func (h *ChatCompletionsHandler) captureClosyMemory(ctx context.Context, agentID uuid.UUID, userID, sessionKey, userMessage, assistantMessage string) {
	if h == nil || h.closyMemory == nil || agentID == uuid.Nil || strings.TrimSpace(userID) == "" {
		return
	}
	closy.PersistExtractedMemories(ctx, h.closyMemory, closy.ExtractMemoryParams{
		UserID:           userID,
		AgentID:          agentID,
		UserMessage:      userMessage,
		AssistantMessage: assistantMessage,
		SessionKey:       sessionKey,
	})
}

func writeSSEChunk(w http.ResponseWriter, flusher http.Flusher, id, model string, delta *chatMessage, finishReason string) error {
	chunk := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         delta,
			"finish_reason": nilIfEmpty(finishReason),
		}},
	}

	data, _ := json.Marshal(chunk)
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func agentEventPayloadString(payload any, key string) string {
	switch p := payload.(type) {
	case map[string]string:
		return p[key]
	case map[string]any:
		if v, ok := p[key].(string); ok {
			return v
		}
	}
	return ""
}
