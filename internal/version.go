package internal

import (
	"github.com/Masterminds/semver/v3"
)

var (
	Branch  = "main"
	Version = "0.21.6"
	Commit  = ""
	Date    = ""
)

// FullVersion returns the full semantic version string. FullVersion panics if
// the version string is not a valid semantic version.
func FullVersion() string {
	if Version == "vdev" { // v + dev for dev image
		return Version // don't chck the version for dev images
	} else {
		return semver.MustParse(Version).String()
	}
}
