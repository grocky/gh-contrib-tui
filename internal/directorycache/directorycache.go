// Package directorycache persists the discovered user directory between runs so
// autocomplete is populated at startup instead of after a network round trip.
package directorycache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gh-contribs/internal/contrib"
)

const (
	// schemaVersion is bumped whenever the on-disk payload shape changes; a
	// mismatch discards the cache rather than attempting a migration.
	schemaVersion = 1

	// DefaultTTL is how long a cached directory is served before a refresh is
	// required. The directory is refreshed in the background regardless, so this
	// only bounds how stale the *initial* suggestions can be.
	DefaultTTL = 24 * time.Hour

	dirPerm  = 0o700
	filePerm = 0o600
)

// entry is the serialized cache payload.
type entry struct {
	SchemaVersion int              `json:"schemaVersion"`
	Host          string           `json:"host"`
	FetchedAt     time.Time        `json:"fetchedAt"`
	People        []contrib.Person `json:"people"`
}

// Cache reads and writes one host's directory. A Cache is always usable: if the
// cache location cannot be determined, loads miss and stores report an error,
// but nothing fails fatally.
type Cache struct {
	host     string
	path     string
	ttl      time.Duration
	now      func() time.Time
	disabled error
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

// WithTTL overrides how long a cached directory stays fresh.
func WithTTL(ttl time.Duration) Option {
	return func(c *Cache) { c.ttl = ttl }
}

// withClock overrides the clock, for tests.
func withClock(now func() time.Time) Option {
	return func(c *Cache) { c.now = now }
}

// Open returns the cache for a GitHub host. It never fails: an unusable cache
// location simply yields a Cache that misses on Load and reports the reason
// from Store.
func Open(host string, opts ...Option) *Cache {
	c := &Cache{host: host, ttl: DefaultTTL, now: time.Now}

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
func (c *Cache) Path() string { return c.path }

// Load returns the cached people for the host. The second result is false when
// the cache is missing, unreadable, corrupt, for a different host or schema, or
// older than the TTL — all of which are ordinary cold-start conditions.
func (c *Cache) Load() ([]contrib.Person, bool) {
	if c == nil || c.path == "" {
		return nil, false
	}
	raw, err := os.ReadFile(c.path)
	if err != nil {
		return nil, false
	}

	var e entry
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, false
	}
	if e.SchemaVersion != schemaVersion || e.Host != c.host {
		return nil, false
	}
	if len(e.People) == 0 {
		return nil, false
	}
	if c.now().Sub(e.FetchedAt) > c.ttl {
		return nil, false
	}
	return e.People, true
}

// Store writes people to the cache, replacing any previous contents. The write
// goes to a temporary file first so a crash cannot leave a torn cache behind.
func (c *Cache) Store(people []contrib.Person) error {
	if c == nil {
		return errors.New("nil cache")
	}
	if c.path == "" {
		if c.disabled != nil {
			return c.disabled
		}
		return errors.New("cache is disabled")
	}
	if len(people) == 0 {
		return nil
	}

	raw, err := json.Marshal(entry{
		SchemaVersion: schemaVersion,
		Host:          c.host,
		FetchedAt:     c.now(),
		People:        people,
	})
	if err != nil {
		return fmt.Errorf("encoding directory cache: %w", err)
	}

	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("creating cache dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".directory-*.json")
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

// fileName derives a filesystem-safe name so github.com and Enterprise hosts
// never share a cache file.
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
	return "directory-" + safe + ".json"
}
