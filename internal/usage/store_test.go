package usage

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/testpg"
)

// testNow is the clock every store test runs at.
var testNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func newTestStore(t *testing.T) (*Store, *db.DB) {
	t.Helper()
	database := testpg.New(t)
	s := NewStore(database)
	s.SetClock(func() time.Time { return testNow })
	return s, database
}

// seedRun inserts a run that reported usage.
func seedRun(t *testing.T, database *db.DB, id, project, by, owner, agent, model, usage string, created time.Time) {
	t.Helper()
	if _, err := database.Exec(`
INSERT INTO runs (id, project, node_id, runner_id, owner_id, agent, model, repo_id, status, usage,
    node_title, created_by, created_by_name, created_at)
VALUES (?, ?, 'n1', 'r1', ?, ?, ?, 'repo', 'succeeded', ?, 'Fix it', ?, ?, ?)`,
		id, project, owner, agent, model, usage, by, by+"-name", created.UTC().Format(stampLayout)); err != nil {
		t.Fatalf("seed run: %v", err)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestRecordSessionReplacesTotals(t *testing.T) {
	s, database := newTestStore(t)
	ctx := context.Background()
	report := SessionReport{Project: "p", SessionID: "claude:s1", SessionName: "bea-claude", ActorID: "bea",
		ActorName: "Bea", Agent: "claude", Models: []ModelUsage{{Model: "claude-opus-5-5", InputTokens: 100, OutputTokens: 10}}}
	if err := s.RecordSession(ctx, report); err != nil {
		t.Fatal(err)
	}
	report.Models = []ModelUsage{
		{Model: "Claude-Opus-5-5", InputTokens: 250, OutputTokens: 30, CacheReadTokens: 7},
		{Model: "", OutputTokens: 1},
	}
	if err := s.RecordSession(ctx, report); err != nil {
		t.Fatal(err)
	}
	var in, out, read int64
	if err := database.QueryRow(`SELECT input_tokens, output_tokens, cache_read_tokens FROM agent_session_usage
WHERE project = 'p' AND session_id = 'claude:s1' AND model = 'claude-opus-5-5'`).Scan(&in, &out, &read); err != nil {
		t.Fatal(err)
	}
	if in != 250 || out != 30 || read != 7 {
		t.Fatalf("totals = %d/%d/%d, want the second report's", in, out, read)
	}
	var rows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM agent_session_usage WHERE model = 'unknown'`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("unknown model rows = %d %v", rows, err)
	}

	for name, bad := range map[string]SessionReport{
		"no models":  {Project: "p", SessionID: "s"},
		"negative":   {Project: "p", SessionID: "s", Models: []ModelUsage{{Model: "m", InputTokens: -1}}},
		"huge":       {Project: "p", SessionID: "s", Models: []ModelUsage{{Model: "m", OutputTokens: MaxTokens + 1}}},
		"nan cost":   {Project: "p", SessionID: "s", Models: []ModelUsage{{Model: "m", CostUSD: math.NaN()}}},
		"twice":      {Project: "p", SessionID: "s", Models: []ModelUsage{{Model: "m"}, {Model: "M"}}},
		"long name":  {Project: "p", SessionID: "s", Models: []ModelUsage{{Model: string(make([]byte, MaxModelLen+1))}}},
		"no session": {Project: "p", Models: []ModelUsage{{Model: "m"}}},
	} {
		if err := s.RecordSession(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	many := SessionReport{Project: "p", SessionID: "s"}
	for i := 0; i <= MaxModelsPerReport; i++ {
		many.Models = append(many.Models, ModelUsage{Model: string(rune('a'+i%26)) + string(rune('a'+i/26))})
	}
	if err := s.RecordSession(ctx, many); !errors.Is(err, ErrInvalid) {
		t.Errorf("too many models: %v", err)
	}
}

func TestCustomPricesRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	if err := s.SetCustomPrices(ctx, []Price{{Model: "GPT-5.5", InputPerMTok: 1.25, OutputPerMTok: 10}}, "bea"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCustomPrices(ctx, []Price{{Model: "gemini-*", InputPerMTok: 0.3}}, "bea"); err != nil {
		t.Fatal(err)
	}
	got, err := s.CustomPrices(ctx)
	if err != nil || len(got) != 1 || got[0].Model != "gemini-*" || got[0].Source != SourceCustom {
		t.Fatalf("custom = %+v %v; want only the second set", got, err)
	}
	if err := s.SetCustomPrices(ctx, []Price{{Model: "m", InputPerMTok: -1}}, "bea"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad price: %v", err)
	}
	if got, _ := s.CustomPrices(ctx); len(got) != 1 {
		t.Fatalf("a refused set changed the table: %+v", got)
	}
}

func TestReport(t *testing.T) {
	s, database := newTestStore(t)
	ctx := context.Background()
	yesterday := testNow.AddDate(0, 0, -1)
	// bea's claude run: Claude Code said what it cost.
	seedRun(t, database, "run-claude", "p", "bea", "bea", "claude", "default",
		`{"inputTokens":1000,"outputTokens":200,"cacheReadTokens":5000,"costUsd":0.42,"model":"claude-opus-5-5"}`, yesterday)
	// bea's codex run: gpt-5.5 at its estimate price.
	seedRun(t, database, "run-codex", "p", "bea", "bea", "codex", "gpt-5.5",
		`{"inputTokens":3000,"outputTokens":300}`, testNow.Add(-time.Hour))
	// An agy run dispatched as "default" that never named its model: costed
	// at Antigravity's default model.
	seedRun(t, database, "run-agy", "p", "bea", "bea", "antigravity", "default",
		`{"inputTokens":10,"outputTokens":1}`, testNow.Add(-2*time.Hour))
	// cal's run, out of the window, in another project, and one with no usage.
	seedRun(t, database, "run-cal", "p", "cal", "cal", "claude", "sonnet", `{"inputTokens":1000000}`, testNow.Add(-3*time.Hour))
	seedRun(t, database, "run-old", "p", "bea", "bea", "claude", "opus", `{"inputTokens":1}`, testNow.AddDate(0, 0, -40))
	seedRun(t, database, "run-other", "q", "bea", "bea", "claude", "opus", `{"inputTokens":1}`, testNow)
	seedRun(t, database, "run-none", "p", "bea", "bea", "claude", "opus", ``, testNow)

	// An external Claude Code session on two models, priced from the table.
	if err := s.RecordSession(ctx, SessionReport{Project: "p", SessionID: "claude:s1", SessionName: "bea-claude",
		ActorID: "bea", ActorName: "Bea", Agent: "claude", Models: []ModelUsage{
			{Model: "claude-opus-5-5", InputTokens: 1_000_000, OutputTokens: 100_000},
			{Model: "claude-haiku-5-5", InputTokens: 2_000_000},
		}}); err != nil {
		t.Fatal(err)
	}
	// An OpenCode session that said what it cost.
	if err := s.RecordSession(ctx, SessionReport{Project: "p", SessionID: "opencode:s2", SessionName: "bea-opencode",
		ActorID: "bea", Agent: "opencode", Models: []ModelUsage{
			{Model: "anthropic/claude-sonnet-5-5", InputTokens: 100, OutputTokens: 10, CostUSD: 0.05},
		}}); err != nil {
		t.Fatal(err)
	}

	rep, err := s.Report(ctx, Query{Project: "p", Viewer: "bea", Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Days != 7 || rep.Since != "2026-10-18T00:00:00Z" || rep.Currency != "USD" || rep.PricesAsOf != PricesAsOf {
		t.Fatalf("header = %+v", rep)
	}
	tot := rep.Totals
	// Runs: claude 1000+200+5000, codex 3300, agy 11; sessions 3.1M + 110.
	if tot.Runs != 3 || tot.Sessions != 2 || tot.TotalTokens != 6200+3300+11+3_100_000+110 {
		t.Fatalf("totals = %+v", tot)
	}
	// Estimated: opus 1M×4 + 0.1M×20 = 6; haiku 2M×0.10 = 0.2; codex
	// gpt-5.5 3000×1.25 + 300×10 = 0.00675; agy gemini-2.5-pro 10×1.25 + 1×10
	// = 0.0000225. Reported: 0.42 + 0.05.
	if !near(tot.EstimatedCostUSD, 6.2067725) || !near(tot.ReportedCostUSD, 0.47) || !near(tot.CostUSD, 6.6767725) {
		t.Fatalf("costs = %+v", tot)
	}
	if tot.UnpricedTokens != 0 {
		t.Fatalf("unpriced = %d", tot.UnpricedTokens)
	}

	if len(rep.ByAgent) != 4 || rep.ByAgent[0].Key != "claude" || rep.ByAgent[0].Runs != 1 || rep.ByAgent[0].Sessions != 1 {
		t.Fatalf("byAgent = %+v", rep.ByAgent)
	}
	models := map[string]KeyBucket{}
	for _, m := range rep.ByModel {
		models[m.Agent+"/"+m.Key] = m
	}
	if m := models["claude/claude-opus-5-5"]; m.Runs != 1 || m.Sessions != 1 || !near(m.CostUSD, 6.42) {
		t.Fatalf("opus = %+v", m)
	}
	if m := models["antigravity/unknown"]; m.UnpricedTokens != 0 || !near(m.EstimatedCostUSD, 0.0000225) {
		t.Fatalf("agy = %+v", m)
	}
	if m := models["codex/gpt-5.5"]; m.UnpricedTokens != 0 || !near(m.CostUSD, 0.00675) {
		t.Fatalf("codex = %+v", m)
	}

	if len(rep.ByDay) != 7 || rep.ByDay[0].Key != "2026-10-18" || rep.ByDay[6].Key != "2026-10-24" {
		t.Fatalf("byDay keys = %+v", rep.ByDay)
	}
	if d := rep.ByDay[5]; d.Runs != 1 || !near(d.ReportedCostUSD, 0.42) {
		t.Fatalf("yesterday = %+v", d)
	}
	if d := rep.ByDay[6]; d.Runs != 2 || d.Sessions != 2 {
		t.Fatalf("today = %+v", d)
	}

	if len(rep.Sessions) != 5 {
		t.Fatalf("sessions = %+v", rep.Sessions)
	}
	byID := map[string]SessionEntry{}
	for _, e := range rep.Sessions {
		byID[e.ID] = e
	}
	if e := byID["claude:s1"]; e.Source != SourceSession || e.Name != "bea-claude" || e.ActorName != "Bea" ||
		e.Model != "claude-haiku-5-5" || len(e.Models) != 2 || e.Sessions != 1 || !near(e.CostUSD, 6.2) {
		t.Fatalf("claude session = %+v", e)
	}
	if e := byID["run-claude"]; e.Source != SourceRun || e.RunID != "run-claude" || e.NodeID != "n1" ||
		e.Name != "Fix it" || e.Model != "claude-opus-5-5" || e.Runs != 1 || !near(e.CostUSD, 0.42) {
		t.Fatalf("claude run = %+v", e)
	}
	// Newest first: the sessions reported at testNow, the run of yesterday last.
	if last := rep.Sessions[len(rep.Sessions)-1]; last.ID != "run-claude" {
		t.Fatalf("order = %+v", rep.Sessions)
	}

	// An administrator sees cal's run too.
	all, err := s.Report(ctx, Query{Project: "p", Days: 7})
	if err != nil || all.Totals.Runs != 4 {
		t.Fatalf("admin report runs = %d %v", all.Totals.Runs, err)
	}
	// A custom price prices codex.
	if err := s.SetCustomPrices(ctx, []Price{{Model: "gpt-5.5", InputPerMTok: 1, OutputPerMTok: 10}}, "root"); err != nil {
		t.Fatal(err)
	}
	priced, _ := s.Report(ctx, Query{Project: "p", Viewer: "bea", Days: 7})
	if !near(priced.Totals.EstimatedCostUSD, 6.2+0.003+0.003+0.0000225) || priced.Totals.UnpricedTokens != 0 {
		t.Fatalf("with a gpt price = %+v", priced.Totals)
	}
	// The window clamps.
	if wide, _ := s.Report(ctx, Query{Project: "p", Days: 1000}); wide.Days != MaxDays || len(wide.ByDay) != MaxDays {
		t.Fatalf("clamped = %d days, %d buckets", wide.Days, len(wide.ByDay))
	}
	if def, _ := s.Report(ctx, Query{Project: "p"}); def.Days != DefaultDays || def.Totals.Runs != 4 {
		t.Fatalf("default window = %d days, %d runs", def.Days, def.Totals.Runs)
	}
}
