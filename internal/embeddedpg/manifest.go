package embeddedpg

import (
	"fmt"
	"runtime"
)

// PinnedVersion is the PostgreSQL release every target is pinned to: the
// theseus-rs/postgresql-binaries build that bundles ICU on every platform
// (17.11.0's Linux builds lack it, and migration 0001 creates an ICU
// collation). The desktop release workflows read it from this file
// (desktop/scripts/verify-pg-bundle.mjs).
const PinnedVersion = "17.11.1"

// Target is one platform's pinned archive.
type Target struct {
	// Name is GOOS-GOARCH, e.g. "windows-amd64".
	Name string
	// Archive is the release asset's file name.
	Archive string
	// URLs are tried in order: our immutable mirror first, upstream second.
	// Every one is https, and the hash is enforced whichever answers.
	URLs []string
	// SHA256 is the archive's hex digest.
	SHA256 string
	// Size is the archive's length in bytes, for the progress line.
	Size int64
	// Root is the archive's top-level directory.
	Root string
}

// mirrorBase is our own release holding byte-identical copies of the upstream
// archives. Upstream releases are mutable (theseus-rs re-released 14.x/15.x
// builds in place in October 2026), so the hash below is the pin and the
// mirror only keeps the download working if upstream assets move.
const mirrorBase = "https://github.com/staphit/Nodevas/releases/download/pg-binaries-" + PinnedVersion + "/"

// upstreamBase is theseus-rs/postgresql-binaries, the fallback.
const upstreamBase = "https://github.com/theseus-rs/postgresql-binaries/releases/download/" + PinnedVersion + "/"

func pinned(name, triple, ext, sha string, size int64) Target {
	root := "postgresql-" + PinnedVersion + "-" + triple
	archive := root + ext
	return Target{
		Name:    name,
		Archive: archive,
		URLs:    []string{mirrorBase + archive, upstreamBase + archive},
		SHA256:  sha,
		Size:    size,
		Root:    root,
	}
}

// Targets is the compiled-in manifest, one entry per release target.
//
// The hashes were taken from upstream's .sha256 files on 2026-10-28 and the
// Linux x64 and Windows x64 archives were hashed locally as well. Changing a
// hash is a deliberate supply-chain decision: the same values are pinned again
// in manifest_test.go so a change shows up twice in review.
func Targets() []Target {
	return []Target{
		pinned("windows-amd64", "x86_64-pc-windows-msvc", ".zip",
			"4dee656719d4182055219765c29c9e692993d14ba0d1b4344dd24a8ff7296782", 48338921),
		pinned("linux-amd64", "x86_64-unknown-linux-gnu", ".tar.gz",
			"aa8e2e97574bd789119ac01f1dddcd7a038c84e5c308d7b753018618258f0fbd", 40073705),
		pinned("linux-arm64", "aarch64-unknown-linux-gnu", ".tar.gz",
			"edaab3230a014680ebedd836824962628f66ceabd94c36ba3f953bf9e04dc166", 39478345),
		pinned("darwin-amd64", "x86_64-apple-darwin", ".tar.gz",
			"edb44b25cf8bffec60fa300fbe100fa4238bf2cab4f4bd98d4b7255c61780df3", 93265621),
		pinned("darwin-arm64", "aarch64-apple-darwin", ".tar.gz",
			"f4b2ff0587d80b6f22bf09ec2d133a3c2949b6423318d3fed0f4ddddddf2f004", 90030572),
	}
}

// TargetFor returns the manifest entry for a platform.
func TargetFor(goos, goarch string) (Target, bool) {
	name := goos + "-" + goarch
	for _, target := range Targets() {
		if target.Name == name {
			return target, true
		}
	}
	return Target{}, false
}

// TargetNamed returns the manifest entry with that name ("windows-amd64").
func TargetNamed(name string) (Target, bool) {
	for _, target := range Targets() {
		if target.Name == name {
			return target, true
		}
	}
	return Target{}, false
}

// CurrentTarget is TargetFor this process's platform.
func CurrentTarget() (Target, error) {
	target, ok := TargetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return Target{}, fmt.Errorf("embeddedpg: no pinned PostgreSQL for %s-%s; set NODEVAS_DATABASE_URL", runtime.GOOS, runtime.GOARCH)
	}
	return target, nil
}

// executable is a binary's file name on the target's OS.
func executable(target Target, name string) string {
	if isWindowsTarget(target) {
		return name + ".exe"
	}
	return name
}

func isWindowsTarget(target Target) bool {
	return len(target.Name) >= 7 && target.Name[:7] == "windows"
}
