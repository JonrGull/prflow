package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Validate ---------------------------------------------------------------
//
// Before Validate existed, the only check was compiling the ticket regex, so a
// base directory that did not exist, a glob matching nothing, and a group listed
// in neither column all produced one identical symptom: an empty list with no
// explanation.

// validFlows is the minimum a config needs to be able to open a PR at all, so
// fixtures that are testing something else still have to carry one.
func validFlows() []FlowEntry {
	return []FlowEntry{{Head: "dev", Base: "staging"}}
}

func TestValidate(t *testing.T) {
	base := t.TempDir()
	mustMkdir(t, filepath.Join(base, "frontend", "web"))

	t.Run("clean config is quiet", func(t *testing.T) {
		c := &Config{
			Flows:   validFlows(),
			Paths:   PathsConfig{ReposDir: base},
			Columns: ColumnsConfig{Left: []string{"Frontend"}},
			Globs:   []GlobEntry{{Pattern: "frontend/*", Group: "Frontend"}},
		}
		if diags := c.Validate(); len(diags) != 0 {
			t.Errorf("expected no diagnostics, got %v", diags)
		}
	})

	// The nastiest case: RepoInfo.InColumn treats "not in columns.left" as
	// "right", so a group nobody assigned silently lands in the right column.
	t.Run("group assigned to no column", func(t *testing.T) {
		c := &Config{
			Flows:   validFlows(),
			Paths:   PathsConfig{ReposDir: base},
			Columns: ColumnsConfig{Left: []string{"Frontend"}},
			Globs: []GlobEntry{
				{Pattern: "frontend/*", Group: "Frontend"},
				{Pattern: "frontend/*", Group: "Backend"}, // matches, but unassigned
			},
		}
		diags := c.Validate()
		if !hasMessage(diags, `group "Backend" is not listed`) {
			t.Errorf("expected unassigned-group diagnostic, got %v", diags)
		}
		if HasErrors(diags) {
			t.Errorf("should be a warning, not an error: %v", diags)
		}
	})

	t.Run("column naming a group with no repos", func(t *testing.T) {
		c := &Config{
			Flows:   validFlows(),
			Paths:   PathsConfig{ReposDir: base},
			Columns: ColumnsConfig{Left: []string{"Frontend"}, Right: []string{"Typo"}},
			Globs:   []GlobEntry{{Pattern: "frontend/*", Group: "Frontend"}},
		}
		if diags := c.Validate(); !hasMessage(diags, `group "Typo" has no repos`) {
			t.Errorf("expected empty-column diagnostic, got %v", diags)
		}
	})

	t.Run("glob matching nothing", func(t *testing.T) {
		c := &Config{
			Flows:   validFlows(),
			Paths:   PathsConfig{ReposDir: base},
			Columns: ColumnsConfig{Left: []string{"Nope"}},
			Globs:   []GlobEntry{{Pattern: "nowhere/*", Group: "Nope"}},
		}
		if diags := c.Validate(); !hasMessage(diags, "matches nothing") {
			t.Errorf("expected empty-glob diagnostic, got %v", diags)
		}
	})

	t.Run("duplicate glob pattern", func(t *testing.T) {
		c := &Config{
			Flows:   validFlows(),
			Paths:   PathsConfig{ReposDir: base},
			Columns: ColumnsConfig{Left: []string{"Frontend"}},
			Globs: []GlobEntry{
				{Pattern: "frontend/*", Group: "Frontend"},
				{Pattern: "frontend/*", Group: "Frontend"},
			},
		}
		if diags := c.Validate(); !hasMessage(diags, "listed more than once") {
			t.Errorf("expected duplicate-pattern diagnostic, got %v", diags)
		}
	})

	// Column matching is case-insensitive at runtime — LeftGroups lowercases —
	// so validation must be too. It previously was not, and reported a spurious
	// "has no repos" for a column entry that worked perfectly well.
	t.Run("column case does not have to match the glob group", func(t *testing.T) {
		c := &Config{
			Flows:   validFlows(),
			Paths:   PathsConfig{ReposDir: base},
			Columns: ColumnsConfig{Left: []string{"frontend"}},
			Globs:   []GlobEntry{{Pattern: "frontend/*", Group: "Frontend"}},
		}
		diags := c.Validate()
		if hasMessage(diags, "has no repos") {
			t.Errorf("case mismatch reported as a missing group: %v", diags)
		}
		if hasMessage(diags, "is not listed in") {
			t.Errorf("case mismatch reported as unassigned: %v", diags)
		}
		if len(diags) != 0 {
			t.Errorf("expected no diagnostics, got %v", diags)
		}
	})

	t.Run("missing base dir is fatal without explicit repos", func(t *testing.T) {
		c := &Config{Paths: PathsConfig{ReposDir: filepath.Join(base, "gone")}}
		if !HasErrors(c.Validate()) {
			t.Error("expected an error-severity diagnostic")
		}
	})

	// Explicit repos are discovered independently, so a missing base directory
	// downgrades to a warning rather than blocking everything.
	t.Run("missing base dir is a warning with explicit repos", func(t *testing.T) {
		c := &Config{
			Flows:   validFlows(),
			Paths:   PathsConfig{ReposDir: filepath.Join(base, "gone")},
			Columns: ColumnsConfig{Left: []string{"Services"}},
			Repos:   []RepoEntry{{Path: base, Group: "Services"}},
		}
		diags := c.Validate()
		if HasErrors(diags) {
			t.Errorf("expected warnings only, got %v", diags)
		}
	})
}

// KnownGroups feeds both the validator and the settings editor's hint, so it
// has to agree with what discovery would actually produce.
func TestKnownGroups(t *testing.T) {
	c := &Config{
		Globs: []GlobEntry{
			{Pattern: "frontend/*", Group: "Frontend"},
			{Pattern: "web/*", Group: "Frontend"}, // duplicate group
			{Pattern: "backend/*", Group: "Backend"},
			{Pattern: "misc/*", Group: ""}, // no group: not a candidate
		},
		Repos: []RepoEntry{
			{Path: "/tmp/a", Group: "Services"},
			{Path: "/tmp/b", Group: "frontend"}, // same group, different case
		},
	}

	got := c.KnownGroups()
	want := []string{"Backend", "Frontend", "Services"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v (sorted, de-duplicated case-insensitively)", got, want)
		}
	}

	if groups := (&Config{}).KnownGroups(); len(groups) != 0 {
		t.Errorf("empty config should have no known groups, got %v", groups)
	}
}

// --- Atomic writes ----------------------------------------------------------
//
// Save used to truncate the real config before writing, so an interrupted write
// left it truncated or empty, which then failed to parse on the next launch.

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")

	if err := writeFileAtomic(path, []byte("first"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != "first" {
		t.Fatalf("got %q", got)
	}

	if err := writeFileAtomic(path, []byte("second"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != "second" {
		t.Fatalf("overwrite: got %q", got)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0644 {
		t.Errorf("permissions = %v, want 0644", perm)
	}

	// A failed rename or an unclosed handle would leave .tmp files behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("expected only the target file, found %v", names)
	}

	// Parent directories are created on demand (first run, no config dir yet).
	nested := filepath.Join(dir, "a", "b", "c.toml")
	if err := writeFileAtomic(nested, []byte("x"), 0644); err != nil {
		t.Fatalf("nested write: %v", err)
	}
}

// --- Migrations -------------------------------------------------------------

// The glob migration regressed once already (commit 7cded8b, "old custom globs
// were silently dropped"), which is the argument for pinning it.
func TestLoadMigrations(t *testing.T) {
	t.Run("deprecated globs become [[globs]] entries", func(t *testing.T) {
		cfg := loadFrom(t, `
[paths]
repos_dir = '/tmp/x'
frontend_glob = 'fe/*'
backend_glob = 'be/*'
`)
		if len(cfg.Globs) != 2 {
			t.Fatalf("expected 2 globs, got %+v", cfg.Globs)
		}
		if cfg.Globs[0].Pattern != "fe/*" || cfg.Globs[0].Group != "Frontend" {
			t.Errorf("frontend glob = %+v", cfg.Globs[0])
		}
		if cfg.Globs[1].Pattern != "be/*" || cfg.Globs[1].Group != "Backend" {
			t.Errorf("backend glob = %+v", cfg.Globs[1])
		}
		if cfg.Paths.FrontendGlob != "" || cfg.Paths.BackendGlob != "" {
			t.Error("deprecated fields should be cleared after migration")
		}
	})

	// This is the shape that regressed: custom [[globs]] alongside the old keys
	// must win, not be replaced by defaults.
	t.Run("custom globs survive", func(t *testing.T) {
		cfg := loadFrom(t, `
[paths]
repos_dir = '/tmp/x'

[[globs]]
pattern = 'services/*'
group = 'Services'
`)
		if len(cfg.Globs) != 1 || cfg.Globs[0].Pattern != "services/*" {
			t.Errorf("custom globs were dropped: %+v", cfg.Globs)
		}
	})

	t.Run("repo category becomes group", func(t *testing.T) {
		cfg := loadFrom(t, `
[paths]
repos_dir = '/tmp/x'

[[repos]]
path = '/some/repo'
category = 'Legacy'
`)
		if len(cfg.Repos) != 1 || cfg.Repos[0].Group != "Legacy" {
			t.Fatalf("category not migrated: %+v", cfg.Repos)
		}
		if cfg.Repos[0].Category != "" {
			t.Error("deprecated category should be cleared")
		}
	})

	// Update bookkeeping moved out of the config file so that merely launching
	// the app stops rewriting (and de-commenting) the user's TOML.
	t.Run("update state migrates out of the config", func(t *testing.T) {
		cfg := loadFrom(t, `
[paths]
repos_dir = '/tmp/x'

[update]
enabled = true
skipped_version = 'v9.9.9'
last_check = 2026-01-02T03:04:05Z
`)
		if got := cfg.SkippedVersion(); got != "v9.9.9" {
			t.Errorf("skipped version = %q, want v9.9.9", got)
		}
		if cfg.State().LastCheck.IsZero() {
			t.Error("last check should have been adopted")
		}
		if cfg.Update.SkippedVersion != "" || !cfg.Update.LastCheck.IsZero() {
			t.Error("values should be cleared from the config struct after migration")
		}
	})
}

func TestStateMigrateFrom(t *testing.T) {
	last := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	s := &State{}
	if !s.migrateFrom(UpdateConfig{LastCheck: last, SkippedVersion: "v1.0.0"}) {
		t.Error("expected migration to report a change")
	}
	if !s.LastCheck.Equal(last) || s.SkippedVersion != "v1.0.0" {
		t.Errorf("not adopted: %+v", s)
	}

	// Existing state wins; the config copy is stale by definition.
	existing := &State{LastCheck: last.Add(time.Hour), SkippedVersion: "v2.0.0"}
	if existing.migrateFrom(UpdateConfig{LastCheck: last, SkippedVersion: "v1.0.0"}) {
		t.Error("should not overwrite existing state")
	}
	if existing.SkippedVersion != "v2.0.0" {
		t.Errorf("state was clobbered: %+v", existing)
	}
}

// --- Secrets ----------------------------------------------------------------

// ~/.secrets is normally shell-sourced, so the parser has to cope with the
// shapes it actually takes. It previously matched only a bare KEY=value line.
func TestLinearAPIKeyFromSecrets(t *testing.T) {
	tests := map[string]string{
		"LINEAR_API_KEY=lin_plain":          "lin_plain",
		"export LINEAR_API_KEY=lin_export":  "lin_export",
		`LINEAR_API_KEY="lin_double"`:       "lin_double",
		`export LINEAR_API_KEY='lin_both'`:  "lin_both",
		"  LINEAR_API_KEY=lin_indented  ":   "lin_indented",
		"OTHER=x\nLINEAR_API_KEY=lin_later": "lin_later",
		"NOT_THE_KEY=nope":                  "",
	}

	for content, want := range tests {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("LINEAR_API_KEY", "")
		if err := os.WriteFile(filepath.Join(home, ".secrets"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if got := (&Config{}).LinearAPIKey(); got != want {
			t.Errorf("content %q: got %q, want %q", content, got, want)
		}
	}

	// The environment variable takes precedence over the file.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LINEAR_API_KEY", "from_env")
	if err := os.WriteFile(filepath.Join(home, ".secrets"), []byte("LINEAR_API_KEY=from_file"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := (&Config{}).LinearAPIKey(); got != "from_env" {
		t.Errorf("env should win, got %q", got)
	}
}

// --- helpers ----------------------------------------------------------------

// loadFrom writes content as the user's config and loads it, with the config
// and state directories redirected into a temp dir.
func loadFrom(t *testing.T, content string) *Config {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func hasMessage(diags []Diagnostic, substr string) bool {
	for _, d := range diags {
		if strings.Contains(d.Message, substr) {
			return true
		}
	}
	return false
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
