package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/google/go-github/v66/github"
	"github.com/rivo/tview"
	"golang.org/x/oauth2"
)

const (
	dateFormat = "2006-01-02"
)

type graphQLRequest struct {
	Query     string            `json:"query"`
	Variables map[string]string `json:"variables"`
}

type graphQLError struct {
	Message string `json:"message"`
}

type contributionResponse struct {
	Data struct {
		User struct {
			Login                   string `json:"login"`
			Name                    string `json:"name"`
			ContributionsCollection struct {
				StartedAt                         time.Time `json:"startedAt"`
				EndedAt                           time.Time `json:"endedAt"`
				TotalCommitContributions          int       `json:"totalCommitContributions"`
				TotalIssueContributions           int       `json:"totalIssueContributions"`
				TotalPullRequestContributions     int       `json:"totalPullRequestContributions"`
				TotalPullRequestReviewContributions int     `json:"totalPullRequestReviewContributions"`
				ContributionCalendar              struct {
					TotalContributions int `json:"totalContributions"`
					Weeks              []struct {
						ContributionDays []struct {
							Date              string `json:"date"`
							ContributionCount int    `json:"contributionCount"`
							Color             string `json:"color"`
						} `json:"contributionDays"`
					} `json:"weeks"`
				} `json:"contributionCalendar"`
				PullRequestContributionsByRepository []struct {
					Repository struct {
						NameWithOwner string `json:"nameWithOwner"`
					} `json:"repository"`
					Contributions struct {
						Nodes []struct {
							PullRequest struct {
								Title string `json:"title"`
								URL   string `json:"url"`
							} `json:"pullRequest"`
							OccurredAt time.Time `json:"occurredAt"`
						} `json:"nodes"`
					} `json:"contributions"`
				} `json:"pullRequestContributionsByRepository"`
				PullRequestReviewContributionsByRepository []struct {
					Repository struct {
						NameWithOwner string `json:"nameWithOwner"`
					} `json:"repository"`
					Contributions struct {
						Nodes []struct {
							PullRequest struct {
								Title string `json:"title"`
								URL   string `json:"url"`
							} `json:"pullRequest"`
							OccurredAt time.Time `json:"occurredAt"`
						} `json:"nodes"`
					} `json:"contributions"`
				} `json:"pullRequestReviewContributionsByRepository"`
			} `json:"contributionsCollection"`
		} `json:"user"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

type prItem struct {
	OccurredAt time.Time
	Kind       string
	Repo       string
	Title      string
	URL        string
}

func main() {
	app := tview.NewApplication()
	status := tview.NewTextView().SetDynamicColors(true)
	status.SetBorder(true).SetTitle("Status")

	userField := tview.NewInputField().SetLabel("User: ")
	fromField := tview.NewInputField().SetLabel("From (YYYY-MM-DD): ")
	toField := tview.NewInputField().SetLabel("To (YYYY-MM-DD): ")
	userField.SetText(os.Getenv("GH_USER"))

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
			return
		}
		fromTS, toTS, err := parseRange(from, to)
		if err != nil {
			status.SetText("[red]" + err.Error())
			return
		}

		status.SetText("[yellow]loading contributions...")
		resp, err := fetchContributions(context.Background(), username, fromTS, toTS)
		if err != nil {
			status.SetText("[red]" + err.Error())
			return
		}
		renderSummary(summary, resp)
		renderHeatmap(heatmap, resp)
		prs := collectPRs(resp)
		renderPRList(prList, prDetail, prs)
		status.SetText(fmt.Sprintf("[green]loaded %d PR references", len(prs)))
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
		AddItem(status, 3, 0, false)

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
	if err := app.SetRoot(layout, true).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tui failed: %v\n", err)
		os.Exit(1)
	}
}

func fetchContributions(ctx context.Context, username, fromISO, toISO string) (*contributionResponse, error) {
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		return nil, errors.New("set GITHUB_TOKEN for api access")
	}
	baseURL := strings.TrimSpace(os.Getenv("GITHUB_BASE_URL"))
	uploadURL := strings.TrimSpace(os.Getenv("GITHUB_UPLOAD_URL"))
	if baseURL == "" {
		baseURL = "https://github.com/api/v3/"
	}
	if uploadURL == "" {
		uploadURL = "https://github.com/api/uploads/"
	}
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	if !strings.HasSuffix(uploadURL, "/") {
		uploadURL += "/"
	}

	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	httpClient := oauth2.NewClient(ctx, src)

	client, err := github.NewEnterpriseClient(baseURL, uploadURL, httpClient)
	if err != nil {
		return nil, fmt.Errorf("enterprise client setup failed: %w", err)
	}

	query := `query($login: String!, $from: DateTime!, $to: DateTime!) {
  user(login: $login) {
    login
    name
    contributionsCollection(from: $from, to: $to) {
      startedAt
      endedAt
      totalCommitContributions
      totalIssueContributions
      totalPullRequestContributions
      totalPullRequestReviewContributions
      contributionCalendar {
        totalContributions
        weeks {
          contributionDays {
            date
            contributionCount
            color
          }
        }
      }
      pullRequestContributionsByRepository(maxRepositories: 100) {
        repository { nameWithOwner }
        contributions(first: 100) {
          nodes {
            occurredAt
            pullRequest {
              title
              url
            }
          }
        }
      }
      pullRequestReviewContributionsByRepository(maxRepositories: 100) {
        repository { nameWithOwner }
        contributions(first: 100) {
          nodes {
            occurredAt
            pullRequest {
              title
              url
            }
          }
        }
      }
    }
  }
}`

	reqBody := graphQLRequest{
		Query: query,
		Variables: map[string]string{
			"login": username,
			"from":  fromISO,
			"to":    toISO,
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := client.NewRequest("POST", "graphql", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp := &contributionResponse{}
	_, err = client.Do(ctx, req, resp)
	if err != nil {
		return nil, err
	}
	if len(resp.Errors) > 0 {
		return nil, errors.New(resp.Errors[0].Message)
	}
	if resp.Data.User.Login == "" {
		return nil, errors.New("user not found or inaccessible")
	}
	return resp, nil
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
