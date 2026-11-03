package embeddedpg

import (
	"regexp"
	"strings"
	"testing"
)

// The archive hashes are the supply-chain pin: checked against the upstream
// .sha256 files on 2026-10-28 (Linux x64 and Windows x64 also hashed locally).
// Changing one is a deliberate act that must show up in review here too.
var pinnedSHA256 = map[string]string{
	"windows-amd64": "4dee656719d4182055219765c29c9e692993d14ba0d1b4344dd24a8ff7296782",
	"linux-amd64":   "aa8e2e97574bd789119ac01f1dddcd7a038c84e5c308d7b753018618258f0fbd",
	"linux-arm64":   "edaab3230a014680ebedd836824962628f66ceabd94c36ba3f953bf9e04dc166",
	"darwin-amd64":  "edb44b25cf8bffec60fa300fbe100fa4238bf2cab4f4bd98d4b7255c61780df3",
	"darwin-arm64":  "f4b2ff0587d80b6f22bf09ec2d133a3c2949b6423318d3fed0f4ddddddf2f004",
}

func TestManifestPinsEveryReleaseTarget(t *testing.T) {
	if PinnedVersion != "17.11.1" {
		t.Fatalf("PinnedVersion = %q; 17.11.0 lacks ICU on Linux and breaks migration 0001", PinnedVersion)
	}
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	seen := map[string]bool{}
	for _, target := range Targets() {
		if seen[target.Name] {
			t.Errorf("target %s listed twice", target.Name)
		}
		seen[target.Name] = true
		want, pinned := pinnedSHA256[target.Name]
		if !pinned {
			t.Errorf("unexpected target %s", target.Name)
			continue
		}
		if !hex64.MatchString(target.SHA256) || target.SHA256 != want {
			t.Errorf("%s sha256 = %q, want %s", target.Name, target.SHA256, want)
		}
		if !strings.Contains(target.Archive, "postgresql-"+PinnedVersion+"-") {
			t.Errorf("%s archive %q is not version %s", target.Name, target.Archive, PinnedVersion)
		}
		if target.Root == "" || target.Size <= 0 {
			t.Errorf("%s root %q size %d", target.Name, target.Root, target.Size)
		}
		if len(target.URLs) < 2 {
			t.Errorf("%s needs the mirror and the upstream fallback, has %v", target.Name, target.URLs)
		}
		for _, url := range target.URLs {
			if !strings.HasPrefix(url, "https://") || !strings.HasSuffix(url, "/"+target.Archive) {
				t.Errorf("%s url %q must be https and name the archive", target.Name, url)
			}
		}
	}
	for name := range pinnedSHA256 {
		if !seen[name] {
			t.Errorf("target %s missing from the manifest", name)
		}
	}
	if _, err := CurrentTarget(); err != nil {
		t.Errorf("CurrentTarget on the test platform: %v", err)
	}
}
