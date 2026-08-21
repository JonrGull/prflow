package git

import (
	"regexp"
	"sort"
	"strings"

	"github.com/JonrGull/prflow/internal/models"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ExtractTickets extracts ticket IDs from text using the given compiled regex
func ExtractTickets(text string, ticketRegex *regexp.Regexp) []string {
	if ticketRegex == nil {
		return nil
	}

	matches := ticketRegex.FindAllStringSubmatch(text, -1)

	ticketSet := make(map[string]bool)
	for _, match := range matches {
		if len(match) > 1 {
			ticket := strings.ToUpper(match[1])
			ticketSet[ticket] = true
		}
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
