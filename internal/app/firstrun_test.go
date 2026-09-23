package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/git"

	tea "github.com/charmbracelet/bubbletea"
)

// repoTree builds a directory of real git repos matching the default globs.
func repoTree(t *testing.T) string {
	t.Helper()
	return gitRepos(t, "frontend/web", "frontend/mobile", "backend/api")
}

// gitRepos builds a temp directory holding a git repo at each path.
func gitRepos(t *testing.T, paths ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, r := range paths {
		dir := filepath.Join(root, r)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := exec.Command("git", "-C", dir, "init", "-q").Run(); err != nil {
			t.Skipf("git unavailable: %v", err)
		}
	}
	return root
}

func firstRunModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	return Model{screen: ScreenFirstRun, config: config.DefaultConfig()}
}

func typeRunes(m Model, s string) Model {
	next, _ := m.handleFirstRunKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return next.(Model)
}

func pressEnter(m Model) (Model, tea.Cmd) {
	next, cmd := m.handleFirstRunKey(tea.KeyMsg{Type: tea.KeyEnter})
	return next.(Model), cmd
}

// The whole point of this screen: show what a directory contains before writing
// it to the config, so a wrong path is visible immediately rather than as an
// unexplained empty list three screens later.
func TestFirstRunPreviewsBeforeSaving(t *testing.T) {
	root := repoTree(t)
	m := firstRunModel(t)
	m.firstRun.value = ""
	m = typeRunes(m, root)

	// First Enter scans rather than accepting.
	m, cmd := pressEnter(m)
	if !m.firstRun.scanning {
		t.Fatal("Enter should start a scan")
	}
	if cmd == nil {
		t.Fatal("Enter should return a scan command")
	}
	if m.screen != ScreenFirstRun {
		t.Error("the first Enter should not leave the setup screen")
	}
	if m.config.Paths.ReposDir == root {
		t.Error("the path was saved before the user saw the preview")
	}

	// Run the scan and feed the result back.
	msg, ok := cmd().(firstRunPreviewResult)
	if !ok {
		t.Fatalf("unexpected message type %T", cmd())
	}
	next, _ := m.handleFirstRunPreview(msg)
	m = next.(Model)

	if m.firstRun.scanning {
		t.Error("scanning flag should clear once results arrive")
	}
	if len(m.firstRun.preview.Repos) != 3 {
		t.Fatalf("found %d repos, want 3: %+v", len(m.firstRun.preview.Repos), m.firstRun.preview.Repos)
	}
	if m.firstRun.preview.Globs != nil {
		t.Errorf("the default globs matched, so none should be detected: %+v", m.firstRun.preview.Globs)
	}

	// Second Enter accepts.
	m, _ = pressEnter(m)
	if m.config.Paths.ReposDir != root {
		t.Errorf("path = %q, want %q", m.config.Paths.ReposDir, root)
	}
	if m.screen != ScreenMainMenu {
		t.Errorf("screen = %v, want MainMenu", m.screen)
	}

	// And it must actually be on disk, or the next launch asks again.
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("config not written: %v", err)
	}
}

// Setup only scanned frontend/* and backend/*, so repos kept directly under the
// directory — the most common layout — found nothing and setup could not be
// finished. The detected globs must also be what gets saved, or the main menu
// would come up empty after setup said it found them.
func TestFirstRunFindsAPlainLayout(t *testing.T) {
	root := gitRepos(t, "web", "api", "work/svc")
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0755); err != nil {
		t.Fatal(err)
	}
	m := firstRunModel(t)
	m.firstRun.value = ""
	m = typeRunes(m, root)

	m, cmd := pressEnter(m)
	next, _ := m.handleFirstRunPreview(cmd().(firstRunPreviewResult))
	m = next.(Model)
	if len(m.firstRun.preview.Repos) != 3 {
		t.Fatalf("found %d repos, want 3: %+v", len(m.firstRun.preview.Repos), m.firstRun.preview.Repos)
	}

	m, _ = pressEnter(m)
	if m.screen != ScreenMainMenu {
		t.Fatalf("screen = %v, want MainMenu", m.screen)
	}

	// Reload from disk, as the next launch would.
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []config.GlobEntry{{Pattern: "*", Group: "Repos"}, {Pattern: "work/*", Group: "Work"}}
	if len(cfg.Globs) != len(want) || cfg.Globs[0] != want[0] || cfg.Globs[1] != want[1] {
		t.Errorf("saved globs = %+v, want %+v", cfg.Globs, want)
	}
	repos, err := git.FindRepos(cfg.Paths.ReposDir, cfg.GlobEntries(), cfg.ExplicitRepos())
	if err != nil || len(repos) != 3 {
		t.Errorf("saved config finds %d repos (err %v), want 3", len(repos), err)
	}
	if diags := cfg.Validate(); len(diags) != 0 {
		t.Errorf("saved config has diagnostics: %+v", diags)
	}
}

// A directory with nothing in it must not be accepted by a second Enter — that
// is precisely the state this screen exists to catch.
func TestFirstRunDoesNotAcceptAnEmptyResult(t *testing.T) {
	m := firstRunModel(t)
	m.firstRun.value = ""
	empty := t.TempDir()
	m = typeRunes(m, empty)

	m, cmd := pressEnter(m)
	next, _ := m.handleFirstRunPreview(cmd().(firstRunPreviewResult))
	m = next.(Model)

	if len(m.firstRun.preview.Repos) != 0 {
		t.Fatalf("expected no repos, got %d", len(m.firstRun.preview.Repos))
	}

	// Pressing Enter again should re-scan, not accept.
	m, cmd = pressEnter(m)
	if m.screen != ScreenFirstRun {
		t.Error("an empty result should not be accepted")
	}
	if cmd == nil {
		t.Error("Enter on an empty result should re-scan")
	}
}

// Editing the path must drop the previous result, or a second Enter could
// accept a scan belonging to a different directory.
func TestFirstRunEditingClearsStalePreview(t *testing.T) {
	root := repoTree(t)
	m := firstRunModel(t)
	m.firstRun.value = ""
	m = typeRunes(m, root)

	m, cmd := pressEnter(m)
	next, _ := m.handleFirstRunPreview(cmd().(firstRunPreviewResult))
	m = next.(Model)
	if len(m.firstRun.preview.Repos) == 0 {
		t.Fatal("setup: expected a successful scan")
	}

	m = typeRunes(m, "-typo")
	if m.firstRun.preview.Ran {
		t.Error("editing the path left the old preview in place")
	}

	m, _ = pressEnter(m)
	if m.screen != ScreenFirstRun {
		t.Error("accepted a preview that belonged to a different path")
	}
}

func TestFirstRunSkipWritesConfigAndLeaves(t *testing.T) {
	m := firstRunModel(t)
	next, _ := m.handleFirstRunKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)

	if m.screen != ScreenMainMenu {
		t.Errorf("screen = %v, want MainMenu", m.screen)
	}
	// Skipping still writes, so the app does not re-prompt on every launch.
	path, _ := config.Path()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("skipping setup should still write a config: %v", err)
	}
}

// A config file that already exists must never route to setup.
func TestStartScreen(t *testing.T) {
	if got := startScreen(config.DefaultConfig()); got != ScreenMainMenu {
		t.Errorf("existing config -> %v, want MainMenu", got)
	}

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.IsFirstRun() {
		t.Fatal("a missing config file should report a first run")
	}
	if got := startScreen(cfg); got != ScreenFirstRun {
		t.Errorf("first run -> %v, want FirstRun", got)
	}
	// Load must not have written anything: that is what used to leave people
	// with a config pointing at a directory they had never heard of.
	path, _ := config.Path()
	if _, err := os.Stat(path); err == nil {
		t.Error("Load wrote a config file before the user confirmed anything")
	}
}
