package channels

import "testing"

func TestParseAlias(t *testing.T) {
	cases := map[string]string{
		"@Example":       "example",
		"https://t.me/A": "",
		"t.me/golang":    "golang",
	}
	for input, expected := range cases {
		alias, err := ParseAlias(input)
		if expected == "" {
			if err == nil {
				t.Fatalf("ожидали ошибку для %s", input)
			}
			continue
		}
		if err != nil {
			t.Fatalf("не ожидали ошибку: %v", err)
		}
		if alias != expected {
			t.Fatalf("ожидали %s, получили %s", expected, alias)
		}
	}
}

func TestNormalizeTags(t *testing.T) {
	input := []string{"  Новости ", "игры", "Новости", "", "Игры"}
	normalized := NormalizeTags(input)
	if len(normalized) != 2 {
		t.Fatalf("ожидали 2 тега, получили %d", len(normalized))
	}
	if normalized[0] != "Новости" || normalized[1] != "игры" {
		t.Fatalf("неожиданное содержимое: %#v", normalized)
	}
}
