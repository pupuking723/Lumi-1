package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	sessionspkg "github.com/nextlevelbuilder/goclaw/internal/sessions"
)

func TestChatMessagesHandlerListsUserSessionHistory(t *testing.T) {
	InitGatewayToken("history-token")
	t.Cleanup(func() { InitGatewayToken("") })

	store := sessionspkg.NewManager("")
	h := NewChatMessagesHandler(store)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	createdAt := time.Date(2026, 5, 27, 3, 10, 0, 0, time.UTC)
	sessionKey := chatCompletionSessionKey("closy", "google.user-1", "mochi-session-1", "history000")
	store.AddMessage(context.Background(), sessionKey, providers.Message{
		Role:      "user",
		Content:   "hello",
		CreatedAt: &createdAt,
	})
	store.AddMessage(context.Background(), sessionKey, providers.Message{
		Role:    "assistant",
		Content: "hi there",
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/messages?session_id=mochi-session-1&model=agent:closy", nil)
	req.Header.Set("Authorization", "Bearer history-token")
	req.Header.Set("X-GoClaw-User-Id", "google.user-1")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var got chatMessagesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.SessionID != sessionKey {
		t.Fatalf("session_id = %q, want %q", got.SessionID, sessionKey)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(got.Messages))
	}
	if got.Messages[0].Role != "user" || got.Messages[0].Content != "hello" {
		t.Fatalf("first message = %+v", got.Messages[0])
	}
	if got.Messages[0].CreatedAt != "2026-05-27T03:10:00Z" {
		t.Fatalf("created_at = %q", got.Messages[0].CreatedAt)
	}
	if got.Messages[1].Role != "assistant" || got.Messages[1].Content != "hi there" {
		t.Fatalf("second message = %+v", got.Messages[1])
	}
}

func TestChatMessagesHandlerRequiresSessionID(t *testing.T) {
	InitGatewayToken("history-token")
	t.Cleanup(func() { InitGatewayToken("") })

	h := NewChatMessagesHandler(sessionspkg.NewManager(""))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/messages?model=agent:closy", nil)
	req.Header.Set("Authorization", "Bearer history-token")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
