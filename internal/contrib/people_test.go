package contrib

import (
	"reflect"
	"testing"
)

func logins(people []Person) []string {
	out := make([]string, 0, len(people))
	for _, p := range people {
		out = append(out, p.Login)
	}
	return out
}

func TestMergePeople_DeduplicatesByLogin(t *testing.T) {
	got := MergePeople(
		[]Person{{Login: "nash-robin", Name: "Nash, Robin [USA]", Orgs: []string{"originops"}, Active: true}},
		[]Person{{Login: "nash-robin", Orgs: []string{"platform"}}},
	)
	if len(got) != 1 {
		t.Fatalf("expected one merged person, got %+v", got)
	}
	if got[0].Name != "Nash, Robin [USA]" {
		t.Errorf("expected the non-empty name to win, got %q", got[0].Name)
	}
	if !reflect.DeepEqual(got[0].Orgs, []string{"originops", "platform"}) {
		t.Errorf("orgs = %v, want the sorted union", got[0].Orgs)
	}
	if !got[0].Active {
		t.Error("expected Active to win over inactive")
	}
}

func TestMergePeople_ActiveWinsRegardlessOfOrder(t *testing.T) {
	got := MergePeople(
		[]Person{{Login: "someone"}},
		[]Person{{Login: "someone", Active: true}},
	)
	if len(got) != 1 || !got[0].Active {
		t.Fatalf("expected a single active person, got %+v", got)
	}
}

func TestMergePeople_LoginMatchIsCaseInsensitive(t *testing.T) {
	got := MergePeople(
		[]Person{{Login: "Bianchi-Priya", Name: "Bianchi, Priya [USA]"}},
		[]Person{{Login: "bianchi-priya", Active: true}},
	)
	if len(got) != 1 {
		t.Fatalf("expected logins differing only by case to merge, got %+v", got)
	}
	if got[0].Login != "Bianchi-Priya" {
		t.Errorf("expected the first-seen casing to be kept, got %q", got[0].Login)
	}
	if got[0].Name == "" || !got[0].Active {
		t.Errorf("expected fields to be combined, got %+v", got[0])
	}
}

func TestMergePeople_SkipsEmptyLogins(t *testing.T) {
	got := MergePeople([]Person{{Login: ""}, {Login: "  "}, {Login: "real"}})
	if !reflect.DeepEqual(logins(got), []string{"real"}) {
		t.Fatalf("logins = %v, want only the real login", logins(got))
	}
}

func TestMergePeople_SortsActiveFirstThenLogin(t *testing.T) {
	got := MergePeople([]Person{
		{Login: "zoe", Active: true},
		{Login: "adam"},
		{Login: "beth", Active: true},
	})
	if !reflect.DeepEqual(logins(got), []string{"beth", "zoe", "adam"}) {
		t.Fatalf("logins = %v, want active first then alphabetical", logins(got))
	}
}

// directoryFixture mirrors the shape of the real corporate directory: logins
// are surname-first, display names are "Surname, Given [USA]".
func directoryFixture() *Directory {
	return NewDirectory([]Person{
		{Login: "nash-robin", Name: "Nash, Robin [USA]", Active: true},
		{Login: "nash-tyler", Name: "Nash, Tyler [USA]", Active: true},
		{Login: "avery-lynn", Name: "Avery, Lynn [USA]", Active: true},
		{Login: "robbins-dara", Name: "Robbins, Dara [USA]", Active: true},
		{Login: "Bianchi-Priya", Name: "Bianchi, Priya [USA]"},
	})
}

func TestDirectoryMatch_Ranking(t *testing.T) {
	d := directoryFixture()

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "exact login ranks first",
			query: "nash-robin",
			want:  []string{"nash-robin"},
		},
		{
			name:  "login prefix beats name match",
			query: "nash",
			want:  []string{"nash-robin", "nash-tyler"},
		},
		{
			name:  "given name matches via name token prefix",
			query: "robin",
			want:  []string{"nash-robin"},
		},
		{
			// "ob" starts neither login nor any name token, so both people
			// land in the lower substring tier and order alphabetically.
			name:  "mid-word query falls back to substring matching",
			query: "ob",
			want:  []string{"nash-robin", "robbins-dara"},
		},
		{
			// robbins-dara matches by login prefix (tier 1); nash-robin only
			// by the name token "Robin" (tier 2), so the tiers decide order.
			name:  "login prefix outranks name token prefix",
			query: "rob",
			want:  []string{"robbins-dara", "nash-robin"},
		},
		{
			name:  "surname given order",
			query: "nash robin",
			want:  []string{"nash-robin"},
		},
		{
			name:  "given surname order",
			query: "robin nash",
			want:  []string{"nash-robin"},
		},
		{
			name:  "case insensitive",
			query: "ROBIN NASH",
			want:  []string{"nash-robin"},
		},
		{
			name:  "no match",
			query: "nobody",
			want:  []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := logins(d.Match(tc.query, 10))
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Match(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

func TestDirectoryMatch_HyphenatedQueryConvergesWithSpaced(t *testing.T) {
	d := directoryFixture()
	for _, q := range []string{"nash-robin", "nash robin", "robin nash", "Nash, Robin"} {
		got := logins(d.Match(q, 10))
		if len(got) == 0 || got[0] != "nash-robin" {
			t.Fatalf("Match(%q) = %v, want nash-robin first", q, got)
		}
	}
}

func TestDirectoryMatch_EmptyQueryReturnsHeadOfList(t *testing.T) {
	d := directoryFixture()

	got := logins(d.Match("   ", 3))
	want := []string{"avery-lynn", "nash-robin", "nash-tyler"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Match(\"\") = %v, want %v (active, alphabetical)", got, want)
	}
}

func TestDirectoryMatch_InactiveSortsLastAtEqualRank(t *testing.T) {
	d := NewDirectory([]Person{
		{Login: "smith-zoe", Name: "Smith, Zoe", Active: true},
		{Login: "smith-adam", Name: "Smith, Adam"}, // inactive but alphabetically first
	})

	got := logins(d.Match("smith", 10))
	if !reflect.DeepEqual(got, []string{"smith-zoe", "smith-adam"}) {
		t.Fatalf("Match = %v, want the active person first", got)
	}
}

func TestDirectoryMatch_RespectsLimit(t *testing.T) {
	d := directoryFixture()
	if got := d.Match("", 2); len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if got := d.Match("nash", 1); len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if got := d.Match("nash", 0); got != nil {
		t.Fatalf("expected nil for a zero limit, got %v", got)
	}
}

func TestDirectory_EmptyAndNil(t *testing.T) {
	if got := NewDirectory(nil).Match("anything", 10); len(got) != 0 {
		t.Fatalf("expected no matches from an empty directory, got %v", got)
	}
	if got := NewDirectory(nil).Len(); got != 0 {
		t.Fatalf("Len = %d, want 0", got)
	}
	var d *Directory
	if got := d.Match("x", 5); got != nil {
		t.Fatalf("nil directory must be usable, got %v", got)
	}
	if got := d.Len(); got != 0 {
		t.Fatalf("nil directory Len = %d, want 0", got)
	}
}

func TestDirectory_LenCountsMergedPeople(t *testing.T) {
	d := NewDirectory([]Person{
		{Login: "a"}, {Login: "A"}, {Login: "b"},
	})
	if got := d.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2 after deduplication", got)
	}
}

func TestDirectoryMatch_DoesNotAliasInternalState(t *testing.T) {
	d := directoryFixture()
	first := d.Match("", 2)
	first[0] = Person{Login: "mutated"}

	if got := d.Match("", 2); got[0].Login == "mutated" {
		t.Fatal("Match returned a slice aliasing the directory's people")
	}
}

func TestDirectoryPeople_ReturnsMergedSet(t *testing.T) {
	d := directoryFixture()
	if got := len(d.People()); got != 5 {
		t.Fatalf("People() returned %d entries, want 5", got)
	}
}
