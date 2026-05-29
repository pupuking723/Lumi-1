package closy

import (
	"errors"
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

func TestBuildOOTDReportPromptContainsSkillSchemaAndSafety(t *testing.T) {
	prompt := BuildOOTDReportPrompt("SKILL: only structured OOTD JSON", "想低调一点", "date", "<MOCHI_MEMORY>\n- color.avoid: neon\n</MOCHI_MEMORY>", "en")
	for _, want := range []string{
		"SKILL: only structured OOTD JSON",
		"Return only compact JSON",
		"todayJudgment",
		"shareCard",
		"date",
		"想低调一点",
		"body shaming",
		"Use only visible evidence from the current image",
		"visible wearable outfit",
		"Output language: en",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "<MOCHI_MEMORY>") || strings.Contains(prompt, "color.avoid") {
		t.Fatalf("report prompt should not include chat memory:\n%s", prompt)
	}
}

func TestParseOOTDReportValidJSON(t *testing.T) {
	report, err := ParseOOTDReport(validOOTDReportJSON())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if report.TodayJudgment.Title != "城市休闲极简主义" || report.TodayJudgment.Score != 5.5 {
		t.Fatalf("todayJudgment = %#v", report.TodayJudgment)
	}
	if len(report.Highlights) != 2 || report.Palette[0].Hex != "#1A1A1A" || report.ShareCard.CTA == "" {
		t.Fatalf("report = %#v", report)
	}
}

func TestParseOOTDReportRejectsInvalidShape(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "missing core title", raw: `{"todayJudgment":{"score":8,"label":"ok"}}`},
		{name: "missing core label", raw: `{"todayJudgment":{"title":"x","score":8}}`},
		{name: "score out of range", raw: strings.Replace(validOOTDReportJSON(), `"score":5.5`, `"score":11`, 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseOOTDReport(tt.raw); err == nil {
				t.Fatalf("expected parse error")
			}
		})
	}
}

func TestParseOOTDReportAcceptsCoreOnlyReport(t *testing.T) {
	report, err := ParseOOTDReport(`{"todayJudgment":{"title":"Clean enough","score":7,"label":"Wearable"}}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if report.TodayJudgment.Title != "Clean enough" || report.TodayJudgment.Score != 7 || report.TodayJudgment.Label != "Wearable" {
		t.Fatalf("todayJudgment = %#v", report.TodayJudgment)
	}
	if report.OverallStyle != "" || len(report.Highlights) != 0 || len(report.Suggestions) != 0 || len(report.Palette) != 0 {
		t.Fatalf("optional fields should stay empty: %#v", report)
	}
}

func TestParseOOTDReportNormalizesUIOverages(t *testing.T) {
	raw := strings.Replace(validOOTDReportJSON(), `"title":"城市休闲极简主义"`, `"title":"这是一条明显超过三十二个字符但仍然适合被前端截断展示的今日判断标题"`, 1)
	raw = strings.Replace(raw, `"highlights":["比例干净","色彩稳定"]`, `"highlights":["a","b","c","d"]`, 1)
	raw = strings.Replace(raw, `"suggestions":[{"title":"补一个焦点","body":"换一只更利落的包。"}]`, `"suggestions":[{"title":"a","body":"a"},{"title":"b","body":"b"},{"title":"c","body":"c"},{"title":"d","body":"d"}]`, 1)
	raw = strings.Replace(raw, `"advice":["把鞋换浅","补金属小配件"]`, `"advice":["a","b","c"]`, 1)
	raw = strings.Replace(raw, `{"name":"Black","hex":"#1A1A1A"},{"name":"Bone","hex":"#EAE9E1"}`, `{"name":"Bad","hex":"#111"},{"name":"","hex":"#222222"},{"name":"Bone","hex":"#EAE9E1"}`, 1)
	raw = strings.Replace(raw, `"mochiLine":"底子不差，但现在少一口气。"`, `"mochiLine":"这是一句特别长但仍然安全的 Mochi 点评，应该被后端截断成适合 UI 展示的长度，而不是让整份报告直接失败。这里继续补足更多文字确保超过限制。"`, 1)

	report, err := ParseOOTDReport(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(report.Highlights) != 3 || len(report.Suggestions) != 3 || len(report.ShareCard.Advice) != 2 {
		t.Fatalf("report was not normalized: highlights=%d suggestions=%d advice=%d", len(report.Highlights), len(report.Suggestions), len(report.ShareCard.Advice))
	}
	if len(report.Palette) != 1 || report.Palette[0].Hex != "#EAE9E1" {
		t.Fatalf("invalid palette colors were not filtered: %#v", report.Palette)
	}
	if len([]rune(report.TodayJudgment.Title)) > 32 || len([]rune(report.MochiLine)) > 80 {
		t.Fatalf("short fields were not truncated: title=%q mochiLine=%q", report.TodayJudgment.Title, report.MochiLine)
	}
}

func TestParseOOTDReportRejectsUnsafeOutput(t *testing.T) {
	raw := strings.Replace(validOOTDReportJSON(), "整体有方向，但鞋包和上身还差一个清晰态度。", "这套显胖，脸大所以不适合。", 1)
	_, err := ParseOOTDReport(raw)
	if err != ErrUnsafeOOTDReport {
		t.Fatalf("err = %v", err)
	}
}

func TestParseOOTDReportRejectsUngroundedHistoryOutput(t *testing.T) {
	raw := strings.Replace(validOOTDReportJSON(), "整体有方向，但鞋包和上身还差一个清晰态度。", "这是你第七次发这张图了，别再循环了。", 1)
	_, err := ParseOOTDReport(raw)
	if !errors.Is(err, ErrInvalidOOTDReport) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseOOTDReportRejectsFalseImageUnavailableOutput(t *testing.T) {
	raw := strings.Replace(validOOTDReportJSON(), "整体有方向，但鞋包和上身还差一个清晰态度。", "图片未能正常显示，我没法对着空气给你做穿搭诊断。", 1)
	_, err := ParseOOTDReport(raw)
	if !errors.Is(err, ErrInvalidOOTDReport) {
		t.Fatalf("err = %v", err)
	}
}

func TestFallbackOOTDReportIsValidAndLanguageAware(t *testing.T) {
	en := FallbackOOTDReport(ErrInvalidOOTDReport, "en")
	if err := ValidateOOTDReport(en); err != nil {
		t.Fatalf("english fallback invalid: %v %#v", err, en)
	}
	if en.TodayJudgment.Title != "Needs another pass" || en.TodayJudgment.Label != "Retry" {
		t.Fatalf("english fallback = %#v", en.TodayJudgment)
	}

	zh := FallbackOOTDReport(ErrInvalidOOTDReport, "zh")
	if err := ValidateOOTDReport(zh); err != nil {
		t.Fatalf("chinese fallback invalid: %v %#v", err, zh)
	}
	if zh.TodayJudgment.Title != "需要重新生成" || zh.TodayJudgment.Label != "重试" {
		t.Fatalf("chinese fallback = %#v", zh.TodayJudgment)
	}
}

func validOOTDReportJSON() string {
	return `{
		"todayJudgment":{"title":"城市休闲极简主义","score":5.5,"label":"还可以更好","summary":"整体有方向，但鞋包和上身还差一个清晰态度。"},
		"overallStyle":"偏城市休闲，靠中性色和宽松廓形成立。",
		"highlights":["比例干净","色彩稳定"],
		"biggestIssue":"上身黑色太整块，缺少细节焦点。",
		"suggestions":[{"title":"补一个焦点","body":"换一只更利落的包。"}],
		"palette":[{"name":"Black","hex":"#1A1A1A"},{"name":"Bone","hex":"#EAE9E1"}],
		"mochiLine":"底子不差，但现在少一口气。",
		"shareCard":{"title":"城市休闲极简主义","quote":"底子不差，但现在少一口气。","advice":["把鞋换浅","补金属小配件"],"cta":"让 Mochi 也看看你的 OOTD"}
	}`
}
