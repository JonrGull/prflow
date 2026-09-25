package github

import (
	"encoding/json"
	"fmt"
)

// Viewer is the signed-in user's login and the teams they are in, each as
// org/team. A token without the read:org scope cannot list teams, so they are
// left out rather than failing the rest.
func Viewer() (login string, teams []string, err error) {
	body, err := cachedGet("user")
	if err != nil {
		return "", nil, fmt.Errorf("gh api user failed: %s", err)
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &user); err != nil {
		return "", nil, fmt.Errorf("failed to parse gh api user: %w", err)
	}
	if body, err := cachedGet("user/teams?per_page=100"); err == nil {
		var list []struct {
			Slug         string `json:"slug"`
			Organization struct {
				Login string `json:"login"`
			} `json:"organization"`
		}
		if json.Unmarshal(body, &list) == nil {
			for _, t := range list {
				teams = append(teams, t.Organization.Login+"/"+t.Slug)
			}
		}
	}
	return user.Login, teams, nil
}
