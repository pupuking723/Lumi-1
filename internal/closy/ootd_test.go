package closy

import (
	"strings"
	"testing"
)

func TestBuildOOTDReviewPromptContainsSchemaAndSafety(t *testing.T) {
	prompt := BuildOOTDReviewPrompt("想低调一点", "date night", "<MOCHI_MEMORY>\n- color.avoid: neon\n</MOCHI_MEMORY>")
	for _, want := range []string{"Return only compact JSON", "overall_judgement", "date night", "想低调一点", "Never judge body size", "<MOCHI_MEMORY>"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestParseOOTDReviewResultFromFencedJSON(t *testing.T) {
	result, err := ParseOOTDReviewResult("```json\n{\"overall_judgement\":\"能出门\",\"style_label\":\"clean casual\",\"highlight\":\"颜色很干净\",\"main_issue\":\"鞋子有点断层\",\"suggestion\":\"换浅色鞋\",\"mochi_line\":\"人会问链接。\"}\n```")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if result.OverallJudgement != "能出门" || result.Suggestion != "换浅色鞋" {
		t.Fatalf("result = %#v", result)
	}
}

func TestSanitizeOOTDReviewResultRemovesBodyJudgement(t *testing.T) {
	result := SanitizeOOTDReviewResult(OOTDReviewResult{
		OverallJudgement: "显胖",
		Highlight:        "颜色可以",
		MainIssue:        "腿粗所以不能穿",
		Suggestion:       "别穿",
		MochiLine:        "不行",
	})
	if strings.Contains(result.MainIssue, "腿粗") || !strings.Contains(result.MochiLine, "改衣服") || result.SafetyNotes == "" {
		t.Fatalf("result = %#v", result)
	}
}
