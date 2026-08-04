// Package reportcache persists contribution reports between runs so a team
// dashboard paints from disk on launch instead of after a round trip per
// person. Entries carry the time they were fetched: a stale report is still
// worth showing while a fresh one is on its way, so staleness is reported to
// the caller rather than hidden by dropping the entry.
package reportcache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gh-contribs/internal/contrib"
)

const (
	// schemaVersion is bumped whenever the on-disk payload shape changes; a
	// mismatch discards the cache rather than attempting a migration.
	schemaVersion = 1

	// DefaultTTL is how long a report is considered current. The window
	// normally ends today, so a report goes out of date as its subject works.
	DefaultTTL = time.Hour

	// DefaultMaxEntries bounds the file. One entry per person per window means
	// a browsed range leaves an entry behind, and nothing else prunes them.
	DefaultMaxEntries = 200

	dirPerm  = 0o700
	filePerm = 0o600
)

// Entry is a cached report and what is known about its age.
type Entry struct {
	Report    *contrib.Report
	FetchedAt time.Time

	// Fresh reports whether the entry is within the TTL. A stale entry is still
	// returned; it is the caller's business whether to paint it while
	// refetching.
	Fresh bool
}

// record is the serialized form of one entry.
type record struct {
	Key       string          `json:"key"`
	FetchedAt time.Time       `json:"fetchedAt"`
	Report    *contrib.Report `json:"report"`
}

// payload is the serialized file.
type payload struct {
	SchemaVersion int      `json:"schemaVersion"`
	Host          string   `json:"host"`
	Records       []record `json:"records"`
}

// Cache reads and writes one host's reports. Like the directory cache it is
// always usable: an unusable location simply misses on Load and reports the
// reason from Store.
type Cache struct {
	host       string
	path       string
	ttl        time.Duration
	maxEntries int
	now        func() time.Time
	disabled   error
}

// Option customizes a Cache.
type Option func(*Cache)

// WithBaseDir overrides the cache directory, primarily for tests.
func WithBaseDir(dir string) Option {
	return func(c *Cache) {
		c.disabled = nil
		c.path = filepath.Join(dir, fileName(c.host))
	}
}

// WithTTL overrides how long a report stays fresh.
func WithTTL(ttl time.Duration) Option {
	return func(c *Cache) { c.ttl = ttl }
}

// WithMaxEntries overrides how many reports are kept.
func WithMaxEntries(n int) Option {
	return func(c *Cache) { c.maxEntries = n }
}

// withClock overrides the clock, for tests.
func withClock(now func() time.Time) Option {
	return func(c *Cache) { c.now = now }
}

// Open returns the report cache for a GitHub host.
func Open(host string, opts ...Option) *Cache {
	c := &Cache{
		host:       host,
		ttl:        DefaultTTL,
		maxEntries: DefaultMaxEntries,
		now:        time.Now,
	}

	base, err := os.UserCacheDir()
	if err != nil {
		c.disabled = fmt.Errorf("locating user cache dir: %w", err)
	} else {
		c.path = filepath.Join(base, "gh-contribs", fileName(host))
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Path reports where the cache is stored; empty when the cache is disabled.
func (c *Cache) Path() string {
	if c == nil {
		return ""
	}
	return c.path
}

// Load returns the cached report for a person over a window. The second result
// is false when nothing is cached; a cached but stale report is returned with
// Fresh unset.
func (c *Cache) Load(login string, window contrib.Range) (Entry, bool) {
	if c == nil || c.path == "" {
		return Entry{}, false
	}
	records, err := c.read()
	if err != nil {
		return Entry{}, false
	}

	key := entryKey(login, window)
	for _, rec := range records {
		if rec.Key != key || rec.Report == nil {
			continue
		}
		return Entry{
			Report:    rec.Report,
			FetchedAt: rec.FetchedAt,
			Fresh:     c.now().Sub(rec.FetchedAt) <= c.ttl,
		}, true
	}
	return Entry{}, false
}

// Store writes a report for a person over a window, replacing any previous one
// and evicting the oldest entries once the cache is full.
func (c *Cache) Store(login string, window contrib.Range, report *contrib.Report) error {
	if c == nil {
		return errors.New("nil report cache")
	}
	if c.path == "" {
		if c.disabled != nil {
			return c.disabled
		}
		return errors.New("report cache is disabled")
	}
	if report == nil {
		return errors.New("nil report")
	}

	// A cache that cannot be read is a cache that gets rebuilt: the entries are
	// all refetchable, so a corrupt file is overwritten rather than reported.
	records, _ := c.read()

	key := entryKey(login, window)
	kept := make([]record, 0, len(records)+1)
	for _, rec := range records {
		if rec.Key != key {
			kept = append(kept, rec)
		}
	}
	kept = append(kept, record{Key: key, FetchedAt: c.now(), Report: report})

	// Evict oldest first. Sorting by fetch time rather than by insertion order
	// keeps the newest entries when a file written by an older run is merged in.
	if len(kept) > c.maxEntries {
		sort.SliceStable(kept, func(i, j int) bool {
			return kept[i].FetchedAt.Before(kept[j].FetchedAt)
		})
		kept = kept[len(kept)-c.maxEntries:]
	}
	return c.write(kept)
}

func (c *Cache) read() ([]record, error) {
	raw, err := os.ReadFile(c.path)
	if err != nil {
		return nil, err
	}
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if p.SchemaVersion != schemaVersion || p.Host != c.host {
		return nil, fmt.Errorf("report cache %s is for another host or schema", c.path)
	}
	return p.Records, nil
}

// write replaces the cache file. The write goes to a temporary file first so a
// crash cannot leave a torn cache behind.
func (c *Cache) write(records []record) error {
	raw, err := json.Marshal(payload{
		SchemaVersion: schemaVersion,
		Host:          c.host,
		Records:       records,
	})
	if err != nil {
		return fmt.Errorf("encoding report cache: %w", err)
	}

	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("creating cache dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".reports-*.json")
	if err != nil {
		return fmt.Errorf("creating cache file: %w", err)
	}
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(filePerm); err != nil {
		tmp.Close()
		return fmt.Errorf("securing cache file: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("writing cache file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing cache file: %w", err)
	}
	if err := os.Rename(tmp.Name(), c.path); err != nil {
		return fmt.Errorf("replacing cache file: %w", err)
	}
	return nil
}

// entryKey identifies one person's report over one window. Logins are folded to
// lower case, the way GitHub treats them.
func entryKey(login string, window contrib.Range) string {
	return fmt.Sprintf("%s|%s|%s",
		strings.ToLower(strings.TrimSpace(login)),
		window.From.UTC().Format(contrib.DateFormat),
		window.To.UTC().Format(contrib.DateFormat))
}

// fileName derives a filesystem-safe name so hosts never share a cache file.
func fileName(host string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '_'
		}
	}, host)
	if safe == "" {
		safe = "unknown"
	}
	return "reports-" + safe + ".json"
}
