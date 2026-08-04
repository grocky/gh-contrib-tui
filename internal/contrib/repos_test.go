package contrib

import (
	"reflect"
	"testing"
)

func events(spec ...string) []Event {
	out := make([]Event, 0, len(spec))
	for i, repo := range spec {
		out = append(out, Event{
			OccurredAt: at(i%28+1, 12),
			Kind:       KindCommit,
			Repo:       repo,
			Title:      "work",
		})
	}
	return out
}

// The repo breakdown answers "where did the work go", so it leads with wherever
// most of it went.
func TestRepoTotals_CountsAndRanks(t *testing.T) {
	report := &Report{Collection: Collection{
		Events: events("acme/api", "acme/web", "acme/api", "acme/api", "acme/web", "acme/docs"),
	}}

	got := RepoTotals(report)
	want := []RepoTotal{
		{Repo: "acme/api", Count: 3},
		{Repo: "acme/web", Count: 2},
		{Repo: "acme/docs", Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("totals = %+v\nwant %+v", got, want)
	}
}

// Equal counts are common, and a breakdown that reorders itself between two
// refreshes of the same data is one nobody can trust.
func TestRepoTotals_BreaksTiesByName(t *testing.T) {
	report := &Report{Collection: Collection{
		Events: events("zeta/one", "alpha/two", "middle/three"),
	}}

	got := RepoTotals(report)
	want := []string{"alpha/two", "middle/three", "zeta/one"}
	for i, repo := range want {
		if got[i].Repo != repo {
			t.Fatalf("order = %+v, want equal counts ordered by name %v", got, want)
		}
	}
}

// Events carry a repo for everything the search APIs return, but a stream that
// omits one must not open a nameless row on the chart.
func TestRepoTotals_SkipsEventsWithNoRepo(t *testing.T) {
	report := &Report{Collection: Collection{
		Events: append(events("acme/api"), Event{OccurredAt: at(2, 12), Kind: KindCommit}),
	}}

	got := RepoTotals(report)
	if len(got) != 1 || got[0].Repo != "acme/api" || got[0].Count != 1 {
		t.Fatalf("totals = %+v, want only the named repository", got)
	}
}

func TestRepoTotals_EmptyReport(t *testing.T) {
	if got := RepoTotals(nil); got != nil {
		t.Errorf("RepoTotals(nil) = %+v, want nothing", got)
	}
	if got := RepoTotals(&Report{}); len(got) != 0 {
		t.Errorf("RepoTotals(empty) = %+v, want nothing", got)
	}
}

// Every kind of contribution counts toward where the work went, not just the
// commits.
func TestRepoTotals_CountsEveryKind(t *testing.T) {
	report := &Report{Collection: Collection{Events: []Event{
		{OccurredAt: at(1, 12), Kind: KindCommit, Repo: "acme/api"},
		{OccurredAt: at(2, 12), Kind: KindIssue, Repo: "acme/api"},
		{OccurredAt: at(3, 12), Kind: KindAuthored, Repo: "acme/api"},
		{OccurredAt: at(4, 12), Kind: KindReviewed, Repo: "acme/api"},
	}}}

	got := RepoTotals(report)
	if len(got) != 1 || got[0].Count != 4 {
		t.Fatalf("totals = %+v, want all four kinds counted", got)
	}
}

// Share is what the tile chart draws, and it has to be a share of the whole:
// the parts add up to one.
func TestRepoTotals_Share(t *testing.T) {
	totals := RepoTotals(&Report{Collection: Collection{
		Events: events("acme/api", "acme/api", "acme/api", "acme/web"),
	}})

	total := TotalOf(totals)
	if total != 4 {
		t.Fatalf("total = %d, want 4", total)
	}
	if got := totals[0].Share(total); got < 0.749 || got > 0.751 {
		t.Errorf("share = %v, want three quarters", got)
	}
	if got := totals[1].Share(total); got < 0.249 || got > 0.251 {
		t.Errorf("share = %v, want a quarter", got)
	}
	if got := totals[0].Share(0); got != 0 {
		t.Errorf("share of nothing = %v, want 0 rather than a division by zero", got)
	}
}
