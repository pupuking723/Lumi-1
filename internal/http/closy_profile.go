package http

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/closy"
	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

type ClosyProfileHandler struct {
	agents store.AgentStore
	memory store.ClosyMemoryStore
}

func NewClosyProfileHandler(agents store.AgentStore, memory store.ClosyMemoryStore) *ClosyProfileHandler {
	return &ClosyProfileHandler{agents: agents, memory: memory}
}

func (h *ClosyProfileHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/closy/profile", h.handleGet)
	mux.HandleFunc("PUT /v1/closy/profile", h.handlePutProfile)
	mux.HandleFunc("POST /v1/closy/profile/preferences", h.handleUpsertPreference)
}

type closyProfileResponse struct {
	AgentKey    string                           `json:"agent_key"`
	DisplayName string                           `json:"display_name"`
	UserID      string                           `json:"user_id"`
	Profile     *store.ClosyProfileData          `json:"profile,omitempty"`
	Preferences []store.ClosyStylePreferenceData `json:"preferences"`
}

type closyProfileUpdateRequest struct {
	UserID                    string  `json:"user_id,omitempty"`
	StyleSummary              string  `json:"style_summary,omitempty"`
	SelfExpressionSummary     string  `json:"self_expression_summary,omitempty"`
	SocialPresentationSummary string  `json:"social_presentation_summary,omitempty"`
	CurrentStateSummary       string  `json:"current_state_summary,omitempty"`
	Confidence                float64 `json:"confidence,omitempty"`
}

type closyPreferenceUpdateRequest struct {
	UserID           string  `json:"user_id,omitempty"`
	Category         string  `json:"category"`
	Polarity         string  `json:"polarity"`
	Value            string  `json:"value"`
	Evidence         string  `json:"evidence,omitempty"`
	SourceSessionKey string  `json:"source_session_key,omitempty"`
	Confidence       float64 `json:"confidence,omitempty"`
}

func (h *ClosyProfileHandler) auth(r *http.Request, w http.ResponseWriter) (*http.Request, bool) {
	locale := extractLocale(r)
	auth := resolveAuth(r)
	if !auth.Authenticated {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s","type":"invalid_request_error"}}`, i18n.T(locale, i18n.MsgInvalidAuth)), http.StatusUnauthorized)
		return nil, false
	}
	if !permissions.HasMinRole(auth.Role, permissions.RoleOperator) {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s","type":"invalid_request_error"}}`, i18n.T(locale, i18n.MsgPermissionDenied, "/v1/closy/profile")), http.StatusForbidden)
		return nil, false
	}
	return r.WithContext(enrichContext(r.Context(), r, auth)), true
}

func (h *ClosyProfileHandler) resolveScope(r *http.Request, bodyUserID string) (*store.AgentData, string, error) {
	if h == nil || h.agents == nil || h.memory == nil {
		return nil, "", fmt.Errorf("closy profile API is not configured")
	}
	ag, err := h.agents.GetByKey(r.Context(), closy.AgentKey)
	if err != nil || ag == nil {
		return nil, "", fmt.Errorf("Mochi agent not found")
	}
	userID := strings.TrimSpace(bodyUserID)
	if userID == "" {
		userID = strings.TrimSpace(r.URL.Query().Get("user_id"))
	}
	if userID == "" {
		userID = store.UserIDFromContext(r.Context())
	}
	if userID == "" {
		return nil, "", fmt.Errorf("user_id is required")
	}
	return ag, userID, nil
}

func (h *ClosyProfileHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	r, ok := h.auth(r, w)
	if !ok {
		return
	}
	ag, userID, err := h.resolveScope(r, "")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	profile, err := h.memory.GetClosyProfile(r.Context(), ag.ID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	prefs, err := h.memory.ListClosyStylePreferences(r.Context(), ag.ID, userID, 100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, closyProfileResponse{
		AgentKey:    ag.AgentKey,
		DisplayName: ag.DisplayName,
		UserID:      userID,
		Profile:     profile,
		Preferences: prefs,
	})
}

func (h *ClosyProfileHandler) handlePutProfile(w http.ResponseWriter, r *http.Request) {
	r, ok := h.auth(r, w)
	if !ok {
		return
	}
	locale := extractLocale(r)
	var req closyProfileUpdateRequest
	if !bindJSON(w, r, locale, &req) {
		return
	}
	ag, userID, err := h.resolveScope(r, req.UserID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	profile, err := h.memory.UpsertClosyProfile(r.Context(), store.UpsertClosyProfileParams{
		UserID:                    userID,
		AgentID:                   ag.ID,
		StyleSummary:              req.StyleSummary,
		SelfExpressionSummary:     req.SelfExpressionSummary,
		SocialPresentationSummary: req.SocialPresentationSummary,
		CurrentStateSummary:       req.CurrentStateSummary,
		Confidence:                req.Confidence,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (h *ClosyProfileHandler) handleUpsertPreference(w http.ResponseWriter, r *http.Request) {
	r, ok := h.auth(r, w)
	if !ok {
		return
	}
	locale := extractLocale(r)
	var req closyPreferenceUpdateRequest
	if !bindJSON(w, r, locale, &req) {
		return
	}
	ag, userID, err := h.resolveScope(r, req.UserID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.Confidence == 0 {
		req.Confidence = 0.9
	}
	pref, err := h.memory.UpsertClosyStylePreference(r.Context(), store.UpsertClosyStylePreferenceParams{
		UserID:           userID,
		AgentID:          ag.ID,
		Category:         req.Category,
		Polarity:         req.Polarity,
		Value:            req.Value,
		Evidence:         req.Evidence,
		SourceSessionKey: req.SourceSessionKey,
		Confidence:       req.Confidence,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, pref)
}
