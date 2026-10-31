package agents

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// startAsk runs CreateAndWait for in in the background and returns the ask
// once it is pending, with a channel for its outcome.
func startAsk(t *testing.T, hub *Hub, in AskInput, timeout time.Duration) (Ask, chan Ask) {
	t.Helper()
	ctx := context.Background()
	done := make(chan Ask, 1)
	go func() {
		decided, err := hub.CreateAndWait(ctx, in, timeout)
		if err != nil {
			t.Errorf("CreateAndWait(%+v): %v", in, err)
		}
		done <- decided
	}()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		pending, _ := hub.ListAsks(ctx, in.Project, AskPending)
		for _, a := range pending {
			if a.SessionID == in.SessionID && a.Question == strings.TrimSpace(in.Question) {
				return a, done
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the ask %q never became pending", in.Question)
	return Ask{}, nil
}

func waitDecided(t *testing.T, done chan Ask) Ask {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("CreateAndWait did not return")
		return Ask{}
	}
}

func TestAskKindIsValidatedOnCreate(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		base := AskInput{Project: "alpha", SessionID: "s1", SessionName: "api-claude", ActorID: "alice", Question: "Which port?"}
		long := strings.Repeat("x", 201)
		nine := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
		for name, mutate := range map[string]func(*AskInput){
			"unknown kind":          func(in *AskInput) { in.Kind = "poll" },
			"choice with one":       func(in *AskInput) { in.Kind = AskKindChoice; in.Options = []string{"only"} },
			"choice with nine":      func(in *AskInput) { in.Kind = AskKindChoice; in.Options = nine },
			"choice option too big": func(in *AskInput) { in.Kind = AskKindChoice; in.Options = []string{"a", long} },
			"choice blank option":   func(in *AskInput) { in.Kind = AskKindChoice; in.Options = []string{"a", "  "} },
			"choice repeats":        func(in *AskInput) { in.Kind = AskKindChoice; in.Options = []string{"a", "a"} },
			"options on a question": func(in *AskInput) { in.Kind = AskKindQuestion; in.Options = []string{"a", "b"} },
			"tool on a question":    func(in *AskInput) { in.Kind = AskKindQuestion; in.Tool = "Bash" },
		} {
			in := base
			mutate(&in)
			if _, err := hub.CreateAndWait(ctx, in, time.Second); !errors.Is(err, ErrInvalid) {
				t.Errorf("%s: err = %v, want ErrInvalid", name, err)
			}
		}
		if pending, _ := hub.ListAsks(ctx, "alpha", ""); len(pending) != 0 {
			t.Fatalf("refused asks were stored: %+v", pending)
		}
	})
}

// An ask from before kinds existed, or one that names none, is an approval.
func TestAskDefaultsToApproval(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ask, done := startAsk(t, hub, AskInput{
			Project: "alpha", SessionID: "s1", SessionName: "api-claude", ActorID: "alice", Question: "Ship it?",
		}, 5*time.Second)
		if ask.Kind != AskKindApproval || len(ask.Options) != 0 {
			t.Fatalf("listed ask = %+v, want kind approval", ask)
		}
		if ask.ExpiresAt-ask.CreatedAt != (5 * time.Second).Milliseconds() {
			t.Fatalf("expiresAt %d - createdAt %d, want 5s", ask.ExpiresAt, ask.CreatedAt)
		}
		if _, err := hub.Answer(context.Background(), "alpha", ask.ID, "approve", "", Viewer{ID: "alice"}); err != nil {
			t.Fatal(err)
		}
		if got := waitDecided(t, done); got.Status != AskApproved || got.Kind != AskKindApproval {
			t.Fatalf("decided = %+v", got)
		}
	})
}

func TestQuestionIsAnsweredWithText(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		ask, done := startAsk(t, hub, AskInput{
			Project: "alpha", SessionID: "s1", SessionName: "api-claude", ActorID: "alice",
			Kind: AskKindQuestion, Question: "Which port should the dev server use?",
		}, 5*time.Second)
		if ask.Kind != AskKindQuestion {
			t.Fatalf("kind = %q", ask.Kind)
		}
		alice := Viewer{ID: "alice"}
		for name, in := range map[string]AnswerInput{
			"approve":          {Decision: "approve"},
			"approve_run":      {Decision: "approve_run"},
			"blank answer":     {Decision: "answer", Answer: "   "},
			"answer too long":  {Decision: "answer", Answer: strings.Repeat("y", MaxAskAnswerRunes+1)},
			"unknown decision": {Decision: "maybe", Answer: "8080"},
		} {
			if _, err := hub.AnswerWith(ctx, "alpha", ask.ID, in, alice); !errors.Is(err, ErrInvalid) {
				t.Errorf("%s: err = %v, want ErrInvalid", name, err)
			}
		}
		answered, err := hub.AnswerWith(ctx, "alpha", ask.ID, AnswerInput{Decision: "answer", Answer: " 8080 ", Note: "any free port"}, alice)
		if err != nil {
			t.Fatal(err)
		}
		if answered.Status != AskAnswered || answered.Answer != "8080" || answered.Note != "any free port" || answered.DecidedBy != "alice" {
			t.Fatalf("answered = %+v", answered)
		}
		if got := waitDecided(t, done); got.Status != AskAnswered || got.Answer != "8080" {
			t.Fatalf("the agent got %+v", got)
		}
		listed, _ := hub.ListAsks(ctx, "alpha", AskAnswered)
		if len(listed) != 1 || listed[0].Answer != "8080" || listed[0].Kind != AskKindQuestion {
			t.Fatalf("listed answered = %+v", listed)
		}
	})
}

// Deny on a question is "won't answer".
func TestQuestionCanBeDismissed(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ask, done := startAsk(t, hub, AskInput{
			Project: "alpha", SessionID: "s1", SessionName: "api-claude", ActorID: "alice",
			Kind: AskKindQuestion, Question: "What is the staging password?",
		}, 5*time.Second)
		if _, err := hub.AnswerWith(context.Background(), "alpha", ask.ID, AnswerInput{Decision: "deny", Note: "not telling"}, Viewer{ID: "alice"}); err != nil {
			t.Fatal(err)
		}
		if got := waitDecided(t, done); got.Status != AskDenied || got.Answer != "" || got.Note != "not telling" {
			t.Fatalf("dismissed = %+v", got)
		}
	})
}

func TestChoiceAnswerMustBeOneOfItsOptions(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		ask, done := startAsk(t, hub, AskInput{
			Project: "alpha", SessionID: "s1", SessionName: "api-claude", ActorID: "alice",
			Kind: AskKindChoice, Question: "Which database?", Options: []string{" Postgres ", "MySQL", "SQL Server"},
		}, 5*time.Second)
		if ask.Kind != AskKindChoice || len(ask.Options) != 3 || ask.Options[0] != "Postgres" {
			t.Fatalf("listed choice = %+v", ask)
		}
		alice := Viewer{ID: "alice"}
		for _, bad := range []string{"", "SQLite", "postgres"} {
			if _, err := hub.AnswerWith(ctx, "alpha", ask.ID, AnswerInput{Decision: "answer", Answer: bad}, alice); !errors.Is(err, ErrInvalid) {
				t.Errorf("answer %q: err = %v, want ErrInvalid", bad, err)
			}
		}
		if _, err := hub.AnswerWith(ctx, "alpha", ask.ID, AnswerInput{Decision: "answered", Answer: "Postgres"}, alice); err != nil {
			t.Fatal(err)
		}
		if got := waitDecided(t, done); got.Status != AskAnswered || got.Answer != "Postgres" || len(got.Options) != 3 {
			t.Fatalf("chosen = %+v", got)
		}
	})
}

// The run an ask belongs to comes from the transport, or else from the
// asking session's registration; never from what the agent says.
func TestAskRecordsItsRun(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		if _, err := hub.Touch(Heartbeat{SessionID: "r1", Project: "alpha", ActorID: "alice", Name: "app-claude-r1", RunID: "run-1"}); err != nil {
			t.Fatal(err)
		}
		fromSession, done1 := startAsk(t, hub, AskInput{
			Project: "alpha", SessionID: "r1", SessionName: "app-claude-r1", ActorID: "alice", Question: "From the session?",
		}, 5*time.Second)
		if fromSession.RunID != "run-1" {
			t.Fatalf("runId from the session = %q", fromSession.RunID)
		}
		ctx := context.Background()
		if _, err := hub.Answer(ctx, "alpha", fromSession.ID, "deny", "", Viewer{ID: "alice"}); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, done1)
		fromTransport, done2 := startAsk(t, hub, AskInput{
			Project: "alpha", SessionID: "s9", SessionName: "other", ActorID: "alice", Question: "From the proxy?", RunID: "run-2",
		}, 5*time.Second)
		if fromTransport.RunID != "run-2" {
			t.Fatalf("runId from the transport = %q", fromTransport.RunID)
		}
		if _, err := hub.Answer(ctx, "alpha", fromTransport.ID, "deny", "", Viewer{ID: "alice"}); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, done2)
	})
}

// A late answer to a question still reaches the agent, text included.
func TestLateAnswerToAQuestionCarriesTheText(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		expired, err := hub.CreateAndWait(ctx, AskInput{
			Project: "alpha", SessionID: "s1", SessionName: "api-claude", ActorID: "alice",
			Kind: AskKindQuestion, Question: "Which region?",
		}, 30*time.Millisecond)
		if err != nil || expired.Status != AskExpired {
			t.Fatalf("expired = %+v, %v", expired, err)
		}
		late, err := hub.AnswerWith(ctx, "alpha", expired.ID, AnswerInput{Decision: "answer", Answer: "eu-west-1"}, Viewer{ID: "alice"})
		if err != nil || late.Status != AskAnswered || late.Answer != "eu-west-1" {
			t.Fatalf("late = %+v, %v", late, err)
		}
		inbox, _ := hub.CheckInbox(ctx, "alpha", "api-claude", "alice", 0)
		if len(inbox) != 1 || !strings.Contains(inbox[0].Body, "eu-west-1") {
			t.Fatalf("late answer mail = %+v", inbox)
		}
	})
}
