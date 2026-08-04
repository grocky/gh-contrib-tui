package platform

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenURL_RejectsEmpty(t *testing.T) {
	if err := OpenURL("   "); err == nil {
		t.Fatal("expected an error for an empty url")
	}
}

func TestBrowserCommand_TargetsURL(t *testing.T) {
	const url = "https://example.com/pr/1"
	cmd := browserCommand(url)
	if len(cmd.Args) == 0 {
		t.Fatal("expected a command to be built")
	}
	if got := cmd.Args[len(cmd.Args)-1]; got != url {
		t.Fatalf("last arg = %q, want the url", got)
	}
}

// Every host's opener is checked from whichever host the tests run on. WSL
// reports itself as linux and is told apart by a file on the machine, so a test
// that asks runtime.GOOS what to expect is wrong on exactly the platform the
// WSL case was added for.
func TestBrowserCommandFor_MapsEveryHost(t *testing.T) {
	const url = "https://example.com/pr/1"
	tests := map[string]string{
		"darwin":  "open",
		"windows": "rundll32",
		"wsl":     "explorer.exe",
		"linux":   "xdg-open",
	}
	for host, want := range tests {
		cmd := browserCommandFor(host, url)
		if !strings.HasSuffix(cmd.Args[0], want) {
			t.Errorf("%s: command = %q, want %q", host, cmd.Args[0], want)
		}
		if got := cmd.Args[len(cmd.Args)-1]; got != url {
			t.Errorf("%s: last arg = %q, want the url", host, got)
		}
	}
}

// The host this test is running on has to be one the mapping knows, or the
// opener silently falls through to the linux default.
func TestResolveRuntimeOS_IsAKnownHost(t *testing.T) {
	switch got := resolveRuntimeOS(); got {
	case "darwin", "windows", "wsl", "linux":
	default:
		t.Fatalf("resolveRuntimeOS = %q, which the mapping does not name", got)
	}
}

func TestCopyToClipboard_NoCommandAvailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := CopyToClipboard("hello")
	if !errors.Is(err, ErrNoClipboard) {
		t.Fatalf("err = %v, want ErrNoClipboard", err)
	}
}

func TestCopyToClipboard_UsesFirstWorkingCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub requires a POSIX shell")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "clipboard.txt")

	// Stub the first candidate (xclip) so the real clipboard is never touched.
	stub := "#!/bin/sh\ncat > " + out + "\n"
	if err := os.WriteFile(filepath.Join(dir, "xclip"), []byte(stub), 0o755); err != nil {
		t.Fatalf("writing stub: %v", err)
	}
	// Prepend rather than replace so the stub's own shell utilities resolve.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := CopyToClipboard("https://example.com/pr/1"); err != nil {
		t.Fatalf("CopyToClipboard returned error: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading stub output: %v", err)
	}
	if string(got) != "https://example.com/pr/1" {
		t.Fatalf("clipboard content = %q", string(got))
	}
}

func TestCopyToClipboard_FallsBackWhenCommandFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub requires a POSIX shell")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "clipboard.txt")

	if err := os.WriteFile(filepath.Join(dir, "xclip"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("writing failing stub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wl-copy"), []byte("#!/bin/sh\ncat > "+out+"\n"), 0o755); err != nil {
		t.Fatalf("writing stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := CopyToClipboard("payload"); err != nil {
		t.Fatalf("CopyToClipboard returned error: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("expected fallback command to run: %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("clipboard content = %q", string(got))
	}
}
