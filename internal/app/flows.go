package app

import (
	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/ui"

	"github.com/charmbracelet/lipgloss"
)

// How the configured release steps are presented.
//
// Every screen that used to name "dev", "staging" and "main" now asks the
// config which steps exist and renders one row or one column per step. The
// palettes below are what makes that possible without a per-step colour
// setting: they are indexed, and they wrap, so a three- or four-step chain
// still gets distinct colours without anyone configuring them.

// flowChainColors colour a *branch* by its position in the chain, so a branch
// keeps one colour wherever it appears. The default two-step chain lands on
// dev green, staging yellow, main red — which is what it always looked like.
var flowChainColors = []lipgloss.TerminalColor{
	ui.ColorGreen, ui.ColorYellow, ui.ColorRed,
	ui.ColorMagenta, ui.ColorCyan, ui.ColorBlue,
}

// flowColumnColors identify a whole *step* on the open-PRs screen. They are a
// separate sequence because a column is a step rather than a branch: the two
// default columns read green and red, not green and yellow.
var flowColumnColors = []lipgloss.TerminalColor{
	ui.ColorGreen, ui.ColorRed, ui.ColorYellow,
	ui.ColorMagenta, ui.ColorCyan, ui.ColorBlue,
}

// flowColumnMarkers pair with flowColumnColors for terminals where the box
// border colour alone is hard to tell apart.
var flowColumnMarkers = []string{"●", "■", "▲", "◆", "○", "□"}

func flowChainColor(i int) lipgloss.TerminalColor  { return flowChainColors[i%len(flowChainColors)] }
func flowColumnColor(i int) lipgloss.TerminalColor { return flowColumnColors[i%len(flowColumnColors)] }
func flowColumnMarker(i int) string                { return flowColumnMarkers[i%len(flowColumnMarkers)] }

// flows returns the configured release steps, in order: none when trunk-based,
// so nothing compares, fetches or colours by an unused chain.
func (m Model) flows() []models.Flow {
	if m.config == nil || m.trunkBased() {
		return nil
	}
	return m.config.FlowEntries()
}

func (m Model) trunkBased() bool { return m.config != nil && m.config.TrunkBased() }

// visibleTabs are the tabs shown, in order. Single, Batch and Release PRs
// release along the chain, which trunk-based work does not have.
func (m Model) visibleTabs() []int {
	if m.trunkBased() {
		return []int{ui.TabAllPRs, ui.TabActions}
	}
	return ui.AllTabs
}

// startRow is a tab's row on the Start card, which lists the visible tabs.
func (m Model) startRow(tab int) (int, bool) {
	for i, t := range m.visibleTabs() {
		if t == tab {
			return i, true
		}
	}
	return 0, false
}

// chainBranches lists every branch the release chain touches, in order: each
// step's head, then the final step's base, with the @default token resolved to
// the current repo's default branch. For the default two-step chain that is
// dev, staging and main — which is exactly the fixed list the pull screen and
// the branch colours used to carry as literals.
func (m Model) chainBranches() []string {
	flows := m.flows()
	if len(flows) == 0 {
		return nil
	}

	seen := map[string]bool{}
	var out []string
	add := func(branch string) {
		if branch == "" || seen[branch] {
			return
		}
		seen[branch] = true
		out = append(out, branch)
	}

	for _, f := range flows {
		add(f.HeadBranch())
	}
	add(flows[len(flows)-1].BaseBranch(m.mainBranch()))
	return out
}

// branchColor colours a branch by its position in the chain, so the release
// branches stay visually distinct whatever they are called. A branch outside
// the chain — a feature branch on an Actions run, say — stays neutral.
//
// This replaces a switch in internal/ui that matched the literal strings "dev",
// "staging", "main" and "master", and coloured everything else white.
func (m Model) branchColor(branch string) lipgloss.TerminalColor {
	for i, b := range m.chainBranches() {
		if b == branch {
			return flowChainColor(i)
		}
	}
	return ui.ColorWhite
}
