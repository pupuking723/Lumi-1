package pg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

type PGClosyShareCardStore struct {
	db *sql.DB
}

func NewPGClosyShareCardStore(db *sql.DB) *PGClosyShareCardStore {
	return &PGClosyShareCardStore{db: db}
}

func (s *PGClosyShareCardStore) CreateClosyShareCard(ctx context.Context, p store.CreateClosyShareCardParams) (*store.ClosyShareCardData, error) {
	if strings.TrimSpace(p.UserID) == "" {
		return nil, fmt.Errorf("closy share card user_id is required")
	}
	if p.AgentID == uuid.Nil || p.OOTDReviewID == uuid.Nil || p.MediaID == uuid.Nil {
		return nil, fmt.Errorf("closy share card agent_id, ootd_review_id and media_id are required")
	}
	if strings.TrimSpace(p.Slug) == "" {
		return nil, fmt.Errorf("closy share card slug is required")
	}
	if p.ID == uuid.Nil {
		p.ID = store.GenNewID()
	}
	if p.Status == "" {
		p.Status = store.ClosyShareCardStatusActive
	}
	if len(p.Payload) == 0 {
		p.Payload = json.RawMessage(`{}`)
	}
	now := time.Now().UTC()
	tid := tenantIDForInsert(ctx)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO closy_share_cards (
			id, tenant_id, user_id, agent_id, ootd_review_id, media_id, slug,
			share_url, cta_text, cta_url, payload, status, expires_at, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15
		)`,
		p.ID, tid, strings.TrimSpace(p.UserID), p.AgentID, p.OOTDReviewID, p.MediaID,
		strings.TrimSpace(p.Slug), strings.TrimSpace(p.ShareURL), strings.TrimSpace(p.CTAText),
		strings.TrimSpace(p.CTAURL), p.Payload, p.Status, p.ExpiresAt, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("create closy share card: %w", err)
	}
	return s.GetClosyShareCard(ctx, p.ID)
}

func (s *PGClosyShareCardStore) GetClosyShareCard(ctx context.Context, id uuid.UUID) (*store.ClosyShareCardData, error) {
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		tid = store.MasterTenantID
	}
	return s.scanOne(ctx, `
		SELECT id, tenant_id, user_id, agent_id, ootd_review_id, media_id, slug,
		       share_url, cta_text, cta_url, payload, status, view_count, expires_at, created_at, updated_at
		  FROM closy_share_cards
		 WHERE id = $1 AND tenant_id = $2`, id, tid)
}

func (s *PGClosyShareCardStore) GetClosyShareCardBySlug(ctx context.Context, slug string) (*store.ClosyShareCardData, error) {
	return s.scanOne(ctx, `
		SELECT id, tenant_id, user_id, agent_id, ootd_review_id, media_id, slug,
		       share_url, cta_text, cta_url, payload, status, view_count, expires_at, created_at, updated_at
		  FROM closy_share_cards
		 WHERE slug = $1`, strings.TrimSpace(slug))
}

func (s *PGClosyShareCardStore) ListClosyShareCards(ctx context.Context, p store.ListClosyShareCardsParams) ([]store.ClosyShareCardData, error) {
	limit := p.Limit
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		tid = store.MasterTenantID
	}
	query := `
		SELECT id, tenant_id, user_id, agent_id, ootd_review_id, media_id, slug,
		       share_url, cta_text, cta_url, payload, status, view_count, expires_at, created_at, updated_at
		  FROM closy_share_cards
		 WHERE tenant_id = $1`
	args := []any{tid}
	if userID := strings.TrimSpace(p.UserID); userID != "" {
		args = append(args, userID)
		query += fmt.Sprintf(" AND user_id = $%d", len(args))
	}
	if p.OOTDReviewID != uuid.Nil {
		args = append(args, p.OOTDReviewID)
		query += fmt.Sprintf(" AND ootd_review_id = $%d", len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list closy share cards: %w", err)
	}
	defer rows.Close()
	var out []store.ClosyShareCardData
	for rows.Next() {
		card, err := scanClosyShareCard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *card)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate closy share cards: %w", err)
	}
	return out, nil
}

func (s *PGClosyShareCardStore) IncrementClosyShareCardViews(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `UPDATE closy_share_cards SET view_count = view_count + 1, updated_at = NOW() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("increment closy share card views: %w", err)
	}
	return nil
}

func (s *PGClosyShareCardStore) scanOne(ctx context.Context, query string, args ...any) (*store.ClosyShareCardData, error) {
	row := s.db.QueryRowContext(ctx, query, args...)
	card, err := scanClosyShareCard(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get closy share card: %w", err)
	}
	return card, nil
}

type closyShareCardScanner interface {
	Scan(dest ...any) error
}

func scanClosyShareCard(scanner closyShareCardScanner) (*store.ClosyShareCardData, error) {
	var card store.ClosyShareCardData
	if err := scanner.Scan(
		&card.ID, &card.TenantID, &card.UserID, &card.AgentID, &card.OOTDReviewID,
		&card.MediaID, &card.Slug, &card.ShareURL, &card.CTAText, &card.CTAURL,
		&card.Payload, &card.Status, &card.ViewCount, &card.ExpiresAt, &card.CreatedAt, &card.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &card, nil
}
