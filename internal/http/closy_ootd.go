package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/agent"
	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/closy"
	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/media"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/sessions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

type ClosyOOTDHandler struct {
	agents      *agent.Router
	mediaAssets store.MediaAssetStore
	objectStore *media.ObjectStore
	reviews     store.ClosyOOTDStore
	memory      store.ClosyMemoryStore
}

func NewClosyOOTDHandler(agents *agent.Router, mediaAssets store.MediaAssetStore, reviews store.ClosyOOTDStore, memory store.ClosyMemoryStore) *ClosyOOTDHandler {
	return &ClosyOOTDHandler{agents: agents, mediaAssets: mediaAssets, reviews: reviews, memory: memory}
}

func (h *ClosyOOTDHandler) SetObjectStore(objectStore *media.ObjectStore) {
	h.objectStore = objectStore
}

func (h *ClosyOOTDHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/closy/ootd/reviews", h.handleCreate)
	mux.HandleFunc("GET /v1/closy/ootd/reviews", h.handleList)
	mux.HandleFunc("GET /v1/closy/ootd/reviews/{id}", h.handleGet)
}

type closyOOTDCreateRequest struct {
	MediaID   string `json:"media_id"`
	SessionID string `json:"session_id,omitempty"`
	Occasion  string `json:"occasion,omitempty"`
	Note      string `json:"note,omitempty"`
	UserID    string `json:"user_id,omitempty"`
}

type closyOOTDCreateResponse struct {
	Review *store.ClosyOOTDReviewData `json:"review"`
	Result closy.OOTDReviewResult     `json:"result"`
}

func (h *ClosyOOTDHandler) auth(r *http.Request, w http.ResponseWriter) (*http.Request, bool) {
	locale := extractLocale(r)
	auth := resolveAuth(r)
	if !auth.Authenticated {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s","type":"invalid_request_error"}}`, i18n.T(locale, i18n.MsgInvalidAuth)), http.StatusUnauthorized)
		return nil, false
	}
	if !permissions.HasMinRole(auth.Role, permissions.RoleOperator) {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s","type":"invalid_request_error"}}`, i18n.T(locale, i18n.MsgPermissionDenied, "/v1/closy/ootd/reviews")), http.StatusForbidden)
		return nil, false
	}
	return r.WithContext(enrichContext(r.Context(), r, auth)), true
}

func (h *ClosyOOTDHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	r, ok := h.auth(r, w)
	if !ok {
		return
	}
	if h == nil || h.agents == nil || h.mediaAssets == nil || h.reviews == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "closy ootd API is not configured"})
		return
	}
	locale := extractLocale(r)
	var req closyOOTDCreateRequest
	if !bindJSON(w, r, locale, &req) {
		return
	}
	userID := strings.TrimSpace(req.UserID)
	if userID == "" {
		userID = store.UserIDFromContext(r.Context())
	}
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id is required"})
		return
	}
	media, asset, cleanupMedia, err := h.resolveOOTDMedia(r.Context(), req.MediaID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	defer cleanupMedia()
	loop, err := h.agents.Get(r.Context(), closy.AgentKey)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Mochi agent not found"})
		return
	}

	runID := uuid.NewString()
	sessionKey := chatCompletionSessionKey(closy.AgentKey, userID, req.SessionID, runID)
	memoryPrompt := closy.BuildMemoryPromptForUser(r.Context(), h.memory, loop.UUID(), userID)
	prompt := closy.BuildOOTDReviewPrompt(req.Note, req.Occasion, memoryPrompt)
	result, raw, runErr := h.runOOTDReview(r.Context(), loop, sessionKey, runID, userID, prompt, media)
	status := store.ClosyOOTDStatusCompleted
	errMsg := ""
	if runErr != nil {
		status = store.ClosyOOTDStatusFailed
		errMsg = runErr.Error()
	}
	review, err := h.reviews.CreateClosyOOTDReview(r.Context(), store.CreateClosyOOTDReviewParams{
		UserID:           userID,
		AgentID:          loop.UUID(),
		MediaID:          asset.ID,
		SessionID:        sessionKey,
		Occasion:         req.Occasion,
		UserNote:         req.Note,
		OverallJudgement: result.OverallJudgement,
		StyleLabel:       result.StyleLabel,
		Highlight:        result.Highlight,
		MainIssue:        result.MainIssue,
		Suggestion:       result.Suggestion,
		MochiLine:        result.MochiLine,
		SafetyNotes:      result.SafetyNotes,
		RawResponse:      raw,
		Status:           status,
		ErrorMessage:     errMsg,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if status == store.ClosyOOTDStatusCompleted {
		h.persistOOTDMemory(r.Context(), userID, loop.UUID(), sessionKey, review)
	}
	if runErr != nil {
		writeJSON(w, http.StatusBadGateway, closyOOTDCreateResponse{Review: review, Result: result})
		return
	}
	writeJSON(w, http.StatusOK, closyOOTDCreateResponse{Review: review, Result: result})
}

func (h *ClosyOOTDHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	r, ok := h.auth(r, w)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid review id"})
		return
	}
	review, err := h.reviews.GetClosyOOTDReview(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if review == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "review not found"})
		return
	}
	writeJSON(w, http.StatusOK, review)
}

func (h *ClosyOOTDHandler) handleList(w http.ResponseWriter, r *http.Request) {
	r, ok := h.auth(r, w)
	if !ok {
		return
	}
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if userID == "" {
		userID = store.UserIDFromContext(r.Context())
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	var since *time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("since")); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			since = &t
		}
	}
	reviews, err := h.reviews.ListClosyOOTDReviews(r.Context(), store.ListClosyOOTDReviewsParams{
		UserID: userID,
		Limit:  limit,
		Since:  since,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reviews": reviews})
}

func (h *ClosyOOTDHandler) resolveOOTDMedia(ctx context.Context, mediaID string) (bus.MediaFile, *store.MediaAssetData, func(), error) {
	id, err := uuid.Parse(strings.TrimSpace(mediaID))
	if err != nil {
		return bus.MediaFile{}, nil, func() {}, fmt.Errorf("valid media_id is required")
	}
	asset, err := h.mediaAssets.GetMediaAsset(ctx, id)
	if err != nil {
		return bus.MediaFile{}, nil, func() {}, fmt.Errorf("load media %s: %w", mediaID, err)
	}
	if asset == nil {
		return bus.MediaFile{}, nil, func() {}, fmt.Errorf("media not found: %s", mediaID)
	}
	if asset.Status != store.MediaStatusReady {
		return bus.MediaFile{}, nil, func() {}, fmt.Errorf("media is not ready: %s", mediaID)
	}
	if !strings.HasPrefix(strings.ToLower(asset.MimeType), "image/") {
		return bus.MediaFile{}, nil, func() {}, fmt.Errorf("OOTD review requires image/* media, got %q", asset.MimeType)
	}
	localPath, cleanup, err := mediaAssetTempPath(ctx, h.objectStore, asset)
	if err != nil {
		return bus.MediaFile{}, nil, func() {}, err
	}
	return bus.MediaFile{ID: asset.ID.String(), Path: localPath, MimeType: asset.MimeType, Filename: asset.OriginalFilename}, asset, cleanup, nil
}

func (h *ClosyOOTDHandler) runOOTDReview(ctx context.Context, loop agent.Agent, sessionKey, runID, userID, prompt string, media bus.MediaFile) (closy.OOTDReviewResult, string, error) {
	result, err := loop.Run(ctx, agent.RunRequest{
		SessionKey:        sessionKey,
		Message:           prompt,
		Media:             []bus.MediaFile{media},
		ForceInlineImages: true,
		Channel:           "http",
		ChatID:            "ootd",
		PeerKind:          string(sessions.PeerDirect),
		RunID:             runID,
		UserID:            userID,
		Stream:            false,
	})
	if err != nil {
		return closy.OOTDReviewResult{}, "", err
	}
	raw := ""
	if result != nil {
		raw = result.Content
	}
	parsed, parseErr := closy.ParseOOTDReviewResult(raw)
	if parseErr != nil {
		return parsed, raw, parseErr
	}
	return parsed, raw, nil
}

func (h *ClosyOOTDHandler) persistOOTDMemory(ctx context.Context, userID string, agentID uuid.UUID, sessionKey string, review *store.ClosyOOTDReviewData) {
	if h == nil || h.memory == nil || review == nil {
		return
	}
	value := strings.TrimSpace(strings.Join([]string{review.StyleLabel, review.OverallJudgement}, " - "))
	if value == "-" || value == "" {
		value = "OOTD review " + review.ID.String()
	}
	_, _ = h.memory.UpsertClosyStylePreference(ctx, store.UpsertClosyStylePreferenceParams{
		UserID:           userID,
		AgentID:          agentID,
		Category:         store.ClosyPrefCategoryChoice,
		Polarity:         store.ClosyPrefPolarityNeutral,
		Value:            value,
		Evidence:         review.MochiLine,
		SourceSessionKey: sessionKey,
		Confidence:       0.72,
	})
}

func ootdResultFromReview(review *store.ClosyOOTDReviewData) closy.OOTDReviewResult {
	if review == nil {
		return closy.OOTDReviewResult{}
	}
	return closy.OOTDReviewResult{
		OverallJudgement: review.OverallJudgement,
		StyleLabel:       review.StyleLabel,
		Highlight:        review.Highlight,
		MainIssue:        review.MainIssue,
		Suggestion:       review.Suggestion,
		MochiLine:        review.MochiLine,
		SafetyNotes:      review.SafetyNotes,
	}
}

func ootdResultJSON(result closy.OOTDReviewResult) string {
	data, _ := json.Marshal(result)
	return string(data)
}
