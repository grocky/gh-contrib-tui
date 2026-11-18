package ui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/grocky/gh-contrib-tui/internal/contrib"
	"github.com/grocky/gh-contrib-tui/internal/reportcache"
	"github.com/grocky/gh-contrib-tui/internal/teamconfig"
)

// fakeTeamStore is an in-memory roster.
type fakeTeamStore struct {
	mu       sync.Mutex
	team     teamconfig.Team
	loadErr  error
	saveErr  error
	saves    int
	lastSave teamconfig.Team
}

func (s *fakeTeamStore) Load() (teamconfig.Team, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.team, s.loadErr
}

func (s *fakeTeamStore) Save(team teamconfig.Team) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	s.lastSave = team
	if s.saveErr == nil {
		s.team, s.loadErr = team, nil
	}
	return s.saveErr
}

func (s *fakeTeamStore) saved() (int, teamconfig.Team) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saves, s.lastSave
}

// fakeReportCache is an in-memory report cache keyed by login only: the tests
// that care about windows drive the real cache instead.
type fakeReportCache struct {
	mu      sync.Mutex
	entries map[string]reportcache.Entry
	stores  []string
}

func newFakeReportCache() *fakeReportCache {
	return &fakeReportCache{entries: map[string]reportcache.Entry{}}
}

// cacheKey mirrors the real cache: a report is only about the window it was
// fetched for, and the two screens ask for different windows.
func cacheKey(login string, window contrib.Range) string {
	return strings.ToLower(login) + "|" +
		window.From.Format(contrib.DateFormat) + "|" +
		window.To.Format(contrib.DateFormat)
}

func (c *fakeReportCache) put(login string, window contrib.Range, report *contrib.Report, fresh bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[cacheKey(login, window)] = reportcache.Entry{
		Report:    report,
		FetchedAt: time.Date(2026, 8, 4, 11, 0, 0, 0, time.UTC),
		Fresh:     fresh,
	}
}

func (c *fakeReportCache) Load(login string, window contrib.Range) (reportcache.Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[cacheKey(login, window)]
	return entry, ok
}

func (c *fakeReportCache) Store(login string, window contrib.Range, report *contrib.Report) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stores = append(c.stores, strings.ToLower(login))
	c.entries[cacheKey(login, window)] = reportcache.Entry{Report: report, Fresh: true}
	return nil
}

func (c *fakeReportCache) stored() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.stores...)
}

// teamService answers per-login, recording who was asked for.
type teamService struct {
	mu      sync.Mutex
	reports map[string]*contrib.Report
	viewer  contrib.Person
	people  []contrib.Person
	err     error
	asked   []string
}

func newTeamService(logins ...string) *teamService {
	svc := &teamService{
		reports: map[string]*contrib.Report{},
		viewer:  contrib.Person{Login: "nash-robin", Name: "Nash, Robin", Active: true},
	}
	for _, login := range logins {
		report := sampleReport()
		report.Login, report.Name = login, "Name of "+login
		svc.reports[strings.ToLower(login)] = report
	}
	return svc
}

func (s *teamService) FetchContributions(_ context.Context, login string, _ contrib.Range) (*contrib.Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, strings.ToLower(login))
	if s.err != nil {
		return nil, s.err
	}
	report, ok := s.reports[strings.ToLower(login)]
	if !ok {
		return nil, errors.New("user not found")
	}
	return report, nil
}

func (s *teamService) DiscoverPeople(context.Context) ([]contrib.Person, error) {
	return s.people, nil
}

func (s *teamService) Viewer(context.Context) (contrib.Person, error) {
	return s.viewer, s.err
}

func (s *teamService) askedFor() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.asked...)
}

func teamOf(logins ...string) teamconfig.Team {
	team := teamconfig.Team{}
	for _, login := range logins {
		team = team.Add(contrib.Person{Login: login, Name: "Name of " + login, Active: true})
	}
	return team
}

// startDashboard builds a sized model and runs its startup commands the way the
// runtime does, so the team load and the fetches it triggers all land.
func startDashboard(t *testing.T, svc ContributionService, store TeamStore, cache ReportCache, w, h int) model {
	t.Helper()
	m := newModel()
	m.svc = svc
	m.teams = store
	m.reports = cache
	m = send(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	return runAndSettle(t, m, m.Init())
}

// runAndSettle feeds a command's messages back into the model until quiet.
func runAndSettle(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	queue := collect(t, cmd)
	for steps := 0; steps < 128 && len(queue) > 0; steps++ {
		next, nextCmd := sendCmd(t, m, queue[0])
		m, queue = next, queue[1:]
		queue = append(queue, collect(t, nextCmd)...)
	}
	return m
}

// pressRune sends a single letter key.
func pressRune(t *testing.T, m model, r rune) model {
	t.Helper()
	return send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
}

// Landing screen.

func TestDashboard_IsTheLandingScreen(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat")}
	m := startDashboard(t, newTeamService("octocat"), store, newFakeReportCache(), 120, 40)

	if m.screen != screenDashboard {
		t.Fatalf("screen = %v, want the dashboard", m.screen)
	}
	if got := plain(m.View()); !strings.Contains(got, "Team") {
		t.Errorf("dashboard does not name itself:\n%s", got)
	}
}

// A first launch has nobody configured, so it puts whoever is running the app
// on their own team rather than opening on an empty screen.
func TestDashboard_SeedsTheTeamWithTheViewer(t *testing.T) {
	store := &fakeTeamStore{loadErr: teamconfig.ErrNotConfigured}
	svc := newTeamService("nash-robin")
	m := startDashboard(t, svc, store, newFakeReportCache(), 120, 40)

	if !m.team.Has("nash-robin") {
		t.Fatalf("team = %+v, want the viewer seeded onto it", m.team.Members)
	}
	saves, saved := store.saved()
	if saves != 1 || !saved.Has("nash-robin") {
		t.Fatalf("saves = %d, saved = %+v, want the seeded team persisted", saves, saved.Members)
	}
}

// A team that was configured and then emptied is a decision, not a first
// launch: re-seeding it would put back the person they just removed.
func TestDashboard_DoesNotReseedAnEmptyTeam(t *testing.T) {
	store := &fakeTeamStore{team: teamconfig.Team{}}
	m := startDashboard(t, newTeamService(), store, newFakeReportCache(), 120, 40)

	if len(m.team.Members) != 0 {
		t.Fatalf("team = %+v, want it left empty", m.team.Members)
	}
	if saves, _ := store.saved(); saves != 0 {
		t.Errorf("saves = %d, want the empty team left alone", saves)
	}
}

func TestDashboard_LoadsTheConfiguredTeam(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat", "avery-lynn")}
	m := startDashboard(t, newTeamService("octocat", "avery-lynn"), store, newFakeReportCache(), 120, 40)

	if got := len(m.rows()); got != 2 {
		t.Fatalf("rows = %d, want one per team member", got)
	}
	view := plain(m.View())
	for _, want := range []string{"Name of octocat", "Name of avery-lynn"} {
		if !strings.Contains(view, want) {
			t.Errorf("dashboard is missing %q:\n%s", want, view)
		}
	}
}

// Data on disk paints immediately. Refetching someone whose report is current
// costs a round trip and shows the user nothing new.
func TestDashboard_PaintsFreshCacheWithoutFetching(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat")}
	cache := newFakeReportCache()
	cached := sampleReport()
	cached.Login, cached.Name = "octocat", "Name of octocat"
	cache.put("octocat", contrib.TrailingDays(dashboardDays), cached, true)

	svc := newTeamService("octocat")
	m := startDashboard(t, svc, store, cache, 120, 40)

	if got := svc.askedFor(); len(got) != 0 {
		t.Errorf("fetched %v, want the fresh cache served instead", got)
	}
	if row := m.rows()[0]; row.state != rowLoaded {
		t.Errorf("row state = %v, want it loaded from cache", row.state)
	}
}

// A stale report is still worth painting while the fresh one is on its way.
func TestDashboard_PaintsStaleCacheAndRefetches(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat")}
	cache := newFakeReportCache()
	stale := sampleReport()
	stale.Login, stale.Name = "octocat", "Name of octocat"
	cache.put("octocat", contrib.TrailingDays(dashboardDays), stale, false)

	svc := newTeamService("octocat")
	m := startDashboard(t, svc, store, cache, 120, 40)

	if got := svc.askedFor(); len(got) != 1 || got[0] != "octocat" {
		t.Errorf("fetched %v, want the stale row refetched", got)
	}
	if row := m.rows()[0]; row.report == nil {
		t.Error("row has no report, want the stale one painted while it refetched")
	}
}

func TestDashboard_FetchesWhenNothingIsCached(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat", "avery-lynn")}
	svc := newTeamService("octocat", "avery-lynn")
	m := startDashboard(t, svc, store, newFakeReportCache(), 120, 40)

	if got := svc.askedFor(); len(got) != 2 {
		t.Fatalf("fetched %v, want both members fetched", got)
	}
	for _, row := range m.rows() {
		if row.state != rowLoaded {
			t.Errorf("%s state = %v, want loaded", row.person.Login, row.state)
		}
	}
}

// What arrives over the network is what the next launch paints from.
func TestDashboard_CachesWhatItFetches(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat")}
	cache := newFakeReportCache()
	startDashboard(t, newTeamService("octocat"), store, cache, 120, 40)

	if got := cache.stored(); len(got) != 1 || got[0] != "octocat" {
		t.Errorf("stored %v, want the fetched report cached", got)
	}
}

// A member who cannot be fetched leaves a row that says so, rather than an
// empty row indistinguishable from someone with no contributions.
func TestDashboard_FailedRowSaysSo(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("ghost")}
	svc := newTeamService() // knows nobody
	m := startDashboard(t, svc, store, newFakeReportCache(), 120, 40)

	row := m.rows()[0]
	if row.state != rowFailed {
		t.Fatalf("row state = %v, want it marked failed", row.state)
	}
	if !strings.Contains(plain(m.View()), "failed") {
		t.Errorf("dashboard does not show the failure:\n%s", plain(m.View()))
	}
}

// Navigation.

func TestDashboard_EnterOpensTheDetailsScreen(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat", "avery-lynn")}
	m := startDashboard(t, newTeamService("octocat", "avery-lynn"), store, newFakeReportCache(), 120, 40)

	// Rows are ordered by contributions, so the second row is whoever the sort
	// put there rather than whoever is second in the config.
	want := m.rows()[1].person.Login
	m = press(t, m, tea.KeyDown)
	m = pressAndSettle(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.screen != screenDetails {
		t.Fatalf("screen = %v, want details", m.screen)
	}
	if got := m.inputs[focusUser].Value(); got != want {
		t.Errorf("user field = %q, want the selected member %q", got, want)
	}
	if m.report == nil || m.report.Login != want {
		t.Fatalf("details report = %+v, want %q's", m.report, want)
	}
}

func TestDashboard_EscapeReturnsFromDetails(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat")}
	m := startDashboard(t, newTeamService("octocat"), store, newFakeReportCache(), 120, 40)

	m = pressAndSettle(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.screen != screenDetails {
		t.Fatalf("screen = %v, want details before going back", m.screen)
	}

	m = pressAndSettle(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.screen != screenDashboard {
		t.Fatalf("screen = %v, want the dashboard back", m.screen)
	}
}

// The dates are the one control the two screens share, so a window chosen on
// the dashboard is the window the details screen opens on.
func TestDashboard_CarriesTheWindowIntoDetails(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat")}
	m := startDashboard(t, newTeamService("octocat"), store, newFakeReportCache(), 120, 40)

	m.inputs[focusFrom].SetValue("2026-01-01")
	m.inputs[focusTo].SetValue("2026-06-30")
	m = pressAndSettle(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if got := m.inputs[focusFrom].Value(); got != "2026-01-01" {
		t.Errorf("from = %q, want the dashboard's window carried in", got)
	}
}

// Editing the team.

func TestDashboard_AddsAPerson(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat")}
	svc := newTeamService("octocat", "avery-lynn")
	svc.people = []contrib.Person{{Login: "avery-lynn", Name: "Avery, Lynn", Active: true}}
	m := startDashboard(t, svc, store, newFakeReportCache(), 120, 40)

	m = pressRune(t, m, 'a')
	if !m.adding {
		t.Fatal("pressing a did not open the add field")
	}
	m = typeText(t, m, "avery-lynn")
	m = pressAndSettle(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if !m.team.Has("avery-lynn") {
		t.Fatalf("team = %+v, want the person added", m.team.Members)
	}
	if _, saved := store.saved(); !saved.Has("avery-lynn") {
		t.Fatalf("saved team = %+v, want the addition persisted", saved.Members)
	}
	if m.adding {
		t.Error("add field stayed open after accepting")
	}
}

// Somebody just added has no report yet, so the dashboard goes and gets one
// rather than leaving a blank row until the next launch.
func TestDashboard_FetchesSomeoneJustAdded(t *testing.T) {
	store := &fakeTeamStore{team: teamconfig.Team{}}
	svc := newTeamService("avery-lynn")
	m := startDashboard(t, svc, store, newFakeReportCache(), 120, 40)

	m = pressRune(t, m, 'a')
	m = typeText(t, m, "avery-lynn")
	m = pressAndSettle(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if got := svc.askedFor(); len(got) != 1 || got[0] != "avery-lynn" {
		t.Errorf("fetched %v, want the new member fetched", got)
	}
}

func TestDashboard_RemovesTheSelectedPerson(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat", "avery-lynn")}
	m := startDashboard(t, newTeamService("octocat", "avery-lynn"), store, newFakeReportCache(), 120, 40)

	// Whoever the sort put on the second row is who x removes.
	gone := m.rows()[1].person.Login
	m = press(t, m, tea.KeyDown)
	m = pressAndSettle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	if m.team.Has(gone) {
		t.Fatalf("team = %+v, want %q removed", m.team.Members, gone)
	}
	if got := len(m.rows()); got != 1 {
		t.Errorf("rows = %d, want the removed row gone", got)
	}
	if _, saved := store.saved(); saved.Has(gone) {
		t.Errorf("saved team = %+v, want the removal persisted", saved.Members)
	}
}

// Escape has to mean "close this" before it means "go back", or dismissing the
// add field would drop the user out of the dashboard entirely.
func TestDashboard_EscapeClosesTheAddFieldFirst(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat")}
	m := startDashboard(t, newTeamService("octocat"), store, newFakeReportCache(), 120, 40)

	m = pressRune(t, m, 'a')
	m = press(t, m, tea.KeyEsc)

	if m.adding {
		t.Error("add field survived escape")
	}
	if m.screen != screenDashboard {
		t.Errorf("screen = %v, want to still be on the dashboard", m.screen)
	}
}

// Refreshing.

func TestDashboard_ForcedRefreshRefetchesEveryone(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat")}
	cache := newFakeReportCache()
	cached := sampleReport()
	cached.Login = "octocat"
	cache.put("octocat", contrib.TrailingDays(dashboardDays), cached, true)

	svc := newTeamService("octocat")
	m := startDashboard(t, svc, store, cache, 120, 40)
	if got := svc.askedFor(); len(got) != 0 {
		t.Fatalf("fetched %v on launch, want the fresh cache used", got)
	}

	pressAndSettle(t, m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if got := svc.askedFor(); len(got) != 1 {
		t.Errorf("fetched %v, want ctrl+r to go past the cache", got)
	}
}

// pressAndSettle sends a key and feeds every message it produces back in, which
// is how a fetch triggered by a keystroke completes.
func pressAndSettle(t *testing.T, m model, msg tea.KeyMsg) model {
	t.Helper()
	return sendAndSettle(t, m, msg)
}

// Ordering.

// withTotals gives each login a report carrying that many contributions.
func (s *teamService) withTotals(totals map[string]int) *teamService {
	s.mu.Lock()
	defer s.mu.Unlock()
	for login, total := range totals {
		s.reports[strings.ToLower(login)] = reportOver(login, total, 1)
	}
	return s
}

func loginsOf(rows []teamRow) []string {
	logins := make([]string, 0, len(rows))
	for _, row := range rows {
		logins = append(logins, row.person.Login)
	}
	return logins
}

// The dashboard is read top down, so it leads with whoever has been busiest
// rather than with whoever happens to sit first in the config file.
func TestDashboard_SortsByContributions(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("quiet", "busiest", "middling")}
	svc := newTeamService("quiet", "busiest", "middling").
		withTotals(map[string]int{"quiet": 2, "busiest": 40, "middling": 12})

	m := startDashboard(t, svc, store, newFakeReportCache(), 120, 60)

	want := []string{"busiest", "middling", "quiet"}
	if got := loginsOf(m.rows()); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// Reports land one at a time and in no particular order, so the ordering has to
// be reapplied as they arrive rather than fixed at the first paint.
func TestDashboard_ResortsAsReportsLand(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("first", "second")}
	svc := newTeamService("first", "second").
		withTotals(map[string]int{"first": 3, "second": 90})

	m := startDashboard(t, svc, store, newFakeReportCache(), 120, 60)
	if got := loginsOf(m.rows()); got[0] != "second" {
		t.Fatalf("order = %v, want the busier member first", got)
	}

	// A later report overtakes.
	window, err := m.window()
	if err != nil {
		t.Fatalf("window: %v", err)
	}
	m = send(t, m, teamFetchedMsg{login: "first", window: window, report: reportOver("first", 500, 1)})

	if got := loginsOf(m.rows()); got[0] != "first" {
		t.Fatalf("order = %v, want the new report to have moved its row up", got)
	}
}

// A row with no numbers cannot be ranked, and counting it as a zero would rank
// someone by a number nobody measured. It waits at the bottom.
func TestDashboard_RowsWithoutDataSinkToTheBottom(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("ghost", "octocat")}
	svc := newTeamService("octocat").withTotals(map[string]int{"octocat": 5})

	m := startDashboard(t, svc, store, newFakeReportCache(), 120, 60)

	want := []string{"octocat", "ghost"}
	if got := loginsOf(m.rows()); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want the unreachable member last", got)
	}
}

// Equal counts are common on a quiet week. Falling back to the name keeps the
// list from reshuffling itself every refresh.
func TestDashboard_BreaksTiesByName(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("zed", "adams", "moss")}
	svc := newTeamService("zed", "adams", "moss").
		withTotals(map[string]int{"zed": 7, "adams": 7, "moss": 7})

	m := startDashboard(t, svc, store, newFakeReportCache(), 120, 60)

	want := []string{"adams", "moss", "zed"}
	if got := loginsOf(m.rows()); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want equal counts ordered by name", got)
	}
}

// Until someone moves it, the cursor belongs to the top of the list rather than
// to a person: rows arrive unsorted and settle into order, and a cursor that
// clung to whoever landed first would end up parked mid-list for no reason.
func TestDashboard_CursorRidesTheTopUntilItIsMoved(t *testing.T) {
	// Rows start out with no reports and sort by name, so "alpha" leads at
	// first; the totals then put "omega" on top. The cursor has to move with
	// the top row, not stay with whoever was there before the data arrived.
	store := &fakeTeamStore{team: teamOf("alpha", "omega")}
	svc := newTeamService("alpha", "omega").
		withTotals(map[string]int{"alpha": 2, "omega": 90})

	m := startDashboard(t, svc, store, newFakeReportCache(), 120, 60)

	if got := m.teamList.Index(); got != 0 {
		t.Fatalf("cursor is at index %d, want it still at the top", got)
	}
	row, ok := m.selectedRow()
	if !ok || row.person.Login != "omega" {
		t.Fatalf("selected = %+v, want the busiest member once the rows settled", row.person)
	}
}

// The cursor belongs to a person, not to a position. A report landing elsewhere
// on the team must not slide someone else under the cursor mid-read.
func TestDashboard_KeepsTheCursorOnItsPersonAcrossASort(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("low", "high")}
	svc := newTeamService("low", "high").
		withTotals(map[string]int{"low": 1, "high": 100})

	m := startDashboard(t, svc, store, newFakeReportCache(), 120, 60)
	m = press(t, m, tea.KeyDown)

	selected, ok := m.selectedRow()
	if !ok || selected.person.Login != "low" {
		t.Fatalf("selected = %+v, want the second row", selected.person)
	}

	window, err := m.window()
	if err != nil {
		t.Fatalf("window: %v", err)
	}
	m = send(t, m, teamFetchedMsg{login: "low", window: window, report: reportOver("low", 500, 1)})

	after, ok := m.selectedRow()
	if !ok || after.person.Login != "low" {
		t.Fatalf("selected = %+v after the sort, want the same person", after.person)
	}
	if got := m.teamList.Index(); got != 0 {
		t.Errorf("cursor is at index %d, want it to have followed the row to the top", got)
	}
}

// Ordering is a view of the roster, not a rewrite of it: the config keeps the
// order it was curated in, so nothing churns the file on every refresh.
func TestDashboard_SortingDoesNotReorderTheStoredTeam(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("quiet", "busiest")}
	svc := newTeamService("quiet", "busiest").
		withTotals(map[string]int{"quiet": 1, "busiest": 50})

	m := startDashboard(t, svc, store, newFakeReportCache(), 120, 60)
	if got := loginsOf(m.rows())[0]; got != "busiest" {
		t.Fatalf("first row = %s, want the busier member", got)
	}

	if got := m.team.Logins(); !reflect.DeepEqual(got, []string{"quiet", "busiest"}) {
		t.Fatalf("roster = %v, want it left in curated order", got)
	}
	if saves, _ := store.saved(); saves != 0 {
		t.Errorf("saves = %d, want sorting to leave the config alone", saves)
	}
}

// Row layout.

// reportOver builds a report whose calendar spans a given number of days, so
// rows with different histories can be compared.
func reportOver(login string, days, perDay int) *contrib.Report {
	to := time.Date(2026, 8, 4, 23, 59, 59, 0, time.UTC)
	window := contrib.Range{From: to.AddDate(0, 0, -days).Truncate(24 * time.Hour), To: to}

	var events []contrib.Event
	for day := 0; day < days; day++ {
		for n := 0; n < perDay; n++ {
			events = append(events, contrib.Event{
				OccurredAt: window.From.AddDate(0, 0, day),
				Kind:       contrib.KindCommit,
				Repo:       "acme/repo",
				Title:      "work",
			})
		}
	}
	return &contrib.Report{
		Login:      login,
		Name:       "Name of " + login,
		Collection: contrib.NewCollection(events, window),
	}
}

func TestTeamRow_ShowsNameStatsAndCalendar(t *testing.T) {
	row := teamRow{
		person:    contrib.Person{Login: "nash-robin", Name: "Nash, Robin"},
		report:    reportOver("nash-robin", 30, 2),
		state:     rowLoaded,
		fetchedAt: time.Now(),
	}
	got := plain(renderTeamRow(row, 110, false))

	for _, want := range []string{"Nash, Robin", "@nash-robin", "contributions", "commits", "reviews"} {
		if !strings.Contains(got, want) {
			t.Errorf("row is missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, miniUpper) {
		t.Errorf("row has no minified calendar:\n%s", got)
	}
	if lines := strings.Split(got, "\n"); len(lines) != rowHeight {
		t.Errorf("row is %d lines, want %d", len(lines), rowHeight)
	}
}

// The stats are a column down the dashboard, so every label has to fit its cell
// -- including the widest one, a pull request count with its merged tally.
func TestTeamRow_StatLabelsFitTheirCells(t *testing.T) {
	report := reportOver("nash-robin", 30, 2)
	report.Collection.TotalPullRequests = 128
	report.Collection.TotalMergedPullRequests = 116

	got := plain(renderTeamRow(teamRow{
		person: contrib.Person{Login: "nash-robin", Name: "Nash, Robin"},
		report: report,
		state:  rowLoaded,
	}, 110, false))

	if !strings.Contains(got, "prs (116 merged)") {
		t.Errorf("the pull request stat was cut short:\n%s", got)
	}
	if strings.Contains(got, "merg…") {
		t.Errorf("a stat label was truncated:\n%s", got)
	}
}

// Rows are read as columns: whatever each person's history looks like, the
// calendars have to start in the same place or the dashboard reads as ragged.
func TestTeamRow_CalendarsAlignAcrossRows(t *testing.T) {
	short := teamRow{person: contrib.Person{Login: "a"}, report: reportOver("a", 7, 1), state: rowLoaded}
	long := teamRow{person: contrib.Person{Login: "b"}, report: reportOver("b", 60, 3), state: rowLoaded}

	first := calendarColumn(t, plain(renderTeamRow(short, 110, false)))
	second := calendarColumn(t, plain(renderTeamRow(long, 110, false)))
	if first != second {
		t.Errorf("calendars start at columns %d and %d, want them aligned", first, second)
	}
}

// calendarColumn is where the mini calendar starts on a row's first line.
func calendarColumn(t *testing.T, row string) int {
	t.Helper()
	line := strings.Split(row, "\n")[0]
	idx := strings.Index(line, miniUpper)
	if idx < 0 {
		if idx = strings.Index(line, miniLower); idx < 0 {
			t.Fatalf("row has no calendar:\n%s", row)
		}
	}
	return idx
}

// Seven weekdays into four lines is the whole point of the half-block cell.
func TestMiniCalendar_PacksTwoWeekdaysPerLine(t *testing.T) {
	got := plain(renderMiniCalendar(reportOver("a", 30, 1), 26))
	if lines := strings.Split(got, "\n"); len(lines) != 4 {
		t.Fatalf("mini calendar is %d lines, want 7 weekdays halved into 4:\n%s", len(lines), got)
	}
}

// A row with nothing to draw yet must not draw a calendar of empty days: a grid
// of blanks reads as a month of doing nothing.
func TestMiniCalendar_IsEmptyWithoutAReport(t *testing.T) {
	if got := renderMiniCalendar(nil, 26); got != "" {
		t.Errorf("mini calendar without a report = %q, want nothing", got)
	}
}

// However narrow the panel, a row stays inside it: a row wider than its panel
// makes lipgloss drop the border around the whole dashboard.
func TestTeamRow_StaysInsideItsWidth(t *testing.T) {
	row := teamRow{
		person: contrib.Person{Login: "nash-robin", Name: "Nash, Robin"},
		report: reportOver("nash-robin", 60, 3),
		state:  rowLoaded,
	}
	for width := 20; width <= 140; width += 4 {
		for _, line := range strings.Split(plain(renderTeamRow(row, width, true)), "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("width %d: line is %d columns: %q", width, w, line)
			}
		}
	}
}

// Below the width the calendar needs it is dropped rather than shredded, and
// the stats fold to one column rather than losing their labels.
func TestTeamRow_FoldsBeforeItTruncates(t *testing.T) {
	row := teamRow{
		person: contrib.Person{Login: "nash-robin", Name: "Nash, Robin"},
		report: reportOver("nash-robin", 30, 2),
		state:  rowLoaded,
	}

	narrow := plain(renderTeamRow(row, 46, false))
	if strings.Contains(narrow, miniUpper) || strings.Contains(narrow, miniLower) {
		t.Errorf("row kept its calendar with no room for it:\n%s", narrow)
	}
	for _, want := range []string{"commits", "reviews", "contributions"} {
		if !strings.Contains(narrow, want) {
			t.Errorf("folded row lost %q:\n%s", want, narrow)
		}
	}

	// One column of stats still fits a calendar when the terminal allows it.
	if got := plain(renderTeamRow(row, 62, false)); !strings.Contains(got, miniUpper) {
		t.Errorf("row dropped its calendar with room for one:\n%s", got)
	}
}

// Default windows.

// A person's history is read over a longer span than a team's standing: the
// dashboard answers "what is everyone up to lately", details answers "what has
// this person been doing".
func TestWindow_DetailsDefaultsToThreeMonths(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat")}
	m := startDashboard(t, newTeamService("octocat"), store, newFakeReportCache(), 120, 40)

	dashboard, err := m.window()
	if err != nil {
		t.Fatalf("dashboard window: %v", err)
	}
	if days := dashboard.To.Sub(dashboard.From).Hours() / 24; days < 29 || days > 32 {
		t.Errorf("dashboard window spans %.0f days, want the trailing 30", days)
	}

	m = pressAndSettle(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.screen != screenDetails {
		t.Fatalf("screen = %v, want details", m.screen)
	}

	details, err := m.window()
	if err != nil {
		t.Fatalf("details window: %v", err)
	}
	if days := details.To.Sub(details.From).Hours() / 24; days < 88 || days > 93 {
		t.Errorf("details window spans %.0f days, want the trailing three months", days)
	}
}

// The dates a person typed are the dates they asked for, whichever screen they
// are looking at.
func TestWindow_TypedDatesBeatTheDefault(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat")}
	m := startDashboard(t, newTeamService("octocat"), store, newFakeReportCache(), 120, 40)

	m.inputs[focusFrom].SetValue("2026-05")
	m.inputs[focusTo].SetValue("2026-05")

	got, err := m.window()
	if err != nil {
		t.Fatalf("window: %v", err)
	}
	if from := got.From.Format(contrib.DateFormat); from != "2026-05-01" {
		t.Errorf("from = %s, want the month expanded to its first day", from)
	}
	if to := got.To.Format(contrib.DateFormat); to != "2026-05-31" {
		t.Errorf("to = %s, want the month expanded to its last day", to)
	}
}

// The placeholder is the only thing on screen saying which window an untouched
// field resolves to, so it has to say the right one for the screen you are on.
func TestWindow_PlaceholdersFollowTheScreen(t *testing.T) {
	store := &fakeTeamStore{team: teamOf("octocat")}
	m := startDashboard(t, newTeamService("octocat"), store, newFakeReportCache(), 120, 40)

	onDashboard := m.inputs[focusFrom].Placeholder
	m = pressAndSettle(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	onDetails := m.inputs[focusFrom].Placeholder

	if onDashboard == onDetails {
		t.Fatalf("both screens advertise %q, want each to show its own default", onDashboard)
	}
	window, err := m.window()
	if err != nil {
		t.Fatalf("window: %v", err)
	}
	if onDetails != window.From.Format(contrib.DateFormat) {
		t.Errorf("placeholder = %q, want the window it resolves to (%s)",
			onDetails, window.From.Format(contrib.DateFormat))
	}
}

// A name long enough to fill its column must still leave a gap before the
// stats, or the two columns read as one word.
func TestTeamRow_LongNameKeepsItsColumn(t *testing.T) {
	row := teamRow{
		person: contrib.Person{
			Login: "bianchi-priya",
			Name:  "Bianchi, Priya [USA]",
		},
		report: reportOver("bianchi-priya", 9, 1),
		state:  rowLoaded,
	}

	first := strings.Split(plain(renderTeamRow(row, 110, false)), "\n")[0]

	// Clipped by display column, not by byte: an ellipsis is one column and
	// three bytes, and slicing bytes cuts it in half.
	name := lipgloss.NewStyle().MaxWidth(rowNameWidth).Render(first)
	if !strings.HasSuffix(name, " ") {
		t.Errorf("name column = %q, want it to end in a space:\n%s", name, first)
	}
	if !strings.Contains(first, "contributions") {
		t.Fatalf("row lost its stats:\n%s", first)
	}
}
