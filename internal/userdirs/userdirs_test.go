package userdirs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/grocky/gh-contrib-tui/internal/userdirs"
)

func TestConfig(t *testing.T) {
	t.Parallel()
	absolute := filepath.Join(t.TempDir(), "config")
	platform, platformErr := os.UserConfigDir()

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "absolute XDG_CONFIG_HOME wins", value: absolute, want: absolute},
		{name: "unset falls back to the platform dir", value: "", want: platform},
		{name: "relative is ignored per the XDG spec", value: "relative/config", want: platform},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := userdirs.Config(env("XDG_CONFIG_HOME", tc.value))
			if tc.want == platform && platformErr != nil {
				if err == nil {
					t.Fatalf("Config(%q) error = nil, want %v", tc.value, platformErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Config(%q) error = %v", tc.value, err)
			}
			if got != tc.want {
				t.Errorf("Config(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestCache(t *testing.T) {
	t.Parallel()
	absolute := filepath.Join(t.TempDir(), "cache")
	platform, platformErr := os.UserCacheDir()

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "absolute XDG_CACHE_HOME wins", value: absolute, want: absolute},
		{name: "unset falls back to the platform dir", value: "", want: platform},
		{name: "relative is ignored per the XDG spec", value: "relative/cache", want: platform},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := userdirs.Cache(env("XDG_CACHE_HOME", tc.value))
			if tc.want == platform && platformErr != nil {
				if err == nil {
					t.Fatalf("Cache(%q) error = nil, want %v", tc.value, platformErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Cache(%q) error = %v", tc.value, err)
			}
			if got != tc.want {
				t.Errorf("Cache(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

// env returns a getenv that knows a single variable.
func env(key, value string) func(string) string {
	return func(k string) string {
		if k == key {
			return value
		}
		return ""
	}
}
