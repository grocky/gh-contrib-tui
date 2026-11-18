package ghapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/shurcooL/githubv4"

	"github.com/grocky/gh-contrib-tui/internal/contrib"
)

// viewerQuery identifies the account a token authenticates as.
type viewerQuery struct {
	Viewer struct {
		Login githubv4.String
		Name  githubv4.String
	}
}

// Viewer returns the account the client is authenticated as. It is what a first
// launch seeds the team with: whoever is running the app is on their own team.
//
// GraphQL answers first and carries the display name; the REST fallback covers
// deployments where GraphQL is unavailable, the same split DiscoverPeople uses.
func (c *Client) Viewer(ctx context.Context) (contrib.Person, error) {
	person, gqlErr := c.graphqlViewer(ctx)
	if gqlErr == nil {
		return person, nil
	}
	c.logf("[yellow]graphql viewer unavailable, falling back to REST: %v[-]", gqlErr)

	person, restErr := c.restViewer(ctx)
	if restErr == nil {
		return person, nil
	}
	return contrib.Person{}, fmt.Errorf("resolving the authenticated user: %w",
		errors.Join(gqlErr, restErr))
}

func (c *Client) graphqlViewer(ctx context.Context) (contrib.Person, error) {
	return withTokens(c, ctx, "viewer", func(httpClient *http.Client) (contrib.Person, error) {
		var q viewerQuery
		if err := c.newGraphQLClient(httpClient).Query(ctx, &q, nil); err != nil {
			return contrib.Person{}, err
		}
		return newViewer(string(q.Viewer.Login), string(q.Viewer.Name))
	})
}

func (c *Client) restViewer(ctx context.Context) (contrib.Person, error) {
	return withTokens(c, ctx, "viewer-rest", func(httpClient *http.Client) (contrib.Person, error) {
		client, err := c.newRESTClient(httpClient)
		if err != nil {
			return contrib.Person{}, err
		}
		self, _, err := client.Users.Get(ctx, "")
		if err != nil {
			return contrib.Person{}, err
		}
		return newViewer(self.GetLogin(), self.GetName())
	})
}

// newViewer rejects an empty login. A token that authenticates but resolves to
// nobody would otherwise seed the team with a row that can never load.
func newViewer(login, name string) (contrib.Person, error) {
	if login == "" {
		return contrib.Person{}, errors.New("the token authenticated but resolved to no account")
	}
	return contrib.Person{Login: login, Name: name, Active: true}, nil
}
