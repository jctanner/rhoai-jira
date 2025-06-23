package jira

import (
	"encoding/json"
	"fmt"
	"time"
)

type UpdatedIssue struct {
	Key         string
	UpdatedTime time.Time
}

// SprintList can handle either a structured list or a list of legacy strings
type SprintList []Sprint

func (s *SprintList) UnmarshalJSON(data []byte) error {
	// Try JSON array of structs first
	var structList []Sprint
	if err := json.Unmarshal(data, &structList); err == nil {
		*s = structList
		return nil
	}

	// Try array of strings
	var stringList []string
	if err := json.Unmarshal(data, &stringList); err == nil {
		var parsed []Sprint
		for _, raw := range stringList {
			sprint, err := ParseSprintString(raw)
			if err != nil {
				// You can log or skip invalid strings here
				continue
			}
			parsed = append(parsed, *sprint)
		}
		*s = parsed
		return nil
	}

	return fmt.Errorf("unsupported sprint format: %s", string(data))
}

// Fields is the inner portion of the issue
type Fields struct {
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Created     string `json:"created`

	Status struct {
		Name string `json:"name"`
	} `json:"status"`

	IssueType struct {
		Name string `json:"name"`
	} `json:"issuetype"`

	Parent struct {
		Key string `json:"key"`
	} `json:"parent"`

	Project struct {
		Key string `json:"key"`
	} `json:"project"`

	Sprints SprintList `json:"customfield_12310940"`
}

// JiraIssueWithSprints represents a complete issue
type JiraIssueWithSprints struct {
	Key    string `json:"key"`
	Fields Fields `json:"fields"`
}

func ToChangelog(issue JiraIssueWithSprints) (*Changelog, error) {
	var entries []HistoryEntry
	var entry HistoryEntry
	var items []HistoryItem

	for _, sprint := range issue.Fields.Sprints {
		item := HistoryItem{
			Field:      "sprint",
			ToString:   sprint.Name,
			FromString: "",
		}
		items = append(items, item)
	}

	entry.Created = issue.Fields.Created
	entry.Items = items
	entries = append(entries, entry)

	return &Changelog{
		Histories: entries,
	}, nil
}
