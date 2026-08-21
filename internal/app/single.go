package app

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/JonrGull/prflow/internal/git"
	"github.com/JonrGull/prflow/internal/github"
	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The single-repo flow: pick a PR type, review the commits, edit the title,
// confirm, create.

type fetchCommitsResult struct {
	commits    []models.CommitInfo
	tickets    []string
	existingPR *models.GhPr
	err        error
}

type prCreatedResult struct {
	url      string
	prNumber uint64
	updated  bool
	err      error
}

func fetchCommitsCmd(repo *models.RepoInfo, flow *models.Flow, ticketRegex *regexp.Regexp, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		if dryRun {
			return dryRunCommits()
		}

		if repo == nil || flow == nil {
			return fetchCommitsResult{err: nil}
		}

		headBranch := flow.HeadBranch()
		baseBranch := flow.BaseBranch(repo.MainBranch)

		// Fetch branches from remote
		if err := git.FetchBranches(repo.Path, []string{headBranch, baseBranch}); err != nil {
			return fetchCommitsResult{err: err}
		}

		// Get commits between branches
		commits, err := git.GetCommitsBetween(repo.Path, baseBranch, headBranch, ticketRegex)
		if err != nil {
			return fetchCommitsResult{err: err}
		}

		// Extract all unique tickets
		tickets := git.GetAllTickets(commits)

		// Check for existing PR
		existingPR, _ := github.GetExistingPR(repo.Path, headBranch, baseBranch)

		return fetchCommitsResult{commits: commits, tickets: tickets, existingPR: existingPR}
	}
}

func createPRCmd(repo *models.RepoInfo, flow *models.Flow, title string, tickets []string, linearOrg string, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		if dryRun {
			return dryRunPRCreated(repo)
		}

		if repo == nil || flow == nil {
			return prCreatedResult{err: nil}
		}

		headBranch := flow.HeadBranch()
		baseBranch := flow.BaseBranch(repo.MainBranch)

		// Create or update PR
		pr, updated, err := github.CreateOrUpdatePR(repo.Path, headBranch, baseBranch, title, tickets, linearOrg)
		if err != nil {
			return prCreatedResult{err: err}
		}

		return prCreatedResult{url: pr.URL, prNumber: pr.Number, updated: updated}
	}
}

type currentRepoLoadedResult struct {
	repo *models.RepoInfo
	err  error
}

// loadCurrentRepoCmd loads info for the current repository
func loadCurrentRepoCmd() tea.Cmd {
	return func() tea.Msg {
		repo, err := git.GetCurrentRepoInfo()
		if err != nil {
			return currentRepoLoadedResult{err: err}
		}
		return currentRepoLoadedResult{repo: repo}
	}
}

func (m Model) handleCurrentRepoLoaded(msg currentRepoLoadedResult) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.errorMessage = "Not in a git repository: " + msg.err.Error()
		m.screen = ScreenError
		return m, nil
	}

	m.repoInfo = msg.repo
	m.screen = ScreenPrTypeSelect
	m.menuIndex = 0
	return m, nil
}

func (m Model) handleFetchCommitsResult(msg fetchCommitsResult) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.errorMessage = msg.err.Error()
		m.screen = ScreenError
		return m, nil
	}

	m.commits = msg.commits
	m.tickets = msg.tickets
	m.existingPR = msg.existingPR
	m.screen = ScreenCommitReview
	m.menuIndex = 0
	return m, nil
}

func (m Model) handlePrCreatedResult(msg prCreatedResult) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.errorMessage = msg.err.Error()
		m.screen = ScreenError
		return m, nil
	}

	m.prURL = msg.url
	m.screen = ScreenComplete
	m.spawnConfetti()

	if m.repoInfo != nil && m.flow != nil {
		action := "created"
		if msg.updated {
			action = "updated"
		}
		m.recordSessionPR(m.repoInfo.DisplayName, msg.url, m.flow.Display(m.mainBranch()), action, msg.prNumber)
	}

	return m, nil
}

func (m Model) handlePrTypeSelectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.shouldQuit = true
		return m, tea.Quit
	case "up", "k":
		navigateColumnIndex(&m.menuIndex, len(m.flows()), true)
	case "down", "j":
		navigateColumnIndex(&m.menuIndex, len(m.flows()), false)
	case "enter", "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if idx, ok := numKeyIndex(msg.String(), len(m.flows())); ok {
			m.menuIndex = idx
		}
		return m.selectPrType()
	case "esc":
		m.screen = ScreenMainMenu
		m.mode = nil
		m.menuIndex = 0
	}
	return m, nil
}

func (m Model) selectPrType() (tea.Model, tea.Cmd) {
	flows := m.flows()
	if m.menuIndex >= len(flows) {
		// No steps configured, or the config changed under us. Validate()
		// already flags an empty [[flows]], so say nothing extra here.
		return m, nil
	}
	flow := flows[m.menuIndex]
	m.flow = &flow

	if m.mode != nil && *m.mode == ModeBatch {
		// Batch mode - load repos, then fetch commits in background
		m.screen = ScreenLoading
		m.loadingMessage = "Scanning repositories..."
		// Create channel for background fetch results
		m.batch.resultsChan = make(chan batchRepoCommitResult, 50)
		return m, loadBatchReposCmd(m.config, m.flow, m.dryRun, m.batch.resultsChan)
	}

	// Single mode - start fetching commits
	m.screen = ScreenLoading
	m.loadingMessage = "Fetching branches and commits..."
	return m, fetchCommitsCmd(m.repoInfo, m.flow, m.config.TicketRegex(), m.dryRun)
}

func (m Model) handleCommitReviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		// Don't allow continuing if there are no commits
		if len(m.commits) == 0 {
			return m, nil
		}
		// Use default title if none entered
		if m.prTitle == "" && m.flow != nil {
			m.prTitle = m.flow.DefaultTitle(m.mainBranch())
		}
		// Go directly to confirmation (skip title input screen)
		if m.mode != nil && *m.mode == ModeBatch {
			m.screen = ScreenBatchConfirmation
			m.batch.confirmScroll = 0
		} else {
			m.screen = ScreenConfirmation
		}
		m.confirmSelection = 0
	case tea.KeyEsc:
		m.screen = ScreenPrTypeSelect
		m.flow = nil
		m.prTitle = ""
		m.commits = nil
		m.tickets = nil
		m.menuIndex = 0
	case tea.KeyBackspace:
		if len(m.prTitle) > 0 {
			m.prTitle = trimLastRune(m.prTitle)
		}
	case tea.KeySpace:
		m.prTitle += " "
	case tea.KeyRunes:
		key := string(msg.Runes)
		if key == "q" && m.prTitle == "" {
			// Only quit if no title entered (so 'q' can be typed in title)
			m.shouldQuit = true
			return m, tea.Quit
		}
		m.prTitle += key
	}
	return m, nil
}

func (m Model) handleTitleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		if m.prTitle == "" && m.flow != nil {
			m.prTitle = m.flow.DefaultTitle(m.mainBranch())
		}
		if m.mode != nil && *m.mode == ModeBatch {
			m.screen = ScreenBatchConfirmation
			m.batch.confirmScroll = 0
		} else {
			m.screen = ScreenConfirmation
		}
		m.confirmSelection = 0
	case tea.KeyEsc:
		if m.mode != nil && *m.mode == ModeBatch {
			m.screen = ScreenBatchRepoSelect
		} else {
			m.screen = ScreenCommitReview
		}
		m.menuIndex = 0
	case tea.KeyBackspace:
		if len(m.prTitle) > 0 {
			m.prTitle = trimLastRune(m.prTitle)
		}
	case tea.KeySpace:
		m.prTitle += " "
	case tea.KeyRunes:
		m.prTitle += string(msg.Runes)
	}
	return m, nil
}

func (m Model) handleConfirmationKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.shouldQuit = true
		return m, tea.Quit
	case "left", "right", "tab":
		m.confirmSelection = 1 - m.confirmSelection
	case "up":
		if m.screen == ScreenBatchConfirmation {
			m.scrollBatchConfirm(-1)
		}
	case "down":
		if m.screen == ScreenBatchConfirmation {
			m.scrollBatchConfirm(1)
		}
	case "y":
		m.confirmSelection = 0
		return m.confirmAction()
	case "n":
		return m.goBack()
	case "enter":
		if m.confirmSelection == 0 {
			return m.confirmAction()
		}
		return m.goBack()
	case "esc":
		return m.goBack()
	}
	return m, nil
}

func (m Model) confirmAction() (tea.Model, tea.Cmd) {
	switch m.screen {
	case ScreenConfirmation:
		m.screen = ScreenCreating
		return m, createPRCmd(m.repoInfo, m.flow, m.prTitle, m.tickets, m.config.Tickets.LinearOrg, m.dryRun)
	case ScreenBatchConfirmation:
		// Block if no repos have commits
		if m.batch.reposWithCommits == 0 {
			return m, nil
		}
		return m.startBatchProcessing()
	case ScreenMergeConfirmation:
		// Count selected PRs
		m.merge.total = 0
		for _, selected := range m.merge.selected {
			if selected {
				m.merge.total++
			}
		}
		m.merge.current = 0
		m.merge.results = nil
		m.screen = ScreenMerging
		// Find first selected PR
		for i, selected := range m.merge.selected {
			if selected {
				return m, startMergingCmd(&m, i)
			}
		}
	}
	return m, nil
}

func (m Model) handleCompleteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.shouldQuit = true
		return m, tea.Quit
	case "m":
		return m.navigateToMergePRs()
	case "o":
		if m.prURL != "" {
			_ = openURL(m.prURL)
		}
	case "c":
		if m.prURL != "" {
			// Format as markdown list item
			repoName := "PR"
			if m.repoInfo != nil {
				repoName = m.repoInfo.DisplayName
			}
			formatted := fmt.Sprintf("- %s: %s", repoName, m.prURL)
			m.copyWithFeedback(formatted, "Copied URL!")
		}
		return m, nil
	case "enter", "esc":
		return m.reset()
	}
	return m, nil
}

func (m Model) renderPrTypeSelect() string {
	mainBranch := m.mainBranch()
	flows := m.flows()

	// Build left column (menu) content
	var menuLines []string
	menuLines = append(menuLines, "")

	for i, f := range flows {
		isSelected := i == m.menuIndex
		head, base := f.HeadBranch(), f.BaseBranch(mainBranch)

		// The title colours head and base branches differently, so it is
		// composed here rather than passed as a single colour.
		headStyle := lipgloss.NewStyle().Foreground(flowChainColor(i)).Bold(true)
		baseStyle := lipgloss.NewStyle().Foreground(flowChainColor(i + 1)).Bold(true)
		sep := " → "
		gap := " "
		if isSelected {
			headStyle = headStyle.Background(ui.ColorDarkGray)
			baseStyle = baseStyle.Background(ui.ColorDarkGray)
			bgStyle := lipgloss.NewStyle().Background(ui.ColorDarkGray)
			sep = bgStyle.Render(" → ")
			gap = bgStyle.Render(" ")
		}
		title := gap + headStyle.Render(head) + sep + baseStyle.Render(base)

		menuLines = append(menuLines, numberedMenuRow(fmt.Sprintf("%d.", i+1), title, flowSummary(f, mainBranch, i == len(flows)-1), isSelected)...)
		menuLines = append(menuLines, "")
	}

	if len(flows) == 0 {
		menuLines = append(menuLines, ui.Red.Render("  No release steps configured."))
		menuLines = append(menuLines, ui.Dim.Render("  Add [[flows]] entries in settings."))
		menuLines = append(menuLines, "")
	}

	// Get panel title
	panelTitle := " Select PR Type "
	if m.repoInfo != nil {
		panelTitle = fmt.Sprintf(" %s ", m.repoInfo.DisplayName)
	} else if m.mode != nil && *m.mode == ModeBatch {
		panelTitle = " Batch Mode "
	}

	menuTitleStyle := ui.CyanBold
	menuContent := menuTitleStyle.Render(panelTitle) + "\n" + strings.Join(menuLines, "\n")

	// Build right column (info panel)
	var infoLines []string
	infoLines = append(infoLines, "")

	if m.menuIndex < len(flows) {
		f := flows[m.menuIndex]
		head, base := f.HeadBranch(), f.BaseBranch(mainBranch)
		headStyle := lipgloss.NewStyle().Foreground(flowChainColor(m.menuIndex)).Bold(true)
		baseStyle := lipgloss.NewStyle().Foreground(flowChainColor(m.menuIndex + 1)).Bold(true)
		arrowStyle := ui.WhiteBold
		labelStyle := ui.White

		infoLines = append(infoLines, "  "+headStyle.Render(head)+arrowStyle.Render(" → ")+baseStyle.Render(base))
		infoLines = append(infoLines, "")
		infoLines = append(infoLines, wrapToWidth(flowDetail(f, mainBranch, m.menuIndex == len(flows)-1), 28, "  ")...)
		infoLines = append(infoLines, "")
		infoLines = append(infoLines, labelStyle.Render("  Base: ")+baseStyle.Render(base))
		infoLines = append(infoLines, labelStyle.Render("  Head: ")+headStyle.Render(head))
	}

	infoTitleStyle := ui.WhiteBold
	infoContent := panel(infoTitleStyle, "PR Details", infoLines)

	return ui.UnifiedPanel(menuContent, infoContent, 48, 48, ui.ColorCyan)
}

// flowSummary is the one-line description beside a step in the menu. The
// wording used to be hardcoded per step ("Merge to staging for QA", "Release
// to production"); it is derived now because the steps themselves are.
func flowSummary(f models.Flow, mainBranch string, final bool) string {
	if final {
		return "Release to " + f.BaseBranch(mainBranch)
	}
	return "Merge to " + f.BaseBranch(mainBranch)
}

// flowDetail is the same description at full width, for the detail panel.
func flowDetail(f models.Flow, mainBranch string, final bool) string {
	head, base := f.HeadBranch(), f.BaseBranch(mainBranch)
	if final {
		return fmt.Sprintf("Release %s to %s — the last step in the chain.", head, base)
	}
	return fmt.Sprintf("Merge %s into %s for the next stage of review.", head, base)
}

func (m Model) renderLoading() string {
	message := m.loadingMessage
	spinner := ui.Spinner(m.spinnerFrame)
	spinnerStyle := ui.Cyan
	textStyle := ui.Cyan

	loadingText := fmt.Sprintf("%s %s", spinnerStyle.Render(spinner), textStyle.Render(message))

	// Center the text within the box
	innerWidth := m.contentWidth() - 6
	centeredStyle := lipgloss.NewStyle().Width(innerWidth).Align(lipgloss.Center)

	var lines []string
	lines = append(lines, "")
	lines = append(lines, "")
	lines = append(lines, centeredStyle.Render(loadingText))
	lines = append(lines, "")
	lines = append(lines, "")

	content := strings.Join(lines, "\n")

	// Purple border box
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.ColorPurple).
		Width(m.contentWidth()).
		Padding(1, 2)

	return boxStyle.Render(content)
}

func (m Model) renderCommitReviewWithHeight(availableHeight int) string {
	// Fixed column sizing for stable layout
	columnWidth := (m.contentWidth() - 6) / 2
	panelHeight := availableHeight - 2
	if panelHeight < 10 {
		panelHeight = 10
	}

	mainBranch := m.mainBranch()

	// Build LEFT column (PR info + title input + tickets)
	var leftLines []string

	// PR Info section
	if m.repoInfo != nil {
		labelStyle := ui.Dim
		valueStyle := ui.CyanBold
		leftLines = append(leftLines, labelStyle.Render("  Repo: ")+valueStyle.Render(m.repoInfo.DisplayName))
	}

	if m.flow != nil {
		labelStyle := ui.Dim
		arrowStyle := ui.White
		headBranch := m.flow.HeadBranch()
		baseBranch := m.flow.BaseBranch(mainBranch)
		headStyle := lipgloss.NewStyle().Foreground(m.branchColor(headBranch)).Bold(true)
		baseStyle := lipgloss.NewStyle().Foreground(m.branchColor(baseBranch)).Bold(true)
		leftLines = append(leftLines, labelStyle.Render("  Type: ")+headStyle.Render(headBranch)+arrowStyle.Render(" → ")+baseStyle.Render(baseBranch))
	}

	leftLines = append(leftLines, "")

	// Title input section
	if len(m.commits) > 0 {
		titleSectionStyle := ui.YellowBold
		leftLines = append(leftLines, titleSectionStyle.Render(" PR Title "))
		leftLines = append(leftLines, "")

		defaultTitle := ""
		if m.flow != nil {
			defaultTitle = m.flow.DefaultTitle(mainBranch)
		}

		borderStyle := ui.Yellow
		cursorStyle := ui.Yellow

		var displayText string
		var textColor lipgloss.Color
		if m.prTitle == "" {
			displayText = defaultTitle
			textColor = ui.ColorDarkGray
		} else {
			displayText = m.prTitle
			textColor = ui.ColorWhite
		}
		// Truncate display if too long (use rune count for proper Unicode width)
		innerWidth := 40
		maxLen := innerWidth - 1 // leave room for cursor
		displayRunes := utf8.RuneCountInString(displayText)
		if displayRunes > maxLen {
			// Truncate by runes, not bytes
			runes := []rune(displayText)
			displayText = string(runes[:maxLen])
			displayRunes = maxLen
		}
		textStyle := lipgloss.NewStyle().Foreground(textColor)
		padding := innerWidth - displayRunes - 1 // -1 for cursor

		leftLines = append(leftLines, borderStyle.Render("  ┌"+strings.Repeat("─", innerWidth)+"┐"))
		leftLines = append(leftLines, borderStyle.Render("  │")+textStyle.Render(displayText)+cursorStyle.Render("█")+strings.Repeat(" ", padding)+borderStyle.Render("│"))
		leftLines = append(leftLines, borderStyle.Render("  └"+strings.Repeat("─", innerWidth)+"┘"))
		leftLines = append(leftLines, "")
	}

	// Tickets section
	ticketTitleStyle := ui.WhiteBold
	leftLines = append(leftLines, ticketTitleStyle.Render(fmt.Sprintf(" Tickets (%d) ", len(m.tickets))))
	leftLines = append(leftLines, "")

	if len(m.tickets) == 0 {
		dimStyle := ui.Dim
		leftLines = append(leftLines, dimStyle.Render("  No tickets found"))
	} else {
		ticketStyle := ui.YellowBold
		for _, ticket := range m.tickets {
			leftLines = append(leftLines, fmt.Sprintf("  🎫 %s", ticketStyle.Render(ticket)))
		}
	}

	leftLines = append(leftLines, "")
	if len(m.commits) > 0 {
		hintStyle := ui.Dim
		enterStyle := ui.GreenBold
		leftLines = append(leftLines, hintStyle.Render("  Type to edit title"))
		leftLines = append(leftLines, hintStyle.Render("  Press ")+enterStyle.Render("Enter")+hintStyle.Render(" to create PR"))
	} else {
		dimStyle := ui.Dim
		leftLines = append(leftLines, dimStyle.Render("  Nothing to merge"))
	}

	leftTitleStyle := ui.CyanBold
	leftContent := panel(leftTitleStyle, "🚀 Create PR", leftLines)

	// Build RIGHT column (commits list)
	var commitLines []string
	commitLines = append(commitLines, "")

	// Max message length per line (account for indent)
	maxMsgLen := columnWidth - 14

	if len(m.commits) == 0 {
		dimStyle := ui.Dim
		commitLines = append(commitLines, dimStyle.Render("  No commits to merge"))
	} else {
		ticketRegex := m.config.TicketRegex()

		for _, commit := range m.commits {
			hashStyle := ui.Magenta
			ticketStyle := ui.YellowBold

			// Format: hash on line 1
			commitLines = append(commitLines,
				fmt.Sprintf("  %s", hashStyle.Render(commit.Hash)),
			)

			// Highlight tickets in yellow within the message
			msg := commit.Message
			styledMsg := msg
			if ticketRegex != nil {
				styledMsg = ticketRegex.ReplaceAllStringFunc(msg, func(match string) string {
					return ticketStyle.Render(match)
				})
			}

			// Wrap message to fit column, with indent
			indent := "    "
			words := strings.Fields(styledMsg)
			var line string
			for _, word := range words {
				testLine := line + " " + word
				if len(strings.TrimSpace(testLine)) > maxMsgLen && line != "" {
					commitLines = append(commitLines, indent+strings.TrimSpace(line))
					line = word
				} else {
					line = testLine
				}
			}
			if strings.TrimSpace(line) != "" {
				commitLines = append(commitLines, indent+strings.TrimSpace(line))
			}

			commitLines = append(commitLines, "") // spacing between commits
		}
	}

	commitTitleStyle := ui.CyanBold
	commitContent := commitTitleStyle.Render(fmt.Sprintf(" %d commits ", len(m.commits))) + "\n" + strings.Join(commitLines, "\n")

	// Use ColumnBox for consistent sizing - purple outer borders for consistency
	leftColumn := ui.ColumnBox(leftContent, "", ui.ColorPurple, true, columnWidth, panelHeight)
	rightColumn := ui.ColumnBox(commitContent, "", ui.ColorPurple, false, columnWidth-10, panelHeight)

	return ui.TwoColumns(leftColumn, rightColumn, 2)
}

func (m Model) renderTitleInput() string {
	mainBranch := m.mainBranch()

	defaultTitle := ""
	if m.flow != nil {
		defaultTitle = m.flow.DefaultTitle(mainBranch)
	}

	// Build left column (title input)
	var leftLines []string
	leftLines = append(leftLines, "")

	// Show branch flow
	if m.flow != nil {
		leftLines = append(leftLines, ui.BranchFlowDiagram(m.flow.HeadBranch(), m.flow.BaseBranch(mainBranch), m.branchColor(m.flow.HeadBranch()), m.branchColor(m.flow.BaseBranch(mainBranch))))
		leftLines = append(leftLines, "")
	}

	leftLines = append(leftLines, ui.SectionHeader("ENTER TITLE", ui.ColorCyan))
	leftLines = append(leftLines, "")

	// Input box with yellow border
	borderStyle := ui.Yellow
	cursorStyle := ui.Yellow

	var displayText string
	var textColor lipgloss.Color
	if m.prTitle == "" {
		displayText = fmt.Sprintf("%s (default)", defaultTitle)
		textColor = ui.ColorDarkGray
	} else {
		displayText = m.prTitle
		textColor = ui.ColorWhite
	}
	textStyle := lipgloss.NewStyle().Foreground(textColor)

	leftLines = append(leftLines, borderStyle.Render("  ┌")+borderStyle.Render(strings.Repeat("─", 38))+borderStyle.Render("┐"))
	leftLines = append(leftLines, borderStyle.Render("  │ ")+textStyle.Render(displayText)+cursorStyle.Render("█"))
	leftLines = append(leftLines, borderStyle.Render("  └")+borderStyle.Render(strings.Repeat("─", 38))+borderStyle.Render("┘"))
	leftLines = append(leftLines, "")

	hintStyle := ui.White
	enterStyle := ui.GreenBold
	escStyle := ui.YellowBold
	leftLines = append(leftLines, hintStyle.Render("  Press ")+enterStyle.Render("Enter")+hintStyle.Render(" to continue"))
	leftLines = append(leftLines, hintStyle.Render("  ")+escStyle.Render("Esc")+hintStyle.Render(" to go back"))

	leftTitleStyle := ui.YellowBold
	leftContent := panel(leftTitleStyle, "PR Title", leftLines)

	// Build right column (context)
	var rightLines []string
	rightLines = append(rightLines, "")
	rightLines = append(rightLines, ui.SectionHeader("CONTEXT", ui.ColorMagenta))
	rightLines = append(rightLines, "")

	labelStyle := ui.White

	if m.mode != nil && *m.mode == ModeBatch {
		// Batch mode - show selected repos and tickets
		selectedCount := 0
		var selectedNames []string
		for i, selected := range m.batch.selected {
			if selected && i < len(m.batch.repos) {
				selectedCount++
				selectedNames = append(selectedNames, m.batch.repos[i].DisplayName)
			}
		}
		repoStyle := ui.CyanBold
		ticketStyle := ui.YellowBold
		rightLines = append(rightLines, labelStyle.Render("  Repos: ")+repoStyle.Render(fmt.Sprintf("%d selected", selectedCount)))
		rightLines = append(rightLines, labelStyle.Render("  Tickets: ")+ticketStyle.Render(fmt.Sprintf("%d", len(m.tickets))))
		rightLines = append(rightLines, "")

		// Show tickets if any
		if len(m.tickets) > 0 {
			rightLines = append(rightLines, ui.SectionHeader("TICKETS", ui.ColorYellow))
			rightLines = append(rightLines, "")
			for i, ticket := range m.tickets {
				if i >= 6 {
					remaining := len(m.tickets) - 6
					rightLines = append(rightLines, fmt.Sprintf("  ... and %d more", remaining))
					break
				}
				rightLines = append(rightLines, fmt.Sprintf("  %s", ticketStyle.Render(ticket)))
			}
			rightLines = append(rightLines, "")
		}

		// Show selected repo names
		if len(selectedNames) > 0 {
			rightLines = append(rightLines, ui.SectionHeader("REPOS", ui.ColorCyan))
			rightLines = append(rightLines, "")
			for i, name := range selectedNames {
				if i >= 5 {
					remaining := len(selectedNames) - 5
					rightLines = append(rightLines, fmt.Sprintf("  ... and %d more", remaining))
					break
				}
				rightLines = append(rightLines, fmt.Sprintf("  • %s", repoStyle.Render(name)))
			}
		}
	} else {
		// Single mode - show repo and commits
		if m.repoInfo != nil {
			valueStyle := ui.Cyan
			rightLines = append(rightLines, labelStyle.Render("  Repo: ")+valueStyle.Render(m.repoInfo.DisplayName))
		}

		commitStyle := ui.CyanBold
		ticketStyle := ui.YellowBold
		rightLines = append(rightLines, labelStyle.Render("  Commits: ")+commitStyle.Render(fmt.Sprintf("%d", len(m.commits))))
		rightLines = append(rightLines, labelStyle.Render("  Tickets: ")+ticketStyle.Render(fmt.Sprintf("%d", len(m.tickets))))
		rightLines = append(rightLines, "")

		// Tickets preview
		if len(m.tickets) > 0 {
			rightLines = append(rightLines, ui.SectionHeader("TICKETS", ui.ColorYellow))
			rightLines = append(rightLines, "")
			for i, ticket := range m.tickets {
				if i >= 5 {
					remaining := len(m.tickets) - 5
					rightLines = append(rightLines, fmt.Sprintf("  ... and %d more", remaining))
					break
				}
				rightLines = append(rightLines, fmt.Sprintf("  %s", ticketStyle.Render(ticket)))
			}
		}
	}

	rightTitleStyle := ui.MagentaBold
	rightContent := panel(rightTitleStyle, "Context", rightLines)

	return ui.UnifiedPanel(leftContent, rightContent, 60, 35, ui.ColorYellow)
}

func (m Model) renderConfirmation() string {
	mainBranch := m.mainBranch()

	// Build left column (PR details)
	var leftLines []string
	leftLines = append(leftLines, "")

	// Show branch flow diagram
	if m.flow != nil {
		leftLines = append(leftLines, ui.BranchFlowDiagram(m.flow.HeadBranch(), m.flow.BaseBranch(mainBranch), m.branchColor(m.flow.HeadBranch()), m.branchColor(m.flow.BaseBranch(mainBranch))))
		leftLines = append(leftLines, "")
	}

	leftLines = append(leftLines, ui.SectionHeader("PR DETAILS", ui.ColorCyan))
	leftLines = append(leftLines, "")

	labelStyle := ui.White
	titleStyle := ui.WhiteBold
	leftLines = append(leftLines, fmt.Sprintf("  📝 %s %s", labelStyle.Render("Title:"), titleStyle.Render(m.prTitle)))

	if m.repoInfo != nil {
		repoStyle := ui.Cyan
		leftLines = append(leftLines, fmt.Sprintf("  📦 %s %s", labelStyle.Render("Repo: "), repoStyle.Render(m.repoInfo.DisplayName)))
	}

	leftLines = append(leftLines, "")

	// PR body preview section
	leftLines = append(leftLines, ui.SectionHeader("PR BODY PREVIEW", ui.ColorYellow))
	leftLines = append(leftLines, "")

	if len(m.tickets) == 0 {
		dimStyle := ui.Dim
		leftLines = append(leftLines, dimStyle.Render("  (empty)"))
	} else {
		leftLines = append(leftLines, "  # Tickets")
		leftLines = append(leftLines, "")
		for _, ticket := range m.tickets {
			ticketStyle := ui.Yellow
			urlStyle := ui.Cyan
			linearURL := fmt.Sprintf("https://linear.app/%s/issue/%s", m.config.Tickets.LinearOrg, strings.ToLower(ticket))
			leftLines = append(leftLines, fmt.Sprintf("  ### - Closes %s%s", ticketStyle.Render(fmt.Sprintf("[%s]", ticket)), urlStyle.Render(fmt.Sprintf("(%s)", linearURL))))
		}
	}

	leftLines = append(leftLines, "")

	// Confirm section
	leftLines = append(leftLines, ui.SectionHeader("CONFIRM", ui.ColorGreen))
	leftLines = append(leftLines, "")

	// Show different message for create vs update
	isUpdate := m.existingPR != nil
	if isUpdate {
		warningStyle := ui.YellowBold
		leftLines = append(leftLines, warningStyle.Render("  ⚠ PR already exists - will update"))
		leftLines = append(leftLines, "")
		leftLines = append(leftLines, "  Update this PR?")
	} else {
		leftLines = append(leftLines, "  Create this PR?")
	}
	leftLines = append(leftLines, "")
	leftLines = append(leftLines, ui.YesNoButtons(m.confirmSelection))

	leftTitleStyle := ui.CyanBold
	panelTitle := " 🚀 Create PR "
	if isUpdate {
		panelTitle = " 🔄 Update PR "
	}
	leftContent := leftTitleStyle.Render(panelTitle) + "\n" + strings.Join(leftLines, "\n")

	// Build right column (summary)
	var rightLines []string
	rightLines = append(rightLines, "")

	// Branch flow
	if m.flow != nil {
		headBranch := m.flow.HeadBranch()
		baseBranch := m.flow.BaseBranch(mainBranch)
		headStyle := lipgloss.NewStyle().Foreground(m.branchColor(headBranch)).Bold(true)
		baseStyle := lipgloss.NewStyle().Foreground(m.branchColor(baseBranch)).Bold(true)
		arrowStyle := ui.White
		rightLines = append(rightLines, fmt.Sprintf("  %s %s %s", headStyle.Render(headBranch), arrowStyle.Render("→"), baseStyle.Render(baseBranch)))
		rightLines = append(rightLines, "")
	}

	// Repo
	if m.repoInfo != nil {
		labelStyle := ui.Dim
		valueStyle := ui.CyanBold
		rightLines = append(rightLines, fmt.Sprintf("  %s %s", labelStyle.Render("Repo:"), valueStyle.Render(m.repoInfo.DisplayName)))
	}

	// Title preview
	if m.prTitle != "" {
		labelStyle := ui.Dim
		titleStyle := ui.White
		title := truncateString(m.prTitle, 25)
		rightLines = append(rightLines, fmt.Sprintf("  %s %s", labelStyle.Render("Title:"), titleStyle.Render(title)))
	}

	rightLines = append(rightLines, "")
	rightLines = append(rightLines, ui.SectionHeader("STATS", ui.ColorMagenta))
	rightLines = append(rightLines, "")

	commitStyle := ui.CyanBold
	ticketStyle := ui.YellowBold
	rightLines = append(rightLines, fmt.Sprintf("  📊 %s commits", commitStyle.Render(fmt.Sprintf("%d", len(m.commits)))))
	rightLines = append(rightLines, fmt.Sprintf("  🎫 %s tickets", ticketStyle.Render(fmt.Sprintf("%d", len(m.tickets)))))

	// List tickets
	if len(m.tickets) > 0 {
		rightLines = append(rightLines, "")
		dimStyle := ui.Dim
		for _, ticket := range m.tickets {
			rightLines = append(rightLines, fmt.Sprintf("     %s %s", dimStyle.Render("•"), ticketStyle.Render(ticket)))
		}
	}

	if m.dryRun {
		rightLines = append(rightLines, "")
		warningStyle := ui.YellowBold
		rightLines = append(rightLines, warningStyle.Render("  ⚠ DRY RUN MODE"))
	}

	rightTitleStyle := ui.MagentaBold
	rightContent := panel(rightTitleStyle, "📊 Summary", rightLines)

	return ui.UnifiedPanel(leftContent, rightContent, 60, 35, ui.ColorCyan)
}

func (m Model) renderCreating() string {
	mainBranch := m.mainBranch()

	var lines []string
	lines = append(lines, "")

	// Main status
	spinner := ui.Spinner(m.spinnerFrame)
	spinnerStyle := ui.Cyan
	statusStyle := ui.CyanBold
	lines = append(lines, fmt.Sprintf("  %s %s", spinnerStyle.Render(spinner), statusStyle.Render("Creating Pull Request...")))
	lines = append(lines, "")

	// Details section
	if m.flow != nil && m.repoInfo != nil {
		lines = append(lines, ui.SectionHeader("DETAILS", ui.ColorMagenta))
		lines = append(lines, "")

		labelStyle := ui.White
		repoStyle := ui.Cyan
		headStyle := lipgloss.NewStyle().Foreground(m.branchColor(m.flow.HeadBranch())).Bold(true)
		baseStyle := lipgloss.NewStyle().Foreground(m.branchColor(m.flow.BaseBranch(mainBranch))).Bold(true)
		titleStyle := ui.Yellow

		lines = append(lines, labelStyle.Render("  Repo:   ")+repoStyle.Render(m.repoInfo.DisplayName))
		lines = append(lines, labelStyle.Render("  Branch: ")+headStyle.Render(m.flow.HeadBranch())+labelStyle.Render(" -> ")+baseStyle.Render(m.flow.BaseBranch(mainBranch)))
		lines = append(lines, labelStyle.Render("  Title:  ")+titleStyle.Render(m.prTitle))
	}

	titleStyle := ui.CyanBold
	return panel(titleStyle, "Creating PR", lines)
}

func (m Model) renderComplete() string {
	var lines []string

	// Use pulsing green effect based on sine wave
	var successColor lipgloss.Color
	pulseIntensity := (math.Sin(m.pulsePhase) + 1.0) / 2.0
	if pulseIntensity > 0.5 {
		successColor = ui.ColorGreen
	} else {
		successColor = ui.ColorLightGreen
	}

	// Typewriter effect for message
	message := "PR Created Successfully!"
	revealedText := revealRunes(message, m.typewriterPos)

	successStyle := lipgloss.NewStyle().Foreground(successColor).Bold(true)
	iconStyle := lipgloss.NewStyle().Foreground(successColor).Bold(true)
	urlStyle := ui.Cyan

	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %s %s", iconStyle.Render("✓"), successStyle.Render(revealedText)))
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  🔗 %s", urlStyle.Render(m.prURL)))
	lines = append(lines, "")

	// Render confetti
	confettiLines := m.renderConfetti()
	if confettiLines != "" {
		lines = append(lines, confettiLines)
	}

	titleStyle := ui.GreenBold
	return panel(titleStyle, "🎉 Success", lines)
}
