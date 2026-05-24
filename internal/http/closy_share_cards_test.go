package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

type fakeShareCardStore struct {
	created *store.ClosyShareCardData
	byID    map[uuid.UUID]*store.ClosyShareCardData
	bySlug  map[string]*store.ClosyShareCardData
}

func (f *fakeShareCardStore) CreateClosyShareCard(ctx context.Context, p store.CreateClosyShareCardParams) (*store.ClosyShareCardData, error) {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		tid = store.MasterTenantID
	}
	now := time.Now().UTC()
	card := &store.ClosyShareCardData{
		BaseModel:    store.BaseModel{ID: p.ID, CreatedAt: now, UpdatedAt: now},
		TenantID:     tid,
		UserID:       p.UserID,
		AgentID:      p.AgentID,
		OOTDReviewID: p.OOTDReviewID,
		MediaID:      p.MediaID,
		Slug:         p.Slug,
		ShareURL:     p.ShareURL,
		CTAText:      p.CTAText,
		CTAURL:       p.CTAURL,
		Payload:      p.Payload,
		Status:       p.Status,
		ExpiresAt:    p.ExpiresAt,
	}
	f.created = card
	if f.byID == nil {
		f.byID = map[uuid.UUID]*store.ClosyShareCardData{}
	}
	if f.bySlug == nil {
		f.bySlug = map[string]*store.ClosyShareCardData{}
	}
	f.byID[card.ID] = card
	f.bySlug[card.Slug] = card
	return card, nil
}

func (f *fakeShareCardStore) GetClosyShareCard(_ context.Context, id uuid.UUID) (*store.ClosyShareCardData, error) {
	return f.byID[id], nil
}

func (f *fakeShareCardStore) GetClosyShareCardBySlug(_ context.Context, slug string) (*store.ClosyShareCardData, error) {
	return f.bySlug[slug], nil
}

func (f *fakeShareCardStore) ListClosyShareCards(_ context.Context, _ store.ListClosyShareCardsParams) ([]store.ClosyShareCardData, error) {
	if f.created == nil {
		return nil, nil
	}
	return []store.ClosyShareCardData{*f.created}, nil
}

func (f *fakeShareCardStore) IncrementClosyShareCardViews(_ context.Context, id uuid.UUID) error {
	if card := f.byID[id]; card != nil {
		card.ViewCount++
	}
	return nil
}

func TestClosyShareCardsHandlerCreateAndPublicSlug(t *testing.T) {
	InitGatewayToken("test-token")
	t.Cleanup(func() { InitGatewayToken("") })

	reviewID := uuid.New()
	mediaID := uuid.New()
	agentID := uuid.New()
	reviews := &fakeOOTDStore{byID: map[uuid.UUID]*store.ClosyOOTDReviewData{
		reviewID: {
			BaseModel:        store.BaseModel{ID: reviewID},
			UserID:           "user-a",
			AgentID:          agentID,
			MediaID:          mediaID,
			OverallJudgement: "能出门",
			StyleLabel:       "clean casual",
			Highlight:        "颜色干净",
			MainIssue:        "鞋子略断层",
			Suggestion:       "换浅色鞋",
			MochiLine:        "人会问链接。",
			Status:           store.ClosyOOTDStatusCompleted,
		},
	}}
	cards := &fakeShareCardStore{}
	h := NewClosyShareCardsHandler(reviews, cards)

	body, _ := json.Marshal(map[string]any{
		"ootd_review_id": reviewID.String(),
		"cta_text":       "让 Mochi 也看看你",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/closy/share-cards", bytes.NewReader(body))
	req.Host = "app.test"
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-GoClaw-User-Id", "user-a")
	rr := httptest.NewRecorder()

	h.handleCreate(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if cards.created == nil || cards.created.OOTDReviewID != reviewID || !strings.Contains(cards.created.ShareURL, "/s/closy/") {
		t.Fatalf("created = %#v", cards.created)
	}
	if !strings.Contains(string(cards.created.Payload), `"aspect_ratio":"9:16"`) || !strings.Contains(string(cards.created.Payload), "能出门") {
		t.Fatalf("payload = %s", string(cards.created.Payload))
	}

	publicReq := httptest.NewRequest(http.MethodGet, "/s/closy/"+cards.created.Slug, nil)
	publicReq.SetPathValue("slug", cards.created.Slug)
	publicRR := httptest.NewRecorder()
	h.handlePublicSlug(publicRR, publicReq)
	if publicRR.Code != http.StatusOK {
		t.Fatalf("public status=%d body=%s", publicRR.Code, publicRR.Body.String())
	}
	if cards.created.ViewCount != 1 || !strings.Contains(publicRR.Body.String(), `"view_count":1`) {
		t.Fatalf("view count card=%d body=%s", cards.created.ViewCount, publicRR.Body.String())
	}
}
