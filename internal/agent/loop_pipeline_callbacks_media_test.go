package agent

import (
	"context"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

type captureSessionStore struct {
	nopSessionStore
	messages []providers.Message
}

func (s *captureSessionStore) AddMessage(_ context.Context, _ string, msg providers.Message) {
	s.messages = append(s.messages, msg)
}

func TestFlushMessagesPersistsEnrichedInputMediaRefs(t *testing.T) {
	store := &captureSessionStore{}
	loop := &Loop{sessions: store}
	req := &RunRequest{
		SessionKey: "agent:closy:cchat:direct:user-a-ootd-1",
		Message:    "<media:image>\n\n你再看一下",
		enrichedInput: &providers.Message{
			Role:    "user",
			Content: "<media:image id=\"img-1\">\n\n你再看一下",
			MediaRefs: []providers.MediaRef{{
				ID:       "img-1",
				Kind:     "image",
				MimeType: "image/png",
				Path:     "/tmp/look.png",
			}},
		},
	}

	flush := loop.makeFlushMessages(req)
	if err := flush(context.Background(), req.SessionKey, []providers.Message{{Role: "assistant", Content: "看到了"}}); err != nil {
		t.Fatal(err)
	}

	if len(store.messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(store.messages))
	}
	input := store.messages[0]
	if input.Role != "user" || len(input.MediaRefs) != 1 || input.MediaRefs[0].ID != "img-1" {
		t.Fatalf("input was not persisted with media refs: %+v", input)
	}
	if input.Content != "<media:image id=\"img-1\">\n\n你再看一下" {
		t.Fatalf("input content = %q", input.Content)
	}
}
