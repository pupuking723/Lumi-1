package closy

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

type ShareCardCTA struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

type ShareCardPayload struct {
	Version          string       `json:"version"`
	Format           string       `json:"format"`
	AspectRatio      string       `json:"aspect_ratio"`
	Brand            string       `json:"brand"`
	Product          string       `json:"product"`
	OOTDReviewID     string       `json:"ootd_review_id"`
	MediaID          string       `json:"media_id"`
	StyleLabel       string       `json:"style_label"`
	OverallJudgement string       `json:"overall_judgement"`
	Highlight        string       `json:"highlight"`
	MainIssue        string       `json:"main_issue"`
	Suggestion       string       `json:"suggestion"`
	MochiLine        string       `json:"mochi_line"`
	CTA              ShareCardCTA `json:"cta"`
	ShareURL         string       `json:"share_url"`
	QRValue          string       `json:"qr_value"`
	CreatedAt        string       `json:"created_at"`
}

func NewShareSlug() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strings.ReplaceAll(store.GenNewID().String()[:13], "-", "")
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]))
}

func BuildShareCardPayload(review *store.ClosyOOTDReviewData, shareURL, ctaText, ctaURL string, createdAt time.Time) ShareCardPayload {
	if ctaText = strings.TrimSpace(ctaText); ctaText == "" {
		ctaText = "Let Mochi see your outfit"
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	payload := ShareCardPayload{
		Version:     "2026-05-24.1",
		Format:      "ootd_share_card",
		AspectRatio: "9:16",
		Brand:       DisplayName,
		Product:     "Closy",
		CTA:         ShareCardCTA{Text: ctaText, URL: strings.TrimSpace(ctaURL)},
		ShareURL:    strings.TrimSpace(shareURL),
		QRValue:     strings.TrimSpace(shareURL),
		CreatedAt:   createdAt.UTC().Format(time.RFC3339),
	}
	if review != nil {
		payload.OOTDReviewID = review.ID.String()
		payload.MediaID = review.MediaID.String()
		payload.StyleLabel = review.StyleLabel
		payload.OverallJudgement = review.OverallJudgement
		payload.Highlight = review.Highlight
		payload.MainIssue = review.MainIssue
		payload.Suggestion = review.Suggestion
		payload.MochiLine = review.MochiLine
	}
	return payload
}
