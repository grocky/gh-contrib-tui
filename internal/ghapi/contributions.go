package ghapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v66/github"
	"github.com/shurcooL/githubv4"

	"gh-contribs/internal/contrib"
)

// Contribution history is assembled from the search APIs rather than from
// GraphQL's contributionsCollection.
//
// contributionsCollection ignores activity in private and internal
// repositories -- on GitHub Enterprise Server it does not even report it as
// restricted, so the omission is invisible. Measured against
// github.example.com, a user with 237 contributions on their profile
// reported 3 through that endpoint, because every repository they work in is
// private or internal. The search APIs honour the token's actual read access
// instead, so a `repo`-scoped token sees the real history. See
// docs/design/contribution-history.md.

const (
	// searchPageSize is the maximum GitHub allows for a search page.
	searchPageSize = 100

	// maxSearchPages bounds each search stream. Hitting it truncates the
	// report, which is always logged -- a silently short report is exactly the
	// failure mode this file exists to fix.
	maxSearchPages = 10
)

// FetchContributions returns the contribution report for a user over a range.
func (c *Client) FetchContributions(ctx context.Context, username string, window contrib.Range) (*contrib.Report, error) {
	c.logf("[gray]rest base=%s[-]", c.cfg.RestBase)
	c.logf("[gray]graphql endpoint=%s[-]", c.cfg.GraphQLURL)
	c.logf("[gray]host source=%s[-]", c.cfg.Host)

	return withTokens(c, ctx, "contributions", func(httpClient *http.Client) (*contrib.Report, error) {
		gql := c.newGraphQLClient(httpClient)

		report, authorID, err := c.resolveUser(ctx, gql, username)
		if err != nil {
			return nil, err
		}

		var events []contrib.Event

		authored, err := c.searchAuthored(ctx, gql, report.Login, window)
		if err != nil {
			return nil, fmt.Errorf("searching authored pull requests and issues: %w", err)
		}
		events = append(events, authored...)

		reviewed, err := c.searchReviews(ctx, gql, report.Login, window)
		if err != nil {
			return nil, fmt.Errorf("searching reviews: %w", err)
		}
		events = append(events, reviewed...)

		commits, err := c.searchCommits(ctx, httpClient, report.Login, window)
		switch {
		case err == nil:
			events = append(events, commits...)
		case isUnauthorized(err):
			// Let withTokens move on to the next credential rather than
			// reporting a commit-less history as if it were complete.
			return nil, err
		default:
			// Commit search is the one stream a deployment can plausibly have
			// disabled, and the other three are still worth showing.
			c.logf("[yellow]commit search failed: %v[-]", err)
			report.Warnings = append(report.Warnings, fmt.Sprintf("commits unavailable: %v", err))
		}

		// Strictly after the commit search, so that when the same commit turns
		// up in both streams the default-branch copy is the one dedupe keeps
		// and a landed commit is never labelled with a branch.
		branched, err := c.searchBranchCommits(ctx, gql, authorID, window)
		switch {
		case err == nil:
			events = append(events, branched...)
		case isUnauthorized(err):
			return nil, err
		default:
			// Non-fatal for the same reason as commit search: an incomplete
			// commit count is better than no report, as long as it says so.
			c.logf("[yellow]branch scan failed: %v[-]", err)
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("commits on unmerged branches unavailable: %v", err))
		}

		report.Collection = contrib.NewCollection(events, window)
		cc := report.Collection
		c.logf("[gray]contributions commits=%d issues=%d prs=%d (merged %d) reviews=%d[-]",
			cc.TotalCommits, cc.TotalIssues, cc.TotalPullRequests, cc.TotalMergedPullRequests, cc.TotalReviews)
		return report, nil
	})
}

// resolveUser confirms the login exists and picks up the display name, along
// with the node ID the branch scan filters commit history by.
func (c *Client) resolveUser(ctx context.Context, gql *githubv4.Client, username string) (*contrib.Report, githubv4.ID, error) {
	var q userQuery
	if err := gql.Query(ctx, &q, map[string]any{"login": githubv4.String(username)}); err != nil {
		return nil, nil, fmt.Errorf("graphql query failed: %w", err)
	}
	if q.User.Login == "" {
		return nil, nil, ErrUserNotFound
	}
	report := &contrib.Report{Login: string(q.User.Login), Name: string(q.User.Name)}
	return report, q.User.ID, nil
}

// searchAuthored collects the pull requests and issues opened in the window.
func (c *Client) searchAuthored(ctx context.Context, gql *githubv4.Client, login string, window contrib.Range) ([]contrib.Event, error) {
	query := fmt.Sprintf("author:%s created:%s..%s", login, day(window.From), day(window.To))

	var events []contrib.Event
	err := paginate(c, "authored", func(cursor *githubv4.String) (pageInfoFragment, error) {
		var q authoredSearchQuery
		vars := map[string]any{"query": githubv4.String(query), "cursor": cursor}
		if err := gql.Query(ctx, &q, vars); err != nil {
			return pageInfoFragment{}, err
		}
		for _, node := range q.Search.Nodes {
			switch node.Typename {
			case "PullRequest":
				pr := node.PullRequest
				events = append(events, contrib.Event{
					OccurredAt: pr.CreatedAt.Time,
					Kind:       contrib.KindAuthored,
					Repo:       string(pr.Repository.NameWithOwner),
					Title:      string(pr.Title),
					URL:        uriString(pr.URL),
					Merged:     bool(pr.Merged),
				})
			case "Issue":
				issue := node.Issue
				events = append(events, contrib.Event{
					OccurredAt: issue.CreatedAt.Time,
					Kind:       contrib.KindIssue,
					Repo:       string(issue.Repository.NameWithOwner),
					Title:      string(issue.Title),
					URL:        uriString(issue.URL),
				})
			}
		}
		return q.Search.PageInfo, nil
	})
	return events, err
}

// searchReviews collects individual review submissions. Search cannot filter
// on review time, so it asks for every pull request the user has reviewed that
// was touched since the window opened -- a superset -- and each review carries
// its own timestamp for the domain layer to filter on.
func (c *Client) searchReviews(ctx context.Context, gql *githubv4.Client, login string, window contrib.Range) ([]contrib.Event, error) {
	query := fmt.Sprintf("is:pr reviewed-by:%s updated:>=%s", login, day(window.From))

	var events []contrib.Event
	err := paginate(c, "reviews", func(cursor *githubv4.String) (pageInfoFragment, error) {
		var q reviewSearchQuery
		vars := map[string]any{
			"query":  githubv4.String(query),
			"cursor": cursor,
			"login":  githubv4.String(login),
		}
		if err := gql.Query(ctx, &q, vars); err != nil {
			return pageInfoFragment{}, err
		}
		for _, node := range q.Search.Nodes {
			pr := node.PullRequest
			for _, review := range pr.Reviews.Nodes {
				url := uriString(review.URL)
				if url == "" {
					url = uriString(pr.URL)
				}
				events = append(events, contrib.Event{
					OccurredAt: review.SubmittedAt.Time,
					Kind:       contrib.KindReviewed,
					Repo:       string(pr.Repository.NameWithOwner),
					Title:      string(pr.Title),
					URL:        url,
				})
			}
		}
		return q.Search.PageInfo, nil
	})
	return events, err
}

// searchCommits collects commits authored in the window. This is the one
// stream with no GraphQL equivalent, so it goes through REST search.
func (c *Client) searchCommits(ctx context.Context, httpClient *http.Client, login string, window contrib.Range) ([]contrib.Event, error) {
	rest, err := c.newRESTClient(httpClient)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("author:%s author-date:%s..%s", login, day(window.From), day(window.To))

	var events []contrib.Event
	opts := &github.SearchOptions{ListOptions: github.ListOptions{PerPage: searchPageSize}}
	for page := 1; ; page++ {
		result, resp, err := rest.Search.Commits(ctx, query, opts)
		if err != nil {
			return nil, err
		}
		for _, item := range result.Commits {
			commit := item.GetCommit()
			events = append(events, contrib.Event{
				OccurredAt: commit.GetAuthor().GetDate().Time,
				Kind:       contrib.KindCommit,
				Repo:       item.GetRepository().GetFullName(),
				Title:      firstLine(commit.GetMessage()),
				URL:        item.GetHTMLURL(),
			})
		}
		if resp == nil || resp.NextPage == 0 {
			return events, nil
		}
		if page >= maxSearchPages {
			c.logf("[yellow]commit search truncated at %d results; narrow the date range for a complete count[-]", len(events))
			return events, nil
		}
		opts.Page = resp.NextPage
	}
}

// paginate walks a GraphQL search connection, stopping at maxSearchPages and
// saying so when it does.
func paginate(c *Client, label string, page func(cursor *githubv4.String) (pageInfoFragment, error)) error {
	var cursor *githubv4.String
	for i := 0; i < maxSearchPages; i++ {
		info, err := page(cursor)
		if err != nil {
			return err
		}
		if !bool(info.HasNextPage) {
			return nil
		}
		end := info.EndCursor
		cursor = &end
	}
	c.logf("[yellow]%s search truncated at %d results; narrow the date range for a complete count[-]",
		label, maxSearchPages*searchPageSize)
	return nil
}

// day renders a time as the YYYY-MM-DD a search qualifier expects.
func day(t time.Time) string {
	return t.UTC().Format(contrib.DateFormat)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
