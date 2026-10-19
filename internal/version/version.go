package version

import (
	"runtime/debug"
	"strings"
)

// GoReleaser sets these with ldflags at build time. A binary from go install
// or go build has no ldflags, so init reads them from the build information
// that the go command stores in each binary.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		fromBuildInfo(info)
	}
}

// fromBuildInfo sets each value that ldflags did not set. "go install
// github.com/juthrbog/lazycloud@v0.6.0" gives the module version v0.6.0. A
// go build in a git checkout gives the commit and the commit time.
func fromBuildInfo(info *debug.BuildInfo) {
	if v := info.Main.Version; Version == "dev" && v != "" && v != "(devel)" {
		Version = strings.TrimPrefix(v, "v")
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if Commit == "none" {
				Commit = s.Value[:min(len(s.Value), 7)]
			}
		case "vcs.time":
			if Date == "unknown" {
				Date = s.Value
			}
		}
	}
}
