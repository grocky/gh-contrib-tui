package ui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/grocky/gh-contrib-tui/internal/contrib"
)

const (
	badgeWidth = 6
	dateWidth  = 10
	repoWidth  = 26

	// branchWidth is the extra room a commit's branch is allowed on top of the
	// repository, enough for the ticket-shaped names branches usually carry.
	branchWidth = 18
)

// eventItem adapts a contribution to the list component.
type eventItem struct {
	contrib.Event
}

// FilterValue is what "/" searches over: the title, the repository and branch,
// and the kind, so "review acme" narrows to reviews in one repository and a
// ticket-shaped branch name finds the commits pushed for that ticket.
func (i eventItem) FilterValue() string {
	return strings.Join([]string{i.Title, eventLocation(i.Event), eventLabel(i.Event)}, " ")
}

// eventLocation is where a contribution went: its repository, and for a commit
// that never reached a default branch, the branch it is sitting on.
func eventLocation(event contrib.Event) string {
	if event.Branch == "" {
		return event.Repo
	}
	return event.Repo + "@" + event.Branch
}

func newEventItems(events []contrib.Event) []list.Item {
	items := make([]list.Item, 0, len(events))
	for _, e := range events {
		items = append(items, eventItem{Event: e})
	}
	return items
}

// eventLabel is the badge text for a contribution.
func eventLabel(event contrib.Event) string {
	switch event.Kind {
	case contrib.KindAuthored:
		if event.Merged {
			return "merged"
		}
		return "pr"
	case contrib.KindReviewed:
		return "review"
	default:
		return event.Kind
	}
}

// eventDelegate renders one contribution per line: date, kind badge, title,
// and a dimmed repository pinned to the right.
type eventDelegate struct{}

func (eventDelegate) Height() int                         { return 1 }
func (eventDelegate) Spacing() int                        { return 0 }
func (eventDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d eventDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	event, ok := item.(eventItem)
	if !ok {
		return
	}
	selected := index == m.Index()

	cursor := "  "
	if selected {
		cursor = cursorStyle.Render("▸ ")
	}

	label := eventLabel(event.Event)
	badge := kindStyles[label].Render(pad(label, badgeWidth))
	date := faintStyle.Render(event.OccurredAt.Format(contrib.DateFormat))

	// The repository is the first thing to lose room, then the title.
	width := m.Width()
	fixed := lipgloss.Width(cursor) + dateWidth + badgeWidth + 3
	repo := ""
	room := width - fixed
	if room > 30 {
		// A branch pushes the location past the usual repository budget, so it
		// is given room to survive truncation -- "acme/repo@" with the branch
		// cut off would say less than the repository alone.
		budget := repoWidth
		if event.Branch != "" {
			budget = repoWidth + branchWidth
		}
		repo = truncate(eventLocation(event.Event), min(budget, room/2))
		room -= lipgloss.Width(repo) + 1
	}

	title := truncate(event.Title, room)
	titleStyle := lipgloss.NewStyle().Foreground(colorText)
	if selected {
		titleStyle = selectedTitleStyle
	}

	line := fmt.Sprintf("%s%s %s %s", cursor, date, badge, titleStyle.Render(title))
	if repo != "" {
		gap := width - lipgloss.Width(line) - lipgloss.Width(repo)
		if gap < 1 {
			gap = 1
		}
		line += strings.Repeat(" ", gap) + faintStyle.Render(repo)
	}
	fmt.Fprint(w, line)
}

// newEventList builds the contribution list. Its own chrome is switched off:
// the model draws the title and the help line itself so every panel matches.
func newEventList() list.Model {
	l := list.New(nil, eventDelegate{}, 0, 0)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)
	l.SetFilteringEnabled(true)
	// The panel draws the filter query in its own title, clamped to the width
	// it has. Left to the list, the query renders at full length and wraps the
	// panel -- and appears twice.
	l.SetShowFilter(false)
	l.Styles.NoItems = faintStyle.PaddingLeft(2)
	return l
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	runes := []rune(s)
	return string(runes[:min(width-1, len(runes))]) + "…"
}
