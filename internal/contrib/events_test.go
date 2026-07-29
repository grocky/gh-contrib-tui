package contrib

import (
	"testing"
	"time"
)

func testWindow() Range {
	return Range{
		From: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 7, 29, 23, 59, 59, 0, time.UTC),
	}
}

func at(day int, hour int) time.Time {
	return time.Date(2026, 7, day, hour, 0, 0, 0, time.UTC)
}

func TestNewCollection_CountsByKind(t *testing.T) {
	got := NewCollection([]Event{
		{OccurredAt: at(2, 9), Kind: KindCommit, URL: "https://example/c/1"},
		{OccurredAt: at(2, 10), Kind: KindCommit, URL: "https://example/c/2"},
		{OccurredAt: at(3, 9), Kind: KindIssue, URL: "https://example/i/1"},
		{OccurredAt: at(4, 9), Kind: KindAuthored, URL: "https://example/pr/1", Merged: true},
		{OccurredAt: at(5, 9), Kind: KindAuthored, URL: "https://example/pr/2"},
		{OccurredAt: at(6, 9), Kind: KindReviewed, URL: "https://example/pr/9#r1"},
	}, testWindow())

	if got.TotalCommits != 2 || got.TotalIssues != 1 || got.TotalPullRequests != 2 || got.TotalReviews != 1 {
		t.Fatalf("totals = %+v", got)
	}
	if got.TotalMergedPullRequests != 1 {
		t.Fatalf("merged = %d, want 1", got.TotalMergedPullRequests)
	}
	if got.Calendar.Total != 6 {
		t.Fatalf("calendar total = %d, want one per event", got.Calendar.Total)
	}
	if !got.StartedAt.Equal(testWindow().From) || !got.EndedAt.Equal(testWindow().To) {
		t.Fatalf("window not carried through: %s -> %s", got.StartedAt, got.EndedAt)
	}
}

func TestNewCollection_SortsNewestFirst(t *testing.T) {
	got := NewCollection([]Event{
		{OccurredAt: at(2, 9), Kind: KindCommit, URL: "https://example/c/old"},
		{OccurredAt: at(20, 9), Kind: KindCommit, URL: "https://example/c/new"},
		{OccurredAt: at(10, 9), Kind: KindCommit, URL: "https://example/c/mid"},
	}, testWindow())

	want := []string{"https://example/c/new", "https://example/c/mid", "https://example/c/old"}
	for i, url := range want {
		if got.Events[i].URL != url {
			t.Fatalf("event %d = %q, want %q", i, got.Events[i].URL, url)
		}
	}
}

// The same pull request is one authored contribution and, separately, however
// many reviews the user submitted on it. Only exact kind+URL repeats collapse.
func TestNewCollection_DedupesWithinKindOnly(t *testing.T) {
	got := NewCollection([]Event{
		{OccurredAt: at(4, 9), Kind: KindAuthored, URL: "https://example/pr/1"},
		{OccurredAt: at(4, 11), Kind: KindAuthored, URL: "https://example/pr/1"},
		{OccurredAt: at(5, 9), Kind: KindReviewed, URL: "https://example/pr/1"},
	}, testWindow())

	if len(got.Events) != 2 {
		t.Fatalf("events = %+v, want the authored duplicate collapsed and the review kept", got.Events)
	}
	if got.TotalPullRequests != 1 || got.TotalReviews != 1 {
		t.Fatalf("totals = %+v", got)
	}
}

// Reviews are found by searching for recently updated pull requests, which
// drags in reviews submitted outside the window.
func TestNewCollection_DropsEventsOutsideWindow(t *testing.T) {
	window := testWindow()
	got := NewCollection([]Event{
		{OccurredAt: window.From.Add(-time.Hour), Kind: KindReviewed, URL: "https://example/r/before"},
		{OccurredAt: window.From, Kind: KindReviewed, URL: "https://example/r/first"},
		{OccurredAt: window.To, Kind: KindReviewed, URL: "https://example/r/last"},
		{OccurredAt: window.To.Add(time.Hour), Kind: KindReviewed, URL: "https://example/r/after"},
	}, window)

	if got.TotalReviews != 2 {
		t.Fatalf("reviews = %d, want the two inside the inclusive window: %+v", got.TotalReviews, got.Events)
	}
}

func TestNewCollection_CalendarSpansTheWholeWindow(t *testing.T) {
	window := Range{
		From: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),     // Wednesday
		To:   time.Date(2026, 7, 14, 23, 59, 59, 0, time.UTC), // Tuesday
	}
	got := NewCollection([]Event{
		{OccurredAt: at(1, 9), Kind: KindCommit, URL: "https://example/c/1"},
	}, window)

	// Columns start on the Monday at or before the window: Jun 29, Jul 6, Jul 13.
	if len(got.Calendar.Weeks) != 3 {
		t.Fatalf("weeks = %d, want 3: %+v", len(got.Calendar.Weeks), got.Calendar.Weeks)
	}
	first := got.Calendar.Weeks[0].Days
	if len(first) != 7 {
		t.Fatalf("first week has %d days, want a full 7", len(first))
	}
	if got := first[0].Date.Format(DateFormat); got != "2026-06-29" {
		t.Fatalf("first day = %s, want the Monday before the window", got)
	}
	// The window's own first day, Wednesday July 1, carries the one event.
	if first[2].Count != 1 {
		t.Fatalf("2026-07-01 count = %d, want 1", first[2].Count)
	}
	if first[0].Count != 0 || first[0].Color != heatLevels[0] {
		t.Fatalf("padding day = %+v, want an empty cell", first[0])
	}
	last := got.Calendar.Weeks[2].Days
	if got := last[len(last)-1].Date.Format(DateFormat); got != "2026-07-14" {
		t.Fatalf("last day = %s, want the window's end", got)
	}
}

func TestHeatColor_ScalesToTheBusiestDay(t *testing.T) {
	if got := heatColor(0, 8); got != heatLevels[0] {
		t.Fatalf("empty day = %s", got)
	}
	if got := heatColor(1, 8); got != heatLevels[1] {
		t.Fatalf("quiet day = %s, want the lightest shade", got)
	}
	if got := heatColor(8, 8); got != heatLevels[4] {
		t.Fatalf("busiest day = %s, want the darkest shade", got)
	}
	// A single contribution on the busiest day must not read as the quietest.
	if got := heatColor(1, 1); got != heatLevels[4] {
		t.Fatalf("lone contribution = %s, want the darkest shade", got)
	}
}

func TestNewCollection_NoEvents(t *testing.T) {
	got := NewCollection(nil, testWindow())

	if got.Calendar.Total != 0 || len(got.Events) != 0 {
		t.Fatalf("collection = %+v, want empty", got)
	}
	if len(got.Calendar.Weeks) == 0 {
		t.Fatal("expected an empty calendar grid, not a missing one")
	}
	for _, week := range got.Calendar.Weeks {
		for _, day := range week.Days {
			if day.Count != 0 || day.Color != heatLevels[0] {
				t.Fatalf("day = %+v, want empty", day)
			}
		}
	}
}

func TestEvents_NilReport(t *testing.T) {
	if got := Events(nil); got != nil {
		t.Fatalf("expected nil for a nil report, got %+v", got)
	}
}
