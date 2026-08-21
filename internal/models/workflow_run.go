package models

import "time"

type WorkflowRun struct {
	DatabaseID   uint64    `json:"databaseId"`
	DisplayTitle string    `json:"displayTitle"`
	WorkflowName string    `json:"workflowName"`
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion"`
	HeadBranch   string    `json:"headBranch"`
	Event        string    `json:"event"`
	URL          string    `json:"url"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type WorkflowJob struct {
	Name        string         `json:"name"`
	Status      string         `json:"status"`
	Conclusion  string         `json:"conclusion"`
	StartedAt   time.Time      `json:"startedAt"`
	CompletedAt time.Time      `json:"completedAt"`
	Steps       []WorkflowStep `json:"steps"`
	URL         string         `json:"url"`
}

type WorkflowStep struct {
	Name       string `json:"name"`
	Number     int    `json:"number"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}
