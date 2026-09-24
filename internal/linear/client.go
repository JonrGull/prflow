package linear

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// A var so tests can point it at a fake.
var apiURL = "https://api.linear.app/graphql"

// httpClient bounds Linear calls. http.DefaultClient has no timeout, so a
// stalled connection would hang the TUI indefinitely with no way to cancel.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// QaTagResult holds the outcome of tagging a single ticket
type QaTagResult struct {
	Ticket  string
	Success bool
	Error   string
	// Warning is set when the comment posted but the QA person wasn't
	// subscribed, so they may not be notified.
	Warning string
}

type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type graphqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func doQuery(apiKey string, req graphqlRequest) (*graphqlResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest("POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", apiKey)

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Check the status before decoding. An auth failure returns a non-JSON
	// body, which previously surfaced as a confusing decode error rather than
	// "your Linear API key is wrong".
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("linear auth failed (HTTP %d) — check LINEAR_API_KEY", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			detail = resp.Status
		}
		return nil, fmt.Errorf("linear API error (HTTP %d): %s", resp.StatusCode, detail)
	}

	var result graphqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("graphql: %s", result.Errors[0].Message)
	}
	return &result, nil
}

// FindUserByDisplayName looks up a Linear user by display name and returns their UUID
func FindUserByDisplayName(apiKey, displayName string) (string, error) {
	resp, err := doQuery(apiKey, graphqlRequest{
		Query: `query($name: String!) {
			users(filter: { displayName: { eq: $name } }) {
				nodes { id }
			}
		}`,
		Variables: map[string]any{"name": displayName},
	})
	if err != nil {
		return "", err
	}

	var data struct {
		Users struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		} `json:"users"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return "", err
	}
	if len(data.Users.Nodes) == 0 {
		return "", fmt.Errorf("user %q not found", displayName)
	}
	return data.Users.Nodes[0].ID, nil
}

// FindIssueID looks up a Linear issue by identifier (e.g. "PROJ-1234") and returns its UUID
func FindIssueID(apiKey, identifier string) (string, error) {
	resp, err := doQuery(apiKey, graphqlRequest{
		Query:     `query($id: String!) { issue(id: $id) { id } }`,
		Variables: map[string]any{"id": strings.ToUpper(identifier)},
	})
	if err != nil {
		return "", err
	}

	var data struct {
		Issue *struct {
			ID string `json:"id"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return "", err
	}
	if data.Issue == nil {
		return "", fmt.Errorf("issue %s not found", identifier)
	}
	return data.Issue.ID, nil
}

// CreateComment posts a comment on a Linear issue
func CreateComment(apiKey, issueID, body string) error {
	_, err := doQuery(apiKey, graphqlRequest{
		Query: `mutation($input: CommentCreateInput!) {
			commentCreate(input: $input) {
				success
			}
		}`,
		Variables: map[string]any{
			"input": map[string]any{
				"issueId": issueID,
				"body":    body,
			},
		},
	})
	return err
}

// SubscribeUserToIssue adds a user as a subscriber to an issue.
//
// This used to pass subscriberIds, which issueSubscribe has never taken, so
// Linear rejected every call and the error was thrown away.
func SubscribeUserToIssue(apiKey, issueID, userID string) error {
	_, err := doQuery(apiKey, graphqlRequest{
		Query: `mutation($id: String!, $userId: String!) {
			issueSubscribe(id: $id, userId: $userId) {
				success
			}
		}`,
		Variables: map[string]any{
			"id":     issueID,
			"userId": userID,
		},
	})
	return err
}

// FetchTicketTitles batch-queries Linear for issue titles by identifier.
// Returns map like {"PROJ-1234": "Fix login redirect"}. Missing issues are omitted.
//
// It runs one issues() filter per team key, aliased into a single request.
// The lookup it used, issue(id:), is non-null, so one ticket that did not
// exist nulled the whole response and every title disappeared. A single
// filter with an or of team+number clauses does not work either: Linear
// ignores the team inside or, so it matches the number in every team.
func FetchTicketTitles(apiKey string, identifiers []string) map[string]string {
	wanted := map[string]bool{}
	numbers := map[string][]int{} // team key -> issue numbers
	var keys []string
	for _, id := range identifiers {
		key, number, ok := splitIdentifier(id)
		if !ok {
			continue // not a Linear key, e.g. a #12 from a custom pattern
		}
		if _, seen := numbers[key]; !seen {
			keys = append(keys, key)
		}
		numbers[key] = append(numbers[key], number)
		wanted[fmt.Sprintf("%s-%d", key, number)] = true
	}
	if len(keys) == 0 {
		return nil
	}

	var params, fields []string
	vars := map[string]any{}
	for i, key := range keys {
		params = append(params, fmt.Sprintf("$k%d: String!, $n%d: [Float!]!", i, i))
		fields = append(fields, fmt.Sprintf(
			"t%d: issues(first: %d, filter: { team: { key: { eq: $k%d } }, number: { in: $n%d } }) { nodes { identifier title } }",
			i, min(len(numbers[key]), 250), i, i))
		vars[fmt.Sprintf("k%d", i)] = key
		vars[fmt.Sprintf("n%d", i)] = numbers[key]
	}
	resp, err := doQuery(apiKey, graphqlRequest{
		Query:     "query(" + strings.Join(params, ", ") + ") { " + strings.Join(fields, " ") + " }",
		Variables: vars,
	})
	if err != nil {
		return nil
	}

	var data map[string]struct {
		Nodes []struct {
			Identifier string `json:"identifier"`
			Title      string `json:"title"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return nil
	}

	titles := map[string]string{}
	for _, team := range data {
		for _, node := range team.Nodes {
			if wanted[node.Identifier] {
				titles[node.Identifier] = node.Title
			}
		}
	}
	return titles
}

// splitIdentifier splits "ATT-123" into its team key and number.
func splitIdentifier(id string) (key string, number int, ok bool) {
	i := strings.LastIndexByte(id, '-')
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(id[i+1:])
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return strings.ToUpper(id[:i]), n, true
}

// TagTicketsForQA posts a QA comment on each ticket and subscribes the QA person
func TagTicketsForQA(apiKey string, tickets []string, qaPerson, qaPersonID, environment, prURL string) []QaTagResult {
	results := make([]QaTagResult, len(tickets))

	// Use provided ID or look up by display name
	userID := qaPersonID
	lookupErr := ""
	if userID == "" {
		id, err := FindUserByDisplayName(apiKey, qaPerson)
		if err != nil {
			lookupErr = "not subscribed: " + err.Error()
		}
		userID = id
	}

	body := fmt.Sprintf(
		"@%s This ticket is on **%s**.\nPR: %s",
		qaPerson, environment, prURL,
	)

	for i, ticket := range tickets {
		results[i].Ticket = ticket
		issueID, err := FindIssueID(apiKey, ticket)
		if err != nil {
			results[i].Error = err.Error()
			continue
		}
		// Subscribe QA person so they get notified
		results[i].Warning = lookupErr
		if userID != "" {
			if err := SubscribeUserToIssue(apiKey, issueID, userID); err != nil {
				results[i].Warning = "not subscribed: " + err.Error()
			}
		}
		if err := CreateComment(apiKey, issueID, body); err != nil {
			results[i].Error = err.Error()
			continue
		}
		results[i].Success = true
	}
	return results
}
