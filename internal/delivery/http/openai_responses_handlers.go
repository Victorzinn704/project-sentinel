package httpdelivery

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
)

func PostOpenAIResponsesHandler(chatHandler http.HandlerFunc, defaultModel string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := readLimitedBody(w, r, openAIRequestLimitBytes)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error(), "invalid_body")
			return
		}

		chatRaw, responsesModel, stream, err := responsesToChatCompletionsRaw(raw, defaultModel)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error(), "invalid_responses_request")
			return
		}

		chatReq := r.Clone(r.Context())
		chatReq.URL.Path = "/v1/chat/completions"
		chatReq.Body = io.NopCloser(bytes.NewReader(chatRaw))
		chatReq.ContentLength = int64(len(chatRaw))
		chatReq.Header = r.Header.Clone()
		chatReq.Header.Set("Content-Type", "application/json")

		if stream {
			bridge := newResponsesStreamBridge(w, responsesModel)
			bridge.start()
			chatHandler(bridge, chatReq)
			bridge.finish()
			return
		}

		recorder := httptest.NewRecorder()
		chatHandler(recorder, chatReq)
		result := recorder.Result()
		defer result.Body.Close()

		body, err := io.ReadAll(result.Body)
		if err != nil {
			writeOpenAIError(w, http.StatusInternalServerError, "server_error", "failed to read internal response", "internal_error")
			return
		}

		for key, values := range result.Header {
			if strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "Content-Type") {
				continue
			}
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}

		if result.StatusCode < 200 || result.StatusCode >= 300 {
			contentType := result.Header.Get("Content-Type")
			if contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			w.WriteHeader(result.StatusCode)
			_, _ = w.Write(body)
			return
		}

		responseBody, err := chatCompletionToResponsesBody(body, responsesModel)
		if err != nil {
			writeOpenAIError(w, http.StatusInternalServerError, "server_error", err.Error(), "response_translation_failed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(responseBody)
	}
}

func responsesToChatCompletionsRaw(raw []byte, defaultModel string) ([]byte, string, bool, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, "", false, fmt.Errorf("invalid JSON payload")
	}

	model := rawString(body["model"])
	if model == "" {
		model = strings.TrimSpace(defaultModel)
	}
	if model == "" {
		return nil, "", false, fmt.Errorf("model is required")
	}

	inputRaw, ok := body["input"]
	if !ok || len(bytes.TrimSpace(inputRaw)) == 0 {
		return nil, "", false, fmt.Errorf("input is required")
	}

	stream := rawBool(body["stream"])
	messages := make([]map[string]any, 0, 4)
	if instructions := rawString(body["instructions"]); instructions != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": instructions,
		})
	}

	convertedInput, err := responsesInputToMessages(inputRaw)
	if err != nil {
		return nil, "", false, err
	}
	messages = append(messages, convertedInput...)
	if len(messages) == 0 {
		return nil, "", false, fmt.Errorf("input produced no messages")
	}

	chat := map[string]any{
		"model":    model,
		"stream":   stream,
		"messages": messages,
	}
	if effort := responsesReasoningEffort(body); effort != "" {
		chat["reasoning_effort"] = effort
	}

	out, err := json.Marshal(chat)
	if err != nil {
		return nil, "", false, fmt.Errorf("encode internal chat request: %w", err)
	}
	return out, model, stream, nil
}

func responsesInputToMessages(raw json.RawMessage) ([]map[string]any, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("input is empty")
		}
		return []map[string]any{{"role": "user", "content": text}}, nil
	}

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err == nil {
		messages := make([]map[string]any, 0, len(items))
		for _, item := range items {
			message, ok := responseInputItemToMessage(item)
			if ok {
				messages = append(messages, message)
			}
		}
		if len(messages) == 0 {
			return nil, fmt.Errorf("input array contains no supported message items")
		}
		return messages, nil
	}

	return nil, fmt.Errorf("input must be a string or array")
}

func responseInputItemToMessage(raw json.RawMessage) (map[string]any, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return map[string]any{"role": "user", "content": text}, strings.TrimSpace(text) != ""
	}

	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, false
	}

	itemType := rawString(item["type"])
	role := rawString(item["role"])
	if role == "" {
		role = "user"
	}
	if role == "developer" {
		role = "system"
	}

	if itemType == "input_text" || itemType == "output_text" || itemType == "text" {
		if text := rawString(item["text"]); text != "" {
			return map[string]any{"role": role, "content": text}, true
		}
	}

	contentRaw, hasContent := item["content"]
	if !hasContent {
		return nil, false
	}
	if content := rawString(contentRaw); content != "" {
		return map[string]any{"role": role, "content": content}, true
	}

	var parts []map[string]any
	if err := json.Unmarshal(contentRaw, &parts); err == nil {
		converted := make([]map[string]any, 0, len(parts))
		for _, part := range parts {
			partType, _ := part["type"].(string)
			switch partType {
			case "input_text", "output_text", "text":
				if text, _ := part["text"].(string); text != "" {
					converted = append(converted, map[string]any{"type": "text", "text": text})
				}
			case "input_image":
				if imageURL, _ := part["image_url"].(string); imageURL != "" {
					converted = append(converted, map[string]any{
						"type": "image_url",
						"image_url": map[string]any{
							"url": imageURL,
						},
					})
				}
			}
		}
		if len(converted) > 0 {
			return map[string]any{"role": role, "content": converted}, true
		}
	}

	return nil, false
}

func chatCompletionToResponsesBody(raw []byte, fallbackModel string) ([]byte, error) {
	var chat struct {
		ID      string `json:"id"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content any    `json:"content"`
				Role    string `json:"role"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage any `json:"usage,omitempty"`
	}
	if err := json.Unmarshal(raw, &chat); err != nil {
		return nil, fmt.Errorf("decode internal chat response: %w", err)
	}
	if chat.Model == "" {
		chat.Model = fallbackModel
	}
	if chat.Created == 0 {
		chat.Created = time.Now().Unix()
	}

	content := ""
	if len(chat.Choices) > 0 {
		content = textFromAny(chat.Choices[0].Message.Content)
	}
	content = sanitizeInternalTraceText(content)
	responseID := responseIDFromChatID(chat.ID)
	messageID := "msg_" + strings.TrimPrefix(responseID, "resp_")

	response := map[string]any{
		"id":         responseID,
		"object":     "response",
		"created_at": chat.Created,
		"status":     "completed",
		"model":      chat.Model,
		"output": []map[string]any{
			{
				"id":     messageID,
				"type":   "message",
				"status": "completed",
				"role":   "assistant",
				"content": []map[string]any{
					{
						"type": "output_text",
						"text": content,
					},
				},
			},
		},
	}
	if chat.Usage != nil {
		response["usage"] = chat.Usage
	}

	out, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encode responses response: %w", err)
	}
	return out, nil
}

func writeResponsesSSEFromChatSSE(w io.Writer, raw []byte, fallbackModel string) error {
	responseID := fmt.Sprintf("resp_%d", time.Now().UnixNano())
	messageID := "msg_" + strings.TrimPrefix(responseID, "resp_")
	created := time.Now().Unix()
	model := fallbackModel
	var fullText strings.Builder

	writeEvent := func(payload map[string]any) error {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "data: %s\n\n", encoded)
		return err
	}

	if err := writeEvent(map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":         responseID,
			"object":     "response",
			"created_at": created,
			"status":     "in_progress",
			"model":      model,
			"output":     []any{},
		},
	}); err != nil {
		return err
	}
	if err := writeEvent(map[string]any{
		"type": "response.output_item.added",
		"output_index": 0,
		"item": map[string]any{
			"id":      messageID,
			"type":    "message",
			"status":  "in_progress",
			"role":    "assistant",
			"content": []any{},
		},
	}); err != nil {
		return err
	}
	if err := writeEvent(map[string]any{
		"type":          "response.content_part.added",
		"item_id":       messageID,
		"output_index":  0,
		"content_index": 0,
		"part": map[string]any{
			"type": "output_text",
			"text": "",
		},
	}); err != nil {
		return err
	}

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 512*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}

		var chunk struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Created int64  `json:"created"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.ID != "" {
			responseID = responseIDFromChatID(chunk.ID)
			messageID = "msg_" + strings.TrimPrefix(responseID, "resp_")
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Created > 0 {
			created = chunk.Created
		}
		for _, choice := range chunk.Choices {
			delta := sanitizeInternalTraceText(choice.Delta.Content)
			if delta != "" {
				fullText.WriteString(delta)
				if err := writeEvent(map[string]any{
					"type":          "response.output_text.delta",
					"item_id":       messageID,
					"output_index":  0,
					"content_index": 0,
					"delta":         delta,
				}); err != nil {
					return err
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	text := sanitizeInternalTraceText(fullText.String())
	if err := writeEvent(map[string]any{
		"type":          "response.output_text.done",
		"item_id":       messageID,
		"output_index":  0,
		"content_index": 0,
		"text":          text,
	}); err != nil {
		return err
	}
	if err := writeEvent(map[string]any{
		"type":          "response.content_part.done",
		"item_id":       messageID,
		"output_index":  0,
		"content_index": 0,
		"part": map[string]any{
			"type": "output_text",
			"text": text,
		},
	}); err != nil {
		return err
	}
	if err := writeEvent(map[string]any{
		"type":         "response.output_item.done",
		"output_index": 0,
		"item": map[string]any{
			"id":     messageID,
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []map[string]any{
				{
					"type": "output_text",
					"text": text,
				},
			},
		},
	}); err != nil {
		return err
	}
	if err := writeEvent(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":         responseID,
			"object":     "response",
			"created_at": created,
			"status":     "completed",
			"model":      model,
			"output": []map[string]any{
				{
					"id":     messageID,
					"type":   "message",
					"status": "completed",
					"role":   "assistant",
					"content": []map[string]any{
						{
							"type": "output_text",
							"text": text,
						},
					},
				},
			},
		},
	}); err != nil {
		return err
	}
	_, err := fmt.Fprint(w, "data: [DONE]\n\n")
	return err
}

type responsesStreamBridge struct {
	downstream http.ResponseWriter
	header     http.Header
	model      string

	responseID string
	messageID  string
	created    int64
	started    bool
	failed     bool
	itemStarted bool
	partStarted bool
	statusCode int
	fullText   strings.Builder
}

func newResponsesStreamBridge(downstream http.ResponseWriter, model string) *responsesStreamBridge {
	responseID := fmt.Sprintf("resp_%d", time.Now().UnixNano())
	return &responsesStreamBridge{
		downstream: downstream,
		header:     make(http.Header),
		model:      model,
		responseID: responseID,
		messageID:  "msg_" + strings.TrimPrefix(responseID, "resp_"),
		created:    time.Now().Unix(),
		statusCode: http.StatusOK,
	}
}

func (b *responsesStreamBridge) Header() http.Header {
	return b.header
}

func (b *responsesStreamBridge) WriteHeader(statusCode int) {
	b.statusCode = statusCode
	if statusCode >= 200 && statusCode <= 299 {
		b.start()
	}
}

func (b *responsesStreamBridge) Write(data []byte) (int, error) {
	if b.statusCode >= 400 || !bytes.Contains(data, []byte("data:")) {
		return len(data), b.writeFailure(data)
	}
	if !b.started {
		b.start()
	}
	if err := b.writeChatSSEAsResponses(data); err != nil {
		return len(data), err
	}
	return len(data), nil
}

func (b *responsesStreamBridge) Flush() {
	if flusher, ok := b.downstream.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (b *responsesStreamBridge) start() {
	if b.started {
		return
	}
	b.started = true
	h := b.downstream.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	b.downstream.WriteHeader(http.StatusOK)
	_ = b.writeEvent(map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":         b.responseID,
			"object":     "response",
			"created_at": b.created,
			"status":     "in_progress",
			"model":      b.model,
			"output":     []any{},
		},
	})
	_ = b.ensureOutputItemStarted()
	b.Flush()
}

func (b *responsesStreamBridge) finish() {
	if !b.started {
		b.start()
	}
	if !b.failed {
		text := sanitizeInternalTraceText(b.fullText.String())
		_ = b.ensureOutputItemStarted()
		_ = b.writeEvent(map[string]any{
			"type":          "response.output_text.done",
			"item_id":       b.messageID,
			"output_index":  0,
			"content_index": 0,
			"text":          text,
		})
		_ = b.writeEvent(map[string]any{
			"type":          "response.content_part.done",
			"item_id":       b.messageID,
			"output_index":  0,
			"content_index": 0,
			"part": map[string]any{
				"type": "output_text",
				"text": text,
			},
		})
		_ = b.writeEvent(map[string]any{
			"type":         "response.output_item.done",
			"output_index": 0,
			"item": map[string]any{
				"id":     b.messageID,
				"type":   "message",
				"status": "completed",
				"role":   "assistant",
				"content": []map[string]any{
					{
						"type": "output_text",
						"text": text,
					},
				},
			},
		})
		_ = b.writeEvent(map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id":         b.responseID,
				"object":     "response",
				"created_at": b.created,
				"status":     "completed",
				"model":      b.model,
				"output": []map[string]any{
					{
						"id":     b.messageID,
						"type":   "message",
						"status": "completed",
						"role":   "assistant",
						"content": []map[string]any{
							{
								"type": "output_text",
								"text": text,
							},
						},
					},
				},
			},
		})
	}
	_, _ = fmt.Fprint(b.downstream, "data: [DONE]\n\n")
	b.Flush()
}

func (b *responsesStreamBridge) writeChatSSEAsResponses(raw []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 512*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}

		var chunk struct {
			Model   string `json:"model"`
			Created int64  `json:"created"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Model != "" {
			b.model = chunk.Model
		}
		if chunk.Created > 0 {
			b.created = chunk.Created
		}
		for _, choice := range chunk.Choices {
			delta := sanitizeInternalTraceText(choice.Delta.Content)
			if delta == "" {
				continue
			}
			if err := b.ensureOutputItemStarted(); err != nil {
				return err
			}
			b.fullText.WriteString(delta)
			if err := b.writeEvent(map[string]any{
				"type":          "response.output_text.delta",
				"item_id":       b.messageID,
				"output_index":  0,
				"content_index": 0,
				"delta":         delta,
			}); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	b.Flush()
	return nil
}

func (b *responsesStreamBridge) ensureOutputItemStarted() error {
	if !b.itemStarted {
		if err := b.writeEvent(map[string]any{
			"type":         "response.output_item.added",
			"output_index": 0,
			"item": map[string]any{
				"id":      b.messageID,
				"type":    "message",
				"status":  "in_progress",
				"role":    "assistant",
				"content": []any{},
			},
		}); err != nil {
			return err
		}
		b.itemStarted = true
	}
	if !b.partStarted {
		if err := b.writeEvent(map[string]any{
			"type":          "response.content_part.added",
			"item_id":       b.messageID,
			"output_index":  0,
			"content_index": 0,
			"part": map[string]any{
				"type": "output_text",
				"text": "",
			},
		}); err != nil {
			return err
		}
		b.partStarted = true
	}
	return nil
}

func (b *responsesStreamBridge) writeFailure(raw []byte) error {
	if !b.started {
		b.start()
	}
	message := strings.TrimSpace(string(raw))
	errorType := "server_error"
	code := "internal_error"
	var envelope OpenAIErrorEnvelope
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Error.Message != "" {
		message = envelope.Error.Message
		errorType = envelope.Error.Type
		code = envelope.Error.Code
	}
	if message == "" {
		message = "stream failed"
	}
	b.failed = true
	return b.writeEvent(map[string]any{
		"type": "response.failed",
		"response": map[string]any{
			"id":         b.responseID,
			"object":     "response",
			"created_at": b.created,
			"status":     "failed",
			"model":      b.model,
			"error": map[string]any{
				"message": message,
				"type":    errorType,
				"code":    code,
			},
		},
	})
}

func (b *responsesStreamBridge) writeEvent(payload map[string]any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(b.downstream, "data: %s\n\n", encoded)
	return err
}

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return strings.TrimSpace(value)
	}
	return ""
}

func rawBool(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value bool
	_ = json.Unmarshal(raw, &value)
	return value
}

func responsesReasoningEffort(body map[string]json.RawMessage) string {
	if effort := rawString(body["reasoning_effort"]); effort != "" {
		return effort
	}
	var reasoning map[string]json.RawMessage
	if err := json.Unmarshal(body["reasoning"], &reasoning); err != nil {
		return ""
	}
	return rawString(reasoning["effort"])
}

func textFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(encoded)
	}
}

func responseIDFromChatID(chatID string) string {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return fmt.Sprintf("resp_%d", time.Now().UnixNano())
	}
	chatID = strings.TrimPrefix(chatID, "chatcmpl-")
	return "resp_" + strings.NewReplacer("-", "_", ".", "_").Replace(chatID)
}
