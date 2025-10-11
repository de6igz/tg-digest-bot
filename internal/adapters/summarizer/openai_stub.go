package summarizer

import (
	"strings"

	"tg-digest-bot/internal/domain"
)

// OpenAIStub имитирует работу LLM-провайдера OpenAI.
type OpenAIStub struct{}

// NewOpenAIStub создаёт заглушку.
func NewOpenAIStub() *OpenAIStub {
	return &OpenAIStub{}
}

// Summarize возвращает простое краткое содержание поста.
func (s *OpenAIStub) Summarize(post domain.Post) (domain.Summary, error) {
	text := strings.TrimSpace(post.Text)
	if text == "" {
		return domain.Summary{Headline: "Новый пост"}, nil
	}
	lines := strings.Split(text, "\n")
	headline := strings.TrimSpace(lines[0])
	if headline == "" {
		headline = "Новый пост"
	}
	if len(headline) > 120 {
		headline = headline[:120] + "…"
	}
	bullets := make([]string, 0, 3)
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		bullets = append(bullets, trimmed)
		if len(bullets) == 3 {
			break
		}
	}
	return domain.Summary{Headline: headline, Bullets: bullets}, nil
}
