package digest

import (
	"fmt"
	"strings"
	"testing"

	"tg-digest-bot/internal/domain"
)

func TestFormatDigestBuildsTopicSections(t *testing.T) {
	digest := domain.Digest{
		Overview: "День выдался насыщенным событиями.",
		Theses:   []string{"Игры стартовали", "Спорт удивил"},
		Items: []domain.DigestItem{
			{
				Post: domain.Post{URL: "https://t.me/example/1"},
				Summary: domain.Summary{
					Headline:     "Battlefield 6 установила рекорд",
					Bullets:      []string{"Старт с максимальным онлайном", "Игроки жалуются на очереди"},
					Topic:        "Игры и технологии",
					TopicSummary: "Главные релизы и анонсы из игровой индустрии.",
				},
			},
			{
				Post: domain.Post{URL: "https://t.me/example/2"},
				Summary: domain.Summary{
					Headline: "Дани Ольмо получил травму",
					Bullets:  []string{"Испания потеряла полузащитника перед матчами"},
					Topic:    "Спорт",
				},
			},
		},
	}

	formatted := FormatDigest(digest)

	mustContain(t, formatted, "🧭 <b>Итоги дня</b>")
	mustContain(t, formatted, "📌 <b>Самые главные тезисы</b>")
	mustContain(t, formatted, "🗂 <b>Темы дня</b>")
	mustContain(t, formatted, "<b>Игры и технологии</b>")
	mustContain(t, formatted, "Главные релизы и анонсы из игровой индустрии.")
	mustContain(t, formatted, "<a href=\"https://t.me/example/1\">Battlefield 6 установила рекорд</a>")
	mustContain(t, formatted, "• <a href=\"https://t.me/example/2\">Дани Ольмо получил травму</a> — Испания потеряла полузащитника перед матчами")
	mustContain(t, formatted, fmt.Sprintf("<a href=\"%s\">Дайджест создан с помощью %s</a>", footerLinkURL, footerLinkName))
}

func mustContain(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Fatalf("ожидали найти подстроку %q в %q", substr, s)
	}
}
