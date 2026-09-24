package app

import (
	"reflect"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// The message loop: what every screen shares before its own handler runs.
//
// Update takes the global keys (help, tabs, fullscreen, quit) and the window
// resize, then dispatches to the per-screen handler in that screen's own file.
// Anything screen-specific belongs there, not here.
//
// isTextInputActive is the subtle one: a screen with a text field must appear
// in it, or the global keys will eat characters mid-word.

// trimLastRune removes the final character from s, for backspace handling.
// Every hand-rolled text input here used to slice off a byte, which splits
// multi-byte characters and leaves invalid UTF-8 behind.
func trimLastRune(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return string(r[:len(r)-1])
}

// typedText is what a key press adds to a text field. A paste arrives as one
// key press and kept its newlines and tabs, which no field here can hold; they
// become spaces, and other control characters are dropped.
func typedText(r []rune) string {
	var b strings.Builder
	for _, c := range r {
		switch {
		case c == '\n' || c == '\r' || c == '\t':
			b.WriteByte(' ')
		case unicode.IsControl(c):
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// numKeyIndex converts a number key string ("1"-"9") to a 0-based index.
// Returns (index, true) if the key is a valid number within maxItems, or (0, false) otherwise.
func numKeyIndex(key string, maxItems int) (int, bool) {
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		idx := int(key[0] - '1')
		if idx < maxItems {
			return idx, true
		}
	}
	return 0, false
}

// Update handles all messages and updates state.
//
// It wraps dispatch so the animation tick can be restarted from one place:
// any message may move the app into a state that animates (starting a fetch,
// entering a progress screen), and the tick chain stops itself when idle.
//
// It is also where stale results are dropped; see flowResult.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if e, ok := msg.(epochMsg); ok {
		if e.epoch != m.epoch {
			// Answers a flow the user has since left.
			if a, ok := e.msg.(abandoner); ok {
				a.abandon()
			}
			return m, nil
		}
		msg = e.msg
	}

	wasMenu := m.screen == ScreenMainMenu
	next, cmd := m.dispatch(msg)

	updated, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	// Every exit from a flow passes through the main menu or a tab switch, so
	// arriving here is where the flow's outstanding requests stop mattering.
	if !wasMenu && updated.screen == ScreenMainMenu {
		updated.newEpoch()
	}
	cmd = tagEpoch(updated.epoch, cmd)

	if !updated.tickRunning && updated.needsAnimation() {
		updated.tickRunning = true
		return updated, tea.Batch(cmd, tickCmd())
	}
	return updated, cmd
}

// flowResult marks a message that answers a request some flow made. Update
// stamps each one with the epoch it was requested in and drops it if the user
// has moved on since, because the handlers all assume the screen that asked
// is still showing. Before this, a result that landed late acted on whatever
// came next: tabbing away mid-batch and ticking another repo meant the old
// run's result started a PR there, with the old title.
//
// Messages that are not flow results — the animation tick, the auth and update
// checks, bubbletea's own — pass through untouched. So do results that only
// update data keyed to what they fetched and never move the screen, like a
// pinned run's jobs: dropping those just leaves a panel waiting.
type flowResult interface{ flowResult() }

// abandoner is a flow result that holds something to release if it is dropped,
// such as the cancel func for work it started.
type abandoner interface{ abandon() }

type epochMsg struct {
	epoch uint64
	msg   tea.Msg
}

var cmdType = reflect.TypeOf(tea.Cmd(nil))

// tagEpoch wraps cmd so any flow result it produces carries epoch. It
// recurses into tea.Batch and tea.Sequence, whose commands the runtime runs
// itself. Sequence's message type is unexported, so both are matched by
// shape — a slice of commands — and rebuilt as the same type.
func tagEpoch(epoch uint64, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if r, ok := msg.(flowResult); ok {
			return epochMsg{epoch: epoch, msg: r}
		}
		v := reflect.ValueOf(msg)
		if v.Kind() != reflect.Slice || v.Type().Elem() != cmdType {
			return msg
		}
		tagged := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			c, _ := v.Index(i).Interface().(tea.Cmd)
			tagged.Index(i).Set(reflect.ValueOf(tagEpoch(epoch, c)))
		}
		return tagged.Interface()
	}
}

// newEpoch abandons everything the current flow is waiting for: its results
// will be dropped, so the flags that say they are coming are cleared too, or
// a spinner and the refresh guards would wait for them forever.
func (m *Model) newEpoch() {
	m.epoch++
	m.cancelBatchFetch()
	m.allPRs.loading = false
	m.actions.loading = false
}

// isBusy reports a screen running a write: creating PRs, merging, pulling,
// replacing the binary. The tab keys are ignored there, because leaving would
// abandon the run halfway and its remaining results would be dropped as stale.
func isBusy(s Screen) bool {
	switch s {
	case ScreenCreating, ScreenBatchProcessing, ScreenMerging, ScreenPullProgress, ScreenUpdating:
		return true
	}
	return false
}

func (m Model) dispatch(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		m.spinnerFrame = (m.spinnerFrame + 1) % 10
		m.updateAnimations()
		if !m.needsAnimation() {
			m.tickRunning = false
			return m, nil
		}
		return m, tickCmd()

	// Task result messages
	case fetchCommitsResult:
		return m.handleFetchCommitsResult(msg)

	case batchCommitsResult:
		return m.handleBatchCommitsResult(msg)

	case prCreatedResult:
		return m.handlePrCreatedResult(msg)

	case batchRepoResult:
		return m.handleBatchRepoResult(msg)

	case batchProgressMsg:
		m.batch.currentStep = msg.step
		// Continue listening for more progress updates
		return m, listenForProgress(m.batch.progressChan)

	case openPRsFetchedResult:
		return m.handleOpenPRsFetchedResult(msg)

	case allOpenPRsFetchedResult:
		return m.handleAllOpenPRsFetched(msg)

	case allPRsRefreshTickMsg:
		return m.handleAllPRsRefreshTick(msg)

	case mergeCompleteResult:
		return m.handleMergeCompleteResult(msg)

	case batchReposLoadedResult:
		return m.handleBatchReposLoaded(msg)

	case batchRepoCommitResult:
		return m.handleBatchRepoCommitResult(msg)

	case currentRepoLoadedResult:
		return m.handleCurrentRepoLoaded(msg)

	case authCheckResult:
		m.authError = msg.err
		m.ghUser = msg.user
		return m, nil

	case updateCheckResult:
		return m.handleUpdateCheckResult(msg)

	case updateDownloadResult:
		return m.handleUpdateDownloadResult(msg)

	case pullReposLoadedResult:
		return m.handlePullReposLoaded(msg)

	case pullRepoResult:
		return m.handlePullRepoResult(msg)

	case actionsRunsFetchedResult:
		return m.handleActionsRunsFetched(msg)

	case actionsRefreshTickMsg:
		return m.handleActionsRefreshTick(msg)

	case actionsJobsFetchedResult:
		return m.handleActionsJobsFetched(msg)

	case firstRunPreviewResult:
		return m.handleFirstRunPreview(msg)

	case qaTagResultMsg:
		m.qa.results = msg.results
		return m, nil

	case qaTicketTitlesResult:
		m.qa.titles = msg.titles
		return m, nil

	case qaPersonLookupResult:
		m.settings.looking = false
		if msg.err != nil {
			m.settings.feedback = msg.err.Error()
			m.settings.success = false
			return m, nil
		}
		m.config.Tickets.QaPerson = msg.name
		m.config.Tickets.QaPersonID = msg.id
		if err := m.saveConfig(); err != nil {
			// saveConfig has already put the failure in settings.feedback.
			return m, nil
		}
		m.settings.feedback = "Will tag " + msg.name
		m.settings.success = true
		return m, nil
	}

	return m, nil
}

func (m *Model) isTextInputActive() bool {
	switch m.screen {
	case ScreenTitleInput, ScreenCommitReview:
		return true
	case ScreenActionsOverview:
		return m.actions.filterActive
	case ScreenBatchRepoSelect:
		// This list filters as you type, with no mode to enter. Once something
		// is typed, F ? [ ] belong to the filter; before that they keep
		// working, or the screen would lose help and the tab keys entirely.
		return m.batch.filter != ""
	case ScreenFirstRun:
		return true
	case ScreenListEdit:
		// Only while a cell is open, so ? and the tab keys still work when
		// merely navigating the table.
		return m.list.editing
	case ScreenSettings:
		// Same rule, and it matters most here: the ticket pattern field wants
		// a regex, so [ has to reach the input rather than switch tabs.
		return m.settings.editing
	}
	return false
}

// navigateToTab switches to a tab by index, using cached data when available
func (m Model) navigateToTab(tab int) (tea.Model, tea.Cmd) {
	// Compared against the displayed tab, not the stored one: on the main menu
	// nothing is selected, so navigating to tab 0 is a real move rather than a
	// no-op against a value left over from last time.
	if tab < 0 || tab > 4 || tab == m.activeTabForDisplay() {
		return m, nil
	}
	m.activeTab = tab
	m.newEpoch()

	// Use cached data for instant switching; fall back to loading
	switch tab {
	case 0: // Single — always needs fresh repo detection
		m.menuIndex = 0
		return m.selectMainMenuItem()
	case 1: // Batch — goes to type select, no data to cache
		m.menuIndex = 1
		return m.selectMainMenuItem()
	case 2: // Release PRs — use cached if available
		if len(m.merge.openPRs) > 0 {
			m.screen = ScreenViewOpenPrs
			return m, nil
		}
		m.menuIndex = 2
		return m.selectMainMenuItem()
	case 3: // All Open PRs — use cached if available
		if len(m.allPRs.entries) > 0 {
			m.screen = ScreenViewAllPrs
			// The old refresh chain ended with the epoch, as the Actions one
			// below does.
			if m.allPRs.autoRefresh {
				cmd := m.startAllPRsRefresh()
				return m, cmd
			}
			return m, nil
		}
		m.menuIndex = 3
		return m.selectMainMenuItem()
	case 4: // Actions — use cached if available
		if len(m.actions.entries) > 0 {
			m.screen = ScreenActionsOverview
			if m.actions.autoRefresh {
				cmd := m.startActionsRefresh()
				return m, cmd
			}
			return m, nil
		}
		m.menuIndex = 4
		return m.selectMainMenuItem()
	}
	return m, nil
}

// handleKey processes keyboard input
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Clear copy feedback on any keypress
	m.copyFeedback = ""

	// Global quit
	if msg.Type == tea.KeyCtrlC {
		m.shouldQuit = true
		return m, tea.Quit
	}

	// Help overlay. While it is open it swallows input, so a stray key cannot
	// act on the screen hidden behind it.
	if m.showHelp {
		switch msg.String() {
		case "?", "esc", "q", "enter", " ":
			m.showHelp = false
		}
		return m, nil
	}
	if msg.String() == "?" && !m.isTextInputActive() {
		m.showHelp = true
		return m, nil
	}

	// Global fullscreen toggle (f key, but not during text input or filter)
	if msg.String() == "F" && !m.isTextInputActive() {
		m.fullscreen = !m.fullscreen
		return m, nil
	}

	// Tab switching with [ and ] (blocked during text input/filter, and while
	// a write is running).
	//
	// Stepping from activeTabForDisplay rather than activeTab is what makes the
	// main menu behave like the "no tab selected" it now looks like: from there
	// ] enters Single instead of skipping past it to Batch.
	if (msg.String() == "[" || msg.String() == "]") && !m.isTextInputActive() && !isBusy(m.screen) {
		current := m.activeTabForDisplay()
		if msg.String() == "[" {
			tab := current - 1
			if tab < 0 {
				tab = 4
			}
			return m.navigateToTab(tab)
		}
		tab := current + 1
		if tab > 4 {
			tab = 0
		}
		return m.navigateToTab(tab)
	}

	switch m.screen {
	case ScreenMainMenu:
		return m.handleMainMenuKey(msg)
	case ScreenPrTypeSelect:
		return m.handlePrTypeSelectKey(msg)
	case ScreenCommitReview:
		return m.handleCommitReviewKey(msg)
	case ScreenTitleInput:
		return m.handleTitleInputKey(msg)
	case ScreenConfirmation, ScreenBatchConfirmation, ScreenMergeConfirmation:
		return m.handleConfirmationKey(msg)
	case ScreenComplete:
		return m.handleCompleteKey(msg)
	case ScreenError:
		return m.handleErrorKey(msg)
	case ScreenBatchRepoSelect:
		return m.handleBatchRepoSelectKey(msg)
	case ScreenBatchSummary:
		return m.handleBatchSummaryKey(msg)
	case ScreenViewOpenPrs:
		return m.handleViewOpenPrsKey(msg)
	case ScreenViewAllPrs:
		return m.handleViewAllPrsKey(msg)
	case ScreenMergeSummary:
		return m.handleMergeSummaryKey(msg)
	case ScreenUpdatePrompt:
		return m.handleUpdatePromptKey(msg)
	case ScreenSessionHistory:
		return m.handleSessionHistoryKey(msg)
	case ScreenPullBranchSelect:
		return m.handlePullBranchSelectKey(msg)
	case ScreenPullSummary:
		return m.handlePullSummaryKey(msg)
	case ScreenActionsOverview:
		return m.handleActionsOverviewKey(msg)
	case ScreenQaTagSelect:
		return m.handleQaTagSelectKey(msg)
	case ScreenSettings:
		return m.handleSettingsKey(msg)
	case ScreenFirstRun:
		return m.handleFirstRunKey(msg)
	case ScreenListEdit:
		return m.handleListEditKey(msg)
	}

	return m, nil
}

func (m Model) goBack() (tea.Model, tea.Cmd) {
	switch m.screen {
	case ScreenConfirmation:
		m.screen = ScreenCommitReview
	case ScreenBatchConfirmation:
		m.screen = ScreenTitleInput // batch mode still uses separate title input
	case ScreenMergeConfirmation:
		m.screen = ScreenViewOpenPrs
	case ScreenQaTagSelect:
		m.screen = ScreenMergeSummary
	}
	m.confirmSelection = 0
	return m, nil
}

// navigateColumnIndex wraps up/down navigation within a filtered list
func navigateColumnIndex(idx *int, listLen int, up bool) {
	if listLen == 0 {
		return
	}
	if up {
		if *idx > 0 {
			*idx--
		} else {
			*idx = listLen - 1
		}
	} else {
		if *idx < listLen-1 {
			*idx++
		} else {
			*idx = 0
		}
	}
}

// toggleSelection toggles a boolean in a selection slice at the index pointed to by the current column position
func toggleSelection(selected []bool, filtered []int, currentIdx int) {
	if currentIdx >= len(filtered) {
		return
	}
	idx := filtered[currentIdx]
	if idx < len(selected) {
		selected[idx] = !selected[idx]
	}
}

// GitHub Actions key handlers

func (m Model) reset() (tea.Model, tea.Cmd) {
	// Stop any background fetch before dropping its channel and cancel func.
	m.cancelBatchFetch()

	// Each screen clears by assignment. The previous version listed fifty
	// fields by hand and missed about twenty of them — every batch and merge
	// cursor, both repo-error lists, and existingPR, which meant a second
	// single-PR run started out believing the previous repo's PR already
	// existed until the commit fetch overwrote it.
	m.batch = batchState{}
	m.merge = mergeState{}
	m.allPRs = allPRsState{}
	m.actions = actionsState{}
	m.pull = pullState{}
	m.qa = qaState{}

	m.screen = ScreenMainMenu
	m.menuIndex = 0
	m.mode = nil

	m.repoInfo = nil
	m.flow = nil
	m.commits = nil
	m.tickets = nil
	m.prTitle = ""
	m.prURL = ""
	m.existingPR = nil

	// Neither of these is reachable stale — every path to ScreenError and
	// ScreenLoading sets its message on the way in, and handleErrorKey clears
	// errorMessage on dismissal. They are cleared so reset() is complete by
	// inspection rather than by tracing twenty-one call sites.
	m.errorMessage = ""
	m.loadingMessage = ""

	m.confirmSelection = 0
	m.updateAvailable = nil
	m.updateSelection = 0
	m.confetti = nil
	m.typewriterPos = 0
	return m, nil
}
