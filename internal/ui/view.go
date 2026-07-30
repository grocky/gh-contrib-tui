package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"

	"gh-contribs/internal/contrib"
)

// Vertical costs the list has to give way to. Only the panels whose height is
// fixed by construction are constants; the rest are measured in resize.
const (
	panelChrome    = 3  // border, panel title, border
	calendarHeight = 12 // chrome, month row, seven weekdays, legend
	minListRows    = 3
)

func (m model) View() string {
	if !m.ready {
		return "\n  starting..."
	}

	sections := []string{
		m.viewHeader(),
		m.viewFilters(),
	}
	if rows := m.viewSuggestions(); rows != "" {
		sections = append(sections, rows)
	}
	if m.showSummary {
		sections = append(sections, m.viewSummary())
	}
	if m.showCalendar {
		sections = append(sections, m.viewCalendar())
	}
	if m.showLogs {
		sections = append(sections, m.viewLogs())
	} else {
		sections = append(sections, m.viewList())
	}
	sections = append(sections, m.viewFooter())

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m model) viewHeader() string {
	title := titleStyle.Render("gh-contribs")
	host := hostStyle.Render(m.host)
	gap := m.width - lipgloss.Width(title) - lipgloss.Width(host)
	if gap < 1 {
		gap = 1
	}
	return title + strings.Repeat(" ", gap) + host + "\n"
}

// viewFilters draws the three inputs on one row, with the focused label
// highlighted so it is obvious where typing lands.
func (m model) viewFilters() string {
	fields := []struct {
		label string
		area  focusArea
	}{
		{"User", focusUser},
		{"From", focusFrom},
		{"To", focusTo},
	}

	cells := make([]string, 0, len(fields))
	for _, f := range fields {
		style := labelStyle
		if m.focus == f.area {
			style = focusedLabelStyle
		}
		cells = append(cells, style.Render(f.label)+"  "+m.inputs[f.area].View())
	}

	row := strings.Join(cells, mutedStyle.Render("   │   "))
	if lipgloss.Width(row) > m.contentWidth()-2 {
		// Too narrow for one row: stack the fields rather than let them wrap
		// mid-input.
		row = strings.Join(cells, "\n")
	}
	return panelStyle.Width(m.contentWidth()).Render(row)
}

// viewSuggestions draws the autocomplete list under the filters. It only
// appears once the user has typed something.
func (m model) viewSuggestions() string {
	if !m.showingSuggestions() {
		return ""
	}

	start := 0
	if m.suggestIndex >= visibleSuggestions {
		start = m.suggestIndex - visibleSuggestions + 1
	}
	end := min(start+visibleSuggestions, len(m.suggestions))

	// Two columns, sized to whatever room the terminal leaves.
	inner := m.contentWidth() - 4
	loginWidth := min(22, max(inner/2, 8))
	nameWidth := inner - loginWidth - 2

	lines := make([]string, 0, visibleSuggestions+1)
	for i := start; i < end; i++ {
		person := m.suggestions[i]
		login, name := person.Login, person.Name
		if !person.Active {
			name = strings.TrimSpace(name + " (" + inactiveMarker + ")")
		}
		login = pad(truncate(login, loginWidth), loginWidth)
		name = faintStyle.Render(truncate(name, nameWidth))
		if i == m.suggestIndex {
			lines = append(lines, suggestionSelectedStyle.Render("▸ "+login)+name)
			continue
		}
		lines = append(lines, "  "+login+name)
	}
	if hidden := len(m.suggestions) - end; hidden > 0 {
		lines = append(lines, faintStyle.Render(fmt.Sprintf("  +%d more", hidden)))
	}

	return suggestionStyle.Render(strings.Join(lines, "\n"))
}

// viewSummary is the headline card: who, over what window, and the totals.
func (m model) viewSummary() string {
	cc := m.report.Collection
	who := m.report.DisplayName()
	if m.report.Name != "" {
		who += " " + faintStyle.Render("@"+m.report.Login)
	}
	head := fmt.Sprintf("%s  %s",
		headlineStyle.Render(who),
		faintStyle.Render(fmt.Sprintf("%s → %s",
			cc.StartedAt.Format(contrib.DateFormat),
			cc.EndedAt.Format(contrib.DateFormat))),
	)

	stats := []string{
		totalStyle.Render(fmt.Sprintf("%d", cc.Calendar.Total)) + mutedStyle.Render(" contributions"),
		stat("commits", cc.TotalCommits, "commit"),
		stat("issues", cc.TotalIssues, "issue"),
		fmt.Sprintf("%s %s", kindStyles["pr"].Render(fmt.Sprintf("%d", cc.TotalPullRequests)),
			mutedStyle.Render(fmt.Sprintf("prs (%d merged)", cc.TotalMergedPullRequests))),
		stat("reviews", cc.TotalReviews, "review"),
	}

	body := []string{head, strings.Join(stats, mutedStyle.Render(" · "))}
	for _, warning := range m.report.Warnings {
		body = append(body, warningStyle.Render("! "+warning))
	}
	return panelStyle.Width(m.contentWidth()).Render(strings.Join(body, "\n"))
}

func stat(label string, count int, kind string) string {
	return fmt.Sprintf("%s %s",
		kindStyles[kind].Render(fmt.Sprintf("%d", count)),
		mutedStyle.Render(label))
}

func (m model) viewCalendar() string {
	inner := m.contentWidth() - 4
	body := strings.Join([]string{
		panelTitleStyle.Render("Contribution calendar"),
		renderHeatmap(m.report.Collection.Calendar, inner),
		renderLegend(),
	}, "\n")
	return panelStyle.Width(m.contentWidth()).Render(body)
}

func (m model) viewList() string {
	title := panelTitleStyle.Render("Contributions")
	body := m.list.View()

	// The count follows what is on screen: with a filter applied, Items() is
	// still the whole report but only VisibleItems() are listed.
	visible := len(m.list.VisibleItems())

	switch {
	case m.focus == focusList && m.list.FilterState() != list.Unfiltered:
		// The query is clamped rather than truncated after styling: a title
		// wider than its panel makes lipgloss drop the border, and cutting an
		// already-styled string would cut an ANSI escape in half.
		query := truncate(m.list.FilterValue(), max(m.contentWidth()/3, 8))
		title += labelStyle.Render("  filter: ") + query
		title = appendIfItFits(m, title, fmt.Sprintf("  %d of %d", visible, len(m.list.Items())))
	case visible > 0:
		title = appendIfItFits(m, title, fmt.Sprintf("  %d of %d", m.list.Index()+1, visible))
	default:
		// The list's own empty text is "No items.", which says nothing about
		// what to do next. Pad to the list's height so the panel still fills
		// the space it was given.
		hint := truncate("nothing loaded yet — type a user and press ctrl+r", m.contentWidth()-6)
		body = faintStyle.Render("  "+hint) + strings.Repeat("\n", max(m.list.Height()-1, 0))
	}
	return panelStyle.Width(m.contentWidth()).Render(title + "\n" + body)
}

// appendIfItFits adds a faint suffix to a panel title only when the panel has
// room for it.
func appendIfItFits(m model, title, suffix string) string {
	if lipgloss.Width(title)+lipgloss.Width(suffix) > m.contentWidth()-4 {
		return title
	}
	return title + faintStyle.Render(suffix)
}

func (m model) viewLogs() string {
	title := panelTitleStyle.Render("Logs") + faintStyle.Render("  ctrl+l to close")
	return panelStyle.Width(m.contentWidth()).Render(title + "\n" + m.logView.View())
}

func (m model) viewFooter() string {
	status := m.status
	if m.busy {
		status = m.spinner.View() + status
	}

	bindings := keys.ShortHelp()
	switch {
	case m.showingSuggestions():
		bindings = suggestionHelp()
	case m.showLogs:
		bindings = logHelp()
	}
	return m.level.style().Render(truncate(status, m.width)) + "\n" + m.help.ShortHelpView(bindings)
}

// contentWidth is the usable width for a panel, leaving a one-column margin.
func (m model) contentWidth() int {
	if m.width < 20 {
		return 20
	}
	return m.width - 2
}

// resize decides what fits and sizes the panes that flex. A terminal too short
// for everything drops the calendar first and then the summary, rather than
// pushing the header off the top of the screen: the list is the part people
// scroll, so it keeps its rows.
func (m *model) resize() {
	m.help.Width = m.width

	// The panels are measured rather than predicted: a long stat row, a wrapped
	// warning, or a narrow terminal that stacks the filter fields all change
	// their height, and a budget that guesses wrong scrolls the screen.
	free := m.height - lipgloss.Height(m.viewHeader()) -
		lipgloss.Height(m.viewFilters()) - lipgloss.Height(m.viewFooter())
	if rows := m.viewSuggestions(); rows != "" {
		free -= lipgloss.Height(rows)
	}
	m.showSummary, m.showCalendar = false, false

	if m.report != nil {
		if summary := lipgloss.Height(m.viewSummary()); free-summary >= panelChrome+minListRows {
			m.showSummary = true
			free -= summary
		}
		if m.showSummary && free-calendarHeight >= panelChrome+minListRows {
			m.showCalendar = true
			free -= calendarHeight
		}
	}

	rows := free - panelChrome
	if rows < minListRows {
		rows = minListRows
	}

	inner := m.contentWidth() - 4
	m.list.SetSize(inner, rows)

	// Collaborators log from their own goroutines, so the pane is refreshed
	// from the buffer on every message rather than pushed to. It follows the
	// tail unless the user has scrolled up to read something, which is the one
	// moment an arriving line must not yank the view out from under them.
	follow := m.logView.AtBottom()
	m.logView.Width = inner
	m.logView.Height = rows
	m.logView.SetContent(m.logs.Render(inner))
	if follow {
		m.logView.GotoBottom()
	}
}
