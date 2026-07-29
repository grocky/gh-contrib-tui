package ghapi

import (
	"github.com/shurcooL/githubv4"
)

// The fragment types below define the GraphQL documents sent to GitHub: the
// githubv4 client derives each query from the struct layout and its tags, so
// field names and tags are the query. They are named (rather than inline
// anonymous structs) so the mapping to the domain model stays readable and
// testable.

// userQuery resolves a login, and distinguishes "no such user" from "this user
// simply has no activity" -- the search endpoints return an empty result set
// for both.
type userQuery struct {
	User struct {
		Login githubv4.String
		Name  githubv4.String
	} `graphql:"user(login: $login)"`
}

type repositoryFragment struct {
	NameWithOwner githubv4.String
}

// authoredSearchQuery finds the issues and pull requests a user opened. Both
// types come back from a single `type: ISSUE` search, discriminated by
// __typename.
type authoredSearchQuery struct {
	Search struct {
		PageInfo pageInfoFragment
		Nodes    []struct {
			Typename    githubv4.String `graphql:"__typename"`
			PullRequest struct {
				Title      githubv4.String
				URL        githubv4.URI
				CreatedAt  githubv4.DateTime
				Merged     githubv4.Boolean
				Repository repositoryFragment
			} `graphql:"... on PullRequest"`
			Issue struct {
				Title      githubv4.String
				URL        githubv4.URI
				CreatedAt  githubv4.DateTime
				Repository repositoryFragment
			} `graphql:"... on Issue"`
		}
	} `graphql:"search(type: ISSUE, query: $query, first: 100, after: $cursor)"`
}

// reviewSearchQuery finds pull requests the user reviewed, carrying each of
// their reviews with its submission time. Search can only filter by when the
// pull request was last updated, so the review timestamps are what actually
// place a review inside the queried window.
type reviewSearchQuery struct {
	Search struct {
		PageInfo pageInfoFragment
		Nodes    []struct {
			PullRequest struct {
				Title      githubv4.String
				URL        githubv4.URI
				Repository repositoryFragment
				Reviews    struct {
					Nodes []struct {
						SubmittedAt githubv4.DateTime
						URL         githubv4.URI
					}
				} `graphql:"reviews(first: 50, author: $login)"`
			} `graphql:"... on PullRequest"`
		}
	} `graphql:"search(type: ISSUE, query: $query, first: 100, after: $cursor)"`
}

// uriString renders a githubv4.URI, tolerating the null URLs GitHub returns
// for resources the token cannot see.
func uriString(u githubv4.URI) string {
	if u.URL == nil {
		return ""
	}
	return u.String()
}
