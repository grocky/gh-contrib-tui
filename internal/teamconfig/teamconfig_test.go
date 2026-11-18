package teamconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gh-contrib/internal/contrib"
)

func person(login, name string) contrib.Person {
	return contrib.Person{Login: login, Name: name, Active: true}
}

// A first launch has to be told apart from a team someone has emptied on
// purpose: one prompts to set the team up, the other is a standing decision.
func TestLoad_UnconfiguredIsNotTheSameAsEmpty(t *testing.T) {
	store := Open("github.com", WithBaseDir(t.TempDir()))

	if _, err := store.Load(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("load before any save = %v, want ErrNotConfigured", err)
	}

	if err := store.Save(Team{}); err != nil {
		t.Fatalf("save empty team: %v", err)
	}
	team, err := store.Load()
	if err != nil {
		t.Fatalf("load after saving an empty team: %v", err)
	}
	if len(team.Members) != 0 {
		t.Fatalf("members = %v, want the empty team back", team.Members)
	}
}

func TestSaveLoad_RoundTrips(t *testing.T) {
	store := Open("github.com", WithBaseDir(t.TempDir()))
	want := Team{Members: []contrib.Person{person("nash-robin", "Nash, Robin"), person("octocat", "Mona")}}

	if err := store.Save(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Members) != 2 || got.Members[0].Login != "nash-robin" || got.Members[1].Name != "Mona" {
		t.Fatalf("round trip = %+v, want %+v", got.Members, want.Members)
	}
}

// The roster is curated, so it stays in the order it was curated in rather than
// being sorted out from under whoever arranged it.
func TestAdd_KeepsOrderAndRejectsDuplicates(t *testing.T) {
	team := Team{}
	team = team.Add(person("zed", "Zed"))
	team = team.Add(person("avery-lynn", "Avery, Lynn"))
	team = team.Add(person("ZED", "Zed Again"))

	if len(team.Members) != 2 {
		t.Fatalf("members = %+v, want the duplicate login rejected whatever its case", team.Members)
	}
	if team.Members[0].Login != "zed" || team.Members[1].Login != "avery-lynn" {
		t.Fatalf("members = %+v, want insertion order kept", team.Members)
	}
}

// A person added from autocomplete carries a display name; the same person
// typed in by hand may not. Re-adding is how someone fills that in.
func TestAdd_FillsInAMissingName(t *testing.T) {
	team := Team{}.Add(contrib.Person{Login: "octocat"})
	team = team.Add(person("octocat", "Mona"))

	if len(team.Members) != 1 {
		t.Fatalf("members = %+v, want one", team.Members)
	}
	if team.Members[0].Name != "Mona" {
		t.Fatalf("name = %q, want the name filled in", team.Members[0].Name)
	}
}

func TestRemove(t *testing.T) {
	team := Team{}.Add(person("a", "A")).Add(person("b", "B")).Add(person("c", "C"))

	team = team.Remove("B")
	if len(team.Members) != 2 {
		t.Fatalf("members = %+v, want b removed whatever its case", team.Members)
	}
	if team.Members[0].Login != "a" || team.Members[1].Login != "c" {
		t.Fatalf("members = %+v, want the rest in order", team.Members)
	}

	if got := team.Remove("nobody"); len(got.Members) != 2 {
		t.Fatalf("removing someone absent = %+v, want no change", got.Members)
	}
}

func TestHas(t *testing.T) {
	team := Team{}.Add(person("octocat", "Mona"))
	if !team.Has("OCTOCAT") {
		t.Error("Has is case sensitive, want it to match logins the way GitHub does")
	}
	if team.Has("someone-else") {
		t.Error("Has reported a member who is not on the team")
	}
}

// Two hosts are two different teams: the same login on github.com and on an
// Enterprise deployment are not the same account.
func TestOpen_SeparatesHosts(t *testing.T) {
	dir := t.TempDir()
	dotcom := Open("github.com", WithBaseDir(dir))
	ghes := Open("github.example.com", WithBaseDir(dir))

	if err := dotcom.Save(Team{Members: []contrib.Person{person("octocat", "Mona")}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := ghes.Load(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("other host's load = %v, want it unconfigured", err)
	}
}

// A half-written or hand-edited file is a bad config, not a first launch:
// silently starting over would throw away a team someone curated.
func TestLoad_CorruptFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	store := Open("github.com", WithBaseDir(dir))
	if err := os.WriteFile(store.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("writing corrupt config: %v", err)
	}

	_, err := store.Load()
	if err == nil || errors.Is(err, ErrNotConfigured) {
		t.Fatalf("load of a corrupt file = %v, want a real error", err)
	}
}

func TestSave_IsReadableOnlyByItsOwner(t *testing.T) {
	store := Open("github.com", WithBaseDir(t.TempDir()))
	if err := store.Save(Team{Members: []contrib.Person{person("octocat", "Mona")}}); err != nil {
		t.Fatalf("save: %v", err)
	}

	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != filePerm {
		t.Fatalf("permissions = %o, want %o", perm, filePerm)
	}
}

// The file is meant to be hand-editable, so it has to be something a person can
// read and edit without a manual.
func TestSave_WritesReadableJSON(t *testing.T) {
	store := Open("github.com", WithBaseDir(t.TempDir()))
	if err := store.Save(Team{Members: []contrib.Person{person("octocat", "Mona")}}); err != nil {
		t.Fatalf("save: %v", err)
	}

	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var pretty map[string]any
	if err := json.Unmarshal(raw, &pretty); err != nil {
		t.Fatalf("stored config is not json: %v", err)
	}
	if !json.Valid(raw) || len(raw) == 0 {
		t.Fatal("stored config is not valid json")
	}
	if got := string(raw); got[len(got)-1] != '\n' {
		t.Error("config does not end in a newline, which every text editor will add")
	}
	if !containsIndent(raw) {
		t.Error("config is not indented, want it readable by hand")
	}
}

func containsIndent(raw []byte) bool {
	for i := 0; i < len(raw)-1; i++ {
		if raw[i] == '\n' && raw[i+1] == ' ' {
			return true
		}
	}
	return false
}

// Saving must not leave a half-written config behind if it fails partway, and
// must not depend on the directory already existing.
func TestSave_CreatesItsDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "deeper")
	store := Open("github.com", WithBaseDir(dir))

	if err := store.Save(Team{Members: []contrib.Person{person("octocat", "Mona")}}); err != nil {
		t.Fatalf("save into a missing directory: %v", err)
	}
	if _, err := store.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
}
