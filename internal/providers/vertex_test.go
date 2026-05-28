package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVertexProviderChatUsesGenerateContent(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotBody vertexGeminiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{
			"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],
			"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}
		}`))
	}))
	defer srv.Close()

	p := NewVertexProvider(
		"vertex-test",
		"test-token",
		srv.URL+"/v1",
		"gemini-2.5-flash",
		WithVertexProjectID("project-1"),
		WithVertexLocation("us-central1"),
	)
	resp, err := p.Chat(context.Background(), ChatRequest{
		Messages: []Message{
			{Role: "system", Content: "be concise"},
			{Role: "user", Content: "hello", Images: []ImageContent{{MimeType: "image/png", Data: "AA=="}}},
		},
		Tools: []ToolDefinition{{
			Type: "function",
			Function: &ToolFunctionSchema{
				Name:        "pick_outfit",
				Description: "Pick outfit",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("content = %q, want ok", resp.Content)
	}
	wantPath := "/v1/projects/project-1/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent"
	if gotPath != wantPath {
		t.Fatalf("path = %q, want %q", gotPath, wantPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotBody.SystemInstruction == nil || gotBody.SystemInstruction.Parts[0].Text != "be concise" {
		t.Fatalf("system instruction not serialized: %#v", gotBody.SystemInstruction)
	}
	if len(gotBody.Contents) != 1 || len(gotBody.Contents[0].Parts) != 2 {
		t.Fatalf("contents not serialized with text+image: %#v", gotBody.Contents)
	}
	if len(gotBody.Tools) != 1 || gotBody.Tools[0].FunctionDeclarations[0].Name != "pick_outfit" {
		t.Fatalf("tools not serialized: %#v", gotBody.Tools)
	}
}

func TestVertexRequestGroupsConsecutiveToolResponses(t *testing.T) {
	req := ChatRequest{Messages: []Message{
		{Role: "user", Content: "use tools"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "read_image", Arguments: map[string]any{"path": "a.png"}},
			{ID: "call_2", Name: "skill_search", Arguments: map[string]any{"q": "ootd"}},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: "image ok"},
		{Role: "tool", ToolCallID: "call_2", Content: "skill ok"},
		{Role: "assistant", Content: "done"},
	}}

	got := vertexRequestFromChat(req)
	if len(got.Contents) != 4 {
		t.Fatalf("contents len = %d, want 4: %#v", len(got.Contents), got.Contents)
	}
	toolTurn := got.Contents[2]
	if toolTurn.Role != "function" {
		t.Fatalf("tool turn role = %q, want function", toolTurn.Role)
	}
	if len(toolTurn.Parts) != 2 {
		t.Fatalf("function response parts len = %d, want 2: %#v", len(toolTurn.Parts), toolTurn.Parts)
	}
	if toolTurn.Parts[0].FunctionResponse == nil || toolTurn.Parts[0].FunctionResponse.Name != "read_image" {
		t.Fatalf("first response not mapped to read_image: %#v", toolTurn.Parts[0])
	}
	if toolTurn.Parts[1].FunctionResponse == nil || toolTurn.Parts[1].FunctionResponse.Name != "skill_search" {
		t.Fatalf("second response not mapped to skill_search: %#v", toolTurn.Parts[1])
	}
}

func TestVertexProviderChatStreamParsesSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.String(), ":streamGenerateContent?alt=sse") {
			t.Fatalf("unexpected stream URL: %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"he\"}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"llo\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"totalTokenCount\":4}}\n\n"))
	}))
	defer srv.Close()

	p := NewVertexProvider("vertex-test", "test-token", srv.URL+"/v1", "gemini-2.5-flash", WithVertexProjectID("p"))
	var chunks []string
	resp, err := p.ChatStream(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}}, func(chunk StreamChunk) {
		if chunk.Content != "" {
			chunks = append(chunks, chunk.Content)
		}
	})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if resp.Content != "hello" {
		t.Fatalf("content = %q, want hello", resp.Content)
	}
	if strings.Join(chunks, "") != "hello" {
		t.Fatalf("chunks = %v", chunks)
	}
}

func TestVertexEndpointBaseURL(t *testing.T) {
	if got := VertexEndpointBaseURL("us-central1", "v1"); got != "https://us-central1-aiplatform.googleapis.com/v1" {
		t.Fatalf("us-central1 base = %q", got)
	}
	if got := VertexEndpointBaseURL("global", "v1beta1"); got != "https://aiplatform.googleapis.com/v1beta1" {
		t.Fatalf("global base = %q", got)
	}
}

func TestVertexProjectIDErrorIsActionable(t *testing.T) {
	p := NewVertexProvider("vertex-test", "test-token", "", "gemini-2.5-flash")
	_, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected project ID error")
	}
	for _, want := range []string{"provider settings.project_id", "GOCLAW_VERTEX_PROJECT_ID", "projects/<project>/locations/<location>"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not include %q", err.Error(), want)
		}
	}
}

func TestVertexHTTPErrorAddsAuthAndRoutingHints(t *testing.T) {
	cases := []struct {
		statusCode int
		status     string
		want       string
	}{
		{statusCode: http.StatusUnauthorized, status: "401 Unauthorized", want: "check credentials"},
		{statusCode: http.StatusForbidden, status: "403 Forbidden", want: "Vertex AI API is enabled"},
		{statusCode: http.StatusNotFound, status: "404 Not Found", want: "check project_id, location, and model id"},
	}
	for _, tc := range cases {
		err := (&vertexHTTPError{statusCode: tc.statusCode, status: tc.status, body: `{"error":"nope"}`}).Error()
		if !strings.Contains(err, tc.want) {
			t.Fatalf("%s error = %q, want %q", tc.status, err, tc.want)
		}
	}
}
