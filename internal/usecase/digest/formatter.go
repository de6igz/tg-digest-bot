package digest

import (
	"fmt"
	"html"
	"strings"

	"tg-digest-bot/internal/domain"
)

// FormatDigest формирует текстовое представление дайджеста для отправки пользователю.
func FormatDigest(d domain.Digest) string {
	var b strings.Builder
	b.WriteString("📰 Дайджест за 24 часа\n\n")
	for i, item := range d.Items {
		title := strings.TrimSpace(item.Summary.Headline)
		if title == "" {
			title = fmt.Sprintf("Запись #%d", i+1)
		}
		b.WriteString(fmt.Sprintf("%d. <b>%s</b>\n", i+1, escapeHTML(title)))
		if len(item.Summary.Bullets) > 0 {
			for _, bullet := range item.Summary.Bullets {
				trimmed := strings.TrimSpace(bullet)
				if trimmed == "" {
					continue
				}
				b.WriteString("• " + escapeHTML(trimmed) + "\n")
			}
		}
		if item.Post.URL != "" {
			b.WriteString(fmt.Sprintf("<a href=\"%s\">Читать</a>\n", html.EscapeString(item.Post.URL)))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func escapeHTML(s string) string {
	return html.EscapeString(s)
}
