package app

import (
	"sync"

	"github.com/JonrGull/prflow/internal/config"
	"github.com/JonrGull/prflow/internal/git"
	"github.com/JonrGull/prflow/internal/github"
	"github.com/JonrGull/prflow/internal/models"
)

// There is deliberately no TTL: the cache is keyed on the config values that
// affect discovery, so any settings change invalidates it automatically.
//
// What it does not have is a general user-driven refresh. Only the merge and
// all-PRs screens call invalidateRepoCache from their `r` key; Batch uses `r`
// as filter text, Pull has no refresh binding, and the Actions auto-refresh
// re-reads the cache without dropping it. So a repo cloned, moved or deleted
// mid-session stays invisible on those screens until a settings change or a
// restart. That is a gap rather than a design — adding a refresh to Batch and
// Pull needs a spare key on each and is worth doing on its own.

var (
	repoCacheMu   sync.Mutex
	repoCacheKey  string
	repoCacheVal  []models.RepoInfo
	repoCacheErr  error
	repoCacheGood bool
)

// discoverRepos returns the configured repositories, memoised.
//
// git.FindRepos walks the filesystem and opens every candidate with
// go-git. It was called from five places with no caching, so entering any list
// screen re-globbed the whole tree — noticeable with many repos, and pure
// waste when switching tabs back and forth.
func discoverRepos(cfg *config.Config) ([]models.RepoInfo, error) {
	key := repoCacheKeyFor(cfg)

	repoCacheMu.Lock()
	defer repoCacheMu.Unlock()

	if repoCacheGood && repoCacheKey == key {
		return repoCacheVal, repoCacheErr
	}

	repos, err := git.FindRepos(cfg.ReposPath(), cfg.GlobEntries(), cfg.ExplicitRepos())
	releaseTargets(repos, cfg.FlowEntries(), git.GuessMainBranch)

	repoCacheKey = key
	repoCacheVal = repos
	repoCacheErr = err
	repoCacheGood = true
	return repos, err
}

// invalidateRepoCache forces the next discoverRepos call to re-scan. Called
// when the user explicitly refreshes or changes the paths in settings.
func invalidateRepoCache() {
	repoCacheMu.Lock()
	defer repoCacheMu.Unlock()
	repoCacheGood = false
}

// repoCacheKeyFor derives a key from the inputs that affect discovery, so a
// config change invalidates the cache without anyone remembering to.
func repoCacheKeyFor(cfg *config.Config) string {
	key := cfg.ReposPath()
	for _, g := range cfg.GlobEntries() {
		key += "\x00g:" + g.Pattern + "=" + g.Group
	}
	for _, r := range cfg.ExplicitRepos() {
		key += "\x00r:" + r.Path + "=" + r.Group
	}
	for _, f := range cfg.FlowEntries() {
		key += "\x00f:" + f.HeadBranch()
	}
	return key
}

// releaseTargets resolves @default for a repo whose default branch is one the
// chain releases from, like dev: to main or master, since a chain that ends
// where it starts would release staging into dev.
func releaseTargets(repos []models.RepoInfo, flows []models.Flow, guess func(path string) string) {
	heads := map[string]bool{}
	for _, f := range flows {
		heads[f.HeadBranch()] = true
	}
	for i := range repos {
		if heads[repos[i].MainBranch] {
			repos[i].MainBranch = guess(repos[i].Path)
		}
	}
}

// parallelMap applies fn to every item concurrently and returns the results in
// input order.
//
// This shape — WaitGroup, a buffered channel, a goroutine to close it, then a
// drain loop — was written out longhand five times in commands.go with a
// bespoke result struct each time. Preserving input order (rather than
// draining a channel as results land) also makes the resulting lists stable
// between refreshes, which the channel version did not guarantee.
func parallelMap[T, R any](items []T, fn func(T) R) []R {
	results := make([]R, len(items))
	var wg sync.WaitGroup
	for i, item := range items {
		wg.Add(1)
		go func(i int, item T) {
			defer wg.Done()
			results[i] = fn(item)
		}(i, item)
	}
	wg.Wait()
	return results
}

// repoNWO is a discovered repo with its GitHub owner/repo.
type repoNWO struct {
	Repo models.RepoInfo
	NWO  string
}

// githubRepos looks up each repo's owner/repo, leaving out repos with no GitHub
// remote.
func githubRepos(repos []models.RepoInfo) []repoNWO {
	return uniqueGitHubRepos(parallelMap(repos, func(r models.RepoInfo) repoNWO {
		nwo, _ := github.GetRepoNWO(r.Path) // "" for a repo not on GitHub
		return repoNWO{Repo: r, NWO: nwo}
	}))
}

// uniqueGitHubRepos keeps the first checkout of each GitHub repo: a worktree
// shares its owner/repo, and counting it again doubled that repo's numbers.
func uniqueGitHubRepos(repos []repoNWO) []repoNWO {
	var out []repoNWO
	seen := map[string]bool{}
	for _, r := range repos {
		if r.NWO != "" && !seen[r.NWO] {
			seen[r.NWO] = true
			out = append(out, r)
		}
	}
	return out
}
