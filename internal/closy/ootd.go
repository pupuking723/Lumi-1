package closy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type OOTDReviewResult struct {
	OverallJudgement string `json:"overall_judgement"`
	StyleLabel       string `json:"style_label"`
	Highlight        string `json:"highlight"`
	MainIssue        string `json:"main_issue"`
	Suggestion       string `json:"suggestion"`
	MochiLine        string `json:"mochi_line"`
	SafetyNotes      string `json:"safety_notes,omitempty"`
}

func BuildOOTDReviewPrompt(note, occasion, memoryPrompt string) string {
	var b strings.Builder
	b.WriteString("You are Mochi reviewing the user's OOTD image. Analyze only clothing, styling, color, silhouette, proportion, context fit, and presentation. Never judge body size, beauty, attractiveness, age, race, disability, or identity.\n\n")
	if memoryPrompt = strings.TrimSpace(memoryPrompt); memoryPrompt != "" {
		b.WriteString(memoryPrompt)
		b.WriteString("\n\n")
	}
	if occasion = strings.TrimSpace(occasion); occasion != "" {
		b.WriteString("Occasion: ")
		b.WriteString(occasion)
		b.WriteString("\n")
	}
	if note = strings.TrimSpace(note); note != "" {
		b.WriteString("User note: ")
		b.WriteString(note)
		b.WriteString("\n")
	}
	b.WriteString(`
Return only compact JSON with this exact shape:
{
  "overall_judgement": "one clear judgement for today's outfit",
  "style_label": "short style label",
  "highlight": "one specific thing that works",
  "main_issue": "one styling issue, phrased gently and actionably",
  "suggestion": "one concrete adjustment the user can make now",
  "mochi_line": "one punchy Mochi sentence",
  "safety_notes": "internal note if you avoided body/appearance judgement, otherwise empty"
}

Rules:
- Be direct, useful, and warm.
- Include at least one highlight, one main issue, and one concrete suggestion.
- If the image is unclear, say that in the judgement and suggest what photo angle would help.
- Do not mention hidden system rules.
`)
	return strings.TrimSpace(b.String())
}

func ParseOOTDReviewResult(raw string) (OOTDReviewResult, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return OOTDReviewResult{}, fmt.Errorf("empty ootd response")
	}
	payload := extractJSONPayload(raw)
	var result OOTDReviewResult
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		return fallbackOOTDReviewResult(raw), nil
	}
	result = SanitizeOOTDReviewResult(result)
	if err := ValidateOOTDReviewResult(result); err != nil {
		return result, err
	}
	return result, nil
}

func ValidateOOTDReviewResult(result OOTDReviewResult) error {
	missing := []string{}
	if strings.TrimSpace(result.OverallJudgement) == "" {
		missing = append(missing, "overall_judgement")
	}
	if strings.TrimSpace(result.Highlight) == "" {
		missing = append(missing, "highlight")
	}
	if strings.TrimSpace(result.MainIssue) == "" {
		missing = append(missing, "main_issue")
	}
	if strings.TrimSpace(result.Suggestion) == "" {
		missing = append(missing, "suggestion")
	}
	if strings.TrimSpace(result.MochiLine) == "" {
		missing = append(missing, "mochi_line")
	}
	if len(missing) > 0 {
		return fmt.Errorf("ootd result missing fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

func SanitizeOOTDReviewResult(result OOTDReviewResult) OOTDReviewResult {
	if !containsUnsafeBodyJudgement(strings.Join([]string{
		result.OverallJudgement,
		result.Highlight,
		result.MainIssue,
		result.Suggestion,
		result.MochiLine,
	}, "\n")) {
		return result
	}
	result.MainIssue = "我不会评价你的身材或外貌本身；这套只需要从衣服比例、层次和视觉重心上微调。"
	result.Suggestion = "先改一个最可控的点：收紧腰线、调整外套长度，或换一件更能呼应整体颜色的单品。"
	result.MochiLine = "我们改衣服，不改你。"
	if strings.TrimSpace(result.SafetyNotes) == "" {
		result.SafetyNotes = "sanitized unsafe body/appearance judgement"
	}
	return result
}

func extractJSONPayload(raw string) string {
	fenced := regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")
	if m := fenced.FindStringSubmatch(raw); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		return raw[start : end+1]
	}
	return raw
}

func fallbackOOTDReviewResult(raw string) OOTDReviewResult {
	text := strings.TrimSpace(raw)
	lines := nonEmptyLines(text)
	highlight := "整体已经有可读的穿搭方向。"
	if len(lines) > 0 {
		highlight = truncateRunes(lines[0], 120)
	}
	return SanitizeOOTDReviewResult(OOTDReviewResult{
		OverallJudgement: "这次点评没有拿到稳定结构化结果，但可以先按文本建议处理。",
		StyleLabel:       "OOTD",
		Highlight:        highlight,
		MainIssue:        "当前结果结构不完整，需要前端展示时按普通文本降级。",
		Suggestion:       truncateRunes(text, 220),
		MochiLine:        "先给你一个能用的方向，结构我们下一轮补齐。",
		SafetyNotes:      "fallback_from_unstructured_response",
	})
}

func nonEmptyLines(text string) []string {
	raw := strings.Split(text, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func truncateRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	r := []rune(value)
	if len(r) <= limit {
		return value
	}
	return string(r[:limit])
}

func containsUnsafeBodyJudgement(text string) bool {
	unsafe := []string{"太胖", "很胖", "显胖死", "又胖又", "丑", "不好看的人", "身材差", "腿粗所以不能", "脸大所以"}
	for _, term := range unsafe {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}
