package version

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFromBuildInfo(t *testing.T) {
	vcs := []debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
		{Key: "vcs.time", Value: "2026-10-19T12:00:00Z"},
	}
	cases := []struct {
		name                string
		ldflags             [3]string // Version, Commit, Date before init
		main                string
		settings            []debug.BuildSetting
		version, commit, at string
	}{
		{
			name:    "go install with a version",
			ldflags: [3]string{"dev", "none", "unknown"},
			main:    "v0.6.0",
			version: "0.6.0", commit: "none", at: "unknown",
		},
		{
			name:     "go build in a git checkout",
			ldflags:  [3]string{"dev", "none", "unknown"},
			main:     "(devel)",
			settings: vcs,
			version:  "dev", commit: "0123456", at: "2026-10-19T12:00:00Z",
		},
		{
			name:     "go build of a tagged commit",
			ldflags:  [3]string{"dev", "none", "unknown"},
			main:     "v0.6.0",
			settings: vcs,
			version:  "0.6.0", commit: "0123456", at: "2026-10-19T12:00:00Z",
		},
		{
			name:     "GoReleaser ldflags win",
			ldflags:  [3]string{"0.6.0", "abcdef1", "2026-10-18T09:00:00Z"},
			main:     "v0.5.0",
			settings: vcs,
			version:  "0.6.0", commit: "abcdef1", at: "2026-10-18T09:00:00Z",
		},
		{
			name:    "no build information",
			ldflags: [3]string{"dev", "none", "unknown"},
			version: "dev", commit: "none", at: "unknown",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			saved := [3]string{Version, Commit, Date}
			t.Cleanup(func() { Version, Commit, Date = saved[0], saved[1], saved[2] })
			Version, Commit, Date = tc.ldflags[0], tc.ldflags[1], tc.ldflags[2]

			info := &debug.BuildInfo{Settings: tc.settings}
			info.Main.Version = tc.main
			fromBuildInfo(info)

			assert.Equal(t, tc.version, Version)
			assert.Equal(t, tc.commit, Commit)
			assert.Equal(t, tc.at, Date)
		})
	}
}
