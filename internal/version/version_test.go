package version_test

import (
	"runtime/debug"
	"testing"

	"github.com/grocky/gh-contrib-tui/internal/version"
)

func TestString(t *testing.T) {
	t.Parallel()

	build := func(mainVersion string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Version: mainVersion}}
	}

	tests := []struct {
		name   string
		linked string
		info   *debug.BuildInfo
		want   string
	}{
		{name: "release build stamps the version", linked: "v0.1.0", info: build("(devel)"), want: "v0.1.0"},
		{name: "go install at a tag reports the module version", info: build("v0.1.0"), want: "v0.1.0"},
		{name: "local build in a checkout reports its pseudo-version", info: build("v0.0.0-20261118175501-44920e65c3c9+dirty"), want: "v0.0.0-20261118175501-44920e65c3c9+dirty"},
		{name: "local build outside a checkout", info: build("(devel)"), want: "(devel)"},
		{name: "empty module version", info: build(""), want: "(devel)"},
		{name: "no build info at all", want: "(devel)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := version.String(tc.linked, tc.info); got != tc.want {
				t.Errorf("String(%q, info) = %q, want %q", tc.linked, got, tc.want)
			}
		})
	}
}
