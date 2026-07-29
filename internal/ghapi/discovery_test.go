package ghapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// restServer serves a fake GitHub Enterprise REST API from a path -> JSON map.
// Paths absent from the map return 404. A body may be prefixed with "403|" or
// "401|" to serve that status instead.
func restServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"Not Found"}`)
			return
		}
		if code, rest, found := strings.Cut(body, "|"); found {
			switch code {
			case "401":
				w.WriteHeader(http.StatusUnauthorized)
				io.WriteString(w, rest)
				return
			case "403":
				w.WriteHeader(http.StatusForbidden)
				io.WriteString(w, rest)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// failingGraphQL always errors, forcing DiscoverPeople onto the REST fallback.
func failingGraphQL(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"errors":[{"message":"graphql disabled"}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRESTFallback_ViewerFailureIsFatal(t *testing.T) {
	rest := restServer(t, map[string]string{
		// No /user route, and no teams either, so nothing at all can be found.
	})

	_, err := peopleClient(t, failingGraphQL(t).URL, rest.URL).DiscoverPeople(context.Background())
	if err == nil {
		t.Fatal("expected an error when the viewer lookup fails")
	}
	if !strings.Contains(err.Error(), "discover viewer failed") {
		t.Fatalf("err = %v, want it to name the viewer lookup", err)
	}
}

func TestRESTFallback_OrgListFailureIsFatal(t *testing.T) {
	rest := restServer(t, map[string]string{
		"/api/v3/user": `{"login":"octocat"}`,
	})

	_, err := peopleClient(t, failingGraphQL(t).URL, rest.URL).DiscoverPeople(context.Background())
	if err == nil {
		t.Fatal("expected an error when the org listing fails")
	}
	if !strings.Contains(err.Error(), "discover orgs failed") {
		t.Fatalf("err = %v, want it to name the org listing", err)
	}
}

func TestRESTFallback_SkipsUnreadableOrgs(t *testing.T) {
	rest := restServer(t, map[string]string{
		"/api/v3/user":                `{"login":"octocat"}`,
		"/api/v3/user/orgs":           `[{"login":"acme"},{"login":"secret"}]`,
		"/api/v3/orgs/acme/members":   `[{"login":"alice"}]`,
		"/api/v3/orgs/secret/members": `403|{"message":"Forbidden"}`,
		"/api/v3/user/teams":          `[]`,
	})

	var logged []string
	client := peopleClient(t, failingGraphQL(t).URL, rest.URL)
	WithLogger(func(format string, args ...any) { logged = append(logged, format) })(client)

	people, err := client.DiscoverPeople(context.Background())
	if err != nil {
		t.Fatalf("partial failures must not fail discovery, got: %v", err)
	}

	got := make([]string, 0, len(people))
	for _, p := range people {
		got = append(got, p.Login)
	}
	if !reflect.DeepEqual(got, []string{"alice", "octocat"}) {
		t.Fatalf("logins = %v, want the readable org plus the viewer", got)
	}
	if len(logged) == 0 {
		t.Fatal("expected the skipped org to be logged")
	}
}

func TestDiscoverPeople_FallsBackToNextTokenOn401(t *testing.T) {
	calls := 0
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/user":
			calls++
			if strings.Contains(r.Header.Get("Authorization"), "bad") {
				w.WriteHeader(http.StatusUnauthorized)
				io.WriteString(w, `{"message":"Bad credentials"}`)
				return
			}
			io.WriteString(w, `{"login":"octocat"}`)
		default:
			io.WriteString(w, `[]`)
		}
	}))
	t.Cleanup(rest.Close)

	client := New(
		WithConfig(testConfig(failingGraphQL(t).URL, rest.URL)),
		WithTokenResolver(staticTokens("bad", "good")),
	)

	people, err := client.DiscoverPeople(context.Background())
	if err != nil {
		t.Fatalf("expected fallback to the second token, got: %v", err)
	}
	if len(people) != 1 || people[0].Login != "octocat" {
		t.Fatalf("people = %+v", people)
	}
	if calls != 2 {
		t.Fatalf("viewer lookups = %d, want 2 (one per token)", calls)
	}
}
