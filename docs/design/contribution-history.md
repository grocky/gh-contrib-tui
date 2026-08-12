# Design: Comprehensive contribution history

Status: **Implemented** · Author: Rocky Gray (with Claude) · Date: 2026-07-29

## 1. Problem

The report was wildly under-counting. `nash-robin` shows **237 contributions** on
their profile for the trailing year; the app reported **3** — one pull request
and two reviews.

## 2. Findings

Verified live against `github.example.com` (GHES 3.21.2, token scopes
`repo, read:org, gist, admin:public_key`).

### 2.1 `contributionsCollection` does not see private or internal repositories

For `nash-robin`, window `2025-07-29 .. 2026-07-29`:

```
totalCommitContributions              0
totalIssueContributions               0
totalPullRequestContributions         1
totalPullRequestReviewContributions   2
contributionCalendar.totalContributions 3
restrictedContributionsCount          0
hasAnyRestrictedContributions         false
```

The same window through the search APIs, using the same token:

| Source | Count |
| --- | --- |
| Pull requests opened (`author:` + `created:`) | 57 (48 merged) |
| Issues opened | 0 |
| Pull requests reviewed (`reviewed-by:`) | 141 |
| Commits (`/search/commits`, `author-date:`) | 37 |
| **Total** | **235** — against the 237 on the profile |

Every repository behind those numbers is `PRIVATE` or `INTERNAL` (`originops/*`).
That is the whole gap.

The two flags matter as much as the totals: `restrictedContributionsCount: 0`
and `hasAnyRestrictedContributions: false` mean the endpoint does not merely
withhold the detail of private work — it does not admit the work exists. A
caller has no way to detect the omission from the response.

### 2.2 It is not a permission or a viewer problem

The token already carries `repo`, which is exactly why search can see all 235.
And the asymmetry is not about viewing *other* people: for the viewer's own
account, `contributionsCollection` reported 2 pull requests over the same
window where search found 9.

So on GHES, `contributionsCollection` is unusable for an organization whose
work happens in private and internal repositories — which is to say, for this
one.

## 3. Approach

Drop `contributionsCollection`. Assemble history from four search streams,
which honour the token's actual read access, and aggregate them locally.

| Stream | Query | API |
| --- | --- | --- |
| Pull requests and issues opened | `author:LOGIN created:FROM..TO` | GraphQL `search(type: ISSUE)` |
| Reviews submitted | `is:pr reviewed-by:LOGIN updated:>=FROM` | GraphQL `search(type: ISSUE)` |
| Commits on default branches | `author:LOGIN author-date:FROM..TO` | REST `/search/commits` |
| Commits on other branches | `refs` + `history(author:, since:)` | GraphQL `repository` |

Pull requests and issues arrive from one search, discriminated by `__typename`.

**Review windowing.** Search cannot filter on when a review was submitted, only
on when the pull request was last updated. The query therefore has no upper
bound — a review inside the window can sit on a pull request updated long
after — and each pull request carries `reviews(author: LOGIN)` with real
`submittedAt` timestamps. The domain layer filters on those. The lower bound is
safe: submitting a review updates the pull request, so `updated:>=FROM` cannot
miss one.

**Aggregation** lives in `contrib.NewCollection`, which is pure: it drops
events outside the window, deduplicates on kind+URL, sorts newest first, counts
by kind, and builds the calendar. The calendar is now ours rather than
GitHub's, so it spans the queried window (not a fixed year) with week columns
starting on the Monday at or before the window, shaded by each day's share of
the busiest day.

**Merged pull requests** are tracked separately: `TotalMergedPullRequests`
counts how many of the pull requests *opened* in the window have since landed,
so shipped work is distinguishable from opened work. They may have merged after
the window closed.

## 4. What counts

GitHub's own set: commits, issues opened, pull requests opened, reviews
submitted. Comments are deliberately excluded — `commenter:nash-robin` matches 159
threads in the window, which would inflate the total well past anything
comparable to the profile figure.

Our year total for `nash-robin` is **261**, against 237 on the profile. The
difference is review granularity: we count **167 review submissions across 138
distinct pull requests**. Counting one contribution per reviewed pull request
would give 232. GitHub's exact rule is undocumented and its "last year" window
has its own boundaries and timezone, so an exact match is not a goal; a
distinct event per review submission is, since each has its own timestamp and
permalink and belongs on its own day of the heatmap.

## 5. Cost and limits

- Four to roughly ten HTTP requests per report, against one before. A trailing
  year for a heavy contributor measured **~3s**; a light one **~1s**.
- Each stream paginates at 100 per page and stops after 10 pages. Truncation is
  logged with the advice to narrow the range — never silent.
- `/search/commits` is the one stream a deployment could plausibly have
  disabled. Its failure is non-fatal: the other three still render, and the
  report carries a `Warnings` entry that the summary pane shows in yellow. An
  auth failure is *not* swallowed — it propagates so the token fallback can try
  the next credential.
- Search endpoints have their own rate limit (30 requests/minute
  authenticated), which one report does not come close to.

## 6. Alternatives rejected

- **Keep `contributionsCollection` and add search alongside it.** Two panes
  disagreeing by 80×, with the wrong number in the headline.
- **Fix only the pull request list.** Leaves the summary and heatmap wrong,
  which is where the discrepancy was noticed.
- **`restrictedContributionsCount` as a fallback total.** It reports 0 here; the
  endpoint does not know about the work at all.

## 7. Amendment: commits that never reached a default branch

Date: 2026-08-12. Status: **Implemented**.

Section 8 below originally recorded default-branch-only commit search as an
accepted limitation, on the grounds that it matched GitHub's own counting. It
does not match what the people using the report are being measured on.

One user was reported as having made **0 commits** in the trailing 30 days.
They had made **5** — one on an unmerged feature branch in one repository, four
on an unmerged feature branch in another.

None were reachable from a default branch, so `/search/commits` returned
nothing for them: scoped to the repository holding them, `author:LOGIN
repo:OWNER/NAME` matches 0. This is not a ranking problem; the commits are
absent from the index.

Two cheaper repairs were tested against the live host and both fail:

- **Derive commits from pull requests.** The branch holding the first commit
  has no pull request in any state. Pushed-but-never-proposed branches are
  exactly the blind spot.
- **The events API.** `/users/:user/events` returns no `PushEvent`s: for a user
  other than the viewer it carries public events only, and every repository
  here is private.

So the branches are walked directly, via `repository.refs` with
`history(author:, since:)`. What keeps that from being a crawl:

- **Repositories are pruned by `pushedAt`** — only those written to inside the
  window can hold a commit inside it — 6 of 32 in that measurement.
- **Branches whose tip predates the window are skipped** without reading their
  history, since a branch holds nothing newer than its own tip.

**Branches cannot be pruned before they are fetched.** `RefOrderField` offers
`ALPHABETICAL` and `TAG_COMMIT_DATE`, and the latter does not order *branches*
by commit date — asked for it `DESC`, the host returned refs in reverse
alphabetical order with tips ranging from 2024 to 2026 in no date order. An
early implementation stopped the walk at the first old tip and consequently
found nothing at all. The scan reads every branch of a recently-pushed
repository, bounded at 400.

**Dates.** `history(since:)` filters on committer date; the window is measured
in author date, as commit search is. `since` is the looser of the two —
rebasing moves committer date forward, never back — so the walk cannot miss an
in-window commit, and `NewCollection` drops whatever falls outside on the author
date. There is no `until` for the same reason.

**Overlap.** A branch's history includes every ancestor, so a commit arrives
once per branch built on it, and again from commit search once it lands.
Dedupe on kind+URL collapses these. The branch stream is collected strictly
after commit search so the default-branch copy wins, and the scan skips the
default ref outright — a landed commit is never labelled with a branch.

**Counting.** These are commits like any other: they count toward
`TotalCommits` and the calendar. The event carries `Branch`, and the list
renders `owner/repo@branch`, so unmerged work is identifiable without being
second-class. This is a deliberate departure from GitHub's counting.

**Cost.** One repository listing plus one query per recently-pushed repository.
A trailing 30 days for the user above measured **7.1s**, against 3.5s before.
Failure is non-fatal and warns, as commit search does; note that losing commit
search no longer means losing every commit, since this stream is independent.

## 8. Follow-ups

- Commit search matches on the account, so commits authored under an unlinked
  email are missed. The branch scan filters history by the same account node
  ID, so it shares the limitation. Worth measuring before doing anything.
- The branch scan runs per person, so a team dashboard repeats the same
  repository walks for everyone on it. Scanning each repository once and
  attributing commits to authors afterwards would collapse that.
- The heatmap column headers are week ordinals. Now that the calendar is built
  locally, month labels are straightforward.
