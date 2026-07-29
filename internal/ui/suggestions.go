package ui

import (
	"strings"
	"sync"
)

// maxSuggestions caps how many autocomplete entries the input field offers.
const maxSuggestions = 25

// suggestionStore holds user logins for autocomplete. Discovery runs on a
// background goroutine while the input field reads from the UI goroutine, so
// access is guarded.
type suggestionStore struct {
	mu     sync.RWMutex
	logins []string
}

func (s *suggestionStore) set(logins []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logins = logins
}

func (s *suggestionStore) match(query string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return filterSuggestions(s.logins, query, maxSuggestions)
}

// filterSuggestions returns up to limit case-insensitive substring matches. An
// empty query returns the first limit entries.
func filterSuggestions(all []string, query string, limit int) []string {
	if len(all) == 0 || limit <= 0 {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		if len(all) < limit {
			limit = len(all)
		}
		return append([]string(nil), all[:limit]...)
	}
	out := make([]string, 0, limit)
	for _, login := range all {
		if strings.Contains(strings.ToLower(login), q) {
			out = append(out, login)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}
