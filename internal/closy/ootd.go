package closy

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	ErrInvalidOOTDReport = errors.New("model_output_invalid")
	ErrUnsafeOOTDReport  = errors.New("unsafe_analysis_output")
	ootdPaletteHex       = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
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

type OOTDReport struct {
	TodayJudgment OOTDTodayJudgment `json:"todayJudgment"`
	OverallStyle  string            `json:"overallStyle"`
	Highlights    []string          `json:"highlights"`
	BiggestIssue  string            `json:"biggestIssue"`
	Suggestions   []OOTDSuggestion  `json:"suggestions"`
	Palette       []OOTDPalette     `json:"palette"`
	MochiLine     string            `json:"mochiLine"`
	ShareCard     OOTDShareCardCopy `json:"shareCard"`
}

type OOTDTodayJudgment struct {
	Title   string  `json:"title"`
	Score   float64 `json:"score"`
	Label   string  `json:"label"`
	Summary string  `json:"summary"`
}

type OOTDSuggestion struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type OOTDPalette struct {
	Name string `json:"name"`
	Hex  string `json:"hex"`
}

type OOTDShareCardCopy struct {
	Title  string   `json:"title"`
	Quote  string   `json:"quote"`
	Advice []string `json:"advice"`
	CTA    string   `json:"cta"`
}

func OOTDReportJSONSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"todayJudgment"},
		"properties": map[string]any{
			"todayJudgment": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []any{"title", "score", "label"},
				"properties": map[string]any{
					"title":   map[string]any{"type": "string"},
					"score":   map[string]any{"type": "number", "minimum": 0, "maximum": 10},
					"label":   map[string]any{"type": "string"},
					"summary": map[string]any{"type": "string"},
				},
			},
			"overallStyle": map[string]any{"type": "string"},
			"highlights":   map[string]any{"type": "array", "maxItems": 3, "items": map[string]any{"type": "string"}},
			"biggestIssue": map[string]any{"type": "string"},
			"suggestions": map[string]any{
				"type":     "array",
				"maxItems": 3,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"title": map[string]any{"type": "string"},
						"body":  map[string]any{"type": "string"},
					},
				},
			},
			"palette": map[string]any{
				"type":     "array",
				"maxItems": 4,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"name": map[string]any{"type": "string"},
						"hex":  map[string]any{"type": "string", "pattern": "^#[0-9A-Fa-f]{6}$"},
					},
				},
			},
			"mochiLine": map[string]any{"type": "string"},
			"shareCard": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"title":  map[string]any{"type": "string"},
					"quote":  map[string]any{"type": "string"},
					"advice": map[string]any{"type": "array", "maxItems": 2, "items": map[string]any{"type": "string"}},
					"cta":    map[string]any{"type": "string"},
				},
			},
		},
	}
}

func OOTDReportVertexSchema() map[string]any {
	return map[string]any{
		"type":     "OBJECT",
		"required": []any{"todayJudgment"},
		"properties": map[string]any{
			"todayJudgment": map[string]any{
				"type":     "OBJECT",
				"required": []any{"title", "score", "label"},
				"properties": map[string]any{
					"title":   map[string]any{"type": "STRING"},
					"score":   map[string]any{"type": "NUMBER"},
					"label":   map[string]any{"type": "STRING"},
					"summary": map[string]any{"type": "STRING"},
				},
			},
			"overallStyle": map[string]any{"type": "STRING"},
			"highlights":   map[string]any{"type": "ARRAY", "items": map[string]any{"type": "STRING"}},
			"biggestIssue": map[string]any{"type": "STRING"},
			"suggestions": map[string]any{
				"type": "ARRAY",
				"items": map[string]any{
					"type": "OBJECT",
					"properties": map[string]any{
						"title": map[string]any{"type": "STRING"},
						"body":  map[string]any{"type": "STRING"},
					},
				},
			},
			"palette": map[string]any{
				"type": "ARRAY",
				"items": map[string]any{
					"type": "OBJECT",
					"properties": map[string]any{
						"name": map[string]any{"type": "STRING"},
						"hex":  map[string]any{"type": "STRING"},
					},
				},
			},
			"mochiLine": map[string]any{"type": "STRING"},
			"shareCard": map[string]any{
				"type": "OBJECT",
				"properties": map[string]any{
					"title":  map[string]any{"type": "STRING"},
					"quote":  map[string]any{"type": "STRING"},
					"advice": map[string]any{"type": "ARRAY", "items": map[string]any{"type": "STRING"}},
					"cta":    map[string]any{"type": "STRING"},
				},
			},
		},
	}
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

func BuildOOTDReportPrompt(skillBody, note, scene, memoryPrompt, outputLanguage string) string {
	var b strings.Builder
	b.WriteString("You are Mochi running the mochi-ootd-review runtime skill for an OOTD image.\n\n")
	_ = memoryPrompt
	if skillBody = strings.TrimSpace(skillBody); skillBody != "" {
		b.WriteString("<MOCHI_OOTD_SKILL>\n")
		b.WriteString(skillBody)
		b.WriteString("\n</MOCHI_OOTD_SKILL>\n\n")
	}
	if scene = strings.TrimSpace(scene); scene != "" {
		b.WriteString("Scene: ")
		b.WriteString(scene)
		b.WriteString("\n")
	}
	if outputLanguage = strings.TrimSpace(outputLanguage); outputLanguage != "" {
		b.WriteString("Output language: ")
		b.WriteString(outputLanguage)
		b.WriteString(". Write all user-facing JSON string values in this language unless the user note clearly uses another language. Keep JSON keys exactly as specified in English.\n")
	}
	if note = strings.TrimSpace(note); note != "" {
		b.WriteString("User note: ")
		b.WriteString(note)
		b.WriteString("\n")
	}
	b.WriteString(`
Return only compact JSON. The only required object is todayJudgment with title, score, and label. Add the other fields when there is enough visible evidence:
{
  "todayJudgment": {"title": "short judgement title", "score": 0, "label": "short score label", "summary": "optional UI-ready summary"},
  "overallStyle": "one concise style description",
  "highlights": ["up to 3 clothing/styling highlights"],
  "biggestIssue": "one biggest clothing/styling issue, not about the user's body",
  "suggestions": [{"title": "short action title", "body": "concrete styling adjustment"}],
  "palette": [{"name": "color name", "hex": "#RRGGBB"}],
  "mochiLine": "one punchy Mochi sentence",
  "shareCard": {"title": "short title", "quote": "short quote", "advice": ["1-2 shareable adjustments"], "cta": "short CTA"}
}

Rules:
- JSON only. No Markdown, HTML, headings, or prose outside JSON.
- Score must be between 0 and 10.
- Keep highlights and suggestions to at most 3 items, palette to at most 4 colors, shareCard.advice to at most 2 items.
- Focus only on clothing, outfit composition, color, item choice, material, proportion created by garments, and scene fit.
- Use only visible evidence from the current image. Do not mention chat memory, prior uploads, how many times the user sent an image, or any hidden context.
- Do not infer a fictional story, relationship, occupation, identity, or user intent from poster text, captions, watermarks, or image typography.
- If a visible wearable outfit appears in the image, analyze that visible outfit even when the image looks like a reference image, stock photo, poster, screenshot, or cropped upload. You may call it a reference look, but do not say the image is unavailable.
- Return an insufficient-evidence report only when there is no visible wearable outfit, the image is blank/corrupted, or clothing details are genuinely not visible.
- Reject body shaming, weight judgement, facial attractiveness judgement, skin tone hierarchy, sexualized comments, and identity or income inference.
`)
	return strings.TrimSpace(b.String())
}

func BuildOOTDReportRepairPrompt(raw string, validationErr error) string {
	return strings.TrimSpace(fmt.Sprintf(`Repair the previous OOTD JSON so it is valid OOTDReport JSON.
Only todayJudgment.title, todayJudgment.score, and todayJudgment.label are required. Keep optional fields only when they are supported by visible evidence.
Return only compact JSON. Do not add Markdown or explanation.
Use only visible evidence from the current image. Remove any claims about prior chats, repeated uploads, hidden memory, fictional story, identity, or user intent.
If a visible wearable outfit appears in the image, analyze that visible outfit even if it looks like a reference image, stock photo, poster, screenshot, or cropped upload. Do not say the image is unavailable unless the previous output explicitly reported a real file/read error from the system.
Return an insufficient-evidence OOTDReport only when there is no visible wearable outfit, the image is blank/corrupted, or clothing details are genuinely not visible.
Validation error: %v
Previous output:
%s`, validationErr, strings.TrimSpace(raw)))
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

func ParseOOTDReport(raw string) (OOTDReport, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return OOTDReport{}, fmt.Errorf("%w: empty ootd report", ErrInvalidOOTDReport)
	}
	payload := extractJSONPayload(raw)
	var report OOTDReport
	if err := json.Unmarshal([]byte(payload), &report); err != nil {
		return OOTDReport{}, fmt.Errorf("%w: %v", ErrInvalidOOTDReport, err)
	}
	if !ootdReportHasCoreScore(payload) {
		return report, fmt.Errorf("%w: missing fields: todayJudgment.score", ErrInvalidOOTDReport)
	}
	report = sanitizeOOTDReportWhitespace(report)
	if err := ValidateOOTDReport(report); err != nil {
		return report, err
	}
	return report, nil
}

func ValidateOOTDReport(report OOTDReport) error {
	reportText := ootdReportText(report)
	if containsUnsafeBodyJudgement(reportText) {
		return ErrUnsafeOOTDReport
	}
	if containsUngroundedOOTDClaim(reportText) {
		return fmt.Errorf("%w: report contains claims not grounded in the current image", ErrInvalidOOTDReport)
	}
	missing := []string{}
	if strings.TrimSpace(report.TodayJudgment.Title) == "" {
		missing = append(missing, "todayJudgment.title")
	}
	if strings.TrimSpace(report.TodayJudgment.Label) == "" {
		missing = append(missing, "todayJudgment.label")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: missing fields: %s", ErrInvalidOOTDReport, strings.Join(missing, ", "))
	}
	if report.TodayJudgment.Score < 0 || report.TodayJudgment.Score > 10 {
		return fmt.Errorf("%w: todayJudgment.score must be between 0 and 10", ErrInvalidOOTDReport)
	}
	for i, color := range report.Palette {
		if strings.TrimSpace(color.Name) == "" {
			return fmt.Errorf("%w: palette[%d].name is required", ErrInvalidOOTDReport, i)
		}
		if !ootdPaletteHex.MatchString(color.Hex) {
			return fmt.Errorf("%w: palette[%d].hex must be #RRGGBB", ErrInvalidOOTDReport, i)
		}
	}
	for i, suggestion := range report.Suggestions {
		if strings.TrimSpace(suggestion.Title) == "" && strings.TrimSpace(suggestion.Body) == "" {
			return fmt.Errorf("%w: suggestions[%d] is empty", ErrInvalidOOTDReport, i)
		}
	}
	for i, value := range report.Highlights {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: highlights[%d] is empty", ErrInvalidOOTDReport, i)
		}
	}
	for i, value := range report.ShareCard.Advice {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: shareCard.advice[%d] is empty", ErrInvalidOOTDReport, i)
		}
	}
	if len([]rune(report.TodayJudgment.Title)) > 32 || len([]rune(report.MochiLine)) > 80 || len([]rune(report.ShareCard.Quote)) > 80 {
		return fmt.Errorf("%w: short display fields are too long", ErrInvalidOOTDReport)
	}
	return nil
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

func sanitizeOOTDReportWhitespace(report OOTDReport) OOTDReport {
	report.TodayJudgment.Title = strings.TrimSpace(report.TodayJudgment.Title)
	report.TodayJudgment.Label = strings.TrimSpace(report.TodayJudgment.Label)
	report.TodayJudgment.Summary = strings.TrimSpace(report.TodayJudgment.Summary)
	report.OverallStyle = strings.TrimSpace(report.OverallStyle)
	report.BiggestIssue = strings.TrimSpace(report.BiggestIssue)
	report.MochiLine = strings.TrimSpace(report.MochiLine)
	report.ShareCard.Title = strings.TrimSpace(report.ShareCard.Title)
	report.ShareCard.Quote = strings.TrimSpace(report.ShareCard.Quote)
	report.ShareCard.CTA = strings.TrimSpace(report.ShareCard.CTA)
	report.TodayJudgment.Title = truncateRunes(report.TodayJudgment.Title, 32)
	report.MochiLine = truncateRunes(report.MochiLine, 80)
	report.ShareCard.Quote = truncateRunes(report.ShareCard.Quote, 80)
	highlights := report.Highlights[:0]
	for _, value := range report.Highlights {
		if value = strings.TrimSpace(value); value != "" {
			highlights = append(highlights, value)
		}
	}
	report.Highlights = highlights
	if len(report.Highlights) > 3 {
		report.Highlights = report.Highlights[:3]
	}
	suggestions := report.Suggestions[:0]
	for _, suggestion := range report.Suggestions {
		suggestion.Title = strings.TrimSpace(suggestion.Title)
		suggestion.Body = strings.TrimSpace(suggestion.Body)
		if suggestion.Title != "" || suggestion.Body != "" {
			suggestions = append(suggestions, suggestion)
		}
	}
	report.Suggestions = suggestions
	if len(report.Suggestions) > 3 {
		report.Suggestions = report.Suggestions[:3]
	}
	palette := report.Palette[:0]
	for _, color := range report.Palette {
		color.Name = strings.TrimSpace(color.Name)
		color.Hex = strings.ToUpper(strings.TrimSpace(color.Hex))
		if color.Name != "" && ootdPaletteHex.MatchString(color.Hex) {
			palette = append(palette, color)
		}
	}
	report.Palette = palette
	if len(report.Palette) > 4 {
		report.Palette = report.Palette[:4]
	}
	advice := report.ShareCard.Advice[:0]
	for _, value := range report.ShareCard.Advice {
		if value = strings.TrimSpace(value); value != "" {
			advice = append(advice, value)
		}
	}
	report.ShareCard.Advice = advice
	if len(report.ShareCard.Advice) > 2 {
		report.ShareCard.Advice = report.ShareCard.Advice[:2]
	}
	return report
}

func ootdReportHasCoreScore(payload string) bool {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &root); err != nil {
		return false
	}
	rawJudgment, ok := root["todayJudgment"]
	if !ok {
		return false
	}
	var judgment map[string]json.RawMessage
	if err := json.Unmarshal(rawJudgment, &judgment); err != nil {
		return false
	}
	_, ok = judgment["score"]
	return ok
}

func ootdReportText(report OOTDReport) string {
	parts := []string{
		report.TodayJudgment.Title,
		report.TodayJudgment.Label,
		report.TodayJudgment.Summary,
		report.OverallStyle,
		report.BiggestIssue,
		report.MochiLine,
		report.ShareCard.Title,
		report.ShareCard.Quote,
		report.ShareCard.CTA,
	}
	parts = append(parts, report.Highlights...)
	parts = append(parts, report.ShareCard.Advice...)
	for _, suggestion := range report.Suggestions {
		parts = append(parts, suggestion.Title, suggestion.Body)
	}
	for _, color := range report.Palette {
		parts = append(parts, color.Name, color.Hex)
	}
	return strings.Join(parts, "\n")
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

func FallbackOOTDReport(validationErr error, outputLanguage string) OOTDReport {
	_ = validationErr
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(outputLanguage)), "zh") {
		return sanitizeOOTDReportWhitespace(OOTDReport{
			TodayJudgment: OOTDTodayJudgment{
				Title:   "需要重新生成",
				Score:   0,
				Label:   "重试",
				Summary: "Mochi 这次没有拿到稳定的穿搭报告。",
			},
			OverallStyle: "待重新识别",
			Highlights: []string{
				"图片已进入 OOTD 流程。",
			},
			BiggestIssue: "结构化报告没有通过校验。",
			Suggestions: []OOTDSuggestion{
				{Title: "换一张更清晰的全身照", Body: "保持光线稳定，让上衣、下装、鞋和外套都在画面里。"},
				{Title: "补充场景", Body: "告诉 Mochi 你要去上班、约会、上课还是旅行。"},
			},
			Palette: []OOTDPalette{
				{Name: "Neutral", Hex: "#F7F6F8"},
				{Name: "Ink", Hex: "#302D43"},
			},
			MochiLine: "我需要一张更稳的图，再认真给这套打分。",
			ShareCard: OOTDShareCardCopy{
				Title:  "需要重新生成",
				Quote:  "我需要一张更稳的图，再认真给这套打分。",
				Advice: []string{"换一张清晰全身照", "补充今天的场景"},
				CTA:    "让 Mochi 看看你的 OOTD",
			},
		})
	}
	return sanitizeOOTDReportWhitespace(OOTDReport{
		TodayJudgment: OOTDTodayJudgment{
			Title:   "Needs another pass",
			Score:   0,
			Label:   "Retry",
			Summary: "Mochi could not turn this image into a stable outfit report yet.",
		},
		OverallStyle: "Pending outfit read",
		Highlights: []string{
			"The image entered the OOTD flow.",
		},
		BiggestIssue: "The structured report did not pass validation.",
		Suggestions: []OOTDSuggestion{
			{Title: "Send a clearer full-body photo", Body: "Use steady light and keep the top, bottom, shoes, and outer layer in frame."},
			{Title: "Add the occasion", Body: "Tell Mochi whether this is for work, school, a date, a party, or travel."},
		},
		Palette: []OOTDPalette{
			{Name: "Neutral", Hex: "#F7F6F8"},
			{Name: "Ink", Hex: "#302D43"},
		},
		MochiLine: "I need one cleaner read before I rate this look.",
		ShareCard: OOTDShareCardCopy{
			Title:  "Needs another pass",
			Quote:  "I need one cleaner read before I rate this look.",
			Advice: []string{"Send a clearer full-body photo", "Add today's occasion"},
			CTA:    "Let Mochi read your OOTD",
		},
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
	unsafe := []string{"太胖", "很胖", "显胖", "显瘦", "显胖死", "又胖又", "丑", "不好看的人", "身材差", "腿粗所以不能", "脸大所以", "体重", "肤色差", "性感身材"}
	for _, term := range unsafe {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func containsUngroundedOOTDClaim(text string) bool {
	terms := []string{
		"第几次发",
		"第几次上传",
		"第七次发",
		"第六次发",
		"第五次发",
		"第四次发",
		"第三次发",
		"第二次发",
		"第一次发",
		"又发了这张",
		"又发这张",
		"再发这张",
		"又上传这张",
		"再上传这张",
		"重复发",
		"重复上传",
		"反复发",
		"反复上传",
		"之前那张",
		"上次那张",
		"上一次那张",
		"这张图已经被你",
		"你发过",
		"你上传过",
		"你又来了",
		"电子包浆",
		"赛博循环",
		"赛博执念",
		"无尽的赛博",
		"cosplay装备",
		"cosplay 装备",
		"图片未能正常显示",
		"图片无法正常显示",
		"图裂",
		"查无此图",
		"查无此搭",
		"对着空气",
		"没法对着空气",
	}
	lower := strings.ToLower(text)
	for _, term := range terms {
		if strings.Contains(lower, strings.ToLower(term)) {
			return true
		}
	}
	if regexp.MustCompile(`第[一二三四五六七八九十0-9]+次(发|传|上传|提交)`).MatchString(text) {
		return true
	}
	return false
}
