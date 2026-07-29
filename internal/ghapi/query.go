package ghapi

import (
	"github.com/shurcooL/githubv4"

	"gh-contribs/internal/contrib"
)

// The fragment types below define the GraphQL document sent to GitHub: the
// githubv4 client derives the query from this struct layout and its tags, so
// field names and tags are the query. They are named (rather than inline
// anonymous structs) so the mapping to the domain model stays readable and
// testable.

type contributionsQuery struct {
	User userFragment `graphql:"user(login: $login)"`
}

type userFragment struct {
	Login                   githubv4.String
	Name                    githubv4.String
	ContributionsCollection collectionFragment `graphql:"contributionsCollection(from: $from, to: $to)"`
}

type collectionFragment struct {
	StartedAt                                  githubv4.DateTime
	EndedAt                                    githubv4.DateTime
	TotalCommitContributions                   githubv4.Int
	TotalIssueContributions                    githubv4.Int
	TotalPullRequestContributions              githubv4.Int
	TotalPullRequestReviewContributions        githubv4.Int
	ContributionCalendar                       calendarFragment
	PullRequestContributionsByRepository       []repoContributionsFragment `graphql:"pullRequestContributionsByRepository(maxRepositories: 100)"`
	PullRequestReviewContributionsByRepository []repoContributionsFragment `graphql:"pullRequestReviewContributionsByRepository(maxRepositories: 100)"`
}

type calendarFragment struct {
	TotalContributions githubv4.Int
	Weeks              []weekFragment
}

type weekFragment struct {
	ContributionDays []dayFragment
}

type dayFragment struct {
	Date              githubv4.String
	ContributionCount githubv4.Int
	Color             githubv4.String
}

type repoContributionsFragment struct {
	Repository struct {
		NameWithOwner githubv4.String
	}
	Contributions struct {
		Nodes []prNodeFragment
	} `graphql:"contributions(first: 100)"`
}

type prNodeFragment struct {
	OccurredAt  githubv4.DateTime
	PullRequest struct {
		Title githubv4.String
		URL   githubv4.URI
	}
}

// toReport converts the GraphQL response into the domain model. Calendar days
// with unparseable dates are dropped rather than failing the whole report.
func (q contributionsQuery) toReport() *contrib.Report {
	cc := q.User.ContributionsCollection
	report := &contrib.Report{
		Login: string(q.User.Login),
		Name:  string(q.User.Name),
		Collection: contrib.Collection{
			StartedAt:            cc.StartedAt.Time,
			EndedAt:              cc.EndedAt.Time,
			TotalCommits:         int(cc.TotalCommitContributions),
			TotalIssues:          int(cc.TotalIssueContributions),
			TotalPullRequests:    int(cc.TotalPullRequestContributions),
			TotalReviews:         int(cc.TotalPullRequestReviewContributions),
			Calendar:             cc.ContributionCalendar.toCalendar(),
			AuthoredPullRequests: toRepoContributions(cc.PullRequestContributionsByRepository),
			ReviewedPullRequests: toRepoContributions(cc.PullRequestReviewContributionsByRepository),
		},
	}
	return report
}

func (c calendarFragment) toCalendar() contrib.Calendar {
	cal := contrib.Calendar{Total: int(c.TotalContributions)}
	for _, week := range c.Weeks {
		w := contrib.Week{}
		for _, day := range week.ContributionDays {
			date, err := contrib.ParseDate(string(day.Date))
			if err != nil {
				continue
			}
			w.Days = append(w.Days, contrib.Day{
				Date:  date,
				Count: int(day.ContributionCount),
				Color: string(day.Color),
			})
		}
		cal.Weeks = append(cal.Weeks, w)
	}
	return cal
}

func toRepoContributions(groups []repoContributionsFragment) []contrib.RepoContributions {
	if len(groups) == 0 {
		return nil
	}
	out := make([]contrib.RepoContributions, 0, len(groups))
	for _, group := range groups {
		g := contrib.RepoContributions{Repository: string(group.Repository.NameWithOwner)}
		for _, n := range group.Contributions.Nodes {
			g.Nodes = append(g.Nodes, contrib.PullRequestContribution{
				OccurredAt: n.OccurredAt.Time,
				Title:      string(n.PullRequest.Title),
				URL:        uriString(n.PullRequest.URL),
			})
		}
		out = append(out, g)
	}
	return out
}

// uriString renders a githubv4.URI, tolerating the null URLs GitHub returns
// for pull requests the token cannot see.
func uriString(u githubv4.URI) string {
	if u.URL == nil {
		return ""
	}
	return u.String()
}
