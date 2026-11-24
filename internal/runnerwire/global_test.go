package runnerwire

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Global agents' messages and API shapes round-trip with the field names
// the web codes against (phase 2 contract).
func TestGlobalContract(t *testing.T) {
	if CapGlobalRuns != "global-runs" || CapGlobalRunsOn != "global-runs-on" || MaxGlobalTaskBytes != 32768 ||
		MaxGlobalTitleRunes != 120 || MaxSlugBytes != 40 || DefaultGlobalSlug != "task" ||
		GlobalRunsChangedEvent != "global-runs-changed" || GlobalRunEventsEvent != "global-run-events" ||
		GlobalRunNotifyEvent != "global-run-notify" {
		t.Fatal("global names or limits changed")
	}
	cases := []struct {
		value any
		keys  []string
	}{
		{Envelope{Type: TypeOffer, Job: &Job{RunID: "r", Global: true, Slug: "fix-it", NoPR: true}},
			[]string{`"project":""`, `"nodeId":""`, `"global":true,"slug":"fix-it","noPR":true`}},
		{Run{ID: "r", Global: true, Title: "Fix it", NoPR: true},
			[]string{`"project":""`, `"global":true,"title":"Fix it","noPR":true`}},
		{CreateGlobalRunRequest{RunnerID: "rn", RepoID: "repo", Agent: "claude", Model: "m", Mode: ModeInteractive, Base: "main",
			Task: "do it", Slug: "do-it", OpenPR: true},
			[]string{`{"runnerId":"rn","repoId":"repo","agent":"claude","model":"m","mode":"interactive","base":"main","task":"do it","slug":"do-it","openPR":true}`}},
		{GlobalRunsResponse{Runs: []Run{{ID: "r"}}, More: true, Runners: []RunnerLoad{{RunnerID: "rn", Name: "box", Online: true,
			MaxConcurrent: 2, Ceiling: 8, Settable: RunnerBlockedSettingsOff, GlobalRuns: RunnerBlockedOff, Busy: 1, Queued: 3,
			GlobalActive: 2, Today: RunnerToday{Runs: 4, Tokens: 1200, CostUSD: 0.5}}}},
			[]string{`"more":true`, `"runners":[{"runnerId":"rn","name":"box","online":true,"maxConcurrent":2,"ceiling":8,"settable":"settings_off","globalRuns":"off","busy":1,"queued":3,"globalActive":2,"today":{"runs":4,"tokens":1200,"costUsd":0.5}}]`}},
		{CancelAllResponse{Cancelled: 1, Runs: []Run{{ID: "r"}}}, []string{`{"cancelled":1,"runs":[{"id":"r"`}},
		{Envelope{Type: TypeWorkspaceAction, WorkspaceAction: &WorkspaceActionJob{ID: "a", RunID: "r", Slug: "fix-it"}},
			[]string{`"nodeId":"","slug":"fix-it"`}},
		{Envelope{Type: TypeRepoRequest, Repo: &RepoRequest{ID: "q", Op: RepoOpRunInfo, RunID: "r", Slug: "fix-it"}},
			[]string{`"runId":"r","slug":"fix-it"`}},
	}
	for _, c := range cases {
		raw, err := json.Marshal(c.value)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range c.keys {
			if !strings.Contains(string(raw), key) {
				t.Errorf("JSON lacks %s:\n%s", key, raw)
			}
		}
		back := reflect.New(reflect.TypeOf(c.value))
		if err := json.Unmarshal(raw, back.Interface()); err != nil || !reflect.DeepEqual(back.Elem().Interface(), c.value) {
			t.Errorf("round trip of %T = %+v, %v", c.value, back.Elem().Interface(), err)
		}
	}
	// A node's run and job say nothing about global agents.
	for _, v := range []any{Run{ID: "r"}, Job{RunID: "r"}, WorkspaceActionJob{ID: "a"}, RepoRequest{ID: "q"}} {
		raw, _ := json.Marshal(v)
		for _, key := range []string{`"global"`, `"slug"`, `"noPR"`, `"title"`} {
			if strings.Contains(string(raw), key) {
				t.Errorf("%T mentions %s: %s", v, key, raw)
			}
		}
	}
}

func TestValidSlug(t *testing.T) {
	for s, want := range map[string]bool{
		"task": true, "fix-login-bug": true, "a1-b2-3c": true, "0": true, strings.Repeat("a", 40): true,
		"": false, strings.Repeat("a", 41): false, "-a": false, "a-": false, "a--b": false, "Fix": false,
		"a_b": false, "a/b": false, "a b": false, "a.b": false, "修正": false, "a\x00": false,
	} {
		if ValidSlug(s) != want {
			t.Errorf("ValidSlug(%q) = %v", s, !want)
		}
	}
}

func TestSlugFrom(t *testing.T) {
	for title, want := range map[string]string{
		"Fix the login bug":         "fix-the-login-bug",
		"  --Add  README.md!! ":     "add-readme-md",
		"修正登入錯誤":                    "task",
		"修正 login 錯誤 v2":            "login-v2",
		"":                          "task",
		"!!!":                       "task",
		"Ünïcode café":              "n-code-caf",
		strings.Repeat("abcd ", 20): "abcd-abcd-abcd-abcd-abcd-abcd-abcd-abcd",
		strings.Repeat("x", 50):     strings.Repeat("x", 40),
		"aaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhhhh-iiii": "aaaa-bbbb-cccc-dddd-eeee-ffff-gggg",
		"aaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhhh-iiii":  "aaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhhh",
		"aaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhh-iiii":   "aaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhh",
	} {
		got := SlugFrom(title)
		if got != want {
			t.Errorf("SlugFrom(%q) = %q, want %q", title, got, want)
		}
		if !ValidSlug(got) {
			t.Errorf("SlugFrom(%q) = %q is not a valid slug", title, got)
		}
	}
}

func TestGlobalTitle(t *testing.T) {
	cases := []struct{ task, title, body string }{
		{"Fix it", "Fix it", ""},
		{"\n\n  \t\n  Fix it  \n\n details\nmore \n\n", "Fix it", "details\nmore"},
		{"Fix it\r\nline two\r\n", "Fix it", "line two"},
		{"a\rb\nc", "a b", "c"},
		{"   \n\r\n ", "", ""},
		{strings.Repeat("字", 130) + "\nbody", strings.Repeat("字", 120), "body"},
		{strings.Repeat("a", 119) + " b", strings.Repeat("a", 119), ""},
	}
	for _, c := range cases {
		title, body := GlobalTitle(c.task)
		if title != c.title || body != c.body {
			t.Errorf("GlobalTitle(%q) = %q, %q; want %q, %q", c.task, title, body, c.title, c.body)
		}
	}
}

func TestGroupRequestBounds(t *testing.T) {
	if CapContinue != "continue" || MaxFollowUpBytes != MaxGlobalTaskBytes || MinGroupRepos != 2 || MaxGroupRepos != 30 {
		t.Fatal("continue/group constants changed")
	}
	makeIDs := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "repo-" + strings.Repeat("x", 1) + string(rune('a'+i%26)) + string(rune('0'+i/26))
		}
		return out
	}
	if ValidGroupRepoIDs(nil) || ValidGroupRepoIDs([]string{"r1"}) {
		t.Fatal("fewer than 2 repos accepted")
	}
	if !ValidGroupRepoIDs(makeIDs(2)) {
		t.Fatal("2 distinct repos rejected")
	}
	if !ValidGroupRepoIDs(makeIDs(30)) {
		t.Fatal("30 distinct repos rejected")
	}
	if ValidGroupRepoIDs(makeIDs(31)) {
		t.Fatal("31 repos accepted")
	}
	if ValidGroupRepoIDs([]string{"r1", "r1"}) {
		t.Fatal("duplicate repos accepted")
	}
	if ValidGroupRepoIDs([]string{"r1", "  "}) {
		t.Fatal("blank repo accepted")
	}

	req := CreateGlobalGroupRequest{
		RunnerID: "rn1", RepoIDs: []string{"r1", "r2"}, Agent: "claude", Model: "opus",
		Task: "bump dep", OpenPR: true, OnBusy: OnBusySkip,
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var back CreateGlobalGroupRequest
	if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, req) {
		t.Fatalf("group request round trip = %+v, %v", back, err)
	}
}
