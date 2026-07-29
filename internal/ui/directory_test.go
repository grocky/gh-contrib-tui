package ui

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/rivo/tview"

	"gh-contribs/internal/contrib"
)

// countAutocompleteCalls replaces the field's autocomplete callback with a
// counter and returns a pointer to it. tview invokes the callback synchronously
// from InputField.Autocomplete, so a non-zero count proves the dropdown was
// refreshed.
func countAutocompleteCalls(field *tview.InputField) *int {
	calls := 0
	field.SetAutocompleteFunc(func(string) []string {
		calls++
		return nil
	})
	calls = 0 // SetAutocompleteFunc itself triggers one call.
	return &calls
}

// TestApplyPeople_RefreshesDropdown is the regression test for the original
// defect: suggestions arrived from the background goroutine but the dropdown
// was never re-triggered, so it stayed empty until the user edited the field.
func TestApplyPeople_RefreshesDropdown(t *testing.T) {
	app := New(WithService(&fakeService{}))
	calls := countAutocompleteCalls(app.userField)

	app.applyPeople(testPeople(), "github")

	if *calls == 0 {
		t.Fatal("applyPeople must re-trigger the autocomplete dropdown")
	}
}

func TestApplyPeople_PopulatesDirectory(t *testing.T) {
	app := New(WithService(&fakeService{}))

	app.applyPeople(testPeople(), "github")

	if got := app.suggest.len(); got != 3 {
		t.Fatalf("directory size = %d, want 3", got)
	}
	if got := app.suggest.entries("lynn"); len(got) != 1 {
		t.Fatalf("expected the new directory to be searchable, got %v", got)
	}
	if log := app.logView.GetText(true); !strings.Contains(log, "3 user suggestions from github") {
		t.Fatalf("log = %q", log)
	}
}

func TestApplyPeople_WarnsWhenOnlyViewerFound(t *testing.T) {
	app := New(WithService(&fakeService{}))

	app.applyPeople([]contrib.Person{{Login: "nash-robin", Active: true}}, "github")

	log := app.logView.GetText(true)
	if !strings.Contains(log, "read:org") {
		t.Fatalf("expected an actionable scope hint, got: %q", log)
	}
}

func TestApplyPeople_NoScopeWarningFromCache(t *testing.T) {
	app := New(WithService(&fakeService{}))

	app.applyPeople([]contrib.Person{{Login: "nash-robin", Active: true}}, "cache")

	if log := app.logView.GetText(true); strings.Contains(log, "read:org") {
		t.Fatalf("a cached single entry should not trigger the scope hint, got: %q", log)
	}
}

func TestAcceptSuggestion_InsertsBareLogin(t *testing.T) {
	app := New(WithService(&fakeService{}))

	if !app.acceptSuggestion("nash-robin — Nash, Robin [USA]", 0, tview.AutocompletedEnter) {
		t.Fatal("expected the selection to be handled")
	}
	if got := app.userField.GetText(); got != "nash-robin" {
		t.Fatalf("user field = %q, want the bare login", got)
	}
}

func TestAcceptSuggestion_StripsSuspendedMarker(t *testing.T) {
	app := New(WithService(&fakeService{}))

	app.acceptSuggestion("Bianchi-Priya — Bianchi, Priya [USA] (suspended)", 0, tview.AutocompletedClick)

	if got := app.userField.GetText(); got != "Bianchi-Priya" {
		t.Fatalf("user field = %q, want the bare login", got)
	}
}

func TestAcceptSuggestion_IgnoresNavigation(t *testing.T) {
	app := New(WithService(&fakeService{}))
	app.userField.SetText("original")

	if app.acceptSuggestion("nash-robin — Nash, Robin", 0, tview.AutocompletedNavigate) {
		t.Fatal("navigating the list must not accept the entry")
	}
	if got := app.userField.GetText(); got != "original" {
		t.Fatalf("user field = %q, want it untouched while navigating", got)
	}
}

func TestRefreshDirectory_LoadsAndCaches(t *testing.T) {
	svc := &fakeService{people: testPeople()}
	cache := &fakeCache{}
	app := New(WithService(svc), WithDirectoryCache(cache))

	app.refreshDirectory(false)

	if svc.discovers != 1 {
		t.Fatalf("discovers = %d, want 1", svc.discovers)
	}
	if app.suggest.len() != 3 {
		t.Fatalf("directory size = %d, want 3", app.suggest.len())
	}
	if !reflect.DeepEqual(cache.stored, testPeople()) {
		t.Fatalf("cache stored %+v, want the discovered people", cache.stored)
	}
}

func TestRefreshDirectory_ForcedReportsStatus(t *testing.T) {
	app := New(WithService(&fakeService{people: testPeople()}))

	app.refreshDirectory(true)

	if got := app.status.GetText(true); !strings.Contains(got, "3 users loaded") {
		t.Fatalf("status = %q", got)
	}
}

func TestRefreshDirectory_ForcedReportsFailure(t *testing.T) {
	app := New(WithService(&fakeService{discoverErr: errors.New("boom")}))

	app.refreshDirectory(true)

	if got := app.status.GetText(true); !strings.Contains(got, "boom") {
		t.Fatalf("status = %q, want the failure surfaced", got)
	}
	if app.suggest.len() != 0 {
		t.Fatal("a failed refresh must not install an empty directory")
	}
}

func TestRefreshDirectory_QuietFailureOnlyLogs(t *testing.T) {
	app := New(WithService(&fakeService{discoverErr: errors.New("boom")}))

	app.refreshDirectory(false)

	if got := app.status.GetText(true); strings.Contains(got, "boom") {
		t.Fatalf("a background failure should not take over the status line, got %q", got)
	}
	if log := app.logView.GetText(true); !strings.Contains(log, "boom") {
		t.Fatalf("expected the failure in the log, got %q", log)
	}
}

func TestRefreshDirectory_CacheStoreFailureIsNonFatal(t *testing.T) {
	cache := &fakeCache{storeErr: errors.New("disk full")}
	app := New(WithService(&fakeService{people: testPeople()}), WithDirectoryCache(cache))

	app.refreshDirectory(false)

	if app.suggest.len() != 3 {
		t.Fatal("a cache write failure must not lose the loaded directory")
	}
	if log := app.logView.GetText(true); !strings.Contains(log, "disk full") {
		t.Fatalf("expected the cache failure logged, got %q", log)
	}
}

// The cache must be applied before the network refresh, so suggestions are
// usable immediately on launch. A failing refresh leaves the cached set intact.
func TestLoadSuggestions_ServesCacheWhenRefreshFails(t *testing.T) {
	cached := []contrib.Person{{Login: "cached-user", Name: "Cached, User", Active: true}}
	cache := &fakeCache{people: cached}
	svc := &fakeService{discoverErr: errors.New("offline")}
	app := New(WithService(svc), WithDirectoryCache(cache))

	app.loadSuggestions()

	if cache.loads != 1 {
		t.Fatalf("cache loads = %d, want 1", cache.loads)
	}
	if svc.discovers != 1 {
		t.Fatalf("discovers = %d, want 1 (a cache hit must not skip the refresh)", svc.discovers)
	}
	if got := app.suggest.entries("cached"); len(got) != 1 {
		t.Fatalf("expected the cached directory to survive a failed refresh, got %v", got)
	}
}

func TestLoadSuggestions_RefreshReplacesCachedSet(t *testing.T) {
	cache := &fakeCache{people: []contrib.Person{{Login: "cached-user", Active: true}}}
	svc := &fakeService{people: testPeople()}
	app := New(WithService(svc), WithDirectoryCache(cache))

	app.loadSuggestions()

	if got := app.suggest.len(); got != 3 {
		t.Fatalf("directory size = %d, want the refreshed set of 3", got)
	}
	if got := app.suggest.entries("cached"); len(got) != 0 {
		t.Fatalf("expected the stale cached entry to be replaced, got %v", got)
	}
	if !reflect.DeepEqual(cache.stored, testPeople()) {
		t.Fatalf("cache stored %+v, want the refreshed people", cache.stored)
	}
}

func TestLoadSuggestions_ColdCacheStillRefreshes(t *testing.T) {
	cache := &fakeCache{}
	svc := &fakeService{people: testPeople()}
	app := New(WithService(svc), WithDirectoryCache(cache))

	app.loadSuggestions()

	if app.suggest.len() != 3 {
		t.Fatalf("directory size = %d, want 3", app.suggest.len())
	}
}

func TestLoadSuggestions_WorksWithoutACache(t *testing.T) {
	app := New(WithService(&fakeService{people: testPeople()}))

	app.loadSuggestions()

	if app.suggest.len() != 3 {
		t.Fatalf("directory size = %d, want 3", app.suggest.len())
	}
}
