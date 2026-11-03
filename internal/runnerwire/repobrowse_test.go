package runnerwire

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// B-41: the repo browse messages round-trip on the envelope; the limits
// are the contract's; a diff ref is "" or a full commit id.
func TestRepoBrowseEnvelopeRoundTrip(t *testing.T) {
	env := Envelope{Type: TypeRepoRequest, Repo: &RepoRequest{ID: "q1", AccountID: "u1", RepoID: "repo-1", Op: RepoOpList, Path: "src", Limit: 10}}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"type":"repo_request"`, `"repo":{"id":"q1","accountId":"u1","repoId":"repo-1","op":"list","path":"src","limit":10}`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("request JSON lacks %s: %s", key, raw)
		}
	}
	var back Envelope
	if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, env) {
		t.Fatalf("request round trip = %+v, %v", back, err)
	}

	done := Envelope{Type: TypeRepoDone, RepoDone: &RepoDone{ID: "q1",
		Listing: &RepoListing{Path: "src", Entries: []RepoEntry{{Name: "lib", Type: "dir"}, {Name: "out", Type: "symlink", Escape: true}}, Hidden: 1},
		Status:  &RepoGitStatus{Branch: "main", Head: strings.Repeat("a", 40), Entries: []RepoStatusEntry{{Path: "a.go", Mark: "M"}}},
		Log:     []RepoCommit{{SHA: strings.Repeat("b", 40), At: "2026-10-28T10:00:00Z", Author: "ann", Subject: "fix"}},
		Diff:    &RepoDiff{Patch: "diff --git a/a.go b/a.go", Truncated: true},
	}}
	raw, _ = json.Marshal(done)
	for _, key := range []string{`"repoDone":{"id":"q1"`, `"escape":true`, `"hidden":1`, `"mark":"M"`, `"truncated":true`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("done JSON lacks %s: %s", key, raw)
		}
	}
	back = Envelope{}
	if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, done) {
		t.Fatalf("done round trip = %+v, %v", back, err)
	}

	if MaxRepoFileBytes != 512<<10 || MaxRepoPatchBytes != 256<<10 || MaxRepoEntries != 1000 || MaxRepoDoneBytes != 2<<20 ||
		MaxRepoLogEntries != 100 || DefaultRepoLogEntries != 30 || MaxRepoPathBytes != 1024 || MaxRepoStatusEntries != 2000 {
		t.Fatal("repo browse limits changed")
	}

	for ref, want := range map[string]bool{
		"":                      true,
		strings.Repeat("a", 40): true,
		strings.Repeat("0", 64): true,
		strings.Repeat("a", 39): false,
		strings.Repeat("A", 40): false,
		"HEAD":                  false,
		"main":                  false,
		"--output=/tmp/x":       false,
		strings.Repeat("g", 40): false,
		strings.Repeat("a", 41): false,
	} {
		if got := ValidRepoRef(ref); got != want {
			t.Errorf("ValidRepoRef(%q) = %v, want %v", ref, got, want)
		}
	}
}
