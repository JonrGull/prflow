package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/JonrGull/prflow/internal/models"

	"github.com/pelletier/go-toml/v2"
)

type RepoEntry struct {
	Path     string `toml:"path"`
	Group    string `toml:"group,omitempty"`
	Category string `toml:"category,omitempty"` // deprecated: use Group
}

type GlobEntry struct {
	Pattern string `toml:"pattern"`
	Group   string `toml:"group"`
}

// FlowEntry is one release step: Head merged into Base.
//
// Base may be models.DefaultBranchToken ("@default") to mean the repo's own
// default branch, since repos disagree about main versus master. Title seeds
// the PR title input and is optional.
type FlowEntry struct {
	Head  string `toml:"head"`
	Base  string `toml:"base"`
	Title string `toml:"title,omitempty"`
}

type ColumnsConfig struct {
	Left  []string `toml:"left"`
	Right []string `toml:"right"`
}

type Config struct {
	Paths   PathsConfig   `toml:"paths"`
	Columns ColumnsConfig `toml:"columns"`
	Globs   []GlobEntry   `toml:"globs"`
	Repos   []RepoEntry   `toml:"repos"`
	Flows   []FlowEntry   `toml:"flows"`
	Tickets TicketsConfig `toml:"tickets"`
	Update  UpdateConfig  `toml:"update"`

	// Compiled regex from Tickets.Pattern (not serialized)
	ticketRegex *regexp.Regexp

	// state holds machine-written values, persisted separately so that
	// routine bookkeeping never rewrites the user's hand-edited TOML.
	state *State

	// firstRun records that no config file existed, so the app can offer setup
	// instead of dropping the user on an empty list.
	firstRun bool
}

// IsFirstRun reports whether this config was defaulted because no file existed.
func (c *Config) IsFirstRun() bool { return c.firstRun }

type UpdateConfig struct {
	Enabled bool   `toml:"enabled"`
	Repo    string `toml:"repo"`

	// LastCheck and SkippedVersion are read for migration only — they now live
	// in prflow-state.toml. They keep `omitempty` so they are dropped from the
	// config file the next time it is written.
	LastCheck      time.Time `toml:"last_check,omitempty"`
	SkippedVersion string    `toml:"skipped_version,omitempty"`
}

type PathsConfig struct {
	// ReposDir is the directory the globs are resolved under. It was
	// attuned_dir, which only made sense inside one company; the old key is
	// still read so an existing config keeps working.
	ReposDir     string `toml:"repos_dir"`
	AttunedDir   string `toml:"attuned_dir,omitempty"`   // deprecated: use repos_dir
	FrontendGlob string `toml:"frontend_glob,omitempty"` // deprecated: use [[globs]]
	BackendGlob  string `toml:"backend_glob,omitempty"`  // deprecated: use [[globs]]
}

type TicketsConfig struct {
	Pattern    string `toml:"pattern"`
	LinearOrg  string `toml:"linear_org"`
	QaPerson   string `toml:"qa_person"`    // Linear display name for QA tagging; empty = disabled
	QaPersonID string `toml:"qa_person_id"` // Linear user UUID (skips lookup if set)
	QaTagging  bool   `toml:"qa_tagging"`   // Show QA tagging screen (default true; toggle with t)
}

func DefaultConfig() *Config {
	return &Config{
		Paths: PathsConfig{
			ReposDir: "~/Projects",
		},
		Columns: ColumnsConfig{
			Left:  []string{"Frontend"},
			Right: []string{"Backend"},
		},
		Globs: []GlobEntry{
			{Pattern: "frontend/*", Group: "Frontend"},
			{Pattern: "backend/*", Group: "Backend"},
		},
		// Exactly the two steps that used to be a hardcoded enum, so an
		// existing config keeps behaving identically.
		Flows: []FlowEntry{
			{Head: "dev", Base: "staging"},
			{Head: "staging", Base: models.DefaultBranchToken},
		},
		// A ticket key of any project prefix, rather than one company's, so
		// extraction works before the user configures anything. It is
		// deliberately broad, which does mean it also matches technical tokens
		// of the same shape — UTF-8, SHA-256 — so narrowing it to the actual
		// project prefix is worth doing, and the settings screen shows a
		// derived example while you type. The Linear org is left empty:
		// without it the app builds no ticket links, which is the honest
		// default for someone who does not use Linear.
		Tickets: TicketsConfig{
			Pattern:   "[A-Z][A-Z0-9]+-[0-9]+",
			QaTagging: true,
		},
		Update: UpdateConfig{
			Enabled: true,
			Repo:    updateRepo,
		},
	}
}

const (
	// configName is the file the app reads and writes. legacyConfigName is what
	// it was called before the rename, and is still read when the new name is
	// absent so an existing install keeps its settings.
	configName       = "prflow.toml"
	legacyConfigName = "attpr.toml"

	// updateRepo is where self-update looks for releases.
	updateRepo = "JonrGull/prflow"
)

func configPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, configName), nil
}

// errNoConfigDir reports that the OS would not name a config directory, which
// Load treats the same as having no config file.
var errNoConfigDir = errors.New("no user config directory")

// readConfigFile reads the config, falling back to the pre-rename filename.
//
// The fallback is read-only: the next deliberate settings change writes
// prflow.toml, and the old file is left alone rather than deleted, so a
// downgrade still finds its config.
func readConfigFile() ([]byte, error) {
	path, err := configPath()
	if err != nil {
		return nil, errNoConfigDir
	}

	data, err := os.ReadFile(path)
	if err == nil {
		return data, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	legacy := filepath.Join(filepath.Dir(path), legacyConfigName)
	if data, legacyErr := os.ReadFile(legacy); legacyErr == nil {
		return data, nil
	}

	// Report the new path's error, since that is the file the app owns.
	return nil, err
}

// Path returns the config file path
func Path() (string, error) {
	return configPath()
}

func Load() (*Config, error) {
	data, err := readConfigFile()
	if err != nil {
		// No config directory and no config file are the same situation: start
		// from the defaults rather than refusing to run.
		if errors.Is(err, errNoConfigDir) || os.IsNotExist(err) {
			cfg := DefaultConfig()
			if err := cfg.compileRegex(); err != nil {
				return nil, err
			}
			// Deliberately not saved here. Writing a default config pointing at
			// a directory that probably does not exist, and then showing an
			// empty list, is what made a first run so baffling. The setup
			// screen writes it once the user has confirmed a path that works.
			cfg.firstRun = true
			return cfg, nil
		}
		return nil, err
	}

	cfg := DefaultConfig()
	defaultGlobs := cfg.Globs
	cfg.Globs = nil // clear so we can detect old-format configs after unmarshal
	defaultFlows := cfg.Flows
	cfg.Flows = nil // same, for configs written before [[flows]] existed
	defaultReposDir := cfg.Paths.ReposDir
	cfg.Paths.ReposDir = "" // same, so the old attuned_dir key can be spotted
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// Migrate the pre-rename paths.attuned_dir key.
	switch {
	case cfg.Paths.ReposDir != "":
	case cfg.Paths.AttunedDir != "":
		cfg.Paths.ReposDir = cfg.Paths.AttunedDir
	default:
		cfg.Paths.ReposDir = defaultReposDir
	}
	cfg.Paths.AttunedDir = "" // omitempty drops it on the next write

	// Migrate deprecated RepoEntry.Category → Group
	for i := range cfg.Repos {
		if cfg.Repos[i].Group == "" && cfg.Repos[i].Category != "" {
			cfg.Repos[i].Group = cfg.Repos[i].Category
			cfg.Repos[i].Category = ""
		}
	}

	// Migrate deprecated FrontendGlob/BackendGlob → [[globs]]
	if cfg.Paths.FrontendGlob != "" || cfg.Paths.BackendGlob != "" {
		if len(cfg.Globs) == 0 {
			if cfg.Paths.FrontendGlob != "" {
				cfg.Globs = append(cfg.Globs, GlobEntry{
					Pattern: cfg.Paths.FrontendGlob,
					Group:   "Frontend",
				})
			}
			if cfg.Paths.BackendGlob != "" {
				cfg.Globs = append(cfg.Globs, GlobEntry{
					Pattern: cfg.Paths.BackendGlob,
					Group:   "Backend",
				})
			}
		}
		cfg.Paths.FrontendGlob = ""
		cfg.Paths.BackendGlob = ""
	}

	// Apply defaults if no globs from config or migration
	if len(cfg.Globs) == 0 {
		cfg.Globs = defaultGlobs
	}

	// A config written before flows were configurable describes the old
	// hardcoded pair by omission.
	if len(cfg.Flows) == 0 {
		cfg.Flows = defaultFlows
	}

	if err := cfg.compileRegex(); err != nil {
		return nil, err
	}

	// Adopt update bookkeeping that older versions kept in the config file.
	cfg.state = LoadState()
	if cfg.state.migrateFrom(cfg.Update) {
		_ = cfg.state.Save() // best effort: losing it costs one extra check
	}
	cfg.Update.LastCheck = time.Time{}
	cfg.Update.SkippedVersion = ""

	return cfg, nil
}

func (c *Config) compileRegex() error {
	// Empty pattern = ticket extraction disabled
	if c.Tickets.Pattern == "" {
		c.ticketRegex = nil
		return nil
	}
	re, err := regexp.Compile("(?i)(" + c.Tickets.Pattern + ")")
	if err != nil {
		return fmt.Errorf("invalid tickets.pattern %q: %w", c.Tickets.Pattern, err)
	}
	c.ticketRegex = re
	return nil
}

// SetTicketPattern updates the ticket pattern and recompiles it.
//
// The compiled regex is derived state held alongside the pattern, so setting
// the field directly would leave the two disagreeing until the next load. An
// invalid pattern is rejected and the previous one kept, so a typo in the
// settings screen cannot silently disable ticket extraction.
func (c *Config) SetTicketPattern(pattern string) error {
	previous := c.Tickets.Pattern
	c.Tickets.Pattern = pattern
	if err := c.compileRegex(); err != nil {
		c.Tickets.Pattern = previous
		_ = c.compileRegex()
		return err
	}
	return nil
}

// TicketRegex returns the compiled ticket pattern regex (nil if disabled)
func (c *Config) TicketRegex() *regexp.Regexp {
	// Safe even if compileRegex() was never called
	return c.ticketRegex
}

func (c *Config) Save() error {
	path, err := configPath()
	if err != nil {
		return err
	}

	// Ensure config directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	data, err := toml.Marshal(c)
	if err != nil {
		return err
	}

	return writeFileAtomic(path, data, 0644)
}

func (c *Config) ReposPath() string {
	return expandTilde(c.Paths.ReposDir)
}

// ExplicitRepos returns configured repo entries with tilde-expanded paths
func (c *Config) ExplicitRepos() []models.ExplicitRepo {
	result := make([]models.ExplicitRepo, len(c.Repos))
	defaultGroup := "Backend"
	if len(c.Columns.Right) > 0 {
		defaultGroup = c.Columns.Right[0]
	}
	for i, r := range c.Repos {
		group := r.Group
		if group == "" {
			group = defaultGroup
		}
		result[i] = models.ExplicitRepo{
			Path:  expandTilde(r.Path),
			Group: group,
		}
	}
	return result
}

// GlobEntries converts config globs to models.GlobEntry slice
func (c *Config) GlobEntries() []models.GlobEntry {
	result := make([]models.GlobEntry, len(c.Globs))
	for i, g := range c.Globs {
		result[i] = models.GlobEntry{Pattern: g.Pattern, Group: g.Group}
	}
	return result
}

// FlowEntries returns the configured release flows.
func (c *Config) FlowEntries() []models.Flow {
	result := make([]models.Flow, len(c.Flows))
	for i, f := range c.Flows {
		result[i] = models.Flow{Head: f.Head, Base: f.Base, Title: f.Title}
	}
	return result
}

// LeftGroups returns a set of lowercase group names assigned to the left column
func (c *Config) LeftGroups() map[string]bool {
	m := make(map[string]bool)
	for _, g := range c.Columns.Left {
		m[strings.ToLower(g)] = true
	}
	return m
}

// ColumnName returns a display name for the given column (0=left, 1=right)
func (c *Config) ColumnName(column int) string {
	if column == 0 {
		return strings.Join(c.Columns.Left, " / ")
	}
	return strings.Join(c.Columns.Right, " / ")
}

// ShouldCheckForUpdate returns true if update check is enabled and 24h since last check
func (c *Config) ShouldCheckForUpdate() bool {
	if !c.Update.Enabled {
		return false
	}
	return time.Since(c.State().LastCheck) > 24*time.Hour
}

// RecordUpdateCheck stores the check time and persists it to the state file.
// This used to write the whole config back, destroying the user's comments on
// every single launch.
func (c *Config) RecordUpdateCheck() error {
	c.State().LastCheck = time.Now()
	return c.state.Save()
}

// SkippedVersion returns the release the user chose to skip, if any.
func (c *Config) SkippedVersion() string {
	return c.State().SkippedVersion
}

// SetSkippedVersion records a skipped release and persists it.
func (c *Config) SetSkippedVersion(tag string) error {
	c.State().SkippedVersion = tag
	return c.state.Save()
}

// State returns the machine-written state, loading it on first use.
func (c *Config) State() *State {
	if c.state == nil {
		c.state = LoadState()
	}
	return c.state
}

// LinearAPIKey reads the Linear API key from env or ~/.secrets
func (c *Config) LinearAPIKey() string {
	if key := os.Getenv("LINEAR_API_KEY"); key != "" {
		return key
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".secrets"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		// ~/.secrets is normally a shell-sourced file, so tolerate the shapes
		// it actually takes: `export KEY=v`, `KEY="v"`, `KEY='v'`.
		line = strings.TrimPrefix(line, "export ")
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "LINEAR_API_KEY=") {
			continue
		}
		value := strings.TrimPrefix(line, "LINEAR_API_KEY=")
		value = strings.TrimSpace(value)
		// Strip a matched pair of surrounding quotes, if present.
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		return value
	}
	return ""
}

func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
