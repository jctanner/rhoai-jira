package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jctanner/rhoai-jira/internal/jira"
)

type SprintKey struct {
	IssueKey string
	Sprint   string
}

type WindowSpan struct {
	FromTime time.Time
	ToTime   *time.Time
}

type SprintMeta struct {
	Points float64
	Status string
}

func parseInterval(interval string) (time.Duration, error) {
	switch interval {
	case "daily":
		return 24 * time.Hour, nil
	case "hourly":
		return time.Hour, nil
	case "minutely":
		return time.Minute, nil
	default:
		return 0, fmt.Errorf("invalid interval: %s", interval)
	}
}

func timeFormatFor(d time.Duration) string {
	switch d {
	case 24 * time.Hour:
		return "2006-01-02"
	case time.Hour:
		return "2006-01-02 15:00"
	case time.Minute:
		return "2006-01-02 15:04"
	default:
		return time.RFC3339
	}
}

func includes(list []string, target string) bool {
	for _, item := range list {
		if strings.TrimSpace(item) == target {
			return true
		}
	}
	return false
}

func main() {
	dir := flag.String("dir", "issues", "Directory containing *.changelog.json files")
	project := flag.String("project", "", "Filter on a specific project")
	out := flag.String("out", "", "Output CSV file (omit to print to stdout)")
	sprintFilter := flag.String("sprint-filter", "", "If set, only include this sprint in output")
	intervalStr := flag.String("interval", "daily", "Time interval (daily, hourly, minutely)")
	debugLog := flag.Bool("debug", false, "Show debug logging")
	flag.Parse()

	intervalDur, err := parseInterval(*intervalStr)
	if err != nil {
		log.Fatalf("invalid interval: %v", err)
	}

	sprintWindows := make(map[SprintKey][]WindowSpan)
	sprintMeta := make(map[SprintKey]SprintMeta)
	storyPoints := make(map[string]float64)
	statuses := make(map[string]string)

	err = filepath.Walk(*dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if strings.HasSuffix(path, ".changelog.json") || strings.HasSuffix(path, ".denied") || strings.HasSuffix(path, ".swp") {
			return nil
		}

		issueData, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}
		var issue jira.JiraIssueWithSprints
		if err := json.Unmarshal(issueData, &issue); err != nil {
			return fmt.Errorf("parse json: %s %w", path, err)
		}
		if *project != "" && issue.Fields.Project.Key != *project {
			return nil
		}

		changelog, err := jira.GetIssueChangelogFromCache(*dir, issue.Key)
		if err != nil {
			return err
		}

		foundSprintEvents := false
		for _, h := range changelog.Histories {
			for _, item := range h.Items {
				if item.Field == "Sprint" {
					foundSprintEvents = true
					break
				}
			}
			if foundSprintEvents {
				break
			}
		}

		if !foundSprintEvents && issue.Fields.Parent.Key != "" {
			parentChangelog, err := jira.GetIssueChangelogFromCache(*dir, issue.Fields.Parent.Key)
			if err != nil {
				return err
			}
			for _, h := range parentChangelog.Histories {
				for _, item := range h.Items {
					if item.Field == "Sprint" {
						foundSprintEvents = true
						changelog = parentChangelog
						break
					}
				}
				if foundSprintEvents {
					break
				}
			}
		}

		if !foundSprintEvents && len(issue.Fields.Sprints) > 0 {
			tmpChangelog, err := jira.ToChangelog(issue)
			if err != nil {
				fmt.Printf("ERROR: %s\n", err)
			} else {
				changelog = *tmpChangelog
			}
		}

		for _, h := range changelog.Histories {
			t, err := time.Parse("2006-01-02T15:04:05.000-0700", h.Created)
			if err != nil {
				continue
			}
			for _, item := range h.Items {
				switch item.Field {
				case "Sprint":
					originSprints := strings.Split(item.FromString, ",")
					newSprints := strings.Split(item.ToString, ",")

					if *debugLog && (*sprintFilter == "" || includes(originSprints, *sprintFilter) || includes(newSprints, *sprintFilter)) {
						fmt.Printf("%s %s %s -> %s\n", h.Created, issue.Key, originSprints, newSprints)
					}

					for _, sprint := range originSprints {
						sprint = strings.TrimSpace(sprint)
						if sprint == "" || (*sprintFilter != "" && sprint != *sprintFilter) {
							continue
						}
						k := SprintKey{IssueKey: issue.Key, Sprint: sprint}
						if windows := sprintWindows[k]; len(windows) > 0 && windows[len(windows)-1].ToTime == nil {
							windows[len(windows)-1].ToTime = &t
							sprintWindows[k] = windows
						}
					}

					for _, sprint := range newSprints {
						sprint = strings.TrimSpace(sprint)
						if sprint == "" || (*sprintFilter != "" && sprint != *sprintFilter) {
							continue
						}
						k := SprintKey{IssueKey: issue.Key, Sprint: sprint}
						if _, exists := sprintMeta[k]; !exists {
							sprintMeta[k] = SprintMeta{
								Points: storyPoints[issue.Key],
								Status: statuses[issue.Key],
							}
						}
						sprintWindows[k] = append(sprintWindows[k], WindowSpan{FromTime: t})
					}
				case "Story Points":
					if item.ToString != "" {
						if pts, err := strconv.ParseFloat(item.ToString, 64); err == nil {
							storyPoints[issue.Key] = pts
						}
					}
				case "status":
					if item.ToString != "" {
						statuses[issue.Key] = item.ToString
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		log.Fatalf("error scanning files: %v", err)
	}

	fmt.Println("-------------------------------------------------------------------------")
	for skey, windows := range sprintWindows {
		for k, window := range windows {
			fmt.Printf("%s %s %s %s\n", skey.IssueKey, skey.Sprint, k, window)

		}
	}
	fmt.Println("-------------------------------------------------------------------------")

	now := time.Now()
	type key struct {
		Timestamp string
		Sprint    string
	}
	counts := make(map[key]map[string]struct{})
	totalPoints := make(map[key]float64)
	statusCounts := make(map[key]map[string]int)

	for k, windows := range sprintWindows {
		meta := sprintMeta[k]
		seen := map[key]bool{}
		for _, w := range windows {
			end := now
			if w.ToTime != nil {
				end = *w.ToTime
			}
			for t := w.FromTime.Truncate(intervalDur); !t.After(end); t = t.Add(intervalDur) {
				ts := t.Format(timeFormatFor(intervalDur))
				kk := key{Timestamp: ts, Sprint: k.Sprint}
				if counts[kk] == nil {
					counts[kk] = map[string]struct{}{}
				}
				counts[kk][k.IssueKey] = struct{}{}
				if !seen[kk] {
					totalPoints[kk] += meta.Points
					seen[kk] = true
				}
				if statusCounts[kk] == nil {
					statusCounts[kk] = map[string]int{}
				}
				statusCounts[kk][meta.Status]++
			}
		}
	}

	var keys []key
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Timestamp == keys[j].Timestamp {
			return keys[i].Sprint < keys[j].Sprint
		}
		return keys[i].Timestamp < keys[j].Timestamp
	})

	statusesToTrack := []string{"Backlog", "In Progress", "Review", "Testing", "Resolved", "Closed"}

	var writer *csv.Writer
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			log.Fatalf("failed to create output file: %v", err)
		}
		defer f.Close()
		writer = csv.NewWriter(f)
		log.Printf("writing to %s", *out)
	} else {
		writer = csv.NewWriter(os.Stdout)
	}

	headers := append([]string{"timestamp", "sprint", "issue_count", "story_points"}, statusesToTrack...)
	_ = writer.Write(headers)
	for _, k := range keys {
		row := []string{
			k.Timestamp,
			k.Sprint,
			fmt.Sprintf("%d", len(counts[k])),
			fmt.Sprintf("%.1f", totalPoints[k]),
		}
		for _, s := range statusesToTrack {
			row = append(row, fmt.Sprintf("%d", statusCounts[k][s]))
		}
		_ = writer.Write(row)
	}
	writer.Flush()
}
