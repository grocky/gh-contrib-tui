// Package ui renders the terminal front end. It owns the bubbletea model and
// its lipgloss styling only; all GitHub access is delegated to a
// ContributionService.
package ui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/grocky/gh-contrib-tui/internal/contrib"
	"github.com/grocky/gh-contrib-tui/internal/reportcache"
	"github.com/grocky/gh-contrib-tui/internal/teamconfig"
)

// ContributionService is the GitHub-facing behavior the UI depends on.
// ghapi.Client satisfies it.
type ContributionService interface {
	FetchContributions(ctx context.Context, username string, window contrib.Range) (*contrib.Report, error)
	DiscoverPeople(ctx context.Context) ([]contrib.Person, error)

	// Viewer identifies the authenticated account, which is who a first launch
	// seeds the team with.
	Viewer(ctx context.Context) (contrib.Person, error)
}

// DirectoryCache persists the user directory between runs. It is bound to a
// host by the caller, so the UI never needs to know which GitHub it is talking
// to. directorycache.Cache satisfies it.
type DirectoryCache interface {
	Load() ([]contrib.Person, bool)
	Store(people []contrib.Person) error
}

// TeamStore persists the dashboard's roster. teamconfig.Store satisfies it.
type TeamStore interface {
	Load() (teamconfig.Team, error)
	Save(team teamconfig.Team) error
}

// ReportCache serves contribution reports between runs so the dashboard paints
// before the network answers. reportcache.Cache satisfies it.
type ReportCache interface {
	Load(login string, window contrib.Range) (reportcache.Entry, bool)
	Store(login string, window contrib.Range, report *contrib.Report) error
}

// App is the terminal application.
type App struct {
	model model
}

// Option customizes an App.
type Option func(*App)

// WithDefaultUser pre-fills the username field and opens on that person's
// details rather than on the team dashboard: naming a user is asking for them.
func WithDefaultUser(user string) Option {
	return func(a *App) {
		user = strings.TrimSpace(user)
		if user == "" {
			return
		}
		a.model.inputs[focusUser].SetValue(user)
		a.model.inputs[focusUser].CursorEnd()
		a.model.screen = screenDetails
	}
}

// WithService sets the GitHub-facing service.
func WithService(svc ContributionService) Option {
	return func(a *App) { a.model.svc = svc }
}

// WithDirectoryCache supplies a cache for user suggestions.
func WithDirectoryCache(cache DirectoryCache) Option {
	return func(a *App) { a.model.cache = cache }
}

// WithTeamStore supplies the persisted dashboard roster.
func WithTeamStore(store TeamStore) Option {
	return func(a *App) { a.model.teams = store }
}

// WithReportCache supplies the between-runs report cache.
func WithReportCache(cache ReportCache) Option {
	return func(a *App) { a.model.reports = cache }
}

// WithHost labels the header with the GitHub deployment in use.
func WithHost(host string) Option {
	return func(a *App) { a.model.host = host }
}

// New builds the App. The service may be supplied here or later via
// SetService, which lets callers construct a service that logs into this
// App's log pane.
func New(opts ...Option) *App {
	a := &App{model: newModel()}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// SetService attaches the GitHub-facing service after construction.
func (a *App) SetService(svc ContributionService) { a.model.svc = svc }

// SetDirectoryCache attaches a suggestion cache after construction.
func (a *App) SetDirectoryCache(cache DirectoryCache) { a.model.cache = cache }

// SetTeamStore attaches the roster store after construction.
func (a *App) SetTeamStore(store TeamStore) { a.model.teams = store }

// SetReportCache attaches the report cache after construction.
func (a *App) SetReportCache(cache ReportCache) { a.model.reports = cache }

// SetHost labels the header with the GitHub deployment in use.
func (a *App) SetHost(host string) { a.model.host = host }

// Logf appends a line to the log pane. It is safe to call from any goroutine
// and is intended to be handed to collaborators as their logger.
func (a *App) Logf(format string, args ...any) {
	a.model.logs.Logf(format, args...)
}

// Run starts the event loop and blocks until the user quits.
func (a *App) Run() error {
	if a.model.svc == nil {
		return fmt.Errorf("ui: no contribution service configured")
	}
	_, err := tea.NewProgram(a.model, tea.WithAltScreen()).Run()
	return err
}
