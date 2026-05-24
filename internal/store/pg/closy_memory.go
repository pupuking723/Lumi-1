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

type PGClosyMemoryStore struct {
	db *sql.DB
}

func NewPGClosyMemoryStore(db *sql.DB) *PGClosyMemoryStore {
	return &PGClosyMemoryStore{db: db}
}

func (s *PGClosyMemoryStore) GetClosyProfile(ctx context.Context, agentID uuid.UUID, userID string) (*store.ClosyProfileData, error) {
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		tid = store.MasterTenantID
	}
	var p store.ClosyProfileData
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, agent_id, style_summary, self_expression_summary,
		       social_presentation_summary, current_state_summary, confidence, created_at, updated_at
		  FROM closy_profiles
		 WHERE tenant_id = $1 AND agent_id = $2 AND user_id = $3`,
		tid, agentID, strings.TrimSpace(userID),
	).Scan(
		&p.ID, &p.TenantID, &p.UserID, &p.AgentID, &p.StyleSummary, &p.SelfExpressionSummary,
		&p.SocialPresentationSummary, &p.CurrentStateSummary, &p.Confidence, &p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get closy profile: %w", err)
	}
	return &p, nil
}

func (s *PGClosyMemoryStore) UpsertClosyProfile(ctx context.Context, p store.UpsertClosyProfileParams) (*store.ClosyProfileData, error) {
	if strings.TrimSpace(p.UserID) == "" {
		return nil, fmt.Errorf("closy profile user_id is required")
	}
	if p.AgentID == uuid.Nil {
		return nil, fmt.Errorf("closy profile agent_id is required")
	}
	if p.ID == uuid.Nil {
		p.ID = store.GenNewID()
	}
	tid := tenantIDForInsert(ctx)
	now := time.Now().UTC()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO closy_profiles (
			id, tenant_id, user_id, agent_id, style_summary, self_expression_summary,
			social_presentation_summary, current_state_summary, confidence, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (tenant_id, user_id, agent_id) DO UPDATE SET
			style_summary = CASE WHEN EXCLUDED.style_summary <> '' THEN EXCLUDED.style_summary ELSE closy_profiles.style_summary END,
			self_expression_summary = CASE WHEN EXCLUDED.self_expression_summary <> '' THEN EXCLUDED.self_expression_summary ELSE closy_profiles.self_expression_summary END,
			social_presentation_summary = CASE WHEN EXCLUDED.social_presentation_summary <> '' THEN EXCLUDED.social_presentation_summary ELSE closy_profiles.social_presentation_summary END,
			current_state_summary = CASE WHEN EXCLUDED.current_state_summary <> '' THEN EXCLUDED.current_state_summary ELSE closy_profiles.current_state_summary END,
			confidence = GREATEST(closy_profiles.confidence, EXCLUDED.confidence),
			updated_at = EXCLUDED.updated_at`,
		p.ID, tid, strings.TrimSpace(p.UserID), p.AgentID, strings.TrimSpace(p.StyleSummary),
		strings.TrimSpace(p.SelfExpressionSummary), strings.TrimSpace(p.SocialPresentationSummary),
		strings.TrimSpace(p.CurrentStateSummary), p.Confidence, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert closy profile: %w", err)
	}
	return s.GetClosyProfile(ctx, p.AgentID, p.UserID)
}

func (s *PGClosyMemoryStore) ListClosyStylePreferences(ctx context.Context, agentID uuid.UUID, userID string, limit int) ([]store.ClosyStylePreferenceData, error) {
	if limit <= 0 || limit > 100 {
		limit = 40
	}
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		tid = store.MasterTenantID
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, user_id, agent_id, category, polarity, value, evidence,
		       source_session_key, confidence, created_at, updated_at
		  FROM closy_style_preferences
		 WHERE tenant_id = $1 AND agent_id = $2 AND user_id = $3
		 ORDER BY confidence DESC, updated_at DESC
		 LIMIT $4`,
		tid, agentID, strings.TrimSpace(userID), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list closy style preferences: %w", err)
	}
	defer rows.Close()

	out := []store.ClosyStylePreferenceData{}
	for rows.Next() {
		var p store.ClosyStylePreferenceData
		if err := rows.Scan(
			&p.ID, &p.TenantID, &p.UserID, &p.AgentID, &p.Category, &p.Polarity, &p.Value,
			&p.Evidence, &p.SourceSessionKey, &p.Confidence, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan closy style preference: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate closy style preferences: %w", err)
	}
	return out, nil
}

func (s *PGClosyMemoryStore) UpsertClosyStylePreference(ctx context.Context, p store.UpsertClosyStylePreferenceParams) (*store.ClosyStylePreferenceData, error) {
	p.UserID = strings.TrimSpace(p.UserID)
	p.Category = strings.TrimSpace(p.Category)
	p.Polarity = strings.TrimSpace(p.Polarity)
	p.Value = strings.TrimSpace(p.Value)
	if p.UserID == "" {
		return nil, fmt.Errorf("closy preference user_id is required")
	}
	if p.AgentID == uuid.Nil {
		return nil, fmt.Errorf("closy preference agent_id is required")
	}
	if p.Category == "" || p.Polarity == "" || p.Value == "" {
		return nil, fmt.Errorf("closy preference category, polarity and value are required")
	}
	if p.ID == uuid.Nil {
		p.ID = store.GenNewID()
	}
	tid := tenantIDForInsert(ctx)
	now := time.Now().UTC()

	var out store.ClosyStylePreferenceData
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO closy_style_preferences (
			id, tenant_id, user_id, agent_id, category, polarity, value, evidence,
			source_session_key, confidence, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (tenant_id, user_id, agent_id, category, polarity, value) DO UPDATE SET
			evidence = CASE
				WHEN EXCLUDED.confidence >= closy_style_preferences.confidence AND EXCLUDED.evidence <> '' THEN EXCLUDED.evidence
				ELSE closy_style_preferences.evidence
			END,
			source_session_key = CASE
				WHEN EXCLUDED.confidence >= closy_style_preferences.confidence AND EXCLUDED.source_session_key <> '' THEN EXCLUDED.source_session_key
				ELSE closy_style_preferences.source_session_key
			END,
			confidence = GREATEST(closy_style_preferences.confidence, EXCLUDED.confidence),
			updated_at = EXCLUDED.updated_at
		RETURNING id, tenant_id, user_id, agent_id, category, polarity, value, evidence,
		          source_session_key, confidence, created_at, updated_at`,
		p.ID, tid, p.UserID, p.AgentID, p.Category, p.Polarity, p.Value,
		strings.TrimSpace(p.Evidence), strings.TrimSpace(p.SourceSessionKey), p.Confidence, now, now,
	).Scan(
		&out.ID, &out.TenantID, &out.UserID, &out.AgentID, &out.Category, &out.Polarity,
		&out.Value, &out.Evidence, &out.SourceSessionKey, &out.Confidence, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert closy style preference: %w", err)
	}
	return &out, nil
}
