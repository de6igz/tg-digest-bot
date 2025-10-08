package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.openai.com/v1"

// Client выполняет Chat Completions запросы.
type Client struct {
	http    *http.Client
	baseURL string
	apiKey  string
}

// NewClient создаёт клиента OpenAI.
func NewClient(apiKey, baseURL string, timeout time.Duration) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	httpClient := &http.Client{Timeout: timeout + 5*time.Second}
	return &Client{http: httpClient, baseURL: baseURL, apiKey: apiKey}
}

// ChatCompletionRequest описывает тело запроса.
type ChatCompletionRequest struct {
	Model          string                        `json:"model"`
	Messages       []ChatMessage                 `json:"messages"`
	Temperature    float64                       `json:"temperature,omitempty"`
	MaxTokens      int                           `json:"max_tokens,omitempty"`
	ResponseFormat *ChatCompletionResponseFormat `json:"response_format,omitempty"`
}

// ChatMessage представляет сообщение в диалоге.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

const (
	// RoleSystem системная инструкция.
	RoleSystem = "system"
	// RoleUser сообщение пользователя.
	RoleUser = "user"
)

// ChatCompletionResponseFormat задаёт формат ответа.
type ChatCompletionResponseFormat struct {
	Type string `json:"type"`
}

const (
	// ResponseFormatTypeJSONObject просит вернуть объект JSON.
	ResponseFormatTypeJSONObject = "json_object"
)

// ChatCompletionResponse описывает ответ модели.
type ChatCompletionResponse struct {
	Choices []ChatCompletionChoice `json:"choices"`
}

// ChatCompletionChoice содержит сообщение модели.
type ChatCompletionChoice struct {
	Message ChatMessage `json:"message"`
}

// CreateChatCompletion вызывает /chat/completions.
func (c *Client) CreateChatCompletion(ctx context.Context, req ChatCompletionRequest) (ChatCompletionResponse, error) {
	if c.apiKey == "" {
		return ChatCompletionResponse{}, fmt.Errorf("openai: api key is empty")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return ChatCompletionResponse{}, fmt.Errorf("openai: marshal request: %w", err)
	}
	endpoint := c.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ChatCompletionResponse{}, fmt.Errorf("openai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return ChatCompletionResponse{}, fmt.Errorf("openai: do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatCompletionResponse{}, fmt.Errorf("openai: read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		var apiErr apiErrorResponse
		if err := json.Unmarshal(respBody, &apiErr); err == nil && apiErr.Error.Message != "" {
			return ChatCompletionResponse{}, fmt.Errorf("openai: %s", apiErr.Error.Message)
		}
		return ChatCompletionResponse{}, fmt.Errorf("openai: unexpected status %d", resp.StatusCode)
	}
	var completion ChatCompletionResponse
	if err := json.Unmarshal(respBody, &completion); err != nil {
		return ChatCompletionResponse{}, fmt.Errorf("openai: decode response: %w", err)
	}
	return completion, nil
}

type apiErrorResponse struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}
