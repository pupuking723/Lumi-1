package http

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/media"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// ChatMessagesHandler exposes C-side chat session history.
type ChatMessagesHandler struct {
	sessions    chatMessagesSessionStore
	mediaAssets store.MediaAssetStore
	objectStore *media.ObjectStore
}

type chatMessagesSessionStore interface {
	GetHistory(ctx context.Context, key string) []providers.Message
}

type chatMessagesResponse struct {
	SessionID string               `json:"session_id"`
	Messages  []chatHistoryMessage `json:"messages"`
}

type chatHistoryMessage struct {
	ID        string                `json:"id,omitempty"`
	Role      string                `json:"role"`
	Content   string                `json:"content"`
	CreatedAt string                `json:"created_at,omitempty"`
	MediaRefs []chatHistoryMediaRef `json:"media_refs,omitempty"`
}

type chatHistoryMediaRef struct {
	ID         string `json:"id"`
	Kind       string `json:"kind,omitempty"`
	MimeType   string `json:"mime_type,omitempty"`
	URL        string `json:"url,omitempty"`
	PreviewURL string `json:"preview_url,omitempty"`
}

func NewChatMessagesHandler(sess chatMessagesSessionStore) *ChatMessagesHandler {
	return &ChatMessagesHandler{sessions: sess}
}

func (h *ChatMessagesHandler) SetMediaAssetStore(st store.MediaAssetStore) {
	h.mediaAssets = st
}

func (h *ChatMessagesHandler) SetObjectStore(objectStore *media.ObjectStore) {
	h.objectStore = objectStore
}

func (h *ChatMessagesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/chat/messages", requireAuth(permissions.RoleViewer, h.handleList))
}

func (h *ChatMessagesHandler) handleList(w http.ResponseWriter, r *http.Request) {
	if h.sessions == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "session storage is not configured"})
		return
	}

	requestedSessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if requestedSessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id is required"})
		return
	}

	agentID := extractAgentID(r, r.URL.Query().Get("model"))
	userID := store.UserIDFromContext(r.Context())
	sessionKey := chatCompletionSessionKey(agentID, userID, requestedSessionID, "history000")

	history := h.sessions.GetHistory(r.Context(), sessionKey)
	writeJSON(w, http.StatusOK, chatMessagesResponse{
		SessionID: sessionKey,
		Messages:  h.toChatHistoryMessages(r.Context(), history),
	})
}

func (h *ChatMessagesHandler) toChatHistoryMessages(ctx context.Context, history []providers.Message) []chatHistoryMessage {
	result := make([]chatHistoryMessage, 0, len(history))
	for i, msg := range history {
		result = append(result, chatHistoryMessage{
			ID:        "msg-history-" + strconv.Itoa(i),
			Role:      msg.Role,
			Content:   msg.Content,
			CreatedAt: chatHistoryCreatedAt(msg),
			MediaRefs: h.toChatHistoryMediaRefs(ctx, msg.MediaRefs),
		})
	}
	return result
}

func chatHistoryCreatedAt(msg providers.Message) string {
	if msg.CreatedAt == nil {
		return ""
	}
	return msg.CreatedAt.UTC().Format(time.RFC3339Nano)
}

func (h *ChatMessagesHandler) toChatHistoryMediaRefs(ctx context.Context, refs []providers.MediaRef) []chatHistoryMediaRef {
	if len(refs) == 0 {
		return nil
	}
	result := make([]chatHistoryMediaRef, 0, len(refs))
	for _, ref := range refs {
		u := h.mediaRefURL(ctx, ref)
		result = append(result, chatHistoryMediaRef{
			ID:         ref.ID,
			Kind:       ref.Kind,
			MimeType:   ref.MimeType,
			URL:        u,
			PreviewURL: u,
		})
	}
	return result
}

func (h *ChatMessagesHandler) mediaRefURL(ctx context.Context, ref providers.MediaRef) string {
	if strings.HasPrefix(ref.Path, "https://") || strings.HasPrefix(ref.Path, "http://") {
		return ref.Path
	}
	if h == nil || h.mediaAssets == nil || strings.TrimSpace(ref.ID) == "" {
		return ""
	}
	id, err := uuid.Parse(ref.ID)
	if err != nil {
		return ""
	}
	asset, err := h.mediaAssets.GetMediaAsset(ctx, id)
	if err != nil || asset == nil {
		return ""
	}
	return mediaAssetURL(ctx, h.objectStore, asset)
}
