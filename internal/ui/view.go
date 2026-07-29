package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"gh-contribs/internal/contrib"
)

// Fixed vertical costs the list has to give way to.
const (
	headerHeight = 2 // title row + blank
	filterHeight = 4 // panel border, label row, input row, blank
	footerHeight = 2 // status + help
	minListRows  = 3
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
	if m.report != nil {
		sections = append(sections, m.viewSummary(), m.viewCalendar())
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

	lines := make([]string, 0, visibleSuggestions+1)
	for i := start; i < end; i++ {
		person := m.suggestions[i]
		login, name := person.Login, person.Name
		if !person.Active {
			name = strings.TrimSpace(name + " (" + inactiveMarker + ")")
		}
		line := pad(login, 22) + faintStyle.Render(truncate(name, 40))
		if i == m.suggestIndex {
			line = suggestionSelectedStyle.Render("▸ "+pad(login, 22)) + faintStyle.Render(truncate(name, 40))
		} else {
			line = "  " + line
		}
		lines = append(lines, line)
	}
	if hidden := len(m.suggestions) - end; hidden > 0 {
		lines = append(lines, faintStyle.Render(fmt.Sprintf("  +%d more", hidden)))
	}

	return suggestionStyle.Render(strings.Join(lines, "\n"))
}

// viewSummary is the headline card: who, over what window, and the totals.
func (m model) viewSummary() string {
	cc := m.report.Collection
	head := fmt.Sprintf("%s  %s",
		headlineStyle.Render(m.report.DisplayName()),
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

	body := []string{head, strings.Join(stats, mutedStyle.Render("   ·   "))}
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

	switch {
	case m.focus == focusList && m.list.FilterState() != 0:
		title += "  " + m.list.FilterInput.View()
	case len(m.list.Items()) > 0:
		title += faintStyle.Render(fmt.Sprintf("  %d of %d", m.list.Index()+1, len(m.list.Items())))
	default:
		// The list's own empty text is "No items.", which says nothing about
		// what to do next.
		body = faintStyle.Render("  nothing loaded yet — type a user and press ctrl+r")
	}
	return panelStyle.Width(m.contentWidth()).Render(title + "\n" + body)
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
	if m.showingSuggestions() {
		bindings = suggestionHelp()
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

// resize recomputes the heights that flex: the list, and the log viewport that
// replaces it.
func (m *model) resize() {
	m.help.Width = m.width

	used := headerHeight + filterHeight + footerHeight
	if m.report != nil {
		used += m.summaryHeight() + m.calendarHeight()
	}
	rows := m.height - used - 3 // panel border and title
	if rows < minListRows {
		rows = minListRows
	}

	inner := m.contentWidth() - 4
	m.list.SetSize(inner, rows)
	m.logView.Width = inner
	m.logView.Height = rows
	m.logView.SetContent(m.logs.Render(inner))
}

func (m model) summaryHeight() int {
	return 4 + len(m.report.Warnings) // border, headline, stats, warnings
}

func (m model) calendarHeight() int {
	return 12 // border, title, month row, 7 weekdays, legend
}
