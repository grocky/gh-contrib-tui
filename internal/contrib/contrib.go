// Package contrib holds the domain model for GitHub contribution reports and
// the pure logic that operates on it: date/range parsing and PR collection.
// It has no dependency on GitHub clients or on the terminal UI.
package contrib

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// DateFormat is the calendar-date layout used across inputs, GraphQL calendar
// days, and rendered output.
const DateFormat = "2006-01-02"

// Contribution kinds surfaced in the PR list.
const (
	KindAuthored = "authored"
	KindReviewed = "reviewed"
)

// Report is a user's contribution activity over a time range.
type Report struct {
	Login      string
	Name       string
	Collection Collection
}

// DisplayName is the user's real name, falling back to their login.
func (r Report) DisplayName() string {
	if r.Name != "" {
		return r.Name
	}
	return r.Login
}

// Collection mirrors GitHub's contributionsCollection for a single range.
type Collection struct {
	StartedAt            time.Time
	EndedAt              time.Time
	TotalCommits         int
	TotalIssues          int
	TotalPullRequests    int
	TotalReviews         int
	Calendar             Calendar
	AuthoredPullRequests []RepoContributions
	ReviewedPullRequests []RepoContributions
}

// Calendar is the contribution heatmap data.
type Calendar struct {
	Total int
	Weeks []Week
}

// Week is one column of the heatmap.
type Week struct {
	Days []Day
}

// Day is a single heatmap cell.
type Day struct {
	Date  time.Time
	Count int
	Color string
}

// RepoContributions groups a user's PR contributions within one repository.
type RepoContributions struct {
	Repository string
	Nodes      []PullRequestContribution
}

// PullRequestContribution is a single authored or reviewed pull request event.
type PullRequestContribution struct {
	OccurredAt time.Time
	Title      string
	URL        string
}

// PRItem is a flattened, kind-tagged pull request reference for display.
type PRItem struct {
	OccurredAt time.Time
	Kind       string
	Repo       string
	Title      string
	URL        string
}

// Range is a resolved, inclusive query window.
type Range struct {
	From time.Time
	To   time.Time
}

// CollectPRs flattens authored and reviewed PR contributions into a single
// list, deduplicated per kind+URL and sorted newest first.
func CollectPRs(r *Report) []PRItem {
	if r == nil {
		return nil
	}
	seen := map[string]struct{}{}
	items := make([]PRItem, 0, 256)

	add := func(kind string, groups []RepoContributions) {
		for _, group := range groups {
			for _, node := range group.Nodes {
				if node.URL == "" {
					continue
				}
				key := kind + "|" + node.URL
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				items = append(items, PRItem{
					OccurredAt: node.OccurredAt,
					Kind:       kind,
					Repo:       group.Repository,
					Title:      node.Title,
					URL:        node.URL,
				})
			}
		}
	}

	add(KindAuthored, r.Collection.AuthoredPullRequests)
	add(KindReviewed, r.Collection.ReviewedPullRequests)

	sort.Slice(items, func(i, j int) bool {
		return items[i].OccurredAt.After(items[j].OccurredAt)
	})
	return items
}

// ParseDate accepts either a YYYY-MM-DD calendar date or an RFC3339 timestamp
// and normalizes it to a UTC day.
func ParseDate(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, errors.New("empty contribution date")
	}
	if t, err := time.Parse(DateFormat, raw); err == nil {
		return t, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC().Truncate(24 * time.Hour), nil
}

// ParseRange resolves user-supplied from/to calendar dates into an inclusive
// UTC window. An empty range defaults to the trailing 30 days; an empty `to`
// defaults to today.
func ParseRange(from, to string) (Range, error) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)

	if from == "" && to != "" {
		return Range{}, errors.New("from is required when to is set")
	}
	now := time.Now().UTC()
	if from == "" && to == "" {
		return Range{From: startOfDay(now.AddDate(0, 0, -30)), To: endOfDay(now)}, nil
	}
	if to == "" {
		to = now.Format(DateFormat)
	}
	fromTime, err := time.ParseInLocation(DateFormat, from, time.UTC)
	if err != nil {
		return Range{}, fmt.Errorf("invalid from date: %s", from)
	}
	toTime, err := time.ParseInLocation(DateFormat, to, time.UTC)
	if err != nil {
		return Range{}, fmt.Errorf("invalid to date: %s", to)
	}
	return Range{From: startOfDay(fromTime), To: endOfDay(toTime)}, nil
}

func startOfDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func endOfDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, time.UTC)
}
