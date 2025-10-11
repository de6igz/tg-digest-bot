package digest

import (
	"fmt"
	"html"
	"strings"

	"tg-digest-bot/internal/domain"
)

// FormatDigest формирует текстовое представление дайджеста для отправки пользователю.
func FormatDigest(d domain.Digest) string {
	var sections []string

	if overview := strings.TrimSpace(d.Overview); overview != "" {
		sections = append(sections, "🧭 <b>Итоги дня</b>\n"+escapeHTML(overview))
	}

	if len(d.Theses) > 0 {
		var thesesBuilder strings.Builder
		thesesBuilder.WriteString("📌 <b>Самые главные тезисы</b>\n")
		for _, thesis := range d.Theses {
			trimmed := strings.TrimSpace(thesis)
			if trimmed == "" {
				continue
			}
			thesesBuilder.WriteString("- " + escapeHTML(trimmed) + "\n")
		}
		theses := strings.TrimSpace(thesesBuilder.String())
		if theses != "" {
			sections = append(sections, theses)
		}
	}

	if topics := buildTopicSections(d.Items); topics != "" {
		sections = append(sections, topics)
	}

	//if links := buildLinksSection(d.Items); links != "" {
	//	sections = append(sections, links)
	//}

	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func buildTopicSections(items []domain.DigestItem) string {
	if len(items) == 0 {
		return ""
	}

	type topicGroup struct {
		Title   string
		Summary string
		Items   []string
	}

	const fallbackTopic = "Другие темы"

	order := make([]string, 0)
	groups := make(map[string]*topicGroup)

	for _, item := range items {
		topic := strings.TrimSpace(item.Summary.Topic)
		if topic == "" {
			topic = fallbackTopic
		}
		group, ok := groups[topic]
		if !ok {
			order = append(order, topic)
			group = &topicGroup{Title: topic}
			groups[topic] = group
		}
		if group.Summary == "" {
			if summary := strings.TrimSpace(item.Summary.TopicSummary); summary != "" {
				group.Summary = summary
			}
		}

		headline := strings.TrimSpace(item.Summary.Headline)
		bullets := filterNonEmptyStrings(item.Summary.Bullets)
		var parts []string
		if headline != "" {
			title := escapeHTML(headline)
			if url := strings.TrimSpace(item.Post.URL); url != "" {
				title = fmt.Sprintf("<a href=\"%s\">%s</a>", html.EscapeString(url), title)
			}
			parts = append(parts, title)
		}
		if len(bullets) > 0 {
			parts = append(parts, escapeHTML(strings.Join(bullets, " ")))
		}
		if len(parts) == 0 {
			continue
		}
		group.Items = append(group.Items, "• "+strings.Join(parts, " — "))
	}

	if len(order) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("🗂 <b>Темы дня</b>")
	for _, key := range order {
		group := groups[key]
		if len(group.Items) == 0 {
			continue
		}
		builder.WriteString("\n\n<b>" + escapeHTML(group.Title) + "</b>")
		if group.Summary != "" {
			builder.WriteString("\n" + escapeHTML(group.Summary))
		}
		for _, line := range group.Items {
			builder.WriteString("\n" + line)
		}
	}

	return strings.TrimSpace(builder.String())
}

func buildLinksSection(items []domain.DigestItem) string {
	if len(items) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("🔗 <b>Читать подробнее</b>\n")
	for idx, item := range items {
		label := strings.TrimSpace(item.Summary.Headline)
		if label == "" {
			label = fmt.Sprintf("Пост %d", idx+1)
		}
		url := strings.TrimSpace(item.Post.URL)
		if url == "" {
			builder.WriteString("- " + escapeHTML(label) + "\n")
			continue
		}
		builder.WriteString(fmt.Sprintf("- <a href=\"%s\">%s</a>\n", html.EscapeString(url), escapeHTML(label)))
	}
	return strings.TrimSpace(builder.String())
}

func filterNonEmptyStrings(values []string) []string {
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

func escapeHTML(s string) string {
	return html.EscapeString(s)
}
