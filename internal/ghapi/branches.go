package ghapi

import (
	"context"
	"fmt"

	"github.com/shurcooL/githubv4"

	"gh-contribs/internal/contrib"
)

// GitHub's commit search index covers a repository's default branch and
// nothing else. Work that was pushed to a feature branch and has not landed --
// or has landed by a route that rewrote it, or never opened a pull request at
// all -- is not merely ranked low by that endpoint, it is absent from it. The
// report then shows zero commits for someone who has been committing all week.
//
// Measured against an Enterprise host over a trailing 30 days: /search/commits
// returned 0 commits for someone who had in fact made 5, every one of them on
// an unmerged feature branch. Neither branch involved had a pull request open,
// so deriving commits from pull requests would not have found them either, and
// /users/:user/events returned nothing, since it carries public events only
// and every repository involved is private.
//
// So this file walks branches directly. Two things keep that affordable
// rather than a crawl of every branch of every repository:
//
//   - Repositories are pruned by pushedAt. Only those written to inside the
//     window can hold a commit inside it -- 6 of 32 in that measurement.
//   - Within a repository, branches whose tip predates the window are skipped
//     without reading their history, since a branch holds nothing newer than
//     its own tip.
//
// The branches themselves cannot be pruned before they are fetched: GitHub
// offers no ordering that would put the recent ones first. See the note on
// branchCommitQuery.
//
// See docs/design/contribution-history.md.

const (
	// maxBranchRefPages bounds the branch walk per repository. Since branches
	// cannot be ordered by recency, this is a cap on a genuinely exhaustive
	// walk, and reaching it means some branches went unread -- which is logged.
	maxBranchRefPages = 4

	// branchRefPageSize mirrors the `first:` in branchCommitQuery's refs tag,
	// which must be a literal. It is used only to report truncation.
	branchRefPageSize = 100
)

// repoRef is a repository the branch scan intends to look at.
type repoRef struct {
	Owner string
	Name  string
}

func (r repoRef) String() string { return r.Owner + "/" + r.Name }

// searchBranchCommits collects the user's commits that live only on branches,
// across every repository in the viewer's organizations pushed to inside the
// window.
//
// A repository that cannot be read is skipped rather than failing the stream:
// the scan ranges over whole organizations, so one archived or restricted
// repository should not cost the report every other one.
func (c *Client) searchBranchCommits(ctx context.Context, gql *githubv4.Client, authorID githubv4.ID, window contrib.Range) ([]contrib.Event, error) {
	// The whole scan hangs off filtering history by author, and GraphQL will
	// not accept a null there. Report it rather than sending every commit on
	// every branch through as if the user had written it.
	if authorID == nil || authorID == "" {
		return nil, fmt.Errorf("no node ID for the user to attribute commits to")
	}

	repos, err := c.recentRepos(ctx, gql, window)
	if err != nil {
		return nil, fmt.Errorf("listing recently pushed repositories: %w", err)
	}
	c.logf("[gray]branch scan repos=%d[-]", len(repos))

	var events []contrib.Event
	for _, repo := range repos {
		found, err := c.branchCommits(ctx, gql, repo, authorID, window)
		if err != nil {
			c.logf("[yellow]branch scan skipped %s: %v[-]", repo, err)
			continue
		}
		events = append(events, found...)
	}
	return events, nil
}

// recentRepos returns the repositories across the viewer's organizations that
// were pushed to inside the window.
//
// Repositories come back ordered by push time, so the scan stops reading an
// organization's list at the first one that predates the window.
func (c *Client) recentRepos(ctx context.Context, gql *githubv4.Client, window contrib.Range) ([]repoRef, error) {
	var (
		repos  []repoRef
		cursor *githubv4.String
	)
	for i := 0; i < maxSearchPages; i++ {
		var q orgRepoQuery
		if err := gql.Query(ctx, &q, map[string]any{"orgCursor": cursor}); err != nil {
			return nil, err
		}
		for _, org := range q.Viewer.Organizations.Nodes {
			for _, repo := range org.Repositories.Nodes {
				if repo.PushedAt.Time.Before(window.From) {
					break
				}
				repos = append(repos, repoRef{
					Owner: string(repo.Owner.Login),
					Name:  string(repo.Name),
				})
			}
		}
		info := q.Viewer.Organizations.PageInfo
		if !bool(info.HasNextPage) {
			return repos, nil
		}
		end := info.EndCursor
		cursor = &end
	}
	c.logf("[yellow]branch scan saw only the first %d organizations[-]", maxSearchPages*25)
	return repos, nil
}

// branchCommits returns the user's commits on one repository's non-default
// branches, inside the window.
func (c *Client) branchCommits(ctx context.Context, gql *githubv4.Client, repo repoRef, authorID githubv4.ID, window contrib.Range) ([]contrib.Event, error) {
	var (
		events []contrib.Event
		cursor *githubv4.String
	)
	for page := 0; page < maxBranchRefPages; page++ {
		var q branchCommitQuery
		vars := map[string]any{
			"owner":  githubv4.String(repo.Owner),
			"name":   githubv4.String(repo.Name),
			"since":  githubv4.GitTimestamp{Time: window.From},
			"author": authorID,
			"cursor": cursor,
		}
		if err := gql.Query(ctx, &q, vars); err != nil {
			return nil, err
		}

		defaultBranch := string(q.Repository.DefaultBranchRef.Name)
		for _, ref := range q.Repository.Refs.Nodes {
			commit := ref.Target.Commit
			// A branch holds nothing newer than its own tip. Branches arrive
			// in alphabetical order, so an old one says nothing about the ones
			// after it -- skip this branch, keep reading.
			if commit.CommittedDate.Time.Before(window.From) {
				continue
			}
			if string(ref.Name) == defaultBranch {
				continue
			}
			for _, node := range commit.History.Nodes {
				events = append(events, contrib.Event{
					OccurredAt: node.AuthoredDate.Time,
					Kind:       contrib.KindCommit,
					Repo:       repo.String(),
					Title:      string(node.MessageHeadline),
					URL:        uriString(node.CommitURL),
					Branch:     string(ref.Name),
				})
			}
		}

		info := q.Repository.Refs.PageInfo
		if !bool(info.HasNextPage) {
			return events, nil
		}
		end := info.EndCursor
		cursor = &end
	}
	c.logf("[yellow]branch scan stopped at %d branches in %s; commits on older branches may be missing[-]",
		maxBranchRefPages*branchRefPageSize, repo)
	return events, nil
}
