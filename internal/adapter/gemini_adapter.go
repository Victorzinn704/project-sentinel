package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/seu-usuario/project-sentinel/internal/domain"
)

const (
	defaultGeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"
	defaultGeminiTimeout = 120 * time.Second
)

type GeminiAdapter struct {
	client *http.Client
}

func NewGeminiAdapter(timeout time.Duration) *GeminiAdapter {
	if timeout <= 0 {
		timeout = defaultGeminiTimeout
	}
	return &GeminiAdapter{
		client: &http.Client{Timeout: timeout},
	}
}

func (a *GeminiAdapter) Provider() string {
	return domain.ProviderGemini
}

func (a *GeminiAdapter) Execute(
	ctx context.Context,
	requestID string,
	session *domain.Session,
	model domain.ResolvedModel,
	rawBody []byte,
	streamWriter func([]byte) error,
) (*domain.ProviderResult, error) {
	_, stream, messages, _, err := parseOpenAIRequest(rawBody)
	if err != nil {
		return nil, err
	}

	upstreamModel := strings.TrimSpace(model.UpstreamModel)
	if upstreamModel == "" {
		return nil, fmt.Errorf("%w: upstream model is required", domain.ErrInvalidData)
	}

	payload, err := buildGeminiPayload(messages)
	if err != nil {
		return nil, fmt.Errorf("%w: build gemini payload: %v", domain.ErrInvalidData, err)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode gemini payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resolveGeminiURL(session, upstreamModel, stream), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create gemini request: %w", err)
	}
	apiKey := providerToken(session)
	if apiKey == "" {
		return nil, fmt.Errorf("%w: missing provider token", domain.ErrInvalidData)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)
	if stream {
		req.Header.Set("accept", "text/event-stream")
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return &domain.ProviderResult{
			RequestID:  requestID,
			ResourceID: sessionAccountID(session),
		}, fmt.Errorf("%w: %v", domain.ErrTransientUpstream, err)
	}
	defer resp.Body.Close()

	result := &domain.ProviderResult{
		RequestID:  requestID,
		ResourceID: sessionAccountID(session),
		StatusCode: resp.StatusCode,
	}

	if err := handleProviderStatus(resp, result); err != nil {
		return result, err
	}

	if stream {
		if err := translateGeminiStream(resp.Body, requestID, model.ID, streamWriter); err != nil {
			return result, fmt.Errorf("%w: %v", domain.ErrTransientUpstream, err)
		}
		return result, nil
	}

	content, err := decodeGeminiResponse(resp.Body)
	if err != nil {
		return result, fmt.Errorf("%w: %v", domain.ErrTransientUpstream, err)
	}
	if err := writeOpenAITextResponse(requestID, model.ID, content, streamWriter); err != nil {
		return result, fmt.Errorf("%w: %v", domain.ErrTransientUpstream, err)
	}

	return result, nil
}

type geminiPayload struct {
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
	Contents          []geminiContent `json:"contents"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text,omitempty"`
}

func buildGeminiPayload(messages []json.RawMessage) (*geminiPayload, error) {
	payload := &geminiPayload{
		Contents: make([]geminiContent, 0, len(messages)),
	}

	var systemParts []geminiPart
	for _, raw := range messages {
		var msg struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			return nil, err
		}

		text := strings.TrimSpace(extractTextContent(msg.Content))
		if text == "" {
			continue
		}

		switch strings.TrimSpace(msg.Role) {
		case "system", "developer":
			systemParts = append(systemParts, geminiPart{Text: text})
		case "assistant":
			payload.Contents = append(payload.Contents, geminiContent{
				Role:  "model",
				Parts: []geminiPart{{Text: text}},
			})
		default:
			payload.Contents = append(payload.Contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: text}},
			})
		}
	}

	if len(systemParts) > 0 {
		payload.SystemInstruction = &geminiContent{
			Role:  "user",
			Parts: systemParts,
		}
	}
	if len(payload.Contents) == 0 {
		return nil, fmt.Errorf("messages are required")
	}

	return payload, nil
}

func resolveGeminiURL(session *domain.Session, model string, stream bool) string {
	base := strings.TrimSpace(resolveBaseURL(session, defaultGeminiBaseURL))
	if base == "" {
		base = defaultGeminiBaseURL
	}

	action := "generateContent"
	if stream {
		action = "streamGenerateContent?alt=sse"
	}
	if strings.Contains(base, ":generateContent") || strings.Contains(base, ":streamGenerateContent") {
		if stream && !strings.Contains(base, "alt=sse") {
			if strings.Contains(base, "?") {
				return base + "&alt=sse"
			}
			return base + "?alt=sse"
		}
		return base
	}

	return fmt.Sprintf("%s/models/%s:%s", strings.TrimRight(base, "/"), url.PathEscape(model), action)
}

func decodeGeminiResponse(body io.Reader) (string, error) {
	var payload struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		return "", err
	}

	var content strings.Builder
	for _, candidate := range payload.Candidates {
		for _, part := range candidate.Content.Parts {
			content.WriteString(part.Text)
		}
		if content.Len() > 0 {
			break
		}
	}

	return content.String(), nil
}

func translateGeminiStream(body io.Reader, requestID string, model string, write func([]byte) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 512*1024)

	finishReason := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}

		var payload struct {
			Candidates []struct {
				FinishReason string `json:"finishReason"`
				Content      struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			continue
		}

		for _, candidate := range payload.Candidates {
			for _, part := range candidate.Content.Parts {
				if strings.TrimSpace(part.Text) == "" {
					continue
				}
				chunk, err := json.Marshal(buildOpenAISSEChunk(openAICompletionID(requestID), model, part.Text, ""))
				if err != nil {
					return err
				}
				if err := write([]byte(fmt.Sprintf("data: %s\n\n", chunk))); err != nil {
					return err
				}
			}
			if candidate.FinishReason != "" {
				finishReason = mapGeminiFinishReason(candidate.FinishReason)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	if finishReason != "" {
		chunk, err := json.Marshal(buildOpenAISSEChunk(openAICompletionID(requestID), model, "", finishReason))
		if err != nil {
			return err
		}
		if err := write([]byte(fmt.Sprintf("data: %s\n\n", chunk))); err != nil {
			return err
		}
	}

	return write([]byte("data: [DONE]\n\n"))
}

func mapGeminiFinishReason(reason string) string {
	switch strings.ToUpper(strings.TrimSpace(reason)) {
	case "MAX_TOKENS":
		return "length"
	case "STOP", "FINISH_REASON_UNSPECIFIED":
		return "stop"
	default:
		return "stop"
	}
}
