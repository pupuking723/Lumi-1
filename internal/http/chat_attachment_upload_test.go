package http

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	mediapkg "github.com/nextlevelbuilder/goclaw/internal/channels/media"
	mediastore "github.com/nextlevelbuilder/goclaw/internal/media"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

type fakeMediaAssetStore struct {
	created *store.MediaAssetData
	byID    map[uuid.UUID]*store.MediaAssetData
}

func (f *fakeMediaAssetStore) CreateMediaAsset(ctx context.Context, p store.CreateMediaAssetParams) (*store.MediaAssetData, error) {
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		tid = store.MasterTenantID
	}
	now := time.Now().UTC()
	a := &store.MediaAssetData{
		ID:               p.ID,
		TenantID:         tid,
		UserID:           p.UserID,
		SessionID:        p.SessionID,
		AgentID:          p.AgentID,
		OriginalFilename: p.OriginalFilename,
		MimeType:         p.MimeType,
		Size:             p.Size,
		SHA256:           p.SHA256,
		StorageBackend:   p.StorageBackend,
		StorageBucket:    p.StorageBucket,
		StorageKey:       p.StorageKey,
		Status:           p.Status,
		Visibility:       p.Visibility,
		Metadata:         p.Metadata,
		ExpiresAt:        p.ExpiresAt,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	f.created = a
	if f.byID == nil {
		f.byID = make(map[uuid.UUID]*store.MediaAssetData)
	}
	f.byID[a.ID] = a
	return a, nil
}

func (f *fakeMediaAssetStore) GetMediaAsset(_ context.Context, id uuid.UUID) (*store.MediaAssetData, error) {
	if f.byID == nil {
		return nil, nil
	}
	return f.byID[id], nil
}

func TestChatAttachmentUploadHandlerCreatesMediaAsset(t *testing.T) {
	InitGatewayToken("test-token")
	InitOwnerIDs([]string{"system"})
	t.Cleanup(func() {
		InitGatewayToken("")
		InitOwnerIDs(nil)
	})

	ms, err := mediastore.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("media store: %v", err)
	}
	assets := &fakeMediaAssetStore{}
	h := NewChatAttachmentUploadHandler(ms, assets)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "outfit.png")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := fw.Write([]byte("fake image bytes")); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.WriteField("session_id", "session-a"); err != nil {
		t.Fatalf("write field: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/attachments/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-GoClaw-User-Id", "system")
	rr := httptest.NewRecorder()

	h.RegisterRoutes(http.NewServeMux())
	requireAuth("", h.handleUpload)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if assets.created == nil {
		t.Fatal("expected media asset to be created")
	}
	if assets.created.OriginalFilename != "outfit.png" {
		t.Fatalf("filename=%q", assets.created.OriginalFilename)
	}
	if assets.created.UserID != "system" {
		t.Fatalf("user_id=%q", assets.created.UserID)
	}
	if assets.created.SessionID == nil || *assets.created.SessionID != "session-a" {
		t.Fatalf("session_id=%v", assets.created.SessionID)
	}
	if assets.created.StorageBackend != store.MediaStorageLocal {
		t.Fatalf("storage=%q", assets.created.StorageBackend)
	}
	if _, err := os.Stat(assets.created.StorageKey); err != nil {
		t.Fatalf("stored file missing: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response json: %v", err)
	}
	if resp["media_id"] == "" || resp["filename"] != "outfit.png" || resp["status"] != store.MediaStatusReady {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestChatCompletionsResolveAttachments(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), "asset-*.png")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	if _, err := tmp.Write([]byte("image")); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatalf("close temp: %v", err)
	}

	id := uuid.New()
	assets := &fakeMediaAssetStore{byID: map[uuid.UUID]*store.MediaAssetData{
		id: {
			ID:               id,
			TenantID:         store.MasterTenantID,
			UserID:           "system",
			OriginalFilename: "look.png",
			MimeType:         "image/png",
			Size:             5,
			StorageBackend:   store.MediaStorageLocal,
			StorageKey:       tmp.Name(),
			Status:           store.MediaStatusReady,
			Visibility:       "private",
		},
	}}
	h := &ChatCompletionsHandler{}
	h.SetMediaAssetStore(assets)

	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	files, infos, resolved, cleanup, err := h.resolveAttachments(ctx, []chatAttachment{{MediaID: id.String(), Caption: "today look", Source: "camera", Role: "current"}})
	if err != nil {
		t.Fatalf("resolve attachments: %v", err)
	}
	defer cleanup()
	if len(files) != 1 || files[0].ID != id.String() || files[0].Path != tmp.Name() || files[0].Filename != "look.png" {
		t.Fatalf("files=%#v", files)
	}
	if len(infos) != 1 || infos[0].Type != "image" || infos[0].FileName != "look.png" {
		t.Fatalf("infos=%#v", infos)
	}
	if len(resolved) != 1 || resolved[0].MediaID != id.String() || resolved[0].Caption != "today look" || resolved[0].Source != "camera" || resolved[0].Role != "current" {
		t.Fatalf("resolved=%#v", resolved)
	}
}

func TestChatCompletionSessionKeyUsesStableCChatSessionID(t *testing.T) {
	got := chatCompletionSessionKey("closy", "user-a", "ootd-1", "12345678-abcd")
	want := "agent:closy:cchat:direct:user-a-ootd-1"
	if got != want {
		t.Fatalf("session key = %q, want %q", got, want)
	}
}

func TestBuildChatCompletionUserMessageAddsMultimodalContext(t *testing.T) {
	msg := buildChatCompletionUserMessage("你再看一下", chatCompletionsRequest{
		Scenario: "outfit_review",
		InputContext: chatInputContext{
			Source:          "live_voice",
			VoiceTranscript: "我纠结外套",
			RefersToMediaID: "media-1",
		},
	}, []mediapkg.MediaInfo{{Type: mediapkg.TypeImage, FileName: "look.png", ContentType: "image/png"}}, []resolvedChatAttachment{{
		MediaID:  "media-1",
		Kind:     "image",
		Filename: "look.png",
		MIMEType: "image/png",
		Caption:  "OOTD",
		Source:   "camera",
		Role:     "current",
	}}, "agent:closy:cchat:direct:user-a-ootd-1")

	for _, want := range []string{
		"<media:image>",
		"<mochi_multimodal_context>",
		"scenario: outfit_review",
		"voice_transcript: 我纠结外套",
		"attachment_1: media_id=media-1",
		"你再看一下",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestBuildChatCompletionUserMessagePlainTextDoesNotAddContextBlock(t *testing.T) {
	msg := buildChatCompletionUserMessage("hello", chatCompletionsRequest{}, nil, nil, "agent:closy:http:test")
	if msg != "hello" {
		t.Fatalf("message = %q, want plain text", msg)
	}
}
