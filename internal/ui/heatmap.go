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

// heatPalette is GitHub's calendar scale in both themes. The domain layer
// assigns each day a shade by its share of the busiest day; that shade is a
// light-theme hex, so it is mapped back to a level here and re-rendered in
// whichever palette the terminal calls for. Without this, an empty day's near
// white reads as a busy one against a dark background.
var heatPalette = []lipgloss.AdaptiveColor{
	{Light: "#ebedf0", Dark: "#21262d"},
	{Light: "#9be9a8", Dark: "#0e4429"},
	{Light: "#40c463", Dark: "#006d32"},
	{Light: "#30a14e", Dark: "#26a641"},
	{Light: "#216e39", Dark: "#39d353"},
}

// heatLevels indexes the domain's shades so a day can be re-coloured.
var heatLevels = func() map[string]int {
	levels := map[string]int{}
	for i, color := range contrib.HeatLevels() {
		levels[color] = i
	}
	return levels
}()

// cellStyle is the style for one calendar day.
func cellStyle(day contrib.Day) lipgloss.Style {
	level := heatLevels[day.Color]
	if level >= len(heatPalette) {
		level = len(heatPalette) - 1
	}
	return lipgloss.NewStyle().Foreground(heatPalette[level])
}

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
			b.WriteString(cellStyle(*day).Render(dayCell))
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
	for _, shade := range heatPalette {
		b.WriteString(lipgloss.NewStyle().Foreground(shade).Render(dayCell))
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

// pad right-pads to a display width. It measures the way truncate does rather
// than counting bytes: a display name like "Grønvold, Åse" is wider in bytes
// than on screen, and slicing it by byte both misaligns the column and cuts a
// rune in half.
func pad(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return truncate(s, width)
	}
	return s + strings.Repeat(" ", width-w)
}
