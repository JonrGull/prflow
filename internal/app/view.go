package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/ui"
	"github.com/JonrGull/prflow/internal/update"

	"github.com/charmbracelet/lipgloss"
)

// The view shell: the frame every screen draws inside, and the helpers they all
// share.
//
// The screens themselves live in their own files — view.go renders the banner,
// the tabs, the status bar and the help overlay, then hands the middle to
// renderContentWithHeight. Adding a screen means adding a case there and an
// entry in screenTitles; forgetting either renders an empty box rather than
// failing.

// minContentHeight is the floor View puts under the height it hands a screen.
// Below this the banner and status bar have already taken the terminal, so the
// content overflows whatever it is given — the screens size themselves against
// this value rather than against something smaller they can never receive.
const minContentHeight = 10

// minTerminalHeight is the shortest terminal in which a height-aware screen can
// actually fit. Under it the banner, tabs and status bar have taken the window
// and minContentHeight still insists on room for content, so the view overflows
// by design; F (fullscreen) drops the banner and tabs and buys back seven rows.
const minTerminalHeight = 25

// The outer box the simpler screens sit inside: a rounded border top and bottom,
// plus a blank padding row either side. Named so the height charged for it and
// the height it actually takes cannot drift apart.
const outerBoxPadding = 1

// isFullLayoutScreen reports whether a screen draws its own frame rather than
// sitting inside the outer box.
func isFullLayoutScreen(s Screen) bool {
	switch s {
	case ScreenLoading, ScreenBatchRepoSelect, ScreenViewOpenPrs, ScreenViewAllPrs,
		ScreenBatchSummary, ScreenMergeSummary, ScreenCommitReview,
		ScreenPullProgress, ScreenPullSummary, ScreenActionsOverview:
		return true
	}
	return false
}

// activeTabForDisplay returns the tab to highlight, or -1 for none.
//
// The main menu is where you pick a flow, not a flow itself, so nothing there
// should look selected — it used to light up "Single" before you had chosen
// anything, because activeTab starts at 0.
func (m Model) activeTabForDisplay() int {
	if m.screen == ScreenMainMenu {
		return -1
	}
	return m.activeTab
}

// contentWidth returns the usable content width, adapting to terminal size
func (m Model) contentWidth() int {
	w := m.width - 8
	if w < 40 {
		w = 40
	}
	return w
}

// View renders the application
func (m Model) View() string {
	if m.shouldQuit {
		return ""
	}

	contentWidth := m.contentWidth()

	// Some screens draw their own full layout; the rest sit inside an outer box
	// that costs them height. Decided here because the chrome measurement below
	// has to know which.
	boxed := !isFullLayoutScreen(m.screen) || m.showHelp

	// The chrome is drawn first and then measured, rather than described by a
	// set of constants that have to be kept in step with it. The banner has
	// size tiers and the status bar wraps, so its height is not knowable up
	// front — and guessing it low pushes the content off the bottom of the
	// terminal without anything failing.
	var sections []string
	if !m.fullscreen {
		sections = append(sections,
			ui.RenderBanner(m.dryRun, contentWidth),
			// The main menu is not one of the tabs: highlighting Single there
			// claims you are in a flow you have not chosen yet.
			ui.RenderTabBar(m.activeTabForDisplay(), contentWidth))
	}
	statusBar := m.renderStatusBar()

	chrome := 0
	for _, s := range sections {
		chrome += lipgloss.Height(s) + 1 // +1 for the newline joining sections
	}
	if statusBar != "" {
		chrome += lipgloss.Height(statusBar) + 1
	}
	chrome += 3 // blank line before the status bar, and the frame's border rows

	// Deliberately not charging the outer box's two padding rows on top.
	// A review argued they were missing and that boxed screens therefore render
	// two rows too tall; measuring says otherwise. availableHeight is
	// m.height - chrome and a height-aware screen fills exactly that, so the
	// total is m.height whatever chrome is — moving rows between the two only
	// changes how much content is shown. The overflow that prompted the claim
	// is minContentHeight below: at a 24-row terminal chrome is 18, leaving 6,
	// and the floor insists on 10.

	availableHeight := m.height - chrome
	if availableHeight < minContentHeight {
		availableHeight = minContentHeight
	}

	if boxed {
		outerBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ui.ColorPurple).
			Width(contentWidth).
			Padding(outerBoxPadding, 2)

		sections = append(sections, outerBox.Render(m.renderContentWithHeight(availableHeight)))
	} else {
		sections = append(sections, m.renderContentWithHeight(availableHeight))
	}

	// Status bar, rendered above so its height could be measured.
	sections = append(sections, "")
	sections = append(sections, statusBar)

	content := strings.Join(sections, "\n")

	// Center horizontally in the terminal
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top, content)
}

// renderHelp lists the current screen's bindings alongside the global ones.
//
// Both come from the same tables the status bar reads, so the overlay cannot
// drift from what the keys actually do. The status bar only has room for the
// current screen's hints, which left the global keys — and the fact that `?`
// exists at all — documented nowhere but the source.
func (m Model) renderHelp() string {
	var lines []string

	section := func(title string, hints []keyHint) {
		if len(hints) == 0 {
			return
		}
		lines = append(lines, ui.SectionHeader(title, ui.ColorCyan))
		// Align descriptions against the widest key on the screen.
		width := 0
		for _, h := range hints {
			if n := lipgloss.Width(h.Key); n > width {
				width = n
			}
		}
		for _, h := range hints {
			key := lipgloss.NewStyle().Foreground(h.Color).Bold(true).Render(h.Key)
			lines = append(lines, fmt.Sprintf("  %s   %s",
				visRightAlign(key, width), ui.White.Render(h.Desc)))
		}
		lines = append(lines, "")
	}

	section(strings.ToUpper(screenTitle(m.screen)), m.keyHints())
	section("ANYWHERE", globalKeyHints)

	lines = append(lines, ui.Dim.Render("  q closes this help. On most screens q quits;"))
	lines = append(lines, ui.Dim.Render("  on the error screen it goes back instead."))
	lines = append(lines, "")
	// Deliberately not the absolute path: it embeds $HOME, and UserConfigDir
	// resolves differently on macOS, so it cannot be rendered reproducibly.
	lines = append(lines, ui.Dim.Render("  Press c on the main menu to open the config folder."))

	return ui.CyanBold.Render(" ?  Keyboard Shortcuts ") + "\n\n" + strings.Join(lines, "\n")
}

// panel renders a titled block: a padded, styled title above its lines.
//
// This exact concatenation appeared at 14 sites, varying only in the style, the
// title and the slice. Taking the style rather than a colour keeps it a literal
// restatement of what each site already did, so it cannot change output.
func panel(titleStyle lipgloss.Style, title string, lines []string) string {
	return titleStyle.Render(" "+title+" ") + "\n" + strings.Join(lines, "\n")
}

// wrapToWidth word-wraps plain text to width and indents each line.
//
// The descriptions on the PR-type screen used to be hand-broken string
// literals, which only worked while the text was fixed. It is derived from the
// configured steps now, so it has to wrap on its own.
func wrapToWidth(text string, width int, indent string) []string {
	wrapped := lipgloss.NewStyle().Width(width).Render(text)
	lines := strings.Split(wrapped, "\n")
	for i, l := range lines {
		lines[i] = indent + strings.TrimRight(l, " ")
	}
	return lines
}

// menuRowWidth is the width the numbered selection menus highlight across.
const menuRowWidth = 46

// numberedMenuRow renders one two-line row of a numbered selection menu: arrow,
// number and title, with an indented description beneath.
//
// The PR-type and pull-branch screens each wrote this out longhand. Note this is
// deliberately *not* ui.MenuRow: that renders an unstyled icon with different
// spacing and a single title colour, whereas these screens style the number and
// need a multi-coloured title — the PR-type screen colours the head and base
// branches differently, which is what makes it readable at a glance.
//
// title arrives pre-rendered for exactly that reason, and must already carry the
// selected background when selected is true.
func numberedMenuRow(num, title, desc string, selected bool) []string {
	arrow := "  "
	if selected {
		arrow = "▶ "
	}

	if selected {
		rowStyle := lipgloss.NewStyle().Background(ui.ColorDarkGray).Width(menuRowWidth)
		return []string{
			rowStyle.Render(
				ui.Cyan.Background(ui.ColorDarkGray).Render(arrow) +
					ui.YellowBold.Background(ui.ColorDarkGray).Render(num) + title),
			rowStyle.Render("      " + ui.White.Background(ui.ColorDarkGray).Render(desc)),
		}
	}

	return []string{
		ui.Cyan.Render(arrow) + ui.YellowBold.Render(num) + title,
		"      " + ui.White.Render(desc),
	}
}

// screenTitle gives a human label for the help overlay's first section.
func screenTitle(s Screen) string {
	if name, ok := screenTitles[s]; ok {
		return name
	}
	return "This screen"
}

var screenTitles = map[Screen]string{
	ScreenMainMenu:          "Main menu",
	ScreenPrTypeSelect:      "PR type",
	ScreenCommitReview:      "Commit review",
	ScreenTitleInput:        "PR title",
	ScreenConfirmation:      "Confirmation",
	ScreenBatchConfirmation: "Confirmation",
	ScreenMergeConfirmation: "Confirmation",
	ScreenComplete:          "PR created",
	ScreenError:             "Error",
	ScreenBatchRepoSelect:   "Select repos",
	ScreenBatchSummary:      "Batch summary",
	ScreenViewOpenPrs:       "Release PRs",
	ScreenViewAllPrs:        "All open PRs",
	ScreenMergeSummary:      "Merge summary",
	ScreenUpdatePrompt:      "Update available",
	ScreenSessionHistory:    "Session history",
	ScreenPullBranchSelect:  "Pull branch",
	ScreenPullSummary:       "Pull summary",
	ScreenActionsOverview:   "Actions",
	ScreenQaTagSelect:       "QA tagging",
	ScreenSettings:          "Settings",
	ScreenListEdit:          "List editor",
}

func (m Model) renderContentWithHeight(availableHeight int) string {
	if m.showHelp {
		return m.renderHelp()
	}

	switch m.screen {
	case ScreenMainMenu:
		return m.renderMainMenu()
	case ScreenPrTypeSelect:
		return m.renderPrTypeSelect()
	case ScreenLoading:
		return m.renderLoading()
	case ScreenCommitReview:
		return m.renderCommitReviewWithHeight(availableHeight)
	case ScreenTitleInput:
		return m.renderTitleInput()
	case ScreenConfirmation:
		return m.renderConfirmation()
	case ScreenCreating:
		return m.renderCreating()
	case ScreenComplete:
		return m.renderComplete()
	case ScreenError:
		return m.renderError()
	case ScreenBatchRepoSelect:
		return m.renderBatchRepoSelectWithHeight(availableHeight)
	case ScreenBatchConfirmation:
		return m.renderBatchConfirmationWithHeight(availableHeight)
	case ScreenBatchProcessing:
		return m.renderBatchProcessing()
	case ScreenBatchSummary:
		return m.renderBatchSummaryWithHeight(availableHeight)
	case ScreenViewOpenPrs:
		return m.renderViewOpenPrsWithHeight(availableHeight)
	case ScreenMergeConfirmation:
		return m.renderMergeConfirmation()
	case ScreenMerging:
		return m.renderMerging()
	case ScreenMergeSummary:
		return m.renderMergeSummaryWithHeight(availableHeight)
	case ScreenUpdatePrompt:
		return m.renderUpdatePrompt()
	case ScreenUpdating:
		return m.renderUpdating()
	case ScreenSessionHistory:
		return m.renderSessionHistory()
	case ScreenPullBranchSelect:
		return m.renderPullBranchSelect()
	case ScreenPullProgress:
		return m.renderPullProgress()
	case ScreenPullSummary:
		return m.renderPullSummaryWithHeight(availableHeight)
	case ScreenViewAllPrs:
		return m.renderViewAllPrsWithHeight(availableHeight)
	case ScreenActionsOverview:
		return m.renderActionsOverviewWithHeight(availableHeight)
	case ScreenQaTagSelect:
		return m.renderQaTagSelect()
	case ScreenSettings:
		return m.renderSettingsWithHeight(availableHeight)
	case ScreenFirstRun:
		return m.renderFirstRun()
	case ScreenListEdit:
		return m.renderListEditWithHeight(availableHeight)
	default:
		return ""
	}
}

// applyViewportScroll scrolls content to keep the highlighted line visible
func applyViewportScroll(lines []string, headerLines int, highlightedLine int, visibleLines int) string {
	if len(lines) <= headerLines+visibleLines {
		// No scrolling needed
		return strings.Join(lines, "\n")
	}

	// Keep header lines fixed
	header := lines[:headerLines]
	content := lines[headerLines:]

	scrollOffset := 0

	if highlightedLine >= headerLines {
		// Calculate scroll offset to keep highlighted line visible
		highlightInContent := highlightedLine - headerLines

		// Keep some padding around the highlighted item
		padding := 2
		if highlightInContent >= visibleLines-padding {
			scrollOffset = highlightInContent - visibleLines + padding + 1
		}
		if scrollOffset > len(content)-visibleLines {
			scrollOffset = len(content) - visibleLines
		}
		if scrollOffset < 0 {
			scrollOffset = 0
		}
	}

	endOffset := scrollOffset + visibleLines
	if endOffset > len(content) {
		endOffset = len(content)
	}

	// Build visible content with scroll indicators (copy to avoid mutating original)
	visibleContent := make([]string, endOffset-scrollOffset)
	copy(visibleContent, content[scrollOffset:endOffset])

	// Add scroll indicators
	dimStyle := ui.Dim
	hasAbove := scrollOffset > 0
	hasBelow := endOffset < len(content)

	if hasAbove {
		visibleContent[0] = dimStyle.Render("  ▲ more above")
	}
	if hasBelow {
		visibleContent[len(visibleContent)-1] = dimStyle.Render("  ▼ more below")
	}

	return strings.Join(append(header, visibleContent...), "\n")
}

// truncateString truncates a string to maxLen runes, adding ellipsis if needed
func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen < 1 {
		return ""
	}
	return string(runes[:maxLen-1]) + "…"
}

// revealRunes returns the first n characters of s for the typewriter effect.
// It counts runes, not bytes — slicing bytes splits multi-byte characters
// mid-sequence and renders replacement glyphs partway through the animation.
func revealRunes(s string, n int) string {
	runes := []rune(s)
	if n > len(runes) {
		n = len(runes)
	}
	if n < 0 {
		n = 0
	}
	return string(runes[:n])
}

// timeNow is a seam so rendering can be made deterministic in tests. Screens
// that display relative times or a refresh countdown would otherwise produce
// different output on every run.
var timeNow = time.Now

// configPathFn is a seam for the same reason. The real path embeds $HOME, and
// os.UserConfigDir resolves somewhere else entirely on macOS, so a screen that
// prints it cannot be recorded reproducibly without this.
var configPathFn = config.Path

func relativeTime(t time.Time) string {
	d := timeNow().Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// packHints lays hints out across as many lines as it takes to fit width,
// filling each line greedily.
//
// A hint is never split: breaking "⇧C Copy All" across two rows reads worse
// than an extra row does. A single hint wider than the whole bar gets its own
// line and is left to overflow, since there is nothing better to do with it.
func packHints(hints []string, width int) []string {
	const sep = "  "
	sepWidth := lipgloss.Width(sep)

	var lines []string
	var cur string
	curWidth := 0

	for _, h := range hints {
		hw := lipgloss.Width(h)
		if cur == "" {
			cur, curWidth = h, hw
			continue
		}
		if curWidth+sepWidth+hw > width {
			lines = append(lines, cur)
			cur, curWidth = h, hw
			continue
		}
		cur += sep + h
		curWidth += sepWidth + hw
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// truncateVisible cuts a rendered string to width, counting display columns and
// not the ANSI escapes between them.
func truncateVisible(s string, width int) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	shown := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			// Copy the escape through without charging it any width.
			start := i
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				i++
			}
			b.WriteString(s[start:i])
			continue
		}
		r := []rune(s[i:])[0]
		w := lipgloss.Width(string(r))
		if shown+w > width {
			break
		}
		b.WriteRune(r)
		shown += w
		i += len(string(r))
	}
	return b.String()
}

func (m Model) renderStatusBar() string {
	if m.showHelp {
		return lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ui.ColorDarkGray).
			Padding(0, 1).
			Render(ui.KeyBinding("Esc", "Close help", ui.ColorYellow))
	}

	hints := make([]string, 0, len(m.keyHints()))
	for _, h := range m.keyHints() {
		hints = append(hints, ui.KeyBinding(h.Key, h.Desc, h.Color))
	}

	installedVersion := ""
	if m.version != "" {
		installedVersion = update.VersionDisplay(m.version)
	}

	// Add help/tab/fullscreen hints when not in text input mode
	if !m.isTextInputActive() {
		hints = append(hints,
			ui.KeyBinding("?", "Help", ui.ColorDarkGray),
			ui.KeyBinding("[ ]", "Tab", ui.ColorDarkGray),
			ui.KeyBinding("F", "Fullscreen", ui.ColorDarkGray),
		)
	}

	// Flag config problems from anywhere, pointing at the screen that explains
	// them. Without this the only symptom of a broken config is an empty list.
	if n := len(m.configDiagnostics); n > 0 && m.screen != ScreenSettings {
		color := ui.ColorYellow
		if config.HasErrors(m.configDiagnostics) {
			color = ui.ColorRed
		}
		hints = append(hints, ui.KeyBinding("o", fmt.Sprintf("⚠ %d config issue(s)", n), color))
	}

	// Don't render an empty box if there are no hints or version
	if len(hints) == 0 && m.copyFeedback == "" && installedVersion == "" {
		return ""
	}

	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.ColorDarkGray).
		Padding(0, 1)

	// Add copy feedback if present
	if m.copyFeedback != "" {
		feedbackStyle := ui.GreenBold
		if strings.HasPrefix(m.copyFeedback, "✗") {
			feedbackStyle = ui.RedBold
		}
		hints = append(hints, feedbackStyle.Render("│  "+m.copyFeedback))
	}

	// The hints used to be one joined line, which the terminal simply cut off —
	// the main menu's list is 22 columns too wide even at 120.
	var contentLines []string
	hotkeyLines := packHints(hints, m.contentWidth()-4) // border 2 + padding 2
	contentLines = append(contentLines, hotkeyLines...)
	hotkeysLine := ""
	if len(hotkeyLines) > 0 {
		hotkeysLine = hotkeyLines[0]
		for _, l := range hotkeyLines[1:] {
			if lipgloss.Width(l) > lipgloss.Width(hotkeysLine) {
				hotkeysLine = l
			}
		}
	}

	if installedVersion != "" || m.ghUser != "" {
		versionStyle := ui.Dim
		var infoParts []string
		if installedVersion != "" {
			infoParts = append(infoParts, fmt.Sprintf("Version: %s", installedVersion))
		}
		if m.ghUser != "" {
			infoParts = append(infoParts, fmt.Sprintf("gh: %s", m.ghUser))
		}
		infoLine := strings.Join(infoParts, "  •  ")
		if m.updateCheckInProgress {
			spinnerStyle := ui.Cyan
			infoLine = fmt.Sprintf("%s  •  Checking updates %s", infoLine, spinnerStyle.Render(ui.Spinner(m.spinnerFrame)))
		}

		// The metadata was allowed to widen the bar past the hints, and nothing
		// bounded it by the terminal — a long gh login or release tag pushed the
		// whole status box off the edge. It is the one line here that is not
		// under our control, so it gets cut rather than the box.
		inner := m.contentWidth() - 4 // border 2 + padding 2
		if lipgloss.Width(infoLine) > inner {
			infoLine = truncateVisible(infoLine, inner)
		}

		targetWidth := lipgloss.Width(hotkeysLine)
		if w := lipgloss.Width(infoLine); w > targetWidth {
			targetWidth = w
		}
		if targetWidth > 0 {
			infoLine = lipgloss.PlaceHorizontal(targetWidth, lipgloss.Center, infoLine)
		}
		contentLines = append(contentLines, versionStyle.Render(infoLine))
	}

	return borderStyle.Render(strings.Join(contentLines, "\n"))
}

// ptrEqual compares two string pointers for equality
func ptrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// visPad right-pads a (possibly ANSI-styled) string to the given visual width
func visPad(s string, width int) string {
	vis := lipgloss.Width(s)
	if vis >= width {
		return s
	}
	return s + strings.Repeat(" ", width-vis)
}

// visRightAlign right-aligns a (possibly ANSI-styled) string within the given visual width
func visRightAlign(s string, width int) string {
	vis := lipgloss.Width(s)
	if vis >= width {
		return s
	}
	return strings.Repeat(" ", width-vis) + s
}
