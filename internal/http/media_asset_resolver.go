package http

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/media"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func mediaAssetTempPath(ctx context.Context, objectStore *media.ObjectStore, asset *store.MediaAssetData) (string, func(), error) {
	if asset == nil {
		return "", nil, fmt.Errorf("media asset is nil")
	}
	if asset.StorageKey == "" {
		return "", nil, fmt.Errorf("media has no storage key: %s", asset.ID.String())
	}
	backend := strings.TrimSpace(asset.StorageBackend)
	if backend == "" || backend == store.MediaStorageLocal {
		if _, err := os.Stat(asset.StorageKey); err != nil {
			return "", nil, fmt.Errorf("media file unavailable: %s", asset.ID.String())
		}
		return asset.StorageKey, func() {}, nil
	}
	if backend != store.MediaStorageOSS {
		return "", nil, fmt.Errorf("media storage backend %q is not supported by this runtime yet", asset.StorageBackend)
	}
	if objectStore == nil {
		return "", nil, fmt.Errorf("oss media storage is not configured")
	}
	ext := media.ExtFromMime(asset.MimeType)
	if ext == "" {
		ext = filepath.Ext(asset.OriginalFilename)
	}
	if ext == "" {
		ext = ".bin"
	}
	tmp, err := os.CreateTemp("", "goclaw-media-*"+ext)
	if err != nil {
		return "", nil, fmt.Errorf("create media temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", nil, fmt.Errorf("close media temp file: %w", err)
	}
	if err := objectStore.DownloadToFile(ctx, asset.StorageKey, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", nil, err
	}
	return tmpPath, func() { _ = os.Remove(tmpPath) }, nil
}

func readMediaAssetBytes(ctx context.Context, objectStore *media.ObjectStore, asset *store.MediaAssetData) ([]byte, error) {
	if asset == nil {
		return nil, fmt.Errorf("media asset is nil")
	}
	backend := strings.TrimSpace(asset.StorageBackend)
	if backend == "" || backend == store.MediaStorageLocal {
		if asset.StorageKey == "" {
			return nil, fmt.Errorf("media has no storage key: %s", asset.ID.String())
		}
		return os.ReadFile(asset.StorageKey)
	}
	if backend != store.MediaStorageOSS {
		return nil, fmt.Errorf("media storage backend %q is not supported by this runtime yet", asset.StorageBackend)
	}
	if objectStore == nil {
		return nil, fmt.Errorf("oss media storage is not configured")
	}
	return objectStore.ReadFile(ctx, asset.StorageKey)
}

func mediaAssetURL(ctx context.Context, objectStore *media.ObjectStore, asset *store.MediaAssetData) string {
	if asset == nil {
		return ""
	}
	backend := strings.TrimSpace(asset.StorageBackend)
	if backend != store.MediaStorageOSS || objectStore == nil {
		return ""
	}
	u, err := objectStore.URL(ctx, asset.StorageKey)
	if err != nil {
		return ""
	}
	return u
}
