package jira

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"
)

func DoGetWithRetry(url string, token string) ([]byte, error) {
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

func GetHighestIssueKey(baseURL, token, project string) string {
	log.Println("Fetching latest issue key...")

	url := fmt.Sprintf("%s/rest/api/2/search?jql=project=%s&maxResults=1&fields=key&orderBy=created%%20DESC", baseURL, project)
	log.Println(url)

	body, err := DoGetWithRetry(url, token)
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

func LookupSprintIDByName(baseURL, token, project, sprintName, sprintField string) (int, error) {
	jql := fmt.Sprintf(`project = %s AND Sprint ~ "%s"`, project, sprintName)
	reqURL := fmt.Sprintf(
		`%s/rest/api/2/search?jql=%s&fields=key,%s&maxResults=20`,
		baseURL,
		url.QueryEscape(jql),
		sprintField,
	)

	body, err := DoGetWithRetry(reqURL, token)
	if err != nil {
		return 0, fmt.Errorf("Jira search failed: %w", err)
	}

	var result struct {
		Issues []JiraIssueWithSprints `json:"issues"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("parse error: %w", err)
	}

	for _, issue := range result.Issues {
		for _, sprintStr := range issue.Fields.Sprints {
			sprint, err := ParseSprintString(sprintStr)
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

func FetchAndSaveIssueWithChangelog(issueKey, baseURL, token, outputDir string) error {
	url := fmt.Sprintf("%s/rest/api/2/issue/%s?expand=changelog", baseURL, issueKey)
	body, err := DoGetWithRetry(url, token)
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


func QueryUpdatedIssues(baseURL, token, project string, since time.Time) []UpdatedIssue {
	var results []UpdatedIssue
	startAt := 0
	pageSize := 100
	outputDir := "issues"
	stopEarly := false

	for {
		jql := fmt.Sprintf("project = %s AND updated >= \"%s\" ORDER BY updated DESC", project, since.UTC().Format("2006-01-02 15:04"))
		rawURL := fmt.Sprintf("%s/rest/api/2/search?jql=%s&fields=key,updated&startAt=%d&maxResults=%d", baseURL, url.QueryEscape(jql), startAt, pageSize)

		body, err := jira.DoGetWithRetry(rawURL, token)
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


func GetIssuesInSprint(outputDir string, baseURL string, token string, project string, sprintName string) ([]UpdatedIssue, error) {
	var results []UpdatedIssue
	startAt := 0
	pageSize := 100

	sprintField := "customfield_12310940"
	//sprintID, _ := lookupSprintIDByName(baseURL, token, project, sprintName, sprintField)
	sprintID, err := LookupSprintIDFromDisk(outputDir, project, sprintName, sprintField)
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

		body, err := DoGetWithRetry(reqURL, token)
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