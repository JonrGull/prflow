package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The home screen: a dashboard of the release, with the old menu as its
// Start panel. Its data comes from home.go.
//
// It replaced a menu beside a panel of static text, which described what each
// entry did and said nothing about the repos themselves.

// startItems are the Start panel's entries, in menuIndex order.
var startItems = []struct {
	name, desc string
	color      lipgloss.TerminalColor
}{
	{"Single repo", "PR for the repo you're in", ui.ColorCyan},
	{"Batch", "PRs across repos", ui.ColorMagenta},
	{"Release PRs", "review and merge", ui.ColorYellow},
	{"All open PRs", "every PR, every repo", ui.ColorBlue},
	{"Actions", "workflow runs", ui.ColorOrange},
}

func (m Model) handleMainMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	last := len(startItems) - 1
	switch msg.String() {
	case "q":
		m.shouldQuit = true
		return m, tea.Quit
	case "up", "k":
		if m.menuIndex > 0 {
			m.menuIndex--
		} else {
			m.menuIndex = last // Wrap to bottom
		}
	case "down", "j":
		if m.menuIndex < last {
			m.menuIndex++
		} else {
			m.menuIndex = 0 // Wrap to top
		}
	case "enter", "1", "2", "3", "4", "5":
		if idx, ok := numKeyIndex(msg.String(), len(startItems)); ok {
			m.menuIndex = idx
		}
		return m.selectMainMenuItem()
	case "r":
		// An explicit refresh should also pick up repos added on disk.
		invalidateRepoCache()
		cmd := m.startHomeFetch()
		return m, cmd
	case "a":
		m.menuIndex = 4
		return m.selectMainMenuItem()
	case "u":
		// Manual update check. Blocked under --dry-run, which promises no
		// network and no changes: a local build reports Version "dev", which
		// CheckForUpdate treats as older than everything, so this prompt always
		// appears — and accepting it renames a download over the binary being
		// tested.
		if m.dryRun {
			m.copyFeedback = "Update check skipped (dry run)"
			return m, nil
		}
		if m.updateCheckInProgress {
			return m, nil
		}
		m.updateCheckInProgress = true
		return m, checkUpdateCmd(m.version, m.config.Update.Repo)
	case "c":
		// Open config in editor
		return m, openConfigCmd()
	case "h":
		m.screen = ScreenSessionHistory
		m.historyIndex = 0
	case "p":
		// Pull all repos
		m.screen = ScreenPullBranchSelect
		m.menuIndex = 0
	case "o":
		// Settings
		m.screen = ScreenSettings
		m.menuIndex = 0
	}
	return m, nil
}

func (m Model) selectMainMenuItem() (tea.Model, tea.Cmd) {
	// Check for auth error before any GitHub operation. The check runs again
	// each time, so logging in from another terminal takes effect without a
	// restart.
	if m.authError != nil {
		m.screen = ScreenError
		m.errorMessage = m.authError.Error()
		return m, authCheckCmd()
	}

	// Sync activeTab with menu selection (for tabs 0-4)
	if m.menuIndex >= 0 && m.menuIndex <= 4 {
		m.activeTab = m.menuIndex
	}

	switch m.menuIndex {
	case 0: // Single Repo
		mode := ModeSingle
		m.mode = &mode
		m.screen = ScreenLoading
		m.loadingMessage = "Detecting repository..."
		return m, loadCurrentRepoCmd()
	case 1: // Batch Mode
		mode := ModeBatch
		m.mode = &mode
		m.screen = ScreenPrTypeSelect
		m.menuIndex = 0
	case 2: // View Release PRs
		return m.navigateToMergePRs()
	case 3: // All Open PRs
		m.screen = ScreenLoading
		m.loadingMessage = "Fetching all open PRs..."
		return m, fetchAllOpenPRsCmd(m.config, m.dryRun)
	case 4: // GitHub Actions
		m.actions.loading = true
		m.screen = ScreenLoading
		m.loadingMessage = "Fetching workflow runs..."
		return m, fetchActionsRunsCmd(m.config, m.dryRun, nil)
	}
	return m, nil
}

// --- Rendering ---------------------------------------------------------------

// Card heights are fixed so cards side by side line up. Each is three rows of
// title and padding, the content rows, and a padding row.
const (
	pipelineCardHeight = 6  // two rows: the chain and its captions
	middleCardHeight   = 8  // four rows
	lowerCardHeight    = 10 // six rows
	cardGap            = 2  // columns between side-by-side cards
	twoColumnMinWidth  = 106
)

// renderHome lays the dashboard out for the space it is given. Two columns
// need twoColumnMinWidth; below that the cards stack. When the full layout is
// too tall the open-PR and Actions cards go, then the pipeline: Start stays.
func (m Model) renderHome(availableHeight int) string {
	w := m.frameWidth()
	if w < twoColumnMinWidth {
		startHeight := len(startItems) + 4
		start := m.startCard(w, startHeight)
		full := []string{m.pipelineCard(w), m.attentionCard(w, middleCardHeight), start}
		if cardsHeight(full) <= availableHeight {
			return strings.Join(full, "\n\n")
		}
		// 80x24 leaves room for Start and a few rows more: spend them on
		// attention, which shrinks to fit.
		if rest := availableHeight - startHeight - 1; rest >= 4 {
			return fitCards(availableHeight, m.attentionCard(w, min(rest, middleCardHeight)), start)
		}
		return start
	}

	l := (w - cardGap) / 2
	r := w - cardGap - l
	// The full layout, then the same a row shorter per card row, which is what
	// a 30-row terminal has room for.
	for _, h := range [][2]int{{middleCardHeight, lowerCardHeight}, {middleCardHeight - 1, lowerCardHeight - 1}} {
		full := []string{
			m.pipelineCard(w),
			sideBySide(m.releasePRsCard(l, h[0]), m.attentionCard(r, h[0])),
			sideBySide(m.actionsCard(l, h[1]), m.startCard(r, h[1])),
		}
		if cardsHeight(full) <= availableHeight {
			return strings.Join(full, "\n\n")
		}
	}
	return fitCards(availableHeight,
		m.pipelineCard(w),
		sideBySide(m.attentionCard(l, lowerCardHeight), m.startCard(r, lowerCardHeight)))
}

// fitCards stacks rows of cards a blank line apart, dropping from the top
// until they fit. The last row is always kept.
func fitCards(height int, rows ...string) string {
	for len(rows) > 1 && cardsHeight(rows) > height {
		rows = rows[1:]
	}
	return strings.Join(rows, "\n\n")
}

func cardsHeight(rows []string) int {
	h := len(rows) - 1
	for _, r := range rows {
		h += lipgloss.Height(r)
	}
	return h
}

// sideBySide joins two cards of equal height with the gap between them.
func sideBySide(a, b string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, a, strings.Repeat(" ", cardGap), b)
}

// keyMeta is a card's corner hint for the key that opens more.
func keyMeta(key string) string {
	return ui.Dim.Render("press ") + ui.WhiteBold.Render(key)
}

// homeWaiting is what a card shows before there is any data: a spinner while
// the first fetch runs, or why it failed. ok is false once data has loaded.
func (m Model) homeWaiting() (line string, ok bool) {
	switch {
	case m.home.loaded:
		return "", false
	case m.home.loading:
		return ui.Yellow.Render(ui.Spinner(m.spinnerFrame)) + ui.Dim.Render(" Loading..."), true
	case m.home.err != nil:
		return ui.Red.Render("✗ ") + ui.Dim.Render(m.home.err.Error()), true
	}
	return ui.Dim.Render("Press r to load"), true
}

// homeProblem is the partial failure reported for one source, if any.
func (m Model) homeProblem(prefix string) (string, bool) {
	for _, p := range m.home.data.Problems {
		if strings.HasPrefix(p, prefix) {
			return ui.Red.Render("✗ ") + ui.Dim.Render(p), true
		}
	}
	return "", false
}

// homeMeta says how old the data is, or that a refresh is running or failed.
func (m Model) homeMeta() string {
	switch {
	case !m.home.loaded:
		return ""
	case m.home.loading:
		return ui.Yellow.Render(ui.Spinner(m.spinnerFrame)) + ui.Dim.Render(" refreshing")
	case m.home.err != nil:
		return ui.Red.Render("refresh failed") + ui.Dim.Render(" · r retry")
	}
	return ui.Dim.Render("updated "+relativeTime(m.home.fetchedAt)+" · ") + ui.WhiteBold.Render("r") + ui.Dim.Render(" refresh")
}

// pipelineCard draws the release chain, each link labelled with the commits
// waiting to cross it. Steps that do not join end to end are listed instead.
func (m Model) pipelineCard(width int) string {
	var lines []string
	if line, waiting := m.homeWaiting(); waiting {
		lines = []string{line}
	} else if p, bad := m.homeProblem("branch comparison"); bad {
		lines = []string{p}
	} else if len(m.flows()) == 0 {
		lines = []string{ui.Dim.Render("No release steps configured. o opens settings.")}
	} else if m.home.data.Repos == 0 {
		lines = []string{ui.Dim.Render("No repos with a GitHub remote found. o opens settings.")}
	} else if !sameFlows(m.home.data.Steps, m.flows()) {
		lines = []string{ui.Dim.Render("The release steps changed. r to refresh.")}
	} else {
		lines = m.pipelineLines(width - 4)
	}
	return ui.Card("Release pipeline", m.homeMeta(), lines, width, pipelineCardHeight)
}

func sameFlows(steps []homeStep, flows []models.Flow) bool {
	if len(steps) != len(flows) {
		return false
	}
	for i := range steps {
		if steps[i].Flow != flows[i] {
			return false
		}
	}
	return true
}

func (m Model) pipelineLines(inner int) []string {
	steps := m.home.data.Steps
	for i := 0; i+1 < len(steps); i++ {
		if steps[i].Flow.Base != steps[i+1].Flow.Head {
			return m.pipelineList(inner)
		}
	}

	// One node per step head and the last base. Not chainBranches, which
	// drops repeats: a chain that loops back would come up a node short.
	var nodes []string
	for _, st := range steps {
		nodes = append(nodes, st.Flow.HeadBranch())
	}
	nodes = append(nodes, steps[len(steps)-1].Flow.BaseBranch(m.mainBranch()))
	names := make([]string, len(nodes))
	namesWidth := 0
	for i, n := range nodes {
		names[i] = lipgloss.NewStyle().Foreground(flowChainColor(i)).Bold(true).Render("● " + n)
		namesWidth += lipgloss.Width(names[i])
	}
	linkWidth := max((inner-namesWidth)/len(steps), 5)

	line := ""
	starts := make([]int, len(nodes))
	for i, s := range steps {
		starts[i] = lipgloss.Width(line)
		line += names[i] + m.pipelineLink(s, linkWidth)
	}
	starts[len(nodes)-1] = lipgloss.Width(line)
	line += names[len(nodes)-1]

	captions := placeCaptions(starts, m.pipelineCaptions(false), inner)
	if lipgloss.Width(captions) > inner {
		captions = placeCaptions(starts, m.pipelineCaptions(true), inner)
	}
	return []string{line, ui.Dim.Render(captions)}
}

// placeCaptions puts each caption under its node, two columns in so it sits
// under the name rather than the dot, moved left to end inside width and
// right to clear the one before.
func placeCaptions(starts []int, captions []string, width int) string {
	out := ""
	for i, c := range captions {
		at := min(starts[i]+2, width-lipgloss.Width(c))
		if out != "" {
			at = max(at, lipgloss.Width(out)+2)
		}
		out += strings.Repeat(" ", max(at-lipgloss.Width(out), 0)) + c
	}
	return out
}

// pipelineCaptions are the lines under each branch: how many repos are ahead
// of the next branch, and for the last, how many PRs could merge now.
func (m Model) pipelineCaptions(short bool) []string {
	var out []string
	ready := 0
	for _, s := range m.home.data.Steps {
		ready += len(s.Ready)
		switch {
		case s.Compared == 0:
			out = append(out, "not compared")
		case short:
			out = append(out, fmt.Sprintf("%d/%d ahead", s.ReposAhead, s.Compared))
		default:
			out = append(out, fmt.Sprintf("%d of %d repos ahead", s.ReposAhead, s.Compared))
		}
	}
	if short {
		return append(out, fmt.Sprintf("%d ready", ready))
	}
	return append(out, fmt.Sprintf("%d ready to merge", ready))
}

// pipelineList is the pipeline for steps that do not form one chain.
func (m Model) pipelineList(inner int) []string {
	steps := m.home.data.Steps
	shown := steps
	if len(steps) > 2 {
		shown = steps[:1]
	}
	captions := m.pipelineCaptions(false)
	var lines []string
	for i, s := range shown {
		lines = append(lines, truncateString(ui.WhiteBold.Render(s.Flow.Display(m.mainBranch()))+"  "+
			ui.Dim.Render(aheadText(s)+" · "+captions[i]), inner))
	}
	if len(shown) < len(steps) {
		lines = append(lines, ui.Dim.Render(fmt.Sprintf("+%d more steps", len(steps)-len(shown))))
	}
	return lines
}

// pipelineLink is the arrow between two branches, labelled with the commits
// that are on the first and not the second.
func (m Model) pipelineLink(s homeStep, width int) string {
	label := " " + aheadText(s) + " "
	if lipgloss.Width(label)+4 > width {
		label = " " + strings.Fields(aheadText(s))[0] + " "
	}
	rule := lipgloss.NewStyle().Foreground(ui.ColorBorder)
	room := max(width-lipgloss.Width(label)-3, 2) // the spaces either end and the arrowhead
	left := room / 2
	return rule.Render(" "+strings.Repeat("━", left)) + ui.White.Render(label) +
		rule.Render(strings.Repeat("━", room-left)+"▶ ")
}

func aheadText(s homeStep) string {
	switch {
	case s.Compared == 0:
		return "?"
	case s.Ahead == 0:
		return "in sync"
	}
	return fmt.Sprintf("%d commit%s", s.Ahead, pluralS(s.Ahead))
}

// releasePRsCard shows each step's open release PRs: how many there are, how
// many are green, and which could be merged now.
func (m Model) releasePRsCard(width, height int) string {
	inner := width - 4
	rows := ui.CardRows(height)
	var lines []string
	if line, waiting := m.homeWaiting(); waiting {
		lines = []string{line}
	} else if p, bad := m.homeProblem("open PRs"); bad {
		lines = []string{p}
	} else {
		steps := m.home.data.Steps
		if len(steps) > rows-1 {
			steps = steps[:max(rows-1, 0)] // the Ready line needs a row
		}
		nameW := 0
		for _, s := range steps {
			nameW = max(nameW, lipgloss.Width(s.Flow.Display(m.mainBranch())))
		}
		nameW = min(nameW, inner/3)
		barW := max(min(inner-nameW-24, 14), 4)
		var ready []string
		for _, s := range steps {
			name := visPad(ui.White.Render(truncateString(s.Flow.Display(m.mainBranch()), nameW)), nameW+2)
			if s.Open == 0 {
				lines = append(lines, name+ui.Dim.Render("no open PRs"))
				continue
			}
			counts := ui.Green.Render(fmt.Sprintf("✓%d", s.Green))
			if s.Failing > 0 {
				counts += " " + ui.Red.Render(fmt.Sprintf("✗%d", s.Failing))
			}
			if s.Pending > 0 {
				counts += " " + ui.Yellow.Render(fmt.Sprintf("◐%d", s.Pending))
			}
			lines = append(lines, name+meter(s.Green, s.Open, barW)+"  "+
				visPad(ui.White.Render(fmt.Sprintf("%d open", s.Open)), 9)+counts)
			ready = append(ready, s.Ready...)
		}
		if len(ready) > 0 {
			if len(lines)+2 <= rows {
				lines = append(lines, "")
			}
			label := "Ready to merge: "
			lines = append(lines, ui.Dim.Render(label)+ui.Green.Render(joinFit(ready, inner-len(label))))
		}
	}
	return ui.Card("Open release PRs", keyMeta("3"), lines, width, height)
}

// meter is a bar filled done/total of width cells.
func meter(done, total, width int) string {
	n := 0
	if total > 0 {
		n = min(done*width/total, width)
	}
	return ui.Green.Render(strings.Repeat("█", n)) +
		lipgloss.NewStyle().Foreground(ui.ColorBorder).Render(strings.Repeat("█", width-n))
}

// joinFit joins names with commas, ending in "+N" for those that do not fit.
func joinFit(names []string, width int) string {
	out := ""
	for i, n := range names {
		next := n
		if out != "" {
			next = out + ", " + n
		}
		more := ""
		if rest := len(names) - i - 1; rest > 0 {
			more = fmt.Sprintf(" +%d", rest)
		}
		if out != "" && lipgloss.Width(next+more) > width {
			return fmt.Sprintf("%s +%d", out, len(names)-i)
		}
		out = next
	}
	return out
}

// attentionCard lists what needs a person, most blocking first.
func (m Model) attentionCard(width, height int) string {
	inner := width - 4
	rows := ui.CardRows(height)
	items := m.home.data.Attention
	meta := ""
	var lines []string
	if line, waiting := m.homeWaiting(); waiting {
		lines = []string{line}
	} else if p, bad := m.homeProblem("open PRs"); bad {
		lines = []string{p}
	} else if len(items) == 0 {
		lines = []string{ui.Green.Render("✓ ") + ui.Dim.Render("Nothing needs you right now")}
	} else {
		meta = ui.Dim.Render(fmt.Sprintf("%d open", len(items)))
		// The most blocking come first, so a short card still shows what
		// matters; the meta has the full count.
		shown := items
		if len(items) > rows {
			shown = items[:rows]
			if rows >= 3 {
				shown = items[:rows-1] // room for "+N more"
			}
		}
		repoW, prW, stepW := 0, 0, 0
		for _, a := range shown {
			repoW = max(repoW, lipgloss.Width(a.Repo))
			prW = max(prW, len(prLabel(a.PR)))
			stepW = max(stepW, lipgloss.Width(a.Step))
		}
		repoW = min(repoW, inner/4)
		// The step is the first thing to go: the icon, repo and PR say most.
		detailW := inner - 2 - (repoW + 1) - (prW + 1) - 2 - stepW
		if detailW < 12 {
			stepW, detailW = 0, inner-2-(repoW+1)-(prW+1)
		}
		for _, a := range shown {
			icon, color := attentionIcon(a.Kind)
			line := lipgloss.NewStyle().Foreground(color).Render(icon) + " " +
				visPad(ui.CyanBold.Render(truncateString(a.Repo, repoW)), repoW+1) +
				visPad(ui.Dim.Render(prLabel(a.PR)), prW+1) +
				visPad(ui.White.Render(truncateString(a.Detail, detailW)), detailW)
			if stepW > 0 {
				line += "  " + ui.Dim.Render(truncateString(a.Step, stepW))
			}
			lines = append(lines, line)
		}
		if len(shown) < len(items) {
			lines = append(lines, ui.Dim.Render(fmt.Sprintf("+%d more", len(items)-len(shown))))
		}
	}
	return ui.Card("Needs attention", meta, lines, width, height)
}

func prLabel(n uint64) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("#%d", n)
}

func attentionIcon(k attentionKind) (string, lipgloss.TerminalColor) {
	switch k {
	case attnCIFailing:
		return "✗", ui.ColorRed
	case attnConflict:
		return "⚠", ui.ColorOrange
	case attnChangesRequested:
		return "●", ui.ColorYellow
	}
	return "○", ui.ColorDarkGray
}

// actionsCard lists the latest runs across repos and a day of CI history.
func (m Model) actionsCard(width, height int) string {
	inner := width - 4
	rows := ui.CardRows(height)
	meta := keyMeta("5")
	var lines []string
	if line, waiting := m.homeWaiting(); waiting {
		lines = []string{line}
	} else if len(m.home.data.Runs) == 0 {
		lines = []string{ui.Dim.Render("No runs in the last 24 hours")}
		if p, bad := m.homeProblem("Actions"); bad {
			lines = []string{p}
		}
	} else {
		if n := m.home.data.RunErrors; n > 0 {
			meta = ui.Red.Render("✗ ") + ui.Dim.Render(fmt.Sprintf("%d repo%s unread", n, pluralS(n)))
		}
		// The CI history is the last row, a blank row above it when there is
		// room for four runs as well.
		spare := rows - 1
		if spare >= 5 {
			spare--
		}
		runs := m.home.data.Runs
		if len(runs) > min(spare, 4) {
			runs = runs[:max(min(spare, 4), 0)]
		}
		nameW := max((inner-2-5)*3/10, 6)
		repoW := max((inner-2-5)*4/10, 6)
		branchW := inner - 2 - 5 - nameW - repoW - 2
		for _, e := range runs {
			icon, color := runIcon(e.Run)
			lines = append(lines, lipgloss.NewStyle().Foreground(color).Render(icon)+" "+
				visPad(ui.White.Render(truncateString(e.Run.WorkflowName, nameW)), nameW+1)+
				visPad(ui.Cyan.Render(truncateString(e.Repo.ShortName(), repoW)), repoW+1)+
				visPad(lipgloss.NewStyle().Foreground(m.branchColor(e.Run.HeadBranch)).Render(truncateString(e.Run.HeadBranch, branchW)), branchW)+
				visRightAlign(ui.Dim.Render(shortAge(e.Run.UpdatedAt)), 5))
		}
		if len(lines)+2 <= rows {
			lines = append(lines, "")
		}
		lines = append(lines, ui.Dim.Render("CI, last 24h  ")+sparkline(m.home.data.CI)+"  "+ciShare(m.home.data.CIGreen))
	}
	return ui.Card("Recent actions", meta, lines, width, height)
}

// shortAge is relativeTime for a narrow column: "now", "4m", "2h", "3d".
func shortAge(t time.Time) string {
	if age := relativeTime(t); age != "just now" {
		return strings.TrimSuffix(age, " ago")
	}
	return "now"
}

func runIcon(r models.WorkflowRun) (string, lipgloss.TerminalColor) {
	switch {
	case r.Status != "completed":
		return "◐", ui.ColorYellow
	case runFailed(r.Conclusion):
		return "✗", ui.ColorRed
	case strings.EqualFold(r.Conclusion, "success"):
		return "✓", ui.ColorGreen
	}
	return "○", ui.ColorDarkGray
}

// sparkline draws a bar per hour, as tall as that hour was busy, red where a
// run failed.
func sparkline(hours [24]ciHour) string {
	blocks := []rune("▁▂▃▄▅▆▇█")
	peak := 1
	for _, h := range hours {
		peak = max(peak, h.Runs)
	}
	var b strings.Builder
	for _, h := range hours {
		bar := string(blocks[h.Runs*(len(blocks)-1)/peak])
		switch {
		case h.Runs == 0:
			b.WriteString(lipgloss.NewStyle().Foreground(ui.ColorBorder).Render(bar))
		case h.Failed > 0:
			b.WriteString(ui.Red.Render(bar))
		default:
			b.WriteString(ui.Green.Render(bar))
		}
	}
	return b.String()
}

func ciShare(pct int) string {
	text := fmt.Sprintf("%d%% green", pct)
	switch {
	case pct < 0:
		return ui.Dim.Render("no finished runs")
	case pct >= 90:
		return ui.Green.Render(text)
	case pct >= 70:
		return ui.Yellow.Render(text)
	}
	return ui.Red.Render(text)
}

// startCard is the old menu: where to go, the selected entry highlighted.
func (m Model) startCard(width, height int) string {
	inner := width - 4
	var lines []string
	for i, it := range startItems {
		bg := lipgloss.TerminalColor(ui.ColorPanel)
		if i == m.menuIndex {
			bg = ui.ColorSelection
		}
		on := lipgloss.NewStyle().Background(bg)
		row := on.Foreground(ui.ColorDarkGray).Render(fmt.Sprintf(" %d  ", i+1)) +
			on.Foreground(it.color).Bold(true).Render(fmt.Sprintf("%-14s", it.name)) +
			on.Foreground(ui.ColorDarkGray).Render(it.desc)
		if i == m.menuIndex {
			row = ui.OnBackground(row+strings.Repeat(" ", max(inner-lipgloss.Width(row), 0)), ui.ColorSelection)
		}
		lines = append(lines, row)
	}
	return ui.Card("Start", "", lines, width, height)
}
