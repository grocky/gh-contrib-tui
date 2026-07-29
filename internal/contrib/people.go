package contrib

import (
	"sort"
	"strings"
	"unicode"
)

// Person is a GitHub account offered as an autocomplete suggestion.
type Person struct {
	Login  string   `json:"login"`
	Name   string   `json:"name,omitempty"`
	Orgs   []string `json:"orgs,omitempty"`
	Active bool     `json:"active"`
}

// DisplayName is the person's real name, falling back to their login.
func (p Person) DisplayName() string {
	if p.Name != "" {
		return p.Name
	}
	return p.Login
}

// MergePeople combines people discovered from several sources into one set,
// deduplicated by login (case-insensitively). The first spelling of a login
// wins, the first non-empty name wins, orgs are unioned, and Active is true if
// any source saw the account as active. The result is sorted active-first, then
// alphabetically by login.
func MergePeople(groups ...[]Person) []Person {
	index := map[string]int{}
	merged := make([]Person, 0)

	for _, group := range groups {
		for _, p := range group {
			p.Login = strings.TrimSpace(p.Login)
			if p.Login == "" {
				continue
			}
			key := strings.ToLower(p.Login)
			pos, ok := index[key]
			if !ok {
				p.Orgs = normalizeOrgs(p.Orgs)
				index[key] = len(merged)
				merged = append(merged, p)
				continue
			}
			existing := &merged[pos]
			if existing.Name == "" {
				existing.Name = p.Name
			}
			existing.Active = existing.Active || p.Active
			existing.Orgs = normalizeOrgs(append(existing.Orgs, p.Orgs...))
		}
	}

	sort.Slice(merged, func(i, j int) bool {
		return lessPerson(merged[i], merged[j])
	})
	return merged
}

func normalizeOrgs(orgs []string) []string {
	if len(orgs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(orgs))
	out := make([]string, 0, len(orgs))
	for _, org := range orgs {
		org = strings.TrimSpace(org)
		if org == "" {
			continue
		}
		if _, ok := seen[org]; ok {
			continue
		}
		seen[org] = struct{}{}
		out = append(out, org)
	}
	sort.Strings(out)
	return out
}

// lessPerson orders active accounts ahead of inactive ones, then by login.
func lessPerson(a, b Person) bool {
	if a.Active != b.Active {
		return a.Active
	}
	return strings.ToLower(a.Login) < strings.ToLower(b.Login)
}

// Match ranks, best first. Lower is better.
const (
	rankExactLogin = iota
	rankLoginPrefix
	rankNamePrefix
	rankLoginSubstring
	rankNameSubstring
	rankNone
)

// Directory is an immutable, searchable set of people. The zero value and a nil
// *Directory are both usable and simply match nothing.
type Directory struct {
	people  []Person
	indexed []personIndex
}

// personIndex holds the lowercased forms Match compares against, computed once
// at construction rather than per keystroke.
type personIndex struct {
	login      string
	nameTokens []string
	// searchTokens is the union of login and name tokens, so a multi-word query
	// can match across both.
	searchTokens []string
	// haystack is the login and name joined and punctuation-flattened, used for
	// substring matching.
	haystack string
}

// NewDirectory builds a Directory from people, merging duplicates.
func NewDirectory(people []Person) *Directory {
	merged := MergePeople(people)
	d := &Directory{
		people:  merged,
		indexed: make([]personIndex, 0, len(merged)),
	}
	for _, p := range merged {
		loginTokens := tokenize(p.Login)
		nameTokens := tokenize(p.Name)
		d.indexed = append(d.indexed, personIndex{
			login:        strings.ToLower(p.Login),
			nameTokens:   nameTokens,
			searchTokens: append(append([]string(nil), loginTokens...), nameTokens...),
			haystack:     strings.Join(append(append([]string(nil), loginTokens...), nameTokens...), " "),
		})
	}
	return d
}

// Len reports how many people the directory holds.
func (d *Directory) Len() int {
	if d == nil {
		return 0
	}
	return len(d.people)
}

// People returns a copy of the directory's contents, for persisting to a cache.
func (d *Directory) People() []Person {
	if d == nil {
		return nil
	}
	return append([]Person(nil), d.people...)
}

// Match returns up to limit people matching query, best match first. An empty
// query returns the head of the directory.
func (d *Directory) Match(query string, limit int) []Person {
	if d == nil || len(d.people) == 0 || limit <= 0 {
		return nil
	}

	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		if limit > len(d.people) {
			limit = len(d.people)
		}
		return append([]Person(nil), d.people[:limit]...)
	}
	qTokens := tokenize(q)

	type scored struct {
		person Person
		rank   int
	}
	hits := make([]scored, 0, limit)
	for i, idx := range d.indexed {
		if rank := idx.rank(q, qTokens); rank != rankNone {
			hits = append(hits, scored{person: d.people[i], rank: rank})
		}
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].rank != hits[j].rank {
			return hits[i].rank < hits[j].rank
		}
		return lessPerson(hits[i].person, hits[j].person)
	})

	if limit > len(hits) {
		limit = len(hits)
	}
	out := make([]Person, 0, limit)
	for _, hit := range hits[:limit] {
		out = append(out, hit.person)
	}
	return out
}

// rank scores one person against a query, returning rankNone for no match.
func (p personIndex) rank(q string, qTokens []string) int {
	if p.login == q {
		return rankExactLogin
	}
	if strings.HasPrefix(p.login, q) {
		return rankLoginPrefix
	}
	for _, token := range p.nameTokens {
		if strings.HasPrefix(token, q) {
			return rankNamePrefix
		}
	}
	// A multi-word query such as "robin nash" matches when every word is the
	// start of some login or name token, in any order.
	if len(qTokens) > 1 && allTokensPrefixMatch(qTokens, p.searchTokens) {
		return rankNamePrefix
	}
	if strings.Contains(p.login, q) {
		return rankLoginSubstring
	}
	if strings.Contains(p.haystack, q) {
		return rankNameSubstring
	}
	if len(qTokens) > 1 && allTokensContained(qTokens, p.haystack) {
		return rankNameSubstring
	}
	return rankNone
}

func allTokensPrefixMatch(qTokens, candidates []string) bool {
	for _, q := range qTokens {
		matched := false
		for _, c := range candidates {
			if strings.HasPrefix(c, q) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func allTokensContained(qTokens []string, haystack string) bool {
	for _, q := range qTokens {
		if !strings.Contains(haystack, q) {
			return false
		}
	}
	return true
}

// tokenize lowercases and splits on anything that is not a letter or digit, so
// "Nash, Robin [USA]" and "nash-robin" both yield comparable words.
func tokenize(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(fields) == 0 {
		return nil
	}
	return fields
}
