// Package platform wraps the host-OS integrations the UI needs: opening a URL
// in the default browser and copying text to the system clipboard.
package platform

import (
	"errors"
	"fmt"
	"os"
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

func resolveRuntimeOS() string {
	// The existence of WSLInterop means we are either on a WSL 1 or WSL 2 host.
	_, err := os.Stat("/proc/sys/fs/binfmt_misc/WSLInterop")
	if err == nil {
		return "wsl"
	}
	return runtime.GOOS
}

func browserCommand(url string) *exec.Cmd {
	return browserCommandFor(resolveRuntimeOS(), url)
}

// browserCommandFor maps a host to its opener. It takes the host as an argument
// so the mapping can be tested for every host from any one of them -- WSL in
// particular is only detectable by a file that exists on the machine you happen
// to be running on.
func browserCommandFor(host, url string) *exec.Cmd {
	switch host {
	case "darwin":
		return exec.Command("open", url)
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "wsl":
		return exec.Command("explorer.exe", url)
	default:
		return exec.Command("xdg-open", url)
	}
}
