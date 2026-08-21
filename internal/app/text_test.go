package app

import "testing"

// Every hand-rolled text input here used to slice off a byte on backspace, and
// four truncations plus both typewriter reveals did the same. That splits
// multi-byte characters and leaves invalid UTF-8 behind — visible as replacement
// glyphs partway through a Japanese repo name or an emoji.

func TestTrimLastRune(t *testing.T) {
	tests := []struct{ in, want string }{
		{"abc", "ab"},
		{"a", ""},
		{"", ""},
		{"日本語", "日本"}, // 3 bytes per rune
		{"ab日", "ab"}, // mixed widths
		{"🚀🚀", "🚀"},   // 4-byte runes
		{"a🚀", "a"},
		{"café", "caf"}, // combining-free accented rune
	}
	for _, tt := range tests {
		if got := trimLastRune(tt.in); got != tt.want {
			t.Errorf("trimLastRune(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRevealRunes(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 0, ""},
		{"hello", 3, "hel"},
		{"hello", 99, "hello"}, // clamps rather than panicking
		{"hello", -1, ""},      // typewriterPos should never go negative, but don't crash
		{"日本語", 2, "日本"},
		{"🚀ok", 1, "🚀"},
	}
	for _, tt := range tests {
		if got := revealRunes(tt.in, tt.n); got != tt.want {
			t.Errorf("revealRunes(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
		}
	}
}

// The main menu describes what the tool will do, so it has to describe the
// user's actual config. It previously asserted "ATT-XXX" as a literal.
func TestTicketExample(t *testing.T) {
	tests := map[string]string{
		"ATT-[0-9]+":  "ATT-123",
		`PROJ-\d+`:    "PROJ-123",
		"JIRA-[0-9]+": "JIRA-123",
		"":            "disabled",
		// The shipped default, which names no particular project.
		"[A-Z][A-Z0-9]+-[0-9]+": "ABC-123",
		// Anything still regex-shaped is shown verbatim rather than turned into
		// a confidently wrong example.
		"(FOO|BAR)-[0-9]+": "(FOO|BAR)-[0-9]+",
	}
	for pattern, want := range tests {
		if got := ticketExample(pattern); got != want {
			t.Errorf("ticketExample(%q) = %q, want %q", pattern, got, want)
		}
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		in     string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"truncate me", 8, "truncat…"}, // maxLen counts the ellipsis
		{"日本語のリポジトリ", 5, "日本語の…"},
		{"🚀🚀🚀🚀", 3, "🚀🚀…"},
		{"abc", 0, ""}, // degenerate width must not panic
		{"abc", 1, "…"},
	}
	for _, tt := range tests {
		got := truncateString(tt.in, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncateString(%q, %d) = %q, want %q", tt.in, tt.maxLen, got, tt.want)
		}
		if n := len([]rune(got)); tt.maxLen > 0 && n > tt.maxLen {
			t.Errorf("truncateString(%q, %d) returned %d runes, over the limit", tt.in, tt.maxLen, n)
		}
	}
}
