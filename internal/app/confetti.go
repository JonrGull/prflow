package app

import (
	"math"
	"math/rand"
	"strings"

	"github.com/JonrGull/prflow/internal/ui"

	"github.com/charmbracelet/lipgloss"
)

// The confetti animation, shared by the single-PR and batch completion screens.

// ConfettiParticle represents a single confetti particle
type ConfettiParticle struct {
	X, Y   float64
	VX, VY float64
	Char   rune
	Color  lipgloss.Color
}

// spawnConfetti creates confetti particles for celebration
func (m *Model) spawnConfetti() {
	colors := []lipgloss.Color{
		ui.ColorCyan,
		ui.ColorMagenta,
		ui.ColorYellow,
		ui.ColorGreen,
		ui.ColorRed,
		ui.ColorWhite,
	}
	chars := []rune{'*', '•', '✦', '✧', '◆', '◇', '▪', '♦', '★', '☆'}

	m.confetti = nil
	for i := 0; i < 40; i++ {
		angle := (float64(i) / 40.0) * math.Pi * 2.0
		speed := 2.0 + float64(i%5)*0.5
		m.confetti = append(m.confetti, ConfettiParticle{
			X:     40.0, // center-ish
			Y:     5.0,
			VX:    math.Cos(angle) * speed,
			VY:    math.Sin(angle)*speed - 2.0, // bias upward initially
			Char:  chars[rand.Intn(len(chars))],
			Color: colors[rand.Intn(len(colors))],
		})
	}
	m.typewriterPos = 0
}

func (m Model) renderConfetti() string {
	if len(m.confetti) == 0 {
		return ""
	}

	// Create a grid for confetti
	width := 80
	height := 5
	grid := make([][]rune, height)
	colors := make([][]lipgloss.Color, height)
	for i := range grid {
		grid[i] = make([]rune, width)
		colors[i] = make([]lipgloss.Color, width)
		for j := range grid[i] {
			grid[i][j] = ' '
		}
	}

	// Place particles in grid
	for _, p := range m.confetti {
		x := int(p.X)
		y := int(p.Y) - 5 // offset for display area
		if x >= 0 && x < width && y >= 0 && y < height {
			grid[y][x] = p.Char
			colors[y][x] = p.Color
		}
	}

	// Render grid
	var lines []string
	for y := 0; y < height; y++ {
		var line strings.Builder
		line.WriteString("   ")
		for x := 0; x < width; x++ {
			if grid[y][x] != ' ' {
				style := lipgloss.NewStyle().Foreground(colors[y][x])
				line.WriteString(style.Render(string(grid[y][x])))
			} else {
				line.WriteRune(' ')
			}
		}
		lines = append(lines, line.String())
	}

	return strings.Join(lines, "\n")
}
