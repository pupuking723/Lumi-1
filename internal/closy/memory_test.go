package closy

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func TestExtractMemoriesFindsPreferencesAndAvoidances(t *testing.T) {
	agentID := uuid.New()
	prefs, profile := ExtractMemories(ExtractMemoryParams{
		UserID:      "user-a",
		AgentID:     agentID,
		SessionKey:  "agent:closy:cchat:direct:user-a-fit",
		UserMessage: "我喜欢干净松弛的风格，也常穿黑白灰。我不喜欢太紧身的裙子，今天有点没自信。",
	})
	if profile == nil || !strings.Contains(profile.CurrentStateSummary, "没自信") {
		t.Fatalf("profile = %#v", profile)
	}
	var hasStyle, hasAvoid bool
	for _, p := range prefs {
		if p.Polarity == store.ClosyPrefPolarityLike && strings.Contains(p.Value, "干净松弛") {
			hasStyle = true
		}
		if p.Polarity == store.ClosyPrefPolarityAvoid && strings.Contains(p.Value, "紧身") {
			hasAvoid = true
		}
		if p.UserID != "user-a" || p.AgentID != agentID || p.SourceSessionKey == "" {
			t.Fatalf("bad preference scope: %#v", p)
		}
	}
	if !hasStyle || !hasAvoid {
		t.Fatalf("prefs = %#v", prefs)
	}
}

func TestBuildMemoryPromptUsesNaturalReferenceGuidance(t *testing.T) {
	prompt := BuildMemoryPrompt(&store.ClosyProfileData{StyleSummary: "clean and relaxed"}, []store.ClosyStylePreferenceData{
		{Category: store.ClosyPrefCategoryColor, Polarity: store.ClosyPrefPolarityAvoid, Value: "neon green"},
	})
	for _, want := range []string{"<MOCHI_MEMORY>", "style_summary: clean and relaxed", "color.avoid: neon green", "Do not recite"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
