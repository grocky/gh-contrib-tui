// Command ansi2svg turns a captured terminal frame into a self-contained SVG.
//
// It exists so the screenshots in the README are the real thing: run the app
// with its colour profile forced on, capture the frame, and convert it here. No
// glyph in the output depends on the reader having a terminal font installed --
// the block characters the charts are drawn from become rectangles.
//
//	ansi2svg -title "team dashboard" frame.ansi > dashboard.svg
//	some-program | ansi2svg -o out.svg
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/grocky/gh-contrib-tui/internal/ansisvg"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ansi2svg: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		title = flag.String("title", "", "document title, announced by readers")
		out   = flag.String("o", "", "write to this file instead of stdout")
		bg    = flag.String("bg", "", "page colour (default suits a dark terminal)")
		fg    = flag.String("fg", "", "text colour (default suits a dark terminal)")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"Usage: ansi2svg [flags] [file]\n\nConverts a captured ANSI terminal frame to SVG.\n"+
				"Reads standard input when no file is given.\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	frame, err := readFrame(flag.Arg(0))
	if err != nil {
		return err
	}

	svg := ansisvg.Convert(frame, ansisvg.Options{
		Title:      *title,
		Background: *bg,
		Foreground: *fg,
	})

	if *out == "" {
		_, err := io.WriteString(os.Stdout, svg)
		return err
	}
	// Written whole rather than streamed: a half-written SVG in a README is
	// worse than none.
	return os.WriteFile(*out, []byte(svg), 0o644)
}

func readFrame(path string) (string, error) {
	if path == "" || path == "-" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("reading stdin: %w", err)
		}
		return string(raw), nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	return string(raw), nil
}
