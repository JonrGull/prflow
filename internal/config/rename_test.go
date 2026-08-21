package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JonrGull/prflow/internal/git"
)

// The rename must be invisible to anyone already using the tool: the config and
// state files changed name, and the scan directory changed key, so all three
// have to be read from their old spellings when the new ones are absent.

func TestLoadReadsThePreRenameConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	legacy := filepath.Join(dir, legacyConfigName)
	body := "[paths]\nattuned_dir = '/somewhere/repos'\n\n[tickets]\npattern = 'XYZ-[0-9]+'\n"
	if err := os.WriteFile(legacy, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IsFirstRun() {
		t.Error("an existing config was treated as a first run")
	}
	if cfg.Paths.ReposDir != "/somewhere/repos" {
		t.Errorf("repos dir = %q, want the migrated attuned_dir", cfg.Paths.ReposDir)
	}
	if cfg.Tickets.Pattern != "XYZ-[0-9]+" {
		t.Errorf("pattern = %q, want the file's own value", cfg.Tickets.Pattern)
	}
	// The deprecated key is cleared so the next write drops it rather than
	// leaving two spellings of the same setting in the file.
	if cfg.Paths.AttunedDir != "" {
		t.Errorf("deprecated attuned_dir survived as %q", cfg.Paths.AttunedDir)
	}
}

// The new name wins when both exist, and the old file is left alone.
func TestLoadPrefersTheNewConfigFileAndLeavesTheOldOne(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	legacy := filepath.Join(dir, legacyConfigName)
	current := filepath.Join(dir, configName)
	if err := os.WriteFile(legacy, []byte("[paths]\nrepos_dir = '/old'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte("[paths]\nrepos_dir = '/new'\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Paths.ReposDir != "/new" {
		t.Errorf("repos dir = %q, want the new file's value", cfg.Paths.ReposDir)
	}

	// Saving writes the new name; the old file must not be touched, so a
	// downgrade still finds its config.
	cfg.Paths.ReposDir = "/saved"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != "[paths]\nrepos_dir = '/old'\n" {
		t.Errorf("the pre-rename config was rewritten: %q", old)
	}
	if _, err := os.Stat(current); err != nil {
		t.Errorf("the new config was not written: %v", err)
	}
}

// A missing config in a fresh directory is still a first run, not a read error.
func TestLoadWithNeitherFileIsAFirstRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.IsFirstRun() {
		t.Error("an empty config directory should be a first run")
	}
}

// Losing the state file means a spurious update check and a forgotten skipped
// version, which is exactly the annoyance it was split out to avoid.
func TestLoadStateReadsThePreRenameFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	legacy := filepath.Join(dir, legacyStateName)
	body := "last_check = 2026-01-02T03:04:05Z\nskipped_version = 'v9.9.9'\n"
	if err := os.WriteFile(legacy, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	st := LoadState()
	if st.SkippedVersion != "v9.9.9" {
		t.Errorf("skipped version = %q, want the migrated value", st.SkippedVersion)
	}
	want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if !st.LastCheck.Equal(want) {
		t.Errorf("last check = %v, want %v", st.LastCheck, want)
	}

	// And it saves under the new name.
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, stateName)); err != nil {
		t.Errorf("state was not written under the new name: %v", err)
	}
}

// The error shown when the repo directory is missing named paths.attuned_dir,
// a key that no longer exists — so the one message telling a user how to fix
// their config pointed at a setting they could not find. This lives here
// because the rename is what broke it.
func TestMissingRepoDirErrorNamesTheCurrentKey(t *testing.T) {
	_, err := git.FindRepos(filepath.Join(t.TempDir(), "nope"), nil, nil)
	if err == nil {
		t.Fatal("a missing repo directory with no explicit repos should error")
	}
	if !strings.Contains(err.Error(), "paths.repos_dir") {
		t.Errorf("error does not name the current config key: %v", err)
	}
	if strings.Contains(err.Error(), "attuned") {
		t.Errorf("error still names the pre-rename key: %v", err)
	}
}
