package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/google/uuid"
	"github.com/seu-usuario/project-sentinel/internal/domain"
)

const (
	// chatGPTCodexBaseURL is the Codex CLI web API endpoint root.
	chatGPTCodexBaseURL = "https://chatgpt.com/backend-api/codex"

	// defaultChatGPTTimeout is generous to accommodate long model responses.
	defaultChatGPTTimeout = 120 * time.Second
)

// protectedHeaders must never be overwritten by AuthParams. These are either
// built from the authenticated session or required by the upstream handshake.
var protectedHeaders = map[string]struct{}{
	"content-type":       {},
	"accept":             {},
	"originator":         {},
	"version":            {},
	"user-agent":         {},
	"session_id":         {},
	"openai-beta":        {},
	"authorization":      {},
	"chatgpt-account-id": {},
	"connection":         {},
}

// blockedSessionHeaderPrefixes are local control-plane headers. They may be
// useful between a client and Sentinel, but must not leak to chatgpt.com.
var blockedSessionHeaderPrefixes = []string{
	"sentinel-",
	"x-sentinel-",
	"codex-",
	"x-codex-",
}

// blockedSessionHeaders are client/proxy credentials or hop-by-hop headers that
// should never be taken from stored AuthParams for the upstream request.
var blockedSessionHeaders = map[string]struct{}{
	"api-key":               {},
	"codex_api_key":         {},
	"codex_base_url":        {},
	"codex_model":           {},
	"host":                  {},
	"openai-api-key":        {},
	"openai_api_key":        {},
	"openai-organization":   {},
	"openai-project":        {},
	"proxy-authenticate":    {},
	"proxy-authorization":   {},
	"sentinel_api_key":      {},
	"sentinel_base_url":     {},
	"sentinel_model":        {},
	"te":                    {},
	"trailer":               {},
	"transfer-encoding":     {},
	"upgrade":               {},
	"x-api-key":             {},
	"x-openai-api-key":      {},
	"x-openai-organization": {},
	"x-openai-project":      {},
}

// ChatGPTAdapter translates OpenAI-compatible requests into ChatGPT Codex
// backend-api requests. It spoofs TLS as Chrome 131 (Cloudflare bypass) while
// sending the HTTP headers of the real Codex CLI.
type ChatGPTAdapter struct {
	client                 tls_client.HttpClient
	timeout                time.Duration
	defaultReasoningEffort string
}

func NewChatGPTAdapter(timeout time.Duration, defaultReasoningEffort string) (*ChatGPTAdapter, error) {
	if timeout <= 0 {
		timeout = defaultChatGPTTimeout
	}
	defaultReasoningEffort = normalizeDefaultReasoningEffort(defaultReasoningEffort)

	jar := tls_client.NewCookieJar()
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(int(timeout.Seconds())),
		tls_client.WithClientProfile(profiles.Chrome_131),
		tls_client.WithCookieJar(jar),
		tls_client.WithNotFollowRedirects(),
	}

	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		return nil, fmt.Errorf("create stealth TLS client: %w", err)
	}

	return &ChatGPTAdapter{
		client:                 client,
		timeout:                timeout,
		defaultReasoningEffort: defaultReasoningEffort,
	}, nil
}

func (a *ChatGPTAdapter) Provider() string {
	return domain.ProviderChatGPT
}

// Execute performs the upstream round-trip. Lease acquire/release is the
// caller's responsibility; this method only does format translation and I/O.
func (a *ChatGPTAdapter) Execute(
	ctx context.Context,
	requestID string,
	session *domain.Session,
	model domain.ResolvedModel,
	rawBody []byte,
	streamWriter func([]byte) error,
) (*domain.ProviderResult, error) {

	_, stream, messages, reasoningEffort, err := parseOpenAIRequest(rawBody)
	if err != nil {
		return nil, err
	}

	upstreamModel := strings.TrimSpace(model.UpstreamModel)
	if upstreamModel == "" {
		return nil, fmt.Errorf("%w: upstream model is required", domain.ErrInvalidData)
	}

	backendPayload, err := buildCodexPayload(messages, upstreamModel, a.effectiveReasoningEffort(reasoningEffort))
	if err != nil {
		return nil, fmt.Errorf("%w: build codex payload: %v", domain.ErrInvalidData, err)
	}

	payloadBytes, err := json.Marshal(backendPayload)
	if err != nil {
		return nil, fmt.Errorf("encode backend-api payload: %w", err)
	}

	reqURL := resolveChatGPTURL(session)
	req, err := fhttp.NewRequest(fhttp.MethodPost, reqURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("create upstream request: %w", err)
	}

	req.Header = fhttp.Header{
		"content-type": {"application/json"},
		"accept":       {"text/event-stream"},
		"originator":   {"codex_cli_rs"},
		"version":      {"0.101.0"},
		"user-agent":   {"codex_cli_rs/0.101.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464"},
		"connection":   {"Keep-Alive"},
		"session_id":   {uuid.NewString()},
	}
	if accountID := chatGPTAccountID(session); accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}

	injectSessionCredentials(req, session)

	resp, err := a.client.Do(req)
	if err != nil {
		return &domain.ProviderResult{
			RequestID:  requestID,
			ResourceID: session.AccountID,
		}, fmt.Errorf("%w: %v", domain.ErrTransientUpstream, err)
	}
	defer resp.Body.Close()

	result := &domain.ProviderResult{
		RequestID:  requestID,
		ResourceID: session.AccountID,
		StatusCode: resp.StatusCode,
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode <= 299:
		// Success — fall through to stream translation.
	case resp.StatusCode == 429:
		result.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		if result.RetryAfter <= 0 {
			result.RetryAfter = parseCodexRetryAfter(resp.Body)
		}
		return result, domain.ErrPolicyRateLimit
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return result, domain.ErrAuthFailure
	case resp.StatusCode >= 500 && resp.StatusCode <= 599:
		return result, fmt.Errorf("%w: upstream returned status %d", domain.ErrTransientUpstream, resp.StatusCode)
	default:
		return result, fmt.Errorf("%w: upstream returned status %d: %s", domain.ErrInvalidData, resp.StatusCode, readUpstreamErrorSnippet(resp.Body))
	}

	if stream {
		err = translateSSEStream(ctx, resp.Body, requestID, model.ID, streamWriter)
	} else {
		err = translateNonStreamingResponse(resp.Body, requestID, model.ID, streamWriter)
	}

	if err != nil {
		return result, fmt.Errorf("%w: %v", domain.ErrTransientUpstream, err)
	}

	return result, nil
}

// ============================================================================
// Request translation: OpenAI → Codex backend-api
// ============================================================================

func parseOpenAIRequest(rawBody []byte) (string, bool, []json.RawMessage, string, error) {
	var body struct {
		Model           string            `json:"model"`
		Messages        []json.RawMessage `json:"messages"`
		Stream          *bool             `json:"stream,omitempty"`
		ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return "", false, nil, "", fmt.Errorf("%w: invalid JSON payload", domain.ErrInvalidData)
	}

	if body.Model == "" {
		return "", false, nil, "", fmt.Errorf("%w: model is required", domain.ErrInvalidData)
	}
	if len(body.Messages) == 0 {
		return "", false, nil, "", fmt.Errorf("%w: messages are required", domain.ErrInvalidData)
	}

	stream := true
	if body.Stream != nil {
		stream = *body.Stream
	}

	return body.Model, stream, body.Messages, strings.TrimSpace(body.ReasoningEffort), nil
}

type codexPayload struct {
	Model             string         `json:"model"`
	Stream            bool           `json:"stream"`
	Store             bool           `json:"store"`
	Instructions      string         `json:"instructions"`
	Input             []codexInput   `json:"input"`
	Reasoning         codexReasoning `json:"reasoning"`
	ParallelToolCalls bool           `json:"parallel_tool_calls"`
	Include           []string       `json:"include,omitempty"`
}

type codexInput struct {
	Type    string             `json:"type"`
	Role    string             `json:"role"`
	Content []codexContentPart `json:"content"`
}

type codexContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexReasoning struct {
	Effort  string `json:"effort"`
	Summary string `json:"summary"`
}

func buildCodexPayload(openAIMessages []json.RawMessage, upstreamModel string, reasoningEffort string) (*codexPayload, error) {
	instructions := ""
	inputs := make([]codexInput, 0, len(openAIMessages))
	reasoningEffort = normalizeReasoningEffort(reasoningEffort)

	for _, raw := range openAIMessages {
		var msg struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			return nil, fmt.Errorf("parse message: %w", err)
		}

		text := extractTextContent(msg.Content)

		role := "user"
		partType := "input_text"
		switch msg.Role {
		case "system", "developer":
			role = "developer"
			if text != "" {
				if instructions != "" {
					instructions += "\n"
				}
				instructions += text
			}
		case "assistant":
			role = "assistant"
			partType = "output_text"
		case "user":
			role = "user"
		}

		inputs = append(inputs, codexInput{
			Type:    "message",
			Role:    role,
			Content: []codexContentPart{{Type: partType, Text: text}},
		})
	}

	return &codexPayload{
		Model:             upstreamModel,
		Stream:            true,
		Store:             false,
		Instructions:      instructions,
		Input:             inputs,
		Reasoning:         codexReasoning{Effort: reasoningEffort, Summary: "auto"},
		ParallelToolCalls: true,
		Include:           []string{"reasoning.encrypted_content"},
	}, nil
}

func normalizeReasoningEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "minimal", "low", "medium", "high", "xhigh":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "medium"
	}
}

func normalizeDefaultReasoningEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "xhigh":
		return "xhigh"
	default:
		return "high"
	}
}

func (a *ChatGPTAdapter) effectiveReasoningEffort(requested string) string {
	requested = normalizeReasoningEffort(requested)
	if a.defaultReasoningEffort == "xhigh" {
		return "xhigh"
	}
	if requested == "xhigh" {
		return "xhigh"
	}
	return "high"
}

// extractTextContent handles OpenAI content fields that can be either a plain
// string or an array of parts (multimodal shape).
func extractTextContent(content any) string {
	if s, ok := content.(string); ok {
		return s
	}
	if content == nil {
		return ""
	}

	if parts, ok := content.([]any); ok {
		var texts []string
		for _, part := range parts {
			if s, ok := part.(string); ok {
				texts = append(texts, s)
				continue
			}
			if m, ok := part.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					texts = append(texts, t)
				}
			}
		}
		return strings.Join(texts, "\n")
	}

	return fmt.Sprintf("%v", content)
}

// ============================================================================
// Response translation: Codex SSE → OpenAI SSE
// ============================================================================

type codexSSEData struct {
	Type  string `json:"type"`
	Delta string `json:"delta,omitempty"`
}

func translateSSEStream(
	ctx context.Context,
	body io.Reader,
	requestID string,
	model string,
	write func([]byte) error,
) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 512*1024)

	completionID := "chatcmpl-" + requestID

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		if data == "[DONE]" {
			if err := write([]byte("data: [DONE]\n\n")); err != nil {
				return err
			}
			return nil
		}

		var event codexSSEData
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		finishReason := ""
		delta := ""

		switch event.Type {
		case "response.output_text.delta":
			delta = event.Delta
		case "response.completed":
			finishReason = "stop"
		default:
			continue
		}

		if delta == "" && finishReason == "" {
			continue
		}

		chunk := buildOpenAISSEChunk(completionID, model, delta, finishReason)
		chunkBytes, err := json.Marshal(chunk)
		if err != nil {
			continue
		}

		sseEvent := fmt.Sprintf("data: %s\n\n", chunkBytes)
		if err := write([]byte(sseEvent)); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read upstream SSE stream: %w", err)
	}

	if err := write([]byte("data: [DONE]\n\n")); err != nil {
		return err
	}

	return nil
}

func translateNonStreamingResponse(
	body io.Reader,
	requestID string,
	model string,
	write func([]byte) error,
) error {
	raw, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("read upstream response: %w", err)
	}

	if bytes.Contains(raw, []byte("data:")) {
		content, ok := extractCompletedCodexText(raw)
		if !ok {
			return fmt.Errorf("stream closed before response.completed")
		}
		return writeOpenAITextResponse(requestID, model, content, write)
	}

	type compactContent struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}

	type compactOutput struct {
		Type    string           `json:"type"`
		Content []compactContent `json:"content"`
	}

	var payload struct {
		Type     string `json:"type"`
		Response struct {
			Output []compactOutput `json:"output"`
		} `json:"response"`
	}

	if err := json.Unmarshal(raw, &payload); err != nil {
		// Upstream returned a non-JSON body (HTML challenge, plain error, etc.).
		// Surfacing it as assistant content masks failures; report transient.
		return fmt.Errorf("decode upstream compact response: %w", err)
	}

	var fullContent string
	for _, out := range payload.Response.Output {
		if out.Type == "message" {
			for _, content := range out.Content {
				if content.Type == "output_text" {
					fullContent += content.Text
				}
			}
		}
	}

	completionID := "chatcmpl-" + requestID
	response := map[string]any{
		"id":      completionID,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": fullContent,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		},
	}

	responseBytes, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}

	return write(responseBytes)
}

type codexCompletedEvent struct {
	Type     string              `json:"type"`
	Delta    string              `json:"delta,omitempty"`
	Response codexResponseObject `json:"response"`
}

type codexResponseObject struct {
	Output []compactOutput `json:"output"`
}

type compactContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type compactOutput struct {
	Type    string           `json:"type"`
	Content []compactContent `json:"content"`
}

func extractCompletedCodexText(raw []byte) (string, bool) {
	lines := bytes.Split(raw, []byte("\n"))
	var deltas strings.Builder

	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if bytes.Equal(data, []byte("[DONE]")) || len(data) == 0 {
			continue
		}

		var event codexCompletedEvent
		if err := json.Unmarshal(data, &event); err != nil {
			continue
		}

		switch event.Type {
		case "response.output_text.delta":
			deltas.WriteString(event.Delta)
		case "response.completed":
			if text := extractCodexResponseText(event.Response); text != "" {
				return text, true
			}
			return deltas.String(), true
		}
	}

	return "", false
}

func extractCodexResponseText(response codexResponseObject) string {
	var fullContent strings.Builder
	for _, out := range response.Output {
		if out.Type != "message" {
			continue
		}
		for _, content := range out.Content {
			if content.Type == "output_text" {
				fullContent.WriteString(content.Text)
			}
		}
	}
	return fullContent.String()
}

// ============================================================================
// OpenAI SSE chunk builder
// ============================================================================

type openAIChunk struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
}

type openAIChoice struct {
	Index        int         `json:"index"`
	Delta        openAIDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type openAIDelta struct {
	Content string `json:"content,omitempty"`
	Role    string `json:"role,omitempty"`
}

func buildOpenAISSEChunk(completionID, model, delta, finishReason string) openAIChunk {
	choice := openAIChoice{
		Index: 0,
		Delta: openAIDelta{Content: delta},
	}
	if finishReason != "" {
		choice.FinishReason = &finishReason
	}

	return openAIChunk{
		ID:      completionID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []openAIChoice{choice},
	}
}

// ============================================================================
// Session credential injection
// ============================================================================

func injectSessionCredentials(req *fhttp.Request, session *domain.Session) {
	if session == nil {
		return
	}

	if session.AccessToken != "" {
		req.Header.Set("authorization", "Bearer "+session.AccessToken)
	}

	for key, value := range session.AuthParams {
		lower := strings.ToLower(strings.TrimSpace(key))
		if lower == "raw_cookies" {
			req.Header.Set("cookie", value)
			continue
		}
		if shouldDropSessionHeader(lower) {
			continue
		}
		req.Header.Set(key, value)
	}
}

func shouldDropSessionHeader(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "" {
		return true
	}
	if _, protected := protectedHeaders[lower]; protected {
		return true
	}
	if _, blocked := blockedSessionHeaders[lower]; blocked {
		return true
	}
	for _, prefix := range blockedSessionHeaderPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func resolveChatGPTURL(session *domain.Session) string {
	base := strings.TrimSpace(resolveBaseURL(session, chatGPTCodexBaseURL))
	if base == "" {
		base = chatGPTCodexBaseURL
	}
	base = strings.TrimRight(base, "/")
	base = strings.TrimSuffix(base, "/responses/compact")
	if strings.HasSuffix(base, "/responses") {
		return base
	}
	if strings.HasSuffix(base, "/backend-api/codex") {
		return base + "/responses"
	}
	if strings.Contains(base, "/backend-api/codex/") {
		return strings.TrimRight(base, "/") + "/responses"
	}
	return base + "/backend-api/codex/responses"
}

// ============================================================================
// Helpers
// ============================================================================

// parseRetryAfter returns the duration encoded in a Retry-After header.
// Accepts either seconds (RFC 7231 §7.1.3) or an HTTP-date. Returns zero when
// the value is missing or unparseable, letting the caller pick a default.
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if d, err := time.ParseDuration(value + "s"); err == nil && d > 0 {
		return d
	}
	if when, err := fhttp.ParseTime(value); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

func parseCodexRetryAfter(body io.Reader) time.Duration {
	if body == nil {
		return 0
	}
	var payload struct {
		Error struct {
			ResetsAt        int64 `json:"resets_at"`
			ResetsInSeconds int64 `json:"resets_in_seconds"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 4096)).Decode(&payload); err != nil {
		return 0
	}
	if payload.Error.ResetsAt > 0 {
		if d := time.Until(time.Unix(payload.Error.ResetsAt, 0)); d > 0 {
			return d
		}
	}
	if payload.Error.ResetsInSeconds > 0 {
		return time.Duration(payload.Error.ResetsInSeconds) * time.Second
	}
	return 0
}

func readUpstreamErrorSnippet(body io.Reader) string {
	if body == nil {
		return ""
	}
	raw, _ := io.ReadAll(io.LimitReader(body, 4096))
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "empty upstream error body"
	}

	var payload struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil {
		if payload.Error.Message != "" {
			return payload.Error.Message
		}
		if payload.Message != "" {
			return payload.Message
		}
	}

	const maxSnippet = 512
	snippet := string(raw)
	if len(snippet) > maxSnippet {
		snippet = snippet[:maxSnippet]
	}
	return snippet
}

func chatGPTAccountID(session *domain.Session) string {
	if session == nil {
		return ""
	}
	if session.AuthParams != nil {
		for _, key := range []string{"chatgpt_account_id", "chatgpt-account-id", "Chatgpt-Account-Id", "account_id"} {
			if value := strings.TrimSpace(session.AuthParams[key]); value != "" {
				return value
			}
		}
	}
	return chatGPTAccountIDFromToken(session.AccessToken)
}

func chatGPTAccountIDFromToken(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}

	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}

	if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if id, ok := auth["chatgpt_account_id"].(string); ok {
			return strings.TrimSpace(id)
		}
	}
	if id, ok := claims["chatgpt_account_id"].(string); ok {
		return strings.TrimSpace(id)
	}
	return ""
}
