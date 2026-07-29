// Package contrib holds the domain model for GitHub contribution reports and
// the pure logic that operates on it: date/range parsing and the aggregation
// of raw contribution events into a report. It has no dependency on GitHub
// clients or on the terminal UI.
package contrib

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// DateFormat is the calendar-date layout used across inputs, search
// qualifiers, and rendered output.
const DateFormat = "2006-01-02"

// Report is a user's contribution activity over a time range.
type Report struct {
	Login      string
	Name       string
	Collection Collection

	// Warnings records sources that could not be read, so a partial report is
	// never mistaken for a complete one.
	Warnings []string
}

// DisplayName is the user's real name, falling back to their login.
func (r Report) DisplayName() string {
	if r.Name != "" {
		return r.Name
	}
	return r.Login
}

// Collection is a user's contribution activity over one range, aggregated from
// the individual events that make it up.
type Collection struct {
	StartedAt time.Time
	EndedAt   time.Time

	TotalCommits      int
	TotalIssues       int
	TotalPullRequests int
	TotalReviews      int

	// TotalMergedPullRequests counts how many of the pull requests opened in
	// this window have since landed. They are a subset of TotalPullRequests,
	// and may have merged after the window closed.
	TotalMergedPullRequests int

	Calendar Calendar

	// Events holds every counted contribution, newest first.
	Events []Event
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

// Range is a resolved, inclusive query window.
type Range struct {
	From time.Time
	To   time.Time
}

// Events returns every contribution in the report, newest first.
func Events(r *Report) []Event {
	if r == nil {
		return nil
	}
	return r.Collection.Events
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
