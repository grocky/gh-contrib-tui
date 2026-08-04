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
	panelChrome  = 3  // border, panel title, border
	calendarRows = 10 // month row, seven weekdays, blank line, legend
	minListRows  = 3

	// minChartsSideBySide is the width below which the charts row holds the
	// calendar alone. Two panels each need their own border, padding, and
	// enough columns to say something.
	minChartsSideBySide = 84
)

// calendarHeight is what the calendar panel costs. Spacing the rows out puts a
// blank line under each weekday row but the last, which buys the grid room to
// breathe at the price of six list rows.
func calendarHeight(spaced bool) int {
	if spaced {
		return panelChrome + calendarRows + 6
	}
	return panelChrome + calendarRows
}

func (m model) View() string {
	if !m.ready {
		return "\n  starting..."
	}
	if m.screen == screenDashboard {
		return m.viewDashboard()
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
		sections = append(sections, m.viewCharts())
	}
	if m.showLogs {
		sections = append(sections, m.viewLogs())
	} else {
		sections = append(sections, m.viewList())
	}
	sections = append(sections, m.viewFooter())

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// viewDashboard is the landing screen: the window being summarized, then a row
// per team member.
func (m model) viewDashboard() string {
	sections := []string{
		m.viewHeader(),
		m.viewWindow(),
	}
	if rows := m.viewSuggestions(); rows != "" {
		sections = append(sections, rows)
	}
	if m.showLogs {
		sections = append(sections, m.viewLogs())
	} else {
		sections = append(sections, m.viewTeam())
	}
	sections = append(sections, m.viewFooter())

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// viewWindow draws the dashboard's date fields, plus the add-person field while
// it is open.
func (m model) viewWindow() string {
	cells := []string{
		m.fieldCell("From", focusFrom),
		m.fieldCell("To", focusTo),
	}
	if m.adding {
		cells = append(cells, m.fieldCell("Add", focusUser))
	}

	row := strings.Join(cells, mutedStyle.Render("   │   "))
	if lipgloss.Width(row) > m.contentWidth()-2 {
		row = strings.Join(cells, "\n")
	}
	return panelStyle.Width(m.contentWidth()).Render(row)
}

func (m model) fieldCell(label string, area focusArea) string {
	style := labelStyle
	if m.focus == area {
		style = focusedLabelStyle
	}
	return style.Render(label) + "  " + m.inputs[area].View()
}

// viewTeam draws the roster panel.
func (m model) viewTeam() string {
	title := panelTitleStyle.Render("Team")
	title = appendIfItFits(m, title, fmt.Sprintf("  %d people", len(m.rows())))

	body := m.teamList.View()
	if len(m.rows()) == 0 {
		hint := truncate("nobody on your team yet — press a to add someone", m.contentWidth()-6)
		body = faintStyle.Render("  "+hint) + strings.Repeat("\n", max(m.teamList.Height()-1, 0))
	}
	return panelStyle.Width(m.contentWidth()).Render(title + "\n" + body)
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

// viewCharts is the row of charts under the summary: the contribution calendar,
// and beside it the repository breakdown.
//
// The repositories panel is dropped rather than squeezed when the terminal is
// too narrow for two: the calendar needs a column per week, and halving that
// costs more than the breakdown adds.
func (m model) viewCharts() string {
	if m.contentWidth() < minChartsSideBySide {
		return m.viewCalendar(m.contentWidth())
	}

	// Side by side there are two borders to pay for rather than one, and a
	// border sits outside the width its panel was given.
	total := m.contentWidth() - 2
	calendarWidth := total / 2
	calendar := m.viewCalendar(calendarWidth)

	// The panels are read as one row, so the repositories match the calendar's
	// height rather than each standing at its own.
	body := lipgloss.Height(calendar) - panelChrome + 1
	repos := m.viewRepos(total-calendarWidth, body)
	return lipgloss.JoinHorizontal(lipgloss.Top, calendar, repos)
}

func (m model) viewCalendar(width int) string {
	body := strings.Join([]string{
		panelTitleStyle.Render("Contribution calendar"),
		renderCalendar(m.report.Collection.Calendar, width-4, m.calendarSpaced),
	}, "\n")
	return panelStyle.Width(width).Render(body)
}

// viewRepos is the repository breakdown, in whichever of its two modes is on
// show. Height is what the calendar beside it came to, so the row is square.
func (m model) viewRepos(width, height int) string {
	title := panelTitleStyle.Render("Repositories") +
		faintStyle.Render("  "+m.repoChart.String())

	totals := contrib.RepoTotals(m.report)
	inner := width - 4
	rows := max(height-1, 1)

	var body string
	switch {
	case len(totals) == 0:
		body = faintStyle.Render("no repository activity")
	case m.repoChart == chartTiles:
		body = renderRepoTiles(totals, inner)
	default:
		body = renderRepoBars(totals, inner, rows)
	}

	// Pad to the height the row was given so the two panels close together.
	body = lipgloss.NewStyle().Height(rows).Render(body)
	return panelStyle.Width(width).Render(title + "\n" + body)
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
	case m.busy:
		// A fetch is slow enough that a screen saying nothing looks broken. The
		// spinner goes where the results will appear, not only in the status
		// line under the layout.
		waiting := strings.TrimSpace(m.inputs[focusUser].Value())
		if m.pending.login != "" {
			waiting = m.pending.login
		}
		body = "  " + m.spinner.View() + faintStyle.Render(" loading "+waiting+"…") +
			strings.Repeat("\n", max(m.list.Height()-1, 0))
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
	title := panelTitleStyle.Render("Logs") + faintStyle.Render("  alt+L to close")
	return panelStyle.Width(m.contentWidth()).Render(title + "\n" + m.logView.View())
}

func (m model) viewFooter() string {
	status := m.status
	if m.busy {
		status = m.spinner.View() + status
	}

	bindings := keys.ShortHelp()
	switch {
	case m.adding:
		bindings = addPersonHelp()
	case m.showingSuggestions():
		bindings = suggestionHelp()
	case m.showLogs:
		bindings = logHelp()
	case m.screen == screenDashboard:
		bindings = dashboardHelp()
	}
	// The help component stops truncating once its running width lands exactly
	// on the limit, and then appends every remaining binding in full. Clamping
	// the rendered line is ANSI-aware, where cutting the string by hand would
	// slice an escape sequence in half.
	help := lipgloss.NewStyle().MaxWidth(m.width).Render(m.help.ShortHelpView(bindings))
	return m.level.style().Render(truncate(status, m.width)) + "\n" + help
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

	if m.screen == screenDashboard {
		m.resizeDashboard()
		return
	}

	// The panels are measured rather than predicted: a long stat row, a wrapped
	// warning, or a narrow terminal that stacks the filter fields all change
	// their height, and a budget that guesses wrong scrolls the screen.
	free := m.height - lipgloss.Height(m.viewHeader()) -
		lipgloss.Height(m.viewFilters()) - lipgloss.Height(m.viewFooter())
	if rows := m.viewSuggestions(); rows != "" {
		free -= lipgloss.Height(rows)
	}
	m.showSummary, m.showCalendar, m.calendarSpaced = false, false, false

	if m.report != nil {
		if summary := lipgloss.Height(m.viewSummary()); free-summary >= panelChrome+minListRows {
			m.showSummary = true
			free -= summary
		}
		// The roomy calendar first, then the tight one, and only then is the
		// calendar dropped: losing the spacing is a smaller loss than losing the
		// calendar with it.
		for _, spaced := range []bool{true, false} {
			if !m.showSummary {
				break
			}
			if h := calendarHeight(spaced); free-h >= panelChrome+minListRows {
				m.showCalendar, m.calendarSpaced = true, spaced
				free -= h
				break
			}
		}
	}

	rows := free - panelChrome
	if rows < minListRows {
		rows = minListRows
	}

	inner := m.contentWidth() - 4
	m.list.SetSize(inner, rows)
	m.sizeLogPane(inner, rows)
}

// resizeDashboard gives the roster everything the header, the date fields, and
// the footer leave behind. There is nothing here to drop when the terminal is
// short: the rows themselves scroll.
func (m *model) resizeDashboard() {
	free := m.height - lipgloss.Height(m.viewHeader()) -
		lipgloss.Height(m.viewWindow()) - lipgloss.Height(m.viewFooter())
	if rows := m.viewSuggestions(); rows != "" {
		free -= lipgloss.Height(rows)
	}

	rows := free - panelChrome
	if rows < minListRows {
		rows = minListRows
	}

	inner := m.contentWidth() - 4
	m.teamList.SetSize(inner, rows)
	m.sizeLogPane(inner, rows)
}

// sizeLogPane fits the log viewport to the space the list would have had.
//
// Collaborators log from their own goroutines, so the pane is refreshed from
// the buffer on every message rather than pushed to. It follows the tail unless
// the user has scrolled up to read something, which is the one moment an
// arriving line must not yank the view out from under them.
func (m *model) sizeLogPane(width, height int) {
	follow := m.logView.AtBottom()
	m.logView.Width = width
	m.logView.Height = height
	m.logView.SetContent(m.logs.Render(width))
	if follow {
		m.logView.GotoBottom()
	}
}
