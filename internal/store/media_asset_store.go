package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	MediaStorageLocal = "local"
	MediaStorageOSS   = "oss"
	MediaStatusReady  = "ready"
)

// MediaAssetData records where an uploaded user file lives and how it may be
// resolved for agent runs. Chat APIs should pass media_id rather than storage
// paths so local/object storage can be swapped without changing clients.
type MediaAssetData struct {
	ID               uuid.UUID       `json:"media_id" db:"id"`
	TenantID         uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	UserID           string          `json:"user_id" db:"user_id"`
	SessionID        *string         `json:"session_id,omitempty" db:"session_id"`
	AgentID          *uuid.UUID      `json:"agent_id,omitempty" db:"agent_id"`
	OriginalFilename string          `json:"filename" db:"original_filename"`
	MimeType         string          `json:"mime_type" db:"mime_type"`
	Size             int64           `json:"size" db:"file_size"`
	SHA256           string          `json:"sha256" db:"sha256"`
	StorageBackend   string          `json:"storage" db:"storage_backend"`
	StorageBucket    *string         `json:"storage_bucket,omitempty" db:"storage_bucket"`
	StorageKey       string          `json:"-" db:"storage_key"`
	Status           string          `json:"status" db:"status"`
	Visibility       string          `json:"visibility" db:"visibility"`
	Metadata         json.RawMessage `json:"metadata,omitempty" db:"metadata"`
	ExpiresAt        *time.Time      `json:"expires_at,omitempty" db:"expires_at"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at" db:"updated_at"`
}

type CreateMediaAssetParams struct {
	ID               uuid.UUID
	UserID           string
	SessionID        *string
	AgentID          *uuid.UUID
	OriginalFilename string
	MimeType         string
	Size             int64
	SHA256           string
	StorageBackend   string
	StorageBucket    *string
	StorageKey       string
	Status           string
	Visibility       string
	Metadata         json.RawMessage
	ExpiresAt        *time.Time
}

type MediaAssetStore interface {
	CreateMediaAsset(ctx context.Context, p CreateMediaAssetParams) (*MediaAssetData, error)
	GetMediaAsset(ctx context.Context, id uuid.UUID) (*MediaAssetData, error)
}
