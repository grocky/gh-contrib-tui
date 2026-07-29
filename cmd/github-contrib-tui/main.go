package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/google/go-github/v66/github"
	"github.com/rivo/tview"
	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"
)

const (
	dateFormat = "2006-01-02"
)

type contributionResponse struct {
	Data contributionData
}

type contributionData struct {
	User contributionUser
}

type contributionUser struct {
	Login                   string
	Name                    string
	ContributionsCollection contributionCollection
}

type contributionCollection struct {
	StartedAt                             time.Time
	EndedAt                               time.Time
	TotalCommitContributions              int
	TotalIssueContributions               int
	TotalPullRequestContributions         int
	TotalPullRequestReviewContributions   int
	ContributionCalendar                  contributionCalendar
	PullRequestContributionsByRepository  []prContributionByRepo
	PullRequestReviewContributionsByRepository []prContributionByRepo
}

type contributionCalendar struct {
	TotalContributions int
	Weeks              []contributionWeek
}

type contributionWeek struct {
	ContributionDays []contributionDay
}

type contributionDay struct {
	Date              string
	ContributionCount int
	Color             string
}

type prContributionByRepo struct {
	Repository struct {
		NameWithOwner string
	}
	Contributions struct {
		Nodes []prContributionNode
	}
}

type prContributionNode struct {
	PullRequest struct {
		Title string
		URL   string
	}
	OccurredAt time.Time
}

type prItem struct {
	OccurredAt time.Time
	Kind       string
	Repo       string
	Title      string
	URL        string
}

type appConfig struct {
	Host       string
	RestBase   string
	UploadBase string
	GraphQLURL string
}

func main() {
	app := tview.NewApplication()
	status := tview.NewTextView().SetDynamicColors(true)
	status.SetBorder(true).SetTitle("Status")
	logView := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	logView.SetBorder(true).SetTitle("Logs")
	logView.SetChangedFunc(func() { app.Draw() })

	logf := func(format string, args ...any) {
		fmt.Fprintf(logView, "%s %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	}

	userField := tview.NewInputField().SetLabel("User: ")
	fromField := tview.NewInputField().SetLabel("From (YYYY-MM-DD): ")
	toField := tview.NewInputField().SetLabel("To (YYYY-MM-DD): ")
	userField.SetText(os.Getenv("GH_USER"))
	userSuggestions := make([]string, 0)
	var suggestionsMu sync.RWMutex
	userField.SetAutocompleteFunc(func(currentText string) (entries []string) {
		q := strings.ToLower(strings.TrimSpace(currentText))
		suggestionsMu.RLock()
		defer suggestionsMu.RUnlock()
		if len(userSuggestions) == 0 {
			return nil
		}
		if q == "" {
			limit := 25
			if len(userSuggestions) < limit {
				limit = len(userSuggestions)
			}
			return append([]string(nil), userSuggestions[:limit]...)
		}
		out := make([]string, 0, 25)
		for _, u := range userSuggestions {
			if strings.Contains(strings.ToLower(u), q) {
				out = append(out, u)
				if len(out) >= 25 {
					break
				}
			}
		}
		return out
	})

	summary := tview.NewTextView().SetDynamicColors(true)
	summary.SetBorder(true).SetTitle("Contribution Summary")

	heatmap := tview.NewTable()
	heatmap.SetFixed(1, 1).SetBorders(false)
	heatmap.SetBorder(true).SetTitle("Contribution Calendar")

	prList := tview.NewList().ShowSecondaryText(true)
	prList.SetBorder(true).SetTitle("PR References (Enter=open, Ctrl+Y=copy URL)")
	prDetail := tview.NewTextView().SetDynamicColors(true)
	prDetail.SetBorder(true).SetTitle("Selected PR URL")

	fetchAndRender := func() {
		username := strings.TrimSpace(userField.GetText())
		from := strings.TrimSpace(fromField.GetText())
		to := strings.TrimSpace(toField.GetText())
		if username == "" {
			status.SetText("[red]user is required")
			logf("[red]validation failed: username is empty[-]")
			return
		}
		fromTS, toTS, err := parseRange(from, to)
		if err != nil {
			status.SetText("[red]" + err.Error())
			logf("[red]validation failed: %v[-]", err)
			return
		}

		status.SetText("[yellow]loading contributions...")
		logf("[yellow]fetch start user=%s from=%s to=%s[-]", username, fromTS, toTS)
		resp, err := fetchContributions(context.Background(), username, fromTS, toTS, logf)
		if err != nil {
			status.SetText("[red]" + err.Error())
			logf("[red]fetch failed: %v[-]", err)
			return
		}
		renderSummary(summary, resp)
		renderHeatmap(heatmap, resp)
		prs := collectPRs(resp)
		renderPRList(prList, prDetail, prs)
		status.SetText(fmt.Sprintf("[green]loaded %d PR references", len(prs)))
		logf("[green]fetch complete total=%d prs=%d[-]", resp.Data.User.ContributionsCollection.ContributionCalendar.TotalContributions, len(prs))
	}

	form := tview.NewForm().
		AddFormItem(userField).
		AddFormItem(fromField).
		AddFormItem(toField).
		AddButton("Fetch", fetchAndRender)
	form.SetBorder(true).SetTitle("Filters").SetTitleAlign(tview.AlignLeft)

	left := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(form, 11, 0, true).
		AddItem(summary, 10, 0, false).
		AddItem(status, 3, 0, false).
		AddItem(logView, 0, 1, false)

	right := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(heatmap, 0, 3, false).
		AddItem(prList, 0, 2, false).
		AddItem(prDetail, 3, 0, false)

	layout := tview.NewFlex().
		AddItem(left, 0, 2, true).
		AddItem(right, 0, 3, false)

	prList.SetSelectedFunc(func(i int, mainText string, secondary string, shortcut rune) {
		_ = i
		_ = mainText
		_ = secondary
		_ = shortcut
		url := strings.TrimSpace(prDetail.GetText(false))
		if url == "" {
			return
		}
		if err := openURL(url); err != nil {
			status.SetText("[red]open failed: " + err.Error())
			return
		}
		status.SetText("[green]opened in browser")
	})

	app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyCtrlC:
			app.Stop()
			return nil
		case tcell.KeyCtrlQ:
			app.Stop()
			return nil
		case tcell.KeyCtrlR:
			fetchAndRender()
			return nil
		case tcell.KeyCtrlY:
			url := strings.TrimSpace(prDetail.GetText(false))
			if url == "" {
				status.SetText("[red]no PR selected")
				return nil
			}
			if err := copyToClipboard(url); err != nil {
				status.SetText("[yellow]copy not available; URL shown below")
				return nil
			}
			status.SetText("[green]copied URL")
			return nil
		}
		return ev
	})

	prDetail.SetText("")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		users, err := discoverTeammateLogins(ctx, logf)
		app.QueueUpdateDraw(func() {
			if err != nil {
				logf("[yellow]user discovery unavailable: %v[-]", err)
				return
			}
			suggestionsMu.Lock()
			userSuggestions = users
			suggestionsMu.Unlock()
			logf("[green]loaded %d user suggestions[-]", len(users))
		})
	}()

	if err := app.SetRoot(layout, true).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tui failed: %v\n", err)
		os.Exit(1)
	}
}

func fetchContributions(ctx context.Context, username, fromISO, toISO string, logf func(string, ...any)) (*contributionResponse, error) {
	tokens, err := resolveTokenCandidates()
	if err != nil {
		return nil, err
	}
	cfg := resolveAppConfig()
	logf("[gray]rest base=%s[-]", cfg.RestBase)
	logf("[gray]graphql endpoint=%s[-]", cfg.GraphQLURL)
	logf("[gray]host source=%s[-]", cfg.Host)
	fromTime, err := time.Parse(time.RFC3339, fromISO)
	if err != nil {
		return nil, fmt.Errorf("invalid from timestamp: %w", err)
	}
	toTime, err := time.Parse(time.RFC3339, toISO)
	if err != nil {
		return nil, fmt.Errorf("invalid to timestamp: %w", err)
	}

	schemes := []authScheme{
		{Name: "Bearer", Prefix: "Bearer "},
		{Name: "token", Prefix: "token "},
	}
	for i, candidate := range tokens {
		logf("[gray]token source=%s (attempt %d/%d) fingerprint=%s[-]", candidate.Source, i+1, len(tokens), tokenFingerprint(candidate.Token))
		src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: candidate.Token})
		httpClient := oauth2.NewClient(ctx, src)
		for j, scheme := range schemes {
			client, err := newGraphQLClient(cfg.GraphQLURL, httpClient)
			if err != nil {
				return nil, err
			}

			var q struct {
				User struct {
					Login                   githubv4.String
					Name                    githubv4.String
					ContributionsCollection struct {
						StartedAt                           githubv4.DateTime
						EndedAt                             githubv4.DateTime
						TotalCommitContributions            githubv4.Int
						TotalIssueContributions             githubv4.Int
						TotalPullRequestContributions       githubv4.Int
						TotalPullRequestReviewContributions githubv4.Int
						ContributionCalendar                struct {
							TotalContributions githubv4.Int
							Weeks              []struct {
								ContributionDays []struct {
									Date              githubv4.String
									ContributionCount githubv4.Int
									Color             githubv4.String
								}
							}
						}
						PullRequestContributionsByRepository []struct {
							Repository struct {
								NameWithOwner githubv4.String
							}
							Contributions struct {
								Nodes []struct {
									OccurredAt  githubv4.DateTime
									PullRequest struct {
										Title githubv4.String
										URL   githubv4.URI
									}
								}
							} `graphql:"contributions(first: 100)"`
						} `graphql:"pullRequestContributionsByRepository(maxRepositories: 100)"`
						PullRequestReviewContributionsByRepository []struct {
							Repository struct {
								NameWithOwner githubv4.String
							}
							Contributions struct {
								Nodes []struct {
									OccurredAt  githubv4.DateTime
									PullRequest struct {
										Title githubv4.String
										URL   githubv4.URI
									}
								}
							} `graphql:"contributions(first: 100)"`
						} `graphql:"pullRequestReviewContributionsByRepository(maxRepositories: 100)"`
					} `graphql:"contributionsCollection(from: $from, to: $to)"`
				} `graphql:"user(login: $login)"`
			}

			vars := map[string]any{
				"login": githubv4.String(username),
				"from":  githubv4.DateTime{Time: fromTime},
				"to":    githubv4.DateTime{Time: toTime},
			}

			err = client.Query(ctx, &q, vars)
			if err != nil {
				logf("[gray]auth=%s graphql query failed: %v[-]", scheme.Name, err)
				if isUnauthorizedErr(err) && (j < len(schemes)-1 || i < len(tokens)-1) {
					logf("[yellow]401 with %s/%s, trying next auth source[-]", candidate.Source, scheme.Name)
					continue
				}
				return nil, fmt.Errorf("graphql query failed: %w", err)
			}

			resp := mapGraphQLResult(q)
			if resp.Data.User.Login == "" {
				return nil, errors.New("user not found or inaccessible")
			}
			return resp, nil
		}
	}
	return nil, errors.New("all token sources failed with 401; refresh credentials")
}

type tokenCandidate struct {
	Source string
	Token  string
}

type authScheme struct {
	Name   string
	Prefix string
}

func resolveTokenCandidates() ([]tokenCandidate, error) {
	candidates := make([]tokenCandidate, 0, 3)
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		candidates = append(candidates, tokenCandidate{Source: "GITHUB_TOKEN", Token: tok})
	}
	if tok := strings.TrimSpace(os.Getenv("GH_TOKEN")); tok != "" {
		candidates = append(candidates, tokenCandidate{Source: "GH_TOKEN", Token: tok})
	}
	if _, err := exec.LookPath("gh"); err == nil {
		out, err := exec.Command("gh", "auth", "token").Output()
		if err == nil {
			tok := strings.TrimSpace(string(out))
			if tok != "" {
				candidates = append(candidates, tokenCandidate{Source: "gh auth token", Token: tok})
			}
		}
	}
	if len(candidates) == 0 {
		return nil, errors.New("no token found; set GITHUB_TOKEN or GH_TOKEN, or run `gh auth login`")
	}
	return dedupeTokenCandidates(candidates), nil
}

func dedupeTokenCandidates(in []tokenCandidate) []tokenCandidate {
	seen := map[string]struct{}{}
	out := make([]tokenCandidate, 0, len(in))
	for _, c := range in {
		if _, ok := seen[c.Token]; ok {
			continue
		}
		seen[c.Token] = struct{}{}
		out = append(out, c)
	}
	return out
}

func tokenFingerprint(token string) string {
	if len(token) <= 8 {
		return fmt.Sprintf("len:%d", len(token))
	}
	return fmt.Sprintf("%s...%s(len:%d)", token[:4], token[len(token)-4:], len(token))
}

func newGraphQLClient(graphqlURL string, httpClient *http.Client) (*githubv4.Client, error) {
	if graphqlURL == "https://api.github.com/graphql" {
		return githubv4.NewClient(httpClient), nil
	}
	return githubv4.NewEnterpriseClient(graphqlURL, httpClient), nil
}

func isUnauthorizedErr(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "401") || strings.Contains(s, "unauthorized") || strings.Contains(s, "bad credentials")
}

func mapGraphQLResult(q struct {
	User struct {
		Login                   githubv4.String
		Name                    githubv4.String
		ContributionsCollection struct {
			StartedAt                           githubv4.DateTime
			EndedAt                             githubv4.DateTime
			TotalCommitContributions            githubv4.Int
			TotalIssueContributions             githubv4.Int
			TotalPullRequestContributions       githubv4.Int
			TotalPullRequestReviewContributions githubv4.Int
			ContributionCalendar                struct {
				TotalContributions githubv4.Int
				Weeks              []struct {
					ContributionDays []struct {
						Date              githubv4.String
						ContributionCount githubv4.Int
						Color             githubv4.String
					}
				}
			}
			PullRequestContributionsByRepository []struct {
				Repository struct {
					NameWithOwner githubv4.String
				}
				Contributions struct {
					Nodes []struct {
						OccurredAt  githubv4.DateTime
						PullRequest struct {
							Title githubv4.String
							URL   githubv4.URI
						}
					}
				} `graphql:"contributions(first: 100)"`
			} `graphql:"pullRequestContributionsByRepository(maxRepositories: 100)"`
			PullRequestReviewContributionsByRepository []struct {
				Repository struct {
					NameWithOwner githubv4.String
				}
				Contributions struct {
					Nodes []struct {
						OccurredAt  githubv4.DateTime
						PullRequest struct {
							Title githubv4.String
							URL   githubv4.URI
						}
					}
				} `graphql:"contributions(first: 100)"`
			} `graphql:"pullRequestReviewContributionsByRepository(maxRepositories: 100)"`
		} `graphql:"contributionsCollection(from: $from, to: $to)"`
	} `graphql:"user(login: $login)"`
}) *contributionResponse {
	resp := &contributionResponse{}
	resp.Data.User.Login = string(q.User.Login)
	resp.Data.User.Name = string(q.User.Name)
	resp.Data.User.ContributionsCollection.StartedAt = q.User.ContributionsCollection.StartedAt.Time
	resp.Data.User.ContributionsCollection.EndedAt = q.User.ContributionsCollection.EndedAt.Time
	resp.Data.User.ContributionsCollection.TotalCommitContributions = int(q.User.ContributionsCollection.TotalCommitContributions)
	resp.Data.User.ContributionsCollection.TotalIssueContributions = int(q.User.ContributionsCollection.TotalIssueContributions)
	resp.Data.User.ContributionsCollection.TotalPullRequestContributions = int(q.User.ContributionsCollection.TotalPullRequestContributions)
	resp.Data.User.ContributionsCollection.TotalPullRequestReviewContributions = int(q.User.ContributionsCollection.TotalPullRequestReviewContributions)
	resp.Data.User.ContributionsCollection.ContributionCalendar.TotalContributions = int(q.User.ContributionsCollection.ContributionCalendar.TotalContributions)

	for _, week := range q.User.ContributionsCollection.ContributionCalendar.Weeks {
		w := contributionWeek{}
		for _, day := range week.ContributionDays {
			parsedDate, err := parseContributionDate(string(day.Date))
			if err != nil {
				continue
			}
			w.ContributionDays = append(w.ContributionDays, contributionDay{
				Date:              parsedDate,
				ContributionCount: int(day.ContributionCount),
				Color:             string(day.Color),
			})
		}
		resp.Data.User.ContributionsCollection.ContributionCalendar.Weeks = append(resp.Data.User.ContributionsCollection.ContributionCalendar.Weeks, w)
	}

	for _, repoGroup := range q.User.ContributionsCollection.PullRequestContributionsByRepository {
		g := prContributionByRepo{}
		g.Repository.NameWithOwner = string(repoGroup.Repository.NameWithOwner)
		for _, n := range repoGroup.Contributions.Nodes {
			node := prContributionNode{OccurredAt: n.OccurredAt.Time}
			node.PullRequest.Title = string(n.PullRequest.Title)
			node.PullRequest.URL = n.PullRequest.URL.String()
			g.Contributions.Nodes = append(g.Contributions.Nodes, node)
		}
		resp.Data.User.ContributionsCollection.PullRequestContributionsByRepository = append(resp.Data.User.ContributionsCollection.PullRequestContributionsByRepository, g)
	}

	for _, repoGroup := range q.User.ContributionsCollection.PullRequestReviewContributionsByRepository {
		g := prContributionByRepo{}
		g.Repository.NameWithOwner = string(repoGroup.Repository.NameWithOwner)
		for _, n := range repoGroup.Contributions.Nodes {
			node := prContributionNode{OccurredAt: n.OccurredAt.Time}
			node.PullRequest.Title = string(n.PullRequest.Title)
			node.PullRequest.URL = n.PullRequest.URL.String()
			g.Contributions.Nodes = append(g.Contributions.Nodes, node)
		}
		resp.Data.User.ContributionsCollection.PullRequestReviewContributionsByRepository = append(resp.Data.User.ContributionsCollection.PullRequestReviewContributionsByRepository, g)
	}
	return resp
}

func resolveGitHubHost() string {
	if h := strings.TrimSpace(os.Getenv("GH_HOST")); h != "" {
		return h
	}
	if _, err := exec.LookPath("gh"); err == nil {
		out, err := exec.Command("gh", "auth", "status").CombinedOutput()
		if err == nil {
			clean := stripANSICodes(string(out))
			lines := strings.Split(clean, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "✓") || strings.HasPrefix(line, "-") {
					continue
				}
				if strings.Contains(line, ".") && !strings.Contains(line, " ") {
					return line
				}
			}
		}
	}
	return ""
}

func buildGitHubEndpoints(host string) (string, string, string) {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimSuffix(host, "/")

	switch host {
	case "github.com", "www.github.com", "api.github.com":
		return "https://api.github.com/", "https://uploads.github.com/", "https://api.github.com/graphql"
	default:
		return fmt.Sprintf("https://%s/api/v3/", host),
			fmt.Sprintf("https://%s/api/uploads/", host),
			fmt.Sprintf("https://%s/api/graphql", host)
	}
}

func resolveAppConfig() appConfig {
	host := strings.TrimSpace(os.Getenv("GITHUB_HOSTNAME"))
	if host == "" {
		host = resolveGitHubHost()
	}
	if host == "" {
		host = "github.com"
	}
	rest, upload, gql := buildGitHubEndpoints(host)
	return appConfig{
		Host:       host,
		RestBase:   rest,
		UploadBase: upload,
		GraphQLURL: gql,
	}
}

func discoverTeammateLogins(ctx context.Context, logf func(string, ...any)) ([]string, error) {
	tokens, err := resolveTokenCandidates()
	if err != nil {
		return nil, err
	}
	cfg := resolveAppConfig()
	for i, candidate := range tokens {
		logf("[gray]discovery token source=%s (%d/%d)[-]", candidate.Source, i+1, len(tokens))
		client, err := newRESTClient(ctx, cfg, candidate.Token)
		if err != nil {
			return nil, err
		}
		users, err := loadDiscoveryUsers(ctx, client, logf)
		if err != nil {
			if isUnauthorizedErr(err) && i < len(tokens)-1 {
				logf("[yellow]discovery 401 with %s, trying next token[-]", candidate.Source)
				continue
			}
			return nil, err
		}
		return users, nil
	}
	return nil, errors.New("user discovery failed for all token sources")
}

func newRESTClient(ctx context.Context, cfg appConfig, token string) (*github.Client, error) {
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	httpClient := oauth2.NewClient(ctx, src)
	if cfg.Host == "github.com" || cfg.Host == "www.github.com" || cfg.Host == "api.github.com" {
		return github.NewClient(httpClient), nil
	}
	return github.NewEnterpriseClient(cfg.RestBase, cfg.UploadBase, httpClient)
}

func loadDiscoveryUsers(ctx context.Context, client *github.Client, logf func(string, ...any)) ([]string, error) {
	set := map[string]struct{}{}
	self, _, err := client.Users.Get(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("discover viewer failed: %w", err)
	}
	if self.GetLogin() != "" {
		set[self.GetLogin()] = struct{}{}
	}

	orgs, _, err := client.Organizations.List(ctx, "", &github.ListOptions{PerPage: 100})
	if err != nil {
		return nil, fmt.Errorf("discover orgs failed: %w", err)
	}
	for _, org := range orgs {
		if org.GetLogin() == "" {
			continue
		}
		members, _, err := client.Organizations.ListMembers(ctx, org.GetLogin(), &github.ListMembersOptions{
			ListOptions: github.ListOptions{PerPage: 100},
		})
		if err != nil {
			logf("[yellow]skipping org %s members: %v[-]", org.GetLogin(), err)
			continue
		}
		for _, m := range members {
			if m.GetLogin() != "" {
				set[m.GetLogin()] = struct{}{}
			}
		}
	}

	teams, _, err := client.Teams.ListUserTeams(ctx, &github.ListOptions{PerPage: 100})
	if err != nil {
		logf("[yellow]team discovery unavailable: %v[-]", err)
	} else {
		for _, team := range teams {
			orgID := int64(0)
			if team.Organization != nil {
				orgID = team.Organization.GetID()
			}
			teamID := team.GetID()
			if orgID == 0 || teamID == 0 {
				continue
			}
			members, _, err := client.Teams.ListTeamMembersByID(ctx, orgID, teamID, &github.TeamListTeamMembersOptions{
				Role: "all",
				ListOptions: github.ListOptions{
					PerPage: 100,
				},
			})
			if err != nil {
				logf("[yellow]skipping team %s members: %v[-]", team.GetName(), err)
				continue
			}
			for _, m := range members {
				if m.GetLogin() != "" {
					set[m.GetLogin()] = struct{}{}
				}
			}
		}
	}

	users := make([]string, 0, len(set))
	for login := range set {
		users = append(users, login)
	}
	sort.Strings(users)
	return users, nil
}

func stripANSICodes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) {
				c := s[i]
				if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
					i++
					break
				}
				i++
			}
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func parseContributionDate(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty contribution date")
	}
	if t, err := time.Parse(dateFormat, raw); err == nil {
		return t.Format(dateFormat), nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "", err
	}
	return t.UTC().Format(dateFormat), nil
}

func parseRange(from, to string) (string, string, error) {
	if from == "" && to != "" {
		return "", "", errors.New("--from is required when --to is set")
	}
	if from == "" && to == "" {
		now := time.Now().UTC()
		fromTime := now.AddDate(0, 0, -30)
		return toISODateStart(fromTime.Format(dateFormat)), toISODateEnd(now.Format(dateFormat)), nil
	}
	if to == "" {
		to = time.Now().UTC().Format(dateFormat)
	}
	if _, err := time.Parse(dateFormat, from); err != nil {
		return "", "", fmt.Errorf("invalid from date: %s", from)
	}
	if _, err := time.Parse(dateFormat, to); err != nil {
		return "", "", fmt.Errorf("invalid to date: %s", to)
	}
	return toISODateStart(from), toISODateEnd(to), nil
}

func toISODateStart(d string) string {
	return d + "T00:00:00Z"
}

func toISODateEnd(d string) string {
	return d + "T23:59:59Z"
}

func renderSummary(v *tview.TextView, resp *contributionResponse) {
	cc := resp.Data.User.ContributionsCollection
	name := resp.Data.User.Name
	if name == "" {
		name = resp.Data.User.Login
	}
	v.Clear()
	fmt.Fprintf(v,
		"[::b]%s[::-]\nRange: %s -> %s\nTotal: [green]%d[-]\nCommits: %d  Issues: %d  PRs: %d  Reviews: %d",
		name,
		cc.StartedAt.Format(dateFormat),
		cc.EndedAt.Format(dateFormat),
		cc.ContributionCalendar.TotalContributions,
		cc.TotalCommitContributions,
		cc.TotalIssueContributions,
		cc.TotalPullRequestContributions,
		cc.TotalPullRequestReviewContributions,
	)
}

func renderHeatmap(table *tview.Table, resp *contributionResponse) {
	table.Clear()
	table.SetCell(0, 0, tview.NewTableCell("D/W").SetTextColor(tcell.ColorYellow))
	weekdays := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	for row, day := range weekdays {
		table.SetCell(row+1, 0, tview.NewTableCell(day).SetTextColor(tcell.ColorGray))
	}

	weeks := resp.Data.User.ContributionsCollection.ContributionCalendar.Weeks
	for col, week := range weeks {
		table.SetCell(0, col+1, tview.NewTableCell(fmt.Sprintf("%d", col+1)).SetTextColor(tcell.ColorGray))
		for _, d := range week.ContributionDays {
			t, err := time.Parse(dateFormat, d.Date)
			if err != nil {
				continue
			}
			row := normalizeWeekday(t.Weekday()) + 1
			bg := tcell.GetColor(d.Color)
			cell := tview.NewTableCell(fmt.Sprintf("%2d", d.ContributionCount)).
				SetTextColor(tcell.ColorBlack).
				SetBackgroundColor(bg).
				SetAlign(tview.AlignCenter)
			table.SetCell(row, col+1, cell)
		}
	}
}

func normalizeWeekday(w time.Weekday) int {
	if w == time.Sunday {
		return 6
	}
	return int(w) - 1
}

func collectPRs(resp *contributionResponse) []prItem {
	cc := resp.Data.User.ContributionsCollection
	seen := map[string]struct{}{}
	items := make([]prItem, 0, 256)

	for _, repoGroup := range cc.PullRequestContributionsByRepository {
		for _, node := range repoGroup.Contributions.Nodes {
			if node.PullRequest.URL == "" {
				continue
			}
			key := "authored|" + node.PullRequest.URL
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			items = append(items, prItem{
				OccurredAt: node.OccurredAt,
				Kind:       "authored",
				Repo:       repoGroup.Repository.NameWithOwner,
				Title:      node.PullRequest.Title,
				URL:        node.PullRequest.URL,
			})
		}
	}

	for _, repoGroup := range cc.PullRequestReviewContributionsByRepository {
		for _, node := range repoGroup.Contributions.Nodes {
			if node.PullRequest.URL == "" {
				continue
			}
			key := "reviewed|" + node.PullRequest.URL
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			items = append(items, prItem{
				OccurredAt: node.OccurredAt,
				Kind:       "reviewed",
				Repo:       repoGroup.Repository.NameWithOwner,
				Title:      node.PullRequest.Title,
				URL:        node.PullRequest.URL,
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].OccurredAt.After(items[j].OccurredAt)
	})
	return items
}

func renderPRList(list *tview.List, detail *tview.TextView, prs []prItem) {
	list.Clear()
	detail.Clear()
	for _, item := range prs {
		title := fmt.Sprintf("[%s] %s", item.Kind, item.Title)
		secondary := fmt.Sprintf("%s | %s", item.OccurredAt.Format(dateFormat), item.Repo)
		url := item.URL
		list.AddItem(title, secondary, 0, func() {
			detail.SetText(url)
		})
	}
	if len(prs) > 0 {
		detail.SetText(prs[0].URL)
	}
}

func openURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

func copyToClipboard(s string) error {
	candidates := [][]string{
		{"xclip", "-selection", "clipboard"},
		{"wl-copy"},
		{"pbcopy"},
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Stdin = strings.NewReader(s)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	return errors.New("no clipboard command found")
}
