package pg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

type PGClosyOOTDStore struct {
	db *sql.DB
}

func NewPGClosyOOTDStore(db *sql.DB) *PGClosyOOTDStore {
	return &PGClosyOOTDStore{db: db}
}

func (s *PGClosyOOTDStore) CreateClosyOOTDReview(ctx context.Context, p store.CreateClosyOOTDReviewParams) (*store.ClosyOOTDReviewData, error) {
	if strings.TrimSpace(p.UserID) == "" {
		return nil, fmt.Errorf("closy ootd user_id is required")
	}
	if p.AgentID == uuid.Nil {
		return nil, fmt.Errorf("closy ootd agent_id is required")
	}
	if p.MediaID == uuid.Nil {
		return nil, fmt.Errorf("closy ootd media_id is required")
	}
	if p.ID == uuid.Nil {
		p.ID = store.GenNewID()
	}
	if p.Status == "" {
		p.Status = store.ClosyOOTDStatusCompleted
	}
	now := time.Now().UTC()
	tid := tenantIDForInsert(ctx)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO closy_ootd_reviews (
			id, tenant_id, user_id, agent_id, media_id, session_id, occasion, user_note,
			overall_judgement, style_label, highlight, main_issue, suggestion, mochi_line,
			safety_notes, raw_response, report_json, status, error_message, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21
		)`,
		p.ID, tid, strings.TrimSpace(p.UserID), p.AgentID, p.MediaID, strings.TrimSpace(p.SessionID),
		strings.TrimSpace(p.Occasion), strings.TrimSpace(p.UserNote), strings.TrimSpace(p.OverallJudgement),
		strings.TrimSpace(p.StyleLabel), strings.TrimSpace(p.Highlight), strings.TrimSpace(p.MainIssue),
		strings.TrimSpace(p.Suggestion), strings.TrimSpace(p.MochiLine), strings.TrimSpace(p.SafetyNotes),
		strings.TrimSpace(p.RawResponse), nullableRawJSON(p.ReportJSON), p.Status, strings.TrimSpace(p.ErrorMessage), now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("create closy ootd review: %w", err)
	}
	return s.GetClosyOOTDReview(ctx, p.ID)
}

func (s *PGClosyOOTDStore) GetClosyOOTDReview(ctx context.Context, id uuid.UUID) (*store.ClosyOOTDReviewData, error) {
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		tid = store.MasterTenantID
	}
	var r store.ClosyOOTDReviewData
	var reportJSON sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, agent_id, media_id, session_id, occasion, user_note,
		       overall_judgement, style_label, highlight, main_issue, suggestion, mochi_line,
		       safety_notes, raw_response, report_json, status, error_message, created_at, updated_at
		  FROM closy_ootd_reviews
		 WHERE id = $1 AND tenant_id = $2`,
		id, tid,
	).Scan(
		&r.ID, &r.TenantID, &r.UserID, &r.AgentID, &r.MediaID, &r.SessionID, &r.Occasion, &r.UserNote,
		&r.OverallJudgement, &r.StyleLabel, &r.Highlight, &r.MainIssue, &r.Suggestion, &r.MochiLine,
		&r.SafetyNotes, &r.RawResponse, &reportJSON, &r.Status, &r.ErrorMessage, &r.CreatedAt, &r.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get closy ootd review: %w", err)
	}
	if reportJSON.Valid && strings.TrimSpace(reportJSON.String) != "" {
		r.ReportJSON = []byte(reportJSON.String)
	}
	return &r, nil
}

func (s *PGClosyOOTDStore) FindLatestClosyOOTDReport(ctx context.Context, p store.FindLatestClosyOOTDReportParams) (*store.ClosyOOTDReviewData, error) {
	userID := strings.TrimSpace(p.UserID)
	if userID == "" {
		return nil, fmt.Errorf("closy ootd user_id is required")
	}
	if p.MediaID == uuid.Nil {
		return nil, fmt.Errorf("closy ootd media_id is required")
	}
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		tid = store.MasterTenantID
	}
	var r store.ClosyOOTDReviewData
	var reportJSON sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, agent_id, media_id, session_id, occasion, user_note,
		       overall_judgement, style_label, highlight, main_issue, suggestion, mochi_line,
		       safety_notes, raw_response, report_json, status, error_message, created_at, updated_at
		  FROM closy_ootd_reviews
		 WHERE tenant_id = $1
		   AND user_id = $2
		   AND media_id = $3
		   AND status = $4
		   AND report_json IS NOT NULL
		 ORDER BY created_at DESC
		 LIMIT 1`,
		tid, userID, p.MediaID, store.ClosyOOTDStatusCompleted,
	).Scan(
		&r.ID, &r.TenantID, &r.UserID, &r.AgentID, &r.MediaID, &r.SessionID, &r.Occasion, &r.UserNote,
		&r.OverallJudgement, &r.StyleLabel, &r.Highlight, &r.MainIssue, &r.Suggestion, &r.MochiLine,
		&r.SafetyNotes, &r.RawResponse, &reportJSON, &r.Status, &r.ErrorMessage, &r.CreatedAt, &r.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find latest closy ootd report: %w", err)
	}
	if reportJSON.Valid && strings.TrimSpace(reportJSON.String) != "" {
		r.ReportJSON = []byte(reportJSON.String)
	}
	return &r, nil
}

func (s *PGClosyOOTDStore) ListClosyOOTDReviews(ctx context.Context, p store.ListClosyOOTDReviewsParams) ([]store.ClosyOOTDReviewData, error) {
	limit := p.Limit
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		tid = store.MasterTenantID
	}
	userID := strings.TrimSpace(p.UserID)
	query := `
		SELECT id, tenant_id, user_id, agent_id, media_id, session_id, occasion, user_note,
		       overall_judgement, style_label, highlight, main_issue, suggestion, mochi_line,
		       safety_notes, raw_response, report_json, status, error_message, created_at, updated_at
		  FROM closy_ootd_reviews
		 WHERE tenant_id = $1`
	args := []any{tid}
	if userID != "" {
		args = append(args, userID)
		query += fmt.Sprintf(" AND user_id = $%d", len(args))
	}
	if p.Since != nil {
		args = append(args, *p.Since)
		query += fmt.Sprintf(" AND created_at >= $%d", len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list closy ootd reviews: %w", err)
	}
	defer rows.Close()

	var out []store.ClosyOOTDReviewData
	for rows.Next() {
		var r store.ClosyOOTDReviewData
		var reportJSON sql.NullString
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.UserID, &r.AgentID, &r.MediaID, &r.SessionID, &r.Occasion, &r.UserNote,
			&r.OverallJudgement, &r.StyleLabel, &r.Highlight, &r.MainIssue, &r.Suggestion, &r.MochiLine,
			&r.SafetyNotes, &r.RawResponse, &reportJSON, &r.Status, &r.ErrorMessage, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan closy ootd review: %w", err)
		}
		if reportJSON.Valid && strings.TrimSpace(reportJSON.String) != "" {
			r.ReportJSON = []byte(reportJSON.String)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate closy ootd reviews: %w", err)
	}
	return out, nil
}

func nullableRawJSON(data []byte) any {
	if len(data) == 0 {
		return nil
	}
	return data
}
