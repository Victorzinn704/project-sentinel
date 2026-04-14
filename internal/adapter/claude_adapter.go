package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/seu-usuario/project-sentinel/internal/domain"
)

const (
	defaultClaudeURL     = "https://api.anthropic.com/v1/messages"
	defaultClaudeTimeout = 120 * time.Second
	anthropicVersion     = "2023-06-01"
	defaultClaudeTokens  = 4096
)

type ClaudeAdapter struct {
	client *http.Client
}

func NewClaudeAdapter(timeout time.Duration) *ClaudeAdapter {
	if timeout <= 0 {
		timeout = defaultClaudeTimeout
	}
	return &ClaudeAdapter{
		client: &http.Client{Timeout: timeout},
	}
}

func (a *ClaudeAdapter) Provider() string {
	return domain.ProviderClaude
}

func (a *ClaudeAdapter) Execute(
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

	payload, err := buildClaudePayload(messages, upstreamModel, stream)
	if err != nil {
		return nil, fmt.Errorf("%w: build claude payload: %v", domain.ErrInvalidData, err)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode claude payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resolveClaudeURL(session), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create claude request: %w", err)
	}
	apiKey := providerToken(session)
	if apiKey == "" {
		return nil, fmt.Errorf("%w: missing provider token", domain.ErrInvalidData)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("x-api-key", apiKey)
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
		if err := translateClaudeStream(resp.Body, requestID, model.ID, streamWriter); err != nil {
			return result, fmt.Errorf("%w: %v", domain.ErrTransientUpstream, err)
		}
		return result, nil
	}

	content, err := decodeClaudeResponse(resp.Body)
	if err != nil {
		return result, fmt.Errorf("%w: %v", domain.ErrTransientUpstream, err)
	}
	if err := writeOpenAITextResponse(requestID, model.ID, content, streamWriter); err != nil {
		return result, fmt.Errorf("%w: %v", domain.ErrTransientUpstream, err)
	}

	return result, nil
}

type claudePayload struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Stream    bool            `json:"stream"`
	System    string          `json:"system,omitempty"`
	Messages  []claudeMessage `json:"messages"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Content []claudeContent `json:"content"`
}

type claudeContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func buildClaudePayload(messages []json.RawMessage, upstreamModel string, stream bool) (*claudePayload, error) {
	payload := &claudePayload{
		Model:     upstreamModel,
		MaxTokens: defaultClaudeTokens,
		Stream:    stream,
		Messages:  make([]claudeMessage, 0, len(messages)),
	}

	var systemParts []string
	for _, raw := range messages {
		var msg struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			return nil, err
		}

		role := strings.TrimSpace(msg.Role)
		text := strings.TrimSpace(extractTextContent(msg.Content))
		if text == "" {
			continue
		}

		switch role {
		case "system", "developer":
			systemParts = append(systemParts, text)
		case "assistant":
			payload.Messages = append(payload.Messages, claudeMessage{
				Role:    "assistant",
				Content: []claudeContent{{Type: "text", Text: text}},
			})
		default:
			payload.Messages = append(payload.Messages, claudeMessage{
				Role:    "user",
				Content: []claudeContent{{Type: "text", Text: text}},
			})
		}
	}

	payload.System = strings.Join(systemParts, "\n\n")
	if len(payload.Messages) == 0 {
		return nil, fmt.Errorf("messages are required")
	}

	return payload, nil
}

func resolveClaudeURL(session *domain.Session) string {
	value := strings.TrimSpace(resolveBaseURL(session, defaultClaudeURL))
	if value == "" {
		return defaultClaudeURL
	}
	if strings.Contains(value, "/v1/messages") {
		return value
	}
	return strings.TrimRight(value, "/") + "/v1/messages"
}

func decodeClaudeResponse(body io.Reader) (string, error) {
	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		return "", err
	}

	var content strings.Builder
	for _, item := range payload.Content {
		if item.Type == "text" {
			content.WriteString(item.Text)
		}
	}

	return content.String(), nil
}

func translateClaudeStream(body io.Reader, requestID string, model string, write func([]byte) error) error {
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

		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "content_block_delta":
			if event.Delta.Type == "text_delta" && event.Delta.Text != "" {
				chunk, err := json.Marshal(buildOpenAISSEChunk(openAICompletionID(requestID), model, event.Delta.Text, ""))
				if err != nil {
					return err
				}
				if err := write([]byte(fmt.Sprintf("data: %s\n\n", chunk))); err != nil {
					return err
				}
			}
		case "message_delta":
			if event.Delta.StopReason != "" {
				finishReason = mapClaudeStopReason(event.Delta.StopReason)
			}
		case "error":
			if event.Error != nil && strings.TrimSpace(event.Error.Message) != "" {
				return errors.New(event.Error.Message)
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

func mapClaudeStopReason(reason string) string {
	switch strings.TrimSpace(reason) {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}
