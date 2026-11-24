package runnerwire

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestRepoKeyNormalisesRemotes(t *testing.T) {
	hex32 := regexp.MustCompile(`^[0-9a-f]{32}$`)
	equivalents := []string{
		"git@github.com:Org/Repo.git",
		"git@GITHUB.COM:org/repo",
		"https://github.com/org/repo.git",
		"https://github.com/Org/Repo",
		"https://user:secret-token@GitHub.com/Org/Repo.git/",
		"http://github.com/org/repo.git",
		"ssh://git@github.com/org/repo.git",
		"ssh://git@github.com:22/Org/Repo.git",
		"https://github.com:443/org/repo",
	}
	first := RepoKey("runner-1", Repo{ID: "r1", Remote: equivalents[0]})
	if !hex32.MatchString(first) {
		t.Fatalf("RepoKey = %q, want 32 lowercase hex", first)
	}
	for _, remote := range equivalents[1:] {
		got := RepoKey("runner-2", Repo{ID: "r9", Remote: remote})
		if got != first {
			t.Errorf("RepoKey(%q) = %q, want %q", remote, got, first)
		}
	}
	// Different repo or non-default port gives a different key.
	other := RepoKey("runner-1", Repo{ID: "r1", Remote: "https://github.com/org/other.git"})
	if other == first || !hex32.MatchString(other) {
		t.Fatalf("other repo key = %q (first %q)", other, first)
	}
	customPort := RepoKey("runner-1", Repo{ID: "r1", Remote: "https://git.corp.local:8443/org/repo.git"})
	defaultPort := RepoKey("runner-1", Repo{ID: "r1", Remote: "https://git.corp.local/org/repo.git"})
	if customPort == defaultPort {
		t.Fatalf("custom port collided with default port: %q", customPort)
	}

	// No remote uses runnerID + repo.ID (different across runners or repo IDs).
	localA := RepoKey("rn1", Repo{ID: "repo-a"})
	localA2 := RepoKey("rn1", Repo{ID: "repo-a", Remote: "   "})
	localB := RepoKey("rn1", Repo{ID: "repo-b"})
	localOtherRunner := RepoKey("rn2", Repo{ID: "repo-a"})
	if !hex32.MatchString(localA) || localA != localA2 {
		t.Fatalf("local keys = %q vs %q", localA, localA2)
	}
	if localA == localB || localA == localOtherRunner || localA == first {
		t.Fatalf("local key collided: A=%q B=%q otherRunner=%q remote=%q", localA, localB, localOtherRunner, first)
	}
}

func TestCoordinationWireShapes(t *testing.T) {
	if CapTouched != "touched" || CapCoord != "coord" || TypeTouched != "touched" ||
		JobCompare != "compare" || WorkspaceOpenPR != "open_pr" ||
		MaxTouchedFiles != 500 || MaxTouchedPerMessage != 200 ||
		MinContestEntries != 2 || MaxContestEntries != 3 || MaxHolderIDs != 8 {
		t.Fatal("coordination constants changed")
	}

	// OnBusy validation: single-run vs group.
	for choice, want := range map[string]bool{
		OnBusyAsk:      true,
		OnBusyQueue:    true,
		OnBusyTakeOver: true,
		OnBusyBoth:     true,
		OnBusyContinue: true,
		OnBusySkip:     false,
		"force":        false,
	} {
		if got := ValidOnBusy(choice, false); got != want {
			t.Errorf("ValidOnBusy(%q, group=false) = %v, want %v", choice, got, want)
		}
	}
	for choice, want := range map[string]bool{
		OnBusyAsk:      true,
		OnBusyBoth:     true,
		OnBusySkip:     true,
		OnBusyQueue:    false,
		OnBusyTakeOver: false,
		OnBusyContinue: false,
		"other":        false,
	} {
		if got := ValidOnBusy(choice, true); got != want {
			t.Errorf("ValidOnBusy(%q, group=true) = %v, want %v", choice, got, want)
		}
	}

	// holderIds <= 8 and no blanks.
	if !ValidHolderIDs(nil) || !ValidHolderIDs([]string{"a", "b", "c", "d", "e", "f", "g", "h"}) {
		t.Fatal("valid holderIds rejected")
	}
	if ValidHolderIDs([]string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}) {
		t.Fatal("9 holderIds accepted")
	}
	if ValidHolderIDs([]string{"a", ""}) {
		t.Fatal("empty holderId accepted")
	}

	// Touched envelope, End NoPR, Job coordinated/contest, BusyResponse, Overlap, Contest.
	cases := []struct {
		value any
		keys  []string
	}{
		{
			Envelope{Type: TypeTouched, RunID: "r1", Touched: &Touched{Files: []string{"a.go", "b.go"}, AddOnly: []string{"b.go"}, More: true}},
			[]string{`"type":"touched"`, `"runId":"r1"`, `"files":["a.go","b.go"]`, `"addOnly":["b.go"]`, `"more":true`},
		},
		{
			Envelope{Type: TypeEnd, RunID: "r1", NoPR: true},
			[]string{`"type":"end"`, `"runId":"r1"`, `"noPR":true`},
		},
		{
			Job{RunID: "r1", Coordinated: true, Contest: &JobContest{ID: "c1", Label: "A", Of: 2}},
			[]string{`"coordinated":true`, `"contest":{"id":"c1","label":"A","of":2}`},
		},
		{
			BusyResponse{
				Holders:  []RunHolder{{RunID: "r1", Title: "Fix", Agent: "claude", Model: "opus", Status: RunRunning, Waiting: false, Since: "2026-11-18T00:00:00Z", By: "ann", MayStop: true, SameNode: true}},
				Others:   1,
				Choices:  []string{OnBusyQueue, OnBusyBoth},
				Continue: &BusyContinue{RunID: "r1", Agent: "claude", Model: "opus", OpenPR: false},
			},
			[]string{`"holders":[`, `"others":1`, `"choices":["queue","both"]`, `"continue":{"runId":"r1","agent":"claude","model":"opus","openPR":false}`},
		},
		{
			Overlap{Hidden: true, Since: "2026-11-18T00:00:00Z"},
			[]string{`{"hidden":true,"since":"2026-11-18T00:00:00Z"}`},
		},
		{
			CreateContestRequest{
				NodeID: "n1", RepoID: "repo",
				Entries:  []ContestEntrySpec{{RunnerID: "rn1", Agent: "claude", Model: "opus"}, {RunnerID: "rn1", Agent: "codex", Model: "gpt-5"}},
				Reviewer: &ContestEntrySpec{RunnerID: "rn1", Agent: "claude", Model: "sonnet"},
				OnBusy:   OnBusyBoth,
			},
			[]string{`"entries":[`, `"reviewer":{`, `"onBusy":"both"`},
		},
		{
			PickRequest{RunID: "r1", Apply: ContestApplyPR, DiscardOthers: true},
			[]string{`"runId":"r1"`, `"apply":"pr"`, `"discardOthers":true`},
		},
	}
	for _, c := range cases {
		raw, err := json.Marshal(c.value)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range c.keys {
			if !strings.Contains(string(raw), k) {
				t.Errorf("%T JSON lacks %s:\n%s", c.value, k, raw)
			}
		}
		back := reflect.New(reflect.TypeOf(c.value))
		if err := json.Unmarshal(raw, back.Interface()); err != nil || !reflect.DeepEqual(back.Elem().Interface(), c.value) {
			t.Errorf("round trip %T = %+v, %v", c.value, back.Elem().Interface(), err)
		}
	}
}
