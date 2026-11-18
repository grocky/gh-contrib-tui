# Design: Organization-wide user suggestions

Status: **Draft** · Author: Rocky Gray (with Claude) · Date: 2026-07-29

## 1. Goal

The `User:` field should autocomplete against **every member of the viewer's
organizations**, matching on both GitHub login and display name, and it should
be available immediately on startup rather than after a network round trip.

## 2. Findings from the current implementation

Verified against the live instance this repo is configured for
(`github.example.com`, GHES 3.21.2, token scopes `repo, read:org, gist,
admin:public_key`).

### 2.1 The data layer already returns the whole org

Running the existing `ghapi.Client.DiscoverUserLogins` end to end returns **59
logins** — all 58 `originops` members plus one extra. So the premise that only
the viewer's own handle is retrieved does not hold at the API layer today; the
data is arriving, and the defect is downstream.

### 2.2 The suggestions never reach the dropdown (root cause)

`tview` invokes the autocomplete callback in exactly three situations
(`inputfield.go`): when `SetAutocompleteFunc` is first called, when the field's
text changes, and on <kbd>↓</kbd>. We call `SetAutocompleteFunc` during widget
construction — while the store is still empty — and when background discovery
completes we populate the store but never re-trigger the dropdown.

Consequently the dropdown is empty until the user types, and if discovery is
slow or the user has already typed, it stays empty for that keystroke. This is
almost certainly the observed symptom. A related aggravator: if `GH_USER` is
set, the field is pre-filled with the viewer's own handle, so the first
<kbd>↓</kbd> filters the list down to exactly that one login — matching the
"only my own handle is suggested" description precisely.

`InputField.Autocomplete()` is explicitly documented as safe to call from any
goroutine, so this is a small fix.

### 2.3 Real robustness gaps in discovery

| Gap | Impact |
| --- | --- |
| No pagination — `PerPage: 100` is set but `NextPage` is never followed | Any org over 100 members is silently truncated. `originops` has 58, so this is latent here but will bite on a larger org. |
| Logins only, no display names | Users must know the login. These are corporate accounts: login `nash-robin`, name `Nash, Robin [USA]`. Searching "Robin" finds nothing today. |
| N+1 REST calls (1 + orgs + teams) | 5 HTTP round trips for one org; scales linearly with orgs and teams. |
| No cache | Every launch re-fetches; suggestions are unavailable for the first ~0.7s (worse on a large org or slow VPN). |
| Failures only reach the log pane | A `read:org`-less token degrades to viewer-only with no visible explanation. |

### 2.4 Team membership is *not* redundant with org membership

I assumed team members were a strict subset of org members and could be
dropped. That is wrong. `Bianchi-Priya` appears in `originops/dev-*` teams
but is absent from both `/orgs/originops/members` and GraphQL
`membersWithRole`. The reason: the account is **suspended**
(`suspended_at: 2025-09-22`). GHES drops suspended users from org member
listings but retains them in team listings.

This matters for a contribution browser — a suspended/departed teammate is
exactly the kind of person whose past contributions you want to look up. **The
team query must be kept** as a second source, not deleted as an optimization.

## 3. API options evaluated

### Option A — GraphQL, one query (recommended)

```graphql
query($orgCursor: String, $memberCursor: String) {
  viewer {
    login
    name
    organizations(first: 10, after: $orgCursor) {
      totalCount
      pageInfo { hasNextPage endCursor }
      nodes {
        login
        membersWithRole(first: 100, after: $memberCursor) {
          totalCount
          pageInfo { hasNextPage endCursor }
          nodes { login name }
        }
      }
    }
  }
}
```

Verified working on GHES 3.21.2: returned all 58 members with names in a single
request, plus `totalCount` and cursors.

- ✅ One round trip for the common case (viewer + all orgs + all members)
- ✅ Returns `name` — enables name-based matching
- ✅ Cursor pagination with `totalCount` for progress reporting
- ✅ We already have a GraphQL client and endpoint resolution
- ⚠️ Nested pagination is awkward: if one org has >100 members, the follow-up
  query must target that org directly (`organization(login:)`) rather than
  re-walking `viewer.organizations`
- ⚠️ `rateLimit` returns `null` on this GHES instance, so we cannot read budget
  from the response — treat rate limiting as unobservable here

### Option B — REST, paginated (current approach, fixed)

`GET /user/orgs` → `GET /orgs/{org}/members` → `GET /user/teams` →
`GET /organizations/{org_id}/team/{team_id}/members`, each following
`NextPage`.

- ✅ Already implemented; well-understood; `go-github` handles pagination state
- ✅ Works when a token/host rejects GraphQL
- ❌ No display names (would need `GET /users/{login}` per person — unacceptable)
- ❌ N+1 round trips

**Role: fallback only**, and the source of truth for the team query.

### Option C — Search API

`GET /search/users?q=type:user+org:originops`

- ❌ Hard cap of 1000 results, 30 requests/minute, and it only matches indexed
  public profile data. Strictly worse than A or B here. **Rejected.**

### Option D — Outside collaborators

`GET /orgs/{org}/outside_collaborators`

- ❌ Returns HTTP 403 on this instance: requires org **owner** plus the
  `admin:org` scope, which the `gh` token does not carry and which we should
  not ask users to grant for an autocomplete feature. **Rejected**, but see
  §8 — outside collaborators can still author PRs.

### Decision

**GraphQL (A) as the primary source for org members, REST (B) retained for the
team query** (which catches suspended members GraphQL omits) **and as a whole
fallback if GraphQL fails.**

## 4. Proposed design

### 4.1 New domain type

Suggestions become people, not strings. In `internal/contrib` (it is the shared
domain package and has no outbound dependencies):

```go
// Person is a GitHub account offered as a suggestion.
type Person struct {
    Login  string
    Name   string   // display name; may be empty
    Orgs   []string // orgs they were discovered through
    Active bool     // false for accounts only found via team membership (e.g. suspended)
}

// Directory is a searchable set of people.
type Directory struct { ... }

func NewDirectory(people []Person) *Directory
func (d *Directory) Match(query string, limit int) []Person
func (d *Directory) Len() int
```

`Match` ranking, best-first:

1. exact login match
2. login prefix match
3. name-token prefix match (so `robin` hits `Nash, Robin [USA]`)
4. login substring
5. name substring

Ties break alphabetically by login; inactive accounts sort after active ones at
equal rank. Matching is case-insensitive and ignores punctuation in names so
`nash robin`, `robin nash`, and `nash-robin` all converge.

### 4.2 `ghapi` changes

```go
// DiscoverPeople returns everyone the viewer can see across their orgs.
// Partial failures are logged and skipped; it errors only if nothing at all
// could be loaded.
func (c *Client) DiscoverPeople(ctx context.Context) ([]contrib.Person, error)
```

Internally:

1. `graphqlDirectory(ctx)` — Option A, following `pageInfo` cursors; per-org
   follow-up queries via `organization(login:)` when an org exceeds 100 members.
2. `restTeamMembers(ctx)` — Option B teams walk, merged in and marked
   `Active: false` when the login was not present in the org member set.
3. On GraphQL failure, fall back to the existing full REST walk (now paginated).

`DiscoverUserLogins` is removed; `ui` moves to `DiscoverPeople`. It has one
caller, so there is no reason to keep both.

### 4.3 Caching — `internal/directorycache`

Suggestions should be instant on the second launch.

- Path: `os.UserCacheDir()/gh-contrib/directory-<host>.json`
  (`<host>` sanitized, so github.com and GHES caches never collide)
- Written `0600` — this is org roster data, not secret, but not world-readable either
- Payload: `{schemaVersion, host, viewer, fetchedAt, people[]}`
- TTL **24h**. On startup: load cache → populate the directory immediately →
  always refresh in the background → rewrite the cache and refresh the dropdown
- A corrupt, unreadable, or schema-mismatched cache is discarded silently and
  treated as a cold start — never fatal
- `--refresh-users` / `Ctrl+U` forces a re-fetch (see §8)

### 4.4 UI changes

1. **Re-trigger the dropdown when data lands.** In `loadSuggestions`'s
   `QueueUpdateDraw`, call `a.userField.Autocomplete()` after
   `a.suggest.set(...)`. This alone fixes the reported symptom.
2. **Display names, insert logins.** Entries render as
   `nash-robin — Nash, Robin [USA]`; `SetAutocompletedFunc` intercepts selection
   and writes only `nash-robin` into the field. Without this, tview inserts the
   entry verbatim and the query breaks.
3. **Mark inactive accounts** with a dim `(suspended)` suffix so it is obvious
   why a familiar name behaves differently.
4. **Status line feedback**: `58 users loaded` on success; on failure, the
   actionable cause rather than a silent degrade — e.g.
   `user list unavailable: token lacks read:org — run 'gh auth refresh -s read:org'`.
5. Raise the dropdown cap from 25 to 50 and make it a constant tied to the
   directory, not the store.

### 4.5 Package layout after the change

```
internal/
  contrib/         + Person, Directory, Match ranking      (pure, 100% testable)
  ghapi/           + DiscoverPeople, GraphQL org walk, REST fallback
  directorycache/  NEW — load/store/expire the on-disk roster
  ui/              suggestion store delegates to contrib.Directory
```

No new third-party dependencies.

## 5. Testing plan (TDD)

Tests are written before the implementation, in this order:

1. **`contrib.Directory.Match`** — table-driven ranking tests: exact > prefix >
   name-token > substring; case-insensitivity; `robin nash` vs `nash robin` vs
   `nash-robin`; limit enforcement; inactive accounts sorting last; empty query
   returns the head of the list; no aliasing of the backing slice.
2. **`ghapi.DiscoverPeople`** — against `httptest`, as with the existing suite:
   - single-page happy path, asserting the pinned GraphQL document
   - multi-page org members (cursor followed, all pages merged)
   - multi-page orgs
   - an org whose members query 403s → skipped, others still returned
   - GraphQL total failure → REST fallback produces the same people
   - team-only member is merged with `Active: false`
   - 401 → next token candidate (existing `withTokens` coverage extended)
3. **`directorycache`** — round-trip; TTL expiry; corrupt JSON discarded;
   schema-version mismatch discarded; file mode is `0600`; unwritable cache
   directory is non-fatal.
4. **`ui`** — a fake service that blocks until released, asserting
   `Autocomplete()` is re-triggered once results land (regression test for the
   root cause); entry rendering is `login — Name` while the accepted value is
   the bare login; suspended marker; status text on failure.

Existing coverage stays green; the live-API check stays out of CI and remains a
manual diagnostic.

## 6. Phasing

| Phase | Scope | Value |
| --- | --- | --- |
| **1** | §4.4.1 only — call `Autocomplete()` when suggestions arrive | Fixes the reported bug in ~3 lines |
| **2** | `contrib.Person` + `Directory` + GraphQL `DiscoverPeople` with pagination | All members, with names, one round trip |
| **3** | `directorycache` | Instant suggestions on launch |
| **4** | UI polish — name rendering, suspended markers, actionable errors | Discoverability |

Phase 1 is worth shipping on its own, immediately.

## 7. Risks

- **Nested GraphQL pagination** is the most error-prone part; the >100-member
  path is untestable against this instance (58 members) and must be covered by
  `httptest` fixtures.
- **`read:org` is required.** Without it `/user/orgs` returns an empty array
  rather than an error, which looks identical to "you have no orgs" — hence the
  explicit remediation message in §4.4.4.
- **Cache staleness** for a newly onboarded teammate, bounded at 24h and
  mitigated by the always-on background refresh.

## 8. Open questions

1. **Multi-org users.** The viewer here has one org. Should suggestions from
   different orgs be grouped, or flat with an org hint in the entry?
2. **Suspended accounts** — include (current proposal), hide behind a flag, or
   exclude? Including them seems right for a historical contributions tool.
3. **Outside collaborators** can author PRs but are unreachable without
   `admin:org` (§3D). Worth harvesting logins opportunistically from the
   contribution results the user already fetches?
4. **Force-refresh binding** — `Ctrl+U` is free; is a CLI flag wanted too, given
   the app currently takes no flags?
5. **Cap on org size.** Should we bound the walk (e.g. 5000 people) to protect
   very large enterprise orgs, and surface a "showing first N" note?
