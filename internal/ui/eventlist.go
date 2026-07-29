package ui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"gh-contribs/internal/contrib"
)

const (
	badgeWidth = 6
	dateWidth  = 10
	repoWidth  = 26
)

// eventItem adapts a contribution to the list component.
type eventItem struct {
	contrib.Event
}

// FilterValue is what "/" searches over: the title, the repository, and the
// kind, so "review acme" narrows to reviews in one repository.
func (i eventItem) FilterValue() string {
	return strings.Join([]string{i.Title, i.Repo, eventLabel(i.Event)}, " ")
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
		repo = truncate(event.Repo, min(repoWidth, room/3))
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
	l.SetFilteringEnabled(true)
	l.SetShowPagination(false)
	l.Styles.NoItems = faintStyle.PaddingLeft(2)
	l.FilterInput.Prompt = "filter: "
	l.FilterInput.PromptStyle = labelStyle
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
