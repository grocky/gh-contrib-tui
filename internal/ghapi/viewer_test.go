package ghapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const viewerPayload = `{"data":{"viewer":{"login":"nash-robin","name":"Nash, Robin [USA]"}}}`

func TestViewer_ReturnsTheAuthenticatedAccount(t *testing.T) {
	script := &scriptedGraphQL{payloads: []string{viewerPayload}}
	srv := httptest.NewServer(http.HandlerFunc(script.handler))
	t.Cleanup(srv.Close)

	viewer, err := peopleClient(t, srv.URL, "").Viewer(context.Background())
	if err != nil {
		t.Fatalf("Viewer returned error: %v", err)
	}
	if viewer.Login != "nash-robin" {
		t.Errorf("login = %q, want nash-robin", viewer.Login)
	}
	if viewer.Name != "Nash, Robin [USA]" {
		t.Errorf("name = %q, want the display name", viewer.Name)
	}
	if !viewer.Active {
		t.Error("the account we are authenticated as is not marked active")
	}
}

// A token that authenticates but resolves to nobody is a broken credential, not
// an anonymous viewer: seeding a team with an empty login would put a row on the
// dashboard that can never load.
func TestViewer_EmptyLoginIsAnError(t *testing.T) {
	script := &scriptedGraphQL{payloads: []string{`{"data":{"viewer":{"login":"","name":""}}}`}}
	srv := httptest.NewServer(http.HandlerFunc(script.handler))
	t.Cleanup(srv.Close)

	if _, err := peopleClient(t, srv.URL, "").Viewer(context.Background()); err == nil {
		t.Fatal("Viewer accepted an empty login, want an error")
	}
}

// GraphQL is disabled on some Enterprise deployments, which is exactly where
// the REST fallback for discovery already earns its keep.
func TestViewer_FallsBackToREST(t *testing.T) {
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"Not Found"}`)
	}))
	t.Cleanup(gql.Close)

	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/user") {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"Not Found"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"login":"nash-robin","name":"Nash, Robin [USA]"}`)
	}))
	t.Cleanup(rest.Close)

	viewer, err := peopleClient(t, gql.URL, rest.URL).Viewer(context.Background())
	if err != nil {
		t.Fatalf("Viewer returned error: %v", err)
	}
	if viewer.Login != "nash-robin" {
		t.Errorf("login = %q, want the REST fallback to have answered", viewer.Login)
	}
}

func TestViewer_ReportsWhenNeitherAPIAnswers(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"message":"boom"}`)
	}))
	t.Cleanup(dead.Close)

	if _, err := peopleClient(t, dead.URL, dead.URL).Viewer(context.Background()); err == nil {
		t.Fatal("Viewer succeeded with both APIs down, want an error")
	}
}
