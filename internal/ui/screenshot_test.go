package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/grocky/gh-contrib-tui/internal/contrib"
)

func shotFixture() Screenshot {
	window := contrib.Range{
		From: sampleReport().Collection.StartedAt,
		To:   sampleReport().Collection.EndedAt,
	}
	busy := reportOver("nash-robin", 30, 3)
	busy.Name = "Nash, Robin"

	return Screenshot{
		Width: 112, Height: 30, Host: "github.example.com",
		Team: []contrib.Person{
			{Login: "nash-robin", Name: "Nash, Robin", Active: true},
			{Login: "avery-lynn", Name: "Avery, Lynn", Active: true},
			{Login: "ghost", Name: "Nobody, Home", Active: true},
		},
		Reports: map[string]*contrib.Report{
			"nash-robin": busy,
			"avery-lynn": reportOver("avery-lynn", 12, 1),
		},
		Window: window,
	}
}

func TestRender_Dashboard(t *testing.T) {
	got := plain(Render(shotFixture()))

	for _, want := range []string{"Team", "Nash, Robin", "@nash-robin", "contributions"} {
		if !strings.Contains(got, want) {
			t.Errorf("dashboard frame is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Contribution calendar") {
		t.Errorf("dashboard frame shows the details screen:\n%s", got)
	}
}

// A member with no report is drawn the way an unreachable one is, which is a
// state the screenshots are meant to be able to show.
func TestRender_MemberWithoutAReport(t *testing.T) {
	got := plain(Render(shotFixture()))

	if !strings.Contains(got, "Nobody, Home") {
		t.Errorf("frame dropped the member with no report:\n%s", got)
	}
	if !strings.Contains(got, "failed") {
		t.Errorf("frame does not show the member as unreachable:\n%s", got)
	}
}

func TestRender_Details(t *testing.T) {
	shot := shotFixture()
	shot.Open = "nash-robin"
	shot.Width, shot.Height = 120, 42

	got := plain(Render(shot))
	for _, want := range []string{"Contribution calendar", "Repositories", "Contributions", "Nash, Robin"} {
		if !strings.Contains(got, want) {
			t.Errorf("details frame is missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "bars") {
		t.Errorf("details frame does not name its chart mode:\n%s", got)
	}
}

func TestRender_DetailsTiles(t *testing.T) {
	shot := shotFixture()
	shot.Open, shot.Tiles = "nash-robin", true
	shot.Width, shot.Height = 120, 42

	got := plain(Render(shot))
	if !strings.Contains(got, "tiles") {
		t.Errorf("frame is not in tiles mode:\n%s", got)
	}
	if !strings.Contains(got, miniUpper) && !strings.Contains(got, "█") {
		t.Errorf("tiles frame drew no tiles:\n%s", got)
	}
}

// A screenshot is a picture of a terminal, so it has to be the size of the
// terminal it claims to be.
func TestRender_FitsTheSizeItWasGiven(t *testing.T) {
	for _, size := range []struct{ w, h int }{{112, 30}, {120, 42}, {80, 24}} {
		shot := shotFixture()
		shot.Width, shot.Height = size.w, size.h

		for _, open := range []string{"", "nash-robin"} {
			shot.Open = open
			frame := plain(Render(shot))

			if got := lipgloss.Height(frame); got > size.h {
				t.Errorf("%dx%d open=%q: frame is %d rows", size.w, size.h, open, got)
			}
			for _, line := range strings.Split(frame, "\n") {
				if got := lipgloss.Width(line); got > size.w {
					t.Errorf("%dx%d open=%q: line is %d columns: %q",
						size.w, size.h, open, got, line)
				}
			}
		}
	}
}

// The same input has to draw the same frame, or the committed images churn on
// every regeneration.
func TestRender_IsDeterministic(t *testing.T) {
	shot := shotFixture()
	shot.Open = "nash-robin"

	first := Render(shot)
	for i := 0; i < 5; i++ {
		if got := Render(shot); got != first {
			t.Fatalf("run %d drew a different frame", i+2)
		}
	}
}
