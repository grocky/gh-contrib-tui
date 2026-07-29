package ghapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gh-contribs/internal/config"
	"gh-contribs/internal/contrib"
)

const (
	userPayload = `{"data":{"user":{"login":"octocat","name":"Mona"}}}`

	authoredPayload = `{"data":{"search":{
		"pageInfo":{"hasNextPage":false,"endCursor":""},
		"nodes":[
			{"__typename":"PullRequest","title":"Add feature","url":"https://example.com/pr/1",
				"createdAt":"2026-07-02T10:00:00Z","merged":true,
				"repository":{"nameWithOwner":"acme/repo1"}},
			{"__typename":"PullRequest","title":"Work in progress","url":"https://example.com/pr/2",
				"createdAt":"2026-07-06T10:00:00Z","merged":false,
				"repository":{"nameWithOwner":"acme/repo1"}},
			{"__typename":"Issue","title":"Something is broken","url":"https://example.com/issue/1",
				"createdAt":"2026-07-03T10:00:00Z",
				"repository":{"nameWithOwner":"acme/repo2"}}
		]}}}`

	// The second review falls outside the window: search can only filter on
	// when the pull request was last updated, so this is expected traffic.
	reviewPayload = `{"data":{"search":{
		"pageInfo":{"hasNextPage":false,"endCursor":""},
		"nodes":[
			{"title":"Review me","url":"https://example.com/pr/9",
				"repository":{"nameWithOwner":"acme/repo2"},
				"reviews":{"nodes":[
					{"submittedAt":"2026-07-04T10:00:00Z","url":"https://example.com/pr/9#review-1"},
					{"submittedAt":"2026-06-01T10:00:00Z","url":"https://example.com/pr/9#review-0"}
				]}}
		]}}}`

	commitPayload = `{"total_count":1,"items":[
		{"sha":"abc123","html_url":"https://example.com/commit/abc123",
			"commit":{"message":"Fix the thing\n\nWith a longer explanation.",
				"author":{"date":"2026-07-05T10:00:00Z"}},
			"repository":{"full_name":"acme/repo1"}}
	]}`
)

// recorder captures every request a fetch makes, so tests can assert on the
// search qualifiers we send as well as on what we do with the answers.
type recorder struct {
	mu       sync.Mutex
	graphql  []string
	restURLs []string
}

func (r *recorder) add(query, url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if query != "" {
		r.graphql = append(r.graphql, query)
	}
	if url != "" {
		r.restURLs = append(r.restURLs, url)
	}
}

func (r *recorder) graphqlQueries() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.graphql...)
}

// contributionsServer answers the four requests a fetch makes: the user
// lookup, the authored search, the review search, and the REST commit search.
// commitStatus of 0 serves commitPayload; anything else fails that one call.
func contributionsServer(t *testing.T, commitStatus int) (*Client, *recorder) {
	t.Helper()
	rec := &recorder{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/search/commits") {
			rec.add("", r.URL.String())
			if commitStatus != 0 {
				w.WriteHeader(commitStatus)
				io.WriteString(w, `{"message":"Search is disabled"}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, commitPayload)
			return
		}

		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(raw, &req)

		searchQuery, _ := req.Variables["query"].(string)
		rec.add(searchQuery, "")

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(req.Query, "user(login:"):
			io.WriteString(w, userPayload)
		case strings.Contains(req.Query, "reviews(first:"):
			io.WriteString(w, reviewPayload)
		default:
			io.WriteString(w, authoredPayload)
		}
	}))
	t.Cleanup(srv.Close)

	client := New(
		WithConfig(config.Config{
			Host:       "github.example.com",
			GraphQLURL: srv.URL + "/api/graphql",
			RestBase:   srv.URL + "/api/v3/",
		}),
		WithTokenResolver(staticTokens("test-token")),
	)
	return client, rec
}

func testWindow() contrib.Range {
	return contrib.Range{
		From: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 7, 29, 23, 59, 59, 0, time.UTC),
	}
}

func TestFetchContributions_AggregatesEverySource(t *testing.T) {
	client, _ := contributionsServer(t, 0)

	report, err := client.FetchContributions(context.Background(), "octocat", testWindow())
	if err != nil {
		t.Fatalf("FetchContributions returned error: %v", err)
	}

	if report.Login != "octocat" || report.DisplayName() != "Mona" {
		t.Fatalf("user = %+v", report)
	}
	cc := report.Collection
	if cc.TotalCommits != 1 || cc.TotalIssues != 1 || cc.TotalPullRequests != 2 || cc.TotalReviews != 1 {
		t.Fatalf("totals = %+v", cc)
	}
	if cc.TotalMergedPullRequests != 1 {
		t.Fatalf("merged = %d, want 1 of the 2 authored", cc.TotalMergedPullRequests)
	}
	if cc.Calendar.Total != 5 {
		t.Fatalf("calendar total = %d, want 5 (the out-of-window review dropped)", cc.Calendar.Total)
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", report.Warnings)
	}

	// Newest first, across all four sources.
	want := []struct{ kind, url string }{
		{contrib.KindAuthored, "https://example.com/pr/2"},
		{contrib.KindCommit, "https://example.com/commit/abc123"},
		{contrib.KindReviewed, "https://example.com/pr/9#review-1"},
		{contrib.KindIssue, "https://example.com/issue/1"},
		{contrib.KindAuthored, "https://example.com/pr/1"},
	}
	if len(cc.Events) != len(want) {
		t.Fatalf("events = %+v, want %d", cc.Events, len(want))
	}
	for i, w := range want {
		if cc.Events[i].Kind != w.kind || cc.Events[i].URL != w.url {
			t.Errorf("event %d = %s %s, want %s %s", i, cc.Events[i].Kind, cc.Events[i].URL, w.kind, w.url)
		}
	}
}

func TestFetchContributions_MapsEventDetail(t *testing.T) {
	client, _ := contributionsServer(t, 0)

	report, err := client.FetchContributions(context.Background(), "octocat", testWindow())
	if err != nil {
		t.Fatalf("FetchContributions returned error: %v", err)
	}

	byURL := map[string]contrib.Event{}
	for _, e := range report.Collection.Events {
		byURL[e.URL] = e
	}

	commit := byURL["https://example.com/commit/abc123"]
	if commit.Repo != "acme/repo1" {
		t.Errorf("commit repo = %q", commit.Repo)
	}
	if commit.Title != "Fix the thing" {
		t.Errorf("commit title = %q, want just the subject line", commit.Title)
	}
	if got := commit.OccurredAt.Format(time.RFC3339); got != "2026-07-05T10:00:00Z" {
		t.Errorf("commit time = %s", got)
	}

	review := byURL["https://example.com/pr/9#review-1"]
	if review.Repo != "acme/repo2" || review.Title != "Review me" {
		t.Errorf("review = %+v", review)
	}
	if got := review.OccurredAt.Format(time.RFC3339); got != "2026-07-04T10:00:00Z" {
		t.Errorf("review time = %s, want the review's own timestamp", got)
	}

	if pr := byURL["https://example.com/pr/1"]; !pr.Merged {
		t.Errorf("expected the merged pull request to be flagged: %+v", pr)
	}
	if pr := byURL["https://example.com/pr/2"]; pr.Merged {
		t.Errorf("expected the open pull request not to be flagged: %+v", pr)
	}
}

func TestFetchContributions_SendsScopedSearchQueries(t *testing.T) {
	client, rec := contributionsServer(t, 0)

	if _, err := client.FetchContributions(context.Background(), "octocat", testWindow()); err != nil {
		t.Fatalf("FetchContributions returned error: %v", err)
	}

	queries := rec.graphqlQueries()
	want := []string{
		"author:octocat created:2026-07-01..2026-07-29",
		// No upper bound: a review inside the window can sit on a pull request
		// updated long after it closed.
		"is:pr reviewed-by:octocat updated:>=2026-07-01",
	}
	for _, w := range want {
		found := false
		for _, got := range queries {
			if got == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no search sent %q; sent %q", w, queries)
		}
	}

	if len(rec.restURLs) != 1 || !strings.Contains(rec.restURLs[0], "author%3Aoctocat+author-date%3A2026-07-01..2026-07-29") {
		t.Errorf("commit search URLs = %q", rec.restURLs)
	}
}

// A deployment can have commit search unavailable. The rest of the history is
// still worth showing, but the report must say what is missing from it.
func TestFetchContributions_WarnsWhenCommitSearchFails(t *testing.T) {
	client, _ := contributionsServer(t, http.StatusServiceUnavailable)

	report, err := client.FetchContributions(context.Background(), "octocat", testWindow())
	if err != nil {
		t.Fatalf("commit search failure must not fail the whole fetch: %v", err)
	}
	if report.Collection.TotalCommits != 0 {
		t.Fatalf("commits = %d, want 0", report.Collection.TotalCommits)
	}
	if report.Collection.TotalPullRequests != 2 || report.Collection.TotalReviews != 1 {
		t.Fatalf("expected the other sources to survive: %+v", report.Collection)
	}
	if len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "commits unavailable") {
		t.Fatalf("warnings = %v", report.Warnings)
	}
}

func TestFetchContributions_UnknownUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":{"user":{"login":""}}}`)
	}))
	t.Cleanup(srv.Close)
	client := New(
		WithConfig(config.Config{Host: "github.example.com", GraphQLURL: srv.URL}),
		WithTokenResolver(staticTokens("test-token")),
	)

	_, err := client.FetchContributions(context.Background(), "ghost", contrib.Range{})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
}

func TestFetchContributions_SurfacesGraphQLErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"errors":[{"message":"Something went wrong"}]}`)
	}))
	t.Cleanup(srv.Close)
	client := New(
		WithConfig(config.Config{Host: "github.example.com", GraphQLURL: srv.URL}),
		WithTokenResolver(staticTokens("test-token")),
	)

	_, err := client.FetchContributions(context.Background(), "octocat", contrib.Range{})
	if err == nil {
		t.Fatal("expected the GraphQL error to be returned")
	}
	if !strings.Contains(err.Error(), "Something went wrong") {
		t.Fatalf("err = %v, want it to mention the GraphQL message", err)
	}
}

func TestFirstLine(t *testing.T) {
	tests := map[string]string{
		"Fix the thing":                   "Fix the thing",
		"Fix the thing\n\nAnd explain it": "Fix the thing",
		"  Padded  \nrest":                "Padded",
		"":                                "",
	}
	for in, want := range tests {
		if got := firstLine(in); got != want {
			t.Errorf("firstLine(%q) = %q, want %q", in, got, want)
		}
	}
}
