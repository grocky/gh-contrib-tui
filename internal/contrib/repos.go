package contrib

import (
	"sort"
	"strings"
)

// RepoTotal is one repository's share of a report.
type RepoTotal struct {
	Repo  string
	Count int
}

// Share is this repository's fraction of a total, in the range 0 to 1. A total
// of zero has no shares to give out rather than a division to perform.
func (r RepoTotal) Share(total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(r.Count) / float64(total)
}

// TotalOf sums a breakdown, which is the denominator its shares are taken over.
func TotalOf(totals []RepoTotal) int {
	sum := 0
	for _, t := range totals {
		sum += t.Count
	}
	return sum
}

// RepoTotals counts a report's contributions by repository, busiest first.
//
// Every kind counts: where someone's reviews went is as much a part of where
// their work went as where their commits did. Equal counts fall back to the
// name, so a breakdown of the same data never comes back in a different order.
func RepoTotals(report *Report) []RepoTotal {
	if report == nil || len(report.Collection.Events) == 0 {
		return nil
	}

	counts := map[string]int{}
	for _, event := range report.Collection.Events {
		repo := strings.TrimSpace(event.Repo)
		if repo == "" {
			continue
		}
		counts[repo]++
	}

	totals := make([]RepoTotal, 0, len(counts))
	for repo, count := range counts {
		totals = append(totals, RepoTotal{Repo: repo, Count: count})
	}
	sort.Slice(totals, func(i, j int) bool {
		if totals[i].Count != totals[j].Count {
			return totals[i].Count > totals[j].Count
		}
		return totals[i].Repo < totals[j].Repo
	})
	return totals
}
