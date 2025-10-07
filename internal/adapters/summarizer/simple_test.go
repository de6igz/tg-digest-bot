package summarizer

import (
"strings"
"testing"

"tg-digest-bot/internal/domain"
)

func TestSummarize(t *testing.T) {
s := NewSimple()
post := domain.Post{Text: strings.Repeat("слово ", 50)}
sum, err := s.Summarize(post)
if err != nil {
t.Fatalf("не ожидали ошибку: %v", err)
}
if sum.Headline == "" || len(sum.Bullets) == 0 {
t.Fatalf("ожидали заполненный заголовок и буллеты")
}
}
