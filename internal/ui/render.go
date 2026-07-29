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
		"[::b]%s[::-]\nRange: %s -> %s\nTotal: [green]%d[-]\nCommits: %d  Issues: %d  PRs: %d  Reviews: %d",
		report.DisplayName(),
		cc.StartedAt.Format(contrib.DateFormat),
		cc.EndedAt.Format(contrib.DateFormat),
		cc.Calendar.Total,
		cc.TotalCommits,
		cc.TotalIssues,
		cc.TotalPullRequests,
		cc.TotalReviews,
	)
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

// renderPRList fills the PR list and seeds the detail pane with the first URL.
func renderPRList(list *tview.List, detail *tview.TextView, prs []contrib.PRItem) {
	list.Clear()
	detail.Clear()
	for _, item := range prs {
		url := item.URL
		list.AddItem(prTitle(item), prSubtitle(item), 0, func() {
			detail.SetText(url)
		})
	}
	if len(prs) > 0 {
		detail.SetText(prs[0].URL)
	}
}

func prTitle(item contrib.PRItem) string {
	return fmt.Sprintf("[%s] %s", item.Kind, item.Title)
}

func prSubtitle(item contrib.PRItem) string {
	return fmt.Sprintf("%s | %s", item.OccurredAt.Format(contrib.DateFormat), item.Repo)
}

// normalizeWeekday maps a weekday onto a Monday-first row index.
func normalizeWeekday(w time.Weekday) int {
	if w == time.Sunday {
		return 6
	}
	return int(w) - 1
}
