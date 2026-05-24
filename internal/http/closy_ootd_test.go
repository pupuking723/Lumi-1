package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/agent"
	"github.com/nextlevelbuilder/goclaw/internal/closy"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

type fakeOOTDAgent struct {
	id      uuid.UUID
	lastReq agent.RunRequest
	content string
}

func (f *fakeOOTDAgent) ID() string                   { return closy.AgentKey }
func (f *fakeOOTDAgent) UUID() uuid.UUID              { return f.id }
func (f *fakeOOTDAgent) OtherConfig() json.RawMessage { return nil }
func (f *fakeOOTDAgent) IsRunning() bool              { return false }
func (f *fakeOOTDAgent) Model() string                { return "test-model" }
func (f *fakeOOTDAgent) ProviderName() string         { return "test" }
func (f *fakeOOTDAgent) Provider() providers.Provider { return nil }
func (f *fakeOOTDAgent) Run(_ context.Context, req agent.RunRequest) (*agent.RunResult, error) {
	f.lastReq = req
	return &agent.RunResult{Content: f.content}, nil
}

type fakeOOTDStore struct {
	created *store.ClosyOOTDReviewData
	byID    map[uuid.UUID]*store.ClosyOOTDReviewData
}

func (f *fakeOOTDStore) CreateClosyOOTDReview(ctx context.Context, p store.CreateClosyOOTDReviewParams) (*store.ClosyOOTDReviewData, error) {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		tid = store.MasterTenantID
	}
	now := time.Now().UTC()
	r := &store.ClosyOOTDReviewData{
		BaseModel:        store.BaseModel{ID: p.ID, CreatedAt: now, UpdatedAt: now},
		TenantID:         tid,
		UserID:           p.UserID,
		AgentID:          p.AgentID,
		MediaID:          p.MediaID,
		SessionID:        p.SessionID,
		Occasion:         p.Occasion,
		UserNote:         p.UserNote,
		OverallJudgement: p.OverallJudgement,
		StyleLabel:       p.StyleLabel,
		Highlight:        p.Highlight,
		MainIssue:        p.MainIssue,
		Suggestion:       p.Suggestion,
		MochiLine:        p.MochiLine,
		SafetyNotes:      p.SafetyNotes,
		RawResponse:      p.RawResponse,
		Status:           p.Status,
		ErrorMessage:     p.ErrorMessage,
	}
	f.created = r
	if f.byID == nil {
		f.byID = map[uuid.UUID]*store.ClosyOOTDReviewData{}
	}
	f.byID[r.ID] = r
	return r, nil
}

func (f *fakeOOTDStore) GetClosyOOTDReview(_ context.Context, id uuid.UUID) (*store.ClosyOOTDReviewData, error) {
	return f.byID[id], nil
}

func (f *fakeOOTDStore) ListClosyOOTDReviews(_ context.Context, _ store.ListClosyOOTDReviewsParams) ([]store.ClosyOOTDReviewData, error) {
	if f.created == nil {
		return nil, nil
	}
	return []store.ClosyOOTDReviewData{*f.created}, nil
}

func TestClosyOOTDHandlerCreateRunsAgentAndStoresReview(t *testing.T) {
	InitGatewayToken("test-token")
	t.Cleanup(func() { InitGatewayToken("") })

	tmp, err := os.CreateTemp(t.TempDir(), "ootd-*.jpg")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	if _, err := tmp.Write([]byte("jpg")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	mediaID := uuid.New()
	assets := &fakeMediaAssetStore{byID: map[uuid.UUID]*store.MediaAssetData{
		mediaID: {
			ID:               mediaID,
			TenantID:         store.MasterTenantID,
			UserID:           "user-a",
			OriginalFilename: "fit.jpg",
			MimeType:         "image/jpeg",
			Size:             3,
			StorageBackend:   store.MediaStorageLocal,
			StorageKey:       tmp.Name(),
			Status:           store.MediaStatusReady,
		},
	}}
	ag := &fakeOOTDAgent{id: uuid.New(), content: `{"overall_judgement":"能出门","style_label":"clean casual","highlight":"颜色干净","main_issue":"鞋子略断层","suggestion":"换浅色鞋","mochi_line":"人会问链接。"}`}
	router := agent.NewRouter()
	router.SetResolver(func(context.Context, string) (agent.Agent, error) { return ag, nil })
	reviews := &fakeOOTDStore{}
	h := NewClosyOOTDHandler(router, assets, reviews, nil)

	body, _ := json.Marshal(map[string]any{
		"media_id":   mediaID.String(),
		"session_id": "fit-1",
		"occasion":   "约会",
		"note":       "想低调用力一点",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/closy/ootd/reviews", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-GoClaw-User-Id", "user-a")
	rr := httptest.NewRecorder()

	h.handleCreate(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if reviews.created == nil || reviews.created.MediaID != mediaID || reviews.created.OverallJudgement != "能出门" {
		t.Fatalf("created = %#v", reviews.created)
	}
	if !ag.lastReq.ForceInlineImages || len(ag.lastReq.Media) != 1 || !strings.Contains(ag.lastReq.Message, "Return only compact JSON") {
		t.Fatalf("run request = %#v", ag.lastReq)
	}
	if !strings.HasPrefix(reviews.created.SessionID, "agent:closy:cchat:direct:user-a-fit-1") {
		t.Fatalf("session id = %q", reviews.created.SessionID)
	}
}
