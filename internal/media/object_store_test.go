package media

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestObjectStoreBuildsStableOSSKeysAndPublicURLs(t *testing.T) {
	store, err := NewObjectStore(ObjectStoreConfig{
		AccessKeyID:     "ak",
		AccessKeySecret: "secret",
		Bucket:          "bucket",
		Prefix:          "uploads/chat",
		PublicBaseURL:   "https://cdn.example.com/media",
	})
	if err != nil {
		t.Fatalf("new object store: %v", err)
	}

	key := store.ObjectKey(uuid.Nil, "google.user@example.com", "session-1", "media-1", ".png")
	if !strings.HasPrefix(key, "uploads/chat/tenants/master/users/") || !strings.HasSuffix(key, "/media-1.png") {
		t.Fatalf("key = %q", key)
	}
	got, err := store.URL(context.Background(), key)
	if err != nil {
		t.Fatalf("url: %v", err)
	}
	want := "https://cdn.example.com/media/uploads/chat/tenants/master/users/"
	if !strings.HasPrefix(got, want) || !strings.HasSuffix(got, "/media-1.png") {
		t.Fatalf("url = %q", got)
	}

	artifactKey := store.ArtifactKey(uuid.Nil, "closy", "artifact-1", ".webp")
	if !strings.HasPrefix(artifactKey, "uploads/chat/tenants/master/artifacts/") || !strings.HasSuffix(artifactKey, "/artifact-1.webp") {
		t.Fatalf("artifact key = %q", artifactKey)
	}
}
