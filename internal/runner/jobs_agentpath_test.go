package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// binaryAgent is fakeAgent that also says which program the runner chose.
type binaryAgent struct {
	fakeAgent
	seen chan string
}

func (b binaryAgent) Command(job runnerwire.Job, env adapter.RunEnv) (*exec.Cmd, error) {
	b.saw(env.Binary)
	return b.fakeAgent.Command(job, env)
}

func (b binaryAgent) CommandRelay(job runnerwire.Job, env adapter.RunEnv) (*exec.Cmd, bool, error) {
	b.saw(env.Binary)
	return b.fakeAgent.CommandRelay(job, env)
}

func (b binaryAgent) saw(bin string) {
	select {
	case b.seen <- bin:
	default:
	}
}

// A run starts the program its owner added, though nothing is on PATH.
func TestStartAgentUsesAddedAgentPath(t *testing.T) {
	t.Setenv("PATH", os.Getenv("PATH"))
	noAgentTempDir(t)
	added := writeAgentFile(t, filepath.Join(t.TempDir(), "tools"), "claude")
	seen := make(chan string, 4)
	env := startJobRunner(t, func(c *Client) {
		c.Config.AgentPaths = map[string]string{"claude": added}
		c.Commands = &fakeCommands{paths: map[string]string{},
			outputs: map[string]string{added + " --version": "2.1.299 (Claude Code)\n"}}
		inner := c.Adapter
		c.Adapter = func(kind string) adapter.Adapter {
			if fa, ok := inner(kind).(fakeAgent); ok {
				return binaryAgent{fakeAgent: fa, seen: seen}
			}
			return inner(kind)
		}
	})
	env.hub.hello()
	env.hub.offer(testJob("run-added", "ok:"))
	result := env.hub.wait("run-added", runnerwire.TypeFinish).Result
	if result == nil || result.Status != runnerwire.RunSucceeded {
		t.Fatalf("result = %+v", result)
	}
	select {
	case bin := <-seen:
		if bin != added {
			t.Fatalf("ran %q, want %q", bin, added)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the agent was never launched")
	}
}

// The added program gone, with none on PATH: the agent is absent, and
// nothing else is started.
func TestStartAgentAddedAgentPathGone(t *testing.T) {
	gone := filepath.Join(t.TempDir(), agentFileName("claude"))
	c := &Client{Config: &Config{}, Commands: &fakeCommands{paths: map[string]string{}}}
	j := newJobs(context.Background(), c)
	r := &run{job: testJob("run-gone", "ok:"), cfg: &Config{AgentPaths: map[string]string{"claude": gone}}}
	result, _ := j.runAgent(context.Background(), r, Worktree{Path: t.TempDir()}, r.job)
	if result.Status != runnerwire.RunFailed || result.FailureKind != runnerwire.FailureAgentAbsent ||
		result.Error != "claude: the program added for it is no longer there" || strings.Contains(result.Error, gone) {
		t.Fatalf("result = %+v", result)
	}
}
