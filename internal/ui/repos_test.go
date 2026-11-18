package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/grocky/gh-contrib-tui/internal/contrib"
)

func totals(pairs ...any) []contrib.RepoTotal {
	out := make([]contrib.RepoTotal, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, contrib.RepoTotal{Repo: pairs[i].(string), Count: pairs[i+1].(int)})
	}
	return out
}

// Bars.

func TestRepoBars_RanksAndCounts(t *testing.T) {
	got := plain(renderRepoBars(totals("acme/api", 128, "acme/web", 64, "acme/docs", 8), 60, 6))
	lines := strings.Split(got, "\n")

	if !strings.Contains(lines[0], "acme/api") || !strings.Contains(lines[0], "128") {
		t.Errorf("first line = %q, want the busiest repository and its count", lines[0])
	}
	if !strings.Contains(lines[2], "acme/docs") {
		t.Errorf("third line = %q, want the quietest repository last", lines[2])
	}
}

// A bar is read against its neighbours, so its length has to carry the ratio.
func TestRepoBars_LengthTracksTheCount(t *testing.T) {
	got := plain(renderRepoBars(totals("half", 50, "quarter", 25), 60, 4))
	lines := strings.Split(got, "\n")

	long := strings.Count(lines[0], barFull)
	short := strings.Count(lines[1], barFull)
	if long <= short {
		t.Fatalf("bars are %d and %d wide, want the larger count drawn longer", long, short)
	}
	if ratio := float64(short) / float64(long); ratio < 0.4 || ratio > 0.6 {
		t.Errorf("bar ratio = %.2f, want about half", ratio)
	}
}

// A repository someone touched once still gets a mark: a bar rounded away to
// nothing reads as no activity at all.
func TestRepoBars_KeepsTheSmallestVisible(t *testing.T) {
	got := plain(renderRepoBars(totals("busy", 500, "once", 1), 60, 4))
	lines := strings.Split(got, "\n")
	if !strings.Contains(lines[1], barFull) {
		t.Errorf("second line = %q, want a mark for the one contribution", lines[1])
	}
}

// The panel is only so tall, and a list cut off without saying so reads as the
// whole story.
func TestRepoBars_SaysWhatItLeftOut(t *testing.T) {
	got := plain(renderRepoBars(totals("a", 9, "b", 8, "c", 7, "d", 6, "e", 5), 60, 3))
	lines := strings.Split(got, "\n")

	if len(lines) != 3 {
		t.Fatalf("bars are %d lines, want the 3 they were given:\n%s", len(lines), got)
	}
	if !strings.Contains(lines[2], "more") {
		t.Errorf("last line = %q, want it to account for the rest", lines[2])
	}
}

// Tiles.

func TestRepoTiles_WidthsFollowShare(t *testing.T) {
	got := plain(renderRepoTiles(totals("big", 75, "small", 25), 40))
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("tiles are %d lines, want a row of tiles and its labels:\n%s", len(lines), got)
	}

	tiles := strings.Fields(lines[0])
	if len(tiles) != 2 {
		t.Fatalf("drew %d tiles, want one per repository: %q", len(tiles), lines[0])
	}
	// Measured on screen rather than in bytes: a block character is three bytes
	// wide and one column, and counting bytes would hide a sliced rune.
	wide, narrow := lipgloss.Width(tiles[0]), lipgloss.Width(tiles[1])
	if ratio := float64(narrow) / float64(wide); ratio < 0.25 || ratio > 0.45 {
		t.Errorf("tile widths are %d and %d columns, want about three to one", wide, narrow)
	}
	for _, tile := range tiles {
		for _, r := range tile {
			if !strings.ContainsRune("█▓▒░", r) {
				t.Fatalf("tile %q contains %q, want whole block characters", tile, r)
			}
		}
	}
	if !strings.Contains(lines[1], "big") || !strings.Contains(lines[1], "small") {
		t.Errorf("labels = %q, want both repositories named", lines[1])
	}
}

// Whatever the split, the row fills its width and no more: a tile row wider
// than its panel takes the border with it.
func TestRepoTiles_FitTheirWidth(t *testing.T) {
	for _, width := range []int{20, 30, 48, 90} {
		got := plain(renderRepoTiles(totals("a", 40, "b", 30, "c", 20, "d", 7, "e", 3), width))
		for _, line := range strings.Split(got, "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("width %d: line is %d columns: %q", width, w, line)
			}
		}
	}
}

// Repositories too small to draw are gathered rather than dropped, so the tiles
// still add up to everything the person did.
func TestRepoTiles_GathersTheTail(t *testing.T) {
	many := totals("big", 100, "a", 1, "b", 1, "c", 1, "d", 1, "e", 1, "f", 1, "g", 1)
	got := plain(renderRepoTiles(many, 24))
	if !strings.Contains(got, "+") {
		t.Errorf("tiles = %q, want the tail gathered into one marked tile", got)
	}
}

// Panel.

func TestViewRepos_ShowsBothModes(t *testing.T) {
	m := detailsWithRepos(t, 140, 44)

	bars := plain(m.viewRepos(60, 8))
	if !strings.Contains(bars, "Repositories") || !strings.Contains(bars, "acme/api") {
		t.Fatalf("bars panel = %q, want it titled and populated", bars)
	}

	m.repoChart = chartTiles
	tiles := plain(m.viewRepos(60, 8))
	if tiles == bars {
		t.Fatal("both modes rendered the same panel")
	}
	// Tiles are only as wide as their share, so the labels drop the owner --
	// which is the same for every repository on most people's charts.
	if !strings.Contains(tiles, "api") || strings.Contains(tiles, "acme/api") {
		t.Errorf("tiles panel = %q, want it labelled with short repository names", tiles)
	}
}

// A report with nothing in it must say so rather than drawing an empty frame.
func TestViewRepos_EmptyReport(t *testing.T) {
	m := newTestModel(t, &fakeService{}, 140, 44)
	m.report = &contrib.Report{Login: "octocat"}
	if got := plain(m.viewRepos(60, 8)); !strings.Contains(got, "no repository activity") {
		t.Errorf("panel = %q, want it to say there is nothing", got)
	}
}

// The two panels are read together, so they have to line up.
func TestViewCharts_PanelsMatchInHeight(t *testing.T) {
	m := detailsWithRepos(t, 140, 44)
	charts := m.viewCharts()

	if !strings.Contains(plain(charts), "Contribution calendar") ||
		!strings.Contains(plain(charts), "Repositories") {
		t.Fatalf("charts row is missing a panel:\n%s", plain(charts))
	}
	// A panel renders its border outside the width it is given, so a row of
	// them comes to the terminal's width, the same as a full-width panel does.
	for i, line := range strings.Split(plain(charts), "\n") {
		if w := lipgloss.Width(line); w > m.width {
			t.Errorf("line %d is %d columns, want at most %d", i, w, m.width)
		}
	}
}

// Below the width two panels need, the calendar keeps the row to itself rather
// than both being squeezed into something unreadable.
func TestViewCharts_DropsTheReposPanelWhenNarrow(t *testing.T) {
	m := detailsWithRepos(t, 70, 44)
	charts := plain(m.viewCharts())

	if strings.Contains(charts, "Repositories") {
		t.Errorf("kept the repo panel with no room for it:\n%s", charts)
	}
	if !strings.Contains(charts, "Contribution calendar") {
		t.Errorf("dropped the calendar too:\n%s", charts)
	}
}

// Toggling.

func TestDetails_TogglesTheChartMode(t *testing.T) {
	m := detailsWithRepos(t, 140, 44)
	if m.repoChart != chartBars {
		t.Fatalf("chart mode = %v, want bars to start", m.repoChart)
	}

	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}, Alt: true})
	if m.repoChart != chartTiles {
		t.Fatalf("chart mode = %v, want tiles after toggling", m.repoChart)
	}

	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}, Alt: true})
	if m.repoChart != chartBars {
		t.Fatalf("chart mode = %v, want it back to bars", m.repoChart)
	}
}

// detailsWithRepos is a details screen showing a report spread over several
// repositories.
func detailsWithRepos(t *testing.T, w, h int) model {
	t.Helper()

	report := sampleReport()
	window := contrib.Range{From: report.Collection.StartedAt, To: report.Collection.EndedAt}
	var events []contrib.Event
	for repo, n := range map[string]int{"acme/api": 20, "acme/web": 9, "acme/docs": 3} {
		for i := 0; i < n; i++ {
			events = append(events, contrib.Event{
				OccurredAt: window.From.AddDate(0, 0, i%20+1),
				Kind:       contrib.KindCommit,
				Repo:       repo,
				Title:      "work",
			})
		}
	}
	report.Collection = contrib.NewCollection(events, window)

	m := newTestModel(t, &fakeService{report: report}, w, h)
	return deliver(t, m, report, nil)
}
