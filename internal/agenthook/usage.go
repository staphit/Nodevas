package agenthook

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"nodevas/internal/mcp"
)

// Session usage: what an external session has used, in tokens per model, in
// total since it started. Agents keep that in their own transcripts; the hook
// reads the transcript at Stop and session-end and sends the totals, which
// the hub stores in place of what it had. OpenCode has no transcript file:
// its plugin counts the session's messages and passes the totals in.
//
// A transcript read that does not finish (too big, too slow, unreadable)
// sends nothing: a partial total would replace a complete one with less.

// usageReadTimeout bounds reading the transcripts, inside the hook's own
// timeout; the reports after it need the rest.
const usageReadTimeout = 4 * time.Second

// maxTranscriptBytes bounds what one report reads, all files together.
const maxTranscriptBytes = 512 << 20

// maxUsageModels bounds the models one report carries.
const maxUsageModels = 32

// maxUsageModelLen bounds a model name in a report.
const maxUsageModelLen = 128

// unknownModel names usage whose transcript does not say the model.
const unknownModel = "unknown"

// errTranscriptTooBig ends a read past maxTranscriptBytes.
var errTranscriptTooBig = errors.New("transcript too big")

// SessionUsage is the session's cumulative usage per model, for agent, from
// what in carries: the totals the agent passed in, or its transcript. nil when
// there is nothing to report or the read did not finish.
func SessionUsage(ctx context.Context, agent string, in Input) []mcp.SessionModelUsage {
	if len(in.Usage) > 0 {
		return cleanUsage(in.Usage)
	}
	ctx, cancel := context.WithTimeout(ctx, usageReadTimeout)
	defer cancel()
	var (
		totals map[string]*mcp.SessionModelUsage
		err    error
	)
	switch agent {
	case "claude":
		if in.TranscriptPath == "" {
			return nil
		}
		totals, err = claudeUsage(ctx, in.TranscriptPath)
	case "codex":
		path := in.TranscriptPath
		if path == "" {
			path = codexRollout(in.SessionID)
		}
		if path == "" {
			return nil
		}
		totals, err = codexUsage(ctx, path)
	case "antigravity":
		if in.TranscriptPath == "" {
			return nil
		}
		totals, err = antigravityUsage(ctx, in.TranscriptPath, in.Model)
	default:
		// Gemini CLI: its transcript format is not checked against a real
		// one yet, so nothing is read rather than something guessed.
		return nil
	}
	if err != nil {
		return nil
	}
	out := make([]mcp.SessionModelUsage, 0, len(totals))
	for _, u := range totals {
		out = append(out, *u)
	}
	return cleanUsage(out)
}

// cleanUsage drops empty and malformed entries, merges repeats of a model,
// and orders the rest by model.
func cleanUsage(in []mcp.SessionModelUsage) []mcp.SessionModelUsage {
	byModel := map[string]*mcp.SessionModelUsage{}
	for _, u := range in {
		model := strings.TrimSpace(u.Model)
		if model == "" {
			model = unknownModel
		}
		if len(model) > maxUsageModelLen {
			model = model[:maxUsageModelLen]
		}
		u.Model = model
		u.InputTokens = max(u.InputTokens, 0)
		u.OutputTokens = max(u.OutputTokens, 0)
		u.CacheReadTokens = max(u.CacheReadTokens, 0)
		u.CacheWriteTokens = max(u.CacheWriteTokens, 0)
		if u.CostUSD < 0 || math.IsNaN(u.CostUSD) || math.IsInf(u.CostUSD, 0) {
			u.CostUSD = 0
		}
		if u.InputTokens+u.OutputTokens+u.CacheReadTokens+u.CacheWriteTokens == 0 && u.CostUSD == 0 {
			continue
		}
		if cur, ok := byModel[model]; ok {
			add(cur, u)
			continue
		}
		copied := u
		byModel[model] = &copied
	}
	out := make([]mcp.SessionModelUsage, 0, len(byModel))
	for _, u := range byModel {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	if len(out) > maxUsageModels {
		out = out[:maxUsageModels]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func add(to *mcp.SessionModelUsage, u mcp.SessionModelUsage) {
	to.InputTokens += u.InputTokens
	to.OutputTokens += u.OutputTokens
	to.CacheReadTokens += u.CacheReadTokens
	to.CacheWriteTokens += u.CacheWriteTokens
	to.CostUSD += u.CostUSD
}

func totalFor(totals map[string]*mcp.SessionModelUsage, model string) *mcp.SessionModelUsage {
	model = strings.TrimSpace(model)
	if model == "" {
		model = unknownModel
	}
	u, ok := totals[model]
	if !ok {
		u = &mcp.SessionModelUsage{Model: model}
		totals[model] = u
	}
	return u
}

// lineReader reads JSONL files a line at a time within one byte budget and
// the context's deadline.
type lineReader struct {
	ctx    context.Context
	budget int64
}

// each calls fn with every line of path holding all of markers (a cheap
// filter before any JSON is parsed).
func (r *lineReader) each(path string, markers [][]byte, fn func(line []byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil {
		if info.Size() > r.budget {
			return errTranscriptTooBig
		}
	}
	br := bufio.NewReaderSize(f, 1<<20)
	for n := 0; ; n++ {
		if n%256 == 0 {
			if err := r.ctx.Err(); err != nil {
				return err
			}
		}
		line, err := br.ReadBytes('\n')
		r.budget -= int64(len(line))
		if r.budget < 0 {
			return errTranscriptTooBig
		}
		if len(line) > 0 && hasAll(line, markers) {
			fn(line)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func hasAll(line []byte, markers [][]byte) bool {
	for _, m := range markers {
		if !bytes.Contains(line, m) {
			return false
		}
	}
	return true
}

// claudeUsage reads a Claude Code transcript and its subagents' transcripts
// (<transcript without .jsonl>/subagents/*.jsonl): every assistant line
// carries message.usage, and a message streamed in several lines repeats its
// id and usage on each, so usage counts once per message id.
func claudeUsage(ctx context.Context, path string) (map[string]*mcp.SessionModelUsage, error) {
	files := []string{path}
	sub, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(path, filepath.Ext(path)), "subagents", "*.jsonl"))
	sort.Strings(sub)
	files = append(files, sub...)

	type line struct {
		Type      string `json:"type"`
		UUID      string `json:"uuid"`
		RequestID string `json:"requestId"`
		Message   struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage *struct {
				Input      int64 `json:"input_tokens"`
				Output     int64 `json:"output_tokens"`
				CacheWrite int64 `json:"cache_creation_input_tokens"`
				CacheRead  int64 `json:"cache_read_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	type entry struct {
		model string
		usage mcp.SessionModelUsage
	}
	seen := map[string]entry{}
	r := &lineReader{ctx: ctx, budget: maxTranscriptBytes}
	markers := [][]byte{[]byte(`"assistant"`), []byte(`"usage"`)}
	for i, file := range files {
		err := r.each(file, markers, func(raw []byte) {
			var l line
			if json.Unmarshal(raw, &l) != nil || l.Type != "assistant" || l.Message.Usage == nil {
				return
			}
			if l.Message.Model == "" || l.Message.Model == "<synthetic>" {
				return
			}
			id := l.Message.ID
			if id == "" {
				id = l.RequestID
			}
			if id == "" {
				id = l.UUID
			}
			u := l.Message.Usage
			// The last line of a message has its final usage.
			seen[id] = entry{model: l.Message.Model, usage: mcp.SessionModelUsage{
				InputTokens: u.Input, OutputTokens: u.Output,
				CacheReadTokens: u.CacheRead, CacheWriteTokens: u.CacheWrite,
			}}
		})
		if err != nil {
			if i == 0 || !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
	}
	totals := map[string]*mcp.SessionModelUsage{}
	for _, e := range seen {
		add(totalFor(totals, e.model), e.usage)
	}
	return totals, nil
}

// codexRollout finds the Codex rollout file of a session when the hook
// payload does not name it: $CODEX_HOME/sessions/YYYY/MM/DD/rollout-…-<id>.jsonl.
func codexRollout(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || strings.ContainsAny(sessionID, `/\*?[`) {
		return ""
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(userHome, ".codex")
	}
	matches, _ := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", "rollout-*"+sessionID+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[len(matches)-1]
}

// codexUsage reads a Codex rollout: token_count events carry the session's
// running total (total_token_usage, where input_tokens include the cached
// ones and output_tokens the reasoning ones), and turn_context lines name the
// model each turn runs on. Each total's growth counts toward the model of
// the turn it happened in.
func codexUsage(ctx context.Context, path string) (map[string]*mcp.SessionModelUsage, error) {
	type tokens struct {
		Input      int64 `json:"input_tokens"`
		Cached     int64 `json:"cached_input_tokens"`
		CacheWrite int64 `json:"cache_write_input_tokens"`
		Output     int64 `json:"output_tokens"`
	}
	type line struct {
		Type    string `json:"type"`
		Payload struct {
			Type  string `json:"type"`
			Model string `json:"model"`
			Info  *struct {
				Total *tokens `json:"total_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}
	totals := map[string]*mcp.SessionModelUsage{}
	model := ""
	var prev tokens
	r := &lineReader{ctx: ctx, budget: maxTranscriptBytes}
	err := r.each(path, nil, func(raw []byte) {
		if !bytes.Contains(raw, []byte(`"turn_context"`)) && !bytes.Contains(raw, []byte(`"token_count"`)) {
			return
		}
		var l line
		if json.Unmarshal(raw, &l) != nil {
			return
		}
		switch {
		case l.Type == "turn_context":
			if m := strings.TrimSpace(l.Payload.Model); m != "" {
				model = m
			}
		case l.Type == "event_msg" && l.Payload.Type == "token_count" && l.Payload.Info != nil && l.Payload.Info.Total != nil:
			cur := *l.Payload.Info.Total
			delta := tokens{
				Input: cur.Input - prev.Input, Cached: cur.Cached - prev.Cached,
				CacheWrite: cur.CacheWrite - prev.CacheWrite, Output: cur.Output - prev.Output,
			}
			if delta.Input < 0 || delta.Cached < 0 || delta.Output < 0 || delta.CacheWrite < 0 {
				// The running total started over: all of it is new.
				delta = cur
			}
			prev = cur
			add(totalFor(totals, model), mcp.SessionModelUsage{
				InputTokens:      max(delta.Input-delta.Cached, 0),
				OutputTokens:     delta.Output,
				CacheReadTokens:  delta.Cached,
				CacheWriteTokens: delta.CacheWrite,
			})
		}
	})
	if err != nil {
		return nil, err
	}
	return totals, nil
}

// antigravityUsage reads an Antigravity CLI transcript
// (…/brain/<conversation>/.system_generated/logs/transcript.jsonl): each
// model step records input_tokens (cached ones included), cache_read_tokens
// and output_tokens, by step_index. Steps do not name the model, so it all
// counts toward the model the hook payload names.
func antigravityUsage(ctx context.Context, path, model string) (map[string]*mcp.SessionModelUsage, error) {
	type line struct {
		Step      *int  `json:"step_index"`
		Input     int64 `json:"input_tokens"`
		CacheRead int64 `json:"cache_read_tokens"`
		Output    int64 `json:"output_tokens"`
	}
	steps := map[int]mcp.SessionModelUsage{}
	r := &lineReader{ctx: ctx, budget: maxTranscriptBytes}
	err := r.each(path, [][]byte{[]byte(`"input_tokens"`)}, func(raw []byte) {
		var l line
		if json.Unmarshal(raw, &l) != nil || l.Step == nil {
			return
		}
		steps[*l.Step] = mcp.SessionModelUsage{
			InputTokens:     max(l.Input-l.CacheRead, 0),
			OutputTokens:    l.Output,
			CacheReadTokens: l.CacheRead,
		}
	})
	if err != nil {
		return nil, err
	}
	totals := map[string]*mcp.SessionModelUsage{}
	for _, u := range steps {
		add(totalFor(totals, model), u)
	}
	return totals, nil
}

// parseUsage reads the totals OpenCode's plugin passes in: a "usage" array
// of mcp.SessionModelUsage objects.
func parseUsage(v any) []mcp.SessionModelUsage {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	num := func(m map[string]any, k string) float64 {
		f, _ := m[k].(float64)
		if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
			return 0
		}
		return f
	}
	var out []mcp.SessionModelUsage
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		model, _ := m["model"].(string)
		out = append(out, mcp.SessionModelUsage{
			Model:            strings.TrimSpace(model),
			InputTokens:      int64(num(m, "inputTokens")),
			OutputTokens:     int64(num(m, "outputTokens")),
			CacheReadTokens:  int64(num(m, "cacheReadTokens")),
			CacheWriteTokens: int64(num(m, "cacheWriteTokens")),
			CostUSD:          num(m, "costUsd"),
		})
		if len(out) >= maxUsageModels {
			break
		}
	}
	return out
}
