package runnerwire

import (
	"encoding/json"
	"strings"
	"testing"
)

// Easy start: the wire fields F0-F3 add (agent path, ask-each-time switch,
// result preview, the simple flag) and how they encode.

func TestSettingsPermissionRelayDecodesNilOrValue(t *testing.T) {
	var off RunnerSettingsBody
	if err := json.Unmarshal([]byte(`{"permissionRelay":false}`), &off); err != nil {
		t.Fatal(err)
	}
	if off.PermissionRelay == nil || *off.PermissionRelay {
		t.Fatalf("permissionRelay false = %v, want a non-nil false", off.PermissionRelay)
	}
	var omitted RunnerSettingsBody
	if err := json.Unmarshal([]byte(`{"maxConcurrent":2}`), &omitted); err != nil {
		t.Fatal(err)
	}
	if omitted.PermissionRelay != nil || omitted.AgentPath != nil {
		t.Fatalf("omitted = relay %v path %v, want nil", omitted.PermissionRelay, omitted.AgentPath)
	}
	var req SettingsRequest
	if err := json.Unmarshal([]byte(`{"id":"x","permissionRelay":true}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.PermissionRelay == nil || !*req.PermissionRelay {
		t.Fatalf("request permissionRelay = %v", req.PermissionRelay)
	}
}

func TestSettingsAgentPathRoundTrips(t *testing.T) {
	body := RunnerSettingsBody{AgentPath: map[string]string{"claude": `C:\Tools\claude.exe`}}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"agentPath":{"claude":"C:\\Tools\\claude.exe"}`) {
		t.Fatalf("body = %s", raw)
	}
	var back RunnerSettingsBody
	if err := json.Unmarshal(raw, &back); err != nil || back.AgentPath["claude"] != `C:\Tools\claude.exe` {
		t.Fatalf("back = %+v, %v", back, err)
	}

	req := SettingsRequest{ID: "1", AgentPath: map[string]string{"codex": ""}}
	raw, _ = json.Marshal(req)
	var reqBack SettingsRequest
	if err := json.Unmarshal(raw, &reqBack); err != nil {
		t.Fatal(err)
	}
	if v, ok := reqBack.AgentPath["codex"]; !ok || v != "" {
		t.Fatalf("a clearing entry did not survive: %s", raw)
	}
	raw, _ = json.Marshal(SettingsRequest{ID: "2"})
	if strings.Contains(string(raw), "agentPath") || strings.Contains(string(raw), "permissionRelay") {
		t.Fatalf("empty request = %s", raw)
	}

	local := LocalSettings{AgentPaths: map[string]string{"gemini": "/opt/bin/gemini"}}
	raw, _ = json.Marshal(local)
	var localBack LocalSettings
	if err := json.Unmarshal(raw, &localBack); err != nil || localBack.AgentPaths["gemini"] != "/opt/bin/gemini" {
		t.Fatalf("local = %s, %v", raw, err)
	}
	raw, _ = json.Marshal(LocalSettings{})
	if strings.Contains(string(raw), "agentPaths") {
		t.Fatalf("no agent paths = %s", raw)
	}
}

func TestSettingsDoneRefusedOmittedWhenEmpty(t *testing.T) {
	raw, _ := json.Marshal(SettingsDone{ID: "1"})
	if strings.Contains(string(raw), "refused") {
		t.Fatalf("done = %s", raw)
	}
	raw, _ = json.Marshal(SettingsDone{ID: "1", Code: SettingsErrInvalid, Refused: AgentPathRefusedNoAnswer})
	if !strings.Contains(string(raw), `"refused":"no_answer"`) {
		t.Fatalf("done = %s", raw)
	}
	for want, got := range map[string]string{
		"name": AgentPathRefusedName, "place": AgentPathRefusedPlace, "not_program": AgentPathRefusedNotProgram,
		"missing": AgentPathRefusedMissing, "no_answer": AgentPathRefusedNoAnswer, "invalid": AgentPathRefusedInvalid,
	} {
		if got != want {
			t.Fatalf("refused code %q = %q", want, got)
		}
	}
	if MaxAgentPathBytes != 1024 {
		t.Fatalf("MaxAgentPathBytes = %d", MaxAgentPathBytes)
	}
}

func TestRunPreviewWire(t *testing.T) {
	raw, _ := json.Marshal(RepoDone{ID: "1"})
	if strings.Contains(string(raw), "preview") {
		t.Fatalf("done without a preview = %s", raw)
	}
	raw, _ = json.Marshal(RepoDone{ID: "1", Preview: &RunPreview{Port: 4242, Token: "ab", Entry: "index.html", Web: true}})
	var back RepoDone
	if err := json.Unmarshal(raw, &back); err != nil || back.Preview == nil || back.Preview.Port != 4242 ||
		back.Preview.Entry != "index.html" || !back.Preview.Web {
		t.Fatalf("preview = %s, %v", raw, err)
	}
	if !strings.Contains(string(raw), `"expiresAt":`) || !strings.Contains(string(raw), `"web":true`) {
		t.Fatalf("preview = %s", raw)
	}
	if !RunRepoOp(RepoOpRunPreview) || RepoOpRunPreview != "run_preview" {
		t.Fatal("run_preview is not a run-scoped op")
	}
	if RepoErrPreviewOff != "preview_off" || CapRunPreview != "run-preview" {
		t.Fatalf("codes = %q %q", RepoErrPreviewOff, CapRunPreview)
	}
}

func TestSimpleFlagOmittedWhenFalse(t *testing.T) {
	raw, _ := json.Marshal(CreateRunRequest{NodeID: "n1"})
	if strings.Contains(string(raw), "simple") {
		t.Fatalf("run request = %s", raw)
	}
	raw, _ = json.Marshal(CreateGlobalRunRequest{Task: "x"})
	if strings.Contains(string(raw), "simple") {
		t.Fatalf("global request = %s", raw)
	}
	var run CreateRunRequest
	if err := json.Unmarshal([]byte(`{"nodeId":"n1","simple":true}`), &run); err != nil || !run.Simple {
		t.Fatalf("run simple = %v, %v", run.Simple, err)
	}
	var global CreateGlobalRunRequest
	if err := json.Unmarshal([]byte(`{"task":"x","simple":true}`), &global); err != nil || !global.Simple {
		t.Fatalf("global simple = %v, %v", global.Simple, err)
	}
}
