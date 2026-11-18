package ghapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"gh-contrib/internal/config"
	"gh-contrib/internal/contrib"
)

// scriptedGraphQL replies with the next payload in the list for each request,
// repeating the last one once exhausted, and records every request body.
type scriptedGraphQL struct {
	mu       sync.Mutex
	payloads []string
	bodies   []string
	calls    int
}

func (s *scriptedGraphQL) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	s.bodies = append(s.bodies, string(raw))
	idx := s.calls
	if idx >= len(s.payloads) {
		idx = len(s.payloads) - 1
	}
	s.calls++
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, s.payloads[idx])
}

func (s *scriptedGraphQL) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// variablesOf extracts the GraphQL variables from a recorded request body.
func variablesOf(t *testing.T, body string) map[string]any {
	t.Helper()
	var req struct {
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	return req.Variables
}

// peopleClient wires a Client to a GraphQL server and a REST server. Either URL
// may be empty, in which case that transport points at an always-404 server.
func peopleClient(t *testing.T, graphqlURL, restURL string) *Client {
	t.Helper()
	if restURL == "" {
		dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"Not Found"}`)
		}))
		t.Cleanup(dead.Close)
		restURL = dead.URL
	}
	return New(
		WithConfig(testConfig(graphqlURL, restURL)),
		WithTokenResolver(staticTokens("test-token")),
	)
}

// testConfig points both transports at the supplied test servers.
func testConfig(graphqlURL, restURL string) config.Config {
	return config.Config{
		Host:       "github.example.com",
		GraphQLURL: graphqlURL,
		RestBase:   restURL + "/api/v3/",
		UploadBase: restURL + "/api/uploads/",
	}
}

const viewerOnePagePayload = `{"data":{"viewer":{
	"login":"nash-robin",
	"name":"Nash, Robin [USA]",
	"organizations":{
		"pageInfo":{"hasNextPage":false,"endCursor":"ORG1"},
		"nodes":[{
			"login":"originops",
			"membersWithRole":{
				"totalCount":2,
				"pageInfo":{"hasNextPage":false,"endCursor":"M1"},
				"nodes":[
					{"login":"avery-lynn","name":"Avery, Lynn [USA]"},
					{"login":"nash-robin","name":"Nash, Robin [USA]"}
				]
			}
		}]
	}
}}}`

func TestDiscoverPeople_SinglePage(t *testing.T) {
	script := &scriptedGraphQL{payloads: []string{viewerOnePagePayload}}
	srv := httptest.NewServer(http.HandlerFunc(script.handler))
	t.Cleanup(srv.Close)

	people, err := peopleClient(t, srv.URL, "").DiscoverPeople(context.Background())
	if err != nil {
		t.Fatalf("DiscoverPeople returned error: %v", err)
	}

	want := []contrib.Person{
		{Login: "avery-lynn", Name: "Avery, Lynn [USA]", Orgs: []string{"originops"}, Active: true},
		{Login: "nash-robin", Name: "Nash, Robin [USA]", Orgs: []string{"originops"}, Active: true},
	}
	if !reflect.DeepEqual(people, want) {
		t.Fatalf("people = %+v\nwant %+v", people, want)
	}
}

func TestDiscoverPeople_PinsGraphQLDocument(t *testing.T) {
	script := &scriptedGraphQL{payloads: []string{viewerOnePagePayload}}
	srv := httptest.NewServer(http.HandlerFunc(script.handler))
	t.Cleanup(srv.Close)

	if _, err := peopleClient(t, srv.URL, "").DiscoverPeople(context.Background()); err != nil {
		t.Fatalf("DiscoverPeople returned error: %v", err)
	}

	var req struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(script.bodies[0]), &req); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	const want = `query($orgCursor:String){viewer{login,name,` +
		`organizations(first: 25, after: $orgCursor){pageInfo{hasNextPage,endCursor},` +
		`nodes{login,membersWithRole(first: 100){totalCount,pageInfo{hasNextPage,endCursor},nodes{login,name}}}}}}`
	if req.Query != want {
		t.Errorf("unexpected directory query:\n got: %s\nwant: %s", req.Query, want)
	}
}

func TestDiscoverPeople_FollowsMemberPages(t *testing.T) {
	firstPage := `{"data":{"viewer":{
		"login":"nash-robin","name":"Nash, Robin [USA]",
		"organizations":{
			"pageInfo":{"hasNextPage":false,"endCursor":"ORG1"},
			"nodes":[{"login":"originops","membersWithRole":{
				"totalCount":3,
				"pageInfo":{"hasNextPage":true,"endCursor":"CURSOR1"},
				"nodes":[{"login":"avery-lynn","name":"Avery, Lynn"}]
			}}]
		}}}}`
	secondPage := `{"data":{"organization":{"membersWithRole":{
		"totalCount":3,
		"pageInfo":{"hasNextPage":true,"endCursor":"CURSOR2"},
		"nodes":[{"login":"bergen-oscar","name":"Bergen, Oscar"}]
	}}}}`
	thirdPage := `{"data":{"organization":{"membersWithRole":{
		"totalCount":3,
		"pageInfo":{"hasNextPage":false,"endCursor":"CURSOR3"},
		"nodes":[{"login":"cortez-jonas","name":"Cortez, Jonas"}]
	}}}}`

	script := &scriptedGraphQL{payloads: []string{firstPage, secondPage, thirdPage}}
	srv := httptest.NewServer(http.HandlerFunc(script.handler))
	t.Cleanup(srv.Close)

	people, err := peopleClient(t, srv.URL, "").DiscoverPeople(context.Background())
	if err != nil {
		t.Fatalf("DiscoverPeople returned error: %v", err)
	}

	got := make([]string, 0, len(people))
	for _, p := range people {
		got = append(got, p.Login)
	}
	want := []string{"avery-lynn", "bergen-oscar", "cortez-jonas", "nash-robin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("logins = %v, want %v (all member pages merged)", got, want)
	}
	if script.requestCount() != 3 {
		t.Fatalf("graphql requests = %d, want 3", script.requestCount())
	}

	// The follow-up pages must target the org directly and carry the cursor.
	vars := variablesOf(t, script.bodies[1])
	if vars["org"] != "originops" || vars["memberCursor"] != "CURSOR1" {
		t.Fatalf("second request variables = %v, want org=originops memberCursor=CURSOR1", vars)
	}
	if vars := variablesOf(t, script.bodies[2]); vars["memberCursor"] != "CURSOR2" {
		t.Fatalf("third request memberCursor = %v, want CURSOR2", vars["memberCursor"])
	}
}

func TestDiscoverPeople_FollowsOrgPages(t *testing.T) {
	firstPage := `{"data":{"viewer":{
		"login":"nash-robin","name":"Nash, Robin",
		"organizations":{
			"pageInfo":{"hasNextPage":true,"endCursor":"ORGCURSOR"},
			"nodes":[{"login":"org-a","membersWithRole":{
				"totalCount":1,"pageInfo":{"hasNextPage":false,"endCursor":""},
				"nodes":[{"login":"alice","name":"Alice"}]}}]
		}}}}`
	secondPage := `{"data":{"viewer":{
		"login":"nash-robin","name":"Nash, Robin",
		"organizations":{
			"pageInfo":{"hasNextPage":false,"endCursor":""},
			"nodes":[{"login":"org-b","membersWithRole":{
				"totalCount":1,"pageInfo":{"hasNextPage":false,"endCursor":""},
				"nodes":[{"login":"bob","name":"Bob"}]}}]
		}}}}`

	script := &scriptedGraphQL{payloads: []string{firstPage, secondPage}}
	srv := httptest.NewServer(http.HandlerFunc(script.handler))
	t.Cleanup(srv.Close)

	people, err := peopleClient(t, srv.URL, "").DiscoverPeople(context.Background())
	if err != nil {
		t.Fatalf("DiscoverPeople returned error: %v", err)
	}
	if len(people) != 3 {
		t.Fatalf("expected alice, bob and the viewer, got %+v", people)
	}
	if vars := variablesOf(t, script.bodies[1]); vars["orgCursor"] != "ORGCURSOR" {
		t.Fatalf("second page orgCursor = %v, want ORGCURSOR", vars["orgCursor"])
	}
}

func TestDiscoverPeople_MergesOrgsForSharedMembers(t *testing.T) {
	payload := `{"data":{"viewer":{
		"login":"nash-robin","name":"Nash, Robin",
		"organizations":{
			"pageInfo":{"hasNextPage":false,"endCursor":""},
			"nodes":[
				{"login":"org-a","membersWithRole":{"totalCount":1,"pageInfo":{"hasNextPage":false,"endCursor":""},
					"nodes":[{"login":"alice","name":"Alice"}]}},
				{"login":"org-b","membersWithRole":{"totalCount":1,"pageInfo":{"hasNextPage":false,"endCursor":""},
					"nodes":[{"login":"alice","name":"Alice"}]}}
			]
		}}}}`
	script := &scriptedGraphQL{payloads: []string{payload}}
	srv := httptest.NewServer(http.HandlerFunc(script.handler))
	t.Cleanup(srv.Close)

	people, err := peopleClient(t, srv.URL, "").DiscoverPeople(context.Background())
	if err != nil {
		t.Fatalf("DiscoverPeople returned error: %v", err)
	}
	for _, p := range people {
		if p.Login != "alice" {
			continue
		}
		if !reflect.DeepEqual(p.Orgs, []string{"org-a", "org-b"}) {
			t.Fatalf("alice orgs = %v, want both orgs", p.Orgs)
		}
		return
	}
	t.Fatal("alice missing from the results")
}

func TestDiscoverPeople_MergesTeamOnlyMembersAsInactive(t *testing.T) {
	script := &scriptedGraphQL{payloads: []string{viewerOnePagePayload}}
	gql := httptest.NewServer(http.HandlerFunc(script.handler))
	t.Cleanup(gql.Close)

	// Bianchi-Priya is on a team but suspended, so the org member list omits
	// them -- the REST team walk is what surfaces the account.
	rest := restServer(t, map[string]string{
		"/api/v3/user/teams":                      `[{"id":42,"name":"core","organization":{"id":7,"login":"originops"}}]`,
		"/api/v3/organizations/7/team/42/members": `[{"login":"Bianchi-Priya"},{"login":"avery-lynn"}]`,
	})

	people, err := peopleClient(t, gql.URL, rest.URL).DiscoverPeople(context.Background())
	if err != nil {
		t.Fatalf("DiscoverPeople returned error: %v", err)
	}

	byLogin := map[string]contrib.Person{}
	for _, p := range people {
		byLogin[p.Login] = p
	}
	suspended, ok := byLogin["Bianchi-Priya"]
	if !ok {
		t.Fatalf("team-only member missing from results: %+v", people)
	}
	if suspended.Active {
		t.Error("team-only member should be marked inactive")
	}
	if !reflect.DeepEqual(suspended.Orgs, []string{"originops"}) {
		t.Errorf("team-only member orgs = %v", suspended.Orgs)
	}
	if member, ok := byLogin["avery-lynn"]; !ok || !member.Active {
		t.Errorf("an org member also on a team must stay active, got %+v", member)
	}
}

func TestDiscoverPeople_TeamFailureIsNonFatal(t *testing.T) {
	script := &scriptedGraphQL{payloads: []string{viewerOnePagePayload}}
	gql := httptest.NewServer(http.HandlerFunc(script.handler))
	t.Cleanup(gql.Close)

	rest := restServer(t, map[string]string{
		"/api/v3/user/teams": `403|{"message":"Forbidden"}`,
	})

	people, err := peopleClient(t, gql.URL, rest.URL).DiscoverPeople(context.Background())
	if err != nil {
		t.Fatalf("a team failure must not fail discovery, got: %v", err)
	}
	if len(people) != 2 {
		t.Fatalf("expected the GraphQL results to survive, got %+v", people)
	}
}

func TestDiscoverPeople_FallsBackToRESTWhenGraphQLFails(t *testing.T) {
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"errors":[{"message":"Field 'membersWithRole' doesn't exist"}]}`)
	}))
	t.Cleanup(gql.Close)

	rest := restServer(t, map[string]string{
		"/api/v3/user":                            `{"login":"nash-robin","name":"Nash, Robin [USA]"}`,
		"/api/v3/user/orgs":                       `[{"login":"originops"}]`,
		"/api/v3/orgs/originops/members":          `[{"login":"avery-lynn"}]`,
		"/api/v3/user/teams":                      `[{"id":42,"name":"core","organization":{"id":7,"login":"originops"}}]`,
		"/api/v3/organizations/7/team/42/members": `[{"login":"Bianchi-Priya"}]`,
	})

	people, err := peopleClient(t, gql.URL, rest.URL).DiscoverPeople(context.Background())
	if err != nil {
		t.Fatalf("expected the REST fallback to succeed, got: %v", err)
	}

	got := make([]string, 0, len(people))
	for _, p := range people {
		got = append(got, p.Login)
	}
	want := []string{"avery-lynn", "nash-robin", "Bianchi-Priya"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("logins = %v, want %v (active first, then the suspended team member)", got, want)
	}
}

func TestDiscoverPeople_ErrorsWhenEverythingFails(t *testing.T) {
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"errors":[{"message":"nope"}]}`)
	}))
	t.Cleanup(gql.Close)

	_, err := peopleClient(t, gql.URL, "").DiscoverPeople(context.Background())
	if err == nil {
		t.Fatal("expected an error when no source yields anyone")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v, want it to carry the underlying cause", err)
	}
}

func TestDiscoverPeople_RESTPaginationIsFollowed(t *testing.T) {
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"errors":[{"message":"forced fallback"}]}`)
	}))
	t.Cleanup(gql.Close)

	var restBase string
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v3/user":
			io.WriteString(w, `{"login":"viewer"}`)
		case r.URL.Path == "/api/v3/user/orgs":
			io.WriteString(w, `[{"login":"originops"}]`)
		case r.URL.Path == "/api/v3/orgs/originops/members" && r.URL.Query().Get("page") == "":
			w.Header().Set("Link", `<`+restBase+`/api/v3/orgs/originops/members?page=2>; rel="next"`)
			io.WriteString(w, `[{"login":"page1-user"}]`)
		case r.URL.Path == "/api/v3/orgs/originops/members":
			io.WriteString(w, `[{"login":"page2-user"}]`)
		default:
			io.WriteString(w, `[]`)
		}
	}))
	t.Cleanup(rest.Close)
	restBase = rest.URL

	people, err := peopleClient(t, gql.URL, rest.URL).DiscoverPeople(context.Background())
	if err != nil {
		t.Fatalf("DiscoverPeople returned error: %v", err)
	}

	found := map[string]bool{}
	for _, p := range people {
		found[p.Login] = true
	}
	if !found["page1-user"] || !found["page2-user"] {
		t.Fatalf("expected both member pages, got %+v", people)
	}
}

func TestDiscoverPeople_LogsWhenNoOrgsAreVisible(t *testing.T) {
	// A token without read:org yields a valid viewer and an empty org list --
	// no error, just nobody but yourself. That must be called out, because it
	// is otherwise indistinguishable from genuinely having no teammates.
	payload := `{"data":{"viewer":{
		"login":"nash-robin","name":"Nash, Robin [USA]",
		"organizations":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]}
	}}}`
	script := &scriptedGraphQL{payloads: []string{payload}}
	srv := httptest.NewServer(http.HandlerFunc(script.handler))
	t.Cleanup(srv.Close)

	var logged []string
	client := peopleClient(t, srv.URL, "")
	WithLogger(func(format string, args ...any) {
		logged = append(logged, fmt.Sprintf(format, args...))
	})(client)

	people, err := client.DiscoverPeople(context.Background())
	if err != nil {
		t.Fatalf("DiscoverPeople returned error: %v", err)
	}
	if len(people) != 1 {
		t.Fatalf("people = %+v, want just the viewer", people)
	}

	all := strings.Join(logged, "\n")
	if !strings.Contains(all, "organizations=0") {
		t.Errorf("expected the org count to be logged, got:\n%s", all)
	}
	if !strings.Contains(all, "read:org") || !strings.Contains(all, "GITHUB_TOKEN") {
		t.Errorf("expected a hint naming the scope and the token precedence, got:\n%s", all)
	}
}
