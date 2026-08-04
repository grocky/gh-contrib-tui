package reportcache

import (
	"os"
	"testing"
	"time"

	"gh-contribs/internal/contrib"
)

var (
	july = contrib.Range{
		From: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 7, 31, 23, 59, 59, 0, time.UTC),
	}
	august = contrib.Range{
		From: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC),
	}
)

func report(login string, commits int) *contrib.Report {
	return &contrib.Report{
		Login:      login,
		Name:       "Mona",
		Collection: contrib.Collection{TotalCommits: commits},
	}
}

// clock is a hand-wound clock, so freshness can be tested without sleeping.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func newCache(t *testing.T, c *clock, opts ...Option) *Cache {
	t.Helper()
	opts = append([]Option{WithBaseDir(t.TempDir()), withClock(c.Now)}, opts...)
	return Open("github.com", opts...)
}

func TestStoreLoad_RoundTrips(t *testing.T) {
	c := &clock{now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)}
	cache := newCache(t, c)

	if err := cache.Store("octocat", july, report("octocat", 12)); err != nil {
		t.Fatalf("store: %v", err)
	}

	got, ok := cache.Load("octocat", july)
	if !ok {
		t.Fatal("load missed the report just stored")
	}
	if !got.Fresh {
		t.Error("a report stored a moment ago is not fresh")
	}
	if got.Report.Collection.TotalCommits != 12 || got.Report.Name != "Mona" {
		t.Errorf("report = %+v, want it back intact", got.Report)
	}
	if !got.FetchedAt.Equal(c.now) {
		t.Errorf("fetchedAt = %v, want %v", got.FetchedAt, c.now)
	}
}

// The whole point of the cache is that a stale report still paints instantly
// while a fresh one is on its way, so staleness is reported rather than
// withheld.
func TestLoad_ServesStaleReportsFlagged(t *testing.T) {
	c := &clock{now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)}
	cache := newCache(t, c, WithTTL(time.Hour))

	if err := cache.Store("octocat", july, report("octocat", 12)); err != nil {
		t.Fatalf("store: %v", err)
	}
	c.now = c.now.Add(90 * time.Minute)

	got, ok := cache.Load("octocat", july)
	if !ok {
		t.Fatal("a stale report was dropped, want it served for the first paint")
	}
	if got.Fresh {
		t.Error("report older than the TTL reported as fresh")
	}
	if got.Report.Collection.TotalCommits != 12 {
		t.Errorf("stale report = %+v, want it usable", got.Report)
	}
}

// A report is only about the window it was fetched for. Serving July's totals
// for an August query would be wrong in a way nobody could see.
func TestLoad_IsKeyedByWindow(t *testing.T) {
	c := &clock{now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)}
	cache := newCache(t, c)

	if err := cache.Store("octocat", july, report("octocat", 12)); err != nil {
		t.Fatalf("store: %v", err)
	}
	if _, ok := cache.Load("octocat", august); ok {
		t.Fatal("a report for July was served for August")
	}
}

func TestLoad_IsKeyedByLoginCaseInsensitively(t *testing.T) {
	c := &clock{now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)}
	cache := newCache(t, c)

	if err := cache.Store("OctoCat", july, report("OctoCat", 12)); err != nil {
		t.Fatalf("store: %v", err)
	}
	if _, ok := cache.Load("octocat", july); !ok {
		t.Fatal("a login stored in one case missed in another, want GitHub's own matching")
	}
}

func TestLoad_MissesOnAnEmptyCache(t *testing.T) {
	c := &clock{now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)}
	if _, ok := newCache(t, c).Load("octocat", july); ok {
		t.Fatal("empty cache reported a hit")
	}
}

// Reports accumulate one per person per window, so the file needs a ceiling or
// a year of daily use leaves a cache nobody prunes.
func TestStore_BoundsTheFile(t *testing.T) {
	c := &clock{now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)}
	cache := newCache(t, c, WithMaxEntries(3))

	logins := []string{"a", "b", "c", "d", "e"}
	for _, login := range logins {
		if err := cache.Store(login, july, report(login, 1)); err != nil {
			t.Fatalf("store %s: %v", login, err)
		}
		c.now = c.now.Add(time.Minute)
	}

	var kept []string
	for _, login := range logins {
		if _, ok := cache.Load(login, july); ok {
			kept = append(kept, login)
		}
	}
	if len(kept) != 3 {
		t.Fatalf("kept %v, want the newest 3", kept)
	}
	if kept[0] != "c" || kept[2] != "e" {
		t.Fatalf("kept %v, want the oldest evicted first", kept)
	}
}

// Every store rewrites the file, so a second report must not lose the first.
func TestStore_KeepsEarlierEntries(t *testing.T) {
	c := &clock{now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)}
	cache := newCache(t, c)

	if err := cache.Store("octocat", july, report("octocat", 12)); err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := cache.Store("nash-robin", july, report("nash-robin", 5)); err != nil {
		t.Fatalf("store: %v", err)
	}

	for _, login := range []string{"octocat", "nash-robin"} {
		if _, ok := cache.Load(login, july); !ok {
			t.Errorf("%s was lost by a later store", login)
		}
	}
}

// The cache survives the process, which is the only reason it is on disk.
func TestCache_SurvivesAReopen(t *testing.T) {
	c := &clock{now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)}
	dir := t.TempDir()

	first := Open("github.com", WithBaseDir(dir), withClock(c.Now))
	if err := first.Store("octocat", july, report("octocat", 12)); err != nil {
		t.Fatalf("store: %v", err)
	}

	second := Open("github.com", WithBaseDir(dir), withClock(c.Now))
	got, ok := second.Load("octocat", july)
	if !ok {
		t.Fatal("a reopened cache lost its reports")
	}
	if got.Report.Collection.TotalCommits != 12 {
		t.Errorf("report = %+v, want it back intact", got.Report)
	}
}

func TestOpen_SeparatesHosts(t *testing.T) {
	c := &clock{now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)}
	dir := t.TempDir()

	dotcom := Open("github.com", WithBaseDir(dir), withClock(c.Now))
	ghes := Open("github.example.com", WithBaseDir(dir), withClock(c.Now))
	if err := dotcom.Store("octocat", july, report("octocat", 12)); err != nil {
		t.Fatalf("store: %v", err)
	}
	if _, ok := ghes.Load("octocat", july); ok {
		t.Fatal("one host served another host's report")
	}
}

// A corrupt cache is a cold start, not a crash: it holds nothing that cannot be
// fetched again.
func TestLoad_CorruptFileMissesQuietly(t *testing.T) {
	c := &clock{now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)}
	dir := t.TempDir()
	cache := Open("github.com", WithBaseDir(dir), withClock(c.Now))

	if err := cache.Store("octocat", july, report("octocat", 12)); err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := os.WriteFile(cache.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupting the cache: %v", err)
	}

	if _, ok := cache.Load("octocat", july); ok {
		t.Fatal("a corrupt cache reported a hit")
	}
	if err := cache.Store("octocat", july, report("octocat", 12)); err != nil {
		t.Fatalf("store over a corrupt cache: %v", err)
	}
	if _, ok := cache.Load("octocat", july); !ok {
		t.Fatal("cache did not recover after being overwritten")
	}
}

func TestStore_IsReadableOnlyByItsOwner(t *testing.T) {
	c := &clock{now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)}
	cache := newCache(t, c)
	if err := cache.Store("octocat", july, report("octocat", 12)); err != nil {
		t.Fatalf("store: %v", err)
	}

	info, err := os.Stat(cache.Path())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != filePerm {
		t.Fatalf("permissions = %o, want %o", perm, filePerm)
	}
}

// A nil cache is how the app runs when the cache location is unusable, so every
// method has to tolerate it.
func TestNilCache_IsUsable(t *testing.T) {
	var cache *Cache
	if _, ok := cache.Load("octocat", july); ok {
		t.Error("nil cache reported a hit")
	}
	if err := cache.Store("octocat", july, report("octocat", 12)); err == nil {
		t.Error("nil cache stored without complaint, want an error to log")
	}
}
