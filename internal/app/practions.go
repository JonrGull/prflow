package app

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JonrGull/prflow/internal/git"
	"github.com/JonrGull/prflow/internal/github"

	tea "github.com/charmbracelet/bubbletea"
)

// The All PRs screen's actions on the highlighted PR: merge it, re-run its
// failed checks, or check it out into a worktree. A merge or re-run is checked
// with GitHub first and then waits for y, so nothing is written until then.

type prActionKind int

const (
	prMerge prActionKind = iota
	prRerun
)

// prAction is a checked action waiting for y.
type prAction struct {
	kind   prActionKind
	entry  allPREntry
	method string             // merge: MERGE, SQUASH or REBASE
	runs   []github.FailedRun // re-run
}

func prRef(e allPREntry) string { return fmt.Sprintf("%s#%d", e.Repo.ShortName(), e.PR.Number) }

// prompt says what y will do.
func (a prAction) prompt() string {
	if a.kind == prMerge {
		return fmt.Sprintf("Merge %s into %s (%s)", prRef(a.entry), a.entry.PR.BaseBranch, strings.ToLower(a.method))
	}
	var names []string
	for i, r := range a.runs {
		if i == 2 {
			names = append(names, fmt.Sprintf("%d more", len(a.runs)-i))
			break
		}
		names = append(names, truncateString(r.Name, 30))
	}
	return fmt.Sprintf("Re-run failed jobs in %s (%s)", strings.Join(names, ", "), prRef(a.entry))
}

// prNWO is the owner/repo a PR belongs to, from its URL. A fork's PR belongs to
// the repo it asks to merge into, which is the one its URL names.
func prNWO(url string) string {
	parts := strings.Split(strings.TrimPrefix(url, "https://github.com/"), "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// prCheckResult answers a merge or re-run check: the action to confirm, or why
// it cannot go ahead.
type prCheckResult struct {
	action  *prAction
	refusal string
	err     error
}

func (prCheckResult) flowResult() {}

// prActionDoneResult reports a merge, re-run or checkout. It is not a
// flowResult: the write has happened whether or not the screen is still up,
// and the footer says so wherever the user now is.
type prActionDoneResult struct {
	text    string      // what happened
	copy    string      // a path to put on the clipboard
	merged  *allPREntry // for the session history
	refresh bool        // the list has changed
	err     error
}

// mergeBlockers say why GitHub would not merge a PR now, by mergeStateStatus.
var mergeBlockers = map[string]string{
	"DIRTY":    "has conflicts",
	"BLOCKED":  "is blocked: a required review or check is missing",
	"BEHIND":   "is behind its base branch, which the repo requires it to be up to date with",
	"UNSTABLE": "has failing checks",
	"DRAFT":    "is a draft",
	"UNKNOWN":  "is still being checked by GitHub; try again in a moment",
}

var prChecks = map[string]func(allPREntry, bool) tea.Cmd{
	"m": prMergeCheckCmd,
	"R": prRerunCheckCmd,
}

// startPRAction checks the highlighted PR before a merge or re-run, or starts
// a checkout, which writes nothing on GitHub and so goes straight ahead.
func (m Model) startPRAction(key string) (tea.Model, tea.Cmd) {
	e, ok := m.highlightedPR()
	if !ok || m.allPRs.checking != "" || m.allPRs.running != "" {
		return m, nil
	}
	ref := prRef(e)
	check, ok := prChecks[key]
	if !ok {
		m.allPRs.running = "checking out " + ref
		return m, prWorktreeCmd(e, m.dryRun)
	}
	if e.PR.HeadSHA == "" {
		m.copyFeedback = "✗ No head commit recorded for " + ref + "; press r to refresh"
		return m, nil
	}
	m.allPRs.checking = ref
	return m, check(e, m.dryRun)
}

func prMergeCheckCmd(e allPREntry, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		var check github.MergeCheck
		var err error
		if dryRun {
			check = dryRunMergeCheck(e)
		} else {
			nwo := prNWO(e.PR.URL)
			check, err = github.CheckMerge(nwo, e.PR.Number)
			// The first request for a PR's state gets UNKNOWN while GitHub works it out.
			if err == nil && check.Status == "UNKNOWN" {
				time.Sleep(homeMergeStateRetry)
				check, err = github.CheckMerge(nwo, e.PR.Number)
			}
		}
		if err != nil {
			return prCheckResult{err: err}
		}
		return mergeVerdict(e, check)
	}
}

// mergeVerdict decides a merge from GitHub's answer. The head must be the one
// the list showed, since that is what the user looked at.
func mergeVerdict(e allPREntry, check github.MergeCheck) prCheckResult {
	ref := prRef(e)
	switch {
	case check.HeadSHA != e.PR.HeadSHA:
		return prCheckResult{refusal: ref + " has new commits since the list loaded; press r and look at them first"}
	case !readyMergeStates[check.Status]:
		reason, known := mergeBlockers[check.Status]
		if !known {
			reason = "can't be merged now (" + strings.ToLower(check.Status) + ")"
		}
		return prCheckResult{refusal: ref + " " + reason}
	}
	return prCheckResult{action: &prAction{kind: prMerge, entry: e, method: check.Method}}
}

func prRerunCheckCmd(e allPREntry, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		var runs []github.FailedRun
		var err error
		if dryRun {
			runs = dryRunFailedRuns(e)
		} else {
			runs, err = github.FailedRunsFor(prNWO(e.PR.URL), e.PR.HeadSHA)
		}
		if err != nil {
			return prCheckResult{err: err}
		}
		return rerunVerdict(e, runs)
	}
}

func rerunVerdict(e allPREntry, runs []github.FailedRun) prCheckResult {
	ref := prRef(e)
	switch {
	case len(runs) > 0:
		return prCheckResult{action: &prAction{kind: prRerun, entry: e, runs: runs}}
	case e.CIStatus == "failure":
		return prCheckResult{refusal: ref + " has no failed Actions run to re-run: its failing checks are from another app or still running"}
	}
	return prCheckResult{refusal: "Nothing failed on " + ref + "'s latest commit"}
}

func (m Model) handlePRCheck(msg prCheckResult) (tea.Model, tea.Cmd) {
	m.allPRs.checking = ""
	switch {
	case msg.err != nil:
		m.copyFeedback = "✗ " + firstLine(msg.err.Error())
	case msg.refusal != "":
		m.copyFeedback = "✗ " + msg.refusal
	default:
		m.allPRs.pending = msg.action
	}
	return m, nil
}

// runPRAction does what the user confirmed with y.
func (m Model) runPRAction(a prAction) (tea.Model, tea.Cmd) {
	ref, nwo, dry := prRef(a.entry), prNWO(a.entry.PR.URL), m.dryRun
	if a.kind == prMerge {
		m.allPRs.running = "merging " + ref
		return m, func() tea.Msg {
			if dry {
				time.Sleep(dryRunNormal)
				return prActionDoneResult{text: "Dry run: " + ref + " not merged", refresh: true}
			}
			if err := github.MergePRBy(nwo, a.entry.PR.Number, a.entry.PR.HeadSHA, a.method); err != nil {
				return prActionDoneResult{err: fmt.Errorf("%s: %w", ref, err)}
			}
			return prActionDoneResult{text: "Merged " + ref, merged: &a.entry, refresh: true}
		}
	}
	m.allPRs.running = "re-running " + ref
	return m, func() tea.Msg {
		if dry {
			time.Sleep(dryRunNormal)
			return prActionDoneResult{text: "Dry run: nothing re-run on " + ref}
		}
		var errs []error
		for _, r := range a.runs {
			if err := github.RerunFailedJobs(nwo, r.ID); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			return prActionDoneResult{err: fmt.Errorf("%s: %d of %d not re-run: %w",
				ref, len(errs), len(a.runs), errors.Join(errs...)), refresh: len(errs) < len(a.runs)}
		}
		return prActionDoneResult{text: fmt.Sprintf("Re-running %d run%s on %s", len(a.runs), pluralS(len(a.runs)), ref), refresh: true}
	}
}

// prWorktreeCmd checks a PR out into its own worktree of the repo, so the
// checkout the user works in, and anything uncommitted there, is untouched.
func prWorktreeCmd(e allPREntry, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		ref := prRef(e)
		if dryRun {
			time.Sleep(dryRunNormal)
			return prActionDoneResult{text: "Dry run: " + ref + " would go in " + git.PRWorktreePath(e.Repo.Path, e.PR.Number)}
		}
		main, err := git.MainWorktree(e.Repo.Path)
		if err != nil {
			return prActionDoneResult{err: err}
		}
		dir := git.PRWorktreePath(main, e.PR.Number)
		if _, err := os.Stat(dir); err == nil {
			return prActionDoneResult{text: ref + " is already checked out at " + dir, copy: dir}
		}
		if path, ok := git.WorktreeWithBranch(main, e.PR.HeadBranch); ok && !e.PR.IsCrossRepository {
			return prActionDoneResult{text: ref + "'s branch is already checked out at " + path, copy: path}
		}
		if err := git.AddWorktree(main, dir); err != nil {
			return prActionDoneResult{err: fmt.Errorf("%s: %w", ref, err)}
		}
		if err := github.CheckoutPR(dir, prNWO(e.PR.URL), e.PR.Number); err != nil {
			_ = git.RemoveWorktree(main, dir)
			return prActionDoneResult{err: fmt.Errorf("%s: %w", ref, err)}
		}
		return prActionDoneResult{text: "Checked " + ref + " out at " + dir, copy: dir}
	}
}

func (m Model) handlePRActionDone(msg prActionDoneResult) (tea.Model, tea.Cmd) {
	m.allPRs.running = ""
	if msg.err != nil {
		m.copyFeedback = "✗ " + firstLine(msg.err.Error())
	} else {
		m.copyFeedback = "✓ " + msg.text
		if msg.copy != "" && copyToClipboard(msg.copy) == nil {
			m.copyFeedback += " (path copied)"
		}
	}
	if e := msg.merged; e != nil {
		m.recordSessionPR(e.Repo.DisplayName, e.PR.URL, e.PR.HeadBranch+" → "+e.PR.BaseBranch, "merged", e.PR.Number)
	}
	if msg.refresh && m.screen == ScreenViewAllPrs && !m.allPRs.loading {
		m.allPRs.loading = true
		return m, fetchAllOpenPRsCmd(m.config, m.dryRun)
	}
	return m, nil
}

// firstLine keeps a multi-line error, like gh's, to the one line a footer has.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
