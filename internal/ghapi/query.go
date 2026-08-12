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
// The node ID is what the branch scan filters commit history by: matching on
// the account rather than on a commit's author email, exactly as commit search
// does, so the two streams agree on whose work a commit is.
type userQuery struct {
	User struct {
		ID    githubv4.ID
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

// orgRepoQuery lists the viewer's organizations with their repositories most
// recently pushed to first, which is what lets the branch scan skip the great
// majority of repositories without asking about their branches at all.
type orgRepoQuery struct {
	Viewer struct {
		Organizations struct {
			PageInfo pageInfoFragment
			Nodes    []struct {
				Repositories struct {
					Nodes []struct {
						Name     githubv4.String
						PushedAt githubv4.DateTime
						Owner    struct {
							Login githubv4.String
						}
					}
				} `graphql:"repositories(first: 100, orderBy: {field: PUSHED_AT, direction: DESC})"`
			}
		} `graphql:"organizations(first: 25, after: $orgCursor)"`
	}
}

// branchCommitQuery walks one repository's branches, carrying the commits each
// one holds that the given user authored inside the window.
//
// A branch's history includes every ancestor, so the same commit arrives once
// per branch built on top of it; the domain layer's dedupe collapses them.
// The default branch is named so the scan can skip it -- commit search already
// covers it, and a landed commit should not be labelled with a branch.
//
// The ordering is ALPHABETICAL because it is the only one that means anything
// here. RefOrderField's other value, TAG_COMMIT_DATE, does not order branches
// by their commit date: asked for it DESC, an Enterprise host returned refs in
// reverse alphabetical order, tips ranging over two years in no date order at
// all. Any attempt to stop the walk early on "the tips are old
// now" is therefore unsound, and the scan reads every branch.
//
// `since` bounds the walk by committer date, which is not the author date the
// window is measured in. It is deliberately the looser of the two: rebasing
// moves a commit's committer date forward, never back, so anything authored
// inside the window is still caught here and the domain layer drops whatever
// falls outside on the author date -- the same date commit search filters on.
// There is no `until` for the same reason: a commit authored inside the window
// can be rebased after it closes.
type branchCommitQuery struct {
	Repository struct {
		DefaultBranchRef struct {
			Name githubv4.String
		}
		Refs struct {
			PageInfo pageInfoFragment
			Nodes    []struct {
				Name   githubv4.String
				Target struct {
					Commit struct {
						CommittedDate githubv4.DateTime
						History       struct {
							Nodes []struct {
								AuthoredDate    githubv4.DateTime
								MessageHeadline githubv4.String
								CommitURL       githubv4.URI
							}
						} `graphql:"history(since: $since, author: {id: $author}, first: 100)"`
					} `graphql:"... on Commit"`
				}
			}
		} `graphql:"refs(refPrefix: \"refs/heads/\", first: 100, after: $cursor, orderBy: {field: ALPHABETICAL, direction: ASC})"`
	} `graphql:"repository(owner: $owner, name: $name)"`
}

// uriString renders a githubv4.URI, tolerating the null URLs GitHub returns
// for resources the token cannot see.
func uriString(u githubv4.URI) string {
	if u.URL == nil {
		return ""
	}
	return u.String()
}
