package contrib

import (
	"testing"
	"time"
)

func TestParseDate(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "date only", raw: "2026-06-29", want: "2026-06-29"},
		{name: "rfc3339", raw: "2026-06-29T11:22:33Z", want: "2026-06-29"},
		{name: "surrounding space", raw: "  2026-06-29  ", want: "2026-06-29"},
		{name: "invalid", raw: "not-a-date", wantErr: true},
		{name: "empty", raw: "   ", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDate(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseDate(%q) = %v, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDate(%q) returned error: %v", tc.raw, err)
			}
			if got.Format(DateFormat) != tc.want {
				t.Fatalf("ParseDate(%q) = %s, want %s", tc.raw, got.Format(DateFormat), tc.want)
			}
		})
	}
}

func TestParseRange_Valid(t *testing.T) {
	got, err := ParseRange("2026-07-01", "2026-07-29")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if from := got.From.Format(time.RFC3339); from != "2026-07-01T00:00:00Z" {
		t.Fatalf("From = %s, want 2026-07-01T00:00:00Z", from)
	}
	if to := got.To.Format(time.RFC3339); to != "2026-07-29T23:59:59Z" {
		t.Fatalf("To = %s, want 2026-07-29T23:59:59Z", to)
	}
}

func TestParseRange_TrimsInput(t *testing.T) {
	got, err := ParseRange("  2026-07-01 ", " 2026-07-29 ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if from := got.From.Format(time.RFC3339); from != "2026-07-01T00:00:00Z" {
		t.Fatalf("From = %s, want 2026-07-01T00:00:00Z", from)
	}
}

func TestParseRange_ToWithoutFrom(t *testing.T) {
	if _, err := ParseRange("", "2026-07-29"); err == nil {
		t.Fatal("expected error when to is provided without from")
	}
}

func TestParseRange_EmptyDefaultsToTrailing30Days(t *testing.T) {
	got, err := ParseRange("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	now := time.Now().UTC()
	if want := now.AddDate(0, 0, -30).Format(DateFormat); got.From.Format(DateFormat) != want {
		t.Fatalf("From = %s, want %s", got.From.Format(DateFormat), want)
	}
	if got.To.Format(DateFormat) != now.Format(DateFormat) {
		t.Fatalf("To = %s, want today (%s)", got.To.Format(DateFormat), now.Format(DateFormat))
	}
	if h, m, s := got.From.Clock(); h != 0 || m != 0 || s != 0 {
		t.Fatalf("From clock = %02d:%02d:%02d, want start of day", h, m, s)
	}
	if h, m, s := got.To.Clock(); h != 23 || m != 59 || s != 59 {
		t.Fatalf("To clock = %02d:%02d:%02d, want end of day", h, m, s)
	}
}

func TestParseRange_FromOnlyDefaultsToToday(t *testing.T) {
	got, err := ParseRange("2026-07-01", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := time.Now().UTC().Format(DateFormat); got.To.Format(DateFormat) != want {
		t.Fatalf("To = %s, want %s", got.To.Format(DateFormat), want)
	}
}

func TestParseRange_InvalidDates(t *testing.T) {
	if _, err := ParseRange("07/01/2026", ""); err == nil {
		t.Fatal("expected error for non ISO from date")
	}
	if _, err := ParseRange("2026-07-01", "nope"); err == nil {
		t.Fatal("expected error for non ISO to date")
	}
}

func TestCollectPRs_DedupesAndSorts(t *testing.T) {
	now := time.Now().UTC()
	report := &Report{Collection: Collection{
		AuthoredPullRequests: []RepoContributions{{
			Repository: "acme/repo1",
			Nodes: []PullRequestContribution{
				{OccurredAt: now, Title: "A", URL: "https://example/pr/1"},
				{OccurredAt: now.Add(-2 * time.Hour), Title: "A dup", URL: "https://example/pr/1"},
			},
		}},
		ReviewedPullRequests: []RepoContributions{{
			Repository: "acme/repo1",
			Nodes: []PullRequestContribution{
				{OccurredAt: now.Add(-24 * time.Hour), Title: "A", URL: "https://example/pr/1"},
				{OccurredAt: now.Add(-time.Hour), Title: "B", URL: "https://example/pr/2"},
			},
		}},
	}}

	got := CollectPRs(report)
	if len(got) != 3 {
		t.Fatalf("expected 3 items (authored dup collapsed, same URL kept per kind), got %d: %+v", len(got), got)
	}
	if got[0].URL != "https://example/pr/1" || got[0].Kind != KindAuthored {
		t.Fatalf("expected newest authored PR first, got %+v", got[0])
	}
	if got[1].URL != "https://example/pr/2" || got[1].Kind != KindReviewed {
		t.Fatalf("expected second newest to be the reviewed PR, got %+v", got[1])
	}
	if got[2].Kind != KindReviewed || got[2].URL != "https://example/pr/1" {
		t.Fatalf("expected oldest reviewed duplicate last, got %+v", got[2])
	}
	if got[0].Repo != "acme/repo1" {
		t.Fatalf("expected repo to be carried through, got %q", got[0].Repo)
	}
}

func TestCollectPRs_SkipsEmptyURLs(t *testing.T) {
	report := &Report{Collection: Collection{
		AuthoredPullRequests: []RepoContributions{{
			Repository: "acme/repo1",
			Nodes: []PullRequestContribution{
				{OccurredAt: time.Now(), Title: "hidden", URL: ""},
			},
		}},
	}}
	if got := CollectPRs(report); len(got) != 0 {
		t.Fatalf("expected URL-less contributions to be skipped, got %+v", got)
	}
}

func TestCollectPRs_NilReport(t *testing.T) {
	if got := CollectPRs(nil); got != nil {
		t.Fatalf("expected nil for nil report, got %+v", got)
	}
}

func TestReportDisplayName(t *testing.T) {
	if got := (Report{Login: "octocat", Name: "Mona"}).DisplayName(); got != "Mona" {
		t.Fatalf("expected name, got %q", got)
	}
	if got := (Report{Login: "octocat"}).DisplayName(); got != "octocat" {
		t.Fatalf("expected login fallback, got %q", got)
	}
}
