package git

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// A match cut out of a longer word is not a ticket.
func TestExtractTicketsNeedsWordBoundaries(t *testing.T) {
	att := regexp.MustCompile(`(?i:ATT-)[0-9]+`)
	cases := []struct {
		re   *regexp.Regexp
		text string
		want []string
	}{
		{att, "Merge branch 'matt-12-login'", nil}, // MATT-12 is not ATT-12
		{att, "see ATT-12abc", nil},
		{att, "Merge pull request #4 from acme/jon/att-123-fix", []string{"ATT-123"}},
		{att, "(ATT-1) and ATT-2, ATT-3.", []string{"ATT-1", "ATT-2", "ATT-3"}},
		{att, "ATT-7_login", []string{"ATT-7"}},
		// An edge that is not a letter or digit is never cut.
		{regexp.MustCompile(`#[0-9]+`), "fixes PR#12 and #13", []string{"#12", "#13"}},
	}
	for _, tc := range cases {
		got := ExtractTickets(tc.text, tc.re)
		if len(got) == 0 {
			got = nil
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %v, want %v", tc.text, got, tc.want)
		}
	}
}

// The screen highlights exactly what goes into the PR.
func TestHighlightTicketsMatchesExtraction(t *testing.T) {
	re := regexp.MustCompile(`(?i:ATT-)[0-9]+`)
	got := HighlightTickets("MATT-1 fixes ATT-2", re, func(s string) string { return "[" + s + "]" })
	if got != "MATT-1 fixes [ATT-2]" {
		t.Errorf("got %q", got)
	}
	if strings.Contains(HighlightTickets("x", nil, strings.ToUpper), "X") {
		t.Error("a nil pattern changed the text")
	}
}
