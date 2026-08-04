package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"gh-contribs/internal/contrib"
)

// tileShades are the blocks the tile chart is drawn from, densest first, so the
// biggest share is also the heaviest mark on the row. They carry the ranking a
// second way for anyone who cannot separate the palette's greens. They are
// separate strings rather than one: indexing a string of block characters by
// position slices a rune in half.
var tileShades = []string{"█", "▓", "▒", "░"}

const (
	// barFull is the bar chart's unit. A full block reads as a measured length
	// rather than a row of pips.
	barFull = "█"

	// repoNameWidth bounds the label column on the bar chart.
	repoNameWidth = 24

	// repoCountWidth is the right-hand column the counts are aligned in.
	repoCountWidth = 6

	// minTileWidth is the narrowest tile worth drawing. Anything smaller is
	// gathered into the tail tile rather than drawn as a sliver nobody can
	// label.
	minTileWidth = 2
)

// repoChartMode is which view of the repository breakdown is on show.
type repoChartMode int

const (
	// chartBars ranks repositories by length, which is the easier of the two to
	// compare exactly.
	chartBars repoChartMode = iota
	// chartTiles lays them out as one row divided by share, which is the easier
	// to take in at a glance.
	chartTiles
)

func (c repoChartMode) String() string {
	if c == chartTiles {
		return "tiles"
	}
	return "bars"
}

func (c repoChartMode) next() repoChartMode {
	if c == chartBars {
		return chartTiles
	}
	return chartBars
}

// renderRepoBars draws one repository per line: name, a bar in proportion to
// the busiest, and the count.
//
// Bars are scaled against the largest rather than against the total, so the
// chart uses its whole width whatever the spread looks like.
func renderRepoBars(totals []contrib.RepoTotal, width, height int) string {
	if len(totals) == 0 || height <= 0 {
		return faintStyle.Render("no repository activity")
	}

	nameWidth := min(repoNameWidth, max(width/3, 8))
	barWidth := width - nameWidth - repoCountWidth - 2
	if barWidth < 1 {
		barWidth = 1
	}

	// One line goes to the tail when there is more than the panel can hold.
	shown := min(len(totals), height)
	tail := len(totals) - shown
	if tail > 0 {
		shown = max(height-1, 0)
		tail = len(totals) - shown
	}

	largest := totals[0].Count
	lines := make([]string, 0, height)
	for _, total := range totals[:shown] {
		lines = append(lines, fmt.Sprintf("%s %s %s",
			pad(truncate(total.Repo, nameWidth), nameWidth),
			repoBar(total.Count, largest, barWidth),
			mutedStyle.Render(padLeft(fmt.Sprintf("%d", total.Count), repoCountWidth))))
	}
	if tail > 0 {
		lines = append(lines, faintStyle.Render(fmt.Sprintf("  +%d more", tail)))
	}
	return strings.Join(lines, "\n")
}

// repoBar is one bar, scaled against the busiest repository. A repository
// somebody touched once still gets a mark: a bar rounded away to nothing reads
// as no activity at all.
func repoBar(count, largest, width int) string {
	if largest <= 0 || width <= 0 {
		return ""
	}
	length := int(math.Round(float64(count) / float64(largest) * float64(width)))
	if length < 1 && count > 0 {
		length = 1
	}
	return kindStyles["commit"].Render(strings.Repeat(barFull, length)) +
		strings.Repeat(" ", width-length)
}

// renderRepoTiles draws the breakdown as a single row divided by share, with
// the names beneath. It answers "how is the work split" in one line, which the
// ranked bars make you read down to work out.
func renderRepoTiles(totals []contrib.RepoTotal, width int) string {
	if len(totals) == 0 || width < minTileWidth {
		return faintStyle.Render("no repository activity")
	}

	sized := sizeTiles(totals, width)
	if len(sized) == 0 {
		return faintStyle.Render("no repository activity")
	}

	var tiles, labels strings.Builder
	for i, tile := range sized {
		if i > 0 {
			tiles.WriteString(" ")
			labels.WriteString(" ")
		}
		shade := tileShades[min(i, len(tileShades)-1)]
		tiles.WriteString(heatShade(i).Render(strings.Repeat(shade, tile.width)))
		labels.WriteString(faintStyle.Render(pad(truncate(tile.label, tile.width), tile.width)))
	}
	return tiles.String() + "\n" + strings.TrimRight(labels.String(), " ")
}

// tile is one block of the row: how wide it is and what it is called.
type tile struct {
	width int
	label string
}

// sizeTiles divides the width by share. Repositories too small to draw are
// gathered into a tail tile rather than dropped, so the row still adds up to
// everything the person did.
func sizeTiles(totals []contrib.RepoTotal, width int) []tile {
	total := contrib.TotalOf(totals)
	if total <= 0 {
		return nil
	}

	tiles := make([]tile, 0, len(totals))
	used, gathered := 0, 0
	for i, t := range totals {
		// Each tile after the first pays for the space before it.
		room := width - used - len(tiles)
		w := int(math.Round(t.Share(total) * float64(width)))
		if w > room {
			w = room
		}
		if w < minTileWidth || room < minTileWidth {
			gathered = len(totals) - i
			break
		}
		tiles = append(tiles, tile{width: w, label: shortRepo(t.Repo)})
		used += w
	}

	if gathered > 0 && len(tiles) > 0 {
		room := width - used - len(tiles)

		// The tail needs a tile of its own to be labelled, and the widest tile
		// can spare a column or two more easily than the row can lose a name.
		if need := minTileWidth - room; need > 0 && tiles[0].width-need >= minTileWidth {
			tiles[0].width -= need
			room += need
		}
		if room >= minTileWidth {
			tiles = append(tiles, tile{width: room, label: fmt.Sprintf("+%d", gathered)})
		} else {
			// Still nothing to give: the last tile speaks for the rest.
			tiles[len(tiles)-1].label = fmt.Sprintf("+%d", gathered+1)
		}
	}
	return tiles
}

// heatShade colours a tile by its rank, reusing the calendar's palette so the
// two charts on the row are read in the same language.
func heatShade(rank int) lipgloss.Style {
	level := len(heatPalette) - 1 - rank
	if level < 1 {
		level = 1
	}
	return lipgloss.NewStyle().Foreground(heatPalette[level])
}

// shortRepo drops the owner, which is the same for every repository on most
// people's charts and costs width that the name itself needs.
func shortRepo(repo string) string {
	if i := strings.LastIndex(repo, "/"); i >= 0 && i+1 < len(repo) {
		return repo[i+1:]
	}
	return repo
}

// padLeft right-aligns to a display width.
func padLeft(s string, width int) string {
	if w := lipgloss.Width(s); w < width {
		return strings.Repeat(" ", width-w) + s
	}
	return truncate(s, width)
}
