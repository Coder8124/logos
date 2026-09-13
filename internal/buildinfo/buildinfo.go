// Package buildinfo holds the one version string this product reports.
//
// It exists because there were three. `brain version` printed a value stamped
// at link time, the MCP handshake announced a hard-coded "0.1.0", and the npm
// wrapper carried a third number in its manifest. An MCP client that asked the
// server what it was talking to got an answer that had been wrong since the
// v0.1.1 tag, and would have stayed wrong through every release after it,
// because nothing about cutting a tag touches a string literal buried in a
// handshake reply.
//
// A version people read off a running process has to come from the build, not
// from a constant someone has to remember to bump. So there is one variable,
// stamped by scripts/release.sh, and every surface that reports a version reads
// it from here.
package buildinfo

import (
	"regexp"
	"runtime/debug"
)

// Version is the release this binary was built from, stamped at link time:
//
//	-ldflags "-X github.com/Coder8124/brain/internal/buildinfo.Version=v0.1.2"
//
// An unstamped build says "dev", which is the honest answer for one.
var Version = "dev"

// `go install github.com/Coder8124/brain/cmd/brain@v0.4.2` is the route the
// plugin and the npm wrapper tell people to take, and it never passes ldflags,
// so that binary said "dev" and `brain update` refused to check it. The Go
// toolchain does record the module version it fetched, so an unstamped build
// takes that instead — but only a plain release tag. A local `go build` records
// a pseudo-version or "+dirty", and announcing that as a release would send
// the updater comparing against something that was never published.
func init() {
	if Version != "dev" {
		return
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		Version = fromModule(Version, bi.Main.Version)
	}
}

var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

func fromModule(stamped, module string) string {
	if releaseTag.MatchString(module) {
		return module
	}
	return stamped
}
