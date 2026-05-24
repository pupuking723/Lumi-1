package closy

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func TestBuildShareCardPayloadUsesOOTDResult(t *testing.T) {
	reviewID := uuid.New()
	mediaID := uuid.New()
	payload := BuildShareCardPayload(&store.ClosyOOTDReviewData{
		BaseModel:        store.BaseModel{ID: reviewID},
		MediaID:          mediaID,
		StyleLabel:       "clean casual",
		OverallJudgement: "能出门",
		Highlight:        "颜色干净",
		MainIssue:        "鞋子略断层",
		Suggestion:       "换浅色鞋",
		MochiLine:        "人会问链接。",
	}, "https://example.test/s/closy/abc", "", "https://example.test", time.Date(2026, 5, 24, 1, 2, 3, 0, time.UTC))

	if payload.AspectRatio != "9:16" || payload.Brand != DisplayName || payload.OOTDReviewID != reviewID.String() || payload.MediaID != mediaID.String() {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.CTA.Text == "" || payload.ShareURL != payload.QRValue {
		t.Fatalf("payload CTA/share = %#v", payload)
	}
}

func TestNewShareSlug(t *testing.T) {
	if slug := NewShareSlug(); len(slug) < 10 {
		t.Fatalf("slug too short: %q", slug)
	}
}
