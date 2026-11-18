package ghapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/go-github/v66/github"
	"github.com/shurcooL/githubv4"

	"gh-contrib/internal/contrib"
)

// GraphQL connection page sizes live in the struct tags below, which must be
// literals; 100 is the maximum GitHub allows for a single connection.
const (
	// restPageSize is the REST per_page value for the fallback and team walks.
	restPageSize = 100

	// maxDirectorySize bounds the walk so a very large enterprise org cannot
	// stall startup or exhaust memory. Truncation is logged, never silent.
	maxDirectorySize = 5000
)

// pageInfoFragment is GitHub's standard cursor pagination envelope.
type pageInfoFragment struct {
	HasNextPage githubv4.Boolean
	EndCursor   githubv4.String
}

type memberFragment struct {
	Login githubv4.String
	Name  githubv4.String
}

type membersFragment struct {
	TotalCount githubv4.Int
	PageInfo   pageInfoFragment
	Nodes      []memberFragment
}

type orgDirectoryFragment struct {
	Login   githubv4.String
	Members membersFragment `graphql:"membersWithRole(first: 100)"`
}

// viewerDirectoryQuery fetches the viewer, their orgs, and the first page of
// each org's members in one round trip.
type viewerDirectoryQuery struct {
	Viewer struct {
		Login         githubv4.String
		Name          githubv4.String
		Organizations struct {
			PageInfo pageInfoFragment
			Nodes    []orgDirectoryFragment
		} `graphql:"organizations(first: 25, after: $orgCursor)"`
	}
}

// orgMembersQuery fetches subsequent member pages for a single org. Nested
// connections cannot be paged independently from the viewer query, so orgs
// with more than one page of members are followed up individually.
type orgMembersQuery struct {
	Organization struct {
		Members membersFragment `graphql:"membersWithRole(first: 100, after: $memberCursor)"`
	} `graphql:"organization(login: $org)"`
}

// DiscoverPeople returns everyone the viewer can see across their organizations,
// for username autocompletion.
//
// Org members come from GraphQL (one round trip, and it returns display names).
// Team members are then merged in from REST because GitHub omits suspended
// accounts from org member listings but retains them in team listings — for a
// contribution browser, a departed teammate is exactly who you want to look up.
// Such accounts are marked inactive.
//
// Partial failures are logged and skipped; an error is returned only when no
// source produced anyone.
func (c *Client) DiscoverPeople(ctx context.Context) ([]contrib.Person, error) {
	var (
		groups []([]contrib.Person)
		causes []error
	)

	people, err := c.graphqlDirectory(ctx)
	if err != nil {
		causes = append(causes, err)
		c.logf("[yellow]graphql directory unavailable, falling back to REST: %v[-]", err)
		people, err = c.restDirectory(ctx)
		if err != nil {
			causes = append(causes, err)
			c.logf("[yellow]rest directory unavailable: %v[-]", err)
		}
	}
	if len(people) > 0 {
		groups = append(groups, people)
	}

	teamMembers, err := c.restTeamMembers(ctx)
	if err != nil {
		causes = append(causes, err)
		c.logf("[yellow]team membership unavailable: %v[-]", err)
	} else if len(teamMembers) > 0 {
		groups = append(groups, teamMembers)
	}

	merged := contrib.MergePeople(groups...)
	if len(merged) == 0 {
		return nil, fmt.Errorf("user discovery found nobody: %w", errors.Join(causes...))
	}
	if len(merged) > maxDirectorySize {
		c.logf("[yellow]directory truncated to %d of %d people[-]", maxDirectorySize, len(merged))
		merged = merged[:maxDirectorySize]
	}
	return merged, nil
}

// graphqlDirectory walks the viewer's orgs and their members via GraphQL.
func (c *Client) graphqlDirectory(ctx context.Context) ([]contrib.Person, error) {
	return withTokens(c, ctx, "directory", func(httpClient *http.Client) ([]contrib.Person, error) {
		client := c.newGraphQLClient(httpClient)
		people := make([]contrib.Person, 0, 128)

		orgCursor := (*githubv4.String)(nil)
		firstPage := true
		for {
			var q viewerDirectoryQuery
			if err := client.Query(ctx, &q, map[string]any{"orgCursor": orgCursor}); err != nil {
				return nil, fmt.Errorf("directory query failed: %w", err)
			}

			if login := string(q.Viewer.Login); login != "" {
				people = append(people, contrib.Person{
					Login:  login,
					Name:   string(q.Viewer.Name),
					Active: true,
				})
			}

			if firstPage {
				firstPage = false
				c.logf("[gray]directory: viewer=%s organizations=%d[-]",
					q.Viewer.Login, len(q.Viewer.Organizations.Nodes))
				if len(q.Viewer.Organizations.Nodes) == 0 {
					c.logf("[yellow]this token can see no organizations. Env GITHUB_TOKEN and " +
						"GH_TOKEN take precedence over 'gh auth token', so check the token source " +
						"logged above; org listings need the read:org scope[-]")
				}
			}

			for _, org := range q.Viewer.Organizations.Nodes {
				orgLogin := string(org.Login)
				people = append(people, membersToPeople(orgLogin, org.Members.Nodes)...)

				// Nested connections page independently; follow this org directly.
				page := org.Members.PageInfo
				for page.HasNextPage && len(people) < maxDirectorySize {
					cursor := page.EndCursor
					var mq orgMembersQuery
					err := client.Query(ctx, &mq, map[string]any{
						"org":          githubv4.String(orgLogin),
						"memberCursor": githubv4.String(cursor),
					})
					if err != nil {
						// Keep the pages already collected for this org.
						c.logf("[yellow]paging members of %s stopped: %v[-]", orgLogin, err)
						break
					}
					people = append(people, membersToPeople(orgLogin, mq.Organization.Members.Nodes)...)
					page = mq.Organization.Members.PageInfo
				}
			}

			if !q.Viewer.Organizations.PageInfo.HasNextPage || len(people) >= maxDirectorySize {
				break
			}
			next := q.Viewer.Organizations.PageInfo.EndCursor
			orgCursor = &next
		}

		if len(people) == 0 {
			return nil, errors.New("directory query returned nobody")
		}
		return people, nil
	})
}

func membersToPeople(org string, members []memberFragment) []contrib.Person {
	out := make([]contrib.Person, 0, len(members))
	for _, m := range members {
		login := string(m.Login)
		if login == "" {
			continue
		}
		out = append(out, contrib.Person{
			Login:  login,
			Name:   string(m.Name),
			Orgs:   []string{org},
			Active: true,
		})
	}
	return out
}

// restDirectory is the fallback when GraphQL is unavailable. It costs one
// request per org page and yields no display names.
func (c *Client) restDirectory(ctx context.Context) ([]contrib.Person, error) {
	return withTokens(c, ctx, "directory-rest", func(httpClient *http.Client) ([]contrib.Person, error) {
		client, err := c.newRESTClient(httpClient)
		if err != nil {
			return nil, err
		}

		people := make([]contrib.Person, 0, 128)
		self, _, err := client.Users.Get(ctx, "")
		if err != nil {
			return nil, fmt.Errorf("discover viewer failed: %w", err)
		}
		if self.GetLogin() != "" {
			people = append(people, contrib.Person{
				Login:  self.GetLogin(),
				Name:   self.GetName(),
				Active: true,
			})
		}

		orgs, err := allPages(ctx, func(opts github.ListOptions) ([]*github.Organization, *github.Response, error) {
			return client.Organizations.List(ctx, "", &opts)
		})
		if err != nil {
			return nil, fmt.Errorf("discover orgs failed: %w", err)
		}

		for _, org := range orgs {
			orgLogin := org.GetLogin()
			if orgLogin == "" {
				continue
			}
			members, err := allPages(ctx, func(opts github.ListOptions) ([]*github.User, *github.Response, error) {
				return client.Organizations.ListMembers(ctx, orgLogin, &github.ListMembersOptions{ListOptions: opts})
			})
			if err != nil {
				c.logf("[yellow]skipping org %s members: %v[-]", orgLogin, err)
				continue
			}
			for _, m := range members {
				if m.GetLogin() == "" {
					continue
				}
				people = append(people, contrib.Person{
					Login:  m.GetLogin(),
					Orgs:   []string{orgLogin},
					Active: true,
				})
			}
		}
		return people, nil
	})
}

// restTeamMembers lists members of the viewer's teams. These are marked
// inactive; merging promotes anyone who also appears as an org member.
func (c *Client) restTeamMembers(ctx context.Context) ([]contrib.Person, error) {
	return withTokens(c, ctx, "teams", func(httpClient *http.Client) ([]contrib.Person, error) {
		client, err := c.newRESTClient(httpClient)
		if err != nil {
			return nil, err
		}

		teams, err := allPages(ctx, func(opts github.ListOptions) ([]*github.Team, *github.Response, error) {
			return client.Teams.ListUserTeams(ctx, &opts)
		})
		if err != nil {
			return nil, fmt.Errorf("list teams failed: %w", err)
		}

		people := make([]contrib.Person, 0, 64)
		for _, team := range teams {
			orgID, teamID := int64(0), team.GetID()
			orgLogin := ""
			if team.Organization != nil {
				orgID = team.Organization.GetID()
				orgLogin = team.Organization.GetLogin()
			}
			if orgID == 0 || teamID == 0 {
				continue
			}
			members, err := allPages(ctx, func(opts github.ListOptions) ([]*github.User, *github.Response, error) {
				return client.Teams.ListTeamMembersByID(ctx, orgID, teamID, &github.TeamListTeamMembersOptions{
					Role:        "all",
					ListOptions: opts,
				})
			})
			if err != nil {
				c.logf("[yellow]skipping team %s members: %v[-]", team.GetName(), err)
				continue
			}
			for _, m := range members {
				if m.GetLogin() == "" {
					continue
				}
				person := contrib.Person{Login: m.GetLogin()}
				if orgLogin != "" {
					person.Orgs = []string{orgLogin}
				}
				people = append(people, person)
			}
		}
		return people, nil
	})
}

// allPages exhausts a go-github list endpoint, following NextPage.
func allPages[T any](ctx context.Context, list func(github.ListOptions) ([]T, *github.Response, error)) ([]T, error) {
	opts := github.ListOptions{PerPage: restPageSize}
	var out []T
	for {
		page, resp, err := list(opts)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if resp == nil || resp.NextPage == 0 || len(out) >= maxDirectorySize {
			return out, nil
		}
		opts.Page = resp.NextPage
	}
}
