package jira

type HistoryItem struct {
	Field      string `json:"field"`
	ToString   string `json:"toString"`
	FromString string `json:"fromString"`
}

type HistoryEntry struct {
	Created string        `json:"created"`
	Items   []HistoryItem `json:"items"`
}

type Changelog struct {
	Histories []HistoryEntry `json:"histories"`
}
