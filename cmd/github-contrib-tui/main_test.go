package main

import (
	"testing"
	"time"
)

func TestParseContributionDate_DateOnly(t *testing.T) {
	got, err := parseContributionDate("2026-06-29")
	if err != nil {
		t.Fatalf("expected date-only value to parse, got error: %v", err)
	}
	if got != "2026-06-29" {
		t.Fatalf("expected unchanged date, got %q", got)
	}
}

func TestParseContributionDate_DateTime(t *testing.T) {
	got, err := parseContributionDate("2026-06-29T11:22:33Z")
	if err != nil {
		t.Fatalf("expected datetime value to parse, got error: %v", err)
	}
	if got != "2026-06-29" {
		t.Fatalf("expected normalized date, got %q", got)
	}
}

func TestBuildGitHubEndpoints(t *testing.T) {
	rest, upload, gql := buildGitHubEndpoints("github.example.com")
	if rest != "https://github.example.com/api/v3/" {
		t.Fatalf("unexpected rest endpoint: %s", rest)
	}
	if upload != "https://github.example.com/api/uploads/" {
		t.Fatalf("unexpected upload endpoint: %s", upload)
	}
	if gql != "https://github.example.com/api/graphql" {
		t.Fatalf("unexpected graphql endpoint: %s", gql)
	}
}

func TestBuildGitHubEndpoints_Dotcom(t *testing.T) {
	rest, upload, gql := buildGitHubEndpoints("github.com")
	if rest != "https://api.github.com/" {
		t.Fatalf("unexpected rest endpoint: %s", rest)
	}
	if upload != "https://uploads.github.com/" {
		t.Fatalf("unexpected upload endpoint: %s", upload)
	}
	if gql != "https://api.github.com/graphql" {
		t.Fatalf("unexpected graphql endpoint: %s", gql)
	}
}

func TestParseContributionDate_Invalid(t *testing.T) {
	_, err := parseContributionDate("not-a-date")
	if err == nil {
		t.Fatalf("expected parse error for invalid date")
	}
}

func TestParseRange_Valid(t *testing.T) {
	from, to, err := parseRange("2026-07-01", "2026-07-29")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if from != "2026-07-01T00:00:00Z" || to != "2026-07-29T23:59:59Z" {
		t.Fatalf("unexpected range: %s %s", from, to)
	}
}

func TestParseRange_ToWithoutFrom(t *testing.T) {
	_, _, err := parseRange("", "2026-07-29")
	if err == nil {
		t.Fatalf("expected error when --to is provided without --from")
	}
}

func TestNormalizeWeekday(t *testing.T) {
	if got := normalizeWeekday(time.Monday); got != 0 {
		t.Fatalf("expected monday=0, got %d", got)
	}
	if got := normalizeWeekday(time.Sunday); got != 6 {
		t.Fatalf("expected sunday=6, got %d", got)
	}
}

func TestDedupeTokenCandidates(t *testing.T) {
	in := []tokenCandidate{
		{Source: "A", Token: "same"},
		{Source: "B", Token: "same"},
		{Source: "C", Token: "other"},
	}
	got := dedupeTokenCandidates(in)
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(got))
	}
	if got[0].Source != "A" || got[1].Source != "C" {
		t.Fatalf("expected stable first-seen order, got %+v", got)
	}
}

func TestCollectPRs_DedupesAndSorts(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-24 * time.Hour)
	resp := &contributionResponse{}
	resp.Data.User.ContributionsCollection.PullRequestContributionsByRepository = []prContributionByRepo{
		{
			Repository: struct{ NameWithOwner string }{NameWithOwner: "acme/repo1"},
			Contributions: struct{ Nodes []prContributionNode }{
				Nodes: []prContributionNode{
					{
						OccurredAt: now,
						PullRequest: struct {
							Title string
							URL   string
						}{Title: "A", URL: "https://example/pr/1"},
					},
				},
			},
		},
	}
	resp.Data.User.ContributionsCollection.PullRequestReviewContributionsByRepository = []prContributionByRepo{
		{
			Repository: struct{ NameWithOwner string }{NameWithOwner: "acme/repo1"},
			Contributions: struct{ Nodes []prContributionNode }{
				Nodes: []prContributionNode{
					{
						OccurredAt: old,
						PullRequest: struct {
							Title string
							URL   string
						}{Title: "A", URL: "https://example/pr/1"},
					},
					{
						OccurredAt: now.Add(-time.Hour),
						PullRequest: struct {
							Title string
							URL   string
						}{Title: "B", URL: "https://example/pr/2"},
					},
				},
			},
		},
	}

	got := collectPRs(resp)
	if len(got) != 3 {
		t.Fatalf("expected 3 entries (authored+reviewed URL can both exist), got %d", len(got))
	}
	if got[0].URL != "https://example/pr/1" || got[0].Kind != "authored" {
		t.Fatalf("expected newest authored PR first, got %+v", got[0])
	}
	if got[1].URL != "https://example/pr/2" || got[1].Kind != "reviewed" {
		t.Fatalf("expected second newest reviewed PR, got %+v", got[1])
	}
}

