package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/agent"
	"github.com/nextlevelbuilder/goclaw/internal/closy"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/skills"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

type fakeOOTDAgent struct {
	id       uuid.UUID
	lastReq  agent.RunRequest
	requests []agent.RunRequest
	content  string
	contents []string
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
	f.requests = append(f.requests, req)
	if len(f.contents) > 0 {
		out := f.contents[0]
		f.contents = f.contents[1:]
		return &agent.RunResult{Content: out}, nil
	}
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
		ReportJSON:       p.ReportJSON,
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

func (f *fakeOOTDStore) FindLatestClosyOOTDReport(ctx context.Context, p store.FindLatestClosyOOTDReportParams) (*store.ClosyOOTDReviewData, error) {
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		tid = store.MasterTenantID
	}
	var latest *store.ClosyOOTDReviewData
	seen := map[uuid.UUID]bool{}
	consider := func(r *store.ClosyOOTDReviewData) {
		if r == nil || seen[r.ID] {
			return
		}
		seen[r.ID] = true
		if r.TenantID != uuid.Nil && r.TenantID != tid {
			return
		}
		if r.UserID != p.UserID || r.MediaID != p.MediaID || r.Status != store.ClosyOOTDStatusCompleted || len(r.ReportJSON) == 0 {
			return
		}
		if latest == nil || r.CreatedAt.After(latest.CreatedAt) {
			latest = r
		}
	}
	consider(f.created)
	for _, r := range f.byID {
		consider(r)
	}
	return latest, nil
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
	h := NewClosyOOTDHandler(router, assets, reviews, nil, nil)

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

func TestClosyOOTDHandlerCreateReportLoadsSkillAndStoresReportJSON(t *testing.T) {
	InitGatewayToken("test-token")
	t.Cleanup(func() { InitGatewayToken("") })

	mediaID, assets := testOOTDMediaStore(t)
	ag := &fakeOOTDAgent{id: uuid.New(), content: testOOTDReportJSON()}
	router := agent.NewRouter()
	router.SetResolver(func(context.Context, string) (agent.Agent, error) { return ag, nil })
	reviews := &fakeOOTDStore{}
	h := NewClosyOOTDHandler(router, assets, reviews, nil, testOOTDSkillsLoader(t))

	body, _ := json.Marshal(map[string]any{
		"media_id":   mediaID.String(),
		"session_id": "fit-report-1",
		"scene":      "daily",
		"note":       "想像 Chance AI 那样直接一点",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/closy/ootd/reports", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-GoClaw-User-Id", "user-a")
	rr := httptest.NewRecorder()

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if reviews.created == nil || len(reviews.created.ReportJSON) == 0 {
		t.Fatalf("created = %#v", reviews.created)
	}
	if !strings.Contains(ag.lastReq.Message, "TEST OOTD SKILL") || !strings.Contains(ag.lastReq.Message, "todayJudgment") {
		t.Fatalf("prompt = %s", ag.lastReq.Message)
	}
	if ag.lastReq.ProviderOptions == nil || ag.lastReq.ProviderOptions[providers.OptResponseMimeType] != "application/json" || ag.lastReq.ProviderOptions[providers.OptResponseSchema] == nil || ag.lastReq.ProviderOptions[providers.OptResponseJSONSchema] == nil {
		t.Fatalf("missing structured output options: %#v", ag.lastReq.ProviderOptions)
	}
	if strings.Contains(ag.lastReq.SessionKey, "fit-report-1") {
		t.Fatalf("report session should not reuse chat/request session: %q", ag.lastReq.SessionKey)
	}
	if !strings.Contains(ag.lastReq.SessionKey, "ootd-report-"+mediaID.String()) {
		t.Fatalf("report session should be scoped to media id, got %q", ag.lastReq.SessionKey)
	}
	if reviews.created.SessionID != ag.lastReq.SessionKey {
		t.Fatalf("stored session id = %q, want run session %q", reviews.created.SessionID, ag.lastReq.SessionKey)
	}
	var resp struct {
		ID            string `json:"id"`
		MediaID       string `json:"mediaId"`
		ImageURL      string `json:"imageUrl"`
		TodayJudgment struct {
			Title string  `json:"title"`
			Score float64 `json:"score"`
			Label string  `json:"label"`
		} `json:"todayJudgment"`
		Highlights []string `json:"highlights"`
		ShareCard  struct {
			Advice []string `json:"advice"`
			CTA    string   `json:"cta"`
		} `json:"shareCard"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	if resp.MediaID != mediaID.String() || resp.TodayJudgment.Title != "城市休闲极简主义" || resp.TodayJudgment.Score != 5.5 || len(resp.ShareCard.Advice) != 2 {
		t.Fatalf("resp = %#v", resp)
	}
	if resp.ImageURL == "" {
		t.Fatalf("missing imageUrl: %#v", resp)
	}
}

func TestClosyOOTDHandlerCreateReportReusesExistingCompletedReport(t *testing.T) {
	InitGatewayToken("test-token")
	t.Cleanup(func() { InitGatewayToken("") })

	mediaID, assets := testOOTDMediaStore(t)
	reportID := uuid.New()
	createdAt := time.Now().UTC().Add(-time.Hour)
	reviews := &fakeOOTDStore{byID: map[uuid.UUID]*store.ClosyOOTDReviewData{
		reportID: {
			BaseModel:  store.BaseModel{ID: reportID, CreatedAt: createdAt, UpdatedAt: createdAt},
			TenantID:   store.MasterTenantID,
			UserID:     "user-a",
			MediaID:    mediaID,
			Status:     store.ClosyOOTDStatusCompleted,
			ReportJSON: json.RawMessage(testOOTDReportJSON()),
		},
	}}
	ag := &fakeOOTDAgent{id: uuid.New(), content: testOOTDReportJSON()}
	router := agent.NewRouter()
	router.SetResolver(func(context.Context, string) (agent.Agent, error) { return ag, nil })
	h := NewClosyOOTDHandler(router, assets, reviews, nil, testOOTDSkillsLoader(t))

	body, _ := json.Marshal(map[string]any{"media_id": mediaID.String(), "note": "generate again"})
	req := httptest.NewRequest(http.MethodPost, "/v1/closy/ootd/reports", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-GoClaw-User-Id", "user-a")
	rr := httptest.NewRecorder()

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(ag.requests) != 0 {
		t.Fatalf("expected cached report without model call, got %d requests", len(ag.requests))
	}
	if reviews.created != nil {
		t.Fatalf("expected no new review, created = %#v", reviews.created)
	}
	var resp struct {
		ID            string `json:"id"`
		MediaID       string `json:"mediaId"`
		TodayJudgment struct {
			Title string  `json:"title"`
			Score float64 `json:"score"`
		} `json:"todayJudgment"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	if resp.ID != reportID.String() || resp.MediaID != mediaID.String() || resp.TodayJudgment.Title != "城市休闲极简主义" || resp.TodayJudgment.Score != 5.5 {
		t.Fatalf("resp = %#v", resp)
	}
}

func TestClosyOOTDHandlerCreateReportDoesNotReuseFailedReport(t *testing.T) {
	InitGatewayToken("test-token")
	t.Cleanup(func() { InitGatewayToken("") })

	mediaID, assets := testOOTDMediaStore(t)
	failedID := uuid.New()
	createdAt := time.Now().UTC().Add(-time.Hour)
	reviews := &fakeOOTDStore{byID: map[uuid.UUID]*store.ClosyOOTDReviewData{
		failedID: {
			BaseModel:  store.BaseModel{ID: failedID, CreatedAt: createdAt, UpdatedAt: createdAt},
			TenantID:   store.MasterTenantID,
			UserID:     "user-a",
			MediaID:    mediaID,
			Status:     store.ClosyOOTDStatusFailed,
			ReportJSON: json.RawMessage(testOOTDReportJSON()),
		},
	}}
	ag := &fakeOOTDAgent{id: uuid.New(), content: testOOTDReportJSON()}
	router := agent.NewRouter()
	router.SetResolver(func(context.Context, string) (agent.Agent, error) { return ag, nil })
	h := NewClosyOOTDHandler(router, assets, reviews, nil, testOOTDSkillsLoader(t))

	body, _ := json.Marshal(map[string]any{"media_id": mediaID.String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/closy/ootd/reports", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-GoClaw-User-Id", "user-a")
	rr := httptest.NewRecorder()

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(ag.requests) != 1 {
		t.Fatalf("expected failed report to regenerate, got %d requests", len(ag.requests))
	}
	if reviews.created == nil || reviews.created.ID == failedID {
		t.Fatalf("created = %#v", reviews.created)
	}
}

func TestClosyOOTDHandlerCreateReportDoesNotReuseInvalidStoredReportJSON(t *testing.T) {
	InitGatewayToken("test-token")
	t.Cleanup(func() { InitGatewayToken("") })

	mediaID, assets := testOOTDMediaStore(t)
	invalidID := uuid.New()
	createdAt := time.Now().UTC().Add(-time.Hour)
	reviews := &fakeOOTDStore{byID: map[uuid.UUID]*store.ClosyOOTDReviewData{
		invalidID: {
			BaseModel:  store.BaseModel{ID: invalidID, CreatedAt: createdAt, UpdatedAt: createdAt},
			TenantID:   store.MasterTenantID,
			UserID:     "user-a",
			MediaID:    mediaID,
			Status:     store.ClosyOOTDStatusCompleted,
			ReportJSON: json.RawMessage(`{"todayJudgment":{"title":"missing score and label"}}`),
		},
	}}
	ag := &fakeOOTDAgent{id: uuid.New(), content: testOOTDReportJSON()}
	router := agent.NewRouter()
	router.SetResolver(func(context.Context, string) (agent.Agent, error) { return ag, nil })
	h := NewClosyOOTDHandler(router, assets, reviews, nil, testOOTDSkillsLoader(t))

	body, _ := json.Marshal(map[string]any{"media_id": mediaID.String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/closy/ootd/reports", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-GoClaw-User-Id", "user-a")
	rr := httptest.NewRecorder()

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(ag.requests) != 1 {
		t.Fatalf("expected invalid cached report to regenerate, got %d requests", len(ag.requests))
	}
	if reviews.created == nil || reviews.created.ID == invalidID {
		t.Fatalf("created = %#v", reviews.created)
	}
}

func TestClosyOOTDHandlerCreateReportRepairsInvalidModelOutput(t *testing.T) {
	InitGatewayToken("test-token")
	t.Cleanup(func() { InitGatewayToken("") })

	mediaID, assets := testOOTDMediaStore(t)
	ag := &fakeOOTDAgent{id: uuid.New(), contents: []string{`{"todayJudgment":{"title":"x","label":"ok"}}`, testOOTDReportJSON()}}
	router := agent.NewRouter()
	router.SetResolver(func(context.Context, string) (agent.Agent, error) { return ag, nil })
	h := NewClosyOOTDHandler(router, assets, &fakeOOTDStore{}, nil, testOOTDSkillsLoader(t))

	body, _ := json.Marshal(map[string]any{"media_id": mediaID.String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/closy/ootd/reports", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-GoClaw-User-Id", "user-a")
	rr := httptest.NewRecorder()

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(ag.requests) != 2 || !strings.Contains(ag.requests[1].Message, "Repair the previous OOTD JSON") {
		t.Fatalf("requests = %#v", ag.requests)
	}
}

func TestClosyOOTDHandlerCreateReportRejectsUnsafeOutput(t *testing.T) {
	InitGatewayToken("test-token")
	t.Cleanup(func() { InitGatewayToken("") })

	mediaID, assets := testOOTDMediaStore(t)
	ag := &fakeOOTDAgent{id: uuid.New(), content: strings.Replace(testOOTDReportJSON(), "整体有方向，但鞋包和上身还差一个清晰态度。", "这套显胖，脸大所以不适合。", 1)}
	router := agent.NewRouter()
	router.SetResolver(func(context.Context, string) (agent.Agent, error) { return ag, nil })
	h := NewClosyOOTDHandler(router, assets, &fakeOOTDStore{}, nil, testOOTDSkillsLoader(t))

	body, _ := json.Marshal(map[string]any{"media_id": mediaID.String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/closy/ootd/reports", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-GoClaw-User-Id", "user-a")
	rr := httptest.NewRecorder()

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rr.Body.String(), "unsafe_analysis_output") {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestClosyOOTDHandlerCreateReportFallsBackAfterInvalidRepair(t *testing.T) {
	InitGatewayToken("test-token")
	t.Cleanup(func() { InitGatewayToken("") })

	mediaID, assets := testOOTDMediaStore(t)
	invalid := `{"todayJudgment":{"title":"x","score":99,"label":"ok","summary":"ok"}}`
	ag := &fakeOOTDAgent{id: uuid.New(), contents: []string{invalid, invalid}}
	router := agent.NewRouter()
	router.SetResolver(func(context.Context, string) (agent.Agent, error) { return ag, nil })
	reviews := &fakeOOTDStore{}
	h := NewClosyOOTDHandler(router, assets, reviews, nil, testOOTDSkillsLoader(t))

	body, _ := json.Marshal(map[string]any{"media_id": mediaID.String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/closy/ootd/reports", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-GoClaw-User-Id", "user-a")
	rr := httptest.NewRecorder()

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(ag.requests) != 2 {
		t.Fatalf("requests = %#v", ag.requests)
	}
	if reviews.created == nil || len(reviews.created.ReportJSON) == 0 {
		t.Fatalf("created = %#v", reviews.created)
	}
	if !strings.Contains(rr.Body.String(), `"title":"Needs another pass"`) || !strings.Contains(rr.Body.String(), `"label":"Retry"`) {
		t.Fatalf("body=%s", rr.Body.String())
	}
}

func TestClosyOOTDHandlerGetReportReturnsStoredReport(t *testing.T) {
	InitGatewayToken("test-token")
	t.Cleanup(func() { InitGatewayToken("") })

	reportJSON := json.RawMessage(testOOTDReportJSON())
	reportID := uuid.New()
	mediaID := uuid.New()
	reviews := &fakeOOTDStore{byID: map[uuid.UUID]*store.ClosyOOTDReviewData{
		reportID: {
			BaseModel:  store.BaseModel{ID: reportID, CreatedAt: time.Now().UTC()},
			UserID:     "user-a",
			MediaID:    mediaID,
			Status:     store.ClosyOOTDStatusCompleted,
			ReportJSON: reportJSON,
		},
	}}
	h := NewClosyOOTDHandler(agent.NewRouter(), nil, reviews, nil, testOOTDSkillsLoader(t))

	req := httptest.NewRequest(http.MethodGet, "/v1/closy/ootd/reports/"+reportID.String(), nil)
	req.SetPathValue("id", reportID.String())
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-GoClaw-User-Id", "user-a")
	rr := httptest.NewRecorder()

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"mediaId":"`+mediaID.String()+`"`) || !strings.Contains(rr.Body.String(), `"todayJudgment"`) {
		t.Fatalf("body=%s", rr.Body.String())
	}
}

func TestClosyOOTDHandlerCreateReportRejectsMediaOwnerMismatch(t *testing.T) {
	InitGatewayToken("test-token")
	t.Cleanup(func() { InitGatewayToken("") })

	mediaID, assets := testOOTDMediaStore(t)
	assets.byID[mediaID].UserID = "other-user"
	h := NewClosyOOTDHandler(agent.NewRouter(), assets, &fakeOOTDStore{}, nil, testOOTDSkillsLoader(t))

	body, _ := json.Marshal(map[string]any{"media_id": mediaID.String(), "user_id": "user-a"})
	req := httptest.NewRequest(http.MethodPost, "/v1/closy/ootd/reports", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-GoClaw-User-Id", "user-a")
	rr := httptest.NewRecorder()

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func testOOTDMediaStore(t *testing.T) (uuid.UUID, *fakeMediaAssetStore) {
	t.Helper()
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
	return mediaID, &fakeMediaAssetStore{byID: map[uuid.UUID]*store.MediaAssetData{
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
}

func testOOTDSkillsLoader(t *testing.T) *skills.Loader {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "skills", "mochi-ootd-review")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	body := "---\nname: mochi-ootd-review\n---\nTEST OOTD SKILL: only structured OOTD JSON."
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	return skills.NewLoader(root, "", "")
}

func testOOTDReportJSON() string {
	return `{"todayJudgment":{"title":"城市休闲极简主义","score":5.5,"label":"还可以更好","summary":"整体有方向，但鞋包和上身还差一个清晰态度。"},"overallStyle":"偏城市休闲，靠中性色和宽松廓形成立。","highlights":["比例干净","色彩稳定"],"biggestIssue":"上身黑色太整块，缺少细节焦点。","suggestions":[{"title":"补一个焦点","body":"换一只更利落的包。"}],"palette":[{"name":"Black","hex":"#1A1A1A"},{"name":"Bone","hex":"#EAE9E1"}],"mochiLine":"底子不差，但现在少一口气。","shareCard":{"title":"城市休闲极简主义","quote":"底子不差，但现在少一口气。","advice":["把鞋换浅","补金属小配件"],"cta":"让 Mochi 也看看你的 OOTD"}}`
}
