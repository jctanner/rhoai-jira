package jira

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Sprint struct {
	ID                          int     `json:"id"`
	RapidViewID                 int     `json:"rapidViewId"`
	State                       string  `json:"state"`
	Name                        string  `json:"name"`
	StartDate                   string  `json:"startDate"`
	EndDate                     string  `json:"endDate"`
	CompleteDate                *string `json:"completeDate,omitempty"`
	ActivatedDate               string  `json:"activatedDate"`
	Sequence                    int     `json:"sequence"`
	Goal                        string  `json:"goal"`
	Synced                      bool    `json:"synced"`
	AutoStartStop               bool    `json:"autoStartStop"`
	IncompleteIssuesDestination *string `json:"incompleteIssuesDestinationId,omitempty"`
}

type SprintWindow struct {
	Sprint   string
	FromTime time.Time
	ToTime   *time.Time
	Points   float64
	Status   string
}

func ParseSprintString(s string) (*Sprint, error) {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start == -1 || end == -1 {
		return nil, fmt.Errorf("invalid sprint string format")
	}

	content := s[start+1 : end]
	parts := strings.Split(content, ",")

	result := Sprint{}
	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}

		key := kv[0]
		val := strings.TrimSpace(kv[1])

		switch key {
		case "id":
			result.ID, _ = strconv.Atoi(val)
		case "rapidViewId":
			result.RapidViewID, _ = strconv.Atoi(val)
		case "state":
			result.State = val
		case "name":
			result.Name = val
		case "startDate":
			result.StartDate = val
		case "endDate":
			result.EndDate = val
		case "completeDate":
			if val != "<null>" {
				result.CompleteDate = &val
			}
		case "activatedDate":
			result.ActivatedDate = val
		case "sequence":
			result.Sequence, _ = strconv.Atoi(val)
		case "goal":
			result.Goal = val
		case "synced":
			result.Synced = val == "true"
		case "autoStartStop":
			result.AutoStartStop = val == "true"
		case "incompleteIssuesDestinationId":
			if val != "<null>" {
				result.IncompleteIssuesDestination = &val
			}
		}
	}

	return &result, nil
}

/*
func SprintToWindow(s Sprint) (*SprintWindow, error) {
	start, err := time.Parse(timeLayout, s.StartDate)
	if err != nil {
		return nil, fmt.Errorf("invalid startDate: %v", err)
	}

	var end *time.Time
	if s.EndDate != "" {
		if t, err := time.Parse(timeLayout, s.EndDate); err == nil {
			end = &t
		}
	}

	return &SprintWindow{
		Sprint:   s.Name,
		FromTime: start,
		ToTime:   end,
		Points:   0,       // You can fill this in later
		Status:   s.State, // ACTIVE, CLOSED, FUTURE
	}, nil
}
*/
