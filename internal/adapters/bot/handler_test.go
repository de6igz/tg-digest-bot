package bot

import "testing"

func TestParseLocalTime(t *testing.T) {
	tm, err := ParseLocalTime(" 09:15 ")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if tm.Format("15:04") != "09:15" {
		t.Fatalf("expected 09:15, got %s", tm.Format("15:04"))
	}
}

func TestParseLocalTimeInvalid(t *testing.T) {
	if _, err := ParseLocalTime("9-15"); err == nil {
		t.Fatal("expected error for invalid time format")
	}
}
