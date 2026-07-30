package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"gh-contribs/internal/contrib"
	"gh-contribs/internal/platform"
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

// Messages produced by background work.
type (
	fetchedMsg struct {
		report *contrib.Report
		err    error
	}
	discoveredMsg struct {
		people []contrib.Person
		err    error
		forced bool
		source string
	}
)

// model is the bubbletea application state.
type model struct {
	svc     ContributionService
	cache   DirectoryCache
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
		logs:    newLogBuffer(),
		suggest: &suggestionStore{},
		list:    newEventList(),
		help:    help.New(),
		spinner: spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(mutedStyle)),
		logView: viewport.New(0, 0),
		status:  "enter a user and press ctrl+r",
	}
	m.help.Styles.ShortKey = mutedStyle
	m.help.Styles.ShortDesc = faintStyle
	m.help.Styles.ShortSeparator = faintStyle

	placeholders := [inputCount]string{"login or name", "YYYY-MM-DD", "YYYY-MM-DD"}
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
	m.inputs[focusUser].Focus()
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.loadCacheCmd(), m.discoverCmd(false))
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
		return m.applyFetched(msg), nil

	case discoveredMsg:
		return m.applyDiscovered(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Anything else belongs to the list: it filters asynchronously, so its own
	// FilterMatchesMsg has to get back to it or "/" silently does nothing.
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// handleKey routes a keystroke. Order matters: the open suggestion list and an
// active filter both claim keys that mean something else elsewhere.
func (m model) handleKey(msg tea.KeyMsg) (model, tea.Cmd) {
	if key.Matches(msg, keys.Quit) {
		return m, tea.Quit
	}

	// While the list is being filtered, typing belongs to the filter.
	if m.focus == focusList && m.list.SettingFilter() {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}

	if m.showingSuggestions() {
		if handled, next, cmd := m.handleSuggestionKey(msg); handled {
			return next, cmd
		}
	}

	switch {
	case key.Matches(msg, keys.Next):
		return m.moveFocus(1), nil
	case key.Matches(msg, keys.Prev):
		return m.moveFocus(-1), nil
	case key.Matches(msg, keys.Fetch):
		return m.startFetch()
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
		return m.startFetch()
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
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}

	i := int(m.focus)
	var cmd tea.Cmd
	m.inputs[i], cmd = m.inputs[i].Update(msg)
	if m.focus == focusUser {
		m.refreshSuggestions()
	}
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

func (m model) moveFocus(delta int) model {
	// Leaving the user field closes the dropdown with it.
	m.suggestions = nil

	next := (int(m.focus) + delta + 4) % 4
	m.focus = focusArea(next)
	for i := range m.inputs {
		if i == next {
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
func (m model) startFetch() (model, tea.Cmd) {
	username := strings.TrimSpace(m.inputs[focusUser].Value())
	if username == "" {
		m.setStatus("user is required", statusError)
		return m, nil
	}
	window, err := contrib.ParseRange(m.inputs[focusFrom].Value(), m.inputs[focusTo].Value())
	if err != nil {
		m.setStatus(err.Error(), statusError)
		return m, nil
	}

	m.suggestions = nil
	m.busy = true
	m.setStatus(fmt.Sprintf("loading %s...", username), statusInfo)
	m.logs.Logf("[yellow]fetch start user=%s from=%s to=%s[-]",
		username, window.From.Format(time.RFC3339), window.To.Format(time.RFC3339))

	return m, tea.Batch(m.fetchCmd(username, window), m.spinner.Tick)
}

func (m model) applyFetched(msg fetchedMsg) model {
	m.busy = false
	if msg.err != nil {
		m.setStatus(msg.err.Error(), statusError)
		m.logs.Logf("[red]fetch failed: %v[-]", msg.err)
		return m
	}

	m.report = msg.report
	// Echo the window that was actually used: an empty From means "trailing 30
	// days", and leaving the fields blank hides which days those were.
	m.inputs[focusFrom].SetValue(msg.report.Collection.StartedAt.Format(contrib.DateFormat))
	m.inputs[focusTo].SetValue(msg.report.Collection.EndedAt.Format(contrib.DateFormat))

	events := contrib.Events(msg.report)
	m.list.SetItems(newEventItems(events))
	m.list.ResetSelected()
	m.setStatus(fmt.Sprintf("%d contributions", len(events)), statusOK)
	m.logs.Logf("[green]fetch complete total=%d[-]", len(events))
	if len(events) > 0 {
		m.focus = focusList
		for i := range m.inputs {
			m.inputs[i].Blur()
		}
	}
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
		return fetchedMsg{report: report, err: err}
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
