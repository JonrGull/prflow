package config

import (
	"os"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// State holds machine-written values that used to live in the user's config
// file under [update].
//
// Save() marshals the whole Config struct back over the config file, which
// discards every comment and any hand-chosen formatting. That was tolerable for
// a deliberate settings edit, but last_check is rewritten on essentially every
// launch — so simply running the app destroyed the user's annotations. Moving
// the volatile values into their own file means the config is only ever touched
// when the user actually changes a setting.
type State struct {
	LastCheck      time.Time `toml:"last_check"`
	SkippedVersion string    `toml:"skipped_version"`
}

const (
	stateName       = "prflow-state.toml"
	legacyStateName = "attpr-state.toml"
)

func statePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, stateName), nil
}

// LoadState reads the state file. A missing or unreadable file yields a zero
// State: this is derived data, and losing it costs at most one extra update
// check.
func LoadState() *State {
	path, err := statePath()
	if err != nil {
		return &State{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// Fall back to the pre-rename filename, so an upgrade does not forget a
		// skipped version or trigger an immediate update check.
		data, err = os.ReadFile(filepath.Join(filepath.Dir(path), legacyStateName))
		if err != nil {
			return &State{}
		}
	}
	var s State
	if err := toml.Unmarshal(data, &s); err != nil {
		return &State{}
	}
	return &s
}

// Save writes the state file atomically.
func (s *State) Save() error {
	path, err := statePath()
	if err != nil {
		return err
	}
	data, err := toml.Marshal(s)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0644)
}

// migrateFrom adopts values that older versions stored in the config file, so
// a user upgrading does not get a spurious update check or lose a version they
// had chosen to skip.
func (s *State) migrateFrom(u UpdateConfig) bool {
	changed := false
	if s.LastCheck.IsZero() && !u.LastCheck.IsZero() {
		s.LastCheck = u.LastCheck
		changed = true
	}
	if s.SkippedVersion == "" && u.SkippedVersion != "" {
		s.SkippedVersion = u.SkippedVersion
		changed = true
	}
	return changed
}
