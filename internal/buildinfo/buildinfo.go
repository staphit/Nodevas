// Package buildinfo says which commit this binary was built from.
//
// A build from a git checkout gets it from the VCS stamp Go writes into the
// binary. A build without one (the container image: .dockerignore leaves .git
// out) gets it from the linker:
//
//	go build -ldflags "-X nodevas/internal/buildinfo.commit=<40-char sha>" ./cmd/nodevas
//
// The hub reports its commit to its runners (GET /api/runner/version), and
// scripts/install.sh uses it to pick which GitHub-built runner binary to
// fetch. The commit only selects a binary; the installer verifies what it
// fetched against GitHub, never against the hub.
package buildinfo

import (
	"regexp"
	"runtime/debug"
)

// commit is set with -ldflags -X; see the package comment.
var commit string

var fullCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

// IsCommit reports whether s is a full commit: 40 lowercase hex characters.
func IsCommit(s string) bool { return fullCommit.MatchString(s) }

// Commit is the full commit this binary was built from, or "" when it is not
// known. A linker-set value that is not a full commit is ignored rather than
// passed on.
func Commit() string {
	if IsCommit(commit) {
		return commit
	}
	revision, _ := vcs()
	if IsCommit(revision) {
		return revision
	}
	return ""
}

// Version identifies this build in log lines and `nodevas runner status`: the
// module version when it has one, else the commit's first seven characters
// ("-dirty" when the checkout had changes), else "dev".
func Version() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	if IsCommit(commit) {
		return commit[:7]
	}
	revision, dirty := vcs()
	if revision == "" {
		return "dev"
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}
	if dirty {
		revision += "-dirty"
	}
	return revision
}

// vcs reads the VCS stamp Go writes into a binary built in a checkout.
func vcs() (revision string, dirty bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	return revision, dirty
}
