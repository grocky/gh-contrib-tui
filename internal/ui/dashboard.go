package ui

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"gh-contribs/internal/contrib"
)

const (
	// miniUpper and miniLower are half-block cells. One character carries two
	// weekdays -- the foreground paints the upper, the background the lower --
	// so a seven-row calendar fits in the four lines a dashboard row can spare.
	miniUpper = "▀"
	miniLower = "▄"

	// rowHeight is the four lines one team member occupies, which is what the
	// halved calendar needs.
	rowHeight = 4

	// Column widths for a row. The stat cell is sized for the widest label a
	// stat can carry -- a four-figure pull request count with its merged tally
	// -- so no number in the column is ever the one that gets cut.
	rowNameWidth  = 22
	statCellWidth = 21
	rowStatWidth  = statCellWidth * 2

	// miniWeeks bounds the calendar. It is also a fixed block width: rows are
	// read down the screen, so a short history and a long one have to start
	// their calendars in the same column.
	miniWeeks = 26
)

// rowState is how far along a team member's report is.
type rowState int

const (
	rowLoading rowState = iota
	rowLoaded
	rowFailed
)

// teamRow is one person on the dashboard, and whatever is known about them so
// far. A row exists as soon as the person is on the team: it is the thing that
// shows them loading, stale, or unreachable.
type teamRow struct {
	person contrib.Person
	report *contrib.Report

	// window records which range the report covers, so a row is not painted
	// against dates it was never fetched for.
	window contrib.Range

	fetchedAt time.Time
	stale     bool
	state     rowState
	err       string
}

// FilterValue is what "/" searches the dashboard by.
func (r teamRow) FilterValue() string {
	return strings.TrimSpace(r.person.Login + " " + r.person.Name)
}

// newTeamList builds the dashboard list. Like the contribution list it draws no
// chrome of its own; the panel around it does that.
func newTeamList() list.Model {
	l := list.New(nil, teamDelegate{}, 0, 0)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)
	l.SetFilteringEnabled(true)
	l.SetShowFilter(false)
	l.Styles.NoItems = faintStyle.PaddingLeft(2)
	return l
}

// teamDelegate renders one team member as three side-by-side blocks: who they
// are, a stack of their totals, and a minified contribution calendar.
type teamDelegate struct{}

func (teamDelegate) Height() int                         { return rowHeight }
func (teamDelegate) Spacing() int                        { return 1 }
func (teamDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d teamDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	row, ok := item.(teamRow)
	if !ok {
		return
	}
	fmt.Fprint(w, renderTeamRow(row, m.Width(), index == m.Index()))
}

// renderTeamRow lays out one member. The blocks are joined at the top so the
// name, the first stat, and the first calendar row all share a line.
//
// It folds rather than truncates as the terminal narrows: two stat columns
// become one, then the stats give way to the name alone. A label cut in half is
// worse than a label that moved.
func renderTeamRow(row teamRow, width int, selected bool) string {
	if width <= 0 {
		width = rowNameWidth + rowStatWidth
	}

	// Too narrow for even one stat column: the name block takes the row, and
	// carries the headline total so the row still says something.
	if width < rowNameWidth+statCellWidth {
		return renderRowName(row, selected, width, true)
	}

	// Two stat columns need their own width plus room to breathe; below that the
	// stats stack in one column and the total moves in with the name.
	wide := width >= rowNameWidth+rowStatWidth+2
	columns := 1
	if wide {
		columns = 2
	}

	name := renderRowName(row, selected, rowNameWidth, !wide)
	stats := renderRowStats(row, columns)

	// The calendar sits in a block of its own, pinned to the right. The block
	// keeps its width whether or not this particular row has a calendar to put
	// in it, so every row's calendar starts in the same column. Below the width
	// it needs, the calendar is dropped rather than squeezed into a smear.
	used := rowNameWidth + statCellWidth*columns
	room := width - used - 2
	if room < 6 {
		return lipgloss.JoinHorizontal(lipgloss.Top, name, stats)
	}

	weeks := min(room, miniWeeks)
	calendar := lipgloss.NewStyle().Width(weeks).Render(renderMiniCalendar(row.report, weeks))
	gap := max(width-used-weeks, 1)

	return lipgloss.JoinHorizontal(lipgloss.Top,
		name, stats, lipgloss.NewStyle().Width(gap).Render(""), calendar)
}

// renderRowName is the who-and-how-fresh block: display name, login, and what
// the row is currently doing. It takes the headline total as well when the row
// is too narrow for the stats to carry it.
func renderRowName(row teamRow, selected bool, width int, withTotal bool) string {
	cursor := "  "
	nameStyle := lipgloss.NewStyle().Foreground(colorText)
	if selected {
		cursor = cursorStyle.Render("▸ ")
		nameStyle = selectedTitleStyle
	}

	name := truncate(row.person.DisplayName(), width-2)
	login := truncate("@"+row.person.Login, width-2)

	total := ""
	if withTotal && row.report != nil {
		total = "  " + totalStyle.Render(fmt.Sprintf("%d", row.report.Collection.Calendar.Total)) +
			mutedStyle.Render(" contributions")
	}

	lines := []string{
		cursor + nameStyle.Render(name),
		"  " + faintStyle.Render(login),
		"  " + rowStatusLine(row, width),
		total,
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

// rowStatusLine says where the row's data came from and how old it is, so a
// number nobody has refreshed in a day is never mistaken for a live one.
func rowStatusLine(row teamRow, width int) string {
	switch {
	case row.state == rowFailed:
		return dangerStyle.Render(truncate("failed: "+row.err, width-2))
	case row.state == rowLoading:
		return faintStyle.Render("loading…")
	case row.stale:
		return warningStyle.Render(truncate(ago(row.fetchedAt)+" · refreshing", width-2))
	case row.fetchedAt.IsZero():
		return ""
	default:
		return faintStyle.Render(ago(row.fetchedAt))
	}
}

// renderRowStats is the stack of totals: the headline count, then the four
// kinds in a fixed grid so the numbers line up down the dashboard. In one
// column the headline moves to the name block and each kind takes its own line.
func renderRowStats(row teamRow, columns int) string {
	width := statCellWidth * columns
	if row.report == nil {
		return lipgloss.NewStyle().Width(width).Render(faintStyle.Render("  no data yet"))
	}

	cc := row.report.Collection
	prs := fmt.Sprintf("prs (%d merged)", cc.TotalMergedPullRequests)

	var lines []string
	if columns == 1 {
		lines = []string{
			statCell(cc.TotalCommits, "commits", "commit"),
			statCell(cc.TotalPullRequests, prs, "pr"),
			statCell(cc.TotalIssues, "issues", "issue"),
			statCell(cc.TotalReviews, "reviews", "review"),
		}
	} else {
		lines = []string{
			totalStyle.Render(fmt.Sprintf("%d", cc.Calendar.Total)) +
				mutedStyle.Render(" contributions"),
			statCell(cc.TotalCommits, "commits", "commit") + statCell(cc.TotalPullRequests, prs, "pr"),
			statCell(cc.TotalIssues, "issues", "issue") + statCell(cc.TotalReviews, "reviews", "review"),
			"",
		}
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

// statCell is one number and its label, the number right-aligned in a fixed
// field so the column reads as a column.
func statCell(count int, label, kind string) string {
	cell := kindStyles[kind].Render(fmt.Sprintf("%4d", count)) + " " + mutedStyle.Render(label)
	return pad(cell, statCellWidth)
}

// renderMiniCalendar draws the heatmap at half height: two weekdays per line.
// It is a shape rather than a readable grid at this size, which is the point --
// it says at a glance whether someone has been busy and when.
func renderMiniCalendar(report *contrib.Report, maxWeeks int) string {
	if report == nil {
		return ""
	}
	weeks := report.Collection.Calendar.Weeks
	if len(weeks) == 0 {
		return ""
	}
	if maxWeeks > 0 && len(weeks) > maxWeeks {
		weeks = weeks[len(weeks)-maxWeeks:]
	}

	// Index by weekday so ragged first and last weeks line up, the same way the
	// full calendar does.
	grid := make([][]*contrib.Day, 7)
	for i := range grid {
		grid[i] = make([]*contrib.Day, len(weeks))
	}
	for col, week := range weeks {
		for i := range week.Days {
			day := week.Days[i]
			grid[weekdayRow(day)][col] = &day
		}
	}

	lines := make([]string, 0, rowHeight)
	for row := 0; row < 7; row += 2 {
		var b strings.Builder
		for col := range weeks {
			var lower *contrib.Day
			if row+1 < 7 {
				lower = grid[row+1][col]
			}
			b.WriteString(miniCell(grid[row][col], lower))
		}
		lines = append(lines, b.String())
	}
	return strings.Join(lines, "\n")
}

// miniCell pairs two weekdays into one character. A day missing from a ragged
// week leaves its half blank rather than borrowing the empty-day shade, which
// at this size would read as a day someone did nothing on.
func miniCell(upper, lower *contrib.Day) string {
	switch {
	case upper == nil && lower == nil:
		return " "
	case upper == nil:
		return lipgloss.NewStyle().Foreground(shadeOf(*lower)).Render(miniLower)
	case lower == nil:
		return lipgloss.NewStyle().Foreground(shadeOf(*upper)).Render(miniUpper)
	default:
		return lipgloss.NewStyle().
			Foreground(shadeOf(*upper)).
			Background(shadeOf(*lower)).
			Render(miniUpper)
	}
}

// ago renders how long since a report was fetched, at the resolution people
// actually care about.
func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
