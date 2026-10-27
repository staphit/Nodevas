package embeddedpg

import (
	"fmt"
	"runtime"
)

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

// Targets is the compiled-in manifest, one entry per release target.
//
// RED PHASE stub: empty.
func Targets() []Target { return nil }

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

// CurrentTarget is TargetFor this process's platform.
func CurrentTarget() (Target, error) {
	target, ok := TargetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return Target{}, fmt.Errorf("embeddedpg: no pinned PostgreSQL for %s-%s; set NODEVAS_DATABASE_URL", runtime.GOOS, runtime.GOARCH)
	}
	return target, nil
}
