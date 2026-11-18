package ghapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/go-github/v66/github"

	"github.com/grocky/gh-contrib-tui/internal/auth"
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

func TestNew_DefaultsResolveFromEnvironment(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_HOSTNAME", "github.example.com")

	c := New()
	if got := c.Config().Host; got != "github.example.com" {
		t.Fatalf("Config().Host = %s, want github.example.com", got)
	}
}
