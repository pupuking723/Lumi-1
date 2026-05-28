package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	ClosyOOTDStatusCompleted = "completed"
	ClosyOOTDStatusFailed    = "failed"
)

type ClosyOOTDReviewData struct {
	BaseModel
	TenantID         uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	UserID           string          `json:"user_id" db:"user_id"`
	AgentID          uuid.UUID       `json:"agent_id" db:"agent_id"`
	MediaID          uuid.UUID       `json:"media_id" db:"media_id"`
	SessionID        string          `json:"session_id" db:"session_id"`
	Occasion         string          `json:"occasion" db:"occasion"`
	UserNote         string          `json:"user_note" db:"user_note"`
	OverallJudgement string          `json:"overall_judgement" db:"overall_judgement"`
	StyleLabel       string          `json:"style_label" db:"style_label"`
	Highlight        string          `json:"highlight" db:"highlight"`
	MainIssue        string          `json:"main_issue" db:"main_issue"`
	Suggestion       string          `json:"suggestion" db:"suggestion"`
	MochiLine        string          `json:"mochi_line" db:"mochi_line"`
	SafetyNotes      string          `json:"safety_notes,omitempty" db:"safety_notes"`
	RawResponse      string          `json:"raw_response,omitempty" db:"raw_response"`
	ReportJSON       json.RawMessage `json:"report_json,omitempty" db:"report_json"`
	Status           string          `json:"status" db:"status"`
	ErrorMessage     string          `json:"error_message,omitempty" db:"error_message"`
}

type CreateClosyOOTDReviewParams struct {
	ID               uuid.UUID
	UserID           string
	AgentID          uuid.UUID
	MediaID          uuid.UUID
	SessionID        string
	Occasion         string
	UserNote         string
	OverallJudgement string
	StyleLabel       string
	Highlight        string
	MainIssue        string
	Suggestion       string
	MochiLine        string
	SafetyNotes      string
	RawResponse      string
	ReportJSON       json.RawMessage
	Status           string
	ErrorMessage     string
}

type ListClosyOOTDReviewsParams struct {
	UserID string
	Limit  int
	Since  *time.Time
}

type ClosyOOTDStore interface {
	CreateClosyOOTDReview(ctx context.Context, p CreateClosyOOTDReviewParams) (*ClosyOOTDReviewData, error)
	GetClosyOOTDReview(ctx context.Context, id uuid.UUID) (*ClosyOOTDReviewData, error)
	ListClosyOOTDReviews(ctx context.Context, p ListClosyOOTDReviewsParams) ([]ClosyOOTDReviewData, error)
}
