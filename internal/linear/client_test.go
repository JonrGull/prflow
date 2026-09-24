package linear

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeLinear answers each operation by a keyword in its query. A value that
// starts with "error:" is returned as a GraphQL error.
func fakeLinear(t *testing.T, answers map[string]string) *[]graphqlRequest {
	t.Helper()
	var seen []graphqlRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req graphqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("bad request: %v", err)
		}
		seen = append(seen, req)
		for key, answer := range answers {
			if !strings.Contains(req.Query, key) {
				continue
			}
			if msg, ok := strings.CutPrefix(answer, "error:"); ok {
				json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"message": msg}}})
				return
			}
			w.Write([]byte(`{"data":` + answer + `}`))
			return
		}
		t.Errorf("unexpected query: %s", req.Query)
	}))
	t.Cleanup(srv.Close)
	orig := apiURL
	apiURL = srv.URL
	t.Cleanup(func() { apiURL = orig })
	return &seen
}

const (
	issueFound    = `{"issue":{"id":"issue-uuid"}}`
	commentPosted = `{"commentCreate":{"success":true}}`
)

// The subscribe call failed on every ticket and the error was discarded, so
// QA tagging reported success while the QA person was never subscribed.
func TestQATaggingReportsAFailedSubscribe(t *testing.T) {
	fakeLinear(t, map[string]string{
		"issue(id:":      issueFound,
		"issueSubscribe": "error:Entity not found: User",
		"commentCreate":  commentPosted,
	})

	r := TagTicketsForQA("key", []string{"PROJ-1"}, "qa.person", "user-uuid", "staging", "https://pr")[0]

	if !r.Success {
		t.Errorf("Success = false, want true: the comment still posted")
	}
	if !strings.Contains(r.Warning, "not subscribed") || !strings.Contains(r.Warning, "Entity not found") {
		t.Errorf("Warning = %q, want the subscribe error", r.Warning)
	}
}

func TestQATaggingReportsAnUnknownQAPerson(t *testing.T) {
	fakeLinear(t, map[string]string{
		"users(":        `{"users":{"nodes":[]}}`,
		"issue(id:":     issueFound,
		"commentCreate": commentPosted,
	})

	r := TagTicketsForQA("key", []string{"PROJ-1"}, "nobody", "", "staging", "https://pr")[0]

	if !r.Success || !strings.Contains(r.Warning, `user "nobody" not found`) {
		t.Errorf("got %+v, want success with a not-found warning", r)
	}
}

func TestQATaggingSubscribesTheQAPerson(t *testing.T) {
	seen := fakeLinear(t, map[string]string{
		"issue(id:":      issueFound,
		"issueSubscribe": `{"issueSubscribe":{"success":true}}`,
		"commentCreate":  commentPosted,
	})

	r := TagTicketsForQA("key", []string{"PROJ-1"}, "qa.person", "user-uuid", "staging", "https://pr")[0]

	if !r.Success || r.Warning != "" {
		t.Errorf("got %+v, want a clean success", r)
	}
	for _, req := range *seen {
		if strings.Contains(req.Query, "issueSubscribe") {
			if req.Variables["id"] != "issue-uuid" || req.Variables["userId"] != "user-uuid" {
				t.Errorf("subscribe variables = %v", req.Variables)
			}
			return
		}
	}
	t.Error("issueSubscribe was never called")
}

// Titles were fetched as one query of aliased issue(id:) lookups. issue is
// non-null, so one ticket that doesn't exist nulled the whole response
// (checked against the live API) and every title disappeared.
func TestTicketTitlesSurviveAMissingTicket(t *testing.T) {
	seen := fakeLinear(t, map[string]string{
		// INT-1 was not asked for: kept out even if a filter lets it through.
		"issues(": `{"t0":{"nodes":[{"identifier":"ATT-1","title":"Fix login"},{"identifier":"INT-1","title":"Other"}]}}`,
	})

	titles := FetchTicketTitles("key", []string{"ATT-1", "ATT-999999", "#12"})

	if titles["ATT-1"] != "Fix login" || len(titles) != 1 {
		t.Errorf("titles = %v, want just ATT-1's", titles)
	}
	b, _ := json.Marshal((*seen)[0].Variables)
	if !strings.Contains(string(b), `"ATT"`) || !strings.Contains(string(b), "999999") || strings.Contains(string(b), "#12") {
		t.Errorf("filter = %s, want both ATT keys and not the non-Linear #12", b)
	}
}
