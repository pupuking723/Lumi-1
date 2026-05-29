package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/closy"
	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

type ClosyShareCardsHandler struct {
	reviews store.ClosyOOTDStore
	cards   store.ClosyShareCardStore
}

func NewClosyShareCardsHandler(reviews store.ClosyOOTDStore, cards store.ClosyShareCardStore) *ClosyShareCardsHandler {
	return &ClosyShareCardsHandler{reviews: reviews, cards: cards}
}

func (h *ClosyShareCardsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/closy/share-cards", h.handleCreate)
	mux.HandleFunc("GET /v1/closy/share-cards", h.handleList)
	mux.HandleFunc("GET /v1/closy/share-cards/{id}", h.handleGet)
	mux.HandleFunc("POST /v1/closy/ootd/reports/{id}/share-card", h.handleCreateReportShareCard)
	mux.HandleFunc("GET /s/closy/{slug}", h.handlePublicSlug)
}

type closyShareCardCreateRequest struct {
	OOTDReviewID string `json:"ootd_review_id"`
	UserID       string `json:"user_id,omitempty"`
	CTAText      string `json:"cta_text,omitempty"`
	CTAURL       string `json:"cta_url,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
}

type closyShareCardResponse struct {
	Card    *store.ClosyShareCardData `json:"card"`
	Payload closy.ShareCardPayload    `json:"payload"`
}

type ootdReportShareCardResponse struct {
	ID         string `json:"id"`
	ReportID   string `json:"reportId"`
	ShortURL   string `json:"shortUrl"`
	QRImageURL string `json:"qrImageUrl"`
	CreatedAt  string `json:"createdAt"`
}

func (h *ClosyShareCardsHandler) auth(r *http.Request, w http.ResponseWriter) (*http.Request, bool) {
	locale := extractLocale(r)
	auth := resolveAuth(r)
	if !auth.Authenticated {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s","type":"invalid_request_error"}}`, i18n.T(locale, i18n.MsgInvalidAuth)), http.StatusUnauthorized)
		return nil, false
	}
	if !permissions.HasMinRole(auth.Role, permissions.RoleOperator) {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s","type":"invalid_request_error"}}`, i18n.T(locale, i18n.MsgPermissionDenied, "/v1/closy/share-cards")), http.StatusForbidden)
		return nil, false
	}
	return r.WithContext(enrichContext(r.Context(), r, auth)), true
}

func (h *ClosyShareCardsHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	r, ok := h.auth(r, w)
	if !ok {
		return
	}
	if h == nil || h.reviews == nil || h.cards == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "closy share cards API is not configured"})
		return
	}
	locale := extractLocale(r)
	var req closyShareCardCreateRequest
	if !bindJSON(w, r, locale, &req) {
		return
	}
	reviewID, err := uuid.Parse(strings.TrimSpace(req.OOTDReviewID))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "valid ootd_review_id is required"})
		return
	}
	review, err := h.reviews.GetClosyOOTDReview(r.Context(), reviewID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if review == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "OOTD review not found"})
		return
	}
	userID := strings.TrimSpace(req.UserID)
	if userID == "" {
		userID = store.UserIDFromContext(r.Context())
	}
	if userID == "" {
		userID = review.UserID
	}
	if userID != review.UserID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "user_id does not match OOTD review owner"})
		return
	}
	expiresAt, err := parseOptionalRFC3339(req.ExpiresAt)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expires_at must be RFC3339"})
		return
	}
	slug := closy.NewShareSlug()
	shareURL := absoluteRequestURL(r, "/s/closy/"+slug, nil)
	ctaURL := strings.TrimSpace(req.CTAURL)
	if ctaURL == "" {
		ctaURL = absoluteRequestURL(r, "/", url.Values{
			"from":      []string{"mochi_share"},
			"review_id": []string{review.ID.String()},
		})
	}
	payload := closy.BuildShareCardPayload(review, shareURL, req.CTAText, ctaURL, time.Now().UTC())
	payloadJSON, _ := json.Marshal(payload)
	card, err := h.cards.CreateClosyShareCard(r.Context(), store.CreateClosyShareCardParams{
		UserID:       review.UserID,
		AgentID:      review.AgentID,
		OOTDReviewID: review.ID,
		MediaID:      review.MediaID,
		Slug:         slug,
		ShareURL:     shareURL,
		CTAText:      payload.CTA.Text,
		CTAURL:       payload.CTA.URL,
		Payload:      payloadJSON,
		Status:       store.ClosyShareCardStatusActive,
		ExpiresAt:    expiresAt,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, closyShareCardResponse{Card: card, Payload: payload})
}

func (h *ClosyShareCardsHandler) handleCreateReportShareCard(w http.ResponseWriter, r *http.Request) {
	r, ok := h.auth(r, w)
	if !ok {
		return
	}
	if h == nil || h.reviews == nil || h.cards == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "closy share cards API is not configured"})
		return
	}
	reportID, err := uuid.Parse(strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid report id"})
		return
	}
	review, err := h.reviews.GetClosyOOTDReview(r.Context(), reportID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if review == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "OOTD report not found"})
		return
	}
	if _, err := ootdReportFromReview(review); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "OOTD report not found"})
		return
	}
	userID := store.UserIDFromContext(r.Context())
	if userID != "" && review.UserID != "" && userID != review.UserID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "user_id does not match OOTD report owner"})
		return
	}
	slug := closy.NewShareSlug()
	shortURL := absoluteRequestURL(r, "/s/closy/"+slug, nil)
	payload := closy.BuildShareCardPayload(review, shortURL, "", absoluteRequestURL(r, "/", url.Values{
		"from":      []string{"mochi_share"},
		"report_id": []string{review.ID.String()},
	}), time.Now().UTC())
	payloadJSON, _ := json.Marshal(payload)
	card, err := h.cards.CreateClosyShareCard(r.Context(), store.CreateClosyShareCardParams{
		UserID:       review.UserID,
		AgentID:      review.AgentID,
		OOTDReviewID: review.ID,
		MediaID:      review.MediaID,
		Slug:         slug,
		ShareURL:     shortURL,
		CTAText:      payload.CTA.Text,
		CTAURL:       payload.CTA.URL,
		Payload:      payloadJSON,
		Status:       store.ClosyShareCardStatusActive,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ootdReportShareCardResponse{
		ID:         card.ID.String(),
		ReportID:   review.ID.String(),
		ShortURL:   card.ShareURL,
		QRImageURL: qrImageURL(card.ShareURL),
		CreatedAt:  card.CreatedAt.UTC().Format(time.RFC3339),
	})
}

func (h *ClosyShareCardsHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	r, ok := h.auth(r, w)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid share card id"})
		return
	}
	card, err := h.cards.GetClosyShareCard(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if card == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "share card not found"})
		return
	}
	writeJSON(w, http.StatusOK, card)
}

func (h *ClosyShareCardsHandler) handleList(w http.ResponseWriter, r *http.Request) {
	r, ok := h.auth(r, w)
	if !ok {
		return
	}
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if userID == "" {
		userID = store.UserIDFromContext(r.Context())
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	var reviewID uuid.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("ootd_review_id")); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			reviewID = id
		}
	}
	cards, err := h.cards.ListClosyShareCards(r.Context(), store.ListClosyShareCardsParams{
		UserID:       userID,
		OOTDReviewID: reviewID,
		Limit:        limit,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"share_cards": cards})
}

func (h *ClosyShareCardsHandler) handlePublicSlug(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.cards == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "share card not found"})
		return
	}
	card, err := h.cards.GetClosyShareCardBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if card == nil || card.Status != store.ClosyShareCardStatusActive || shareCardExpired(card.ExpiresAt) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "share card not found"})
		return
	}
	nextViewCount := card.ViewCount + 1
	_ = h.cards.IncrementClosyShareCardViews(r.Context(), card.ID)
	if !shareCardRequestWantsJSON(r) {
		http.Redirect(w, r, absoluteRequestURL(r, "/", url.Values{
			"from":  []string{"mochi_share"},
			"share": []string{card.Slug},
		}), http.StatusFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"slug":       card.Slug,
		"share_url":  card.ShareURL,
		"cta_text":   card.CTAText,
		"cta_url":    card.CTAURL,
		"payload":    json.RawMessage(card.Payload),
		"view_count": nextViewCount,
	})
}

func parseOptionalRFC3339(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func shareCardExpired(expiresAt *time.Time) bool {
	return expiresAt != nil && time.Now().UTC().After(expiresAt.UTC())
}

func shareCardRequestWantsJSON(r *http.Request) bool {
	accept := strings.ToLower(r.Header.Get("Accept"))
	return strings.Contains(accept, "application/json") || strings.Contains(accept, "text/json")
}

func absoluteRequestURL(r *http.Request, path string, query url.Values) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwarded != "" {
		host = forwarded
	}
	u := url.URL{Scheme: scheme, Host: host, Path: path}
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	return u.String()
}

func qrImageURL(value string) string {
	q := url.Values{}
	q.Set("size", "160x160")
	q.Set("data", value)
	return "https://api.qrserver.com/v1/create-qr-code/?" + q.Encode()
}
