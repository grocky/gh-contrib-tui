package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/grocky/gh-contrib-tui/internal/contrib"
)

const (
	// hairlineCell is one day of the calendar. It fills all but the top eighth
	// of its character cell, and that sliver is the gap between one weekday row
	// and the next.
	//
	// The gap used to be a blank line, which is a whole row of it -- far more
	// than the grid needs, and six rows the contribution list could have had.
	// A terminal cannot draw a smaller gap than a row, but a cell can decline to
	// fill one.
	hairlineCell = "▇"

	// denseCell is the fallback for terminals too narrow to give every week a
	// gap. Full blocks with nothing between them would fuse a row into one
	// unreadable bar, so the smaller glyph draws its own separation.
	denseCell = "■"

	// maxCellWidth caps how wide a day grows. A character cell is about twice
	// as tall as it is wide, so two columns is roughly square; wider than that
	// and the calendar reads as a row of bars.
	maxCellWidth = 2

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
// The light column is GitHub's own scale. The dark column climbs the same hues
// the other way, but starts well clear of black: GitHub's dark scale opens on a
// near-black grey and a near-black green, which a browser separates with a
// border and a terminal does not, so the two lowest levels arrive looking like
// the same cell.
var heatPalette = []lipgloss.AdaptiveColor{
	{Light: "#ebedf0", Dark: "#30363d"},
	{Light: "#aceebb", Dark: "#196c2e"},
	{Light: "#4ac26b", Dark: "#2ea043"},
	{Light: "#2da44e", Dark: "#46c76a"},
	{Light: "#116329", Dark: "#7ee787"},
}

// heatLevels indexes the domain's shades so a day can be re-coloured.
var heatLevels = func() map[string]int {
	levels := map[string]int{}
	for i, color := range contrib.HeatLevels() {
		levels[color] = i
	}
	return levels
}()

// shadeOf maps a day onto the palette. The domain hands out a light-theme hex;
// this turns it back into a level so the terminal's own theme can colour it.
func shadeOf(day contrib.Day) lipgloss.AdaptiveColor {
	level := heatLevels[day.Color]
	if level >= len(heatPalette) {
		level = len(heatPalette) - 1
	}
	return heatPalette[level]
}

// cellStyle is the style for one calendar day.
func cellStyle(day contrib.Day) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(shadeOf(day))
}

// layout is how one day of the calendar is drawn: cell glyph columns followed
// by step-cell columns of gap before the next week.
type layout struct{ cell, step int }

// layoutFor grows the day cell to whatever the panel can afford. A year is 53
// columns however big the terminal is, so the room left over is spent making
// the days themselves bigger rather than left as dead space on the right.
func layoutFor(weeks, width int) layout {
	avail := width - gutterWidth
	for cell := maxCellWidth; cell >= 1; cell-- {
		if weeks*(cell+1) <= avail {
			return layout{cell, cell + 1}
		}
	}
	return layout{1, 1} // Too narrow for gaps: pack the weeks shoulder to shoulder.
}

func (l layout) text() string {
	if l.step == 1 {
		return denseCell
	}
	return strings.Repeat(hairlineCell, l.cell)
}

func (l layout) blank() string { return strings.Repeat(" ", l.cell) }
func (l layout) gap() string   { return strings.Repeat(" ", l.step-l.cell) }

// weeksThatFit is how many columns the width holds. The last week pays for its
// cell but not for a trailing gap.
func (l layout) weeksThatFit(width int) int {
	return (width-gutterWidth-l.cell)/l.step + 1
}

// renderCalendar draws the contribution calendar and its legend, both at the
// cell size the width affords. The legend always keeps a blank line of its own:
// butted against the grid it reads as an eighth weekday row.
func renderCalendar(cal contrib.Calendar, width int) string {
	if len(cal.Weeks) == 0 {
		return faintStyle.Render("no calendar data")
	}
	l := layoutFor(len(cal.Weeks), width)
	return renderHeatmap(cal, width, l) + "\n\n" + renderLegend(l)
}

// renderHeatmap draws the calendar as weekday rows by week columns. When the
// terminal is too narrow for the whole window, the most recent weeks win: the
// current stretch of activity is what people look at.
func renderHeatmap(cal contrib.Calendar, width int, l layout) string {
	weeks := cal.Weeks
	if max := l.weeksThatFit(width); max > 0 && len(weeks) > max {
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
	b.WriteString(monthRow(weeks, l))
	b.WriteByte('\n')
	for row := 0; row < 7; row++ {
		b.WriteString(faintStyle.Render(pad(weekdayLabels[row], gutterWidth)))
		for col, day := range grid[row] {
			if col > 0 {
				b.WriteString(l.gap())
			}
			if day == nil {
				b.WriteString(l.blank())
				continue
			}
			b.WriteString(cellStyle(*day).Render(l.text()))
		}
		if row < 6 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// monthRow labels each column where a new month begins.
func monthRow(weeks []contrib.Week, l layout) string {
	row := []rune(strings.Repeat(" ", gutterWidth+(len(weeks)-1)*l.step+l.cell))
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
			start := gutterWidth + col*l.step
			if day.Date.Day() > 7 || start+3 > len(row) {
				continue
			}
			copy(row[start:], []rune(month))
			break
		}
	}
	return faintStyle.Render(string(row))
}

// renderLegend draws the shade scale under the calendar, in the same cells the
// calendar itself uses.
func renderLegend(l layout) string {
	var b strings.Builder
	b.WriteString(faintStyle.Render("less "))
	for i, shade := range heatPalette {
		if i > 0 {
			b.WriteString(l.gap())
		}
		b.WriteString(lipgloss.NewStyle().Foreground(shade).Render(l.text()))
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
