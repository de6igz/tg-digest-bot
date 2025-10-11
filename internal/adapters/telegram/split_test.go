package telegram

import (
	"strings"
	"testing"
)

func TestSplitMessageRespectsLimit(t *testing.T) {
	var builder strings.Builder
	builder.WriteString(strings.Repeat("a", 3000))
	builder.WriteString("\n\n")
	builder.WriteString(strings.Repeat("b", 2000))
	builder.WriteString("\n")
	builder.WriteString(strings.Repeat("c", 500))

	parts := SplitMessage(builder.String())
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}

	for i, part := range parts {
		if length := len([]rune(part)); length > messageLimit {
			t.Fatalf("part %d exceeds limit: %d", i, length)
		}
	}

	if parts[0] != strings.Repeat("a", 3000) {
		t.Fatalf("unexpected content in first part")
	}

	if parts[1][0] != 'b' {
		t.Fatalf("unexpected prefix for second part: %q", parts[1][0])
	}

	if !strings.HasSuffix(parts[1], strings.Repeat("c", 500)) {
		t.Fatalf("second part should contain trailing block of 'c'")
	}
}

func TestSplitMessageShortText(t *testing.T) {
	text := "hello world"
	parts := SplitMessage(text)
	if len(parts) != 1 {
		t.Fatalf("expected single part, got %d", len(parts))
	}
	if parts[0] != text {
		t.Fatalf("unexpected text: %q", parts[0])
	}
}

func TestSplitMessageEmpty(t *testing.T) {
	parts := SplitMessage("   \n  ")
	if len(parts) != 0 {
		t.Fatalf("expected no parts for empty input, got %d", len(parts))
	}
}
