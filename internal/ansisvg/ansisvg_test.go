package ansisvg

import (
	"encoding/xml"
	"strings"
	"testing"
)

// parse fails the test unless the output is well-formed XML, which is the one
// thing a browser will not forgive.
func parse(t *testing.T, svg string) {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(svg))
	for {
		_, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				return
			}
			t.Fatalf("output is not well-formed XML: %v\n%s", err, svg)
		}
	}
}

func TestConvert_PlainText(t *testing.T) {
	got := Convert("hello", Options{Title: "a frame"})
	parse(t, got)

	if !strings.Contains(got, ">hello<") {
		t.Errorf("output does not contain the text:\n%s", got)
	}
	if !strings.Contains(got, "<title>a frame</title>") {
		t.Errorf("output has no title:\n%s", got)
	}
}

// The canvas is the widest line by however many lines there are, measured in
// cells rather than bytes.
func TestConvert_SizesToTheWidestLine(t *testing.T) {
	got := Convert("ab\nabcd\nabc", Options{})
	parse(t, got)

	// Through a variable: converting a fractional constant to int is a compile
	// error, not a truncation.
	widest := 4 * cellWidth
	want := int(widest) + int(padX)*2
	if !strings.Contains(got, `width="`+itoa(want)+`"`) {
		t.Errorf("width is not the widest line (%d):\n%s", want, firstLine(got))
	}
}

// Escapes are measured as nothing at all, and colours become fills.
func TestConvert_TrueColorForeground(t *testing.T) {
	got := Convert("\x1b[38;2;255;128;0mwarm\x1b[0m", Options{})
	parse(t, got)

	if !strings.Contains(got, `fill="#ff8000"`) {
		t.Errorf("foreground colour was lost:\n%s", got)
	}
	if !strings.Contains(got, ">warm<") {
		t.Errorf("text was lost:\n%s", got)
	}
}

func TestConvert_BackgroundBecomesARect(t *testing.T) {
	got := Convert("\x1b[48;2;0;64;0mgrass\x1b[0m", Options{})
	parse(t, got)

	if !strings.Contains(got, `fill="#004000"`) {
		t.Errorf("background colour was lost:\n%s", got)
	}
	if strings.Count(got, "<rect") < 2 {
		t.Errorf("expected a page rect and a cell rect:\n%s", got)
	}
}

func TestConvert_BoldAndFaint(t *testing.T) {
	bold := Convert("\x1b[1mloud\x1b[0m", Options{})
	if !strings.Contains(bold, "font-weight") {
		t.Errorf("bold was lost:\n%s", bold)
	}

	faint := Convert("\x1b[2mquiet\x1b[0m", Options{})
	if !strings.Contains(faint, "opacity") {
		t.Errorf("faint was lost:\n%s", faint)
	}
}

func TestConvert_ResetClearsState(t *testing.T) {
	got := Convert("\x1b[1;38;2;255;0;0mred\x1b[0mplain", Options{})
	parse(t, got)

	// The span carrying the text after the reset must not have inherited the
	// colour or the weight before it.
	span := spanContaining(t, got, "plain")
	if strings.Contains(span, "#ff0000") || strings.Contains(span, "font-weight") {
		t.Errorf("state leaked past the reset: %q", span)
	}
	if red := spanContaining(t, got, "red"); !strings.Contains(red, "#ff0000") {
		t.Errorf("the coloured span lost its colour: %q", red)
	}
}

// spanContaining returns the tspan element whose text is want.
func spanContaining(t *testing.T, svg, want string) string {
	t.Helper()
	for _, part := range strings.Split(svg, "<tspan ")[1:] {
		end := strings.Index(part, "</tspan>")
		if end < 0 {
			continue
		}
		span := part[:end]
		if strings.Contains(span, ">"+want) {
			return span
		}
	}
	t.Fatalf("no span carrying %q:\n%s", want, svg)
	return ""
}

// Block characters are geometry, not writing. Their advance varies by font, and
// a glyph wider than its cell paints over whatever follows it, so they are
// drawn rather than typeset.
func TestConvert_DrawsBlocksAsRects(t *testing.T) {
	got := Convert("\x1b[38;2;0;255;0m███\x1b[0m", Options{})
	parse(t, got)

	if strings.Contains(got, "█") {
		t.Errorf("blocks were typeset rather than drawn:\n%s", got)
	}
	if n := strings.Count(got, `fill="#00ff00"`); n != 3 {
		t.Errorf("drew %d green cells, want one rect per block:\n%s", n, got)
	}
}

// Half blocks fill half the cell, which is what lets the dashboard put two
// weekdays on one line.
func TestConvert_HalfBlocksFillHalfACell(t *testing.T) {
	upper := Convert("\x1b[38;2;0;255;0m▀\x1b[0m", Options{})
	lower := Convert("\x1b[38;2;0;255;0m▄\x1b[0m", Options{})
	parse(t, upper)
	parse(t, lower)

	half := ftoa(lineHeight / 2)
	if !strings.Contains(upper, `height="`+half+`"`) {
		t.Errorf("upper half block is not half a cell tall:\n%s", upper)
	}
	if !strings.Contains(lower, `height="`+half+`"`) {
		t.Errorf("lower half block is not half a cell tall:\n%s", lower)
	}
	// The lower half starts halfway down; the upper one starts at the top.
	if strings.Contains(lower, `y="`+ftoa(padY)+`"`) {
		t.Errorf("lower half block is drawn at the top of its cell:\n%s", lower)
	}
}

// Renderers disagree about xml:space, and a collapsed run shifts the rest of
// its line, so spaces are paid for in x rather than written out.
func TestConvert_SpacesArePositionNotText(t *testing.T) {
	got := Convert("    indented", Options{})
	parse(t, got)

	if strings.Contains(got, ">    indented<") {
		t.Errorf("leading spaces were written out:\n%s", got)
	}
	if !strings.Contains(got, ">indented<") {
		t.Errorf("text was lost:\n%s", got)
	}
	if !strings.Contains(got, `x="`+ftoa(padX+4*cellWidth)+`"`) {
		t.Errorf("text is not offset by the four spaces before it:\n%s", got)
	}
}

// A line of nothing but spaces has nothing to draw.
func TestConvert_BlankLinesDrawNothing(t *testing.T) {
	got := Convert("a\n   \nb", Options{})
	parse(t, got)

	if n := strings.Count(got, "<text"); n != 2 {
		t.Errorf("drew %d text elements, want one per non-empty line:\n%s", n, got)
	}
}

// The frames contain repository names and titles, which contain the characters
// XML reserves.
func TestConvert_EscapesMarkup(t *testing.T) {
	got := Convert(`a & b <tag> "quoted"`, Options{Title: "x & y"})
	parse(t, got)

	if strings.Contains(got, "<tag>") {
		t.Errorf("markup was not escaped:\n%s", got)
	}
	if !strings.Contains(got, "&amp;") {
		t.Errorf("ampersand was not escaped:\n%s", got)
	}
}

// A cell is a cell whatever it costs in bytes: an ellipsis is three bytes and
// one column, and measuring bytes would shift everything after it.
func TestConvert_MeasuresInCellsNotBytes(t *testing.T) {
	// An ellipsis, then a colour change so the rest is a run of its own: it has
	// to start one cell along, not three.
	got := Convert("…\x1b[38;2;255;0;0mx\x1b[0m", Options{})
	parse(t, got)

	span := spanContaining(t, got, "x")
	if !strings.Contains(span, `x="`+ftoa(padX+cellWidth)+`"`) {
		t.Errorf("run after a multi-byte character is misplaced: %q", span)
	}
}

// Unknown sequences are skipped rather than printed: a stray escape rendered as
// text would be worse than one ignored.
func TestConvert_IgnoresSequencesItDoesNotKnow(t *testing.T) {
	got := Convert("\x1b[38;5;42mpaletted\x1b[0m", Options{})
	parse(t, got)

	if strings.Contains(got, "38;5") || strings.Contains(got, "\x1b") {
		t.Errorf("an escape leaked into the output:\n%s", got)
	}
	if !strings.Contains(got, ">paletted<") {
		t.Errorf("text was lost:\n%s", got)
	}
}

func TestConvert_Empty(t *testing.T) {
	got := Convert("", Options{})
	parse(t, got)
	if !strings.Contains(got, "<svg") {
		t.Errorf("empty input produced no document:\n%s", got)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Spaces inside a run are position too. SVG collapses runs of whitespace in
// text content, so a month row drawn as one string arrives with its labels
// bunched together at the left.
func TestConvert_SpacesInsideARunArePositionToo(t *testing.T) {
	got := Convert("May   Jun", Options{})
	parse(t, got)

	if strings.Contains(got, ">May   Jun<") {
		t.Fatalf("the gap between the labels is text, which renderers collapse:\n%s", got)
	}
	jun := spanContaining(t, got, "Jun")
	if !strings.Contains(jun, `x="`+ftoa(padX+6*cellWidth)+`"`) {
		t.Errorf("second label = %q, want it placed at the column it starts in", jun)
	}
}

// A single space between words is writing, not layout: renderers keep it, and
// pinning every word to the grid separately makes the spacing jitter wherever
// the font disagrees with the cell width by a fraction.
func TestConvert_KeepsSingleSpacesInTheText(t *testing.T) {
	got := Convert("Drop the vendored fork", Options{})
	parse(t, got)

	if n := strings.Count(got, "<tspan"); n != 1 {
		t.Errorf("drew %d spans, want the phrase kept whole:\n%s", n, got)
	}
	if !strings.Contains(got, ">Drop the vendored fork<") {
		t.Errorf("phrase was broken up:\n%s", got)
	}
}

// Box drawing.

// Borders are geometry too. Typeset, the horizontal rule renders at whatever
// width the font gives its glyph, which leaves it short of the corner the
// column grid puts at the end of the line.
func TestConvert_DrawsBordersAsLines(t *testing.T) {
	got := Convert("╭──╮", Options{})
	parse(t, got)

	for _, ch := range []string{"╭", "─", "╮"} {
		if strings.Contains(got, ch) {
			t.Errorf("%q was typeset rather than drawn:\n%s", ch, got)
		}
	}
	if n := strings.Count(got, "<path"); n != 4 {
		t.Errorf("drew %d paths, want one per border cell:\n%s", n, got)
	}
}

// A rule has to reach the full width of every cell it occupies, or the corner
// at the end of the line does not meet it.
func TestConvert_RuleSpansItsCells(t *testing.T) {
	got := Convert("──", Options{})
	parse(t, got)

	// The second cell's rule runs to the right edge of the second cell.
	if !strings.Contains(got, `H`+ftoa(padX+2*cellWidth)) {
		t.Errorf("rule stops short of its cell:\n%s", got)
	}
}

// The sides of a panel are drawn down the middle of their column, so every row
// of a border lines up with the one above it.
func TestConvert_SidesShareAColumn(t *testing.T) {
	got := Convert("│\n│", Options{})
	parse(t, got)

	centre := ftoa(padX + cellWidth/2)
	if n := strings.Count(got, "M"+centre+","); n != 2 {
		t.Errorf("the two sides are not on the same centre line:\n%s", got)
	}
}

// A panel's line is a border, a gap, and its contents, all in one run.
func TestConvert_MixesDrawingAndTextInOneRun(t *testing.T) {
	got := Convert("│ Team", Options{})
	parse(t, got)

	if strings.Contains(got, "│") {
		t.Errorf("the border was typeset:\n%s", got)
	}
	span := spanContaining(t, got, "Team")
	if !strings.Contains(span, `x="`+ftoa(padX+2*cellWidth)+`"`) {
		t.Errorf("text beside a border is misplaced: %q", span)
	}
}
