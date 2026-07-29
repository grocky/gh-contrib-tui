package ui

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// maxLogLines bounds the log pane's memory. Long sessions discard the oldest.
const maxLogLines = 500

// colorTag matches the markup the API layer writes into its log messages. That
// layer predates this UI and speaks tview's tag syntax; rather than make every
// caller aware of the renderer, the tags are translated here and stripped from
// the text. Only known colour names are matched, so bracketed text in an error
// message or a pull request title survives untouched.
var colorTag = regexp.MustCompile(`\[(gray|grey|white|blue|yellow|green|red|-)\]`)

type logLine struct {
	at    time.Time
	text  string
	level statusLevel
}

// logBuffer collects log lines. Collaborators log from their own goroutines,
// so it is guarded.
type logBuffer struct {
	mu    sync.Mutex
	lines []logLine
}

func newLogBuffer() *logBuffer {
	return &logBuffer{}
}

// Logf records one line, translating any tview colour tags it carries.
func (b *logBuffer) Logf(format string, args ...any) {
	raw := fmt.Sprintf(format, args...)
	line := logLine{at: time.Now(), text: colorTag.ReplaceAllString(raw, ""), level: levelOf(raw)}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, line)
	if len(b.lines) > maxLogLines {
		b.lines = b.lines[len(b.lines)-maxLogLines:]
	}
}

// levelOf reads the severity from the first colour tag in a message.
func levelOf(raw string) statusLevel {
	match := colorTag.FindStringSubmatch(raw)
	if match == nil {
		return statusInfo
	}
	switch match[1] {
	case "red":
		return statusError
	case "yellow":
		return statusWarn
	case "green":
		return statusOK
	default:
		return statusInfo
	}
}

// Render styles the buffer for display, wrapped to width.
func (b *logBuffer) Render(width int) string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.lines) == 0 {
		return faintStyle.Render("no activity yet")
	}
	body := lipgloss.NewStyle()
	if width > 0 {
		body = body.Width(width)
	}

	out := make([]string, 0, len(b.lines))
	for _, line := range b.lines {
		stamp := faintStyle.Render(line.at.Format("15:04:05") + " ")
		out = append(out, body.Render(stamp+line.level.style().Render(line.text)))
	}
	return strings.Join(out, "\n")
}

// Lines returns the plain text of every line, oldest first.
func (b *logBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]string, 0, len(b.lines))
	for _, line := range b.lines {
		out = append(out, line.text)
	}
	return out
}
