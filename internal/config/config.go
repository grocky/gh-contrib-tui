// Package config resolves which GitHub deployment to talk to (github.com or a
// GitHub Enterprise host) and the API endpoints that follow from it.
package config

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Config is the resolved GitHub deployment the app talks to.
type Config struct {
	Host       string
	RestBase   string
	UploadBase string
	GraphQLURL string
}

// IsDotcom reports whether the config points at public github.com, which the
// GitHub client libraries construct differently from Enterprise hosts.
func (c Config) IsDotcom() bool {
	switch c.Host {
	case "github.com", "www.github.com", "api.github.com":
		return true
	default:
		return false
	}
}

// Resolve determines the GitHub host from GITHUB_HOSTNAME, then GH_HOST, then
// `gh auth status`, falling back to github.com, and derives its endpoints.
func Resolve() Config {
	host := strings.TrimSpace(os.Getenv("GITHUB_HOSTNAME"))
	if host == "" {
		host = resolveHost()
	}
	if host == "" {
		host = "github.com"
	}
	rest, upload, gql := BuildEndpoints(host)
	return Config{
		Host:       host,
		RestBase:   rest,
		UploadBase: upload,
		GraphQLURL: gql,
	}
}

// BuildEndpoints returns the REST, upload, and GraphQL endpoints for a host.
func BuildEndpoints(host string) (rest, upload, graphql string) {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimSuffix(host, "/")

	switch host {
	case "github.com", "www.github.com", "api.github.com":
		return "https://api.github.com/", "https://uploads.github.com/", "https://api.github.com/graphql"
	default:
		return fmt.Sprintf("https://%s/api/v3/", host),
			fmt.Sprintf("https://%s/api/uploads/", host),
			fmt.Sprintf("https://%s/api/graphql", host)
	}
}

// resolveHost reads GH_HOST, then falls back to scraping `gh auth status`.
func resolveHost() string {
	if h := strings.TrimSpace(os.Getenv("GH_HOST")); h != "" {
		return h
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return ""
	}
	out, err := exec.Command("gh", "auth", "status").CombinedOutput()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(stripANSICodes(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "✓") || strings.HasPrefix(line, "-") {
			continue
		}
		if strings.Contains(line, ".") && !strings.Contains(line, " ") {
			return line
		}
	}
	return ""
}

// stripANSICodes removes CSI escape sequences so `gh` output can be parsed
// even when it decides to colorize.
func stripANSICodes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) {
				c := s[i]
				i++
				if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
					break
				}
			}
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
