package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JonrGull/prflow/internal/models"
)

// The defaults are the conventional two-step chain and nothing more: no
// company's ticket prefix, no company's sprint-title convention. The chain
// itself still matches what the old hardcoded enum did, so a config written
// before [[flows]] existed keeps creating the same two PRs.
func TestFlowDefaultsAreTheConventionalChain(t *testing.T) {
	got := DefaultConfig().FlowEntries()
	want := []models.Flow{
		{Head: "dev", Base: "staging"},
		{Head: "staging", Base: models.DefaultBranchToken},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d flows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("flow %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// And the derived strings have to match what the enum produced.
	if s := got[0].Display("main"); s != "dev → staging" {
		t.Errorf("first flow displays as %q", s)
	}
	if s := got[1].Display("master"); s != "staging → master" {
		t.Errorf("@default did not resolve to the repo's own branch: %q", s)
	}
	if s := got[0].DefaultTitle("main"); s != "dev → staging" {
		t.Errorf("a flow with no title should fall back to its description, got %q", s)
	}

	// A title is still a per-step setting, just not a defaulted one.
	titled := models.Flow{Head: "staging", Base: models.DefaultBranchToken, Title: "Release "}
	if s := titled.DefaultTitle("main"); s != "Release " {
		t.Errorf("an explicit title should seed the input verbatim, got %q", s)
	}
}

// Nothing in the shipped defaults should name one company.
func TestDefaultsCarryNoOrganisationSpecificValues(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Tickets.LinearOrg != "" {
		t.Errorf("default linear_org = %q, want empty", cfg.Tickets.LinearOrg)
	}
	for _, f := range cfg.Flows {
		if f.Title != "" {
			t.Errorf("flow %s→%s ships a default title %q", f.Head, f.Base, f.Title)
		}
	}
	// The ticket pattern has to match a generic Linear-style key, not one prefix.
	if err := cfg.compileRegex(); err != nil {
		t.Fatalf("default ticket pattern does not compile: %v", err)
	}
	re := cfg.TicketRegex()
	for _, id := range []string{"ATT-1234", "ENG-7", "PROJ2-99"} {
		if !re.MatchString(id) {
			t.Errorf("default pattern does not match %q", id)
		}
	}
	// Case-insensitively, by design — a commit may write the key in lower case.
	if !re.MatchString("fixes att-1234") {
		t.Error("default pattern should match a lowercase key")
	}
	// The known cost of a prefix-agnostic default, recorded rather than
	// discovered: technical tokens of the same shape match too. Narrowing the
	// pattern to a real project prefix is the fix, and the docs say so.
	if !re.MatchString("switch to UTF-8") {
		t.Log("note: the generic default also matches lookalikes like UTF-8")
	}
	for _, notATicket := range []string{"no ticket here", "1234", "v1.2.3"} {
		if re.MatchString(notATicket) {
			t.Errorf("default pattern matched %q", notATicket)
		}
	}
}

// A config file predating [[flows]] describes the old pair by omission.
func TestLoadMigratesAConfigWithNoFlows(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	old := "[paths]\nrepos_dir = '/tmp'\n\n[[globs]]\npattern = 'x/*'\ngroup = 'X'\n"
	if err := os.WriteFile(filepath.Join(dir, configName), []byte(old), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Flows) != 2 {
		t.Fatalf("a config with no [[flows]] got %d flows, want the default 2: %+v", len(cfg.Flows), cfg.Flows)
	}
	// The user's own globs must survive the flow defaulting.
	if len(cfg.Globs) != 1 || cfg.Globs[0].Pattern != "x/*" {
		t.Errorf("globs were clobbered: %+v", cfg.Globs)
	}
}

// An explicit [[flows]] block replaces the defaults rather than adding to them.
func TestLoadKeepsExplicitFlows(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	written := "[paths]\nrepos_dir = '/tmp'\n\n[[flows]]\nhead = 'develop'\nbase = '@default'\n"
	if err := os.WriteFile(filepath.Join(dir, configName), []byte(written), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Flows) != 1 {
		t.Fatalf("got %d flows, want just the configured one: %+v", len(cfg.Flows), cfg.Flows)
	}
	if f := cfg.FlowEntries()[0]; f.Head != "develop" || f.BaseBranch("trunk") != "trunk" {
		t.Errorf("configured flow round-tripped as %+v", f)
	}
}

// A broken flow is not a crash — it is a PR that never appears. Each of these
// used to be expressible only by editing Go.
func TestValidateFlows(t *testing.T) {
	tests := []struct {
		name    string
		flows   []FlowEntry
		wantMsg string
		isError bool
	}{
		{"none at all", nil, "no release flows configured", true},
		{"no head", []FlowEntry{{Base: "main"}}, "has no head branch", true},
		{"no base", []FlowEntry{{Head: "dev"}}, "has no base branch", true},
		{"neither", []FlowEntry{{}}, "has no head or base branch", true},
		{"merges into itself", []FlowEntry{{Head: "main", Base: "main"}}, "into itself", true},
		{
			"duplicate pair",
			[]FlowEntry{{Head: "dev", Base: "staging"}, {Head: "dev", Base: "staging"}},
			"duplicates an earlier flow", false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Flows: tc.flows}
			diags := c.validateFlows()
			if !hasMessage(diags, tc.wantMsg) {
				t.Errorf("expected a diagnostic mentioning %q, got %v", tc.wantMsg, diags)
			}
			if got := HasErrors(diags); got != tc.isError {
				t.Errorf("HasErrors = %v, want %v (%v)", got, tc.isError, diags)
			}
		})
	}

	t.Run("a valid set is quiet", func(t *testing.T) {
		c := &Config{Flows: []FlowEntry{
			{Head: "dev", Base: "staging"},
			{Head: "staging", Base: models.DefaultBranchToken},
		}}
		if diags := c.validateFlows(); len(diags) != 0 {
			t.Errorf("expected no diagnostics, got %v", diags)
		}
	})
}
