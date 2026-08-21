package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JonrGull/prflow/internal/models"
)

// Severity distinguishes a config that cannot work from one that probably
// isn't what the user meant.
type Severity int

const (
	// SeverityWarning: the app will run, but something is likely misconfigured.
	SeverityWarning Severity = iota
	// SeverityError: this will produce an empty or broken experience.
	SeverityError
)

// Diagnostic is one problem found in the config, with a concrete suggestion.
type Diagnostic struct {
	Severity Severity
	Field    string // the TOML path, e.g. "paths.repos_dir"
	Message  string
	Fix      string // what the user should do about it
}

func (d Diagnostic) String() string {
	if d.Fix == "" {
		return fmt.Sprintf("%s: %s", d.Field, d.Message)
	}
	return fmt.Sprintf("%s: %s — %s", d.Field, d.Message, d.Fix)
}

// Validate checks the config for the mistakes that previously failed silently.
//
// Until now the only validation was compiling the ticket regex. A base
// directory that did not exist, a glob matching nothing, or a group name
// assigned to no column all produced the same symptom — an empty list with no
// explanation — which is what made a mistyped path so hard to diagnose.
func (c *Config) Validate() []Diagnostic {
	var diags []Diagnostic

	diags = append(diags, c.validateFlows()...)

	base := c.ReposPath()
	hasExplicit := len(c.Repos) > 0

	switch {
	case base == "" && !hasExplicit:
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Field:    "paths.repos_dir",
			Message:  "not set, and no [[repos]] entries are configured",
			Fix:      "set it to the directory containing your repositories",
		})
	case base != "":
		info, err := os.Stat(base)
		switch {
		case err != nil:
			sev := SeverityError
			if hasExplicit {
				sev = SeverityWarning // explicit repos can still be discovered
			}
			diags = append(diags, Diagnostic{
				Severity: sev,
				Field:    "paths.repos_dir",
				Message:  fmt.Sprintf("%q does not exist", base),
				Fix:      "correct the path, or remove it if you only use [[repos]]",
			})
		case !info.IsDir():
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Field:    "paths.repos_dir",
				Message:  fmt.Sprintf("%q is a file, not a directory", base),
			})
		default:
			diags = append(diags, c.validateGlobs(base)...)
		}
	}

	diags = append(diags, c.validateExplicitRepos()...)
	diags = append(diags, c.validateColumns()...)

	if c.Tickets.Pattern != "" && c.ticketRegex == nil {
		diags = append(diags, Diagnostic{
			Severity: SeverityWarning,
			Field:    "tickets.pattern",
			Message:  "could not be compiled, so ticket extraction is disabled",
		})
	}

	return diags
}

func (c *Config) validateGlobs(base string) []Diagnostic {
	var diags []Diagnostic

	if len(c.Globs) == 0 && len(c.Repos) == 0 {
		return append(diags, Diagnostic{
			Severity: SeverityError,
			Field:    "globs",
			Message:  "no glob patterns configured, so no repos will be found",
			Fix:      "add a [[globs]] entry, e.g. pattern = '*' with a group name",
		})
	}

	seen := make(map[string]bool)
	for _, g := range c.Globs {
		if seen[g.Pattern] {
			diags = append(diags, Diagnostic{
				Severity: SeverityWarning,
				Field:    "globs",
				Message:  fmt.Sprintf("pattern %q is listed more than once", g.Pattern),
			})
			continue
		}
		seen[g.Pattern] = true

		if g.Group == "" {
			diags = append(diags, Diagnostic{
				Severity: SeverityWarning,
				Field:    "globs",
				Message:  fmt.Sprintf("pattern %q has no group", g.Pattern),
				Fix:      "add a group name so it can be assigned to a column",
			})
		}

		matches, err := filepath.Glob(filepath.Join(base, g.Pattern))
		if err != nil {
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Field:    "globs",
				Message:  fmt.Sprintf("pattern %q is not a valid glob: %v", g.Pattern, err),
			})
			continue
		}
		if len(matches) == 0 {
			diags = append(diags, Diagnostic{
				Severity: SeverityWarning,
				Field:    "globs",
				Message:  fmt.Sprintf("pattern %q matches nothing under %s", g.Pattern, base),
			})
		}
	}

	return diags
}

func (c *Config) validateExplicitRepos() []Diagnostic {
	var diags []Diagnostic
	for _, r := range c.ExplicitRepos() {
		if info, err := os.Stat(r.Path); err != nil || !info.IsDir() {
			diags = append(diags, Diagnostic{
				Severity: SeverityWarning,
				Field:    "repos",
				Message:  fmt.Sprintf("%q does not exist", r.Path),
			})
		}
	}
	return diags
}

// validateColumns catches the failure that is hardest to spot by eye: a group
// listed in neither column. RepoInfo.InColumn treats "not in columns.left" as
// "right", so a typo silently dumps those repos into the right-hand column
// rather than reporting anything.
func (c *Config) validateColumns() []Diagnostic {
	var diags []Diagnostic

	columns := append(append([]string{}, c.Columns.Left...), c.Columns.Right...)

	assigned := make(map[string]bool)
	for _, g := range columns {
		assigned[strings.ToLower(g)] = true
	}

	// Every group a repo could land in, indexed case-insensitively to match
	// LeftGroups, which is what actually decides the column at runtime.
	known := make(map[string]bool)
	for _, g := range c.KnownGroups() {
		known[strings.ToLower(g)] = true
	}

	for _, g := range c.KnownGroups() {
		if !assigned[strings.ToLower(g)] {
			diags = append(diags, Diagnostic{
				Severity: SeverityWarning,
				Field:    "columns",
				Message:  fmt.Sprintf("group %q is not listed in columns.left or columns.right", g),
				Fix:      "add it to one of them, otherwise its repos fall into the right column",
			})
		}
	}

	// And the reverse: a column naming a group nothing produces. This compares
	// case-insensitively too — it previously did not, so a column entry of
	// "frontend" against a glob group of "Frontend" worked correctly at runtime
	// while being reported here as having no repos.
	for _, g := range columns {
		if !known[strings.ToLower(g)] {
			diags = append(diags, Diagnostic{
				Severity: SeverityWarning,
				Field:    "columns",
				Message:  fmt.Sprintf("group %q has no repos", g),
				Fix:      "check the spelling against your [[globs]] and [[repos]] groups",
			})
		}
	}

	return diags
}

// KnownGroups returns every group name a discovered repo could end up in,
// gathered from the glob entries and the explicit repos, sorted.
//
// This is the set a column assignment has to match, so the settings editor can
// show it while a group name is being typed rather than reporting the typo
// afterwards.
func (c *Config) KnownGroups() []string {
	seen := make(map[string]bool)
	var groups []string

	add := func(g string) {
		if g == "" || seen[strings.ToLower(g)] {
			return
		}
		seen[strings.ToLower(g)] = true
		groups = append(groups, g)
	}

	for _, g := range c.Globs {
		add(g.Group)
	}
	for _, r := range c.ExplicitRepos() {
		add(r.Group)
	}

	sort.Strings(groups)
	return groups
}

// HasErrors reports whether any diagnostic is fatal to normal operation.
func HasErrors(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

// validateFlows checks the release flows.
//
// A flow with a missing branch, or one that merges a branch into itself, is not
// a runtime error — it is a PR that silently never gets created, which is the
// class of failure this whole validator exists to convert into a sentence.
func (c *Config) validateFlows() []Diagnostic {
	if len(c.Flows) == 0 {
		return []Diagnostic{{
			Severity: SeverityError,
			Field:    "flows",
			Message:  "no release flows configured",
			Fix:      `add a [[flows]] entry, e.g. head = "dev", base = "staging"`,
		}}
	}

	var diags []Diagnostic
	seen := map[string]bool{}

	for i, f := range c.Flows {
		head := strings.TrimSpace(f.Head)
		base := strings.TrimSpace(f.Base)
		label := fmt.Sprintf("flows[%d]", i)

		switch {
		case head == "" && base == "":
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Field: label,
				Message: "has no head or base branch",
				Fix:     "give it both, or remove the entry",
			})
			continue
		case head == "":
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Field: label,
				Message: fmt.Sprintf("has no head branch (base %q)", base),
				Fix:     "name the branch being merged",
			})
			continue
		case base == "":
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Field: label,
				Message: fmt.Sprintf("has no base branch (head %q)", head),
				Fix:     fmt.Sprintf("name the branch to merge into, or %s for the repo's own default", models.DefaultBranchToken),
			})
			continue
		}

		if head == base {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Field: label,
				Message: fmt.Sprintf("merges %q into itself", head),
				Fix:     "point base at a different branch",
			})
			continue
		}

		// Two flows with the same pair would create the same PR twice and show
		// it in two columns.
		key := head + "\x00" + base
		if seen[key] {
			diags = append(diags, Diagnostic{
				Severity: SeverityWarning, Field: label,
				Message: fmt.Sprintf("duplicates an earlier flow (%s → %s)", head, base),
				Fix:     "remove one of them",
			})
		}
		seen[key] = true
	}

	return diags
}
