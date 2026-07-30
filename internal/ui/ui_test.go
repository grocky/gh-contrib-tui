package ui

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

func testPeople() []contrib.Person {
	return []contrib.Person{
		{Login: "nash-robin", Name: "Nash, Robin [USA]", Active: true},
		{Login: "avery-lynn", Name: "Avery, Lynn [USA]", Active: true},
		{Login: "Bianchi-Priya", Name: "Bianchi, Priya [USA]"},
	}
}

func sampleReport() *contrib.Report {
	window := contrib.Range{
		From: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 7, 29, 23, 59, 59, 0, time.UTC),
	}
	events := []contrib.Event{
		{OccurredAt: time.Date(2026, 7, 2, 9, 0, 0, 0, time.UTC), Kind: contrib.KindAuthored,
			Repo: "acme/repo1", Title: "Add feature", URL: "https://example.com/pr/1", Merged: true},
		{OccurredAt: time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC), Kind: contrib.KindReviewed,
			Repo: "acme/repo2", Title: "Review me", URL: "https://example.com/pr/9#review-1"},
		{OccurredAt: time.Date(2026, 7, 4, 9, 0, 0, 0, time.UTC), Kind: contrib.KindCommit,
			Repo: "acme/repo1", Title: "Fix the thing", URL: "https://example.com/commit/abc"},
	}
	return &contrib.Report{
		Login:      "octocat",
		Name:       "Mona",
		Collection: contrib.NewCollection(events, window),
	}
}

// Test helpers.

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

// newTestModel returns a sized model, which is what the program does before
// the first View.
func newTestModel(t *testing.T, svc ContributionService, w, h int) model {
	t.Helper()
	m := newModel()
	m.svc = svc
	return send(t, m, tea.WindowSizeMsg{Width: w, Height: h})
}

// send applies one message and returns the new model, dropping the command.
func send(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	got, ok := next.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", next)
	}
	return got
}

// sendCmd applies one message and returns the new model with its command.
func sendCmd(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	got, ok := next.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", next)
	}
	return got, cmd
}

func typeText(t *testing.T, m model, text string) model {
	t.Helper()
	for _, r := range text {
		m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func press(t *testing.T, m model, k tea.KeyType) model {
	t.Helper()
	return send(t, m, tea.KeyMsg{Type: k})
}

// runCmd executes a command the way the runtime does, flattening the batches
// that Update returns when it starts several things at once.
func runCmd(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, sub := range msg {
			runCmd(t, sub)
		}
	}
}

// sendAndSettle applies a message and then feeds the resulting messages back,
// the way the runtime's loop does. Components that work asynchronously -- the
// list's filtering, for one -- only finish if their own messages come home.
func sendAndSettle(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()

	queue := []tea.Msg{msg}
	for steps := 0; steps < 64 && len(queue) > 0; steps++ {
		next, cmd := sendCmd(t, m, queue[0])
		m, queue = next, queue[1:]
		queue = append(queue, collect(t, cmd)...)
	}
	return m
}

// collect drains a command into the messages it produces, flattening batches.
// Commands that do not answer within a tick are timers -- cursor blink,
// spinner ticks -- and are dropped rather than waited on.
func collect(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(50 * time.Millisecond):
		return nil
	}

	switch msg := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, sub := range msg {
			out = append(out, collect(t, sub)...)
		}
		return out
	default:
		return []tea.Msg{msg}
	}
}

// Layout.

// The whole point of a full-screen TUI is that it does not scroll. If the view
// is taller than the terminal, the header rolls off the top.
func TestView_FitsTheTerminal(t *testing.T) {
	sizes := []struct{ w, h int }{
		{80, 24}, {100, 30}, {120, 45}, {200, 60}, {80, 15}, {40, 20},
	}
	for _, size := range sizes {
		for _, withReport := range []bool{false, true} {
			m := newTestModel(t, &fakeService{}, size.w, size.h)
			if withReport {
				m = send(t, m, fetchedMsg{report: sampleReport()})
			}
			view := m.View()
			if got := lipgloss.Height(view); got > size.h {
				t.Errorf("%dx%d report=%v: view is %d rows, want at most %d",
					size.w, size.h, withReport, got, size.h)
			}
			if got := lipgloss.Width(view); got > size.w {
				t.Errorf("%dx%d report=%v: view is %d columns, want at most %d",
					size.w, size.h, withReport, got, size.w)
			}
		}
	}
}

// The suggestion dropdown grows the layout, and must not push it off-screen.
func TestView_FitsTheTerminalWithSuggestions(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 24)
	m.suggest.set(contrib.NewDirectory(manyPeople(20)))
	m = send(t, m, fetchedMsg{report: sampleReport()})

	// The results took focus; tab wraps from the list back to the user field.
	m = press(t, m, tea.KeyTab)
	m = typeText(t, m, "user")

	if len(m.suggestions) == 0 {
		t.Fatal("expected suggestions to be open for this test to mean anything")
	}
	if got := lipgloss.Height(m.View()); got > 24 {
		t.Errorf("view is %d rows with the dropdown open, want at most 24", got)
	}
}

// A title wider than its panel makes lipgloss give up on the border, and the
// filter box grows as it is typed into.
func TestView_FitsTheTerminalWhileFiltering(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 80, 24)
	m = send(t, m, fetchedMsg{report: sampleReport()})
	m = sendAndSettle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range "a filter query long enough to crowd the panel title out" {
		m = sendAndSettle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	view := m.View()
	if got := lipgloss.Width(view); got > 80 {
		t.Errorf("view is %d columns while filtering, want at most 80", got)
	}
	if got := lipgloss.Height(view); got > 24 {
		t.Errorf("view is %d rows while filtering, want at most 24", got)
	}
}

func manyPeople(n int) []contrib.Person {
	people := make([]contrib.Person, 0, n)
	for i := 0; i < n; i++ {
		people = append(people, contrib.Person{
			Login:  "user-" + string(rune('a'+i%26)),
			Name:   "User, Number",
			Active: true,
		})
	}
	return people
}

func TestView_BeforeSizingSaysSomething(t *testing.T) {
	if got := newModel().View(); !strings.Contains(got, "starting") {
		t.Fatalf("unsized view = %q", got)
	}
}

// Suggestions.

func TestSuggestions_OnlyAfterTyping(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 40)
	m.suggest.set(contrib.NewDirectory(testPeople()))

	if m.showingSuggestions() {
		t.Fatal("the dropdown must stay closed until the user types")
	}
	m = typeText(t, m, "rob")
	if !m.showingSuggestions() {
		t.Fatal("expected suggestions after typing")
	}
	if got := len(m.suggestions); got != 1 {
		t.Fatalf("suggestions = %d, want the one match: %+v", got, m.suggestions)
	}
	if !strings.Contains(plain(m.View()), "nash-robin") {
		t.Fatalf("dropdown not rendered:\n%s", plain(m.View()))
	}
}

func TestSuggestions_MatchOnDisplayName(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 40)
	m.suggest.set(contrib.NewDirectory(testPeople()))

	m = typeText(t, m, "lynn")
	if len(m.suggestions) != 1 || m.suggestions[0].Login != "avery-lynn" {
		t.Fatalf("suggestions = %+v", m.suggestions)
	}
}

func TestSuggestions_EnterAcceptsTheBareLogin(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 40)
	m.suggest.set(contrib.NewDirectory(testPeople()))

	m = typeText(t, m, "rob")
	m = press(t, m, tea.KeyEnter)

	if got := m.inputs[focusUser].Value(); got != "nash-robin" {
		t.Fatalf("user field = %q, want just the login", got)
	}
	if m.showingSuggestions() {
		t.Fatal("expected the dropdown to close on accept")
	}
}

func TestSuggestions_ArrowsMoveTheSelection(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 40)
	m.suggest.set(contrib.NewDirectory(testPeople()))

	m = typeText(t, m, "a") // matches all three
	if len(m.suggestions) < 2 {
		t.Fatalf("need several matches for this test, got %+v", m.suggestions)
	}
	m = press(t, m, tea.KeyDown)
	if m.suggestIndex != 1 {
		t.Fatalf("suggestIndex = %d, want 1", m.suggestIndex)
	}
	m = press(t, m, tea.KeyUp)
	if m.suggestIndex != 0 {
		t.Fatalf("suggestIndex = %d, want 0 after moving back", m.suggestIndex)
	}

	want := m.suggestions[0].Login
	m = press(t, m, tea.KeyEnter)
	if got := m.inputs[focusUser].Value(); got != want {
		t.Fatalf("user field = %q, want %q", got, want)
	}
}

func TestSuggestions_EscapeDismisses(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 40)
	m.suggest.set(contrib.NewDirectory(testPeople()))

	m = typeText(t, m, "rob")
	m = press(t, m, tea.KeyEsc)

	if m.showingSuggestions() {
		t.Fatal("expected escape to close the dropdown")
	}
	if got := m.inputs[focusUser].Value(); got != "rob" {
		t.Fatalf("user field = %q, want the typed text kept", got)
	}
}

func TestSuggestions_ClosedByLeavingTheField(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 40)
	m.suggest.set(contrib.NewDirectory(testPeople()))

	m = typeText(t, m, "rob")
	m = press(t, m, tea.KeyTab)

	if m.focus != focusFrom {
		t.Fatalf("focus = %v, want the From field", m.focus)
	}
	if len(m.suggestions) != 0 {
		t.Fatalf("expected the dropdown to close with the field, got %+v", m.suggestions)
	}
}

// Focus.

func TestFocus_CyclesForwardAndBack(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 40)

	for _, want := range []focusArea{focusFrom, focusTo, focusList, focusUser} {
		m = press(t, m, tea.KeyTab)
		if m.focus != want {
			t.Fatalf("focus = %v, want %v", m.focus, want)
		}
	}
	m = press(t, m, tea.KeyShiftTab)
	if m.focus != focusList {
		t.Fatalf("focus = %v, want it to wrap backwards to the list", m.focus)
	}
	if !m.inputs[focusUser].Focused() == false {
		t.Fatal("only the focused input may be focused")
	}
}

func TestFocus_TypingLandsInTheFocusedField(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 40)

	m = press(t, m, tea.KeyTab)
	m = typeText(t, m, "2026-07-01")

	if got := m.inputs[focusFrom].Value(); got != "2026-07-01" {
		t.Fatalf("from field = %q", got)
	}
	if got := m.inputs[focusUser].Value(); got != "" {
		t.Fatalf("user field = %q, want it untouched", got)
	}
}

// Fetching.

func TestFetch_RequiresAUser(t *testing.T) {
	svc := &fakeService{report: sampleReport()}
	m := newTestModel(t, svc, 100, 40)

	m, cmd := sendCmd(t, m, tea.KeyMsg{Type: tea.KeyCtrlR})

	if cmd != nil {
		t.Fatal("expected no fetch without a username")
	}
	if m.status != "user is required" || m.level != statusError {
		t.Fatalf("status = %q (level %v)", m.status, m.level)
	}
}

func TestFetch_ReportsAnInvalidRange(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 40)
	m = typeText(t, m, "octocat")
	m = press(t, m, tea.KeyTab) // From
	m = press(t, m, tea.KeyTab) // To
	m = typeText(t, m, "2026-07-29")

	m, cmd := sendCmd(t, m, tea.KeyMsg{Type: tea.KeyCtrlR})

	if cmd != nil {
		t.Fatal("expected no fetch for an invalid range")
	}
	if !strings.Contains(m.status, "from is required") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestFetch_PassesTheTrimmedUserAndWindow(t *testing.T) {
	svc := &fakeService{report: sampleReport()}
	m := newTestModel(t, svc, 100, 40)
	m = typeText(t, m, "  octocat  ")
	m.suggestions = nil
	m = press(t, m, tea.KeyTab)
	m = typeText(t, m, "2026-07-01")
	m = press(t, m, tea.KeyTab)
	m = typeText(t, m, "2026-07-29")

	m, cmd := sendCmd(t, m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if cmd == nil {
		t.Fatal("expected a fetch command")
	}
	if !m.busy {
		t.Fatal("expected the model to be busy while fetching")
	}
	runCmd(t, cmd) // the batch carries the fetch and the spinner tick

	if svc.gotUser != "octocat" {
		t.Fatalf("service received user %q, want it trimmed", svc.gotUser)
	}
	if got := svc.gotWindow.From.Format(time.RFC3339); got != "2026-07-01T00:00:00Z" {
		t.Fatalf("window.From = %s", got)
	}
}

func TestFetch_PopulatesTheViews(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 120, 40)

	m = send(t, m, fetchedMsg{report: sampleReport()})

	if m.busy {
		t.Fatal("expected busy to clear")
	}
	if got := len(m.list.Items()); got != 3 {
		t.Fatalf("list items = %d, want 3", got)
	}
	if m.focus != focusList {
		t.Fatalf("focus = %v, want the list so the results can be navigated", m.focus)
	}
	if m.level != statusOK || !strings.Contains(m.status, "3 contributions") {
		t.Fatalf("status = %q (level %v)", m.status, m.level)
	}

	view := plain(m.View())
	for _, want := range []string{
		"Mona", "2026-07-01 → 2026-07-29", "3 contributions",
		"1 commits", "1 prs (1 merged)", "1 reviews",
		"Contribution calendar", "Add feature", "acme/repo1", "merged", "review",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n%s", want, view)
		}
	}
}

// "/" filters the list. Its matching runs as a command, so this also pins the
// requirement that stray messages get back to the list.
func TestList_FilterNarrowsToMatchingContributions(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 120, 40)
	m = send(t, m, fetchedMsg{report: sampleReport()})
	if m.focus != focusList {
		t.Fatalf("focus = %v, want the list", m.focus)
	}

	m = sendAndSettle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range "Review me" {
		m = sendAndSettle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = sendAndSettle(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	visible := m.list.VisibleItems()
	if len(visible) != 1 {
		titles := make([]string, 0, len(visible))
		for _, item := range visible {
			titles = append(titles, item.(eventItem).Title)
		}
		t.Fatalf("visible = %v, want only the review", titles)
	}
	if got := visible[0].(eventItem).Title; got != "Review me" {
		t.Fatalf("visible = %q", got)
	}
	if got := m.selectedURL(); got != "https://example.com/pr/9#review-1" {
		t.Fatalf("selected = %q, want the filtered row", got)
	}
}

func TestFetch_KeepsFocusWhenThereIsNothingToShow(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 40)
	empty := &contrib.Report{Login: "ghost"}

	m = send(t, m, fetchedMsg{report: empty})

	if m.focus != focusUser {
		t.Fatalf("focus = %v, want it left in the user field", m.focus)
	}
}

func TestFetch_SurfacesServiceErrors(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 40)

	m = send(t, m, fetchedMsg{err: errors.New("graphql exploded")})

	if m.level != statusError || !strings.Contains(m.status, "graphql exploded") {
		t.Fatalf("status = %q (level %v)", m.status, m.level)
	}
	if m.busy {
		t.Fatal("expected busy to clear on failure")
	}
	if got := len(m.list.Items()); got != 0 {
		t.Fatalf("list items = %d, want none on failure", got)
	}
}

func TestFetch_ShowsWarningsFromThePartialReport(t *testing.T) {
	report := sampleReport()
	report.Warnings = []string{"commits unavailable: search is disabled"}
	m := newTestModel(t, &fakeService{}, 120, 40)

	m = send(t, m, fetchedMsg{report: report})

	if !strings.Contains(plain(m.View()), "commits unavailable") {
		t.Fatalf("expected the gap to be visible:\n%s", plain(m.View()))
	}
}

// Suggestions plumbing.

func TestDiscovered_InstallsTheDirectory(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 40)

	m = send(t, m, discoveredMsg{people: testPeople(), source: "github"})

	if got := m.suggest.len(); got != 3 {
		t.Fatalf("directory size = %d, want 3", got)
	}
	if logs := strings.Join(m.logs.Lines(), "\n"); !strings.Contains(logs, "3 user suggestions from github") {
		t.Fatalf("logs = %q", logs)
	}
}

func TestDiscovered_CachesWhatCameFromGitHub(t *testing.T) {
	cache := &fakeCache{}
	m := newTestModel(t, &fakeService{}, 100, 40)
	m.cache = cache

	m, cmd := sendCmd(t, m, discoveredMsg{people: testPeople(), source: "github"})
	if cmd == nil {
		t.Fatal("expected a cache-store command")
	}
	cmd()

	if len(cache.stored) != 3 {
		t.Fatalf("stored = %+v, want the three people", cache.stored)
	}
}

func TestDiscovered_DoesNotWriteTheCacheBackFromItself(t *testing.T) {
	cache := &fakeCache{}
	m := newTestModel(t, &fakeService{}, 100, 40)
	m.cache = cache

	_, cmd := sendCmd(t, m, discoveredMsg{people: testPeople(), source: "cache"})

	if cmd != nil {
		t.Fatal("cached suggestions must not be written straight back")
	}
}

func TestDiscovered_FailureIsOnlyLoggedUnlessForced(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 40)
	before := m.status

	m = send(t, m, discoveredMsg{err: errors.New("no read:org"), source: "github"})
	if m.status != before {
		t.Fatalf("status = %q, want the background failure kept out of the way", m.status)
	}

	m = send(t, m, discoveredMsg{err: errors.New("no read:org"), forced: true, source: "github"})
	if m.level != statusError || !strings.Contains(m.status, "no read:org") {
		t.Fatalf("status = %q (level %v), want the forced failure reported", m.status, m.level)
	}
}

func TestDiscovered_ForcedRefreshReportsTheCount(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 40)
	m.busy = true

	m = send(t, m, discoveredMsg{people: testPeople(), forced: true, source: "github"})

	if m.busy {
		t.Fatal("expected busy to clear after a forced refresh")
	}
	if m.level != statusOK || !strings.Contains(m.status, "3 users loaded") {
		t.Fatalf("status = %q (level %v)", m.status, m.level)
	}
}

func TestRefreshKey_StartsDiscovery(t *testing.T) {
	svc := &fakeService{people: testPeople()}
	m := newTestModel(t, svc, 100, 40)

	m, cmd := sendCmd(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	if cmd == nil {
		t.Fatal("expected a discovery command")
	}
	if !m.busy {
		t.Fatal("expected the model to be busy while refreshing")
	}
}

// Selection actions.

func TestCopy_WithNothingSelected(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 40)

	m = send(t, m, tea.KeyMsg{Type: tea.KeyCtrlY})

	if m.level != statusWarn || !strings.Contains(m.status, "nothing selected") {
		t.Fatalf("status = %q (level %v)", m.status, m.level)
	}
}

func TestSelectedURL_FollowsTheList(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 40)
	m = send(t, m, fetchedMsg{report: sampleReport()})

	if got := m.selectedURL(); got != "https://example.com/commit/abc" {
		t.Fatalf("selected = %q, want the newest event", got)
	}
	m = press(t, m, tea.KeyDown)
	if got := m.selectedURL(); got != "https://example.com/pr/9#review-1" {
		t.Fatalf("selected = %q, want the next event down", got)
	}
}

// Logs.

func TestLogs_ToggleAndDismiss(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 40)
	m.logs.Logf("[green]hello from the api layer[-]")

	m = send(t, m, tea.KeyMsg{Type: tea.KeyCtrlL})
	if !m.showLogs {
		t.Fatal("expected the log pane to open")
	}
	view := plain(m.View())
	if !strings.Contains(view, "hello from the api layer") {
		t.Fatalf("log pane missing the line:\n%s", view)
	}
	if strings.Contains(view, "[green]") {
		t.Fatalf("colour tags leaked into the view:\n%s", view)
	}

	m = press(t, m, tea.KeyEsc)
	if m.showLogs {
		t.Fatal("expected escape to close the log pane")
	}
}

// A log pane pinned to the top of a long buffer shows the oldest lines, which
// is never what someone opening it wants to read.
func TestLogs_FollowTheTail(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 24)
	for i := 0; i < 60; i++ {
		m.logs.Logf("line %d", i)
	}

	m = send(t, m, tea.KeyMsg{Type: tea.KeyCtrlL})

	view := plain(m.View())
	if !strings.Contains(view, "line 59") {
		t.Errorf("newest line is not on screen:\n%s", view)
	}
	if strings.Contains(view, "line 0 ") {
		t.Errorf("pane is showing the top of the buffer:\n%s", view)
	}

	// Lines that land while the pane is open follow too.
	m.logs.Logf("line 60")
	m = send(t, m, discoveredMsg{people: testPeople(), source: "cache"})
	if !strings.Contains(plain(m.View()), "line 60") {
		t.Errorf("an arriving line did not scroll into view:\n%s", plain(m.View()))
	}
}

// Following the tail must not fight someone scrolling back through it.
func TestLogs_ScrollingBackHoldsPosition(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 24)
	for i := 0; i < 60; i++ {
		m.logs.Logf("line %d", i)
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyCtrlL})

	for i := 0; i < 5; i++ {
		m = press(t, m, tea.KeyUp)
	}
	if m.logView.AtBottom() {
		t.Fatal("expected the arrow keys to scroll the log pane back")
	}

	m.logs.Logf("line 60")
	m = send(t, m, discoveredMsg{people: testPeople(), source: "cache"})

	if m.logView.AtBottom() {
		t.Error("an arriving line yanked the pane away from where it was parked")
	}
}

// The filter fields stay on screen behind the log pane, cursor and all, so
// keystrokes meant for them cannot be swallowed by the viewport.
func TestLogs_TypingStillReachesTheFocusedField(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 24)

	m = send(t, m, tea.KeyMsg{Type: tea.KeyCtrlL})
	m = typeText(t, m, "octocat")

	if got := m.inputs[focusUser].Value(); got != "octocat" {
		t.Fatalf("user field = %q, want the typing to land with the log pane open", got)
	}
}

// The log pane covers the list, so keys that would steer the list have to stop
// there rather than filter and scroll something nobody can see.
func TestLogs_KeysDoNotReachTheHiddenList(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 100, 24)
	m = send(t, m, fetchedMsg{report: sampleReport()})
	if m.focus != focusList {
		t.Fatalf("focus = %v, want the list for this test to mean anything", m.focus)
	}

	m = send(t, m, tea.KeyMsg{Type: tea.KeyCtrlL})

	var before [inputCount]string
	for i, in := range m.inputs {
		before[i] = in.Value()
	}
	selected := m.list.Index()

	m = sendAndSettle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeText(t, m, "xyz")

	if m.list.SettingFilter() {
		t.Error(`"/" opened a filter on the list hidden behind the log pane`)
	}
	if got := m.list.Index(); got != selected {
		t.Errorf("selection moved to %d, want it left at %d", got, selected)
	}
	for i, in := range m.inputs {
		if in.Value() != before[i] {
			t.Errorf("input %d = %q, want it left at %q", i, in.Value(), before[i])
		}
	}
}

func TestLogBuffer_StripsTagsAndReadsSeverity(t *testing.T) {
	b := newLogBuffer()
	b.Logf("[red]fetch failed: %v[-]", errors.New("boom"))
	b.Logf("plain line with [ORIGINOPS-1234] in it")

	lines := b.Lines()
	if lines[0] != "fetch failed: boom" {
		t.Fatalf("line = %q, want the tags stripped", lines[0])
	}
	if lines[1] != "plain line with [ORIGINOPS-1234] in it" {
		t.Fatalf("line = %q, want unknown brackets preserved", lines[1])
	}
	if got := levelOf("[red]nope[-]"); got != statusError {
		t.Fatalf("levelOf red = %v", got)
	}
	if got := levelOf("[yellow]hmm[-]"); got != statusWarn {
		t.Fatalf("levelOf yellow = %v", got)
	}
	if got := levelOf("no tags"); got != statusInfo {
		t.Fatalf("levelOf untagged = %v", got)
	}
}

func TestLogBuffer_BoundsItsMemory(t *testing.T) {
	b := newLogBuffer()
	for i := 0; i < maxLogLines+50; i++ {
		b.Logf("line %d", i)
	}
	if got := len(b.Lines()); got != maxLogLines {
		t.Fatalf("lines = %d, want the %d cap", got, maxLogLines)
	}
	if got := b.Lines()[0]; got != "line 50" {
		t.Fatalf("oldest kept line = %q, want the oldest dropped", got)
	}
}

// Heatmap.

func TestRenderHeatmap_DrawsAWeekPerColumn(t *testing.T) {
	cal := sampleReport().Collection.Calendar
	got := plain(renderHeatmap(cal, 80))

	lines := strings.Split(got, "\n")
	if len(lines) != 8 {
		t.Fatalf("lines = %d, want a month row and seven weekdays:\n%s", len(lines), got)
	}
	if !strings.Contains(lines[0], "Jun") && !strings.Contains(lines[0], "Jul") {
		t.Fatalf("month row = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "Mon") {
		t.Fatalf("first weekday row = %q", lines[1])
	}
	// Five week columns cover 2026-07-01 to 2026-07-29.
	if got := strings.Count(lines[1], dayCell); got != 5 {
		t.Fatalf("Monday cells = %d, want 5: %q", got, lines[1])
	}
}

func TestRenderHeatmap_ClampsToWidth(t *testing.T) {
	window := contrib.Range{
		From: time.Date(2025, 7, 29, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 7, 29, 23, 59, 59, 0, time.UTC),
	}
	cal := contrib.NewCollection(nil, window).Calendar
	if len(cal.Weeks) < 52 {
		t.Fatalf("weeks = %d, want a full year", len(cal.Weeks))
	}

	got := plain(renderHeatmap(cal, 30))
	for _, line := range strings.Split(got, "\n") {
		if w := lipgloss.Width(line); w > 30 {
			t.Fatalf("line is %d columns, want it clamped to 30: %q", w, line)
		}
	}
}

func TestRenderHeatmap_EmptyCalendar(t *testing.T) {
	if got := plain(renderHeatmap(contrib.Calendar{}, 40)); !strings.Contains(got, "no calendar data") {
		t.Fatalf("empty calendar = %q", got)
	}
}

// List rows.

func TestEventLabel(t *testing.T) {
	tests := []struct {
		event contrib.Event
		want  string
	}{
		{contrib.Event{Kind: contrib.KindCommit}, "commit"},
		{contrib.Event{Kind: contrib.KindIssue}, "issue"},
		{contrib.Event{Kind: contrib.KindAuthored}, "pr"},
		{contrib.Event{Kind: contrib.KindAuthored, Merged: true}, "merged"},
		{contrib.Event{Kind: contrib.KindReviewed}, "review"},
	}
	for _, tc := range tests {
		if got := eventLabel(tc.event); got != tc.want {
			t.Errorf("eventLabel(%+v) = %q, want %q", tc.event, got, tc.want)
		}
		if len(tc.want) > badgeWidth {
			t.Errorf("label %q is wider than the badge column", tc.want)
		}
	}
}

func TestEventItem_FilterValueCoversTitleRepoAndKind(t *testing.T) {
	item := eventItem{Event: contrib.Event{
		Kind: contrib.KindReviewed, Repo: "acme/repo1", Title: "Add feature",
	}}
	for _, want := range []string{"Add feature", "acme/repo1", "review"} {
		if !strings.Contains(item.FilterValue(), want) {
			t.Errorf("FilterValue() = %q, want it to include %q", item.FilterValue(), want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		in    string
		width int
		want  string
	}{
		{"short", 10, "short"},
		{"exactly-10", 10, "exactly-10"},
		{"truncate me", 8, "truncat…"},
		{"anything", 0, ""},
		{"anything", 1, "…"},
	}
	for _, tc := range tests {
		if got := truncate(tc.in, tc.width); got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
		}
	}
}

// Public surface.

// Columns are laid out by display width; a name measured in bytes pushes the
// one beside it out of line.
func TestPad_MeasuresDisplayWidth(t *testing.T) {
	tests := []struct {
		in    string
		width int
		want  string
	}{
		{"Mon", 4, "Mon "},
		{"", 3, "   "},
		{"Åse", 6, "Åse   "},
		{"exactly-10", 10, "exactly-10"},
		{"far too long for this column", 8, "far too…"},
	}
	for _, tc := range tests {
		got := pad(tc.in, tc.width)
		if got != tc.want {
			t.Errorf("pad(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
		}
		if w := lipgloss.Width(got); w != tc.width {
			t.Errorf("pad(%q, %d) is %d columns wide", tc.in, tc.width, w)
		}
	}
}

func TestRun_WithoutServiceFails(t *testing.T) {
	if err := New().Run(); err == nil {
		t.Fatal("expected Run to fail without a configured service")
	}
}

func TestNew_WithDefaultUserPrefillsField(t *testing.T) {
	app := New(WithDefaultUser("  octocat  "))
	if got := app.model.inputs[focusUser].Value(); got != "octocat" {
		t.Fatalf("user field = %q, want the trimmed default", got)
	}
}

func TestApp_LogfReachesTheModel(t *testing.T) {
	app := New(WithHost("github.example.com"))
	app.Logf("[gray]hello %s[-]", "world")

	if got := app.model.logs.Lines(); len(got) != 1 || got[0] != "hello world" {
		t.Fatalf("logs = %+v", got)
	}
	if app.model.host != "github.example.com" {
		t.Fatalf("host = %q", app.model.host)
	}
}

func TestApp_OptionsAndSetters(t *testing.T) {
	svc := &fakeService{}
	cache := &fakeCache{}

	app := New(WithService(svc), WithDirectoryCache(cache))
	if app.model.svc != ContributionService(svc) || app.model.cache != DirectoryCache(cache) {
		t.Fatal("options did not reach the model")
	}

	other := &fakeService{}
	app.SetService(other)
	app.SetDirectoryCache(&fakeCache{})
	app.SetHost("github.other.com")
	if app.model.svc != ContributionService(other) || app.model.host != "github.other.com" {
		t.Fatal("setters did not reach the model")
	}
}

func TestInit_LoadsCacheAndDiscovers(t *testing.T) {
	svc := &fakeService{people: testPeople()}
	cache := &fakeCache{people: testPeople()}
	m := newModel()
	m.svc, m.cache = svc, cache

	if cmd := m.Init(); cmd == nil {
		t.Fatal("expected Init to start background work")
	}
	// The commands are batched; run them the way the runtime would.
	if msg := m.loadCacheCmd()(); msg == nil {
		t.Fatal("expected the cache to produce suggestions")
	}
	if cache.loads != 1 {
		t.Fatalf("cache loads = %d, want 1", cache.loads)
	}
	if msg := m.discoverCmd(false)(); msg == nil {
		t.Fatal("expected discovery to produce a message")
	}
	if svc.discovers != 1 {
		t.Fatalf("discovers = %d, want 1", svc.discovers)
	}
}

func TestQuitKeys(t *testing.T) {
	for _, k := range []tea.KeyType{tea.KeyCtrlC, tea.KeyCtrlQ} {
		m := newTestModel(t, &fakeService{}, 100, 40)
		_, cmd := sendCmd(t, m, tea.KeyMsg{Type: k})
		if cmd == nil {
			t.Fatalf("%v did not quit", k)
		}
	}
}
