package ranker

import (
    "strings"
    "testing"
    "time"

    "tg-digest-bot/internal/domain"
)

func TestDeduplicateByURL(t *testing.T) {
posts := []domain.Post{
{URL: "https://t.me/a/1"},
{URL: "https://t.me/a/1"},
{URL: "https://t.me/a/2"},
}
res := DeduplicateByURL(posts)
if len(res) != 2 {
t.Fatalf("ожидали 2 поста, получили %d", len(res))
}
}

func TestRankOrdersByScore(t *testing.T) {
r := NewSimple(24)
posts := []domain.Post{
{URL: "https://t.me/a/1", Text: strings.Repeat("a ", 50), PublishedAt: time.Now().Add(-time.Hour)},
{URL: "https://t.me/a/2", Text: "короткий", PublishedAt: time.Now().Add(-2 * time.Hour)},
}
ranked, err := r.Rank(posts)
if err != nil {
t.Fatalf("не ожидали ошибку: %v", err)
}
if len(ranked) != 2 {
t.Fatalf("ожидали 2 элемента")
}
if ranked[0].Post.URL != "https://t.me/a/1" {
t.Fatalf("ожидали первым длинный пост")
}
}
