package ranker

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/rs/zerolog/log"
	"strings"
	"time"

	"tg-digest-bot/internal/domain"
	openai "tg-digest-bot/internal/infra/openai"
)

type chatCompletionClient interface {
	CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

// LLMRanker использует LLM для группировки и аннотирования постов.
type LLMRanker struct {
	client   chatCompletionClient
	model    string
	timeout  time.Duration
	maxItems int
}

// NewLLM создаёт ранжировщик на базе OpenAI Chat Completions.
func NewLLM(client chatCompletionClient, model string, timeout time.Duration, maxItems int) *LLMRanker {
	if maxItems <= 0 {
		maxItems = 10
	}
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	return &LLMRanker{client: client, model: model, timeout: timeout, maxItems: maxItems}
}

type llmPostPayload struct {
	ID          int    `json:"id"`
	ChannelID   int64  `json:"channel_id"`
	TGMessageID int64  `json:"tg_message_id"`
	PublishedAt string `json:"published_at"`
	URL         string `json:"url,omitempty"`
	Text        string `json:"text"`
}

type llmDigestResponse struct {
	Overview string             `json:"overview"`
	Theses   []string           `json:"theses"`
	Posts    []llmDigestPostRef `json:"posts"`
}

type llmDigestPostRef struct {
	PostID       json.Number `json:"post_id"`
	Title        string      `json:"title"`
	Summary      string      `json:"summary"`
	Topic        string      `json:"topic"`
	TopicSummary string      `json:"topic_summary"`
}

// Rank анализирует посты с помощью LLM и формирует дайджест.
func (r *LLMRanker) Rank(posts []domain.Post) (domain.DigestOutline, error) {
	posts = DeduplicateByURL(posts)
	if len(posts) == 0 {
		return domain.DigestOutline{}, nil
	}
	payload := make([]llmPostPayload, 0, len(posts))
	postMap := make(map[int]domain.Post, len(posts))
	for idx, post := range posts {
		id := idx + 1
		postMap[id] = post
		payload = append(payload, llmPostPayload{
			ID:          id,
			ChannelID:   post.ChannelID,
			TGMessageID: post.TGMsgID,
			PublishedAt: post.PublishedAt.UTC().Format(time.RFC3339),
			URL:         post.URL,
			Text:        truncate(post.Text, 4000),
		})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return domain.DigestOutline{}, fmt.Errorf("marshal posts: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	userPrompt := fmt.Sprintf(`
Проанализируй список постов телеграм-каналов пользователя и подготовь короткий дайджест на русском языке.
1. Сформулируй один абзац, который кратко описывает общую картину дня.
2. Выдели 3-5 ключевых тезисов — короткие предложения, отражающие самые важные идеи.
3. Сгруппируй посты по темам: для каждой темы придумай короткий заголовок (2-4 слова) и одно предложение-описание, а также укажи тему у каждого выбранного поста.
4. Выбери до %d постов, которые стоит прочитать, и для каждого придумай ёмкий заголовок и 1-2 предложения пояснения.
5. Всегда используй поле "id" из входных данных как "post_id" в ответе и не придумывай новых идентификаторов.
6. Ответ верни строго в формате JSON: {"overview": "...", "theses": ["..."], "posts": [{"post_id": 1, "title": "...", "summary": "...", "topic": "...", "topic_summary": "..."}]}.

Вот данные постов в JSON:
%s`, r.maxItems, string(body))

	req := openai.ChatCompletionRequest{
		//Model:       r.model,
		Model: "gpt-5-mini",
		//Temperature: 0.2,
		//MaxTokens:   8000,
		Messages: []openai.ChatMessage{
			{
				Role:    openai.RoleSystem,
				Content: "Ты редактор русскоязычного новостного дайджеста. Пиши только проверенные факты из данных постов и не добавляй выдумок.",
			},
			{
				Role:    openai.RoleUser,
				Content: userPrompt,
			},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ResponseFormatTypeJSONObject,
		},
	}

	resp, err := r.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return domain.DigestOutline{}, fmt.Errorf("openai completion: %w", err)
	}
	if len(resp.Choices) == 0 {
		return domain.DigestOutline{}, fmt.Errorf("openai completion: пустой ответ")
	}
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	var parsed llmDigestResponse
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return domain.DigestOutline{}, fmt.Errorf("распаковка ответа LLM: %w", err)
	}

	outline := domain.DigestOutline{
		Overview: strings.TrimSpace(parsed.Overview),
		Theses:   filterNonEmpty(parsed.Theses),
	}
	log.Info().Msgf("%v", resp.Usage.String())
	for _, ref := range parsed.Posts {
		if len(outline.Items) >= r.maxItems {
			break
		}
		id, err := ref.PostID.Int64()
		if err != nil {
			continue
		}
		post, ok := postMap[int(id)]
		if !ok {
			continue
		}
		headline := strings.TrimSpace(ref.Title)
		summary := strings.TrimSpace(ref.Summary)
		item := domain.RankedPost{
			Post:  post,
			Score: float64(r.maxItems - len(outline.Items)),
			Summary: domain.Summary{
				Headline:     headline,
				Topic:        strings.TrimSpace(ref.Topic),
				TopicSummary: strings.TrimSpace(ref.TopicSummary),
			},
		}
		if summary != "" {
			item.Summary.Bullets = []string{summary}
		}
		outline.Items = append(outline.Items, item)
	}
	return outline, nil
}

func filterNonEmpty(values []string) []string {
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

func truncate(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}
