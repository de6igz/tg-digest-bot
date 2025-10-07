package summarizer

import (
	"strings"
	"unicode/utf8"

	"tg-digest-bot/internal/domain"
)

// SimpleSummarizer реализует доменный интерфейс Summarizer эвристикой.
type SimpleSummarizer struct{}

// NewSimple создаёт Summarizer.
func NewSimple() *SimpleSummarizer {
	return &SimpleSummarizer{}
}

// Summarize строит заголовок и две короткие реплики из текста.
func (s *SimpleSummarizer) Summarize(post domain.Post) (domain.Summary, error) {
	text := strings.TrimSpace(post.Text)
	if text == "" {
		return domain.Summary{Headline: "Без текста", Bullets: []string{}}, nil
	}
	words := strings.Fields(text)
	headline := strings.Join(words[:min(len(words), 12)], " ")
	headline = truncate(headline, 80)
	bullets := []string{}
	remaining := words[min(len(words), 12):]
	if len(remaining) > 0 {
		b1 := truncate(strings.Join(remaining[:min(len(remaining), 25)], " "), 160)
		bullets = append(bullets, b1)
		remaining = remaining[min(len(remaining), 25):]
	}
	if len(remaining) > 0 {
		b2 := truncate(strings.Join(remaining[:min(len(remaining), 25)], " "), 160)
		bullets = append(bullets, b2)
	}
	return domain.Summary{Headline: headline, Bullets: bullets}, nil
}

func truncate(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return string(runes[:limit]) + "…"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
