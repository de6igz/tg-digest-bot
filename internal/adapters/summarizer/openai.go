package summarizer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tg-digest-bot/internal/domain"
	openai "tg-digest-bot/internal/infra/openai"
)

type chatClient interface {
	CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

// OpenAI реализует summarizer через OpenAI Chat Completions.
type OpenAI struct {
	client  chatClient
	model   string
	timeout time.Duration
}

// NewOpenAI создаёт провайдер суммаризации.
func NewOpenAI(client chatClient, model string, timeout time.Duration) *OpenAI {
	if model == "" {
		model = "gpt-4.1-mini"
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &OpenAI{client: client, model: model, timeout: timeout}
}

type summaryPayload struct {
	Headline string   `json:"headline"`
	Bullets  []string `json:"bullets"`
}

// Summarize строит краткое содержание поста.
func (s *OpenAI) Summarize(post domain.Post) (domain.Summary, error) {
	text := strings.TrimSpace(post.Text)
	if text == "" {
		return domain.Summary{Headline: "Пост без текста"}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	userPrompt := fmt.Sprintf(`Подготовь краткое резюме телеграм-поста на русском языке.
Верни JSON формата {"headline": "...", "bullets": ["..."]} без пояснений.
Текст поста:
%s`, clipRunes(text, 2000))

	req := openai.ChatCompletionRequest{
		Model:       s.model,
		Temperature: 0.2,
		MaxTokens:   300,
		Messages: []openai.ChatMessage{
			{
				Role:    openai.RoleSystem,
				Content: "Ты помощник-редактор. Сохраняй факты из текста и не выдумывай ничего нового.",
			},
			{
				Role:    openai.RoleUser,
				Content: userPrompt,
			},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ResponseFormatTypeJSONObject},
	}

	resp, err := s.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return domain.Summary{}, fmt.Errorf("openai completion: %w", err)
	}
	if len(resp.Choices) == 0 {
		return domain.Summary{}, fmt.Errorf("openai completion: пустой ответ")
	}
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	var parsed summaryPayload
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return domain.Summary{}, fmt.Errorf("распаковка ответа LLM: %w", err)
	}
	return domain.Summary{
		Headline: strings.TrimSpace(parsed.Headline),
		Bullets:  filterValues(parsed.Bullets),
	}, nil
}

func filterValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func clipRunes(text string, limit int) string {
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
