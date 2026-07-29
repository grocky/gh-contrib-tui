package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"gh-contribs/internal/contrib"
)

const (
	// dayCell is one day of the calendar. One column per week keeps a full
	// year inside 53 columns, which fits an 80-column terminal with room for
	// the weekday gutter.
	dayCell = "■"

	// gutter labels the weekday rows. Only every other row is labelled, the
	// way GitHub does it, so the calendar stays readable when it is dense.
	gutterWidth = 4
)

var weekdayLabels = []string{"Mon", "", "Wed", "", "Fri", "", ""}

// renderHeatmap draws the contribution calendar as weekday rows by week
// columns. When the terminal is too narrow for the whole window, the most
// recent weeks win: the current stretch of activity is what people look at.
func renderHeatmap(cal contrib.Calendar, width int) string {
	weeks := cal.Weeks
	if len(weeks) == 0 {
		return faintStyle.Render("no calendar data")
	}
	if max := width - gutterWidth; max > 0 && len(weeks) > max {
		weeks = weeks[len(weeks)-max:]
	}

	// Index the days by weekday so ragged first and last weeks still line up.
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

	var b strings.Builder
	b.WriteString(monthRow(weeks))
	b.WriteByte('\n')
	for row := 0; row < 7; row++ {
		b.WriteString(faintStyle.Render(pad(weekdayLabels[row], gutterWidth)))
		for _, day := range grid[row] {
			if day == nil {
				b.WriteString(" ")
				continue
			}
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(day.Color)).Render(dayCell))
		}
		if row < 6 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// monthRow labels each column where a new month begins.
func monthRow(weeks []contrib.Week) string {
	row := []rune(strings.Repeat(" ", gutterWidth+len(weeks)))
	lastMonth := ""
	for col, week := range weeks {
		for _, day := range week.Days {
			month := day.Date.Format("Jan")
			if month == lastMonth {
				continue
			}
			lastMonth = month
			// Only label if the abbreviation fits before the next column's
			// label would start, and if this is the month's first week.
			if day.Date.Day() > 7 || col+gutterWidth+3 > len(row) {
				continue
			}
			copy(row[col+gutterWidth:], []rune(month))
			break
		}
	}
	return faintStyle.Render(string(row))
}

// renderLegend draws the shade scale under the calendar.
func renderLegend() string {
	var b strings.Builder
	b.WriteString(faintStyle.Render("less "))
	for _, color := range contrib.HeatLevels() {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(dayCell))
	}
	b.WriteString(faintStyle.Render(" more"))
	return b.String()
}

// weekdayRow maps a day onto a Monday-first row index.
func weekdayRow(day contrib.Day) int {
	if w := int(day.Date.Weekday()); w > 0 {
		return w - 1
	}
	return 6 // Sunday closes the week.
}

func pad(s string, width int) string {
	if len(s) >= width {
		return s[:width]
	}
	return s + strings.Repeat(" ", width-len(s))
}
