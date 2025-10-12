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

	"tg-digest-bot/internal/infra/metrics"
)

const defaultBaseURL = "https://api.openai.com/v1"

// const defaultBaseURL = "http://localhost:11434/v1" // for local testing with mock server
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
	Usage   *ChatCompletionUsage   `json:"usage,omitempty"`
}

// ChatCompletionChoice содержит сообщение модели.
type ChatCompletionChoice struct {
	Message ChatMessage `json:"message"`
}

// ChatCompletionUsage описывает статистику использования токенов.
type ChatCompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (c *ChatCompletionUsage) String() string {
	b, err := json.Marshal(c)
	if err != nil {
		return fmt.Sprintf("ChatCompletionResponse{error: %v}", err)
	}
	return string(b)
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

	start := time.Now()
	resp, err := c.http.Do(httpReq)
	if err != nil {
		metrics.ObserveNetworkRequest("openai", "chat_completions", req.Model, start, err)
		return ChatCompletionResponse{}, fmt.Errorf("openai: do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		metrics.ObserveNetworkRequest("openai", "chat_completions", req.Model, start, err)
		return ChatCompletionResponse{}, fmt.Errorf("openai: read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		var apiErr apiErrorResponse
		if err := json.Unmarshal(respBody, &apiErr); err == nil && apiErr.Error.Message != "" {
			err = fmt.Errorf("openai: %s", apiErr.Error.Message)
		} else {
			err = fmt.Errorf("openai: unexpected status %d", resp.StatusCode)
		}
		metrics.ObserveNetworkRequest("openai", "chat_completions", req.Model, start, err)
		return ChatCompletionResponse{}, err
	}
	var completion ChatCompletionResponse
	if err := json.Unmarshal(respBody, &completion); err != nil {
		metrics.ObserveNetworkRequest("openai", "chat_completions", req.Model, start, err)
		return ChatCompletionResponse{}, fmt.Errorf("openai: decode response: %w", err)
	}
	metrics.ObserveNetworkRequest("openai", "chat_completions", req.Model, start, nil)
	if completion.Usage != nil {
		metrics.ObserveLLMGeneration(req.Model, time.Since(start), completion.Usage.PromptTokens, completion.Usage.CompletionTokens, completion.Usage.TotalTokens)
	}
	return completion, nil
}

type apiErrorResponse struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}
