package app

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/JonrGull/prflow/internal/models"
)

// Derived PR status columns for the All Open PRs screen.
//
// This was one 170-line function with its rules — and a compiled regex —
// rebuilt inline on every call, for every PR, on every auto-refresh. Splitting
// it into named steps makes each rule testable and states what it is for.

// noiseAuthors never post actionable feedback: their comments are status
// updates that already have their own columns on screen.
var noiseAuthors = map[string]bool{
	"linear":         true, // linkback comments only
	"github-actions": true, // E2E/deploy status
}

// boilerplateMarkers identify generated comments regardless of who posted them.
var boilerplateMarkers = []string{
	"<!-- linear-linkback -->",
	"<!-- e2e-results -->",
	"You have reached your Codex usage limits",
	"## Preview Deployment",
}

// e2eRegex pulls the counts out of the E2E results comment. Hoisted to a
// package var: it used to be recompiled inside the per-PR loop.
var e2eRegex = regexp.MustCompile(`\*\*(\d+)\*\* passed, \*\*(\d+)\*\* failed`)

// ciExcluded checks are reported in their own columns rather than folded into
// the overall CI status.
var ciExcluded = map[string]bool{"deploy-preview": true, "e2e": true}

func isBoilerplate(body string) bool {
	for _, marker := range boilerplateMarkers {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

// isNoise reports whether a comment should be ignored when deciding if the
// author still owes someone a reply.
func isNoise(login, body, prAuthor string) bool {
	return login == prAuthor || noiseAuthors[login] || isBoilerplate(body)
}

// latestAuthorAction returns the timestamp of the PR author's most recent
// comment or review — the point after which incoming feedback is unanswered.
//
// Timestamps are compared as strings. That is safe for the UTC RFC 3339 values
// GitHub returns, where lexical and chronological order agree, but it would be
// wrong for mixed offsets. If this ever handles other sources, parse instead.
func latestAuthorAction(entry *allPREntry) string {
	latest := ""
	author := entry.PR.Author.Login

	for _, c := range entry.PR.Comments {
		if c.Author.Login == author && c.CreatedAt > latest {
			latest = c.CreatedAt
		}
	}
	for _, r := range entry.PR.Reviews {
		if r.Author.Login == author && r.SubmittedAt > latest {
			latest = r.SubmittedAt
		}
	}
	for _, ic := range entry.InlineComments {
		if ic.User.Login == author && ic.CreatedAt > latest {
			latest = ic.CreatedAt
		}
	}
	return latest
}

// computeUnresponded counts substantive comments left after the author's last
// action — i.e. feedback still waiting on them.
func computeUnresponded(entry *allPREntry) int {
	author := entry.PR.Author.Login
	since := latestAuthorAction(entry)

	count := 0
	for _, c := range entry.PR.Comments {
		if isNoise(c.Author.Login, c.Body, author) {
			continue
		}
		if c.CreatedAt > since {
			count++
		}
	}
	for _, ic := range entry.InlineComments {
		if isNoise(ic.User.Login, ic.Body, author) {
			continue
		}
		if ic.CreatedAt > since {
			count++
		}
	}
	return count
}

// computeCommentCount totals issue comments plus review comments, falling back
// to review events when the inline comments were not fetched.
func computeCommentCount(entry *allPREntry) int {
	reviewCount := len(entry.InlineComments)
	if reviewCount == 0 {
		reviewCount = len(entry.PR.Reviews)
	}
	return len(entry.PR.Comments) + reviewCount
}

// computeReviewStatus classifies where the PR sits in review.
// unresponded is passed in because it takes precedence over staleness.
func computeReviewStatus(entry *allPREntry, unresponded int) string {
	if len(entry.PR.ReviewRequests) > 0 || entry.HasPendingReview {
		return "pending"
	}
	if len(entry.PR.LatestReviews) == 0 {
		return "none"
	}
	if unresponded > 0 {
		return "unresponded"
	}

	latestCommit := ""
	for _, c := range entry.PR.Commits {
		if c.AuthoredDate > latestCommit {
			latestCommit = c.AuthoredDate
		}
	}
	latestReview := ""
	for _, r := range entry.PR.LatestReviews {
		if r.SubmittedAt > latestReview {
			latestReview = r.SubmittedAt
		}
	}
	if latestCommit > latestReview {
		return "stale" // new commits since the last review
	}
	return "current"
}

// computeCIStatus aggregates every check except those with their own column.
func computeCIStatus(checks []models.CheckRun) string {
	hasAny, hasFailure, hasPending := false, false, false
	for _, cr := range checks {
		if ciExcluded[cr.Name] {
			continue
		}
		hasAny = true
		switch {
		case strings.EqualFold(cr.Conclusion, "failure"):
			hasFailure = true
		case strings.EqualFold(cr.Status, "in_progress"), strings.EqualFold(cr.Status, "queued"):
			hasPending = true
		}
	}
	switch {
	case !hasAny:
		return "none"
	case hasFailure:
		return "failure"
	case hasPending:
		return "pending"
	default:
		return "success"
	}
}

// computePreviewStatus reports the deploy-preview check specifically.
func computePreviewStatus(checks []models.CheckRun) string {
	for _, cr := range checks {
		if cr.Name != "deploy-preview" {
			continue
		}
		switch {
		case strings.EqualFold(cr.Conclusion, "failure"):
			return "failure"
		case strings.EqualFold(cr.Status, "in_progress"), strings.EqualFold(cr.Status, "queued"):
			return "pending"
		case strings.EqualFold(cr.Conclusion, "success"):
			return "success"
		}
		break
	}
	return "none"
}

// computeE2E reads the latest E2E results comment.
// passed is -1 when there is no such comment, which the renderer shows as "no
// data" rather than as zero passing tests.
func computeE2E(comments []models.PrComment) (passed, failed, total int) {
	for i := len(comments) - 1; i >= 0; i-- {
		c := comments[i]
		if c.Author.Login != "github-actions" || !strings.Contains(c.Body, "<!-- e2e-results -->") {
			continue
		}
		if m := e2eRegex.FindStringSubmatch(c.Body); len(m) == 3 {
			p, _ := strconv.Atoi(m[1])
			f, _ := strconv.Atoi(m[2])
			return p, f, p + f
		}
		break // latest results comment found but unparseable; don't look further back
	}
	return -1, 0, 0
}

// enrichAllPREntry computes the derived status columns for one PR.
func enrichAllPREntry(entry *allPREntry) {
	entry.CommentCount = computeCommentCount(entry)
	entry.UnrespondedCount = computeUnresponded(entry)
	entry.ReviewStatus = computeReviewStatus(entry, entry.UnrespondedCount)
	entry.CIStatus = computeCIStatus(entry.PR.StatusCheckRollup)
	entry.PreviewStatus = computePreviewStatus(entry.PR.StatusCheckRollup)
	entry.E2EPassed, entry.E2EFailed, entry.E2ETotal = computeE2E(entry.PR.Comments)
}
