// Command github-contrib-tui is a terminal browser for a GitHub user's
// contribution activity: summary totals, a contribution heatmap, and the pull
// requests they authored or reviewed.
package main

import (
	"fmt"
	"os"

	"gh-contribs/internal/ghapi"
	"gh-contribs/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "github-contrib-tui: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	app := ui.New(ui.WithDefaultUser(os.Getenv("GH_USER")))
	app.SetService(ghapi.New(ghapi.WithLogger(app.Logf)))
	return app.Run()
}
