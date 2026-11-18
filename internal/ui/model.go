package ui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/grocky/gh-contrib-tui/internal/contrib"
	"github.com/grocky/gh-contrib-tui/internal/platform"
	"github.com/grocky/gh-contrib-tui/internal/reportcache"
	"github.com/grocky/gh-contrib-tui/internal/teamconfig"
)

// discoveryTimeout bounds the background user-suggestion lookup.
const discoveryTimeout = 30 * time.Second

// focusArea is which part of the UI takes keystrokes.
type focusArea int

const (
	focusUser focusArea = iota
	focusFrom
	focusTo
	focusList
)

const inputCount = 3

// The default windows the two screens open on.
const (
	dashboardDays = 30
	detailsMonths = 3
)

// pendingFetch is a details request in flight.
type pendingFetch struct {
	login  string
	window contrib.Range
}

func (p pendingFetch) active() bool { return p.login != "" }

// answeredBy reports whether a result is the one being waited for.
func (p pendingFetch) answeredBy(login string, window contrib.Range) bool {
	return p.active() && strings.EqualFold(p.login, login) && p.window == window
}

// screen is which of the two views is on show. The dashboard summarizes the
// whole team; details is one person's history, which is where the app started
// life and what a dashboard row opens into.
type screen int

const (
	screenDashboard screen = iota
	screenDetails
)

// Messages produced by background work.
type (
	fetchedMsg struct {
		login  string
		window contrib.Range
		report *contrib.Report
		err    error
	}
	discoveredMsg struct {
		people []contrib.Person
		err    error
		forced bool
		source string
	}
	teamLoadedMsg struct {
		team teamconfig.Team
		err  error
	}
	viewerMsg struct {
		person contrib.Person
		err    error
	}
	teamFetchedMsg struct {
		login  string
		window contrib.Range
		report *contrib.Report
		err    error
	}
)

// model is the bubbletea application state.
type model struct {
	svc     ContributionService
	cache   DirectoryCache
	teams   TeamStore
	reports ReportCache
	logs    *logBuffer
	suggest *suggestionStore
	host    string

	inputs  [inputCount]textinput.Model
	focus   focusArea
	list    list.Model
	spinner spinner.Model
	help    help.Model
	logView viewport.Model

	suggestions  []contrib.Person
	suggestIndex int

	// The dashboard: the roster, a row per member, and whether the add field is
	// open over it.
	screen   screen
	team     teamconfig.Team
	teamList list.Model
	adding   bool

	// teamCursorMoved records that someone has driven the roster list, which is
	// what turns the cursor from "the top row" into "this person".
	teamCursorMoved bool

	// repoChart is which view of the repository breakdown the details screen is
	// showing.
	repoChart repoChartMode

	// pending is the request the details screen is waiting on. A fetch takes
	// long enough that the user can be on somebody else by the time it lands,
	// and an answer to a question nobody is asking any more is somebody else's
	// data.
	pending pendingFetch

	report *contrib.Report
	status string
	level  statusLevel
	busy   bool

	showLogs bool
	width    int
	height   int
	ready    bool

	// Set by resize: whether there is vertical room for these panels.
	showSummary  bool
	showCalendar bool
}

func newModel() model {
	m := model{
		logs:     newLogBuffer(),
		suggest:  &suggestionStore{},
		list:     newEventList(),
		teamList: newTeamList(),
		help:     help.New(),
		spinner:  spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(mutedStyle)),
		logView:  viewport.New(0, 0),
		status:   "loading your team...",
	}
	m.help.Styles.ShortKey = mutedStyle
	m.help.Styles.ShortDesc = faintStyle
	m.help.Styles.ShortSeparator = faintStyle

	placeholders := [inputCount]string{"login or name", "", ""}
	widths := [inputCount]int{24, 12, 12}
	for i := range m.inputs {
		in := textinput.New()
		in.Placeholder = placeholders[i]
		in.CharLimit = 64
		in.Width = widths[i]
		in.Prompt = ""
		in.PlaceholderStyle = faintStyle
		m.inputs[i] = in
	}
	m.focus = focusList

	// The date fields advertise the window an empty pair resolves to, so a
	// screen says which range it is showing without putting text in the way of
	// the next thing typed.
	m.advertiseWindow()
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.loadTeamCmd(), m.loadCacheCmd(), m.discoverCmd(false))
}

// window is the range the current screen works over. Typed dates win; empty
// fields fall back to the screen's own default.
func (m model) window() (contrib.Range, error) {
	from := strings.TrimSpace(m.inputs[focusFrom].Value())
	to := strings.TrimSpace(m.inputs[focusTo].Value())
	if from == "" && to == "" {
		return m.defaultWindow(), nil
	}
	return contrib.ParseRange(from, to)
}

// defaultWindow is what an untouched pair of date fields means here.
//
// The two screens ask different questions. The dashboard asks what the team has
// been up to lately, which is a recent span; details asks what one person has
// been doing, which is read over a longer one.
func (m model) defaultWindow() contrib.Range {
	if m.screen == screenDetails {
		return contrib.TrailingMonths(detailsMonths)
	}
	return contrib.TrailingDays(dashboardDays)
}

// advertiseWindow spells the current default out in the date placeholders. They
// are the only thing on screen saying what an empty field resolves to, so they
// follow the screen rather than being fixed at startup.
func (m *model) advertiseWindow() {
	window := m.defaultWindow()
	m.inputs[focusFrom].Placeholder = window.From.Format(contrib.DateFormat)
	m.inputs[focusTo].Placeholder = window.To.Format(contrib.DateFormat)
}

// setTeamRows orders the dashboard and installs it.
//
// What the cursor holds onto depends on whether anyone has touched it. Rows
// arrive unsorted and settle as reports land, so a cursor nobody has moved
// rides the top of the list rather than clinging to whoever happened to be
// first before the numbers came in. Once it has been moved it belongs to a
// person, and a report landing elsewhere on the team must not slide a stranger
// under it mid-read.
func (m *model) setTeamRows(items []list.Item) {
	selected := ""
	if row, ok := m.selectedRow(); ok && m.teamCursorMoved {
		selected = row.person.Login
	}

	sortTeamRows(items)
	m.teamList.SetItems(items)

	if selected == "" {
		m.teamList.Select(0)
		return
	}
	m.selectLogin(selected)
}

// sortTeamRows puts the busiest first. The dashboard is read top down, and the
// order people were added to the config in says nothing about who to look at.
//
// A row with no report cannot be ranked, so it waits at the bottom rather than
// being counted as a zero: that would rank someone by a number nobody measured.
// Ties fall back to the name, so a quiet week does not reshuffle the list on
// every refresh.
func sortTeamRows(items []list.Item) {
	sort.SliceStable(items, func(i, j int) bool {
		a, aok := items[i].(teamRow)
		b, bok := items[j].(teamRow)
		if !aok || !bok {
			return aok
		}

		if (a.report != nil) != (b.report != nil) {
			return a.report != nil
		}
		if a.report != nil {
			if at, bt := a.report.Collection.Calendar.Total, b.report.Collection.Calendar.Total; at != bt {
				return at > bt
			}
		}
		return strings.ToLower(a.person.DisplayName()) < strings.ToLower(b.person.DisplayName())
	})
}

// selectLogin puts the cursor on a team member by login.
func (m *model) selectLogin(login string) {
	for i, item := range m.teamList.Items() {
		if row, ok := item.(teamRow); ok && strings.EqualFold(row.person.Login, login) {
			m.teamList.Select(i)
			return
		}
	}
}

// rows returns the dashboard's rows.
func (m model) rows() []teamRow {
	items := m.teamList.Items()
	rows := make([]teamRow, 0, len(items))
	for _, item := range items {
		if row, ok := item.(teamRow); ok {
			rows = append(rows, row)
		}
	}
	return rows
}

// selectedRow is the team member under the cursor.
func (m model) selectedRow() (teamRow, bool) {
	row, ok := m.teamList.SelectedItem().(teamRow)
	return row, ok
}

// Update handles one message. Everything that can change the layout -- a
// report arriving, the dropdown opening, the log pane toggling -- goes through
// here, so the layout is recomputed once on the way out rather than at each of
// the dozen places that could have invalidated it.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	next.resize()
	return next, cmd
}

func (m model) update(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		return m, nil

	case spinner.TickMsg:
		if !m.busy {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case fetchedMsg:
		return m.applyFetched(msg)

	case discoveredMsg:
		return m.applyDiscovered(msg)

	case teamLoadedMsg:
		return m.applyTeamLoaded(msg)

	case viewerMsg:
		return m.applyViewer(msg)

	case teamFetchedMsg:
		return m.applyTeamFetched(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Anything else belongs to the list on show: it filters asynchronously, so
	// its own FilterMatchesMsg has to get back to it or "/" silently does
	// nothing.
	var cmd tea.Cmd
	if m.screen == screenDashboard {
		m.teamList, cmd = m.teamList.Update(msg)
		return m, cmd
	}
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// applyTeamLoaded takes the roster from disk. A first launch -- no config at
// all -- seeds the team with whoever is running the app, so the dashboard is
// never an empty screen with no way in. A team that exists and is empty is a
// decision somebody made, and is left alone.
func (m model) applyTeamLoaded(msg teamLoadedMsg) (model, tea.Cmd) {
	switch {
	case errors.Is(msg.err, teamconfig.ErrNotConfigured):
		m.logs.Logf("[gray]no team configured yet; seeding it with your own account[-]")
		return m, m.viewerCmd()
	case msg.err != nil:
		m.setStatus("team config unreadable: "+msg.err.Error(), statusError)
		m.logs.Logf("[red]team config: %v[-]", msg.err)
		return m, nil
	}

	m.team = msg.team
	m.logs.Logf("[gray]team loaded members=%d[-]", len(m.team.Members))
	return m.syncTeam(false)
}

// applyViewer seeds a first-launch team with the authenticated account.
func (m model) applyViewer(msg viewerMsg) (model, tea.Cmd) {
	if msg.err != nil {
		m.setStatus("could not identify you: "+msg.err.Error(), statusWarn)
		m.logs.Logf("[yellow]viewer lookup failed: %v[-]", msg.err)
		return m, nil
	}

	m.team = teamconfig.Team{}.Add(msg.person)
	m.logs.Logf("[green]seeded your team with %s[-]", msg.person.Login)

	next, cmd := m.syncTeam(false)
	return next, tea.Batch(m.saveTeamCmd(next.team), cmd)
}

// syncTeam rebuilds the rows to match the roster and goes after whatever data
// is missing. Cached reports paint first: a launch shows numbers immediately
// and refreshes behind them, rather than showing a screen of spinners.
//
// force skips the cache entirely, which is what ctrl+r is for.
func (m model) syncTeam(force bool) (model, tea.Cmd) {
	window, err := m.window()
	if err != nil {
		m.setStatus(err.Error(), statusError)
		return m, nil
	}

	previous := map[string]teamRow{}
	for _, row := range m.rows() {
		previous[strings.ToLower(row.person.Login)] = row
	}

	items := make([]list.Item, 0, len(m.team.Members))
	cmds := make([]tea.Cmd, 0, len(m.team.Members))
	for _, person := range m.team.Members {
		row := teamRow{person: person}
		if prev, ok := previous[strings.ToLower(person.Login)]; ok {
			row = prev
			row.person = person
		}
		// A report fetched for another range says nothing about this one.
		if row.report != nil && row.window != window {
			row = teamRow{person: person}
		}

		if !force {
			if entry, ok := m.cachedReport(person.Login, window); ok {
				row.report = entry.Report
				row.window = window
				row.fetchedAt = entry.FetchedAt
				row.state = rowLoaded
				row.stale = !entry.Fresh
				if entry.Fresh {
					items = append(items, row)
					continue
				}
			}
		}

		if row.report == nil {
			row.state = rowLoading
		}
		row.err = ""
		items = append(items, row)
		cmds = append(cmds, m.fetchTeamCmd(person.Login, window))
	}

	m.setTeamRows(items)
	if len(cmds) == 0 {
		return m.settleTeamStatus(), nil
	}

	m.busy = true
	m.setStatus(fmt.Sprintf("refreshing %d of %d...", len(cmds), len(items)), statusInfo)
	return m, tea.Batch(append(cmds, m.spinner.Tick)...)
}

// cachedReport reads the report cache, which may not be configured at all.
func (m model) cachedReport(login string, window contrib.Range) (reportcache.Entry, bool) {
	if m.reports == nil {
		return reportcache.Entry{}, false
	}
	entry, ok := m.reports.Load(login, window)
	if !ok || entry.Report == nil {
		return reportcache.Entry{}, false
	}
	return entry, true
}

// applyTeamFetched folds one member's result into their row. Every member is
// fetched independently, so results arrive in whatever order they finish.
func (m model) applyTeamFetched(msg teamFetchedMsg) (model, tea.Cmd) {
	items := m.teamList.Items()
	for i, item := range items {
		row, ok := item.(teamRow)
		if !ok || !strings.EqualFold(row.person.Login, msg.login) {
			continue
		}

		if msg.err != nil {
			row.state, row.err, row.stale = rowFailed, firstLine(msg.err.Error()), false
			m.logs.Logf("[red]team fetch %s failed: %v[-]", msg.login, msg.err)
			items[i] = row
			m.setTeamRows(items)
			return m.settleTeamStatus(), nil
		}

		row.report = msg.report
		row.window = msg.window
		row.fetchedAt = time.Now()
		row.state, row.stale, row.err = rowLoaded, false, ""
		if row.person.Name == "" {
			row.person.Name = msg.report.Name
		}
		items[i] = row
		m.setTeamRows(items)
		return m.settleTeamStatus(), m.storeReportCmd(msg.login, msg.window, msg.report)
	}
	return m, nil
}

// settleTeamStatus reports how the dashboard stands once a row has landed.
func (m model) settleTeamStatus() model {
	if m.screen != screenDashboard {
		return m
	}

	loading, failed := 0, 0
	for _, row := range m.rows() {
		switch row.state {
		case rowLoading:
			loading++
		case rowFailed:
			failed++
		}
	}
	if loading > 0 {
		m.busy = true
		m.setStatus(fmt.Sprintf("refreshing %d of %d...", loading, len(m.rows())), statusInfo)
		return m
	}

	m.busy = false
	switch {
	case len(m.team.Members) == 0:
		m.setStatus("no one on your team yet — press a to add someone", statusInfo)
	case failed > 0:
		m.setStatus(fmt.Sprintf("%d of %d could not be loaded", failed, len(m.rows())), statusWarn)
	default:
		m.setStatus(fmt.Sprintf("%d on your team", len(m.rows())), statusOK)
	}
	return m
}

// firstLine keeps a row's error to something that fits beside a name.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// handleKey routes a keystroke. Order matters: the open suggestion list, the
// add field, and an active filter all claim keys that mean something else
// elsewhere.
func (m model) handleKey(msg tea.KeyMsg) (model, tea.Cmd) {
	if key.Matches(msg, keys.Quit) {
		return m, tea.Quit
	}

	// While a list is being filtered, typing belongs to the filter.
	if m.focus == focusList && m.activeList().SettingFilter() {
		return m.updateActiveList(msg)
	}

	// The add field owns enter and escape while it is open: enter puts the
	// person on the team, and escape has to close the field before it can mean
	// "leave this screen".
	if m.adding {
		switch {
		case key.Matches(msg, keys.Accept):
			return m.acceptAdd()
		case key.Matches(msg, keys.Dismiss):
			return m.closeAdd(), nil
		}
	}

	if m.showingSuggestions() {
		if handled, next, cmd := m.handleSuggestionKey(msg); handled {
			return next, cmd
		}
	}

	if m.screen == screenDashboard {
		if handled, next, cmd := m.handleDashboardKey(msg); handled {
			return next, cmd
		}
	} else {
		switch {
		case key.Matches(msg, keys.Back) && !m.showLogs:
			return m.showDashboard(), nil
		case key.Matches(msg, keys.RepoChart):
			m.repoChart = m.repoChart.next()
			return m, nil
		}
	}

	switch {
	case key.Matches(msg, keys.Next):
		return m.moveFocus(1), nil
	case key.Matches(msg, keys.Prev):
		return m.moveFocus(-1), nil
	case key.Matches(msg, keys.Fetch):
		// The same key forces a refetch on the dashboard; it means the same
		// thing here.
		return m.startFetch(true)
	case key.Matches(msg, keys.Refresh):
		m.busy = true
		m.setStatus("refreshing user list...", statusInfo)
		return m, tea.Batch(m.discoverCmd(true), m.spinner.Tick)
	case key.Matches(msg, keys.Copy):
		return m.copySelected(), nil
	case key.Matches(msg, keys.Logs):
		m.showLogs = !m.showLogs
		if m.showLogs {
			// Reopening lands on the newest lines even if the pane was closed
			// halfway up the buffer.
			m.logView.GotoBottom()
		}
		return m, nil
	case key.Matches(msg, keys.Dismiss) && m.showLogs:
		m.showLogs = false
		return m, nil
	case key.Matches(msg, keys.Open):
		if m.focus == focusList {
			return m.openSelected(), nil
		}
		// Enter is "show me this", which a fresh cache answers perfectly well.
		return m.startFetch(false)
	}

	// The log pane replaces the list, but the filter fields stay on screen and
	// keep their cursor, so it claims only the keys it needs to scroll. Handing
	// it every keystroke swallows typing into a field the user can still see.
	if m.showLogs && key.Matches(msg, keys.Scroll) {
		var cmd tea.Cmd
		m.logView, cmd = m.logView.Update(msg)
		return m, cmd
	}

	if m.focus == focusList {
		// The log pane has replaced the list. Left to it, "/" would open a
		// filter and the arrow keys would move a selection on a list nobody can
		// see, and closing the pane would reveal the damage.
		if m.showLogs {
			return m, nil
		}
		// Any key the roster list handles is someone driving it, from which
		// point the cursor follows its person rather than the top row.
		if m.screen == screenDashboard {
			m.teamCursorMoved = true
		}
		return m.updateActiveList(msg)
	}

	i := int(m.focus)
	var cmd tea.Cmd
	m.inputs[i], cmd = m.inputs[i].Update(msg)
	if m.focus == focusUser {
		m.refreshSuggestions()
	}
	return m, cmd
}

// handleDashboardKey consumes the keys that only mean something on the
// dashboard, reporting whether it took the keystroke. They are guarded by the
// list having focus: with the cursor in a date field, a letter is a date being
// typed, not a command.
func (m model) handleDashboardKey(msg tea.KeyMsg) (bool, model, tea.Cmd) {
	if m.focus != focusList || m.showLogs {
		return false, m, nil
	}

	switch {
	case key.Matches(msg, keys.AddPerson):
		return true, m.openAdd(), textinput.Blink
	case key.Matches(msg, keys.RemovePerson):
		next, cmd := m.removeSelected()
		return true, next, cmd
	case key.Matches(msg, keys.Open):
		next, cmd := m.openDetails()
		return true, next, cmd
	case key.Matches(msg, keys.Fetch):
		// On the dashboard the fetch key means "everyone, now", which is the
		// only way past a cache that is still within its TTL.
		next, cmd := m.forceRefresh()
		return true, next, cmd
	}
	return false, m, nil
}

// openAdd puts the cursor in the user field, where the same autocomplete that
// serves the details screen picks the person to add.
func (m model) openAdd() model {
	m.adding = true
	m.focus = focusUser
	m.inputs[focusUser].SetValue("")
	m.inputs[focusUser].Focus()
	m.suggestions = nil
	return m
}

func (m model) closeAdd() model {
	m.adding = false
	m.suggestions = nil
	m.inputs[focusUser].SetValue("")
	m.inputs[focusUser].Blur()
	m.focus = focusList
	return m
}

// acceptAdd puts the typed or highlighted person on the team. The highlighted
// suggestion wins when the dropdown is open, so enter never adds a half-typed
// login that happens to be a prefix of a real one.
func (m model) acceptAdd() (model, tea.Cmd) {
	person := contrib.Person{Login: strings.TrimSpace(m.inputs[focusUser].Value()), Active: true}
	if m.showingSuggestions() && m.suggestIndex < len(m.suggestions) {
		person = m.suggestions[m.suggestIndex]
	}
	if person.Login == "" {
		return m.closeAdd(), nil
	}
	if m.team.Has(person.Login) {
		m.setStatus(person.Login+" is already on your team", statusWarn)
		return m.closeAdd(), nil
	}

	m.team = m.team.Add(person)
	m = m.closeAdd()
	m.logs.Logf("[green]added %s to your team[-]", person.Login)

	next, cmd := m.syncTeam(false)
	next.selectLogin(person.Login)
	return next, tea.Batch(m.saveTeamCmd(next.team), cmd)
}

// removeSelected takes the row under the cursor off the team.
func (m model) removeSelected() (model, tea.Cmd) {
	row, ok := m.selectedRow()
	if !ok {
		return m, nil
	}

	index := m.teamList.Index()
	m.team = m.team.Remove(row.person.Login)
	m.logs.Logf("[yellow]removed %s from your team[-]", row.person.Login)

	next, cmd := m.syncTeam(false)
	if index >= len(next.rows()) {
		index = len(next.rows()) - 1
	}
	if index >= 0 {
		next.teamList.Select(index)
	}
	return next.settleTeamStatus(), tea.Batch(m.saveTeamCmd(next.team), cmd)
}

// forceRefresh refetches every member, cache or no cache.
func (m model) forceRefresh() (model, tea.Cmd) {
	if len(m.team.Members) == 0 {
		m.setStatus("no one on your team yet — press a to add someone", statusInfo)
		return m, nil
	}
	m.logs.Logf("[yellow]refreshing the whole team[-]")
	return m.syncTeam(true)
}

// openDetails opens the selected member's history. A report already on the row
// for this window is shown as it stands rather than fetched again; anything
// else -- a failed row, a stale one, a window that has since changed -- goes
// and gets it.
func (m model) openDetails() (model, tea.Cmd) {
	row, ok := m.selectedRow()
	if !ok {
		return m, nil
	}

	m.screen = screenDetails
	m.advertiseWindow()
	m.inputs[focusUser].SetValue(row.person.Login)
	m.inputs[focusUser].CursorEnd()
	m.suggestions = nil
	m.focus = focusList

	// Whatever was on screen belonged to whoever was open before. Left there it
	// reads as this person's history until the new one lands.
	if m.report != nil && !strings.EqualFold(m.report.Login, row.person.Login) {
		m = m.clearReport()
	}

	// The window is resolved after the screen has changed, so an untouched pair
	// of dates means the details default rather than the dashboard's.
	window, err := m.window()
	if err != nil {
		m.setStatus(err.Error(), statusError)
		return m, nil
	}
	if row.report != nil && row.window == window && !row.stale {
		return m.showReport(row.report), nil
	}
	return m.startFetch(false)
}

// showDashboard returns from a details view.
func (m model) showDashboard() model {
	m.screen = screenDashboard
	m.advertiseWindow()
	m.focus = focusList
	m.adding = false
	m.suggestions = nil
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
	return m.settleTeamStatus()
}

// activeList is whichever list the screen on show owns.
func (m model) activeList() list.Model {
	if m.screen == screenDashboard {
		return m.teamList
	}
	return m.list
}

func (m model) updateActiveList(msg tea.Msg) (model, tea.Cmd) {
	var cmd tea.Cmd
	if m.screen == screenDashboard {
		m.teamList, cmd = m.teamList.Update(msg)
		return m, cmd
	}
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// handleSuggestionKey consumes the keys the autocomplete list owns while it is
// open, reporting whether it took the keystroke.
func (m model) handleSuggestionKey(msg tea.KeyMsg) (bool, model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Down):
		m.suggestIndex = (m.suggestIndex + 1) % len(m.suggestions)
		return true, m, nil
	case key.Matches(msg, keys.Up):
		m.suggestIndex = (m.suggestIndex - 1 + len(m.suggestions)) % len(m.suggestions)
		return true, m, nil
	case key.Matches(msg, keys.Accept):
		return true, m.acceptSuggestion(), nil
	case key.Matches(msg, keys.Dismiss):
		m.suggestions = nil
		return true, m, nil
	}
	return false, m, nil
}

func (m model) showingSuggestions() bool {
	return m.focus == focusUser && len(m.suggestions) > 0
}

// refreshSuggestions recomputes the dropdown from what has been typed.
func (m *model) refreshSuggestions() {
	m.suggestions = m.suggest.match(m.inputs[focusUser].Value())
	m.suggestIndex = 0
}

// acceptSuggestion puts the bare login in the field: the row also carries the
// display name, which is not what GitHub wants back.
func (m model) acceptSuggestion() model {
	if m.suggestIndex >= len(m.suggestions) {
		return m
	}
	login := m.suggestions[m.suggestIndex].Login
	m.inputs[focusUser].SetValue(login)
	m.inputs[focusUser].CursorEnd()
	m.suggestions = nil
	return m
}

// focusRing is the order tab walks. The dashboard has no user field to visit
// unless the add field is open over it, so tab skips it there rather than
// parking the cursor somewhere with nothing to type into.
func (m model) focusRing() []focusArea {
	if m.screen == screenDetails || m.adding {
		return []focusArea{focusUser, focusFrom, focusTo, focusList}
	}
	return []focusArea{focusFrom, focusTo, focusList}
}

func (m model) moveFocus(delta int) model {
	// Leaving the user field closes the dropdown with it.
	m.suggestions = nil

	ring := m.focusRing()
	at := 0
	for i, area := range ring {
		if area == m.focus {
			at = i
			break
		}
	}
	next := ring[(at+delta+len(ring))%len(ring)]

	m.focus = next
	for i := range m.inputs {
		if focusArea(i) == next {
			m.inputs[i].Focus()
			continue
		}
		m.inputs[i].Blur()
	}
	if m.focus == focusUser {
		m.refreshSuggestions()
	}
	return m
}

// startFetch validates the filters and kicks off the request.
//
// force is ctrl+r: an explicit "go and look again", which a cache inside its
// TTL must not answer. The dashboard's copy of that key already forces, and
// the two screens disagreeing about what it means is how work that has just
// landed gets reported as absent.
func (m model) startFetch(force bool) (model, tea.Cmd) {
	username := strings.TrimSpace(m.inputs[focusUser].Value())
	if username == "" {
		m.setStatus("user is required", statusError)
		return m, nil
	}
	window, err := m.window()
	if err != nil {
		m.setStatus(err.Error(), statusError)
		return m, nil
	}

	m.suggestions = nil

	// A report already on disk and inside its TTL is the answer: paint it and
	// spend nothing. A stale one is painted too, and refreshed behind it --
	// something to read beats an empty screen while the network works. A forced
	// fetch paints it for the same reason, but never settles for it.
	entry, cached := m.cachedReport(username, window)
	if cached {
		m = m.showReport(entry.Report)
		m.logs.Logf("[gray]served %s from cache fetched=%s[-]",
			username, entry.FetchedAt.Format(time.RFC3339))
		if entry.Fresh && !force {
			m.busy = false
			m.pending = pendingFetch{}
			return m, nil
		}
	}

	m.pending = pendingFetch{login: username, window: window}
	m.busy = true
	m.setStatus(fmt.Sprintf("loading %s...", username), statusInfo)
	m.logs.Logf("[yellow]fetch start user=%s from=%s to=%s[-]",
		username, window.From.Format(time.RFC3339), window.To.Format(time.RFC3339))

	return m, tea.Batch(m.fetchCmd(username, window), m.spinner.Tick)
}

func (m model) applyFetched(msg fetchedMsg) (model, tea.Cmd) {
	// Whatever came back is worth keeping even if the user has moved on: the
	// next visit should not pay for it again.
	var cmd tea.Cmd
	if msg.err == nil && msg.report != nil {
		cmd = m.storeReportCmd(msg.login, msg.window, msg.report)
	}

	// An answer to a question nobody is asking any more belongs in the cache
	// and nowhere else.
	if !m.pending.answeredBy(msg.login, msg.window) {
		m.logs.Logf("[gray]dropped a late report for %s[-]", msg.login)
		return m, cmd
	}
	m.pending = pendingFetch{}
	m.busy = false

	if msg.err != nil {
		m.setStatus(msg.err.Error(), statusError)
		m.logs.Logf("[red]fetch failed: %v[-]", msg.err)
		return m, cmd
	}

	m.logs.Logf("[green]fetch complete total=%d[-]", len(contrib.Events(msg.report)))
	m = m.showReport(msg.report)
	if len(contrib.Events(msg.report)) > 0 {
		m.focus = focusList
		for i := range m.inputs {
			m.inputs[i].Blur()
		}
	}
	return m, cmd
}

// clearReport empties the details screen, which is what it should show while it
// is loading somebody new.
func (m model) clearReport() model {
	m.report = nil
	m.list.SetItems(nil)
	m.list.ResetSelected()
	return m
}

// showReport puts a report on the details screen.
//
// The date fields are left exactly as they were found. They once echoed the
// window a fetch had used, which was how an empty field showed what it had
// resolved to -- the placeholders say that now. Writing the dates back turned a
// default into a typed choice that then followed the user to the other screen,
// where it meant something different.
func (m model) showReport(report *contrib.Report) model {
	m.report = report

	events := contrib.Events(report)
	m.list.SetItems(newEventItems(events))
	m.list.ResetSelected()
	m.setStatus(fmt.Sprintf("%d contributions", len(events)), statusOK)
	return m
}

func (m model) applyDiscovered(msg discoveredMsg) (model, tea.Cmd) {
	if msg.forced {
		m.busy = false
	}
	if msg.err != nil {
		m.logs.Logf("[yellow]user discovery unavailable: %v[-]", msg.err)
		if msg.forced {
			m.setStatus("user list refresh failed: "+msg.err.Error(), statusError)
		}
		return m, nil
	}

	directory := contrib.NewDirectory(msg.people)
	m.suggest.set(directory)
	m.logs.Logf("[green]loaded %d user suggestions from %s[-]", directory.Len(), msg.source)
	if directory.Len() <= 1 && msg.source != "cache" {
		m.logs.Logf("[yellow]only your own account was found; if you expect teammates, " +
			"your token may lack the read:org scope - run 'gh auth refresh -s read:org'[-]")
	}
	if msg.forced {
		m.setStatus(fmt.Sprintf("%d users loaded", directory.Len()), statusOK)
	}
	if m.focus == focusUser {
		m.refreshSuggestions()
	}

	var cmd tea.Cmd
	if msg.source == "github" && m.cache != nil {
		cmd = m.storeCacheCmd(msg.people)
	}
	return m, cmd
}

func (m model) openSelected() model {
	url := m.selectedURL()
	if url == "" {
		m.setStatus("nothing selected", statusWarn)
		return m
	}
	if err := platform.OpenURL(url); err != nil {
		m.setStatus("open failed: "+err.Error(), statusError)
		return m
	}
	m.setStatus("opened in browser", statusOK)
	return m
}

func (m model) copySelected() model {
	url := m.selectedURL()
	if url == "" {
		m.setStatus("nothing selected", statusWarn)
		return m
	}
	if err := platform.CopyToClipboard(url); err != nil {
		m.setStatus("copy unavailable: "+url, statusWarn)
		return m
	}
	m.setStatus("copied "+url, statusOK)
	return m
}

func (m model) selectedURL() string {
	item, ok := m.list.SelectedItem().(eventItem)
	if !ok {
		return ""
	}
	return item.URL
}

func (m *model) setStatus(text string, level statusLevel) {
	m.status, m.level = text, level
}

// Commands.

func (m model) fetchCmd(username string, window contrib.Range) tea.Cmd {
	svc := m.svc
	return func() tea.Msg {
		report, err := svc.FetchContributions(context.Background(), username, window)
		return fetchedMsg{login: username, window: window, report: report, err: err}
	}
}

func (m model) discoverCmd(forced bool) tea.Cmd {
	svc := m.svc
	if svc == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), discoveryTimeout)
		defer cancel()
		people, err := svc.DiscoverPeople(ctx)
		return discoveredMsg{people: people, err: err, forced: forced, source: "github"}
	}
}

func (m model) loadCacheCmd() tea.Cmd {
	cache := m.cache
	if cache == nil {
		return nil
	}
	return func() tea.Msg {
		people, ok := cache.Load()
		if !ok {
			return nil
		}
		return discoveredMsg{people: people, source: "cache"}
	}
}

// loadTeamCmd reads the roster from the app's config.
func (m model) loadTeamCmd() tea.Cmd {
	store := m.teams
	if store == nil {
		return nil
	}
	return func() tea.Msg {
		team, err := store.Load()
		return teamLoadedMsg{team: team, err: err}
	}
}

// saveTeamCmd persists the roster. A roster that cannot be saved is still used
// for this session: losing the change on the next launch is better than
// refusing the change now.
func (m model) saveTeamCmd(team teamconfig.Team) tea.Cmd {
	store := m.teams
	logs := m.logs
	if store == nil {
		return nil
	}
	return func() tea.Msg {
		if err := store.Save(team); err != nil {
			logs.Logf("[yellow]could not save your team: %v[-]", err)
		}
		return nil
	}
}

// viewerCmd asks who we are authenticated as, for a first-launch team.
func (m model) viewerCmd() tea.Cmd {
	svc := m.svc
	if svc == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), discoveryTimeout)
		defer cancel()
		person, err := svc.Viewer(ctx)
		return viewerMsg{person: person, err: err}
	}
}

// fetchTeamCmd fetches one member's report. One command per member means the
// runtime runs them concurrently and each row updates as its own result lands.
func (m model) fetchTeamCmd(login string, window contrib.Range) tea.Cmd {
	svc := m.svc
	if svc == nil {
		return nil
	}
	return func() tea.Msg {
		report, err := svc.FetchContributions(context.Background(), login, window)
		return teamFetchedMsg{login: login, window: window, report: report, err: err}
	}
}

// storeReportCmd caches a fetched report for the next launch.
func (m model) storeReportCmd(login string, window contrib.Range, report *contrib.Report) tea.Cmd {
	cache := m.reports
	logs := m.logs
	if cache == nil {
		return nil
	}
	return func() tea.Msg {
		if err := cache.Store(login, window, report); err != nil {
			logs.Logf("[yellow]could not cache %s's report: %v[-]", login, err)
		}
		return nil
	}
}

func (m model) storeCacheCmd(people []contrib.Person) tea.Cmd {
	cache := m.cache
	logs := m.logs
	return func() tea.Msg {
		if err := cache.Store(people); err != nil {
			logs.Logf("[yellow]could not cache user list: %v[-]", err)
		}
		return nil
	}
}
