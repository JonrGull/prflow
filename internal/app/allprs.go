package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/github"
	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The all-open-PRs table: every open PR across every repo, with its review, CI,
// preview and E2E state as derived columns.
//
// The derivation itself lives in prstatus.go, which is where the rules and their
// tests are; this file is the screen around it.

// allPRsState is everything the all-open-PRs table owns.
type allPRsState struct {
	entries     []allPREntry
	index       int
	scroll      int
	loading     bool
	autoRefresh bool // refresh every 60s
	sortAsc     bool // true=ascending PR number, false=descending (the default)
}

// allPREntry holds a single open PR with its repo and derived status columns
type allPREntry struct {
	Repo             models.RepoInfo
	PR               models.GhPr
	InlineComments   []models.InlineComment // individual review comments from REST API
	HasPendingReview bool                   // from REST API (catches bot reviewers GraphQL misses)
	CommentCount     int
	UnrespondedCount int    // non-bot, non-author comments after author's last action
	CIStatus         string // "success", "failure", "pending", "none"
	PreviewStatus    string // "success", "failure", "pending", "none"
	ReviewStatus     string // "pending", "stale", "current", "unresponded", "none"
	E2EPassed        int    // -1 = no data
	E2ETotal         int
	E2EFailed        int
}

type allOpenPRsFetchedResult struct {
	entries []allPREntry
	err     error
}

type allPRsRefreshTickMsg struct{}

func allPRsRefreshTickCmd() tea.Cmd {
	return tea.Tick(60*time.Second, func(_ time.Time) tea.Msg {
		return allPRsRefreshTickMsg{}
	})
}

// sortAllPREntries sorts entries by repo name, then PR number (asc or desc)
func sortAllPREntries(entries []allPREntry, ascending bool) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Repo.DisplayName != entries[j].Repo.DisplayName {
			return entries[i].Repo.DisplayName < entries[j].Repo.DisplayName
		}
		if ascending {
			return entries[i].PR.Number < entries[j].PR.Number
		}
		return entries[i].PR.Number > entries[j].PR.Number
	})
}

func fetchAllOpenPRsCmd(cfg *config.Config, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		if dryRun {
			return dryRunAllOpenPRs()
		}

		repos, err := discoverRepos(cfg)
		if err != nil {
			return allOpenPRsFetchedResult{err: err}
		}

		// Build NWO → repo mapping (git remote parsing, no API calls)
		nwoToRepo := make(map[string]models.RepoInfo)
		var nwos []string
		for _, r := range repos {
			if nwo, err := github.GetRepoNWO(r.Path); err == nil {
				nwoToRepo[nwo] = r
				nwos = append(nwos, nwo)
			}
		}

		// Single GraphQL query for ALL open PRs across all repos
		prsByRepo, err := github.SearchAllOpenPRs(nwos)
		if err != nil {
			return allOpenPRsFetchedResult{err: err}
		}

		// Fetch inline review comments and check bot reviewers in parallel
		type supplementResult struct {
			nwo          string
			comments     map[uint64][]models.InlineComment
			hasReviewers map[uint64]bool
		}
		// Only repos that actually have open PRs need supplementing.
		prNwos := make([]string, 0, len(prsByRepo))
		for nwo := range prsByRepo {
			prNwos = append(prNwos, nwo)
		}

		supplements := parallelMap(prNwos, func(nwo string) supplementResult {
			repo := nwoToRepo[nwo]
			comments, _ := github.ListPRReviewComments(repo.Path, nwo)
			hasReviewers := make(map[uint64]bool)
			for _, pr := range prsByRepo[nwo] {
				if len(pr.ReviewRequests) == 0 {
					hasReviewers[pr.Number] = github.HasRequestedReviewers(repo.Path, nwo, pr.Number)
				}
			}
			return supplementResult{nwo: nwo, comments: comments, hasReviewers: hasReviewers}
		})

		inlineByRepo := make(map[string]map[uint64][]models.InlineComment)
		reviewersByRepo := make(map[string]map[uint64]bool)
		for _, res := range supplements {
			if res.comments != nil {
				inlineByRepo[res.nwo] = res.comments
			}
			reviewersByRepo[res.nwo] = res.hasReviewers
		}

		// Build entries
		var entries []allPREntry
		for nwo, prs := range prsByRepo {
			repo, ok := nwoToRepo[nwo]
			if !ok {
				continue
			}
			for _, pr := range prs {
				entry := allPREntry{Repo: repo, PR: pr}
				if comments, ok := inlineByRepo[nwo]; ok {
					entry.InlineComments = comments[pr.Number]
				}
				if reviewers, ok := reviewersByRepo[nwo]; ok && reviewers[pr.Number] {
					entry.HasPendingReview = true
				}
				entries = append(entries, entry)
			}
		}

		// Enrich all entries with derived status fields
		for i := range entries {
			enrichAllPREntry(&entries[i])
		}

		// Sort by repo name, then PR number (sort direction applied in handler)
		sortAllPREntries(entries, true) // default ascending; handler re-sorts per user pref

		return allOpenPRsFetchedResult{entries: entries}
	}
}

func (m Model) handleAllOpenPRsFetched(msg allOpenPRsFetchedResult) (tea.Model, tea.Cmd) {
	wasLoading := m.allPRs.loading
	m.allPRs.loading = false

	if msg.err != nil {
		if wasLoading && m.screen == ScreenViewAllPrs {
			// Refresh failed but we already have data — just clear loading indicator
			return m, nil
		}
		m.errorMessage = msg.err.Error()
		m.screen = ScreenError
		return m, nil
	}

	// Apply user's sort preference
	sortAllPREntries(msg.entries, m.allPRs.sortAsc) // default is descending (newest first)

	var cmd tea.Cmd
	if m.allPRs.autoRefresh {
		cmd = allPRsRefreshTickCmd()
	}

	// If already viewing, swap data in place preserving cursor position
	if m.screen == ScreenViewAllPrs {
		m.allPRs.entries = msg.entries
		if m.allPRs.index >= len(m.allPRs.entries) {
			m.allPRs.index = len(m.allPRs.entries) - 1
			if m.allPRs.index < 0 {
				m.allPRs.index = 0
			}
		}
		return m, cmd
	}

	m.allPRs.entries = msg.entries
	m.allPRs.index = 0
	m.allPRs.scroll = 0
	m.screen = ScreenViewAllPrs
	return m, cmd
}

func (m Model) handleViewAllPrsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.shouldQuit = true
		return m, tea.Quit
	case "up", "k":
		if m.allPRs.index > 0 {
			m.allPRs.index--
		}
	case "down", "j":
		if m.allPRs.index < len(m.allPRs.entries)-1 {
			m.allPRs.index++
		}
	case "o":
		if m.allPRs.index < len(m.allPRs.entries) {
			entry := m.allPRs.entries[m.allPRs.index]
			if entry.PR.URL != "" {
				_ = openURL(entry.PR.URL)
			}
		}
	case "r":
		// An explicit refresh should also pick up repos added on disk.
		invalidateRepoCache()
		m.allPRs.loading = true
		return m, fetchAllOpenPRsCmd(m.config, m.dryRun)
	case "a":
		m.allPRs.autoRefresh = !m.allPRs.autoRefresh
		if m.allPRs.autoRefresh {
			return m, allPRsRefreshTickCmd()
		}
	case "s":
		m.allPRs.sortAsc = !m.allPRs.sortAsc
		sortAllPREntries(m.allPRs.entries, m.allPRs.sortAsc)
	case "esc":
		m.allPRs.entries = nil
		m.allPRs.index = 0
		m.allPRs.scroll = 0
		m.allPRs.loading = false
		m.allPRs.autoRefresh = false
		m.screen = ScreenMainMenu
		m.menuIndex = 0
		return m, nil
	}
	// Adjust scroll to keep highlighted item visible
	visibleHeight := m.height - 15 // approximate visible rows in box
	if visibleHeight < 5 {
		visibleHeight = 5
	}
	if m.allPRs.index < m.allPRs.scroll {
		m.allPRs.scroll = m.allPRs.index
	} else if m.allPRs.index >= m.allPRs.scroll+visibleHeight {
		m.allPRs.scroll = m.allPRs.index - visibleHeight + 1
	}
	return m, nil
}

func (m Model) reviewStatusIcon(status string) string {
	switch status {
	case "current":
		return ui.Green.Render("✓")
	case "stale":
		return ui.Yellow.Render("⚠")
	case "unresponded":
		return ui.Red.Render("✗")
	case "pending":
		return ui.Yellow.Render("⏳")
	default:
		return ui.Dim.Render("—")
	}
}

func (m Model) prStatusIcon(status string) string {
	switch status {
	case "success":
		return ui.Green.Render("✓")
	case "failure":
		return ui.Red.Render("✗")
	case "pending":
		return ui.Yellow.Render(ui.Spinner(m.spinnerFrame))
	default:
		return ui.Dim.Render("—")
	}
}

func (m Model) renderViewAllPrsWithHeight(availableHeight int) string {
	availableHeight -= 2 // account for ColumnBox border lines
	contentWidth := m.contentWidth()
	boxWidth := contentWidth - 10

	if len(m.allPRs.entries) == 0 {
		emptyStyle := ui.Dim
		content := "\n" + emptyStyle.Render("  No open PRs found across configured repos.") + "\n"
		return ui.ColumnBox(content, " All Open PRs ", ui.ColorBlue, true, boxWidth, availableHeight)
	}

	// Fixed-width status columns
	const (
		indent   = 4
		prNumW   = 7 // "#1234  "
		commentW = 8 // "💬15(3) "
		revW     = 4 // " ⚠  "
		ciW      = 4 // " ✓  "
		prevW    = 5 // " ✓   "
		e2eW     = 6 // " 16/16"
	)
	fixedW := indent + 2 + prNumW + commentW + revW + ciW + prevW + e2eW + 4 // +2 for draft icon, +4 for box border padding

	// Dynamic title/branch: give remaining space 60/40 split
	flexW := boxWidth - fixedW
	if flexW < 30 {
		flexW = 30
	}
	titleW := flexW * 6 / 10
	branchW := flexW - titleW
	if titleW < 15 {
		titleW = 15
	}
	if branchW < 12 {
		branchW = 12
	}

	// visibleHeight used for scrolling (scroll adjusted in update.go key handler)
	visibleHeight := availableHeight - 4 // title + legend + padding
	if visibleHeight < 5 {
		visibleHeight = 5
	}

	var lines []string

	// Legend header — plain text, pad to match data columns
	dimStyle := ui.Dim
	legendPad := strings.Repeat(" ", 2+prNumW+2+titleW+branchW) // 2=row indent, prNumW+2=draft prefix+prNum
	legend := legendPad + dimStyle.Render(
		visPad("💬", commentW)+
			visPad("Rev", revW)+
			visPad("CI", ciW)+
			visPad("Prev", prevW)+
			visRightAlign("E2E", e2eW),
	)
	lines = append(lines, legend)

	repoStyle := ui.Cyan

	lastRepo := ""
	lineIdx := 0
	for i, entry := range m.allPRs.entries {
		// Group header when repo changes
		if entry.Repo.DisplayName != lastRepo {
			if lastRepo != "" {
				if lineIdx >= m.allPRs.scroll && lineIdx < m.allPRs.scroll+visibleHeight {
					lines = append(lines, "")
				}
				lineIdx++
			}
			if lineIdx >= m.allPRs.scroll && lineIdx < m.allPRs.scroll+visibleHeight {
				lines = append(lines, repoStyle.Render("  "+entry.Repo.DisplayName))
			}
			lineIdx++
			lastRepo = entry.Repo.DisplayName
		}

		if lineIdx >= m.allPRs.scroll && lineIdx < m.allPRs.scroll+visibleHeight {
			highlighted := i == m.allPRs.index
			bg := lipgloss.Color("")
			if highlighted {
				bg = ui.ColorDarkGray
			}

			// Styles that adapt to highlight state
			sPrNum := ui.Blue.Bold(true)
			sTitle := ui.White
			sBranch := ui.Dim
			sDim := dimStyle
			sComment := ui.Yellow
			sGreen := ui.Green
			sRed := ui.Red
			if highlighted {
				sPrNum = ui.CyanBold.Background(bg)
				sTitle = sTitle.Background(bg)
				sBranch = ui.White.Background(bg) // brighten branch on highlight
				sDim = ui.White.Background(bg)
				sComment = sComment.Background(bg)
				sGreen = sGreen.Background(bg)
				sRed = sRed.Background(bg)
			}

			// Draft indicator
			draftPrefix := "  "
			if entry.PR.IsDraft {
				draftStyle := ui.Dim
				if highlighted {
					draftStyle = draftStyle.Background(bg)
				}
				draftPrefix = draftStyle.Render("◇ ")
			}

			// PR number — fixed visual width
			prNum := fmt.Sprintf("#%d", entry.PR.Number)
			col1 := visPad(draftPrefix+sPrNum.Render(prNum), prNumW+2) // +2 for draft prefix

			// Title — truncate then pad; dim for drafts
			titleStyle := sTitle
			if entry.PR.IsDraft {
				titleStyle = ui.Dim
				if highlighted {
					titleStyle = titleStyle.Background(bg)
				}
			}
			title := truncateString(entry.PR.Title, titleW-1)
			col2 := visPad(titleStyle.Render(title), titleW)

			// Branch flow — truncate then pad
			branchFlow := fmt.Sprintf("%s → %s", entry.PR.HeadBranch, entry.PR.BaseBranch)
			maxBranch := branchW - 2 // account for parens
			if len(branchFlow) > maxBranch {
				branchFlow = branchFlow[:maxBranch-3] + "..."
			}
			col3 := visPad(sBranch.Render("("+branchFlow+")"), branchW)

			// Comment count — styled, then visually padded
			var commentRaw string
			if entry.CommentCount == 0 {
				commentRaw = sDim.Render("💬0")
			} else if entry.UnrespondedCount > 0 {
				commentRaw = sComment.Render(fmt.Sprintf("💬%d", entry.CommentCount)) +
					sRed.Render(fmt.Sprintf("(%d)", entry.UnrespondedCount))
			} else {
				commentRaw = sGreen.Render(fmt.Sprintf("💬%d", entry.CommentCount))
			}
			col4 := visPad(commentRaw, commentW)

			// Review status icon
			col5 := visPad(m.reviewStatusIcon(entry.ReviewStatus), revW)

			// CI icon — styled, then visually padded
			col6 := visPad(m.prStatusIcon(entry.CIStatus), ciW)

			// Preview icon — styled, then visually padded
			col7 := visPad(m.prStatusIcon(entry.PreviewStatus), prevW)

			// E2E — styled, then right-aligned
			var e2eRaw string
			if entry.E2EPassed < 0 {
				e2eRaw = sDim.Render("—")
			} else if entry.E2EFailed > 0 {
				e2eRaw = sRed.Render(fmt.Sprintf("%d/%d", entry.E2EPassed, entry.E2ETotal))
			} else {
				e2eRaw = sGreen.Render(fmt.Sprintf("%d/%d", entry.E2EPassed, entry.E2ETotal))
			}
			col8 := visRightAlign(e2eRaw, e2eW)

			line := "  " + col1 + col2 + col3 + col4 + col5 + col6 + col7 + col8

			if highlighted {
				// Fill any remaining gaps with the highlight background
				hlBg := lipgloss.NewStyle().Background(bg)
				line = hlBg.Render(line)
			}
			lines = append(lines, line)
		}
		lineIdx++
	}

	// Legend anchored to bottom of table
	lGreen := ui.Green
	lYellow := ui.Yellow
	lRed := ui.Red
	sep := dimStyle.Render("  │  ")
	legendLine := dimStyle.Render("💬 N") +
		dimStyle.Render(" total  ") +
		lRed.Render("(N)") + dimStyle.Render(" unresponded") +
		sep +
		dimStyle.Render("Review: ") +
		lGreen.Render("✓") + dimStyle.Render(" reviewed  ") +
		lYellow.Render("⚠") + dimStyle.Render(" stale  ") +
		lRed.Render("✗") + dimStyle.Render(" unresponded  ") +
		lYellow.Render("⏳") + dimStyle.Render(" pending") +
		sep +
		dimStyle.Render("CI/Preview Env: ") +
		lGreen.Render("✓") + dimStyle.Render(" pass  ") +
		lRed.Render("✗") + dimStyle.Render(" fail")
	// Pad content so legend sits at the very bottom of the box
	// ColumnBox uses: 1 line for title + height lines for content
	// We need: len(lines) + padding + 1 (blank) + 1 (legend) = availableHeight - 1
	contentCapacity := availableHeight - 1 // subtract title line
	legendLines := 2                       // blank line + legend
	padNeeded := contentCapacity - len(lines) - legendLines
	for i := 0; i < padNeeded; i++ {
		lines = append(lines, "")
	}
	lines = append(lines, "")
	centeredLegend := lipgloss.PlaceHorizontal(boxWidth, lipgloss.Center, legendLine)
	lines = append(lines, centeredLegend)

	content := strings.Join(lines, "\n")
	countStyle := ui.Dim
	loadingIndicator := ""
	if m.allPRs.loading {
		spinStyle := ui.Yellow
		loadingIndicator = " " + spinStyle.Render(ui.Spinner(m.spinnerFrame)+" refreshing")
	}
	boxTitle := fmt.Sprintf(" All Open PRs %s%s", countStyle.Render(fmt.Sprintf("(%d)", len(m.allPRs.entries))), loadingIndicator)

	return ui.ColumnBox(content, boxTitle, ui.ColorBlue, true, boxWidth, availableHeight)
}
