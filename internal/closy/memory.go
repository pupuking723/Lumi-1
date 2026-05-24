package closy

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

const memoryPromptFile = "MOCHI_MEMORY"

type ExtractMemoryParams struct {
	UserID           string
	AgentID          uuid.UUID
	UserMessage      string
	AssistantMessage string
	SessionKey       string
}

func BuildMemoryPrompt(profile *store.ClosyProfileData, prefs []store.ClosyStylePreferenceData) string {
	var lines []string
	add := func(label, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			lines = append(lines, "- "+label+": "+value)
		}
	}
	if profile != nil {
		add("style_summary", profile.StyleSummary)
		add("self_expression", profile.SelfExpressionSummary)
		add("social_presentation", profile.SocialPresentationSummary)
		add("current_state", profile.CurrentStateSummary)
	}
	for _, p := range prefs {
		value := strings.TrimSpace(p.Value)
		if value == "" {
			continue
		}
		label := p.Category
		if p.Polarity != "" {
			label += "." + p.Polarity
		}
		add(label, value)
	}
	if len(lines) == 0 {
		return ""
	}
	return fmt.Sprintf("<%s>\nUse these user memories naturally when they help. Do not recite the list mechanically; prefer brief, specific references like \"I remember you avoid ...\" only when useful.\n%s\n</%s>", memoryPromptFile, strings.Join(lines, "\n"), memoryPromptFile)
}

func BuildMemoryPromptForUser(ctx context.Context, mem store.ClosyMemoryStore, agentID uuid.UUID, userID string) string {
	if mem == nil || agentID == uuid.Nil || strings.TrimSpace(userID) == "" {
		return ""
	}
	profile, _ := mem.GetClosyProfile(ctx, agentID, userID)
	prefs, _ := mem.ListClosyStylePreferences(ctx, agentID, userID, 30)
	return BuildMemoryPrompt(profile, prefs)
}

func ExtractMemories(params ExtractMemoryParams) ([]store.UpsertClosyStylePreferenceParams, *store.UpsertClosyProfileParams) {
	userText := strings.TrimSpace(params.UserMessage)
	if userText == "" || params.AgentID == uuid.Nil || strings.TrimSpace(params.UserID) == "" {
		return nil, nil
	}
	plain := stripContextBlocks(userText)
	var prefs []store.UpsertClosyStylePreferenceParams
	addPref := func(category, polarity, value string, confidence float64) {
		value = normalizeMemoryValue(value)
		if value == "" || len([]rune(value)) > 80 {
			return
		}
		prefs = append(prefs, store.UpsertClosyStylePreferenceParams{
			UserID:           params.UserID,
			AgentID:          params.AgentID,
			Category:         category,
			Polarity:         polarity,
			Value:            value,
			Evidence:         truncateMemoryEvidence(plain),
			SourceSessionKey: params.SessionKey,
			Confidence:       confidence,
		})
	}

	for _, value := range extractByPatterns(plain, []string{
		`(?i)(?:我|本人)?(?:喜欢|偏爱|爱穿|常穿|更想要|想走|想尝试)\s*([^。.!！？\n]+)`,
		`(?i)(?:我的风格|我想要的风格|风格方向)(?:是|偏|偏向|想走)?\s*([^。.!！？\n]+)`,
	}) {
		addPref(classifyMemoryCategory(value, store.ClosyPrefCategoryStyle), store.ClosyPrefPolarityLike, value, 0.74)
	}
	for _, value := range extractByPatterns(plain, []string{
		`(?i)(?:我|本人)?(?:不喜欢|不想要|不想穿|不要|避开|雷区是|讨厌)\s*([^。.!！？\n]+)`,
	}) {
		addPref(classifyMemoryCategory(value, store.ClosyPrefCategoryAvoidance), store.ClosyPrefPolarityAvoid, value, 0.82)
	}
	for _, value := range extractByPatterns(plain, []string{
		`(?i)(?:通勤|约会|面试|见朋友|上课|上班|聚会|婚礼|旅行|拍照)[^。.!！？\n]{0,16}(?:要穿|想穿|需要|场景)\s*([^。.!！？\n]+)`,
	}) {
		addPref(store.ClosyPrefCategoryOccasion, store.ClosyPrefPolarityNeutral, value, 0.68)
	}
	for _, value := range extractByPatterns(plain, []string{
		`(?i)(?:买了|下单了|最后选了|决定穿|今天穿)\s*([^。.!！？\n]+)`,
	}) {
		addPref(store.ClosyPrefCategoryChoice, store.ClosyPrefPolarityNeutral, value, 0.65)
	}

	var profile *store.UpsertClosyProfileParams
	if state := extractFirst(plain, []string{
		`(?i)(?:我今天|今天|最近)(?:状态|感觉|心情)?\s*(没状态|有点没自信|怕太用力|不想出门|想低调|想被看见|想有气场|想显得松弛)`,
	}); state != "" {
		profile = &store.UpsertClosyProfileParams{
			UserID:              params.UserID,
			AgentID:             params.AgentID,
			CurrentStateSummary: normalizeMemoryValue(state),
			Confidence:          0.65,
		}
	}
	return dedupePrefs(prefs), profile
}

func PersistExtractedMemories(ctx context.Context, mem store.ClosyMemoryStore, params ExtractMemoryParams) {
	if mem == nil {
		return
	}
	prefs, profile := ExtractMemories(params)
	if profile != nil {
		_, _ = mem.UpsertClosyProfile(ctx, *profile)
	}
	for _, p := range prefs {
		_, _ = mem.UpsertClosyStylePreference(ctx, p)
	}
}

func stripContextBlocks(text string) string {
	for _, tag := range []string{"mochi_multimodal_context", "mochi_live_media_context"} {
		re := regexp.MustCompile(`(?s)<` + tag + `>.*?</` + tag + `>`)
		text = re.ReplaceAllString(text, " ")
	}
	return strings.TrimSpace(text)
}

func extractByPatterns(text string, patterns []string) []string {
	var out []string
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			if len(m) > 1 {
				out = append(out, m[1])
			}
		}
	}
	return out
}

func extractFirst(text string, patterns []string) string {
	for _, v := range extractByPatterns(text, patterns) {
		if v = normalizeMemoryValue(v); v != "" {
			return v
		}
	}
	return ""
}

func normalizeMemoryValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, " ，,。.!！？:：；;\"'")
	value = strings.Join(strings.Fields(value), " ")
	return value
}

func classifyMemoryCategory(value, fallback string) string {
	v := strings.ToLower(value)
	switch {
	case strings.ContainsAny(value, "黑白灰红蓝绿黄紫粉棕咖杏米") || strings.Contains(v, "color") || strings.Contains(value, "颜色") || strings.Contains(value, "色"):
		return store.ClosyPrefCategoryColor
	case strings.Contains(value, "高腰") || strings.Contains(value, "宽松") || strings.Contains(value, "修身") || strings.Contains(value, "廓形") || strings.Contains(value, "版型") || strings.Contains(value, "显腿") || strings.Contains(value, "比例"):
		return store.ClosyPrefCategorySilhouette
	}
	return fallback
}

func truncateMemoryEvidence(value string) string {
	value = strings.TrimSpace(value)
	r := []rune(value)
	if len(r) > 180 {
		return string(r[:180])
	}
	return value
}

func dedupePrefs(in []store.UpsertClosyStylePreferenceParams) []store.UpsertClosyStylePreferenceParams {
	seen := map[string]bool{}
	out := make([]store.UpsertClosyStylePreferenceParams, 0, len(in))
	for _, p := range in {
		key := p.Category + "\x00" + p.Polarity + "\x00" + strings.ToLower(p.Value)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out
}
