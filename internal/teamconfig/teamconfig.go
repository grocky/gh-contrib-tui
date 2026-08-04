// Package teamconfig persists the roster the dashboard summarizes: who is on
// your team. Unlike the directory cache, this is a decision rather than a copy
// of something GitHub knows, so it lives in the user's config directory, is
// written to be hand-editable, and is never silently discarded.
package teamconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gh-contribs/internal/contrib"
)

const (
	// schemaVersion is bumped whenever the on-disk shape changes. A mismatch is
	// reported rather than discarded: the file holds a curated list that nothing
	// else can reconstruct.
	schemaVersion = 1

	dirPerm  = 0o700
	filePerm = 0o600
)

// ErrNotConfigured reports that no team has been set up yet, which is a first
// launch rather than a failure. A team that exists and happens to be empty
// loads cleanly and does not return this.
var ErrNotConfigured = errors.New("no team configured")

// Team is the roster the dashboard summarizes.
type Team struct {
	Members []contrib.Person `json:"members"`
}

// entry is the serialized payload.
type entry struct {
	SchemaVersion int              `json:"schemaVersion"`
	Host          string           `json:"host"`
	Members       []contrib.Person `json:"members"`
}

// Add puts a person on the team, in the order they were added. A login already
// on the roster is not duplicated, whatever case it arrives in; it picks up a
// display name if it was missing one.
func (t Team) Add(p contrib.Person) Team {
	p.Login = strings.TrimSpace(p.Login)
	if p.Login == "" {
		return t
	}

	members := append([]contrib.Person(nil), t.Members...)
	for i, member := range members {
		if !strings.EqualFold(member.Login, p.Login) {
			continue
		}
		if members[i].Name == "" {
			members[i].Name = p.Name
		}
		members[i].Active = members[i].Active || p.Active
		return Team{Members: members}
	}
	return Team{Members: append(members, p)}
}

// Remove takes a login off the team, matching the way GitHub treats logins:
// case-insensitively. Removing someone absent is not an error.
func (t Team) Remove(login string) Team {
	login = strings.TrimSpace(login)
	members := make([]contrib.Person, 0, len(t.Members))
	for _, member := range t.Members {
		if strings.EqualFold(member.Login, login) {
			continue
		}
		members = append(members, member)
	}
	return Team{Members: members}
}

// Has reports whether a login is on the team.
func (t Team) Has(login string) bool {
	login = strings.TrimSpace(login)
	for _, member := range t.Members {
		if strings.EqualFold(member.Login, login) {
			return true
		}
	}
	return false
}

// Logins returns the roster's logins in order.
func (t Team) Logins() []string {
	logins := make([]string, 0, len(t.Members))
	for _, member := range t.Members {
		logins = append(logins, member.Login)
	}
	return logins
}

// Store reads and writes one host's team.
type Store struct {
	host     string
	path     string
	disabled error
}

// Option customizes a Store.
type Option func(*Store)

// WithBaseDir overrides the config directory, primarily for tests.
func WithBaseDir(dir string) Option {
	return func(s *Store) {
		s.disabled = nil
		s.path = filepath.Join(dir, fileName(s.host))
	}
}

// Open returns the team store for a GitHub host. Like the directory cache it
// never fails to construct: an unusable config location yields a Store that
// reports the reason from Load and Save rather than taking the app down.
func Open(host string, opts ...Option) *Store {
	s := &Store{host: host}

	base, err := os.UserConfigDir()
	if err != nil {
		s.disabled = fmt.Errorf("locating user config dir: %w", err)
	} else {
		s.path = filepath.Join(base, "gh-contribs", fileName(host))
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Path reports where the team is stored; empty when the store is disabled.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Load returns the configured team. It reports ErrNotConfigured when there is
// no team on disk yet, and a real error for a file that exists but cannot be
// used -- a corrupt or hand-mangled config is not a reason to quietly start the
// roster over.
func (s *Store) Load() (Team, error) {
	if s == nil || s.path == "" {
		if s != nil && s.disabled != nil {
			return Team{}, s.disabled
		}
		return Team{}, ErrNotConfigured
	}

	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Team{}, ErrNotConfigured
	}
	if err != nil {
		return Team{}, fmt.Errorf("reading team config: %w", err)
	}

	var e entry
	if err := json.Unmarshal(raw, &e); err != nil {
		return Team{}, fmt.Errorf("parsing team config %s: %w", s.path, err)
	}
	if e.SchemaVersion != schemaVersion {
		return Team{}, fmt.Errorf("team config %s has schema version %d, want %d",
			s.path, e.SchemaVersion, schemaVersion)
	}
	if e.Members == nil {
		e.Members = []contrib.Person{}
	}
	return Team{Members: e.Members}, nil
}

// Save writes the team, replacing any previous contents. The write goes through
// a temporary file so an interrupted save cannot leave a torn roster behind.
func (s *Store) Save(team Team) error {
	if s == nil {
		return errors.New("nil team store")
	}
	if s.path == "" {
		if s.disabled != nil {
			return s.disabled
		}
		return errors.New("team config is disabled")
	}

	members := team.Members
	if members == nil {
		members = []contrib.Person{}
	}
	raw, err := json.MarshalIndent(entry{
		SchemaVersion: schemaVersion,
		Host:          s.host,
		Members:       members,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding team config: %w", err)
	}
	raw = append(raw, '\n')

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".team-*.json")
	if err != nil {
		return fmt.Errorf("creating team config: %w", err)
	}
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(filePerm); err != nil {
		tmp.Close()
		return fmt.Errorf("securing team config: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("writing team config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing team config: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("replacing team config: %w", err)
	}
	return nil
}

// fileName derives a filesystem-safe name so each host keeps its own roster.
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
	return "team-" + safe + ".json"
}
