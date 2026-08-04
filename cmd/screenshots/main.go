// Command screenshots renders the frames the README shows and converts them to
// SVG.
//
// The screenshots are the real screens rather than mockups, so they have to be
// reproducible: this owns the sample team and their histories, renders each
// frame through the UI itself, and writes the images. The dates move with the
// calendar -- the app summarizes trailing windows and the frames should show
// what a reader would see today -- but everything else is fixed.
//
//	go run ./cmd/screenshots                 # writes docs/images/*.svg
//	go run ./cmd/screenshots -ansi /tmp/f    # keeps the captured frames too
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"gh-contribs/internal/ansisvg"
	"gh-contribs/internal/contrib"
	"gh-contribs/internal/ui"
)

// The sample team. Real-looking names, because the name column's width is one
// of the things a screenshot is meant to show.
var team = []contrib.Person{
	{Login: "nash-robin", Name: "Nash, Robin", Active: true},
	{Login: "avery-lynn", Name: "Avery, Lynn", Active: true},
	{Login: "olsen-ana", Name: "Olsen, Ana", Active: true},
	{Login: "bianchi-priya", Name: "Bianchi, Priya", Active: true},
}

// work is how many contributions a person made to a repository. A slice rather
// than a map: ranging over a map is randomised, and a screenshot that comes out
// differently every run is not reproducible.
type work struct {
	repo  string
	count int
}

var (
	// The dashboard's window: what the team has been doing lately.
	recent = map[string][]work{
		"nash-robin":        {{"acme/platform-api", 38}, {"acme/web-console", 17}, {"acme/terraform-modules", 9}},
		"avery-lynn":       {{"acme/web-console", 24}, {"acme/design-system", 11}},
		"olsen-ana":        {{"acme/ingest", 21}, {"acme/platform-api", 6}, {"acme/scripts", 2}},
		"bianchi-priya": {{"acme/platform-api", 12}, {"acme/docs", 4}},
	}

	// The details window is longer, so one person's history is deeper and
	// spread over more repositories than the dashboard shows.
	deep = []work{
		{"acme/platform-api", 84},
		{"acme/web-console", 41},
		{"acme/terraform-modules", 19},
		{"acme/docs", 7},
		{"acme/scripts", 3},
	}
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "screenshots: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	out := flag.String("out", "docs/images", "directory to write the SVGs to")
	ansiDir := flag.String("ansi", "", "also keep the captured frames in this directory")
	host := flag.String("host", "github.example.com", "host to label the header with")
	flag.Parse()

	// The frames are captured from a process with no terminal, so the colour
	// profile has to be stated rather than detected.
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)

	dashboardWindow := contrib.TrailingDays(30)
	detailsWindow := contrib.TrailingMonths(3)

	dashboardReports := map[string]*contrib.Report{}
	for _, person := range team {
		if spread, ok := recent[person.Login]; ok {
			dashboardReports[person.Login] = report(person, dashboardWindow, spread)
		}
	}

	// The person the details frames open on has a longer history than the
	// dashboard's window holds.
	subject := team[0]
	detailsReports := map[string]*contrib.Report{subject.Login: report(subject, detailsWindow, deep)}

	frames := []struct {
		name  string
		title string
		shot  ui.Screenshot
	}{
		{
			name:  "dashboard",
			title: "gh-contribs team dashboard",
			shot: ui.Screenshot{
				Width: 112, Height: 30, Host: *host,
				Team: team, Reports: dashboardReports, Window: dashboardWindow,
			},
		},
		{
			name:  "details",
			title: "gh-contribs contribution details",
			shot: ui.Screenshot{
				Width: 120, Height: 42, Host: *host,
				Team: team, Reports: detailsReports, Window: detailsWindow,
				Open: subject.Login,
			},
		},
		{
			name:  "details-tiles",
			title: "gh-contribs repository tiles",
			shot: ui.Screenshot{
				Width: 120, Height: 42, Host: *host,
				Team: team, Reports: detailsReports, Window: detailsWindow,
				Open: subject.Login, Tiles: true,
			},
		},
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", *out, err)
	}
	if *ansiDir != "" {
		if err := os.MkdirAll(*ansiDir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", *ansiDir, err)
		}
	}

	for _, frame := range frames {
		captured := ui.Render(frame.shot)

		if *ansiDir != "" {
			path := filepath.Join(*ansiDir, frame.name+".ansi")
			if err := os.WriteFile(path, []byte(captured), 0o644); err != nil {
				return fmt.Errorf("writing %s: %w", path, err)
			}
		}

		path := filepath.Join(*out, frame.name+".svg")
		svg := ansisvg.Convert(captured, ansisvg.Options{Title: frame.title})
		if err := os.WriteFile(path, []byte(svg), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		fmt.Printf("%s (%d bytes)\n", path, len(svg))
	}
	return nil
}

// report builds one person's history: contributions spread over the window,
// leaning on weekdays the way real work does, and mixed across the four kinds.
func report(person contrib.Person, window contrib.Range, spread []work) *contrib.Report {
	span := int(window.To.Sub(window.From).Hours() / 24)
	if span < 1 {
		span = 1
	}

	var (
		events []contrib.Event
		day    int
	)
	for _, w := range spread {
		for i := 0; i < w.count; i++ {
			// A fixed walk rather than a random one, so the calendar has a
			// shape and the same shape every time.
			day = (day*7 + 11) % span
			at := window.From.AddDate(0, 0, day)
			if at.Weekday() == time.Saturday || at.Weekday() == time.Sunday {
				at = at.AddDate(0, 0, 2)
			}

			kind := kindFor(i)
			events = append(events, contrib.Event{
				OccurredAt: at,
				Kind:       kind,
				Repo:       w.repo,
				Title:      titleFor(kind, i),
				Merged:     kind == contrib.KindAuthored && i%4 == 0,
				// Unique per event: the collection dedupes on kind and URL, and
				// a shared one collapses a whole history into four entries.
				URL: fmt.Sprintf("https://example.com/%s/%d", w.repo, i),
			})
		}
	}

	return &contrib.Report{
		Login:      person.Login,
		Name:       person.Name,
		Collection: contrib.NewCollection(events, window),
	}
}

func kindFor(i int) string {
	switch i % 7 {
	case 3:
		return contrib.KindAuthored
	case 5:
		return contrib.KindReviewed
	case 6:
		return contrib.KindIssue
	default:
		return contrib.KindCommit
	}
}

func titleFor(kind string, i int) string {
	titles := map[string][]string{
		contrib.KindCommit:   {"Fix the retry budget on 429s", "Drop the vendored fork", "Cache directory lookups"},
		contrib.KindAuthored: {"Add read:org fallback for team listings", "Rework the token walk"},
		contrib.KindReviewed: {"Review: split the report cache", "Review: bound the org walk"},
		contrib.KindIssue:    {"Search API undercounts private repos", "Suspended users missing from listings"},
	}
	list := titles[kind]
	return list[i%len(list)]
}
