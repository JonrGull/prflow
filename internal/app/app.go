package app

import (
	"errors"
	"math"
	"time"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/models"
	"github.com/JonrGull/prflow/internal/update"

	tea "github.com/charmbracelet/bubbletea"
)

// The Model, its construction, and the animation clock.
//
// Model holds every screen's state in one struct because that is what bubbletea
// wants — Update takes a Model and returns a Model. The fields are grouped by
// the screen that owns them, and each screen's behaviour lives in its own file.

// Model is the main application state
type Model struct {
	// Configuration
	config     *config.Config
	dryRun     bool
	testUpdate bool

	// Navigation
	screen     Screen
	menuIndex  int
	shouldQuit bool
	activeTab  int  // 0=Single, 1=Batch, 2=Release PRs, 3=All Open PRs, 4=Actions
	fullscreen bool // hides banner + tabs

	// Mode
	mode *AppMode

	// The PR being built. Shared rather than per-screen on purpose: the single
	// and batch paths both set flow and accumulate tickets, and QA tagging
	// reads those tickets after either one.
	repoInfo   *models.RepoInfo
	flow       *models.Flow
	commits    []models.CommitInfo
	tickets    []string
	prTitle    string
	prURL      string
	existingPR *models.GhPr // non-nil if the PR already exists, so it is updated

	// UI state
	confirmSelection int // 0=Yes, 1=No
	errorMessage     string
	loadingMessage   string
	spinnerFrame     int
	copyFeedback     string // Brief "Copied!" message, clears on next action
	authError        error  // Non-nil if gh auth check failed
	ghUser           string // Active GitHub username from gh auth

	// Update state
	version               string          // Current app version
	updateAvailable       *update.Release // Non-nil if update available
	updateSelection       int             // 0=Update now, 1=Skip, 2=Skip this version
	updateCheckInProgress bool            // True while checking for updates (manual)

	// Animation state
	confetti      []ConfettiParticle
	pulsePhase    float64 // 0.0 - 2*PI for sine wave
	typewriterPos int     // Characters revealed so far

	// Session history (survives reset)
	sessionPRs   []sessionPR
	historyIndex int

	// Config diagnostics, computed once at startup and after a settings change
	configDiagnostics []config.Diagnostic

	// showHelp overlays the keybinding reference over the current screen
	showHelp bool

	// Per-screen state. Each of these is defined in the file that owns the
	// screen, so a screen's state sits next to the code that reads it — and
	// reset() can clear a screen by assigning a zero value rather than by
	// listing its fields and missing some.
	batch    batchState
	merge    mergeState
	pull     pullState
	qa       qaState
	allPRs   allPRsState
	actions  actionsState
	settings settingsState
	list     listState
	firstRun firstRunState

	// Window size
	width  int
	height int

	// tickRunning tracks whether the animation tick chain is live, so Update
	// can restart it when something starts animating again.
	tickRunning bool
}

// errDryRun reports a config write skipped because this is a dry run. It is not
// a failure: the in-memory change stands and the screen goes on working, only
// the file was left alone. Callers that distinguish the two use errors.Is.
var errDryRun = errors.New("dry run — config not written")

// saveConfig persists the config and surfaces any failure.
//
// Every call site used to discard this error, so a read-only config directory,
// a full disk, or a bad path failed completely silently — the setting appeared
// to stick until the next launch. copyFeedback renders in the status bar on
// every screen; settings.feedback is picked up by the settings screen.
//
// It returns the error as well as displaying it, because setting the feedback
// was not enough on its own: callers went on to overwrite it with "<field>
// saved" and to leave the setup screen, so a failed write still read as a
// success. Anything that reports success or navigates away must check this.
func (m *Model) saveConfig() error {
	// --dry-run says "simulate operations without making changes", and this is
	// the only thing in the app that writes outside the repo. Editing settings
	// under it used to rewrite the real prflow.toml — which also drops any
	// comments the user had added, since Save marshals the whole struct.
	if m.dryRun {
		m.settings.feedback = errDryRun.Error()
		m.settings.success = true
		return errDryRun
	}

	err := m.config.Save()
	if err != nil {
		msg := "Config save failed: " + err.Error()
		m.copyFeedback = "✗ " + msg
		m.settings.feedback = msg
		m.settings.success = false
	}
	return err
}

// copyWithFeedback copies text to the clipboard and sets copyFeedback
func (m *Model) copyWithFeedback(text, successMsg string) {
	if err := copyToClipboard(text); err == nil {
		m.copyFeedback = "✓ " + successMsg
	} else {
		m.copyFeedback = "✗ Copy failed"
	}
}

// leftGroups returns the set of group names assigned to the left column
func (m Model) leftGroups() map[string]bool {
	return m.config.LeftGroups()
}

// mainBranch returns the main branch name for the current repo, defaulting to "main"
func (m Model) mainBranch() string {
	if m.repoInfo != nil {
		return m.repoInfo.MainBranch
	}
	return "main"
}

// New creates a new application model
func New(cfg *config.Config, dryRun, testUpdate bool, version string) Model {
	return Model{
		config:      cfg,
		dryRun:      dryRun,
		testUpdate:  testUpdate,
		version:     version,
		screen:      startScreen(cfg),
		menuIndex:   0,
		width:       80,
		height:      24,
		sessionPRs:  loadHistory(),
		tickRunning: true, // Init starts the chain

		configDiagnostics: cfg.Validate(),
		firstRun:          firstRunState{value: cfg.Paths.ReposDir},
	}
}

// startScreen sends a user with no config file to setup rather than to a main
// menu whose every option leads to an empty list.
func startScreen(cfg *config.Config) Screen {
	if cfg.IsFirstRun() {
		return ScreenFirstRun
	}
	return ScreenMainMenu
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tea.EnterAltScreen,
		tickCmd(),
	}
	if !m.dryRun {
		cmds = append(cmds, authCheckCmd())
		// Check for updates if enabled and 24h since last check
		if m.config.ShouldCheckForUpdate() {
			cmds = append(cmds, checkUpdateCmd(m.version, m.config.Update.Repo))
		}
	}
	// Test update flag shows fake update prompt
	if m.testUpdate {
		cmds = append(cmds, func() tea.Msg {
			return updateCheckResult{release: &update.Release{TagName: "v99.0.0"}}
		})
	}
	return tea.Batch(cmds...)
}

// tickMsg is sent on each tick for animations
type tickMsg struct{}

func tickCmd() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(_ time.Time) tea.Msg {
		return tickMsg{}
	})
}

// needsAnimation reports whether anything on screen is currently moving.
//
// The tick chain used to run at 80ms forever, redrawing menus and summaries
// that never change. It now stops when nothing is animating and is restarted
// by Update as soon as something is. Err on the side of true: a missed case
// freezes a spinner, which is far worse than an extra redraw.
func (m Model) needsAnimation() bool {
	// Confetti physics still running.
	if len(m.confetti) > 0 {
		return true
	}

	// Any in-flight background work draws a spinner, on whatever screen.
	if m.allPRs.loading || m.actions.loading || m.settings.looking ||
		m.updateCheckInProgress || m.batch.fetchPending > 0 {
		return true
	}

	switch m.screen {
	// Progress screens: spinner and/or pulse, always moving.
	case ScreenLoading, ScreenCreating, ScreenBatchProcessing,
		ScreenMerging, ScreenUpdating, ScreenPullProgress:
		return true

	// Success screens animate a typewriter reveal, then settle.
	case ScreenComplete, ScreenBatchSummary:
		return m.typewriterPos < 100

	// These poll and animate per-item status icons for in-progress work.
	case ScreenActionsOverview, ScreenViewAllPrs:
		return true
	}

	return false
}

// updateAnimations updates all animation state
func (m *Model) updateAnimations() {
	// Update pulse phase (smooth sine wave)
	m.pulsePhase = math.Mod(m.pulsePhase+0.08, 2.0*math.Pi)

	// Update confetti physics
	for i := range m.confetti {
		m.confetti[i].X += m.confetti[i].VX
		m.confetti[i].Y += m.confetti[i].VY
		m.confetti[i].VY += 0.15 // gravity
		m.confetti[i].VX *= 0.98 // air resistance
	}

	// Remove particles that fell off screen
	filtered := m.confetti[:0]
	for _, p := range m.confetti {
		if p.Y < 50.0 {
			filtered = append(filtered, p)
		}
	}
	m.confetti = filtered

	// Typewriter effect - reveal more characters on success screens
	if (m.screen == ScreenComplete || m.screen == ScreenBatchSummary) && m.typewriterPos < 100 {
		m.typewriterPos++
	}
}
