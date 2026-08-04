package ui

import "github.com/charmbracelet/bubbles/key"

// keyMap is the application's bindings. Bubbles' help component renders
// straight from it, so the help line can never drift from the behaviour.
type keyMap struct {
	Next         key.Binding
	Prev         key.Binding
	Fetch        key.Binding
	Open         key.Binding
	Back         key.Binding
	AddPerson    key.Binding
	RemovePerson key.Binding
	Copy         key.Binding
	Refresh      key.Binding
	Logs         key.Binding
	Up           key.Binding
	Down         key.Binding
	Scroll       key.Binding
	Accept       key.Binding
	Dismiss      key.Binding
	Quit         key.Binding
}

var keys = keyMap{
	Next: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "next field"),
	),
	Prev: key.NewBinding(
		key.WithKeys("shift+tab"),
		key.WithHelp("shift+tab", "previous"),
	),
	Fetch: key.NewBinding(
		key.WithKeys("ctrl+r"),
		key.WithHelp("ctrl+r", "fetch"),
	),
	Open: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "open"),
	),
	// Back leaves a details view. It shares escape with Dismiss, which is why
	// everything that can be dismissed -- the dropdown, the add field, the log
	// pane -- is offered the keystroke first.
	Back: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back"),
	),
	// AddPerson and RemovePerson are plain letters, which is safe only because
	// they are read on the dashboard with the cursor on the list; a date field
	// with focus takes its own keystrokes first.
	AddPerson: key.NewBinding(
		key.WithKeys("a"),
		key.WithHelp("a", "add person"),
	),
	RemovePerson: key.NewBinding(
		key.WithKeys("x"),
		key.WithHelp("x", "remove"),
	),
	Copy: key.NewBinding(
		key.WithKeys("ctrl+y"),
		key.WithHelp("ctrl+y", "copy url"),
	),
	Refresh: key.NewBinding(
		key.WithKeys("ctrl+u"),
		key.WithHelp("ctrl+u", "reload users"),
	),
	Logs: key.NewBinding(
		key.WithKeys("alt+L"),
		key.WithHelp("alt+L", "logs"),
	),
	Up: key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "down"),
	),
	// Scroll moves the log pane. It is deliberately free of letter keys: the
	// filter fields stay live behind the pane, so anything that could be typed
	// into one has to reach it.
	Scroll: key.NewBinding(
		key.WithKeys("up", "down", "pgup", "pgdown", "home", "end"),
		key.WithHelp("↑/↓", "scroll"),
	),
	// Accept deliberately does not include tab: tab is "next field" everywhere
	// else in this UI, and having it mean two things depending on whether the
	// dropdown happens to be open is worse than one extra keystroke.
	Accept: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "accept"),
	),
	Dismiss: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "dismiss"),
	),
	Quit: key.NewBinding(
		key.WithKeys("ctrl+c", "ctrl+q"),
		key.WithHelp("ctrl+c", "quit"),
	),
}

// ShortHelp is the one-line help under the details layout. It shows the keys
// that are always live, in the order they are most often reached for.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Next, k.Back, k.Fetch, k.Open, k.Copy, k.Logs, k.Quit}
}

// dashboardHelp is the help line for the team screen, where enter drills into a
// person and the roster keys are live.
func dashboardHelp() []key.Binding {
	return []key.Binding{
		keys.Open, keys.AddPerson, keys.RemovePerson, keys.Fetch, keys.Next, keys.Logs, keys.Quit,
	}
}

// addPersonHelp replaces the help line while the add field is open.
func addPersonHelp() []key.Binding {
	return []key.Binding{keys.Up, keys.Down, keys.Accept, keys.Dismiss}
}

// FullHelp is required by the help component; this UI only uses short help.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{k.ShortHelp()}
}

// suggestionHelp replaces the help line while the autocomplete list is open,
// where enter and the arrow keys mean something different.
func suggestionHelp() []key.Binding {
	return []key.Binding{keys.Up, keys.Down, keys.Accept, keys.Dismiss}
}

// logHelp replaces the help line while the log pane is open, where the arrow
// keys scroll it and escape closes it.
func logHelp() []key.Binding {
	return []key.Binding{keys.Scroll, keys.Dismiss, keys.Fetch, keys.Quit}
}
