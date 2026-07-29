package config

import "testing"

func TestBuildEndpoints(t *testing.T) {
	tests := []struct {
		name                  string
		host                  string
		rest, upload, graphql string
	}{
		{
			name:    "enterprise",
			host:    "github.example.com",
			rest:    "https://github.example.com/api/v3/",
			upload:  "https://github.example.com/api/uploads/",
			graphql: "https://github.example.com/api/graphql",
		},
		{
			name:    "dotcom",
			host:    "github.com",
			rest:    "https://api.github.com/",
			upload:  "https://uploads.github.com/",
			graphql: "https://api.github.com/graphql",
		},
		{
			name:    "api subdomain is dotcom",
			host:    "api.github.com",
			rest:    "https://api.github.com/",
			upload:  "https://uploads.github.com/",
			graphql: "https://api.github.com/graphql",
		},
		{
			name:    "normalizes scheme case and trailing slash",
			host:    "HTTPS://GitHub.Example.com/",
			rest:    "https://github.example.com/api/v3/",
			upload:  "https://github.example.com/api/uploads/",
			graphql: "https://github.example.com/api/graphql",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rest, upload, graphql := BuildEndpoints(tc.host)
			if rest != tc.rest {
				t.Errorf("rest = %s, want %s", rest, tc.rest)
			}
			if upload != tc.upload {
				t.Errorf("upload = %s, want %s", upload, tc.upload)
			}
			if graphql != tc.graphql {
				t.Errorf("graphql = %s, want %s", graphql, tc.graphql)
			}
		})
	}
}

func TestConfigIsDotcom(t *testing.T) {
	for _, host := range []string{"github.com", "www.github.com", "api.github.com"} {
		if !(Config{Host: host}).IsDotcom() {
			t.Errorf("IsDotcom(%s) = false, want true", host)
		}
	}
	if (Config{Host: "github.example.com"}).IsDotcom() {
		t.Error("IsDotcom(github.example.com) = true, want false")
	}
}

func TestResolve_UsesGitHubHostnameEnv(t *testing.T) {
	t.Setenv("GITHUB_HOSTNAME", "github.example.com")
	t.Setenv("PATH", "") // keep `gh` out of the resolution path

	got := Resolve()
	if got.Host != "github.example.com" {
		t.Fatalf("Host = %s, want github.example.com", got.Host)
	}
	if got.GraphQLURL != "https://github.example.com/api/graphql" {
		t.Fatalf("GraphQLURL = %s", got.GraphQLURL)
	}
	if got.IsDotcom() {
		t.Fatal("expected enterprise host")
	}
}

func TestResolve_FallsBackToGHHostThenDotcom(t *testing.T) {
	t.Setenv("PATH", "")

	t.Setenv("GITHUB_HOSTNAME", "")
	t.Setenv("GH_HOST", "github.example.com")
	if got := Resolve(); got.Host != "github.example.com" {
		t.Fatalf("Host = %s, want GH_HOST value", got.Host)
	}

	t.Setenv("GH_HOST", "")
	got := Resolve()
	if got.Host != "github.com" {
		t.Fatalf("Host = %s, want github.com fallback", got.Host)
	}
	if !got.IsDotcom() {
		t.Fatal("expected dotcom fallback")
	}
}

func TestStripANSICodes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "github.example.com", want: "github.example.com"},
		{name: "colored", in: "\x1b[0;32m✓\x1b[0m Logged in to github.example.com", want: "✓ Logged in to github.example.com"},
		{name: "trailing escape", in: "host\x1b[", want: "host"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripANSICodes(tc.in); got != tc.want {
				t.Fatalf("stripANSICodes(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
