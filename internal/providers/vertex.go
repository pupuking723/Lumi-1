package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/providers/googleauth"
)

const (
	vertexDefaultModel    = "gemini-2.5-flash"
	vertexDefaultLocation = "us-central1"
)

const vertexProjectIDHelp = "set GOCLAW_VERTEX_PROJECT_ID, VERTEX_PROJECT_ID, GOOGLE_CLOUD_PROJECT, provider settings.project_id, or pass a full model resource like projects/<project>/locations/<location>/publishers/google/models/<model>"
const vertexAuthHelp = "set provider api_key to a Google OAuth access token, set GOCLAW_VERTEX_ACCESS_TOKEN, provide service account credentials via GOOGLE_APPLICATION_CREDENTIALS / GOOGLE_APPLICATION_CREDENTIALS_JSON / GOCLAW_VERTEX_SERVICE_ACCOUNT_FILE / GOCLAW_VERTEX_SERVICE_ACCOUNT_JSON, or run gcloud auth print-access-token locally"

type VertexOption func(*VertexProvider)

type VertexProvider struct {
	mu             sync.Mutex
	name           string
	token          string
	tokenRefresher func(context.Context) (string, error)

	apiBase           string
	apiBaseConfigured bool
	projectID         string
	location          string
	defaultModel      string
	httpClient        *http.Client
}

func NewVertexProvider(name, token, apiBase, defaultModel string, opts ...VertexOption) *VertexProvider {
	if name == "" {
		name = "vertex"
	}
	if defaultModel == "" {
		defaultModel = vertexDefaultModel
	}
	token = firstNonEmptyVertex(token, os.Getenv("GOCLAW_VERTEX_ACCESS_TOKEN"), os.Getenv("VERTEX_ACCESS_TOKEN"), os.Getenv("GOOGLE_OAUTH_ACCESS_TOKEN"), os.Getenv("GOOGLE_ACCESS_TOKEN"))
	apiBase = strings.TrimRight(apiBase, "/")
	location := firstNonEmptyVertex(os.Getenv("GOCLAW_VERTEX_LOCATION"), os.Getenv("VERTEX_LOCATION"), os.Getenv("GOOGLE_CLOUD_LOCATION"), os.Getenv("CLOUD_ML_REGION"), vertexDefaultLocation)
	projectID := firstNonEmptyVertex(os.Getenv("GOCLAW_VERTEX_PROJECT_ID"), os.Getenv("VERTEX_PROJECT_ID"), os.Getenv("GOOGLE_CLOUD_PROJECT"), os.Getenv("GCLOUD_PROJECT"))
	client := &http.Client{Timeout: 600 * time.Second}
	p := &VertexProvider{
		name:              name,
		token:             token,
		apiBase:           apiBase,
		apiBaseConfigured: apiBase != "",
		projectID:         projectID,
		location:          location,
		defaultModel:      defaultModel,
		httpClient:        client,
		tokenRefresher: func(ctx context.Context) (string, error) {
			return googleauth.AccessTokenFromEnvOrGcloud(ctx, client)
		},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func WithVertexProjectID(projectID string) VertexOption {
	return func(p *VertexProvider) {
		if projectID = strings.TrimSpace(projectID); projectID != "" {
			p.projectID = projectID
		}
	}
}

func WithVertexLocation(location string) VertexOption {
	return func(p *VertexProvider) {
		if location = strings.TrimSpace(location); location != "" {
			p.location = location
		}
	}
}

func WithVertexHTTPClient(client *http.Client) VertexOption {
	return func(p *VertexProvider) {
		if client != nil {
			p.httpClient = client
			p.tokenRefresher = func(ctx context.Context) (string, error) {
				return googleauth.AccessTokenFromEnvOrGcloud(ctx, client)
			}
		}
	}
}

func WithVertexTokenRefresher(refresher func(context.Context) (string, error)) VertexOption {
	return func(p *VertexProvider) {
		if refresher != nil {
			p.tokenRefresher = refresher
		}
	}
}

func (p *VertexProvider) Name() string         { return p.name }
func (p *VertexProvider) DefaultModel() string { return p.defaultModel }
func (p *VertexProvider) SupportsThinking() bool {
	return true
}

func (p *VertexProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		Streaming:        true,
		ToolCalling:      true,
		StreamWithTools:  true,
		Thinking:         true,
		Vision:           true,
		CacheControl:     false,
		MaxContextWindow: 1_048_576,
		TokenizerID:      "cl100k_base",
	}
}

func (p *VertexProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if err := p.ensureToken(ctx); err != nil {
		return nil, err
	}
	model, err := p.modelResource(req.Model)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(vertexRequestFromChat(req))
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/%s:generateContent", p.endpointBaseURL(model.Location), strings.TrimLeft(model.Path, "/"))
	resp, err := p.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		if isUnauthorizedVertex(err) && p.refreshAccessToken(ctx) == nil {
			resp, err = p.doJSON(ctx, http.MethodPost, url, body)
		}
		if err != nil {
			return nil, err
		}
	}
	defer resp.Close()
	var parsed vertexGeminiResponse
	if err := json.NewDecoder(resp).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("%s: decode vertex response: %w", p.name, err)
	}
	return vertexResponseToChat(p.resolveModel(req.Model), parsed)
}

func (p *VertexProvider) ChatStream(ctx context.Context, req ChatRequest, onChunk func(StreamChunk)) (*ChatResponse, error) {
	if err := p.ensureToken(ctx); err != nil {
		return nil, err
	}
	model, err := p.modelResource(req.Model)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(vertexRequestFromChat(req))
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/%s:streamGenerateContent?alt=sse", p.endpointBaseURL(model.Location), strings.TrimLeft(model.Path, "/"))
	resp, err := p.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		if isUnauthorizedVertex(err) && p.refreshAccessToken(ctx) == nil {
			resp, err = p.doJSON(ctx, http.MethodPost, url, body)
		}
		if err != nil {
			return nil, err
		}
	}
	cb := NewCtxBody(ctx, resp)
	defer cb.Close()
	result, err := parseVertexStream(p.resolveModel(req.Model), cb, onChunk)
	if err != nil {
		return nil, err
	}
	if onChunk != nil {
		onChunk(StreamChunk{Done: true})
	}
	return result, nil
}

func (p *VertexProvider) resolveModel(model string) string {
	if strings.TrimSpace(model) != "" {
		return strings.TrimSpace(model)
	}
	return p.defaultModel
}

type vertexModelResource struct {
	Path     string
	Location string
	ModelID  string
}

func (p *VertexProvider) modelResource(model string) (vertexModelResource, error) {
	model = p.resolveModel(model)
	if strings.Contains(model, "/") {
		return parseVertexModelResource(model), nil
	}
	if strings.TrimSpace(p.projectID) == "" {
		return vertexModelResource{}, fmt.Errorf("vertex project ID is required for model %q; %s", model, vertexProjectIDHelp)
	}
	location := p.location
	if location == "" {
		location = vertexDefaultLocation
	}
	return vertexModelResource{
		Path:     fmt.Sprintf("projects/%s/locations/%s/publishers/google/models/%s", p.projectID, location, model),
		Location: location,
		ModelID:  model,
	}, nil
}

func parseVertexModelResource(path string) vertexModelResource {
	resource := vertexModelResource{Path: strings.TrimLeft(path, "/")}
	parts := strings.Split(resource.Path, "/")
	for i := 0; i+1 < len(parts); i++ {
		switch parts[i] {
		case "locations":
			resource.Location = parts[i+1]
		case "models":
			resource.ModelID = parts[i+1]
		}
	}
	return resource
}

func (p *VertexProvider) endpointBaseURL(location string) string {
	if p.apiBaseConfigured {
		return p.apiBase
	}
	return VertexEndpointBaseURL(location, "v1")
}

func VertexEndpointBaseURL(location, apiVersion string) string {
	location = strings.ToLower(strings.TrimSpace(location))
	apiVersion = strings.Trim(strings.TrimSpace(apiVersion), "/")
	if apiVersion == "" {
		apiVersion = "v1"
	}
	var host string
	switch location {
	case "global":
		host = "https://aiplatform.googleapis.com"
	case "us":
		host = "https://aiplatform.us.rep.googleapis.com"
	case "eu":
		host = "https://aiplatform.eu.rep.googleapis.com"
	case "":
		host = "https://us-central1-aiplatform.googleapis.com"
	default:
		host = fmt.Sprintf("https://%s-aiplatform.googleapis.com", location)
	}
	return host + "/" + apiVersion
}

func (p *VertexProvider) ensureToken(ctx context.Context) error {
	if p.currentToken() != "" {
		return nil
	}
	if err := p.refreshAccessToken(ctx); err != nil {
		return fmt.Errorf("vertex access token is required; %s: %w", vertexAuthHelp, err)
	}
	return nil
}

func (p *VertexProvider) currentToken() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.token
}

func (p *VertexProvider) setToken(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.token = strings.TrimSpace(token)
}

func (p *VertexProvider) refreshAccessToken(ctx context.Context) error {
	if p.tokenRefresher == nil {
		return fmt.Errorf("no token refresher configured")
	}
	token, err := p.tokenRefresher(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("token refresher returned empty token")
	}
	p.setToken(token)
	return nil
}

type vertexHTTPError struct {
	statusCode int
	status     string
	body       string
}

func (e *vertexHTTPError) Error() string {
	body := strings.TrimSpace(e.body)
	switch e.statusCode {
	case http.StatusUnauthorized:
		return fmt.Sprintf("vertex request failed: %s: %s; check credentials: %s", e.status, body, vertexAuthHelp)
	case http.StatusForbidden:
		return fmt.Sprintf("vertex request failed: %s: %s; check that Vertex AI API is enabled and the credential has aiplatform access for the configured project/location", e.status, body)
	case http.StatusNotFound:
		return fmt.Sprintf("vertex request failed: %s: %s; check project_id, location, and model id. %s", e.status, body, vertexProjectIDHelp)
	default:
		return fmt.Sprintf("vertex request failed: %s: %s", e.status, body)
	}
}

func isUnauthorizedVertex(err error) bool {
	if e, ok := err.(*vertexHTTPError); ok {
		return e.statusCode == http.StatusUnauthorized
	}
	return false
}

func (p *VertexProvider) doJSON(ctx context.Context, method, url string, body []byte) (io.ReadCloser, error) {
	httpReq, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.currentToken())
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		return nil, &vertexHTTPError{statusCode: resp.StatusCode, status: resp.Status, body: strings.TrimSpace(string(data))}
	}
	return resp.Body, nil
}

type vertexGeminiRequest struct {
	Contents          []vertexGeminiContent        `json:"contents"`
	GenerationConfig  vertexGeminiGenerationConfig `json:"generationConfig,omitempty"`
	Tools             []vertexGeminiTool           `json:"tools,omitempty"`
	SystemInstruction *vertexGeminiContent         `json:"systemInstruction,omitempty"`
}

type vertexGeminiContent struct {
	Role  string             `json:"role,omitempty"`
	Parts []vertexGeminiPart `json:"parts"`
}

type vertexGeminiPart struct {
	Text             string                        `json:"text,omitempty"`
	InlineData       *vertexGeminiBlob             `json:"inlineData,omitempty"`
	FunctionCall     *vertexGeminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *vertexGeminiFunctionResponse `json:"functionResponse,omitempty"`
	ThoughtSignature string                        `json:"thoughtSignature,omitempty"`
}

type vertexGeminiBlob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type vertexGeminiGenerationConfig struct {
	Temperature      *float64       `json:"temperature,omitempty"`
	MaxOutputTokens  int            `json:"maxOutputTokens,omitempty"`
	ResponseMimeType string         `json:"responseMimeType,omitempty"`
	ResponseSchema   map[string]any `json:"responseSchema,omitempty"`
	ThinkingConfig   *struct {
		ThinkingBudget int `json:"thinkingBudget,omitempty"`
	} `json:"thinkingConfig,omitempty"`
}

type vertexGeminiTool struct {
	FunctionDeclarations []vertexGeminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type vertexGeminiFunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type vertexGeminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type vertexGeminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

func vertexRequestFromChat(req ChatRequest) vertexGeminiRequest {
	out := vertexGeminiRequest{Contents: make([]vertexGeminiContent, 0, len(req.Messages))}
	toolNameByID := buildToolNameIndex(req.Messages)
	for i := 0; i < len(req.Messages); i++ {
		msg := req.Messages[i]
		if msg.Role == "system" {
			out.SystemInstruction = appendVertexSystem(out.SystemInstruction, msg.Content)
			continue
		}
		if msg.Role == "tool" {
			content := vertexGeminiContent{Role: "function"}
			for ; i < len(req.Messages) && req.Messages[i].Role == "tool"; i++ {
				toolMsg := req.Messages[i]
				name := toolNameByID[toolMsg.ToolCallID]
				if name == "" {
					name = "tool_result"
				}
				content.Parts = append(content.Parts, vertexGeminiPart{
					FunctionResponse: &vertexGeminiFunctionResponse{
						Name:     name,
						Response: map[string]any{"content": toolMsg.Content, "is_error": toolMsg.IsError},
					},
				})
			}
			i--
			if len(content.Parts) > 0 {
				out.Contents = append(out.Contents, content)
			}
			continue
		}
		content := vertexGeminiContent{Role: vertexRole(msg.Role)}
		if msg.Content != "" {
			content.Parts = append(content.Parts, vertexGeminiPart{Text: msg.Content})
		}
		for _, img := range msg.Images {
			content.Parts = append(content.Parts, vertexGeminiPart{
				InlineData: &vertexGeminiBlob{MimeType: img.MimeType, Data: img.Data},
			})
		}
		for _, tc := range msg.ToolCalls {
			part := vertexGeminiPart{
				FunctionCall: &vertexGeminiFunctionCall{Name: tc.Name, Args: tc.Arguments},
			}
			if tc.Metadata != nil {
				part.ThoughtSignature = tc.Metadata["thought_signature"]
			}
			content.Parts = append(content.Parts, part)
		}
		if len(content.Parts) > 0 {
			out.Contents = append(out.Contents, content)
		}
	}
	if len(req.Tools) > 0 {
		decls := make([]vertexGeminiFunctionDeclaration, 0, len(req.Tools))
		for _, tool := range req.Tools {
			if tool.Function == nil {
				continue
			}
			decls = append(decls, vertexGeminiFunctionDeclaration{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  tool.Function.Parameters,
			})
		}
		if len(decls) > 0 {
			out.Tools = []vertexGeminiTool{{FunctionDeclarations: decls}}
		}
	}
	if req.Options != nil {
		if v, ok := req.Options[OptMaxTokens].(int); ok && v > 0 {
			out.GenerationConfig.MaxOutputTokens = v
		}
		if v, ok := req.Options[OptTemperature].(float64); ok {
			out.GenerationConfig.Temperature = &v
		}
		if level, ok := req.Options[OptThinkingLevel].(string); ok {
			if budget := vertexThinkingBudget(level); budget >= 0 {
				out.GenerationConfig.ThinkingConfig = &struct {
					ThinkingBudget int `json:"thinkingBudget,omitempty"`
				}{ThinkingBudget: budget}
			}
		}
		if v, ok := req.Options[OptResponseMimeType].(string); ok {
			out.GenerationConfig.ResponseMimeType = strings.TrimSpace(v)
		}
		if v, ok := req.Options[OptResponseSchema].(map[string]any); ok && len(v) > 0 {
			out.GenerationConfig.ResponseSchema = v
		}
	}
	return out
}

func appendVertexSystem(existing *vertexGeminiContent, text string) *vertexGeminiContent {
	if strings.TrimSpace(text) == "" {
		return existing
	}
	if existing == nil {
		return &vertexGeminiContent{Parts: []vertexGeminiPart{{Text: text}}}
	}
	existing.Parts = append(existing.Parts, vertexGeminiPart{Text: text})
	return existing
}

func vertexRole(role string) string {
	switch role {
	case "assistant":
		return "model"
	case "tool":
		return "function"
	default:
		return "user"
	}
}

func vertexThinkingBudget(level string) int {
	switch level {
	case "off":
		return 0
	case "low", "minimal":
		return 1024
	case "medium", "":
		return 8192
	case "high":
		return 24576
	default:
		return -1
	}
}

type vertexGeminiResponse struct {
	Candidates    []vertexGeminiCandidate   `json:"candidates"`
	UsageMetadata vertexGeminiUsageMetadata `json:"usageMetadata,omitempty"`
}

type vertexGeminiCandidate struct {
	Content      vertexGeminiContent `json:"content"`
	FinishReason string              `json:"finishReason"`
}

type vertexGeminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
	ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
}

func vertexResponseToChat(model string, resp vertexGeminiResponse) (*ChatResponse, error) {
	out := &ChatResponse{FinishReason: "stop"}
	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]
		for i, part := range candidate.Content.Parts {
			if part.Text != "" {
				out.Content += part.Text
			}
			if part.FunctionCall != nil {
				tc := ToolCall{
					ID:        fmt.Sprintf("vertex-call-%d", i+1),
					Name:      part.FunctionCall.Name,
					Arguments: part.FunctionCall.Args,
				}
				if part.ThoughtSignature != "" {
					tc.Metadata = map[string]string{"thought_signature": part.ThoughtSignature}
				}
				out.ToolCalls = append(out.ToolCalls, tc)
			}
		}
		if candidate.FinishReason != "" {
			out.FinishReason = strings.ToLower(candidate.FinishReason)
		}
		if len(out.ToolCalls) > 0 && out.FinishReason != "length" {
			out.FinishReason = "tool_calls"
		}
	}
	if resp.UsageMetadata.TotalTokenCount > 0 {
		out.Usage = &Usage{
			PromptTokens:     resp.UsageMetadata.PromptTokenCount,
			CompletionTokens: resp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      resp.UsageMetadata.TotalTokenCount,
			ThinkingTokens:   resp.UsageMetadata.ThoughtsTokenCount,
		}
	}
	if len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("vertex %s: no candidates in response", model)
	}
	return out, nil
}

func parseVertexStream(model string, body io.Reader, onChunk func(StreamChunk)) (*ChatResponse, error) {
	result := &ChatResponse{FinishReason: "stop"}
	sse := NewSSEScanner(body)
	for sse.Next() {
		var chunk vertexGeminiResponse
		if err := json.Unmarshal([]byte(sse.Data()), &chunk); err != nil {
			continue
		}
		resp, err := vertexResponseToChat(model, chunk)
		if err != nil {
			continue
		}
		if resp.Content != "" {
			result.Content += resp.Content
			if onChunk != nil {
				onChunk(StreamChunk{Content: resp.Content})
			}
		}
		if len(resp.ToolCalls) > 0 {
			result.ToolCalls = append(result.ToolCalls, resp.ToolCalls...)
		}
		if resp.FinishReason != "" {
			result.FinishReason = resp.FinishReason
		}
		if resp.Usage != nil {
			result.Usage = resp.Usage
		}
	}
	if err := sse.Err(); err != nil {
		return nil, fmt.Errorf("vertex stream read error: %w", err)
	}
	if len(result.ToolCalls) > 0 && result.FinishReason != "length" {
		result.FinishReason = "tool_calls"
	}
	return result, nil
}

func firstNonEmptyVertex(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
