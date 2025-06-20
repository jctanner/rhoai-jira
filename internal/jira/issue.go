package jira

import "time"

type JiraIssueWithSprints struct {
	Key    string `json:"key"`
	Fields struct {
		Summary     string `json:"summary"`
		Description string `json:"description"`
		Status      struct {
			Name string `json:"name"`
		} `json:"status"`
		Sprints []string `json:"customfield_12310940"`
	} `json:"fields"`
}


type UpdatedIssue struct {
	Key         string
	UpdatedTime time.Time
}