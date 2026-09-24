package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JonrGull/prflow/internal/models"

	tea "github.com/charmbracelet/bubbletea"
)

// The key handlers and the renderers worked out the visible rows separately,
// and differently, so moving down a long list walked the cursor off screen,
// and the batch confirmation could not scroll to its last lines. These step
// through every item and check it is actually drawn.

var keyDown = tea.KeyMsg{Type: tea.KeyDown}

func sized(m Model) Model {
	m.width, m.height = 120, 40
	return m
}

// Two PRs per repo, so repo headers and blank lines interleave with the rows.
func TestAllPRsCursorStaysOnScreen(t *testing.T) {
	m := sized(staleModel(ScreenViewAllPrs))
	for i := 0; i < 60; i++ {
		repo := models.NewRepoInfo("/r", fmt.Sprintf("Org/repo-%02d", i/2), "main", "Backend")
		m.allPRs.entries = append(m.allPRs.entries, allPREntry{Repo: repo,
			PR: models.GhPr{Number: uint64(1000 + i), Title: fmt.Sprintf("title-%02d", i)}})
	}
	for i := range m.allPRs.entries {
		if want := fmt.Sprintf("title-%02d", i); !strings.Contains(m.View(), want) {
			t.Fatalf("cursor on %d: %s is not on screen", i, want)
		}
		m = send(t, m, keyDown)
	}
}

func TestActionsCursorStaysOnScreen(t *testing.T) {
	m := sized(staleModel(ScreenActionsOverview))
	for i := 0; i < 60; i++ {
		repo := models.NewRepoInfo("/r", fmt.Sprintf("Org/repo-%02d", i/3), "main", "Backend")
		m.actions.entries = append(m.actions.entries, actionsEntry{Repo: repo, Run: models.WorkflowRun{
			DatabaseID: uint64(i + 1), WorkflowName: fmt.Sprintf("wf-%02d", i),
			Status: "completed", Conclusion: "success", HeadBranch: "main", UpdatedAt: time.Now()}})
	}
	for i := range m.actions.entries {
		if want := fmt.Sprintf("wf-%02d", i); !strings.Contains(m.View(), want) {
			t.Fatalf("cursor on %d: %s is not on screen", i, want)
		}
		m = send(t, m, keyDown)
	}
}

// Every line of the confirmation's Changes column has to be reachable.
func TestBatchConfirmationScrollsToTheEnd(t *testing.T) {
	m := sized(staleModel(ScreenBatchConfirmation))
	for i := 0; i < 12; i++ {
		m.batch.repos = append(m.batch.repos, models.NewRepoInfo("/r", fmt.Sprintf("Org/repo-%02d", i), "main", "Backend"))
		m.batch.selected = append(m.batch.selected, true)
		c := []models.CommitInfo{{Hash: "abc1234", Message: fmt.Sprintf("change %d", i)}}
		m.batch.repoCommits = append(m.batch.repoCommits, &c)
		m.tickets = append(m.tickets, fmt.Sprintf("PROJ-%d", 100+i))
	}
	m.batch.reposWithCommits = 12
	m.dryRun = false

	for i := 0; i < 200 && !strings.Contains(m.View(), "PROJ-111"); i++ {
		m = send(t, m, keyDown)
	}
	if !strings.Contains(m.View(), "PROJ-111") {
		t.Error("the last ticket can never be scrolled into view")
	}
}

// The pull summary box cut what did not fit, and Failed was the last section,
// so failures were what disappeared. Failures now come first, and a cut says so.
func TestPullSummaryShowsFailuresAndTheCut(t *testing.T) {
	m := sized(staleModel(ScreenPullSummary))
	m.height = 24
	m.pull.branch = "dev"
	for i := 0; i < 40; i++ {
		m.pull.results = append(m.pull.results, models.PullResult{
			Repo: models.NewRepoInfo("/r", fmt.Sprintf("G/repo-%02d", i), "main", "G"), Status: models.PullUpToDate})
	}
	m.pull.results = append(m.pull.results, models.PullResult{
		Repo: models.NewRepoInfo("/r", "G/broken", "main", "G"), Status: models.PullFailed, Error: "network unreachable"})

	v := m.View()
	if !strings.Contains(v, "G/broken") {
		t.Error("the failure was cut off")
	}
	if !strings.Contains(v, "more lines not shown") {
		t.Error("the cut was silent")
	}
}
