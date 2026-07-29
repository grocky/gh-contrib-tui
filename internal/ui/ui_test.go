package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rivo/tview"

	"gh-contribs/internal/contrib"
)

// fakeService is a scripted ContributionService.
type fakeService struct {
	report      *contrib.Report
	fetchErr    error
	people      []contrib.Person
	discoverErr error

	gotUser   string
	gotWindow contrib.Range
	fetches   int
	discovers int
}

func (f *fakeService) FetchContributions(_ context.Context, username string, window contrib.Range) (*contrib.Report, error) {
	f.fetches++
	f.gotUser = username
	f.gotWindow = window
	return f.report, f.fetchErr
}

func (f *fakeService) DiscoverPeople(context.Context) ([]contrib.Person, error) {
	f.discovers++
	return f.people, f.discoverErr
}

// fakeCache is an in-memory DirectoryCache.
type fakeCache struct {
	people   []contrib.Person
	storeErr error

	stored []contrib.Person
	loads  int
}

func (c *fakeCache) Load() ([]contrib.Person, bool) {
	c.loads++
	return c.people, len(c.people) > 0
}

func (c *fakeCache) Store(people []contrib.Person) error {
	c.stored = people
	return c.storeErr
}

func sampleReport() *contrib.Report {
	day := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC) // a Wednesday
	return &contrib.Report{
		Login: "octocat",
		Name:  "Mona",
		Collection: contrib.Collection{
			StartedAt:         time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			EndedAt:           time.Date(2026, 7, 29, 23, 59, 59, 0, time.UTC),
			TotalCommits:      11,
			TotalIssues:       2,
			TotalPullRequests: 5,
			TotalReviews:      7,
			Calendar: contrib.Calendar{
				Total: 25,
				Weeks: []contrib.Week{{Days: []contrib.Day{{Date: day, Count: 3, Color: "#216e39"}}}},
			},
			AuthoredPullRequests: []contrib.RepoContributions{{
				Repository: "acme/repo1",
				Nodes: []contrib.PullRequestContribution{
					{OccurredAt: day, Title: "Add feature", URL: "https://example.com/pr/1"},
				},
			}},
		},
	}
}

func TestNormalizeWeekday(t *testing.T) {
	want := map[time.Weekday]int{
		time.Monday: 0, time.Tuesday: 1, time.Wednesday: 2, time.Thursday: 3,
		time.Friday: 4, time.Saturday: 5, time.Sunday: 6,
	}
	for day, idx := range want {
		if got := normalizeWeekday(day); got != idx {
			t.Errorf("normalizeWeekday(%s) = %d, want %d", day, got, idx)
		}
	}
}

func TestRenderSummary(t *testing.T) {
	v := tview.NewTextView().SetDynamicColors(true)
	renderSummary(v, sampleReport())

	text := v.GetText(true)
	for _, want := range []string{"Mona", "2026-07-01 -> 2026-07-29", "Total: 25", "Commits: 11", "Issues: 2", "PRs: 5", "Reviews: 7"} {
		if !strings.Contains(text, want) {
			t.Errorf("summary missing %q\ngot: %s", want, text)
		}
	}
}

func TestRenderSummary_FallsBackToLogin(t *testing.T) {
	report := sampleReport()
	report.Name = ""

	v := tview.NewTextView().SetDynamicColors(true)
	renderSummary(v, report)

	if !strings.Contains(v.GetText(true), "octocat") {
		t.Fatalf("expected the login as a fallback name, got: %s", v.GetText(true))
	}
}

func TestRenderSummary_NilReportClears(t *testing.T) {
	v := tview.NewTextView().SetDynamicColors(true)
	renderSummary(v, sampleReport())
	renderSummary(v, nil)

	if got := strings.TrimSpace(v.GetText(true)); got != "" {
		t.Fatalf("expected a cleared view, got %q", got)
	}
}

func TestRenderHeatmap(t *testing.T) {
	table := tview.NewTable()
	renderHeatmap(table, sampleReport())

	if got := table.GetCell(0, 0).Text; got != "D/W" {
		t.Fatalf("corner cell = %q", got)
	}
	for row, label := range weekdayLabels {
		if got := table.GetCell(row+1, 0).Text; got != label {
			t.Fatalf("row %d label = %q, want %q", row+1, got, label)
		}
	}
	if got := table.GetCell(0, 1).Text; got != "1" {
		t.Fatalf("week header = %q, want 1", got)
	}
	// 2026-07-01 is a Wednesday, so it belongs in row 3 (Mon=1).
	if got := strings.TrimSpace(table.GetCell(3, 1).Text); got != "3" {
		t.Fatalf("wednesday cell = %q, want the contribution count 3", got)
	}
}

func TestRenderHeatmap_ClearsPreviousData(t *testing.T) {
	table := tview.NewTable()
	renderHeatmap(table, sampleReport())
	renderHeatmap(table, &contrib.Report{})

	if got := table.GetCell(3, 1).Text; got != "" {
		t.Fatalf("expected stale cells to be cleared, got %q", got)
	}
	if got := table.GetCell(0, 0).Text; got != "D/W" {
		t.Fatalf("expected headers to be re-drawn, got %q", got)
	}
}

func TestRenderPRList(t *testing.T) {
	list := tview.NewList()
	detail := tview.NewTextView()
	prs := contrib.CollectPRs(sampleReport())

	renderPRList(list, detail, prs)

	if got := list.GetItemCount(); got != 1 {
		t.Fatalf("item count = %d, want 1", got)
	}
	main, secondary := list.GetItemText(0)
	if main != "[authored] Add feature" {
		t.Fatalf("primary text = %q", main)
	}
	if secondary != "2026-07-01 | acme/repo1" {
		t.Fatalf("secondary text = %q", secondary)
	}
	if got := detail.GetText(true); got != "https://example.com/pr/1" {
		t.Fatalf("detail seeded with %q", got)
	}
}

func TestRenderPRList_EmptyClearsDetail(t *testing.T) {
	list := tview.NewList()
	detail := tview.NewTextView()
	detail.SetText("https://stale.example.com")

	renderPRList(list, detail, nil)

	if got := list.GetItemCount(); got != 0 {
		t.Fatalf("item count = %d, want 0", got)
	}
	if got := strings.TrimSpace(detail.GetText(true)); got != "" {
		t.Fatalf("expected the detail pane to be cleared, got %q", got)
	}
}

func TestFetchAndRender_PopulatesViews(t *testing.T) {
	svc := &fakeService{report: sampleReport()}
	app := New(WithService(svc), WithDefaultUser("octocat"))
	app.fromField.SetText("2026-07-01")
	app.toField.SetText("2026-07-29")

	app.fetchAndRender()

	if svc.gotUser != "octocat" {
		t.Fatalf("service received user %q", svc.gotUser)
	}
	if got := svc.gotWindow.From.Format(time.RFC3339); got != "2026-07-01T00:00:00Z" {
		t.Fatalf("window.From = %s", got)
	}
	if !strings.Contains(app.summary.GetText(true), "Mona") {
		t.Fatalf("summary not rendered: %s", app.summary.GetText(true))
	}
	if got := app.prList.GetItemCount(); got != 1 {
		t.Fatalf("PR list count = %d, want 1", got)
	}
	if got := app.status.GetText(true); !strings.Contains(got, "loaded 1 PR references") {
		t.Fatalf("status = %q", got)
	}
}

func TestFetchAndRender_TrimsUsername(t *testing.T) {
	svc := &fakeService{report: sampleReport()}
	app := New(WithService(svc))
	app.userField.SetText("  octocat  ")

	app.fetchAndRender()

	if svc.gotUser != "octocat" {
		t.Fatalf("service received user %q, want it trimmed", svc.gotUser)
	}
}

func TestFetchAndRender_RequiresUsername(t *testing.T) {
	svc := &fakeService{report: sampleReport()}
	app := New(WithService(svc))

	app.fetchAndRender()

	if svc.fetches != 0 {
		t.Fatal("expected no fetch without a username")
	}
	if got := app.status.GetText(true); !strings.Contains(got, "user is required") {
		t.Fatalf("status = %q", got)
	}
}

func TestFetchAndRender_ReportsInvalidRange(t *testing.T) {
	svc := &fakeService{report: sampleReport()}
	app := New(WithService(svc), WithDefaultUser("octocat"))
	app.toField.SetText("2026-07-29") // to without from

	app.fetchAndRender()

	if svc.fetches != 0 {
		t.Fatal("expected no fetch for an invalid range")
	}
	if got := app.status.GetText(true); !strings.Contains(got, "from is required") {
		t.Fatalf("status = %q", got)
	}
}

func TestFetchAndRender_ReportsServiceError(t *testing.T) {
	svc := &fakeService{fetchErr: errors.New("graphql exploded")}
	app := New(WithService(svc), WithDefaultUser("octocat"))

	app.fetchAndRender()

	if got := app.status.GetText(true); !strings.Contains(got, "graphql exploded") {
		t.Fatalf("status = %q, want the service error surfaced", got)
	}
	if got := app.prList.GetItemCount(); got != 0 {
		t.Fatalf("expected no PR items on failure, got %d", got)
	}
}

func TestRun_WithoutServiceFails(t *testing.T) {
	if err := New().Run(); err == nil {
		t.Fatal("expected Run to fail without a configured service")
	}
}

func TestNew_WithDefaultUserPrefillsField(t *testing.T) {
	app := New(WithDefaultUser("  octocat  "))
	if got := app.userField.GetText(); got != "octocat" {
		t.Fatalf("user field = %q, want the trimmed default", got)
	}
}

func TestSetService(t *testing.T) {
	svc := &fakeService{report: sampleReport()}
	app := New()
	app.SetService(svc)
	app.userField.SetText("octocat")

	app.fetchAndRender()

	if svc.fetches != 1 {
		t.Fatalf("fetches = %d, want 1", svc.fetches)
	}
}

func TestLogf_WritesTimestampedLines(t *testing.T) {
	app := New()
	app.Logf("hello %s", "world")

	got := app.logView.GetText(true)
	if !strings.Contains(got, "hello world") {
		t.Fatalf("log = %q", got)
	}
	if len(strings.TrimSpace(got)) <= len("hello world") {
		t.Fatalf("expected a timestamp prefix, got %q", got)
	}
}
