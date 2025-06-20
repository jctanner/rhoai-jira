package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

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

func sortNumerically(keys []string) {
	sort.Slice(keys, func(i, j int) bool {
		a, _ := strconv.Atoi(keys[i])
		b, _ := strconv.Atoi(keys[j])
		return a < b
	})
}

func main() {
	dir := flag.String("dir", "issues", "Directory containing *.changelog.json files")

	sprintFilter := flag.String("sprint-filter", "", "If set, only include this sprint in output")
	flag.Parse()

	//fmt.Println(sprintFilter)

	type SprintKey struct {
		IssueKey string
		Sprint   string
	}

	var matchedKeys []string

	err := filepath.Walk(*dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || strings.HasSuffix(path, ".changelog.json") || strings.HasSuffix(path, ".swp") || strings.HasSuffix(path, ".denied") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		/*
			var changelog Changelog
			if err := json.Unmarshal(data, &changelog); err != nil {
				return fmt.Errorf("failed to parse JSON in %s: %w", path, err)
			}
		*/

		//var issueData map[string]interface{}
		var issueData JiraIssue
		if err := json.Unmarshal(data, &issueData); err != nil {
			return fmt.Errorf("parse json: %s %w", path, err)
		}
		//fmt.Println(issueData.Key)

		for _, sprintraw := range issueData.Fields.Sprints {
			//fmt.Println(sprint)
			sprint, err := ParseSprintString(sprintraw)
			if err != nil {
				continue
			}
			//fmt.Println(sprint.Name)
			if sprint.Name == *sprintFilter {
				matchedKeys = append(matchedKeys, issueData.Key)
				break
			}
		}

		return nil
	})
	if err != nil {
		log.Fatalf("error scanning files: %v", err)
	}

	sortNumerically(matchedKeys)
	for ix, key := range matchedKeys {
		fmt.Printf("%d. %s\n", ix, key)
	}

}
