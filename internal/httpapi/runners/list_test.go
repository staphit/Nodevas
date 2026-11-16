package runners_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	runnersapi "nodevas/internal/httpapi/runners"
	"nodevas/internal/identity"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
	"nodevas/internal/testpg"
)

// listed is GET /api/runners as a client reads it, each runner by id. The
// raw fields are kept so a test can tell an absent field from a false one.
func listed(t *testing.T, reg *runners.Registry, single bool) map[string]map[string]json.RawMessage {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	runnersapi.New(reg, nil).WithEdition(single).Register(engine.Group("/api"))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/runners", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/runners = %d %s", response.Code, response.Body)
	}
	var body struct {
		Runners []map[string]json.RawMessage `json:"runners"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// An empty list is still a list: the app reads its length.
	if body.Runners == nil {
		t.Fatalf("runners is not a list: %s", response.Body)
	}
	out := map[string]map[string]json.RawMessage{}
	for _, runner := range body.Runners {
		var id string
		if err := json.Unmarshal(runner["id"], &id); err != nil {
			t.Fatal(err)
		}
		out[id] = runner
	}
	return out
}

// The list names the hub's own runner from the moment the hub registers it,
// before the runner has connected and been welcomed as built in; any other
// runner of the same person carries no such field.
func TestListMarksTheHubsOwnRunnerBeforeItConnects(t *testing.T) {
	ctx := t.Context()
	reg := runners.NewRegistry(testpg.New(t), nil)
	if list := listed(t, reg, true); len(list) != 0 {
		t.Fatalf("list with no runners = %v", list)
	}

	own, _, err := reg.Create(ctx, identity.Local, runnerwire.LocalRunnerName)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := reg.Create(ctx, identity.Local, "other")
	if err != nil {
		t.Fatal(err)
	}

	// Nothing named yet (serve --no-runner): no runner is the hub's own.
	if _, has := listed(t, reg, true)[own.ID]["hubLocal"]; has {
		t.Fatal("hubLocal before the hub named its runner")
	}

	reg.SetBuiltIn(own.ID)
	list := listed(t, reg, true)
	if string(list[own.ID]["hubLocal"]) != "true" {
		t.Fatalf("the hub's own runner = %s", list[own.ID])
	}
	// Never connected: not online, and not built in until its welcome.
	if _, has := list[own.ID]["builtIn"]; has || string(list[own.ID]["online"]) != "false" {
		t.Fatalf("the hub's own runner before it connects = %s", list[own.ID])
	}
	if _, has := list[other.ID]["hubLocal"]; has {
		t.Fatalf("another runner = %s", list[other.ID])
	}

	// Welcomed as built in: both say so, each on its own word.
	reg.SetWelcomed(own.ID, true)
	list = listed(t, reg, true)
	if string(list[own.ID]["hubLocal"]) != "true" || string(list[own.ID]["builtIn"]) != "true" {
		t.Fatalf("the hub's own runner once welcomed = %s", list[own.ID])
	}

	// A hub with accounts has no runner of its own, whatever the registry
	// was told.
	if _, has := listed(t, reg, false)[own.ID]["hubLocal"]; has {
		t.Fatal("hubLocal on a hub with accounts")
	}
}
