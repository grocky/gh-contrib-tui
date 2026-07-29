// Package ghapi talks to the GitHub GraphQL and REST APIs and returns domain
// values from the contrib package. It owns credential fallback: every request
// walks the available token candidates until one authenticates.
package ghapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-github/v66/github"
	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"

	"gh-contribs/internal/auth"
	"gh-contribs/internal/config"
	"gh-contribs/internal/contrib"
	"gh-contribs/internal/logging"
)

// ErrUserNotFound is returned when the queried login is missing or the token
// cannot see it.
var ErrUserNotFound = errors.New("user not found or inaccessible")

// TokenResolver supplies the ordered token candidates a Client will try.
type TokenResolver func() ([]auth.Candidate, error)

// Client queries GitHub for contribution data and user suggestions.
type Client struct {
	cfg    config.Config
	tokens TokenResolver
	logf   logging.Logf
}

// Option customizes a Client.
type Option func(*Client)

// WithConfig overrides the auto-resolved GitHub deployment.
func WithConfig(cfg config.Config) Option {
	return func(c *Client) { c.cfg = cfg }
}

// WithTokenResolver overrides how tokens are discovered.
func WithTokenResolver(r TokenResolver) Option {
	return func(c *Client) { c.tokens = r }
}

// WithLogger attaches a progress logger.
func WithLogger(logf logging.Logf) Option {
	return func(c *Client) { c.logf = logging.OrNop(logf) }
}

// New builds a Client. By default it resolves the GitHub host from the
// environment and discovers tokens from the environment and the gh CLI.
func New(opts ...Option) *Client {
	c := &Client{
		cfg:    config.Resolve(),
		tokens: auth.ResolveCandidates,
		logf:   logging.Nop,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Config reports the resolved GitHub deployment.
func (c *Client) Config() config.Config { return c.cfg }

// FetchContributions returns the contribution report for a user over a range.
func (c *Client) FetchContributions(ctx context.Context, username string, window contrib.Range) (*contrib.Report, error) {
	c.logf("[gray]rest base=%s[-]", c.cfg.RestBase)
	c.logf("[gray]graphql endpoint=%s[-]", c.cfg.GraphQLURL)
	c.logf("[gray]host source=%s[-]", c.cfg.Host)

	vars := map[string]any{
		"login": githubv4.String(username),
		"from":  githubv4.DateTime{Time: window.From},
		"to":    githubv4.DateTime{Time: window.To},
	}

	report, err := withTokens(c, ctx, "contributions", func(httpClient *http.Client) (*contrib.Report, error) {
		var q contributionsQuery
		if err := c.newGraphQLClient(httpClient).Query(ctx, &q, vars); err != nil {
			return nil, fmt.Errorf("graphql query failed: %w", err)
		}
		if q.User.Login == "" {
			return nil, ErrUserNotFound
		}
		return q.toReport(), nil
	})
	return report, err
}

// withTokens runs call against each token candidate in turn, advancing to the
// next candidate only when the failure looks like an authentication problem.
func withTokens[T any](c *Client, ctx context.Context, label string, call func(*http.Client) (T, error)) (T, error) {
	var zero T
	candidates, err := c.tokens()
	if err != nil {
		return zero, err
	}
	for i, candidate := range candidates {
		c.logf("[gray]%s token source=%s (attempt %d/%d) fingerprint=%s[-]",
			label, candidate.Source, i+1, len(candidates), candidate.Fingerprint())

		httpClient := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: candidate.Token}))
		result, err := call(httpClient)
		if err == nil {
			return result, nil
		}
		if isUnauthorized(err) && i < len(candidates)-1 {
			c.logf("[yellow]%s unauthorized with %s, trying next token source[-]", label, candidate.Source)
			continue
		}
		return zero, err
	}
	return zero, fmt.Errorf("%s failed for all token sources; refresh credentials", label)
}

func (c *Client) newGraphQLClient(httpClient *http.Client) *githubv4.Client {
	if c.cfg.IsDotcom() {
		return githubv4.NewClient(httpClient)
	}
	return githubv4.NewEnterpriseClient(c.cfg.GraphQLURL, httpClient)
}

func (c *Client) newRESTClient(httpClient *http.Client) (*github.Client, error) {
	if c.cfg.IsDotcom() {
		return github.NewClient(httpClient), nil
	}
	return github.NewEnterpriseClient(c.cfg.RestBase, c.cfg.UploadBase, httpClient)
}

// isUnauthorized reports whether an API error looks like a credential problem
// worth retrying with a different token.
func isUnauthorized(err error) bool {
	if err == nil {
		return false
	}
	var errResp *github.ErrorResponse
	if errors.As(err, &errResp) && errResp.Response != nil {
		return errResp.Response.StatusCode == http.StatusUnauthorized
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "401") ||
		strings.Contains(s, "unauthorized") ||
		strings.Contains(s, "bad credentials")
}
