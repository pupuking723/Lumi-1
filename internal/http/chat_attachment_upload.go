package http

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	mediapkg "github.com/nextlevelbuilder/goclaw/internal/channels/media"
	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/media"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// ChatAttachmentUploadHandler is the C-side upload endpoint used by
// /v1/chat/completions attachments. It intentionally does not replace the
// legacy /v1/media/upload endpoint used by the console WebSocket flow.
type ChatAttachmentUploadHandler struct {
	mediaStore *media.Store
	assets     store.MediaAssetStore
}

func NewChatAttachmentUploadHandler(mediaStore *media.Store, assets store.MediaAssetStore) *ChatAttachmentUploadHandler {
	return &ChatAttachmentUploadHandler{mediaStore: mediaStore, assets: assets}
}

func (h *ChatAttachmentUploadHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/chat/attachments/upload", requireAuth(permissions.RoleOperator, h.handleUpload))
}

func (h *ChatAttachmentUploadHandler) handleUpload(w http.ResponseWriter, r *http.Request) {
	locale := extractLocale(r)
	if h.mediaStore == nil || h.assets == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": i18n.T(locale, i18n.MsgInternalError, "media storage is not configured")})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(locale, i18n.MsgFileTooLarge)})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(locale, i18n.MsgMissingFileField)})
		return
	}
	defer file.Close()

	origName := filepath.Base(header.Filename)
	if origName == "." || origName == "/" || strings.Contains(origName, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(locale, i18n.MsgInvalidFilename)})
		return
	}
	mimeType := mediapkg.DetectMIMEType(origName)
	ext := filepath.Ext(origName)
	if ext == "" {
		ext = media.ExtFromMime(mimeType)
	}
	if ext == "" {
		ext = ".bin"
	}

	tmp, err := os.CreateTemp("", "cchat-upload-*"+ext)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": i18n.T(locale, i18n.MsgInternalError, "failed to create temp file")})
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	hasher := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(tmp, hasher), file)
	closeErr := tmp.Close()
	if copyErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": i18n.T(locale, i18n.MsgInternalError, "failed to save file")})
		return
	}
	if closeErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": i18n.T(locale, i18n.MsgInternalError, "failed to finalize file")})
		return
	}

	userID := store.UserIDFromContext(r.Context())
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	var sessionPtr *string
	if sessionID != "" {
		sessionPtr = &sessionID
	}

	var agentID *uuid.UUID
	if rawAgentID := strings.TrimSpace(r.FormValue("agent_id")); rawAgentID != "" {
		id, err := uuid.Parse(rawAgentID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(locale, i18n.MsgInvalidID, "agent")})
			return
		}
		agentID = &id
	}

	mediaSessionKey := fmt.Sprintf("c-chat:%s:%s", store.TenantIDFromContext(r.Context()), userID)
	if sessionID != "" {
		mediaSessionKey = "c-chat:" + sessionID
	}
	mediaID, storedPath, err := h.mediaStore.SaveFile(mediaSessionKey, tmpPath, mimeType)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": i18n.T(locale, i18n.MsgInternalError, "failed to persist file")})
		return
	}

	id, err := uuid.Parse(mediaID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": i18n.T(locale, i18n.MsgInternalError, "invalid generated media id")})
		return
	}
	asset, err := h.assets.CreateMediaAsset(r.Context(), store.CreateMediaAssetParams{
		ID:               id,
		UserID:           userID,
		SessionID:        sessionPtr,
		AgentID:          agentID,
		OriginalFilename: origName,
		MimeType:         mimeType,
		Size:             size,
		SHA256:           hex.EncodeToString(hasher.Sum(nil)),
		StorageBackend:   store.MediaStorageLocal,
		StorageKey:       storedPath,
		Status:           store.MediaStatusReady,
		Visibility:       "private",
	})
	if err != nil {
		_ = os.Remove(storedPath)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": i18n.T(locale, i18n.MsgInternalError, err.Error())})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"media_id":  asset.ID.String(),
		"filename":  asset.OriginalFilename,
		"mime_type": asset.MimeType,
		"size":      asset.Size,
		"sha256":    asset.SHA256,
		"storage":   asset.StorageBackend,
		"status":    asset.Status,
	})
}
