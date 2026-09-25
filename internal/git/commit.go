package git

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/JonrGull/prflow/internal/models"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// findTickets returns where ticketRegex matches in text, skipping any match
// cut out of a longer word: STOPS-12 is not OPS-12, and PROJ-12abc is not
// PROJ-12. A match edge only counts as cut when a letter or digit sits on
// both sides of it, so a pattern starting with # still matches in "see #12".
func findTickets(text string, ticketRegex *regexp.Regexp) [][]int {
	if ticketRegex == nil {
		return nil
	}
	var spans [][]int
	for _, loc := range ticketRegex.FindAllStringIndex(text, -1) {
		if loc[0] == loc[1] {
			continue
		}
		before, _ := utf8.DecodeLastRuneInString(text[:loc[0]])
		first, _ := utf8.DecodeRuneInString(text[loc[0]:])
		last, _ := utf8.DecodeLastRuneInString(text[:loc[1]])
		after, _ := utf8.DecodeRuneInString(text[loc[1]:])
		if (isWordRune(before) && isWordRune(first)) || (isWordRune(last) && isWordRune(after)) {
			continue
		}
		spans = append(spans, loc)
	}
	return spans
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// HighlightTickets returns text with style applied to each ticket ID, using
// the same rules as ExtractTickets so the screen marks exactly what goes into
// the PR.
func HighlightTickets(text string, ticketRegex *regexp.Regexp, style func(string) string) string {
	var b strings.Builder
	prev := 0
	for _, span := range findTickets(text, ticketRegex) {
		b.WriteString(text[prev:span[0]])
		b.WriteString(style(text[span[0]:span[1]]))
		prev = span[1]
	}
	b.WriteString(text[prev:])
	return b.String()
}

// ExtractTickets extracts ticket IDs from text using the given compiled regex
func ExtractTickets(text string, ticketRegex *regexp.Regexp) []string {
	if ticketRegex == nil {
		return nil
	}

	ticketSet := make(map[string]bool)
	for _, span := range findTickets(text, ticketRegex) {
		ticketSet[strings.ToUpper(text[span[0]:span[1]])] = true
	}

	// Convert to sorted slice
	tickets := make([]string, 0, len(ticketSet))
	for ticket := range ticketSet {
		tickets = append(tickets, ticket)
	}
	sort.Strings(tickets)

	return tickets
}

// GetCommitsBetween gets commits between two branches (base..head)
// Returns commits that are in head but not in base
func GetCommitsBetween(repoPath, baseBranch, headBranch string, ticketRegex *regexp.Regexp) ([]models.CommitInfo, error) {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return nil, err
	}

	baseRef := "refs/remotes/origin/" + baseBranch
	headRef := "refs/remotes/origin/" + headBranch

	baseHash, err := repo.ResolveRevision(plumbing.Revision(baseRef))
	if err != nil {
		return nil, &BranchNotFoundError{Branches: []string{baseBranch}}
	}

	headHash, err := repo.ResolveRevision(plumbing.Revision(headRef))
	if err != nil {
		return nil, &BranchNotFoundError{Branches: []string{headBranch}}
	}

	// Build set of commits reachable from base
	baseCommits := make(map[plumbing.Hash]bool)
	baseIter, err := repo.Log(&git.LogOptions{From: *baseHash})
	if err != nil {
		return nil, err
	}
	baseIter.ForEach(func(c *object.Commit) error {
		baseCommits[c.Hash] = true
		return nil
	})

	// Get commits from head that are not in base
	headIter, err := repo.Log(&git.LogOptions{From: *headHash})
	if err != nil {
		return nil, err
	}

	var commits []models.CommitInfo
	seen := make(map[plumbing.Hash]bool)
	err = headIter.ForEach(func(c *object.Commit) error {
		// Skip if already processed or reachable from base.
		// Don't stop iteration - merge commits have multiple parents
		// and we need to traverse all paths to find feature commits.
		if seen[c.Hash] || baseCommits[c.Hash] {
			return nil
		}
		seen[c.Hash] = true

		hash := c.Hash.String()[:7]
		message := strings.Split(c.Message, "\n")[0]      // First line for display
		tickets := ExtractTickets(c.Message, ticketRegex) // Full message for tickets

		commits = append(commits, models.NewCommitInfo(hash, message, tickets))
		return nil
	})

	if err != nil {
		return nil, err
	}

	return commits, nil
}

// GetAllTickets gets all unique tickets from a list of commits
func GetAllTickets(commits []models.CommitInfo) []string {
	ticketSet := make(map[string]bool)

	for _, commit := range commits {
		for _, ticket := range commit.Tickets {
			ticketSet[ticket] = true
		}
	}

	tickets := make([]string, 0, len(ticketSet))
	for ticket := range ticketSet {
		tickets = append(tickets, ticket)
	}
	sort.Strings(tickets)

	return tickets
}
