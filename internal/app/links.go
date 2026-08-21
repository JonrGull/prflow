package app

import (
	"fmt"
	"strings"
)

// namedLink pairs a display name with a URL.
//
// Four screens offer "open all" and "copy as a markdown list" over some
// collection of PRs. Each built its own []string of URLs and its own
// `- name: url` formatting inline, so the same two loops appeared nine times
// between them — and the merge summary ran its O(n²) result-to-PR lookup twice
// in a row, once per action.
type namedLink struct {
	Name string
	URL  string
}

// urls extracts just the URLs, for openURLs.
func urls(links []namedLink) []string {
	out := make([]string, 0, len(links))
	for _, l := range links {
		out = append(out, l.URL)
	}
	return out
}

// markdownList renders links as `- name: url` lines.
func markdownList(links []namedLink) string {
	lines := make([]string, 0, len(links))
	for _, l := range links {
		lines = append(lines, fmt.Sprintf("- %s: %s", l.Name, l.URL))
	}
	return strings.Join(lines, "\n")
}

// batchPRLinks returns links for the PRs created by a batch run.
func (m Model) batchPRLinks() []namedLink {
	var links []namedLink
	for _, result := range m.batch.results {
		if result.PrURL != nil {
			links = append(links, namedLink{result.Repo.DisplayName, *result.PrURL})
		}
	}
	return links
}

// openPRLinks returns links for every open release PR currently listed.
func (m Model) openPRLinks() []namedLink {
	var links []namedLink
	for _, pr := range m.merge.prs {
		links = append(links, namedLink{pr.Repo.DisplayName, pr.URL})
	}
	return links
}

// mergedPRLinks returns links for the PRs that merged successfully.
// The merge results carry a repo name and PR number rather than a URL, so the
// URL has to be looked back up in the original list.
func (m Model) mergedPRLinks() []namedLink {
	byKey := make(map[string]string, len(m.merge.prs))
	for _, pr := range m.merge.prs {
		byKey[mergeKey(pr.Repo.DisplayName, pr.PrNumber)] = pr.URL
	}

	var links []namedLink
	for _, result := range m.merge.results {
		if !result.Success {
			continue
		}
		if url, ok := byKey[mergeKey(result.RepoName, result.PrNumber)]; ok {
			links = append(links, namedLink{result.RepoName, url})
		}
	}
	return links
}

func mergeKey(repoName string, prNumber uint64) string {
	return fmt.Sprintf("%s#%d", repoName, prNumber)
}

// copyLinks copies links as a markdown list, reporting via the status bar.
func (m *Model) copyLinks(links []namedLink, successMsg string) {
	if len(links) == 0 {
		return
	}
	m.copyWithFeedback(markdownList(links), successMsg)
}
