package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"gh-contribs/internal/contrib"
	"gh-contribs/internal/reportcache"
)

// openDetailsFor drives the dashboard the way a person does: put the cursor on
// someone and press enter.
func openDetailsFor(t *testing.T, m model, login string) model {
	t.Helper()
	m.teamCursorMoved = true
	m.selectLogin(login)
	if row, ok := m.selectedRow(); !ok || !strings.EqualFold(row.person.Login, login) {
		t.Fatalf("could not put the cursor on %s", login)
	}
	return pressAndSettle(t, m, tea.KeyMsg{Type: tea.KeyEnter})
}

// A details screen still showing the last person is worse than a blank one: it
// looks like an answer.
func TestDetails_DoesNotShowThePreviousPerson(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("first", "second")}
	svc := newTeamService("first", "second")
	m := startDashboard(t, svc, store, newFakeReportCache(), 120, 44)

	m = openDetailsFor(t, m, "first")
	if m.report == nil || m.report.Login != "first" {
		t.Fatalf("report = %+v, want the first person's", m.report)
	}

	m = pressAndSettle(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	m = openDetailsFor(t, m, "second")

	if m.report != nil && m.report.Login == "first" {
		t.Fatalf("details still shows %s, want the newly selected person", m.report.Login)
	}
	if m.report != nil && m.report.Login != "second" {
		t.Fatalf("report = %+v, want the second person's", m.report)
	}
}

// A slow answer to a question nobody is asking any more must not land on the
// screen: by the time it arrives, it is somebody else's data.
func TestDetails_IgnoresALateReportForSomeoneElse(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("first", "second")}
	m := startDashboard(t, newTeamService("first", "second"), store, newFakeReportCache(), 120, 44)

	m = openDetailsFor(t, m, "second")
	window, err := m.window()
	if err != nil {
		t.Fatalf("window: %v", err)
	}

	stray := sampleReport()
	stray.Login, stray.Name = "first", "Name of first"
	m = send(t, m, fetchedMsg{login: "first", window: window, report: stray})

	if m.report == nil || m.report.Login != "second" {
		t.Fatalf("report = %+v, want the screen to have ignored the late answer", m.report)
	}
}

// The details window differs from the dashboard's, so the first visit fetches.
// The second must not: the report is on disk and inside its TTL.
func TestDetails_SecondVisitComesFromTheCache(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat")}
	svc := newTeamService("octocat")
	cache := newFakeReportCache()

	m := startDashboard(t, svc, store, cache, 120, 44)
	before := len(svc.askedFor())

	m = openDetailsFor(t, m, "octocat")
	if got := len(svc.askedFor()); got != before+1 {
		t.Fatalf("fetches = %d, want the first visit to fetch once", got-before)
	}
	if m.report == nil {
		t.Fatal("first visit showed no report")
	}

	m = pressAndSettle(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	m = openDetailsFor(t, m, "octocat")

	if got := len(svc.askedFor()); got != before+1 {
		t.Errorf("fetches = %d, want the second visit served from the cache", got-before)
	}
	if m.report == nil || m.report.Login != "octocat" {
		t.Fatalf("report = %+v, want it painted from the cache", m.report)
	}
	if m.busy {
		t.Error("a cached report left the screen looking like it was still loading")
	}
}

// What the details screen fetched is what the next visit reads, so it has to be
// written down.
func TestDetails_CachesWhatItFetches(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat")}
	cache := newFakeReportCache()
	m := startDashboard(t, newTeamService("octocat"), store, cache, 120, 44)

	openDetailsFor(t, m, "octocat")

	stored := cache.stored()
	if len(stored) < 2 {
		t.Fatalf("cached %v, want the details fetch written down as well as the dashboard's", stored)
	}
}

// A cached report past its TTL is stale data. It paints, because something is
// better than an empty screen, but a fresh one is fetched behind it.
func TestDetails_StaleCacheStillRefetches(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat")}
	svc := newTeamService("octocat")
	cache := newFakeReportCache()
	m := startDashboard(t, svc, store, cache, 120, 44)

	// Age everything already cached.
	cache.mu.Lock()
	for key, entry := range cache.entries {
		entry.Fresh = false
		entry.FetchedAt = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		cache.entries[key] = entry
	}
	cache.mu.Unlock()

	before := len(svc.askedFor())
	m = openDetailsFor(t, m, "octocat")

	if got := len(svc.askedFor()); got <= before {
		t.Errorf("fetches = %d, want a stale report refetched", got-before)
	}
	if m.report == nil {
		t.Error("stale cache left the screen empty while it refetched")
	}
}

// ctrl+r is someone saying "go and look again", usually because they know the
// data changed. A cache still inside its TTL must not answer it -- on the
// dashboard the same key already forces a refetch, and the details screen
// silently serving cached data from it is the difference between seeing work
// that just landed and being told there is none.
func TestDetails_ForcedFetchIgnoresAFreshCache(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat")}
	svc := newTeamService("octocat")
	cache := newFakeReportCache()
	m := startDashboard(t, svc, store, cache, 120, 44)
	m = openDetailsFor(t, m, "octocat")

	// Everything cached is inside its TTL, which is the case that used to
	// short-circuit the fetch entirely.
	cache.mu.Lock()
	for key, entry := range cache.entries {
		entry.Fresh = true
		cache.entries[key] = entry
	}
	cache.mu.Unlock()

	before := len(svc.askedFor())
	m = pressAndSettle(t, m, tea.KeyMsg{Type: tea.KeyCtrlR})

	if got := len(svc.askedFor()); got <= before {
		t.Errorf("fetches = %d, want ctrl+r to refetch past a fresh cache", got-before)
	}
	if m.report == nil {
		t.Error("a forced refetch left the screen empty")
	}
}

// Loading.

// The fetch takes a while, and a screen that says nothing while it waits looks
// broken. It says whose data it is waiting for, and keeps the spinner turning.
func TestDetails_SaysItIsLoading(t *testing.T) {
	m := newTestModel(t, &fakeService{report: sampleReport()}, 120, 44)
	m.inputs[focusUser].SetValue("octocat")

	next, _ := m.startFetch(false)
	if !next.busy {
		t.Fatal("startFetch did not mark the model busy")
	}

	view := plain(next.View())
	if !strings.Contains(view, "loading") || !strings.Contains(view, "octocat") {
		t.Errorf("view does not say what it is waiting for:\n%s", view)
	}
	if strings.Contains(view, "nothing loaded yet") {
		t.Errorf("view still offers the cold-start hint while loading:\n%s", view)
	}
}

// The spinner has to be on the panel people are watching, not only in the
// status line under it.
func TestDetails_SpinsWhileItLoads(t *testing.T) {
	m := newTestModel(t, &fakeService{report: sampleReport()}, 120, 44)
	m.inputs[focusUser].SetValue("octocat")
	next, _ := m.startFetch(false)

	frame := strings.TrimSpace(plain(next.spinner.View()))
	if frame == "" {
		t.Skip("spinner has no visible frame to look for")
	}
	body := plain(next.viewList())
	if !strings.Contains(body, frame) {
		t.Errorf("list panel = %q, want the spinner frame %q in it", body, frame)
	}
}

// Switching people mid-flight leaves the screen loading the new one rather than
// showing the old one's numbers under the new one's name.
func TestDetails_ClearsTheScreenWhileLoadingSomeoneNew(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("first", "second")}
	m := startDashboard(t, newTeamService("first", "second"), store, newFakeReportCache(), 120, 44)
	m = openDetailsFor(t, m, "first")

	m = pressAndSettle(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	m.teamCursorMoved = true
	m.selectLogin("second")

	// Open without settling, so the fetch is still in flight.
	next, _ := m.openDetails()
	if next.report != nil {
		t.Errorf("report = %+v, want the previous person cleared while the new one loads", next.report)
	}
	if !next.busy {
		t.Error("opening someone with nothing cached did not start loading")
	}
}

// entryFor is a cache entry built by hand, for the freshness cases.
func staleEntry(report *contrib.Report) reportcache.Entry {
	return reportcache.Entry{
		Report:    report,
		FetchedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		Fresh:     false,
	}
}

var _ = staleEntry
