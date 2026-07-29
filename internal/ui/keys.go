package ui

import "github.com/charmbracelet/bubbles/key"

// keyMap is the application's bindings. Bubbles' help component renders
// straight from it, so the help line can never drift from the behaviour.
type keyMap struct {
	Next    key.Binding
	Prev    key.Binding
	Fetch   key.Binding
	Open    key.Binding
	Copy    key.Binding
	Refresh key.Binding
	Logs    key.Binding
	Up      key.Binding
	Down    key.Binding
	Accept  key.Binding
	Dismiss key.Binding
	Quit    key.Binding
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
	Copy: key.NewBinding(
		key.WithKeys("ctrl+y"),
		key.WithHelp("ctrl+y", "copy url"),
	),
	Refresh: key.NewBinding(
		key.WithKeys("ctrl+u"),
		key.WithHelp("ctrl+u", "reload users"),
	),
	Logs: key.NewBinding(
		key.WithKeys("ctrl+l"),
		key.WithHelp("ctrl+l", "logs"),
	),
	Up: key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "down"),
	),
	Accept: key.NewBinding(
		key.WithKeys("enter", "tab"),
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

// ShortHelp is the one-line help under the layout. It shows the keys that are
// always live, in the order they are most often reached for.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Next, k.Fetch, k.Open, k.Copy, k.Refresh, k.Logs, k.Quit}
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
