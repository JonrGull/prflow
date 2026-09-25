package github

import (
	"testing"
)

// A commit is associated with every PR that carried it, a release PR included,
// so the PR that merged it is the one whose merge commit it is. A rollback's
// commits come from the reverse comparison.
func TestParseReleaseDiff(t *testing.T) {
	out := []byte(`{"data": {"repository": {
		"shipped": {"compare": {"status": "DIVERGED", "aheadBy": 1, "behindBy": 1, "commits": {"nodes": [
			{"oid": "c1", "messageHeadline": "Fix (#9)", "associatedPullRequests": {"nodes": [
				{"number": 20, "title": "dev → staging", "headRefName": "dev", "mergeCommit": {"oid": "m20"}},
				{"number": 9, "title": "Fix", "headRefName": "fix", "mergeCommit": {"oid": "c1"}}
			]}}
		]}}},
		"removed": {"compare": {"commits": {"nodes": [
			{"oid": "c0", "messageHeadline": "Old thing", "associatedPullRequests": {"nodes": []}}
		]}}}
	}}}`)
	d, err := parseReleaseDiff(out)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "DIVERGED" || d.ShippedTotal != 1 || d.RemovedTotal != 1 {
		t.Errorf("diff = %+v", d)
	}
	if len(d.Shipped) != 1 || d.Shipped[0].PR == nil || d.Shipped[0].PR.Number != 9 {
		t.Errorf("shipped = %+v, want c1 credited to #9, which merged it", d.Shipped)
	}
	if len(d.Removed) != 1 || d.Removed[0].SHA != "c0" || d.Removed[0].PR != nil {
		t.Errorf("removed = %+v, want c0 with no PR", d.Removed)
	}
}

func TestParseReleasesSkipsDraftsAndLostTags(t *testing.T) {
	out := []byte(`{"data": {"r0": {"releases": {"nodes": [
		{"tagName": "v3", "isDraft": true, "tagCommit": null},
		{"tagName": "v2", "publishedAt": "2026-09-24T07:21:01Z", "url": "u2", "tagCommit": {"oid": "s2"}},
		{"tagName": "v1", "publishedAt": "2026-09-20T07:21:01Z", "tagCommit": null},
		{"tagName": "v0", "publishedAt": "2026-09-10T07:21:01Z", "isPrerelease": true, "tagCommit": {"oid": "s0"}}
	]}}}}`)
	got, err := parseReleases(out, []string{"acme/web"})
	if err != nil {
		t.Fatal(err)
	}
	rs := got["acme/web"]
	if len(rs) != 2 || rs[0].Tag != "v2" || rs[0].SHA != "s2" || rs[1].Tag != "v0" || !rs[1].Prerelease {
		t.Errorf("releases = %+v, want v2 and the prerelease v0", rs)
	}
}
