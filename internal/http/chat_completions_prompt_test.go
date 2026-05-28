package http

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/closy"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/sessions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func TestBuildChatExtraSystemPromptKeepsMemoryForTextChat(t *testing.T) {
	prompt := buildChatExtraSystemPrompt(closy.AgentKey, false, "<MOCHI_MEMORY>likes black</MOCHI_MEMORY>", "")
	if !strings.Contains(prompt, "<MOCHI_MEMORY>") {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestBuildChatExtraSystemPromptIsGroundedForImageChat(t *testing.T) {
	prompt := buildChatExtraSystemPrompt(closy.AgentKey, true, "<MOCHI_MEMORY>likes black</MOCHI_MEMORY>", "")
	for _, want := range []string{
		"Answer conversationally",
		"Do not output JSON",
		"Use only visible evidence from the current attached image",
		"Do not mention chat memory",
		"visible wearable outfit",
		"Do not ask the user to resend solely because the image is low-resolution",
		"Do not say the image is broken or unavailable unless the system actually returned an attachment/file error",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "<MOCHI_MEMORY>") || strings.Contains(prompt, "likes black") {
		t.Fatalf("image chat prompt should not include memory:\n%s", prompt)
	}
}

func TestBuildChatExtraSystemPromptIncludesOOTDContextForFollowUp(t *testing.T) {
	context := "<MOCHI_OOTD_REPORT_CONTEXT>\nTitle: Quiet office polish\n</MOCHI_OOTD_REPORT_CONTEXT>"
	prompt := buildChatExtraSystemPrompt(closy.AgentKey, false, "", context)
	if !strings.Contains(prompt, "MOCHI_OOTD_REPORT_CONTEXT") || !strings.Contains(prompt, "Quiet office polish") {
		t.Fatalf("prompt missing OOTD context:\n%s", prompt)
	}
}

func TestOOTDReportPromptContextReadsOwnedReport(t *testing.T) {
	reportID := uuid.New()
	handler := &ChatCompletionsHandler{
		closyOOTD: &fakeOOTDStore{byID: map[uuid.UUID]*store.ClosyOOTDReviewData{
			reportID: {
				BaseModel:  store.BaseModel{ID: reportID},
				UserID:     "user-a",
				Status:     store.ClosyOOTDStatusCompleted,
				ReportJSON: json.RawMessage(testOOTDReportJSON()),
			},
		}},
	}
	context := handler.ootdReportPromptContext(context.Background(), closy.AgentKey, "user-a", chatInputContext{
		RefersToOOTDReportID: reportID.String(),
	})
	if !strings.Contains(context, "MOCHI_OOTD_REPORT_CONTEXT") || !strings.Contains(context, "城市休闲极简主义") {
		t.Fatalf("context = %q", context)
	}
}

func TestOOTDReportPromptContextRejectsDifferentOwner(t *testing.T) {
	reportID := uuid.New()
	handler := &ChatCompletionsHandler{
		closyOOTD: &fakeOOTDStore{byID: map[uuid.UUID]*store.ClosyOOTDReviewData{
			reportID: {
				BaseModel:  store.BaseModel{ID: reportID},
				UserID:     "other-user",
				Status:     store.ClosyOOTDStatusCompleted,
				ReportJSON: json.RawMessage(testOOTDReportJSON()),
			},
		}},
	}
	context := handler.ootdReportPromptContext(context.Background(), closy.AgentKey, "user-a", chatInputContext{
		RefersToOOTDReportID: reportID.String(),
	})
	if context != "" {
		t.Fatalf("context should be empty for owner mismatch, got %q", context)
	}
}

func TestCleanChatCompletionSessionDropsOOTDReportJSONHistory(t *testing.T) {
	ctx := context.Background()
	sessionKey := "agent:closy:cchat:direct:user-a-chat-1"
	sessionStore := sessions.NewManager("")
	sessionStore.AddMessage(ctx, sessionKey, providers.Message{Role: "user", Content: "Can you review this look?"})
	sessionStore.AddMessage(ctx, sessionKey, providers.Message{Role: "assistant", Content: testOOTDReportJSON()})
	sessionStore.AddMessage(ctx, sessionKey, providers.Message{Role: "user", Content: "What about shoes?"})
	sessionStore.AddMessage(ctx, sessionKey, providers.Message{Role: "assistant", Content: "Try a sharper loafer."})
	sessionStore.SetSummary(ctx, sessionKey, testOOTDReportJSON())

	cleanChatCompletionSession(ctx, sessionStore, sessionKey)

	history := sessionStore.GetHistory(ctx, sessionKey)
	if len(history) != 3 {
		t.Fatalf("history len = %d, want 3: %#v", len(history), history)
	}
	for _, msg := range history {
		if strings.Contains(msg.Content, `"todayJudgment"`) {
			t.Fatalf("OOTD report JSON still in history: %#v", history)
		}
	}
	if summary := sessionStore.GetSummary(ctx, sessionKey); summary != "" {
		t.Fatalf("summary = %q, want empty", summary)
	}
}

func TestNormalizeChatAssistantContentConvertsOOTDReportJSON(t *testing.T) {
	got := normalizeChatAssistantContent(testOOTDReportJSON())
	for _, forbidden := range []string{`"todayJudgment"`, `"shareCard"`, `"palette"`} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("normalized reply leaked %s: %s", forbidden, got)
		}
	}
	for _, want := range []string{"城市休闲极简主义", "5.5/10", "换一只更利落的包", "底子不差"} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized reply missing %q: %s", want, got)
		}
	}
}

func TestStructuredOOTDStreamGuardReleasesNaturalLanguageImmediately(t *testing.T) {
	guard := newStructuredOOTDStreamGuard(true)

	release, ok := guard.Push("这套适合上班，")
	if !ok || release != "这套适合上班，" {
		t.Fatalf("first push = (%q, %v), want immediate natural language release", release, ok)
	}

	release, ok = guard.Push("但鞋子可以换轻一点。")
	if !ok || release != "但鞋子可以换轻一点。" {
		t.Fatalf("second push = (%q, %v), want passthrough after decision", release, ok)
	}

	if final := guard.Final("这套适合上班，但鞋子可以换轻一点。"); final != "" {
		t.Fatalf("final = %q, want no duplicate final content after passthrough", final)
	}
}

func TestStructuredOOTDStreamGuardBuffersAndConvertsReportJSON(t *testing.T) {
	guard := newStructuredOOTDStreamGuard(true)

	release, ok := guard.Push("{\n  \"todayJudgment\": ")
	if ok || release != "" {
		t.Fatalf("first push = (%q, %v), want buffered structured JSON", release, ok)
	}

	final := guard.Final(testOOTDReportJSON())
	for _, forbidden := range []string{`"todayJudgment"`, `"shareCard"`, `"palette"`} {
		if strings.Contains(final, forbidden) {
			t.Fatalf("final leaked %s: %s", forbidden, final)
		}
	}
	for _, want := range []string{"城市休闲极简主义", "5.5/10", "换一只更利落的包"} {
		if !strings.Contains(final, want) {
			t.Fatalf("final missing %q: %s", want, final)
		}
	}
}
