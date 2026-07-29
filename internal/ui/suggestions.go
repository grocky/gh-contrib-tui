package ui

import (
	"strings"
	"sync"

	"gh-contribs/internal/contrib"
)

const (
	// maxSuggestions caps how many autocomplete entries the user field offers.
	// The dropdown grows to fit its contents, so this is kept small enough that
	// it never swallows the rest of the layout.
	maxSuggestions = 10

	// visibleSuggestions bounds how many of those are on screen at once; the
	// rest are reachable by scrolling with the arrow keys.
	visibleSuggestions = 6

	// inactiveMarker flags accounts that only appear via team membership, which
	// on GitHub means suspended or otherwise deactivated.
	inactiveMarker = "suspended"
)

// suggestionStore holds the user directory for autocomplete. Discovery runs on
// a background goroutine while the model reads from the UI goroutine, so
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

// match returns the people to offer for a query.
//
// An empty query yields nothing: a directory of hundreds of colleagues would
// otherwise pop open over the whole layout the moment the field takes focus.
// The list appears once the user starts typing.
func (s *suggestionStore) match(query string) []contrib.Person {
	if strings.TrimSpace(query) == "" {
		return nil
	}

	s.mu.RLock()
	directory := s.directory
	s.mu.RUnlock()

	return directory.Match(query, maxSuggestions)
}
