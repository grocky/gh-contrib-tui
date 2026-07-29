// Package platform wraps the host-OS integrations the UI needs: opening a URL
// in the default browser and copying text to the system clipboard.
package platform

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// ErrNoClipboard is returned when no supported clipboard helper is installed.
var ErrNoClipboard = errors.New("no clipboard command found")

// clipboardCommands are tried in order; the first installed one that succeeds wins.
var clipboardCommands = [][]string{
	{"xclip", "-selection", "clipboard"},
	{"wl-copy"},
	{"pbcopy"},
}

// OpenURL launches the URL in the user's default browser.
func OpenURL(url string) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("empty url")
	}
	return browserCommand(url).Start()
}

// CopyToClipboard writes s to the system clipboard, trying each supported
// helper in turn. If a helper is installed but fails, its error is reported
// rather than being flattened into ErrNoClipboard.
func CopyToClipboard(s string) error {
	var attempted []error
	for _, argv := range clipboardCommands {
		if _, err := exec.LookPath(argv[0]); err != nil {
			continue
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin = strings.NewReader(s)
		if err := cmd.Run(); err != nil {
			attempted = append(attempted, fmt.Errorf("%s: %w", argv[0], err))
			continue
		}
		return nil
	}
	if len(attempted) > 0 {
		return errors.Join(attempted...)
	}
	return ErrNoClipboard
}

func browserCommand(url string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url)
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return exec.Command("xdg-open", url)
	}
}
