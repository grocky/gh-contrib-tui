package contrib

import (
	"sort"
	"time"
)

// Contribution kinds. These mirror what GitHub's own profile calendar counts:
// commits, issues opened, pull requests opened, and pull request reviews
// submitted.
const (
	KindCommit   = "commit"
	KindIssue    = "issue"
	KindAuthored = "authored"
	KindReviewed = "reviewed"
)

// Event is a single contribution: one commit, one issue or pull request
// opened, or one review submitted.
type Event struct {
	OccurredAt time.Time
	Kind       string
	Repo       string
	Title      string
	URL        string

	// Merged reports whether an authored pull request has landed. It is
	// meaningless for every other kind.
	Merged bool

	// Branch names the branch a commit was found on, and is set only for
	// commits that are not reachable from their repository's default branch.
	// GitHub's commit search indexes default branches alone, so this is how
	// pushed-but-unmerged work enters the report at all -- and the one thing
	// that distinguishes it once it is here. Empty for every other kind, and
	// for commits that have landed.
	Branch string
}

// heatLevels are GitHub's calendar shades, from "nothing" to "busiest day".
// Days are bucketed by their share of the busiest day in the window, so the
// palette adapts to the user instead of assuming a fixed daily volume.
var heatLevels = []string{"#ebedf0", "#9be9a8", "#40c463", "#30a14e", "#216e39"}

// HeatLevels returns the calendar shades in order, lightest first, so a
// renderer can draw a legend that matches the days it is drawing.
func HeatLevels() []string {
	return append([]string(nil), heatLevels...)
}

// NewCollection folds raw events into a report collection: deduplicated and
// sorted newest first, counted by kind, and laid out as a calendar covering
// the whole queried window.
//
// Events outside the window are dropped. Callers fetch with coarser filters
// than the window itself -- reviews in particular are found by searching for
// recently updated pull requests -- so this is the single place the window is
// enforced.
func NewCollection(events []Event, window Range) Collection {
	events = filterWindow(events, window)
	events = dedupe(events)
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].OccurredAt.After(events[j].OccurredAt)
	})

	c := Collection{
		StartedAt: window.From,
		EndedAt:   window.To,
		Events:    events,
	}
	for _, e := range events {
		switch e.Kind {
		case KindCommit:
			c.TotalCommits++
		case KindIssue:
			c.TotalIssues++
		case KindAuthored:
			c.TotalPullRequests++
			if e.Merged {
				c.TotalMergedPullRequests++
			}
		case KindReviewed:
			c.TotalReviews++
		}
	}
	c.Calendar = buildCalendar(events, window)
	return c
}

func filterWindow(events []Event, window Range) []Event {
	out := make([]Event, 0, len(events))
	for _, e := range events {
		if e.OccurredAt.Before(window.From) || e.OccurredAt.After(window.To) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// dedupe drops repeats of the same contribution. The same URL can legitimately
// appear under two kinds -- authoring a pull request and later reviewing
// someone else's commits on it -- so the kind is part of the identity.
func dedupe(events []Event) []Event {
	seen := make(map[string]struct{}, len(events))
	out := make([]Event, 0, len(events))
	for _, e := range events {
		key := e.Kind + "|" + e.URL
		if e.URL == "" {
			// Nothing to key on; keep it rather than collapsing unrelated events.
			out = append(out, e)
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, e)
	}
	return out
}

// buildCalendar lays events out as week columns of weekday rows, spanning the
// whole window so quiet stretches stay visible as empty cells.
func buildCalendar(events []Event, window Range) Calendar {
	cal := Calendar{Total: len(events)}
	if window.To.Before(window.From) {
		return cal
	}

	perDay := make(map[string]int, len(events))
	busiest := 0
	for _, e := range events {
		key := e.OccurredAt.UTC().Format(DateFormat)
		perDay[key]++
		if perDay[key] > busiest {
			busiest = perDay[key]
		}
	}

	// Columns are calendar weeks, so the first one starts on the Monday at or
	// before the window; the leading days simply render as empty.
	day := mondayOnOrBefore(window.From)
	last := startOfDay(window.To)
	for !day.After(last) {
		week := Week{}
		for i := 0; i < 7 && !day.After(last); i++ {
			count := perDay[day.Format(DateFormat)]
			week.Days = append(week.Days, Day{
				Date:  day,
				Count: count,
				Color: heatColor(count, busiest),
			})
			day = day.AddDate(0, 0, 1)
		}
		cal.Weeks = append(cal.Weeks, week)
	}
	return cal
}

func mondayOnOrBefore(t time.Time) time.Time {
	t = startOfDay(t)
	offset := int(t.Weekday()) - int(time.Monday)
	if offset < 0 {
		offset += 7 // Sunday belongs to the week that began six days earlier.
	}
	return t.AddDate(0, 0, -offset)
}

// heatColor buckets a day by its share of the busiest day in the window.
func heatColor(count, busiest int) string {
	if count <= 0 || busiest <= 0 {
		return heatLevels[0]
	}
	level := (count*(len(heatLevels)-1) + busiest - 1) / busiest // ceil, 1..4
	if level >= len(heatLevels) {
		level = len(heatLevels) - 1
	}
	return heatLevels[level]
}
