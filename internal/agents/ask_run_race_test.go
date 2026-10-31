package agents

import (
	"context"
	"sync"
	"testing"
	"time"
)

// A prompt checked against the run's allows just before the person allows
// its tool for the run, and stored just after the waiting prompts were
// released, is allowed too: it must not ask again for what was just
// allowed, nor wait out its time and be denied.
func TestAPromptRacingAllowForThisRunIsAllowed(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		alice := Viewer{ID: "alice"}
		registerRun(t, hub, "r1", "app-claude-r1", "run-1")
		first, done1 := startAsk(t, hub, permissionIn("Bash", "Run go test?"), 5*time.Second)

		var once sync.Once
		hub.beforeAskInsert = func() {
			once.Do(func() {
				if _, err := hub.AnswerWith(ctx, "alpha", first.ID, AnswerInput{Decision: DecisionApproveRun}, alice); err != nil {
					t.Error(err)
				}
			})
		}
		racing := make(chan Ask, 1)
		go func() {
			got, err := hub.CreateAndWait(ctx, permissionIn("Bash", "Run go test again?"), 2*time.Second)
			if err != nil {
				t.Error(err)
			}
			racing <- got
		}()
		if got := waitDecided(t, done1); got.Status != AskApproved {
			t.Fatalf("first = %+v", got)
		}
		if got := waitDecided(t, racing); got.Status != AskApproved || got.Note != AllowedForRunNote || got.DecidedBy != "alice" {
			t.Fatalf("the racing prompt = %+v, want allowed for this run", got)
		}
	})
}
