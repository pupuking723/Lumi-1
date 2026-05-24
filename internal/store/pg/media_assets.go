package pg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

type PGMediaAssetStore struct {
	db *sql.DB
}

func NewPGMediaAssetStore(db *sql.DB) *PGMediaAssetStore {
	return &PGMediaAssetStore{db: db}
}

func (s *PGMediaAssetStore) CreateMediaAsset(ctx context.Context, p store.CreateMediaAssetParams) (*store.MediaAssetData, error) {
	if p.ID == uuid.Nil {
		p.ID = store.GenNewID()
	}
	if p.StorageBackend == "" {
		p.StorageBackend = store.MediaStorageLocal
	}
	if p.Status == "" {
		p.Status = store.MediaStatusReady
	}
	if p.Visibility == "" {
		p.Visibility = "private"
	}
	if len(p.Metadata) == 0 {
		p.Metadata = json.RawMessage(`{}`)
	}
	now := time.Now().UTC()
	tid := tenantIDForInsert(ctx)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO media_assets (
			id, tenant_id, user_id, session_id, agent_id, original_filename, mime_type,
			file_size, sha256, storage_backend, storage_bucket, storage_key, status,
			visibility, metadata, expires_at, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18
		)`,
		p.ID, tid, p.UserID, p.SessionID, p.AgentID, p.OriginalFilename, p.MimeType,
		p.Size, p.SHA256, p.StorageBackend, p.StorageBucket, p.StorageKey, p.Status,
		p.Visibility, p.Metadata, p.ExpiresAt, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("create media asset: %w", err)
	}
	return s.GetMediaAsset(ctx, p.ID)
}

func (s *PGMediaAssetStore) GetMediaAsset(ctx context.Context, id uuid.UUID) (*store.MediaAssetData, error) {
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		tid = store.MasterTenantID
	}
	var a store.MediaAssetData
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, session_id, agent_id, original_filename, mime_type,
		       file_size, sha256, storage_backend, storage_bucket, storage_key, status,
		       visibility, metadata, expires_at, created_at, updated_at
		  FROM media_assets
		 WHERE id = $1 AND tenant_id = $2`,
		id, tid,
	).Scan(
		&a.ID, &a.TenantID, &a.UserID, &a.SessionID, &a.AgentID, &a.OriginalFilename,
		&a.MimeType, &a.Size, &a.SHA256, &a.StorageBackend, &a.StorageBucket,
		&a.StorageKey, &a.Status, &a.Visibility, &a.Metadata, &a.ExpiresAt,
		&a.CreatedAt, &a.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get media asset: %w", err)
	}
	return &a, nil
}
