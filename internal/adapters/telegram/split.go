package telegram

import "strings"

const messageLimit = 4096

// SplitMessage breaks the text into chunks that respect Telegram's message size limit.
// It prefers to split on newline boundaries so formatted blocks stay intact.
func SplitMessage(text string) []string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}

	runes := []rune(trimmed)
	if len(runes) <= messageLimit {
		return []string{trimmed}
	}

	var parts []string
	for start := 0; start < len(runes); {
		end := start + messageLimit
		if end >= len(runes) {
			chunk := strings.Trim(string(runes[start:]), "\n")
			if chunk != "" {
				parts = append(parts, chunk)
			}
			break
		}

		split := -1
		for i := end; i > start; i-- {
			if runes[i-1] == '\n' {
				split = i
				break
			}
		}
		if split == -1 {
			split = end
		}

		chunk := strings.Trim(string(runes[start:split]), "\n")
		if chunk != "" {
			parts = append(parts, chunk)
		}

		start = split
		for start < len(runes) && runes[start] == '\n' {
			start++
		}
	}

	if len(parts) == 0 {
		return []string{trimmed}
	}

	return parts
}
