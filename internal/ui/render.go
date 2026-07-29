package ui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"gh-contribs/internal/contrib"
)

var weekdayLabels = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

// renderSummary writes the headline contribution totals.
func renderSummary(v *tview.TextView, report *contrib.Report) {
	v.Clear()
	if report == nil {
		return
	}
	cc := report.Collection
	fmt.Fprintf(v,
		"[::b]%s[::-]\nRange: %s -> %s\nTotal: [green]%d[-]\nCommits: %d  Issues: %d  PRs: %d (%d merged)  Reviews: %d",
		report.DisplayName(),
		cc.StartedAt.Format(contrib.DateFormat),
		cc.EndedAt.Format(contrib.DateFormat),
		cc.Calendar.Total,
		cc.TotalCommits,
		cc.TotalIssues,
		cc.TotalPullRequests,
		cc.TotalMergedPullRequests,
		cc.TotalReviews,
	)
	for _, warning := range report.Warnings {
		fmt.Fprintf(v, "\n[yellow]! %s[-]", warning)
	}
}

// renderHeatmap lays the contribution calendar out as weekday rows by week
// columns, matching GitHub's own orientation.
func renderHeatmap(table *tview.Table, report *contrib.Report) {
	table.Clear()
	table.SetCell(0, 0, tview.NewTableCell("D/W").SetTextColor(tcell.ColorYellow))
	for row, label := range weekdayLabels {
		table.SetCell(row+1, 0, tview.NewTableCell(label).SetTextColor(tcell.ColorGray))
	}
	if report == nil {
		return
	}

	for col, week := range report.Collection.Calendar.Weeks {
		table.SetCell(0, col+1, tview.NewTableCell(fmt.Sprintf("%d", col+1)).SetTextColor(tcell.ColorGray))
		for _, day := range week.Days {
			cell := tview.NewTableCell(fmt.Sprintf("%2d", day.Count)).
				SetTextColor(tcell.ColorBlack).
				SetBackgroundColor(tcell.GetColor(day.Color)).
				SetAlign(tview.AlignCenter)
			table.SetCell(normalizeWeekday(day.Date.Weekday())+1, col+1, cell)
		}
	}
}

// renderEventList fills the activity list and seeds the detail pane with the
// first URL.
func renderEventList(list *tview.List, detail *tview.TextView, events []contrib.Event) {
	list.Clear()
	detail.Clear()
	for _, event := range events {
		url := event.URL
		list.AddItem(eventTitle(event), eventSubtitle(event), 0, func() {
			detail.SetText(url)
		})
	}
	if len(events) > 0 {
		detail.SetText(events[0].URL)
	}
}

// eventTitle prefixes the title with the kind. tview parses square brackets in
// list items as color tags even though lists have no dynamic-color switch, so
// a bracketed label like "[review]" would be swallowed whole; the label is
// padded into a column instead.
func eventTitle(event contrib.Event) string {
	return fmt.Sprintf("%-9s %s", eventLabel(event), event.Title)
}

func eventLabel(event contrib.Event) string {
	switch event.Kind {
	case contrib.KindAuthored:
		if event.Merged {
			return "pr merged"
		}
		return "pr"
	case contrib.KindReviewed:
		return "review"
	default:
		return event.Kind
	}
}

func eventSubtitle(event contrib.Event) string {
	return fmt.Sprintf("%s | %s", event.OccurredAt.Format(contrib.DateFormat), event.Repo)
}

// normalizeWeekday maps a weekday onto a Monday-first row index.
func normalizeWeekday(w time.Weekday) int {
	if w == time.Sunday {
		return 6
	}
	return int(w) - 1
}
