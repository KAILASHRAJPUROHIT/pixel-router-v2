// Package buildinfo holds a build-time-stamped version string, set via
// -ldflags (see deploy/build.sh) rather than derived from git (this
// repo has no .git here) or left as a guess from a binary's mtime -
// confirmed live 2026-07-11 that mtimes alone made "is this the binary
// I think it is" a recurring source of doubt during redeploys.
package buildinfo

// Version is overwritten at build time via:
//
//	-ldflags "-X github.com/antriva/pixel-router-v2/internal/buildinfo.Version=<value>"
//
// Left as "dev" for any binary built without that flag (e.g. `go run`
// during local development).
var Version = "dev"
