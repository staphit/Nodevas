package runnerwire

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The single-user edition's messages (offer asks, repo admin, local
// settings) round-trip with the names the hub and the web read.
func TestSingleUserContract(t *testing.T) {
	if TypeOfferAsk != "offer_ask" || TypeOfferAnswer != "offer_answer" || CapOfferAsk != "offer-ask" ||
		CapAppAccept != "app-accept" || AskInApp != "app" || AskOnRunner != "runner" ||
		AskShellAgent != "shell_agent" || AskEveryRun != "ask_every_run" || AskNotOnList != "not_on_list" ||
		AskNeedsStepUp != "needs_step_up" || AskVerifyTests != "verify_tests" || AppAskTimeoutSeconds != 1800 ||
		EditionSingle != "single" || EditionTeam != "team" {
		t.Fatal("offer ask or edition names changed")
	}
	if TypeRepoAdmin != "repo_admin" || TypeRepoAdminDone != "repo_admin_done" || CapRepoAdmin != "repo-admin" ||
		RepoAdminInspect != "inspect" || RepoAdminAdd != "add" || RepoAdminInitAdd != "init_add" ||
		RepoAdminRemove != "remove" || RepoAdminSetTest != "set_test" || RepoAdminTimeoutSeconds != 30 ||
		RepoAdminErrPlanChanged != "plan_changed" || RepoAdminErrGit != "git_failed" || RepoAdminErrGitMissing != "git_missing" ||
		MaxInitFiles != 20000 || MaxInitBytes != 1<<30 {
		t.Fatal("repo admin names or limits changed")
	}
	if OriginPerson != "person" || OriginAutopilot != "autopilot" || OriginPipeline != "pipeline" || OriginHandoff != "handoff" ||
		DeclineInApp != "declined_in_app" || DeclineOnRunner != "declined_on_runner" || DeclineTimedOut != "timed_out" ||
		DeclineNoOneThere != "no_one_there" || DeclineRepoNotAdded != "repo_not_added" || DeclineAgentMissing != "agent_missing" ||
		DeclineInteractiveOff != "interactive_off" || DeclineGlobalOff != "global_off" || DeclineBusy != "busy" ||
		DeclineOther != "other" || SettingsErrLocalOnly != "local_only" {
		t.Fatal("origin, decline or settings codes changed")
	}
	on := true
	cases := []struct {
		value any
		keys  []string
	}{
		{Envelope{Type: TypeOfferAsk, RunID: "run-1", OfferAsk: &OfferAsk{RunID: "run-1", Where: AskInApp,
			Reasons:   []AskReason{{Code: AskShellAgent, Agent: "codex", Setting: "codexSandbox=danger-full-access"}},
			ExpiresAt: "2026-11-14T14:32:00Z", Machine: "pc"}},
			[]string{`"offerAsk":{"runId":"run-1","where":"app","reasons":[{"code":"shell_agent","agent":"codex","setting":"codexSandbox=danger-full-access"}],"expiresAt":"2026-11-14T14:32:00Z","machine":"pc"}`}},
		{Envelope{Type: TypeOfferAnswer, OfferAnswer: &OfferAnswer{RunID: "run-1", AccountID: "u", Start: true, AlwaysAllowShell: true}},
			[]string{`"offerAnswer":{"runId":"run-1","accountId":"u","start":true,"alwaysAllowShell":true}`}},
		{Envelope{Type: TypeDecline, RunID: "run-1", Reason: "no", Code: DeclineInApp}, []string{`"code":"declined_in_app"`}},
		{Welcome{RunnerID: "r", Edition: EditionSingle, BuiltIn: true}, []string{`"edition":"single","builtIn":true`}},
		{Job{RunID: "run-1", Origin: OriginAutopilot}, []string{`"origin":"autopilot"`}},
		{Run{ID: "run-1", Origin: OriginPerson, Ask: &OfferAsk{RunID: "run-1", Where: AskOnRunner, Reasons: []AskReason{}}, DeclineCode: DeclineTimedOut},
			[]string{`"origin":"person"`, `"ask":{"runId":"run-1","where":"runner"`, `"declineCode":"timed_out"`}},
		{Runner{ID: "rn", BuiltIn: true}, []string{`"builtIn":true`}},
		{Info{Local: &LocalSettings{Permission: map[string]string{"codex": PermEdit}, PermissionDetail: map[string]string{"codex": "codexSandbox=workspace-write"},
			ShellWithoutAsking: []string{"claude"}, AutoPR: true, ConfigPath: "/x.json"}},
			[]string{`"local":{"permission":{"codex":"edit"},"permissionDetail":{"codex":"codexSandbox=workspace-write"},"askEveryRun":false,"shellWithoutAsking":["claude"],"autoPR":true,"prDraft":false,"configPath":"/x.json"}`}},
		{SettingsRequest{ID: "s", AccountID: "u", Permission: map[string]string{"codex": PermFull}, AskEveryRun: &on,
			ShellWithoutAsking: map[string]bool{"codex": false}, AutoPR: &on, PRDraft: &on, Redetect: true},
			[]string{`"permission":{"codex":"full"}`, `"askEveryRun":true`, `"shellWithoutAsking":{"codex":false}`, `"autoPR":true`, `"prDraft":true`, `"redetect":true`}},
		{Envelope{Type: TypeRepoAdmin, RepoAdmin: &RepoAdminRequest{ID: "a", AccountID: "u", Op: RepoAdminInitAdd, Path: "/src/app", InitPlan: "h"}},
			[]string{`"repoAdmin":{"id":"a","accountId":"u","op":"init_add","path":"/src/app","initPlan":"h"}`}},
		{Envelope{Type: TypeRepoAdminDone, RepoAdminDone: &RepoAdminDone{ID: "a", Code: RepoAdminErrPlanChanged, Error: "x",
			Inspect: &RepoInspect{Path: "/src/app", State: RepoStateNotRepo, Init: &InitPlan{Files: 3, Bytes: 9, Ignored: []string{"node_modules/"},
				WritesGitignore: true, Hash: "h"}}}},
			[]string{`"repoAdminDone":{"id":"a","code":"plan_changed","error":"x","inspect":{"path":"/src/app","state":"not_repo","init":{"files":3,"bytes":9,"ignored":["node_modules/"],"writesGitignore":true,"tooBig":false,"hash":"h"}}}`}},
		{RepoAdminDone{ID: "b", Inspect: &RepoInspect{Path: "/home/me", State: RepoStateRefused, Refused: RepoRefusedHome}},
			[]string{`"state":"refused","refused":"home"`}},
		{RunOfferBody{Start: true, AlwaysAllowShell: true}, []string{`{"start":true,"alwaysAllowShell":true}`}},
		{RepoAddResponse{Repo: Repo{ID: "r"}, Runner: Runner{ID: "rn"}}, []string{`"repo":{"id":"r"`, `"runner":{"id":"rn"`}},
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
	// What an older hub or runner sends says nothing new.
	for _, value := range []any{Welcome{}, Job{}, Info{}, Envelope{Type: TypeDecline}, Runner{}} {
		raw, _ := json.Marshal(value)
		for _, name := range []string{"edition", "builtIn", "origin", "local", `"code"`, "offerAsk"} {
			if strings.Contains(string(raw), name) {
				t.Errorf("%T without the new fields names %s: %s", value, name, raw)
			}
		}
	}
}
