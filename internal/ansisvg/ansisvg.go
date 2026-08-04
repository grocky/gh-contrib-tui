// Package ansisvg turns a captured terminal frame into a self-contained SVG.
//
// It handles what a lipgloss-rendered frame contains and nothing else:
// truecolor foreground and background, bold, faint, and reset. Anything it does
// not recognise is dropped rather than guessed at, on the grounds that a stray
// escape printed as text is worse than one ignored.
package ansisvg

import (
	"fmt"
	"strconv"
	"strings"
)

// Cell metrics. The frame is a grid, so every run is placed at the column it
// starts in rather than flowed, and nothing depends on the viewer's font
// metrics matching these.
const (
	cellWidth  = 8.4
	lineHeight = 17.0
	fontSize   = 14
	padX       = 16.0
	padY       = 14.0
)

// Defaults for a frame captured from a dark terminal.
const (
	defaultBackground = "#0d1117"
	defaultForeground = "#c9d1d9"
	fontStack         = `ui-monospace, SFMono-Regular, Menlo, Consolas, "DejaVu Sans Mono", monospace`
)

// block describes a block-drawing character as the rectangle it fills: where it
// starts down the cell, how much of the cell it covers, and how solid it is.
//
// These are geometry rather than writing. Their advance varies by font, and a
// glyph wider than its cell paints over whatever follows it -- which is how a
// bar chart ends up with the number beside it half missing.
type block struct {
	top, height, alpha float64
}

var blocks = map[rune]block{
	'█': {0, 1, 1},
	'▀': {0, 0.5, 1},
	'▄': {0.5, 0.5, 1},
	'▓': {0, 1, 0.75},
	'▒': {0, 1, 0.5},
	'░': {0, 1, 0.25},
	'■': {0.15, 0.7, 1},
}

// Panel borders are geometry for the same reason the blocks are, and a worse
// offender: a rule typeset at the font's own advance stops short of the corner
// the grid puts at the end of the line, which leaves every panel with its
// right-hand corners hanging off the end of the border.
//
// Each character is described by the half-cells its stroke reaches into, so a
// rule covers its whole cell and meets its neighbours exactly, and a side runs
// down the middle of its column on every row.
type boxArms struct{ left, right, up, down, round bool }

var boxes = map[rune]boxArms{
	'─': {left: true, right: true},
	'│': {up: true, down: true},
	'╭': {right: true, down: true, round: true},
	'╮': {left: true, down: true, round: true},
	'╰': {right: true, up: true, round: true},
	'╯': {left: true, up: true, round: true},
	'┌': {right: true, down: true},
	'┐': {left: true, down: true},
	'└': {right: true, up: true},
	'┘': {left: true, up: true},
	'├': {right: true, up: true, down: true},
	'┤': {left: true, up: true, down: true},
	'┬': {left: true, right: true, down: true},
	'┴': {left: true, right: true, up: true},
	'┼': {left: true, right: true, up: true, down: true},
}

// borderStroke is the weight of a drawn border, and cornerRadius how far a
// rounded corner cuts across its cell.
const (
	borderStroke = 1.1
	cornerRadius = 3.0
)

// Options customizes a conversion.
type Options struct {
	// Title is the document's title, which readers announce and browsers show
	// as a tooltip.
	Title string

	// Background and Foreground override the defaults, for a frame captured
	// from a light terminal.
	Background, Foreground string
}

func (o Options) background() string {
	if o.Background != "" {
		return o.Background
	}
	return defaultBackground
}

func (o Options) foreground() string {
	if o.Foreground != "" {
		return o.Foreground
	}
	return defaultForeground
}

// state is the drawing state a run inherits.
type state struct {
	fg, bg      string
	bold, faint bool
}

// run is a stretch of text sharing one state, and the column it starts at.
type run struct {
	text  string
	col   int
	state state
}

// Convert renders a captured ANSI frame as an SVG document.
func Convert(frame string, opts Options) string {
	lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")

	widest := 0
	parsed := make([][]run, 0, len(lines))
	for _, line := range lines {
		runs := splitRuns(line)
		parsed = append(parsed, runs)
		if len(runs) > 0 {
			last := runs[len(runs)-1]
			if end := last.col + cells(last.text); end > widest {
				widest = end
			}
		}
	}

	width := int(float64(widest)*cellWidth) + int(padX)*2
	height := int(float64(len(lines))*lineHeight) + int(padY)*2

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" `+
		`viewBox="0 0 %d %d" font-family="%s" font-size="%d">`+"\n",
		width, height, width, height, escape(fontStack), fontSize)
	if opts.Title != "" {
		fmt.Fprintf(&b, "<title>%s</title>\n", escape(opts.Title))
	}
	fmt.Fprintf(&b, `<rect width="%d" height="%d" rx="8" fill="%s"/>`+"\n",
		width, height, opts.background())

	// Backgrounds first, so nothing drawn on top of them is painted over by a
	// later cell's fill.
	for row, runs := range parsed {
		y := padY + float64(row)*lineHeight
		for _, r := range runs {
			if r.state.bg == "" || strings.TrimSpace(r.text) == "" {
				continue
			}
			fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"/>`+"\n",
				ftoa(padX+float64(r.col)*cellWidth), ftoa(y),
				ftoa(float64(cells(r.text))*cellWidth), ftoa(lineHeight), r.state.bg)
		}
	}

	for row, runs := range parsed {
		y := padY + float64(row)*lineHeight
		var spans strings.Builder
		for _, r := range runs {
			// Every stretch of spaces is paid for in x rather than written out,
			// inside a run as much as before it: SVG collapses runs of
			// whitespace in text content, which would bunch a row of month
			// labels up against the left of the calendar.
			for _, seg := range segments(r) {
				// A panel's line is a border, a gap, and its contents all in
				// one run, so the drawn characters are picked out of the
				// writing rather than the whole segment going one way.
				for _, piece := range pieces(seg) {
					switch {
					case piece.drawn:
						draw(&b, piece.text, piece.col, y, r.state, opts)
					default:
						spans.WriteString(textSpan(piece.text, piece.col, r.state, opts))
					}
				}
			}
		}
		if spans.Len() > 0 {
			fmt.Fprintf(&b, `<text y="%s">%s</text>`+"\n",
				ftoa(y+fontSize*0.8), spans.String())
		}
	}

	b.WriteString("</svg>\n")
	return b.String()
}

// segment is a stretch of a run with no spaces in it, and the column it starts
// at.
type segment struct {
	text string
	col  int
}

// segments splits a run at the gaps that carry layout, leaving the spaces that
// carry language.
//
// A renderer collapses a stretch of whitespace to a single space, so any gap of
// two or more has to become position instead -- otherwise a row of month labels
// bunches up against the left of the calendar. A single space between words
// survives, and is left in the text so the words flow with the font's own
// spacing rather than being pinned one by one to a grid the font may not agree
// with.
func segments(r run) []segment {
	var (
		out   []segment
		runes = []rune(r.text)
		i     int
	)
	for i < len(runes) {
		// Skip the gap, paying for it in the column count.
		if runes[i] == ' ' {
			i++
			continue
		}

		start, text := i, strings.Builder{}
		for i < len(runes) {
			if runes[i] == ' ' {
				// A single space between two words is part of the writing; two
				// or more end the segment.
				if i+1 < len(runes) && runes[i+1] != ' ' && text.Len() > 0 {
					text.WriteRune(' ')
					i++
					continue
				}
				break
			}
			text.WriteRune(runes[i])
			i++
		}
		out = append(out, segment{text: text.String(), col: r.col + start})
	}
	return out
}

// piece is a stretch of a segment that is drawn, or one that is written.
type piece struct {
	text  string
	col   int
	drawn bool
}

// drawable reports whether a character is geometry rather than writing.
func drawable(r rune) bool {
	_, block := blocks[r]
	_, box := boxes[r]
	return block || box
}

// pieces splits a segment where it changes between drawing and writing.
func pieces(seg segment) []piece {
	runes := []rune(seg.text)
	if len(runes) == 0 {
		return nil
	}

	var (
		out   []piece
		start = 0
		mode  = drawable(runes[0])
	)
	flush := func(end int) {
		text := string(runes[start:end])
		if !mode {
			// Spaces at the edge of a written piece are position, not text.
			trimmed := strings.TrimLeft(text, " ")
			start += len([]rune(text)) - len([]rune(trimmed))
			text = strings.TrimRight(trimmed, " ")
		}
		if text != "" {
			out = append(out, piece{text: text, col: seg.col + start, drawn: mode})
		}
	}

	for i, r := range runes {
		if d := drawable(r); d != mode {
			flush(i)
			start, mode = i, d
		}
	}
	flush(len(runes))
	return out
}

// draw renders a piece of geometry: blocks as rectangles, borders as strokes.
func draw(b *strings.Builder, text string, col int, y float64, st state, opts Options) {
	fill := st.fg
	if fill == "" {
		fill = opts.foreground()
	}

	for i, r := range []rune(text) {
		x := padX + float64(col+i)*cellWidth
		if spec, ok := blocks[r]; ok {
			opacity := ""
			if spec.alpha < 1 {
				opacity = fmt.Sprintf(` opacity="%g"`, spec.alpha)
			}
			fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"%s/>`+"\n",
				ftoa(x), ftoa(y+spec.top*lineHeight),
				ftoa(cellWidth), ftoa(spec.height*lineHeight), fill, opacity)
			continue
		}
		if arms, ok := boxes[r]; ok {
			fmt.Fprintf(b, `<path d="%s" stroke="%s" stroke-width="%s" fill="none"/>`+"\n",
				borderPath(arms, x, y), fill, ftoa(borderStroke))
		}
	}
}

// borderPath draws one border cell: a stroke from the middle of the cell out to
// each edge the character reaches, with a rounded join where it turns a corner.
func borderPath(arms boxArms, x, y float64) string {
	cx, cy := x+cellWidth/2, y+lineHeight/2
	radius := 0.0
	if arms.round {
		radius = cornerRadius
	}

	var d []string
	horizontal := func() {
		switch {
		case arms.left && arms.right:
			d = append(d, "M"+ftoa(x)+","+ftoa(cy)+"H"+ftoa(x+cellWidth))
		case arms.left:
			d = append(d, "M"+ftoa(x)+","+ftoa(cy)+"H"+ftoa(cx-radius))
		case arms.right:
			d = append(d, "M"+ftoa(cx+radius)+","+ftoa(cy)+"H"+ftoa(x+cellWidth))
		}
	}
	vertical := func() {
		switch {
		case arms.up && arms.down:
			d = append(d, "M"+ftoa(cx)+","+ftoa(y)+"V"+ftoa(y+lineHeight))
		case arms.up:
			d = append(d, "M"+ftoa(cx)+","+ftoa(y)+"V"+ftoa(cy-radius))
		case arms.down:
			d = append(d, "M"+ftoa(cx)+","+ftoa(cy+radius)+"V"+ftoa(y+lineHeight))
		}
	}
	horizontal()
	vertical()

	// The quarter turn joining the two arms of a rounded corner.
	if radius > 0 {
		fromX, fromY := cx, cy-radius
		if arms.down {
			fromY = cy + radius
		}
		toX := cx - radius
		if arms.right {
			toX = cx + radius
		}
		d = append(d, fmt.Sprintf("M%s,%sQ%s,%s %s,%s",
			ftoa(fromX), ftoa(fromY), ftoa(cx), ftoa(cy), ftoa(toX), ftoa(cy)))
	}
	return strings.Join(d, "")
}

// textSpan renders one run of writing, pinned to the column it starts at.
func textSpan(text string, col int, st state, opts Options) string {
	fill := st.fg
	if fill == "" {
		fill = opts.foreground()
	}

	attrs := fmt.Sprintf(`x="%s" fill="%s"`, ftoa(padX+float64(col)*cellWidth), fill)
	if st.bold {
		attrs += ` font-weight="600"`
	}
	if st.faint && st.fg == "" {
		attrs += ` opacity="0.65"`
	}
	return fmt.Sprintf(`<tspan %s>%s</tspan>`, attrs, escape(text))
}

// splitRuns breaks one line into runs, tracking the drawing state across them
// and recording the column each starts at.
func splitRuns(line string) []run {
	var (
		runs    []run
		current state
		text    strings.Builder
		col     int
	)

	flush := func() {
		if text.Len() == 0 {
			return
		}
		s := text.String()
		runs = append(runs, run{text: s, col: col, state: current})
		col += cells(s)
		text.Reset()
	}

	for i := 0; i < len(line); {
		if params, size, ok := escapeAt(line, i); ok {
			flush()
			current.apply(params)
			i += size
			continue
		}
		r, size := decodeRune(line[i:])
		text.WriteRune(r)
		i += size
	}
	flush()
	return runs
}

// escapeAt reports whether an SGR sequence starts at i, returning its
// parameters and length. Other escape sequences are consumed and discarded.
func escapeAt(s string, i int) (params string, size int, sgr bool) {
	if s[i] != 0x1b || i+1 >= len(s) || s[i+1] != '[' {
		return "", 0, false
	}
	for j := i + 2; j < len(s); j++ {
		c := s[j]
		if (c >= '0' && c <= '9') || c == ';' {
			continue
		}
		if c == 'm' {
			return s[i+2 : j], j - i + 1, true
		}
		// Some other CSI sequence: swallow it whole.
		return "", j - i + 1, true
	}
	return "", len(s) - i, true
}

// apply folds an SGR parameter list into the state.
func (s *state) apply(params string) {
	if params == "" {
		*s = state{}
		return
	}

	codes := strings.Split(params, ";")
	for i := 0; i < len(codes); i++ {
		switch code := atoi(codes[i]); code {
		case 0:
			*s = state{}
		case 1:
			s.bold = true
		case 2:
			s.faint = true
		case 22:
			s.bold, s.faint = false, false
		case 39:
			s.fg = ""
		case 49:
			s.bg = ""
		case 38, 48:
			// Only truecolor is understood. A 256-colour argument is skipped
			// rather than approximated.
			if i+4 < len(codes) && atoi(codes[i+1]) == 2 {
				rgb := fmt.Sprintf("#%02x%02x%02x",
					atoi(codes[i+2]), atoi(codes[i+3]), atoi(codes[i+4]))
				if code == 38 {
					s.fg = rgb
				} else {
					s.bg = rgb
				}
				i += 4
				continue
			}
			if i+2 < len(codes) && atoi(codes[i+1]) == 5 {
				i += 2
			}
		}
	}
}

// cells counts display columns. Every character a frame contains is one column
// wide, but plenty of them are several bytes.
func cells(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

func decodeRune(s string) (rune, int) {
	for i, r := range s {
		if i == 0 {
			return r, runeLen(s)
		}
	}
	return 0, 1
}

func runeLen(s string) int {
	for i := range s {
		if i > 0 {
			return i
		}
	}
	return len(s)
}

func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return -1
	}
	return n
}

func itoa(n int) string { return strconv.Itoa(n) }

// ftoa formats a coordinate without trailing zeros, so the document stays
// readable and small.
func ftoa(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

var escaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
)

func escape(s string) string { return escaper.Replace(s) }
