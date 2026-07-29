// Package ui renders the terminal front end. It owns tview widgets and event
// wiring only; all GitHub access is delegated to a ContributionService.
package ui

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"gh-contribs/internal/contrib"
	"gh-contribs/internal/platform"
)

// discoveryTimeout bounds the background user-suggestion lookup.
const discoveryTimeout = 30 * time.Second

// ContributionService is the GitHub-facing behavior the UI depends on.
// ghapi.Client satisfies it.
type ContributionService interface {
	FetchContributions(ctx context.Context, username string, window contrib.Range) (*contrib.Report, error)
	DiscoverPeople(ctx context.Context) ([]contrib.Person, error)
}

// DirectoryCache persists the user directory between runs. It is bound to a
// host by the caller, so the UI never needs to know which GitHub it is talking
// to. directorycache.Cache satisfies it.
type DirectoryCache interface {
	Load() ([]contrib.Person, bool)
	Store(people []contrib.Person) error
}

// App is the terminal application.
type App struct {
	app     *tview.Application
	svc     ContributionService
	cache   DirectoryCache
	suggest suggestionStore
	running atomic.Bool

	defaultUser string

	userField *tview.InputField
	fromField *tview.InputField
	toField   *tview.InputField
	status    *tview.TextView
	logView   *tview.TextView
	summary   *tview.TextView
	heatmap   *tview.Table
	prList    *tview.List
	prDetail  *tview.TextView
}

// Option customizes an App.
type Option func(*App)

// WithDefaultUser pre-fills the username field.
func WithDefaultUser(user string) Option {
	return func(a *App) { a.defaultUser = strings.TrimSpace(user) }
}

// WithService sets the GitHub-facing service.
func WithService(svc ContributionService) Option {
	return func(a *App) { a.svc = svc }
}

// WithDirectoryCache supplies a cache for user suggestions.
func WithDirectoryCache(cache DirectoryCache) Option {
	return func(a *App) { a.cache = cache }
}

// New builds the App and its widget tree. The service may be supplied here or
// later via SetService, which lets callers construct a service that logs into
// this App's log pane.
func New(opts ...Option) *App {
	a := &App{app: tview.NewApplication()}
	a.buildWidgets()
	for _, opt := range opts {
		opt(a)
	}
	a.userField.SetText(a.defaultUser)
	return a
}

// SetService attaches the GitHub-facing service after construction.
func (a *App) SetService(svc ContributionService) { a.svc = svc }

// SetDirectoryCache attaches a suggestion cache after construction.
func (a *App) SetDirectoryCache(cache DirectoryCache) { a.cache = cache }

// Logf appends a timestamped line to the log pane. It is safe to call from any
// goroutine and is intended to be handed to collaborators as their logger.
func (a *App) Logf(format string, args ...any) {
	fmt.Fprintf(a.logView, "%s %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// Run starts the event loop and blocks until the user quits.
func (a *App) Run() error {
	if a.svc == nil {
		return fmt.Errorf("ui: no contribution service configured")
	}
	a.running.Store(true)
	defer a.running.Store(false)

	go a.loadSuggestions()
	return a.app.SetRoot(a.layout(), true).Run()
}

// redraw refreshes the screen. tview's Draw blocks until the event loop picks
// the update up, so it is only safe once Run is underway.
func (a *App) redraw() {
	if a.running.Load() {
		a.app.Draw()
	}
}

// onUIThread runs fn on the tview event loop and redraws. Queued updates block
// until the loop picks them up, so before Run (and after it returns) fn is
// invoked directly -- there is no loop to hand it to, and no screen to race.
func (a *App) onUIThread(fn func()) {
	if a.running.Load() {
		a.app.QueueUpdateDraw(fn)
		return
	}
	fn()
}

func (a *App) buildWidgets() {
	a.status = tview.NewTextView().SetDynamicColors(true)
	a.status.SetBorder(true).SetTitle("Status")

	a.logView = tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	a.logView.SetBorder(true).SetTitle("Logs")
	a.logView.SetChangedFunc(a.redraw)

	a.userField = tview.NewInputField().SetLabel("User: ")
	a.userField.SetAutocompleteFunc(a.suggest.entries)
	a.userField.SetAutocompletedFunc(a.acceptSuggestion)
	a.fromField = tview.NewInputField().SetLabel("From (YYYY-MM-DD): ")
	a.toField = tview.NewInputField().SetLabel("To (YYYY-MM-DD): ")

	a.summary = tview.NewTextView().SetDynamicColors(true)
	a.summary.SetBorder(true).SetTitle("Contribution Summary")

	a.heatmap = tview.NewTable()
	a.heatmap.SetFixed(1, 1).SetBorders(false)
	a.heatmap.SetBorder(true).SetTitle("Contribution Calendar")

	a.prList = tview.NewList().ShowSecondaryText(true)
	a.prList.SetBorder(true).SetTitle("PR References (Enter=open, Ctrl+Y=copy URL)")
	a.prList.SetSelectedFunc(a.openSelectedPR)

	a.prDetail = tview.NewTextView().SetDynamicColors(true)
	a.prDetail.SetBorder(true).SetTitle("Selected PR URL")

	a.app.SetInputCapture(a.handleKey)
}

func (a *App) layout() tview.Primitive {
	form := tview.NewForm().
		AddFormItem(a.userField).
		AddFormItem(a.fromField).
		AddFormItem(a.toField).
		AddButton("Fetch", a.fetchAndRender)
	form.SetBorder(true).SetTitle("Filters").SetTitleAlign(tview.AlignLeft)

	left := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(form, 11, 0, true).
		AddItem(a.summary, 10, 0, false).
		AddItem(a.status, 3, 0, false).
		AddItem(a.logView, 0, 1, false)

	right := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.heatmap, 0, 3, false).
		AddItem(a.prList, 0, 2, false).
		AddItem(a.prDetail, 3, 0, false)

	return tview.NewFlex().
		AddItem(left, 0, 2, true).
		AddItem(right, 0, 3, false)
}

func (a *App) handleKey(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyCtrlC, tcell.KeyCtrlQ:
		a.app.Stop()
		return nil
	case tcell.KeyCtrlR:
		a.fetchAndRender()
		return nil
	case tcell.KeyCtrlY:
		a.copySelectedPR()
		return nil
	case tcell.KeyCtrlU:
		go a.refreshDirectory(true)
		return nil
	}
	return ev
}

// fetchAndRender validates the filter inputs, loads the report, and repaints
// the summary, heatmap, and PR list.
func (a *App) fetchAndRender() {
	username := strings.TrimSpace(a.userField.GetText())
	if username == "" {
		a.fail("user is required")
		return
	}
	window, err := contrib.ParseRange(a.fromField.GetText(), a.toField.GetText())
	if err != nil {
		a.fail(err.Error())
		return
	}

	a.status.SetText("[yellow]loading contributions...")
	a.Logf("[yellow]fetch start user=%s from=%s to=%s[-]",
		username, window.From.Format(time.RFC3339), window.To.Format(time.RFC3339))

	report, err := a.svc.FetchContributions(context.Background(), username, window)
	if err != nil {
		a.status.SetText("[red]" + err.Error())
		a.Logf("[red]fetch failed: %v[-]", err)
		return
	}

	prs := contrib.CollectPRs(report)
	renderSummary(a.summary, report)
	renderHeatmap(a.heatmap, report)
	renderPRList(a.prList, a.prDetail, prs)
	a.status.SetText(fmt.Sprintf("[green]loaded %d PR references", len(prs)))
	a.Logf("[green]fetch complete total=%d prs=%d[-]", report.Collection.Calendar.Total, len(prs))
}

// loadSuggestions serves cached suggestions immediately, then refreshes them
// from GitHub in the background. Failure is non-fatal and only degrades
// autocomplete.
func (a *App) loadSuggestions() {
	if a.cache != nil {
		if people, ok := a.cache.Load(); ok {
			a.onUIThread(func() { a.applyPeople(people, "cache") })
		}
	}
	a.refreshDirectory(false)
}

// refreshDirectory re-fetches the directory from GitHub. When forced (Ctrl+U)
// the outcome is reported in the status line as well as the log.
func (a *App) refreshDirectory(forced bool) {
	if a.svc == nil {
		return
	}
	if forced {
		a.onUIThread(func() { a.status.SetText("[yellow]refreshing user list...") })
	}

	ctx, cancel := context.WithTimeout(context.Background(), discoveryTimeout)
	defer cancel()
	people, err := a.svc.DiscoverPeople(ctx)

	a.onUIThread(func() {
		if err != nil {
			a.Logf("[yellow]user discovery unavailable: %v[-]", err)
			if forced {
				a.status.SetText("[red]user list refresh failed: " + err.Error())
			}
			return
		}

		a.applyPeople(people, "github")
		if forced {
			a.status.SetText(fmt.Sprintf("[green]%d users loaded", len(people)))
		}
		if a.cache != nil {
			if err := a.cache.Store(people); err != nil {
				a.Logf("[yellow]could not cache user list: %v[-]", err)
			}
		}
	})
}

// applyPeople installs a new directory and refreshes the open dropdown.
//
// The refresh matters: tview only invokes the autocomplete callback when the
// field text changes, on the down arrow, or when Autocomplete is called
// explicitly. Without this call, suggestions that arrive after the widget was
// built stay invisible until the user edits the field.
func (a *App) applyPeople(people []contrib.Person, source string) {
	directory := contrib.NewDirectory(people)
	a.suggest.set(directory)
	a.userField.Autocomplete()

	a.Logf("[green]loaded %d user suggestions from %s[-]", directory.Len(), source)
	if directory.Len() <= 1 && source != "cache" {
		a.Logf("[yellow]only your own account was found; if you expect teammates, " +
			"your token may lack the read:org scope - run 'gh auth refresh -s read:org'[-]")
	}
}

// acceptSuggestion replaces the dropdown label with the bare login, since the
// label also carries the display name and any suspended marker.
func (a *App) acceptSuggestion(entry string, index, source int) bool {
	if index < 0 || source == tview.AutocompletedNavigate {
		return false
	}
	a.userField.SetText(loginFromEntry(entry))
	return true
}

func (a *App) openSelectedPR(int, string, string, rune) {
	url := a.selectedURL()
	if url == "" {
		return
	}
	if err := platform.OpenURL(url); err != nil {
		a.status.SetText("[red]open failed: " + err.Error())
		return
	}
	a.status.SetText("[green]opened in browser")
}

func (a *App) copySelectedPR() {
	url := a.selectedURL()
	if url == "" {
		a.status.SetText("[red]no PR selected")
		return
	}
	if err := platform.CopyToClipboard(url); err != nil {
		a.status.SetText("[yellow]copy not available; URL shown below")
		return
	}
	a.status.SetText("[green]copied URL")
}

func (a *App) selectedURL() string {
	return strings.TrimSpace(a.prDetail.GetText(false))
}

func (a *App) fail(msg string) {
	a.status.SetText("[red]" + msg)
	a.Logf("[red]validation failed: %s[-]", msg)
}
