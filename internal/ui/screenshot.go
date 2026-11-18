package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"

	"gh-contrib/internal/contrib"
)

// Screenshot describes a frame to render for the documentation.
//
// It exists because the screens are worth showing and cannot be photographed:
// the model, its rows, and View are unexported, and the fakes that stand in for
// GitHub live in test files. This hands the UI finished reports and asks it to
// draw, with no service, no network, and no terminal.
type Screenshot struct {
	Width, Height int
	Host          string

	// Team is the roster, in the order it was curated. The dashboard sorts it.
	Team []contrib.Person

	// Reports are the finished reports, by login. A member with none is drawn
	// the way an unreachable one is, which is a state worth showing.
	Reports map[string]*contrib.Report

	// Window is the range the reports cover, so rows are not treated as
	// belonging to a range they were never fetched for.
	Window contrib.Range

	// Open is the login whose details to show. Empty renders the dashboard.
	Open string

	// Tiles draws the repository breakdown as tiles rather than bars.
	Tiles bool
}

// Render draws one frame and returns it, escape sequences and all.
func Render(s Screenshot) string {
	m := newModel()
	m.host = s.Host
	m.width, m.height = s.Width, s.Height
	m.ready = true
	if s.Tiles {
		m.repoChart = chartTiles
	}

	// The roster and its rows. Reports are already in hand, so every row is
	// either loaded or failed -- there is nothing in flight in a screenshot.
	items := make([]list.Item, 0, len(s.Team))
	for _, person := range s.Team {
		m.team = m.team.Add(person)

		row := teamRow{
			person: person,
			state:  rowFailed,
			err:    "user not found",
		}
		if report := s.report(person.Login); report != nil {
			row = teamRow{
				person:    person,
				report:    report,
				window:    s.Window,
				fetchedAt: time.Now(),
				state:     rowLoaded,
			}
		}
		items = append(items, row)
	}
	m.setTeamRows(items)
	m = m.settleTeamStatus()

	if s.Open != "" {
		m.screen = screenDetails
		m.advertiseWindow()
		m.inputs[focusUser].SetValue(s.Open)
		if report := s.report(s.Open); report != nil {
			m = m.showReport(report)
		}
	}

	m.resize()
	return m.View()
}

func (s Screenshot) report(login string) *contrib.Report {
	return s.Reports[strings.ToLower(strings.TrimSpace(login))]
}
