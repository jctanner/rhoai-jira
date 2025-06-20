package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
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

type JiraIssue struct {
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


var (
	project       = flag.String("project", "", "Jira project key (e.g., ABC)")
	token         = flag.String("token", "", "Jira API token (or fallback to JIRA_TOKEN env var)")
	baseURL       = flag.String("base-url", "", "Base URL (e.g. https://issues.redhat.com)")
	lookbackHours = flag.Int("lookback-hours", 0, "How many hours to look back from the last known updated timestamp")
	forceUpdate   = flag.Bool("force-update", false, "force refetch -every- issue")
	smartUpdate   = flag.Bool("smart-update", false, "force refetch some* issues")
	sprintUpdate  = flag.String("sprint", "", "refetch issues in a specific sprint")
)

type UpdatedIssue struct {
	Key         string
	UpdatedTime time.Time
}

func main() {
	flag.Parse()

	if *token == "" {
		*token = os.Getenv("JIRA_TOKEN")
	}
	if *baseURL == "" {
		*baseURL = "https://issues.redhat.com"
	}
	if *project == "" || *token == "" || *baseURL == "" {
		log.Fatal("All of --project must be provided. Token must be passed via --token or JIRA_TOKEN.")
	}

	outputDir := "issues"
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("failed to create output directory: %v", err)
	}

	// Step 3: Find latest updated timestamp
	//latestUpdate := findLatestUpdatedTimestamp(outputDir, *project)
	latestUpdate := findLatestUpdatedTimestamp(outputDir, *project).Add(-time.Duration(*lookbackHours) * time.Hour)
	log.Printf("Most recent updated timestamp: %s", latestUpdate.Format(time.RFC3339))

	// Step 4: Fetch updated issues
	updatedIssues := queryUpdatedIssues(*baseURL, *token, *project, latestUpdate)
	for _, issue := range updatedIssues {
		issueKey := issue.Key
		deniedFile := path.Join(outputDir, fmt.Sprintf("%s.denied", issueKey))
		// filename := path.Join(outputDir, fmt.Sprintf("%s.json", issueKey))

		// Skip if denied
		if _, err := os.Stat(deniedFile); err == nil {
			log.Printf("skipping %s, previously marked as denied", issueKey)
			continue
		}

		// Refetch and save
		if err := fetchAndSaveIssueWithChangelog(issueKey, *baseURL, *token, outputDir); err != nil {
			log.Printf("error updating %s: %v", issueKey, err)
			if strings.Contains(err.Error(), "403") {
				_ = os.WriteFile(deniedFile, []byte("denied"), 0644)
				log.Printf("marked %s as denied", issueKey)
			}
		}
	}

	// Step 1: Find highest numbered issue
	latestIssueKey := getHighestIssueKey(*baseURL, *token, *project)
	log.Printf("Latest issue found: %s", latestIssueKey)

	maxNumber := extractIssueNumber(latestIssueKey)
	if maxNumber == 0 {
		log.Fatalf("failed to extract numeric part of issue key from %s", latestIssueKey)
	}

	// Step 2: Fetch missing issues in reverse order
	numbersOnDisk := projectNumbersOnDisk(outputDir, *project)
	for i := maxNumber; i >= 1; i-- {
		if _, exists := numbersOnDisk[i]; exists {
			continue // Already fetched or denied
		}

		issueKey := fmt.Sprintf("%s-%d", strings.ToUpper(*project), i)
		if err := fetchAndSaveIssueWithChangelog(issueKey, *baseURL, *token, outputDir); err != nil {
			log.Printf("error processing %s: %v", issueKey, err)
			if strings.Contains(err.Error(), "403") {
				deniedFile := path.Join(outputDir, fmt.Sprintf("%s.denied", issueKey))
				_ = os.WriteFile(deniedFile, []byte("denied"), 0644)
				log.Printf("marked %s as denied", issueKey)
			}
		}
	}

	if *forceUpdate == true {
		for i := maxNumber; i >= 1; i-- {
			issueKey := fmt.Sprintf("%s-%d", strings.ToUpper(*project), i)
			if err := fetchAndSaveIssueWithChangelog(issueKey, *baseURL, *token, outputDir); err != nil {
				log.Printf("error processing %s: %v", issueKey, err)
				if strings.Contains(err.Error(), "403") {
					deniedFile := path.Join(outputDir, fmt.Sprintf("%s.denied", issueKey))
					_ = os.WriteFile(deniedFile, []byte("denied"), 0644)
					log.Printf("marked %s as denied", issueKey)
				}
			}
		}
	}

	if *smartUpdate == true {
		allKeys := getAllProjectIssueKeys(outputDir, *project)
    	staleKeys := filterRecentlyFetchedIssues(outputDir, allKeys, time.Duration(*lookbackHours)*time.Hour)

		sort.Slice(staleKeys, func(i, j int) bool {
			// Extract numeric parts
			getNumber := func(key string) int {
				parts := strings.Split(key, "-")
				if len(parts) != 2 {
					return 0
				}
				n, err := strconv.Atoi(parts[1])
				if err != nil {
					return 0
				}
				return n
			}
			return getNumber(staleKeys[i]) > getNumber(staleKeys[j])
		})

		log.Printf("Refetching %d stale issues (not fetched in the last %d hours)", len(staleKeys), *lookbackHours)

		for _, issueKey := range staleKeys {
			if err := fetchAndSaveIssueWithChangelog(issueKey, *baseURL, *token, outputDir); err != nil {
				continue
			}
		}
	}

	if *sprintUpdate != "" {
		 sprintIssues, err := getIssuesInSprint(outputDir, *baseURL, *token, *project, *sprintUpdate)
		 if err != nil {
			log.Fatalf("%s", err)
		 } else {
			// log.Printf("results: %s", results)
			for _, issue := range sprintIssues {
				fetchAndSaveIssueWithChangelog(issue.Key, *baseURL, *token, outputDir)
			}
		 }

	}

}

func projectNumbersOnDisk(dir, project string) map[int]struct{} {
	found := make(map[int]struct{})

	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Fatalf("failed to read directory %s: %v", dir, err)
	}

	prefix := strings.ToUpper(project) + "-"
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, prefix) &&
			(strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".denied")) {

			base := strings.TrimSuffix(strings.TrimSuffix(name, ".json"), ".denied")
			numStr := strings.TrimPrefix(base, prefix)
			if num, err := strconv.Atoi(numStr); err == nil {
				found[num] = struct{}{}
			}
		}
	}

	return found
}

func getHighestIssueKey(baseURL, token, project string) string {
	log.Println("Fetching latest issue key...")

	url := fmt.Sprintf("%s/rest/api/2/search?jql=project=%s&maxResults=1&fields=key&orderBy=created%%20DESC", baseURL, project)
	log.Println(url)

	body, err := doGetWithRetry(url, token)
	if err != nil {
		log.Fatalf("failed to fetch latest issue: %v", err)
	}

	log.Printf("Raw response:\n%s\n", string(body))

	var result struct {
		Issues []struct {
			Key string `json:"key"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		log.Fatalf("failed to parse response: %v", err)
	}

	if len(result.Issues) == 0 {
		log.Fatalf("no issues found in project %s", project)
	}

	return result.Issues[0].Key
}

func extractIssueNumber(issueKey string) int {
	parts := strings.Split(issueKey, "-")
	if len(parts) != 2 {
		return 0
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}
	return n
}

func fetchAndSaveIssueWithChangelog(issueKey, baseURL, token, outputDir string) error {
	url := fmt.Sprintf("%s/rest/api/2/issue/%s?expand=changelog", baseURL, issueKey)
	body, err := doGetWithRetry(url, token)
	if err != nil {
		return fmt.Errorf("fetch failed: %w", err)
	}

	var issueData map[string]interface{}
	if err := json.Unmarshal(body, &issueData); err != nil {
		return fmt.Errorf("parse json: %w", err)
	}

	changelog, ok := issueData["changelog"]
	if ok {
		changelogBytes, err := json.MarshalIndent(changelog, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal changelog: %w", err)
		}

		changelogPath := path.Join(outputDir, fmt.Sprintf("%s.changelog.json", issueKey))
		if err := os.WriteFile(changelogPath, changelogBytes, 0644); err != nil {
			return fmt.Errorf("write changelog: %w", err)
		}
		log.Printf("saved %s", changelogPath)

		delete(issueData, "changelog")
	}

	issueData["fetched"] = time.Now().UTC().Format(time.RFC3339)
	strippedBytes, err := json.MarshalIndent(issueData, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal issue without changelog: %w", err)
	}

	fullPath := path.Join(outputDir, fmt.Sprintf("%s.json", issueKey))
	if err := os.WriteFile(fullPath, strippedBytes, 0644); err != nil {
		return fmt.Errorf("write issue: %w", err)
	}
	log.Printf("saved %s", fullPath)

	return nil
}

func doGetWithRetry(url string, token string) ([]byte, error) {
	var resp *http.Response
	var err error

	for attempt := 1; attempt <= 5; attempt++ {
		if attempt == 1 {
			log.Printf("GET %s", url)
		} else {
			log.Printf("GET %s (attempt %d)", url, attempt)
		}
		req, reqErr := http.NewRequest("GET", url, nil)
		if reqErr != nil {
			return nil, fmt.Errorf("failed to create request: %w", reqErr)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")

		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request error: %w", err)
		}

		if resp.StatusCode == 429 {
			log.Printf("Rate limit exceeded. Sleeping %d seconds before retrying...", attempt)
			resp.Body.Close()
			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}

		if resp.StatusCode == 404 {
			resp.Body.Close()
			return nil, fmt.Errorf("resource not found (404)")
		}

		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("error reading response: %w", readErr)
		}

		time.Sleep(500 * time.Millisecond)
		return body, nil
	}

	return nil, fmt.Errorf("exceeded retries for GET %s", url)
}

func stripChangelogFromFile(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	var issue map[string]interface{}
	if err := json.Unmarshal(data, &issue); err != nil {
		return fmt.Errorf("unmarshal json: %w", err)
	}

	if _, hasChangelog := issue["changelog"]; hasChangelog {
		delete(issue, "changelog")
		cleaned, err := json.MarshalIndent(issue, "", "  ")
		if err != nil {
			return fmt.Errorf("re-marshal: %w", err)
		}
		if err := os.WriteFile(filename, cleaned, 0644); err != nil {
			return fmt.Errorf("overwrite: %w", err)
		}
		log.Printf("stripped changelog from %s", filename)
	}
	return nil
}

func findLatestUpdatedTimestamp(dirpath string, project string) time.Time {
	var latest time.Time
	projectPrefix := strings.ToUpper(project) + "-"

	_ = filepath.Walk(dirpath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		filename := filepath.Base(path)
		if !strings.HasSuffix(filename, ".json") || strings.HasSuffix(filename, ".changelog.json") || !strings.HasPrefix(filename, projectPrefix) {
			return nil
		}

		deniedFile := filepath.Join(dirpath, strings.TrimSuffix(filename, ".json")+".denied")
		if _, err := os.Stat(deniedFile); err == nil {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var obj map[string]interface{}
		if err := json.Unmarshal(data, &obj); err != nil {
			return nil
		}

		fields, ok := obj["fields"].(map[string]interface{})
		if !ok {
			return nil
		}
		updatedStr, ok := fields["updated"].(string)
		if !ok {
			return nil
		}
		// updatedTime, err := time.Parse(time.RFC3339, updatedStr)
		updatedTime, err := time.Parse("2006-01-02T15:04:05.000-0700", updatedStr)
		if err != nil {
			return nil
		}
		if updatedTime.After(latest) {
			latest = updatedTime
		}
		return nil
	})

	if latest.IsZero() {
		return time.Now().Add(-30 * 24 * time.Hour) // default to 30 days ago
	}
	return latest
}

func queryUpdatedIssues(baseURL, token, project string, since time.Time) []UpdatedIssue {
	var results []UpdatedIssue
	startAt := 0
	pageSize := 100
	outputDir := "issues"
	stopEarly := false

	for {
		jql := fmt.Sprintf("project = %s AND updated >= \"%s\" ORDER BY updated DESC", project, since.UTC().Format("2006-01-02 15:04"))
		rawURL := fmt.Sprintf("%s/rest/api/2/search?jql=%s&fields=key,updated&startAt=%d&maxResults=%d", baseURL, url.QueryEscape(jql), startAt, pageSize)

		body, err := doGetWithRetry(rawURL, token)
		if err != nil {
			log.Fatalf("failed to query updated issues: %v", err)
		}

		var result struct {
			StartAt    int `json:"startAt"`
			MaxResults int `json:"maxResults"`
			Total      int `json:"total"`
			Issues     []struct {
				Key    string `json:"key"`
				Fields struct {
					Updated string `json:"updated"`
				} `json:"fields"`
			} `json:"issues"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			log.Fatalf("failed to parse updated issues response: %v", err)
		}

		log.Printf("Fetched %d issues (startAt=%d/%d)", len(result.Issues), result.StartAt, result.Total)

		for _, issue := range result.Issues {
			searchUpdatedTime, err := time.Parse("2006-01-02T15:04:05.000-0700", issue.Fields.Updated)
			if err != nil {
				log.Printf("could not parse updated time for %s: %v", issue.Key, err)
				continue
			}

			diskPath := path.Join(outputDir, fmt.Sprintf("%s.json", issue.Key))
			if data, err := os.ReadFile(diskPath); err == nil {
				var obj map[string]interface{}
				if err := json.Unmarshal(data, &obj); err == nil {
					if fields, ok := obj["fields"].(map[string]interface{}); ok {
						if diskUpdatedStr, ok := fields["updated"].(string); ok {
							if diskUpdatedTime, err := time.Parse("2006-01-02T15:04:05.000-0700", diskUpdatedStr); err == nil {
								log.Printf("%s: disk=%s vs search=%s", issue.Key, diskUpdatedTime, searchUpdatedTime)

								if !searchUpdatedTime.After(diskUpdatedTime) {
									log.Printf("Stopping early at %s: already up-to-date", issue.Key)
									stopEarly = true
									break
								}
							}
						}
					}
				}
			} else {
				log.Printf("%s: not found on disk", issue.Key)
			}

			results = append(results, UpdatedIssue{
				Key:         issue.Key,
				UpdatedTime: searchUpdatedTime,
			})
		}

		if stopEarly {
			break
		}

		startAt += len(result.Issues)
		if startAt >= result.Total || len(result.Issues) == 0 {
			break
		}
	}

	log.Printf("Total updated issues to refetch: %d", len(results))
	return results
}


func getAllProjectIssueKeys(dir, project string) []string {
	var keys []string
	prefix := strings.ToUpper(project) + "-"

	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".changelog.json") {
			key := strings.TrimSuffix(name, ".json")
			keys = append(keys, key)
		}
	}
	return keys
}

func filterRecentlyFetchedIssues(dir string, keys []string, window time.Duration) []string {
	var remaining []string
	cutoff := time.Now().Add(-window)

	for _, key := range keys {
		fullPath := filepath.Join(dir, key + ".json")

		data, err := os.ReadFile(fullPath)
		if err != nil {
			remaining = append(remaining, key)
			continue
		}

		var issue map[string]interface{}
		if err := json.Unmarshal(data, &issue); err != nil {
			remaining = append(remaining, key)
			continue
		}

		// Use "fetched" if it exists
		if fetchedStr, ok := issue["fetched"].(string); ok {
			if fetchedTime, err := time.Parse(time.RFC3339, fetchedStr); err == nil {
				if fetchedTime.After(cutoff) {
					continue // Fetched recently — skip it
				}
			}
		} else if fields, ok := issue["fields"].(map[string]interface{}); ok {
			// Fallback to "fields.updated" if available
			if updatedStr, ok := fields["updated"].(string); ok {
				parsedUpdated, err := time.Parse("2006-01-02T15:04:05.000-0700", updatedStr)
				if err == nil && parsedUpdated.After(cutoff) {
					continue // Updated recently — skip it
				}
			}
		}

		remaining = append(remaining, key)
	}
	return remaining
}

func escapeForJQL(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}

func getIssuesInSprint(outputDir string, baseURL string, token string, project string, sprintName string) ([]UpdatedIssue, error) {
	var results []UpdatedIssue
	startAt := 0
	pageSize := 100

	sprintField := "customfield_12310940"
	//sprintID, _ := lookupSprintIDByName(baseURL, token, project, sprintName, sprintField)
	sprintID, err := lookupSprintIDFromDisk(outputDir, project, sprintName, sprintField)
	if err != nil {
		log.Fatalf("%s", err)
		return results, err
	}
	log.Printf("%s -> %d", sprintName, sprintID)

	//jql := fmt.Sprintf("project = %s AND Sprint = %d ORDER BY key ASC", project, sprintID)
	jql := fmt.Sprintf(`project = %s AND Sprint = %d ORDER BY key ASC`, project, sprintID)

	for {
		escapedJQL := url.QueryEscape(jql)
		reqURL := fmt.Sprintf("%s/rest/api/2/search?jql=%s&fields=key,updated&startAt=%d&maxResults=%d", baseURL, escapedJQL, startAt, pageSize)

		body, err := doGetWithRetry(reqURL, token)
		if err != nil {
			return nil, fmt.Errorf("fetch sprint issues: %w", err)
		}

		var result struct {
			Issues []struct {
				Key    string `json:"key"`
				Fields struct {
					Updated string `json:"updated"`
				} `json:"fields"`
			} `json:"issues"`
			Total      int `json:"total"`
			StartAt    int `json:"startAt"`
			MaxResults int `json:"maxResults"`
		}

		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("unmarshal: %w", err)
		}

		for _, issue := range result.Issues {
			parsedTime, err := time.Parse("2006-01-02T15:04:05.000-0700", issue.Fields.Updated)
			if err != nil {
				log.Printf("warning: could not parse updated time for %s: %v", issue.Key, err)
				continue
			}

			results = append(results, UpdatedIssue{
				Key:         issue.Key,
				UpdatedTime: parsedTime,
			})
		}

		startAt += len(result.Issues)
		if startAt >= result.Total || len(result.Issues) == 0 {
			break
		}
	}

	return results, nil
}


func lookupSprintIDByName(baseURL, token, project, sprintName, sprintField string) (int, error) {
	jql := fmt.Sprintf(`project = %s AND Sprint ~ "%s"`, project, sprintName)
	reqURL := fmt.Sprintf(
		`%s/rest/api/2/search?jql=%s&fields=key,%s&maxResults=20`,
		baseURL,
		url.QueryEscape(jql),
		sprintField,
	)

	body, err := doGetWithRetry(reqURL, token)
	if err != nil {
		return 0, fmt.Errorf("Jira search failed: %w", err)
	}

	var result struct {
		Issues []JiraIssue `json:"issues"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("parse error: %w", err)
	}

	for _, issue := range result.Issues {
		for _, sprintStr := range issue.Fields.Sprints {
			sprint, err := parseSprintString(sprintStr)
			if err != nil {
				continue
			}
			if sprint.Name == sprintName {
				return sprint.ID, nil
			}
		}
	}

	return 0, fmt.Errorf("could not find sprint ID for name %q", sprintName)
}

func lookupSprintIDFromDisk(dir, project, sprintName string, sprintField string) (int, error) {
	prefix := strings.ToUpper(project) + "-"
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("could not read %s", dir)
		return 0, fmt.Errorf("read dir: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".changelog.json") {
			continue
		}

		fullPath := filepath.Join(dir, name)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}

		var issue JiraIssue
		if err := json.Unmarshal(data, &issue); err != nil {
			continue
		}

		for _, sprintStr := range issue.Fields.Sprints {
			sprint, err := parseSprintString(sprintStr)
			if err != nil {
				continue
			}
			if sprint.Name == sprintName {
				return sprint.ID, nil
			}
		}
	}

	return 0, fmt.Errorf("sprint %q not found in local cache", sprintName)
}


func parseSprintString(s string) (*Sprint, error) {
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