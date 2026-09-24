package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JonrGull/prflow/internal/models"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

// Session history drew every entry, so after a big batch it ran off the
// screen and the cursor went with it.
func TestSessionHistoryCursorStaysOnScreen(t *testing.T) {
	m := sized(staleModel(ScreenSessionHistory))
	for i := 0; i < 30; i++ {
		m.sessionPRs = append(m.sessionPRs, sessionPR{repoName: fmt.Sprintf("G/repo-%02d", i),
			url: fmt.Sprintf("https://example.test/pull/%d", 100+i), prType: "dev → staging", createdAt: time.Now()})
	}
	for i := range m.sessionPRs {
		v := m.View()
		if want := fmt.Sprintf("/pull/%d", 100+i); !strings.Contains(v, want) {
			t.Fatalf("cursor on %d: %s is not on screen", i, want)
		}
		if h := strings.Count(v, "\n") + 1; h > m.height {
			t.Fatalf("view is %d rows on a %d-row terminal", h, m.height)
		}
		m = send(t, m, keyDown)
	}
}

// A value longer than its field ran past the panel edge while being edited.
func TestLongEditValuesStayInside(t *testing.T) {
	long := strings.Repeat("abcdefghij", 30)
	widest := func(v string) int {
		w := 0
		for _, l := range strings.Split(v, "\n") {
			w = max(w, lipgloss.Width(l))
		}
		return w
	}

	s := sized(settingsModel(t))
	s.menuIndex = fieldIndex(t, "Repo directory")
	s.settings.editing = true
	s.settings.editValue = long
	if w := widest(s.View()); w > s.width {
		t.Errorf("settings: a line is %d wide on a %d-wide terminal", w, s.width)
	}
	// The value wrapped instead, which broke the height budget.
	if h := strings.Count(s.View(), "\n") + 1; h > s.height {
		t.Errorf("settings: view is %d rows on a %d-row terminal", h, s.height)
	}

	l := sized(listModel(t, listGlobs))
	l.screen = ScreenListEdit
	l.list.editing = true
	l.list.editValue = long
	if w := widest(l.View()); w > l.width {
		t.Errorf("list editor: a line is %d wide on a %d-wide terminal", w, l.width)
	}
	if h := strings.Count(l.View(), "\n") + 1; h > l.height {
		t.Errorf("list editor: view is %d rows on a %d-row terminal", h, l.height)
	}
	if !strings.Contains(l.View(), "…") || !strings.Contains(s.View(), "…") {
		t.Error("a cut value should be marked with …")
	}
}

// First run kept showing the last scan after the path was edited, so the
// result on screen described a different directory.
func TestFirstRunDropsAStaleScan(t *testing.T) {
	m := sized(staleModel(ScreenFirstRun))
	m.firstRun.value = "/elsewhere"
	m.firstRun.preview = firstRunPreview{Ran: true, Path: "/old/path"}
	if v := m.View(); !strings.Contains(v, "Press Enter to see what it finds") {
		t.Error("a scan of /old/path was shown under /elsewhere")
	}
}
