package app

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

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
// user's actual config. It previously asserted one project's key as a
// literal.
func TestTicketExample(t *testing.T) {
	tests := map[string]string{
		"ACME-[0-9]+": "ACME-123",
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
		{"truncate me", 8, "truncat…"}, // the limit counts the ellipsis
		// Wide characters take two columns each. These used to expect a rune
		// count — "日本語の…" is 9 columns in a 5-column slot, which is the
		// overflow that pushed the All PRs columns off their row.
		{"日本語のリポジトリ", 5, "日本…"},
		{"🚀🚀🚀🚀", 3, "🚀…"},
		{"feature/very-long-branch → staging", 12, "feature/ver…"},
		{"abc", 0, ""}, // degenerate width must not panic
		{"abc", 1, "…"},
	}
	for _, tt := range tests {
		got := truncateString(tt.in, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncateString(%q, %d) = %q, want %q", tt.in, tt.maxLen, got, tt.want)
		}
		if w := lipgloss.Width(got); w > tt.maxLen {
			t.Errorf("truncateString(%q, %d) is %d columns wide, over the limit", tt.in, tt.maxLen, w)
		}
	}
}

// The All PRs table pads and truncates each column. Truncating titles by rune
// count let a CJK title run twice as wide as its column and push the status
// cells off the row, and cutting the branch label by bytes could split the
// three-byte "→" into invalid UTF-8.
func TestAllPRsRowsStayAlignedWithWideText(t *testing.T) {
	sgr := regexp.MustCompile("\x1b\\[[0-9;]*m")
	endOf := func(out, cell string) int {
		for _, l := range strings.Split(sgr.ReplaceAllString(out, ""), "\n") {
			if i := strings.Index(l, cell); i >= 0 {
				return lipgloss.Width(l[:i]) + lipgloss.Width(cell)
			}
		}
		return -1
	}

	repos := testRepos()
	for _, width := range []int{80, 120} {
		for n := 1; n <= 60; n++ {
			ascii := makeAllPREntry(repos[0], 101, "Add the banner", "dev", "success", "success", "current", 12, 12, 0)
			wide := makeAllPREntry(repos[2], 7, strings.Repeat("日本語のプルリクエスト", 4), strings.Repeat("b", n), "failure", "pending", "unresponded", 3, 10, 7)
			ascii.PR.BaseBranch, wide.PR.BaseBranch = "staging", "staging"

			m := Model{config: testConfig(), screen: ScreenViewAllPrs, width: width, height: 40}
			m.allPRs.entries = []allPREntry{ascii, wide}
			out := m.renderViewAllPrsWithHeight(30)

			if !utf8.ValidString(out) {
				t.Fatalf("width %d, %d-char branch: rendered invalid UTF-8", width, n)
			}
			if a, b := endOf(out, "12/12"), endOf(out, "3/10"); a != b {
				t.Fatalf("width %d, %d-char branch: E2E cells end at columns %d and %d", width, n, a, b)
			}
		}
	}
}
