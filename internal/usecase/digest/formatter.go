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
	overview := strings.TrimSpace(d.Overview)
	if overview != "" {
		sections = append(sections, escapeHTML(overview))
	}
	if len(d.Theses) > 0 {
		var thesesBuilder strings.Builder
		thesesBuilder.WriteString("Самые главные тезисы:\n")
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
	if len(d.Items) > 0 {
		var linksBuilder strings.Builder
		linksBuilder.WriteString("Читать подробнее:\n")
		for idx, item := range d.Items {
			label := strings.TrimSpace(item.Summary.Headline)
			if label == "" {
				label = fmt.Sprintf("Пост %d", idx+1)
			}
			url := strings.TrimSpace(item.Post.URL)
			if url == "" {
				linksBuilder.WriteString("- " + escapeHTML(label) + "\n")
				continue
			}
			linksBuilder.WriteString(fmt.Sprintf("- <a href=\"%s\">%s</a>\n", html.EscapeString(url), escapeHTML(label)))
		}
		links := strings.TrimSpace(linksBuilder.String())
		if links != "" {
			sections = append(sections, links)
		}
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func escapeHTML(s string) string {
	return html.EscapeString(s)
}
