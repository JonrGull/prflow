package linear

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const apiURL = "https://api.linear.app/graphql"

// httpClient bounds Linear calls. http.DefaultClient has no timeout, so a
// stalled connection would hang the TUI indefinitely with no way to cancel.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// QaTagResult holds the outcome of tagging a single ticket
type QaTagResult struct {
	Ticket  string
	Success bool
	Error   string
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

// FindIssueID looks up a Linear issue by identifier (e.g. "ATT-1234") and returns its UUID
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

// SubscribeUserToIssue adds a user as a subscriber to an issue
func SubscribeUserToIssue(apiKey, issueID, userID string) error {
	_, err := doQuery(apiKey, graphqlRequest{
		Query: `mutation($id: String!, $userIds: [String!]!) {
			issueSubscribe(id: $id, subscriberIds: $userIds) {
				success
			}
		}`,
		Variables: map[string]any{
			"id":      issueID,
			"userIds": []string{userID},
		},
	})
	return err
}

// FetchTicketTitles batch-queries Linear for issue titles by identifier.
// Returns map like {"ATT-1234": "Fix login redirect"}. Missing issues are omitted.
func FetchTicketTitles(apiKey string, identifiers []string) map[string]string {
	if len(identifiers) == 0 {
		return nil
	}

	// Build a single query with aliased issue() calls: { i0: issue(id:"ATT-1234") { ... } i1: ... }
	var b strings.Builder
	b.WriteString("{ ")
	for i, id := range identifiers {
		fmt.Fprintf(&b, "i%d: issue(id: %q) { identifier title } ", i, strings.ToUpper(id))
	}
	b.WriteString("}")

	resp, err := doQuery(apiKey, graphqlRequest{Query: b.String()})
	if err != nil {
		return nil
	}

	// Response is { "i0": { "identifier": "ATT-1234", "title": "..." }, "i1": ... }
	var data map[string]*struct {
		Identifier string `json:"identifier"`
		Title      string `json:"title"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return nil
	}

	titles := make(map[string]string, len(data))
	for _, node := range data {
		if node != nil {
			titles[node.Identifier] = node.Title
		}
	}
	return titles
}

// TagTicketsForQA posts a QA comment on each ticket and subscribes the QA person
func TagTicketsForQA(apiKey string, tickets []string, qaPerson, qaPersonID, environment, prURL string) []QaTagResult {
	results := make([]QaTagResult, len(tickets))

	// Use provided ID or look up by display name
	userID := qaPersonID
	if userID == "" {
		id, err := FindUserByDisplayName(apiKey, qaPerson)
		if err == nil {
			userID = id
		}
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
		if userID != "" {
			_ = SubscribeUserToIssue(apiKey, issueID, userID)
		}
		if err := CreateComment(apiKey, issueID, body); err != nil {
			results[i].Error = err.Error()
			continue
		}
		results[i].Success = true
	}
	return results
}
