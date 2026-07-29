// Package logging defines the minimal logging contract shared between the
// business-logic packages and whatever front end is driving them.
package logging

// Logf receives human-readable progress messages. The TUI renders them into a
// log pane; tests and headless callers can pass Nop.
type Logf func(format string, args ...any)

// Nop discards all messages. Use it instead of a nil Logf.
func Nop(string, ...any) {}

// OrNop returns logf, or Nop when logf is nil, so callers never need a nil check.
func OrNop(logf Logf) Logf {
	if logf == nil {
		return Nop
	}
	return logf
}
