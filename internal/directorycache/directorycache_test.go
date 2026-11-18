package directorycache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"gh-contrib/internal/contrib"
)

func samplePeople() []contrib.Person {
	return []contrib.Person{
		{Login: "nash-robin", Name: "Nash, Robin [USA]", Orgs: []string{"originops"}, Active: true},
		{Login: "Bianchi-Priya", Name: "Bianchi, Priya [USA]", Orgs: []string{"originops"}},
	}
}

func TestStoreLoad_RoundTrip(t *testing.T) {
	c := Open("github.example.com", WithBaseDir(t.TempDir()))

	if err := c.Store(samplePeople()); err != nil {
		t.Fatalf("Store returned error: %v", err)
	}
	got, ok := c.Load()
	if !ok {
		t.Fatal("expected a cache hit")
	}
	if !reflect.DeepEqual(got, samplePeople()) {
		t.Fatalf("people = %+v, want %+v", got, samplePeople())
	}
}

func TestLoad_MissWhenAbsent(t *testing.T) {
	c := Open("github.example.com", WithBaseDir(t.TempDir()))
	if _, ok := c.Load(); ok {
		t.Fatal("expected a miss for an empty cache dir")
	}
}

func TestLoad_MissWhenExpired(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

	writer := Open("github.example.com", WithBaseDir(dir), withClock(func() time.Time { return now }))
	if err := writer.Store(samplePeople()); err != nil {
		t.Fatalf("Store returned error: %v", err)
	}

	fresh := Open("github.example.com", WithBaseDir(dir), WithTTL(time.Hour),
		withClock(func() time.Time { return now.Add(59 * time.Minute) }))
	if _, ok := fresh.Load(); !ok {
		t.Fatal("expected a hit just inside the TTL")
	}

	stale := Open("github.example.com", WithBaseDir(dir), WithTTL(time.Hour),
		withClock(func() time.Time { return now.Add(61 * time.Minute) }))
	if _, ok := stale.Load(); ok {
		t.Fatal("expected a miss past the TTL")
	}
}

func TestLoad_MissOnCorruptPayload(t *testing.T) {
	dir := t.TempDir()
	c := Open("github.example.com", WithBaseDir(dir))
	if err := os.WriteFile(c.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("writing corrupt cache: %v", err)
	}

	if _, ok := c.Load(); ok {
		t.Fatal("expected a corrupt cache to be discarded")
	}
}

func TestLoad_MissOnSchemaMismatch(t *testing.T) {
	dir := t.TempDir()
	c := Open("github.example.com", WithBaseDir(dir))

	raw, err := json.Marshal(map[string]any{
		"schemaVersion": schemaVersion + 1,
		"host":          "github.example.com",
		"fetchedAt":     time.Now(),
		"people":        samplePeople(),
	})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if err := os.WriteFile(c.Path(), raw, 0o600); err != nil {
		t.Fatalf("writing cache: %v", err)
	}

	if _, ok := c.Load(); ok {
		t.Fatal("expected a schema mismatch to be discarded")
	}
}

func TestLoad_MissOnHostMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := Open("github.example.com", WithBaseDir(dir)).Store(samplePeople()); err != nil {
		t.Fatalf("Store returned error: %v", err)
	}

	// Same directory, different host: the file names differ, so this is a miss.
	if _, ok := Open("github.com", WithBaseDir(dir)).Load(); ok {
		t.Fatal("expected hosts not to share a cache file")
	}
}

func TestHostsGetDistinctFiles(t *testing.T) {
	dir := t.TempDir()
	a := Open("github.com", WithBaseDir(dir))
	b := Open("GitHub.Example.com", WithBaseDir(dir))

	if a.Path() == b.Path() {
		t.Fatalf("expected distinct cache paths, both are %s", a.Path())
	}
	if got := filepath.Base(b.Path()); got != "directory-github.example.com.json" {
		t.Fatalf("file name = %s, want the host lowercased", got)
	}
	if got := filepath.Base(Open("weird/host:8080", WithBaseDir(dir)).Path()); got != "directory-weird_host_8080.json" {
		t.Fatalf("file name = %s, want path separators sanitized", got)
	}
}

func TestStore_FileIsNotWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	c := Open("github.example.com", WithBaseDir(t.TempDir()))
	if err := c.Store(samplePeople()); err != nil {
		t.Fatalf("Store returned error: %v", err)
	}

	info, err := os.Stat(c.Path())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != filePerm {
		t.Fatalf("mode = %o, want %o", mode, filePerm)
	}
}

func TestStore_ReplacesPreviousContents(t *testing.T) {
	c := Open("github.example.com", WithBaseDir(t.TempDir()))
	if err := c.Store(samplePeople()); err != nil {
		t.Fatalf("Store returned error: %v", err)
	}
	if err := c.Store([]contrib.Person{{Login: "solo", Active: true}}); err != nil {
		t.Fatalf("second Store returned error: %v", err)
	}

	got, ok := c.Load()
	if !ok {
		t.Fatal("expected a hit")
	}
	if len(got) != 1 || got[0].Login != "solo" {
		t.Fatalf("people = %+v, want only the newest write", got)
	}
}

func TestStore_LeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	c := Open("github.example.com", WithBaseDir(dir))
	if err := c.Store(samplePeople()); err != nil {
		t.Fatalf("Store returned error: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading dir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected only the cache file, got %v", names)
	}
}

func TestStore_EmptyPeopleIsANoOp(t *testing.T) {
	c := Open("github.example.com", WithBaseDir(t.TempDir()))
	if err := c.Store(nil); err != nil {
		t.Fatalf("Store(nil) returned error: %v", err)
	}
	if _, err := os.Stat(c.Path()); !os.IsNotExist(err) {
		t.Fatal("expected no cache file to be written for an empty directory")
	}
}

func TestStore_UnwritableLocationIsNonFatal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatalf("creating read-only dir: %v", err)
	}

	c := Open("github.example.com", WithBaseDir(filepath.Join(locked, "sub")))
	err := c.Store(samplePeople())
	if err == nil {
		t.Fatal("expected Store to report the failure")
	}
	if _, ok := c.Load(); ok {
		t.Fatal("expected Load to miss after a failed Store")
	}
}

func TestDisabledCacheIsUsable(t *testing.T) {
	var c Cache // zero value: no path
	if _, ok := c.Load(); ok {
		t.Fatal("expected a disabled cache to miss")
	}
	if err := c.Store(samplePeople()); err == nil {
		t.Fatal("expected a disabled cache to report why it cannot store")
	}
}

func TestNilCacheIsUsable(t *testing.T) {
	var c *Cache
	if _, ok := c.Load(); ok {
		t.Fatal("expected a nil cache to miss")
	}
	if err := c.Store(samplePeople()); err == nil {
		t.Fatal("expected a nil cache to report an error")
	}
}

func TestOpen_HonorsXDGCacheHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)

	want := filepath.Join(dir, "gh-contrib", fileName("github.com"))
	if got := Open("github.com").Path(); got != want {
		t.Errorf("Open(%q).Path() = %q, want %q", "github.com", got, want)
	}
}
