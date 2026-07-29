package ui

import (
	"reflect"
	"testing"

	"gh-contribs/internal/contrib"
)

func testPeople() []contrib.Person {
	return []contrib.Person{
		{Login: "nash-robin", Name: "Nash, Robin [USA]", Orgs: []string{"originops"}, Active: true},
		{Login: "avery-lynn", Name: "Avery, Lynn [USA]", Orgs: []string{"originops"}, Active: true},
		{Login: "Bianchi-Priya", Name: "Bianchi, Priya [USA]", Orgs: []string{"originops"}},
	}
}

func TestEntryFor(t *testing.T) {
	tests := []struct {
		name   string
		person contrib.Person
		want   string
	}{
		{
			name:   "login and name",
			person: contrib.Person{Login: "nash-robin", Name: "Nash, Robin [USA]", Active: true},
			want:   "nash-robin — Nash, Robin [USA]",
		},
		{
			name:   "login only",
			person: contrib.Person{Login: "octocat", Active: true},
			want:   "octocat",
		},
		{
			name:   "inactive is marked",
			person: contrib.Person{Login: "Bianchi-Priya", Name: "Bianchi, Priya [USA]"},
			want:   "Bianchi-Priya — Bianchi, Priya [USA] (suspended)",
		},
		{
			name:   "inactive without a name",
			person: contrib.Person{Login: "ghost"},
			want:   "ghost (suspended)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := entryFor(tc.person); got != tc.want {
				t.Fatalf("entryFor = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoginFromEntry(t *testing.T) {
	tests := []struct{ entry, want string }{
		{entry: "nash-robin — Nash, Robin [USA]", want: "nash-robin"},
		{entry: "Bianchi-Priya — Bianchi, Priya [USA] (suspended)", want: "Bianchi-Priya"},
		{entry: "octocat", want: "octocat"},
		{entry: "ghost (suspended)", want: "ghost"},
		{entry: "  octocat  ", want: "octocat"},
		{entry: "", want: ""},
	}
	for _, tc := range tests {
		if got := loginFromEntry(tc.entry); got != tc.want {
			t.Errorf("loginFromEntry(%q) = %q, want %q", tc.entry, got, tc.want)
		}
	}
}

// entryFor and loginFromEntry must be exact inverses, or selecting a suggestion
// would put an unusable value in the query field.
func TestEntryRoundTrip(t *testing.T) {
	for _, person := range testPeople() {
		if got := loginFromEntry(entryFor(person)); got != person.Login {
			t.Errorf("round trip for %+v gave %q", person, got)
		}
	}
}

func TestSuggestionStore_EmptyBeforePopulated(t *testing.T) {
	var store suggestionStore
	if got := store.entries("nash"); got != nil {
		t.Fatalf("expected no entries before the directory is set, got %v", got)
	}
	if got := store.len(); got != 0 {
		t.Fatalf("len = %d, want 0", got)
	}
}

func TestSuggestionStore_EntriesAreRenderedAndRanked(t *testing.T) {
	var store suggestionStore
	store.set(contrib.NewDirectory(testPeople()))

	if got := store.len(); got != 3 {
		t.Fatalf("len = %d, want 3", got)
	}
	got := store.entries("robin")
	want := []string{"nash-robin — Nash, Robin [USA]"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries(\"rocky\") = %v, want %v", got, want)
	}
}

func TestSuggestionStore_EmptyQueryListsDirectory(t *testing.T) {
	var store suggestionStore
	store.set(contrib.NewDirectory(testPeople()))

	got := store.entries("")
	if len(got) != 3 {
		t.Fatalf("entries = %v, want all three people", got)
	}
	// Active people sort ahead of the suspended account.
	if got[len(got)-1] != "Bianchi-Priya — Bianchi, Priya [USA] (suspended)" {
		t.Fatalf("expected the suspended account last, got %v", got)
	}
}

func TestSuggestionStore_MatchesOnDisplayName(t *testing.T) {
	var store suggestionStore
	store.set(contrib.NewDirectory(testPeople()))

	// The whole point of carrying names: "Lynn" does not appear in any login.
	got := store.entries("lynn")
	if len(got) != 1 || got[0] != "avery-lynn — Avery, Lynn [USA]" {
		t.Fatalf("entries(\"leann\") = %v, want the Adams entry", got)
	}
}

func TestSuggestionStore_RespectsMaxSuggestions(t *testing.T) {
	people := make([]contrib.Person, 0, maxSuggestions+10)
	for i := 0; i < maxSuggestions+10; i++ {
		people = append(people, contrib.Person{
			Login:  string(rune('a'+i%26)) + "-user-" + string(rune('a'+i/26)) + string(rune('a'+i%26)),
			Active: true,
		})
	}
	var store suggestionStore
	store.set(contrib.NewDirectory(people))

	if got := len(store.entries("")); got != maxSuggestions {
		t.Fatalf("entries = %d, want the %d cap", got, maxSuggestions)
	}
}
