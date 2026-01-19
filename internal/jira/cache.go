package jira

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func LookupSprintIDFromDisk(dir, project, sprintName string, sprintField string) (int, error) {
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

		var issue JiraIssueWithSprints
		if err := json.Unmarshal(data, &issue); err != nil {
			continue
		}

		for _, sprint := range issue.Fields.Sprints {
			if sprint.Name == sprintName {
				return sprint.ID, nil
			}
		}
	}

	return 0, fmt.Errorf("sprint %q not found in local cache", sprintName)
}

func GetAllProjectIssueKeys(dir, project string) []string {
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

func GetAllCachedIssueKeys(dir string) []string {
	var keys []string

	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".changelog.json") && !strings.HasSuffix(name, ".denied") {
			key := strings.TrimSuffix(name, ".json")
			keys = append(keys, key)
		}
	}
	return keys
}

func GetProjectNumbersOnDisk(dir, project string) map[int]struct{} {
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

func FindLatestUpdatedTimestamp(dirpath string, project string) time.Time {
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

func FilterRecentlyFetchedIssues(dir string, keys []string, window time.Duration) []string {
	var remaining []string
	cutoff := time.Now().Add(-window)

	for _, key := range keys {
		fullPath := filepath.Join(dir, key+".json")

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

func GetIssueChangelogFromCache(dir string, key string) (Changelog, error) {
	var changelog Changelog
	changelogPath := dir + "/" + key + ".changelog.json"
	changelogData, err := os.ReadFile(changelogPath)
	if err != nil {
		return changelog, err
	}

	if err := json.Unmarshal(changelogData, &changelog); err != nil {
		return changelog, err
	}

	return changelog, nil
}

func GetIssueFromCache(dir string, key string) JiraIssueWithSprints {
	var issueData JiraIssueWithSprints
	path := dir + "/" + key + ".json"
	issueData, err := os.ReadFile(path)
	if err != nil {
		return issueData, fmt.Errorf("failed to read %s: %w", path, err)
	}
	var issue jira.JiraIssueWithSprints
	if err := json.Unmarshal(issueData, &issue); err != nil {
		return issueData, fmt.Errorf("parse json: %s %w", path, err)
	}
	return issueData, nil
}
