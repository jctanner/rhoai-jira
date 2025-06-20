package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jctanner/rhoai-jira/internal/jira"
)

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
	latestUpdate := jira.FindLatestUpdatedTimestamp(outputDir, *project).Add(-time.Duration(*lookbackHours) * time.Hour)
	log.Printf("Most recent updated timestamp: %s", latestUpdate.Format(time.RFC3339))

	// Step 4: Fetch updated issues
	updatedIssues := jira.QueryUpdatedIssues(*baseURL, *token, *project, latestUpdate)
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
		if err := jira.FetchAndSaveIssueWithChangelog(issueKey, *baseURL, *token, outputDir); err != nil {
			log.Printf("error updating %s: %v", issueKey, err)
			if strings.Contains(err.Error(), "403") {
				_ = os.WriteFile(deniedFile, []byte("denied"), 0644)
				log.Printf("marked %s as denied", issueKey)
			}
		}
	}

	// Step 1: Find highest numbered issue
	latestIssueKey := jira.GetHighestIssueKey(*baseURL, *token, *project)
	log.Printf("Latest issue found: %s", latestIssueKey)

	maxNumber := extractIssueNumber(latestIssueKey)
	if maxNumber == 0 {
		log.Fatalf("failed to extract numeric part of issue key from %s", latestIssueKey)
	}

	// Step 2: Fetch missing issues in reverse order
	numbersOnDisk := jira.GetProjectNumbersOnDisk(outputDir, *project)
	for i := maxNumber; i >= 1; i-- {
		if _, exists := numbersOnDisk[i]; exists {
			continue // Already fetched or denied
		}

		issueKey := fmt.Sprintf("%s-%d", strings.ToUpper(*project), i)
		if err := jira.FetchAndSaveIssueWithChangelog(issueKey, *baseURL, *token, outputDir); err != nil {
			log.Printf("error processing %s: %v", issueKey, err)
			if strings.Contains(err.Error(), "403") {
				deniedFile := path.Join(outputDir, fmt.Sprintf("%s.denied", issueKey))
				_ = os.WriteFile(deniedFile, []byte("denied"), 0644)
				log.Printf("marked %s as denied", issueKey)
			}
		}
	}

	if *forceUpdate {
		for i := maxNumber; i >= 1; i-- {
			issueKey := fmt.Sprintf("%s-%d", strings.ToUpper(*project), i)
			if err := jira.FetchAndSaveIssueWithChangelog(issueKey, *baseURL, *token, outputDir); err != nil {
				log.Printf("error processing %s: %v", issueKey, err)
				if strings.Contains(err.Error(), "403") {
					deniedFile := path.Join(outputDir, fmt.Sprintf("%s.denied", issueKey))
					_ = os.WriteFile(deniedFile, []byte("denied"), 0644)
					log.Printf("marked %s as denied", issueKey)
				}
			}
		}
	}

	if *smartUpdate {
		allKeys := jira.GetAllProjectIssueKeys(outputDir, *project)
    	staleKeys := jira.FilterRecentlyFetchedIssues(outputDir, allKeys, time.Duration(*lookbackHours)*time.Hour)

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
			if err := jira.FetchAndSaveIssueWithChangelog(issueKey, *baseURL, *token, outputDir); err != nil {
				continue
			}
		}
	}

	if *sprintUpdate != "" {
		 sprintIssues, err := jira.GetIssuesInSprint(outputDir, *baseURL, *token, *project, *sprintUpdate)
		 if err != nil {
			log.Fatalf("%s", err)
		 } else {
			// log.Printf("results: %s", results)
			for _, issue := range sprintIssues {
				jira.FetchAndSaveIssueWithChangelog(issue.Key, *baseURL, *token, outputDir)
			}
		 }

	}

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