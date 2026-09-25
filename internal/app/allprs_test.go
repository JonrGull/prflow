package app

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/JonrGull/prflow/internal/models"
)

func minePR(num uint64, author string, build func(*models.GhPr)) allPREntry {
	pr := models.GhPr{Number: num, URL: fmt.Sprintf("https://github.com/o/r/pull/%d", num)}
	pr.Author.Login = author
	if build != nil {
		build(&pr)
	}
	return allPREntry{Repo: models.NewRepoInfo("/r", "Org/r", "main", "Backend"), PR: pr}
}

func mineFixture() allOpenPRsFetchedResult {
	reviewedByMe := models.PrReview{State: "APPROVED"}
	reviewedByMe.Author.Login = "me"
	return allOpenPRsFetchedResult{viewer: "me", teams: []string{"Org/Web"}, entries: []allPREntry{
		minePR(1, "me", nil),
		minePR(2, "maria", func(pr *models.GhPr) { pr.ReviewRequests = []models.ReviewRequest{{Login: "ME"}} }),
		minePR(3, "maria", func(pr *models.GhPr) { pr.ReviewRequests = []models.ReviewRequest{{Name: "Web", Slug: "Org/web"}} }),
		minePR(4, "maria", func(pr *models.GhPr) { pr.LatestReviews = []models.PrReview{reviewedByMe} }),
		minePR(5, "maria", func(pr *models.GhPr) {
			pr.ReviewRequests = []models.ReviewRequest{{Login: "someone"}, {Name: "API", Slug: "org/api"}}
		}),
	}}
}

func shownNumbers(m Model) []uint64 {
	var nums []uint64
	for _, e := range m.shownPRs() {
		nums = append(nums, e.PR.Number)
	}
	return nums
}

// Written, reviewed, or asked for directly or through a team; logins and team
// slugs compared the way GitHub does, without case.
func TestMineIsWhatInvolvesTheViewer(t *testing.T) {
	m := send(t, sized(staleModel(ScreenViewAllPrs)), mineFixture())
	m.allPRs.sortAsc = true
	sortAllPREntries(m.allPRs.entries, true)
	m = send(t, m, key("@"))
	if got, want := shownNumbers(m), []uint64{1, 2, 3, 4}; !reflect.DeepEqual(got, want) {
		t.Errorf("mine = %v, want %v", got, want)
	}
	if m = send(t, m, key("@")); len(m.shownPRs()) != 5 {
		t.Errorf("@ again shows %d PRs, want all 5", len(m.shownPRs()))
	}
}

// A refresh used to keep the cursor's row, so a PR opened or merged above it
// moved the highlight onto another PR, where o and the merge keys then act.
func TestAllPRsCursorFollowsItsPR(t *testing.T) {
	m := send(t, sized(staleModel(ScreenViewAllPrs)), mineFixture())
	m.selectPR(minePR(3, "", nil).PR.URL)
	m = send(t, m, key("@"))
	if e, _ := m.highlightedPR(); e.PR.Number != 3 {
		t.Fatalf("after @ the cursor is on #%d, want #3", e.PR.Number)
	}
	refreshed := mineFixture()
	refreshed.entries = append(refreshed.entries, minePR(9, "me", nil))
	m = send(t, m, refreshed)
	if e, _ := m.highlightedPR(); e.PR.Number != 3 {
		t.Errorf("after a refresh the cursor is on #%d, want #3", e.PR.Number)
	}
}
