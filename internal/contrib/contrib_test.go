package contrib

import (
	"testing"
	"time"
)

func TestParseDate(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "date only", raw: "2026-06-29", want: "2026-06-29"},
		{name: "rfc3339", raw: "2026-06-29T11:22:33Z", want: "2026-06-29"},
		{name: "surrounding space", raw: "  2026-06-29  ", want: "2026-06-29"},
		{name: "invalid", raw: "not-a-date", wantErr: true},
		{name: "empty", raw: "   ", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDate(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseDate(%q) = %v, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDate(%q) returned error: %v", tc.raw, err)
			}
			if got.Format(DateFormat) != tc.want {
				t.Fatalf("ParseDate(%q) = %s, want %s", tc.raw, got.Format(DateFormat), tc.want)
			}
		})
	}
}

func TestParseRange_Valid(t *testing.T) {
	got, err := ParseRange("2026-07-01", "2026-07-29")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if from := got.From.Format(time.RFC3339); from != "2026-07-01T00:00:00Z" {
		t.Fatalf("From = %s, want 2026-07-01T00:00:00Z", from)
	}
	if to := got.To.Format(time.RFC3339); to != "2026-07-29T23:59:59Z" {
		t.Fatalf("To = %s, want 2026-07-29T23:59:59Z", to)
	}
}

func TestParseRange_TrimsInput(t *testing.T) {
	got, err := ParseRange("  2026-07-01 ", " 2026-07-29 ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if from := got.From.Format(time.RFC3339); from != "2026-07-01T00:00:00Z" {
		t.Fatalf("From = %s, want 2026-07-01T00:00:00Z", from)
	}
}

func TestParseRange_ToWithoutFrom(t *testing.T) {
	if _, err := ParseRange("", "2026-07-29"); err == nil {
		t.Fatal("expected error when to is provided without from")
	}
}

func TestParseRange_EmptyDefaultsToTrailing30Days(t *testing.T) {
	got, err := ParseRange("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	now := time.Now().UTC()
	if want := now.AddDate(0, 0, -30).Format(DateFormat); got.From.Format(DateFormat) != want {
		t.Fatalf("From = %s, want %s", got.From.Format(DateFormat), want)
	}
	if got.To.Format(DateFormat) != now.Format(DateFormat) {
		t.Fatalf("To = %s, want today (%s)", got.To.Format(DateFormat), now.Format(DateFormat))
	}
	if h, m, s := got.From.Clock(); h != 0 || m != 0 || s != 0 {
		t.Fatalf("From clock = %02d:%02d:%02d, want start of day", h, m, s)
	}
	if h, m, s := got.To.Clock(); h != 23 || m != 59 || s != 59 {
		t.Fatalf("To clock = %02d:%02d:%02d, want end of day", h, m, s)
	}
}

func TestParseRange_FromOnlyDefaultsToToday(t *testing.T) {
	got, err := ParseRange("2026-07-01", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := time.Now().UTC().Format(DateFormat); got.To.Format(DateFormat) != want {
		t.Fatalf("To = %s, want %s", got.To.Format(DateFormat), want)
	}
}

func TestParseRange_InvalidDates(t *testing.T) {
	if _, err := ParseRange("07/01/2026", ""); err == nil {
		t.Fatal("expected error for non ISO from date")
	}
	if _, err := ParseRange("2026-07-01", "nope"); err == nil {
		t.Fatal("expected error for non ISO to date")
	}
}

func TestReportDisplayName(t *testing.T) {
	if got := (Report{Login: "octocat", Name: "Mona"}).DisplayName(); got != "Mona" {
		t.Fatalf("expected name, got %q", got)
	}
	if got := (Report{Login: "octocat"}).DisplayName(); got != "octocat" {
		t.Fatalf("expected login fallback, got %q", got)
	}
}

// Partial dates.

// Typing a whole month is how people ask for a month, and typing the day it
// ends in is arithmetic they should not have to do. A partial date names a
// period, and a period has two ends: From takes the first day, To the last.
func TestParseRange_ExpandsPartialDates(t *testing.T) {
	tests := []struct {
		name     string
		from, to string
		wantFrom string
		wantTo   string
	}{
		{"month to month", "2026-05", "2026-07", "2026-05-01", "2026-07-31"},
		{"month with slashes", "2026/05", "2026/07", "2026-05-01", "2026-07-31"},
		{"february in a leap year", "2024-02", "2024-02", "2024-02-01", "2024-02-29"},
		{"february otherwise", "2026-02", "2026-02", "2026-02-01", "2026-02-28"},
		{"a whole year", "2025", "2025", "2025-01-01", "2025-12-31"},
		{"month to a day", "2026-05", "2026-06-15", "2026-05-01", "2026-06-15"},
		{"day to a month", "2026-05-04", "2026-06", "2026-05-04", "2026-06-30"},
		{"full dates are unchanged", "2026-05-04", "2026-06-15", "2026-05-04", "2026-06-15"},
		{"full dates with slashes", "2026/05/04", "2026/06/15", "2026-05-04", "2026-06-15"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRange(tt.from, tt.to)
			if err != nil {
				t.Fatalf("ParseRange(%q, %q) error: %v", tt.from, tt.to, err)
			}
			if gotFrom := got.From.Format(DateFormat); gotFrom != tt.wantFrom {
				t.Errorf("from = %s, want %s", gotFrom, tt.wantFrom)
			}
			if gotTo := got.To.Format(DateFormat); gotTo != tt.wantTo {
				t.Errorf("to = %s, want %s", gotTo, tt.wantTo)
			}
		})
	}
}

// The window stays inclusive however it was spelled: the last day of a partial
// range runs to the end of that day, not to midnight at its start.
func TestParseRange_PartialToCoversItsWholeLastDay(t *testing.T) {
	got, err := ParseRange("2026-05", "2026-05")
	if err != nil {
		t.Fatalf("ParseRange: %v", err)
	}
	if h, m, s := got.To.Clock(); h != 23 || m != 59 || s != 59 {
		t.Errorf("to = %v, want it to run to the end of the day", got.To)
	}
	if h, m, s := got.From.Clock(); h != 0 || m != 0 || s != 0 {
		t.Errorf("from = %v, want it to start at the beginning of the day", got.From)
	}
}

func TestParseRange_RejectsNonsense(t *testing.T) {
	for _, raw := range []string{"2026-13", "2026-00", "20265", "2026-05-32", "may", "2026-", "-05"} {
		if _, err := ParseRange(raw, ""); err == nil {
			t.Errorf("ParseRange(%q) was accepted, want an error", raw)
		}
	}
}

// Trailing windows.

func TestTrailingDays(t *testing.T) {
	got := TrailingDays(30)
	if days := got.To.Sub(got.From).Hours() / 24; days < 30 || days > 31 {
		t.Fatalf("window spans %.1f days, want 30", days)
	}
	if h, m, s := got.From.Clock(); h != 0 || m != 0 || s != 0 {
		t.Errorf("from = %v, want the start of a day", got.From)
	}
}

// Three months back from the 31st has no 31st to land on in every month, so the
// window has to be defined in months rather than in a count of days.
func TestTrailingMonths(t *testing.T) {
	got := TrailingMonths(3)
	if months := got.To.Sub(got.From).Hours() / 24 / 30; months < 2.8 || months > 3.4 {
		t.Fatalf("window spans about %.1f months, want 3", months)
	}
	if got.To.Before(got.From) {
		t.Fatalf("window runs backwards: %v to %v", got.From, got.To)
	}
}
