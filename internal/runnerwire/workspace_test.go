package runnerwire

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The agent workspace's messages and API shapes round-trip with the field
// names the web codes against (phase 1 contract).
func TestWorkspaceContract(t *testing.T) {
	for op, want := range map[string]bool{RepoOpRunList: true, RepoOpRunRead: true, RepoOpRunStatus: true, RepoOpRunInfo: true,
		RepoOpList: false, RepoOpRead: false, RepoOpStatus: false, RepoOpLog: false, RepoOpDiff: false, "": false} {
		if RunRepoOp(op) != want {
			t.Errorf("RunRepoOp(%q) = %v", op, !want)
		}
	}
	if RepoOpRunList != "run_list" || RepoOpRunRead != "run_read" || RepoOpRunStatus != "run_status" || RepoOpRunInfo != "run_info" ||
		RepoErrNoWorktree != "no_worktree" || CapRunWorkspace != "run-workspace" || CapWorkspaceActions != "workspace-actions" ||
		TypeWorkspaceAction != "workspace_action" || TypeWorkspaceResult != "workspace_result" || WorkspaceActionTTLSeconds != 900 {
		t.Fatal("workspace names or limits changed")
	}

	cases := []struct {
		value any
		keys  []string
	}{
		{Envelope{Type: TypeWorkspaceAction, RunID: "r", WorkspaceAction: &WorkspaceActionJob{ID: "a", RunID: "r", AccountID: "u",
			Action: WorkspaceMergeLocal, RepoID: "repo", NodeID: "n", Kind: JobTest, Branch: "agent/n-r", Base: "main", HeadSHA: "abc"}},
			[]string{`"type":"workspace_action"`, `"workspaceAction":{"id":"a","runId":"r","accountId":"u","action":"merge_local","repoId":"repo","nodeId":"n","kind":"test","branch":"agent/n-r","base":"main","headSha":"abc"}`}},
		{Envelope{Type: TypeWorkspaceResult, RunID: "r", WorkspaceResult: &WorkspaceOutcome{ID: "a", RunID: "r", Action: WorkspaceDiscard,
			Code: WorkspaceErrConflict, Error: "x", MergeCommit: "m", WorktreeRemoved: true, BranchDeleted: true, PRClosed: true, Note: "n"}},
			[]string{`"workspaceResult":{"id":"a","runId":"r","action":"discard","code":"conflict","error":"x","mergeCommit":"m","worktreeRemoved":true,"branchDeleted":true,"prClosed":true,"note":"n"}`}},
		{Envelope{Type: TypeRepoRequest, Repo: &RepoRequest{ID: "q", AccountID: "u", RepoID: "repo", Op: RepoOpRunInfo, RunID: "r", NodeID: "n", Branch: "b", Base: "main"}},
			[]string{`"op":"run_info","runId":"r","nodeId":"n","branch":"b","base":"main"`}},
		{Envelope{Type: TypeRepoDone, RepoDone: &RepoDone{ID: "q", RunInfo: &RunWorkspaceInfo{Exists: true, Path: "/p", Branch: "b", Head: "h",
			OwnBranch: true, BranchExists: true, BranchHead: "h", Uncommitted: 2, Running: true, Kept: true, BaseCheckout: BaseCheckoutNone,
			BaseClean: true, WatchedPR: "u"}}},
			[]string{`"runInfo":{"exists":true,"path":"/p","branch":"b","head":"h","ownBranch":true,"branchExists":true,"branchHead":"h","uncommitted":2,"running":true,"kept":true,"baseCheckout":"none","baseClean":true,"watchedPr":"u"}`}},
		{Run{Workspace: RunWorkspaceMergedLocal, WorkspaceCommit: "c", WorkspaceAction: &RunWorkspaceAction{ID: "a", RunID: "r",
			Action: WorkspaceMergeLocal, Status: WorkspaceFailed, Code: WorkspaceErrDirtyBase, Reason: "x", Note: "n", HeadSHA: "h",
			Base: "main", MergeCommit: "m", RequestedBy: "ann", CreatedAt: "t1", FinishedAt: "t2"}},
			[]string{`"workspace":"merged_local","workspaceCommit":"c","workspaceAction":{"id":"a","runId":"r","action":"merge_local","status":"failed","code":"dirty_base","reason":"x","note":"n","headSha":"h","base":"main","mergeCommit":"m","requestedBy":"ann","createdAt":"t1","finishedAt":"t2"}`}},
		{RunWorkspaceView{RunID: "r", Owner: true, RunnerOnline: true, Files: RunWorkspaceFiles{Allowed: true},
			Merge: RunWorkspaceMergeGate{Mode: WorkspaceMergeModeLocal}, Discard: RunWorkspaceDiscardGate{Allowed: true}},
			[]string{`{"runId":"r","owner":true,"runnerOnline":true,"files":{"allowed":true,"blocked":""},"merge":{"mode":"local","blocked":""},"discard":{"allowed":true,"blocked":""},"info":null,"infoError":""}`}},
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
	// A run untouched by the workspace says nothing about it.
	raw, _ := json.Marshal(Run{ID: "r"})
	if strings.Contains(string(raw), "workspace") {
		t.Fatalf("a plain run mentions the workspace: %s", raw)
	}
}
