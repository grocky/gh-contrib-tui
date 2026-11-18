// Command gh-contrib-tui is a terminal browser for a GitHub user's
// contribution activity: summary totals, a contribution heatmap, and the pull
// requests they authored or reviewed.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/grocky/gh-contrib-tui/internal/directorycache"
	"github.com/grocky/gh-contrib-tui/internal/ghapi"
	"github.com/grocky/gh-contrib-tui/internal/reportcache"
	"github.com/grocky/gh-contrib-tui/internal/teamconfig"
	"github.com/grocky/gh-contrib-tui/internal/ui"
	"github.com/grocky/gh-contrib-tui/internal/version"
)

// buildVersion is stamped in by release builds:
// -ldflags "-X main.buildVersion=v0.1.0".
var buildVersion string

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "gh-contrib-tui: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("gh-contrib-tui", flag.ContinueOnError)
	flags.SetOutput(stderr)
	showVersion := flags.Bool("version", false, "print the version and exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVersion {
		info, _ := debug.ReadBuildInfo()
		fmt.Fprintln(stdout, "gh-contrib-tui", version.String(buildVersion, info))
		return nil
	}

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
