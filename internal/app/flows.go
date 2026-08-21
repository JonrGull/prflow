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
var flowChainColors = []lipgloss.Color{
	ui.ColorGreen, ui.ColorYellow, ui.ColorRed,
	ui.ColorMagenta, ui.ColorCyan, ui.ColorBlue,
}

// flowColumnColors identify a whole *step* on the open-PRs screen. They are a
// separate sequence because a column is a step rather than a branch: the two
// default columns read green and red, not green and yellow.
var flowColumnColors = []lipgloss.Color{
	ui.ColorGreen, ui.ColorRed, ui.ColorYellow,
	ui.ColorMagenta, ui.ColorCyan, ui.ColorBlue,
}

// flowColumnMarkers pair with flowColumnColors for terminals where the box
// border colour alone is hard to tell apart.
var flowColumnMarkers = []string{"🟢", "🔴", "🟡", "🟣", "🔵", "⚪"}

func flowChainColor(i int) lipgloss.Color  { return flowChainColors[i%len(flowChainColors)] }
func flowColumnColor(i int) lipgloss.Color { return flowColumnColors[i%len(flowColumnColors)] }
func flowColumnMarker(i int) string        { return flowColumnMarkers[i%len(flowColumnMarkers)] }

// flows returns the configured release steps, in order.
func (m Model) flows() []models.Flow {
	if m.config == nil {
		return nil
	}
	return m.config.FlowEntries()
}

// flowIndex reports which configured step a PR belongs to, or -1 if the config
// changed since the PR list was built. Flow is a plain comparable struct, so
// this is an equality check rather than an identity one.
func flowIndex(flows []models.Flow, flow models.Flow) int {
	for i, f := range flows {
		if f == flow {
			return i
		}
	}
	return -1
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
func (m Model) branchColor(branch string) lipgloss.Color {
	for i, b := range m.chainBranches() {
		if b == branch {
			return flowChainColor(i)
		}
	}
	return ui.ColorWhite
}
