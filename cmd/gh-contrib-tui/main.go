// Command gh-contrib-tui is a terminal browser for a GitHub user's
// contribution activity: summary totals, a contribution heatmap, and the pull
// requests they authored or reviewed.
package main

import (
	"fmt"
	"os"

	"gh-contrib/internal/directorycache"
	"gh-contrib/internal/ghapi"
	"gh-contrib/internal/reportcache"
	"gh-contrib/internal/teamconfig"
	"gh-contrib/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gh-contrib-tui: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	app := ui.New(ui.WithDefaultUser(os.Getenv("GH_USER")))
	client := ghapi.New(ghapi.WithLogger(app.Logf))
	host := client.Config().Host

	app.SetService(client)
	app.SetHost(host)

	// Each store is scoped to the host: a login on github.com and the same
	// login on an Enterprise deployment are different accounts, and their teams
	// and reports must not be shared.
	app.SetDirectoryCache(directorycache.Open(host))
	app.SetTeamStore(teamconfig.Open(host))
	app.SetReportCache(reportcache.Open(host))
	return app.Run()
}
