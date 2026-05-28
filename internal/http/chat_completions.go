package http

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/agent"
	"github.com/nextlevelbuilder/goclaw/internal/bus"
	mediapkg "github.com/nextlevelbuilder/goclaw/internal/channels/media"
	"github.com/nextlevelbuilder/goclaw/internal/closy"
	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	mediastore "github.com/nextlevelbuilder/goclaw/internal/media"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
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
	objectStore *mediastore.ObjectStore
	agentStore  store.AgentStore
	closyMemory store.ClosyMemoryStore
	closyOOTD   store.ClosyOOTDStore
}

// SetPostTurnProcessor sets the post-turn processor for team task dispatch.
func (h *ChatCompletionsHandler) SetPostTurnProcessor(pt tools.PostTurnProcessor) {
	h.postTurn = pt
}

// SetMediaAssetStore enables media_id attachments on /v1/chat/completions.
func (h *ChatCompletionsHandler) SetMediaAssetStore(st store.MediaAssetStore) {
	h.mediaAssets = st
}

func (h *ChatCompletionsHandler) SetObjectStore(objectStore *mediastore.ObjectStore) {
	h.objectStore = objectStore
}

// SetClosyMemoryStore enables Mochi domain memory prompt injection and post-turn extraction.
func (h *ChatCompletionsHandler) SetClosyMemoryStore(agentStore store.AgentStore, mem store.ClosyMemoryStore) {
	h.agentStore = agentStore
	h.closyMemory = mem
}

// SetClosyOOTDStore enables hidden OOTD report context for Mochi follow-up chat.
func (h *ChatCompletionsHandler) SetClosyOOTDStore(st store.ClosyOOTDStore) {
	h.closyOOTD = st
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
	Source               string   `json:"source,omitempty"`
	Mode                 string   `json:"mode,omitempty"`
	VoiceTranscript      string   `json:"voice_transcript,omitempty"`
	Note                 string   `json:"note,omitempty"`
	RefersToMediaID      string   `json:"refers_to_media_id,omitempty"`
	RefersToMediaIDs     []string `json:"refers_to_media_ids,omitempty"`
	RefersToOOTDReportID string   `json:"refers_to_ootd_report_id,omitempty"`
	OOTDReportSummary    string   `json:"ootd_report_summary,omitempty"`
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
		strings.TrimSpace(req.InputContext.RefersToOOTDReportID) == "" &&
		strings.TrimSpace(req.InputContext.OOTDReportSummary) == "" &&
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
	add("refers_to_ootd_report_id", req.InputContext.RefersToOOTDReportID)
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

	mediaFiles, mediaInfos, resolvedAttachments, cleanupAttachments, err := h.resolveAttachments(r.Context(), req.Attachments)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s"}}`, err.Error()), http.StatusBadRequest)
		return
	}
	defer cleanupAttachments()

	runID := uuid.NewString()
	sessionKey := chatCompletionSessionKey(agentID, userID, req.SessionID, runID)
	cleanChatCompletionSession(r.Context(), h.sessions, sessionKey)
	lastMessage = buildChatCompletionUserMessage(lastMessage, req, mediaInfos, resolvedAttachments, sessionKey)
	memoryPrompt, memoryAgentID := h.closyMemoryPrompt(r.Context(), agentID, userID)
	ootdContext := h.ootdReportPromptContext(r.Context(), agentID, userID, req.InputContext)
	extraSystemPrompt := buildChatExtraSystemPrompt(agentID, len(mediaFiles) > 0, memoryPrompt, ootdContext)
	if len(mediaFiles) > 0 {
		memoryAgentID = uuid.Nil
	}

	w.Header().Set("X-GoClaw-Session-Id", sessionKey)
	slog.Info("chat completions request", "agent", agentID, "stream", req.Stream, "user", userID, "session", sessionKey, "attachments", len(resolvedAttachments))
	guardStructuredOOTDOutput := agentID == closy.AgentKey && len(mediaFiles) > 0

	if req.Stream {
		h.handleStream(w, r, loop, runID, sessionKey, lastMessage, req.Model, userID, mediaFiles, extraSystemPrompt, memoryAgentID, guardStructuredOOTDOutput)
	} else {
		h.handleNonStream(w, r, loop, runID, sessionKey, lastMessage, req.Model, userID, mediaFiles, extraSystemPrompt, memoryAgentID, guardStructuredOOTDOutput)
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

func (h *ChatCompletionsHandler) resolveAttachments(ctx context.Context, attachments []chatAttachment) ([]bus.MediaFile, []mediapkg.MediaInfo, []resolvedChatAttachment, func(), error) {
	if len(attachments) == 0 {
		return nil, nil, nil, func() {}, nil
	}
	if h.mediaAssets == nil {
		return nil, nil, nil, func() {}, fmt.Errorf("media attachments are not configured")
	}

	var cleanups []func()
	cleanupAll := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			if cleanups[i] != nil {
				cleanups[i]()
			}
		}
	}
	files := make([]bus.MediaFile, 0, len(attachments))
	infos := make([]mediapkg.MediaInfo, 0, len(attachments))
	resolved := make([]resolvedChatAttachment, 0, len(attachments))
	for _, att := range attachments {
		if att.MediaID == "" {
			cleanupAll()
			return nil, nil, nil, func() {}, fmt.Errorf("attachment media_id is required")
		}
		id, err := uuid.Parse(att.MediaID)
		if err != nil {
			cleanupAll()
			return nil, nil, nil, func() {}, fmt.Errorf("invalid attachment media_id: %s", att.MediaID)
		}
		asset, err := h.mediaAssets.GetMediaAsset(ctx, id)
		if err != nil {
			cleanupAll()
			return nil, nil, nil, func() {}, fmt.Errorf("load attachment %s: %w", att.MediaID, err)
		}
		if asset == nil {
			cleanupAll()
			return nil, nil, nil, func() {}, fmt.Errorf("attachment not found: %s", att.MediaID)
		}
		if asset.Status != store.MediaStatusReady {
			cleanupAll()
			return nil, nil, nil, func() {}, fmt.Errorf("attachment is not ready: %s", att.MediaID)
		}
		localPath, cleanup, err := mediaAssetTempPath(ctx, h.objectStore, asset)
		if err != nil {
			cleanupAll()
			return nil, nil, nil, func() {}, fmt.Errorf("attachment file unavailable: %s", att.MediaID)
		}
		cleanups = append(cleanups, cleanup)
		mimeType := asset.MimeType
		if mimeType == "" {
			mimeType = mediapkg.DetectMIMEType(asset.OriginalFilename)
		}
		kind := mediapkg.MediaKindFromMime(mimeType)
		sourceURL := mediaAssetURL(ctx, h.objectStore, asset)
		files = append(files, bus.MediaFile{
			ID:        att.MediaID,
			Path:      localPath,
			SourceURL: sourceURL,
			MimeType:  mimeType,
			Filename:  asset.OriginalFilename,
		})
		infos = append(infos, mediapkg.MediaInfo{
			Type:        kind,
			FilePath:    localPath,
			FileID:      att.MediaID,
			SourceURL:   sourceURL,
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
	return files, infos, resolved, cleanupAll, nil
}

func (h *ChatCompletionsHandler) ootdReportPromptContext(ctx context.Context, agentID, userID string, input chatInputContext) string {
	if agentID != closy.AgentKey || strings.TrimSpace(userID) == "" {
		return ""
	}
	if h != nil && h.closyOOTD != nil && strings.TrimSpace(input.RefersToOOTDReportID) != "" {
		id, err := uuid.Parse(strings.TrimSpace(input.RefersToOOTDReportID))
		if err == nil {
			review, err := h.closyOOTD.GetClosyOOTDReview(ctx, id)
			if err == nil && review != nil && (review.UserID == "" || review.UserID == userID) {
				if report, err := ootdReportFromReview(review); err == nil {
					return buildOOTDReportPromptContext(report)
				}
			}
		}
	}
	if summary := strings.TrimSpace(input.OOTDReportSummary); summary != "" {
		return strings.TrimSpace(`<MOCHI_OOTD_REPORT_CONTEXT>
Use this only when the user asks follow-up questions about the current outfit or OOTD report. Do not quote this block directly.
` + summary + `
</MOCHI_OOTD_REPORT_CONTEXT>`)
	}
	return ""
}

func buildOOTDReportPromptContext(report closy.OOTDReport) string {
	var lines []string
	add := func(label, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			lines = append(lines, label+": "+value)
		}
	}
	add("Title", report.TodayJudgment.Title)
	add("Score", fmt.Sprintf("%.1f/10 (%s)", report.TodayJudgment.Score, report.TodayJudgment.Label))
	add("Summary", report.TodayJudgment.Summary)
	add("Overall style", report.OverallStyle)
	if len(report.Highlights) > 0 {
		add("Highlights", strings.Join(report.Highlights, "; "))
	}
	add("Biggest issue", report.BiggestIssue)
	if len(report.Suggestions) > 0 {
		var suggestions []string
		for _, item := range report.Suggestions {
			suggestion := strings.TrimSpace(item.Title)
			if body := strings.TrimSpace(item.Body); body != "" {
				if suggestion != "" {
					suggestion += ": "
				}
				suggestion += body
			}
			if suggestion != "" {
				suggestions = append(suggestions, suggestion)
			}
		}
		add("Suggestions", strings.Join(suggestions, " | "))
	}
	add("Mochi line", report.MochiLine)
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(`<MOCHI_OOTD_REPORT_CONTEXT>
Use this only when the user asks follow-up questions about the current outfit or OOTD report. Do not quote this block directly.
` + strings.Join(lines, "\n") + `
</MOCHI_OOTD_REPORT_CONTEXT>`)
}

func buildChatExtraSystemPrompt(agentID string, hasMedia bool, memoryPrompt, ootdContext string) string {
	memoryPrompt = strings.TrimSpace(memoryPrompt)
	ootdContext = strings.TrimSpace(ootdContext)
	if agentID != closy.AgentKey || !hasMedia {
		return strings.TrimSpace(strings.Join([]string{memoryPrompt, ootdContext}, "\n\n"))
	}
	return strings.TrimSpace(strings.Join([]string{ootdContext, `For this image chat turn:
- Answer conversationally. Do not output JSON, Markdown tables, or the OOTDReport schema.
- Use only visible evidence from the current attached image.
- Do not mention chat memory, prior uploads, repeated images, or how many times the user sent something.
- If a visible wearable outfit appears in the image, discuss that visible outfit even when the image looks like a reference image, stock photo, poster, screenshot, or cropped upload.
- If the image is low-resolution, blurry, cropped, or watermarked but clothing is still visible, state uncertainty briefly and still give concrete outfit feedback from visible colors, silhouette, items, and styling.
- Do not ask the user to resend solely because the image is low-resolution, blurry, cropped, watermarked, or looks like a screenshot.
- Ask for a clearer full-body outfit photo only when there is no visible wearable outfit, the image is blank/corrupted, or clothing details are genuinely not visible.
- Do not say the image is broken or unavailable unless the system actually returned an attachment/file error.
- Critique clothes, styling, colors, silhouette, and scene fit only; do not critique the person's body, face, identity, or attractiveness.`}, "\n\n"))
}

type chatCompletionSessionCleaner interface {
	GetHistory(ctx context.Context, key string) []providers.Message
	SetHistory(ctx context.Context, key string, msgs []providers.Message)
	GetSummary(ctx context.Context, key string) string
	SetSummary(ctx context.Context, key, summary string)
	Save(ctx context.Context, key string) error
}

func cleanChatCompletionSession(ctx context.Context, sessions chatCompletionSessionCleaner, sessionKey string) {
	if sessions == nil || strings.TrimSpace(sessionKey) == "" {
		return
	}

	history := sessions.GetHistory(ctx, sessionKey)
	if len(history) == 0 && strings.TrimSpace(sessions.GetSummary(ctx, sessionKey)) == "" {
		return
	}

	changed := false
	cleaned := make([]providers.Message, 0, len(history))
	for _, msg := range history {
		if msg.Role == "assistant" && isOOTDReportChatContent(msg.Content) {
			changed = true
			continue
		}
		cleaned = append(cleaned, msg)
	}
	if changed {
		sessions.SetHistory(ctx, sessionKey, cleaned)
	}
	if summary := sessions.GetSummary(ctx, sessionKey); isOOTDReportChatContent(summary) {
		sessions.SetSummary(ctx, sessionKey, "")
		changed = true
	}
	if changed {
		_ = sessions.Save(ctx, sessionKey)
	}
}

func isOOTDReportChatContent(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return false
	}
	if _, err := closy.ParseOOTDReport(content); err == nil {
		return true
	}
	return strings.Contains(content, `"todayJudgment"`) &&
		(strings.Contains(content, `"overallStyle"`) ||
			strings.Contains(content, `"biggestIssue"`) ||
			strings.Contains(content, `"shareCard"`))
}

func normalizeChatAssistantContent(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return content
	}
	report, err := closy.ParseOOTDReport(content)
	if err != nil {
		return content
	}
	return ootdReportAsChatReply(report)
}

const structuredOOTDStreamGuardMaxPrefix = 512

type structuredOOTDStreamGuard struct {
	enabled            bool
	decided            bool
	suppressStructured bool
	released           bool
	buffer             strings.Builder
}

func newStructuredOOTDStreamGuard(enabled bool) *structuredOOTDStreamGuard {
	return &structuredOOTDStreamGuard{enabled: enabled}
}

func (g *structuredOOTDStreamGuard) Push(content string) (string, bool) {
	if !g.enabled {
		return content, true
	}
	if content == "" {
		return "", false
	}
	if g.decided {
		if g.suppressStructured {
			g.buffer.WriteString(content)
			return "", false
		}
		g.released = true
		return content, true
	}

	g.buffer.WriteString(content)
	buffered := g.buffer.String()
	if isLikelyStructuredOOTDStream(buffered) {
		g.decided = true
		g.suppressStructured = true
		return "", false
	}
	if shouldHoldStructuredOOTDStreamPrefix(buffered) && len(buffered) < structuredOOTDStreamGuardMaxPrefix {
		return "", false
	}

	g.decided = true
	g.released = true
	return buffered, true
}

func (g *structuredOOTDStreamGuard) Final(content string) string {
	if !g.enabled {
		return content
	}
	candidate := strings.TrimSpace(content)
	if candidate == "" {
		candidate = strings.TrimSpace(g.buffer.String())
	}
	if candidate == "" {
		return ""
	}
	if g.suppressStructured || !g.released {
		return normalizeChatAssistantContent(candidate)
	}
	return ""
}

func isLikelyStructuredOOTDStream(content string) bool {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSpace(content)
	return strings.HasPrefix(content, "{") && strings.Contains(content, `"todayJudgment"`)
}

func shouldHoldStructuredOOTDStreamPrefix(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return true
	}
	return strings.HasPrefix(content, "{") ||
		strings.HasPrefix(content, "[") ||
		strings.HasPrefix(content, "```")
}

func ootdReportAsChatReply(report closy.OOTDReport) string {
	var parts []string
	if title := strings.TrimSpace(report.TodayJudgment.Title); title != "" {
		score := fmt.Sprintf("%.1f/10", report.TodayJudgment.Score)
		if label := strings.TrimSpace(report.TodayJudgment.Label); label != "" {
			parts = append(parts, fmt.Sprintf("%s（%s，%s）", title, score, label))
		} else {
			parts = append(parts, fmt.Sprintf("%s（%s）", title, score))
		}
	}
	if summary := strings.TrimSpace(report.TodayJudgment.Summary); summary != "" {
		parts = append(parts, summary)
	}
	if style := strings.TrimSpace(report.OverallStyle); style != "" {
		parts = append(parts, "整体风格："+style)
	}
	if len(report.Highlights) > 0 {
		var highlights []string
		for _, item := range report.Highlights {
			if item = strings.TrimSpace(item); item != "" {
				highlights = append(highlights, item)
			}
		}
		if len(highlights) > 0 {
			parts = append(parts, "亮点："+strings.Join(highlights, "；"))
		}
	}
	if issue := strings.TrimSpace(report.BiggestIssue); issue != "" {
		parts = append(parts, "最大问题："+issue)
	}
	if len(report.Suggestions) > 0 {
		var suggestions []string
		for _, item := range report.Suggestions {
			title := strings.TrimSpace(item.Title)
			body := strings.TrimSpace(item.Body)
			switch {
			case title != "" && body != "":
				suggestions = append(suggestions, title+"："+body)
			case body != "":
				suggestions = append(suggestions, body)
			case title != "":
				suggestions = append(suggestions, title)
			}
		}
		if len(suggestions) > 0 {
			parts = append(parts, "可以这样改："+strings.Join(suggestions, "；"))
		}
	}
	if line := strings.TrimSpace(report.MochiLine); line != "" {
		parts = append(parts, line)
	}
	if len(parts) == 0 {
		return "I can review the outfit, but I need a clearer view to be specific."
	}
	return strings.Join(parts, "\n\n")
}

func (h *ChatCompletionsHandler) handleNonStream(w http.ResponseWriter, r *http.Request, loop agent.Agent, runID, sessionKey, message, model, userID string, mediaFiles []bus.MediaFile, extraSystemPrompt string, memoryAgentID uuid.UUID, guardStructuredOOTDOutput bool) {
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
		if guardStructuredOOTDOutput {
			result.Content = normalizeChatAssistantContent(result.Content)
		}
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

func (h *ChatCompletionsHandler) handleStream(w http.ResponseWriter, r *http.Request, loop agent.Agent, runID, sessionKey, message, model, userID string, mediaFiles []bus.MediaFile, extraSystemPrompt string, memoryAgentID uuid.UUID, guardStructuredOOTDOutput bool) {
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
	streamGuard := newStructuredOOTDStreamGuard(guardStructuredOOTDOutput)
	writeEvent := func(event agent.AgentEvent) bool {
		if event.Type != protocol.ChatEventChunk {
			return true
		}
		content := agentEventPayloadString(event.Payload, "content")
		if content == "" {
			return true
		}
		if guardStructuredOOTDOutput {
			release, ok := streamGuard.Push(content)
			if !ok || release == "" {
				return true
			}
			streamedContent = true
			if err := writeSSEChunk(w, flusher, completionID, model, &chatMessage{Content: release}, ""); err != nil {
				cancel()
				return false
			}
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
				_ = writeSSEError(w, flusher, "The upstream model stream was interrupted. Please retry.")
			} else if guardStructuredOOTDOutput {
				content := ""
				if run.result != nil && strings.TrimSpace(run.result.Content) != "" {
					content = run.result.Content
				}
				content = streamGuard.Final(content)
				if run.result != nil {
					if content != "" {
						run.result.Content = content
					} else {
						run.result.Content = normalizeChatAssistantContent(run.result.Content)
					}
				}
				if content != "" {
					_ = writeSSEChunk(w, flusher, completionID, model, &chatMessage{Content: SignFileURLs(content, FileSigningKey())}, "stop")
				} else {
					_ = writeSSEChunk(w, flusher, completionID, model, &chatMessage{}, "stop")
				}
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

func writeSSEError(w http.ResponseWriter, flusher http.Flusher, message string) error {
	payload := map[string]any{
		"error": map[string]string{
			"message": message,
		},
	}
	data, _ := json.Marshal(payload)
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
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
