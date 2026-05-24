package store

import (
	"context"

	"github.com/google/uuid"
)

const (
	ClosyPrefCategoryStyle      = "style"
	ClosyPrefCategoryAvoidance  = "avoidance"
	ClosyPrefCategoryColor      = "color"
	ClosyPrefCategorySilhouette = "silhouette"
	ClosyPrefCategoryOccasion   = "occasion"
	ClosyPrefCategoryConfidence = "confidence"
	ClosyPrefCategoryChoice     = "recent_choice"

	ClosyPrefPolarityLike    = "like"
	ClosyPrefPolarityAvoid   = "avoid"
	ClosyPrefPolarityNeutral = "neutral"
)

type ClosyProfileData struct {
	BaseModel
	TenantID                  uuid.UUID `json:"tenant_id" db:"tenant_id"`
	UserID                    string    `json:"user_id" db:"user_id"`
	AgentID                   uuid.UUID `json:"agent_id" db:"agent_id"`
	StyleSummary              string    `json:"style_summary" db:"style_summary"`
	SelfExpressionSummary     string    `json:"self_expression_summary" db:"self_expression_summary"`
	SocialPresentationSummary string    `json:"social_presentation_summary" db:"social_presentation_summary"`
	CurrentStateSummary       string    `json:"current_state_summary" db:"current_state_summary"`
	Confidence                float64   `json:"confidence" db:"confidence"`
}

type ClosyStylePreferenceData struct {
	BaseModel
	TenantID         uuid.UUID `json:"tenant_id" db:"tenant_id"`
	UserID           string    `json:"user_id" db:"user_id"`
	AgentID          uuid.UUID `json:"agent_id" db:"agent_id"`
	Category         string    `json:"category" db:"category"`
	Polarity         string    `json:"polarity" db:"polarity"`
	Value            string    `json:"value" db:"value"`
	Evidence         string    `json:"evidence" db:"evidence"`
	SourceSessionKey string    `json:"source_session_key" db:"source_session_key"`
	Confidence       float64   `json:"confidence" db:"confidence"`
}

type UpsertClosyProfileParams struct {
	ID                        uuid.UUID
	UserID                    string
	AgentID                   uuid.UUID
	StyleSummary              string
	SelfExpressionSummary     string
	SocialPresentationSummary string
	CurrentStateSummary       string
	Confidence                float64
}

type UpsertClosyStylePreferenceParams struct {
	ID               uuid.UUID
	UserID           string
	AgentID          uuid.UUID
	Category         string
	Polarity         string
	Value            string
	Evidence         string
	SourceSessionKey string
	Confidence       float64
}

type ClosyMemoryStore interface {
	GetClosyProfile(ctx context.Context, agentID uuid.UUID, userID string) (*ClosyProfileData, error)
	UpsertClosyProfile(ctx context.Context, p UpsertClosyProfileParams) (*ClosyProfileData, error)
	ListClosyStylePreferences(ctx context.Context, agentID uuid.UUID, userID string, limit int) ([]ClosyStylePreferenceData, error)
	UpsertClosyStylePreference(ctx context.Context, p UpsertClosyStylePreferenceParams) (*ClosyStylePreferenceData, error)
}
