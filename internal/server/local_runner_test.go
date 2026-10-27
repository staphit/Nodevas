package server

import (
	"context"
	"testing"

	"nodevas/internal/identity"
)

func TestLocalRunnerTokenKeepsAGoodTokenAndReplacesOthers(t *testing.T) {
	pm := projectManagerForTest(t)
	database := dbtestOpenForServer(t, pm.Workspace())
	local := serverForTest(t, pm, nil, nil)
	local.UseRunnersDB(database)
	ctx := context.Background()

	token, created, err := local.LocalRunnerToken(ctx, "", "this machine")
	if err != nil || !created {
		t.Fatalf("first token: created=%v err=%v", created, err)
	}
	runner, err := local.runners.Authenticate(ctx, token)
	if err != nil || runner.OwnerID != identity.Local.ID || runner.Name != "this machine" {
		t.Fatalf("minted runner = %+v, %v", runner, err)
	}

	// A restart keeps the runner rather than adding one per boot.
	again, created, err := local.LocalRunnerToken(ctx, token, "this machine")
	if err != nil || created || again != token {
		t.Fatalf("restart: created=%v same=%v err=%v", created, again == token, err)
	}

	// Revoked on the Runners page, or never this hub's: a new runner.
	if err := local.runners.Revoke(ctx, identity.Local, runner.ID); err != nil {
		t.Fatal(err)
	}
	for _, stale := range []string{token, "nvr_not-a-token-this-hub-knows", "garbage"} {
		fresh, created, err := local.LocalRunnerToken(ctx, stale, "this machine")
		if err != nil || !created || fresh == stale {
			t.Fatalf("stale %q: created=%v err=%v", stale, created, err)
		}
	}
}

func TestLocalRunnerTokenRefusesAServerWithAccounts(t *testing.T) {
	server, _, _, database, _ := accountServerPartsForTest(t)
	server.UseRunnersDB(database)
	if _, _, err := server.LocalRunnerToken(context.Background(), "", "this machine"); err == nil {
		t.Fatal("a server with accounts minted a runner for the local actor")
	}
}

func TestLocalRunnerTokenNeedsRunners(t *testing.T) {
	local := serverForTest(t, projectManagerForTest(t), nil, nil)
	if _, _, err := local.LocalRunnerToken(context.Background(), "", "this machine"); err == nil {
		t.Fatal("minted without a runners registry")
	}
}
