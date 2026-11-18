// Package userdirs locates the per-user config and cache base directories.
//
// The standard library only honors the XDG variables on Linux and the BSDs; on
// macOS it always answers ~/Library. Command-line tools are commonly configured
// under ~/.config on macOS too, so an XDG variable, when set, wins on every
// platform, and the standard library's answer is the fallback.
package userdirs

import (
	"os"
	"path/filepath"
)

// Config returns the base directory for user configuration: $XDG_CONFIG_HOME
// when it is an absolute path, otherwise os.UserConfigDir.
func Config(getenv func(string) string) (string, error) {
	if dir := xdg(getenv, "XDG_CONFIG_HOME"); dir != "" {
		return dir, nil
	}
	return os.UserConfigDir()
}

// Cache returns the base directory for user caches: $XDG_CACHE_HOME when it is
// an absolute path, otherwise os.UserCacheDir.
func Cache(getenv func(string) string) (string, error) {
	if dir := xdg(getenv, "XDG_CACHE_HOME"); dir != "" {
		return dir, nil
	}
	return os.UserCacheDir()
}

// xdg reads an XDG base directory variable. The spec says a relative path is
// invalid and must be ignored.
func xdg(getenv func(string) string, key string) string {
	dir := getenv(key)
	if !filepath.IsAbs(dir) {
		return ""
	}
	return dir
}
