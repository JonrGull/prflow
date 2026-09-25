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
// The screens themselves live in their own files — view.go renders the header,
// the footer and the help overlay, then hands the middle to
// renderContentWithHeight. Adding a screen means adding a case there and an
// entry in screenTitles; forgetting either renders an empty box rather than
// failing.

// minContentHeight is the floor View puts under the height it hands a screen.
// Below this the header and footer have already taken the terminal, so the
// content overflows whatever it is given — the screens size themselves against
// this value rather than against something smaller they can never receive.
const minContentHeight = 10

// minTerminalHeight is the shortest terminal in which a height-aware screen can
// actually fit. Under it the header and footer have taken the window and
// minContentHeight still insists on room for content, so the view overflows by
// design; F (fullscreen) drops the header and buys back three rows. It was 25
// while the header was a five-row ASCII wordmark.
const minTerminalHeight = 19

// The outer box the simpler screens sit inside: a rounded border top and bottom,
// plus a blank padding row either side. Named so the height charged for it and
// the height it actually takes cannot drift apart.
const outerBoxPadding = 1

// unboxedHeight is the room a full-layout screen has, as View spends it. chrome's
// figure charges for the outer box and has a floor a short terminal lacks.
func (m Model) unboxedHeight() int {
	header, footer, _ := m.chrome()
	room := m.height - lipgloss.Height(footer) - 1 // the blank row above the footer
	if header != "" {
		room -= lipgloss.Height(header)
	}
	return room
}

// boxedHeight is the room inside the outer box, from the same arithmetic: the
// box takes its two borders and two padding rows.
func (m Model) boxedHeight() int {
	return max(m.unboxedHeight()-2-2*outerBoxPadding, 1)
}

// isFullLayoutScreen reports whether a screen draws its own frame rather than
// sitting inside the outer box.
func isFullLayoutScreen(s Screen) bool {
	switch s {
	case ScreenMainMenu, ScreenLoading, ScreenBatchRepoSelect, ScreenViewOpenPrs, ScreenViewAllPrs,
		ScreenBatchSummary, ScreenMergeSummary, ScreenCommitReview,
		ScreenPullProgress, ScreenPullSummary, ScreenActionsOverview, ScreenShipped:
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

	header, statusBar, availableHeight := m.chrome()

	var content string
	if boxed {
		outerBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ui.ColorBorder).
			Width(contentWidth).
			Padding(outerBoxPadding, 2)

		content = outerBox.Render(m.renderContentWithHeight(availableHeight))
	} else {
		content = m.renderContentWithHeight(availableHeight)
	}

	// The header keeps the top row and the footer the bottom one. A screen
	// shorter than the space between them is centred in it, rather than left
	// under the header with the spare rows piled above the footer. At least one
	// blank line always separates content and footer: the row chrome() charges.
	spare := m.height - lipgloss.Height(content) - lipgloss.Height(statusBar)
	if header != "" {
		spare -= lipgloss.Height(header)
	}
	above := max(spare-1, 0) / 2
	below := max(spare-above, 1)

	body := strings.Repeat("\n", above) + content
	if header != "" {
		body = header + "\n" + body
	}
	if statusBar != "" {
		body += strings.Repeat("\n", below+1) + statusBar
	}

	// Center horizontally in the terminal
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top, body)
}

// frameWidth is the width of the outer box, border included. The header and
// footer span it, so the three line up.
func (m Model) frameWidth() int {
	return m.contentWidth() + 2
}

// chrome renders the header and footer and returns the height left between
// them for the screen.
//
// The chrome is drawn and then measured, rather than described by constants
// that have to be kept in step with it: the header sheds content by width and
// the footer wraps, so neither height is knowable up front — and guessing low
// pushes the content off the bottom of the terminal without anything failing.
// It is a method so a key handler can size its scrolling against the same
// number the renderer is handed; Actions used to guess from a banner height and
// scroll its cursor out of sight.
//
// The outer box's two padding rows are deliberately not charged. availableHeight
// is m.height minus chrome and a height-aware screen fills exactly that, so the
// total is m.height whatever chrome is — moving rows between the two only
// changes how much content is shown.
func (m Model) chrome() (header, footer string, availableHeight int) {
	if !m.fullscreen {
		header = m.renderHeader()
	}
	footer = m.renderStatusBar()

	used := 0
	if header != "" {
		used += lipgloss.Height(header) + 1 // +1 for the newline joining it
	}
	if footer != "" {
		used += lipgloss.Height(footer) + 1
	}
	used += 3 // blank line before the footer, and the frame's border rows

	return header, footer, max(m.height-used, minContentHeight)
}

// renderHeader draws the brand, tabs and metadata line.
func (m Model) renderHeader() string {
	var meta []string
	if m.ghUser != "" {
		meta = append(meta, "gh: "+m.ghUser)
	}
	if m.version != "" {
		meta = append(meta, "v"+update.VersionDisplay(m.version))
	}
	if m.updateCheckInProgress {
		meta = append(meta, "checking for updates "+ui.Spinner(m.spinnerFrame))
	}
	return ui.RenderHeader(ui.HeaderInfo{
		// The main menu is not one of the tabs: highlighting Single there
		// claims you are in a flow you have not chosen yet.
		ActiveTab: m.activeTabForDisplay(),
		Tabs:      m.visibleTabs(),
		DryRun:    m.dryRun,
		Meta:      strings.Join(meta, " · "),
	}, m.frameWidth())
}

// renderHelp lists the current screen's bindings alongside the global ones.
//
// Both come from the same tables the footer reads, so the overlay cannot
// drift from what the keys actually do. The footer only has room for the
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
	section("ANYWHERE", m.anywhereHints())

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

// tailToWidth keeps the end of s that fits in width cells, marking the cut
// with "…". For a value being typed, where the end is where the cursor is. The
// editors drew the whole value, and a long one wrapped, which broke the
// screen's height budget.
func tailToWidth(s string, width int) string {
	if width < 2 || lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	for i := range r {
		if lipgloss.Width(string(r[i:])) <= width-1 {
			return "…" + string(r[i:])
		}
	}
	return "…"
}

// menuRowWidth is the width the numbered selection menus highlight across.
const menuRowWidth = 46

// numberedMenuRow renders one two-line row of a numbered selection menu: arrow,
// number and title, with an indented description beneath.
//
// The PR-type and pull-branch screens each wrote this out longhand. The title
// can be multi-coloured: the PR-type screen colours the head and base branches
// differently, which is what makes it readable at a glance.
//
// title arrives pre-rendered for exactly that reason, and must already carry the
// selected background when selected is true.
func numberedMenuRow(num, title, desc string, selected bool) []string {
	arrow := "  "
	if selected {
		arrow = "▶ "
	}

	if selected {
		rowStyle := lipgloss.NewStyle().Background(ui.ColorSelection).Width(menuRowWidth)
		return []string{
			rowStyle.Render(
				ui.Cyan.Background(ui.ColorSelection).Render(arrow) +
					ui.YellowBold.Background(ui.ColorSelection).Render(num) + title),
			rowStyle.Render("      " + ui.White.Background(ui.ColorSelection).Render(desc)),
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
	ScreenShipped:           "Shipped",
}

func (m Model) renderContentWithHeight(availableHeight int) string {
	if m.showHelp {
		return m.renderHelp()
	}

	switch m.screen {
	case ScreenMainMenu:
		return m.renderHome(m.unboxedHeight())
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
		return m.renderSessionHistory(m.boxedHeight())
	case ScreenPullBranchSelect:
		return m.renderPullBranchSelect()
	case ScreenPullProgress:
		return m.renderPullProgress()
	case ScreenPullSummary:
		return m.renderPullSummaryWithHeight(availableHeight)
	case ScreenViewAllPrs:
		return m.renderViewAllPrsWithHeight(availableHeight)
	case ScreenActionsOverview:
		return m.renderActionsOverviewWithHeight(m.unboxedHeight())
	case ScreenShipped:
		return m.renderShippedWithHeight(m.unboxedHeight())
	case ScreenQaTagSelect:
		return m.renderQaTagSelect()
	case ScreenSettings:
		return m.renderSettingsWithHeight(m.boxedHeight())
	case ScreenFirstRun:
		return m.renderFirstRun()
	case ScreenListEdit:
		return m.renderListEditWithHeight(m.boxedHeight())
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

// truncateString cuts s to maxWidth display columns, ending in an ellipsis
// when anything was cut.
//
// It used to count runes, so a CJK or emoji title — two columns a rune — came
// back up to twice as wide as asked, and pushed the All PRs status columns off
// their row.
func truncateString(s string, maxWidth int) string {
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	if maxWidth < 1 {
		return ""
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > maxWidth {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
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

// renderStatusBar draws the footer: a rule, then the current screen's key hints
// wrapped to the frame. The gh user and version used to share a box with the
// hints; they are in the header now.
func (m Model) renderStatusBar() string {
	width := m.frameWidth()
	rule := lipgloss.NewStyle().Foreground(ui.ColorBorder).Render(strings.Repeat("─", width))

	var hints []string
	if m.showHelp {
		hints = []string{ui.KeyBinding("Esc", "Close help", ui.ColorYellow)}
	} else {
		for _, h := range m.keyHints() {
			hints = append(hints, ui.KeyBinding(h.Key, h.Desc, h.Color))
		}
		// Help/tab/fullscreen hints, except while a text input takes the keys.
		if !m.isTextInputActive() {
			hints = append(hints, ui.KeyBinding("?", "Help", ui.ColorDarkGray))
			if !isBusy(m.screen) {
				hints = append(hints, ui.KeyBinding("[ ]", "Tabs", ui.ColorDarkGray))
			}
			hints = append(hints, ui.KeyBinding("F", "Fullscreen", ui.ColorDarkGray))
		}

		// Flag config problems from anywhere, pointing at the screen that
		// explains them. Without this the only symptom of a broken config is an
		// empty list.
		if n := len(m.configDiagnostics); n > 0 && m.screen != ScreenSettings {
			color := ui.ColorYellow
			if config.HasErrors(m.configDiagnostics) {
				color = ui.ColorRed
			}
			hints = append(hints, ui.KeyBinding("o", fmt.Sprintf("⚠ %d config issue(s)", n), color))
		}

		if m.copyFeedback != "" {
			feedbackStyle := ui.GreenBold
			if strings.HasPrefix(m.copyFeedback, "✗") {
				feedbackStyle = ui.RedBold
			}
			hints = append(hints, feedbackStyle.Render(m.copyFeedback))
		}
	}
	if len(hints) == 0 {
		return ""
	}

	// Padded to the frame, because View centres each line on its own and a
	// short hint line would otherwise drift off the rule's left edge.
	lines := []string{rule}
	for _, l := range packHints(hints, width-1) {
		lines = append(lines, visPad(" "+l, width))
	}
	return strings.Join(lines, "\n")
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
