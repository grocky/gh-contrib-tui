package ghapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"gh-contribs/internal/config"
	"gh-contribs/internal/logging"
)

// restServer serves a fake GitHub Enterprise REST API from a path -> JSON map.
// Paths absent from the map return 404.
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

func discoveryClient(t *testing.T, srv *httptest.Server, tokens ...string) *Client {
	t.Helper()
	if len(tokens) == 0 {
		tokens = []string{"test-token"}
	}
	return New(
		WithConfig(config.Config{
			Host:       "github.example.com",
			RestBase:   srv.URL + "/api/v3/",
			UploadBase: srv.URL + "/api/uploads/",
		}),
		WithTokenResolver(staticTokens(tokens...)),
		WithLogger(logging.Nop),
	)
}

func TestDiscoverUserLogins_MergesViewerOrgsAndTeams(t *testing.T) {
	srv := restServer(t, map[string]string{
		"/api/v3/user":                            `{"login":"octocat"}`,
		"/api/v3/user/orgs":                       `[{"login":"acme"}]`,
		"/api/v3/orgs/acme/members":               `[{"login":"bob"},{"login":"alice"}]`,
		"/api/v3/user/teams":                      `[{"id":42,"name":"core","organization":{"id":7}}]`,
		"/api/v3/organizations/7/team/42/members": `[{"login":"carol"},{"login":"alice"}]`,
	})

	got, err := discoveryClient(t, srv).DiscoverUserLogins(context.Background())
	if err != nil {
		t.Fatalf("DiscoverUserLogins returned error: %v", err)
	}
	want := []string{"alice", "bob", "carol", "octocat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("logins = %v, want %v (deduped and sorted)", got, want)
	}
}

func TestDiscoverUserLogins_SkipsUnreadableOrgsAndTeams(t *testing.T) {
	srv := restServer(t, map[string]string{
		"/api/v3/user":              `{"login":"octocat"}`,
		"/api/v3/user/orgs":         `[{"login":"acme"},{"login":"secret"}]`,
		"/api/v3/orgs/acme/members": `[{"login":"alice"}]`,
		// secret's members and the teams endpoint both 403.
		"/api/v3/orgs/secret/members": `403|{"message":"Forbidden"}`,
		"/api/v3/user/teams":          `403|{"message":"Forbidden"}`,
	})

	var logged []string
	client := discoveryClient(t, srv)
	WithLogger(func(format string, args ...any) { logged = append(logged, format) })(client)

	got, err := client.DiscoverUserLogins(context.Background())
	if err != nil {
		t.Fatalf("partial failures must not fail discovery, got: %v", err)
	}
	want := []string{"alice", "octocat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("logins = %v, want %v", got, want)
	}
	if len(logged) == 0 {
		t.Fatal("expected the skipped org/team to be logged")
	}
}

func TestDiscoverUserLogins_ViewerFailureIsFatal(t *testing.T) {
	srv := restServer(t, map[string]string{})

	_, err := discoveryClient(t, srv).DiscoverUserLogins(context.Background())
	if err == nil {
		t.Fatal("expected an error when the viewer lookup fails")
	}
	if !strings.Contains(err.Error(), "discover viewer failed") {
		t.Fatalf("err = %v, want it to name the viewer lookup", err)
	}
}

func TestDiscoverUserLogins_OrgListFailureIsFatal(t *testing.T) {
	srv := restServer(t, map[string]string{
		"/api/v3/user": `{"login":"octocat"}`,
	})

	_, err := discoveryClient(t, srv).DiscoverUserLogins(context.Background())
	if err == nil {
		t.Fatal("expected an error when the org listing fails")
	}
	if !strings.Contains(err.Error(), "discover orgs failed") {
		t.Fatalf("err = %v, want it to name the org listing", err)
	}
}

func TestDiscoverUserLogins_FallsBackToNextTokenOn401(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/user" {
			calls++
			if strings.Contains(r.Header.Get("Authorization"), "bad") {
				w.WriteHeader(http.StatusUnauthorized)
				io.WriteString(w, `{"message":"Bad credentials"}`)
				return
			}
			io.WriteString(w, `{"login":"octocat"}`)
			return
		}
		if r.URL.Path == "/api/v3/user/orgs" {
			io.WriteString(w, `[]`)
			return
		}
		io.WriteString(w, `[]`)
	}))
	t.Cleanup(srv.Close)

	got, err := discoveryClient(t, srv, "bad", "good").DiscoverUserLogins(context.Background())
	if err != nil {
		t.Fatalf("expected fallback to the second token, got: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"octocat"}) {
		t.Fatalf("logins = %v", got)
	}
	if calls != 2 {
		t.Fatalf("viewer lookups = %d, want 2 (one per token)", calls)
	}
}
