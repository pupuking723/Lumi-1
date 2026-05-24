package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	ClosyShareCardStatusActive  = "active"
	ClosyShareCardStatusRevoked = "revoked"
)

type ClosyShareCardData struct {
	BaseModel
	TenantID     uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	UserID       string          `json:"user_id" db:"user_id"`
	AgentID      uuid.UUID       `json:"agent_id" db:"agent_id"`
	OOTDReviewID uuid.UUID       `json:"ootd_review_id" db:"ootd_review_id"`
	MediaID      uuid.UUID       `json:"media_id" db:"media_id"`
	Slug         string          `json:"slug" db:"slug"`
	ShareURL     string          `json:"share_url" db:"share_url"`
	CTAText      string          `json:"cta_text" db:"cta_text"`
	CTAURL       string          `json:"cta_url" db:"cta_url"`
	Payload      json.RawMessage `json:"payload" db:"payload"`
	Status       string          `json:"status" db:"status"`
	ViewCount    int64           `json:"view_count" db:"view_count"`
	ExpiresAt    *time.Time      `json:"expires_at,omitempty" db:"expires_at"`
}

type CreateClosyShareCardParams struct {
	ID           uuid.UUID
	UserID       string
	AgentID      uuid.UUID
	OOTDReviewID uuid.UUID
	MediaID      uuid.UUID
	Slug         string
	ShareURL     string
	CTAText      string
	CTAURL       string
	Payload      json.RawMessage
	Status       string
	ExpiresAt    *time.Time
}

type ListClosyShareCardsParams struct {
	UserID       string
	OOTDReviewID uuid.UUID
	Limit        int
}

type ClosyShareCardStore interface {
	CreateClosyShareCard(ctx context.Context, p CreateClosyShareCardParams) (*ClosyShareCardData, error)
	GetClosyShareCard(ctx context.Context, id uuid.UUID) (*ClosyShareCardData, error)
	GetClosyShareCardBySlug(ctx context.Context, slug string) (*ClosyShareCardData, error)
	ListClosyShareCards(ctx context.Context, p ListClosyShareCardsParams) ([]ClosyShareCardData, error)
	IncrementClosyShareCardViews(ctx context.Context, id uuid.UUID) error
}
