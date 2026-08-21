package app

import (
	"testing"

	"github.com/JonrGull/prflow/internal/models"
)

// The GhPr sub-structs are anonymous, so they cannot be written as composite
// literals. These builders keep the tables below readable.

func mkComment(login, body, ts string) models.PrComment {
	var c models.PrComment
	c.Author.Login = login
	c.Body = body
	c.CreatedAt = ts
	return c
}

func mkReview(login, state, ts string) models.PrReview {
	var r models.PrReview
	r.Author.Login = login
	r.State = state
	r.SubmittedAt = ts
	return r
}

func mkInline(login, body, ts string) models.InlineComment {
	var ic models.InlineComment
	ic.User.Login = login
	ic.Body = body
	ic.CreatedAt = ts
	return ic
}

func entryBy(author string) *allPREntry {
	e := &allPREntry{}
	e.PR.Author.Login = author
	return e
}

// --- unresponded ------------------------------------------------------------
//
// "Unresponded" drives whether a PR is flagged as needing the author's
// attention, so the boundary against the author's last action is the rule that
// most needs pinning.

func TestComputeUnresponded(t *testing.T) {
	t.Run("counts only feedback after the author's last action", func(t *testing.T) {
		e := entryBy("alice")
		e.PR.Comments = []models.PrComment{
			mkComment("bob", "early question", "2026-01-01T10:00:00Z"), // answered
			mkComment("alice", "my reply", "2026-01-01T11:00:00Z"),     // the boundary
			mkComment("bob", "follow-up", "2026-01-01T12:00:00Z"),      // unanswered
			mkComment("carol", "another", "2026-01-01T13:00:00Z"),      // unanswered
		}
		if got := computeUnresponded(e); got != 2 {
			t.Errorf("got %d, want 2", got)
		}
	})

	t.Run("the author's own comments never count", func(t *testing.T) {
		e := entryBy("alice")
		e.PR.Comments = []models.PrComment{
			mkComment("bob", "question", "2026-01-01T10:00:00Z"),
			mkComment("alice", "reply", "2026-01-01T11:00:00Z"),
			mkComment("alice", "one more thought", "2026-01-01T12:00:00Z"),
		}
		if got := computeUnresponded(e); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})

	t.Run("bot noise is ignored", func(t *testing.T) {
		e := entryBy("alice")
		e.PR.Comments = []models.PrComment{
			mkComment("alice", "kickoff", "2026-01-01T09:00:00Z"),
			mkComment("linear", "linkback", "2026-01-01T10:00:00Z"),
			mkComment("github-actions", "deploy done", "2026-01-01T11:00:00Z"),
			mkComment("bob", "<!-- linear-linkback -->", "2026-01-01T12:00:00Z"),
			mkComment("bob", "## Preview Deployment ready", "2026-01-01T13:00:00Z"),
			mkComment("bob", "You have reached your Codex usage limits", "2026-01-01T14:00:00Z"),
		}
		if got := computeUnresponded(e); got != 0 {
			t.Errorf("got %d, want 0 — boilerplate should not look like feedback", got)
		}
	})

	t.Run("a review by the author moves the boundary", func(t *testing.T) {
		e := entryBy("alice")
		e.PR.Comments = []models.PrComment{
			mkComment("bob", "question", "2026-01-01T10:00:00Z"),
		}
		// Alice responded via a review rather than a comment.
		e.PR.Reviews = []models.PrReview{
			mkReview("alice", "COMMENTED", "2026-01-01T11:00:00Z"),
		}
		if got := computeUnresponded(e); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})

	t.Run("inline review comments count too", func(t *testing.T) {
		e := entryBy("alice")
		e.InlineComments = []models.InlineComment{
			mkInline("bob", "nit: rename this", "2026-01-01T12:00:00Z"),
			mkInline("alice", "done", "2026-01-01T13:00:00Z"),
			mkInline("bob", "still not right", "2026-01-01T14:00:00Z"),
		}
		if got := computeUnresponded(e); got != 1 {
			t.Errorf("got %d, want 1", got)
		}
	})

	t.Run("no comments at all", func(t *testing.T) {
		if got := computeUnresponded(entryBy("alice")); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})
}

// --- review status ----------------------------------------------------------

func TestComputeReviewStatus(t *testing.T) {
	tests := []struct {
		name        string
		build       func(*allPREntry)
		unresponded int
		want        string
	}{
		{
			name:  "no reviews at all",
			build: func(e *allPREntry) {},
			want:  "none",
		},
		{
			name: "reviewers requested",
			build: func(e *allPREntry) {
				e.PR.ReviewRequests = []models.ReviewRequest{{Login: "bob", TypeName: "User"}}
			},
			want: "pending",
		},
		{
			name:  "a bot reviewer GraphQL missed",
			build: func(e *allPREntry) { e.HasPendingReview = true },
			want:  "pending",
		},
		{
			name: "unresponded feedback outranks staleness",
			build: func(e *allPREntry) {
				e.PR.LatestReviews = []models.PrReview{mkReview("bob", "COMMENTED", "2026-01-01T10:00:00Z")}
				e.PR.Commits = []models.PrCommit{{AuthoredDate: "2026-01-02T10:00:00Z"}}
			},
			unresponded: 1,
			want:        "unresponded",
		},
		{
			name: "commits after the last review are stale",
			build: func(e *allPREntry) {
				e.PR.LatestReviews = []models.PrReview{mkReview("bob", "APPROVED", "2026-01-01T10:00:00Z")}
				e.PR.Commits = []models.PrCommit{{AuthoredDate: "2026-01-02T10:00:00Z"}}
			},
			want: "stale",
		},
		{
			name: "review after the last commit is current",
			build: func(e *allPREntry) {
				e.PR.LatestReviews = []models.PrReview{mkReview("bob", "APPROVED", "2026-01-03T10:00:00Z")}
				e.PR.Commits = []models.PrCommit{{AuthoredDate: "2026-01-02T10:00:00Z"}}
			},
			want: "current",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := entryBy("alice")
			tt.build(e)
			if got := computeReviewStatus(e, tt.unresponded); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// --- CI and preview ---------------------------------------------------------

func TestComputeCIStatus(t *testing.T) {
	check := func(name, status, conclusion string) models.CheckRun {
		return models.CheckRun{Name: name, Status: status, Conclusion: conclusion}
	}

	tests := []struct {
		name   string
		checks []models.CheckRun
		want   string
	}{
		{"no checks", nil, "none"},
		{
			// deploy-preview and e2e have their own columns and must not sway
			// the overall CI light.
			"only excluded checks",
			[]models.CheckRun{check("deploy-preview", "COMPLETED", "FAILURE"), check("e2e", "COMPLETED", "FAILURE")},
			"none",
		},
		{"all green", []models.CheckRun{check("build", "COMPLETED", "SUCCESS")}, "success"},
		{
			"failure wins over pending",
			[]models.CheckRun{check("build", "COMPLETED", "FAILURE"), check("lint", "IN_PROGRESS", "")},
			"failure",
		},
		{
			"pending wins over success",
			[]models.CheckRun{check("build", "COMPLETED", "SUCCESS"), check("lint", "QUEUED", "")},
			"pending",
		},
		{
			"an excluded failure does not mask a real pass",
			[]models.CheckRun{check("deploy-preview", "COMPLETED", "FAILURE"), check("build", "COMPLETED", "SUCCESS")},
			"success",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := computeCIStatus(tt.checks); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestComputePreviewStatus(t *testing.T) {
	tests := []struct {
		name   string
		checks []models.CheckRun
		want   string
	}{
		{"absent", []models.CheckRun{{Name: "build", Conclusion: "SUCCESS"}}, "none"},
		{"success", []models.CheckRun{{Name: "deploy-preview", Status: "COMPLETED", Conclusion: "SUCCESS"}}, "success"},
		{"failure", []models.CheckRun{{Name: "deploy-preview", Status: "COMPLETED", Conclusion: "FAILURE"}}, "failure"},
		{"running", []models.CheckRun{{Name: "deploy-preview", Status: "IN_PROGRESS"}}, "pending"},
		{"skipped counts as none", []models.CheckRun{{Name: "deploy-preview", Status: "COMPLETED", Conclusion: "SKIPPED"}}, "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := computePreviewStatus(tt.checks); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// --- E2E --------------------------------------------------------------------

func TestComputeE2E(t *testing.T) {
	results := func(passed, failed string) string {
		return "<!-- e2e-results -->\nRun complete: **" + passed + "** passed, **" + failed + "** failed"
	}

	t.Run("no results comment yields the no-data sentinel", func(t *testing.T) {
		passed, failed, total := computeE2E([]models.PrComment{mkComment("bob", "hello", "t1")})
		// -1 rather than 0: the renderer must distinguish "not run" from
		// "zero tests passed".
		if passed != -1 || failed != 0 || total != 0 {
			t.Errorf("got (%d, %d, %d), want (-1, 0, 0)", passed, failed, total)
		}
	})

	t.Run("parses counts", func(t *testing.T) {
		passed, failed, total := computeE2E([]models.PrComment{
			mkComment("github-actions", results("12", "3"), "t1"),
		})
		if passed != 12 || failed != 3 || total != 15 {
			t.Errorf("got (%d, %d, %d), want (12, 3, 15)", passed, failed, total)
		}
	})

	t.Run("the latest results comment wins", func(t *testing.T) {
		passed, _, _ := computeE2E([]models.PrComment{
			mkComment("github-actions", results("1", "9"), "t1"),
			mkComment("bob", "unrelated", "t2"),
			mkComment("github-actions", results("10", "0"), "t3"),
		})
		if passed != 10 {
			t.Errorf("passed = %d, want 10 (latest comment)", passed)
		}
	})

	t.Run("the marker alone is not enough", func(t *testing.T) {
		// Same text from a human must not be read as results.
		passed, _, _ := computeE2E([]models.PrComment{
			mkComment("bob", results("5", "5"), "t1"),
		})
		if passed != -1 {
			t.Errorf("passed = %d, want -1 for a non-bot author", passed)
		}
	})

	t.Run("unparseable results are not silently zero", func(t *testing.T) {
		passed, _, _ := computeE2E([]models.PrComment{
			mkComment("github-actions", "<!-- e2e-results -->\nrun cancelled", "t1"),
		})
		if passed != -1 {
			t.Errorf("passed = %d, want -1", passed)
		}
	})
}

// --- orchestration ----------------------------------------------------------

func TestEnrichAllPREntry(t *testing.T) {
	e := entryBy("alice")
	e.PR.Comments = []models.PrComment{
		mkComment("alice", "opening", "2026-01-01T09:00:00Z"),
		mkComment("bob", "please fix", "2026-01-01T10:00:00Z"),
		mkComment("github-actions", "<!-- e2e-results -->\n**8** passed, **2** failed", "2026-01-01T11:00:00Z"),
	}
	// gh returns both: Reviews is every review event, LatestReviews the most
	// recent per reviewer. A fixture with only one of them is not a real PR.
	review := mkReview("bob", "CHANGES_REQUESTED", "2026-01-01T10:00:00Z")
	e.PR.Reviews = []models.PrReview{review}
	e.PR.LatestReviews = []models.PrReview{review}
	e.PR.StatusCheckRollup = []models.CheckRun{
		{Name: "build", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Name: "deploy-preview", Status: "COMPLETED", Conclusion: "FAILURE"},
	}

	enrichAllPREntry(e)

	if e.UnrespondedCount != 1 {
		t.Errorf("UnrespondedCount = %d, want 1", e.UnrespondedCount)
	}
	if e.ReviewStatus != "unresponded" {
		t.Errorf("ReviewStatus = %q, want unresponded", e.ReviewStatus)
	}
	if e.CIStatus != "success" {
		t.Errorf("CIStatus = %q, want success", e.CIStatus)
	}
	if e.PreviewStatus != "failure" {
		t.Errorf("PreviewStatus = %q, want failure", e.PreviewStatus)
	}
	if e.E2EPassed != 8 || e.E2EFailed != 2 || e.E2ETotal != 10 {
		t.Errorf("E2E = (%d, %d, %d), want (8, 2, 10)", e.E2EPassed, e.E2EFailed, e.E2ETotal)
	}
	if e.CommentCount != 4 { // 3 issue comments + 1 review event
		t.Errorf("CommentCount = %d, want 4", e.CommentCount)
	}
}

// An entry with nothing in it must not panic: this runs over every PR on every
// refresh, including ones the API returned only partially.
func TestEnrichAllPREntryEmpty(t *testing.T) {
	e := &allPREntry{}
	enrichAllPREntry(e)
	if e.ReviewStatus != "none" || e.CIStatus != "none" || e.PreviewStatus != "none" {
		t.Errorf("empty entry = %+v", e)
	}
	if e.E2EPassed != -1 {
		t.Errorf("E2EPassed = %d, want -1", e.E2EPassed)
	}
}
