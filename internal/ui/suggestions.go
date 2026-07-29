package ui

import (
	"fmt"
	"strings"
	"sync"

	"gh-contribs/internal/contrib"
)

const (
	// maxSuggestions caps how many autocomplete entries the input field offers.
	maxSuggestions = 50

	// entrySeparator joins a login to its display name in a dropdown entry.
	// Logins cannot contain spaces, so the login is always the first field.
	entrySeparator = " — "

	// inactiveMarker flags accounts that only appear via team membership, which
	// on GitHub means suspended or otherwise deactivated.
	inactiveMarker = " (suspended)"
)

// suggestionStore holds the user directory for autocomplete. Discovery runs on
// a background goroutine while the input field reads from the UI goroutine, so
// access is guarded.
type suggestionStore struct {
	mu        sync.RWMutex
	directory *contrib.Directory
}

func (s *suggestionStore) set(directory *contrib.Directory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.directory = directory
}

func (s *suggestionStore) len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.directory.Len()
}

// entries returns dropdown labels for the query. tview inserts the selected
// label verbatim, so loginFromEntry is used to recover the bare login.
func (s *suggestionStore) entries(query string) []string {
	s.mu.RLock()
	directory := s.directory
	s.mu.RUnlock()

	matches := directory.Match(query, maxSuggestions)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, person := range matches {
		out = append(out, entryFor(person))
	}
	return out
}

// entryFor renders one dropdown row. Style tags are deliberately not used:
// display names routinely contain square brackets (e.g. "Nash, Robin [USA]"),
// which tview would otherwise try to parse as color tags.
func entryFor(person contrib.Person) string {
	var b strings.Builder
	b.WriteString(person.Login)
	if person.Name != "" {
		fmt.Fprintf(&b, "%s%s", entrySeparator, person.Name)
	}
	if !person.Active {
		b.WriteString(inactiveMarker)
	}
	return b.String()
}

// loginFromEntry recovers the login from a dropdown label, so selecting
// "nash-robin — Nash, Robin [USA]" puts only "nash-robin" in the field.
func loginFromEntry(entry string) string {
	entry = strings.TrimSpace(entry)
	if login, _, found := strings.Cut(entry, entrySeparator); found {
		return strings.TrimSpace(login)
	}
	// No name, but there may still be an inactive marker to strip.
	return strings.TrimSpace(strings.TrimSuffix(entry, inactiveMarker))
}
