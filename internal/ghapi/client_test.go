package ghapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v66/github"

	"gh-contribs/internal/auth"
	"gh-contribs/internal/config"
	"gh-contribs/internal/contrib"
)

func staticTokens(tokens ...string) TokenResolver {
	return func() ([]auth.Candidate, error) {
		out := make([]auth.Candidate, 0, len(tokens))
		for _, tok := range tokens {
			out = append(out, auth.Candidate{Source: tok, Token: tok})
		}
		return out, nil
	}
}

func TestIsUnauthorized(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "status code in message", err: errors.New("non-200 OK status code: 401 Unauthorized"), want: true},
		{name: "bad credentials", err: errors.New("Bad credentials"), want: true},
		{name: "mixed case unauthorized", err: errors.New("UNAUTHORIZED request"), want: true},
		{name: "unrelated", err: errors.New("connection refused"), want: false},
		{name: "wrapped rest 401", err: &github.ErrorResponse{
			Response: &http.Response{StatusCode: http.StatusUnauthorized},
			Message:  "Bad credentials",
		}, want: true},
		{name: "wrapped rest 404", err: &github.ErrorResponse{
			Response: &http.Response{StatusCode: http.StatusNotFound},
			Message:  "Not Found",
		}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUnauthorized(tc.err); got != tc.want {
				t.Fatalf("isUnauthorized(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestWithTokens_FallsBackAfterUnauthorized(t *testing.T) {
	c := New(WithTokenResolver(staticTokens("bad", "good")))

	attempts := 0
	got, err := withTokens(c, context.Background(), "test", func(*http.Client) (string, error) {
		attempts++
		if attempts == 1 {
			return "", errors.New("401 Unauthorized")
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("result = %q, want ok", got)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestWithTokens_DoesNotRetryNonAuthErrors(t *testing.T) {
	c := New(WithTokenResolver(staticTokens("one", "two")))

	attempts := 0
	_, err := withTokens(c, context.Background(), "test", func(*http.Client) (string, error) {
		attempts++
		return "", errors.New("service unavailable")
	})
	if err == nil {
		t.Fatal("expected the error to be returned")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (non-auth errors must not burn other tokens)", attempts)
	}
}

func TestWithTokens_AllCandidatesUnauthorized(t *testing.T) {
	c := New(WithTokenResolver(staticTokens("one", "two")))

	attempts := 0
	_, err := withTokens(c, context.Background(), "test", func(*http.Client) (string, error) {
		attempts++
		return "", errors.New("401 Unauthorized")
	})
	if err == nil {
		t.Fatal("expected an error once every candidate fails")
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestWithTokens_PropagatesResolverError(t *testing.T) {
	want := errors.New("no tokens")
	c := New(WithTokenResolver(func() ([]auth.Candidate, error) { return nil, want }))

	_, err := withTokens(c, context.Background(), "test", func(*http.Client) (string, error) {
		t.Fatal("call must not run when token resolution fails")
		return "", nil
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

// graphqlServer returns a server that records the last request body and replies
// with the supplied JSON payload.
func graphqlServer(t *testing.T, payload string) (*httptest.Server, *string, *string) {
	t.Helper()
	var body, authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, payload)
	}))
	t.Cleanup(srv.Close)
	return srv, &body, &authHeader
}

const contributionsPayload = `{"data":{"user":{
	"login":"octocat",
	"name":"Mona",
	"contributionsCollection":{
		"startedAt":"2026-07-01T00:00:00Z",
		"endedAt":"2026-07-29T23:59:59Z",
		"totalCommitContributions":11,
		"totalIssueContributions":2,
		"totalPullRequestContributions":5,
		"totalPullRequestReviewContributions":7,
		"contributionCalendar":{
			"totalContributions":25,
			"weeks":[{"contributionDays":[
				{"date":"2026-07-01","contributionCount":3,"color":"#216e39"},
				{"date":"garbage","contributionCount":9,"color":"#000000"}
			]}]
		},
		"pullRequestContributionsByRepository":[{
			"repository":{"nameWithOwner":"acme/repo1"},
			"contributions":{"nodes":[
				{"occurredAt":"2026-07-02T10:00:00Z","pullRequest":{"title":"Add feature","url":"https://example.com/pr/1"}}
			]}
		}],
		"pullRequestReviewContributionsByRepository":[{
			"repository":{"nameWithOwner":"acme/repo2"},
			"contributions":{"nodes":[
				{"occurredAt":"2026-07-03T10:00:00Z","pullRequest":{"title":"Review me","url":"https://example.com/pr/2"}}
			]}
		}]
	}
}}}`

func fetchTestReport(t *testing.T) (*contrib.Report, string, string) {
	t.Helper()
	srv, body, authHeader := graphqlServer(t, contributionsPayload)
	client := New(
		WithConfig(config.Config{Host: "github.example.com", GraphQLURL: srv.URL}),
		WithTokenResolver(staticTokens("test-token")),
	)

	window := contrib.Range{
		From: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 7, 29, 23, 59, 59, 0, time.UTC),
	}
	report, err := client.FetchContributions(context.Background(), "octocat", window)
	if err != nil {
		t.Fatalf("FetchContributions returned error: %v", err)
	}
	return report, *body, *authHeader
}

func TestFetchContributions_SendsExpectedQuery(t *testing.T) {
	_, body, authHeader := fetchTestReport(t)

	var req struct {
		Query     string          `json:"query"`
		Variables json.RawMessage `json:"variables"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("request body is not JSON: %v (%s)", err, body)
	}

	// The GraphQL document is derived from the fragment structs in query.go, so
	// pinning it here catches accidental changes to what we ask GitHub for.
	const wantQuery = `query($from:DateTime!$login:String!$to:DateTime!){user(login: $login){login,name,` +
		`contributionsCollection(from: $from, to: $to){startedAt,endedAt,totalCommitContributions,` +
		`totalIssueContributions,totalPullRequestContributions,totalPullRequestReviewContributions,` +
		`contributionCalendar{totalContributions,weeks{contributionDays{date,contributionCount,color}}},` +
		`pullRequestContributionsByRepository(maxRepositories: 100){repository{nameWithOwner},` +
		`contributions(first: 100){nodes{occurredAt,pullRequest{title,url}}}},` +
		`pullRequestReviewContributionsByRepository(maxRepositories: 100){repository{nameWithOwner},` +
		`contributions(first: 100){nodes{occurredAt,pullRequest{title,url}}}}}}}`
	if req.Query != wantQuery {
		t.Errorf("unexpected GraphQL document:\n got: %s\nwant: %s", req.Query, wantQuery)
	}

	vars := string(req.Variables)
	for _, want := range []string{`"login":"octocat"`, `"from":"2026-07-01T00:00:00Z"`, `"to":"2026-07-29T23:59:59Z"`} {
		if !strings.Contains(vars, want) {
			t.Errorf("variables missing %s, got %s", want, vars)
		}
	}

	if authHeader == "" || !strings.Contains(authHeader, "test-token") {
		t.Errorf("Authorization header = %q, want it to carry the token", authHeader)
	}
}

func TestFetchContributions_MapsResponseToDomain(t *testing.T) {
	report, _, _ := fetchTestReport(t)

	if report.Login != "octocat" || report.DisplayName() != "Mona" {
		t.Fatalf("user = %+v", report)
	}
	cc := report.Collection
	if cc.TotalCommits != 11 || cc.TotalIssues != 2 || cc.TotalPullRequests != 5 || cc.TotalReviews != 7 {
		t.Fatalf("totals = %+v", cc)
	}
	if cc.Calendar.Total != 25 {
		t.Fatalf("calendar total = %d, want 25", cc.Calendar.Total)
	}
	if !cc.StartedAt.Equal(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("StartedAt = %s", cc.StartedAt)
	}

	if len(cc.Calendar.Weeks) != 1 {
		t.Fatalf("weeks = %d, want 1", len(cc.Calendar.Weeks))
	}
	days := cc.Calendar.Weeks[0].Days
	if len(days) != 1 {
		t.Fatalf("expected the unparseable day to be dropped, got %+v", days)
	}
	if days[0].Count != 3 || days[0].Color != "#216e39" || days[0].Date.Format(contrib.DateFormat) != "2026-07-01" {
		t.Fatalf("day = %+v", days[0])
	}

	if len(cc.AuthoredPullRequests) != 1 || cc.AuthoredPullRequests[0].Repository != "acme/repo1" {
		t.Fatalf("authored = %+v", cc.AuthoredPullRequests)
	}
	if got := cc.AuthoredPullRequests[0].Nodes[0]; got.Title != "Add feature" || got.URL != "https://example.com/pr/1" {
		t.Fatalf("authored node = %+v", got)
	}
	if len(cc.ReviewedPullRequests) != 1 || cc.ReviewedPullRequests[0].Repository != "acme/repo2" {
		t.Fatalf("reviewed = %+v", cc.ReviewedPullRequests)
	}
}

func TestFetchContributions_UnknownUser(t *testing.T) {
	srv, _, _ := graphqlServer(t, `{"data":{"user":{"login":""}}}`)
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
	srv, _, _ := graphqlServer(t, `{"errors":[{"message":"Something went wrong"}]}`)
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

func TestNew_DefaultsResolveFromEnvironment(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_HOSTNAME", "github.example.com")

	c := New()
	if got := c.Config().Host; got != "github.example.com" {
		t.Fatalf("Config().Host = %s, want github.example.com", got)
	}
}
