package ranker

import (
"sort"
"strings"
"time"

"tg-digest-bot/internal/domain"
)

// SimpleRanker применяет эвристический скоринг.
type SimpleRanker struct {
MaxFreshnessHours float64
}

// NewSimple создаёт ранжировщик.
func NewSimple(maxFreshnessHours float64) *SimpleRanker {
return &SimpleRanker{MaxFreshnessHours: maxFreshnessHours}
}

// Rank оценивает посты.
func (r *SimpleRanker) Rank(posts []domain.Post) ([]domain.RankedPost, error) {
    posts = DeduplicateByURL(posts)
    if len(posts) == 0 {
        return nil, nil
    }
now := time.Now().UTC()
items := make([]domain.RankedPost, 0, len(posts))
for _, p := range posts {
words := len(strings.Fields(p.Text))
hasLinks := 0.0
if strings.Contains(p.Text, "http://") || strings.Contains(p.Text, "https://") {
hasLinks = 1
}
fresh := now.Sub(p.PublishedAt).Hours()
freshScore := 0.0
if fresh >= 0 && r.MaxFreshnessHours > 0 {
freshScore = 1 - minFloat(fresh/r.MaxFreshnessHours, 1)
}
score := 0.4*hasLinks + 0.4*float64(words)/200 + 0.2*freshScore
items = append(items, domain.RankedPost{Post: p, Score: score})
}
sort.Slice(items, func(i, j int) bool { return items[i].Score > items[j].Score })
return items, nil
}

func minFloat(a, b float64) float64 {
    if a < b {
        return a
    }
    return b
}

// DeduplicateByURL удаляет посты с одинаковыми ссылками.
func DeduplicateByURL(posts []domain.Post) []domain.Post {
    seen := make(map[string]struct{})
    out := make([]domain.Post, 0, len(posts))
    for _, p := range posts {
        key := p.URL
        if key == "" {
            key = p.Hash
        }
        if key == "" {
            out = append(out, p)
            continue
        }
        if _, ok := seen[key]; ok {
            continue
        }
        seen[key] = struct{}{}
        out = append(out, p)
    }
    return out
}
