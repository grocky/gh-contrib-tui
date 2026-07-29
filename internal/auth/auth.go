// Package auth discovers GitHub API tokens from the environment and the gh CLI.
package auth

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Candidate is a token paired with a human-readable description of where it
// came from, so failures can name the source that failed.
type Candidate struct {
	Source string
	Token  string
}

// Fingerprint renders a token safe for logging.
func (c Candidate) Fingerprint() string {
	if len(c.Token) <= 8 {
		return fmt.Sprintf("len:%d", len(c.Token))
	}
	return fmt.Sprintf("%s...%s(len:%d)", c.Token[:4], c.Token[len(c.Token)-4:], len(c.Token))
}

// ResolveCandidates collects tokens from GITHUB_TOKEN, GH_TOKEN, and
// `gh auth token`, in that order, deduplicated by token value.
func ResolveCandidates() ([]Candidate, error) {
	candidates := make([]Candidate, 0, 3)
	for _, env := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if tok := strings.TrimSpace(os.Getenv(env)); tok != "" {
			candidates = append(candidates, Candidate{Source: env, Token: tok})
		}
	}
	if tok := ghCLIToken(); tok != "" {
		candidates = append(candidates, Candidate{Source: "gh auth token", Token: tok})
	}
	if len(candidates) == 0 {
		return nil, errors.New("no token found; set GITHUB_TOKEN or GH_TOKEN, or run `gh auth login`")
	}
	return Dedupe(candidates), nil
}

// Dedupe drops candidates whose token was already seen, keeping first-seen order.
func Dedupe(in []Candidate) []Candidate {
	seen := make(map[string]struct{}, len(in))
	out := make([]Candidate, 0, len(in))
	for _, c := range in {
		if _, ok := seen[c.Token]; ok {
			continue
		}
		seen[c.Token] = struct{}{}
		out = append(out, c)
	}
	return out
}

func ghCLIToken() string {
	if _, err := exec.LookPath("gh"); err != nil {
		return ""
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
