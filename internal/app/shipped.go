package app

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/git"
	"github.com/JonrGull/prflow/internal/github"
	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The Shipped tab: every repo's GitHub releases, newest first across the
// repos, with what the highlighted one shipped since the release before it:
// its PRs, the commits that came without one, and the tickets they name.

// shippedPerRepo is how many releases each repo lists. One more is fetched so
// the oldest listed has a release to compare with.
const shippedPerRepo = 10

// shippedState is everything this screen owns.
type shippedState struct {
	entries []shippedEntry // newest first
	problem string         // repos whose releases could not be read
	index   int
	scroll  int
	loading bool
	diffs   map[string]shippedDiff // by shippedEntry.key
}

type shippedEntry struct {
	Repo    models.RepoInfo
	NWO     string
	Release github.Release
	Prev    *github.Release // the release before it; nil for the oldest fetched
}

// key names a release by its commit too: a tag moved to another commit is a
// different comparison.
func (e shippedEntry) key() string { return e.NWO + " " + e.Release.Tag + " " + e.Release.SHA }

type shippedDiff struct {
	loading bool
	err     error
	diff    *github.ReleaseDiff
}

type shippedFetchedResult struct {
	entries []shippedEntry
	problem string
	err     error
}

func (shippedFetchedResult) flowResult() {}

// shippedPreviewMsg fires once the cursor has rested on a release, as
// actionsPreviewMsg does for a run.
type shippedPreviewMsg struct{ key string }

func (shippedPreviewMsg) flowResult() {}

// shippedDiffResult is deliberately not a flowResult: it only fills the cache
// under its key, like a run's jobs, and a comparison never changes.
type shippedDiffResult struct {
	key  string
	diff github.ReleaseDiff
	err  error
}

func fetchShippedCmd(cfg *config.Config, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		if dryRun {
			return dryRunShipped()
		}
		repos, err := discoverRepos(cfg)
		if err != nil {
			return shippedFetchedResult{err: err}
		}
		withNWO := githubRepos(repos)
		var nwos []string
		for _, r := range withNWO {
			nwos = append(nwos, r.NWO)
		}
		byRepo, err := github.Releases(nwos, shippedPerRepo+1)
		if err != nil && len(byRepo) == 0 {
			return shippedFetchedResult{err: err}
		}
		res := shippedFetchedResult{entries: shippedEntries(withNWO, byRepo)}
		if err != nil {
			res.problem = "Some repos could not be read: " + firstLine(err.Error())
		}
		return res
	}
}

// shippedEntries pairs each release with the one before it in its repo and
// lists them all newest first.
func shippedEntries(repos []repoNWO, byRepo map[string][]github.Release) []shippedEntry {
	var out []shippedEntry
	for _, r := range repos {
		rels := byRepo[r.NWO]
		for i := 0; i < min(len(rels), shippedPerRepo); i++ {
			e := shippedEntry{Repo: r.Repo, NWO: r.NWO, Release: rels[i]}
			if i+1 < len(rels) {
				prev := rels[i+1]
				e.Prev = &prev
			}
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Release.PublishedAt.After(out[j].Release.PublishedAt) })
	return out
}

func (m Model) highlightedRelease() (shippedEntry, bool) {
	if m.shipped.index < 0 || m.shipped.index >= len(m.shipped.entries) {
		return shippedEntry{}, false
	}
	return m.shipped.entries[m.shipped.index], true
}

// previewRelease schedules the highlighted release's comparison, unless it is
// already there or on its way.
func (m Model) previewRelease() tea.Cmd {
	e, ok := m.highlightedRelease()
	if !ok || e.Prev == nil {
		return nil
	}
	if d := m.shipped.diffs[e.key()]; d.loading || d.diff != nil {
		return nil
	}
	key := e.key()
	return tea.Tick(actionsPreviewDelay, func(time.Time) tea.Msg { return shippedPreviewMsg{key: key} })
}

func (m Model) handleShippedPreview(msg shippedPreviewMsg) (tea.Model, tea.Cmd) {
	e, ok := m.highlightedRelease()
	if !ok || e.key() != msg.key {
		return m, nil // the cursor has moved on
	}
	cmd := m.fetchReleaseDiff(e)
	return m, cmd
}

// fetchReleaseDiff asks for a release's comparison once. It changes m, so call
// it as its own statement.
func (m *Model) fetchReleaseDiff(e shippedEntry) tea.Cmd {
	if e.Prev == nil {
		return nil
	}
	if d := m.shipped.diffs[e.key()]; d.loading || d.diff != nil {
		return nil
	}
	if m.shipped.diffs == nil {
		m.shipped.diffs = map[string]shippedDiff{}
	}
	m.shipped.diffs[e.key()] = shippedDiff{loading: true}
	key, nwo, prev, tag, dry := e.key(), e.NWO, e.Prev.Tag, e.Release.Tag, m.dryRun
	return func() tea.Msg {
		if dry {
			return shippedDiffResult{key: key, diff: dryRunReleaseDiff(tag)}
		}
		d, err := github.CompareReleases(nwo, prev, tag)
		return shippedDiffResult{key: key, diff: d, err: err}
	}
}

func (m Model) handleShippedDiff(msg shippedDiffResult) (tea.Model, tea.Cmd) {
	if m.shipped.diffs == nil {
		m.shipped.diffs = map[string]shippedDiff{}
	}
	if msg.err != nil {
		m.shipped.diffs[msg.key] = shippedDiff{err: msg.err}
		return m, nil
	}
	d := msg.diff
	m.shipped.diffs[msg.key] = shippedDiff{diff: &d}
	return m, nil
}

func (m Model) handleShippedFetched(msg shippedFetchedResult) (tea.Model, tea.Cmd) {
	m.shipped.loading = false
	if msg.err != nil {
		if m.screen == ScreenShipped || m.screen == ScreenLoading {
			m.errorMessage = msg.err.Error()
			m.screen = ScreenError
		}
		return m, nil
	}
	prev, hadPrev := m.highlightedRelease()
	m.shipped.entries, m.shipped.problem = msg.entries, msg.problem
	m.shipped.index = min(m.shipped.index, max(len(m.shipped.entries)-1, 0))
	for i, e := range m.shipped.entries {
		if hadPrev && e.key() == prev.key() {
			m.shipped.index = i
			break
		}
	}
	m.screen = ScreenShipped
	m.keepShippedCursorVisible()
	if e, ok := m.highlightedRelease(); ok {
		cmd := m.fetchReleaseDiff(e)
		return m, cmd
	}
	return m, nil
}

// shippedListRows is how many releases the list shows, from the height the
// renderer is given.
func (m Model) shippedListRows() int {
	return actionsPanelHeight(m.unboxedHeight()) - 1
}

func (m *Model) keepShippedCursorVisible() {
	m.shipped.scroll = actionsScroll(m.shipped.index, m.shipped.scroll, m.shippedListRows(), len(m.shipped.entries))
}

func (m Model) handleShippedKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.shipped.entries)
	page := max(m.shippedListRows()-1, 1)
	switch msg.String() {
	case "up", "k":
		navigateColumnIndex(&m.shipped.index, n, true)
	case "down", "j":
		navigateColumnIndex(&m.shipped.index, n, false)
	case "pgup":
		m.shipped.index = max(m.shipped.index-page, 0)
	case "pgdown":
		m.shipped.index = max(min(m.shipped.index+page, n-1), 0)
	case "home", "g":
		m.shipped.index = 0
	case "end", "G":
		m.shipped.index = max(n-1, 0)
	case "o":
		if e, ok := m.highlightedRelease(); ok && e.Release.URL != "" {
			m.openInBrowser(e.Release.URL)
		}
		return m, nil
	case "c":
		m.copyShipped()
		return m, nil
	case "r":
		if m.shipped.loading {
			return m, nil
		}
		invalidateRepoCache()
		m.shipped.loading = true
		return m, fetchShippedCmd(m.config, m.dryRun)
	case "esc":
		return m.navigateToTab(ui.HomeTab)
	case "q":
		m.shouldQuit = true
		return m, tea.Quit
	default:
		return m, nil
	}
	m.keepShippedCursorVisible()
	return m, m.previewRelease()
}

// shippedSummary is a comparison's commits grouped by the PR that brought
// them in.
type shippedSummary struct {
	prs     []github.ShippedPR     // newest first, each once
	loose   []github.ShippedCommit // commits with no PR, newest first
	tickets []string
}

// summarize groups commits by PR, newest first, leaving out release PRs between
// the chain's branches, whose own commits are listed with their own PRs.
func summarize(commits []github.ShippedCommit, releaseHeads map[string]bool, tickets *regexp.Regexp) shippedSummary {
	var s shippedSummary
	seen := map[uint64]bool{}
	found := map[string]bool{}
	note := func(text string) {
		for _, t := range git.ExtractTickets(text, tickets) {
			found[t] = true
		}
	}
	for i := len(commits) - 1; i >= 0; i-- {
		c := commits[i]
		note(c.Headline)
		switch {
		case c.PR == nil:
			s.loose = append(s.loose, c)
		case releaseHeads[c.PR.HeadBranch] || seen[c.PR.Number]:
		default:
			seen[c.PR.Number] = true
			s.prs = append(s.prs, *c.PR)
			note(c.PR.Title)
		}
	}
	for t := range found {
		s.tickets = append(s.tickets, t)
	}
	sort.Strings(s.tickets)
	return s
}

// releaseHeads are the chain's head branches: a PR from one is a release PR.
func (m Model) releaseHeads() map[string]bool {
	heads := map[string]bool{}
	for _, f := range m.flows() {
		heads[f.HeadBranch()] = true
	}
	return heads
}

// copyShipped copies the highlighted release's PRs and tickets as Markdown,
// for release notes or a channel.
func (m *Model) copyShipped() {
	e, ok := m.highlightedRelease()
	d := m.shipped.diffs[e.key()]
	if !ok || d.diff == nil {
		m.copyFeedback = "✗ Nothing to copy until the comparison has loaded"
		return
	}
	m.copyWithFeedback(shippedMarkdown(e, summarize(d.diff.Shipped, m.releaseHeads(), m.config.TicketRegex())),
		fmt.Sprintf("Copied %s %s", e.Repo.ShortName(), e.Release.Tag))
}

func shippedMarkdown(e shippedEntry, s shippedSummary) string {
	lines := []string{fmt.Sprintf("**%s %s**", e.Repo.ShortName(), e.Release.Tag)}
	if e.Prev != nil {
		lines[0] += " (since " + e.Prev.Tag + ")"
	}
	for _, pr := range s.prs {
		lines = append(lines, fmt.Sprintf("- [#%d](%s) %s", pr.Number, pr.URL, pr.Title))
	}
	for _, c := range s.loose {
		lines = append(lines, "- "+c.Headline)
	}
	if len(s.tickets) > 0 {
		lines = append(lines, "", "Tickets: "+strings.Join(s.tickets, ", "))
	}
	return strings.Join(lines, "\n")
}

// --- Rendering ---------------------------------------------------------------

func (m Model) renderShippedWithHeight(height int) string {
	width := m.frameWidth()
	if len(m.shipped.entries) == 0 && !m.shipped.loading {
		return m.renderShippedEmpty()
	}
	panel := actionsPanelHeight(height)
	listW := (width - 5) * 42 / 100
	detailW := width - 5 - listW
	list := ui.ColumnBox(strings.Join(m.renderShippedList(listW, panel-1), "\n"),
		fmt.Sprintf("RELEASES (%d)", len(m.shipped.entries)), ui.ColorGreen, true, listW, panel)
	title := "RELEASE"
	lines := []string{"", ui.Dim.Render(" No release selected")}
	if e, ok := m.highlightedRelease(); ok {
		title = e.Release.Tag
		lines = m.renderShippedDetail(e, detailW, panel-1)
	}
	detail := ui.ColumnBox(strings.Join(lines, "\n"), title, ui.ColorGreen, false, detailW, panel)
	return m.renderShippedTitleBar(width) + "\n" + ui.TwoColumns(list, detail, 1)
}

func (m Model) renderShippedEmpty() string {
	centered := lipgloss.NewStyle().Width(m.contentWidth()).Align(lipgloss.Center)
	lines := []string{
		"", "",
		centered.Render(ui.Dim.Render("None of the repos has published a GitHub release.")),
		centered.Render(ui.Dim.Render("Shipped lists each release with the PRs and tickets since the one before it.")),
	}
	if m.shipped.problem != "" {
		lines = append(lines, "", centered.Render(ui.Red.Render("✗ ")+ui.Dim.Render(m.shipped.problem)))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderShippedTitleBar(width int) string {
	inner := width - 4
	repos := map[string]bool{}
	for _, e := range m.shipped.entries {
		repos[e.NWO] = true
	}
	left := lipgloss.NewStyle().Foreground(ui.ColorGreen).Bold(true).Render("Shipped") +
		ui.Dim.Render(fmt.Sprintf(" · the last %d releases of %d repo%s", shippedPerRepo, len(repos), pluralS(len(repos))))
	right := ui.WhiteBold.Render("r") + ui.Dim.Render(" refresh")
	switch {
	case m.shipped.loading:
		right = ui.Yellow.Render(ui.Spinner(m.spinnerFrame)) + ui.Dim.Render(" refreshing")
	case m.shipped.problem != "":
		right = ui.Red.Render("✗ ") + ui.Dim.Render(m.shipped.problem)
	}
	line := left
	if gap := inner - lipgloss.Width(left) - lipgloss.Width(right); gap >= 2 {
		line += strings.Repeat(" ", gap) + right
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ui.ColorGreen).
		Padding(0, 1).Width(width - 2).Render(lipgloss.NewStyle().MaxWidth(inner).Render(line))
}

func (m Model) renderShippedList(width, rows int) []string {
	room := max(width-9, 10) // spaces around the columns, the mark and the age
	tagW := min(room*45/100, 16)
	repoW := room - tagW
	start := actionsScroll(m.shipped.index, m.shipped.scroll, rows, len(m.shipped.entries))
	var lines []string
	for i := start; i < min(start+rows, len(m.shipped.entries)); i++ {
		e := m.shipped.entries[i]
		mark := " "
		if d := m.shipped.diffs[e.key()].diff; d != nil && d.RemovedTotal > 0 {
			mark = ui.Red.Render("↩")
		}
		tag := ui.White
		if i == m.shipped.index {
			tag = ui.WhiteBold
		}
		row := " " + mark + " " + visPad(tag.Render(truncateString(e.Release.Tag, tagW)), tagW) + " " +
			visPad(lipgloss.NewStyle().Foreground(m.repoColor(e.Repo)).Render(truncateStart(e.Repo.ShortName(), repoW)), repoW) + " " +
			visRightAlign(ui.Dim.Render(shortAge(e.Release.PublishedAt)), 4)
		if i == m.shipped.index {
			row = ui.OnBackground(visPad(row, width), ui.ColorSelection)
		}
		lines = append(lines, row)
	}
	return lines
}

// renderShippedDetail is what the release shipped, in no more than rows lines:
// the PRs are cut before the tickets are, which are what QA works from.
func (m Model) renderShippedDetail(e shippedEntry, width, rows int) []string {
	published := "published " + relativeTime(e.Release.PublishedAt)
	if e.Release.Prerelease {
		published = "prerelease, " + published
	}
	head := []string{" " + lipgloss.NewStyle().Foreground(m.repoColor(e.Repo)).Render(e.Repo.ShortName()) + ui.Dim.Render(" · "+published)}
	if e.Prev == nil {
		return append(head, "", ui.Dim.Render(" The oldest release listed: there is none before it to compare with."))
	}
	gap := e.Release.PublishedAt.Sub(e.Prev.PublishedAt)
	head = append(head, ui.Dim.Render(" since "+e.Prev.Tag+", "+durationText(gap)+" earlier"), "")

	d := m.shipped.diffs[e.key()]
	switch {
	case d.err != nil:
		return append(head, " "+ui.Red.Render("✗ ")+ui.Dim.Render("Couldn't compare: "+firstLine(d.err.Error())))
	case d.diff == nil:
		return append(head, " "+ui.Yellow.Render(ui.Spinner(m.spinnerFrame))+ui.Dim.Render(" Comparing with "+e.Prev.Tag+"..."))
	case d.diff.Status == "IDENTICAL":
		return append(head, ui.Dim.Render(" The same commit as "+e.Prev.Tag+": nothing new shipped."))
	}

	heads, tickets := m.releaseHeads(), m.config.TicketRegex()
	var body []string
	if d.diff.ShippedTotal > 0 {
		body = append(body, m.shippedSection(summarize(d.diff.Shipped, heads, tickets), d.diff.ShippedTotal, len(d.diff.Shipped), false, width)...)
	}
	if d.diff.RemovedTotal > 0 {
		if len(body) > 0 {
			body = append(body, "")
		}
		body = append(body, " "+ui.Red.Render(fmt.Sprintf("↩ Rolls back %d commit%s that %s had", d.diff.RemovedTotal, pluralS(d.diff.RemovedTotal), e.Prev.Tag)))
		body = append(body, m.shippedSection(summarize(d.diff.Removed, heads, tickets), d.diff.RemovedTotal, len(d.diff.Removed), true, width)...)
	}

	var foot []string
	if s := summarize(d.diff.Shipped, heads, tickets); len(s.tickets) > 0 {
		foot = append(foot, " "+ui.Dim.Render("Tickets ")+ui.Cyan.Render(strings.Join(s.tickets, " ")))
	}
	if s := summarize(d.diff.Removed, heads, tickets); len(s.tickets) > 0 {
		foot = append(foot, " "+ui.Dim.Render("Taken out ")+ui.Red.Render(strings.Join(s.tickets, " ")))
	}
	if len(foot) > 0 {
		foot = append([]string{""}, foot...)
	}
	room := rows - len(head) - len(foot)
	if len(body) > room && room > 0 {
		body = append(body[:room-1], ui.Dim.Render(fmt.Sprintf(" +%d more · c copies it all", len(body)-room+1)))
	}
	return append(append(head, body...), foot...)
}

// shippedSection lists one side of a comparison: its PRs, then the commits
// with none. removed draws them as taken out.
func (m Model) shippedSection(s shippedSummary, total, listed int, removed bool, width int) []string {
	numStyle, text := ui.Blue.Bold(true), ui.White
	if removed {
		numStyle, text = ui.Red, ui.Dim
	}
	summary := fmt.Sprintf(" %d PR%s · %d commit%s", len(s.prs), pluralS(len(s.prs)), total, pluralS(total))
	if listed < total {
		summary += fmt.Sprintf(" (the newest %d listed)", listed)
	}
	lines := []string{ui.Dim.Render(summary)}
	for _, pr := range s.prs {
		num := fmt.Sprintf("#%d", pr.Number)
		lines = append(lines, " "+numStyle.Render(num)+" "+text.Render(truncateString(pr.Title, max(width-len(num)-5, 8))))
	}
	if len(s.loose) > 0 {
		lines = append(lines, ui.Dim.Render(fmt.Sprintf(" %d commit%s without a PR", len(s.loose), pluralS(len(s.loose)))))
		for _, c := range s.loose {
			lines = append(lines, "   "+ui.Dim.Render(truncateString(c.Headline, max(width-6, 8))))
		}
	}
	return lines
}

// durationText is a gap between releases in its largest unit.
func durationText(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", max(int(d.Minutes()), 1))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
