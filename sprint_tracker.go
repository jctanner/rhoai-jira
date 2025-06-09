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
)

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

type SprintWindow struct {
	Sprint   string
	FromTime time.Time
	ToTime   *time.Time
	Points   float64
	Status   string
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

func main() {
	dir := flag.String("dir", "issues", "Directory containing *.changelog.json files")
	out := flag.String("out", "", "Output CSV file (omit to print to stdout)")
	sprintFilter := flag.String("sprint-filter", "", "If set, only include this sprint in output")
	intervalStr := flag.String("interval", "daily", "Time interval (daily, hourly, minutely)")
	flag.Parse()

	intervalDur, err := parseInterval(*intervalStr)
	if err != nil {
		log.Fatalf("invalid interval: %v", err)
	}

	type SprintKey struct {
		IssueKey string
		Sprint   string
	}

	sprintWindows := map[SprintKey][]SprintWindow{}
	storyPoints := map[string]float64{}
	statuses := map[string]string{}

	err = filepath.Walk(*dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".changelog.json") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		var changelog Changelog
		if err := json.Unmarshal(data, &changelog); err != nil {
			return fmt.Errorf("failed to parse JSON in %s: %w", path, err)
		}

		issueKey := strings.TrimSuffix(filepath.Base(path), ".changelog.json")

		for _, h := range changelog.Histories {
			t, err := time.Parse("2006-01-02T15:04:05.000-0700", h.Created)
			if err != nil {
				continue
			}

			for _, item := range h.Items {
				if item.Field == "Sprint" {
					if item.FromString != "" {
						k := SprintKey{IssueKey: issueKey, Sprint: item.FromString}
						windows := sprintWindows[k]
						if len(windows) > 0 {
							last := &windows[len(windows)-1]
							if last.ToTime == nil {
								last.ToTime = &t
							}
							sprintWindows[k] = windows
						}
					}
					if item.ToString != "" && (*sprintFilter == "" || item.ToString == *sprintFilter) {
						points := storyPoints[issueKey]
						status := statuses[issueKey]
						k := SprintKey{IssueKey: issueKey, Sprint: item.ToString}
						sprintWindows[k] = append(sprintWindows[k], SprintWindow{
							Sprint:   item.ToString,
							FromTime: t,
							ToTime:   nil,
							Points:   points,
							Status:   status,
						})
					}
				} else if item.Field == "Story Points" && item.ToString != "" {
					if pts, err := strconv.ParseFloat(item.ToString, 64); err == nil {
						storyPoints[issueKey] = pts
					}
				} else if item.Field == "status" && item.ToString != "" {
					statuses[issueKey] = item.ToString
				}
			}
		}
		return nil
	})
	if err != nil {
		log.Fatalf("error scanning files: %v", err)
	}

	now := time.Now()
	type key struct {
		Timestamp string
		Sprint    string
	}
	counts := map[key]map[string]struct{}{}
	totalPoints := map[key]float64{}
	statusCounts := map[key]map[string]int{}

	for k, windows := range sprintWindows {
		for _, w := range windows {
			end := now
			if w.ToTime != nil {
				end = *w.ToTime
			}
			for t := w.FromTime.Truncate(intervalDur); !t.After(end); t = t.Add(intervalDur) {
				ts := t.Format(timeFormatFor(intervalDur))
				kk := key{Timestamp: ts, Sprint: w.Sprint}
				if counts[kk] == nil {
					counts[kk] = map[string]struct{}{}
				}
				counts[kk][k.IssueKey] = struct{}{}
				totalPoints[kk] += w.Points
				if statusCounts[kk] == nil {
					statusCounts[kk] = map[string]int{}
				}
				statusCounts[kk][w.Status]++
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

	statusesToTrack := []string{"Backlog", "In Progress", "Review", "Testing", "Resolved"}

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

	headers := []string{"timestamp", "sprint", "issue_count", "story_points"}
	headers = append(headers, statusesToTrack...)
	writer.Write(headers)
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
		writer.Write(row)
	}
	writer.Flush()
}
